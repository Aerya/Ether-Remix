package mediabrowser

import (
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

// Tri par défaut des bibliothèques Films et Séries (PSE_LIBRARY_SORT) —
// voir docs/pse/FORK.fr.md. Jellyfin et Emby laissent souvent l'application
// choisir le tri : Remix ne change que ce qui n'est pas demandé.

// pseLibrarySort trie par date de sortie décroissante la liste d'une
// bibliothèque demandée sans SortBy.
func pseLibrarySort(r *http.Request, q *frontend.ItemQuery) {
	if !frontend.PSEReleaseSortDefault() || len(route.Query(r.URL.Query(), "SortBy")) > 0 {
		return
	}
	for _, t := range q.Types {
		if t != "movie" && t != "show" {
			return
		}
	}
	q.Sort = frontend.SortReleased
	if len(route.Query(r.URL.Query(), "SortOrder")) == 0 {
		q.Desc = true
	}
}

// pseDisplaySort propose le tri par date de sortie dans les préférences
// d'affichage par défaut d'une bibliothèque, jamais enregistrées par
// l'application.
func pseDisplaySort(id string, object map[string]any) {
	if !frontend.PSEReleaseSortDefault() {
		return
	}
	if _, err := ids.ParseGUID(ids.View, id); err != nil {
		return
	}
	object["SortBy"] = "PremiereDate"
	object["SortOrder"] = "Descending"
}
