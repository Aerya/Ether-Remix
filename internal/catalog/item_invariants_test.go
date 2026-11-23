package catalog

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestBrowseCheapTotalTalliedAndUntallied(t *testing.T) {
	c, err := New(store.OpenTest(t), nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	rel := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	movies := []core.Title{
		{Key: core.MovieKey(1), Type: core.TypeMovie, Title: `"Pimpernel" Smith`, Year: 1941, Genres: []string{"Drama"}, Released: rel},
		{Key: core.MovieKey(2), Type: core.TypeMovie, Title: `"Sr."`, Year: 2022, Genres: []string{"Documentary"}, Released: rel},
		{Key: core.MovieKey(3), Type: core.TypeMovie, Title: "#1 Cheerleader Camp", Year: 2010, Genres: []string{"Comedy"}, Released: rel},
		{Key: core.MovieKey(4), Type: core.TypeMovie, Title: "#Alive", Year: 2020, Genres: []string{"Drama"}, Released: rel},
		{Key: core.MovieKey(5), Type: core.TypeMovie, Title: "The Matrix", Year: 1999, Genres: []string{"Drama"}, Released: rel},
	}
	for _, m := range movies {
		if err := c.UpsertTitle(ctx, m, true); err != nil {
			t.Fatal(err)
		}
	}

	// Whole-library browse with NoTotal+CheapTotal uses the tally and reports TotalExact=true.
	p1, err := c.Browse(ctx, core.BrowseQuery{
		Filter:     core.Filter{Type: core.TypeMovie},
		Sort:       core.SortTitle,
		Limit:      2,
		NoTotal:    true,
		CheapTotal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p1.TotalExact || p1.Total != 5 {
		t.Fatalf("page 1 Total=%d TotalExact=%v, want 5 / true", p1.Total, p1.TotalExact)
	}
	if len(p1.Items) != 2 || p1.Items[0].Title != "#1 Cheerleader Camp" || p1.Items[1].Title != "#Alive" {
		t.Fatalf("unexpected page 1 order: %q, %q", p1.Items[0].Title, p1.Items[1].Title)
	}

	// Page 2 with CheapTotal keeps exact total 5 (never shifted by offset).
	p2, err := c.Browse(ctx, core.BrowseQuery{
		Filter:     core.Filter{Type: core.TypeMovie},
		Sort:       core.SortTitle,
		Offset:     2,
		Limit:      2,
		NoTotal:    true,
		CheapTotal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p2.TotalExact || p2.Total != 5 {
		t.Fatalf("page 2 Total=%d TotalExact=%v, want 5 / true", p2.Total, p2.TotalExact)
	}
	if len(p2.Items) != 2 || p2.Items[0].Title != "The Matrix" || p2.Items[1].Title != `"Pimpernel" Smith` {
		t.Fatalf("unexpected page 2 order: %q, %q", p2.Items[0].Title, p2.Items[1].Title)
	}

	// Single-facet genre browse is tallied -> exact total 3.
	genrePage, err := c.Browse(ctx, core.BrowseQuery{
		Filter:     core.Filter{Type: core.TypeMovie, Genres: []string{"Drama"}},
		Sort:       core.SortTitle,
		Limit:      10,
		NoTotal:    true,
		CheapTotal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !genrePage.TotalExact || genrePage.Total != 3 {
		t.Fatalf("genre CheapTotal = %d (exact=%v), want 3 / true", genrePage.Total, genrePage.TotalExact)
	}

	// Un-tallied multi-dimension filter (genre + year) omits exact total rather than scanning.
	untallied, err := c.Browse(ctx, core.BrowseQuery{
		Filter:     core.Filter{Type: core.TypeMovie, Genres: []string{"Drama"}, Years: []int{2020}},
		Sort:       core.SortTitle,
		Limit:      10,
		NoTotal:    true,
		CheapTotal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if untallied.TotalExact {
		t.Fatalf("expected un-tallied genre+year CheapTotal to have TotalExact=false, got total=%d", untallied.Total)
	}

	// Plain NoTotal without CheapTotal never reports TotalExact.
	noTotal, err := c.Browse(ctx, core.BrowseQuery{
		Filter:  core.Filter{Type: core.TypeMovie},
		Sort:    core.SortTitle,
		Limit:   2,
		NoTotal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if noTotal.TotalExact {
		t.Fatalf("expected NoTotal without CheapTotal to have TotalExact=false")
	}
}

func TestCertificationsPersistAndInherit(t *testing.T) {
	c, err := New(store.OpenTest(t), nil, config.Catalog{Region: "US"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	rel := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	showKey := core.ShowKey(100)
	if err := c.UpsertTitle(ctx, core.Title{
		Key:            showKey,
		Type:           core.TypeShow,
		Title:          "Multi Cert Show",
		ContentRating:  "TV-14",
		ContentRatings: map[string]string{"US": "TV-14", "GB": "15", "DE": "12"},
		Released:       rel,
	}, true); err != nil {
		t.Fatal(err)
	}
	epKey := core.EpisodeKey(100, 1, 1)
	if err := c.UpsertTitle(ctx, core.Title{
		Key:      epKey,
		ShowKey:  showKey,
		Type:     core.TypeEpisode,
		Season:   1,
		Episode:  1,
		Title:    "Pilot",
		Released: rel,
	}, false); err != nil {
		t.Fatal(err)
	}

	gotShow, err := c.CachedGet(ctx, showKey)
	if err != nil {
		t.Fatal(err)
	}
	if gotShow.ContentRatings["GB"] != "15" || gotShow.ContentRatings["DE"] != "12" || gotShow.ContentRatings["US"] != "TV-14" {
		t.Fatalf("show ContentRatings = %#v", gotShow.ContentRatings)
	}
	gotEp, err := c.CachedGet(ctx, epKey)
	if err != nil {
		t.Fatal(err)
	}
	if gotEp.ContentRatings["GB"] != "15" || gotEp.ContentRatings["US"] != "TV-14" {
		t.Fatalf("episode inherited ContentRatings = %#v", gotEp.ContentRatings)
	}

	// Provider certification merges US into existing foreign certifications.
	movieKey := core.MovieKey(200)
	if err := c.UpsertTitle(ctx, core.Title{
		Key:            movieKey,
		Type:           core.TypeMovie,
		Title:          "Foreign Movie",
		ContentRating:  "de/12",
		ContentRatings: map[string]string{"DE": "12", "GB": "12A"},
		Released:       rel,
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyProviderCertification(ctx, movieKey, "PG-13", "omdb"); err != nil {
		t.Fatal(err)
	}
	gotMovie, err := c.CachedGet(ctx, movieKey)
	if err != nil {
		t.Fatal(err)
	}
	if gotMovie.ContentRatings["US"] != "PG-13" || gotMovie.ContentRatings["GB"] != "12A" || gotMovie.ContentRatings["DE"] != "12" {
		t.Fatalf("provider merged ContentRatings = %#v", gotMovie.ContentRatings)
	}
}
