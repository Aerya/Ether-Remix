package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Playlists importées des comptes Deezer et Last.fm de chaque spectateur.
// Une playlist ne garde que des références au catalogue Deezer (instantané
// des titres) : rien n'est téléchargé, la lecture passe par les mêmes
// résolveurs que le reste de la musique.
func init() {
	store.Register("pse_music_playlists", 1, `
CREATE TABLE pse_music_accounts(
  viewer_id INTEGER NOT NULL,
  service TEXT NOT NULL,
  account TEXT NOT NULL,
  synced_at INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(viewer_id, service)
);
CREATE TABLE pse_music_playlists(
  id INTEGER PRIMARY KEY,
  viewer_id INTEGER NOT NULL,
  source TEXT NOT NULL,
  ref TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  position INTEGER NOT NULL DEFAULT 0,
  tracks TEXT NOT NULL DEFAULT '[]',
  synced_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE(viewer_id, source, ref)
);
CREATE INDEX pse_music_playlists_viewer ON pse_music_playlists(viewer_id, position);
CREATE TABLE pse_music_matches(
  key TEXT PRIMARY KEY,
  track TEXT NOT NULL DEFAULT '',
  checked_at INTEGER NOT NULL DEFAULT 0
);
`)
	store.RegisterViewerTable("pse_music_playlists", "pse_music_accounts", "viewer_id", store.ViewerMergeMove, "service")
	store.RegisterViewerTable("pse_music_playlists", "pse_music_playlists", "viewer_id", store.ViewerMergeMove, "source", "ref")
}

// Services de playlists.
const (
	ServiceDeezer = "deezer"
	ServiceLastFM = "lastfm"
)

// maxPlaylistID garde les identifiants sous la base des ratingKey Plex.
const maxPlaylistID = 1<<49 - 1

// Playlist est une playlist importée.
type Playlist struct {
	ID       int64
	Viewer   int64
	Source   string
	Ref      string
	Name     string
	Tracks   []Track
	SyncedAt int64
}

// Duration renvoie la durée totale en secondes.
func (p Playlist) Duration() int {
	n := 0
	for _, t := range p.Tracks {
		n += t.Duration
	}
	return n
}

// Cover renvoie la pochette du premier titre qui en a une.
func (p Playlist) Cover() string {
	for _, t := range p.Tracks {
		if t.Album.Cover != "" {
			return t.Album.Cover
		}
	}
	return ""
}

// Account est un compte lié par un spectateur.
type Account struct {
	Viewer   int64  `json:"-"`
	Service  string `json:"service"`
	Account  string `json:"account"`
	SyncedAt int64  `json:"syncedAt,omitempty"`
	Error    string `json:"error,omitempty"`
}

// PlaylistID calcule l'identifiant stable d'une playlist.
func PlaylistID(viewer int64, source, ref string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.Join([]string{strconv.FormatInt(viewer, 10), source, ref}, "\x00")))
	id := int64(h.Sum64() & maxPlaylistID)
	if id == 0 {
		id = 1
	}
	return id
}

// Playlists lit et écrit les playlists importées.
type Playlists struct {
	DB  *sql.DB
	Now func() time.Time
}

func (p *Playlists) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Accounts renvoie les comptes liés, de tous les spectateurs si viewer vaut 0.
func (p *Playlists) Accounts(ctx context.Context, viewer int64) ([]Account, error) {
	q := `SELECT viewer_id,service,account,synced_at,error FROM pse_music_accounts`
	var args []any
	if viewer != 0 {
		q += ` WHERE viewer_id=?`
		args = append(args, viewer)
	}
	rows, err := p.DB.QueryContext(ctx, q+` ORDER BY viewer_id, service`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.Viewer, &a.Service, &a.Account, &a.SyncedAt, &a.Error); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAccount lie (ou délie, account vide) un compte ; les playlists d'un
// compte délié ou changé sont retirées.
func (p *Playlists) SetAccount(ctx context.Context, viewer int64, service, account string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var prev string
	err = tx.QueryRowContext(ctx, `SELECT account FROM pse_music_accounts WHERE viewer_id=? AND service=?`, viewer, service).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if prev == account && account != "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pse_music_playlists WHERE viewer_id=? AND source=?`, viewer, service); err != nil {
		return err
	}
	if account == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM pse_music_accounts WHERE viewer_id=? AND service=?`, viewer, service)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO pse_music_accounts(viewer_id,service,account) VALUES(?,?,?)
ON CONFLICT(viewer_id,service) DO UPDATE SET account=excluded.account, synced_at=0, error=''`, viewer, service, account)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// report note le résultat d'une synchronisation.
func (p *Playlists) report(ctx context.Context, a Account, syncErr error) {
	msg := ""
	if syncErr != nil {
		msg = syncErr.Error()
		if len(msg) > 300 {
			msg = msg[:300]
		}
	}
	_, _ = p.DB.ExecContext(ctx, `UPDATE pse_music_accounts SET synced_at=?, error=? WHERE viewer_id=? AND service=? AND account=?`,
		p.now().Unix(), msg, a.Viewer, a.Service, a.Account)
}

