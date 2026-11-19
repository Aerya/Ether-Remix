package releasedb

import (
	"context"
	"encoding/json"

	"github.com/braedonsaunders/ether/internal/core"
)

// PruneSelection removes rejected locators in bounded batches. Selection errors
// abort without deleting that entry. A concurrently refreshed payload is kept.
// Raw, non-playable metadata remains available for diagnostics and fresh searches.
func (l Library) PruneSelection(ctx context.Context) (int64, error) {
	if l.Accept == nil {
		return 0, nil
	}
	var cursor, removed int64
	for {
		rows, err := l.Store.db.QueryContext(ctx, `SELECT rowid,title_key,payload,last_seen FROM pse_release_library WHERE rowid>? ORDER BY rowid LIMIT 256`, cursor)
		if err != nil {
			return removed, err
		}
		type entry struct {
			row     int64
			key     core.RatingKey
			payload string
			seen    int64
		}
		batch := []entry{}
		for rows.Next() {
			var e entry
			if err = rows.Scan(&e.row, &e.key, &e.payload, &e.seen); err != nil {
				break
			}
			batch = append(batch, e)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return removed, err
		}
		if len(batch) == 0 {
			return removed, nil
		}
		for _, e := range batch {
			cursor = e.row
			raw, err := l.Open(e.payload)
			if err != nil {
				return removed, err
			}
			var r core.Release
			if err = json.Unmarshal([]byte(raw), &r); err != nil {
				return removed, err
			}
			allowed, err := l.Accept(ctx, e.key, r)
			if err != nil {
				return removed, err
			}
			if allowed {
				continue
			}
			result, err := l.Store.db.ExecContext(ctx, `DELETE FROM pse_release_library WHERE rowid=? AND payload=? AND last_seen=?`, e.row, e.payload, e.seen)
			if err != nil {
				return removed, err
			}
			n, _ := result.RowsAffected()
			removed += n
		}
	}
}
