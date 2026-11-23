package pluginitems

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestFacetBucketsCollectionsPeopleAndAddedAtProjection(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s, _ := openTestService(t, Options{Now: func() time.Time { return now }})

	decl := plugins.MediaKindMeta{
		Key:   "game",
		Label: "Video Games",
		Library: plugins.KindLibraryDecl{
			Name: "Video Games",
			Card: "landscape",
			Tabs: []plugins.KindLibraryTab{
				{Key: "recommended", Label: "Recommended", Kind: "recommended"},
				{Key: "library", Label: "Library", Kind: "library"},
				{Key: "developers", Label: "Developers", Kind: "collections", Facet: "developers", Order: "name"},
				{Key: "cast", Label: "Voice cast", Kind: "people", Facet: "voiceCast", Order: "name"},
			},
		},
		Levels: []plugins.KindLevel{
			{Key: "game", Label: "Game", Playable: true},
		},
		Fields: []plugins.KindField{
			{Key: "platforms", Type: "strings", Label: "Platforms", Indexed: true},
			{Key: "developers", Type: "people", Label: "Developers", Indexed: true},
			{Key: "voiceCast", Type: "people", Label: "Voice cast", Indexed: true},
			{Key: "genres", Type: "strings", Label: "Genres", Indexed: true},
			{Key: "playtime", Type: "duration", Label: "Playtime", Indexed: true},
			{Key: "players", Type: "int", Label: "Players", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:  []string{"released", "added", "title", "year", "rating", "index"},
			Facets: []string{"platforms", "developers", "voiceCast", "genres", "released", "playtime", "players"},
			Search: true,
		},
		Sync: plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}

	if _, err := s.UpsertKind(ctx, "community.games", 77, decl, false); err != nil {
		t.Fatal(err)
	}
	kb := s.KindBackends()

	rel2024 := time.Date(2024, 5, 10, 0, 0, 0, 0, time.UTC).Unix()
	rel2025Early := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC).Unix()
	rel2025Late := time.Date(2025, 9, 15, 0, 0, 0, 0, time.UTC).Unix()

	items := []core.KindItem{
		{
			ID:       "g1",
			Level:    "game",
			Index:    1,
			Title:    "Chrono Quest",
			Year:     2024,
			Released: rel2024,
			Rating:   8.0,
			Playable: true,
			Art:      []core.KindArtRef{{Role: "thumb", URL: "https://cdn.example/g1-thumb.jpg"}},
			Fields: map[string]json.RawMessage{
				"platforms":  rawJSON(t, []string{"SNES"}),
				"developers": rawJSON(t, []string{"Square"}),
				"voiceCast": rawJSON(t, []map[string]any{
					{"name": "Laura Bailey", "role": "Heroine", "thumb": "https://cdn.example/cast/laura.jpg"},
					{"name": "ashley Johnson", "role": "Scout"},
				}),
				"genres":   rawJSON(t, []string{"RPG", "Adventure"}),
				"playtime": rawJSON(t, 480),
				"players":  rawJSON(t, 1),
			},
		},
		{
			ID:       "g2",
			Level:    "game",
			Index:    2,
			Title:    "Galaxy Racers",
			Year:     2025,
			Released: rel2025Late,
			Rating:   9.2,
			Playable: true,
			Art:      []core.KindArtRef{{Role: "thumb", URL: "https://cdn.example/g2-thumb.jpg"}},
			Fields: map[string]json.RawMessage{
				"platforms":  rawJSON(t, []string{"SNES", "GBA"}),
				"developers": rawJSON(t, []string{"Nintendo"}),
				"voiceCast": rawJSON(t, []map[string]any{
					{"name": "Laura Bailey", "role": "Captain"},
					{"name": "Troy Baker", "role": "Pilot", "thumb": "https://cdn.example/cast/troy.jpg"},
				}),
				"genres":   rawJSON(t, []string{"arcade", "Racing"}),
				"playtime": rawJSON(t, 1500),
				"players":  rawJSON(t, 4),
			},
		},
		{
			ID:       "g3",
			Level:    "game",
			Index:    3,
			Title:    "Star Puzzle",
			Year:     2025,
			Released: rel2025Early,
			Rating:   7.5,
			Playable: true,
			Art:      []core.KindArtRef{{Role: "backdrop", URL: "https://cdn.example/g3-bg.jpg"}},
			Fields: map[string]json.RawMessage{
				"platforms":  rawJSON(t, []string{"SNES"}),
				"developers": rawJSON(t, []string{"Nintendo"}),
				"voiceCast": rawJSON(t, []map[string]any{
					{"name": "Laura Bailey", "role": "Narrator"},
					{"name": "ashley Johnson", "role": "Engineer", "thumb": "https://cdn.example/cast/ashley.jpg"},
				}),
				"genres":   rawJSON(t, []string{"Puzzle"}),
				"playtime": rawJSON(t, 5400),
				"players":  rawJSON(t, 2),
			},
		},
	}
	if _, err := kb.KindUpsert(ctx, "community.games", "game", items); err != nil {
		t.Fatal(err)
	}

	// Verify AddedAt is scanned and mapped to PluginAddedAt without touching Title.AddedAt.
	storedG1, _, err := s.GetByExternal(ctx, "community.games", "game", "g1")
	if err != nil {
		t.Fatal(err)
	}
	if storedG1.AddedAt != now.Unix() {
		t.Fatalf("stored AddedAt = %d, want %d", storedG1.AddedAt, now.Unix())
	}
	proj := Project(storedG1, decl, core.Presentations{})
	if proj.PluginAddedAt != now.Unix() || !proj.AddedAt.IsZero() {
		t.Fatalf("Project PluginAddedAt=%d AddedAt=%v, want %d and zero", proj.PluginAddedAt, proj.AddedAt, now.Unix())
	}

	// 1. Discrete date facet ("released") -> UTC year buckets + Browse filter.
	relBuckets, err := s.FacetBuckets(ctx, 77, "game", nil, "released")
	if err != nil {
		t.Fatal(err)
	}
	if len(relBuckets) != 2 || relBuckets[0].ID != "2025" || relBuckets[0].Count != 2 || relBuckets[1].ID != "2024" || relBuckets[1].Count != 1 {
		t.Fatalf("released buckets = %+v", relBuckets)
	}
	relBrowse, err := s.Browse(ctx, BrowseQuery{
		LibraryID: 77,
		Level:     "game",
		Facets:    map[string][]string{"released": {"2025"}},
		Sort:      "released:desc",
		Limit:     10,
	})
	if err != nil || len(relBrowse.Items) != 2 || relBrowse.Items[0].ID != "g2" || relBrowse.Items[1].ID != "g3" {
		t.Fatalf("released=2025 browse = %+v, err=%v", relBrowse, err)
	}

	// 2. Discrete duration buckets + Browse filter.
	durBuckets, err := s.FacetBuckets(ctx, 77, "game", nil, "playtime")
	if err != nil {
		t.Fatal(err)
	}
	if len(durBuckets) != 3 || durBuckets[0].ID != "under-10m" || durBuckets[0].Label != "Under 10 min" || durBuckets[1].ID != "10-30m" || durBuckets[2].ID != "1-2h" {
		t.Fatalf("playtime buckets = %+v", durBuckets)
	}
	durBrowse, err := s.Browse(ctx, BrowseQuery{
		LibraryID: 77,
		Level:     "game",
		Facets:    map[string][]string{"playtime": {"10-30m", "1-2h"}},
		Sort:      "index:asc",
		Limit:     10,
	})
	if err != nil || len(durBrowse.Items) != 2 || durBrowse.Items[0].ID != "g2" || durBrowse.Items[1].ID != "g3" {
		t.Fatalf("playtime OR browse = %+v, err=%v", durBrowse, err)
	}

	// 3. FacetCollections: default Order="count" vs Order="name" (case-insensitive alphabetical), paged O(page), NewestItem hydrated with art.
	fcPage1, err := s.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: 77,
		Level:     "game",
		FieldKey:  "platforms",
		Offset:    0,
		Limit:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fcPage1.More || len(fcPage1.Items) != 1 {
		t.Fatalf("FacetCollections page1 = %+v", fcPage1)
	}
	if fcPage1.Items[0].ID != "SNES" || fcPage1.Items[0].Count != 3 || fcPage1.Items[0].NewestItem == nil || fcPage1.Items[0].NewestItem.ID != "g2" {
		t.Fatalf("expected SNES count=3 newest=g2, got %+v", fcPage1.Items[0])
	}
	if len(fcPage1.Items[0].NewestItem.Art) != 1 || fcPage1.Items[0].NewestItem.Art[0].URL != "https://cdn.example/g2-thumb.jpg" {
		t.Fatalf("expected hydrated art on NewestItem, got %+v", fcPage1.Items[0].NewestItem.Art)
	}

	// Case-insensitive alphabetical order on genres ("Adventure", "arcade", "Puzzle", "Racing", "RPG").
	fcGenresByName, err := s.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: 77,
		Level:     "game",
		FieldKey:  "genres",
		Order:     "name",
		Offset:    0,
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantGenreOrder := []string{"Adventure", "arcade", "Puzzle", "Racing", "RPG"}
	if len(fcGenresByName.Items) != len(wantGenreOrder) {
		t.Fatalf("genres by name = %+v", fcGenresByName.Items)
	}
	for i, want := range wantGenreOrder {
		if fcGenresByName.Items[i].Label != want || fcGenresByName.Items[i].NewestItem == nil {
			t.Fatalf("genres[%d] = %+v, want label %q with NewestItem", i, fcGenresByName.Items[i], want)
		}
	}

	fcDevs, err := s.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: 77,
		Level:     "game",
		FieldKey:  "developers",
		Offset:    0,
		Limit:     5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fcDevs.More || len(fcDevs.Items) != 2 || fcDevs.Items[0].Label != "Nintendo" || fcDevs.Items[0].Count != 2 || fcDevs.Items[0].NewestItem == nil || fcDevs.Items[0].NewestItem.ID != "g2" {
		t.Fatalf("FacetCollections developers = %+v", fcDevs)
	}

	// 4. FacetPeople on voiceCast: alphabetical case-insensitive default + Order="count" + batched headshot sample selection.
	fpByName, err := s.FacetPeople(ctx, FacetPeopleQuery{
		LibraryID: 77,
		Level:     "game",
		FieldKey:  "voiceCast",
		Order:     "name",
		Offset:    0,
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fpByName.More || len(fpByName.Items) != 3 {
		t.Fatalf("FacetPeople by name = %+v", fpByName)
	}
	// Case-insensitive alphabetical: "ashley Johnson" (2), "Laura Bailey" (3), "Troy Baker" (1).
	if fpByName.Items[0].Name != "ashley Johnson" || fpByName.Items[0].Count != 2 || fpByName.Items[0].Thumb != "https://cdn.example/cast/ashley.jpg" || fpByName.Items[0].SampleItem == nil || fpByName.Items[0].SampleItem.ID != "g3" {
		t.Fatalf("FacetPeople[0] = %+v, want ashley Johnson count=2 thumb from g3", fpByName.Items[0])
	}
	// Laura Bailey appears on g2 (newer, no thumb) and g1 (older, has thumb); r.rn <= 4 picks g1 so her headshot is preserved!
	if fpByName.Items[1].Name != "Laura Bailey" || fpByName.Items[1].Count != 3 || fpByName.Items[1].Thumb != "https://cdn.example/cast/laura.jpg" || fpByName.Items[1].SampleItem == nil || fpByName.Items[1].SampleItem.ID != "g1" {
		t.Fatalf("FacetPeople[1] = %+v, want Laura Bailey count=3 thumb from g1", fpByName.Items[1])
	}
	if fpByName.Items[2].Name != "Troy Baker" || fpByName.Items[2].Count != 1 || fpByName.Items[2].Thumb != "https://cdn.example/cast/troy.jpg" || fpByName.Items[2].SampleItem == nil || fpByName.Items[2].SampleItem.ID != "g2" {
		t.Fatalf("FacetPeople[2] = %+v, want Troy Baker count=1 thumb from g2", fpByName.Items[2])
	}
	for _, fp := range fpByName.Items {
		if !core.IsPluginPersonID(fp.PersonID) {
			t.Fatalf("FacetPeople PersonID %d must satisfy IsPluginPersonID", fp.PersonID)
		}
		pm, ok, err := s.PersonByID(ctx, 77, nil, fp.PersonID, 10)
		if err != nil || !ok || pm.CreditCount != fp.Count {
			t.Fatalf("PersonByID(%d) = (%+v, %v, %v), want ok with count %d", fp.PersonID, pm, ok, err, fp.Count)
		}
	}

	fpByCount, err := s.FacetPeople(ctx, FacetPeopleQuery{
		LibraryID: 77,
		Level:     "game",
		FieldKey:  "voiceCast",
		Order:     "count",
		Offset:    0,
		Limit:     2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fpByCount.More || len(fpByCount.Items) != 2 || fpByCount.Items[0].Name != "Laura Bailey" || fpByCount.Items[1].Name != "ashley Johnson" {
		t.Fatalf("FacetPeople by count = %+v", fpByCount)
	}

	// 5. Int facet > 50 distinct values produces discrete range buckets and filters cleanly.
	var many []core.KindItem
	for i := 1; i <= 55; i++ {
		many = append(many, core.KindItem{
			ID:       fmt.Sprintf("m%d", i),
			Level:    "game",
			Index:    100 + i,
			Title:    fmt.Sprintf("Arcade Title %d", i),
			Playable: true,
			Fields: map[string]json.RawMessage{
				"players": rawJSON(t, i*2),
			},
		})
	}
	if _, err := kb.KindUpsert(ctx, "community.games", "game", many); err != nil {
		t.Fatal(err)
	}
	intBuckets, err := s.FacetBuckets(ctx, 77, "game", nil, "players")
	if err != nil || len(intBuckets) == 0 || len(intBuckets) > 10 {
		t.Fatalf("expected ranged int buckets when >50 distinct values, got len=%d %+v err=%v", len(intBuckets), intBuckets, err)
	}
	rangeBrowse, err := s.Browse(ctx, BrowseQuery{
		LibraryID: 77,
		Level:     "game",
		Facets:    map[string][]string{"players": {intBuckets[0].ID}},
		Limit:     200,
	})
	if err != nil || len(rangeBrowse.Items) != intBuckets[0].Count {
		t.Fatalf("range browse count = %d, want bucket count %d (bucket=%+v, err=%v)", len(rangeBrowse.Items), intBuckets[0].Count, intBuckets[0], err)
	}
}

func TestFacetCollectionsScaleQueryCount(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})

	decl := plugins.MediaKindMeta{
		Key:   "game",
		Label: "Games",
		Library: plugins.KindLibraryDecl{
			Name: "Games",
			Card: "landscape",
			Tabs: []plugins.KindLibraryTab{
				{Key: "developers", Label: "Developers", Kind: "collections", Facet: "developers", Order: "name"},
			},
		},
		Levels: []plugins.KindLevel{{Key: "game", Label: "Game", Playable: true}},
		Fields: []plugins.KindField{
			{Key: "developers", Type: "strings", Label: "Developers", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:  []string{"released", "title", "added"},
			Facets: []string{"developers"},
			Search: true,
		},
		Sync: plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
	if _, err := s.UpsertKind(ctx, "community.scale", 88, decl, false); err != nil {
		t.Fatal(err)
	}

	const totalItems = 20000
	const totalBuckets = 300
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	itemStmt, err := tx.PrepareContext(ctx, `INSERT INTO plugin_items (
		item_ref, seq, plugin_id, kind_key, library_id, external_id,
		parent_ref, level, idx, title, sort_title, summary,
		year, released, rating, playable, child_count, leaf_count,
		unavailable, hints, fields, etag, added_at, updated_at, seen_at, missed_walks
	) VALUES (?, ?, 'community.scale', 'game', 88, ?, '', 'game', ?, ?, ?, '', 2025, ?, 8.0, 1, 0, 0, '', '{}', '{}', 'e1', ?, ?, ?, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	defer itemStmt.Close()
	fieldStmt, err := tx.PrepareContext(ctx, `INSERT INTO plugin_item_fields (item_ref, field, v_text, v_int, v_real) VALUES (?, 'developers', ?, 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	defer fieldStmt.Close()
	artStmt, err := tx.PrepareContext(ctx, `INSERT INTO plugin_item_art (item_ref, role, url, width, height) VALUES (?, 'thumb', ?, 1280, 720)`)
	if err != nil {
		t.Fatal(err)
	}
	defer artStmt.Close()

	baseReleased := int64(1700000000)
	for i := 1; i <= totalItems; i++ {
		ref := fmt.Sprintf("plugin:community.scale:game:g%d", i)
		extID := fmt.Sprintf("g%d", i)
		title := fmt.Sprintf("Game %05d", i)
		dev := fmt.Sprintf("Studio %03d", (i-1)%totalBuckets)
		rel := baseReleased + int64(i)
		if _, err := itemStmt.ExecContext(ctx, ref, int64(100000+i), extID, i, title, strings.ToLower(title), rel, rel, rel, rel); err != nil {
			t.Fatal(err)
		}
		if _, err := fieldStmt.ExecContext(ctx, ref, dev); err != nil {
			t.Fatal(err)
		}
		if _, err := artStmt.ExecContext(ctx, ref, fmt.Sprintf("https://cdn.example/thumb/%d.jpg", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Capture EXPLAIN QUERY PLAN on the newest-item ranked CTE for 100 buckets.
	holders := make([]string, 100)
	args := make([]any, 0, 103)
	args = append(args, "developers")
	for i := 0; i < 100; i++ {
		holders[i] = "?"
		args = append(args, fmt.Sprintf("Studio %03d", i))
	}
	args = append(args, int64(88), "game")
	planSQL := `EXPLAIN QUERY PLAN
		WITH ranked AS (
			SELECT f.v_text AS bucket_val, i.item_ref,
				ROW_NUMBER() OVER (
					PARTITION BY f.v_text
					ORDER BY i.released DESC, i.added_at DESC, i.sort_title ASC, i.item_ref ASC
				) AS rn
			From plugin_item_fields f
			JOIN plugin_items i ON i.item_ref = f.item_ref
			WHERE f.field = ? AND f.v_text IN (` + strings.Join(holders, ",") + `)
				AND i.library_id + 0 = ? AND i.level = ?
		)
		SELECT r.bucket_val, i.item_ref FROM ranked r
		JOIN plugin_items i ON i.item_ref = r.item_ref
		WHERE r.rn = 1`
	if store.DialectOf(db) == store.SQLite {
		pRows, err := db.QueryContext(ctx, planSQL, args...)
		if err != nil {
			t.Fatal(err)
		}
		var planParts []string
		for pRows.Next() {
			var id, parent, notused int
			var detail string
			if err := pRows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			planParts = append(planParts, detail)
		}
		_ = pRows.Close()
		t.Logf("EXPLAIN QUERY PLAN: %s", strings.Join(planParts, " | "))
	}

	for _, order := range []string{"count", "name"} {
		mCtx, stats := store.WithQueryStats(ctx)
		page, err := s.FacetCollections(mCtx, FacetCollectionsQuery{
			LibraryID: 88,
			Level:     "game",
			FieldKey:  "developers",
			Order:     order,
			Offset:    0,
			Limit:     100,
		})
		if err != nil {
			t.Fatalf("FacetCollections(%s): %v", order, err)
		}
		if !page.More || len(page.Items) != 100 {
			t.Fatalf("FacetCollections(%s) len=%d more=%v, want 100 and more=true", order, len(page.Items), page.More)
		}
		for idx, item := range page.Items {
			if item.NewestItem == nil || len(item.NewestItem.Art) == 0 {
				t.Fatalf("FacetCollections(%s)[%d] missing hydrated NewestItem/Art: %+v", order, idx, item)
			}
		}
		if stats.Queries() > 6 {
			t.Fatalf("FacetCollections(%s) executed %d SQL queries, want <= 6 (O(1) queries per page)", order, stats.Queries())
		}
	}
}

func TestMigration4BackfillsFoldedFieldsInBoundedBatches(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)

	if store.DialectOf(db) != store.SQLite {
		t.Skip("SQLite v3->v4 migration test")
	}
	oldBatch := migration4FoldBatchSize
	migration4FoldBatchSize = 2
	t.Cleanup(func() { migration4FoldBatchSize = oldBatch })

	decl := plugins.MediaKindMeta{
		Key:     "game",
		Label:   "Games",
		Library: plugins.KindLibraryDecl{Name: "Games"},
		Levels:  []plugins.KindLevel{{Key: "game", Label: "Game", Playable: true}},
		Fields: []plugins.KindField{
			{Key: "developers", Type: "strings", Label: "Developers", Indexed: true},
			{Key: "voiceCast", Type: "people", Label: "Voice cast", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{Facets: []string{"developers", "voiceCast"}},
		Sync:   plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
	declJSON, err := json.Marshal(decl)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate pre-v4 schema: plugin_item_fields without v_fold and version 4 unapplied.
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS plugin_item_fields`,
		`CREATE TABLE plugin_item_fields (
			item_ref TEXT NOT NULL REFERENCES plugin_items(item_ref) ON DELETE CASCADE,
			field    TEXT NOT NULL,
			v_text   TEXT NOT NULL DEFAULT '',
			v_int    INTEGER NOT NULL DEFAULT 0,
			v_real   REAL NOT NULL DEFAULT 0.0
		)`,
		`CREATE INDEX plugin_item_fields_lookup ON plugin_item_fields(field, v_text, item_ref)`,
		`CREATE INDEX plugin_item_fields_lookup_int ON plugin_item_fields(field, v_int, item_ref)`,
		`CREATE INDEX plugin_item_fields_item ON plugin_item_fields(item_ref)`,
		`DELETE FROM schema_migrations WHERE component = 'pluginitems' AND version = 4`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("setup v3 state (%s): %v", stmt, err)
		}
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO plugin_kinds(plugin_id, kind_key, library_id, decl, decl_hash, schema_version, fts_version)
		VALUES ('community.games', 'game', 77, ?, ?, 1, ?)
	`, string(declJSON), decl.DeclHash(), currentFTSVersion); err != nil {
		t.Fatal(err)
	}

	ref1 := string(core.NewItemRef("community.games", "game", "g1"))
	ref2 := string(core.NewItemRef("community.games", "game", "g2"))
	for idx, ref := range []string{ref1, ref2} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO plugin_items(
				item_ref, seq, plugin_id, kind_key, library_id, external_id, parent_ref,
				level, idx, title, sort_title, summary, year, released, rating, playable,
				child_count, leaf_count, hints, fields, etag, added_at, updated_at, seen_at, missed_walks, unavailable
			) VALUES (?, ?, 'community.games', 'game', 77, ?, '', 'game', ?, ?, ?, '', 2025, 1700000000, 8.0, 1, 0, 0, '{}', '{}', 'e1', 1700000000, 1700000000, 1700000000, 0, '')
		`, ref, idx+1, fmt.Sprintf("g%d", idx+1), idx+1, fmt.Sprintf("Game %d", idx+1), fmt.Sprintf("game %d", idx+1)); err != nil {
			t.Fatal(err)
		}
	}

	zoeID := core.PluginPersonID("Zoë Wanamaker")
	joID := core.PluginPersonID("Mary Jones")
	preRows := []struct {
		ref   string
		field string
		text  string
		vint  int64
	}{
		{ref1, "developers", "Zoë Mode", 0},
		{ref1, "developers", "Jo Example Studios", 0},
		{ref2, "developers", "Mary Jones Interactive", 0},
		{ref1, "voiceCast", "Zoë Wanamaker", zoeID},
		{ref2, "voiceCast", "Mary Jones", joID},
	}
	for _, r := range preRows {
		if _, err := db.ExecContext(ctx, `INSERT INTO plugin_item_fields(item_ref, field, v_text, v_int, v_real) VALUES (?, ?, ?, ?, 0)`, r.ref, r.field, r.text, r.vint); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate v3->v4: %v", err)
	}

	svc, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	fc, err := svc.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: 77,
		Level:     "game",
		FieldKey:  "developers",
		Order:     "name",
		Query:     "zoe",
		Limit:     10,
	})
	if err != nil || len(fc.Items) != 1 || fc.Items[0].Label != "Zoë Mode" {
		t.Fatalf("backfilled FacetCollections(zoe) = %+v, err=%v", fc, err)
	}

	fp, err := svc.FacetPeople(ctx, FacetPeopleQuery{
		LibraryID: 77,
		Level:     "game",
		FieldKey:  "voiceCast",
		Order:     "name",
		Query:     "jo",
		Limit:     10,
	})
	if err != nil || len(fp.Items) != 1 || fp.Items[0].Name != "Mary Jones" {
		t.Fatalf("backfilled FacetPeople(jo) = %+v, err=%v", fp, err)
	}
}

func TestTabSearchFoldingCollectionsAndPeople(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})

	decl := plugins.MediaKindMeta{
		Key:     "game",
		Label:   "Games",
		Library: plugins.KindLibraryDecl{Name: "Games", Card: "landscape"},
		Levels:  []plugins.KindLevel{{Key: "game", Label: "Game", Playable: true}},
		Fields: []plugins.KindField{
			{Key: "developers", Type: "strings", Label: "Developers", Indexed: true},
			{Key: "voiceCast", Type: "people", Label: "Voice cast", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:  []string{"released", "title"},
			Facets: []string{"developers", "voiceCast"},
			Search: true,
		},
		Sync: plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
	if _, err := s.UpsertKind(ctx, "community.games", 77, decl, false); err != nil {
		t.Fatal(err)
	}

	kb := s.KindBackends()
	_, err := kb.KindUpsert(ctx, "community.games", "game", []core.KindItem{
		{
			ID:       "g1",
			Level:    "game",
			Index:    1,
			Title:    "Echoes of Zoë",
			Released: time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC).Unix(),
			Playable: true,
			Art:      []core.KindArtRef{{Role: "thumb", URL: "https://cdn.example/g1.jpg"}},
			Fields: map[string]json.RawMessage{
				"developers": rawJSON(t, []string{"Zoë Mode", "Mary Jones Interactive", "Major Games"}),
				"voiceCast": rawJSON(t, []map[string]string{
					{"name": "Zoë Wanamaker", "thumb": "https://cdn.example/cast/zoe.jpg"},
					{"name": "Mary Jones", "thumb": "https://cdn.example/cast/mary.jpg"},
					{"name": "Major Nelson"},
				}),
			},
		},
		{
			ID:       "g2",
			Level:    "game",
			Index:    2,
			Title:    "Galaxy Racers",
			Released: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC).Unix(),
			Playable: true,
			Art:      []core.KindArtRef{{Role: "thumb", URL: "https://cdn.example/g2.jpg"}},
			Fields: map[string]json.RawMessage{
				"developers": rawJSON(t, []string{"Mary Jones Interactive", "Jo Example Studios"}),
				"voiceCast": rawJSON(t, []map[string]string{
					{"name": "Mary Jones"},
					{"name": "Jo Example", "thumb": "https://cdn.example/cast/jo.jpg"},
				}),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. FacetCollections (string branch): diacritic "zoe" -> "Zoë Mode".
	fcZoe, err := s.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: 77, Level: "game", FieldKey: "developers", Order: "name", Query: "zoe", Limit: 10,
	})
	if err != nil || fcZoe.More || len(fcZoe.Items) != 1 || fcZoe.Items[0].Label != "Zoë Mode" || fcZoe.Items[0].NewestItem == nil {
		t.Fatalf("FacetCollections string diacritic = %+v, err=%v", fcZoe, err)
	}

	// Word-prefix "jo" matches "Jo Example Studios" (1) and "Mary Jones Interactive" (2), NOT "Major Games"; preserves count & name order and pages.
	fcJoCountP1, err := s.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: 77, Level: "game", FieldKey: "developers", Order: "count", Query: "jo", Offset: 0, Limit: 1,
	})
	if err != nil || !fcJoCountP1.More || len(fcJoCountP1.Items) != 1 || fcJoCountP1.Items[0].Label != "Mary Jones Interactive" || fcJoCountP1.Items[0].Count != 2 {
		t.Fatalf("FacetCollections string jo count p1 = %+v, err=%v", fcJoCountP1, err)
	}
	fcJoCountP2, err := s.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: 77, Level: "game", FieldKey: "developers", Order: "count", Query: "jo", Offset: 1, Limit: 1,
	})
	if err != nil || fcJoCountP2.More || len(fcJoCountP2.Items) != 1 || fcJoCountP2.Items[0].Label != "Jo Example Studios" || fcJoCountP2.Items[0].Count != 1 {
		t.Fatalf("FacetCollections string jo count p2 = %+v, err=%v", fcJoCountP2, err)
	}
	fcJoName, err := s.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: 77, Level: "game", FieldKey: "developers", Order: "name", Query: "jo", Limit: 10,
	})
	if err != nil || len(fcJoName.Items) != 2 || fcJoName.Items[0].Label != "Jo Example Studios" || fcJoName.Items[1].Label != "Mary Jones Interactive" {
		t.Fatalf("FacetCollections string jo name = %+v, err=%v", fcJoName, err)
	}

	// 2. FacetCollections (people branch): diacritic "zoe" -> "Zoë Wanamaker", word-prefix "jo" excludes "Major Nelson".
	fcPeopleZoe, err := s.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: 77, Level: "game", FieldKey: "voiceCast", Order: "name", Query: "zoe", Limit: 10,
	})
	if err != nil || len(fcPeopleZoe.Items) != 1 || fcPeopleZoe.Items[0].Label != "Zoë Wanamaker" {
		t.Fatalf("FacetCollections people diacritic = %+v, err=%v", fcPeopleZoe, err)
	}
	fcPeopleJo, err := s.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: 77, Level: "game", FieldKey: "voiceCast", Order: "count", Query: "jo", Limit: 10,
	})
	if err != nil || len(fcPeopleJo.Items) != 2 || fcPeopleJo.Items[0].Label != "Mary Jones" || fcPeopleJo.Items[1].Label != "Jo Example" {
		t.Fatalf("FacetCollections people jo count = %+v, err=%v", fcPeopleJo, err)
	}

	// 3. FacetPeople: diacritic "zoe" -> "Zoë Wanamaker"; word-prefix "jo" -> ["Jo Example", "Mary Jones"] (name) / ["Mary Jones", "Jo Example"] (count) with paging.
	fpZoe, err := s.FacetPeople(ctx, FacetPeopleQuery{
		LibraryID: 77, Level: "game", FieldKey: "voiceCast", Order: "name", Query: "zoe", Limit: 10,
	})
	if err != nil || fpZoe.More || len(fpZoe.Items) != 1 || fpZoe.Items[0].Name != "Zoë Wanamaker" || fpZoe.Items[0].Thumb != "https://cdn.example/cast/zoe.jpg" {
		t.Fatalf("FacetPeople diacritic = %+v, err=%v", fpZoe, err)
	}
	fpJoNameP1, err := s.FacetPeople(ctx, FacetPeopleQuery{
		LibraryID: 77, Level: "game", FieldKey: "voiceCast", Order: "name", Query: "jo", Offset: 0, Limit: 1,
	})
	if err != nil || !fpJoNameP1.More || len(fpJoNameP1.Items) != 1 || fpJoNameP1.Items[0].Name != "Jo Example" {
		t.Fatalf("FacetPeople jo name p1 = %+v, err=%v", fpJoNameP1, err)
	}
	fpJoNameP2, err := s.FacetPeople(ctx, FacetPeopleQuery{
		LibraryID: 77, Level: "game", FieldKey: "voiceCast", Order: "name", Query: "jo", Offset: 1, Limit: 1,
	})
	if err != nil || fpJoNameP2.More || len(fpJoNameP2.Items) != 1 || fpJoNameP2.Items[0].Name != "Mary Jones" || fpJoNameP2.Items[0].Count != 2 {
		t.Fatalf("FacetPeople jo name p2 = %+v, err=%v", fpJoNameP2, err)
	}
	fpJoCount, err := s.FacetPeople(ctx, FacetPeopleQuery{
		LibraryID: 77, Level: "game", FieldKey: "voiceCast", Order: "count", Query: "jo", Limit: 10,
	})
	if err != nil || len(fpJoCount.Items) != 2 || fpJoCount.Items[0].Name != "Mary Jones" || fpJoCount.Items[1].Name != "Jo Example" {
		t.Fatalf("FacetPeople jo count = %+v, err=%v", fpJoCount, err)
	}
}

func TestTabSearchScale35k(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})

	decl := plugins.MediaKindMeta{
		Key:   "game",
		Label: "Games",
		Library: plugins.KindLibraryDecl{
			Name: "Games",
			Card: "landscape",
			Tabs: []plugins.KindLibraryTab{
				{Key: "developers", Label: "Developers", Kind: "collections", Facet: "developers", Order: "name"},
				{Key: "cast", Label: "Voice cast", Kind: "people", Facet: "voiceCast", Order: "name"},
			},
		},
		Levels: []plugins.KindLevel{{Key: "game", Label: "Game", Playable: true}},
		Fields: []plugins.KindField{
			{Key: "developers", Type: "strings", Label: "Developers", Indexed: true},
			{Key: "voiceCast", Type: "people", Label: "Voice cast", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:  []string{"released", "title", "added"},
			Facets: []string{"developers", "voiceCast"},
			Search: true,
		},
		Sync: plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
	if _, err := s.UpsertKind(ctx, "community.scale35k", 99, decl, false); err != nil {
		t.Fatal(err)
	}

	const totalItems = 35000
	const distinctValues = 15000

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	itemStmt, err := tx.PrepareContext(ctx, `INSERT INTO plugin_items (
		item_ref, seq, plugin_id, kind_key, library_id, external_id,
		parent_ref, level, idx, title, sort_title, summary,
		year, released, rating, playable, child_count, leaf_count,
		unavailable, hints, fields, etag, added_at, updated_at, seen_at, missed_walks
	) VALUES (?, ?, 'community.scale35k', 'game', 99, ?, '', 'game', ?, ?, ?, '', 2025, ?, 8.0, 1, 0, 0, '', '{}', ?, 'e1', ?, ?, ?, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	defer itemStmt.Close()
	fieldStmt, err := tx.PrepareContext(ctx, `INSERT INTO plugin_item_fields (item_ref, field, v_text, v_int, v_real, v_fold) VALUES (?, ?, ?, ?, 0, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer fieldStmt.Close()
	artStmt, err := tx.PrepareContext(ctx, `INSERT INTO plugin_item_art (item_ref, role, url, width, height) VALUES (?, 'thumb', ?, 1280, 720)`)
	if err != nil {
		t.Fatal(err)
	}
	defer artStmt.Close()

	devs := make([]string, distinctValues)
	devFolds := make([]string, distinctValues)
	actors := make([]string, distinctValues)
	actorFolds := make([]string, distinctValues)
	actorIDs := make([]int64, distinctValues)
	for b := 0; b < distinctValues; b++ {
		switch {
		case b < 60:
			devs[b] = fmt.Sprintf("Zoë Studio %04d", b)
			actors[b] = fmt.Sprintf("Zoë Actor %04d", b)
		case b < 180:
			devs[b] = fmt.Sprintf("Mary Jones %04d", b)
			actors[b] = fmt.Sprintf("Mary Jones %04d", b)
		default:
			devs[b] = fmt.Sprintf("Developer %05d", b)
			actors[b] = fmt.Sprintf("Voice actor %05d", b)
		}
		devFolds[b] = FoldFieldText(devs[b])
		actorFolds[b] = FoldFieldText(actors[b])
		actorIDs[b] = core.PluginPersonID(actors[b])
	}

	baseReleased := int64(1700000000)
	for i := 1; i <= totalItems; i++ {
		b := (i - 1) % distinctValues
		ref := fmt.Sprintf("plugin:community.scale35k:game:g%d", i)
		extID := fmt.Sprintf("g%d", i)
		title := fmt.Sprintf("Game %05d", i)
		rel := baseReleased + int64(i)
		fieldsJSON := fmt.Sprintf(`{"voiceCast":[{"name":%q,"thumb":"https://cdn.example/cast/%d.jpg"}]}`, actors[b], b)
		if _, err := itemStmt.ExecContext(ctx, ref, int64(200000+i), extID, i, title, strings.ToLower(title), rel, fieldsJSON, rel, rel, rel); err != nil {
			t.Fatal(err)
		}
		if _, err := fieldStmt.ExecContext(ctx, ref, "developers", devs[b], 0, devFolds[b]); err != nil {
			t.Fatal(err)
		}
		if _, err := fieldStmt.ExecContext(ctx, ref, "voiceCast", actors[b], actorIDs[b], actorFolds[b]); err != nil {
			t.Fatal(err)
		}
		if _, err := artStmt.ExecContext(ctx, ref, fmt.Sprintf("https://cdn.example/thumb/%d.jpg", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if store.DialectOf(db) == store.SQLite {
		explainPlan := func(sqlText string, args ...any) string {
			pRows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+sqlText, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer pRows.Close()
			var parts []string
			for pRows.Next() {
				var id, parent, notused int
				var detail string
				if err := pRows.Scan(&id, &parent, &notused, &detail); err != nil {
					t.Fatal(err)
				}
				parts = append(parts, detail)
			}
			return strings.Join(parts, " | ")
		}

		colPlan := explainPlan(`
			SELECT f.v_text, COUNT(DISTINCT i.item_ref) AS c
			FROM plugin_item_fields f
			JOIN plugin_items i ON i.item_ref = f.item_ref
			WHERE f.field = ? AND f.v_text != '' AND f.v_fold > '' AND (f.v_fold LIKE ? OR f.v_fold LIKE ?)
			  AND i.library_id + 0 = ? AND i.level = ?
			GROUP BY f.v_text
			ORDER BY LOWER(f.v_text) ASC, f.v_text ASC
			LIMIT 51 OFFSET 0`, "developers", "zoe%", "% zoe%", int64(99), "game")
		t.Logf("Collections EXPLAIN QUERY PLAN: %s", colPlan)
		if !strings.Contains(colPlan, "plugin_item_fields_fold") {
			t.Fatalf("expected collections plan to use plugin_item_fields_fold, got: %s", colPlan)
		}

		peoplePlan := explainPlan(`
			SELECT f.v_int, MIN(f.v_text) AS name, COUNT(DISTINCT i.item_ref) AS c
			FROM plugin_item_fields f
			JOIN plugin_items i ON i.item_ref = f.item_ref
			WHERE f.field = ? AND f.v_int + 0 > 1000000000 AND f.v_text != '' AND f.v_fold > '' AND (f.v_fold LIKE ? OR f.v_fold LIKE ?)
			  AND i.library_id + 0 = ? AND i.level = ?
			GROUP BY f.v_int
			ORDER BY LOWER(MIN(f.v_text)) ASC, MIN(f.v_text) ASC, f.v_int ASC
			LIMIT 51 OFFSET 0`, "voiceCast", "jo%", "% jo%", int64(99), "game")
		t.Logf("People EXPLAIN QUERY PLAN: %s", peoplePlan)
		if !strings.Contains(peoplePlan, "plugin_item_fields_fold") {
			t.Fatalf("expected people plan to use plugin_item_fields_fold, got: %s", peoplePlan)
		}
	}

	const queryCeiling = 6
	for _, q := range []string{"zoe", "jo"} {
		for _, order := range []string{"count", "name"} {
			mCtx, stats := store.WithQueryStats(ctx)
			fc, err := s.FacetCollections(mCtx, FacetCollectionsQuery{
				LibraryID: 99,
				Level:     "game",
				FieldKey:  "developers",
				Order:     order,
				Query:     q,
				Offset:    0,
				Limit:     50,
			})
			if err != nil || len(fc.Items) != 50 || !fc.More {
				t.Fatalf("FacetCollections(35k, q=%q, order=%s) len=%d more=%v err=%v", q, order, len(fc.Items), fc.More, err)
			}
			if stats.Queries() > queryCeiling {
				t.Fatalf("FacetCollections executed %d queries, want <= %d", stats.Queries(), queryCeiling)
			}

			mCtxP, statsP := store.WithQueryStats(ctx)
			fp, err := s.FacetPeople(mCtxP, FacetPeopleQuery{
				LibraryID: 99,
				Level:     "game",
				FieldKey:  "voiceCast",
				Order:     order,
				Query:     q,
				Offset:    0,
				Limit:     50,
			})
			if err != nil || len(fp.Items) != 50 || !fp.More {
				t.Fatalf("FacetPeople(35k, q=%q, order=%s) len=%d more=%v err=%v", q, order, len(fp.Items), fp.More, err)
			}
			if statsP.Queries() > queryCeiling {
				t.Fatalf("FacetPeople executed %d queries, want <= %d", statsP.Queries(), queryCeiling)
			}
		}
	}
}
