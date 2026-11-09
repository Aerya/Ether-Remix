package core

import "testing"

func TestAnimeClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title *Title
		want  bool
	}{
		{"nil", nil, false},
		{"keyword", &Title{Keywords: []string{"Anime"}}, true},
		{"Japanese animation", &Title{Language: "ja", Genres: []string{"Animation"}}, true},
		{"Japanese drama", &Title{Language: "ja", Genres: []string{"Drama"}}, false},
		{"other animation", &Title{Language: "en", Genres: []string{"Animation"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.title.IsAnime(); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
