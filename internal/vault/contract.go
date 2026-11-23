// Package vault keeps every release this server ever resolved, so an
// indexer is asked for a given release at most once in the server's life.
//
// Payload: zstd-compressed binary records in append-only pack files under
// <cacheDir>/vault (on the NAS when the cache is shared). Index: small rows
// in the server database (store.Register("vault", n, ...)); no NZB XML or
// segment lists in the database. Design:
// thread-storage/thr_mja5k9vcku/nzb-vault-design.md (sections 1-10).
//
// This file is the contract every 0.3.16 vault worker builds against.
// Change it only additively and tell the coordinator thread (thr_mja5k9vcku).
package vault

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/nzb"
)

// IndexerID marks releases served from the vault (core.Release.IndexerID).
const IndexerID = "vault"

// Key is a release's cross-indexer content identity: W116's
// nzb.ContentID ("nzb:" + hex sha256 over the NZB's sorted, de-duplicated
// segment message-ids), or TorrentKey for torrent records.
type Key string

// ContentKey is the release's content identity (nzb.ContentID).
func ContentKey(doc *nzb.NZB) Key { return Key(doc.ContentID()) }

// TorrentKey is the key of a torrent record.
func TorrentKey(infoHash string) Key { return Key("bt:" + infoHash) }

// Kind of record.
type Kind string

const (
	KindUsenet  Kind = "usenet"
	KindTorrent Kind = "torrent"
)

var (
	// ErrNotFound: no index row.
	ErrNotFound = errors.New("vault: not found")
	// ErrPayloadMissing: the index row exists but this node cannot read the
	// payload (pack lost, restored without packs, or held only by an
	// unreachable peer). Callers re-grab as if the vault had nothing.
	ErrPayloadMissing = errors.New("vault: payload missing")
)

// Health of a stored release's articles.
type Health int

const (
	HealthUnknown Health = iota
	HealthOK
	HealthDead
)

// Extent is a contiguous byte range of one posted file (index into
// Record.NZB.Files) that belongs to an archive member.
type Extent struct {
	File   int   `json:"file"`
	Offset int64 `json:"offset"`
	Size   int64 `json:"size"`
}

// Member is one playable file inside the release.
type Member struct {
	// PrefixHash identifies the first 256 member bytes (or the whole member if shorter).
	PrefixHash string   `json:"prefixHash,omitempty"`
	Name       string   `json:"name"`
	Size       int64    `json:"size"`
	Stored     bool     `json:"stored"`
	Extents    []Extent `json:"extents"`
	Season     int      `json:"season,omitempty"` // 0 when not an episode
	Episode    int      `json:"episode,omitempty"`
}

// Layout is learned by the stream engine on the first successful open so
// later opens skip archive discovery entirely.
type Layout struct {
	Container string         `json:"container"` // raw | rar | 7z | split
	Members   []Member       `json:"members"`
	Volumes   []VolumeLayout `json:"volumes,omitempty"`
	// FirstArticles are decoded first articles of archive volumes, keyed by
	// file index, capped by the vault (archive headers only).
	FirstArticles map[int][]byte `json:"-"`
}

// VolumeLayout is one archive volume as the engine verified it, so a
// reopen keeps lazy verification and never trusts a truncated cached head.
type VolumeLayout struct {
	File     int   `json:"file"`
	Size     int64 `json:"size"`
	Verified bool  `json:"verified"`
	HeadSize int   `json:"headSize"`
}

// TorrentFile is one file of a torrent record.
type TorrentFile struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Season  int    `json:"season,omitempty"`
	Episode int    `json:"episode,omitempty"`
}

// Torrent is the payload of a torrent (debrid) record. Never store add-on
// stream URLs: they expire and can embed the viewer's debrid key.
type Torrent struct {
	InfoHash string        `json:"infoHash"`
	Magnet   string        `json:"magnet,omitempty"` // trackers only, no secrets
	Files    []TorrentFile `json:"files,omitempty"`
	Info     []byte        `json:"-"` // raw info dictionary when known
}

