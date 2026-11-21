package state

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("state", 11, `CREATE INDEX state_history_continue_viewer ON state_history(user_id,completed,rating_key,viewed_at DESC,id DESC);`)
}

// Filter the viewer before ranking. Only the selected IDs load snapshots,
// through primary-key lookups rather than a second history-table scan.
const continueSeriesHistoryQuery = `WITH ranked AS (
 SELECT id, viewed_at, ROW_NUMBER() OVER (
 PARTITION BY rating_key/10000000 ORDER BY viewed_at DESC,id DESC) AS position
 FROM state_history WHERE user_id=? AND completed=1 AND rating_key>=? AND rating_key<?
 ), candidates AS (
 SELECT id, viewed_at FROM ranked WHERE position=1 ORDER BY viewed_at DESC,id DESC LIMIT ?
 ) SELECT h.id,h.user_id,h.rating_key,h.viewed_at,h.completed,h.view_type,h.device_id,h.snapshot
 FROM candidates c JOIN state_history h ON h.id=c.id
 ORDER BY c.viewed_at DESC,c.id DESC`

// ContinueSeriesHistory returns the latest completed episode per series,
// newest first, without an age cutoff. Reduce before limiting so repeated
// episodes and movies cannot evict another series from the candidate list.
func (s *State) ContinueSeriesHistory(ctx context.Context, uid int64, limit int) ([]core.HistoryEntry, error) {
	if uid <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, continueSeriesHistoryQuery, uid,
		int64(core.EpisodeKey(0, 0, 0)), int64(core.EpisodeKey(0, 0, 0))+1000000000000000, min(max(limit, 1), 1000)) // PSE-HOOK: séries examinées de Continuer à regarder (plafond réglable jusqu’à 1000) — voir docs/pse/FORK.fr.md
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
