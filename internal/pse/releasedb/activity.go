package releasedb

import (
	"context"
	"errors"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_releases", 7, `CREATE INDEX pse_releases_source ON pse_releases(source_id,title_key);`)
	store.Register("pse_index_activity", 1, `
CREATE TABLE pse_index_activity(
 day TEXT NOT NULL, source_id TEXT NOT NULL,
 searches INTEGER NOT NULL DEFAULT 0, errors INTEGER NOT NULL DEFAULT 0,
 partial INTEGER NOT NULL DEFAULT 0, denied INTEGER NOT NULL DEFAULT 0,
 results INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(day,source_id)
);

`)
}

// SearchEvent never contains upstream error strings, URLs or credentials.
type SearchEvent struct {
	Anime      bool           `json:"anime"`
	ID         uint64         `json:"id"`
	SourceID   string         `json:"sourceId"`
	Source     string         `json:"source"`
	Title      string         `json:"title"`
	Type       core.MediaType `json:"type"`
	Started    int64          `json:"started"`
	Finished   int64          `json:"finished,omitempty"`
	Status     string         `json:"status"`
	Results    int            `json:"results"`
	DurationMS int64          `json:"durationMs"`
}
type activityLog struct {
	mu     sync.Mutex
	seq    uint64
	active map[uint64]SearchEvent
	recent []SearchEvent
}
type Activity struct {
	At     int64         `json:"at"`
	State  string        `json:"state"`
	Active []SearchEvent `json:"active"`
	Recent []SearchEvent `json:"recent"`
}

func (s *Service) Activity() Activity {
	a := Activity{At: s.d.Now().UnixMilli(), State: "waiting", Active: []SearchEvent{}, Recent: []SearchEvent{}}
	switch {
	case !s.d.Settings.Enabled:
		a.State = "disabled"
	case s.d.Active != nil && s.d.Active():
		a.State = "paused"
	case len(s.sources()) == 0:
		a.State = "no_sources"
	}
	s.activity.mu.Lock()
	defer s.activity.mu.Unlock()
	for _, e := range s.activity.active {
		a.Active = append(a.Active, e)
	}
	a.Recent = append(a.Recent, s.activity.recent...)
	if len(a.Active) > 0 {
		a.State = "searching"
	}
	return a
}
func (s *Service) beginSearch(t core.Title, src source) SearchEvent {
	s.activity.mu.Lock()
	defer s.activity.mu.Unlock()
	s.activity.seq++
	e := SearchEvent{ID: s.activity.seq, SourceID: src.id, Source: src.name, Title: t.Title, Type: t.Type, Anime: t.IsAnime(), Started: s.d.Now().UnixMilli(), Status: "searching"}
	if s.activity.active == nil {
		s.activity.active = map[uint64]SearchEvent{}
	}
	s.activity.active[e.ID] = e
	return e
}
func (s *Service) finishSearch(ctx context.Context, e SearchEvent, results int, err error) {
	e.Finished = s.d.Now().UnixMilli()
	e.DurationMS = max(int64(0), e.Finished-e.Started)
	e.Results = results
	e.Status = "success"
	failed, partial, denied := 0, 0, 0
	switch {
	case errors.Is(err, context.Canceled):
		e.Status = "cancelled"
	case errors.Is(err, core.ErrBudgetExhausted):
		e.Status = "budget"
		denied = 1
	case err != nil && results > 0:
		e.Status = "partial"
		partial = 1
	case err != nil:
		e.Status = "error"
		failed = 1
	case results == 0:
		e.Status = "empty"
	}
	s.activity.mu.Lock()
	delete(s.activity.active, e.ID)
	// Une source au budget du jour épuisé n'est pas interrogée : elle reste
	// comptée dans le détail par source (denied) mais n'occupe pas les
	// dernières recherches, qu'elle remplirait à chaque titre.
	if e.Status != "budget" {
		s.activity.recent = append([]SearchEvent{e}, s.activity.recent...)
		if len(s.activity.recent) > 50 {
			s.activity.recent = s.activity.recent[:50]
		}
	}
	s.activity.mu.Unlock()
	if e.Status == "cancelled" {
		return
	}
	if err := s.recordSourceHistory(ctx, e, s.d.Now()); err != nil && ctx.Err() == nil {
		s.d.Logger.Warn("pse indexing history unavailable")
	}
	_, saveErr := s.st.db.ExecContext(ctx, `INSERT INTO pse_index_activity(day,source_id,searches,errors,partial,denied,results,duration_ms)
VALUES(?,?,1,?,?,?,?,?) ON CONFLICT(day,source_id) DO UPDATE SET
searches=searches+1,errors=errors+excluded.errors,partial=partial+excluded.partial,
denied=denied+excluded.denied,results=results+excluded.results,duration_ms=duration_ms+excluded.duration_ms`,
		s.d.Now().UTC().Format("2006-01-02"), e.SourceID, failed, partial, denied, results, e.DurationMS)
	if saveErr != nil && ctx.Err() == nil {
		s.d.Logger.Warn("pse activity statistics unavailable")
	}
}