// replace remplace les playlists d'un compte par celles fournies.
func (p *Playlists) replace(ctx context.Context, viewer int64, source string, lists []Playlist) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := map[string]bool{}
	now := p.now().Unix()
	for i, pl := range lists {
		keep[pl.Ref] = true
		raw, err := json.Marshal(pl.Tracks)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO pse_music_playlists(id,viewer_id,source,ref,name,position,tracks,synced_at) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(viewer_id,source,ref) DO UPDATE SET name=excluded.name, position=excluded.position, tracks=excluded.tracks, synced_at=excluded.synced_at`,
			PlaylistID(viewer, source, pl.Ref), viewer, source, pl.Ref, pl.Name, positionOf(source, i), string(raw), now)
		if err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT ref FROM pse_music_playlists WHERE viewer_id=? AND source=?`, viewer, source)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return err
		}
		if !keep[ref] {
			gone = append(gone, ref)
		}
	}
	rows.Close()
	for _, ref := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM pse_music_playlists WHERE viewer_id=? AND source=? AND ref=?`, viewer, source, ref); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// positionOf range les playlists personnelles puis « Découvertes » en tête, puis les playlists Deezer,
// les listes Last.fm et les tops par pays.
func positionOf(source string, i int) int {
	switch source {
	case SourceUser:
		return -2000 + i
	case SourceDiscover:
		return -1000 + i
	case ServiceLastFM:
		return 10000 + i
	case ServiceListenBrainz:
		return 15000 + i
	case SourceCharts:
		return 20000 + i
	}
	return i
}

// List renvoie les playlists d'un spectateur, avec leurs titres.
func (p *Playlists) List(ctx context.Context, viewer int64) ([]Playlist, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id,source,ref,name,tracks,synced_at FROM pse_music_playlists WHERE viewer_id=? ORDER BY position, name`, viewer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Playlist
	for rows.Next() {
		pl := Playlist{Viewer: viewer}
		var raw string
		if err := rows.Scan(&pl.ID, &pl.Source, &pl.Ref, &pl.Name, &raw, &pl.SyncedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &pl.Tracks)
		out = append(out, pl)
	}
	return out, rows.Err()
}

// Get renvoie une playlist d'un spectateur.
func (p *Playlists) Get(ctx context.Context, viewer, id int64) (Playlist, bool, error) {
	pl := Playlist{ID: id, Viewer: viewer}
	var raw string
	err := p.DB.QueryRowContext(ctx, `SELECT source,ref,name,tracks,synced_at FROM pse_music_playlists WHERE viewer_id=? AND id=?`, viewer, id).
		Scan(&pl.Source, &pl.Ref, &pl.Name, &raw, &pl.SyncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, false, nil
	}
	if err != nil {
		return Playlist{}, false, err
	}
	_ = json.Unmarshal([]byte(raw), &pl.Tracks)
	return pl, true, nil
}

// match lit une correspondance Last.fm → Deezer déjà cherchée.
func (p *Playlists) match(ctx context.Context, key string) (Track, bool, bool) {
	var raw string
	var at int64
	err := p.DB.QueryRowContext(ctx, `SELECT track,checked_at FROM pse_music_matches WHERE key=?`, key).Scan(&raw, &at)
	if err != nil {
		return Track{}, false, false
	}
	if raw == "" {
		// Introuvable : nouvel essai après une semaine.
		return Track{}, false, p.now().Unix()-at < int64(7*24*time.Hour/time.Second)
	}
	var t Track
	if json.Unmarshal([]byte(raw), &t) != nil || t.ID == 0 {
		return Track{}, false, false
	}
	return t, true, true
}

func (p *Playlists) saveMatch(ctx context.Context, key string, t *Track) {
	raw := ""
	if t != nil {
		b, _ := json.Marshal(t)
		raw = string(b)
	}
	_, _ = p.DB.ExecContext(ctx, `INSERT INTO pse_music_matches(key,track,checked_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET track=excluded.track, checked_at=excluded.checked_at`, key, raw, p.now().Unix())
}
