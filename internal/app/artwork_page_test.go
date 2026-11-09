package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/overlays"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestArtworkPageBatchesEpisodesAndKeepsViewerState(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	cat, err := catalog.New(db, nil, config.Catalog{}, log)
	if err != nil {
		t.Fatal(err)
	}
	cols, err := collections.New(db, cat, log)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{catalog: cat, collections: cols, state: state.New(db, state.Options{}), overlays: overlays.New(db, overlays.Options{})}
	if err := a.overlays.SaveSettings(ctx, overlays.Settings{Enabled: true, Status: true}); err != nil {
		t.Fatal(err)
	}
	titles := []core.Title{}
	for id := int64(1); id <= 32; id++ {
		titles = append(titles, core.Title{Key: core.ShowKey(id), Type: core.TypeShow})
		for ep := 1; ep <= 2; ep++ {
			ti := core.Title{Key: core.EpisodeKey(id, 1, ep), ShowKey: core.ShowKey(id), Type: core.TypeEpisode, Episode: ep, Season: 1, Title: "Episode", Released: time.Now().Add(-time.Hour)}
			if err := cat.UpsertTitle(ctx, ti, true); err != nil {
				t.Fatal(err)
			}
			if id == 1 {
				if err := a.state.Scrobble(ctx, 1, ti.Key); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, user := range []*core.User{{ID: 1}, {ID: 2}} {
		request, stats := store.WithQueryStats(reqmemo.With(ctx))
		page := a.prepareArtworkPage(request, user, titles)
		for _, ti := range titles {
			got := a.overlayFacts(page, user, &ti, true, false)
			want := !(user.ID == 1 && ti.Key == core.ShowKey(1))
			if got.NewEpisode != want || got.NewSeason != want {
				t.Fatalf("viewer %d key %v: %+v", user.ID, ti.Key, got)
			}
		}
		t.Logf("32 shows and 64 episodes: %d queries", stats.Queries())
		if stats.Queries() > 5 {
			t.Fatalf("per-series reads returned: %d", stats.Queries())
		}
	}
	// A new page sees the newly watched episode; no cross-request cache.
	if err := a.state.ScrobbleMany(ctx, 2, []core.RatingKey{core.EpisodeKey(1, 1, 1), core.EpisodeKey(1, 1, 2)}); err != nil {
		t.Fatal(err)
	}
	user := &core.User{ID: 2}
	got := a.overlayFacts(a.prepareArtworkPage(reqmemo.With(ctx), user, titles), user, &titles[0], true, false)
	if got.NewEpisode {
		t.Fatal("watch update not visible on next page")
	}
}
