package releasedb

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_index_source_history", 1, `
CREATE TABLE pse_index_source_history(
 day TEXT NOT NULL, source_id TEXT NOT NULL, media_type TEXT NOT NULL,
 searches INTEGER NOT NULL DEFAULT 0,
 results INTEGER NOT NULL DEFAULT 0,
 failed INTEGER NOT NULL DEFAULT 0,
 denied INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(day,source_id,media_type)
);
CREATE INDEX pse_index_source_history_source ON pse_index_source_history(source_id,day,media_type);`)
}

// Agrégats durables : une recherche par titre et par source, séparée par
// type. Un refus de quota ne compte pas comme une recherche effectuée.
func (s *Service) recordSourceHistory(ctx context.Context, e SearchEvent, now time.Time) error {
	searches, failed, denied := 1, 0, 0
	if e.Status == "budget" {
		searches, denied = 0, 1
	}
	if e.Status == "error" || e.Status == "partial" {
		failed = 1
	}
	_, err := s.st.db.ExecContext(ctx, `INSERT INTO pse_index_source_history(day,source_id,media_type,searches,results,failed,denied) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(day,source_id,media_type) DO UPDATE SET searches=searches+excluded.searches,results=results+excluded.results,failed=failed+excluded.failed,denied=denied+excluded.denied`,
		now.UTC().Format("2006-01-02"), e.SourceID, string(e.Type), searches, e.Results, failed, denied)
	return err
}
