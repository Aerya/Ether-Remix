package mediabrowser

import (
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// Listes légères — voir docs/pse/FORK.fr.md.

// pseListItems marque la requête comme celle d'une liste quand ParentId est
// absent, une vue, une collection, ou une série lue en récursif : ces listes
// ne lisent que la base. Une saison ou une série non récursive est l'ouverture
// d'une fiche et garde le comportement d'origine.
func pseListItems(r *http.Request, parent []string) *http.Request {
	if len(parent) == 0 || parent[0] == "" {
		return frontend.PSEListRequest(r)
	}
	if _, err := ids.ParseGUID(ids.View, parent[0]); err == nil {
		return frontend.PSEListRequest(r)
	}
	key, err := ids.ParseRatingKeyGUID(parent[0])
	if err != nil {
		return r
	}
	switch key.Kind() {
	case frontend.KindCollection:
		return frontend.PSEListRequest(r)
	case frontend.KindShow:
		if queryBool(r, "Recursive", false) {
			return frontend.PSEListRequest(r)
		}
	}
	return r
}
