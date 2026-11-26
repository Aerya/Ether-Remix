package pluginitems

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// #646: republishing an item whose stored content is identical must not
// rewrite it. updated_at is the observable witness: a rewrite stamps it
// with the write time, a skip leaves it (and only seen_at advances).
func TestUpsertSkipsUnchangedItems(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	s, db := openTestService(t, Options{Now: func() time.Time { return now }})
	if _, err := s.UpsertKind(ctx, "community.books", 10, sampleBookDecl(), false); err != nil {
		t.Fatal(err)
	}
	items := []core.KindItem{
		{ID: "s1", Level: "series", Title: "Series 1"},
		{ID: "b1", Level: "book", ParentID: "s1", Title: "Book 1", ETag: "v1",
			Fields: map[string]json.RawMessage{
				"format": rawJSON(t, "epub"),
				"tags":   rawJSON(t, []string{"sf", "space"}),
				"pages":  rawJSON(t, 321),
			},
			Art: []core.KindArtRef{{Role: "poster", URL: "https://img.example/b1.jpg", Width: 10, Height: 15}}},
	}
	stats, err := s.UpsertItemsWithStats(ctx, "community.books", "book", items)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 2 || stats.Updated != 0 || stats.Unchanged != 0 {
		t.Fatalf("first publish stats = %+v, want 2 added", stats)
	}
	stamp := func(id string) (updated, seen int64) {
		t.Helper()
		if err := db.QueryRowContext(ctx,
			`SELECT updated_at, seen_at FROM plugin_items WHERE external_id = ?`, id).Scan(&updated, &seen); err != nil {
			t.Fatal(err)
		}
		return updated, seen
	}
	b1Updated, b1Seen := stamp("b1")
	var fieldsBefore int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_item_fields`).Scan(&fieldsBefore); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Hour)
	stats, err = s.UpsertItemsWithStats(ctx, "community.books", "book", items)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 0 || stats.Updated != 0 || stats.Unchanged != 2 {
		t.Fatalf("republish stats = %+v, want 2 unchanged", stats)
	}
	updated, seen := stamp("b1")
	if updated != b1Updated {
		t.Fatalf("unchanged item rewritten: updated_at %d -> %d", b1Updated, updated)
	}
	if seen <= b1Seen {
		t.Fatalf("unchanged item lost its seen refresh: seen_at %d -> %d", b1Seen, seen)
	}
	var fieldsAfter int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_item_fields`).Scan(&fieldsAfter); err != nil {
		t.Fatal(err)
	}
	if fieldsAfter != fieldsBefore {
		t.Fatalf("field rows changed on unchanged republish: %d -> %d", fieldsBefore, fieldsAfter)
	}

	// A real change rewrites exactly that item and reports it updated.
	now = now.Add(time.Hour)
	changed := make([]core.KindItem, len(items))
	copy(changed, items)
	changed[1].Title = "Book 1, revised"
	stats, err = s.UpsertItemsWithStats(ctx, "community.books", "book", changed)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 0 || stats.Updated != 1 || stats.Unchanged != 1 {
		t.Fatalf("changed republish stats = %+v, want 1 updated + 1 unchanged", stats)
	}
	updated, _ = stamp("b1")
	if updated <= b1Updated {
		t.Fatalf("changed item not rewritten: updated_at still %d", updated)
	}
}

