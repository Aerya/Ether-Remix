package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// A custom catalogue library's recursive episode listing (Plex section
// allLeaves) must take the indexed page, not the whole-library walk:
// libraryBrowseFilter stamps the library's own type on the filter, and
// that stamp alone used to fail the indexed page's plain-filter check.
func TestRecursiveIndexWindowCustomLibrary(t *testing.T) {
	v := &core.User{ID: 1, IsOwner: true}
	show := core.Title{Key: core.ShowKey(9), Type: core.TypeShow, Title: "Series"}
	ep := core.Title{Key: core.EpisodeKey(9, 1, 1), ShowKey: show.Key, Type: core.TypeEpisode, Title: "Episode"}
	calls := 0
	s := &Service{d: ServiceDeps{
		Active:          func(context.Context, int64) (*core.User, error) { return v, nil },
		PluginLibraries: fakeCollectionLibraries{{ID: 7, Name: "All TV", Source: core.LibraryCatalog, MediaType: core.TypeShow}},
		LibraryPresentation: func(_ core.Presentations, mt core.MediaType) (core.MediaType, string, bool) {
			return mt, "", true
		},
		Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 7, true }},
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			calls++
			if q.Filter.Type != core.TypeEpisode {
				t.Fatalf("expanded series instead of querying episodes: %+v", q)
			}
			return core.Page{Items: []core.Title{ep}, Total: 100000}, nil
		},
		PageExclusions: func(context.Context, *core.User, core.MediaType) LibraryPageExclusions {
			return LibraryPageExclusions{}
		},
		ParentTitles: func(context.Context, []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			return map[core.RatingKey]core.Title{show.Key: show}, nil
		},
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			t.Fatal("must never hydrate seasons")
			return nil, nil
		},
		StoredEpisodes: StoredEpisodeDeps{
			Page: func(context.Context, core.Filter, core.RatingKey, int) ([]core.Title, error) {
				t.Fatal("must preserve SQL sort")
				return nil, nil
			},
			Count:      func(context.Context, core.Filter) (int, error) { return 0, nil },
			Unranked:   func(context.Context) (bool, error) { return true, nil },
			Customized: func(context.Context, *core.User) (bool, error) { return false, nil },
		},
	}}
	q := frontend.ItemQuery{Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Sort: core.SortTitle, Offset: 400, Limit: 200}
	page, err := s.LibraryItems(context.Background(), v, core.Device{}, "7", q)
	if err != nil || len(page.Items) != 1 || calls != 1 {
		t.Fatalf("calls=%d page=%+v err=%v", calls, page, err)
	}
}

// A recursive page's work is bounded by its requested window, independently
// of the number of series. Re-check parent policy without hydrating children.
func TestRecursiveIndexWindow(t *testing.T) {
	v := &core.User{ID: 1, IsOwner: true}
	show := core.Title{Key: core.ShowKey(9), Type: core.TypeShow, Title: "Series"}
	ep := core.Title{Key: core.EpisodeKey(9, 1, 1), ShowKey: show.Key, Type: core.TypeEpisode, Title: "Episode"}
	calls := 0
	var query core.BrowseQuery
	s := &Service{d: ServiceDeps{
		Active:     func(context.Context, int64) (*core.User, error) { return v, nil },
		Libraries:  []Library{{ID: 2, Type: core.TypeShow}},
		Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }},
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			calls++
			query = q
			if q.Filter.Type != core.TypeEpisode {
				t.Fatalf("expanded series instead of querying episodes: %+v", q)
			}
			return core.Page{Items: []core.Title{ep}, Total: 100000}, nil
		},
		PageExclusions: func(context.Context, *core.User, core.MediaType) LibraryPageExclusions {
			return LibraryPageExclusions{}
		},
		ParentTitles: func(context.Context, []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			return map[core.RatingKey]core.Title{show.Key: show}, nil
		},
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			t.Fatal("must never hydrate seasons")
			return nil, nil
		},
		StoredEpisodes: StoredEpisodeDeps{
			Page: func(context.Context, core.Filter, core.RatingKey, int) ([]core.Title, error) {
				t.Fatal("must preserve SQL sort")
				return nil, nil
			},
			Count:      func(context.Context, core.Filter) (int, error) { return 0, nil },
			Unranked:   func(context.Context) (bool, error) { return true, nil },
			Customized: func(context.Context, *core.User) (bool, error) { return false, nil },
		},
	}}
	q := frontend.ItemQuery{Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Sort: core.SortTitle, Offset: 2500, Limit: 26, NoTotal: true}
	page, err := s.LibraryItems(context.Background(), v, core.Device{}, "2", q)
	if err != nil || len(page.Items) != 1 || calls != 1 || query.Offset != 2500 || query.Limit != 26 || query.Sort != core.SortTitle || !query.NoTotal {
		t.Fatalf("calls=%d query=%+v page=%+v err=%v", calls, query, page, err)
	}
	// A parent that no longer passes current visibility must not leak a child
	// or trigger an unbounded fallback scan to fill the page.
	s.d.ParentTitles = func(context.Context, []core.RatingKey) (map[core.RatingKey]core.Title, error) {
		return map[core.RatingKey]core.Title{}, nil
	}
	page, err = s.LibraryItems(context.Background(), v, core.Device{}, "2", q)
	if err != nil || len(page.Items) != 0 || calls != 2 {
		t.Fatalf("policy race calls=%d page=%+v err=%v", calls, page, err)
	}
}

