package personalize

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/layouts"
	"testing"
)

func TestTemplateRefreshesLiveHome(t *testing.T) {
	h := newHarness(t, nil)
	ctx := t.Context()
	s := layouts.New(h.db, h.e.LayoutTemplatePart())
	a := core.HomeLayout{Global: []core.LayoutPage{{Section: "home", Rows: []core.RowPolicy{{Row: KindTrending, Status: "always", Slot: 1}}}}}
	if _, e := h.e.SetHomeLayout(ctx, a); e != nil {
		t.Fatal(e)
	}
	if e := s.Save(ctx, "A", ""); e != nil {
		t.Fatal(e)
	}
	entries, _ := s.List(ctx)
	a.Global[0].Rows[0].Status = "off"
	if _, e := h.e.SetHomeLayout(ctx, a); e != nil {
		t.Fatal(e)
	}
	if e := s.Apply(ctx, entries[0].ID); e != nil {
		t.Fatal(e)
	}
	got, _ := h.e.HomeLayout(ctx)
	if got.Global[0].Rows[0].Status != "always" {
		t.Fatal("live home retained stale layout")
	}
	if e := s.Apply(ctx, "_previous"); e != nil {
		t.Fatal(e)
	}
	got, _ = h.e.HomeLayout(ctx)
	if got.Global[0].Rows[0].Status != "off" {
		t.Fatal("undo failed")
	}
}
