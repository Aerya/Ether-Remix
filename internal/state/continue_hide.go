package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("state_continue_hidden", 1, `CREATE TABLE state_continue_hidden(user_id INTEGER NOT NULL,item_key INTEGER NOT NULL,PRIMARY KEY(user_id,item_key));`)
	// hidden_at is when the viewer removed the title (unix ns; 0 for rows
	// removed before it was recorded), so a newer playback elsewhere can be
	// told from a stale one.
	store.Register("state_continue_hidden", 2, `ALTER TABLE state_continue_hidden ADD COLUMN hidden_at INTEGER NOT NULL DEFAULT 0;`)
	store.RegisterViewerTable("state", "state_continue_hidden", "user_id", store.ViewerMergeMove, "item_key")
}
func continueIdentity(ctx context.Context, db ancestryReader, key core.RatingKey) (core.RatingKey, error) {
	if key.Kind() == core.KindEpisode || key.Kind() == core.KindSeason {
		show, _, err := childAncestry(ctx, db, key)
		if err != nil || show != 0 {
			return show, err
		}
	}
	return key, nil
}

// HideContinue removes a title from the viewer's Continue Watching and
// keeps its resume state. The removal of each in-progress movie or episode
// it covers is journaled for account sync in the same transaction.
func (s *State) HideContinue(ctx context.Context, uid int64, key core.RatingKey) error {
	return s.hideContinue(ctx, uid, key, false)
}

// HideContinueAccount is HideContinue for a removal read from the viewer's
// account: it is not journaled, so it is never sent back.
func (s *State) HideContinueAccount(ctx context.Context, uid int64, key core.RatingKey) error {
	return s.hideContinue(ctx, uid, key, true)
}

func (s *State) hideContinue(ctx context.Context, uid int64, key core.RatingKey, account bool) error {
	if err := s.lockWrites(ctx); err != nil {
		return err
	}
	defer s.unlockWrites()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cur, err := loadTx(ctx, tx, uid, key)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	cur.ws.UpdatedAt = now
	if err := saveTx(ctx, tx, uid, cur); err != nil {
		return err
	}
	identity, err := continueIdentity(ctx, tx, key)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO state_continue_hidden(user_id,item_key,hidden_at) VALUES(?,?,?)
		ON CONFLICT(user_id,item_key) DO UPDATE SET hidden_at=excluded.hidden_at`, uid, identity, now.UnixNano()); err != nil {
		return err
	}
	if !account {
		evs, err := continueRemovals(ctx, tx, uid, identity, now)
		if err != nil {
			return err
		}
		if err := s.journalEvents(ctx, tx, evs, false); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.gen.Add(1)
	return nil
}

// continueRemovals lists the in-progress titles a removal covers: the movie
// itself, or every episode of the show with a resume point (one primary-key
// range of this viewer's rows).
func continueRemovals(ctx context.Context, tx *sql.Tx, uid int64, key core.RatingKey, at time.Time) ([]Event, error) {
	lo, hi := key, key+1
	switch key.Kind() {
	case core.KindMovie:
	case core.KindShow:
		lo, hi = 0, 0
		if !key.Allocated() {
			lo, hi = core.EpisodeKey(key.TMDB(), 0, 0), core.EpisodeKey(key.TMDB()+1, 0, 0)
		}
	default:
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT rating_key, view_offset_ns, duration_ns FROM state_watch
		WHERE user_id=? AND rating_key>=? AND rating_key<? AND view_offset_ns>0
 UNION ALL SELECT w.rating_key,w.view_offset_ns,w.duration_ns FROM catalog_allocated_child a JOIN state_watch w ON w.rating_key=a.key
 WHERE a.show_key=? AND a.kind=4 AND w.user_id=? AND w.view_offset_ns>0`, uid, lo, hi, key, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evs []Event
	for rows.Next() {
		var k, off, dur int64
		if err := rows.Scan(&k, &off, &dur); err != nil {
			return nil, err
		}
		evs = append(evs, Event{Kind: EventContinueRemoved, UserID: uid, Key: core.RatingKey(k),
			Resume: time.Duration(off), Duration: time.Duration(dur), At: at})
	}
	return evs, rows.Err()
}

func (s *State) RestoreContinue(ctx context.Context, uid int64, key core.RatingKey) error {
	identity, err := continueIdentity(ctx, s.db, key)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM state_continue_hidden WHERE user_id=? AND item_key=?`, uid, identity)
	return err
}

// ContinueHiddenAt reports whether the title is removed from the viewer's
// Continue Watching and when (zero when that was not recorded).
func (s *State) ContinueHiddenAt(ctx context.Context, uid int64, key core.RatingKey) (time.Time, bool, error) {
	identity, err := continueIdentity(ctx, s.db, key)
	if err != nil {
		return time.Time{}, false, err
	}
	var at int64
	err = s.db.QueryRowContext(ctx, `SELECT hidden_at FROM state_continue_hidden WHERE user_id=? AND item_key=?`, uid, identity).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return unixTime(at), true, nil
}

func (s *State) ContinueHidden(ctx context.Context, uid int64, keys []core.RatingKey) (map[core.RatingKey]bool, error) {
	out := map[core.RatingKey]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	ancestry, err := s.allocatedAncestry(ctx, keys)
	if err != nil {
		return nil, err
	}
	args := []any{uid}
	marks := []string{}
	for _, key := range keys {
		if key.Kind() == core.KindEpisode || key.Kind() == core.KindSeason {
			if show := ancestry[key]; show != 0 {
				key = show
			} else if !key.Allocated() {
				key = core.ShowKey(key.TMDB())
			}
		}
		args = append(args, key)
		marks = append(marks, "?")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT item_key FROM state_continue_hidden WHERE user_id=? AND item_key IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key core.RatingKey
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, rows.Err()
}
