package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// rankViewFixture stores movies and rebuilds the rank once, so the live
// copy is catalog_rank_b and catalog_rank_a is the emptied, retired one.
func rankViewFixture(t *testing.T, ctx context.Context) (*Catalog, int) {
	t.Helper()
	c := newChunkTestCatalog(t)
	rel := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var titles []core.Title
	for i := int64(1); i <= 25; i++ {
		titles = append(titles, core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, Title: fmt.Sprint("M", i), Released: rel, Popularity: float64(i)})
	}
	if err := c.upsertMany(ctx, titles, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RebuildRank(ctx); err != nil {
		t.Fatal(err)
	}
	if side := rankSideOf(t, ctx, c.db); side != "b" {
		t.Fatalf("fixture: live rank copy %q after one rebuild, want b", side)
	}
	return c, len(titles)
}

func rankSideOf(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var side string
	if err := db.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k='rank_side'`).Scan(&side); err != nil {
		t.Fatal(err)
	}
	return side
}

func rankViewRows(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_rank`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A logical copy (ether cluster init from SQLite to PostgreSQL, a backup
// restore) restores tables, not views: the target's view is the one its
// migrations created, on catalog_rank_a, while the restored rank_side names
// catalog_rank_b. Popularity pages read the empty retired copy, and on
// PostgreSQL every later rebuild failed to drop catalog_rank_a ("other
// objects depend on it"), so the rank never recovered.
func TestLogicalRestoreKeepsTheRankViewOnTheLiveCopy(t *testing.T) {
	ctx := context.Background()
	src, titles := rankViewFixture(t, ctx)
	var dump bytes.Buffer
	if err := store.ExportLogical(ctx, src.db, &dump); err != nil {
		t.Fatal(err)
	}
	dst := newChunkTestCatalog(t)
	if err := store.ImportLogical(ctx, dst.db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if got := rankViewRows(t, ctx, dst.db); got != titles {
		t.Fatalf("catalog_rank after restore: %d rows, want %d", got, titles)
	}
	if _, err := dst.RebuildRank(ctx); err != nil {
		t.Fatalf("rank rebuild after restore: %v", err)
	}
	if got := rankViewRows(t, ctx, dst.db); got != titles {
		t.Fatalf("catalog_rank after the next rebuild: %d rows, want %d", got, titles)
	}
}

// A database already left with the view on the retired copy recovers at
// the next rebuild.
func TestRankRebuildRepairsAViewOnTheIdleCopy(t *testing.T) {
	ctx := context.Background()
	c, titles := rankViewFixture(t, ctx)
	for _, q := range []string{`DROP VIEW catalog_rank`, `CREATE VIEW catalog_rank AS SELECT ` + rankColumns + ` FROM catalog_rank_a`} {
		if _, err := c.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if got := rankViewRows(t, ctx, c.db); got != 0 {
		t.Fatalf("fixture: view on the retired copy reads %d rows", got)
	}
	if _, err := c.RebuildRank(ctx); err != nil {
		t.Fatalf("rank rebuild with the view on the idle copy: %v", err)
	}
	if got := rankViewRows(t, ctx, c.db); got != titles {
		t.Fatalf("catalog_rank after the rebuild: %d rows, want %d", got, titles)
	}
}
