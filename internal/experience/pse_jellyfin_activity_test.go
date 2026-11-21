package experience

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type activityThemes struct {
	themes []frontend.PSETheme
	rows   map[string][]core.RatingKey
}

func (f activityThemes) Themes() []frontend.PSETheme { return f.themes }

func (f activityThemes) RowKeys(_ context.Context, row string, _ *core.User) []core.RatingKey {
	return f.rows[row]
}

func jellyfinThemeActivityFixture(t *testing.T) (*Service, *core.User) {
	t.Helper()
	viewer := &core.User{ID: 1, AllowedLibraries: []core.LibraryID{5100, 5101, 5102}, MaxContentRating: "PG"}
	movie := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, ContentRating: "PG"}
	otherTheme := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie, ContentRating: "G"}
	outOfTheme := core.Title{Key: core.MovieKey(3), Type: core.TypeMovie, ContentRating: "G"}
	adult := core.Title{Key: core.MovieKey(4), Type: core.TypeMovie, Adult: true}
	show := core.Title{Key: core.ShowKey(5), Type: core.TypeShow, ContentRating: "TV-G"}
	episode := core.Title{Key: core.EpisodeKey(5, 1, 1), Type: core.TypeEpisode, ShowKey: show.Key, ContentRating: "TV-G"}
	hiddenShow := core.Title{Key: core.ShowKey(6), Type: core.TypeShow, ContentRating: "TV-MA"}
	hiddenEpisode := core.Title{Key: core.EpisodeKey(6, 1, 1), Type: core.TypeEpisode, ShowKey: hiddenShow.Key, ContentRating: "TV-G"}
	frontend.SetPSEThemes(activityThemes{
		themes: []frontend.PSETheme{
			{Section: 5101, Title: "Animation", Type: core.TypeMovie, Rows: []frontend.PSEThemeRow{{ID: "films", Title: "Films"}}},
			{Section: 5201, Title: "Animation", Type: core.TypeShow, Rows: []frontend.PSEThemeRow{{ID: "series", Title: "Séries"}}},
			{Section: 5102, Title: "Documentaires", Type: core.TypeMovie, Rows: []frontend.PSEThemeRow{{ID: "docs", Title: "Documentaires"}}},
			{Section: 5103, Title: "Autres", Type: core.TypeMovie, Rows: []frontend.PSEThemeRow{{ID: "other", Title: "Autres"}}},
		},
		rows: map[string][]core.RatingKey{
			"films": {movie.Key, adult.Key}, "series": {show.Key, hiddenShow.Key},
			"docs": {otherTheme.Key}, "other": {outOfTheme.Key},
		},
	})
	t.Cleanup(func() { frontend.SetPSEThemes(nil) })

	titles := map[core.RatingKey]core.Title{}
	for _, title := range []core.Title{movie, otherTheme, outOfTheme, adult, show, episode, hiddenShow, hiddenEpisode} {
		titles[title.Key] = title
	}
	get := func(_ context.Context, key core.RatingKey) (*core.Title, error) {
		if title, ok := titles[key]; ok {
			return &title, nil
		}
		return nil, core.ErrNotFound
	}
	libs := []Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}}
	s := &Service{d: ServiceDeps{
		Protocol: "jellyfin", Libraries: libs, Visibility: BindVisibility(nil, get, nil, nil, libs),
		Active:    func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Get:       func(ctx context.Context, _ *core.User, key core.RatingKey) (*core.Title, error) { return get(ctx, key) },
		CachedGet: get,
		State: activityState{statuses: []core.WatchStatus{
			{Key: adult.Key}, {Key: hiddenEpisode.Key}, {Key: outOfTheme.Key}, {Key: otherTheme.Key},
			{Key: movie.Key, ViewCount: 1}, {Key: episode.Key},
		}},
		Grandchildren: func(_ context.Context, _ *core.User, key core.RatingKey) ([]core.Title, error) {
			if key == show.Key {
				return []core.Title{episode}, nil
			}
			return []core.Title{hiddenEpisode}, nil
		},
		AiredEpisodes: func(_ context.Context, key core.RatingKey, _, _ int) (core.Page, error) {
			if key == show.Key {
				return core.Page{Items: []core.Title{episode}, Total: 1}, nil
			}
			return core.Page{Items: []core.Title{hiddenEpisode}, Total: 1}, nil
		},
		AddedTimes: func(context.Context, []core.RatingKey) (map[core.RatingKey]time.Time, error) {
			return map[core.RatingKey]time.Time{
				movie.Key: time.Unix(10, 0), show.Key: time.Unix(20, 0), episode.Key: time.Unix(30, 0),
				adult.Key: time.Unix(40, 0), hiddenEpisode.Key: time.Unix(50, 0),
				otherTheme.Key: time.Unix(60, 0), outOfTheme.Key: time.Unix(70, 0),
			}, nil
		},
	}}
	return s, viewer
}

