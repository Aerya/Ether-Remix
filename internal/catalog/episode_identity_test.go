package catalog

import (
	"context"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	_ "github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestEpisodeIdentityIncludesGenresAndAnimeKeywords(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	animeShow := core.ShowKey(37854)
	liveShow := core.ShowKey(111110)
	if err := c.UpsertTitle(ctx, core.Title{
		Key:      animeShow,
		Type:     core.TypeShow,
		Title:    "One Piece",
		Year:     1999,
		Language: "ja",
		Genres:   []string{"Action & Adventure", "Animation"},
		Keywords: []string{"anime", "pirate"},
		IDs:      core.IDs{TMDB: 37854},
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertTitle(ctx, core.Title{
		Key:      liveShow,
		Type:     core.TypeShow,
		Title:    "One Piece",
		Year:     2023,
		Language: "en",
		Genres:   []string{"Action & Adventure", "Sci-Fi & Fantasy"},
		Keywords: []string{"live action", "pirate"},
		IDs:      core.IDs{TMDB: 111110},
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertTitle(ctx, core.Title{
		Key:     core.EpisodeKey(111110, 1, 1),
		Type:    core.TypeEpisode,
		ShowKey: liveShow,
		Season:  1,
		Episode: 1,
		Title:   "Romance Dawn",
	}, true); err != nil {
		t.Fatal(err)
	}

	got, err := c.EpisodeIdentity(ctx, animeShow, 1, 1, []string{"One Piece"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.Genres, "Animation") || !slices.Equal(got.Keywords, []string{"anime"}) {
		t.Fatalf("show genres=%v keywords=%v, want Animation and [anime]", got.Genres, got.Keywords)
	}
	others := got.OtherShows["One Piece"]
	if len(others) != 1 {
		t.Fatalf("OtherShows = %+v, want 1 namesake", got.OtherShows)
	}
	if others[0].Year != 2023 || others[0].EpisodeTitle != "Romance Dawn" || others[0].Language != "en" {
		t.Fatalf("namesake = %+v", others[0])
	}
	if !slices.Equal(others[0].Genres, []string{"Action & Adventure", "Sci-Fi & Fantasy"}) || len(others[0].Keywords) != 0 {
		t.Fatalf("namesake genres=%v keywords=%v", others[0].Genres, others[0].Keywords)
	}
}
