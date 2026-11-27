package collections

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/layouts"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
)

func TestLayoutTemplatePreservesSourcesAndAudience(t *testing.T) {
	db := store.OpenTest(t)
	svc := &Service{db: db}
	ctx := t.Context()
	original := core.CollectionSpec{Name: "A", Sources: []core.CollectionSource{{Kind: "titles", Keys: []string{"1"}}}, BrowseInApps: true, Enabled: true}
	b, _ := json.Marshal(original)
	if _, e := db.Exec(`INSERT INTO collections_specs(id,spec_json) VALUES(1,?)`, string(b)); e != nil {
		t.Fatal(e)
	}
	p := svc.LayoutTemplatePart()
	p.Changed = nil
	s := layouts.New(db, p)
	if e := s.Save(ctx, "A", ""); e != nil {
		t.Fatal(e)
	}
	entries, _ := s.List(ctx)
	original.Name = "B"
	original.Sources[0].Keys = []string{"2"}
	original.Users = &core.UserTargets{IDs: []int64{9}}
	original.BrowseInApps = false
	b, _ = json.Marshal(original)
	db.Exec(`UPDATE collections_specs SET spec_json=? WHERE id=1`, string(b))
	if e := s.Apply(ctx, entries[0].ID); e != nil {
		t.Fatal(e)
	}
	var raw string
	db.QueryRow(`SELECT spec_json FROM collections_specs WHERE id=1`).Scan(&raw)
	var got core.CollectionSpec
	json.Unmarshal([]byte(raw), &got)
	if got.Name != "A" || !got.BrowseInApps || got.Users.Includes(&core.User{ID: 8}) || !got.Users.Includes(&core.User{ID: 9}) || got.Sources[0].Keys[0] != "2" {
		t.Fatalf("unsafe restore %+v", got)
	}
	db.Exec(`DELETE FROM collections_specs WHERE id=1`)
	if e := s.Apply(ctx, entries[0].ID); e == nil {
		t.Fatal("missing collection recreated")
	}
}
