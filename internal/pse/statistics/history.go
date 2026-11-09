package statistics

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

type Source struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Bucket struct {
	Start                   string `json:"start"`
	Movies                  int    `json:"movies"`
	Shows                   int    `json:"shows"`
	MovieSearches           int    `json:"movieSearches"`
	ShowSearches            int    `json:"showSearches"`
	Errors                  int    `json:"errors"`
	Denied                  int    `json:"denied"`
	CumulativeMovies        int    `json:"cumulativeMovies"`
	CumulativeShows         int    `json:"cumulativeShows"`
	CumulativeMovieSearches int    `json:"cumulativeMovieSearches"`
	CumulativeShowSearches  int    `json:"cumulativeShowSearches"`
}
type Dashboard struct {
	At                  int64      `json:"at"`
	Period              string     `json:"period"`
	TMDB                TMDBTotals `json:"tmdb"`
	History             []Bucket   `json:"history"`
	Sources             []Source   `json:"sources"`
	SearchTrackingSince string     `json:"searchTrackingSince"`
	Undated             int        `json:"undated"`
}

func periodStart(t time.Time, period string) time.Time {
	t = t.UTC()
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	switch period {
	case "week":
		return t.AddDate(0, 0, -(int(t.Weekday())+6)%7)
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "year":
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return t
}
func stepPeriod(t time.Time, period string, n int) time.Time {
	switch period {
	case "week":
		return t.AddDate(0, 0, 7*n)
	case "month":
		return t.AddDate(0, n, 0)
	case "year":
		return t.AddDate(n, 0, 0)
	default:
		return t.AddDate(0, 0, n)
	}
}
func (s *Service) History(ctx context.Context, period, source string) (Dashboard, error) {
	d := Dashboard{At: s.now().Unix(), Period: period, TMDB: s.Snapshot(), History: []Bucket{}, Sources: []Source{}}
	count := 0
	switch period {
	case "day":
		count = 30
	case "week", "month":
		count = 12
	case "year":
		count = 10
	default:
		return d, errors.New("invalid period")
	}
	if s.db == nil {
		return d, errors.New("statistics database unavailable")
	}
	since := stepPeriod(periodStart(s.now(), period), period, 1-count)
	byStart := map[string]*Bucket{}
	for i := 0; i < count; i++ {
		d.History = append(d.History, Bucket{Start: stepPeriod(since, period, i).Format("2006-01-02")})
	}
	for i := range d.History {
		byStart[d.History[i].Start] = &d.History[i]
	}
	cumulativeMovies, cumulativeShows, cumulativeMovieSearches, cumulativeShowSearches := 0, 0, 0, 0
	// Les dates d'ajout sont conservées par Ether lors des rafraîchissements.
	// Grouper dans SQL évite de renvoyer une ligne par média.
	rows, err := s.db.QueryContext(ctx, `SELECT CAST(added_at/86400 AS INTEGER),type,COUNT(*) FROM catalog_titles WHERE type IN ('movie','show') AND browsable=1 GROUP BY CAST(added_at/86400 AS INTEGER),type`)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var day int64
		var kind string
		var n int
		if err = rows.Scan(&day, &kind, &n); err != nil {
			break
		}
		if day <= 0 {
			d.Undated += n
			continue
		}
		date := time.Unix(day*86400, 0).UTC()
		if date.Before(since) {
			if kind == "movie" {
				cumulativeMovies += n
			} else {
				cumulativeShows += n
			}
		} else if b := byStart[periodStart(date, period).Format("2006-01-02")]; b != nil {
			if kind == "movie" {
				b.Movies += n
			} else {
				b.Shows += n
			}
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	if rowErr != nil {
		return d, rowErr
	}
	var tracking sql.NullString
	if err = s.db.QueryRowContext(ctx, "SELECT MIN(day) FROM pse_index_source_history").Scan(&tracking); err != nil {
		return d, err
	}
	d.SearchTrackingSince = tracking.String
	filter := ""
	var args []any
	if source != "" {
		filter = " WHERE source_id=?"
		args = append(args, source)
	}
	rows, err = s.db.QueryContext(ctx, `SELECT day,media_type,SUM(searches),SUM(failed),SUM(denied) FROM pse_index_source_history`+filter+` GROUP BY day,media_type`, args...)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var day, kind string
		var n, failed, denied int
		if err = rows.Scan(&day, &kind, &n, &failed, &denied); err != nil {
			break
		}
		date, parseErr := time.Parse("2006-01-02", day)
		if parseErr != nil {
			err = parseErr
			break
		}
		if kind != "movie" && kind != "show" {
			continue
		}
		if date.Before(since) {
			if kind == "movie" {
				cumulativeMovieSearches += n
			} else {
				cumulativeShowSearches += n
			}
		} else if b := byStart[periodStart(date, period).Format("2006-01-02")]; b != nil {
			if kind == "movie" {
				b.MovieSearches += n
			} else {
				b.ShowSearches += n
			}
			b.Errors += failed
			b.Denied += denied
		}
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	if rowErr != nil {
		return d, rowErr
	}
	for i := range d.History {
		b := &d.History[i]
		cumulativeMovies += b.Movies
		cumulativeShows += b.Shows
		cumulativeMovieSearches += b.MovieSearches
		cumulativeShowSearches += b.ShowSearches
		b.CumulativeMovies, b.CumulativeShows = cumulativeMovies, cumulativeShows
		b.CumulativeMovieSearches, b.CumulativeShowSearches = cumulativeMovieSearches, cumulativeShowSearches
	}
	rows, err = s.db.QueryContext(ctx, "SELECT DISTINCT source_id FROM pse_index_source_history")
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		d.Sources = append(d.Sources, Source{ID: id, Name: id})
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	if rowErr != nil {
		return d, rowErr
	}
	sort.Slice(d.Sources, func(i, j int) bool { return d.Sources[i].ID < d.Sources[j].ID })
	return d, nil
}
