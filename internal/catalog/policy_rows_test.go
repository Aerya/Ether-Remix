package catalog

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// CachedPolicyRows must match CachedGet for visibility and policy facts
// (including parent-show content-rating and keyword inheritance on episodes),
// read zero trailer/credit/alt-title rows, and never pollute titleCache with
// partial titles.
func TestCachedPolicyRowsInheritsParentPolicyWithoutTrailersOrCachePollution(t *testing.T) {
	c := newChunkTestCatalog(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })

	movie := core.Title{
		Key:               core.MovieKey(101),
		Type:              core.TypeMovie,
		IDs:               core.IDs{TMDB: 101},
		Title:             "Firestorm",
		Released:          time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC),
		Year:              2020,
		ContentRating:     "PG-13",
		Genres:            []string{"Action"},
		Keywords:          []string{"hero"},
		AlternativeTitles: []string{"Firestorm Alt"},
		Cast:              []core.Person{{TMDB: 1, Name: "Actor One", Role: "Lead"}},
	}
	adultMovie := core.Title{
		Key:      core.MovieKey(102),
		Type:     core.TypeMovie,
		IDs:      core.IDs{TMDB: 102},
		Title:    "Hidden Adult",
		Released: time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC),
		Adult:    true,
	}
	show := core.Title{
		Key:               core.ShowKey(201),
		Type:              core.TypeShow,
		IDs:               core.IDs{TMDB: 201},
		Title:             "Firehouse",
		Released:          time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
		Year:              2019,
		ContentRating:     "TV-MA",
		EnforcementRating: "TV-MA",
		ContentRatings:    map[string]string{"US": "TV-MA"},
		Genres:            []string{"Drama"},
		Keywords:          []string{"gritty"},
	}
	ep := core.Title{
		Key:      core.EpisodeKey(201, 1, 1),
		Type:     core.TypeEpisode,
		ShowKey:  show.Key,
		ShowIDs:  core.IDs{TMDB: 201},
		Season:   1,
		Episode:  1,
		Title:    "First Shift",
		Released: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
		Year:     2019,
	}
	for _, title := range []core.Title{movie, adultMovie, show, ep} {
		if err := c.upsert(ctx, title, upsertOpts{browsable: true, detail: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO catalog_remote_trailers(key, name, url) VALUES(?,?,?)`, int64(movie.Key), "Trailer", "https://example.invalid/t"); err != nil {
		t.Fatal(err)
	}
	c.cacheMu.Lock()
	clear(c.titleCache)
	c.cacheMu.Unlock()

	qctx, stats := store.WithQueryStats(ctx)
	rows, err := c.CachedPolicyRows(qctx, []core.RatingKey{movie.Key, adultMovie.Key, ep.Key, movie.Key})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range stats.Statements() {
		low := strings.ToLower(stmt.SQL)
		for _, forbidden := range []string{"catalog_remote_trailers", "catalog_credits", "catalog_alt_titles"} {
			if strings.Contains(low, forbidden) {
				t.Fatalf("CachedPolicyRows must not query %s: %s", forbidden, stmt.SQL)
			}
		}
	}
	if _, ok := rows[adultMovie.Key]; ok {
		t.Fatal("adult movie leaked from CachedPolicyRows")
	}
	gotMovie, ok := rows[movie.Key]
	if !ok {
		t.Fatal("expected visible movie in CachedPolicyRows")
	}
	if len(gotMovie.Genres) != 1 || gotMovie.Genres[0] != "Action" || len(gotMovie.Keywords) != 1 || gotMovie.Keywords[0] != "hero" {
		t.Fatalf("movie visibility facts missing: %+v", gotMovie)
	}
	if len(gotMovie.Cast) != 0 || gotMovie.RemoteTrailerURL != "" {
		t.Fatalf("expected partial policy row without cast/trailer, got cast=%d trailer=%q", len(gotMovie.Cast), gotMovie.RemoteTrailerURL)
	}
	gotEp, ok := rows[ep.Key]
	if !ok {
		t.Fatal("expected episode in CachedPolicyRows")
	}

	// Ensure titleCache was not polluted with partial rows and that episode
	// policy facts match CachedGet identically.
	fullEp, err := c.CachedGet(ctx, ep.Key)
	if err != nil {
		t.Fatal(err)
	}
	if gotEp.ContentRating != fullEp.ContentRating || gotEp.EnforcementRating != fullEp.EnforcementRating || gotEp.ShowTitle != fullEp.ShowTitle || len(gotEp.Keywords) != 1 || gotEp.Keywords[0] != "gritty" {
		t.Fatalf("episode policy mismatch: got rating=%q enf=%q show=%q kw=%v; want rating=%q enf=%q show=%q",
			gotEp.ContentRating, gotEp.EnforcementRating, gotEp.ShowTitle, gotEp.Keywords,
			fullEp.ContentRating, fullEp.EnforcementRating, fullEp.ShowTitle)
	}
	fullMovie, err := c.CachedGet(ctx, movie.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(fullMovie.Cast) != 1 || fullMovie.RemoteTrailerURL == "" || len(fullMovie.AlternativeTitles) != 1 {
		t.Fatalf("CachedGet returned polluted partial row: cast=%d trailer=%q alt=%d", len(fullMovie.Cast), fullMovie.RemoteTrailerURL, len(fullMovie.AlternativeTitles))
	}
}
