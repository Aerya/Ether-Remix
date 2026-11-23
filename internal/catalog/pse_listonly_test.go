package catalog_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/listonly"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// countingTMDB compte les requêtes qui atteignent le faux TMDB.
func countingTMDB(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	inner := fakeTMDB(t)
	u, _ := url.Parse(inner.URL)
	proxy := httputil.NewSingleHostReverseProxy(u)
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestListOnlyGetNeverCallsTMDB(t *testing.T) {
	srv, hits := countingTMDB(t)
	cat := newTestCatalogCfg(t, srv, config.Catalog{Language: "en-US", Region: "US", MinPopularity: 5, MinVotes: 100, RefreshInterval: config.Duration(time.Nanosecond)})
	ctx := listonly.With(context.Background())
	if !listonly.On(ctx) {
		t.Fatal("le marqueur n'est pas posé alors que le réglage est actif par défaut")
	}
	// Titre absent : introuvable, sans appel ni écriture.
	for _, key := range []core.RatingKey{core.MovieKey(550), core.ShowKey(100), core.SeasonKey(100, 1), core.EpisodeKey(100, 1, 1)} {
		if _, err := cat.Get(ctx, key); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("Get(%v) absent = %v, want ErrNotFound", key, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d appels TMDB pour des titres absents", n)
	}
	// Une ouverture de fiche (sans marqueur) complète la base ; la série est
	// ensuite périmée (RefreshInterval minimal) mais une liste la renvoie telle quelle.
	if _, err := cat.Get(context.Background(), core.ShowKey(100)); err != nil {
		t.Fatalf("ouverture de la fiche : %v", err)
	}
	if _, err := cat.Get(context.Background(), core.MovieKey(550)); err != nil {
		t.Fatalf("ouverture de la fiche : %v", err)
	}
	opened := hits.Load()
	if opened == 0 {
		t.Fatal("l'ouverture d'une fiche aurait dû appeler TMDB")
	}
	time.Sleep(20 * time.Millisecond)
	for _, key := range []core.RatingKey{core.ShowKey(100), core.MovieKey(550)} {
		got, err := cat.Get(ctx, key)
		if err != nil || got == nil || got.Title == "" {
			t.Fatalf("Get(%v) en liste = %v, %v", key, got, err)
		}
	}
	// Toutes les saisons et épisodes déjà en base, sans en compléter d'autres.
	if _, err := cat.Grandchildren(ctx, core.ShowKey(100)); err != nil {
		t.Fatalf("Grandchildren en liste : %v", err)
	}
	if n := hits.Load(); n != opened {
		t.Fatalf("la liste a fait %d appels TMDB de plus", n-opened)
	}
}

func TestListOnlyGrandchildrenDoesNotFetchSeasons(t *testing.T) {
	srv, hits := countingTMDB(t)
	cat := newTestCatalog(t, srv)
	if _, err := cat.Get(context.Background(), core.ShowKey(100)); err != nil {
		t.Fatal(err)
	}
	before := hits.Load()
	eps, err := cat.Grandchildren(listonly.With(context.Background()), core.ShowKey(100))
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 0 {
		t.Fatalf("épisodes jamais ouverts renvoyés : %d", len(eps))
	}
	if n := hits.Load(); n != before {
		t.Fatalf("Grandchildren en liste a fait %d appels TMDB", n-before)
	}
	// Ouverture normale : les saisons sont complétées.
	eps, err = cat.Grandchildren(context.Background(), core.ShowKey(100))
	if err != nil || len(eps) == 0 {
		t.Fatalf("Grandchildren normal = %d épisodes, %v", len(eps), err)
	}
}

func TestListOnlyOffKeepsEtherBehaviour(t *testing.T) {
	defer pseenv.Replace(map[string]string{listonly.Key: "off"})()
	srv, hits := countingTMDB(t)
	cat := newTestCatalog(t, srv)
	ctx := listonly.With(context.Background())
	if listonly.On(ctx) {
		t.Fatal("le marqueur est posé alors que le réglage est off")
	}
	if _, err := cat.Get(ctx, core.MovieKey(550)); err != nil {
		t.Fatalf("Get avec réglage off : %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("réglage off : le comportement d'Ether (appel TMDB) est attendu")
	}
}
