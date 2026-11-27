package state

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// What a viewer last played *from*: an album, an artist, a playlist, their
// liked songs. Recently played shows these, not every track. A viewer keeps
// their latest MaxPlayContexts; nothing here grows with the library.
func init() {
	store.Register("state", 14, `
CREATE TABLE state_play_contexts (user_id INTEGER NOT NULL, kind TEXT NOT NULL, ref TEXT NOT NULL, played_at INTEGER NOT NULL, PRIMARY KEY(user_id,kind,ref));
CREATE INDEX state_play_contexts_recent ON state_play_contexts(user_id, played_at DESC);
`)
	store.RegisterViewerTable("state", "state_play_contexts", "user_id", store.ViewerMergeMove, "kind", "ref")
}

// MaxPlayContexts is how many play contexts a viewer keeps.
const MaxPlayContexts = 50

// PlayContext is one thing a viewer played from and when they last did.
type PlayContext struct {
	Kind     string // "title", "playlist" or "favorites"
	Ref      string // a title or playlist key, or a liked-list role
	PlayedAt time.Time
}

var errBadPlayContext = errors.New("state: bad play context")

// RecordPlayContext notes that the viewer started playing from kind/ref now,
// keeping only their newest MaxPlayContexts.
func (s *State) RecordPlayContext(ctx context.Context, userID int64, kind, ref string) error {
	if userID <= 0 || kind == "" || ref == "" || len(ref) > 128 || len(kind) > 16 {
		return errBadPlayContext
	}
	now := s.now().UTC().UnixNano()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO state_play_contexts(user_id,kind,ref,played_at) VALUES(?,?,?,?) ON CONFLICT(user_id,kind,ref) DO UPDATE SET played_at=excluded.played_at`, userID, kind, ref, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM state_play_contexts WHERE user_id=? AND played_at < (SELECT played_at FROM state_play_contexts WHERE user_id=? ORDER BY played_at DESC LIMIT 1 OFFSET ?)`, userID, userID, MaxPlayContexts-1); err != nil {
		return err
	}
	return tx.Commit()
}

// RecentPlayContexts lists the viewer's play contexts, most recent first.
func (s *State) RecentPlayContexts(ctx context.Context, userID int64, limit int) ([]PlayContext, error) {
	if limit <= 0 || limit > MaxPlayContexts {
		limit = MaxPlayContexts
	}
	rows, err := s.db.QueryContext(ctx, `SELECT kind,ref,played_at FROM state_play_contexts WHERE user_id=? ORDER BY played_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayContext
	for rows.Next() {
		var c PlayContext
		var at int64
		if err := rows.Scan(&c.Kind, &c.Ref, &at); err != nil {
			return nil, err
		}
		c.PlayedAt = time.Unix(0, at).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}
