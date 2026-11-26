package personalize

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/layouts"
	"testing"
)

func TestTemplateRefreshesLiveHome(t *testing.T) {
	h := newHarness(t, nil)
	ctx := t.Context()
	s := layouts.New(h.db, h.e.LayoutTemplatePart())
	a := DefaultPage(core.PageHome)
	a.Rows = []core.Row{{Source: core.RowSource{Kind: KindTrending}, Placement: core.RowPlacement{Mode: core.PlacementShow}}}
	if saved, e := h.e.SavePage(ctx, a); e != nil {
		t.Fatal(e)
	} else {
		a = saved
	}
	if e := s.Save(ctx, "A", ""); e != nil {
		t.Fatal(e)
	}
	entries, _ := s.List(ctx)
	a.Rows[0].Placement.Mode = core.PlacementHide
	if _, e := h.e.SavePage(ctx, a); e != nil {
		t.Fatal(e)
	}
	if e := s.Apply(ctx, entries[0].ID); e != nil {
		t.Fatal(e)
	}
	got, _ := h.e.Page(ctx, core.PageHome)
	if got.Rows[0].Placement.Mode != core.PlacementShow {
		t.Fatal("live home retained stale layout")
	}
	if e := s.Apply(ctx, "_previous"); e != nil {
		t.Fatal(e)
	}
	got, _ = h.e.Page(ctx, core.PageHome)
	if got.Rows[0].Placement.Mode != core.PlacementHide {
		t.Fatal("undo failed")
	}
}

func TestLegacyTemplateConvertsToPages(t *testing.T) {
	h := newHarness(t, nil)
	tx, err := h.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw := []byte(`{"global":[{"section":"home","rows":[{"row":"continue","status":"off"}]}],"viewers":[]}`)
	if err := h.e.LayoutTemplatePart().Restore(t.Context(), tx, raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	h.e.LayoutTemplatePart().Changed(t.Context())
	p, ok := h.e.Page(t.Context(), core.PageHome)
	if !ok || len(p.Rows) != 1 || p.Rows[0].Placement.Mode != core.PlacementHide {
		t.Fatalf("legacy template lost: %+v", p)
	}
}

func TestLegacyTemplateRetainsCollectionRowsAndCurrentAudience(t *testing.T) {
	h := newHarness(t, nil)
	ctx := t.Context()
	if _, err := h.db.Exec(`CREATE TABLE IF NOT EXISTS collections_specs(id INTEGER PRIMARY KEY,slug TEXT NOT NULL DEFAULT '',spec_json TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`INSERT INTO collections_specs(id,spec_json) VALUES(42,'{"name":"Selection","mediaType":"movie","enabled":true,"users":[7]}')`); err != nil {
		t.Fatal(err)
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	part := h.e.LayoutTemplatePart()
	raw, err := part.PrepareRestore(ctx, tx, []byte(`{"global":[],"viewers":[]}`), map[string]json.RawMessage{
		"collections": []byte(`[{"ID":42,"Fields":{"enabled":true},"Targets":{"homeRow":true,"schedule":{"days":[6],"devices":["tv"]}}}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := part.Restore(ctx, tx, raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	part.Changed(ctx)
	page, _ := h.e.Page(ctx, core.PageHome)
	var found *core.Row
	for i := range page.Rows {
		if page.Rows[i].Source.Params["collection"] == "42" {
			found = &page.Rows[i]
		}
	}
	if found == nil || found.Placement.Audience.Mode != core.AudienceOnly || len(found.Placement.Audience.Viewers) != 1 || found.Placement.Audience.Viewers[0] != 7 || found.Placement.When == nil || len(found.Placement.When.Days) != 1 || found.Placement.When.Days[0] != 6 {
		t.Fatalf("legacy placement lost: %+v", found)
	}
}
