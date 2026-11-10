package themes

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type concertEditor struct {
	*fakeCols
	updates int
	fail    bool
}

func (e *concertEditor) UpdateSpec(ctx context.Context, id string, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	e.updates++
	if e.fail {
		return nil, errors.New("write failed")
	}
	return e.fakeCols.UpdateSpec(ctx, id, sp)
}

func legacyConcertSpec() core.CollectionSpec {
	sp := PresetSpecs("concerts")[0]
	sp.Sources[0].Params = maps.Clone(sp.Sources[0].Params)
	sp.Sources[0].Params["with_keywords"] = legacyConcertKeywords
	delete(sp.Sources[0].Params, "with_genres")
	delete(sp.Sources[0].Params, "include_video")
	return sp
}

func TestUpgradeConcertCollections(t *testing.T) {
	s, cols := newTest(t)
	ctx := context.Background()
	for _, current := range PresetSpecs("concerts") {
		old := legacyConcertSpec()
		old.Sources[0].Params["sort_by"] = current.Sources[0].Params["sort_by"]
		if v := current.Sources[0].Params["vote_count.gte"]; v != "" {
			old.Sources[0].Params["vote_count.gte"] = v
		}
		old.Name = "Ma sélection renommée"
		old.Enabled = false
		old.Limit = 17
		if _, err := cols.CreateSpec(ctx, old); err != nil {
			t.Fatal(err)
		}
	}
	ed := &concertEditor{fakeCols: cols, fail: true}
	if err := s.UpgradeConcertCollections(ctx, ed); err == nil {
		t.Fatal("erreur ignorée")
	}
	ed.fail, ed.updates = false, 0
	if err := s.UpgradeConcertCollections(ctx, ed); err != nil {
		t.Fatal(err)
	}
	if ed.updates != 3 {
		t.Fatalf("mises à jour : %d", ed.updates)
	}
	for i, expected := range PresetSpecs("concerts") {
		got := cols.specs[i+2]
		if !maps.Equal(got.Sources[0].Params, expected.Sources[0].Params) || got.Name != "Ma sélection renommée" || got.Enabled || got.Limit != 17 {
			t.Fatalf("réglages : %+v", got)
		}
	}
	if err := s.UpgradeConcertCollections(ctx, ed); err != nil {
		t.Fatal(err)
	}
	if ed.updates != 3 {
		t.Fatal("migration non idempotente")
	}
}

func TestUpgradeConcertCollectionsPreservesCustomQueries(t *testing.T) {
	for _, change := range []func(*core.CollectionSpec){
		func(sp *core.CollectionSpec) { sp.Description = "Personnelle" },
		func(sp *core.CollectionSpec) { sp.OwnerUserID = 42 },
		func(sp *core.CollectionSpec) { sp.MediaType = TypeShow },
		func(sp *core.CollectionSpec) { sp.Sources[0].Params["with_genres"] = "99" },
		func(sp *core.CollectionSpec) { sp.Sources[0].Params["with_keywords"] = "6029" },
		func(sp *core.CollectionSpec) { sp.Sources = append(sp.Sources, core.CollectionSource{Kind: "titles"}) },
	} {
		s, cols := newTest(t)
		sp := legacyConcertSpec()
		change(&sp)
		saved, _ := cols.CreateSpec(context.Background(), sp)
		ed := &concertEditor{fakeCols: cols}
		if err := s.UpgradeConcertCollections(context.Background(), ed); err != nil {
			t.Fatal(err)
		}
		if ed.updates != 0 || !reflect.DeepEqual(*saved, cols.specs[2]) {
			t.Fatal("collection personnalisée modifiée")
		}
	}
}

func TestConcertPresetRequiresRecordedMusic(t *testing.T) {
	for _, sp := range PresetSpecs("concerts") {
		p := sp.Sources[0].Params
		if p["with_keywords"] != "156205|304615|318730" || p["with_genres"] != "10402" || p["include_video"] != "true" {
			t.Fatalf("filtre de captation : %v", p)
		}
	}
}
