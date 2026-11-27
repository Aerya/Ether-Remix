package state

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// PluginHistoryPlays lists a viewer's completed plays of plugin items in
// [from, to), oldest first, at most limit (a recap reads one viewer's own
// period through state_history_user, never the library).
func (s *State) PluginHistoryPlays(ctx context.Context, userID int64, from, to time.Time, limit int) ([]core.HistoryPlay, error) {
	if limit <= 0 || limit > 200000 {
		limit = 200000
	}
	lo, hi := int64(core.PluginKey(1)), int64(core.PluginKey(core.PluginKeyMaxSeq-1))
	rows, err := s.db.QueryContext(ctx, `SELECT rating_key, viewed_at FROM state_history
		WHERE user_id = ? AND viewed_at >= ? AND viewed_at < ? AND completed = 1 AND rating_key BETWEEN ? AND ?
		ORDER BY viewed_at LIMIT ?`, userID, from.UnixNano(), to.UnixNano(), lo, hi, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.HistoryPlay
	for rows.Next() {
		var key, at int64
		if err := rows.Scan(&key, &at); err != nil {
			return nil, err
		}
		out = append(out, core.HistoryPlay{Key: core.RatingKey(key), At: time.Unix(0, at)})
	}
	return out, rows.Err()
}
