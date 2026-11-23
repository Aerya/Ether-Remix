package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// favoriteRecursivePage answers a recursive TV listing filtered to the
// viewer's favorites ("IsFavorite", as Jellyfin for Roku's Favorites row asks,
// and as Moonfin's Favorites screen asks one type at a time, #405) from the
// favorites themselves. The library walk would otherwise expand every show into
// its seasons and episodes to find the handful the viewer starred, which reads
// every season of the library inside one request (#421). The set is the same: a
// favorite show, season or episode in this library that the viewer may see and
// that matches the query. showKey, when nonzero, restricts the listing to one
// series (ParentId): favorite episodes of that show are answered the same way,
// without expanding the series. ok is false when this path does not apply and
// the caller must list the library.
func (s *Service) favoriteRecursivePage(ctx context.Context, v *core.User, library *Library, showKey core.RatingKey, q frontend.ItemQuery) (core.Page, bool, error) {
	if library == nil || library.Type != core.TypeShow || !q.Recursive || q.Favorite == nil || !*q.Favorite ||
		q.AdjacentTo != 0 || q.StartAt != 0 || q.Filter.IncludeKeys != nil || s.d.FavoriteTitleKeys == nil || s.d.CachedGetMany == nil {
		return core.Page{}, false, nil
	}
	keys, all, err := s.d.FavoriteTitleKeys(ctx, v, maxIndexedExclusions)
	if err != nil || !all {
		return core.Page{}, false, err
	}
	want := func(typ core.MediaType) bool { return len(q.Types) == 0 || containsMediaType(q.Types, typ) }
	var tree []core.RatingKey
	for _, k := range keys {
		switch k.Kind() {
		case core.KindShow:
			// A single-series listing never returns the show or its seasons.
			if showKey == 0 && want(core.TypeShow) {
				tree = append(tree, k)
			}
		case core.KindSeason:
			if showKey == 0 && want(core.TypeSeason) {
				tree = append(tree, k)
			}
		case core.KindEpisode:
			if want(core.TypeEpisode) && (showKey == 0 || k.TMDB() == showKey.TMDB()) {
				tree = append(tree, k)
			}
		}
	}
	permitted := []core.Title{}
	if len(tree) > 0 {
		var admittedShows map[core.RatingKey]bool
		if showKey == 0 && filterHasLibraryScope(q.Filter) {
			if s.d.Browse == nil {
				return core.Page{}, false, nil
			}
			var uniqueShows []core.RatingKey
			seenShow := map[core.RatingKey]bool{}
			for _, k := range tree {
				sk := k
				if k.Kind() != core.KindShow {
					sk = core.ShowKey(k.TMDB())
				}
				if !seenShow[sk] {
					seenShow[sk] = true
					uniqueShows = append(uniqueShows, sk)
				}
			}
			admittedShows = make(map[core.RatingKey]bool, len(uniqueShows))
			sf := scopedShowFilter(q.Filter, v)
			sf.CatalogViewerID = v.ID
			for i := 0; i < len(uniqueShows); i += 400 {
				chunk := uniqueShows[i:min(i+400, len(uniqueShows))]
				f := sf
				f.IncludeKeys = chunk
				page, err := s.d.Browse(ctx, core.BrowseQuery{Filter: f, Limit: len(chunk), NoTotal: true})
				if err != nil {
					return core.Page{}, false, err
				}
				for _, t := range page.Items {
					admittedShows[t.Key] = true
				}
			}
		}
		titles, err := s.d.CachedGetMany(ctx, tree)
		if err != nil {
			return core.Page{}, false, err
		}
		query := q
		query.Favorite = nil
		if query.Filter.Type == library.Type {
			query.Filter.Type = ""
		}
		virtualVis := withoutSectionCheck(s.d.Visibility)
		for _, k := range tree {
			if admittedShows != nil {
				sk := k
				if k.Kind() != core.KindShow {
					sk = core.ShowKey(k.TMDB())
				}
				if !admittedShows[sk] {
					continue
				}
			}
			title, found := titles[k]
			if !found {
				continue
			}
			if library.Virtual {
				if !virtualVis.Visible(ctx, v, &title) {
					continue
				}
			} else if !s.visible(ctx, v, &title) {
				continue
			}
			ok, err := s.matchesUserQuery(ctx, v, title, query)
			if err != nil {
				return core.Page{}, false, err
			}
			if ok {
				permitted = append(permitted, title)
			}
		}
		if err := s.sortTitles(ctx, permitted, q); err != nil {
			return core.Page{}, false, err
		}
	}
	total := len(permitted)
	start := min(max(q.Offset, 0), total)
	end := total
	if q.Limit > 0 {
		end = min(start+q.Limit, total)
	}
	return core.Page{Items: permitted[start:end], Total: total}, true, nil
}

// MirrorCounts returns the indexed episode and franchise totals when the
// viewer has no per-title content cap or judged rule that would invalidate
// the mirror-wide tally.
func (s *Service) MirrorCounts(ctx context.Context, viewer *core.Viewer) (episodes, collections int, err error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if s == nil || s.d.Catalog == nil {
		return 0, 0, nil
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return 0, 0, err
	}
	if v.MaxContentRating != "" {
		return 0, 0, nil
	}
	if s.d.ViewerRules != nil {
		if _, judged := s.d.ViewerRules(v); judged {
			return 0, 0, nil
		}
	}
	counter, ok := s.d.Catalog.(interface {
		MirrorCounts(context.Context) (int, int, error)
	})
	if !ok {
		return 0, 0, nil
	}
	return counter.MirrorCounts(ctx)
}

// FavoriteCounts counts a viewer's visible favorite movies, series and
// episodes from their bounded favorite rows without walking any library.
func (s *Service) FavoriteCounts(ctx context.Context, viewer *core.Viewer) (movies, series, episodes int, err error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if s == nil || s.d.FavoriteTitleKeys == nil || s.d.CachedGetMany == nil {
		return 0, 0, 0, nil
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return 0, 0, 0, err
	}
	keys, _, err := s.d.FavoriteTitleKeys(ctx, v, maxIndexedExclusions)
	if err != nil || len(keys) == 0 {
		return 0, 0, 0, err
	}
	titles, err := s.d.CachedGetMany(ctx, keys)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, k := range keys {
		title, found := titles[k]
		if !found || !s.visible(ctx, v, &title) {
			continue
		}
		switch title.Type {
		case core.TypeMovie:
			movies++
		case core.TypeShow:
			series++
		case core.TypeEpisode:
			episodes++
		}
	}
	return movies, series, episodes, nil
}
