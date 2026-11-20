package experience

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type jellyfinThemeViewsSource struct {
	parts []frontend.PSETheme
	keys  map[string][]core.RatingKey
}

func (s jellyfinThemeViewsSource) Themes() []frontend.PSETheme { return s.parts }

func (s jellyfinThemeViewsSource) RowKeys(_ context.Context, row string, v *core.User) []core.RatingKey {
	if row == "private" && !v.IsOwner {
		// A collection outside the viewer's media ACL can be empty without
		// returning a nil slice.
		return []core.RatingKey{}
	}
	return s.keys[row]
}

func jellyfinThemeViewsFixture(t *testing.T, viewer *core.User) *Service {
	t.Helper()
	movie := core.Title{Key: core.MovieKey(1), Title: "Le film", Type: core.TypeMovie, ContentRating: "PG",
		Genres: []string{"Animation"}, Studios: []string{"Studio Arnica"}}
	adult := core.Title{Key: core.MovieKey(2), Title: "Hors classification", Type: core.TypeMovie, ContentRating: "R",
		Genres: []string{"Horreur"}, Studios: []string{"Studio Adultes"}}
	show := core.Title{Key: core.ShowKey(5), Title: "La série", Type: core.TypeShow, ContentRating: "TV-PG",
		Genres: []string{"Animation", "Aventure"}, Studios: []string{"Studio Bruyère"}}
	season := core.Title{Key: core.SeasonKey(5, 1), ShowKey: show.Key, Type: core.TypeSeason, ContentRating: "TV-PG", Season: 1}
	episode := core.Title{Key: core.EpisodeKey(5, 1, 1), ShowKey: show.Key, Title: "Le début", Type: core.TypeEpisode, ContentRating: "TV-PG", Season: 1, Episode: 1}
	adultEpisode := core.Title{Key: core.EpisodeKey(5, 1, 2), ShowKey: show.Key, Type: core.TypeEpisode, ContentRating: "TV-MA", Season: 1, Episode: 2}
	outside := core.Title{Key: core.MovieKey(3), Title: "Autre film", Type: core.TypeMovie, ContentRating: "G",
		Genres: []string{"Action"}, Studios: []string{"Studio Caché"}}
	local := core.Title{Key: core.LocalKey(100), Title: "Film local", Type: core.TypeMovie}
	wrongLocal := core.Title{Key: core.LocalKey(101), Title: "Identifiant local de série", Type: core.TypeShow}
	titles := make(map[core.RatingKey]core.Title)
	for _, title := range []core.Title{movie, adult, show, season, episode, adultEpisode, outside, local, wrongLocal} {
		titles[title.Key] = title
	}
	get := func(_ context.Context, key core.RatingKey) (*core.Title, error) {
		if title, ok := titles[key]; ok {
			return &title, nil
		}
		return nil, core.ErrNotFound
	}
	mixed := frontend.PSEThemeRow{ID: "mixed", Title: "Sélection"}
	frontend.SetPSEThemes(jellyfinThemeViewsSource{
		parts: []frontend.PSETheme{
			{Section: 5101, Title: "Découverte · Films", LibraryTitle: "Découverte", Type: core.TypeMovie,
				Rows: []frontend.PSEThemeRow{mixed, {ID: "private", Title: "Privé"}, {ID: "wrongType", Title: "Hors type"}, {ID: "duplicate", Title: "Autre sélection", LangFree: true}}},
			{Section: 5201, Title: "Découverte · Séries", LibraryTitle: "Découverte", Type: core.TypeShow, Rows: []frontend.PSEThemeRow{mixed}},
			{Section: 5102, Title: "Autre bibliothèque", Type: core.TypeMovie, Rows: []frontend.PSEThemeRow{{ID: "outside", Title: "Autre sélection"}}},
			{Section: 5103, Title: "Bibliothèque privée", Type: core.TypeMovie,
				VisibleTo: func(u *core.User) bool { return u.IsOwner }, Rows: []frontend.PSEThemeRow{{ID: "private", Title: "Privé"}}},
			{Section: 5104, Title: "Bibliothèque locale", Type: core.TypeMovie,
				VisibleTo: func(u *core.User) bool { return u.ID == 18 }, Rows: []frontend.PSEThemeRow{{ID: "local", Title: "Rangée locale"}}},
		},
		keys: map[string][]core.RatingKey{
			"mixed": {movie.Key, adult.Key, show.Key}, "duplicate": {movie.Key},
			"private": {outside.Key}, "outside": {outside.Key}, "wrongType": {show.Key},
			"local": {local.Key, wrongLocal.Key, core.PluginKey(1)},
		},
	})
	t.Cleanup(func() { frontend.SetPSEThemes(nil) })
	libraries := []Library{{ID: 1, Name: "Films", Type: core.TypeMovie}, {ID: 2, Name: "Séries", Type: core.TypeShow}}
	return &Service{d: ServiceDeps{
		Protocol: "jellyfin", Libraries: libraries, Visibility: BindVisibility(nil, get, nil, nil, libraries),
		Active: func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Get:    func(ctx context.Context, _ *core.User, key core.RatingKey) (*core.Title, error) { return get(ctx, key) },
		CachedGetMany: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			results := make(map[core.RatingKey]core.Title, len(keys))
			for _, key := range keys {
				if title, ok := titles[key]; ok {
					results[key] = title
				}
			}
			return results, nil
		},
		Children: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			return []core.Title{season}, nil
		},
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			return []core.Title{episode, adultEpisode}, nil
		},
		State: serviceState{},
	}}
}

