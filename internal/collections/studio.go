package collections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/artwork"
	"github.com/braedonsaunders/ether/internal/core"
)

// The Collection Studio API, used by internal/admin.

// listIndexWait bounds how long a collection listing waits for the
// catalogue index's first build.
const listIndexWait = 2 * time.Second

// ListSpecs returns every collection, owner-made first, then built-ins.
func (s *Service) ListSpecs(ctx context.Context) ([]core.CollectionSpec, error) {
	rows, err := s.loadRows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]core.CollectionSpec, 0, len(rows))
	var snap *snapshot
	for _, r := range rows {
		if strings.HasPrefix(r.slug, "plugin:") {
			continue
		}
		if _, ok := localFacetSpec(r.spec); ok {
			// A listing waits briefly for the catalogue index's first
			// build, which reads the whole catalogue; past that the stored
			// counts stand.
			snap = s.idx.readyWithin(ctx, listIndexWait)
			break
		}
	}
	for _, r := range rows {
		if strings.HasPrefix(r.slug, "plugin:") {
			continue
		}
		v := r.view()
		if key, ok := localFacetSpec(r.spec); ok && snap != nil {
			v.ItemCount = len(snap.facetKeys(key))
			if v.Limit > 0 {
				v.ItemCount = min(v.ItemCount, v.Limit)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// ListSpecsLight returns every collection's stored descriptor without waiting
// on the catalogue index or recomputing facet item counts (used by Home
// Designer's AvailableRows, which only reads id, name, enabled and mediaType).
func (s *Service) ListSpecsLight(ctx context.Context) ([]core.CollectionSpec, error) {
	rows, err := s.loadRows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]core.CollectionSpec, 0, len(rows))
	for _, r := range rows {
		if strings.HasPrefix(r.slug, "plugin:") {
			continue
		}
		out = append(out, r.view())
	}
	return out, nil
}

// GetSpec returns one collection by id (number or built-in slug).
func (s *Service) GetSpec(ctx context.Context, id string) (*core.CollectionSpec, error) {
	row, err := s.rowByID(ctx, id)
	if err != nil {
		return nil, err
	}
	v := row.view()
	return &v, nil
}

// CreateSpec validates, saves and builds a new collection. The build is
// bounded; if it runs long it finishes in the background.
func (s *Service) CreateSpec(ctx context.Context, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	Normalize(&sp)
	if err := s.validate(&sp); err != nil {
		return nil, err
	}
	row, err := s.insertRow(ctx, sp, 0)
	if err != nil {
		return nil, err
	}
	return s.afterSave(ctx, row)
}

// CreateSpecDeferred validates and saves a new collection without starting an
// immediate background build. Bulk imports use this so importing hundreds of
// remote catalogues lets the scheduler build them in bounded batches instead
// of launching a burst of concurrent upstream requests.
func (s *Service) CreateSpecDeferred(ctx context.Context, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	Normalize(&sp)
	if err := s.validate(&sp); err != nil {
		return nil, err
	}
	row, err := s.insertRow(ctx, sp, 0)
	if err != nil {
		return nil, err
	}
	v := row.view()
	return &v, nil
}

// UpdateSpec replaces a collection. Built-ins accept only enabled,
// targets, artwork, rotate and limit.
func (s *Service) UpdateSpec(ctx context.Context, id string, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	row, err := s.rowByID(ctx, id)
	if err != nil {
		return nil, err
	}
	Normalize(&sp)
	if row.builtin {
		sp = mergeBuiltin(row.spec, sp)
	}
	// If the owner swapped away from the synced add-on catalogue source,
	// detach the add-on sync metadata automatically.
	if sp.AddonSync != nil {
		wantKind := "plugin:" + sp.AddonSync.AddonID + "/" + sp.AddonSync.CatalogKey
		hasSyncedSource := false
		for _, src := range sp.Sources {
			if src.Kind == wantKind {
				hasSyncedSource = true
				break
			}
		}
		if !hasSyncedSource {
			sp.AddonSync = nil
		}
	}
	// Allow unchanged removed add-on sources so owners can unpublish, rename
	// or reorganize a collection whose catalogue disappeared from its add-on.
	var allowMissing map[string]bool
	if row.spec.AddonSync != nil && row.spec.AddonSync.Removed {
		prevPlugins := map[string]bool{}
		for _, src := range row.spec.Sources {
			if strings.HasPrefix(src.Kind, "plugin:") {
				prevPlugins[src.Kind] = true
			}
		}
		if len(prevPlugins) > 0 {
			allowMissing = prevPlugins
		}
	}
	if err := s.validateWithExisting(&sp, allowMissing); err != nil {
		return nil, err
	}
	keepBuild := row.buildHash != "" && !row.lastBuilt.IsZero() && membershipSpecEqual(row.spec, sp)
	if err := s.updateRow(ctx, row.id, sp, keepBuild); err != nil {
		return nil, err
	}
	s.changed(row.id)
	row, err = s.rowByID(ctx, idString(row.id))
	if err != nil {
		return nil, err
	}
	if keepBuild {
		v := row.view()
		return &v, nil
	}
	return s.afterSave(ctx, row)
}

// UpdateSpecFromAddonSync applies a manifest-sync mutation to an existing
// collection, preserving built members whenever membership inputs did not
// change and optionally updating stored warnings (for example when a catalogue
// is removed from or restored by its add-on).
func (s *Service) UpdateSpecFromAddonSync(ctx context.Context, id string, mutate func(sp *core.CollectionSpec, prevWarnings []string) (warnings []string, setWarnings bool)) (*core.CollectionSpec, error) {
	row, err := s.rowByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.builtin {
		return nil, core.ErrNotFound
	}
	sp := cloneSpec(row.spec)
	warnings, setWarnings := mutate(&sp, append([]string(nil), row.warnings...))
	Normalize(&sp)
	allowMissing := map[string]bool{}
	if sp.AddonSync != nil && sp.AddonSync.Removed {
		for _, src := range sp.Sources {
			if strings.HasPrefix(src.Kind, "plugin:") {
				allowMissing[src.Kind] = true
			}
		}
	}
	if err := s.validateWithExisting(&sp, allowMissing); err != nil {
		return nil, err
	}
	keepBuild := row.buildHash != "" && !row.lastBuilt.IsZero() && membershipSpecEqual(row.spec, sp)
	if err := s.updateRowWarnings(ctx, row.id, sp, keepBuild, warnings, setWarnings); err != nil {
		return nil, err
	}
	s.changed(row.id)
	fresh, err := s.rowByID(ctx, idString(row.id))
	if err != nil {
		return nil, err
	}
	v := fresh.view()
	return &v, nil
}

// membershipSpecEqual reports whether two normalized specs evaluate to the same
// persisted member set and order. Presentation and publication edits (targets,
// schedule, artwork, rotate, description, enabled, refresh) do not invalidate
// existing built members.
func membershipSpecEqual(a, b core.CollectionSpec) bool {
	type key struct {
		MediaType     string                  `json:"mediaType"`
		Sources       []core.CollectionSource `json:"sources"`
		Match         string                  `json:"match"`
		Rules         []core.CollectionRule   `json:"rules"`
		Exclude       []core.CollectionRule   `json:"exclude"`
		Sort          string                  `json:"sort"`
		SortDesc      bool                    `json:"sortDesc"`
		Limit         int                     `json:"limit"`
		LocalOnly     bool                    `json:"localOnly"`
		ReleaseMode   string                  `json:"releaseMode"`
		LookaheadDays int                     `json:"lookaheadDays"`
		RandomName    string                  `json:"randomName,omitempty"`
	}
	makeKey := func(sp core.CollectionSpec) key {
		k := key{
			MediaType:     sp.MediaType,
			Sources:       sp.Sources,
			Match:         sp.Match,
			Rules:         sp.Rules,
			Exclude:       sp.Exclude,
			Sort:          sp.Sort,
			SortDesc:      sp.SortDesc,
			Limit:         sp.Limit,
			LocalOnly:     sp.LocalOnly,
			ReleaseMode:   sp.ReleaseMode,
			LookaheadDays: sp.LookaheadDays,
		}
		if sp.Sort == "random" {
			k.RandomName = sp.Name
		}
		return k
	}
	ja, errA := json.Marshal(makeKey(a))
	jb, errB := json.Marshal(makeKey(b))
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

func (s *Service) remoteBuildGate() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteGate == nil {
		s.remoteGate = make(chan struct{}, 2)
	}
	return s.remoteGate
}

func (s *Service) afterSave(ctx context.Context, row *specRow) (*core.CollectionSpec, error) {
	if row.enabled {
		if hasRemoteListSource(row.spec) || row.spec.NavigationGroup != "" {
			gate := s.remoteBuildGate()
			go func() {
				gate <- struct{}{}
				defer func() { <-gate }()
				bctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				_, _ = s.build(bctx, row)
			}()
			v := row.view()
			return &v, nil
		}
		bctx, cancel := context.WithTimeout(ctx, s.buildBudget)
		_, err := s.build(bctx, row)
		cancel()
		if err != nil {
			// build keeps running for other callers; the scheduler retries.
			s.log.Debug("collections: initial build did not finish", "id", row.publicID(), "err", err)
		}
	}
	fresh, err := s.rowByID(ctx, idString(row.id))
	if err != nil {
		return nil, err
	}
	v := fresh.view()
	return &v, nil
}

// DeleteSpec deletes an owner collection.
func (s *Service) DeleteSpec(ctx context.Context, id string) error {
	row, err := s.rowByID(ctx, id)
	if err != nil {
		return err
	}
	if row.builtin {
		return conflictError{ErrBuiltIn}
	}
	if err := s.deleteRow(ctx, row.id); err != nil {
		return err
	}
	s.changed(row.id)
	return nil
}

// Rebuild evaluates a collection now.
func (s *Service) Rebuild(ctx context.Context, id string) (*core.CollectionSpec, error) {
	row, err := s.rowByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.invalidate(row.id)
	s.idx.invalidate()
	s.notify("index")
	if _, err := s.build(ctx, row); err != nil {
		return nil, err
	}
	fresh, err := s.rowByID(ctx, idString(row.id))
	if err != nil {
		return nil, err
	}
	v := fresh.view()
	return &v, nil
}

// Preview evaluates an unsaved spec and returns the first n titles. Titles
// come from the in-memory catalogue index, so a preview over the whole
// catalogue never waits on the database.
func (s *Service) Preview(ctx context.Context, sp core.CollectionSpec, n int) (*core.CollectionPreview, error) {
	started := time.Now()
	if n <= 0 || n > 200 {
		n = 60
	}
	Normalize(&sp)
	// A preview is of an unsaved draft: everything must be valid except
	// that it may not have a name yet.
	named := sp
	if named.Name == "" {
		named.Name = "Untitled"
	}
	if err := s.validate(&named); err != nil {
		return nil, err
	}
	pluginPreview := false
	for _, src := range named.Sources {
		if backgroundOnlySource(src) {
			pluginPreview = true
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			break
		}
	}
	ctx = context.WithValue(ctx, previewContextKey{}, true)
	res, err := s.evaluate(ctx, named, evalOptions{seed: named.Name})
	if err != nil {
		if pluginPreview && errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("plugin list preview timed out: %w", err)
		}
		return nil, err
	}
	keys := res.keys
	warnings := res.warnings
	if len(res.personal)+len(res.personalX) > 0 {
		var owner *core.User
		if auth := s.authDep(); auth != nil {
			owner, _ = auth.Owner(ctx)
		}
		if owner != nil {
			keys = s.filterForUser(ctx, keys, personalRules{include: res.personal, exclude: res.personalX, any: res.spec.Match == MatchAny, unshared: res.unshared}, &core.User{ID: owner.ID}, res.snap)
			warnings = append(warnings, "watched and in-progress rules are shown for "+firstNonEmpty(owner.Title, owner.Username, "the owner")+"; each viewer sees their own")
		} else {
			warnings = append(warnings, "watched and in-progress rules are applied per viewer in Plex")
		}
	}
	total := len(keys)
	if res.total > len(res.keys) {
		total += res.total - len(res.keys)
	}
	if sp.Limit > 0 && total > sp.Limit {
		total = sp.Limit
	}
	if sp.Limit > 0 && len(keys) > sp.Limit {
		keys = keys[:sp.Limit]
	}
	if sp.Sort == "personal" {
		warnings = append(warnings, "shown by popularity; each viewer gets their own order")
	}
	if sp.Rotate != nil && sp.Rotate.Size > 0 && sp.Rotate.Size < total {
		warnings = append(warnings, fmt.Sprintf("Plex shows a rotating %d of these %d, changing %s", sp.Rotate.Size, total, sp.Rotate.Every))
	}
	if len(keys) > n {
		keys = keys[:n]
	}
	items := make([]core.Title, 0, len(keys))
	for _, k := range keys {
		if e := res.snap.entries[k]; e != nil {
			items = append(items, e.card())
		}
	}
	if warnings == nil {
		warnings = []string{}
	}
	return &core.CollectionPreview{Total: total, Items: items, Warnings: warnings, TookMs: time.Since(started).Milliseconds()}, nil
}

