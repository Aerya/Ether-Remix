package experience

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/playlistimport"
)

// PluginImporter is a plugin catalogue that imports playlists: it reads
// playlist links and finds its own items for described tracks.
type PluginImporter interface {
	ImportSupport(ctx context.Context, lib core.Library) (links []string, identify bool)
	ImportLink(ctx context.Context, lib core.Library, url string) (core.KindImportResponse, error)
	IdentifyTracks(ctx context.Context, lib core.Library, tracks []core.ImportTrack) ([]core.ImportMatch, error)
}

const (
	importBatch      = 25
	importKeep       = 24 * time.Hour
	importMaxAsks    = 3
	importRunTimeout = 10 * time.Minute
	maxImportJobs    = 200
)

type importJob struct {
	mu        sync.Mutex
	id        string
	viewer    int64
	library   core.Library
	created   time.Time
	state     string
	name      string
	desc      string
	tracks    []core.ImportTrack
	done      int
	matches   map[int]core.ImportMatch
	answered  map[int]bool
	errorCode string
	truncated bool
	total     int
}

type importJobs struct {
	mu   sync.Mutex
	jobs map[string]*importJob
}

var importsMu sync.Mutex

func (s *Service) importRegistry() *importJobs {
	importsMu.Lock()
	defer importsMu.Unlock()
	if s.imports == nil {
		s.imports = &importJobs{jobs: map[string]*importJob{}}
	}
	return s.imports
}

func (r *importJobs) put(j *importJob) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for id, old := range r.jobs {
		if now.Sub(old.created) > importKeep {
			delete(r.jobs, id)
		}
	}
	if len(r.jobs) >= maxImportJobs {
		var oldest string
		var at time.Time
		for id, old := range r.jobs {
			if oldest == "" || old.created.Before(at) {
				oldest, at = id, old.created
			}
		}
		delete(r.jobs, oldest)
	}
	r.jobs[j.id] = j
}

func (r *importJobs) get(id string) (*importJob, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if ok && time.Since(j.created) > importKeep {
		delete(r.jobs, id)
		return nil, false
	}
	return j, ok
}

func (s *Service) importer() (PluginImporter, bool) {
	imp, ok := s.d.PluginLibraries.(PluginImporter)
	return imp, ok && s.d.PluginLibraries != nil
}

// PlaylistImportSupport answers the playlist link sites any library reads
// and whether files can be imported at all.
func (s *Service) PlaylistImportSupport(ctx context.Context) ([]string, bool) {
	imp, ok := s.importer()
	if !ok {
		return nil, false
	}
	libs, err := s.d.PluginLibraries.Libraries(ctx)
	if err != nil {
		return nil, false
	}
	var links []string
	files := false
	for _, lib := range libs {
		l, identify := imp.ImportSupport(ctx, lib)
		if !identify {
			continue
		}
		files = true
		for _, site := range l {
			if !slices.Contains(links, site) {
				links = append(links, site)
			}
		}
	}
	sort.Strings(links)
	return links, files
}

// StartPlaylistImport starts importing a link or file into a library the
// viewer can see; it reads and matches in the background.
func (s *Service) StartPlaylistImport(ctx context.Context, viewer *core.Viewer, req frontend.PlaylistImportRequest) (string, error) {
	imp, ok := s.importer()
	if !ok {
		return "", core.ErrNotAvailable
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return "", err
	}
	lib, err := s.d.PluginLibraries.Library(ctx, req.Library)
	if err != nil || !v.AllowsLibrary(lib.ID) {
		return "", core.ErrNotFound
	}
	links, identify := imp.ImportSupport(ctx, lib)
	if !identify {
		return "", core.ErrUnsupportedImport
	}
	j := &importJob{id: newImportID(), viewer: v.ID, library: lib, created: time.Now(), matches: map[int]core.ImportMatch{}, answered: map[int]bool{}}
	switch {
	case strings.TrimSpace(req.URL) != "":
		if !linkFromSites(req.URL, links) {
			return "", core.ErrUnsupportedImport
		}
		j.state = "reading"
	default:
		name, tracks, err := playlistimport.Parse(req.FileFormat, req.FileText)
		if err != nil {
			return "", core.ErrUnsupportedImport
		}
		j.state, j.tracks, j.name = "matching", tracks, name
		if j.name == "" {
			j.name = strings.TrimSuffix(req.FileName, fileExt(req.FileName))
		}
	}
	s.importRegistry().put(j)
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), importRunTimeout)
	go func() {
		defer cancel()
		s.runImport(runCtx, imp, j, strings.TrimSpace(req.URL))
	}()
	return j.id, nil
}

func fileExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[i:]
	}
	return ""
}

func newImportID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Service) runImport(ctx context.Context, imp PluginImporter, j *importJob, url string) {
	fail := func(code string) {
		j.mu.Lock()
		j.state, j.errorCode = "failed", code
		j.mu.Unlock()
	}
	if url != "" {
		page, err := imp.ImportLink(ctx, j.library, url)
		if err != nil {
			if errors.Is(err, core.ErrNotFound) {
				fail("unsupported_link")
			} else {
				fail("unavailable")
			}
			return
		}
		tracks := page.Tracks
		total, truncated := page.Total, page.Truncated
		if len(tracks) > playlistimport.MaxTracks {
			total = max(total, len(tracks))
			tracks, truncated = tracks[:playlistimport.MaxTracks], true
		}
		if len(tracks) == 0 {
			fail("empty")
			return
		}
		j.mu.Lock()
		j.state, j.name, j.desc, j.tracks = "matching", page.Name, page.Description, tracks
		j.truncated, j.total = truncated || (total > len(tracks)), total
		j.mu.Unlock()
	}
	asks := map[int]int{}
	var pending []int
	for i := range j.tracks {
		pending = append(pending, i)
	}
	for len(pending) > 0 && ctx.Err() == nil {
		batch := pending[:min(importBatch, len(pending))]
		tracks := make([]core.ImportTrack, len(batch))
		for n, idx := range batch {
			tracks[n] = j.tracks[idx]
			asks[idx]++
		}
		matches, err := imp.IdentifyTracks(ctx, j.library, tracks)
		answered := map[int]core.ImportMatch{}
		if err == nil {
			for _, m := range matches {
				answered[batch[m.Index]] = core.ImportMatch{Index: batch[m.Index], Key: m.Key, By: m.By, Candidates: m.Candidates}
			}
		}
		var next []int
		j.mu.Lock()
		for _, idx := range batch {
			if m, ok := answered[idx]; ok {
				j.matches[idx], j.answered[idx] = m, true
				j.done++
			} else if asks[idx] >= importMaxAsks {
				j.answered[idx] = true
				j.done++
			} else {
				next = append(next, idx)
			}
		}
		j.mu.Unlock()
		pending = append(next, pending[len(batch):]...)
	}
	j.mu.Lock()
	if ctx.Err() != nil && len(pending) > 0 {
		j.state, j.errorCode = "failed", "timeout"
	} else {
		j.state = "done"
	}
	j.mu.Unlock()
}