func TestPSEJellyfinThemeViewsKeepExplicitAndExistingLibraryRights(t *testing.T) {
	viewer := &core.User{ID: 17, AllowedLibraries: []core.LibraryID{5100, 5201, 5103}}
	s := jellyfinThemeViewsFixture(t, viewer)
	ctx := t.Context()
	views, err := s.Views(ctx, viewer)
	if err != nil || len(views) != 1 || views[0].ID != "5101" || views[0].Name != "Découverte" || views[0].Type != "mixed" {
		t.Fatalf("mixed views for explicit selection: %+v, %v", views, err)
	}
	rows, err := s.PSEThemeRowViews(ctx, viewer, "5101")
	if err != nil || len(rows) != 2 || rows[0].ID != pseJellyfinThemeRowID("5101", "mixed") || rows[0].Type != "mixed" || rows[1].Name != "Autre sélection" {
		t.Fatalf("rows of mixed theme: %+v, %v", rows, err)
	}
	if detail, err := s.PSEThemeRowView(ctx, viewer, rows[0].ID); err != nil || detail != rows[0] {
		t.Fatalf("row detail: %+v, %v", detail, err)
	}
	privateRow := pseJellyfinThemeRowID("5101", "private")
	if _, err = s.PSEThemeRowView(ctx, viewer, privateRow); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("private row detail: %v", err)
	}
	for _, id := range []string{privateRow, pseJellyfinThemeRowID("5101", "wrongType"), "5102", "5103", "5201"} {
		if _, err := s.LibraryItems(ctx, viewer, core.Device{}, id, frontend.ItemQuery{}); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("unexpected access to view %s: %v", id, err)
		}
	}

	viewer.AllowedLibraries = []core.LibraryID{1}
	views, err = s.Views(ctx, viewer)
	if err != nil || len(views) != 3 || views[0].ID != "1" || views[1].ID != "5101" || views[1].Type != core.TypeMovie || views[2].ID != "5102" {
		t.Fatalf("legacy viewer keeps only the allowed base kind: %+v, %v", views, err)
	}
	if detail, err := s.PSEThemeRowView(ctx, viewer, rows[0].ID); err != nil || detail.Type != core.TypeMovie {
		t.Fatalf("same row ID with movie portion only: %+v, %v", detail, err)
	}

	s.d.Protocol = "emby"
	views, err = s.Views(ctx, viewer)
	if err != nil || len(views) != 1 || views[0].ID != "1" {
		t.Fatalf("Emby views changed: %+v, %v", views, err)
	}
}

