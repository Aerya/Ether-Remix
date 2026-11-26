package plex

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// Playlist search never expands a smart query or scans the catalogue. Section
// membership comes from stored member identities or the smart query's section.
func (s *Server) searchPlaylists(ctx context.Context, pr *Request, query, directories string, limit int) []model.Metadata {
	if s.playlists() == nil || pr.User == nil || query == "" {
		return nil
	}
	var lists []frontend.Playlist
	if headers, ok := s.playlists().(frontend.PlaylistHeaderLister); ok {
		// Read only the playlists whose titles match, not every item of
		// every playlist the viewer has.
		hs, err := headers.ListHeaders(ctx, pr.User.ID)
		if err != nil {
			return nil
		}
		for _, h := range hs {
			if h.UserID != pr.User.ID || playlistSearchScore(h.Title, query) == 0 {
				continue
			}
			pl, err := s.playlists().Get(ctx, pr.User.ID, h.Key)
			if err != nil {
				continue
			}
			lists = append(lists, *pl)
		}
	} else {
		var err error
		if lists, err = s.playlists().List(ctx, pr.User.ID); err != nil {
			return nil
		}
	}
	var entrySecs map[frontend.RatingKey]Section
	if directories != "" {
		// Entries' sections for every candidate playlist in one pass.
		var keys []frontend.RatingKey
		for i := range lists {
			if lists[i].UserID == pr.User.ID && playlistSearchScore(lists[i].Title, query) != 0 {
				keys = append(keys, lists[i].Items...)
			}
		}
		entrySecs = s.keySections(withPreferredSections(ctx, sectionIDList(directories)...), keys)
	}
	var out []model.Metadata
	for i := range lists {
		pl := &lists[i]
		if pl.UserID != pr.User.ID || playlistSearchScore(pl.Title, query) == 0 {
			continue
		}
		if directories != "" && !s.playlistInDirectories(pl, directories, entrySecs) {
			continue
		}
		m := s.playlistListingMetadata(ctx, pl)
		m.Score = strconv.FormatFloat(playlistSearchScore(pl.Title, query), 'f', 5, 64)
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Server) playlistInDirectories(pl *frontend.Playlist, raw string, entrySecs map[frontend.RatingKey]Section) bool {
	wants := strings.Split(raw, ",")
	for _, want := range wants {
		if strings.EqualFold(strings.TrimSpace(want), "playlists") {
			return true
		}
	}
	matches := func(id int) bool {
		for _, want := range wants {
			if strconv.Itoa(id) == want {
				return true
			}
		}
		return false
	}
	if pl.SmartURI != "" {
		path, err := uriPath(pl.SmartURI)
		if err != nil {
			return false
		}
		u, err := url.Parse(path)
		if err != nil {
			return false
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 3 && parts[0] == "library" && parts[1] == "sections" {
			id, _ := strconv.Atoi(parts[2])
			return matches(id)
		}
	}
	for _, key := range pl.Items {
		if sec, ok := entrySecs[key]; ok && matches(sec.ID) {
			return true
		}
	}
	return false
}

// PMS's playlist search ladder sits below title results at each match class.
func playlistSearchScore(title, query string) float64 {
	t, q := searchFoldName(title), searchFoldName(query)
	if q == "" {
		return 0
	}
	if t == q {
		return .92
	}
	if strings.HasPrefix(t, q) {
		return .72
	}
	words, terms := strings.Fields(strings.ToLower(title)), strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return 0
	}
	for start := range words {
		if start+len(terms) > len(words) {
			break
		}
		match := true
		for i, term := range terms {
			if !strings.HasPrefix(searchFoldName(words[start+i]), searchFoldName(term)) {
				match = false
				break
			}
		}
		if match {
			if start == 0 {
				return .52
			}
			return .32
		}
	}
	return 0
}

func (s *Server) sectionPlaylistsHub(ctx context.Context, pr *Request, section, count int) (model.Hub, bool) {
	h := model.Hub{Title: "Recent Playlists", Type: "playlist", HubIdentifier: "recent.library.playlists." + strconv.Itoa(section), Style: "shelf"}
	h.Key = "/playlists/all?type=15&playlistType=video&sort=lastViewedAt:desc&sectionID=" + strconv.Itoa(section)
	if pr.User == nil || s.playlists() == nil {
		return h, false
	}
	pls, err := s.playlists().List(ctx, pr.User.ID)
	if err != nil {
		return h, false
	}
	var keys []frontend.RatingKey
	for i := range pls {
		if pls[i].UserID == pr.User.ID {
			keys = append(keys, pls[i].Items...)
		}
	}
	entrySecs := s.keySections(withPreferredSections(ctx, section), keys)
	for _, pl := range pls {
		if pl.UserID != pr.User.ID {
			continue
		}
		if len(pl.Items) > 0 && !s.playlistInDirectories(&pl, strconv.Itoa(section), entrySecs) {
			continue
		}
		h.Metadata = append(h.Metadata, s.playlistListingMetadata(ctx, &pl))
	}
	sort.SliceStable(h.Metadata, func(i, j int) bool { return h.Metadata[i].LastViewedAt > h.Metadata[j].LastViewedAt })
	h.More = len(h.Metadata) > count
	if h.More {
		h.Metadata = h.Metadata[:count]
	}
	h.Size = len(h.Metadata)
	hubKeys := make([]string, 0, h.Size)
	for _, md := range h.Metadata {
		hubKeys = append(hubKeys, md.RatingKey)
	}
	if len(hubKeys) > 0 {
		h.HubKey = "/library/metadata/" + strings.Join(hubKeys, ",")
	}
	return h, true
}
