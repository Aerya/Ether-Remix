package music

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Historique des écoutes : une ligne par écoute complète. Il alimente les
// statistiques de la Vue d'ensemble et l'envoi des écoutes à Last.fm
// (lastfm_scrobble.go). Seules les références du titre sont gardées.
func init() {
	store.Register("pse_music_plays", 1, `
CREATE TABLE pse_music_plays(
  id INTEGER PRIMARY KEY,
  viewer_id INTEGER NOT NULL,
  track_id INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  artist TEXT NOT NULL DEFAULT '',
  album TEXT NOT NULL DEFAULT '',
  duration INTEGER NOT NULL DEFAULT 0,
  played_at INTEGER NOT NULL,
  scrobbled INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX pse_music_plays_viewer ON pse_music_plays(viewer_id, played_at);
CREATE INDEX pse_music_plays_time ON pse_music_plays(played_at);
`)
	store.RegisterViewerTable("pse_music_plays", "pse_music_plays", "viewer_id", store.ViewerMergeMove, "id")
}

// Valeurs de pse_music_plays.scrobbled.
const (
	scrobblePending = 0
	scrobbleSent    = 1
	scrobbleSkipped = 2 // refusée par Last.fm, trop ancienne ou sans compte lié
)

func (l *Library) recordPlay(ctx context.Context, viewer int64, t Track) error {
	_, err := l.db.ExecContext(ctx, `INSERT INTO pse_music_plays(viewer_id, track_id, title, artist, album, duration, played_at) VALUES(?,?,?,?,?,?,?)`,
		viewer, t.ID, t.Title, t.Artist.Name, t.Album.Title, t.Duration, l.now().Unix())
	return err
}

// StatsDay est une journée de l'historique.
type StatsDay struct {
	Date      string  `json:"date"` // AAAA-MM-JJ, heure locale
	Plays     int     `json:"plays"`
	Hours     float64 `json:"hours"`
	Listeners int     `json:"listeners"`
}

// StatsEntry est un titre ou un artiste du classement.
type StatsEntry struct {
	Name   string `json:"name"`
	Artist string `json:"artist,omitempty"`
	Plays  int    `json:"plays"`
}

// Stats résume les écoutes des derniers jours.
type Stats struct {
	Days       []StatsDay   `json:"days"`
	Plays      int          `json:"plays"`
	Previous   int          `json:"previous"` // écoutes de la période précédente
	Hours      float64      `json:"hours"`
	Listeners  int          `json:"listeners"`
	TopTracks  []StatsEntry `json:"topTracks"`
	TopArtists []StatsEntry `json:"topArtists"`
}

// Stats calcule le résumé des days derniers jours (tous spectateurs).
func (l *Library) Stats(ctx context.Context, days int) (Stats, error) {
	if days <= 0 {
		days = 30
	}
	now := l.now()
	y, m, d := now.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))
	prev := start.AddDate(0, 0, -days)
	out := Stats{Days: make([]StatsDay, days), TopTracks: []StatsEntry{}, TopArtists: []StatsEntry{}}
	index := map[string]int{}
	for i := range out.Days {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		out.Days[i].Date = date
		index[date] = i
	}
	rows, err := l.db.QueryContext(ctx, `SELECT viewer_id, title, artist, duration, played_at FROM pse_music_plays WHERE played_at>=?`, prev.Unix())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	listeners := map[int64]bool{}
	dayListeners := map[string]map[int64]bool{}
	tracks := map[[2]string]int{}
	artists := map[string]int{}
	for rows.Next() {
		var viewer, at int64
		var title, artist string
		var duration int
		if err := rows.Scan(&viewer, &title, &artist, &duration, &at); err != nil {
			return out, err
		}
		when := time.Unix(at, 0).In(now.Location())
		if when.Before(start) {
			out.Previous++
			continue
		}
		i, ok := index[when.Format("2006-01-02")]
		if !ok {
			continue
		}
		day := &out.Days[i]
		day.Plays++
		day.Hours += float64(duration) / 3600
		if dayListeners[day.Date] == nil {
			dayListeners[day.Date] = map[int64]bool{}
		}
		dayListeners[day.Date][viewer] = true
		listeners[viewer] = true
		out.Plays++
		out.Hours += float64(duration) / 3600
		if title != "" {
			tracks[[2]string{title, artist}]++
		}
		if artist != "" {
			artists[artist]++
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	for i := range out.Days {
		out.Days[i].Listeners = len(dayListeners[out.Days[i].Date])
	}
	out.Listeners = len(listeners)
	for k, n := range tracks {
		out.TopTracks = append(out.TopTracks, StatsEntry{Name: k[0], Artist: k[1], Plays: n})
	}
	for k, n := range artists {
		out.TopArtists = append(out.TopArtists, StatsEntry{Name: k, Plays: n})
	}
	out.TopTracks = topEntries(out.TopTracks, 5)
	out.TopArtists = topEntries(out.TopArtists, 5)
	return out, nil
}

func topEntries(e []StatsEntry, n int) []StatsEntry {
	sort.Slice(e, func(i, j int) bool {
		if e[i].Plays != e[j].Plays {
			return e[i].Plays > e[j].Plays
		}
		return e[i].Name < e[j].Name
	})
	if len(e) > n {
		e = e[:n]
	}
	return e
}

// pendingPlays renvoie les écoutes d'un spectateur à envoyer à Last.fm,
// les plus anciennes d'abord.
func (l *Library) pendingPlays(ctx context.Context, viewer, since int64, limit int) ([]play, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id, title, artist, album, duration, played_at FROM pse_music_plays
WHERE viewer_id=? AND scrobbled=? AND played_at>=? ORDER BY played_at LIMIT ?`, viewer, scrobblePending, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []play
	for rows.Next() {
		var p play
		if err := rows.Scan(&p.id, &p.title, &p.artist, &p.album, &p.duration, &p.at); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type play struct {
	id                   int64
	title, artist, album string
	duration             int
	at                   int64
}

func (l *Library) markPlays(ctx context.Context, ids []int64, state int) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE pse_music_plays SET scrobbled=? WHERE id=?`, state, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// sentPlays compte les écoutes envoyées à Last.fm pour un spectateur.
func (l *Library) sentPlays(ctx context.Context, viewer int64) int {
	var n int
	if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_music_plays WHERE viewer_id=? AND scrobbled=?`, viewer, scrobbleSent).Scan(&n); err != nil && err != sql.ErrNoRows {
		return 0
	}
	return n
}
