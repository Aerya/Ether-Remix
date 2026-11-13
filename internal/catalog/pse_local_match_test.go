package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
	"testing"
	"time"
)

func TestLocalMatchingReusesCanonicalTitleAndRejectsAmbiguity(t *testing.T) {
	db := store.OpenTest(t)
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	title := core.Title{Key: core.MovieKey(42), Type: core.TypeMovie, IDs: core.IDs{TMDB: 42}, Title: "Un film", Year: 2020, Released: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := c.UpsertDetailedTitle(ctx, title); err != nil {
		t.Fatal(err)
	}
	file := localmedia.File{Path: "Un.Film.2020.1080p.MULTi.mkv"}
	if key := c.MatchLocalVideo(ctx, file); key != title.Key {
		t.Fatalf("match=%v want %v", key, title.Key)
	}
	if key := c.MatchLocalVideo(ctx, localmedia.File{Path: "Mes vacances.mp4"}); key != 0 {
		t.Fatal("personal video incorrectly matched", key)
	}
	if key := c.MatchLocalVideo(ctx, localmedia.File{Path: "Un.Film.2021.mkv"}); key != 0 {
		t.Fatal("wrong year accepted", key)
	}
	title.Key = core.MovieKey(43)
	title.IDs.TMDB = 43
	if err := c.UpsertDetailedTitle(ctx, title); err != nil {
		t.Fatal(err)
	}
	if key := c.MatchLocalVideo(ctx, file); key != 0 {
		t.Fatal("ambiguous title accepted", key)
	}
}

func TestLocalMatchingFallsBackToTMDB(t *testing.T) {
	db := store.OpenTest(t)
	u := &universeTMDB{movies: []uMovie{{id: 42, date: "2020-01-01", pop: 10, votes: 100}}}
	remote := &liveSearchTMDB{universeTMDB: u, hit: &tmdb.Movie{ID: 42, Title: "Movie 42", ReleaseDate: "2020-01-01"}}
	c, err := New(db, remote, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := localmedia.File{Path: "Movie.42.2020.mkv"}
	if key := c.MatchLocalVideo(t.Context(), f); key != core.MovieKey(42) {
		t.Fatal(key)
	}
	remote.set(nil, true)
	if key := c.MatchLocalVideo(t.Context(), f); key != core.MovieKey(42) {
		t.Fatal("stored match required remote", key)
	}
}

func TestLocalMatchingEpisodeKeepsShowSeasonAndEpisode(t *testing.T) {
	db := store.OpenTest(t)
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []core.Title{
		{Key: core.ShowKey(70), Type: core.TypeShow, IDs: core.IDs{TMDB: 70}, Title: "My Series", Year: 2020},
		{Key: core.SeasonKey(70, 1), Type: core.TypeSeason, ShowKey: core.ShowKey(70), Season: 1, Title: "Season 1"},
		{Key: core.EpisodeKey(70, 1, 2), Type: core.TypeEpisode, ShowKey: core.ShowKey(70), Season: 1, Episode: 2, Title: "Second episode"},
	} {
		title.Released = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := c.UpsertDetailedTitle(t.Context(), title); err != nil {
			t.Fatal(err)
		}
	}
	if key := c.MatchLocalVideo(t.Context(), localmedia.File{Path: "My.Series.S01E02.1080p.mkv"}); key != core.EpisodeKey(70, 1, 2) {
		t.Fatal(key)
	}
}
