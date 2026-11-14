package images

// Ajout Remix — voir docs/pse/FORK.fr.md.

// psePosterWidths : largeurs que le CDN de TMDB sert pour une affiche.
var psePosterWidths = []int{92, 154, 185, 300, 342, 500, 780}

// PSEPosterWidth choisit la largeur TMDB d'une affiche demandée à w pixels.
// TMDBSize prend toujours la largeur au-dessus : une télé qui demande 600 px
// reçoit alors l'affiche en 780 px, plus de deux fois plus lourde que celle
// en 500 px. Ici, les largeurs 342 et 500 sont retenues tant que la demande
// ne les dépasse pas de plus d'un quart, et une affiche n'est jamais servie
// au-delà de 780 px. Les petites largeurs gardent la règle d'Ether. Une
// largeur nulle ou négative est renvoyée telle quelle.
func PSEPosterWidth(w int) int {
	if w <= 0 {
		return w
	}
	for _, b := range psePosterWidths {
		limit := b
		if b >= 342 {
			limit = b + b/4
		}
		if w <= limit {
			return b
		}
	}
	return psePosterWidths[len(psePosterWidths)-1]
}
