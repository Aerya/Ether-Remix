package vela

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func (i *instance) recapSource() (frontend.HistoryRecaps, bool) {
	source, ok := i.host.Experience.(frontend.HistoryRecaps)
	return source, ok
}

// historyRecap serves GET /vela/v0/history/recap?period=&at=&library=&tz=:
// a viewer's listening in the week, month or year containing `at` (default
// today), or all of it.
func (i *instance) historyRecap(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.recapSource()
	if !ok {
		i.unavailable(w, r)
		return
	}
	query := r.URL.Query()
	q := frontend.RecapQuery{Period: query.Get("period"), Location: time.UTC}
	if !slices.Contains(frontend.RecapPeriods, q.Period) {
		problem(w, 400, "request", "invalid_request")
		return
	}
	if tz := query.Get("tz"); tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			problem(w, 400, "request", "invalid_request")
			return
		}
		q.Location = loc
	}
	if at := query.Get("at"); at != "" {
		day, err := time.ParseInLocation("2006-01-02", at, q.Location)
		if err != nil {
			problem(w, 400, "request", "invalid_request")
			return
		}
		q.At = day
	}
	if lib := query.Get("library"); lib != "" {
		id, err := strconv.ParseInt(lib, 10, 64)
		if err != nil || id <= 0 {
			problem(w, 400, "request", "invalid_request")
			return
		}
		q.Library = frontend.LibraryID(id)
	}
	recap, err := source.HistoryRecap(r.Context(), v, q)
	if err != nil {
		fail(w, err, r)
		return
	}
	var ts []frontend.Title
	for _, group := range [][]frontend.RecapEntry{recap.TopArtists, recap.TopTracks, recap.TopAlbums} {
		for _, e := range group {
			ts = append(ts, e.Title)
		}
	}
	wired, err := i.titles(r, v, token, ts)
	if err != nil {
		fail(w, err, r)
		return
	}
	n := 0
	entries := func(group []frontend.RecapEntry) []object {
		out := make([]object, 0, len(group))
		for _, e := range group {
			out = append(out, object{"title": wired[n], "plays": e.Plays, "minutes": e.Minutes})
			n++
		}
		return out
	}
	days := make([]object, len(recap.ByDay))
	for k, d := range recap.ByDay {
		days[k] = object{"date": d.Date, "minutes": d.Minutes}
	}
	out := object{
		"period":          recap.Period,
		"from":            recap.From.UTC().Format(time.RFC3339),
		"to":              recap.To.UTC().Format(time.RFC3339),
		"minutes":         recap.Minutes,
		"plays":           recap.Plays,
		"distinctTracks":  recap.DistinctTracks,
		"distinctArtists": recap.DistinctArtists,
		"topArtists":      entries(recap.TopArtists),
		"topTracks":       entries(recap.TopTracks),
		"topAlbums":       entries(recap.TopAlbums),
		"streak":          object{"current": recap.StreakCurrent, "longest": recap.StreakLongest},
		"byDay":           days,
		"generatedAt":     recap.GeneratedAt.UTC().Format(time.RFC3339),
	}
	if recap.FirstPlayAt != nil {
		out["firstPlayAt"] = recap.FirstPlayAt.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	varyLanguage(w)
	send(w, 200, out)
}

func (i *instance) recapAvailable() bool {
	_, ok := i.recapSource()
	return ok
}
