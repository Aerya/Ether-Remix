package core

import "testing"

func TestNormalizePinsKeepsPositionsUnique(t *testing.T) {
	pinned := func(id string, at int, a Audience) Row {
		return Row{ID: id, Placement: RowPlacement{Mode: PlacementPinned, Position: at, Audience: a}}
	}
	everyone := Audience{Mode: AudienceEveryone}
	luca := Audience{Mode: AudienceOnly, Viewers: []int64{7}}
	// The owner's sequence left three rows pinned to #1 and one to #2.
	p := RowPage{Rows: []Row{
		pinned("aaaaaaaa", 1, everyone),
		pinned("bbbbbbbb", 1, everyone),
		pinned("cccccccc", 1, everyone),
		pinned("dddddddd", 2, everyone),
		pinned("eeeeeeee", 1, luca), // shows with a different audience: may share #1
	}, Variants: []PageVariant{{Viewer: 7, Rows: []RowOverride{
		{Row: "aaaaaaaa", Placement: &RowPlacement{Mode: PlacementPinned, Position: 3, Audience: everyone}},
		{Row: "bbbbbbbb", Placement: &RowPlacement{Mode: PlacementPinned, Position: 3, Audience: everyone}},
	}}}}
	p.NormalizePins()
	want := map[string]int{"aaaaaaaa": 1, "bbbbbbbb": 2, "cccccccc": 3, "dddddddd": 4, "eeeeeeee": 1}
	for _, r := range p.Rows {
		if r.Placement.Position != want[r.ID] {
			t.Errorf("%s pinned to #%d, want #%d", r.ID, r.Placement.Position, want[r.ID])
		}
	}
	if a, b := p.Variants[0].Rows[0].Placement.Position, p.Variants[0].Rows[1].Placement.Position; a != 3 || b != 4 {
		t.Errorf("viewer pins = %d, %d, want 3, 4", a, b)
	}
}
