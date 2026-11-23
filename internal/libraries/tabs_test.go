package libraries

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestLibraryTabsSetValidateAndReset(t *testing.T) {
	s, _ := openService(t)
	ctx := context.Background()

	movies, err := s.Get(ctx, core.LibraryMovies)
	if err != nil {
		t.Fatal(err)
	}
	if movies.TabsCustomized || len(movies.Tabs) != 0 {
		t.Fatalf("initial tabs = %+v (customized=%v), want empty", movies.Tabs, movies.TabsCustomized)
	}

	searchOn := true
	var matchStyle core.TabFilter
	if err := json.Unmarshal([]byte(`{
		"match": "all",
		"rules": [{"field": "genre", "op": "in", "value": ["Science Fiction"]}],
		"exclude": [{"field": "year", "op": "lte", "value": 1979}]
	}`), &matchStyle); err != nil {
		t.Fatalf("unmarshal match-style TabFilter: %v", err)
	}

	var groupStyle core.TabFilter
	if err := json.Unmarshal([]byte(`{
		"groups": [{"action": "show", "mediaType": "movie", "match": "all", "rules": [{"field": "rating", "op": "gte", "value": 7.5}]}],
		"visibilityDefault": "hide"
	}`), &groupStyle); err != nil {
		t.Fatalf("unmarshal group-style TabFilter: %v", err)
	}

	custom := []core.LibraryTab{
		{Key: "recommended", Label: "For You", Kind: core.TabRecommended},
		{Key: "library", Label: "All Movies", Kind: core.TabLibrary, Sort: "added", Direction: "desc", Search: &searchOn, SearchPlaceholder: "Search movies"},
		{Key: "scifi-80s", Label: "80s+ Sci-Fi", Kind: core.TabView, Sort: "rating", Direction: "desc", Search: &searchOn, Filters: &matchStyle},
		{Key: "acclaimed", Label: "Acclaimed", Kind: core.TabView, Sort: "year", Direction: "desc", Filters: &groupStyle},
		{Key: "genres", Label: "Genres", Kind: core.TabCollections, Facet: "genres", Order: "count", Search: &searchOn},
		{Key: "directors", Label: "Directors", Kind: core.TabPeople, Facet: "directors", Order: "count"},
	}

	saved, err := s.SetTabs(ctx, core.LibraryMovies, custom, nil)
	if err != nil {
		t.Fatalf("SetTabs: %v", err)
	}
	if len(saved) != 6 {
		t.Fatalf("saved len = %d, want 6", len(saved))
	}
	if saved[4].Facet != "genre" || saved[5].Facet != "director" {
		t.Fatalf("facets not canonicalised: got %q and %q", saved[4].Facet, saved[5].Facet)
	}

	got, err := s.Get(ctx, core.LibraryMovies)
	if err != nil {
		t.Fatal(err)
	}
	if !got.TabsCustomized || len(got.Tabs) != 6 || got.Tabs[2].Key != "scifi-80s" || got.Tabs[2].Filters == nil {
		t.Fatalf("round-trip tabs = %+v (customized=%v)", got.Tabs, got.TabsCustomized)
	}

	if err := s.ResetTabs(ctx, core.LibraryMovies); err != nil {
		t.Fatalf("ResetTabs: %v", err)
	}
	afterReset, err := s.Get(ctx, core.LibraryMovies)
	if err != nil {
		t.Fatal(err)
	}
	if afterReset.TabsCustomized || len(afterReset.Tabs) != 0 {
		t.Fatalf("after ResetTabs = %+v (customized=%v), want empty", afterReset.Tabs, afterReset.TabsCustomized)
	}
}

