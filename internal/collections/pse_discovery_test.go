package collections_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
)

// Les pages de collections appliquent le filtre Remix. L'indexeur, lui,
// continue de voir tous les titres : sinon il ne chercherait jamais ceux
// qui ne sont pas encore lisibles.
func TestPSEDiscoveryFiltersCollectionPagesOnly(t *testing.T) {
	var calls atomic.Int32
	dropFirst := func(_ context.Context, keys []core.RatingKey) ([]core.RatingKey, error) {
		calls.Add(1)
		if len(keys) == 0 {
			return keys, nil
		}
		return keys[1:], nil
	}
	e := newEnv(t, withOpts(collections.WithPSEDiscovery(dropFirst)))
	created, err := e.svc.CreateSpec(e.ctx, spec("Filtrée", "movie", catalogSource()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Rebuild(e.ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	raw := e.svc.PSEKeys(e.ctx, created.ID, nil)
	if len(raw) < 2 {
		t.Fatalf("collection trop petite pour le test : %v", raw)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("PSEKeys ne doit pas filtrer : %d appels", n)
	}
	list, err := e.svc.List(e.ctx, nil, core.TypeMovie)
	if err != nil {
		t.Fatal(err)
	}
	var key core.RatingKey
	for _, c := range list {
		if c.Title == "Filtrée" {
			key = c.Key
		}
	}
	if key == 0 {
		t.Fatalf("collection absente : %v", list)
	}
	page, err := e.svc.Items(e.ctx, nil, key, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != len(raw)-1 {
		t.Fatalf("page : %d titres, attendu %d", page.Total, len(raw)-1)
	}
	for _, it := range page.Items {
		if it.Key == raw[0] {
			t.Fatalf("titre filtré encore présent : %v", it.Key)
		}
	}
}

// Une collection de sorties à venir montre par nature des titres pas encore
// disponibles.
func TestPSEDiscoverySkipsUpcomingCollections(t *testing.T) {
	var calls atomic.Int32
	e := newEnv(t, withOpts(collections.WithPSEDiscovery(func(_ context.Context, keys []core.RatingKey) ([]core.RatingKey, error) {
		calls.Add(1)
		return nil, nil
	})))
	sp := spec("À venir", "movie", catalogSource())
	sp.ReleaseMode, sp.LookaheadDays = "upcoming", 30
	created, err := e.svc.CreateSpec(e.ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Rebuild(e.ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.List(e.ctx, nil, core.TypeMovie); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("collection à venir filtrée : %d appels", n)
	}
}
