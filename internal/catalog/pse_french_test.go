package catalog_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/frenchcat"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func TestPSEFrenchCatalogue(t *testing.T) {
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
	fjord, family, space := core.MovieKey(550), core.MovieKey(551), core.ShowKey(100)
	for _, k := range []core.RatingKey{fjord, family, space} {
		if _, err := c.Get(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	browse := func(typ core.MediaType) []core.RatingKey {
		t.Helper()
		var err error
		c, err = catalog.New(db, client, cfg, slog.Default())
		if err != nil {
			t.Fatal(err)
		}
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: typ}, Sort: core.SortPopularity, Desc: true})
		if err != nil {
			t.Fatalf("browse %q : %v", typ, err)
		}
		var keys []core.RatingKey
		for _, it := range page.Items {
			keys = append(keys, it.Key)
		}
		return keys
	}
	if got := browse(core.TypeMovie); len(got) != 2 {
		t.Fatalf("sans PSE : %v", got)
	}
	if got := browse(core.TypeShow); len(got) != 1 {
		t.Fatalf("séries sans PSE : %v", got)
	}

	all := map[core.MediaType]bool{core.TypeMovie: true, core.TypeShow: true}
	t.Cleanup(frenchcat.Override(frenchcat.Settings{Enabled: true, Countries: []string{"FR"}, Releases: []string{"french", "multi"}, Types: all}))
	if got := browse(core.TypeMovie); len(got) != 0 {
		t.Fatalf("aucun critère vérifié : %v", got)
	}

	// Fjord : sortie numérique en France passée. Une sortie américaine ou
	// une sortie française future ne comptent pas.
	past, future := time.Now().Add(-48*time.Hour).Unix(), time.Now().Add(48*time.Hour).Unix()
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_home_release_dates(key,country,kind,released_ts) VALUES(?,?,4,?),(?,?,4,?),(?,?,5,?)`,
		int64(fjord), "FR", past, int64(family), "US", past, int64(family), "FR", future); err != nil {
		t.Fatal(err)
	}
	if got := browse(core.TypeMovie); len(got) != 1 || got[0] != fjord {
		t.Fatalf("sortie FR : %v", got)
	}
	// Pays configurable : avec CA seul, Fjord disparaît.
	frenchcat.Override(frenchcat.Settings{Enabled: true, Countries: []string{"CA"}, Releases: []string{"french", "multi"}, Types: all})
	if got := browse(core.TypeMovie); len(got) != 0 {
		t.Fatalf("pays CA : %v", got)
	}

	// Family : release MULTi indexée. La série : langue originale française.
	st := releasedb.NewStore(db)
	ftitle := core.Title{Key: family, Type: core.TypeMovie}
	if err := st.Save(ctx, []releasedb.Release{releasedb.FromCore(ftitle, 0, 0, "aio", core.Release{ID: "m", Name: "Family.Fun.2020.MULTi.1080p.WEB.x264-GRP", Protocol: "http"}, nil)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := browse(core.TypeMovie); len(got) != 1 || got[0] != family {
		t.Fatalf("release MULTi : %v", got)
	}
	frenchcat.Override(frenchcat.Settings{Enabled: true, Countries: []string{"FR"}, Releases: []string{"vfq"}, Types: all})
	if got := browse(core.TypeMovie); len(got) != 1 || got[0] != fjord {
		t.Fatalf("VFQ seule : %v", got)
	}
	if got := browse(core.TypeShow); len(got) != 0 {
		t.Fatalf("série anglaise : %v", got)
	}
	if _, err := db.ExecContext(ctx, `UPDATE catalog_titles SET language='fr' WHERE key=?`, int64(space)); err != nil {
		t.Fatal(err)
	}
	if got := browse(core.TypeShow); len(got) != 1 {
		t.Fatalf("série française : %v", got)
	}
	for _, typ := range []core.MediaType{core.TypeEpisode, core.TypeSeason, ""} {
		browse(typ)
	}
	// Films seulement : les séries ne sont plus filtrées.
	if _, err := db.ExecContext(ctx, `UPDATE catalog_titles SET language='en' WHERE key=?`, int64(space)); err != nil {
		t.Fatal(err)
	}
	frenchcat.Override(frenchcat.Settings{Enabled: true, Countries: []string{"FR"}, Releases: []string{"french"}, Types: map[core.MediaType]bool{core.TypeMovie: true}})
	if got := browse(core.TypeShow); len(got) != 1 {
		t.Fatalf("séries non filtrées : %v", got)
	}
	if got := browse(""); len(got) != 2 {
		t.Fatalf("liste mélangée (Fjord + série) : %v", got)
	}
}
