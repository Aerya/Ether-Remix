package experience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type activityState struct {
	core.State
	statuses []core.WatchStatus
}

func (s activityState) InProgress(context.Context, int64, int) ([]core.WatchStatus, error) {
	return s.statuses, nil
}

func (s activityState) GetMany(_ context.Context, _ int64, keys []core.RatingKey) (map[core.RatingKey]core.WatchStatus, error) {
	out := make(map[core.RatingKey]core.WatchStatus, len(keys))
	for _, key := range keys {
		out[key] = core.WatchStatus{Key: key}
	}
	for _, status := range s.statuses {
		if _, ok := out[status.Key]; ok {
			out[status.Key] = status
		}
	}
	return out, nil
}

func TestLibraryActivityDoesNotDependOnHomeAndFiltersBeforeLimit(t *testing.T) {
	ctx := context.Background()
	v := &core.User{ID: 1, MaxContentRating: "PG", AllowedLibraries: []core.LibraryID{1}}
	movie1 := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "Hidden", ContentRating: "R"}
	movie2 := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Visible", ContentRating: "PG"}
	movie3 := core.Title{Key: core.MovieKey(3), Type: core.TypeMovie, Title: "Older", ContentRating: "G"}
	show := core.Title{Key: core.ShowKey(1), Type: core.TypeShow, ContentRating: "TV-MA"}
	episode := core.Title{Key: core.EpisodeKey(1, 1, 1), Type: core.TypeEpisode, ShowKey: show.Key, ContentRating: "TV-G"}
	titles := map[core.RatingKey]*core.Title{movie1.Key: &movie1, movie2.Key: &movie2, movie3.Key: &movie3, show.Key: &show, episode.Key: &episode}
	get := func(_ context.Context, key core.RatingKey) (*core.Title, error) {
		if t := titles[key]; t != nil {
			return t, nil
		}
		return nil, core.ErrNotFound
	}
	var browseCalls int
	s := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return v, nil }, Libraries: []Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}}, CachedGet: get, State: activityState{statuses: []core.WatchStatus{{Key: movie1.Key}, {Key: episode.Key}, {Key: movie2.Key}, {Key: movie3.Key}}}, AddedTimes: func(context.Context, []core.RatingKey) (map[core.RatingKey]time.Time, error) {
		return map[core.RatingKey]time.Time{movie2.Key: time.Unix(20, 0), movie3.Key: time.Unix(10, 0)}, nil
	}, Visibility: Visibility{GetTitle: get, SectionFor: func(typ core.MediaType) (int, bool) {
		if typ == core.TypeMovie {
			return 1, true
		}
		return 2, true
	}}, Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
		browseCalls++
		if q.Filter.Type != core.TypeMovie || q.Sort != core.SortAdded || !q.Desc {
			t.Fatalf("unexpected browse %+v", q)
		}
		return core.Page{Items: []core.Title{movie1, movie2, movie3}, Total: 3}, nil
	}}}
	latest, err := s.LatestAdded(ctx, v, core.Device{}, "1", 1)
	if err != nil || len(latest) != 1 || latest[0].Title.Key != movie2.Key || latest[0].AddedAt == nil || latest[0].AddedAt.Unix() != 20 || browseCalls != 1 {
		t.Fatalf("latest %+v %v", latest, err)
	}
	if _, err := s.LatestAddedGrouped(ctx, v, core.Device{}, "1", 1, false); err != nil {
		t.Fatalf("movie grouping incorrectly rejected: %v", err)
	}
	resume, err := s.Resume(ctx, v, core.Device{}, "1", []core.MediaType{core.TypeMovie}, 0, 1, false)
	if err != nil || len(resume.Items) != 1 || resume.Items[0].Key != movie2.Key || !resume.More || resume.TotalExact {
		t.Fatalf("resume %+v %v", resume, err)
	}
	resume, err = s.Resume(ctx, v, core.Device{}, "1", []core.MediaType{core.TypeMovie}, 0, 1, true)
	if err != nil || !resume.TotalExact || resume.Total != 2 || !resume.More {
		t.Fatalf("exact resume count %+v %v", resume, err)
	}
	resume, err = s.Resume(ctx, v, core.Device{}, "1", []core.MediaType{core.TypeMovie}, 1, 1, false)
	if err != nil || len(resume.Items) != 1 || resume.Items[0].Key != movie3.Key || resume.More || !resume.TotalExact || resume.Total != 2 {
		t.Fatalf("resume next page %+v %v", resume, err)
	}
	if _, err = s.LatestAdded(ctx, v, core.Device{}, "2", 1); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("restricted view: %v", err)
	}
	if _, err = s.Resume(ctx, v, core.Device{}, "2", nil, 0, 1, false); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("restricted resume view: %v", err)
	}
	// The same child remains hidden by inherited show policy when the section is allowed.
	v.AllowedLibraries = []core.LibraryID{1, 2}
	s.d.AiredEpisodes = func(context.Context, core.RatingKey, int, int) (core.Page, error) {
		return core.Page{Items: []core.Title{episode}, Total: 1}, nil
	}
	if got, err := s.LatestAddedGrouped(ctx, v, core.Device{}, "2", 1, false); err != nil || len(got) != 0 {
		t.Fatalf("restricted episode leaked in ungrouped latest: %+v %v", got, err)
	}
	resume, err = s.Resume(ctx, v, core.Device{}, "2", []core.MediaType{core.TypeEpisode}, 0, 5, false)
	if err != nil || len(resume.Items) != 0 || !resume.TotalExact {
		t.Fatalf("restricted parent leaked episode: %+v %v", resume, err)
	}
}

