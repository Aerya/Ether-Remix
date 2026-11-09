package music

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// Playlists personnelles : créées, renommées, complétées et réordonnées
// par chaque spectateur depuis son client Subsonic. Comme les playlists
// importées, elles gardent un instantané des titres du catalogue : elles
// ne dépendent d'aucun fournisseur audio.

// SourceUser marque une playlist personnelle.
const SourceUser = "user"

// MaxUserTracks borne la taille d'une playlist personnelle.
const MaxUserTracks = 2000

// ErrReadOnly signale une playlist importée, non modifiable.
var ErrReadOnly = errors.New("music: playlist importée, non modifiable")

// Editable indique si la playlist appartient au spectateur.
func (p Playlist) Editable() bool { return p.Source == SourceUser }

func newRef() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func clipName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	return name
}

// CreatePersonal crée une playlist personnelle.
func (p *Playlists) CreatePersonal(ctx context.Context, viewer int64, name string, tracks []Track) (Playlist, error) {
	name = clipName(name)
	if viewer == 0 || name == "" {
		return Playlist{}, errors.New("music: nom de playlist requis")
	}
	if len(tracks) > MaxUserTracks {
		tracks = tracks[:MaxUserTracks]
	}
	ref := newRef()
	raw, err := json.Marshal(tracks)
	if err != nil {
		return Playlist{}, err
	}
	var n int
	_ = p.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_music_playlists WHERE viewer_id=? AND source=?`, viewer, SourceUser).Scan(&n)
	pl := Playlist{ID: PlaylistID(viewer, SourceUser, ref), Viewer: viewer, Source: SourceUser, Ref: ref, Name: name, Tracks: tracks, SyncedAt: p.now().Unix()}
	_, err = p.DB.ExecContext(ctx, `INSERT INTO pse_music_playlists(id,viewer_id,source,ref,name,position,tracks,synced_at) VALUES(?,?,?,?,?,?,?,?)`,
		pl.ID, viewer, SourceUser, ref, name, positionOf(SourceUser, n), string(raw), pl.SyncedAt)
	return pl, err
}

// PlaylistEdit décrit une modification de playlist personnelle.
type PlaylistEdit struct {
	Name    *string
	Replace []Track // remplace tous les titres (réordonnancement) si non nil
	Remove  []int   // positions à retirer (avant ajout)
	Add     []Track // titres ajoutés à la fin
}

// UpdatePersonal modifie une playlist personnelle du spectateur.
func (p *Playlists) UpdatePersonal(ctx context.Context, viewer, id int64, e PlaylistEdit) (Playlist, error) {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return Playlist{}, err
	}
	defer tx.Rollback()
	pl := Playlist{ID: id, Viewer: viewer}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT source,ref,name,tracks FROM pse_music_playlists WHERE viewer_id=? AND id=?`, viewer, id).
		Scan(&pl.Source, &pl.Ref, &pl.Name, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, ErrNotFound
	}
	if err != nil {
		return Playlist{}, err
	}
	if !pl.Editable() {
		return Playlist{}, ErrReadOnly
	}
	_ = json.Unmarshal([]byte(raw), &pl.Tracks)
	if e.Name != nil {
		if n := clipName(*e.Name); n != "" {
			pl.Name = n
		}
	}
	if e.Replace != nil {
		pl.Tracks = append([]Track{}, e.Replace...)
	}
	if len(e.Remove) > 0 {
		drop := map[int]bool{}
		for _, i := range e.Remove {
			drop[i] = true
		}
		kept := pl.Tracks[:0:0]
		for i, t := range pl.Tracks {
			if !drop[i] {
				kept = append(kept, t)
			}
		}
		pl.Tracks = kept
	}
	pl.Tracks = append(pl.Tracks, e.Add...)
	if len(pl.Tracks) > MaxUserTracks {
		pl.Tracks = pl.Tracks[:MaxUserTracks]
	}
	out, err := json.Marshal(pl.Tracks)
	if err != nil {
		return Playlist{}, err
	}
	pl.SyncedAt = p.now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE pse_music_playlists SET name=?, tracks=?, synced_at=? WHERE viewer_id=? AND id=?`,
		pl.Name, string(out), pl.SyncedAt, viewer, id); err != nil {
		return Playlist{}, err
	}
	return pl, tx.Commit()
}

// DeletePersonal supprime une playlist personnelle du spectateur.
func (p *Playlists) DeletePersonal(ctx context.Context, viewer, id int64) error {
	pl, ok, err := p.Get(ctx, viewer, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if !pl.Editable() {
		return ErrReadOnly
	}
	_, err = p.DB.ExecContext(ctx, `DELETE FROM pse_music_playlists WHERE viewer_id=? AND id=? AND source=?`, viewer, id, SourceUser)
	return err
}
