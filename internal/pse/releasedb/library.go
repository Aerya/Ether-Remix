package releasedb

import (
	"context"
	"encoding/json"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_release_library", 1, `CREATE TABLE pse_release_library (
 title_key INTEGER NOT NULL, release_id TEXT NOT NULL, source_id TEXT NOT NULL, source_signature TEXT NOT NULL,
 payload TEXT NOT NULL, first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL,
 expires INTEGER NOT NULL, failed INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(title_key,source_id,release_id)); CREATE INDEX pse_release_library_expiry ON pse_release_library(expires);
 CREATE INDEX pse_release_library_retention ON pse_release_library(last_seen);`)
}

const StableReferenceExpiry int64 = 253402300799 // stable source descriptors do not expire by age

// LibraryEnabled remains for callers of the retained-reference API. References
// are now always collected and reused; PSE_INDEXER only controls background searches.
// The legacy PSE_RELEASE_LIBRARY setting is intentionally ignored.
func LibraryEnabled() bool { return true }

// Library keeps encrypted source descriptors, never resolved playback payloads.
// The complete Release preserves provider-specific routing (including verified files).
type Library struct {
	Accept    func(context.Context, core.RatingKey, core.Release) (bool, error)
	Store     *Store
	Seal      func(string) (string, error)
	Open      func(string) (string, error)
	Signature string
	Now       func() time.Time
}

func (l Library) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}
func (l Library) Save(ctx context.Context, key core.RatingKey, rs []core.Release) error {
	if !LibraryEnabled() || key == 0 {
		return nil
	}
	// Evaluate before opening a write transaction: selection can read the catalogue.
	selected := make([]core.Release, 0, len(rs))
	for _, r := range rs {
		if l.Accept != nil {
			ok, err := l.Accept(ctx, key, r)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
		}
		selected = append(selected, r)
	}
	rs = selected
	now := l.now()
	tx, err := l.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Bound retention even when no maintenance worker is active.
	if _, err = tx.ExecContext(ctx, `DELETE FROM pse_release_library WHERE last_seen<? AND expires<=?`, now.Add(-60*24*time.Hour).Unix(), now.Unix()); err != nil {
		return err
	}
	for _, r := range rs {
		if releaseparse.ReferenceReason(r) != "" {
			continue
		}
		raw, e := json.Marshal(r)
		if e != nil {
			return e
		}
		sealed, e := l.Seal(string(raw))
		if e != nil {
			return e
		}
		ttl := 24 * time.Hour
		if r.Protocol == core.ProtocolHTTP && r.StreamDebrid == "" {
			ttl = time.Hour
		}
		expires := now.Add(ttl).Unix()
		if releaseparse.StableReference(r) {
			expires = StableReferenceExpiry
		}
		source := r.IndexerID
		if source == "" {
			source = r.Indexer
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO pse_release_library(title_key,release_id,source_id,source_signature,payload,first_seen,last_seen,expires,browse) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(title_key,source_id,release_id) DO UPDATE SET source_signature=excluded.source_signature,payload=excluded.payload,last_seen=excluded.last_seen,expires=excluded.expires,failed=0,browse=excluded.browse`, int64(key), r.ID, source, l.Signature, sealed, now.Unix(), now.Unix(), expires, browseRelease(r))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

type LibraryEntry struct {
	Release                      core.Release
	FirstSeen, LastSeen, Expires int64
	Failed                       bool
	Current                      bool
}

func (l Library) Entries(ctx context.Context, key core.RatingKey) ([]LibraryEntry, error) {
	rows, err := l.Store.db.QueryContext(ctx, `SELECT payload,first_seen,last_seen,expires,failed,source_signature FROM pse_release_library WHERE title_key=? ORDER BY last_seen DESC`, int64(key))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibraryEntry{}
	for rows.Next() {
		var e LibraryEntry
		var payload, sig string
		if err = rows.Scan(&payload, &e.FirstSeen, &e.LastSeen, &e.Expires, &e.Failed, &sig); err != nil {
			return nil, err
		}
		raw, x := l.Open(payload)
		if x != nil {
			continue
		}
		if json.Unmarshal([]byte(raw), &e.Release) != nil {
			continue
		}
		e.Current = sig == l.Signature
		out = append(out, e)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if l.Accept != nil {
		filtered := out[:0]
		for _, e := range out {
			ok, err := l.Accept(ctx, key, e.Release)
			if err != nil {
				return nil, err
			}
			if ok {
				filtered = append(filtered, e)
			}
		}
		out = filtered
	}
	return out, nil
}
func (l Library) Candidates(ctx context.Context, key core.RatingKey) []core.Release {
	if !LibraryEnabled() {
		return nil
	}
	entries, err := l.Entries(ctx, key)
	if err != nil {
		return nil
	}
	var out []core.Release
	for _, e := range entries {
		if e.Current && !e.Failed && e.Expires > l.now().Unix() {
			out = append(out, e.Release)
		}
	}
	return out
}
func (l Library) Invalidate(ctx context.Context, id string) {
	_, _ = l.Store.db.ExecContext(ctx, `UPDATE pse_release_library SET failed=1 WHERE release_id=?`, id)
}