// Record is a decoded vault payload.
type Record struct {
	Key     Key
	Kind    Kind
	Name    string
	NZB     *nzb.NZB // KindUsenet
	XML     []byte   // canonical NZB XML regenerated on demand (Usenet)
	Torrent *Torrent // KindTorrent
	Layout  *Layout
	Par2    []byte // retained PAR2 summary (opaque to the vault)
	// Password is the archive password from the NZB head, unsealed for the
	// caller; stored sealed. Never logged, never returned by any API.
	Password string
}

// Entry is one index row (no payload).
type Entry struct {
	CacheID   string // original playable release ID (the parent release ID for a pack)
	Key       Key
	Kind      Kind
	Name      string
	TitleKey  string // normalized title tokens
	TMDB      int
	Year      int
	Season    int
	Episode   int
	Size      int64
	Segments  int
	Source    string // indexer id, "import:sabnzbd", "rosetta", ...
	Private   bool   // from a private indexer: DB-equivalent handling
	Added     time.Time
	LastUsed  time.Time
	Uses      int
	Health    Health
	HealthAt  time.Time
	ProfileFP string // profile fingerprint when it was chosen
	Info      core.ReleaseInfo
}

// PutRequest stores a release.
type PutRequest struct {
	Release core.Release // the resolved release (indexer id, guid, name, title ids)
	NZB     []byte       // Usenet
	Torrent *Torrent     // torrent (debrid)
	Source  string
	Private bool
	// ProfileFP fingerprints the quality profile plus the viewer's audio
	// and language rules at choice time (library-first rule).
	ProfileFP string
}

// Stats for the console.
type Stats struct {
	Releases        int
	Bytes           int64
	CapBytes        int64
	GrabsAvoided    int64
	SearchesAvoided int64
}

// Store is the vault.
type Store interface {
	Put(ctx context.Context, r PutRequest) (Key, error)
	Get(ctx context.Context, key Key) (*Record, error)
	ByAlias(ctx context.Context, indexerID, releaseID string) (Key, bool, error)
	Search(ctx context.Context, req core.SearchRequest) ([]Entry, error)
	Entry(ctx context.Context, key Key) (Entry, error)
	SetLayout(ctx context.Context, key Key, l Layout) error
	MarkHealth(ctx context.Context, key Key, h Health) error
	Touch(ctx context.Context, key Key, avoided bool) error // a use; avoided = a grab/search was saved
	Purge(ctx context.Context, key Key) error
	Stats(ctx context.Context) (Stats, error)
}

// ReleaseFor turns an index row into a resolver candidate.
func ReleaseFor(e Entry) core.Release {
	r := core.Release{
		ID: "vault-" + string(e.Key), GUID: "vault:" + string(e.Key), Indexer: "Library", IndexerID: IndexerID,
		Name: e.Name, Size: e.Size, PubDate: e.Added, Info: e.Info,
	}
	if e.Kind == KindUsenet && e.CacheID != "" {
		r.ID = e.CacheID
	}
	r.Protocol = core.ProtocolUsenet
	if e.Kind == KindUsenet && strings.HasPrefix(string(e.Key), nzb.ContentIDPrefix) {
		r.ContentID = string(e.Key)
	}
	if e.Kind == KindTorrent {
		r.Protocol = core.ProtocolTorrent
		if len(e.Key) > 3 {
			r.InfoHash = string(e.Key[3:])
		}
	}
	return r
}

// KeyOf returns the vault key of a vault release, or "".
// Pack targets rewrite release IDs (core.TargetReleaseID); the untargeted
// pack ID is used when set.
func KeyOf(r core.Release) Key {
	if r.IndexerID != IndexerID {
		return ""
	}
	if k, ok := strings.CutPrefix(r.GUID, "vault:"); ok && k != "" {
		return Key(k)
	}
	id := r.ID
	if r.Target != nil && r.Target.PackID != "" {
		id = r.Target.PackID
	}
	if len(id) <= len("vault-") || id[:len("vault-")] != "vault-" {
		return ""
	}
	return Key(id[len("vault-"):])
}

