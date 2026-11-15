package state

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

// ContinueSeriesHistory returns the latest completed episode per series,
// newest first, without an age cutoff. Reduce before limiting so repeated
// episodes and movies cannot evict another series from the candidate list.
func (s *State) ContinueSeriesHistory(ctx context.Context, uid int64, limit int) ([]core.HistoryEntry, error) {
	if uid <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `WITH ranked AS (
 SELECT id, viewed_at, ROW_NUMBER() OVER (
 PARTITION BY rating_key/10000000 ORDER BY viewed_at DESC,id DESC) AS position
 FROM state_history WHERE user_id=? AND completed=1 AND rating_key>=? AND rating_key<?
 ) SELECT `+historyColumns+` FROM state_history WHERE id IN (
 SELECT id FROM ranked WHERE position=1 ORDER BY viewed_at DESC,id DESC LIMIT ?
 ) ORDER BY viewed_at DESC,id DESC`, uid,
		int64(core.EpisodeKey(0, 0, 0)), int64(core.EpisodeKey(0, 0, 0))+1000000000000000, min(max(limit, 1), 200))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.HistoryEntry
	for rows.Next() {
		h, err := scanHistory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