func TestLibraryTabsValidationRejectsInvalidConfigs(t *testing.T) {
	s, _ := openService(t)
	ctx := context.Background()
	searchOn := true

	badCases := []struct {
		name string
		tabs []core.LibraryTab
	}{
		{name: "empty list", tabs: nil},
		{
			name: "all hidden",
			tabs: []core.LibraryTab{{Key: "library", Label: "All", Kind: core.TabLibrary, Hidden: true}},
		},
		{
			name: "duplicate keys",
			tabs: []core.LibraryTab{
				{Key: "picks", Label: "Picks", Kind: core.TabLibrary},
				{Key: "picks", Label: "Picks 2", Kind: core.TabView},
			},
		},
		{
			name: "unknown kind",
			tabs: []core.LibraryTab{{Key: "bad", Label: "Bad", Kind: "charts"}},
		},
		{
			name: "recommended with search enabled",
			tabs: []core.LibraryTab{{Key: "recommended", Label: "Recommended", Kind: core.TabRecommended, Search: &searchOn}},
		},
		{
			name: "collections with unknown catalogue facet",
			tabs: []core.LibraryTab{{Key: "cols", Label: "Collections", Kind: core.TabCollections, Facet: "nonexistent"}},
		},
		{
			name: "people without facet on catalogue library",
			tabs: []core.LibraryTab{{Key: "people", Label: "People", Kind: core.TabPeople}},
		},
		{
			name: "view tab with facet",
			tabs: []core.LibraryTab{{Key: "v", Label: "View", Kind: core.TabView, Facet: "genre"}},
		},
		{
			name: "collections tab with filters",
			tabs: []core.LibraryTab{{
				Key: "cols", Label: "Genres", Kind: core.TabCollections, Facet: "genre",
				Filters: &core.TabFilter{Rules: []core.CollectionRule{{Field: "year", Op: "gte", Value: json.RawMessage(`2000`)}}},
			}},
		},
		{
			name: "invalid filter field",
			tabs: []core.LibraryTab{{
				Key: "v", Label: "Bad Filter", Kind: core.TabView,
				Filters: &core.TabFilter{Rules: []core.CollectionRule{{Field: "not_a_field", Op: "is", Value: json.RawMessage(`"x"`)}}},
			}},
		},
	}

	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.SetTabs(ctx, core.LibraryMovies, tc.tabs, nil); !errors.Is(err, ErrInvalid) {
				t.Fatalf("SetTabs(%s) err = %v, want ErrInvalid", tc.name, err)
			}
		})
	}
}

func TestPluginLibraryTabsValidation(t *testing.T) {
	s, _ := openService(t)
	ctx := context.Background()

	plugin, err := s.UpsertPluginLibrary(ctx, "audiobooks", "book", PluginLibraryDecl{Name: "Audiobooks"})
	if err != nil {
		t.Fatal(err)
	}
	schema := &PluginTabSchema{
		CollectionFacets: map[string]bool{"series": true, "genre": true},
		PeopleFacets:     map[string]bool{"narrator": true, "author": true},
		Sorts:            map[string]bool{"title": true, "added": true, "year": true},
	}

	valid := []core.LibraryTab{
		{Key: "recommended", Label: "Recommended", Kind: core.TabRecommended},
		{Key: "library", Label: "All Books", Kind: core.TabLibrary, Sort: "added", Direction: "desc"},
		{Key: "series", Label: "Series", Kind: core.TabCollections, Facet: "series", Order: "name"},
		{Key: "narrators", Label: "Narrators", Kind: core.TabPeople, Facet: "narrator", Order: "count"},
	}
	if _, err := s.SetTabs(ctx, plugin.ID, valid, schema); err != nil {
		t.Fatalf("valid plugin tabs rejected: %v", err)
	}

	if _, err := s.SetTabs(ctx, plugin.ID, []core.LibraryTab{
		{Key: "curated", Label: "Collections", Kind: core.TabCollections},
	}, schema); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty-facet collections tab on plugin library, got %v", err)
	}
	if _, err := s.SetTabs(ctx, plugin.ID, []core.LibraryTab{
		{Key: "bad", Label: "Studios", Kind: core.TabCollections, Facet: "studio"},
	}, schema); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for undeclared plugin facet, got %v", err)
	}

	// Live tab allowed on plugin library (with optional card and search).
	withLive := append(valid, core.LibraryTab{
		Key: "live", Label: "Live", Kind: core.TabLive, Card: core.CardLandscape, SearchPlaceholder: "Search live channels",
	})
	savedLive, err := s.SetTabs(ctx, plugin.ID, withLive, schema)
	if err != nil {
		t.Fatalf("valid plugin live tab rejected: %v", err)
	}
	if savedLive[len(savedLive)-1].Kind != core.TabLive || savedLive[len(savedLive)-1].Card != core.CardLandscape {
		t.Fatalf("saved live tab = %+v", savedLive[len(savedLive)-1])
	}

	// Live tab rejected on non-plugin libraries and when given invalid card/facet/sort/filters.
	if _, err := s.SetTabs(ctx, core.LibraryMovies, []core.LibraryTab{
		{Key: "live", Label: "Live", Kind: core.TabLive},
	}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for live tab on builtin library, got %v", err)
	}
	if _, err := s.SetTabs(ctx, plugin.ID, []core.LibraryTab{
		{Key: "live", Label: "Live", Kind: core.TabLive, Card: "banner"},
	}, schema); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for bad card on live tab, got %v", err)
	}
	if _, err := s.SetTabs(ctx, plugin.ID, []core.LibraryTab{
		{Key: "live", Label: "Live", Kind: core.TabLive, Sort: "title"},
	}, schema); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for sort on live tab, got %v", err)
	}
}

