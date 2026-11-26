package state

import (
	"context"
	"database/sql"
	"errors"

	"github.com/braedonsaunders/ether/internal/core"
)

// ShowPosition returns the furthest watched or in-progress episode of one show.
// The viewer and episode-key range use the watch primary key; no catalogue or
// other viewer's history is read. A show without activity returns zero status.
func (s *State) ShowPosition(ctx context.Context, uid int64, show core.RatingKey) (core.WatchStatus, error) {
	var key core.RatingKey
	var lo, hi core.RatingKey
	if !show.Allocated() {
		lo, hi = core.EpisodeKey(show.TMDB(), 0, 0), core.EpisodeKey(show.TMDB()+1, 0, 0)
	}
	err := s.db.QueryRowContext(ctx, `SELECT rating_key FROM (
 SELECT rating_key,(rating_key-4000000000000000)/10000%1000 AS season,rating_key%10000 AS episode
 FROM state_watch WHERE user_id=? AND rating_key>=? AND rating_key<? AND (view_count>0 OR view_offset_ns>0)
 UNION ALL
 SELECT w.rating_key,a.season,a.episode FROM catalog_allocated_child a JOIN state_watch w ON w.rating_key=a.key AND w.user_id=?
 WHERE a.show_key=? AND a.kind=4 AND (w.view_count>0 OR w.view_offset_ns>0)
 ) AS progress ORDER BY season DESC,episode DESC,rating_key DESC LIMIT 1`, uid, lo, hi, uid, show).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return core.WatchStatus{}, nil
	}
	if err != nil {
		return core.WatchStatus{}, err
	}
	return s.Get(ctx, uid, key)
}