func TestPSEJellyfinResumeFiltersThemeBeforePagination(t *testing.T) {
	s, viewer := jellyfinThemeActivityFixture(t)
	ctx := context.Background()
	movie, episode := core.MovieKey(1), core.EpisodeKey(5, 1, 1)
	global, err := s.Resume(ctx, viewer, core.Device{}, "", nil, 0, 10, true)
	if err != nil || !global.TotalExact || global.Total != 3 || len(global.Items) != 3 || global.Items[0].Key != core.MovieKey(2) {
		t.Fatalf("global resume %+v, error %v", global, err)
	}
	for _, test := range []struct {
		offset int
		key    core.RatingKey
		more   bool
	}{{0, movie, true}, {1, episode, false}} {
		page, err := s.Resume(ctx, viewer, core.Device{}, "5101", nil, test.offset, 1, true)
		if err != nil || !page.TotalExact || page.Total != 2 || page.More != test.more || len(page.Items) != 1 || page.Items[0].Key != test.key {
			t.Fatalf("themed resume offset %d: %+v, error %v", test.offset, page, err)
		}
	}
	if _, err := s.Resume(ctx, viewer, core.Device{}, "5103", nil, 0, 10, true); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unauthorized theme resume: %v", err)
	}
	if _, err := s.Resume(ctx, viewer, core.Device{}, "1", nil, 0, 10, true); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unauthorized base resume: %v", err)
	}
	s.d.Protocol = "emby"
	unchanged, err := s.Resume(ctx, viewer, core.Device{}, "", nil, 0, 10, true)
	if err != nil || !unchanged.TotalExact || len(unchanged.Items) != 0 {
		t.Fatalf("Emby resume %+v, error %v", unchanged, err)
	}
}

func TestPSEJellyfinLatestUsesOnlyScopedVisibleMembers(t *testing.T) {
	s, viewer := jellyfinThemeActivityFixture(t)
	for _, test := range []struct {
		name  string
		ctx   context.Context
		group bool
		keys  []core.RatingKey
	}{
		{"grouped", context.Background(), true, []core.RatingKey{core.ShowKey(5), core.MovieKey(1)}},
		{"ungrouped unplayed", frontend.WithLatestPlayed(context.Background(), false), false, []core.RatingKey{core.EpisodeKey(5, 1, 1)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			items, err := s.LatestAddedGrouped(test.ctx, viewer, core.Device{}, "5101", 10, test.group)
			keys := make([]core.RatingKey, 0, len(items))
			for _, item := range items {
				keys = append(keys, item.Title.Key)
			}
			if err != nil || !slices.Equal(keys, test.keys) {
				t.Fatalf("latest keys %v, want %v (error %v)", keys, test.keys, err)
			}
		})
	}
	if _, err := s.LatestAdded(context.Background(), viewer, core.Device{}, "5103", 10); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unauthorized theme latest: %v", err)
	}
}
