package watchlist

import (
	"context"
	"database/sql"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("watchlist_title_match", "watchlist_episodes")
	store.Register("watchlist_title_match", 1, `
CREATE INDEX IF NOT EXISTS watchlist_episodes_title_match ON watchlist_episodes(item_key);
`)
}

// RekeyTitle moves watchlist entries and remembered episodes in the caller’s transaction.
func RekeyTitle(ctx context.Context, tx *sql.Tx, old, next *core.Title, ranges []core.TitleKeyRange) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO watchlist_items(user_id,item_key,source,title,added,checked,available,notified,warmed)
		SELECT user_id,?,source,?,added,checked,available,notified,warmed FROM watchlist_items WHERE item_key=?
		ON CONFLICT(user_id,item_key,source) DO NOTHING`, int64(next.Key), next.Title, int64(old.Key)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM watchlist_items WHERE item_key=?`, int64(old.Key)); err != nil {
		return err
	}
	if old.Type == core.TypeShow {
		for _, r := range ranges {
			if core.RatingKey(r.First).Kind() != core.KindEpisode {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO watchlist_episodes(user_id,item_key,episode_key,at)
				SELECT user_id,?,episode_key+?,at FROM watchlist_episodes WHERE item_key=? AND episode_key BETWEEN ? AND ?
				ON CONFLICT(user_id,item_key,episode_key) DO NOTHING`, int64(next.Key), r.Delta, int64(old.Key), r.First, r.Last); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM watchlist_episodes WHERE item_key=? AND episode_key BETWEEN ? AND ?`,
				int64(old.Key), r.First, r.Last); err != nil {
				return err
			}
		}
	}
	return nil
}
