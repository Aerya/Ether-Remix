package releaseparse

import "testing"

func TestTitlesMatchPossessiveCredit(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Naked World History", "Seol Min-seok's Naked World History", true},
		{"Seol Min-seok's Naked World History", "Naked World History", true},
		{"설민석의 벌거벗은 세계사", "벌거벗은 세계사", true},
		{"Jack Ryan", "Tom Clancy's Jack Ryan", true},
		{"House of Payne", "Tyler Perry's House of Payne", true},
		{"The Nightmare Before Christmas", "Tim Burton's The Nightmare Before Christmas", true},
		// The possessive belongs to the name itself.
		{"Anatomy", "Grey's Anatomy", false},
		{"Kitchen", "Hell's Kitchen", false},
		{"Tale", "The Handmaid's Tale", false},
		{"Day Off", "Ferris Bueller's Day Off", false},
		{"Kitchen Nightmares", "Ramsay's Kitchen Nightmares", false},
		{"불시착", "사랑의 불시착", false},
		{"해방일지", "나의 해방일지", false},
		{"하우스헬퍼", "당신의 하우스헬퍼", false},
		// The stem still has to be the same title.
		{"World History", "Seol Min-seok's Naked World History", false},
		{"Naked Attraction", "Seol Min-seok's Naked World History", false},
	}
	for _, tc := range cases {
		if got := TitlesMatch(tc.a, tc.b); got != tc.want {
			t.Errorf("TitlesMatch(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	aliases := TitleAliases("Seol Min-seok's Naked World History")
	found := false
	for _, a := range aliases {
		found = found || a == "Naked World History"
	}
	if !found {
		t.Errorf("TitleAliases did not search the uncredited title; got %v", aliases)
	}
}
