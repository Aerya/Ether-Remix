package plex

import "github.com/braedonsaunders/ether/internal/frontend"

// Tri par défaut des bibliothèques Films et Séries (PSE_LIBRARY_SORT) —
// voir docs/pse/FORK.fr.md. Plex annonce ce tri comme celui par défaut ;
// une application qui a mémorisé un autre tri le garde.

func pseReleaseSort(t frontend.MediaType) bool {
	return (t == frontend.TypeMovie || t == frontend.TypeShow) && frontend.PSEReleaseSortDefault()
}

// pseDefaultSortIndex renvoie la position, dans sortDefs, du tri par défaut.
func pseDefaultSortIndex(t frontend.MediaType) int {
	if pseReleaseSort(t) {
		for i, sd := range sortDefs {
			if sd.key == "originallyAvailableAt" {
				return i
			}
		}
	}
	return 0
}
