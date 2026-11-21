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
	// Resolve the view and its visible members as for a normal library browse.
	// Series are only expanded into episodes for the ungrouped feed, and then
	// only through their newest aired episodes. Read only real addition dates,
	// then page after the played filter and sort.
	page, err := s.LibraryItems(ctx, viewer, device, viewID, frontend.ItemQuery{Types: []core.MediaType{core.TypeMovie, core.TypeShow}})
	if err != nil {
		return nil, err
	}
	limit = activityLimit(limit)
	candidates := page.Items
	if !group {
		var keep func([]core.Title) ([]core.Title, error)
		if played, filterPlayed := frontend.LatestPlayedFilter(ctx); filterPlayed {
			if s.d.State == nil {
				return nil, core.ErrNotAvailable
			}
			keep = func(episodes []core.Title) ([]core.Title, error) {
				if len(episodes) == 0 {
					return nil, nil
				}
				keys := make([]core.RatingKey, len(episodes))
				for i, episode := range episodes {
					keys[i] = episode.Key
				}
				states, err := s.d.State.GetMany(ctx, viewer.ID, keys)
				if err != nil {
					return nil, err
				}
				var out []core.Title
				for _, episode := range episodes {
					if (states[episode.Key].ViewCount > 0) == played {
						out = append(out, episode)
					}
				}
				return out, nil
			}
		}
		candidates, err = s.pseJellyfinLatestEpisodes(ctx, viewer, viewID, page.Items, limit, keep)
		if err != nil {
			return nil, err
		}
	}
	if s.d.AddedTimes == nil {
		return nil, core.ErrNotAvailable
	}
	var latest []frontend.LatestItem
	played, filterPlayed := frontend.LatestPlayedFilter(ctx)
	for batch := range slices.Chunk(candidates, 500) {
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
	return latest[:min(len(latest), limit)], nil
}

// pseJellyfinLatestEpisodes replaces the visible series of a theme with their
// newest aired episodes, in a few batched reads instead of one children
// listing per series. Without a played filter each series contributes its
// newest episodes only; with one, it is read page by page until enough
// episodes pass the filter.
func (s *Service) pseJellyfinLatestEpisodes(ctx context.Context, viewer *core.User, viewID string, items []core.Title, limit int, keep func([]core.Title) ([]core.Title, error)) ([]core.Title, error) {
	if s.d.AiredEpisodes == nil || s.d.Visibility.SectionFor == nil {
		return nil, core.ErrNotAvailable
	}
	membership, _, err := s.pseJellyfinThemeMembership(ctx, viewer, viewID)
	if err != nil {
		return nil, err
	}
	var out []core.Title
	shows := map[core.RatingKey]core.Title{}
	var showKeys []core.RatingKey
	for _, title := range items {
		if title.Type == core.TypeShow {
			shows[title.Key] = title
			showKeys = append(showKeys, title.Key)
		} else if title.Type == core.TypeMovie {
			out = append(out, title)
		}
	}
	fetch := min(2*limit, localEpisodeCap)
	vis := withoutSectionCheck(s.d.Visibility)
	vis.GetTitle = func(_ context.Context, key core.RatingKey) (*core.Title, error) {
		if show, ok := shows[key]; ok {
			return &show, nil
		}
		return nil, core.ErrNotFound
	}
	for batch := range slices.Chunk(showKeys, 50) {
		var pages map[core.RatingKey]core.Page
		if s.d.AiredEpisodesMany != nil && len(batch) > 1 {
			pages, err = s.d.AiredEpisodesMany(ctx, batch, fetch)
		} else {
			pages = make(map[core.RatingKey]core.Page, len(batch))
			for _, show := range batch {
				if pages[show], err = s.d.AiredEpisodes(ctx, show, 0, fetch); err != nil {
					break
				}
			}
		}
		if err != nil {
			return nil, err
		}
		for _, show := range batch {
			listCtx := ctx
			if membership.langFree[show] {
				listCtx = core.WithPSELangFree(ctx)
			}
			page := pages[show]
			accepted := 0
			for offset := 0; ; {
				var episodes []core.Title
				for _, episode := range page.Items {
					if episode.Type == core.TypeEpisode && episode.ShowKey == show && vis.Playable(listCtx, viewer, &episode) {
						episodes = append(episodes, episode)
					}
				}
				episodes = vis.Available(listCtx, viewer, episodes)
				if keep != nil {
					if episodes, err = keep(episodes); err != nil {
						return nil, err
					}
				}
				out = append(out, episodes...)
				accepted += len(episodes)
				offset += len(page.Items)
				// Without a filter the newest episodes are enough. With one, read
				// on page by page, as the library feed does, until enough
				// episodes are accepted or the series is exhausted.
				if keep == nil || accepted >= limit || len(page.Items) == 0 || offset >= page.Total || offset >= activityScanLimit {
					break
				}
				if err = ctx.Err(); err != nil {
					return nil, err
				}
				if page, err = s.d.AiredEpisodes(ctx, show, offset, localEpisodeCap); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}
