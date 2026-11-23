package vault

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/store"
)

const (
	vaultQuotaWarnPercent    = 80.0
	vaultQuotaRecoverPercent = 70.0
)

type trackKey struct {
	key     Key
	season  int
	episode int
}

const maxVaultTrackCache = 4096

type library struct {
	Options
	capBytes     atomic.Int64 // 0 = no limit
	quotaWarned  atomic.Bool
	mu           sync.Mutex // writer name and transaction ordering on standalone SQLite
	writer       string
	cacheMu      sync.Mutex
	cache        []cached
	trackMu      sync.RWMutex
	tracks       map[trackKey]core.MediaInfo
	tracksLoaded bool
}

func (v *library) publishEvent(eventType string, payload any, opts ...events.EnvelopeOption) {
	if v == nil || v.Events == nil {
		return
	}
	_ = v.Events.PublishTyped(eventType, payload, opts...)
}

func (v *library) checkQuotaWarning(usedBytes, budgetBytes int64, recordCount int) {
	if v == nil || v.Events == nil || budgetBytes <= 0 {
		return
	}
	pct := float64(usedBytes) * 100 / float64(budgetBytes)
	if pct >= vaultQuotaWarnPercent {
		if v.quotaWarned.CompareAndSwap(false, true) {
			v.publishEvent(events.EventVaultQuotaWarning, events.VaultQuotaWarningPayload{
				UsedBytes:   usedBytes,
				BudgetBytes: budgetBytes,
				PercentUsed: pct,
				RecordCount: recordCount,
			})
		}
	} else if pct < vaultQuotaRecoverPercent {
		v.quotaWarned.Store(false)
	}
}

type cached struct {
	key Key
	loc location
	r   *Record
}
type location struct {
	pack   string
	off    int64
	length int
	crc    int64
}

var (
	_ ReplicaStore  = (*library)(nil)
	_ Migrator      = (*library)(nil)
	_ Chooser       = (*library)(nil)
	_ Membership    = (*library)(nil)
	_ SearchCounter = (*library)(nil)
	_ Lister        = (*library)(nil)
	_ Capper        = (*library)(nil)
	_ TrackStore    = (*library)(nil)
	_ AliasResolver = (*library)(nil)
)

func Open(ctx context.Context, o Options) (Store, error) {
	if o.DB == nil || o.Dir == "" {
		return nil, errors.New("vault: directory and database required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Join(o.Dir, "packs"), 0700); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lib := &library{Options: o}
	lib.capBytes.Store(max(o.CapBytes, 0))
	lib.preloadTracks(ctx)
	return lib, nil
}

// SetCapBytes implements Capper. A lower cap takes effect at the next
// store; nothing is evicted eagerly.
func (v *library) SetCapBytes(n int64) { v.capBytes.Store(max(n, 0)) }
func (v *library) node() string {
	if v.Shared {
		return ""
	}
	return v.NodeID
}
func (v *library) write(ctx context.Context, fn func(*sql.Tx) error) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	tx, e := v.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = store.LockKey(ctx, store.DialectOf(v.DB), tx, "vault:packs"); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}

var crcTable = crc32.MakeTable(crc32.Castagnoli)

const frameHeader = 12

func frame(b []byte) []byte {
	p := append([]byte("EV01"), make([]byte, 8)...)
	binary.LittleEndian.PutUint32(p[4:], uint32(len(b)))
	binary.LittleEndian.PutUint32(p[8:], crc32.Checksum(b, crcTable))
	return append(p, b...)
}
func check(b []byte) bool {
	return len(b) >= frameHeader && string(b[:4]) == "EV01" && int(binary.LittleEndian.Uint32(b[4:])) == len(b)-frameHeader && binary.LittleEndian.Uint32(b[8:]) == crc32.Checksum(b[frameHeader:], crcTable)
}
func (v *library) appendFrame(b []byte) (location, error) {
	var l location
	if len(b) > 64<<20 {
		return l, errors.New("vault: frame exceeds pack size")
	}
	dir := filepath.Join(v.Dir, "packs")
	var f *os.File
	if v.writer != "" {
		var e error
		f, e = os.OpenFile(filepath.Join(dir, v.writer), os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return l, e
		}
		if f != nil {
			s, e := f.Stat()
			if e != nil {
				f.Close()
				return l, e
			}
			if s.Size()+int64(len(b)) > 64<<20 {
				e = f.Sync()
				f.Close()
				f = nil
				if e != nil {
					return l, e
				}
			} else {
				l.off = s.Size()
			}
		}
	}
	if f == nil {
		var id [16]byte
		if _, e := rand.Read(id[:]); e != nil {
			return l, e
		}
		month := v.Now().UTC().Format("200601")
		if e := os.MkdirAll(filepath.Join(dir, month), 0700); e != nil {
			return l, e
		}
		v.writer = filepath.Join(month, hex.EncodeToString(id[:])+".pack")
		var e error
		f, e = os.OpenFile(filepath.Join(dir, v.writer), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return l, e
		}
	}
	l.pack = filepath.ToSlash(v.writer)
	l.length = len(b)
	l.crc = int64(binary.LittleEndian.Uint32(b[8:]))
	_, e := f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		v.writer = ""
		return l, e
	}
	return l, ce
}
func (v *library) read(l location) ([]byte, error) {
	if !filepath.IsLocal(l.pack) || strings.Contains(l.pack, "..") || l.length < 12 || l.length > 64<<20 || l.off < 0 {
		return nil, ErrPayloadMissing
	}
	f, e := os.Open(filepath.Join(v.Dir, "packs", l.pack))
	if e != nil {
		return nil, ErrPayloadMissing
	}
	defer f.Close()
	b := make([]byte, l.length)
	if _, e = f.ReadAt(b, l.off); e != nil || !check(b) || int64(binary.LittleEndian.Uint32(b[8:])) != l.crc {
		return nil, ErrPayloadMissing
	}
	return b, nil
}

