package catalog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestLibraryFacetCollectionsPeopleAndTabBrowse(t *testing.T) {
	db := store.OpenTest(t)
	ctx := t.Context()

	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	relOld := time.Date(1999, 3, 31, 0, 0, 0, 0, time.UTC)
	relNew := time.Date(2021, 10, 22, 0, 0, 0, 0, time.UTC)
	relMid := time.Date(2014, 11, 7, 0, 0, 0, 0, time.UTC)

	titles := []core.Title{
		{
			Key: core.MovieKey(1), Type: core.TypeMovie, Title: "The Matrix", Year: 1999,
			Released: relOld, AddedAt: relOld, Rating: 8.7,
			Genres: []string{"Science Fiction", "Action"}, Studios: []string{"Warner Bros."},
			Countries: []string{"US"}, CollectionID: 234, CollectionName: "The Matrix Collection",
			Cast:      []core.Person{{TMDB: 6384, Name: "Keanu Reeves"}, {TMDB: 2975, Name: "Laurence Fishburne"}},
			Directors: []core.Person{{TMDB: 905, Name: "Lana Wachowski"}},
		},
		{
			Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Dune", Year: 2021,
			Released: relNew, AddedAt: relNew, Rating: 8.0,
			Genres: []string{"Science Fiction", "Adventure"}, Studios: []string{"Warner Bros.", "Legendary Pictures"},
			Countries: []string{"US", "CA"}, CollectionID: 726871, CollectionName: "Dune Collection",
			Cast:      []core.Person{{TMDB: 1190668, Name: "Timothée Chalamet"}},
			Directors: []core.Person{{TMDB: 137427, Name: "Denis Villeneuve"}},
		},
		{
			Key: core.MovieKey(3), Type: core.TypeMovie, Title: "Amélie", Year: 2001,
			Released: relMid, AddedAt: relMid, Rating: 8.3,
			Genres: []string{"Comedy", "Romance"}, Studios: []string{"Claudie Ossard Productions"},
			Countries: []string{"FR"},
			Cast:      []core.Person{{TMDB: 2405, Name: "Audrey Tautou"}},
			Directors: []core.Person{{TMDB: 2404, Name: "Jean-Pierre Jeunet"}},
		},
	}
	for _, title := range titles {
		if err := c.upsert(ctx, title, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
	}

	movieFilter := core.Filter{Type: core.TypeMovie, LocalOnly: true}

	// 1. Genre facet collections ordered by count -> Science Fiction (2) first, with Dune as NewestItem.
	genres, err := c.LibraryFacetCollections(ctx, core.CatalogFacetCollectionsQuery{
		Filter: movieFilter,
		Facet:  "genre",
		Order:  "count",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("LibraryFacetCollections(genre): %v", err)
	}
	if len(genres.Items) == 0 || genres.Items[0].Name != "Science Fiction" || genres.Items[0].ItemCount != 2 {
		t.Fatalf("unexpected genre buckets: %+v", genres.Items)
	}
	if genres.Items[0].NewestItem == nil || genres.Items[0].NewestItem.Key != core.MovieKey(2) {
		t.Fatalf("expected Dune as NewestItem for Science Fiction, got %+v", genres.Items[0].NewestItem)
	}

	// 2. Studio, decade, year, country, and franchise facets.
	for _, tc := range []struct {
		facet    string
		wantName string
		wantCnt  int
	}{
		{"studio", "Warner Bros.", 2},
		{"decade", "2020s", 1},
		{"year", "1999", 1},
		{"country", "US", 2},
		{"franchise", "Dune Collection", 1},
	} {
		page, err := c.LibraryFacetCollections(ctx, core.CatalogFacetCollectionsQuery{
			Filter: movieFilter,
			Facet:  tc.facet,
			Order:  "count",
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("LibraryFacetCollections(%s): %v", tc.facet, err)
		}
		found := false
		for _, item := range page.Items {
			if item.Name == tc.wantName && item.ItemCount == tc.wantCnt {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("facet %s missing %q (%d) in %+v", tc.facet, tc.wantName, tc.wantCnt, page.Items)
		}
	}

	// 3. Accent-folded query on people (cast: "timothee" matches "Timothée Chalamet").
	castPage, err := c.LibraryFacetPeople(ctx, core.CatalogFacetPeopleQuery{
		Filter: movieFilter,
		Facet:  "cast",
		Order:  "name",
		Query:  "timothee",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("LibraryFacetPeople(cast): %v", err)
	}
	if len(castPage.Items) != 1 || castPage.Items[0].PersonTMDB != 1190668 || castPage.Items[0].ItemCount != 1 {
		t.Fatalf("unexpected cast search result: %+v", castPage.Items)
	}

	directorsPage, err := c.LibraryFacetPeople(ctx, core.CatalogFacetPeopleQuery{
		Filter: movieFilter,
		Facet:  "director",
		Order:  "name",
		Limit:  2,
	})
	if err != nil {
		t.Fatalf("LibraryFacetPeople(director): %v", err)
	}
	if len(directorsPage.Items) != 2 || !directorsPage.More {
		t.Fatalf("expected 2 directors with More=true, got len=%d more=%v", len(directorsPage.Items), directorsPage.More)
	}

	// 4. TabFilter on Browse narrows visible items without altering canonical library rules.
	tf := core.TabFilter{
		Match: "all",
		Rules: []core.CollectionRule{
			{Field: "genre", Op: "in", Value: json.RawMessage(`["Science Fiction"]`)},
		},
		Exclude: []core.CollectionRule{
			{Field: "year", Op: "lte", Value: json.RawMessage(`1999`)},
		},
	}
	tabRules, tabFallback := tf.ToPolicy(core.TypeMovie)
	tabFilter := movieFilter
	tabFilter.TabRules = tabRules
	tabFilter.TabFallback = tabFallback

	browsed, err := c.Browse(ctx, core.BrowseQuery{Filter: tabFilter, Limit: 10})
	if err != nil {
		t.Fatalf("Browse with TabRules: %v", err)
	}
	if browsed.Total != 1 || len(browsed.Items) != 1 || browsed.Items[0].Key != core.MovieKey(2) {
		t.Fatalf("Browse with TabRules = total %d items %+v, want only Dune", browsed.Total, browsed.Items)
	}

	inTab, err := c.TitlesInFilter(ctx, tabFilter, []core.RatingKey{core.MovieKey(1), core.MovieKey(2), core.MovieKey(3)})
	if err != nil {
		t.Fatalf("TitlesInFilter with TabRules: %v", err)
	}
	if len(inTab) != 1 || !inTab[core.MovieKey(2)] {
		t.Fatalf("TitlesInFilter with TabRules = %+v, want only MovieKey(2)", inTab)
	}
}
