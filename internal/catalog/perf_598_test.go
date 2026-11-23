package catalog

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func explainPlanLines(t *testing.T, ctx context.Context, c *Catalog, query string, args ...any) string {
	t.Helper()
	if store.DialectOf(c.db) == store.Postgres {
		conn, err := c.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, `SET enable_seqscan = off`); err != nil {
			t.Fatal(err)
		}
		rows, err := conn.QueryContext(ctx, "EXPLAIN "+query, args...)
		if err != nil {
			t.Fatalf("EXPLAIN failed: %v\nSQL: %s", err, query)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(lines, "\n")
	}
	rows, err := c.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN failed: %v\nSQL: %s", err, query)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(details, " | ")
}

func assertEpisodeReleasePlan(t *testing.T, isPostgres bool, plan string, ordered bool) {
	t.Helper()
	if isPostgres {
		if ordered {
			if !strings.Contains(plan, "catalog_titles_episode_release") {
				t.Fatalf("expected PostgreSQL ordered plan to use catalog_titles_episode_release, got:\n%s", plan)
			}
		} else if !(strings.Contains(plan, "catalog_titles_episode_release") || strings.Contains(plan, "catalog_titles_episode_recent")) {
			t.Fatalf("expected PostgreSQL count plan to use a partial episode release index, got:\n%s", plan)
		}
	} else if !(strings.Contains(plan, "catalog_titles_episode_release") || strings.Contains(plan, "catalog_titles_episode_recent") || strings.Contains(plan, "catalog_plex_shelf_order")) {
		t.Fatalf("expected SQLite plan to use an episode release index, got:\n%s", plan)
	}
	if strings.Contains(plan, "Seq Scan on catalog_titles") || strings.Contains(plan, "Hash Join") || strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("unexpected Seq Scan, Hash Join, or temp sort on catalog_titles:\n%s", plan)
	}
	if isPostgres {
		if !(strings.Contains(plan, "catalog_titles_pkey") || strings.Contains(plan, "catalog_titles_type_key_page")) {
			t.Fatalf("expected PostgreSQL plan to probe show by key index, got:\n%s", plan)
		}
		return
	}
	if !strings.Contains(plan, "SEARCH t USING INTEGER PRIMARY KEY") && !strings.Contains(plan, "USING COVERING INDEX catalog_titles_type_key_page") {
		t.Fatalf("expected SQLite plan to probe show t by key, got:\n%s", plan)
	}
	for _, seg := range strings.Split(plan, " | ") {
		trimmed := strings.TrimSpace(seg)
		if strings.HasPrefix(trimmed, "SCAN catalog_titles") && !strings.Contains(trimmed, "USING") {
			t.Fatalf("unexpected table scan on catalog_titles:\n%s", plan)
		}
	}
}

