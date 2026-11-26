package experience

import (
	"context"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// recapTop is how many artists, tracks and albums a recap ranks.
const recapTop = 10

// recapPeriod is the [from, to) a recap covers in loc, and the days its
// byDay chart lists.
func recapPeriod(period string, at, now time.Time, loc *time.Location) (from, to, chartFrom time.Time, ok bool) {
	at = at.In(loc)
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
	today := now.In(loc)
	tomorrow := time.Date(today.Year(), today.Month(), today.Day()+1, 0, 0, 0, 0, loc)
	switch period {
	case "week":
		from = day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
		to = from.AddDate(0, 0, 7)
	case "month":
		from = time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, loc)
		to = from.AddDate(0, 1, 0)
	case "year":
		from = time.Date(day.Year(), 1, 1, 0, 0, 0, 0, loc)
		to = from.AddDate(1, 0, 0)
	case "all":
		from, to = time.Unix(0, 0).In(loc), tomorrow
		return from, to, tomorrow.AddDate(0, 0, -365), true
	default:
		return time.Time{}, time.Time{}, time.Time{}, false
	}
	return from, to, from, true
}

// HistoryRecap aggregates a viewer's completed plays of plugin items in one
// period: minutes, plays, top artists, tracks and albums, streaks and minutes
// per day. It reads that viewer's own history for the period (indexed) and
// projects the titles it played in batches; it never walks a library.
func (s *Service) HistoryRecap(ctx context.Context, viewer *core.Viewer, q frontend.RecapQuery) (frontend.HistoryRecap, error) {
	if s.d.HistoryPlays == nil || s.d.PluginLibraries == nil {
		return frontend.HistoryRecap{}, core.ErrNotAvailable
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.HistoryRecap{}, err
	}
	loc := q.Location
	if loc == nil {
		loc = time.UTC
	}
	now := time.Now()
	if q.At.IsZero() {
		q.At = now
	}
	from, to, chartFrom, ok := recapPeriod(q.Period, q.At, now, loc)
	if !ok {
		return frontend.HistoryRecap{}, core.ErrNotFound
	}
	out := frontend.HistoryRecap{Period: q.Period, From: from, To: to, GeneratedAt: now.UTC()}
	plays, err := s.d.HistoryPlays(ctx, v.ID, from, to, 0)
	if err != nil {
		return out, err
	}
	keys := make([]core.RatingKey, 0, 64)
	seen := map[core.RatingKey]bool{}
	for _, p := range plays {
		if !seen[p.Key] {
			seen[p.Key] = true
			keys = append(keys, p.Key)
		}
	}
	views, err := s.d.PluginLibraries.ItemsMany(ctx, keys)
	if err != nil {
		return out, err
	}
	var titles []core.Title
	for _, k := range keys {
		if piv, ok := views[k]; ok && (q.Library == 0 || piv.Library.ID == q.Library) {
			titles = append(titles, piv.Title)
		}
	}
	visible, err := s.filterPluginTitles(ctx, v, titles, frontend.ItemQuery{})
	if err != nil {
		return out, err
	}
	byKey := make(map[core.RatingKey]core.Title, len(visible))
	for _, t := range visible {
		byKey[t.Key] = t
	}

	type tally struct {
		plays   int
		minutes float64
	}
	tracks, albums, artists := map[core.RatingKey]*tally{}, map[core.RatingKey]*tally{}, map[core.RatingKey]*tally{}
	add := func(m map[core.RatingKey]*tally, k core.RatingKey, mins float64) {
		if k == 0 {
			return
		}
		t := m[k]
		if t == nil {
			t = &tally{}
			m[k] = t
		}
		t.plays++
		t.minutes += mins
	}
	days := map[string]float64{}
	for _, p := range plays {
		t, ok := byKey[p.Key]
		if !ok {
			continue
		}
		mins := t.Runtime.Minutes()
		out.Plays++
		out.Minutes += mins
		if out.FirstPlayAt == nil {
			first := p.At.UTC()
			out.FirstPlayAt = &first
		}
		days[p.At.In(loc).Format("2006-01-02")] += mins
		add(tracks, t.Key, mins)
		add(albums, t.ParentKey, mins)
		add(artists, recapArtist(t), mins)
	}
	out.DistinctTracks, out.DistinctArtists = len(tracks), len(artists)
	out.Minutes = round1(out.Minutes)

	rank := func(m map[core.RatingKey]*tally) []core.RatingKey {
		ks := make([]core.RatingKey, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Slice(ks, func(i, j int) bool {
			a, b := m[ks[i]], m[ks[j]]
			if a.plays != b.plays {
				return a.plays > b.plays
			}
			if a.minutes != b.minutes {
				return a.minutes > b.minutes
			}
			return ks[i] < ks[j]
		})
		if len(ks) > recapTop {
			ks = ks[:recapTop]
		}
		return ks
	}
	topTracks, topAlbums, topArtists := rank(tracks), rank(albums), rank(artists)
	parents, err := s.d.PluginLibraries.ItemsMany(ctx, append(append([]core.RatingKey{}, topAlbums...), topArtists...))
	if err != nil {
		parents = nil
	}
	entries := func(ks []core.RatingKey, m map[core.RatingKey]*tally, title func(core.RatingKey) (core.Title, bool)) []frontend.RecapEntry {
		var es []frontend.RecapEntry
		for _, k := range ks {
			if t, ok := title(k); ok {
				es = append(es, frontend.RecapEntry{Title: t, Plays: m[k].plays, Minutes: round1(m[k].minutes)})
			}
		}
		return es
	}
	fromParents := func(k core.RatingKey) (core.Title, bool) {
		piv, ok := parents[k]
		return piv.Title, ok
	}
	out.TopTracks = entries(topTracks, tracks, func(k core.RatingKey) (core.Title, bool) { t, ok := byKey[k]; return t, ok })
	out.TopAlbums = entries(topAlbums, albums, fromParents)
	out.TopArtists = entries(topArtists, artists, fromParents)

	out.StreakCurrent, out.StreakLongest = recapStreaks(days, now.In(loc))
	end := to
	if tomorrow := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day()+1, 0, 0, 0, 0, loc); end.After(tomorrow) {
		end = tomorrow
	}
	for d := chartFrom; d.Before(end); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		out.ByDay = append(out.ByDay, frontend.RecapDay{Date: key, Minutes: round1(days[key])})
	}
	return out, nil
}

