package personalize

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestFilteredRowReasonNamesTheFilterThatEmptiedTheRow(t *testing.T) {
	movie := candItem{t: core.Title{Key: 1, Type: core.TypeMovie}}
	cases := []struct {
		name    string
		row     candRow
		section core.MediaType
		pinned  bool
		code    string
	}{
		{"history", candRow{minItems: 5, filtered: &rowFilterStats{movie: filterCounts{history: 30, language: 4}}}, "", false, "filtered_by_history"},
		{"language", candRow{minItems: 5, filtered: &rowFilterStats{show: filterCounts{history: 2, language: 9}}}, "", false, "filtered_by_language"},
		{"notable", candRow{minItems: 5, filtered: &rowFilterStats{movie: filterCounts{notable: 3}}}, "", false, "filtered_by_popularity"},
		{"too few left", candRow{minItems: 5, items: []candItem{movie}, filtered: &rowFilterStats{movie: filterCounts{history: 8}}}, "", false, "below_minimum"},
		{"per page: TV half is language", candRow{minItems: 5, items: []candItem{movie}, filtered: &rowFilterStats{movie: filterCounts{history: 8}, show: filterCounts{language: 6}}}, core.TypeShow, false, "filtered_by_language"},
		{"pinned row with a title is not short", candRow{minItems: 5, items: []candItem{movie}, filtered: &rowFilterStats{movie: filterCounts{history: 8}}}, "", true, ""},
		{"full row", candRow{minItems: 1, items: []candItem{movie}, filtered: &rowFilterStats{movie: filterCounts{history: 8}}}, "", false, ""},
		{"nothing filtered", candRow{minItems: 5, filtered: &rowFilterStats{}}, "", false, ""},
		{"no counts", candRow{minItems: 5}, "", false, ""},
	}
	for _, tc := range cases {
		code, reason, ok := filteredRowReason(&tc.row, tc.section, tc.pinned)
		if code != tc.code || ok != (tc.code != "") || ok && reason == "" {
			t.Errorf("%s: got (%q, %q, %v), want code %q", tc.name, code, reason, ok, tc.code)
		}
	}
}
