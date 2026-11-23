package pluginitems

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func largeFacetTestDecl() plugins.MediaKindMeta {
	return plugins.MediaKindMeta{
		Key:   "works",
		Label: "Works",
		Levels: []plugins.KindLevel{
			{Key: "work", Label: "Work", Playable: true},
			{Key: "chapter", Label: "Chapter", Playable: true},
		},
		Fields: []plugins.KindField{
			{Key: "status", Type: "enum", Label: "Status", Indexed: true, Enum: []string{"ongoing", "complete", "hiatus"}},
			{Key: "tags", Type: "strings", Label: "Tags", Indexed: true},
			{Key: "label", Type: "string", Label: "Label", Indexed: false},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:        []string{"title", "added", "released", "year", "rating", "index"},
			Facets:       []string{"status", "tags"},
			Search:       true,
			SearchFields: []string{"tags"},
		},
	}
}

func TestBrowseTallyTotalsAndCountCacheInvalidation(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	defer s.Close()

	const libID core.LibraryID = 71
	if _, err := s.UpsertKind(ctx, "test.works", libID, largeFacetTestDecl(), false); err != nil {
		t.Fatal(err)
	}

	items := []core.KindItem{
		{ID: "w1", Level: "work", Title: "Alpha", Year: 2020, Fields: map[string]json.RawMessage{"status": rawJSON(t, "ongoing"), "tags": rawJSON(t, []string{"scifi", "space"})}},
		{ID: "w2", Level: "work", Title: "Beta", Year: 2020, Fields: map[string]json.RawMessage{"status": rawJSON(t, "complete"), "tags": rawJSON(t, []string{"scifi", "noir"})}},
		{ID: "w3", Level: "work", Title: "Gamma", Year: 2021, Fields: map[string]json.RawMessage{"status": rawJSON(t, "ongoing"), "tags": rawJSON(t, []string{"scifi", "noir"})}},
		{ID: "w4", Level: "work", Title: "Delta", Year: 2021, Fields: map[string]json.RawMessage{"status": rawJSON(t, "hiatus"), "tags": rawJSON(t, []string{"fantasy"})}},
		{ID: "w5", Level: "work", Title: "Epsilon", Year: 2022, Fields: map[string]json.RawMessage{"status": rawJSON(t, "complete"), "tags": rawJSON(t, []string{"scifi"})}},
		{ID: "c1", Level: "chapter", ParentID: "w1", Title: "Ch 1", Fields: map[string]json.RawMessage{"status": rawJSON(t, "ongoing"), "tags": rawJSON(t, []string{"scifi"})}},
	}
	if _, err := s.UpsertItems(ctx, "test.works", "works", items); err != nil {
		t.Fatal(err)
	}

	// Single-valued enum: multi-value OR and ExcludeFacets are exact from field tallies.
	orPage, err := s.Browse(ctx, BrowseQuery{
		LibraryID:  libID,
		Level:      "work",
		Facets:     map[string][]string{"status": {"ongoing", "complete"}},
		NoTotal:    true,
		CheapTotal: true,
		Limit:      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !orPage.TotalExact || orPage.Total != 4 {
		t.Fatalf("enum OR CheapTotal = %d / exact=%v, want 4 / true", orPage.Total, orPage.TotalExact)
	}

	excPage, err := s.Browse(ctx, BrowseQuery{
		LibraryID:     libID,
		Level:         "work",
		ExcludeFacets: map[string][]string{"status": {"hiatus"}},
		NoTotal:       true,
		CheapTotal:    true,
		Limit:         2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !excPage.TotalExact || excPage.Total != 4 {
		t.Fatalf("enum Exclude CheapTotal = %d / exact=%v, want 4 / true", excPage.Total, excPage.TotalExact)
	}

	// Multi-valued strings: single value (positive or excluded) and AnyLevel are exact;
	// multi-value OR on multi-valued strings cannot sum overlapping tallies.
	oneTag, err := s.Browse(ctx, BrowseQuery{
		LibraryID:  libID,
		AnyLevel:   true,
		Facets:     map[string][]string{"tags": {"scifi"}},
		NoTotal:    true,
		CheapTotal: true,
		Limit:      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !oneTag.TotalExact || oneTag.Total != 5 {
		t.Fatalf("AnyLevel single-tag CheapTotal = %d / exact=%v, want 5 / true", oneTag.Total, oneTag.TotalExact)
	}

	twoTagsMultiPage, err := s.Browse(ctx, BrowseQuery{
		LibraryID:  libID,
		Level:      "work",
		Facets:     map[string][]string{"tags": {"scifi", "noir"}},
		NoTotal:    true,
		CheapTotal: true,
		Limit:      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if twoTagsMultiPage.TotalExact {
		t.Fatalf("multi-value strings multi-page CheapTotal should not be exact, got total=%d", twoTagsMultiPage.Total)
	}

	// Single-page result on un-tallied filter is exact at zero extra cost, while
	// NoTotal without CheapTotal ("none") never claims TotalExact.
	singlePage, err := s.Browse(ctx, BrowseQuery{
		LibraryID:  libID,
		Level:      "work",
		AllFacets:  map[string][]string{"tags": {"scifi", "noir"}},
		NoTotal:    true,
		CheapTotal: true,
		Limit:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !singlePage.TotalExact || singlePage.Total != 2 {
		t.Fatalf("single-page CheapTotal = %d / exact=%v, want 2 / true", singlePage.Total, singlePage.TotalExact)
	}

	nonePage, err := s.Browse(ctx, BrowseQuery{
		LibraryID: libID,
		Level:     "work",
		NoTotal:   true,
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nonePage.TotalExact {
		t.Fatalf("NoTotal without CheapTotal should have TotalExact=false, got total=%d", nonePage.Total)
	}

	// Exact count caches per writeGen, and subsequent mutations invalidate it immediately.
	exactQ := BrowseQuery{
		LibraryID: libID,
		Level:     "work",
		Facets:    map[string][]string{"tags": {"scifi", "noir"}},
		Limit:     2,
	}
	before, err := s.Browse(ctx, exactQ)
	if err != nil {
		t.Fatal(err)
	}
	if !before.TotalExact || before.Total != 4 {
		t.Fatalf("before exact total = %d / exact=%v, want 4 / true", before.Total, before.TotalExact)
	}
	// A CheapTotal call on the same generation can reuse the cached exact total.
	cheapCached := exactQ
	cheapCached.NoTotal = true
	cheapCached.CheapTotal = true
	if hit, err := s.Browse(ctx, cheapCached); err != nil || !hit.TotalExact || hit.Total != 4 {
		t.Fatalf("cached CheapTotal = %d / exact=%v, err=%v; want 4 / true", hit.Total, hit.TotalExact, err)
	}
	// Mutating items advances writeGen and invalidates cached counts.
	if _, err := s.UpsertItems(ctx, "test.works", "works", []core.KindItem{
		{ID: "w6", Level: "work", Title: "Zeta", Fields: map[string]json.RawMessage{"status": rawJSON(t, "ongoing"), "tags": rawJSON(t, []string{"noir"})}},
	}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Browse(ctx, exactQ)
	if err != nil {
		t.Fatal(err)
	}
	if !after.TotalExact || after.Total != 5 {
		t.Fatalf("after upsert exact total = %d / exact=%v, want 5 / true", after.Total, after.TotalExact)
	}

	// Search with NoTotal+CheapTotal returns exact on a single page and omits when truncated.
	searchFull, err := s.Search(ctx, SearchQuery{
		LibraryID:  libID,
		Query:      "scifi",
		Level:      "work",
		NoTotal:    true,
		CheapTotal: true,
		Limit:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !searchFull.TotalExact || searchFull.Total != 4 {
		t.Fatalf("single-page search CheapTotal = %d / exact=%v, want 4 / true", searchFull.Total, searchFull.TotalExact)
	}
	searchTrunc, err := s.Search(ctx, SearchQuery{
		LibraryID:  libID,
		Query:      "scifi",
		Level:      "work",
		NoTotal:    true,
		CheapTotal: true,
		Limit:      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if searchTrunc.TotalExact || len(searchTrunc.Items) != 2 {
		t.Fatalf("truncated search CheapTotal = len=%d total=%d exact=%v, want len=2 exact=false", len(searchTrunc.Items), searchTrunc.Total, searchTrunc.TotalExact)
	}
}

func TestLargeFacetDrivesFromSortIndexAndMatchesFieldDrivenOrder(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	defer s.Close()

	const libID core.LibraryID = 72
	if _, err := s.UpsertKind(ctx, "test.works", libID, largeFacetTestDecl(), false); err != nil {
		t.Fatal(err)
	}

	// Seed 120 items: 60 with tag "common" (50% share) and 60 with "rare",
	// with ties on year/rating/added to exercise keyset tie-breaking.
	items := make([]core.KindItem, 0, 120)
	for i := 0; i < 120; i++ {
		tag := "rare"
		if i%2 == 0 {
			tag = "common"
		}
		items = append(items, core.KindItem{
			ID:       fmt.Sprintf("item-%03d", i),
			Level:    "work",
			Title:    fmt.Sprintf("Work %03d", 120-i),
			AddedAt:  int64(1_700_000_000 + (i/3)*100),
			Released: int64(1_600_000_000 + (i/4)*1000),
			Year:     2000 + (i % 10),
			Rating:   float64(i%10) + 0.5,
			Index:    i / 2,
			Fields: map[string]json.RawMessage{
				"status": rawJSON(t, "ongoing"),
				"tags":   rawJSON(t, []string{tag}),
			},
		})
	}
	if _, err := s.UpsertItems(ctx, "test.works", "works", items); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}

	// Verify SQLite chooses the sort index with no TEMP B-TREE and probes
	// plugin_item_fields via its covering PK across every default sort order.
	sortPlans := []struct {
		sort      string
		col       string
		dir       string
		wantIndex string
		level     string
	}{
		{"title", "sort_title", "ASC", "plugin_items_level_sort", "work"},
		{"title", "sort_title", "ASC", "plugin_items_browse", ""},
		{"added", "added_at", "DESC", "plugin_items_added", "work"},
		{"added", "added_at", "DESC", "plugin_items_added", ""},
		{"released", "released", "DESC", "plugin_items_released", "work"},
		{"year", "year", "DESC", "plugin_items_year", "work"},
		{"rating", "rating", "DESC", "plugin_items_rating", "work"},
		{"index", "idx", "ASC", "plugin_items_idx", "work"},
	}
	for _, tc := range sortPlans {
		var where string
		var args []any
		args = append(args, int64(libID))
		switch {
		case tc.level != "" && tc.col != "sort_title":
			where = `i.library_id = ? AND +i.level = ?`
			args = append(args, tc.level)
		case tc.level != "":
			where = `i.library_id = ? AND i.level = ?`
			args = append(args, tc.level)
		case tc.col != "sort_title":
			where = `i.library_id = ? AND +i.parent_ref = ''`
		default:
			where = `i.library_id = ? AND i.parent_ref = ''`
		}
		where += ` AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_text IN (?))`
		args = append(args, "tags", "common", 11)
		planSQL := fmt.Sprintf(
			`EXPLAIN QUERY PLAN SELECT i.item_ref FROM plugin_items i WHERE %s ORDER BY i.%s %s, i.item_ref ASC LIMIT ?`,
			where, tc.col, tc.dir,
		)
		if store.DialectOf(db) == store.SQLite {
			plan := explainPlanText(t, db, planSQL, args...)
			if !strings.Contains(plan, tc.wantIndex) {
				t.Errorf("sort=%q level=%q: expected index %s in plan:\n%s", tc.sort, tc.level, tc.wantIndex, plan)
			}
			if strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") {
				t.Errorf("sort=%q level=%q: unexpected TEMP B-TREE sort in plan:\n%s", tc.sort, tc.level, plan)
			}
			if !strings.Contains(plan, "COVERING INDEX") {
				t.Errorf("sort=%q level=%q: expected covering PK probe on plugin_item_fields in plan:\n%s", tc.sort, tc.level, plan)
			}
		}
	}

	// Verify keyset pagination via sort-index driving matches field-driven results
	// item-for-item across every sort column.
	collectAll := func(sortKey string, minMatches int) []string {
		prev := largeFacetMinMatches
		largeFacetMinMatches = minMatches
		defer func() { largeFacetMinMatches = prev }()

		var ids []string
		cursor := ""
		for {
			page, err := s.Browse(ctx, BrowseQuery{
				LibraryID:  libID,
				Level:      "work",
				Sort:       sortKey,
				Facets:     map[string][]string{"tags": {"common"}},
				NoTotal:    true,
				CheapTotal: true,
				Cursor:     cursor,
				Limit:      7,
			})
			if err != nil {
				t.Fatalf("sort=%q cursor=%q: %v", sortKey, cursor, err)
			}
			if !page.TotalExact || page.Total != 60 {
				t.Fatalf("sort=%q total=%d exact=%v, want 60 / true", sortKey, page.Total, page.TotalExact)
			}
			for _, it := range page.Items {
				ids = append(ids, it.ID)
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		return ids
	}

	for _, sortKey := range []string{"title", "added", "released", "year", "rating", "index"} {
		fromField := collectAll(sortKey, 10_000)
		fromSort := collectAll(sortKey, 10)
		if len(fromSort) != 60 {
			t.Fatalf("sort=%q returned %d items, want 60", sortKey, len(fromSort))
		}
		if !slices.Equal(fromSort, fromField) {
			t.Fatalf("sort=%q sort-index paging diverged from field-driven paging:\nsort:  %v\nfield: %v", sortKey, fromSort[:10], fromField[:10])
		}
	}
}

func explainPlanText(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("explain query failed: %v", err)
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

func TestReindexChunkedMaintainsFieldTalliesIncrementallyAndIdempotently(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	defer s.Close()

	const libID core.LibraryID = 73
	decl1 := largeFacetTestDecl()
	if _, err := s.UpsertKind(ctx, "test.works", libID, decl1, false); err != nil {
		t.Fatal(err)
	}

	// Seed 450 items (> 2 full upsertChunkSize=200 chunks) so reindexing spans 3 chunks.
	const itemCount = 450
	items := make([]core.KindItem, 0, itemCount)
	statuses := []string{"ongoing", "complete", "hiatus"}
	labels := []string{"indie", "major", "small-press"}
	for i := 0; i < itemCount; i++ {
		items = append(items, core.KindItem{
			ID:       fmt.Sprintf("w-%04d", i),
			Level:    "work",
			Title:    fmt.Sprintf("Work %04d", i),
			Playable: true,
			Fields: map[string]json.RawMessage{
				"status": rawJSON(t, statuses[i%len(statuses)]),
				"tags":   rawJSON(t, []string{fmt.Sprintf("tag-%d", i%5), "shared"}),
				"label":  rawJSON(t, labels[i%len(labels)]),
			},
		})
	}
	if _, err := s.UpsertItems(ctx, "test.works", "works", items); err != nil {
		t.Fatal(err)
	}

	assertTalliesMatchFields := func(step string) {
		t.Helper()
		type key struct {
			level, field, text string
		}
		want := map[key]int{}
		rows, err := db.QueryContext(ctx, `
SELECT i.level, f.field, f.v_text, COUNT(*)
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE i.library_id = ? AND i.level <> '' AND f.v_text <> ''
GROUP BY i.level, f.field, f.v_text`, int64(libID))
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var lv, field, text string
			var cnt int
			if err := rows.Scan(&lv, &field, &text, &cnt); err != nil {
				t.Fatal(err)
			}
			want[key{lv, field, text}] = cnt
			want[key{"", field, text}] += cnt
		}
		rows.Close()

		got := map[key]int{}
		trows, err := db.QueryContext(ctx, `
SELECT level, field, v_text, cnt
FROM plugin_item_field_tally
WHERE library_id = ?`, int64(libID))
		if err != nil {
			t.Fatal(err)
		}
		for trows.Next() {
			var lv, field, text string
			var cnt int
			if err := trows.Scan(&lv, &field, &text, &cnt); err != nil {
				t.Fatal(err)
			}
			got[key{lv, field, text}] = cnt
		}
		trows.Close()

		if len(got) != len(want) {
			t.Fatalf("%s: field tally row count = %d, want %d", step, len(got), len(want))
		}
		for k, w := range want {
			if g := got[k]; g != w {
				t.Fatalf("%s: tally[%+v] = %d, want %d", step, k, g, w)
			}
		}
	}

	assertTalliesMatchFields("initial upsert")

	// Simulate pre-upgrade rows that had "label" stored in JSON before it was
	// indexed, and a schema change that drops "status" and adds "label" to FTS,
	// so every chunk exercises field deletes, field inserts, FTS updates, and
	// both positive and negative incremental field-tally deltas.
	if _, err := db.ExecContext(ctx, `DELETE FROM plugin_item_fields WHERE field = 'label'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM plugin_item_field_tally WHERE library_id = ? AND field = 'label'`, int64(libID)); err != nil {
		t.Fatal(err)
	}
	assertTalliesMatchFields("pre-reindex baseline")

	decl2 := largeFacetTestDecl()
	var keptFields []plugins.KindField
	for _, f := range decl2.Fields {
		if f.Key == "status" {
			continue
		}
		if f.Key == "label" {
			f.Indexed = true
		}
		keptFields = append(keptFields, f)
	}
	decl2.Fields = keptFields
	decl2.Browse.Facets = []string{"tags", "label"}
	decl2.Browse.SearchFields = []string{"tags", "label"}

	// Persist decl2 before interleaved upserts (matching real UpsertKind order,
	// where the new decl commits before the background chunked reindexer runs).
	decl2JSON, _ := json.Marshal(decl2)
	if _, err := db.ExecContext(ctx, `UPDATE plugin_kinds SET decl = ? WHERE plugin_id = ? AND kind_key = ?`, string(decl2JSON), "test.works", "works"); err != nil {
		t.Fatal(err)
	}

	// Simulate partial reindex (chunk 1 only), concurrent upsert + delete, and
	// restart from cursor="" (crash recovery) to verify zero tally drift.
	n1, nextCursor, done1, wrote1, err := s.reindexKindOneChunkLocked(ctx, store.DialectOf(db) == store.Postgres, true, "test.works", "works", libID, decl2, "")
	if err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	if n1 != upsertChunkSize || done1 || !wrote1 || nextCursor == "" {
		t.Fatalf("chunk 1 = (n=%d, next=%q, done=%v, wrote=%v)", n1, nextCursor, done1, wrote1)
	}
	assertTalliesMatchFields("after chunk 1")

	// Interleave a concurrent upsert (updating an already-reindexed item and an un-reindexed item)
	// and a delete before restarting full reindexKindChunked.
	if _, err := s.UpsertItems(ctx, "test.works", "works", []core.KindItem{
		{
			ID:       "w-0005",
			Level:    "work",
			Title:    "Work 0005 Updated",
			Playable: true,
			Fields: map[string]json.RawMessage{
				"tags":  rawJSON(t, []string{"shared", "new-tag"}),
				"label": rawJSON(t, "indie"),
			},
		},
		{
			ID:       "w-0440",
			Level:    "work",
			Title:    "Work 0440 Updated",
			Playable: true,
			Fields: map[string]json.RawMessage{
				"tags":  rawJSON(t, []string{"shared"}),
				"label": rawJSON(t, "major"),
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteItems(ctx, "test.works", "works", []string{"w-0010", "w-0410"}); err != nil {
		t.Fatal(err)
	}
	assertTalliesMatchFields("after interleaved upsert+delete")

	// Restart chunked reindex from scratch (simulating crash recovery).
	if err := s.reindexKindChunked(ctx, store.DialectOf(db) == store.Postgres, "test.works", "works", libID, decl2); err != nil {
		t.Fatalf("reindexKindChunked: %v", err)
	}
	assertTalliesMatchFields("after full chunked reindex")

	// Verify O(1) CheapTotal returns the exact reindexed tally for "label".
	page, err := s.Browse(ctx, BrowseQuery{
		LibraryID:  libID,
		Level:      "work",
		Facets:     map[string][]string{"label": {"indie"}},
		NoTotal:    true,
		CheapTotal: true,
		Limit:      5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !page.TotalExact || page.Total == 0 {
		t.Fatalf("reindexed label CheapTotal = %d / exact=%v, want >0 / true", page.Total, page.TotalExact)
	}

	// Re-running one chunk on an already-reindexed kind must perform zero writes.
	_, _, _, wroteAgain, err := s.reindexKindOneChunkLocked(ctx, store.DialectOf(db) == store.Postgres, true, "test.works", "works", libID, decl2, "")
	if err != nil {
		t.Fatalf("second pass chunk 1: %v", err)
	}
	if store.DialectOf(db) == store.SQLite && wroteAgain {
		t.Fatal("expected already-reindexed chunk to skip opening a write transaction (wrote=false)")
	}
}