func TestPSEJellyfinThemeBrowseFiltersAndPaginatesAuthorizedTitles(t *testing.T) {
	viewer := &core.User{ID: 17, AllowedLibraries: []core.LibraryID{5100, 5101}, MaxContentRating: "PG"}
	s := jellyfinThemeViewsFixture(t, viewer)
	ctx := t.Context()
	movie, show := core.MovieKey(1), core.ShowKey(5)
	season, episode := core.SeasonKey(5, 1), core.EpisodeKey(5, 1, 1)
	for _, tc := range []struct {
		name, id string
		query    frontend.ItemQuery
		want     []core.RatingKey
		total    int
	}{
		{"unified theme", "5101", frontend.ItemQuery{}, []core.RatingKey{movie, show}, 2},
		{"global browsing for restricted viewer", "", frontend.ItemQuery{}, []core.RatingKey{movie, show}, 2},
		{"pagination", "5101", frontend.ItemQuery{Offset: 1, Limit: 1}, []core.RatingKey{show}, 2},
		{"large limit", "5101", frontend.ItemQuery{Offset: 1, Limit: math.MaxInt}, []core.RatingKey{show}, 2},
		{"search within row", pseJellyfinThemeRowID("5101", "mixed"), frontend.ItemQuery{Filter: core.Filter{Query: "film"}}, []core.RatingKey{movie}, 1},
		{"filtered recursive view", "5101", frontend.ItemQuery{Recursive: true, Types: []core.MediaType{core.TypeSeason, core.TypeEpisode}}, []core.RatingKey{season, episode}, 2},
		{"mixed direct IDs", "5101", frontend.ItemQuery{Recursive: true, Filter: core.Filter{IncludeKeys: []core.RatingKey{episode, core.MovieKey(3)}}}, []core.RatingKey{episode}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.LibraryItems(ctx, viewer, core.Device{}, tc.id, tc.query)
			var keys []core.RatingKey
			for _, title := range page.Items {
				keys = append(keys, title.Key)
			}
			if err != nil || page.Total != tc.total || !slices.Equal(keys, tc.want) {
				t.Fatalf("page %v / %d, want %v / %d, error: %v", keys, page.Total, tc.want, tc.total, err)
			}
		})
	}
	viewer.AllowedLibraries = []core.LibraryID{5100}
	if _, err := s.LibraryItems(ctx, viewer, core.Device{}, "5101", frontend.ItemQuery{}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("theme still browsable after a viewer loses access: %v", err)
	}
}

func TestPSEJellyfinThemeFacetsStayScopedToVisibleTitles(t *testing.T) {
	viewer := &core.User{ID: 17, AllowedLibraries: []core.LibraryID{5100, 5101}, MaxContentRating: "PG"}
	s := jellyfinThemeViewsFixture(t, viewer)
	s.d.PluginLibraries = &fakePluginLibraries{libs: []core.Library{
		{ID: 1, Source: core.LibraryBuiltin, MediaType: core.TypeMovie},
		{ID: 2, Source: core.LibraryBuiltin, MediaType: core.TypeShow},
	}}
	s.d.PageExclusions = func(context.Context, *core.User, core.MediaType) LibraryPageExclusions {
		return LibraryPageExclusions{AvailabilityInSQL: true}
	}
	s.d.FacetFilter = func(context.Context, core.Filter, string) ([]core.FacetValue, error) {
		t.Fatal("a themed view called the global facet index")
		return nil, nil
	}
	s.d.FacetPage = func(context.Context, core.Filter, string, int, int) ([]core.FacetValue, int, bool, error) {
		t.Fatal("a themed view called the global paged facet index")
		return nil, 0, false, nil
	}
	s.d.StudioByFacetID = func(context.Context, string) (string, bool, error) {
		t.Fatal("a themed view called the global studio vocabulary")
		return "", false, nil
	}
	sameFacet := func(a, b core.FacetValue) bool {
		return a.ID == b.ID && a.Key == b.Key && a.Title == b.Title && a.Count == b.Count
	}
	ctx := t.Context()
	for _, viewID := range []string{"5101", pseJellyfinThemeRowID("5101", "mixed")} {
		genres, err := s.Facets(ctx, viewer, core.Device{}, viewID, "genre")
		want := []core.FacetValue{
			{ID: core.FacetID("genre", "Animation"), Key: "Animation", Title: "Animation", Count: 2},
			{ID: core.FacetID("genre", "Aventure"), Key: "Aventure", Title: "Aventure", Count: 1},
		}
		if err != nil || !slices.EqualFunc(genres, want, sameFacet) {
			t.Fatalf("view %s: genres = %+v, err = %v", viewID, genres, err)
		}
		studios, total, err := s.FacetPage(ctx, viewer, core.Device{}, viewID, "studio", 1, 1)
		if err != nil || total != 2 || len(studios) != 1 || studios[0].Title != "Studio Bruyère" {
			t.Fatalf("view %s: studios = %+v, total = %d, err = %v", viewID, studios, total, err)
		}
		studio, err := s.ResolveFacet(ctx, viewer, core.Device{}, viewID, "studio", core.FacetID("studio", "Studio Arnica"))
		if err != nil || studio.Key != "Studio Arnica" {
			t.Fatalf("view %s: studio = %+v, err = %v", viewID, studio, err)
		}
		if _, err = s.ResolveFacet(ctx, viewer, core.Device{}, viewID, "studio", core.FacetID("studio", "Studio Caché")); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("view %s resolved a studio outside the theme: %v", viewID, err)
		}
		if _, err = s.ResolveFacet(ctx, viewer, core.Device{}, viewID, "genre", core.FacetID("genre", "Horreur")); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("view %s resolved a genre from a title behind parental filters: %v", viewID, err)
		}
		if filters, err := s.FilterFacets(ctx, viewer, core.Device{}, viewID, "genre"); err != nil || !slices.EqualFunc(filters, genres, sameFacet) {
			t.Fatalf("view %s: genre filters = %+v, err = %v", viewID, filters, err)
		}
	}
	for _, denied := range []string{"5102", pseJellyfinThemeRowID("5101", "private")} {
		if _, _, err := s.FacetPage(ctx, viewer, core.Device{}, denied, "studio", 0, 5); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("view %s exposed studio facets: %v", denied, err)
		}
		if _, err := s.ResolveFacet(ctx, viewer, core.Device{}, denied, "studio", core.FacetID("studio", "Studio Arnica")); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("view %s resolved a studio: %v", denied, err)
		}
	}
}