// Templates returns the curated starting points.
func (s *Service) Templates() []core.CollectionTemplate { return Templates() }

// ---------------------------------------------------------------------------
// Artwork

// ErrNoArtwork means no poster could be produced (no renderer, no members).
var ErrNoArtwork = errors.New("no artwork for this collection")

// Artwork renders (or fetches, for tmdb/custom modes) a collection's
// poster at width w. It returns the image, its content type and when it
// last changed.
func (s *Service) Artwork(ctx context.Context, id string, w int) ([]byte, string, time.Time, error) {
	row, err := s.rowByID(ctx, id)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	keys, err := s.serve(ctx, row, nil, true)
	if err != nil {
		keys = nil
	}
	return s.artworkFor(ctx, row, keys, w)
}

// CollectionArtwork serves generated posters to Plex for owner collections
// (the thumb/poster elements). ok is false for anything else, so the
// caller falls back to TMDB artwork.
func (s *Service) CollectionArtwork(ctx context.Context, user *core.User, key core.RatingKey, element string, w int) ([]byte, string, bool, error) {
	if key.Kind() != core.KindSmart || s.art == nil {
		return nil, "", false, nil
	}
	base, section := key.CollectionSection()
	row, err := s.rowByKey(ctx, base)
	if err != nil || !visible(row, user) || section != "" && row.spec.MediaType != MediaMixed {
		return nil, "", false, nil
	}
	switch element {
	case "thumb", "poster", "composite":
	case "art", "background":
		if row.spec.NavigationGroup == "" && (row.spec.Artwork.Mode != "custom" || strings.TrimSpace(row.spec.Artwork.URL) == "") {
			return nil, "", false, nil
		}
	default:
		return nil, "", false, nil
	}
	keys, err := s.serve(ctx, row, user, true)
	if err != nil {
		return nil, "", false, nil
	}
	keys = sectionKeys(keys, section)
	data, ct, _, err := s.artworkFor(ctx, row, keys, w)
	if err != nil {
		return nil, "", false, nil
	}
	return data, ct, true, nil
}

