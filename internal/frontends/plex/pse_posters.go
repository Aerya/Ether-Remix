package plex

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	kitimages "github.com/braedonsaunders/ether/internal/frontend/kit/images"
)

// Ajouts Remix autour des affiches — voir docs/pse/FORK.fr.md.

// psePosterCacheControl : une affiche venue du fournisseur du propriétaire
// (PostersPlus, XRDB…) peut être gardée un jour par l'application. Son
// adresse change quand le fournisseur est réinterrogé (PSE_POSTER_REFRESH_HOURS)
// et l'ETag de contenu reste envoyé. Un repli TMDB (fournisseur en panne,
// titre sans identifiant) est revérifié à chaque affichage, comme dans
// Ether, pour ne pas rester affiché après la panne.
func psePosterCacheControl(origin *frontend.PosterOrigin) string {
	if origin.FromProvider() {
		return "private, max-age=86400"
	}
	return "private, max-age=0, must-revalidate"
}

// psePosterSize choisit la taille TMDB d'une image d'élément. Une affiche
// (thumb, poster) suit PSEPosterWidth, qui évite de servir 780 px à une
// demande de 600 px ; les autres images gardent la règle d'Ether.
func psePosterSize(element string, width int) string {
	if element == "thumb" || element == "poster" {
		width = kitimages.PSEPosterWidth(width)
	}
	return tmdbSize(width)
}
