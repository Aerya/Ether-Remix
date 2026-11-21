package experience

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// latestFixture: shows 10 and 11 are in the theme, 12 is not. Show N has
// episodes N/1/1 (oldest) to N/1/5 (newest).
func latestFixture(t *testing.T, viewed []core.RatingKey) (*Service, *core.User, *[]string) {
	t.Helper()
	viewer := &core.User{ID: 1, AllowedLibraries: []core.LibraryID{5100, 5101}}
	titles := map[core.RatingKey]core.Title{}
	episodes := map[core.RatingKey][]core.Title{}
	added := map[core.RatingKey]time.Time{}
	for _, n := range []int64{10, 11, 12} {
		show := core.Title{Key: core.ShowKey(n), Type: core.TypeShow, ContentRating: "TV-G"}
		titles[show.Key] = show
		added[show.Key] = time.Unix(1, 0)
		for e := 5; e >= 1; e-- {
			ep := core.Title{Key: core.EpisodeKey(n, 1, e), Type: core.TypeEpisode, ShowKey: show.Key, ContentRating: "TV-G", Season: 1, Episode: e}
			titles[ep.Key] = ep
			episodes[show.Key] = append(episodes[show.Key], ep)
			added[ep.Key] = time.Unix(int64(100*e)+n, 0)
		}
	}
	frontend.SetPSEThemes(activityThemes{
		themes: []frontend.PSETheme{{Section: 5101, Title: "Séries", Type: core.TypeShow, Rows: []frontend.PSEThemeRow{{ID: "s", Title: "Séries"}}}},
		rows:   map[string][]core.RatingKey{"s": {core.ShowKey(10), core.ShowKey(11)}},
	})
	t.Cleanup(func() { frontend.SetPSEThemes(nil) })
	get := func(_ context.Context, key core.RatingKey) (*core.Title, error) {
		if title, ok := titles[key]; ok {
			return &title, nil
		}
		return nil, core.ErrNotFound
	}
	page := func(show core.RatingKey, offset, limit int) core.Page {
		all := episodes[show]
		return core.Page{Items: slices.Clone(all[min(offset, len(all)):min(offset+limit, len(all))]), Total: len(all)}
	}
	var calls []string
	var statuses []core.WatchStatus
	for _, key := range viewed {
		statuses = append(statuses, core.WatchStatus{Key: key, ViewCount: 1})
	}
	libs := []Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}}
	s := &Service{d: ServiceDeps{
		Protocol: "jellyfin", Libraries: libs, Visibility: BindVisibility(nil, get, nil, nil, libs),
		Active:    func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Get:       func(ctx context.Context, _ *core.User, key core.RatingKey) (*core.Title, error) { return get(ctx, key) },
		CachedGet: get,
		State:     activityState{statuses: statuses},
		AiredEpisodes: func(_ context.Context, show core.RatingKey, offset, limit int) (core.Page, error) {
			if show.TMDB() == 12 {
				t.Errorf("series outside the theme read")
			}
			calls = append(calls, "one")
			return page(show, offset, limit), nil
		},
		AddedTimes: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]time.Time, error) {
			out := map[core.RatingKey]time.Time{}
			for _, key := range keys {
				out[key] = added[key]
			}
			return out, nil
		},
	}}
	return s, viewer, &calls
}

func latestKeys(items []frontend.LatestItem) []core.RatingKey {
	var keys []core.RatingKey
	for _, item := range items {
		keys = append(keys, item.Title.Key)
	}
	return keys
}

func TestPSEJellyfinLatestEpisodesBatchSeriesAndSkipOthers(t *testing.T) {
	s, viewer, _ := latestFixture(t, nil)
	many := 0
	s.d.AiredEpisodesMany = func(_ context.Context, shows []core.RatingKey, limit int) (map[core.RatingKey]core.Page, error) {
		many++
		if slices.Contains(shows, core.ShowKey(12)) {
			t.Errorf("series outside the theme read: %v", shows)
		}
		out := map[core.RatingKey]core.Page{}
		for _, show := range shows {
			out[show] = core.Page{Items: []core.Title{
				{Key: core.EpisodeKey(show.TMDB(), 1, 5), Type: core.TypeEpisode, ShowKey: show, ContentRating: "TV-G"},
				{Key: core.EpisodeKey(show.TMDB(), 1, 4), Type: core.TypeEpisode, ShowKey: show, ContentRating: "TV-G"},
			}, Total: 5}
		}
		return out, nil
	}
	ctx := frontend.WithLatestPlayed(context.Background(), false)
	items, err := s.LatestAddedGrouped(ctx, viewer, core.Device{}, "5101", 3, false)
	want := []core.RatingKey{core.EpisodeKey(11, 1, 5), core.EpisodeKey(10, 1, 5), core.EpisodeKey(11, 1, 4)}
	if err != nil || many != 1 || !slices.Equal(latestKeys(items), want) {
		t.Fatalf("latest %v, want %v (batched reads %d, error %v)", latestKeys(items), want, many, err)
	}
}

func TestPSEJellyfinLatestEpisodesReadPastViewedOnes(t *testing.T) {
	viewed := []core.RatingKey{core.EpisodeKey(10, 1, 5), core.EpisodeKey(10, 1, 4), core.EpisodeKey(10, 1, 3)}
	s, viewer, calls := latestFixture(t, viewed)
	// A single series: the unviewed episodes lie beyond the first page of 4.
	frontend.SetPSEThemes(activityThemes{
		themes: []frontend.PSETheme{{Section: 5101, Title: "Séries", Type: core.TypeShow, Rows: []frontend.PSEThemeRow{{ID: "s", Title: "Séries"}}}},
		rows:   map[string][]core.RatingKey{"s": {core.ShowKey(10)}},
	})
	ctx := frontend.WithLatestPlayed(context.Background(), false)
	items, err := s.LatestAddedGrouped(ctx, viewer, core.Device{}, "5101", 2, false)
	want := []core.RatingKey{core.EpisodeKey(10, 1, 2), core.EpisodeKey(10, 1, 1)}
	if err != nil || !slices.Equal(latestKeys(items), want) {
		t.Fatalf("latest %v, want %v (error %v)", latestKeys(items), want, err)
	}
	if len(*calls) < 2 {
		t.Fatalf("series read once only (%d reads)", len(*calls))
	}
	// Played episodes instead: found at once.
	ctx = frontend.WithLatestPlayed(context.Background(), true)
	items, err = s.LatestAddedGrouped(ctx, viewer, core.Device{}, "5101", 2, false)
	if err != nil || !slices.Equal(latestKeys(items), []core.RatingKey{core.EpisodeKey(10, 1, 5), core.EpisodeKey(10, 1, 4)}) {
		t.Fatalf("played latest %v (error %v)", latestKeys(items), err)
	}
}
