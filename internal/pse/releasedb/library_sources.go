package releasedb

import (
	"context"
	"database/sql"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// LibraryKeys reads observations only: browsing/building a source library never
// searches an addon or adds a torrent to a debrid account.
func LibraryKeys(ctx context.Context, db *sql.DB, source string) ([]core.RatingKey, error) {
	if !sourceActive(source) {
		return nil, nil
	}
	age := MaxAge
	if source == XDCCSourceID {
		age = XDCCMaxAge
	}
	rows, err := db.QueryContext(ctx, `SELECT title_key FROM pse_releases WHERE source_id=? AND last_seen>=? GROUP BY title_key ORDER BY MAX(last_seen) DESC,title_key LIMIT 5000`, source, time.Now().Add(-age).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.RatingKey{}
	seen := map[core.RatingKey]bool{}
	for rows.Next() {
		var k core.RatingKey
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if k.Kind() == core.KindEpisode {
			k = core.ShowKey(k.TMDB())
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out, rows.Err()
}
