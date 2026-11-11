package music

import (
	"context"
	"sort"
	"strconv"
	"time"
)

type ListeningBucket struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Plays int    `json:"plays"`
}
type ListeningCell struct {
	Weekday int `json:"weekday"`
	Hour    int `json:"hour"`
	Plays   int `json:"plays"`
}
type ListeningSummary struct {
	Plays       int               `json:"plays"`
	Tracks      int               `json:"tracks"`
	Artists     int               `json:"artists"`
	Listeners   int               `json:"listeners"`
	Daily       []ListeningBucket `json:"daily"`
	Titles      []ListeningBucket `json:"titles"`
	ArtistRanks []ListeningBucket `json:"artistsRanked"`
	Albums      []ListeningBucket `json:"albums"`
	Users       []ListeningBucket `json:"users"`
	Heatmap     []ListeningCell   `json:"heatmap"`
}

// Listening aggregates the entire filtered period, independently of history pagination.
// Counts are reported listens; track length is not measured listening time.
func (l *Library) Listening(ctx context.Context, from, to time.Time, viewer int64, tz int) (ListeningSummary, error) {
	out := ListeningSummary{Daily: []ListeningBucket{}, Heatmap: []ListeningCell{}}
	where := " WHERE played_at>=? AND played_at<?"
	args := []any{from.Unix(), to.Unix()}
	if viewer > 0 {
		where += " AND viewer_id=?"
		args = append(args, viewer)
	}
	rows, err := l.db.QueryContext(ctx, "SELECT viewer_id,title,artist,album,played_at FROM pse_music_plays"+where, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	loc := time.FixedZone("browser", min(840, max(-840, tz))*60)
	daily := map[string]*ListeningBucket{}
	titles := map[string]*ListeningBucket{}
	artists := map[string]*ListeningBucket{}
	albums := map[string]*ListeningBucket{}
	users := map[string]*ListeningBucket{}
	var heat [7][24]int
	add := func(m map[string]*ListeningBucket, key, label string) {
		if m[key] == nil {
			m[key] = &ListeningBucket{Key: key, Label: label}
		}
		m[key].Plays++
	}
	for rows.Next() {
		var id, at int64
		var title, artist, album string
		if err := rows.Scan(&id, &title, &artist, &album, &at); err != nil {
			return out, err
		}
		out.Plays++
		date := time.Unix(at, 0).In(loc)
		day := date.Format("2006-01-02")
		add(daily, day, day)
		heat[int(date.Weekday())][date.Hour()]++
		// Include artist and album in identity so homonymous tracks are not collapsed.
		add(titles, artist+"\x00"+album+"\x00"+title, title+" · "+artist)
		if artist != "" {
			add(artists, artist, artist)
		}
		if album != "" {
			add(albums, artist+"\x00"+album, album+" · "+artist)
		}
		key := strconv.FormatInt(id, 10)
		add(users, key, key)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Tracks = len(titles)
	out.Artists = len(artists)
	out.Listeners = len(users)
	rank := func(m map[string]*ListeningBucket) []ListeningBucket {
		a := []ListeningBucket{}
		for _, b := range m {
			a = append(a, *b)
		}
		sort.Slice(a, func(i, j int) bool {
			if a[i].Plays == a[j].Plays {
				return a[i].Key < a[j].Key
			}
			return a[i].Plays > a[j].Plays
		})
		return a[:min(10, len(a))]
	}
	out.Titles = rank(titles)
	out.ArtistRanks = rank(artists)
	out.Albums = rank(albums)
	out.Users = rank(users)
	for d := time.Date(from.In(loc).Year(), from.In(loc).Month(), from.In(loc).Day(), 0, 0, 0, 0, loc); d.Before(to); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		b := ListeningBucket{Key: key, Label: key}
		if daily[key] != nil {
			b = *daily[key]
		}
		out.Daily = append(out.Daily, b)
	}
	for day := 0; day < 7; day++ {
		for hour := 0; hour < 24; hour++ {
			out.Heatmap = append(out.Heatmap, ListeningCell{day, hour, heat[day][hour]})
		}
	}
	return out, nil
}