// recapArtist is who a title is by: its nearest artist-like ancestor (artist,
// author, host), else its root.
func recapArtist(t core.Title) core.RatingKey {
	for _, p := range t.MediaParents {
		switch p.Role {
		case core.MediaRoleArtist, "author", "host", core.MediaRoleShow:
			if p.Key != 0 {
				return p.Key
			}
		}
	}
	if n := len(t.MediaParents); n > 0 {
		return t.MediaParents[n-1].Key
	}
	return 0
}

// recapStreaks counts consecutive days with plays: the run reaching today
// (or yesterday, before today's first play) and the longest run.
func recapStreaks(days map[string]float64, today time.Time) (current, longest int) {
	if len(days) == 0 {
		return 0, 0
	}
	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	run := 0
	var prev time.Time
	for _, k := range keys {
		d, err := time.ParseInLocation("2006-01-02", k, today.Location())
		if err != nil {
			continue
		}
		if run > 0 && d.Sub(prev) <= 25*time.Hour {
			run++
		} else {
			run = 1
		}
		prev = d
		longest = max(longest, run)
	}
	cursor := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	if _, ok := days[cursor.Format("2006-01-02")]; !ok {
		cursor = cursor.AddDate(0, 0, -1)
	}
	for {
		if _, ok := days[cursor.Format("2006-01-02")]; !ok {
			break
		}
		current++
		cursor = cursor.AddDate(0, 0, -1)
	}
	return current, longest
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// PluginDuplicateGroup is one recording with several copies in a library.
type PluginDuplicateGroup struct {
	Recording string
	Count     int
	Titles    []core.Title
}

// PluginDuplicates is the plugin catalogue's duplicate review source.
type PluginDuplicates interface {
	DuplicateTitles(ctx context.Context, lib core.Library, offset, limit int) ([]PluginDuplicateGroup, int, error)
	RecordingTitles(ctx context.Context, key core.RatingKey) (core.Library, string, []core.Title, error)
}
