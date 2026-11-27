package viewerpolicy

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/braedonsaunders/ether/internal/store"
)

// v8: Recommendations pages replaced personalize_home_layout (dropped by
// personalize/6), so page saves advance the epoch the way layout saves did.
func init() {
	store.Register("viewer_policy", 8, "SELECT 1;")
	store.RegisterSQLiteApply("viewer_policy", 8, func(ctx context.Context, tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='personalize_pages'`).Scan(&n); err != nil || n == 0 {
			return err
		}
		for _, event := range []string{"insert", "update", "delete"} {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS viewer_policy_personalize_pages_%s AFTER %s ON personalize_pages BEGIN UPDATE viewer_policy_epoch SET generation=generation+1 WHERE id=1; END;`, event, event)); err != nil {
				return err
			}
		}
		return nil
	})
	store.RegisterDialect("viewer_policy", 8, store.Postgres, pgRowsChanged+pgGuard("personalize_pages", pgRowsChangedTriggers("personalize_pages")))
}