// #766: KindUpsertWithUndo skips snapshots for unchanged items and skips all
// snapshot reads when snapshotPrevious is false (sync.undo: "ids").
func TestKindUpsertWithUndoSkipsUnchangedSnapshots(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	s, _ := openTestService(t, Options{Now: func() time.Time { return now }})
	if _, err := s.UpsertKind(ctx, "community.books", 10, sampleBookDecl(), false); err != nil {
		t.Fatal(err)
	}
	items := []core.KindItem{
		{ID: "s1", Level: "series", Title: "Series 1"},
		{ID: "b1", Level: "book", ParentID: "s1", Title: "Book 1", ETag: "v1",
			Art: []core.KindArtRef{{Role: "poster", URL: "https://img.example/b1.jpg", Width: 10, Height: 15}}},
	}

	u1, err := s.KindUpsertWithUndo(ctx, "community.books", "book", items, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(u1.AddedIDs) != 2 || len(u1.UpdatedIDs) != 0 || len(u1.Previous) != 0 {
		t.Fatalf("initial undo = %+v, want 2 added, 0 updated, 0 previous", u1)
	}

	// Re-publishing identical content yields zero snapshots and zero changed IDs.
	now = now.Add(time.Hour)
	u2, err := s.KindUpsertWithUndo(ctx, "community.books", "book", items, true)
	if err != nil {
		t.Fatal(err)
	}
	if u2.Result.Unchanged != 2 || len(u2.AddedIDs) != 0 || len(u2.UpdatedIDs) != 0 || len(u2.Previous) != 0 {
		t.Fatalf("unchanged undo = %+v, want 2 unchanged and empty change set", u2)
	}

	// Re-publishing with s1 unchanged, b1 revised, and b2 added snapshots only b1 (with art).
	now = now.Add(time.Hour)
	mixed := []core.KindItem{
		items[0],
		{ID: "b1", Level: "book", ParentID: "s1", Title: "Book 1, revised", ETag: "v2",
			Art: []core.KindArtRef{{Role: "poster", URL: "https://img.example/b1-v2.jpg", Width: 20, Height: 30}}},
		{ID: "b2", Level: "book", ParentID: "s1", Title: "Book 2"},
	}
	u3, err := s.KindUpsertWithUndo(ctx, "community.books", "book", mixed, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(u3.AddedIDs) != 1 || u3.AddedIDs[0] != "b2" {
		t.Fatalf("mixed AddedIDs = %v, want [b2]", u3.AddedIDs)
	}
	if len(u3.UpdatedIDs) != 1 || u3.UpdatedIDs[0] != "b1" {
		t.Fatalf("mixed UpdatedIDs = %v, want [b1]", u3.UpdatedIDs)
	}
	if len(u3.Previous) != 1 || u3.Previous[0].ID != "b1" || u3.Previous[0].Title != "Book 1" || len(u3.Previous[0].Art) != 1 || u3.Previous[0].Art[0].URL != "https://img.example/b1.jpg" {
		t.Fatalf("mixed Previous = %+v, want only pre-write b1 with original art", u3.Previous)
	}

	// With snapshotPrevious=false (sync.undo: "ids"), modified IDs are tracked without loading snapshots.
	now = now.Add(time.Hour)
	mixed[1].Title = "Book 1, v3"
	u4, err := s.KindUpsertWithUndo(ctx, "community.books", "book", mixed, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(u4.AddedIDs) != 0 || len(u4.UpdatedIDs) != 1 || u4.UpdatedIDs[0] != "b1" || len(u4.Previous) != 0 {
		t.Fatalf("ids-mode undo = %+v, want updated [b1] with no previous snapshots", u4)
	}
}

// #647: deleting from a flat single-level kind works and deleting ids
// that do not exist stays a no-op; the descendant walk itself is kept an
// index seek by construction (see loadDeleteTargets).
func TestDeleteFlatKindAndMissingIDs(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	decl := sampleBookDecl()
	decl.Levels = decl.Levels[1:] // book only: a flat kind
	if _, err := s.UpsertKind(ctx, "community.flat", 11, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KindUpsert(ctx, "community.flat", "book", []core.KindItem{
		{ID: "b1", Level: "book", Title: "Book 1"},
		{ID: "b2", Level: "book", Title: "Book 2"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.KindDelete(ctx, "community.flat", "book", []string{"missing", "b1"}); err != nil {
		t.Fatal(err)
	}
	count, err := s.PluginItemCount(ctx, "community.flat")
	if err != nil || count != 1 {
		t.Fatalf("count after flat delete = (%d, %v), want (1, nil)", count, err)
	}
}