// PlaylistImport answers an import's progress and, as it goes, which tracks
// matched (as titles the viewer can see) and which did not, and why.
func (s *Service) PlaylistImport(ctx context.Context, viewer *core.Viewer, id string) (frontend.PlaylistImport, error) {
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.PlaylistImport{}, err
	}
	j, ok := s.importRegistry().get(id)
	if !ok || j.viewer != v.ID {
		return frontend.PlaylistImport{}, core.ErrNotFound
	}
	j.mu.Lock()
	out := frontend.PlaylistImport{ID: j.id, State: j.state, Name: j.name, Description: j.desc, Total: len(j.tracks), Done: j.done, Error: j.errorCode, Truncated: j.truncated, SourceTotal: j.total}
	tracks := append([]core.ImportTrack(nil), j.tracks...)
	matches := make(map[int]core.ImportMatch, len(j.matches))
	for k, m := range j.matches {
		matches[k] = m
	}
	answered := make(map[int]bool, len(j.answered))
	for k := range j.answered {
		answered[k] = true
	}
	j.mu.Unlock()

	var keys []core.RatingKey
	for _, m := range matches {
		if m.Key != 0 {
			keys = append(keys, m.Key)
		}
		keys = append(keys, m.Candidates...)
	}
	visible := map[core.RatingKey]core.Title{}
	if len(keys) > 0 {
		views, err := s.d.PluginLibraries.ItemsMany(ctx, keys)
		if err != nil {
			return out, err
		}
		titles := make([]core.Title, 0, len(views))
		for _, piv := range views {
			titles = append(titles, piv.Title)
		}
		shown, err := s.filterPluginTitles(ctx, v, titles, frontend.ItemQuery{})
		if err != nil {
			return out, err
		}
		for _, t := range shown {
			visible[t.Key] = t
		}
	}
	for idx := range tracks {
		if !answered[idx] {
			continue
		}
		m := matches[idx]
		if t, ok := visible[m.Key]; ok && m.Key != 0 {
			out.Matched = append(out.Matched, frontend.ImportMatched{Index: idx, Source: tracks[idx], Title: t, By: m.By})
			continue
		}
		u := frontend.ImportUnmatched{Index: idx, Source: tracks[idx], Reason: "not_found"}
		for _, k := range m.Candidates {
			if t, ok := visible[k]; ok {
				u.Candidates = append(u.Candidates, t)
			}
		}
		if len(u.Candidates) > 0 {
			u.Reason = "ambiguous"
		}
		out.Unmatched = append(out.Unmatched, u)
	}
	return out, nil
}

// SavePlaylistImport saves a finished import as the viewer's playlist, in
// source order: matched tracks not excluded, and ambiguous tracks the viewer
// chose a release for.
func (s *Service) SavePlaylistImport(ctx context.Context, viewer *core.Viewer, id, name string, exclude []int, choose map[int]core.RatingKey) (*core.Playlist, error) {
	result, err := s.PlaylistImport(ctx, viewer, id)
	if err != nil {
		return nil, err
	}
	if result.State != "done" {
		return nil, core.ErrNotAvailable
	}
	picked := map[int]core.RatingKey{}
	for _, m := range result.Matched {
		picked[m.Index] = m.Title.Key
	}
	for _, u := range result.Unmatched {
		k, ok := choose[u.Index]
		if !ok {
			continue
		}
		if !slices.ContainsFunc(u.Candidates, func(t core.Title) bool { return t.Key == k }) {
			return nil, core.ErrInvalidImportChoice
		}
		picked[u.Index] = k
	}
	for idx := range choose {
		if _, ok := picked[idx]; !ok {
			return nil, core.ErrInvalidImportChoice
		}
	}
	for _, idx := range exclude {
		delete(picked, idx)
	}
	order := make([]int, 0, len(picked))
	for idx := range picked {
		order = append(order, idx)
	}
	sort.Ints(order)
	keys := make([]core.RatingKey, 0, len(order))
	for _, idx := range order {
		keys = append(keys, picked[idx])
	}
	if strings.TrimSpace(name) == "" {
		name = result.Name
	}
	if strings.TrimSpace(name) == "" {
		name = "Imported playlist"
	}
	return s.CreateViewerPlaylist(ctx, viewer, name, keys)
}

// linkFromSites reports whether a link's host names one of the sites the
// library reads (deezer.com for "deezer"), so other links fail at once.
func linkFromSites(raw string, sites []string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, site := range sites {
		if site != "" && (host == site+".com" || strings.HasSuffix(host, "."+site+".com") || strings.Contains(host, site)) {
			return true
		}
	}
	return false
}
