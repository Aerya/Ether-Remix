package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("state_title_match", "state")
	store.Register("state_title_match", 1, `
CREATE INDEX IF NOT EXISTS state_history_title_match ON state_history(rating_key);
CREATE INDEX IF NOT EXISTS state_playlist_items_title_match ON state_playlist_items(rating_key);
`)
}

// RekeyTitle moves watch state, history and playlist entries in the caller’s transaction.
func RekeyTitle(ctx context.Context, tx *sql.Tx, ranges []core.TitleKeyRange) error {
	for _, r := range ranges {
		if err := rekeyWatch(ctx, tx, r); err != nil {
			return err
		}
		// An imported destination history row wins. Playlist entries keep their positions.
		if _, err := tx.ExecContext(ctx, `DELETE FROM state_history AS old WHERE old.rating_key BETWEEN ? AND ?
			AND EXISTS (SELECT 1 FROM state_history AS dest WHERE dest.rating_key=old.rating_key+? AND dest.user_id=old.user_id AND dest.viewed_at=old.viewed_at AND dest.imported=1 AND old.imported=1)`, r.First, r.Last, r.Delta); err != nil {
			return fmt.Errorf("title match: state_history: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE state_history AS old SET rating_key=rating_key+? WHERE old.rating_key BETWEEN ? AND ?`, r.Delta, r.First, r.Last); err != nil {
			return fmt.Errorf("title match: state_history: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE state_playlist_items AS old SET rating_key=rating_key+? WHERE old.rating_key BETWEEN ? AND ?`, r.Delta, r.First, r.Last); err != nil {
			return fmt.Errorf("title match: state_playlist_items: %w", err)
		}
	}
	return nil
}

func rekeyWatch(ctx context.Context, tx *sql.Tx, r core.TitleKeyRange) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO state_watch(user_id,rating_key,view_offset_ns,duration_ns,view_count,counted,last_viewed_at,user_rating,updated_at,version,watch_at,rating_at)
		SELECT user_id,rating_key+?,view_offset_ns,duration_ns,view_count,counted,last_viewed_at,user_rating,?,version+1,watch_at,rating_at
		FROM state_watch WHERE rating_key BETWEEN ? AND ?
		ON CONFLICT(user_id,rating_key) DO UPDATE SET
		view_offset_ns=CASE WHEN excluded.watch_at>state_watch.watch_at OR excluded.last_viewed_at>state_watch.last_viewed_at THEN excluded.view_offset_ns ELSE state_watch.view_offset_ns END,
		duration_ns=CASE WHEN excluded.watch_at>state_watch.watch_at OR excluded.last_viewed_at>state_watch.last_viewed_at THEN excluded.duration_ns ELSE state_watch.duration_ns END,
		counted=CASE WHEN excluded.watch_at>state_watch.watch_at OR excluded.last_viewed_at>state_watch.last_viewed_at THEN excluded.counted ELSE state_watch.counted END,
		view_count=CASE WHEN excluded.view_count>state_watch.view_count THEN excluded.view_count ELSE state_watch.view_count END,
		last_viewed_at=CASE WHEN excluded.last_viewed_at>state_watch.last_viewed_at THEN excluded.last_viewed_at ELSE state_watch.last_viewed_at END,
		user_rating=CASE WHEN excluded.rating_at>state_watch.rating_at OR state_watch.rating_at=0 AND state_watch.user_rating=0 THEN excluded.user_rating ELSE state_watch.user_rating END,
		rating_at=CASE WHEN excluded.rating_at>state_watch.rating_at THEN excluded.rating_at ELSE state_watch.rating_at END,
		watch_at=CASE WHEN excluded.watch_at>state_watch.watch_at THEN excluded.watch_at ELSE state_watch.watch_at END,
		updated_at=excluded.updated_at,version=state_watch.version+1`, r.Delta, time.Now().UnixNano(), r.First, r.Last)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM state_watch WHERE rating_key BETWEEN ? AND ?`, r.First, r.Last)
	return err
}
