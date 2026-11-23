package pluginitems

import (
	"context"
	"maps"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func sampleThreeLevelShowDecl() plugins.MediaKindMeta {
	return plugins.MediaKindMeta{
		Key:     "motorsport",
		Label:   "Motorsport",
		Library: plugins.KindLibraryDecl{Name: "Motorsport"},
		Levels: []plugins.KindLevel{
			{Key: "league", Label: "League", Container: true},
			{Key: "season", Label: "Season", Container: true},
			{Key: "event", Label: "Event", Playable: true},
		},
		Sync: plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
}

func TestParentTallyInvariants(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	decl := sampleThreeLevelShowDecl()
	const (
		pluginID = "test.sports"
		kindKey  = "motorsport"
		libID    = core.LibraryID(77)
	)
	if _, err := s.UpsertKind(ctx, pluginID, libID, decl, false); err != nil {
		t.Fatal(err)
	}
	ref := func(id string) core.ItemRef { return core.NewItemRef(pluginID, kindKey, id) }
	assertCounts := func(label string, want map[string]ParentCounts) {
		t.Helper()
		refs := make([]core.ItemRef, 0, len(want))
		for id := range want {
			refs = append(refs, ref(id))
		}
		got, err := s.ParentCountsMany(ctx, libID, refs)
		if err != nil {
			t.Fatalf("%s: ParentCountsMany: %v", label, err)
		}
		for id, w := range want {
			g := got[ref(id)]
			if g.ChildCount != w.ChildCount || g.LeafCount != w.LeafCount || (w.ByLevel != nil && !maps.Equal(g.ByLevel, w.ByLevel)) {
				t.Fatalf("%s: %s got %+v, want %+v", label, id, g, w)
			}
		}
	}

	// Bottom-up insert inside a single batch: events arrive before their
	// season, which arrives before its league.
	if _, err := s.KindUpsert(ctx, pluginID, kindKey, []core.KindItem{
		{ID: "ev-1", Level: "event", ParentID: "s-2025", Index: 1, Title: "Round 1", Playable: true},
		{ID: "ev-2", Level: "event", ParentID: "s-2025", Index: 2, Title: "Round 2", Playable: true},
		{ID: "s-2025", Level: "season", ParentID: "lg-1", Index: 2025, Title: "2025 Season"},
		{ID: "lg-1", Level: "league", Title: "Formula"},
		{ID: "lg-2", Level: "league", Title: "Endurance"},
	}); err != nil {
		t.Fatal(err)
	}
	assertCounts("bottom-up batch", map[string]ParentCounts{
		"s-2025": {ChildCount: 2, LeafCount: 2},
		"lg-1":   {ChildCount: 1, LeafCount: 2},
		"lg-2":   {},
	})

	// Out-of-order across separate batches: events first, season later.
	if _, err := s.KindUpsert(ctx, pluginID, kindKey, []core.KindItem{
		{ID: "ev-3", Level: "event", ParentID: "s-2026", Index: 1, Title: "2026 Round 1", Playable: true},
		{ID: "ev-4", Level: "event", ParentID: "s-2026", Index: 2, Title: "2026 Round 2", Playable: true},
		{ID: "ev-5", Level: "event", ParentID: "s-2026", Index: 3, Title: "2026 Round 3", Playable: true},
	}); err != nil {
		t.Fatal(err)
	}
	assertCounts("orphan events before season", map[string]ParentCounts{
		"s-2026": {ChildCount: 3, LeafCount: 3},
		"lg-1":   {ChildCount: 1, LeafCount: 2},
	})
	if _, err := s.KindUpsert(ctx, pluginID, kindKey, []core.KindItem{
		{ID: "s-2026", Level: "season", ParentID: "lg-1", Index: 2026, Title: "2026 Season"},
	}); err != nil {
		t.Fatal(err)
	}
	assertCounts("season links pre-existing events to league", map[string]ParentCounts{
		"s-2025": {ChildCount: 2, LeafCount: 2},
		"s-2026": {ChildCount: 3, LeafCount: 3},
		"lg-1":   {ChildCount: 2, LeafCount: 5},
	})

	// Same-batch episode add + reparent of its season to another league.
	if _, err := s.KindUpsert(ctx, pluginID, kindKey, []core.KindItem{
		{ID: "ev-6", Level: "event", ParentID: "s-2025", Index: 3, Title: "Round 3", Playable: true},
		{ID: "s-2025", Level: "season", ParentID: "lg-2", Index: 2025, Title: "2025 Season"},
	}); err != nil {
		t.Fatal(err)
	}
	assertCounts("add event + reparent season in same batch", map[string]ParentCounts{
		"s-2025": {ChildCount: 3, LeafCount: 3},
		"s-2026": {ChildCount: 3, LeafCount: 3},
		"lg-1":   {ChildCount: 1, LeafCount: 3},
		"lg-2":   {ChildCount: 1, LeafCount: 3},
	})

	// Delete a single episode: decrements both season and league.
	if err := s.KindDelete(ctx, pluginID, kindKey, []string{"ev-3"}); err != nil {
		t.Fatal(err)
	}
	assertCounts("delete single episode", map[string]ParentCounts{
		"s-2026": {ChildCount: 2, LeafCount: 2},
		"lg-1":   {ChildCount: 1, LeafCount: 2},
	})

	// Cascading delete of an entire season: clears season tally and subtracts
	// its subtree leaf count from the surviving league (and deletes empty row).
	if err := s.KindDelete(ctx, pluginID, kindKey, []string{"s-2026"}); err != nil {
		t.Fatal(err)
	}
	assertCounts("cascade delete season", map[string]ParentCounts{
		"s-2026": {},
		"lg-1":   {},
		"s-2025": {ChildCount: 3, LeafCount: 3},
		"lg-2":   {ChildCount: 1, LeafCount: 3},
	})

	// Migration 16 backfill: drop the tally table + migration record, re-run
	// migrations, and confirm exact per-level counts are rebuilt from plugin_items.
	if _, err := db.ExecContext(ctx, `DROP TABLE plugin_item_parent_tally; DELETE FROM schema_migrations WHERE component = 'pluginitems' AND version = 16`); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("re-run migration 16: %v", err)
	}
	assertCounts("after migration 16 backfill", map[string]ParentCounts{
		"s-2025": {ChildCount: 3, LeafCount: 3, ByLevel: map[string]int{"event": 3}},
		"lg-2":   {ChildCount: 1, LeafCount: 3, ByLevel: map[string]int{"season": 1}},
		"lg-1":   {},
	})

	// Purge kind removes all parent tally rows for the library.
	if err := s.PurgeKind(ctx, pluginID, kindKey); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_item_parent_tally WHERE library_id = ?`, int64(libID)).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("plugin_item_parent_tally not purged: remaining=%d err=%v", remaining, err)
	}
}
