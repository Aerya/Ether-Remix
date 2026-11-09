package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// La bibliothèque musicale d'un utilisateur se compose de ce qu'il a mis en
// favori ou écouté. Chaque ligne garde un instantané de l'objet HiFi pour
// lister la bibliothèque sans interroger l'API.
func init() {
	store.Register("pse_music", 1, `
CREATE TABLE pse_music_items(
  viewer_id INTEGER NOT NULL,
  kind TEXT NOT NULL,
  item_id INTEGER NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  data TEXT NOT NULL DEFAULT '{}',
  added_at INTEGER NOT NULL DEFAULT 0,
  starred_at INTEGER NOT NULL DEFAULT 0,
  play_count INTEGER NOT NULL DEFAULT 0,
  played_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(viewer_id, kind, item_id)
);
CREATE INDEX pse_music_items_viewer ON pse_music_items(viewer_id, kind, starred_at);
`)
	store.RegisterViewerTable("pse_music", "pse_music_items", "viewer_id", store.ViewerMergeUnion, "kind", "item_id")
}

// Kinds de la bibliothèque.
const (
	KindArtist = "artist"
	KindAlbum  = "album"
	KindTrack  = "track"
)

// Item est une entrée de bibliothèque.
type Item struct {
	Kind      string
	ID        int64
	Data      json.RawMessage
	AddedAt   int64
	StarredAt int64
	PlayCount int
	PlayedAt  int64
}

// Library lit et écrit la bibliothèque musicale.
type Library struct {
	db  *sql.DB
	now func() time.Time
}

// NewLibrary prépare la bibliothèque sur la base d'Ether.
func NewLibrary(db *sql.DB) *Library { return &Library{db: db, now: time.Now} }

func (l *Library) upsert(ctx context.Context, viewer int64, kind string, id int64, name string, data any, star, play int) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	now := l.now().Unix()
	starredAt := int64(-1) // inchangé
	switch star {
	case 1:
		starredAt = now
	case -1:
		starredAt = 0
	}
	playedAt := int64(0)
	if play > 0 {
		playedAt = now
	}
	initialStar := starredAt
	if initialStar < 0 {
		initialStar = 0
	}
	_, err = l.db.ExecContext(ctx, `INSERT INTO pse_music_items(viewer_id,kind,item_id,name,data,added_at,starred_at,play_count,played_at)
VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(viewer_id,kind,item_id) DO UPDATE SET
  name=excluded.name,
  data=CASE WHEN LENGTH(excluded.data)>=LENGTH(pse_music_items.data) THEN excluded.data ELSE pse_music_items.data END,
  starred_at=CASE WHEN ?>=0 THEN ? ELSE pse_music_items.starred_at END,
  play_count=pse_music_items.play_count+excluded.play_count,
  played_at=CASE WHEN excluded.played_at>0 THEN excluded.played_at ELSE pse_music_items.played_at END`,
		viewer, kind, id, strings.ToLower(name), string(raw), now, initialStar, play, playedAt, starredAt, starredAt)
	return err
}

// Star met un objet en favori (star=true) ou le retire (false) et garde
// l'artiste et l'album associés dans la bibliothèque.
func (l *Library) StarTrack(ctx context.Context, viewer int64, t Track, on bool) error {
	if err := l.upsert(ctx, viewer, KindTrack, t.ID, t.Title, t, starFlag(on), 0); err != nil {
		return err
	}
	return l.touchParents(ctx, viewer, t.Album, t.Artist)
}

func (l *Library) StarAlbum(ctx context.Context, viewer int64, a Album, on bool) error {
	if err := l.upsert(ctx, viewer, KindAlbum, a.ID, a.Title, a, starFlag(on), 0); err != nil {
		return err
	}
	return l.touchParents(ctx, viewer, Album{}, a.Artist)
}

func (l *Library) StarArtist(ctx context.Context, viewer int64, a Artist, on bool) error {
	return l.upsert(ctx, viewer, KindArtist, a.ID, a.Name, a, starFlag(on), 0)
}

