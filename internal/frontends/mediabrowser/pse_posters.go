package mediabrowser

import (
	"net/http"
	"strconv"
	"strings"

	kitimages "github.com/braedonsaunders/ether/internal/frontend/kit/images"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

// Ajouts Remix autour des affiches — voir docs/pse/FORK.fr.md.

// pseImageRoute reconnaît une demande d'image (…/Images/…). L'envoi d'une
// image n'a pas besoin des empreintes de flou que Jellyfin joint aux listes :
// attendre leur calcul retardait chaque affiche de 100 ms.
func pseImageRoute(r *http.Request) bool {
	return strings.Contains(r.URL.Path, "/Images/")
}

// pseMaxBackdropWidth borne les fonds d'écran : au-delà, TMDB n'a que
// l'image originale, souvent bien plus lourde.
const pseMaxBackdropWidth = 1280

// pseImageSize traduit la taille demandée par l'application (fillWidth,
// maxWidth ou width, sinon une hauteur pour une affiche) vers le paramètre
// « w » lu par le service d'images. Sans cela, toutes les images partaient en
// 500 px, quelle que soit la taille demandée. Un « w » explicite est respecté.
func pseImageSize(r *http.Request) *http.Request {
	q := r.URL.Query()
	if q.Get("w") != "" {
		return r
	}
	poster := strings.EqualFold(r.PathValue("imageType"), "Primary")
	width := pseQueryInt(r, "fillWidth", "maxWidth", "width")
	if width == 0 && poster {
		width = pseQueryInt(r, "fillHeight", "maxHeight", "height") * 2 / 3
	}
	if width <= 0 {
		return r
	}
	if poster {
		width = kitimages.PSEPosterWidth(width)
	} else {
		width = min(width, pseMaxBackdropWidth)
	}
	q.Set("w", strconv.Itoa(width))
	out := r.Clone(r.Context())
	out.URL.RawQuery = q.Encode()
	// Clone ne recopie pas les valeurs de chemin posées par le routeur.
	for _, name := range []string{"itemId", "imageType", "index"} {
		if v := r.PathValue(name); v != "" {
			out.SetPathValue(name, v)
		}
	}
	return out
}

// pseQueryInt renvoie le premier paramètre entier strictement positif parmi
// names, sans tenir compte de la casse, ou 0.
func pseQueryInt(r *http.Request, names ...string) int {
	q := r.URL.Query()
	for _, name := range names {
		for _, raw := range route.Query(q, name) {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				return min(n, 4000)
			}
		}
	}
	return 0
}
