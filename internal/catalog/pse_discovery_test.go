package catalog_test

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/frenchcat"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// Un film sorti aux États-Unis mais pas encore en France ne doit apparaître
// ni dans les rangées de l'accueil ni dans les collections.
func TestPSEDiscoveryHidesTitlesNotOutInFrance(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	client := tmdb.New("testkey", slog.Default(), tmdb.WithBaseURL(fakeTMDB(t).URL), tmdb.WithCacheTTL(0))
	t.Cleanup(client.Close)
	cfg := config.Catalog{Language: "en-US", Region: "US", MinPopularity: 5, MinVotes: 100, RefreshInterval: config.Duration(24 * time.Hour)}
	c, err := catalog.New(db, client, cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	outInFrance, usOnly := core.MovieKey(550), core.MovieKey(551)
	items := make([]core.Title, 0, 3)
	for _, k := range []core.RatingKey{outInFrance, usOnly} {
		title, err := c.Get(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, *title)
	}
	keys := []core.RatingKey{usOnly, outInFrance}

	// Sans règle Remix, rien ne change.
	all := map[core.MediaType]bool{core.TypeMovie: true, core.TypeShow: true}
	t.Cleanup(frenchcat.Override(frenchcat.Settings{Types: all}))
	if got := c.PSEDiscovery(ctx, items); len(got) != 2 {
		t.Fatalf("sans règle : %v", got)
	}
	if got, err := c.PSEDiscoveryKeys(ctx, keys); err != nil || !slices.Equal(got, keys) {
		t.Fatalf("sans règle : %v, %v", got, err)
	}

	frenchcat.Override(frenchcat.Settings{Enabled: true, Countries: []string{"FR"}, Releases: []string{"french", "multi"}, Types: all})
	past, future := time.Now().Add(-48*time.Hour).Unix(), time.Now().Add(48*time.Hour).Unix()
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_home_release_dates(key,country,kind,released_ts) VALUES(?,?,4,?),(?,?,4,?),(?,?,5,?)`,
		int64(outInFrance), "FR", past, int64(usOnly), "US", past, int64(usOnly), "FR", future); err != nil {
		t.Fatal(err)
	}

	got := c.PSEDiscovery(ctx, items)
	if len(got) != 1 || got[0].Key != outInFrance {
		t.Fatalf("rangées : %v", got)
	}
	gotKeys, err := c.PSEDiscoveryKeys(ctx, keys)
	if err != nil || !slices.Equal(gotKeys, []core.RatingKey{outInFrance}) {
		t.Fatalf("collections : %v, %v", gotKeys, err)
	}

	// Une saison ou un épisode n'est jamais jugé ici : la reprise de lecture
	// d'une série ne doit pas disparaître.
	season := core.Title{Key: core.RatingKey(999), Type: core.TypeSeason}
	if got := c.PSEDiscovery(ctx, []core.Title{season}); len(got) != 1 {
		t.Fatalf("saison filtrée : %v", got)
	}
}
