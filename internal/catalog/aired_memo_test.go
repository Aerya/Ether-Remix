package catalog_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestAiredCountsMemoPerShowAndInvalidatesOnWrite verifies that overlapping
// batches of shows inside one request memo reuse per-show aired counts without
// re-querying already-seen shows, and invalidate cleanly once a catalog write
// increments WriteGeneration.
func TestAiredCountsMemoPerShowAndInvalidatesOnWrite(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cat, err := catalog.New(db, nil, config.Catalog{}, slog.Default(), catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	past := now.AddDate(0, -2, 0)
	s1, s2, s3 := core.ShowKey(101), core.ShowKey(102), core.ShowKey(103)
	for _, sk := range []core.RatingKey{s1, s2, s3} {
		if err := cat.UpsertTitle(ctx, core.Title{Key: sk, Type: core.TypeShow, Title: "Show", Released: past}, false); err != nil {
			t.Fatal(err)
		}
		if err := cat.UpsertTitle(ctx, core.Title{Key: core.SeasonKey(sk.TMDB(), 1), Type: core.TypeSeason, ShowKey: sk, Season: 1, Released: past}, false); err != nil {
			t.Fatal(err)
		}
		if err := cat.UpsertTitle(ctx, core.Title{Key: core.EpisodeKey(sk.TMDB(), 1, 1), Type: core.TypeEpisode, ShowKey: sk, Season: 1, Episode: 1, Released: past}, false); err != nil {
			t.Fatal(err)
		}
	}

	mctx := reqmemo.With(ctx)
	got1, err := cat.CachedGetMany(mctx, []core.RatingKey{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	if got1[s1].LeafCount != 1 || got1[s2].LeafCount != 1 {
		t.Fatalf("first batch leaf counts = (%d, %d), want (1, 1)", got1[s1].LeafCount, got1[s2].LeafCount)
	}

	// Overlapping subset {s2, s3}: s2 reuses the per-show memo; only s3 is fetched.
	qctx, stats := store.WithQueryStats(mctx)
	got2, err := cat.CachedGetMany(qctx, []core.RatingKey{s2, s3})
	if err != nil {
		t.Fatal(err)
	}
	if got2[s2].LeafCount != 1 || got2[s3].LeafCount != 1 {
		t.Fatalf("second batch leaf counts = (%d, %d), want (1, 1)", got2[s2].LeafCount, got2[s3].LeafCount)
	}

	// ResolveShowParents must reuse titleCache entries populated by CachedGetMany
	// (metaKnown=true) without executing SQL.
	rctx, rstats := store.WithQueryStats(mctx)
	parents, err := cat.ResolveShowParents(rctx, []core.RatingKey{s1, s2, s3})
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 3 {
		t.Fatalf("ResolveShowParents returned %d shows, want 3", len(parents))
	}
	if rstats.Queries() != 0 {
		t.Errorf("ResolveShowParents ran %d queries on cached shows, want 0", rstats.Queries())
	}

	// Re-reading {s1, s2} in the same request memo must execute zero SQL queries.
	qctx2, stats2 := store.WithQueryStats(mctx)
	if _, err := cat.CachedGetMany(qctx2, []core.RatingKey{s1, s2}); err != nil {
		t.Fatal(err)
	}
	if stats2.Queries() != 0 {
		t.Errorf("cached overlapping batch ran %d queries, want 0 (s3-only batch ran %d)", stats2.Queries(), stats.Queries())
	}

	// Writing a new episode bumps WriteGeneration, so the same reqmemo context
	// must observe the updated aired count immediately.
	if err := cat.UpsertTitle(ctx, core.Title{Key: core.EpisodeKey(s1.TMDB(), 1, 2), Type: core.TypeEpisode, ShowKey: s1, Season: 1, Episode: 2, Released: past}, false); err != nil {
		t.Fatal(err)
	}
	got3, err := cat.CachedGetMany(mctx, []core.RatingKey{s1})
	if err != nil {
		t.Fatal(err)
	}
	if got3[s1].LeafCount != 2 {
		t.Fatalf("after episode insert, LeafCount = %d, want 2", got3[s1].LeafCount)
	}
}
