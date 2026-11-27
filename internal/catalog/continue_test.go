package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type continueTMDB struct {
	TMDB
	t *testing.T
}

func (f *continueTMDB) SeasonDetails(context.Context, int64, int) (*tmdb.SeasonDetails, error) {
	f.t.Error("Continue Episodes accessed the metadata provider")
	return nil, core.ErrUnsupported
}

// Continue Watching reads the local catalogue and excludes announced episodes.
func TestContinueUsesStoredEpisodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		season int
		air    string
		want   bool
	}{
		{"same season", 1, "2025-01-01", true},
		{"season boundary", 2, "2025-01-01", true},
		{"unaired", 1, "2027-01-01", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			db := store.OpenTest(t)
			c, err := New(db, &continueTMDB{t: t}, config.Catalog{}, nil, WithNow(func() time.Time { return now }))
			if err != nil {
				t.Fatal(err)
			}
			show := core.Title{Key: core.ShowKey(100), Type: core.TypeShow, Title: "Show", Status: "Returning Series", Released: now.AddDate(-1, 0, 0)}
			if err := c.upsert(ctx, show, upsertOpts{detail: true}); err != nil {
				t.Fatal(err)
			}
			for season := 1; season <= tc.season; season++ {
				title := core.Title{Key: core.SeasonKey(100, season), Type: core.TypeSeason, ShowKey: show.Key, Season: season, Released: show.Released}
				if err := c.upsert(ctx, title, upsertOpts{detail: season == 1}); err != nil {
					t.Fatal(err)
				}
			}
			prior := core.EpisodeKey(100, 1, 1)
			if err := c.upsert(ctx, core.Title{Key: prior, Type: core.TypeEpisode, ShowKey: show.Key, Season: 1, Episode: 1, Released: show.Released}, upsertOpts{}); err != nil {
				t.Fatal(err)
			}
			got, err := c.ContinueEpisodes(ctx, []core.RatingKey{prior})
			if err != nil || len(got) != 0 {
				t.Fatalf("cold read: %v %v", got, err)
			}
			number := 2
			if tc.season == 2 {
				number = 1
			}
			next := core.Title{Key: core.EpisodeKey(100, tc.season, number), Type: core.TypeEpisode, ShowKey: show.Key,
				Season: tc.season, Episode: number, Released: metadataDate(tc.air)}
			if err := c.upsert(ctx, next, upsertOpts{detail: true}); err != nil {
				t.Fatal(err)
			}
			got, err = c.ContinueEpisodes(ctx, []core.RatingKey{prior})
			if err != nil {
				t.Fatal(err)
			}
			if (got[prior] != nil) != tc.want {
				t.Fatalf("after hydration: %v, want episode=%v", got, tc.want)
			}
		})
	}
}