// posterWidths are the widths collection posters are rendered at. Requests
// snap up to one of them, so a client asking for every width from 150 to
// 1200 shares a handful of cached renders instead of filling the poster
// cache (and the CPU) with one render per width.
var posterWidths = []int{150, 300, 450, 600, 900, 1200}

func posterWidth(w int) int {
	if w <= 0 {
		return 0 // the renderer's default
	}
	for _, b := range posterWidths {
		if w <= b {
			return b
		}
	}
	return posterWidths[len(posterWidths)-1]
}

func (s *Service) artworkFor(ctx context.Context, row *specRow, keys []core.RatingKey, w int) ([]byte, string, time.Time, error) {
	w = posterWidth(w)
	stamp := row.stamp()
	art := row.spec.Artwork
	if s.images != nil {
		switch art.Mode {
		case "custom":
			var data []byte
			var ct string
			var err error
			if art.PublicOnly {
				data, ct, err = s.images.GetUntrustedURL(ctx, art.URL)
			} else if strings.HasPrefix(art.URL, "/") {
				data, ct, err = s.images.Get(ctx, art.URL, w)
			} else {
				data, ct, err = s.images.GetURL(ctx, art.URL)
			}
			if err == nil {
				return data, ct, stamp, nil
			}
			s.log.Debug("collections: custom artwork", "id", row.publicID(), "err", err)
		case "tmdb":
			if p := s.tmdbCollectionPoster(ctx, row.spec); p != "" {
				if data, ct, err := s.images.Get(ctx, p, w); err == nil {
					return data, ct, stamp, nil
				}
			}
		}
	}
	if s.art == nil {
		return nil, "", time.Time{}, ErrNoArtwork
	}
	req := artwork.PosterRequest{Title: row.spec.Name, Mode: art.Mode, Accent: art.Accent, Width: w, Navigation: row.spec.NavigationGroup != "", Logo: art.Logo}
	if req.Mode == "tmdb" || req.Mode == "custom" {
		req.Mode = "auto"
	}
	if snap := s.idx.current(); snap != nil {
		for _, k := range keys {
			e := snap.entries[k]
			if e == nil {
				continue
			}
			if e.poster != "" && len(req.Posters) < 6 {
				req.Posters = append(req.Posters, e.poster)
			}
			if e.backdrop != "" && len(req.Backdrops) < 3 {
				req.Backdrops = append(req.Backdrops, e.backdrop)
			}
			if len(req.Posters) >= 6 && len(req.Backdrops) >= 3 {
				break
			}
		}
	}
	if req.Navigation && row.spec.NavigationGroup != "genres" {
		req.Posters = nil
		req.Backdrops = nil
	}
	req.Subtitle = subtitleFor(row.spec, len(keys))
	data, ct, err := s.art.Poster(ctx, req)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	return data, ct, stamp, nil
}

// subtitleFor is the small caps line on a generated collection poster.
// It names the kind, never a count: the poster is shared by every viewer,
// and each sees a different number of titles after their own access and
// rating rules, so a baked-in count ("100 FILMS") disagreed with the count
// their app shows.
func subtitleFor(sp core.CollectionSpec, n int) string {
	switch sp.MediaType {
	case MediaMovie:
		if n == 1 {
			return "FILM"
		}
		return "FILMS"
	case MediaShow:
		return "SERIES"
	}
	return "COLLECTION"
}

// tmdbCollectionPoster finds the poster of the spec's TMDB collection.
func (s *Service) tmdbCollectionPoster(ctx context.Context, sp core.CollectionSpec) string {
	for _, src := range sp.Sources {
		if src.Kind != "tmdbCollection" || src.ID <= 0 {
			continue
		}
		if art, err := s.mirror.CollectionArt(ctx, src.ID); err == nil && art.PosterPath != "" {
			return art.PosterPath
		}
	}
	return ""
}
