package images

import "testing"

func TestPSEPosterWidth(t *testing.T) {
	for w, want := range map[int]int{
		0: 0, -5: -5,
		90: 92, 150: 154, 185: 185, 186: 300, 200: 300, 300: 300, 301: 342, 342: 342, 400: 342, 427: 342,
		428: 500, 500: 500, 600: 500, 625: 500, 626: 780, 780: 780, 1000: 780, 4000: 780,
	} {
		if got := PSEPosterWidth(w); got != want {
			t.Errorf("PSEPosterWidth(%d) = %d, attendu %d", w, got, want)
		}
	}
	// La largeur retenue doit correspondre exactement à une taille TMDB.
	for _, w := range []int{200, 400, 600, 1000} {
		b := PSEPosterWidth(w)
		if got := TMDBSize(b); got == "original" {
			t.Errorf("la largeur %d (demande %d) ne correspond à aucune taille TMDB", b, w)
		}
	}
}