func TestEpisodeReleasePlansAndParity(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	isPG := store.DialectOf(c.db) == store.Postgres
	now := c.now()

	for i := int64(1); i <= 20; i++ {
		if err := c.upsert(ctx, core.Title{
			Key:           core.MovieKey(i),
			Type:          core.TypeMovie,
			Title:         "Movie",
			ContentRating: "PG-13",
			Released:      now.Add(-time.Duration(i) * 24 * time.Hour),
		}, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
	}
	for id := int64(1); id <= 18; id++ {
		rating := []string{"TV-PG", "TV-14", "TV-MA"}[id%3]
		show := core.Title{
			Key:           core.ShowKey(id),
			Type:          core.TypeShow,
			Title:         "Series",
			ContentRating: rating,
			Released:      now.AddDate(-1, 0, -int(id)),
		}
		if err := c.upsert(ctx, show, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
		for ep := 1; ep <= 5; ep++ {
			aired := now.AddDate(0, 0, -25+ep*6) // ep 5 airs in the future
			if err := c.upsert(ctx, core.Title{
				Key:           core.EpisodeKey(id, 1, ep),
				Type:          core.TypeEpisode,
				ShowKey:       show.Key,
				Season:        1,
				Episode:       ep,
				Title:         "Episode",
				ContentRating: rating,
				Released:      aired,
			}, upsertOpts{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := c.db.ExecContext(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}

	restricted := &core.Filter{
		MaxContentRating:      "TV-14",
		ExcludeContentRatings: []string{"TV-MA"},
	}
	for _, tc := range []struct {
		name   string
		filter core.Filter
	}{
		{name: "unrestricted", filter: core.Filter{Type: core.TypeEpisode}},
		{name: "restricted", filter: core.Filter{Type: core.TypeEpisode, MaxContentRating: "TV-14", ChildVisibility: restricted}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relPage, err := c.BrowseStoredEpisodes(ctx, core.BrowseQuery{
				Filter: tc.filter,
				Sort:   core.SortReleased,
				Desc:   true,
				Limit:  10,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantTotal, err := c.CountStoredEpisodes(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if relPage.Total != wantTotal || wantTotal == 0 {
				t.Fatalf("BrowseStoredEpisodes total=%d want=%d", relPage.Total, wantTotal)
			}
			for i, item := range relPage.Items {
				if item.Released.After(now) {
					t.Fatalf("unaired episode leaked: %+v", item)
				}
				if i > 0 && relPage.Items[i-1].Released.Before(item.Released) {
					t.Fatalf("episodes out of release order at %d: %v < %v", i, relPage.Items[i-1].Released, item.Released)
				}
			}

			recent, err := c.RecentPage(ctx, core.BrowseQuery{Filter: tc.filter, Limit: 8}, []core.MediaType{core.TypeMovie, core.TypeEpisode})
			if err != nil {
				t.Fatal(err)
			}
			if len(recent.Items) != 8 || recent.Total <= wantTotal {
				t.Fatalf("RecentPage len=%d total=%d wantTotal>%d", len(recent.Items), recent.Total, wantTotal)
			}

			probeWhere, probeArgs, err := c.storedEpisodesProbeWhere(tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			pageSQL := `SELECT ` + prefixedCols("e") + ` FROM catalog_titles e WHERE ` + probeWhere + ` ORDER BY e.released_ts DESC, e.key ASC LIMIT ? OFFSET ?`
			pagePlan := explainPlanLines(t, ctx, c, pageSQL, append(append([]any{}, probeArgs...), 20, 0)...)
			assertEpisodeReleasePlan(t, isPG, pagePlan, true)

			recentBranchSQL := `SELECT key,stamp FROM (SELECT e.key AS key,e.released_ts AS stamp FROM catalog_titles e WHERE ` + probeWhere + ` ORDER BY e.released_ts DESC,e.key ASC LIMIT ?) recent_ep`
			recentPlan := explainPlanLines(t, ctx, c, recentBranchSQL, append(append([]any{}, probeArgs...), 20)...)
			assertEpisodeReleasePlan(t, isPG, recentPlan, true)

			showFilter, _ := splitStoredFilter(tc.filter, core.KindEpisode)
			showWhere, showArgs, err := c.browseWhere(showFilter)
			if err != nil {
				t.Fatal(err)
			}
			vis, visArgs := c.childVisibilityConds(tc.filter.ChildVisibility)
			probeCond := `t.key=e.show_key AND ` + showWhere
			epArgs := append([]any{now.Unix()}, showArgs...)
			if len(vis) > 0 {
				probeCond += ` AND ` + strings.Join(vis, " AND ")
				epArgs = append(epArgs, visArgs...)
			}
			unairedSQL := `SELECT COUNT(*) FROM catalog_titles e WHERE e.type='episode' AND e.release_known=1 AND e.released_ts>? AND e.adult=0 AND e.show_key<>0 AND EXISTS(SELECT 1 FROM catalog_titles t WHERE ` + probeCond + ` LIMIT 1 OFFSET 0)`
			unairedPlan := explainPlanLines(t, ctx, c, unairedSQL, epArgs...)
			assertEpisodeReleasePlan(t, isPG, unairedPlan, false)
		})
	}
}

func TestResolveShowParentsBatchNoReadWait(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := c.now()

	keys := []core.RatingKey{core.ShowKey(101), core.ShowKey(102), core.ShowKey(103)}
	for _, k := range keys {
		if err := c.upsert(ctx, core.Title{
			Key:      k,
			Type:     core.TypeShow,
			Title:    "Stored Show",
			Genres:   []string{"Drama", "Mystery"},
			Keywords: []string{"cliffhanger"},
			Released: now.AddDate(-2, 0, 0),
		}, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
		// Mark stored detail as stale so !c.fresh(st) is exercised.
		if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET detail=1, detail_at=? WHERE key=?`, now.AddDate(-1, 0, 0).Unix(), int64(k)); err != nil {
			t.Fatal(err)
		}
	}

	reqCtx := WithReadWaitBudget(ctx, 0)
	got, err := c.ResolveShowParents(reqCtx, keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(keys) {
		t.Fatalf("ResolveShowParents returned %d shows, want %d", len(got), len(keys))
	}
	for _, k := range keys {
		sh, ok := got[k]
		if !ok {
			t.Fatalf("missing show %v", k)
		}
		if len(sh.Genres) != 2 || len(sh.Keywords) != 1 {
			t.Fatalf("show %v not batch-hydrated: genres=%v keywords=%v", k, sh.Genres, sh.Keywords)
		}
	}
}
