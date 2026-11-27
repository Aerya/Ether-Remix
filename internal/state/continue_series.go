package state

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

var allocatedContinueSeriesWhere = fmt.Sprintf("completed=1 AND rating_key>%d AND rating_key<%d",
	core.AllocatedEpisodeBase, core.AllocatedEpisodeBase+core.AllocatedKeyMaxSeq)

func init() {
	store.Register("state", 11, `CREATE INDEX state_history_continue_viewer ON state_history(user_id,completed,rating_key,viewed_at DESC,id DESC);`)
	store.Register("state", 16, `CREATE INDEX IF NOT EXISTS state_history_continue_allocated
		ON state_history(user_id,viewed_at DESC,id DESC) WHERE `+allocatedContinueSeriesWhere+`;`)
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
	// Capture the pending writes before reading history: an entry that
	// drains during this read is already represented by the durable state.
	var pending map[core.RatingKey]pendingWrite
	if s.pending != nil {
		pending = s.pending.snapshot(uid, s.pending.userKeys(uid))
	}
	limit = min(max(limit, 1), 1000)
	rows, err := s.db.QueryContext(ctx, continueSeriesHistoryQuery, uid,
		int64(core.EpisodeKey(0, 0, 0)), int64(core.EpisodeKey(0, 0, 0))+1000000000000000, limit)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	allocated, err := s.db.QueryContext(ctx, `SELECT id,user_id,rating_key,viewed_at,completed,view_type,device_id,snapshot
		FROM state_history WHERE user_id=? AND `+allocatedContinueSeriesWhere+`
		ORDER BY viewed_at DESC,id DESC LIMIT ?`, uid, limit)
	if err != nil {
		return nil, err
	}
	defer allocated.Close()
	for allocated.Next() {
		h, err := scanHistory(allocated)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := allocated.Err(); err != nil {
		return nil, err
	}
	allocated.Close()

	var keys []core.RatingKey
	for key, write := range pending {
		if key.Kind() == core.KindEpisode && (write.kind == "scrobble" || (write.kind == "progress" && write.progress.state != "progress")) {
			keys = append(keys, key)
		}
	}
	var statuses map[core.RatingKey]core.WatchStatus
	if len(keys) > 0 {
		statuses, err = s.GetMany(ctx, uid, keys)
		if err != nil {
			return nil, err
		}
	}
	for _, key := range keys {
		write := pending[key]
		ws := statuses[key]
		progress := write.progress
		completes := write.kind == "scrobble" || progress.completes()
		if !completes && !strings.HasPrefix(progress.state, "explicit-") && ws.Duration > 0 {
			// Timelines can omit duration and reuse the last reported value.
			completes = float64(progress.offset) >= WatchedThreshold*float64(ws.Duration)
		}
		if completes && ws.ViewCount > 0 && ws.ViewOffset == 0 && ws.LastViewedAt.Equal(progress.at) {
			out = append(out, core.HistoryEntry{UserID: uid, Key: key, ViewedAt: progress.at, Completed: true})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ViewedAt.Equal(out[j].ViewedAt) {
			return out[i].ViewedAt.After(out[j].ViewedAt)
		}
		if out[i].ID == 0 && out[j].ID == 0 {
			return pending[out[i].Key].seq > pending[out[j].Key].seq
		}
		if out[i].ID == 0 || out[j].ID == 0 {
			return out[i].ID == 0
		}
		return out[i].ID > out[j].ID
	})
	var allocatedKeys []core.RatingKey
	for _, entry := range out {
		if entry.Key.Allocated() {
			allocatedKeys = append(allocatedKeys, entry.Key)
		}
	}
	var parents map[core.RatingKey]Ancestry
	if len(allocatedKeys) > 0 {
		parents, err = s.resolveAncestry(ctx, allocatedKeys)
		if err != nil {
			return nil, err
		}
	}
	seen := make(map[core.RatingKey]bool, len(out))
	distinct := out[:0]
	for _, entry := range out {
		show := parents[entry.Key].ShowKey
		if show == 0 {
			show = entry.Key.Parent().Parent()
		}
		if show == 0 && entry.Snapshot != nil {
			show = entry.Snapshot.ShowKey
		}
		if show == 0 {
			// Unlabeled allocated entries are resolved in the bounded title
			// batch by the caller. Keep each distinct identity for that read.
			show = entry.Key
		}
		if seen[show] {
			continue
		}
		seen[show] = true
		distinct = append(distinct, entry)
		if len(distinct) == limit {
			break
		}
	}
	return distinct, nil
}

const continueSeriesQuery = continueSeriesHistoryQuery
const continueSeriesIndex = `CREATE INDEX state_history_continue_viewer ON state_history(user_id,completed,rating_key,viewed_at DESC,id DESC);`
