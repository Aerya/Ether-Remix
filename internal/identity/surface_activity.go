package identity

import (
	"context"
	"database/sql"
	"time"
)

// LastSurfaceActivity reports real use of active credentials, in Unix seconds.
// Issuance alone is not activity. Revoked/expired grants and inactive viewers
// are excluded. It exposes neither credentials nor device identifiers.
func LastSurfaceActivity(ctx context.Context, db *sql.DB, surface string, now time.Time) (map[int64]int64, error) {
	rows, err := db.QueryContext(ctx, `SELECT g.viewer_id, MAX(g.last_used) FROM identity_grants g JOIN auth_users u ON u.id=g.viewer_id WHERE g.surface=? AND g.revoked=0 AND g.expires>? AND g.last_used>g.created AND u.active=1 GROUP BY g.viewer_id`, surface, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	activity := map[int64]int64{}
	for rows.Next() {
		var id, at int64
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		activity[id] = at / 1000
	}
	return activity, rows.Err()
}
