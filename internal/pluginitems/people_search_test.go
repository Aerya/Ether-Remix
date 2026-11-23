package pluginitems

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func sampleStudioClipDecl() plugins.MediaKindMeta {
	return plugins.MediaKindMeta{
		Key:     "clip",
		Label:   "Clips",
		Library: plugins.KindLibraryDecl{Name: "Studio Clips"},
		Levels: []plugins.KindLevel{
			{Key: "channel", Label: "Channel", Container: true},
			{Key: "clip", Label: "Clip", Playable: true},
		},
		Fields: []plugins.KindField{
			// Intentionally omit Indexed and omit SearchFields to prove people
			// fields are always indexed and searchable for any content plugin.
			{Key: "cast", Type: "people", Label: "Cast"},
			{Key: "directors", Type: "people", Label: "Directors"},
			{Key: "writers", Type: "people", Label: "Writers"},
			{Key: "notes", Type: "string", Label: "Notes"},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:        []string{"title", "added", "released", "year"},
			Search:       true,
			SearchFields: []string{"notes"},
		},
	}
}

func TestPeopleMigrationAndReindex(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if store.DialectOf(db) != store.SQLite {
		t.Skip("SQLite FTS5 migration test")
	}

	decl := sampleStudioClipDecl()
	declJSON, err := json.Marshal(decl)
	if err != nil {
		t.Fatal(err)
	}

	// Roll pluginitems back to the pre-v3 schema state: v1 plugin_kinds without
	// fts_version, single-column FTS text, no people rows in plugin_item_fields,
	// and migration version 3 unapplied.
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS plugin_kinds`,
		`CREATE TABLE plugin_kinds (
			plugin_id      TEXT    NOT NULL,
			kind_key       TEXT    NOT NULL,
			library_id     INTEGER NOT NULL,
			decl           TEXT    NOT NULL,
			decl_hash      TEXT    NOT NULL,
			schema_version INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (plugin_id, kind_key)
		)`,
		`CREATE INDEX plugin_kinds_library ON plugin_kinds(library_id)`,
		`DROP TABLE IF EXISTS plugin_item_fts`,
		`CREATE VIRTUAL TABLE plugin_item_fts USING fts5(item_ref UNINDEXED, library_id UNINDEXED, text)`,
		`DELETE FROM schema_migrations WHERE component = 'pluginitems' AND version = 3`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("setup v2 state (%s): %v", stmt, err)
		}
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO plugin_kinds(plugin_id, kind_key, library_id, decl, decl_hash, schema_version)
		VALUES ('generic.clips', 'clip', 42, ?, ?, 1)
	`, string(declJSON), decl.DeclHash()); err != nil {
		t.Fatal(err)
	}

	items := []struct {
		seq       int64
		extID     string
		level     string
		parentRef string
		playable  int
		title     string
		fields    string
	}{
		{
			seq:      1,
			extID:    "ch-1",
			level:    "channel",
			playable: 0,
			title:    "Archive Sessions",
			fields:   `{"cast":[{"name":"Elena Vance","role":"Host","thumb":"https://cdn.example.test/people/elena.png"}]}`,
		},
		{
			seq:       2,
			extID:     "clip-1",
			level:     "clip",
			parentRef: string(core.NewItemRef("generic.clips", "clip", "ch-1")),
			playable:  1,
			title:     "Morning Light",
			fields:    `{"cast":[{"name":"Elena Vance","role":"Featured","thumb":"https://cdn.example.test/people/elena.png"},{"name":"Marcus Hale"}],"directors":[{"name":"Nora Sterling","thumb":"https://cdn.example.test/people/nora.jpg"}]}`,
		},
		{
			seq:       3,
			extID:     "clip-2",
			level:     "clip",
			parentRef: string(core.NewItemRef("generic.clips", "clip", "ch-1")),
			playable:  1,
			title:     "Evening Echo",
			fields:    `{"cast":[{"name":"Elena Vance","role":"Featured"}]}`,
		},
	}
	for _, it := range items {
		ref := string(core.NewItemRef("generic.clips", "clip", it.extID))
		if _, err := db.ExecContext(ctx, `
			INSERT INTO plugin_items(
				item_ref, seq, plugin_id, kind_key, library_id, external_id, parent_ref,
				level, idx, title, sort_title, summary, year, released, rating, playable,
				child_count, leaf_count, hints, fields, etag, added_at, updated_at, seen_at, missed_walks, unavailable
			) VALUES (?, ?, 'generic.clips', 'clip', 42, ?, ?, ?, 1, ?, ?, '', 2024, 1700000000, 0, ?, 0, 0, '{}', ?, 'etag', 1700000000, 1700000000, 1700000000, 0, '')
		`, ref, it.seq, it.extID, it.parentRef, it.level, it.title, strings.ToLower(it.title), it.playable, it.fields); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO plugin_item_fts(rowid, item_ref, library_id, text) VALUES (?, ?, 42, ?)`, it.seq, ref, it.title); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE plugin_items_seq SET next = 4 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	// Run store.Migrate to apply pluginitems migration 3 and reindex pre-existing rows.
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate v2->v3: %v", err)
	}

	svc, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatalf("New after migration: %v", err)
	}
	defer svc.Close()

	// Searching the cast member's name via FTS returns all 3 items listing Elena Vance.
	res, err := svc.Search(ctx, SearchQuery{
		LibraryID: 42,
		Query:     "Elena Vance",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("Search cast member name: %v", err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("expected 3 items for cast member Elena Vance after migration, got %d (%+v)", len(res.Items), res.Items)
	}

	// SearchPeople returns the deterministic plugin person ID, credit count, thumb, and sample items.
	people, err := svc.SearchPeople(ctx, 42, nil, "Elena", 10, 10)
	if err != nil {
		t.Fatalf("SearchPeople: %v", err)
	}
	if len(people) != 1 {
		t.Fatalf("expected 1 person match, got %+v", people)
	}
	wantID := core.PluginPersonID("Elena Vance")
	if people[0].ID != wantID || !core.IsPluginPersonID(people[0].ID) {
		t.Fatalf("unexpected person ID %d (want %d)", people[0].ID, wantID)
	}
	if people[0].CreditCount != 3 || people[0].Thumb != "https://cdn.example.test/people/elena.png" {
		t.Fatalf("unexpected person match metadata: %+v", people[0])
	}

	// PersonByID and Browse(PersonID, AnyLevel) return all items across levels without scanning JSON.
	byID, ok, err := svc.PersonByID(ctx, 42, nil, wantID, 10)
	if err != nil || !ok || byID.Name != "Elena Vance" || len(byID.Items) != 3 {
		t.Fatalf("PersonByID = (%+v, %v, %v)", byID, ok, err)
	}
	browsed, err := svc.Browse(ctx, BrowseQuery{
		LibraryID: 42,
		AnyLevel:  true,
		PersonID:  wantID,
		Limit:     10,
	})
	if err != nil || len(browsed.Items) != 3 {
		t.Fatalf("Browse by PersonID = (%+v, %v)", browsed, err)
	}
}

func TestPeopleSearchTitleFirstRanking(t *testing.T) {
	ctx := context.Background()
	svc, _ := openTestService(t, Options{})

	decl := sampleStudioClipDecl()
	if _, err := svc.UpsertKind(ctx, "generic.clips", 77, decl, false); err != nil {
		t.Fatal(err)
	}

	_, err := svc.UpsertItems(ctx, "generic.clips", "clip", []core.KindItem{
		{
			ID:    "ch-root",
			Level: "channel",
			Title: "Studio Feed",
		},
		{
			ID:       "clip-people-1",
			ParentID: "ch-root",
			Level:    "clip",
			Title:    "Midnight Signal",
			Fields:   map[string]json.RawMessage{"cast": rawJSON(t, []map[string]any{{"name": "Atlas Mercer"}})},
		},
		{
			ID:       "clip-people-2",
			ParentID: "ch-root",
			Level:    "clip",
			Title:    "Silent Harbor",
			Fields:   map[string]json.RawMessage{"cast": rawJSON(t, []map[string]any{{"name": "Atlas Mercer"}})},
		},
		{
			ID:       "clip-prefix",
			ParentID: "ch-root",
			Level:    "clip",
			Title:    "Atlas Protocol",
			Fields:   map[string]json.RawMessage{"notes": rawJSON(t, "studio archive")},
		},
		{
			ID:       "clip-exact",
			ParentID: "ch-root",
			Level:    "clip",
			Title:    "Atlas",
			Fields:   map[string]json.RawMessage{"notes": rawJSON(t, "feature presentation")},
		},
	})
	if err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	res, err := svc.Search(ctx, SearchQuery{
		LibraryID: 77,
		Query:     "Atlas",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("Search Atlas: %v", err)
	}
	if len(res.Items) != 4 {
		t.Fatalf("expected all 4 items for 'Atlas', got %d (%+v)", len(res.Items), res.Items)
	}
	if res.Items[0].Title != "Atlas" {
		t.Fatalf("expected exact title match first, got %q", res.Items[0].Title)
	}
	if res.Items[1].Title != "Atlas Protocol" {
		t.Fatalf("expected prefix title match second, got %q", res.Items[1].Title)
	}
	tail := []string{res.Items[2].Title, res.Items[3].Title}
	if !(tail[0] == "Midnight Signal" || tail[1] == "Midnight Signal") || !(tail[0] == "Silent Harbor" || tail[1] == "Silent Harbor") {
		t.Fatalf("expected cast member matches after title matches, got %v", tail)
	}
}

func TestPersonThumbValidationAndProjection(t *testing.T) {
	ctx := context.Background()
	svc, _ := openTestService(t, Options{})

	decl := sampleStudioClipDecl()
	if _, err := svc.UpsertKind(ctx, "generic.clips", 88, decl, false); err != nil {
		t.Fatal(err)
	}

	// Valid http/https thumbs succeed and project onto Cast/Directors/Writers with deterministic IDs.
	_, err := svc.UpsertItems(ctx, "generic.clips", "clip", []core.KindItem{
		{
			ID:    "ch-root",
			Level: "channel",
			Title: "Studio Feed",
		},
		{
			ID:       "clip-valid",
			ParentID: "ch-root",
			Level:    "clip",
			Title:    "Studio Portrait Test",
			Fields: map[string]json.RawMessage{
				"cast": rawJSON(t, []map[string]any{
					{"name": "Clara Park", "role": "Presenter", "thumb": "https://img.example.test/clara.png"},
				}),
				"directors": rawJSON(t, []map[string]any{
					{"name": "Devon Cole", "thumb": "http://img.example.test/devon.jpg"},
				}),
				"writers": rawJSON(t, []map[string]any{
					{"name": "Evan Ross", "thumb": "https://img.example.test/evan.jpg"},
				}),
			},
		},
	})
	if err != nil {
		t.Fatalf("valid person thumbs rejected: %v", err)
	}

	stored, _, err := svc.GetByExternal(ctx, "generic.clips", "clip", "clip-valid")
	if err != nil {
		t.Fatal(err)
	}
	projected := Project(stored, decl, core.Presentations{})
	if len(projected.Cast) != 1 || projected.Cast[0].ThumbPath != "https://img.example.test/clara.png" || projected.Cast[0].TMDB != core.PluginPersonID("Clara Park") {
		t.Fatalf("unexpected Cast projection: %+v", projected.Cast)
	}
	if len(projected.Directors) != 1 || projected.Directors[0].ThumbPath != "http://img.example.test/devon.jpg" || projected.Directors[0].TMDB != core.PluginPersonID("Devon Cole") {
		t.Fatalf("unexpected Directors projection: %+v", projected.Directors)
	}
	if len(projected.Writers) != 1 || projected.Writers[0].ThumbPath != "https://img.example.test/evan.jpg" || projected.Writers[0].TMDB != core.PluginPersonID("Evan Ross") {
		t.Fatalf("unexpected Writers projection: %+v", projected.Writers)
	}

	// Invalid thumb URLs are rejected on write.
	badThumbs := []string{
		"javascript:alert(1)",
		"file:///etc/passwd",
		"blob:portrait-1",
		"https:///missing-host.png",
		"https://img.example.test/has space.png",
		"https://img.example.test/" + strings.Repeat("a", MaxPersonThumbBytes),
	}
	for _, bad := range badThumbs {
		_, err := svc.UpsertItems(ctx, "generic.clips", "clip", []core.KindItem{
			{
				ID:       "clip-bad",
				ParentID: "ch-root",
				Level:    "clip",
				Title:    "Rejected Thumb",
				Fields: map[string]json.RawMessage{
					"cast": rawJSON(t, []map[string]any{{"name": "Clara Park", "thumb": bad}}),
				},
			},
		})
		if err == nil {
			t.Fatalf("expected error for invalid person thumb %q", bad)
		}
	}
}

func TestSearchExactTitleRanksHighRatingOverSparseStubs(t *testing.T) {
	ctx := context.Background()
	svc, _ := openTestService(t, Options{})

	decl := sampleStudioClipDecl()
	if _, err := svc.UpsertKind(ctx, "generic.clips", 99, decl, false); err != nil {
		t.Fatal(err)
	}
	items := []core.KindItem{
		{ID: "ch-root", Level: "channel", Title: "Music"},
		// Richly tagged canonical entry with long fields text (worse BM25 length norm) but high rating.
		{
			ID:       "canonical",
			ParentID: "ch-root",
			Level:    "clip",
			Title:    "Back In Black",
			Rating:   9.2,
			Fields: map[string]json.RawMessage{
				"notes": rawJSON(t, "hard rock classic rock album label atlantic records 1980 australian rock anthem"),
			},
		},
	}
	// Add several sparse exact-title stubs with empty fields (better BM25 length norm) and zero rating.
	for i := 1; i <= 5; i++ {
		items = append(items, core.KindItem{
			ID:       "stub-" + string(rune('0'+i)),
			ParentID: "ch-root",
			Level:    "clip",
			Title:    "Back In Black",
			Rating:   0,
		})
	}
	if _, err := svc.UpsertItems(ctx, "generic.clips", "clip", items); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}
	// Even under a tight Limit=2, the high-rated canonical exact match must come first (not truncated by BM25).
	res, err := svc.Search(ctx, SearchQuery{LibraryID: 99, Query: "Back In Black", Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 || res.Items[0].ID != "canonical" {
		t.Fatalf("expected canonical high-rated item first under tight limit, got %+v", res.Items)
	}
}
