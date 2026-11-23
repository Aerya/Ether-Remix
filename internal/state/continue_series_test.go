package state

import (
	"context"
	"database/sql"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
	"time"
)

func TestContinueSeriesHistoryKeepsOldShowsAfterBinges(t *testing.T) {
	s, now := newTestState(t)
	ctx := context.Background()
	old := core.EpisodeKey(44, 1, 3)
	recent := core.EpisodeKey(45, 1, 2)
	at := now.Add(-163 * 24 * time.Hour)
	records := []ImportRecord{{Views: 1, UserID: 1, Key: old, ViewedAt: at}, {Views: 1, UserID: 2, Key: core.EpisodeKey(99, 1, 1), ViewedAt: *now}}
	for i := 0; i < 250; i++ {
		records = append(records, ImportRecord{Views: 1, UserID: 1, Key: recent, ViewedAt: now.Add(time.Duration(i) * time.Second)})
		records = append(records, ImportRecord{Views: 1, UserID: 1, Key: core.MovieKey(int64(i + 1)), ViewedAt: *now})
	}
	if err := s.ImportTransaction(ctx, func(*sql.Tx) (ImportBatch, error) { return ImportBatch{Records: records}, nil }); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ContinueSeriesHistory(ctx, 1, 50)
	if err != nil || len(rows) != 2 || rows[0].Key != recent || rows[1].Key != old || !rows[1].ViewedAt.Equal(at) {
		t.Fatalf("anchors: %+v %v", rows, err)
	}
	rows, err = s.ContinueSeriesHistory(ctx, 1, 1)
	if err != nil || len(rows) != 1 || rows[0].Key != recent {
		t.Fatalf("limit: %+v %v", rows, err)
	}
	rows, err = s.ContinueSeriesHistory(ctx, 0, 50)
	if err != nil || len(rows) != 0 {
		t.Fatalf("anonymous leaked history: %+v %v", rows, err)
	}
}

// The hot Home read has bounded output regardless of other viewers' history.
// The plan must seek this viewer before ranking episodes, then fetch only the
// selected snapshots by primary key.
func TestContinueSeriesHistoryViewerBoundedPlan(t *testing.T) {
	s, now := newTestState(t)
	args := []any{int64(1), int64(core.EpisodeKey(0, 0, 0)), int64(core.EpisodeKey(0, 0, 0)) + 1000000000000000, 2}
	insert := func(firstUser, users, events int) {
		t.Helper()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO state_history(user_id,rating_key,viewed_at,completed) VALUES(?,?,?,1)`)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for uid := firstUser; uid < firstUser+users; uid++ {
			for n := 0; n < events; n++ {
				if _, err := stmt.ExecContext(ctx, uid, int64(core.EpisodeKey(int64(n%3+1), 1, n%9+1)), now.Add(time.Duration(n)*time.Second).UnixNano()); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	read := func() {
		t.Helper()
		measured, stats := store.WithQueryStats(ctx)
		got, err := s.ContinueSeriesHistory(measured, 1, 2)
		if err != nil || len(got) != 2 || stats.Queries() > 5 || stats.Rows() > 10 {
			t.Fatalf("history=%v err=%v queries=%d rows=%d", got, err, stats.Queries(), stats.Rows())
		}
		if got[0].Key != core.EpisodeKey(3, 1, 3) || got[1].Key != core.EpisodeKey(2, 1, 2) {
			t.Fatalf("anchors: %+v", got)
		}
	}
	plan := func(query string) string {
		t.Helper()
		prefix := "EXPLAIN QUERY PLAN "
		if store.DialectOf(s.db) == store.Postgres {
			prefix = "EXPLAIN "
		}
		rows, err := s.db.QueryContext(ctx, prefix+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var detail string
			if store.DialectOf(s.db) == store.Postgres {
				err = rows.Scan(&detail)
			} else {
				var id, parent, unused int
				err = rows.Scan(&id, &parent, &unused, &detail)
			}
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(lines, "\n")
	}
	insert(1, 1, 300)
	read()
	for _, size := range []int{10, 100} {
		insert(2+size, size, 100)
		analyze := "ANALYZE state_history"
		if store.DialectOf(s.db) == store.Postgres {
			// Mark the bulk-loaded fixture all-visible so PostgreSQL can
			// cost an index-only read as it does after normal autovacuum.
			analyze = "VACUUM ANALYZE state_history"
		}
		if _, err := s.db.ExecContext(ctx, analyze); err != nil {
			t.Fatal(err)
		}
		read()
		p := plan(continueSeriesQuery)
		t.Logf("after %d other viewers:\n%s", size, p)
		if store.DialectOf(s.db) == store.Postgres {
			if !strings.Contains(p, "state_history_continue_viewer") || !strings.Contains(p, "Index Cond: ((user_id =") || !strings.Contains(p, "state_history_pkey") || strings.Contains(p, "Seq Scan on state_history") {
				t.Fatalf("unbounded plan:\n%s", p)
			}
		} else if !strings.Contains(p, "USING COVERING INDEX state_history_continue_viewer (user_id=? AND completed=? AND rating_key>? AND rating_key<?)") || !strings.Contains(p, "SEARCH h USING INTEGER PRIMARY KEY") || strings.Contains(p, "SCAN h") {
			t.Fatalf("unbounded plan:\n%s", p)
		}
	}
}
