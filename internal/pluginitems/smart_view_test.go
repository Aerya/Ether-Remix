package pluginitems

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func setupViewerStateTables(t *testing.T, db *sql.DB) {
	t.Helper()
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS state_watch (
			user_id BIGINT NOT NULL,
			rating_key BIGINT NOT NULL,
			view_offset_ns BIGINT NOT NULL DEFAULT 0,
			duration_ns BIGINT NOT NULL DEFAULT 0,
			view_count BIGINT NOT NULL DEFAULT 0,
			counted BIGINT NOT NULL DEFAULT 0,
			last_viewed_at BIGINT NOT NULL DEFAULT 0,
			user_rating DOUBLE PRECISION NOT NULL DEFAULT 0,
			updated_at BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, rating_key)
		)`,
		`CREATE INDEX IF NOT EXISTS state_watch_last_viewed ON state_watch(user_id, last_viewed_at DESC, rating_key) WHERE last_viewed_at>0`,
		`CREATE INDEX IF NOT EXISTS state_watch_inprogress ON state_watch(user_id, last_viewed_at DESC) WHERE view_offset_ns > 0`,
		`CREATE TABLE IF NOT EXISTS favorites_items (
			viewer_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			subject_id TEXT NOT NULL,
			added_at INTEGER NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (viewer_id, kind, subject_id)
		)`,
		`CREATE TABLE IF NOT EXISTS watchlist_items (
			user_id BIGINT NOT NULL,
			item_key BIGINT NOT NULL,
			source TEXT NOT NULL,
			title TEXT NOT NULL,
			added INTEGER NOT NULL,
			checked INTEGER NOT NULL DEFAULT 0,
			available INTEGER NOT NULL DEFAULT 0,
			notified INTEGER NOT NULL DEFAULT 0,
			warmed INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, item_key, source)
		)`,
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("ddl %q: %v", stmt, err)
		}
	}
}

func compileSmartViewTab(t *testing.T, ctx context.Context, s *Service, libID core.LibraryID, raw string) *libraryfilter.Filter {
	t.Helper()
	var tf core.TabFilter
	if err := json.Unmarshal([]byte(raw), &tf); err != nil {
		t.Fatalf("unmarshal tab filter: %v", err)
	}
	rules, fallback := tf.ToPolicy("")
	f, err := s.CompilePolicy(ctx, libID, rules, fallback)
	if err != nil {
		t.Fatalf("CompilePolicy(%s): %v", raw, err)
	}
	return f
}

func TestSmartViewViewerFiltersHistoryPaginationAndIsolation(t *testing.T) {
	s, db := openTestService(t, Options{})
	setupViewerStateTables(t, db)
	ctx := context.Background()

	libID := core.LibraryID(42)
	decl := plugins.MediaKindMeta{
		Key:     "doc",
		Label:   "Documentaries",
		Library: plugins.KindLibraryDecl{Name: "Documentaries"},
		Levels:  []plugins.KindLevel{{Key: "doc", Label: "Documentary", Playable: true}},
		Fields: []plugins.KindField{
			{Key: "topic", Type: "enum", Label: "Topic", Indexed: true, Enum: []string{"science", "nature", "history"}},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:  []string{"title", "added", "released"},
			Facets: []string{"topic"},
			Search: true,
		},
		Sync: plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
	if _, err := s.UpsertKind(ctx, "docs.plugin", libID, decl, false); err != nil {
		t.Fatal(err)
	}

	// Seed 240 items so SQLite's planner reflects a realistic library size.
	const totalItems = 240
	batch := make([]core.KindItem, 0, totalItems)
	topics := []string{"science", "nature", "history"}
	for i := 1; i <= totalItems; i++ {
		batch = append(batch, core.KindItem{
			ID:       fmt.Sprintf("d%03d", i),
			Level:    "doc",
			Title:    fmt.Sprintf("Documentary %03d", i),
			Playable: true,
			Year:     2000 + (i % 25),
			Fields:   map[string]json.RawMessage{"topic": json.RawMessage(fmt.Sprintf("%q", topics[i%len(topics)]))},
		})
	}
	if _, err := s.UpsertItems(ctx, "docs.plugin", "doc", batch); err != nil {
		t.Fatal(err)
	}

	keyFor := func(id string) core.RatingKey {
		t.Helper()
		ref := core.NewItemRef("docs.plugin", "doc", id)
		item, _, err := s.GetByRef(ctx, ref)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		return item.Key
	}

	t1 := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC).UnixNano()
	t2 := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC).UnixNano()
	t3 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC).UnixNano()
	t4 := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC).UnixNano()

	// Viewer 1 state:
	// - d001: watched at t1 (topic=nature)
	// - d002: in progress at t3 (view_count=0, view_offset_ns>0, topic=history)
	// - d003: watched at t4 (topic=science)
	// - d004: watched with undated import (view_count=1, last_viewed_at=0, topic=nature)
	// - d005: favorite only (no watch state)
	// - d006: watchlist only (no watch state, topic=science)
	// Viewer 2 state:
	// - d010: watched at t2
	// - d011: favorite
	// - d012: watchlist
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec(`INSERT INTO state_watch(user_id,rating_key,view_offset_ns,view_count,last_viewed_at) VALUES (1,?,0,1,?)`, int64(keyFor("d001")), t1)
	exec(`INSERT INTO state_watch(user_id,rating_key,view_offset_ns,view_count,last_viewed_at) VALUES (1,?,120000000000,0,?)`, int64(keyFor("d002")), t3)
	exec(`INSERT INTO state_watch(user_id,rating_key,view_offset_ns,view_count,last_viewed_at) VALUES (1,?,0,2,?)`, int64(keyFor("d003")), t4)
	exec(`INSERT INTO state_watch(user_id,rating_key,view_offset_ns,view_count,last_viewed_at) VALUES (1,?,0,1,0)`, int64(keyFor("d004")))
	exec(`INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at) VALUES (1,'title',?,1700000000)`, fmt.Sprintf("%d", int64(keyFor("d005"))))
	exec(`INSERT INTO watchlist_items(user_id,item_key,source,title,added) VALUES (1,?,'local','Doc 6',1700000000)`, int64(keyFor("d006")))

	exec(`INSERT INTO state_watch(user_id,rating_key,view_offset_ns,view_count,last_viewed_at) VALUES (2,?,0,1,?)`, int64(keyFor("d010")), t2)
	exec(`INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at) VALUES (2,'title',?,1700000000)`, fmt.Sprintf("%d", int64(keyFor("d011"))))
	exec(`INSERT INTO watchlist_items(user_id,item_key,source,title,added) VALUES (2,?,'local','Doc 12',1700000000)`, int64(keyFor("d012")))

	collectIDs := func(items []core.KindItem) []string {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.ID
		}
		return out
	}

	// 1. History view with sort=lastViewed desc: d003 (t4), d002 (t3), d001 (t1), then undated d004!
	historyPolicy := compileSmartViewTab(t, ctx, s, libID, `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"history":true}}]}`)
	var paged []string
	cursor := ""
	for step := 0; step < 10; step++ {
		page, err := s.Browse(ctx, BrowseQuery{
			LibraryID: libID,
			Level:     "doc",
			ViewerID:  1,
			TabPolicy: historyPolicy,
			Sort:      "lastViewed:desc",
			Cursor:    cursor,
			Limit:     2,
		})
		if err != nil {
			t.Fatalf("history page %d: %v", step, err)
		}
		if step == 0 && page.Total != 4 {
			t.Fatalf("history total = %d, want 4", page.Total)
		}
		paged = append(paged, collectIDs(page.Items)...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	wantDesc := []string{"d003", "d002", "d001", "d004"}
	if strings.Join(paged, ",") != strings.Join(wantDesc, ",") {
		t.Fatalf("history desc paged = %v, want %v", paged, wantDesc)
	}

	// 2. History view with sort=lastViewed asc: d001 (t1), d002 (t3), d003 (t4), then undated d004!
	var pagedAsc []string
	cursor = ""
	for step := 0; step < 10; step++ {
		page, err := s.Browse(ctx, BrowseQuery{
			LibraryID: libID,
			Level:     "doc",
			ViewerID:  1,
			TabPolicy: historyPolicy,
			Sort:      "lastViewed:asc",
			Cursor:    cursor,
			Limit:     2,
		})
		if err != nil {
			t.Fatalf("history asc page %d: %v", step, err)
		}
		pagedAsc = append(pagedAsc, collectIDs(page.Items)...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	wantAsc := []string{"d001", "d002", "d003", "d004"}
	if strings.Join(pagedAsc, ",") != strings.Join(wantAsc, ",") {
		t.Fatalf("history asc paged = %v, want %v", pagedAsc, wantAsc)
	}

	// 3. Viewer isolation: viewer 2 sees only d010; viewer 3 sees nothing.
	v2Hist, err := s.Browse(ctx, BrowseQuery{LibraryID: libID, Level: "doc", ViewerID: 2, TabPolicy: historyPolicy, Sort: "lastViewed:desc", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(collectIDs(v2Hist.Items), ","); got != "d010" || v2Hist.Total != 1 {
		t.Fatalf("viewer 2 history = %s (total=%d), want d010 (1)", got, v2Hist.Total)
	}
	v3Hist, err := s.Browse(ctx, BrowseQuery{LibraryID: libID, Level: "doc", ViewerID: 3, TabPolicy: historyPolicy, Sort: "lastViewed:desc", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(v3Hist.Items) != 0 || v3Hist.Total != 0 {
		t.Fatalf("viewer 3 history = %v (total=%d), want empty", collectIDs(v3Hist.Items), v3Hist.Total)
	}

	// 4. All 7 viewer-state predicates on viewer 1.
	filterCases := []struct {
		name    string
		raw     string
		wantIDs []string
		wantTot int
	}{
		{"watched", `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"watched":true}}]}`, []string{"d001", "d003", "d004"}, 3},
		{"inProgress", `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"inProgress":true}}]}`, []string{"d002"}, 1},
		{"favorite", `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"favorite":true}}]}`, []string{"d005"}, 1},
		{"watchlist", `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"watchlist":true}}]}`, []string{"d006"}, 1},
		{"onWatchlist", `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"onWatchlist":true}}]}`, []string{"d006"}, 1},
		{
			"lastViewedAfter",
			fmt.Sprintf(`{"match":"all","rules":[{"field":"viewer","op":"is","value":{"lastViewedAfter":%d}}]}`, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Unix()),
			[]string{"d002", "d003"}, 2,
		},
		{
			"lastViewedBefore",
			fmt.Sprintf(`{"match":"all","rules":[{"field":"viewer","op":"is","value":{"lastViewedBefore":%d}}]}`, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Unix()),
			[]string{"d001"}, 1,
		},
		{
			"combined viewer + indexed field",
			`{"match":"all","rules":[{"field":"viewer","op":"is","value":{"history":true}},{"field":"browse:topic","op":"is","value":"science"}]}`,
			[]string{"d003"}, 1,
		},
	}
	for _, tc := range filterCases {
		t.Run(tc.name, func(t *testing.T) {
			pol := compileSmartViewTab(t, ctx, s, libID, tc.raw)
			page, err := s.Browse(ctx, BrowseQuery{LibraryID: libID, Level: "doc", ViewerID: 1, TabPolicy: pol, Sort: "title", Limit: 50})
			if err != nil {
				t.Fatalf("Browse(%s): %v", tc.name, err)
			}
			got := collectIDs(page.Items)
			if page.Total != tc.wantTot || strings.Join(got, ",") != strings.Join(tc.wantIDs, ",") {
				t.Fatalf("Browse(%s) = %v (total=%d), want %v (%d)", tc.name, got, page.Total, tc.wantIDs, tc.wantTot)
			}
		})
	}

	// Unwatched excludes the 3 watched items (d001, d003, d004) for viewer 1.
	unwatchedPolicy := compileSmartViewTab(t, ctx, s, libID, `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"unwatched":true}}]}`)
	unwatchedV1, err := s.Browse(ctx, BrowseQuery{LibraryID: libID, Level: "doc", ViewerID: 1, TabPolicy: unwatchedPolicy, Sort: "title", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if unwatchedV1.Total != totalItems-3 {
		t.Fatalf("viewer 1 unwatched total = %d, want %d", unwatchedV1.Total, totalItems-3)
	}

	// 5. Full library lastViewed pagination crosses Phase 0 -> Phase 1 cleanly without duplicates.
	fullP1, err := s.Browse(ctx, BrowseQuery{LibraryID: libID, Level: "doc", ViewerID: 1, Sort: "lastViewed:desc", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	fullP2, err := s.Browse(ctx, BrowseQuery{LibraryID: libID, Level: "doc", ViewerID: 1, Sort: "lastViewed:desc", Cursor: fullP1.NextCursor, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	gotFirst5 := append(collectIDs(fullP1.Items), collectIDs(fullP2.Items)...)
	// Dated first: d003, d002, d001; then undated alphabetical: d004, d005.
	wantFirst5 := []string{"d003", "d002", "d001", "d004", "d005"}
	if strings.Join(gotFirst5, ",") != strings.Join(wantFirst5, ",") {
		t.Fatalf("full lastViewed first 5 = %v, want %v", gotFirst5, wantFirst5)
	}

	// 6. EXPLAIN QUERY PLAN check: positive viewer filters and history sort must NOT full-scan plugin_items.
	if store.DialectOf(db) == store.SQLite {
		explain := func(query string, args ...any) string {
			t.Helper()
			rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
			if err != nil {
				t.Fatalf("EXPLAIN %q: %v", query, err)
			}
			defer rows.Close()
			var lines []string
			for rows.Next() {
				var id, parent, notused int
				var detail string
				if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
					t.Fatal(err)
				}
				lines = append(lines, detail)
			}
			return strings.Join(lines, "\n")
		}

		// Phase 0 history sort plan must drive from state_watch_last_viewed into plugin_items(seq).
		planHist := explain(fmt.Sprintf(`SELECT i.item_ref FROM state_watch w CROSS JOIN plugin_items i WHERE w.user_id = ? AND w.last_viewed_at > 0 AND i.seq = w.rating_key - %d AND i.library_id = ? AND i.level = 'doc' ORDER BY w.last_viewed_at DESC, i.item_ref ASC LIMIT 20`, core.PluginKeyOffset), 1, int64(libID))
		if !strings.Contains(planHist, "state_watch_last_viewed") {
			t.Fatalf("expected state_watch_last_viewed in history sort plan, got:\n%s", planHist)
		}
		if strings.Contains(planHist, "SCAN i") {
			t.Fatalf("history sort must not full-scan plugin_items, got:\n%s", planHist)
		}

		// Positive favorite filter plan must drive from favorites_items via semi-join into plugin_items(seq), not SCAN i.
		favPol := compileSmartViewTab(t, ctx, s, libID, `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"favorite":true}}]}`)
		favSQL, favArgs := favPol.PluginSQLShownForViewer(core.TypeMovie, RuleFields(decl), 1)
		planFav := explain(`SELECT i.item_ref FROM plugin_items i WHERE i.library_id = ? AND i.level = 'doc' AND (`+favSQL+`) ORDER BY i.sort_title ASC, i.item_ref ASC LIMIT 20`, append([]any{int64(libID)}, favArgs...)...)
		if strings.Contains(planFav, "SCAN i") {
			t.Fatalf("favorite Smart View must not full-scan plugin_items, got:\n%s", planFav)
		}
		if !strings.Contains(planFav, "favorites_items") {
			t.Fatalf("expected favorites_items in favorite Smart View plan, got:\n%s", planFav)
		}
	}
}