func TestLatestHidesPlayedWhenTheQueryOmitsIsPlayed(t *testing.T) {
	v := &core.User{ID: 1}
	played := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Played"}
	fresh := core.Title{Key: core.MovieKey(3), Type: core.TypeMovie, Title: "Fresh"}
	s := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return v, nil }, Libraries: []Library{{ID: 1, Type: core.TypeMovie}}, State: activityState{statuses: []core.WatchStatus{{Key: played.Key, ViewCount: 1}}}, Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }}, Browse: func(context.Context, core.BrowseQuery) (core.Page, error) {
		return core.Page{Items: []core.Title{played, fresh}, Total: 2}, nil
	}, AddedTimes: func(context.Context, []core.RatingKey) (map[core.RatingKey]time.Time, error) {
		return map[core.RatingKey]time.Time{played.Key: time.Unix(30, 0), fresh.Key: time.Unix(10, 0)}, nil
	}}}
	hidden, err := s.LatestAdded(frontend.WithLatestPlayed(context.Background(), false), v, core.Device{}, "1", 16)
	if err != nil || len(hidden) != 1 || hidden[0].Title.Key != fresh.Key {
		t.Fatalf("unplayed latest %+v %v", hidden, err)
	}
	onlyPlayed, err := s.LatestAdded(frontend.WithLatestPlayed(context.Background(), true), v, core.Device{}, "1", 16)
	if err != nil || len(onlyPlayed) != 1 || onlyPlayed[0].Title.Key != played.Key {
		t.Fatalf("played latest %+v %v", onlyPlayed, err)
	}
}

func TestLatestAddedGlobalMergesRealAddedTimes(t *testing.T) {
	v := &core.User{ID: 1}
	movie := core.Title{Key: core.MovieKey(4), Type: core.TypeMovie}
	show := core.Title{Key: core.ShowKey(5), Type: core.TypeShow}
	s := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return v, nil }, Libraries: []Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}}, Visibility: Visibility{SectionFor: func(typ core.MediaType) (int, bool) {
		if typ == core.TypeMovie {
			return 1, true
		}
		return 2, true
	}}, Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
		if q.Filter.Type == core.TypeMovie {
			return core.Page{Items: []core.Title{movie}, Total: 1}, nil
		}
		return core.Page{Items: []core.Title{show}, Total: 1}, nil
	}, AddedTimes: func(context.Context, []core.RatingKey) (map[core.RatingKey]time.Time, error) {
		return map[core.RatingKey]time.Time{movie.Key: time.Unix(10, 0), show.Key: time.Unix(20, 0)}, nil
	}}}
	got, err := s.LatestAdded(context.Background(), v, core.Device{}, "", 2)
	if err != nil || len(got) != 2 || got[0].Title.Key != show.Key || got[0].AddedAt == nil || got[0].AddedAt.Unix() != 20 || got[1].Title.Key != movie.Key {
		t.Fatalf("global latest %+v %v", got, err)
	}
}

