package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func testReleaseDates(country, cert string) *tmdb.ReleaseDates {
	rd := &tmdb.ReleaseDates{}
	rd.Results = append(rd.Results, struct {
		ISO3166     string                  `json:"iso_3166_1"`
		ReleaseDate []tmdb.ReleaseDateEntry `json:"release_dates"`
	}{
		ISO3166:     country,
		ReleaseDate: []tmdb.ReleaseDateEntry{{Certification: cert, Type: 3}},
	})
	return rd
}

func TestForeignAndUncertifiedAdultEnforcementInCatalog(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := New(db, nil, config.Catalog{Region: "US"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	released := time.Date(2023, 5, 1, 0, 0, 0, 0, time.UTC)

	phR18 := c.movieDetailsToTitle(ctx, &tmdb.MovieDetails{
		Movie: tmdb.Movie{
			ID:          101,
			Title:       "Manila R18",
			ReleaseDate: "2023-05-01",
			Popularity:  25,
			VoteCount:   100,
		},
		ReleaseDates: testReleaseDates("PH", "R-18"),
	})
	if phR18.ContentRating != "ph/R-18" || phR18.EnforcementRating != "NC-17" {
		t.Fatalf("ph/R-18 conversion = display %q enforce %q", phR18.ContentRating, phR18.EnforcementRating)
	}

	phX := c.movieDetailsToTitle(ctx, &tmdb.MovieDetails{
		Movie: tmdb.Movie{
			ID:          102,
			Title:       "Manila X",
			ReleaseDate: "2023-05-01",
			Popularity:  25,
			VoteCount:   100,
		},
		ReleaseDates: testReleaseDates("PH", "X"),
	})
	if phX.ContentRating != "" || phX.EnforcementRating != "NC-17" {
		t.Fatalf("PH X conversion = display %q enforce %q", phX.ContentRating, phX.EnforcementRating)
	}

	de16 := c.movieDetailsToTitle(ctx, &tmdb.MovieDetails{
		Movie: tmdb.Movie{
			ID:          103,
			Title:       "Berlin 16",
			ReleaseDate: "2023-05-01",
			Popularity:  25,
			VoteCount:   100,
		},
		ReleaseDates: testReleaseDates("DE", "16"),
	})
	if de16.ContentRating != "de/16" || de16.EnforcementRating != "R" {
		t.Fatalf("de/16 conversion = display %q enforce %q", de16.ContentRating, de16.EnforcementRating)
	}

	gb12A := c.movieDetailsToTitle(ctx, &tmdb.MovieDetails{
		Movie: tmdb.Movie{
			ID:          104,
			Title:       "London 12A",
			ReleaseDate: "2023-05-01",
			Popularity:  25,
			VoteCount:   100,
		},
		ReleaseDates: testReleaseDates("GB", "12A"),
	})
	if gb12A.ContentRating != "gb/12A" || gb12A.EnforcementRating != "PG-13" {
		t.Fatalf("gb/12A conversion = display %q enforce %q", gb12A.ContentRating, gb12A.EnforcementRating)
	}

	softcore := c.movieDetailsToTitle(ctx, &tmdb.MovieDetails{
		Movie: tmdb.Movie{
			ID:          105,
			Title:       "Uncertified Softcore",
			ReleaseDate: "2023-05-01",
			Popularity:  25,
			VoteCount:   100,
		},
		Keywords: &tmdb.Keywords{Keywords: []tmdb.Keyword{{Name: "softcore"}}},
	})
	if softcore.ContentRating != "" || softcore.EnforcementRating != contentrating.UncertifiedAdult {
		t.Fatalf("softcore conversion = display %q enforce %q", softcore.ContentRating, softcore.EnforcementRating)
	}

	benignUnrated := core.Title{
		Key:        core.MovieKey(106),
		Type:       core.TypeMovie,
		Title:      "Indie Unrated",
		Released:   released,
		Year:       2023,
		Popularity: 25,
		VoteCount:  100,
		Keywords:   []string{"nudity"},
	}

	// Simulate a pre-migration-64 row whose enforcement_rating is still '' before lazy refresh.
	preRefreshAdult := core.Title{
		Key:        core.MovieKey(107),
		Type:       core.TypeMovie,
		Title:      "PreRefresh Erotic",
		Released:   released,
		Year:       2023,
		Popularity: 25,
		VoteCount:  100,
		Keywords:   []string{"erotic thriller"},
	}

	for _, tt := range []core.Title{phR18, phX, de16, gb12A, softcore, benignUnrated, preRefreshAdult} {
		if err := c.UpsertTitle(ctx, tt, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE catalog_titles SET enforcement_rating='' WHERE key=?`, int64(preRefreshAdult.Key)); err != nil {
		t.Fatal(err)
	}

	titlesOf := func(page core.Page) map[string]bool {
		m := make(map[string]bool, len(page.Items))
		for _, it := range page.Items {
			m[it.Title] = true
		}
		return m
	}

	// 1. Strict MaxContentRating=PG-13: only gb/12A passes; foreign 16/18/X and unrated/softcore are hidden.
	pg13Page, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Type: core.TypeMovie, MaxContentRating: "PG-13"},
		Limit:  50,
	})
	if err != nil {
		t.Fatal(err)
	}
	gotPG13 := titlesOf(pg13Page)
	if pg13Page.Total != 1 || !gotPG13["London 12A"] {
		t.Fatalf("MaxContentRating=PG-13 got total=%d items=%v", pg13Page.Total, gotPG13)
	}
	if pg13Page.Items[0].ContentRating != "gb/12A" {
		t.Fatalf("displayed ContentRating mutated: got %q, want gb/12A", pg13Page.Items[0].ContentRating)
	}

	// 2. PlexShare allowlist with owner allowing unrated ("G","PG","PG-13","None"):
	// admits gb/12A and benign Indie Unrated, while hiding ph/R-18, PH X, de/16, softcore, and pre-refresh erotic.
	allowWithUnrated, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Type: core.TypeMovie, ContentRatings: []string{"G", "PG", "PG-13", "None"}},
		Limit:  50,
	})
	if err != nil {
		t.Fatal(err)
	}
	gotAllow := titlesOf(allowWithUnrated)
	if allowWithUnrated.Total != 2 || !gotAllow["London 12A"] || !gotAllow["Indie Unrated"] {
		t.Fatalf("allowlist+None got total=%d items=%v", allowWithUnrated.Total, gotAllow)
	}

	// 3. PlexShare denylist ("R","NC-17"): hides ph/R-18, PH X, de/16, softcore, and pre-refresh erotic;
	// keeps gb/12A and benign Indie Unrated.
	denyAdult, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Type: core.TypeMovie, ExcludeContentRatings: []string{"R", "NC-17"}},
		Limit:  50,
	})
	if err != nil {
		t.Fatal(err)
	}
	gotDeny := titlesOf(denyAdult)
	if denyAdult.Total != 2 || !gotDeny["London 12A"] || !gotDeny["Indie Unrated"] {
		t.Fatalf("denylist R,NC-17 got total=%d items=%v", denyAdult.Total, gotDeny)
	}

	// 4. Passive FilterVisibility enriches remote search summaries with stored ContentRating & EnforcementRating.
	hits := c.FilterVisibility(ctx, []core.Title{
		{Key: phR18.Key, Type: core.TypeMovie, Title: phR18.Title},
		{Key: softcore.Key, Type: core.TypeMovie, Title: softcore.Title},
	})
	if len(hits) != 2 || hits[0].ContentRating != "ph/R-18" || hits[0].EnforcementRating != "NC-17" || hits[1].EnforcementRating != contentrating.UncertifiedAdult {
		t.Fatalf("FilterVisibility passive enrichment = %+v", hits)
	}

	// 5. RecentPage applies per-kind MovieFilter and EpisodeFilter independently.
	foreignShow := core.Title{
		Key:               core.ShowKey(201),
		Type:              core.TypeShow,
		Title:             "Seoul 19 Show",
		ContentRating:     "kr/청소년 관람불가",
		EnforcementRating: "NC-17",
		Released:          released,
		Year:              2023,
		Popularity:        30,
		VoteCount:         100,
	}
	teenShow := core.Title{
		Key:               core.ShowKey(202),
		Type:              core.TypeShow,
		Title:             "Seoul 12 Show",
		ContentRating:     "kr/12",
		EnforcementRating: "PG-13",
		Released:          released,
		Year:              2023,
		Popularity:        30,
		VoteCount:         100,
	}
	for _, s := range []core.Title{foreignShow, teenShow} {
		if err := c.UpsertTitle(ctx, s, true); err != nil {
			t.Fatal(err)
		}
		ep := core.Title{
			Key:               core.EpisodeKey(s.Key.TMDB(), 1, 1),
			Type:              core.TypeEpisode,
			ShowKey:           s.Key,
			Season:            1,
			Episode:           1,
			Title:             s.Title + " Ep 1",
			ContentRating:     s.ContentRating,
			EnforcementRating: s.EnforcementRating,
			Released:          released,
			Year:              2023,
		}
		if err := c.upsert(ctx, ep, upsertOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	mf := core.Filter{ExcludeContentRatings: []string{"R", "NC-17"}}
	ef := core.Filter{ExcludeContentRatings: []string{"TV-MA", "NC-17"}, ChildVisibility: &core.Filter{ExcludeContentRatings: []string{"TV-MA", "NC-17"}}}
	recent, err := c.RecentPage(ctx, core.BrowseQuery{
		Filter: core.Filter{MovieFilter: &mf, EpisodeFilter: &ef},
		Limit:  50,
	}, []core.MediaType{core.TypeMovie, core.TypeEpisode})
	if err != nil {
		t.Fatal(err)
	}
	gotRecent := titlesOf(recent)
	if gotRecent["Manila R18"] || gotRecent["Manila X"] || gotRecent["Uncertified Softcore"] || gotRecent["Seoul 19 Show Ep 1"] || !gotRecent["London 12A"] || !gotRecent["Seoul 12 Show Ep 1"] {
		t.Fatalf("RecentPage got total=%d items=%v", recent.Total, gotRecent)
	}
}
