package releaseparse

import "testing"

// Titles that differ only in word joining match (#519); different words,
// extra words and sequel numbers still do not.
func TestTitlesMatchJoinedSpellings(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"K-Pop Demon Hunters", "KPop Demon Hunters", true},
		{"JoJo s Bizarre Adventure", "JoJo's Bizarre Adventure", true},
		{"Spiderman Homecoming", "Spider-Man: Homecoming", true},
		{"X-Men", "XMen", true},
		{"WALL-E", "WALLE", true},
		{"Spider-Man 2", "Spiderman 3", false},
		{"X-Men", "X-Men Origins", false},
		{"Blade Runner", "Blade Runner 2049", false},
		{"Up", "U P S", false},
	} {
		if got := TitlesMatch(c.a, c.b); got != c.want {
			t.Errorf("TitlesMatch(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	r := Parse("K-Pop.Demon.Hunters.2025.1080p.WEB-DL.x264-GRP")
	if !TitlesMatch(r.Title, "KPop Demon Hunters") {
		t.Fatalf("parsed %q should match KPop Demon Hunters", r.Title)
	}
}
