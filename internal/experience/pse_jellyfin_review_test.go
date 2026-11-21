package experience

import (
	"context"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestPSEJellyfinThemeOnlySearchStaysInAuthorizedThemes(t *testing.T) {
	viewer := &core.User{ID: 17, AllowedLibraries: []core.LibraryID{5100, 5101}, MaxContentRating: "PG"}
	// The fixture has no catalogue search: reaching it would panic.
	s := jellyfinThemeViewsFixture(t, viewer)
	ctx := t.Context()
	for _, tc := range []struct {
		query string
		types []core.MediaType
		want  []core.RatingKey
	}{
		{"le film", nil, []core.RatingKey{core.MovieKey(1)}},
		{"série", []core.MediaType{core.TypeShow}, []core.RatingKey{core.ShowKey(5)}},
		{"autre film", nil, nil},     // private row, owner only
		{"classification", nil, nil}, // above the parental limit
		{"film local", nil, nil},     // theme reserved for another viewer
		{"début", []core.MediaType{core.TypeEpisode}, []core.RatingKey{core.EpisodeKey(5, 1, 1)}},
	} {
		page, err := s.Search(ctx, viewer, core.Device{}, tc.query, frontend.ItemQuery{Types: tc.types, Limit: 10})
		var keys []core.RatingKey
		for _, title := range page.Page.Items {
			keys = append(keys, title.Key)
		}
		if err != nil || !slices.Equal(keys, tc.want) {
			t.Errorf("search %q: %v, want %v (error %v)", tc.query, keys, tc.want, err)
		}
	}
}

func TestPSEJellyfinRowViewsListOnlyRowsWithAllowedTitles(t *testing.T) {
	s, viewer := jellyfinThemeActivityFixture(t)
	adult := core.MovieKey(4)
	frontend.SetPSEThemes(activityThemes{
		themes: []frontend.PSETheme{{Section: 5101, Title: "Animation", Type: core.TypeMovie, Rows: []frontend.PSEThemeRow{
			{ID: "films", Title: "Films"}, {ID: "adultes", Title: "Adultes"}, {ID: "vide", Title: "Vide"},
		}}},
		rows: map[string][]core.RatingKey{"films": {core.MovieKey(1), adult}, "adultes": {adult}},
	})
	rows, err := s.PSEThemeRowViews(t.Context(), viewer, "5101")
	if err != nil || len(rows) != 1 || rows[0].Name != "Films" {
		t.Fatalf("rows %+v, error %v", rows, err)
	}
	if _, err := s.PSEThemeRowView(t.Context(), viewer, pseJellyfinThemeRowID("5101", "adultes")); err == nil {
		t.Fatal("a row without any allowed title can be opened as a view")
	}
	// A row of an authorized theme is a valid parent for the latest feed.
	items, err := s.LatestAdded(t.Context(), viewer, core.Device{}, pseJellyfinThemeRowID("5101", "films"), 10)
	if err != nil || len(items) != 1 || items[0].Title.Key != core.MovieKey(1) {
		t.Fatalf("latest of a row: %+v, error %v", items, err)
	}
}

func TestPSEJellyfinThemeRecursiveReadsChildrenOnlyWhenAsked(t *testing.T) {
	viewer := &core.User{ID: 17, AllowedLibraries: []core.LibraryID{5100, 5101}, MaxContentRating: "PG"}
	s := jellyfinThemeViewsFixture(t, viewer)
	calls := 0
	children := s.d.Children
	s.d.Children = func(ctx context.Context, v *core.User, key core.RatingKey) ([]core.Title, error) {
		calls++
		return children(ctx, v, key)
	}
	ctx := t.Context()
	if _, err := s.LibraryItems(ctx, viewer, core.Device{}, "5101", frontend.ItemQuery{Recursive: true, Types: []core.MediaType{core.TypeMovie, core.TypeShow}}); err != nil || calls != 0 {
		t.Fatalf("children read %d times for movies and series (error %v)", calls, err)
	}
	// Direct IDs read the series of the listed episodes only.
	page, err := s.LibraryItems(ctx, viewer, core.Device{}, "5101", frontend.ItemQuery{Recursive: true, Filter: core.Filter{IncludeKeys: []core.RatingKey{core.MovieKey(1)}}})
	if err != nil || calls != 0 || len(page.Items) != 1 {
		t.Fatalf("movie by ID: %+v, %d children reads, error %v", page.Items, calls, err)
	}
	page, err = s.LibraryItems(ctx, viewer, core.Device{}, "5101", frontend.ItemQuery{Recursive: true, Filter: core.Filter{IncludeKeys: []core.RatingKey{core.EpisodeKey(5, 1, 1)}}})
	if err != nil || calls == 0 || len(page.Items) != 1 || page.Items[0].Key != core.EpisodeKey(5, 1, 1) {
		t.Fatalf("episode by ID: %+v, %d children reads, error %v", page.Items, calls, err)
	}
}
