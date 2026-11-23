package pluginitems

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func restoreTestDecl() plugins.MediaKindMeta {
	return plugins.MediaKindMeta{
		Key:     "book",
		Label:   "Books",
		Library: plugins.KindLibraryDecl{Name: "Books"},
		Levels:  []plugins.KindLevel{{Key: "book", Label: "Book", Playable: true}},
		Fields: []plugins.KindField{
			{Key: "publisher", Type: "string", Label: "Publisher", Indexed: true},
			{Key: "authors", Type: "people", Label: "Authors", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{
			Search:       true,
			SearchFields: []string{"publisher"},
		},
	}
}

// dropColumnFromLogicalDump removes a single column from one table in a
// logical JSONL dump (simulating a standalone SQLite dump that lacks a
// PostgreSQL-only column like plugin_items.search_tsv).
func dropColumnFromLogicalDump(t *testing.T, dump []byte, table, col string) []byte {
	t.Helper()
	var out bytes.Buffer
	inTable := false
	dropIdx := -1
	for _, line := range bytes.Split(bytes.TrimSpace(dump), []byte("\n")) {
		if bytes.HasPrefix(line, []byte(`{"table"`)) {
			var h struct {
				Table   string   `json:"table"`
				Columns []string `json:"columns"`
			}
			if err := json.Unmarshal(line, &h); err != nil {
				t.Fatal(err)
			}
			inTable = strings.EqualFold(h.Table, table)
			dropIdx = -1
			if inTable {
				cols := make([]string, 0, len(h.Columns))
				for i, c := range h.Columns {
					if strings.EqualFold(c, col) {
						dropIdx = i
						continue
					}
					cols = append(cols, c)
				}
				h.Columns = cols
				b, err := json.Marshal(h)
				if err != nil {
					t.Fatal(err)
				}
				out.Write(b)
				out.WriteByte('\n')
				continue
			}
		} else if inTable && dropIdx >= 0 && bytes.HasPrefix(line, []byte(`[`)) {
			var row []json.RawMessage
			if err := json.Unmarshal(line, &row); err != nil {
				t.Fatal(err)
			}
			if dropIdx < len(row) {
				row = append(row[:dropIdx], row[dropIdx+1:]...)
			}
			b, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			out.Write(b)
			out.WriteByte('\n')
			continue
		} else {
			inTable = false
			dropIdx = -1
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// TestLogicalRestoreRebuildsPluginSearch verifies that a logical backup
// restore marks plugin kinds pending reindex and rebuilds the engine's search
// index (SQLite plugin_item_fts or PostgreSQL plugin_items.search_tsv when
// search_tsv is absent from the dump, as in a standalone SQLite -> PostgreSQL
// cluster upgrade) across multiple keyset chunks with title (A), field (B),
// and people (C) weighting plus orphan rows.
func TestLogicalRestoreRebuildsPluginSearch(t *testing.T) {
	ctx := context.Background()
	src, srcDB := openTestService(t, Options{})
	defer src.Close()

	decl := restoreTestDecl()
	const (
		pluginID                = "restore.books"
		kindKey                 = "book"
		libID    core.LibraryID = 77
	)
	if _, err := src.UpsertKind(ctx, pluginID, libID, decl, false); err != nil {
		t.Fatal(err)
	}

	// Seed more than one upsertChunkSize (200) so chunked keyset rebuild
	// spans multiple chunks. Include distinct title/field/people matches for
	// weighting verification plus a punctuation-only title.
	total := upsertChunkSize + 25
	items := make([]core.KindItem, 0, total+3)
	for i := 0; i < total; i++ {
		items = append(items, core.KindItem{
			ID:       fmt.Sprintf("bk-%04d", i),
			Level:    "book",
			Title:    fmt.Sprintf("Catalog Volume %04d", i),
			Playable: true,
			Fields: map[string]json.RawMessage{
				"publisher": rawJSON(t, "Northwind Press"),
			},
		})
	}
	items = append(items,
		core.KindItem{
			ID:       "rank-title",
			Level:    "book",
			Title:    "Starlight Chronicles",
			Playable: true,
			Fields: map[string]json.RawMessage{
				"publisher": rawJSON(t, "Ordinary House"),
				"authors":   rawJSON(t, []map[string]string{{"name": "Clara Reed"}}),
			},
		},
		core.KindItem{
			ID:       "rank-field",
			Level:    "book",
			Title:    "Quiet Harbor",
			Playable: true,
			Fields: map[string]json.RawMessage{
				"publisher": rawJSON(t, "Starlight Imprint"),
				"authors":   rawJSON(t, []map[string]string{{"name": "Mira Solis"}}),
			},
		},
		core.KindItem{
			ID:       "rank-person",
			Level:    "book",
			Title:    "Distant Lantern",
			Playable: true,
			Fields: map[string]json.RawMessage{
				"publisher": rawJSON(t, "Paper Mill"),
				"authors":   rawJSON(t, []map[string]string{{"name": "Starlight Mercer"}}),
			},
		},
		core.KindItem{
			ID:       "punct-only",
			Level:    "book",
			Title:    "...",
			Playable: true,
		},
	)
	if _, err := src.UpsertItems(ctx, pluginID, kindKey, items); err != nil {
		t.Fatal(err)
	}

	// Also insert an orphan item whose (plugin_id, kind_key) is absent from plugin_kinds.
	orphanRef := string(core.NewItemRef("legacy.removed", "note", "orph-1"))
	if _, err := srcDB.ExecContext(ctx, `
INSERT INTO plugin_items(
	item_ref, seq, plugin_id, kind_key, library_id, external_id, parent_ref,
	level, idx, title, sort_title, summary, year, released, rating, playable,
	child_count, leaf_count, hints, fields, etag, added_at, updated_at, seen_at, missed_walks, unavailable
) VALUES (?, 99999, 'legacy.removed', 'note', 88, 'orph-1', '', 'note', 0, 'Orphaned Manuscript', 'orphaned manuscript', '', 2024, 1700000000, 0, 1, 0, 0, '{}', '{}', '', 1700000000, 1700000000, 1700000000, 0, '')`,
		orphanRef); err != nil {
		t.Fatalf("insert orphan item: %v", err)
	}

	var dump bytes.Buffer
	if err := store.ExportLogical(ctx, srcDB, &dump); err != nil {
		t.Fatalf("ExportLogical: %v", err)
	}
	payload := dump.Bytes()
	if store.DialectOf(srcDB) == store.Postgres {
		// Simulate restoring a standalone SQLite backup onto PostgreSQL:
		// SQLite dumps do not carry plugin_items.search_tsv.
		payload = dropColumnFromLogicalDump(t, payload, "plugin_items", "search_tsv")
	}

	dstDB := store.OpenTest(t)
	if err := store.ImportLogical(ctx, dstDB, bytes.NewReader(payload)); err != nil {
		t.Fatalf("ImportLogical: %v", err)
	}

	// The restore hook must reset fts_version so startup rebuilds the index.
	var ftsVer int64
	if err := dstDB.QueryRowContext(ctx,
		`SELECT fts_version FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?`,
		pluginID, kindKey).Scan(&ftsVer); err != nil {
		t.Fatalf("read fts_version after import: %v", err)
	}
	if ftsVer != 0 {
		t.Fatalf("fts_version after logical restore = %d, want 0", ftsVer)
	}

	dst, err := New(ctx, dstDB, Options{})
	if err != nil {
		t.Fatalf("New after restore: %v", err)
	}
	defer dst.Close()

	if err := dstDB.QueryRowContext(ctx,
		`SELECT fts_version FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?`,
		pluginID, kindKey).Scan(&ftsVer); err != nil {
		t.Fatalf("read fts_version after New: %v", err)
	}
	if ftsVer != currentFTSVersion {
		t.Fatalf("fts_version after New = %d, want %d", ftsVer, currentFTSVersion)
	}

	// Title (A), searchable field (B), and people (C) must all match, with
	// title ranked ahead of field and people matches.
	res, err := dst.Search(ctx, SearchQuery{LibraryID: libID, Query: "Starlight", Limit: 10})
	if err != nil {
		t.Fatalf("Search Starlight: %v", err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("Search Starlight returned %d items, want 3 (%+v)", len(res.Items), res.Items)
	}
	if res.Items[0].ID != "rank-title" {
		t.Fatalf("top Starlight result = %q, want rank-title (weight A)", res.Items[0].ID)
	}
	gotIDs := map[string]bool{}
	for _, it := range res.Items {
		gotIDs[it.ID] = true
	}
	if !gotIDs["rank-field"] || !gotIDs["rank-person"] {
		t.Fatalf("expected field and person matches in results, got %+v", res.Items)
	}
	if store.DialectOf(dstDB) == store.Postgres {
		if res.Items[1].ID != "rank-field" || res.Items[2].ID != "rank-person" {
			t.Fatalf("PostgreSQL search_tsv weighting: expected field (B) ahead of person (C), got %q then %q", res.Items[1].ID, res.Items[2].ID)
		}
	}

	// Verify items from the final chunk (> upsertChunkSize) are indexed.
	lastID := fmt.Sprintf("bk-%04d", total-1)
	tailRes, err := dst.Search(ctx, SearchQuery{LibraryID: libID, Query: fmt.Sprintf("%04d", total-1), Limit: 10})
	if err != nil {
		t.Fatalf("Search tail chunk: %v", err)
	}
	if len(tailRes.Items) != 1 || tailRes.Items[0].ID != lastID {
		t.Fatalf("Search tail chunk = %+v, want [%s]", tailRes.Items, lastID)
	}

	// Verify orphan item without plugin_kinds row was indexed too.
	orphRes, err := dst.Search(ctx, SearchQuery{LibraryID: 88, Query: "Manuscript", Limit: 10})
	if err != nil {
		t.Fatalf("Search orphan: %v", err)
	}
	if len(orphRes.Items) != 1 || orphRes.Items[0].ID != "orph-1" {
		t.Fatalf("Search orphan = %+v, want [orph-1]", orphRes.Items)
	}
}

// TestStartupSelfHealRebuildsEmptySearchIndex verifies that opening pluginitems
// on a database where fts_version is already currentFTSVersion yet the search
// index is empty (e.g. restored before the restore hook existed) heals the
// index once on startup and does not re-trigger on subsequent startups even
// when punctuation-only or whitespace-only titles are present.
func TestStartupSelfHealRebuildsEmptySearchIndex(t *testing.T) {
	ctx := context.Background()
	svc, db := openTestService(t, Options{})

	decl := restoreTestDecl()
	const (
		pluginID                = "heal.books"
		kindKey                 = "book"
		libID    core.LibraryID = 91
	)
	if _, err := svc.UpsertKind(ctx, pluginID, libID, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertItems(ctx, pluginID, kindKey, []core.KindItem{
		{
			ID:       "b1",
			Level:    "book",
			Title:    "Amber Compass",
			Playable: true,
			Fields: map[string]json.RawMessage{
				"publisher": rawJSON(t, "Meridian Press"),
				"authors":   rawJSON(t, []map[string]string{{"name": "Soren Lind"}}),
			},
		},
		{
			ID:       "b-punct",
			Level:    "book",
			Title:    "...",
			Playable: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc.Close()

	// Wipe the engine-specific search index while keeping fts_version = currentFTSVersion.
	isPG := store.DialectOf(db) == store.Postgres
	if isPG {
		if _, err := db.ExecContext(ctx, `UPDATE plugin_items SET search_tsv = ''::tsvector`); err != nil {
			t.Fatalf("clear search_tsv: %v", err)
		}
	} else {
		if _, err := db.ExecContext(ctx, `DELETE FROM plugin_item_fts`); err != nil {
			t.Fatalf("clear plugin_item_fts: %v", err)
		}
	}

	healed, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatalf("New self-heal: %v", err)
	}
	defer healed.Close()

	for _, q := range []string{"Amber", "Meridian", "Soren"} {
		res, err := healed.Search(ctx, SearchQuery{LibraryID: libID, Query: q, Limit: 10})
		if err != nil {
			t.Fatalf("Search %q after self-heal: %v", q, err)
		}
		if len(res.Items) != 1 || res.Items[0].ID != "b1" {
			t.Fatalf("Search %q after self-heal = %+v, want [b1]", q, res.Items)
		}
	}

	// Second startup probe must report no missing kinds despite punctuation-only
	// and whitespace-only titles.
	extra, hasOrphans, err := healed.findKindsMissingSearchIndex(ctx, isPG, map[string]bool{})
	if err != nil {
		t.Fatalf("findKindsMissingSearchIndex on second check: %v", err)
	}
	if len(extra) != 0 || hasOrphans {
		t.Fatalf("unexpected rebuild scheduled on already-indexed DB: extra=%d hasOrphans=%v", len(extra), hasOrphans)
	}
}

// TestSearchIndexRebuildIsResumableAcrossChunks verifies that an interrupted
// chunked rebuild leaves fts_version < currentFTSVersion and a subsequent
// startup finishes the remaining keyset chunks cleanly.
func TestSearchIndexRebuildIsResumableAcrossChunks(t *testing.T) {
	ctx := context.Background()
	svc, db := openTestService(t, Options{})

	decl := restoreTestDecl()
	const (
		pluginID                = "resume.books"
		kindKey                 = "book"
		libID    core.LibraryID = 92
	)
	if _, err := svc.UpsertKind(ctx, pluginID, libID, decl, false); err != nil {
		t.Fatal(err)
	}
	total := upsertChunkSize + 30
	items := make([]core.KindItem, total)
	for i := 0; i < total; i++ {
		items[i] = core.KindItem{
			ID:       fmt.Sprintf("item-%04d", i),
			Level:    "book",
			Title:    fmt.Sprintf("Resumable Token%04d", i),
			Playable: true,
		}
	}
	if _, err := svc.UpsertItems(ctx, pluginID, kindKey, items); err != nil {
		t.Fatal(err)
	}

	// Simulate post-restore state: fts_version=0 and empty search index.
	isPG := store.DialectOf(db) == store.Postgres
	if _, err := db.ExecContext(ctx, `UPDATE plugin_kinds SET fts_version = 0 WHERE plugin_id = ? AND kind_key = ?`, pluginID, kindKey); err != nil {
		t.Fatal(err)
	}
	if isPG {
		if _, err := db.ExecContext(ctx, `UPDATE plugin_items SET search_tsv = ''::tsvector`); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := db.ExecContext(ctx, `DELETE FROM plugin_item_fts`); err != nil {
			t.Fatal(err)
		}
	}

	// Process only the first keyset chunk, simulating a crash before completion.
	n, nextCursor, done, wrote, err := svc.reindexKindOneChunkLocked(ctx, isPG, true, pluginID, kindKey, libID, decl, "")
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if n != upsertChunkSize || done || !wrote || nextCursor == "" {
		t.Fatalf("first chunk = (n=%d, cursor=%q, done=%v, wrote=%v), want full non-final chunk", n, nextCursor, done, wrote)
	}
	svc.Close()

	var ftsVer int64
	if err := db.QueryRowContext(ctx, `SELECT fts_version FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?`, pluginID, kindKey).Scan(&ftsVer); err != nil {
		t.Fatal(err)
	}
	if ftsVer != 0 {
		t.Fatalf("fts_version after partial chunk = %d, want 0", ftsVer)
	}

	// Reopen the service: startup must resume and finish all remaining items.
	resumed, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatalf("New resume: %v", err)
	}
	defer resumed.Close()

	if err := db.QueryRowContext(ctx, `SELECT fts_version FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?`, pluginID, kindKey).Scan(&ftsVer); err != nil {
		t.Fatal(err)
	}
	if ftsVer != currentFTSVersion {
		t.Fatalf("fts_version after resume = %d, want %d", ftsVer, currentFTSVersion)
	}
	for _, idx := range []int{0, upsertChunkSize - 1, total - 1} {
		tok := fmt.Sprintf("Token%04d", idx)
		res, err := resumed.Search(ctx, SearchQuery{LibraryID: libID, Query: tok, Limit: 5})
		if err != nil {
			t.Fatalf("Search %s: %v", tok, err)
		}
		if len(res.Items) != 1 || res.Items[0].ID != fmt.Sprintf("item-%04d", idx) {
			t.Fatalf("Search %s = %+v, want [item-%04d]", tok, res.Items, idx)
		}
	}
}
