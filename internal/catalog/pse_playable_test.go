package catalog_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func TestPSEPlayableOnlyCatalogue(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	client := tmdb.New("testkey", slog.Default(), tmdb.WithBaseURL(fakeTMDB(t).URL), tmdb.WithCacheTTL(0))
	t.Cleanup(client.Close)
	c, err := catalog.New(db, client, config.Catalog{Language: "en-US", Region: "US", MinPopularity: 5, MinVotes: 100, RefreshInterval: config.Duration(24 * time.Hour)}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	fjord, family := core.MovieKey(550), core.MovieKey(551)
	for _, k := range []core.RatingKey{fjord, family} {
		if _, err := c.Get(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	movies := func(f core.Filter) []core.RatingKey {
		t.Helper()
		f.Type = core.TypeMovie
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: f, Sort: core.SortPopularity, Desc: true})
		if err != nil {
			t.Fatal(err)
		}
		var keys []core.RatingKey
		for _, it := range page.Items {
			keys = append(keys, it.Key)
		}
		if page.Total != len(keys) {
			t.Fatalf("total %d pour %d titres", page.Total, len(keys))
		}
		return keys
	}
	if got := movies(core.Filter{}); len(got) != 2 {
		t.Fatalf("sans PSE : %v", got)
	}

	// Une nouvelle instance de catalogue par cas : le cache des pages ne
	// connaît pas les réglages PSE, qui ne changent pas en production.
	fresh := func() {
		var err error
		c, err = catalog.New(db, client, config.Catalog{Language: "en-US", Region: "US", MinPopularity: 5, MinVotes: 100, RefreshInterval: config.Duration(24 * time.Hour)}, slog.Default())
		if err != nil {
			t.Fatal(err)
		}
	}
	st := releasedb.NewStore(db)
	now := time.Now()
	title := core.Title{Key: fjord, Type: core.TypeMovie}
	rel := func(name string, langs ...string) releasedb.Release {
		return releasedb.FromCore(title, 0, 0, "aio", core.Release{ID: name, Name: name, Protocol: "http", Info: core.ReleaseInfo{Height: 1080, Languages: langs}}, []string{"alldebrid"})
	}
	if err := st.Save(ctx, []releasedb.Release{
		rel("Fjord.Harbor.1999.MULTi.1080p.WEB.x264-A", "french"),
		rel("Fjord.Harbor.1999.1080p.BluRay.x264-B", "english"),
	}, now); err != nil {
		t.Fatal(err)
	}
	// Fjord indexé avec 2 releases, Family indexé sans résultat.
	if _, err := db.ExecContext(ctx, `INSERT INTO pse_index_titles(title_key,media_type,last_indexed,next_due,found,status) VALUES(?,?,?,?,2,'found'),(?,?,?,?,0,'none')`,
		int64(fjord), "movie", now.Unix(), now.Unix(), int64(family), "movie", now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}

	p := releasedb.NewPlayable(2, "", false)
	restore := releasedb.OverridePlayable(p)
	t.Cleanup(restore)
	if n, err := st.SyncPlayable(ctx, p); err != nil || n != 1 {
		t.Fatalf("SyncPlayable = %d, %v", n, err)
	}
	fresh()
	if got := movies(core.Filter{}); len(got) != 1 || got[0] != fjord {
		t.Fatalf("lisibles (N=2) : %v", got)
	}
	// Une liste par clé (watchlist…) n'est pas filtrée.
	if got := movies(core.Filter{IncludeKeys: []core.RatingKey{family}}); len(got) != 1 || got[0] != family {
		t.Fatalf("liste par clé : %v", got)
	}

	// Filtre français : une seule release compte, sous le seuil de 2.
	p = releasedb.NewPlayable(2, "lang=fr", false)
	releasedb.OverridePlayable(p)
	if n, err := st.SyncPlayable(ctx, p); err != nil || n != 1 {
		t.Fatalf("recalcul après changement de filtre = %d, %v", n, err)
	}
	if n, _ := st.SyncPlayable(ctx, p); n != 0 {
		t.Fatalf("filtre inchangé : %d titres recalculés", n)
	}
	fresh()
	if got := movies(core.Filter{}); len(got) != 0 {
		t.Fatalf("FR, N=2 : %v", got)
	}
	// Exception de langue (concert, sélection en VOSTFR) : toutes ses
	// releases comptent, quelle que soit leur langue, mais seulement dans
	// sa bibliothèque « toutes langues ». Films et Séries restent en VF.
	if err := st.SetLangExempt(ctx, []core.RatingKey{fjord}); err != nil {
		t.Fatal(err)
	}
	fresh()
	if got := movies(core.Filter{}); len(got) != 0 {
		t.Fatalf("exception de langue visible dans Films : %v", got)
	}
	if got := movies(core.Filter{IncludeKeys: []core.RatingKey{fjord, family}, PSEThemed: true}); len(got) != 0 {
		t.Fatalf("exception de langue dans une bibliothèque à thème en VF : %v", got)
	}
	if got := movies(core.Filter{IncludeKeys: []core.RatingKey{fjord, family}, PSEThemed: true, PSELangFree: true}); len(got) != 1 || got[0] != fjord {
		t.Fatalf("exception de langue dans sa bibliothèque : %v", got)
	}
	if err := st.SetLangExempt(ctx, nil); err != nil {
		t.Fatal(err)
	}
	fresh()
	if got := movies(core.Filter{}); len(got) != 0 {
		t.Fatalf("exception retirée : %v", got)
	}
	// L'indexeur voit tout le catalogue, sinon rien ne deviendrait lisible.
	if got := movies(core.Filter{PSEUnplayable: true}); len(got) != 2 {
		t.Fatalf("parcours de l'indexeur : %v", got)
	}
	releasedb.OverridePlayable(releasedb.NewPlayable(1, "lang=fr", false))
	fresh()
	if got := movies(core.Filter{}); len(got) != 1 || got[0] != fjord {
		t.Fatalf("FR, N=1 : %v", got)
	}

	// Les listes d'épisodes et de saisons passent par la clé de la série.
	for _, typ := range []core.MediaType{core.TypeEpisode, core.TypeSeason, core.TypeShow} {
		if _, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: typ}}); err != nil {
			t.Fatalf("browse %s : %v", typ, err)
		}
	}

	// Titres pas encore indexés visibles avec PSE_PLAYABLE_PENDING=show.
	if _, err := db.ExecContext(ctx, `DELETE FROM pse_index_titles WHERE title_key=?`, int64(family)); err != nil {
		t.Fatal(err)
	}
	releasedb.OverridePlayable(releasedb.NewPlayable(1, "lang=fr", true))
	fresh()
	if got := movies(core.Filter{}); len(got) != 2 {
		t.Fatalf("avec titres en attente : %v", got)
	}
	releasedb.OverridePlayable(releasedb.NewPlayable(1, "lang=fr", false))
	fresh()
	if got := movies(core.Filter{}); len(got) != 1 {
		t.Fatalf("sans titres en attente : %v", got)
	}
}
