package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestCatalogSmartViewViewerStateTabRulesAndIsolation(t *testing.T) {
	c := newChunkTestCatalog(t)
	ctx := context.Background()

	ddl := []string{
		`CREATE TABLE IF NOT EXISTS favorites_items (
			viewer_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			subject_id TEXT NOT NULL,
			added_at INTEGER NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (viewer_id, kind, subject_id)
		)`,
		`CREATE TABLE IF NOT EXISTS watchlist_items (
			user_id INTEGER NOT NULL,
			item_key INTEGER NOT NULL,
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
		if _, err := c.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("ddl %q: %v", stmt, err)
		}
	}

	for i := int64(1); i <= 10; i++ {
		key := int64(core.MovieKey(i))
		if _, err := c.db.ExecContext(ctx, `INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult) VALUES(?,'movie',?,?,1600000000,1,0)`, key, fmt.Sprintf("Movie %02d", i), fmt.Sprintf("movie %02d", i)); err != nil {
			t.Fatal(err)
		}
	}

	t1 := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC).UnixNano()
	t2 := time.Date(2026, 2, 5, 10, 0, 0, 0, time.UTC).UnixNano()
	t3 := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC).UnixNano()

	// Viewer 1: movie 1 watched (t1), movie 2 in-progress (t2), movie 3 watched (t3), movie 4 favorite, movie 5 watchlist.
	// Viewer 2: movie 8 watched (t2), movie 9 favorite, movie 10 watchlist.
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := c.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec(`INSERT INTO state_watch(user_id,rating_key,view_offset_ns,view_count,last_viewed_at) VALUES (1,?,0,1,?)`, int64(core.MovieKey(1)), t1)
	exec(`INSERT INTO state_watch(user_id,rating_key,view_offset_ns,view_count,last_viewed_at) VALUES (1,?,60000000000,0,?)`, int64(core.MovieKey(2)), t2)
	exec(`INSERT INTO state_watch(user_id,rating_key,view_offset_ns,view_count,last_viewed_at) VALUES (1,?,0,2,?)`, int64(core.MovieKey(3)), t3)
	exec(`INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at) VALUES (1,'title',?,1700000000)`, fmt.Sprintf("%d", int64(core.MovieKey(4))))
	exec(`INSERT INTO watchlist_items(user_id,item_key,source,title,added) VALUES (1,?,'local','Movie 5',1700000000)`, int64(core.MovieKey(5)))

	exec(`INSERT INTO state_watch(user_id,rating_key,view_offset_ns,view_count,last_viewed_at) VALUES (2,?,0,1,?)`, int64(core.MovieKey(8)), t2)
	exec(`INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at) VALUES (2,'title',?,1700000000)`, fmt.Sprintf("%d", int64(core.MovieKey(9))))
	exec(`INSERT INTO watchlist_items(user_id,item_key,source,title,added) VALUES (2,?,'local','Movie 10',1700000000)`, int64(core.MovieKey(10)))

	browseTab := func(viewerID int64, rawFilter string, sortLastViewed, desc bool) []int64 {
		t.Helper()
		var tf core.TabFilter
		if err := json.Unmarshal([]byte(rawFilter), &tf); err != nil {
			t.Fatalf("unmarshal TabFilter: %v", err)
		}
		rules, fallback := tf.ToPolicy(core.TypeMovie)
		q := core.BrowseQuery{
			Filter: core.Filter{
				Type:            core.TypeMovie,
				CatalogViewerID: viewerID,
				TabRules:        rules,
				TabFallback:     fallback,
			},
			Sort:           core.SortTitle,
			SortLastViewed: sortLastViewed,
			SortViewerID:   viewerID,
			Desc:           desc,
			Limit:          20,
		}
		page, err := c.Browse(ctx, q)
		if err != nil {
			t.Fatalf("Browse(viewer=%d, %s): %v", viewerID, rawFilter, err)
		}
		out := make([]int64, len(page.Items))
		for i, it := range page.Items {
			out[i] = it.Key.TMDB()
		}
		return out
	}

	assertIDs := func(name string, got, want []int64) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}

	// History sorted by lastViewed DESC + viewer isolation (also exercises browseCache bypass).
	histFilter := `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"history":true}}]}`
	assertIDs("viewer 1 history desc", browseTab(1, histFilter, true, true), []int64{3, 2, 1})
	assertIDs("viewer 2 history desc", browseTab(2, histFilter, true, true), []int64{8})
	assertIDs("viewer 3 history desc", browseTab(3, histFilter, true, true), []int64{})

	// Individual viewer-state filters on viewer 1 & viewer 2.
	assertIDs("viewer 1 watched", browseTab(1, `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"watched":true}}]}`, false, false), []int64{1, 3})
	assertIDs("viewer 1 inProgress", browseTab(1, `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"inProgress":true}}]}`, false, false), []int64{2})
	assertIDs("viewer 1 favorite", browseTab(1, `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"favorite":true}}]}`, false, false), []int64{4})
	assertIDs("viewer 2 favorite", browseTab(2, `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"favorite":true}}]}`, false, false), []int64{9})
	assertIDs("viewer 1 watchlist", browseTab(1, `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"watchlist":true}}]}`, false, false), []int64{5})
	assertIDs("viewer 2 onWatchlist", browseTab(2, `{"match":"all","rules":[{"field":"viewer","op":"is","value":{"onWatchlist":true}}]}`, false, false), []int64{10})
	assertIDs("viewer 1 lastViewedAfter", browseTab(1, fmt.Sprintf(`{"match":"all","rules":[{"field":"viewer","op":"is","value":{"lastViewedAfter":%d}}]}`, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Unix()), false, false), []int64{2, 3})
	assertIDs("viewer 1 lastViewedBefore", browseTab(1, fmt.Sprintf(`{"match":"all","rules":[{"field":"viewer","op":"is","value":{"lastViewedBefore":%d}}]}`, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Unix()), false, false), []int64{1})

	// Verify positive viewer filter SQL uses semi-join against favorites_items without scanning catalog_titles.
	var tf core.TabFilter
	_ = json.Unmarshal([]byte(`{"match":"all","rules":[{"field":"viewer","op":"is","value":{"favorite":true}}]}`), &tf)
	rules, fallback := tf.ToPolicy(core.TypeMovie)
	pol, err := c.compileLibraryRulesModeForViewer(rules, fallback, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	shownSQL, shownArgs := pol.SQLShownForType(core.TypeMovie)
	rows, err := c.db.QueryContext(ctx, "EXPLAIN QUERY PLAN SELECT t.key FROM catalog_titles t WHERE t.type='movie' AND ("+shownSQL+")", shownArgs...)
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	defer rows.Close()
	var planLines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		planLines = append(planLines, detail)
	}
	plan := strings.Join(planLines, "\n")
	if !strings.Contains(plan, "favorites_items") {
		t.Fatalf("expected favorites_items semi-join in plan:\n%s", plan)
	}
	if strings.Contains(plan, "SCAN t") {
		t.Fatalf("favorite Smart View should not full-scan catalog_titles:\n%s", plan)
	}
}
