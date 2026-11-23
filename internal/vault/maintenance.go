package vault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/nzb"
)

// MigrationError reports a migration pass that left NZBs behind.
type MigrationError struct {
	Remaining int    // .nzb files still in the old directory
	Failed    int    // files that failed this pass (kept, retried next pass)
	File      string // the first failing file
	Err       error  // its error
}

func (e *MigrationError) Error() string {
	return fmt.Sprintf("vault: NZB migration left %d file(s); %d failed this pass, first %s: %v", e.Remaining, e.Failed, e.File, e.Err)
}

func (e *MigrationError) Unwrap() error { return e.Err }

// errUnreadableNZB marks a legacy file that is not a usable NZB.
var errUnreadableNZB = errors.New("vault: not a usable NZB")

// UnreadableDir is where MigrateDir sets aside legacy files that are not
// usable NZBs. They are never deleted.
const UnreadableDir = ".unreadable"

// MigrateDir moves a directory of NZBs into the vault one file at a time.
// Each file is imported, read back and compared, and only then removed,
// under a short lock that re-checks its row. A file that cannot be
// imported or removed (a Windows sharing violation, say) stays where it
// is and is retried on the next pass; a file that is not an NZB at all is
// set aside in UnreadableDir. Already-imported files are verified without
// re-encoding, so passes resume cheaply.
func (v *library) MigrateDir(ctx context.Context, dir string) error {
	entries, e := os.ReadDir(dir)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	log := v.Logger
	if log == nil {
		log = slog.Default()
	}
	var moved, setAside, remaining int
	var fail *MigrationError
	total := 0
	for _, f := range entries {
		if f.Type().IsRegular() && strings.EqualFold(filepath.Ext(f.Name()), ".nzb") {
			total++
		}
	}
	seen := 0
	for _, f := range entries {
		if !f.Type().IsRegular() || !strings.EqualFold(filepath.Ext(f.Name()), ".nzb") {
			continue
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		seen++
		path := filepath.Join(dir, f.Name())
		id := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
		err := v.migrateOne(ctx, path, id)
		switch {
		case err == nil:
			moved++
		case errors.Is(err, errUnreadableNZB):
			q := filepath.Join(dir, UnreadableDir)
			if e := os.MkdirAll(q, 0o700); e == nil && os.Rename(path, filepath.Join(q, f.Name())) == nil {
				setAside++
				log.Warn("vault migration set aside an unreadable NZB", "file", f.Name(), "err", err)
				continue
			}
			fallthrough
		default:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			remaining++
			if fail == nil {
				fail = &MigrationError{File: f.Name(), Err: err}
			}
			fail.Failed++
			log.Debug("vault migration kept a file for the next pass", "file", f.Name(), "err", err)
		}
		if seen%100 == 0 {
			log.Info("vault migration progress", "done", seen, "total", total, "moved", moved, "setAside", setAside, "failed", remaining)
		}
	}
	log.Info("vault migration pass finished", "total", total, "moved", moved, "setAside", setAside, "failed", remaining)
	if fail != nil {
		fail.Remaining = remaining
		return fail
	}
	left, e := os.ReadDir(dir)
	if e == nil && len(left) == 0 {
		_ = os.Remove(dir)
	}
	return nil
}

// migrateOne imports, verifies and removes one legacy NZB.
func (v *library) migrateOne(ctx context.Context, path, id string) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	doc, e := nzb.Parse(b)
	if e != nil {
		return fmt.Errorf("%w: %v", errUnreadableNZB, e)
	}
	want := doc.ContentID()
	if want == "" {
		return fmt.Errorf("%w: no segments", errUnreadableNZB)
	}
	k, ok, e := v.ByAlias(ctx, LegacyAliasIndexer, id)
	if e != nil {
		return e
	}
	if !ok || !v.sameNZB(ctx, k, want) {
		if k, e = v.Put(ctx, PutRequest{NZB: b, Source: "legacy:nzb", Release: core.Release{IndexerID: LegacyAliasIndexer, ID: id}}); e != nil {
			if errors.Is(e, invalid) {
				return fmt.Errorf("%w: %v", errUnreadableNZB, e)
			}
			return e
		}
		if !v.sameNZB(ctx, k, want) {
			return errors.New("vault: migration verification failed")
		}
	}
	// Remove under a short lock that re-checks the row, so a purge or cap
	// eviction cannot drop the record between verification and removal.
	return v.write(ctx, func(tx *sql.Tx) error {
		var alias Key
		if e := tx.QueryRowContext(ctx, "SELECT key FROM vault_aliases WHERE indexer_id=? AND release_id=?", LegacyAliasIndexer, id).Scan(&alias); e != nil {
			return e
		}
		if alias != k {
			return invalid
		}
		if _, e := v.loc(ctx, tx, k); e != nil {
			return e
		}
		return os.Remove(path)
	})
}