// Scoped custom TV libraries (rules, empty hide-default, collection) must:
//   - bypass storedEpisodePage even on SortPopularity and send their full
//     library scope to the indexed episode/season browse query without
//     expanding shows via Grandchildren;
//   - allow a restricted viewer granted only that custom library (not the
//     canonical TV section) to see its episodes and seasons;
//   - carry the library scope into playedEpisodePage, favoriteEpisodePage,
//     and favoriteRecursivePage.
func TestRecursiveCustomLibraryCarriesScopeAndVirtualSectionAccess(t *testing.T) {
	restricted := &core.User{ID: 5, AllowedLibraries: []core.LibraryID{7}}
	animeShow := core.Title{Key: core.ShowKey(10), Type: core.TypeShow, Title: "Anime"}
	dramaShow := core.Title{Key: core.ShowKey(20), Type: core.TypeShow, Title: "Drama"}
	animeEp := core.Title{Key: core.EpisodeKey(10, 1, 1), ShowKey: animeShow.Key, Type: core.TypeEpisode, Title: "Anime Ep"}
	dramaEp := core.Title{Key: core.EpisodeKey(20, 1, 1), ShowKey: dramaShow.Key, Type: core.TypeEpisode, Title: "Drama Ep"}
	animeRule := []core.LibraryExclusion{{Action: "show", MediaType: "show", Match: "all"}}

	var lastBrowse core.BrowseQuery
	var lastPlayed, lastFav core.Filter
	s := &Service{d: ServiceDeps{
		Active: func(context.Context, int64) (*core.User, error) { return restricted, nil },
		PluginLibraries: fakeCollectionLibraries{
			{ID: 7, Name: "Anime", Source: core.LibraryCatalog, MediaType: core.TypeShow, Rules: animeRule, VisibilityDefault: "hide"},
			{ID: 8, Name: "Empty Hide", Source: core.LibraryCatalog, MediaType: core.TypeShow, VisibilityDefault: "hide"},
		},
		LibraryPresentation: func(_ core.Presentations, mt core.MediaType) (core.MediaType, string, bool) {
			return mt, "", true
		},
		// Canonical TV section is 2, which restricted does NOT have access to.
		Visibility: Visibility{
			SectionFor: func(core.MediaType) (int, bool) { return 2, true },
			GetTitle: func(_ context.Context, k core.RatingKey) (*core.Title, error) {
				if k == animeShow.Key {
					return &animeShow, nil
				}
				if k == dramaShow.Key {
					return &dramaShow, nil
				}
				return nil, core.ErrNotFound
			},
		},
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			lastBrowse = q
			if q.Filter.ExtraFallback == "hide" && len(q.Filter.ExtraRules) == 0 {
				return core.Page{Items: []core.Title{}, Total: 0}, nil
			}
			if q.Filter.Type == core.TypeShow && q.Filter.IncludeKeys != nil {
				// favoriteRecursivePage show-membership check: admit only animeShow.
				var out []core.Title
				for _, k := range q.Filter.IncludeKeys {
					if k == animeShow.Key && len(q.Filter.ExtraRules) == 1 {
						out = append(out, animeShow)
					}
				}
				return core.Page{Items: out, Total: len(out)}, nil
			}
			return core.Page{Items: []core.Title{animeEp}, Total: 1}, nil
		},
		PageExclusions: func(context.Context, *core.User, core.MediaType) LibraryPageExclusions {
			return LibraryPageExclusions{}
		},
		ParentTitles: func(context.Context, []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			return map[core.RatingKey]core.Title{animeShow.Key: animeShow, dramaShow.Key: dramaShow}, nil
		},
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			t.Fatal("must never expand shows via Grandchildren")
			return nil, nil
		},
		StoredEpisodes: StoredEpisodeDeps{
			Page: func(context.Context, core.Filter, core.RatingKey, int) ([]core.Title, error) {
				t.Fatal("scoped custom library must not enter unscoped storedEpisodePage")
				return nil, nil
			},
			Count:      func(context.Context, core.Filter) (int, error) { return 0, nil },
			Unranked:   func(context.Context) (bool, error) { return true, nil },
			Customized: func(context.Context, *core.User) (bool, error) { return false, nil },
		},
		PlayedEpisodes: PlayedEpisodeDeps{
			Count: func(_ context.Context, f core.Filter, _ int64, _ core.RatingKey) (int, error) {
				lastPlayed = f
				return 1, nil
			},
			Page: func(_ context.Context, f core.Filter, _ int64, _ core.RatingKey, _ bool, _, _ int) ([]core.Title, error) {
				lastPlayed = f
				return []core.Title{animeEp}, nil
			},
		},
		FavoriteEpisodes: FavoriteEpisodeDeps{
			Count: func(_ context.Context, f core.Filter, _ int64, _ core.RatingKey) (int, error) {
				lastFav = f
				return 1, nil
			},
			Page: func(_ context.Context, f core.Filter, _ int64, _ core.RatingKey, _, _ int) ([]core.Title, error) {
				lastFav = f
				return []core.Title{animeEp}, nil
			},
		},
		FavoriteTitleKeys: func(context.Context, *core.User, int) ([]core.RatingKey, bool, error) {
			return []core.RatingKey{animeEp.Key, dramaEp.Key}, true, nil
		},
		CachedGetMany: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			return map[core.RatingKey]core.Title{animeEp.Key: animeEp, dramaEp.Key: dramaEp}, nil
		},
	}}

	// 1. SortPopularity on custom rule library routes through indexedRecursivePage
	// with ExtraRules/ExtraFallback intact and passes virtual-section visibility.
	page, err := s.LibraryItems(context.Background(), restricted, core.Device{}, "7", frontend.ItemQuery{
		Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Sort: core.SortPopularity, Limit: 25,
	})
	if err != nil || len(page.Items) != 1 || page.Items[0].Key != animeEp.Key {
		t.Fatalf("rule library SortPopularity = %+v (%v)", page, err)
	}
	if lastBrowse.Filter.Type != core.TypeEpisode || len(lastBrowse.Filter.ExtraRules) != 1 || lastBrowse.Filter.ExtraFallback != "hide" {
		t.Fatalf("rule library browse filter = %+v", lastBrowse.Filter)
	}

	// 2. Empty hide-default library (no rules, VisibilityDefault="hide") must also
	// skip storedEpisodePage and pass ExtraFallback="hide" to Browse.
	emptyUser := &core.User{ID: 6, AllowedLibraries: []core.LibraryID{8}}
	s.d.Active = func(context.Context, int64) (*core.User, error) { return emptyUser, nil }
	emptyPage, err := s.LibraryItems(context.Background(), emptyUser, core.Device{}, "8", frontend.ItemQuery{
		Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Sort: core.SortPopularity, Limit: 25,
	})
	if err != nil || len(emptyPage.Items) != 0 || emptyPage.Total != 0 || lastBrowse.Filter.ExtraFallback != "hide" {
		t.Fatalf("empty hide-default library = %+v (%v) filter=%+v", emptyPage, err, lastBrowse.Filter)
	}
	s.d.Active = func(context.Context, int64) (*core.User, error) { return restricted, nil }

	// 3. Played episodes (DatePlayed) carry ExtraRules/ExtraFallback and respect virtual access.
	yes := true
	played, err := s.LibraryItems(context.Background(), restricted, core.Device{}, "7", frontend.ItemQuery{
		Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Watched: &yes, Sort: core.SortPlayed, Desc: true, Limit: 10,
	})
	if err != nil || len(played.Items) != 1 || played.Items[0].Key != animeEp.Key || len(lastPlayed.ExtraRules) != 1 || lastPlayed.ExtraFallback != "hide" {
		t.Fatalf("played episodes = %+v (%v) filter=%+v", played, err, lastPlayed)
	}

	// 4. Favorite episodes (SortTitle) carry ExtraRules/ExtraFallback and respect virtual access.
	favEp, err := s.LibraryItems(context.Background(), restricted, core.Device{}, "7", frontend.ItemQuery{
		Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Favorite: &yes, Sort: core.SortTitle, Limit: 10,
	})
	if err != nil || len(favEp.Items) != 1 || favEp.Items[0].Key != animeEp.Key || len(lastFav.ExtraRules) != 1 || lastFav.ExtraFallback != "hide" {
		t.Fatalf("favorite episodes = %+v (%v) filter=%+v", favEp, err, lastFav)
	}

	// 5. Favorite recursive (non-SortTitle order -> favoriteRecursivePage) drops out-of-library dramaEp.
	favRec, err := s.LibraryItems(context.Background(), restricted, core.Device{}, "7", frontend.ItemQuery{
		Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Favorite: &yes, Sort: core.SortReleased, Desc: true, Limit: 10,
	})
	if err != nil || len(favRec.Items) != 1 || favRec.Items[0].Key != animeEp.Key {
		t.Fatalf("favorite recursive = %+v (%v)", favRec, err)
	}
}
