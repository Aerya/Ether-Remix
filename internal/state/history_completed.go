package state

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// completedBetweenQuery is one row per key the viewer completed in the
// window, latest completion first. It reads only this viewer's completed rows
// on state_history_continue_series (user_id, completed, rating_key, viewed_at),
// covering, so started-only rows and later rows never take a share of a
// fixed page (#799) and nothing in it grows with the library.
const completedBetweenQuery = `SELECT rating_key, MAX(viewed_at) AS latest FROM state_history
 WHERE user_id=? AND completed=1 AND viewed_at>=? AND viewed_at<?%s
 GROUP BY rating_key ORDER BY latest DESC, rating_key LIMIT ? OFFSET ?`

// completedCountsQuery counts the distinct films and shows (an episode or
// season counts as its show) userID completed in the window, on the same
// covering index, answering one row.
const completedCountsQuery = `SELECT
 COUNT(DISTINCT CASE WHEN rating_key/1000000000000000=1 THEN rating_key END),
 COUNT(DISTINCT CASE rating_key/1000000000000000
  WHEN 2 THEN rating_key%1000000000000000
  WHEN 3 THEN rating_key%1000000000000000/10000
  WHEN 4 THEN rating_key%1000000000000000/10000000 END)
 FROM state_history WHERE user_id=? AND completed=1 AND viewed_at>=? AND viewed_at<?`

// CompletedBetween lists the distinct keys of kinds (every kind when empty)
// userID completed in [from, to), most recently completed first, limit
// entries from offset (every entry when limit <= 0). Each entry carries the
// key and its latest completion in the window.
func (s *State) CompletedBetween(ctx context.Context, userID int64, from, to time.Time, kinds []core.KeyKind, offset, limit int) ([]core.HistoryEntry, error) {
	if userID <= 0 || !to.After(from) {
		return nil, nil
	}
	if limit <= 0 {
		limit = -1
	}
	filter := ""
	if len(kinds) > 0 {
		list := make([]string, len(kinds))
		for i, k := range kinds {
			list[i] = strconv.Itoa(int(k))
		}
		filter = " AND rating_key/1000000000000000 IN (" + strings.Join(list, ",") + ")"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(completedBetweenQuery, filter), userID, from.UnixNano(), to.UnixNano(), limit, max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.HistoryEntry
	for rows.Next() {
		var key, at int64
		if err := rows.Scan(&key, &at); err != nil {
			return nil, err
		}
		out = append(out, core.HistoryEntry{UserID: userID, Key: core.RatingKey(key), ViewedAt: time.Unix(0, at).UTC(), Completed: true})
	}
	return out, rows.Err()
}

// CompletedCounts reports how many distinct films and shows userID completed
// in [from, to), an episode or season counting as its show.
func (s *State) CompletedCounts(ctx context.Context, userID int64, from, to time.Time) (movies, shows int, err error) {
	if userID <= 0 || !to.After(from) {
		return 0, 0, nil
	}
	err = s.db.QueryRowContext(ctx, completedCountsQuery, userID, from.UnixNano(), to.UnixNano()).Scan(&movies, &shows)
	return movies, shows, err
}
