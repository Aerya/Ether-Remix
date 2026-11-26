package core

import (
	"testing"
	"time"
)

func TestScheduleAllows(t *testing.T) {
	fri21 := time.Date(2026, 10, 16, 21, 30, 0, 0, time.UTC) // Friday, October, evening
	h := func(n int) *int { return &n }
	cases := []struct {
		sc   *Schedule
		at   time.Time
		want bool
	}{
		{nil, fri21, true},
		{&Schedule{Days: []int{5}}, fri21, true},
		{&Schedule{Days: []int{0, 6}}, fri21, false},
		{&Schedule{FromHour: h(17), ToHour: h(22)}, fri21, true},
		{&Schedule{FromHour: h(17), ToHour: h(21)}, fri21, false},
		{&Schedule{FromHour: h(22), ToHour: h(2)}, fri21, false},
		{&Schedule{FromHour: h(22), ToHour: h(2)}, fri21.Add(3 * time.Hour), true},
		{&Schedule{FromHour: h(9), ToHour: h(9)}, fri21, true},
		{&Schedule{Dayparts: []string{DaypartEvening}}, fri21, true},
		{&Schedule{Dayparts: []string{DaypartMorning, DaypartLate}}, fri21, false},
		// Late belongs to the evening it started on: Friday late at 1 AM Saturday.
		{&Schedule{Days: []int{5}, Dayparts: []string{DaypartLate}}, fri21.Add(3*time.Hour + 30*time.Minute), true},
		{&Schedule{Months: []int{10, 12}}, fri21, true},
		{&Schedule{Months: []int{12}}, fri21, false},
		{&Schedule{Dates: []DateRange{{From: "2026-10-16", To: "2026-10-16"}}}, fri21, true},
		{&Schedule{Dates: []DateRange{{From: "2026-10-17"}}}, fri21, false},
		{&Schedule{Dates: []DateRange{{From: "10-01", To: "10-31", Yearly: true}}}, fri21, true},
		{&Schedule{Dates: []DateRange{{From: "12-20", To: "01-06", Yearly: true}}}, time.Date(2027, 1, 3, 12, 0, 0, 0, time.UTC), true},
		{&Schedule{Dates: []DateRange{{From: "12-20", To: "01-06", Yearly: true}}}, fri21, false},
		{&Schedule{Holidays: []string{HolidayHalloween}}, fri21, true},
		{&Schedule{Holidays: []string{HolidayChristmas, HolidayValentines}}, fri21, false},
	}
	for i, c := range cases {
		if got := c.sc.Allows(c.at); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
	if !(*Schedule)(nil).IsZero() || !(&Schedule{}).IsZero() || (&Schedule{Months: []int{1}}).IsZero() {
		t.Fatal("IsZero")
	}
}

func TestScheduleValidate(t *testing.T) {
	bad := []*Schedule{
		{Days: []int{7}},
		{Dayparts: []string{"noon"}},
		{Months: []int{13}},
		{Dates: []DateRange{{From: "2026-13-01"}}},
		{Dates: []DateRange{{From: "2026-10-02", To: "2026-10-01"}}},
		{Dates: []DateRange{{From: "1-1", To: "01-02", Yearly: true}}},
		{Holidays: []string{"festivus"}},
	}
	for i, s := range bad {
		if s.Validate() == nil {
			t.Errorf("case %d: want an error", i)
		}
	}
	ok := &Schedule{Days: []int{5, 6}, Dayparts: []string{DaypartEvening}, Holidays: []string{HolidayDiwali}, Dates: []DateRange{{From: "12-01", To: "12-26", Yearly: true}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRowPageValidateStructure(t *testing.T) {
	good := RowPage{Key: PageHome, Mode: PagePersonalized, Picks: true, Rows: []Row{
		{ID: "rabcdefgh", Source: RowSource{Kind: "trending"}, Scope: RowScope{Libraries: []LibraryID{1, 2}, PerLibrary: true},
			Placement: RowPlacement{Mode: PlacementPinned, Position: 2, Audience: Audience{Mode: AudienceEveryone}}},
		{ID: "rbcdefghi", Source: RowSource{Kind: "collection", Params: map[string]string{"collection": "12"}},
			Placement: RowPlacement{Mode: PlacementShow, Audience: Audience{Mode: AudienceOnly, Viewers: []int64{4, 5}},
				When: &Schedule{Holidays: []string{HolidayChristmas}}}},
	}, Variants: []PageVariant{{Viewer: 4, Rows: []RowOverride{{Row: "rabcdefgh", Placement: &RowPlacement{Mode: PlacementHide}}}}}}
	if err := good.ValidateStructure(); err != nil {
		t.Fatal(err)
	}
	mutate := []func(p *RowPage){
		func(p *RowPage) { p.Key = "library:0/x" },
		func(p *RowPage) { p.Mode = "fixed" },
		func(p *RowPage) { p.Rows[1].ID = p.Rows[0].ID },
		func(p *RowPage) { p.Rows[0].Placement.Position = 0 },
		func(p *RowPage) { p.Rows[1].Placement.Audience.Viewers = nil },
		func(p *RowPage) { p.Rows[0].Look.Style = "carousel" },
		func(p *RowPage) { p.Variants[0].Rows[0].Row = "rzzzzzzzz" },
		func(p *RowPage) { p.Rows[0].Placement.Devices = []string{"fridge"} },
	}
	curated := good
	curated.Mode = PageCurated // pins and Auto rows survive a switch to Curated
	if err := curated.ValidateStructure(); err != nil {
		t.Fatalf("curated page with a pinned row: %v", err)
	}
	for i, m := range mutate {
		p := good
		p.Rows = append([]Row(nil), good.Rows...)
		p.Variants = []PageVariant{{Viewer: 4, Rows: append([]RowOverride(nil), good.Variants[0].Rows...)}}
		m(&p)
		if p.ValidateStructure() == nil {
			t.Errorf("mutation %d: want an error", i)
		}
	}
	if k := LibraryPage(7, "recommended"); k != "library:7/recommended" {
		t.Fatal(k)
	}
	if id, tab, ok := PageKey("library:7/home").Library(); !ok || id != 7 || tab != "home" {
		t.Fatal("Library()")
	}
	if !(Audience{Mode: AudienceExcept, Viewers: []int64{3}}).Includes(0) || (Audience{Mode: AudienceOnly, Viewers: []int64{3}}).Includes(0) {
		t.Fatal("Includes")
	}
}
