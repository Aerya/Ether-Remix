package mediabrowser

// Ajout Remix : règles d'exclusion des « derniers ajouts » Jellyfin des
// bibliothèques Films et Séries (voir docs/pse/FORK.fr.md).

import (
	"slices"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/core"
)

// pseLatestHidden : bibliothèque Films (1) = films, Séries (2) = séries.
func pseLatestHidden(view string) func(*core.Title) bool {
	if view != "1" && view != "2" {
		return nil
	}
	return frontend.PSELatestHidden(view == "1")
}

// pseLatestFetch élargit la demande pour garder assez de titres après filtrage.
func pseLatestFetch(view string, count int) int {
	if pseLatestHidden(view) == nil {
		return count
	}
	return min(count*10, 500)
}

// pseFilterLatest retire les titres exclus puis coupe au nombre demandé.
func pseFilterLatest(view string, rows []frontend.LatestItem, count int) []frontend.LatestItem {
	hidden := pseLatestHidden(view)
	if hidden == nil {
		return rows
	}
	rows = slices.DeleteFunc(rows, func(row frontend.LatestItem) bool { return hidden(&row.Title) })
	if count > 0 && len(rows) > count {
		rows = rows[:count]
	}
	return rows
}
