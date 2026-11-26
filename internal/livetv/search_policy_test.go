package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// SearchLiveTV must batch catalog policy checks (without trailer/credit reads),
// honor parent-show rating inheritance when filtering episodes, stop once limit
// allowed programmes are found, and abort cleanly when ctx is cancelled.
func TestSearchLiveTVBatchesPolicyAndStopsAtLimit(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	db := store.OpenTest(t)

	seedCat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	// Seed a TV-MA show + episode, plus 120 PG movies with remote trailers.
	show := core.Title{
		Key:               core.ShowKey(500),
		Type:              core.TypeShow,
		IDs:               core.IDs{TMDB: 500},
		Title:             "Forbidden Show",
		Released:          time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		ContentRating:     "TV-MA",
		EnforcementRating: "TV-MA",
	}
	ep := core.Title{
		Key:      core.EpisodeKey(500, 1, 1),
		Type:     core.TypeEpisode,
		ShowKey:  show.Key,
		ShowIDs:  core.IDs{TMDB: 500},
		Season:   1,
		Episode:  1,
		Title:    "Fire Pilot",
		Released: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	if err := seedCat.UpsertDetailedTitle(ctx, show); err != nil {
		t.Fatal(err)
	}
	if err := seedCat.UpsertDetailedTitle(ctx, ep); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 120; i++ {
		m := core.Title{
			Key:               core.MovieKey(int64(1000 + i)),
			Type:              core.TypeMovie,
			IDs:               core.IDs{TMDB: int64(1000 + i)},
			Title:             fmt.Sprintf("Fire Film %03d", i),
			Released:          time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			ContentRating:     "PG",
			EnforcementRating: "PG",
		}
		if err := seedCat.UpsertDetailedTitle(ctx, m); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO catalog_remote_trailers(key, name, url) VALUES(?,?,?)`, int64(m.Key), "Trailer", "https://example.invalid/trailer"); err != nil {
			t.Fatal(err)
		}
	}

	// Fresh catalog instance with cold in-memory titleCache.
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	s := testService(t, Options{
		DB:      db,
		Catalog: cat,
		Now:     func() time.Time { return now },
	})

	ch := Channel{
		ID:       "v-fire",
		Name:     "Cinema",
		Number:   "501",
		Kind:     "virtual",
		Enabled:  true,
		SourceID: "virtual",
		Group:    "Movies",
		Epoch:    time.Unix(0, 0).UTC(),
		Items:    []Item{{Key: core.MovieKey(1001), Title: "Fire Film 001", MediaType: "movie", Rating: "PG", DurationMS: 3600_000}},
	}
	if _, err := s.SaveChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshVirtualGuide(ctx); err != nil {
		t.Fatal(err)
	}
	// Mark search ready and populate 135 indexed virtual programmes: first 15
	// reference the TV-MA episode (with a stale "PG" tag on the programme JSON
	// so ONLY the catalog's inherited parent-show TV-MA rating blocks it),
	// followed by 120 distinct PG movies.
	host := liveTVHost{s: s}
	owner := &core.User{ID: 1, IsOwner: true}
	for deadline := time.Now().Add(3 * time.Second); ; {
		hits, e := host.SearchLiveTV(ctx, owner, "zzzz", 5)
		if e != nil {
			t.Fatal(e)
		}
		if !hits.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search index did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM livetv_search_virtual`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		start := now.Add(time.Duration(i) * time.Minute)
		p := Programme{
			ID:        fmt.Sprintf("v-fire-ma-%d", i),
			ChannelID: ch.ID,
			Start:     start,
			End:       start.Add(time.Hour),
			Item:      Item{Key: ep.Key, Title: "Fire Pilot", MediaType: "episode", Rating: "PG", DurationMS: 3600_000},
		}
		raw, _ := json.Marshal(p)
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_search_virtual(id,viewer_id,channel_id,start_ms,end_ms,data,body) VALUES(?,0,?,?,?,?,?)`, p.ID, ch.ID, p.Start.UnixMilli(), p.End.UnixMilli(), string(raw), "Fire Pilot"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 120; i++ {
		start := now.Add(time.Duration(15+i) * time.Minute)
		p := Programme{
			ID:        fmt.Sprintf("v-fire-pg-%d", i),
			ChannelID: ch.ID,
			Start:     start,
			End:       start.Add(time.Hour),
			Item:      Item{Key: core.MovieKey(int64(1000 + i)), Title: fmt.Sprintf("Fire Film %03d", i), MediaType: "movie", Rating: "PG", DurationMS: 3600_000},
		}
		raw, _ := json.Marshal(p)
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_search_virtual(id,viewer_id,channel_id,start_ms,end_ms,data,body) VALUES(?,0,?,?,?,?,?)`, p.ID, ch.ID, p.Start.UnixMilli(), p.End.UnixMilli(), string(raw), p.Item.Title); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// 1. Restricted viewer (MaxContentRating: PG-13) must skip all 15 TV-MA
	// episodes via inherited parent-show rating and still fill limit=10 PG hits
	// in a bounded number of queries with zero trailer reads.
	kid := &core.User{ID: 2, MaxContentRating: "PG-13"}
	qctx, stats := store.WithQueryStats(ctx)
	hits, err := host.SearchLiveTV(qctx, kid, "F", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits.Programmes) != 10 {
		t.Fatalf("expected 10 allowed PG programmes, got %d", len(hits.Programmes))
	}
	for _, p := range hits.Programmes {
		if p.Item.Key == ep.Key {
			t.Fatal("TV-MA episode leaked to PG-13 viewer")
		}
	}
	if stats.Queries() > 25 {
		t.Fatalf("expected bounded query count <= 25, got %d", stats.Queries())
	}
	for _, stmt := range stats.Statements() {
		if strings.Contains(strings.ToLower(stmt.SQL), "catalog_remote_trailers") {
			t.Fatalf("SearchLiveTV queried catalog_remote_trailers: %s", stmt.SQL)
		}
	}

	// 2. Cancelled context aborts cleanly.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := host.SearchLiveTV(cctx, owner, "F", 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