type SourceStats struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Selected   bool   `json:"selected"`
	Limit      int    `json:"limit"`
	Hits       int    `json:"hits"`
	Titles     int    `json:"titles"`
	Releases   int    `json:"releases"`
	Searches   int    `json:"searches"`
	Errors     int    `json:"errors"`
	Partial    int    `json:"partial"`
	Denied     int    `json:"denied"`
	Results    int    `json:"results"`
	DurationMS int64  `json:"durationMs"`
}
type SourceDay struct {
	Day      string `json:"day"`
	SourceID string `json:"sourceId"`
	Hits     int    `json:"hits"`
	Searches int    `json:"searches"`
	Errors   int    `json:"errors"`
}
type SourceOverview struct {
	At      int64         `json:"at"`
	Sources []SourceStats `json:"sources"`
	History []SourceDay   `json:"history"`
}

func (s *Service) SourceOverview(ctx context.Context) (SourceOverview, error) {
	now := s.d.Now()
	out := SourceOverview{At: now.UnixMilli(), Sources: []SourceStats{}, History: []SourceDay{}}
	byID := map[string]*SourceStats{}
	order := []string{}
	get := func(id string) *SourceStats {
		if v := byID[id]; v != nil {
			return v
		}
		v := &SourceStats{ID: id, Name: id}
		byID[id] = v
		order = append(order, id)
		return v
	}
	for _, ix := range s.d.Config().Indexers {
		v := get(ix.ID)
		v.Name = ix.Name
		v.Enabled = ix.Enabled
	}
	for _, src := range s.sources() {
		v := get(src.id)
		v.Selected = true
		v.Limit = src.limit
	}
	if s.d.Settings.SFR.Enabled {
		v := get(SFRSourceID)
		v.Name, v.Enabled = sfrName, true
	}
	rows, err := s.st.db.QueryContext(ctx, `SELECT source_id,COUNT(DISTINCT title_key),COUNT(*) FROM pse_releases GROUP BY source_id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id string
		var titles, releases int
		if err = rows.Scan(&id, &titles, &releases); err != nil {
			break
		}
		v := get(id)
		v.Titles = titles
		v.Releases = releases
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if rowErr != nil {
		return out, rowErr
	}
	since := now.UTC().AddDate(0, 0, -6).Format("2006-01-02")
	rows, err = s.st.db.QueryContext(ctx, `SELECT day,source_id,hits,0,0 FROM pse_index_budget WHERE day>=?
UNION ALL SELECT day,source_id,0,searches,errors+partial FROM pse_index_activity WHERE day>=? ORDER BY 1,2`, since, since)
	if err != nil {
		return out, err
	}
	history := map[string]*SourceDay{}
	for rows.Next() {
		var d SourceDay
		if err = rows.Scan(&d.Day, &d.SourceID, &d.Hits, &d.Searches, &d.Errors); err != nil {
			break
		}
		key := d.Day + "\x00" + d.SourceID
		if prev := history[key]; prev != nil {
			prev.Hits += d.Hits
			prev.Searches += d.Searches
			prev.Errors += d.Errors
		} else {
			copy := d
			history[key] = &copy
		}
		get(d.SourceID)
		if d.Day == now.UTC().Format("2006-01-02") {
			get(d.SourceID).Hits += d.Hits
		}
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if rowErr != nil {
		return out, rowErr
	}
	// Stable chronological order, including zero days, makes charts unambiguous.
	for i := 0; i < 7; i++ {
		day := now.UTC().AddDate(0, 0, i-6).Format("2006-01-02")
		for _, id := range order {
			if d := history[day+"\x00"+id]; d != nil {
				out.History = append(out.History, *d)
			} else {
				out.History = append(out.History, SourceDay{Day: day, SourceID: id})
			}
		}
	}
	rows, err = s.st.db.QueryContext(ctx, `SELECT source_id,searches,errors,partial,denied,results,duration_ms FROM pse_index_activity WHERE day=?`, now.UTC().Format("2006-01-02"))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id string
		var v SourceStats
		if err = rows.Scan(&id, &v.Searches, &v.Errors, &v.Partial, &v.Denied, &v.Results, &v.DurationMS); err != nil {
			break
		}
		dst := get(id)
		dst.Searches = v.Searches
		dst.Errors = v.Errors
		dst.Partial = v.Partial
		dst.Denied = v.Denied
		dst.Results = v.Results
		dst.DurationMS = v.DurationMS
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if rowErr != nil {
		return out, rowErr
	}
	for _, id := range order {
		out.Sources = append(out.Sources, *byID[id])
	}
	return out, nil
}
