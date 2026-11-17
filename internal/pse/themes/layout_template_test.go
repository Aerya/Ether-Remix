package themes

import (
	_ "github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/layouts"
	"testing"
)

func TestTemplateKeepsCurrentLibraryAudience(t *testing.T) {
	s, _ := newTest(t)
	ctx := t.Context()
	s.db.Exec(`INSERT INTO collections_specs(id,spec_json) VALUES(1,'{}'),(2,'{}')`)
	l, e := s.Create(ctx, Library{Name: "A", Type: TypeMovie, Collections: []string{"1", "2"}})
	if e != nil {
		t.Fatal(e)
	}
	templates := layouts.New(s.db, s.LayoutTemplatePart())
	if e = templates.Save(ctx, "A", ""); e != nil {
		t.Fatal(e)
	}
	entries, _ := templates.List(ctx)
	l.Name = "B"
	l.Collections = []string{"2"}
	p := core.UserTargets{IDs: []int64{7}}
	l.Users = &p
	if _, e = s.Update(ctx, l); e != nil {
		t.Fatal(e)
	}
	if e = templates.Apply(ctx, entries[0].ID); e != nil {
		t.Fatal(e)
	}
	got := s.Libraries()[0]
	if got.Name != "A" || len(got.Collections) != 2 || got.Users.Includes(&core.User{ID: 8}) {
		t.Fatalf("%+v", got)
	}
}
