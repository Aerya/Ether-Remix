package experience

import (
	"context"
	"slices"
	"sort"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type pseResumeScope struct {
	library *Library
	theme   pseJellyfinMembership
	themed  bool
}

func (s *Service) pseResumeScope(ctx context.Context, viewer *core.User, viewID string) (pseResumeScope, error) {
	if s.pseJellyfinThemeViewID(viewID) {
		theme, recognized, err := s.pseJellyfinThemeMembership(ctx, viewer, viewID)
		if err != nil {
			return pseResumeScope{}, err
		}
		if !recognized {
			return pseResumeScope{}, core.ErrNotFound
		}
		return pseResumeScope{theme: theme, themed: true}, nil
	}
	library, err := s.activityLibrary(viewer, viewID)
	return pseResumeScope{library: library}, err
}

func (s *Service) pseResumeVisible(ctx context.Context, viewer *core.User, title *core.Title, vis Visibility, scope pseResumeScope) bool {
	section, ok := vis.SectionFor(title.Type)
	if !ok || (scope.library != nil && section != scope.library.ID) || (scope.themed && !scope.theme.contains(title)) {
		return false
	}
	if !SectionAllowed(viewer, section) {
		if s.d.Protocol != "jellyfin" || scope.library != nil || !frontend.ThemeExplicit(viewer) || !s.sectionOrFilterAllowed(ctx, viewer, title, vis.SectionFor) {
			return false
		}
	}
	return vis.Visible(ctx, viewer, title)
}

func (s *Service) pseJellyfinLatest(ctx context.Context, viewer *core.User, device core.Device, viewID string, limit int, group bool) ([]frontend.LatestItem, error) {
	query := frontend.ItemQuery{Types: []core.MediaType{core.TypeMovie, core.TypeShow}}
	if !group {
		query.Types = []core.MediaType{core.TypeMovie, core.TypeEpisode}
		query.Recursive = true
	}
	// Resolve the view and its visible members as for a normal library browse.
	// Read only real addition dates, then page after the played filter and sort.
	page, err := s.LibraryItems(ctx, viewer, device, viewID, query)
	if err != nil {
		return nil, err
	}
	if s.d.AddedTimes == nil {
		return nil, core.ErrNotAvailable
	}
	var latest []frontend.LatestItem
	played, filterPlayed := frontend.LatestPlayedFilter(ctx)
	for batch := range slices.Chunk(page.Items, 500) {
		keys := make([]core.RatingKey, len(batch))
		for i, title := range batch {
			keys[i] = title.Key
		}
		times, err := s.d.AddedTimes(ctx, keys)
		if err != nil {
			return nil, err
		}
		var states map[core.RatingKey]core.WatchStatus
		if filterPlayed {
			if s.d.State == nil {
				return nil, core.ErrNotAvailable
			}
			states, err = s.d.State.GetMany(ctx, viewer.ID, keys)
			if err != nil {
				return nil, err
			}
		}
		for _, title := range batch {
			added := times[title.Key]
			if added.IsZero() || (filterPlayed && (states[title.Key].ViewCount > 0) != played) {
				continue
			}
			latest = append(latest, frontend.LatestItem{Title: title, AddedAt: &added})
		}
	}
	sort.SliceStable(latest, func(i, j int) bool { return latest[i].AddedAt.After(*latest[j].AddedAt) })
	return latest[:min(len(latest), activityLimit(limit))], nil
}