func TestLatestUngroupedTVFiltersBeforeLimit(t *testing.T) {
	v := &core.User{ID: 1, AllowedLibraries: []core.LibraryID{2}, MaxContentRating: "TV-PG"}
	show := core.Title{Key: core.ShowKey(5), Type: core.TypeShow, ContentRating: "TV-PG"}
	hidden := core.Title{Key: core.EpisodeKey(5, 1, 1), Type: core.TypeEpisode, ShowKey: show.Key, ContentRating: "TV-MA"}
	unknown := core.Title{Key: core.EpisodeKey(5, 1, 2), Type: core.TypeEpisode, ShowKey: show.Key, ContentRating: "TV-PG"}
	visible := core.Title{Key: core.EpisodeKey(5, 1, 3), Type: core.TypeEpisode, ShowKey: show.Key, ContentRating: "TV-PG"}
	air := time.Unix(1700000000, 0).UTC()
	s := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return v, nil }, Libraries: []Library{{ID: 2, Type: core.TypeShow}}, Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }, GetTitle: func(context.Context, core.RatingKey) (*core.Title, error) { return &show, nil }}, Browse: func(context.Context, core.BrowseQuery) (core.Page, error) {
		t.Fatal("ungrouped TV used series browse")
		return core.Page{}, nil
	}, AiredEpisodes: func(context.Context, core.RatingKey, int, int) (core.Page, error) {
		return core.Page{Items: []core.Title{hidden, unknown, visible}, Total: 3}, nil
	}, AddedTimes: func(context.Context, []core.RatingKey) (map[core.RatingKey]time.Time, error) {
		return map[core.RatingKey]time.Time{hidden.Key: air.Add(time.Hour), visible.Key: air}, nil
	}}}
	got, err := s.LatestAddedGrouped(context.Background(), v, core.Device{}, "2", 1, false)
	if err != nil || len(got) != 1 || got[0].Title.Key != visible.Key || got[0].AddedAt == nil || !got[0].AddedAt.Equal(air) {
		t.Fatalf("ungrouped latest %+v %v", got, err)
	}
}

type fakeLatestPluginLibraries struct {
	libs        []core.Library
	pluginItems map[core.LibraryID][]core.Title
}

func (f fakeLatestPluginLibraries) Libraries(context.Context) ([]core.Library, error) {
	return f.libs, nil
}
func (f fakeLatestPluginLibraries) Library(_ context.Context, id core.LibraryID) (core.Library, error) {
	for _, lib := range f.libs {
		if lib.ID == id {
			return lib, nil
		}
	}
	return core.Library{}, core.ErrNotFound
}
func (f fakeLatestPluginLibraries) IsPluginLibraryActive(_ context.Context, lib core.Library) bool {
	return lib.Source == core.LibraryPlugin
}
func (f fakeLatestPluginLibraries) RootMediaType(_ context.Context, lib core.Library) core.MediaType {
	if lib.MediaType != "" {
		return lib.MediaType
	}
	return core.TypeMovie
}
func (f fakeLatestPluginLibraries) Count(context.Context, core.Library) (int, error) { return 0, nil }
func (f fakeLatestPluginLibraries) Browse(_ context.Context, lib core.Library, _ frontend.ItemQuery, _ string, limit int) (PluginBrowsePage, error) {
	items := f.pluginItems[lib.ID]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return PluginBrowsePage{Items: items, Total: len(items), TotalExact: true}, nil
}
func (f fakeLatestPluginLibraries) Item(context.Context, core.RatingKey) (PluginItemView, error) {
	return PluginItemView{}, core.ErrNotFound
}
func (f fakeLatestPluginLibraries) ItemsMany(context.Context, []core.RatingKey) (map[core.RatingKey]PluginItemView, error) {
	return nil, nil
}
func (f fakeLatestPluginLibraries) Children(context.Context, core.RatingKey, bool, string, int) (PluginBrowsePage, error) {
	return PluginBrowsePage{}, core.ErrNotFound
}
func (f fakeLatestPluginLibraries) Ancestors(context.Context, core.RatingKey) ([]core.Title, core.Library, error) {
	return nil, core.Library{}, core.ErrNotFound
}
func (f fakeLatestPluginLibraries) Facets(context.Context, core.Library, string) ([]core.FacetValue, error) {
	return nil, nil
}
func (f fakeLatestPluginLibraries) Search(context.Context, core.Library, string, frontend.ItemQuery, int) ([]core.Title, error) {
	return nil, nil
}
func (f fakeLatestPluginLibraries) MemberOf(context.Context, core.Library, core.RatingKey) (bool, error) {
	return false, nil
}