// Played enregistre une écoute complète d'un titre.
func (l *Library) Played(ctx context.Context, viewer int64, t Track) error {
	if err := l.recordPlay(ctx, viewer, t); err != nil {
		return err
	}
	if err := l.upsert(ctx, viewer, KindTrack, t.ID, t.Title, t, 0, 1); err != nil {
		return err
	}
	if t.Album.ID != 0 {
		if err := l.upsert(ctx, viewer, KindAlbum, t.Album.ID, t.Album.Title, t.Album, 0, 1); err != nil {
			return err
		}
	}
	if t.Artist.ID != 0 {
		return l.upsert(ctx, viewer, KindArtist, t.Artist.ID, t.Artist.Name, t.Artist, 0, 1)
	}
	return nil
}

func (l *Library) touchParents(ctx context.Context, viewer int64, album Album, artist Artist) error {
	if album.ID != 0 {
		if err := l.upsert(ctx, viewer, KindAlbum, album.ID, album.Title, album, 0, 0); err != nil {
			return err
		}
	}
	if artist.ID != 0 {
		return l.upsert(ctx, viewer, KindArtist, artist.ID, artist.Name, artist, 0, 0)
	}
	return nil
}

func starFlag(on bool) int {
	if on {
		return 1
	}
	return -1
}

// ListOptions filtre et ordonne une liste de bibliothèque.
type ListOptions struct {
	Starred bool
	Played  bool
	Order   string // name, added, played, frequent, random
	Limit   int
	Offset  int
}

// List renvoie les entrées d'un type pour un utilisateur.
func (l *Library) List(ctx context.Context, viewer int64, kind string, o ListOptions) ([]Item, error) {
	q := `SELECT kind,item_id,data,added_at,starred_at,play_count,played_at FROM pse_music_items WHERE viewer_id=? AND kind=?`
	args := []any{viewer, kind}
	if o.Starred {
		q += ` AND starred_at>0`
	}
	if o.Played {
		q += ` AND play_count>0`
	}
	switch o.Order {
	case "added":
		q += ` ORDER BY added_at DESC, item_id`
	case "played":
		q += ` ORDER BY played_at DESC, item_id`
	case "frequent":
		q += ` ORDER BY play_count DESC, played_at DESC, item_id`
	case "starred":
		q += ` ORDER BY starred_at DESC, item_id`
	case "random":
		q += ` ORDER BY RANDOM()`
	default:
		q += ` ORDER BY name, item_id`
	}
	if o.Limit <= 0 {
		o.Limit = 100000
	}
	q += ` LIMIT ? OFFSET ?`
	args = append(args, o.Limit, o.Offset)
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var data string
		if err := rows.Scan(&it.Kind, &it.ID, &data, &it.AddedAt, &it.StarredAt, &it.PlayCount, &it.PlayedAt); err != nil {
			return nil, err
		}
		it.Data = json.RawMessage(data)
		out = append(out, it)
	}
	return out, rows.Err()
}

// Get renvoie une entrée, ou ok=false si elle n'est pas dans la bibliothèque.
func (l *Library) Get(ctx context.Context, viewer int64, kind string, id int64) (Item, bool, error) {
	var it Item
	var data string
	err := l.db.QueryRowContext(ctx, `SELECT kind,item_id,data,added_at,starred_at,play_count,played_at FROM pse_music_items WHERE viewer_id=? AND kind=? AND item_id=?`, viewer, kind, id).
		Scan(&it.Kind, &it.ID, &data, &it.AddedAt, &it.StarredAt, &it.PlayCount, &it.PlayedAt)
	if err == sql.ErrNoRows {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	it.Data = json.RawMessage(data)
	return it, true, nil
}

// Count renvoie le nombre d'entrées d'un type.
func (l *Library) Count(ctx context.Context, viewer int64, kind string) (int, error) {
	var n int
	err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_music_items WHERE viewer_id=? AND kind=?`, viewer, kind).Scan(&n)
	return n, err
}

// Totals compte les entrées de bibliothèque de tous les utilisateurs.
func (l *Library) Totals(ctx context.Context) (map[string]int, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT kind, COUNT(*) FROM pse_music_items GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}
