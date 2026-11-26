package state

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

// state/15 removes watch evidence written by media that was not the title's
// own: a detail-page trailer, theme or preview a client reported against its
// parent, judged against the trailer's length. Such a report finished a film
// nobody played, and its taste evidence seeded "Because you watched" rows.
//
// The evidence is identifiable because it carries the foreign duration. A play
// whose duration is a fraction of the title's catalogue runtime is that media;
// its "started" and "completed" history rows fall inside the play's window and
// are removed with it, with the view count they added. A watch row whose stored
// duration is foreign was last written by that media: it loses the history
// row logged at that same instant, the duration and its resume point (a later
// report without a duration would otherwise be judged against the trailer).
// Imported history and rows with no catalogue runtime are never touched.
//
// It runs once, inside the migration. Its cost is bounded by the viewers' own
// watch rows and plays, never by the library.
func init() {
	store.Register("state", 15, `SELECT 1;`)
	store.RegisterSQLiteApply("state", 15, func(ctx context.Context, tx *sql.Tx) error {
		return repairForeignMediaWatch(ctx, tx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`)
	})
	store.RegisterPostgresApply("state", 15, func(ctx context.Context, tx *sql.Tx) error {
		return repairForeignMediaWatch(ctx, tx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=?`)
	})
}

type foreignWatchItem struct{ user, key int64 }

func repairForeignMediaWatch(ctx context.Context, tx *sql.Tx, tableExists string) error {
	has := func(table string) (bool, error) {
		var n int
		err := tx.QueryRowContext(ctx, tableExists, table).Scan(&n)
		return n > 0, err
	}
	if ok, err := has("catalog_titles"); err != nil || !ok {
		return err
	}
	ratio := int64(frontend.ForeignMediaRatio)
	removed := map[foreignWatchItem]int{}
	var touched []foreignWatchItem
	seen := map[foreignWatchItem]bool{}
	touch := func(it foreignWatchItem) {
		if !seen[it] {
			seen[it] = true
			touched = append(touched, it)
		}
	}

	if ok, err := has("personalize_plays"); err != nil {
		return err
	} else if ok {
		type play struct {
			id                 int64
			item               foreignWatchItem
			startedMS, endedMS int64
		}
		rows, err := tx.QueryContext(ctx, `SELECT p.id, p.user_id, p.item_key, p.started_at, p.updated_at
			FROM personalize_plays p JOIN catalog_titles t ON t.key = p.item_key
			WHERE `+videoKeys("p.item_key")+` AND p.duration_ms > 0 AND t.runtime_sec > 0 AND p.duration_ms * ? < t.runtime_sec * 1000`, ratio)
		if err != nil {
			return err
		}
		var plays []play
		for rows.Next() {
			var p play
			if err := rows.Scan(&p.id, &p.item.user, &p.item.key, &p.startedMS, &p.endedMS); err != nil {
				rows.Close()
				return err
			}
			plays = append(plays, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, p := range plays {
			// History rows are written in the same transaction as the
			// progress event the play records; a second of slack covers
			// the millisecond rounding of the play's bounds.
			from, to := (p.startedMS-1000)*1_000_000, (p.endedMS+1000)*1_000_000
			var completed int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_history
				WHERE user_id = ? AND rating_key = ? AND imported = 0 AND completed = 1 AND viewed_at BETWEEN ? AND ?`,
				p.item.user, p.item.key, from, to).Scan(&completed); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM state_history
				WHERE user_id = ? AND rating_key = ? AND imported = 0 AND viewed_at BETWEEN ? AND ?`,
				p.item.user, p.item.key, from, to); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM personalize_plays WHERE id = ?`, p.id); err != nil {
				return err
			}
			removed[p.item] += completed
			touch(p.item)
		}
	}

	foreignDuration := map[foreignWatchItem]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT w.user_id, w.rating_key
		FROM state_watch w JOIN catalog_titles t ON t.key = w.rating_key
		WHERE `+videoKeys("w.rating_key")+` AND w.duration_ns > 0 AND t.runtime_sec > 0 AND w.duration_ns * ? < t.runtime_sec * 1000000000`, ratio)
	if err != nil {
		return err
	}
	for rows.Next() {
		var it foreignWatchItem
		if err := rows.Scan(&it.user, &it.key); err != nil {
			rows.Close()
			return err
		}
		foreignDuration[it] = true
		touch(it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, it := range touched {
		var viewCount int
		var lastViewed int64
		err := tx.QueryRowContext(ctx, `SELECT view_count, last_viewed_at FROM state_watch WHERE user_id = ? AND rating_key = ?`, it.user, it.key).Scan(&viewCount, &lastViewed)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if foreignDuration[it] && lastViewed > 0 {
			// The stored duration came from the row's last write, and a
			// write logs its history row at the same instant.
			var completed int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_history
				WHERE user_id = ? AND rating_key = ? AND imported = 0 AND completed = 1 AND viewed_at = ?`,
				it.user, it.key, lastViewed).Scan(&completed); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM state_history
				WHERE user_id = ? AND rating_key = ? AND imported = 0 AND viewed_at = ?`, it.user, it.key, lastViewed); err != nil {
				return err
			}
			removed[it] += completed
		}
		var completed int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_history WHERE user_id = ? AND rating_key = ? AND completed = 1`,
			it.user, it.key).Scan(&completed); err != nil {
			return err
		}
		// Only the completions this repair removed come off the count; a
		// count without history (an import, say) is kept, and the count
		// never rises.
		count := max(viewCount-removed[it], min(completed, viewCount), 0)
		clear := 0
		if foreignDuration[it] {
			clear = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE state_watch SET view_count = ?,
				counted = CASE WHEN ? = 0 THEN 0 ELSE counted END,
				view_offset_ns = CASE WHEN ? = 1 THEN 0 ELSE view_offset_ns END,
				duration_ns = CASE WHEN ? = 1 THEN 0 ELSE duration_ns END,
				version = version + 1
			WHERE user_id = ? AND rating_key = ?`, count, count, clear, clear, it.user, it.key); err != nil {
			return err
		}
	}
	return nil
}

// videoKeys limits the repair to movie and episode keys, the only titles a
// trailer, theme or preview is attributed to.
func videoKeys(column string) string {
	const kindBase = 1_000_000_000_000_000 // core.RatingKey kind stride
	in := func(kind core.KeyKind) string {
		lo := int64(kind) * kindBase
		return "(" + column + " >= " + strconv.FormatInt(lo, 10) + " AND " + column + " < " + strconv.FormatInt(lo+kindBase, 10) + ")"
	}
	return "(" + in(core.KindMovie) + " OR " + in(core.KindEpisode) + ")"
}
