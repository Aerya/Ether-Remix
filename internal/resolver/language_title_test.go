package resolver

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Retry only a known trailing tag and retain the remaining identity gates.
func TestTrailingLanguageTitleGate(t *testing.T) {
	title := &core.Title{Type: core.TypeMovie, Title: "Terkel in Trouble", OriginalTitle: "Terkel i knibe", Year: 2004,
		AlternativeTitles: []string{"Terkel i knipe"}}
	for _, tc := range []struct {
		parsed string
		year   int
		reject bool
	}{
		{"Terkel I Knipe NORWEGIAN", 0, false},
		{"Terkel I Knipe NORSK", 2004, false},
		{"Terkel I Knibe DANISH", 2004, false},
		{"Terkel in Trouble ENGLISH", 2004, false},
		{"Terkel I Knipe NORWEGIAN", 2020, true},
		{"Another Movie NORWEGIAN", 2004, true},
		{"Terkel I Knipe UNKNOWN", 2004, true},
		{"Terkel I Knipe NORWEGIAN Story", 2004, true},
	} {
		t.Run(tc.parsed, func(t *testing.T) {
			release := core.Release{Name: strings.ReplaceAll(tc.parsed, " ", "."), Info: core.ReleaseInfo{Title: tc.parsed, Year: tc.year}}
			reason := rejectIdentity(release, title, searchRequestFor(title), "en-US")
			if (reason != "") != tc.reject {
				t.Fatalf("rejection=%q, want rejected=%v", reason, tc.reject)
			}
		})
	}
}