// Sealer seals secrets at rest (the config sealer).
type Sealer interface {
	Seal(name string, plain []byte) (string, error)
	Unseal(name, sealed string) ([]byte, error)
}

// Verifier proves a release's articles are still retrievable. Bound to
// W119's (*usenet.Fetcher).VerifyArticles, which distrusts lying STAT.
type Verifier interface {
	VerifyArticles(ctx context.Context, ids []string, groups []string) (bool, error)
}

// Options open a vault.
type Options struct {
	Dir      string  // <cacheDir>/vault, or the NAS cache dir's vault/ when shared
	DB       *sql.DB // server database (index only)
	Sealer   Sealer
	NodeID   string // this node (holders); "" on a standalone server
	Shared   bool   // Dir is on the shared NAS (one copy for the cluster)
	CapBytes int64  // payload cap; 0 = no limit (config Cache.LibraryBudgetBytes)
	Logger   *slog.Logger
	Now      func() time.Time
	Events   *events.Bus
}

// Capper changes the payload cap at run time (0 = no limit).
type Capper interface {
	SetCapBytes(n int64)
}

// ErrNotImplemented is returned by placeholders during the build.
var ErrNotImplemented = errors.New("vault: not implemented yet")

// Migrator imports a directory of NZBs (Ether's pre-vault NZB cache or an
// export). Crash-safe and idempotent: import, verify count and bytes, then
// delete; never delete after a partial import. Files of the old cache are
// named <hex>.nzb, where hex is newznab's old disk key; each import records
// the alias (LegacyAliasIndexer, hex) so a release cached before the vault
// is found without a grab.
type Migrator interface {
	MigrateDir(ctx context.Context, dir string) error
}

// LegacyAliasIndexer is the alias namespace of migrated pre-vault NZBs.
const LegacyAliasIndexer = "legacy:nzb"

// ReplicaStore is what the cluster layer needs from a node-local vault.
type ReplicaStore interface {
	Store
	// ReadFrame returns the record's stored compressed frame.
	ReadFrame(ctx context.Context, key Key) ([]byte, error)
	// InstallFrame verifies the frame's CRC and content key, appends it to
	// this node's pack and records this node as a holder, atomically.
	InstallFrame(ctx context.Context, key Key, frame []byte) error
	Holders(ctx context.Context, key Key) ([]string, error)
	DropCache()
	Compact(ctx context.Context) error
}

// Chooser records that a stored release won resolution for a profile
// (winner-only fingerprint update; called after newznab's Put and on alias
// hits).
type Chooser interface {
	MarkChosen(ctx context.Context, key Key, profileFP string) error
}

// Membership answers, in one batched query, which releases the vault holds,
// keyed by core.Release.ID. A vault release matches by key, others by the
// (IndexerID, ID) alias, and rows without an indexer id by the legacy alias.
type Membership interface {
	Kept(ctx context.Context, releases []core.Release) (map[string]bool, error)
}

// SearchCounter counts indexer searches the library made unnecessary (the
// resolver's fast path), separately from grabs avoided (Touch).
type SearchCounter interface {
	SearchAvoided(ctx context.Context, key Key) error
}

// TrackStore persists and reads probed container tracks for vaulted releases.
type TrackStore interface {
	Tracks(ctx context.Context, key Key, season, episode int) (core.MediaInfo, bool, error)
	SetTracks(ctx context.Context, key Key, season, episode int, info core.MediaInfo) error
}

// AliasResolver maps candidate releases to their vault keys in one batched
// query without grabbing NZBs or touching pack files.
type AliasResolver interface {
	KeysFor(ctx context.Context, releases []core.Release) (map[string]Key, error)
}
