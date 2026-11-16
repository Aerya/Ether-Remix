package state

import (
	"context"
	"database/sql"
	"github.com/braedonsaunders/ether/internal/core"
	"strings"
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

// Pin the hot-path access strategy, not timings sensitive to CI machines.
func TestContinueSeriesHistoryPlanScopesViewerAndUsesPrimaryKey(t *testing.T) {
	s, _ := newTestState(t)
	rows, err := s.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+continueSeriesHistoryQuery, 1, int64(core.EpisodeKey(0, 0, 0)), int64(core.EpisodeKey(0, 0, 0))+1000000000000000, 200)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(plan, "\n")
	t.Log("\n" + text)
	if !strings.Contains(text, "USING COVERING INDEX state_history_continue_viewer (user_id=? AND completed=? AND rating_key>? AND rating_key<?)") {
		t.Fatalf("viewer index not used:\n%s", text)
	}
	if !strings.Contains(text, "SEARCH h USING INTEGER PRIMARY KEY (rowid=?)") {
		t.Fatalf("snapshots not loaded by primary key:\n%s", text)
	}
	for _, line := range plan {
		if strings.HasPrefix(line, "SCAN state_history") || strings.HasPrefix(line, "SCAN h ") {
			t.Fatalf("unbounded history scan: %s", line)
		}
	}
}