func TestLibraryTabs50TabCapLayoutAndLastViewedSort(t *testing.T) {
	s, _ := openService(t)
	ctx := context.Background()

	// 50 tabs accepted, 51 rejected.
	fifty := make([]core.LibraryTab, MaxLibraryTabs)
	for i := range fifty {
		fifty[i] = core.LibraryTab{
			Key:    "tab-" + string(rune('a'+i/26)) + string(rune('a'+i%26)),
			Label:  "Tab",
			Kind:   core.TabView,
			Layout: core.TabLayoutGrid,
		}
	}
	saved, err := s.SetTabs(ctx, core.LibraryMovies, fifty, nil)
	if err != nil {
		t.Fatalf("50 tabs should succeed, got %v", err)
	}
	if len(saved) != 50 {
		t.Fatalf("saved len = %d, want 50", len(saved))
	}

	fiftyOne := append(append([]core.LibraryTab(nil), fifty...), core.LibraryTab{
		Key: "tab-extra", Label: "Extra", Kind: core.TabView,
	})
	if _, err := s.SetTabs(ctx, core.LibraryMovies, fiftyOne, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("51 tabs err = %v, want ErrInvalid", err)
	}

	// Layout + lastViewed sort (including aliases) normalize cleanly on catalogue and plugin libraries.
	histFilter := &core.TabFilter{
		Match: "all",
		Rules: []core.CollectionRule{{Field: "viewer", Op: "is", Value: json.RawMessage(`{"history":true}`)}},
	}
	viewTabs, err := s.SetTabs(ctx, core.LibraryMovies, []core.LibraryTab{
		{Key: "history", Label: "History", Kind: core.TabView, Layout: "timeline", Sort: "played", Direction: "desc", Filters: histFilter},
		{Key: "recent", Label: "Recent", Kind: core.TabView, Layout: "list", Sort: "last_viewed"},
	}, nil)
	if err != nil {
		t.Fatalf("SetTabs catalogue history: %v", err)
	}
	if viewTabs[0].Layout != core.TabLayoutTimeline || viewTabs[0].Sort != "lastViewed" || viewTabs[1].Layout != core.TabLayoutList || viewTabs[1].Sort != "lastViewed" {
		t.Fatalf("normalized view tabs = %+v", viewTabs)
	}

	// Reject bad layout or layout on non-view tabs.
	if _, err := s.SetTabs(ctx, core.LibraryMovies, []core.LibraryTab{
		{Key: "bad", Label: "Bad", Kind: core.TabView, Layout: "carousel"},
	}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid layout err = %v, want ErrInvalid", err)
	}
	if _, err := s.SetTabs(ctx, core.LibraryMovies, []core.LibraryTab{
		{Key: "lib", Label: "Library", Kind: core.TabLibrary, Layout: "list"},
	}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("layout on library tab err = %v, want ErrInvalid", err)
	}

	// Plugin library accepts lastViewed sort even if manifest browse.sorts didn't list it.
	plugin, err := s.UpsertPluginLibrary(ctx, "docs", "doc", PluginLibraryDecl{Name: "Documentaries"})
	if err != nil {
		t.Fatal(err)
	}
	pluginSchema := &PluginTabSchema{
		CollectionFacets: map[string]bool{"topic": true},
		PeopleFacets:     map[string]bool{},
		Sorts:            map[string]bool{"title": true},
	}
	savedPlugin, err := s.SetTabs(ctx, plugin.ID, []core.LibraryTab{
		{Key: "history", Label: "History", Kind: core.TabView, Layout: "timeline", Sort: "lastViewed", Direction: "desc", Filters: histFilter},
	}, pluginSchema)
	if err != nil {
		t.Fatalf("plugin history tab rejected: %v", err)
	}
	if savedPlugin[0].Layout != core.TabLayoutTimeline || savedPlugin[0].Sort != "lastViewed" {
		t.Fatalf("saved plugin history tab = %+v", savedPlugin[0])
	}
}