func TestPSEJellyfinThemeIncludesAuthorizedLocalMovies(t *testing.T) {
	viewer := &core.User{ID: 18, AllowedLibraries: []core.LibraryID{5100, 5104}}
	s := jellyfinThemeViewsFixture(t, viewer)
	ctx := t.Context()
	rows, err := s.PSEThemeRowViews(ctx, viewer, "5104")
	if err != nil || len(rows) != 1 || rows[0].Type != core.TypeMovie {
		t.Fatalf("local movie row: %+v, err = %v", rows, err)
	}
	for _, id := range []string{"5104", rows[0].ID} {
		page, err := s.LibraryItems(ctx, viewer, core.Device{}, id, frontend.ItemQuery{})
		if err != nil || len(page.Items) != 1 || page.Items[0].Key != core.LocalKey(100) {
			t.Errorf("local movie in view %s: %+v, err = %v", id, page, err)
		}
	}
	viewer.MaxContentRating = "PG"
	page, err := s.LibraryItems(ctx, viewer, core.Device{}, "5104", frontend.ItemQuery{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("unrated local movie exposed to child: %+v, err = %v", page, err)
	}
}

func TestPSEJellyfinThemeDiscoveryUsesRowLanguageScope(t *testing.T) {
	viewer := &core.User{ID: 17, AllowedLibraries: []core.LibraryID{5100, 5101}, MaxContentRating: "PG"}
	s := jellyfinThemeViewsFixture(t, viewer)
	movie, show := core.MovieKey(1), core.ShowKey(5)
	var normalCalls, allLanguageCalls int
	s.d.PSEThemeDiscoveryKeys = func(ctx context.Context, keys []core.RatingKey) ([]core.RatingKey, error) {
		if got := core.CatalogViewerID(ctx); got != viewer.ID {
			t.Errorf("discovery viewer ID = %d, want %d", got, viewer.ID)
		}
		if core.PSELangFree(ctx) {
			allLanguageCalls++
			if !slices.Equal(keys, []core.RatingKey{movie}) {
				t.Errorf("all-language keys = %v, want %v", keys, movie)
			}
			return keys, nil
		}
		normalCalls++
		if !slices.Contains(keys, show) {
			t.Errorf("normal keys = %v, missing show %v", keys, show)
		}
		return []core.RatingKey{show}, nil
	}
	ctx := t.Context()
	page, err := s.LibraryItems(ctx, viewer, core.Device{}, "5101", frontend.ItemQuery{})
	if err != nil || len(page.Items) != 2 || page.Items[0].Key != movie || page.Items[1].Key != show {
		t.Fatalf("library with language exception = %+v, %v", page, err)
	}
	page, err = s.LibraryItems(ctx, viewer, core.Device{}, pseJellyfinThemeRowID("5101", "mixed"), frontend.ItemQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Key != show {
		t.Fatalf("row without language exception = %+v, %v", page, err)
	}
	if normalCalls != 2 || allLanguageCalls != 1 {
		t.Errorf("discovery calls normal = %d, all languages = %d", normalCalls, allLanguageCalls)
	}
}
