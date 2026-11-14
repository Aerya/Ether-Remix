package frontend

import (
	"strings"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Ajout Remix — voir docs/pse/FORK.fr.md. Tri par défaut des bibliothèques
// Films et Séries dans les applications (réglage PSE_LIBRARY_SORT) :
// « release » (défaut) trie par date de sortie, du plus récent au plus
// ancien ; « title » garde l'ordre alphabétique d'Ether. Un tri choisi dans
// l'application reste prioritaire.

// PSEReleaseSortDefault indique si les bibliothèques s'ouvrent triées par
// date de sortie décroissante.
func PSEReleaseSortDefault() bool {
	switch strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_LIBRARY_SORT"))) {
	case "title", "titre", "name":
		return false
	}
	return true
}
