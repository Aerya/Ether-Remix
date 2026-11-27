package playback

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("playback_title_match", "playback")
	store.Register("playback_title_match", 1, `
CREATE INDEX IF NOT EXISTS playback_resume_title_match ON playback_resume(rating_key);
`)
}

// RekeyTitle moves stored resume selections, keeping existing destination rows.
func RekeyTitle(ctx context.Context, tx *sql.Tx, ranges []core.TitleKeyRange) error {
	for _, r := range ranges {
		if _, err := tx.ExecContext(ctx, `DELETE FROM playback_resume AS old WHERE old.rating_key BETWEEN ? AND ?
			AND EXISTS (SELECT 1 FROM playback_resume AS dest WHERE dest.rating_key=old.rating_key+? AND dest.user_id=old.user_id)`, r.First, r.Last, r.Delta); err != nil {
			return fmt.Errorf("title match: playback_resume: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE playback_resume AS old SET rating_key=rating_key+? WHERE old.rating_key BETWEEN ? AND ?`, r.Delta, r.First, r.Last); err != nil {
			return fmt.Errorf("title match: playback_resume: %w", err)
		}
	}
	return nil
}
