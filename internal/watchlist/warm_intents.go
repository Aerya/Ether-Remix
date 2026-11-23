package watchlist

import (
	"context"
	"database/sql"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("watchlist", 8, `CREATE TABLE watchlist_warm_intents(user_id INTEGER NOT NULL,item_key INTEGER NOT NULL,added INTEGER NOT NULL,PRIMARY KEY(user_id,item_key)); CREATE INDEX watchlist_warm_intents_newest ON watchlist_warm_intents(added);`)
	// Titles already on watchlists when warm intents were introduced were never
	// queued, so Prewarm skipped them. Queue the newest once; the consumer reads
	// intents only while Prewarm is on.
	store.Register("watchlist", 9, `INSERT INTO watchlist_warm_intents(user_id,item_key,added) SELECT user_id,item_key,MAX(added) FROM watchlist_items WHERE 1=1 GROUP BY user_id,item_key ORDER BY MAX(added) DESC LIMIT 100 ON CONFLICT(user_id,item_key) DO NOTHING;`)
}

type intentWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Queue only a new membership, before inserting its first source. Snapshot
// refreshes and duplicate sources never recreate a consumed warming intent.
func queueWarmAdd(ctx context.Context, db intentWriter, uid int64, key core.RatingKey, added int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO watchlist_warm_intents(user_id,item_key,added) SELECT ?,?,? WHERE NOT EXISTS(SELECT 1 FROM watchlist_items WHERE user_id=? AND item_key=?) ON CONFLICT(user_id,item_key) DO NOTHING`, uid, key, added, uid, key)
	return err
}
