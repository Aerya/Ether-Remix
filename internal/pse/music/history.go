package music

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type HistoryPlay struct {
	ID       int64  `json:"id"`
	ViewerID int64  `json:"viewerId"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Duration int    `json:"duration"`
	PlayedAt int64  `json:"playedAt"`
}

type HistoryPage struct {
	Plays []HistoryPlay `json:"plays"`
	Total int           `json:"total"`
}

// History ne modifie pas les écoutes existantes et reste disponible lorsque
// la source musicale est désactivée. Durée = durée du titre, pas temps écouté.
func (l *Library) History(ctx context.Context, from, to time.Time, viewer int64, offset int, limits ...int) (HistoryPage, error) {
	out := HistoryPage{Plays: []HistoryPlay{}}
	where := " WHERE played_at>=? AND played_at<?"
	args := []any{from.Unix(), to.Unix()}
	if viewer > 0 {
		where += " AND viewer_id=?"
		args = append(args, viewer)
	}
	if err := l.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pse_music_plays"+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	limit := 100
	if len(limits) > 0 {
		limit = min(500, max(1, limits[0]))
	}
	args = append(args, limit, max(0, offset))
	rows, err := l.db.QueryContext(ctx, "SELECT id,viewer_id,title,artist,album,duration,played_at FROM pse_music_plays"+where+" ORDER BY played_at DESC,id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p HistoryPlay
		if err := rows.Scan(&p.ID, &p.ViewerID, &p.Title, &p.Artist, &p.Album, &p.Duration, &p.PlayedAt); err != nil {
			return out, err
		}
		out.Plays = append(out.Plays, p)
	}
	return out, rows.Err()
}

// HistoryHandler est monté sous l'API console propriétaire, jamais /rest/.
func HistoryHandler(l *Library) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		days, _ := strconv.Atoi(q.Get("days"))
		if days <= 0 {
			days = 30
		}
		days = min(days, 366)
		now := l.now().UTC()
		to := now.Add(time.Second)
		from := now.AddDate(0, 0, -days)
		if raw := q.Get("year"); raw != "" {
			year, err := strconv.Atoi(raw)
			if err != nil || year < 2000 || year > now.Year() {
				http.Error(w, "Année invalide", 400)
				return
			}
			from = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
			to = from.AddDate(1, 0, 0)
		}
		viewer, _ := strconv.ParseInt(q.Get("user"), 10, 64)
		if q.Get("user") != "" && viewer <= 0 {
			http.Error(w, "Spectateur invalide", 400)
			return
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		offset = max(0, offset)
		if r.URL.Path == "/music/listening" {
			tz, _ := strconv.Atoi(q.Get("tz"))
			result, err := l.Listening(r.Context(), from, to, viewer, tz)
			if err != nil {
				http.Error(w, "Statistiques musicales indisponibles", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		limit := 100
		if n, err := strconv.Atoi(q.Get("limit")); err == nil {
			limit = n
		}
		result, err := l.History(r.Context(), from, to, viewer, offset, limit)
		if err != nil {
			http.Error(w, "Historique musical indisponible", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}
