package experience

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type recapLibraries struct {
	fakePluginLibraries
	views map[core.RatingKey]PluginItemView
}

func (r *recapLibraries) ItemsMany(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]PluginItemView, error) {
	out := map[core.RatingKey]PluginItemView{}
	for _, k := range keys {
		if v, ok := r.views[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func TestRecapPeriodBounds(t *testing.T) {
	loc := time.UTC
	at := time.Date(2026, 11, 26, 15, 0, 0, 0, loc) // a Thursday
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, loc)
	from, to, _, _ := recapPeriod("week", at, now, loc)
	if from != time.Date(2026, 11, 23, 0, 0, 0, 0, loc) || to != time.Date(2026, 11, 30, 0, 0, 0, 0, loc) {
		t.Fatalf("week %v..%v", from, to)
	}
	from, to, _, _ = recapPeriod("month", at, now, loc)
	if from.Day() != 1 || to != time.Date(2026, 12, 1, 0, 0, 0, 0, loc) {
		t.Fatalf("month %v..%v", from, to)
	}
	from, _, _, _ = recapPeriod("year", at, now, loc)
	if from != time.Date(2026, 1, 1, 0, 0, 0, 0, loc) {
		t.Fatalf("year from %v", from)
	}
	if _, _, _, ok := recapPeriod("decade", at, now, loc); ok {
		t.Fatal("unknown period accepted")
	}
}

func TestRecapStreaks(t *testing.T) {
	today := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	days := map[string]float64{"2026-11-20": 1, "2026-11-21": 1, "2026-11-22": 1, "2026-11-25": 1, "2026-11-26": 1}
	cur, longest := recapStreaks(days, today)
	if cur != 2 || longest != 3 {
		t.Fatalf("current %d longest %d (no play yet today counts back from yesterday)", cur, longest)
	}
}

// A recap counts one viewer's completed plays of visible items: minutes from
// runtimes, ranked tracks, albums (parents) and artists (artist ancestors).
func TestHistoryRecapAggregatesPlays(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 4, AllowedLibraries: []core.LibraryID{9}}
	lib := core.Library{ID: 9, Key: "plugin:music:music", Name: "Music", Source: core.LibraryPlugin, PluginID: "music.plugin", KindKey: "music"}
	artist, album := core.PluginKey(10), core.PluginKey(20)
	track := func(seq int64, title string, mins int) PluginItemView {
		return PluginItemView{Library: lib, Title: core.Title{Key: core.PluginKey(seq), Title: title, Type: core.TypeMovie, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack,
			LibraryID: 9, ParentKey: album, Runtime: time.Duration(mins) * time.Minute,
			MediaParents: []core.TitleParent{{Key: album, Title: "Discovery", Role: core.MediaRoleAlbum}, {Key: artist, Title: "Daft Punk", Role: core.MediaRoleArtist}}}}
	}
	views := map[core.RatingKey]PluginItemView{
		core.PluginKey(1): track(1, "One More Time", 5),
		core.PluginKey(2): track(2, "Digital Love", 4),
		album:             {Library: lib, Title: core.Title{Key: album, Title: "Discovery", MediaRole: core.MediaRoleAlbum, LibraryID: 9}},
		artist:            {Library: lib, Title: core.Title{Key: artist, Title: "Daft Punk", MediaRole: core.MediaRoleArtist, LibraryID: 9}},
	}
	day := func(d int) time.Time { return time.Date(2026, 11, d, 20, 0, 0, 0, time.UTC) }
	plays := []core.HistoryPlay{{Key: core.PluginKey(1), At: day(24)}, {Key: core.PluginKey(1), At: day(25)}, {Key: core.PluginKey(2), At: day(25)}, {Key: core.PluginKey(99), At: day(25)}}
	svc := &Service{d: ServiceDeps{
		Active:          func(context.Context, int64) (*core.User, error) { return viewer, nil },
		PluginLibraries: &recapLibraries{fakePluginLibraries: fakePluginLibraries{libs: []core.Library{lib}}, views: views},
		HistoryPlays: func(_ context.Context, user int64, from, to time.Time, _ int) ([]core.HistoryPlay, error) {
			if user != 4 {
				t.Fatalf("user %d", user)
			}
			var out []core.HistoryPlay
			for _, p := range plays {
				if !p.At.Before(from) && p.At.Before(to) {
					out = append(out, p)
				}
			}
			return out, nil
		},
	}}
	r, err := svc.HistoryRecap(ctx, &core.Viewer{ID: 4}, frontend.RecapQuery{Period: "week", At: day(25)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Plays != 3 || r.Minutes != 14 || r.DistinctTracks != 2 || r.DistinctArtists != 1 {
		t.Fatalf("totals: plays %d minutes %v tracks %d artists %d", r.Plays, r.Minutes, r.DistinctTracks, r.DistinctArtists)
	}
	if len(r.TopTracks) != 2 || r.TopTracks[0].Title.Title != "One More Time" || r.TopTracks[0].Plays != 2 {
		t.Fatalf("top tracks: %+v", r.TopTracks)
	}
	if len(r.TopAlbums) != 1 || r.TopAlbums[0].Title.Title != "Discovery" || len(r.TopArtists) != 1 || r.TopArtists[0].Title.Title != "Daft Punk" || r.TopArtists[0].Plays != 3 {
		t.Fatalf("albums %+v artists %+v", r.TopAlbums, r.TopArtists)
	}
	if len(r.ByDay) == 0 || r.StreakLongest != 2 {
		t.Fatalf("days %d longest %d", len(r.ByDay), r.StreakLongest)
	}
}
