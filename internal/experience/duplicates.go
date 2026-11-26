package experience

import (
	"context"
	"slices"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func (s *Service) duplicateSource() (PluginDuplicates, bool) {
	src, ok := s.d.PluginLibraries.(PluginDuplicates)
	return src, ok && s.d.PluginLibraries != nil
}

// LibraryDuplicates pages a plugin library's recordings with more than one
// release the viewer can see, with the viewer's preferred release.
func (s *Service) LibraryDuplicates(ctx context.Context, viewer *core.Viewer, library core.LibraryID, cursor string, limit int) (frontend.DuplicatesPage, error) {
	src, ok := s.duplicateSource()
	if !ok {
		return frontend.DuplicatesPage{}, core.ErrNotAvailable
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.DuplicatesPage{}, err
	}
	lib, err := s.d.PluginLibraries.Library(ctx, library)
	if err != nil || !v.AllowsLibrary(lib.ID) {
		return frontend.DuplicatesPage{}, core.ErrNotFound
	}
	offset, _ := strconv.Atoi(cursor)
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	groups, total, err := src.DuplicateTitles(ctx, lib, max(offset, 0), limit)
	if err != nil {
		return frontend.DuplicatesPage{}, err
	}
	out := frontend.DuplicatesPage{Total: total}
	recs := make([]string, 0, len(groups))
	for _, g := range groups {
		recs = append(recs, g.Recording)
	}
	prefs := map[string]core.RatingKey{}
	if s.d.RecordingPreferences != nil {
		prefs, _ = s.d.RecordingPreferences(ctx, v.ID, lib.ID, recs)
	}
	for _, g := range groups {
		visible, err := s.filterPluginTitles(withHiddenCopies(ctx), v, g.Titles, frontend.ItemQuery{})
		if err != nil {
			return frontend.DuplicatesPage{}, err
		}
		if len(visible) < 2 {
			continue
		}
		out.Groups = append(out.Groups, frontend.DuplicateGroup{RecordingID: g.Recording, Count: len(visible), Titles: visible, Preferred: prefs[g.Recording]})
	}
	if offset+limit < total {
		out.Next = strconv.Itoa(offset + limit)
	}
	return out, nil
}

// TitleRecording lists every release of the recording a title carries.
func (s *Service) TitleRecording(ctx context.Context, viewer *core.Viewer, key core.RatingKey) (frontend.DuplicateGroup, error) {
	src, ok := s.duplicateSource()
	if !ok {
		return frontend.DuplicateGroup{}, core.ErrNotAvailable
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.DuplicateGroup{}, err
	}
	lib, recording, titles, err := src.RecordingTitles(ctx, key)
	if err != nil || !v.AllowsLibrary(lib.ID) {
		return frontend.DuplicateGroup{}, core.ErrNotFound
	}
	visible, err := s.filterPluginTitles(withHiddenCopies(ctx), v, titles, frontend.ItemQuery{})
	if err != nil {
		return frontend.DuplicateGroup{}, err
	}
	g := frontend.DuplicateGroup{RecordingID: recording, Count: len(visible), Titles: visible}
	if s.d.RecordingPreferences != nil {
		if prefs, err := s.d.RecordingPreferences(ctx, v.ID, lib.ID, []string{recording}); err == nil {
			g.Preferred = prefs[recording]
		}
	}
	return g, nil
}

// PreferRecording records the viewer's preferred release of a recording and
// hides the listed other releases from that viewer's lists.
func (s *Service) PreferRecording(ctx context.Context, viewer *core.Viewer, library core.LibraryID, recording string, preferred core.RatingKey, hide []core.RatingKey) (frontend.DuplicateGroup, error) {
	if s.d.SetRecordingPreference == nil {
		return frontend.DuplicateGroup{}, core.ErrNotAvailable
	}
	g, err := s.TitleRecording(ctx, viewer, preferred)
	if err != nil || g.RecordingID != recording {
		return frontend.DuplicateGroup{}, core.ErrNotFound
	}
	keys := make([]core.RatingKey, 0, len(g.Titles))
	for _, t := range g.Titles {
		keys = append(keys, t.Key)
	}
	for _, h := range hide {
		if !slices.Contains(keys, h) {
			return frontend.DuplicateGroup{}, core.ErrNotFound
		}
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.DuplicateGroup{}, err
	}
	if err := s.d.SetRecordingPreference(ctx, v.ID, library, recording, preferred, hide); err != nil {
		return frontend.DuplicateGroup{}, err
	}
	g.Preferred = preferred
	return g, nil
}

type showHiddenCopiesKey struct{}

// withHiddenCopies keeps releases the viewer hid in favour of another copy
// of the same recording (duplicate review lists them).
func withHiddenCopies(ctx context.Context) context.Context {
	return context.WithValue(ctx, showHiddenCopiesKey{}, true)
}

// dropHiddenCopies removes the releases the viewer hid in favour of another
// release of the same recording.
func (s *Service) dropHiddenCopies(ctx context.Context, v *core.User, items []core.Title) []core.Title {
	if v == nil || s.d.HiddenRecordingCopies == nil || len(items) == 0 {
		return items
	}
	if show, _ := ctx.Value(showHiddenCopiesKey{}).(bool); show {
		return items
	}
	hidden, err := s.d.HiddenRecordingCopies(ctx, v.ID)
	if err != nil || len(hidden) == 0 {
		return items
	}
	set := make(map[core.RatingKey]bool, len(hidden))
	for _, k := range hidden {
		set[k] = true
	}
	out := items[:0:0]
	for _, t := range items {
		if !set[t.Key] {
			out = append(out, t)
		}
	}
	return out
}
