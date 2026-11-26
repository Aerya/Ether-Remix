package favorites

import (
	"context"
	"database/sql"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("favorites_title_match", "favorites")
	store.Register("favorites_title_match", 1, `
CREATE INDEX IF NOT EXISTS favorites_title_match ON favorites_items(kind,subject_id);
`)
}

// RekeyTitle moves title favorites, keeping existing destination favorites.
func RekeyTitle(ctx context.Context, tx *sql.Tx, ranges []core.TitleKeyRange) error {
	for _, r := range ranges {
		first, last := core.RatingKey(r.First).String(), core.RatingKey(r.Last).String()
		_, err := tx.ExecContext(ctx, `INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at,sort_order,role)
			SELECT viewer_id,kind,CAST(CAST(subject_id AS BIGINT)+? AS TEXT),added_at,sort_order,role FROM favorites_items
			WHERE kind='title' AND subject_id BETWEEN ? AND ?
			ON CONFLICT(viewer_id,kind,subject_id) DO NOTHING`, r.Delta, first, last)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM favorites_items WHERE kind='title' AND subject_id BETWEEN ? AND ?`, first, last); err != nil {
			return err
		}
	}
	return nil
}
