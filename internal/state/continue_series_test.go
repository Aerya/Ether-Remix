package state

import (
	"context"
	"database/sql"
	"github.com/braedonsaunders/ether/internal/core"
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