func TestLatestAddedHonorsEveryLibrarySource(t *testing.T) {
	ctx := context.Background()
	published := core.Presentations{MediaBrowser: &core.MediaBrowserPresentation{CollectionType: "movies"}}
	publishedTV := core.Presentations{MediaBrowser: &core.MediaBrowserPresentation{CollectionType: "tvshows"}}
	// Viewer is restricted to the custom libraries (built-in Movies=1 and Shows=2 disallowed).
	viewer := &core.User{ID: 7, AllowedLibraries: []core.LibraryID{9, 10, 11, 12, 13, 14}}

	collMovie := core.Title{Key: core.MovieKey(10), Type: core.TypeMovie, Title: "Collection Movie", AddedAt: time.Unix(200, 0).UTC()}
	catMovie := core.Title{Key: core.MovieKey(11), Type: core.TypeMovie, Title: "Catalogue Whole Movie", AddedAt: time.Unix(900, 0).UTC()}
	sourceMovie := core.Title{Key: core.MovieKey(12), Type: core.TypeMovie, Title: "Stremio Movie", AddedAt: time.Unix(300, 0).UTC()}
	ruleMovie := core.Title{Key: core.MovieKey(13), Type: core.TypeMovie, Title: "Rule Movie", AddedAt: time.Unix(250, 0).UTC()}
	collShow := core.Title{Key: core.ShowKey(20), Type: core.TypeShow, Title: "Collection Show", AddedAt: time.Unix(400, 0).UTC()}
	collEp := core.Title{Key: core.EpisodeKey(20, 1, 1), ShowKey: collShow.Key, Type: core.TypeEpisode, Title: "Collection Ep", AddedAt: time.Unix(400, 0).UTC()}
	pluginItem := core.Title{Key: core.PluginKey(1), Type: core.TypeMovie, Title: "Plugin Item", PluginAddedAt: 500}

	libs := fakeLatestPluginLibraries{
		libs: []core.Library{
			{ID: core.LibraryMovies, Name: "Movies", Source: core.LibraryCatalog, MediaType: core.TypeMovie, Present: published},
			{ID: core.LibraryShows, Name: "TV Shows", Source: core.LibraryCatalog, MediaType: core.TypeShow, Present: publishedTV},
			{ID: 9, Name: "Collection Movies", Source: core.LibraryCollection, MediaType: core.TypeMovie, CollectionID: 42, Present: published},
			{ID: 10, Name: "Collection Shows", Source: core.LibraryCollection, MediaType: core.TypeShow, CollectionID: 43, Present: publishedTV},
			{ID: 11, Name: "Source Movies", Source: core.LibraryCatalog, MediaType: core.TypeMovie, Sources: []string{"stremio-pick"}, Present: published},
			{ID: 12, Name: "Rule Movies", Source: core.LibraryCatalog, MediaType: core.TypeMovie, Rules: []core.LibraryExclusion{{Name: "Allow", Action: "allow"}}, VisibilityDefault: "hide", Present: published},
			{ID: 13, Name: "Plugin Lib", Source: core.LibraryPlugin, MediaType: core.TypeMovie, PluginID: "plug", Present: published},
			{ID: 14, Name: "Unbound Collection", Source: core.LibraryCollection, MediaType: core.TypeMovie, CollectionID: 0, Present: published},
		},
		pluginItems: map[core.LibraryID][]core.Title{13: {pluginItem}},
	}

	s := &Service{d: ServiceDeps{
		Protocol:        core.Protocol("jellyfin"),
		Active:          func(context.Context, int64) (*core.User, error) { return viewer, nil },
		PluginLibraries: libs,
		LibraryPresentation: func(p core.Presentations, mt core.MediaType) (core.MediaType, string, bool) {
			if p.MediaBrowser == nil {
				return "", "", false
			}
			return mt, p.MediaBrowser.CollectionType, true
		},
		State: activityState{},
		ParentTitles: func(context.Context, []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			return map[core.RatingKey]core.Title{collShow.Key: collShow}, nil
		},
		Visibility: Visibility{
			SectionFor: func(typ core.MediaType) (int, bool) {
				if typ == core.TypeMovie {
					return 1, true
				}
				return 2, true
			},
			FilterAvailable: func(_ context.Context, titles []core.Title) []core.Title { return titles },
		},
		PageExclusions: func(context.Context, *core.User, core.MediaType) LibraryPageExclusions {
			return LibraryPageExclusions{AvailabilityInSQL: true}
		},
		RecentAir: func(context.Context, core.MediaType, int, int) ([]core.Title, bool, error) {
			t.Fatal("RecentAir must not serve scoped or disallowed libraries")
			return nil, true, nil
		},
		AiredEpisodes: func(context.Context, core.RatingKey, int, int) (core.Page, error) {
			t.Fatal("unscoped AiredEpisodes must not serve collection show libraries")
			return core.Page{}, nil
		},
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			switch {
			case q.Filter.CollectionMembers == 42 && q.Filter.Type == core.TypeMovie:
				return core.Page{Items: []core.Title{collMovie}, Total: 1}, nil
			case q.Filter.CollectionMembers == 43 && q.Filter.Type == core.TypeShow:
				return core.Page{Items: []core.Title{collShow}, Total: 1}, nil
			case q.Filter.CollectionMembers == 43 && q.Filter.Type == core.TypeEpisode:
				return core.Page{Items: []core.Title{collEp}, Total: 1}, nil
			case len(q.Filter.CatalogueSources) == 1 && q.Filter.CatalogueSources[0] == "stremio-pick":
				return core.Page{Items: []core.Title{sourceMovie}, Total: 1}, nil
			case len(q.Filter.ExtraRules) == 1 && q.Filter.ExtraFallback == "hide":
				return core.Page{Items: []core.Title{ruleMovie}, Total: 1}, nil
			default:
				return core.Page{Items: []core.Title{catMovie}, Total: 1}, nil
			}
		},
	}}

	for _, tc := range []struct {
		name   string
		viewID string
		group  bool
		want   []core.RatingKey
	}{
		{"collection movies", "9", true, []core.RatingKey{collMovie.Key}},
		{"collection shows grouped", "10", true, []core.RatingKey{collShow.Key}},
		{"collection shows ungrouped", "10", false, []core.RatingKey{collEp.Key}},
		{"custom catalogue source", "11", true, []core.RatingKey{sourceMovie.Key}},
		{"rule-filtered catalogue", "12", true, []core.RatingKey{ruleMovie.Key}},
		{"plugin library", "13", true, []core.RatingKey{pluginItem.Key}},
		{"unbound collection returns empty", "14", true, nil},
		{"global skips disallowed builtins", "", true, []core.RatingKey{pluginItem.Key, collShow.Key, sourceMovie.Key, ruleMovie.Key, collMovie.Key}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.LatestAddedGrouped(ctx, viewer, core.Device{}, tc.viewID, 10, tc.group)
			if err != nil {
				t.Fatalf("LatestAddedGrouped(%q, group=%v): %v", tc.viewID, tc.group, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d items (%+v), want %d (%v)", len(got), got, len(tc.want), tc.want)
			}
			for i, wantKey := range tc.want {
				if got[i].Title.Key != wantKey {
					t.Fatalf("item[%d] = %v, want %v", i, got[i].Title.Key, wantKey)
				}
			}
		})
	}
	if _, err := s.LatestAddedGrouped(ctx, viewer, core.Device{}, "1", 10, true); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("disallowed built-in library error = %v, want ErrNotFound", err)
	}
}