type query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (v *library) loc(ctx context.Context, q query, k Key) (location, error) {
	var l location
	var node string
	e := q.QueryRowContext(ctx, `SELECT pack,off,len,crc,holders FROM vault_releases WHERE key=? AND dead=0`, k).Scan(&l.pack, &l.off, &l.length, &l.crc, &node)
	if errors.Is(e, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	if e == nil && node != v.node() {
		e = q.QueryRowContext(ctx, `SELECT pack,off,len,crc FROM vault_copies WHERE key=? AND node=? AND crc=?`, k, v.node(), l.crc).Scan(&l.pack, &l.off, &l.length, &l.crc)
		if errors.Is(e, sql.ErrNoRows) {
			e = ErrPayloadMissing
		}
	}
	return l, e
}
func (v *library) ReadFrame(ctx context.Context, k Key) ([]byte, error) {
	l, e := v.loc(ctx, v.DB, k)
	if e != nil {
		return nil, e
	}
	return v.read(l)
}
func (v *library) DropCache() {
	v.cacheMu.Lock()
	v.cache = nil
	v.cacheMu.Unlock()
	v.trackMu.Lock()
	v.tracks = nil
	v.tracksLoaded = false
	v.trackMu.Unlock()
}
func (v *library) InstallFrame(ctx context.Context, k Key, b []byte) error {
	if len(b) > 64<<20 || !check(b) {
		return invalid
	}
	r, e := Decode(b[12:])
	if e != nil || r.Key != k || recordKey(r) != k {
		return invalid
	}
	return v.write(ctx, func(tx *sql.Tx) error {
		var crc int64
		var owner string
		e := tx.QueryRowContext(ctx, `SELECT crc,holders FROM vault_releases WHERE key=? AND dead=0`, k).Scan(&crc, &owner)
		if e != nil {
			return e
		}
		if crc != int64(binary.LittleEndian.Uint32(b[8:])) {
			return invalid
		}
		l, e := v.appendFrame(b)
		if e != nil {
			return e
		}
		if owner == v.node() {
			_, e = tx.ExecContext(ctx, `UPDATE vault_releases SET pack=?,off=?,len=? WHERE key=?`, l.pack, l.off, l.length, k)
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO vault_copies(key,node,pack,off,len,crc) VALUES(?,?,?,?,?,?) ON CONFLICT(key,node) DO UPDATE SET pack=excluded.pack,off=excluded.off,len=excluded.len,crc=excluded.crc`, k, v.node(), l.pack, l.off, l.length, l.crc)
		return e
	})
}
func (v *library) Holders(ctx context.Context, k Key) ([]string, error) {
	rows, e := v.DB.QueryContext(ctx, `SELECT holders FROM vault_releases WHERE key=? AND dead=0 UNION SELECT c.node FROM vault_copies c JOIN vault_releases r ON r.key=c.key AND r.crc=c.crc WHERE r.key=? AND r.dead=0`, k, k)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var a []string
	for rows.Next() {
		var s string
		if e = rows.Scan(&s); e != nil {
			return nil, e
		}
		if s != "" {
			a = append(a, s)
		}
	}
	return a, rows.Err()
}
