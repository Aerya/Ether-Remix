package app

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/ratings"
	"github.com/braedonsaunders/ether/internal/store"
)

// Ratings are cache-only, retain provider identity/scales, and obey the owner's
// enable switch without confusing the single Plex slot choices with Vela's list.
func TestCachedRatingProjection(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Now()
	service := ratings.New(db, ratings.Options{Now: func() time.Time { return now }})
	cfg := ratings.Settings{Enabled: true, Provider: "omdb", APIKey: "offline-key", Critic: "tomatoes", Audience: "imdb", CacheHours: 1}
	if err := service.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	title := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie}
	raw := `[{"source":"imdb","value":8.2,"scale":10,"votes":123},{"source":"tomatoes","value":60,"scale":100},{"source":"popcorn","value":59,"scale":100},{"source":"metacritic","value":0,"scale":100},{"source":"letterboxd","value":4.1,"scale":5}]`
	if _, err := db.ExecContext(ctx, `INSERT INTO ratings_cache(item_key,provider,value,updated) VALUES(?,?,?,?)`, title.Key, "omdb", raw, now.Unix()); err != nil {
		t.Fatal(err)
	}
	s := &surfaceExperience{detail: detailPersonalization{ratings: service}}
	viewer := &core.Viewer{ID: 1}
	got := s.PageCachedRatings(ctx, viewer, []core.Title{title})[title.Key]
	if len(got) != 5 || got[0].Value != 8.2 || got[0].Votes != 123 || got[1].State != "fresh" || got[2].State != "spilled" || got[3].Value != 0 || got[4].Scale != 5 {
		t.Fatal(got)
	}
	cfg.Enabled = false
	if err := service.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if got := s.PageCachedRatings(ctx, viewer, []core.Title{title}); len(got) != 0 {
		t.Fatal("disabled ratings leaked", got)
	}
	cfg.Enabled = true
	if err := service.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if got := s.PageCachedRatings(ctx, viewer, []core.Title{title}); len(got[title.Key]) != 0 {
		t.Fatal("expired ratings leaked", got)
	}
}
