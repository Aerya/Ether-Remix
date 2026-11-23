package core

import (
	"slices"
	"strings"
	"testing"
)

func TestTitleSortKeyRulesAndCollation(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{`"Pimpernel" Smith`, `pimpernel" smith`},
		{`"Sr."`, `sr."`},
		{"#1 Cheerleader Camp", "0000000001 cheerleader camp"},
		{"#Alive", "alive"},
		{"(500) Days of Summer", "0000000500) days of summer"},
		{"!Three Amigos!", "three amigos!"},
		{"[REC]", "rec]"},
		{"The Matrix", "matrix"},
		{`"The Matrix"`, `matrix"`},
		{"The 'Burbs", "burbs"},
		{"A Clockwork Orange", "clockwork orange"},
		{"An American Werewolf", "american werewolf"},
		{"The 39 Steps", "0000000039 steps"},
		{"À Nous la Liberté", "a nous la liberte"},
		{"Été 85", "ete 0000000085"},
		{"Æon Flux", "aeon flux"},
		{"½ Revolution", "½ revolution"},
		{"...", "..."},
	}
	for _, tc := range cases {
		if got := TitleSortKey(tc.title); got != tc.want {
			t.Errorf("TitleSortKey(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}

	// Verify natural numeric order and punctuation-ignoring alphabetical order.
	titles := []string{
		"10 Cloverfield Lane",
		"2 Fast 2 Furious",
		"#1 Cheerleader Camp",
		"(500) Days of Summer",
		"1917",
		`"Sr."`,
		`"Pimpernel" Smith`,
		"#Alive",
		"The Matrix",
		"Saw 10",
		"Saw 2",
		"Alien",
	}
	slices.SortFunc(titles, func(a, b string) int {
		return strings.Compare(TitleSortKey(a), TitleSortKey(b))
	})
	wantOrder := []string{
		"#1 Cheerleader Camp",
		"2 Fast 2 Furious",
		"10 Cloverfield Lane",
		"(500) Days of Summer",
		"1917",
		"Alien",
		"#Alive",
		"The Matrix",
		`"Pimpernel" Smith`,
		"Saw 2",
		"Saw 10",
		`"Sr."`,
	}
	if !slices.Equal(titles, wantOrder) {
		t.Fatalf("sorted order = %q, want %q", titles, wantOrder)
	}
}

func TestEffectiveSortTitleAndBackfill(t *testing.T) {
	// Explicit source sortTitle is preserved (keeps leading article/punctuation) and folded + numeric-collated.
	if got := EffectiveSortTitle("The Matrix", "The Matrix 2"); got != "the matrix 0000000002" {
		t.Fatalf("EffectiveSortTitle explicit = %q", got)
	}
	if got := EffectiveSortTitle(`"Sr."`, ""); got != `sr."` {
		t.Fatalf("EffectiveSortTitle derived = %q", got)
	}
	// Backfill rewrites v0/v1 derived values while keeping genuine custom sort titles.
	if got := FoldedSortTitleForRow("#Alive", "#alive"); got != "alive" {
		t.Fatalf("backfill v1 punctuation = %q, want alive", got)
	}
	if got := FoldedSortTitleForRow("Été 85", "été 85"); got != "ete 0000000085" {
		t.Fatalf("backfill v0 accented = %q, want ete 0000000085", got)
	}
	if got := FoldedSortTitleForRow("Été 85", "ete 85"); got != "ete 0000000085" {
		t.Fatalf("backfill v1 numeric = %q, want ete 0000000085", got)
	}
	if got := FoldedSortTitleForRow("L'Éclisse", "ÉCLISSE"); got != "eclisse" {
		t.Fatalf("backfill custom sortTitle = %q, want eclisse", got)
	}
	// FoldSortTitle is idempotent on already-padded strings.
	first := TitleSortKey("2001: A Space Odyssey")
	if second := FoldSortTitle(first); second != first {
		t.Fatalf("FoldSortTitle not idempotent: %q -> %q", first, second)
	}
}