// sameNZB reports whether the stored record of k decodes to the same
// post (content identity: the same articles). Another indexer's NZB of
// the same post is deduplicated into one record whose subjects and head
// may differ from this file's, which is correct.
func (v *library) sameNZB(ctx context.Context, k Key, want string) bool {
	r, e := v.Get(ctx, k)
	if e != nil || r.NZB == nil {
		return false
	}
	return r.NZB.ContentID() == want
}

type liveFrame struct {
	location
	k    Key
	copy bool
}

// Compact serializes relocation with every node's writer. Old packs are
// unlinked only after the new pointers commit, with a second reference check.
func (v *library) Compact(ctx context.Context) error {
	started := v.Now()
	base := filepath.Join(v.Dir, "packs")
	paths, e := filepath.Glob(filepath.Join(base, "*", "*.pack"))
	if e != nil {
		return e
	}
	packsCompacted := 0
	var bytesReclaimed int64
	for _, path := range paths {
		rel, e := filepath.Rel(base, path)
		if e != nil {
			return e
		}
		pack := filepath.ToSlash(rel)
		rewrite := false
		var reclaimed int64
		e = v.write(ctx, func(tx *sql.Tx) error {
			st, e := os.Stat(path)
			if errors.Is(e, os.ErrNotExist) {
				return nil
			}
			if e != nil {
				return e
			}
			rows, e := tx.QueryContext(ctx, `SELECT key,off,len,crc,0 FROM vault_releases WHERE pack=? AND dead=0 AND holders=? UNION ALL SELECT c.key,c.off,c.len,c.crc,1 FROM vault_copies c JOIN vault_releases r ON r.key=c.key AND r.crc=c.crc AND r.dead=0 WHERE c.pack=? AND c.node=?`, pack, v.node(), pack, v.node())
			if e != nil {
				return e
			}
			var frames []liveFrame
			var live int64
			for rows.Next() {
				x := liveFrame{}
				var replica int
				if e = rows.Scan(&x.k, &x.off, &x.length, &x.crc, &replica); e != nil {
					rows.Close()
					return e
				}
				x.pack = pack
				x.copy = replica != 0
				frames = append(frames, x)
				live += int64(x.length)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if live*2 >= st.Size() {
				return nil
			}
			rewrite = true
			if st.Size() > live {
				reclaimed = st.Size() - live
			}
			if filepath.ToSlash(v.writer) == pack {
				v.writer = ""
			}
			for _, x := range frames {
				b, e := v.read(x.location)
				if e != nil {
					return e
				}
				l, e := v.appendFrame(b)
				if e != nil {
					return e
				}
				if x.copy {
					_, e = tx.ExecContext(ctx, "UPDATE vault_copies SET pack=?,off=? WHERE key=? AND node=?", l.pack, l.off, x.k, v.node())
				} else {
					_, e = tx.ExecContext(ctx, "UPDATE vault_releases SET pack=?,off=? WHERE key=?", l.pack, l.off, x.k)
				}
				if e != nil {
					return e
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
		if rewrite {
			removed := false
			e = v.write(ctx, func(tx *sql.Tx) error {
				var n int
				e := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM vault_releases WHERE pack=? AND dead=0 AND holders=?) + (SELECT count(*) FROM vault_copies c JOIN vault_releases r ON r.key=c.key AND r.crc=c.crc AND r.dead=0 WHERE c.pack=? AND c.node=?)`, pack, v.node(), pack, v.node()).Scan(&n)
				if e != nil || n != 0 {
					return e
				}
				e = os.Remove(path)
				if errors.Is(e, os.ErrNotExist) {
					return nil
				}
				if e == nil {
					removed = true
				}
				return e
			})
			if e != nil {
				return e
			}
			if removed {
				packsCompacted++
				bytesReclaimed += reclaimed
			}
		}
	}
	if packsCompacted > 0 && v.Events != nil {
		v.publishEvent(events.EventVaultPackCompacted, events.VaultPackCompactedPayload{
			PacksCompacted: packsCompacted,
			BytesReclaimed: bytesReclaimed,
			DurationMS:     v.Now().Sub(started).Milliseconds(),
		})
	}
	return nil
}
