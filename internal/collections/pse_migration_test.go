package collections

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
)

func TestLegacyPlacementPreservesCollectionAudience(t *testing.T) {
	db := store.OpenTest(t)
	raw := `{"name":"Private","enabled":true,"mediaType":"movie","targets":{"collectionsTab":true,"homeRow":true,"users":[7]}}`
	if _, err := db.Exec(`INSERT INTO collections_specs(id,spec_json,enabled) VALUES(1,?,1)`, raw); err != nil {
		t.Fatal(err)
	}
	s := &Service{db: db}
	got, err := s.TakeLegacyPlacements(t.Context())
	if err != nil || len(got) != 1 {
		t.Fatalf("placements: %v %v", got, err)
	}
	if err := db.QueryRow(`SELECT spec_json FROM collections_specs WHERE id=1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var sp core.CollectionSpec
	if err := json.Unmarshal([]byte(raw), &sp); err != nil {
		t.Fatal(err)
	}
	row := &specRow{enabled: true, spec: sp}
	if !sp.BrowseInApps || !visible(row, &core.User{ID: 7}) || visible(row, &core.User{ID: 8}) || visible(row, nil) {
		t.Fatalf("audience lost: %+v", sp)
	}
}
