package collections_test

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/themes"
)

// Les collections des modèles de bibliothèques à thème de PSE passent la
// validation d'Ether, apparaissent dans l'onglet Collections, et PSEKeys
// sert aussi une collection masquée partout.
func TestPSEThemePresetsValidate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, p := range themes.Presets() {
		specs := themes.PresetSpecs(p.ID)
		if len(specs) == 0 {
			t.Fatalf("modèle %s vide", p.ID)
		}
		for _, sp := range specs {
			got, err := e.svc.CreateSpec(ctx, sp)
			if err != nil {
				t.Fatalf("%s / %s : %v", p.ID, sp.Name, err)
			}
			if !got.Targets.CollectionsTab || got.Targets.HomeRow || got.Targets.SectionRow {
				t.Fatalf("%s / %s publiée : %+v", p.ID, sp.Name, got.Targets)
			}
			_ = e.svc.PSEKeys(ctx, got.ID, nil)
		}
	}
	hidden := themes.PresetSpecs("services")[0]
	hidden.Name, hidden.Sources = "Tout le catalogue", []core.CollectionSource{{Kind: "catalog"}}
	hidden.Targets.CollectionsTab = false
	got, err := e.svc.CreateSpec(ctx, hidden)
	if err != nil {
		t.Fatal(err)
	}
	if keys := e.svc.PSEKeys(ctx, got.ID, nil); len(keys) == 0 {
		t.Fatal("collection masquée : aucun titre servi")
	}
	if keys := e.svc.PSEKeys(ctx, "999999", nil); keys != nil {
		t.Fatalf("collection inconnue : %v", keys)
	}
}
