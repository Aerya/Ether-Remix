package mediabrowser

// Ajout Remix : règles d'exclusion des « derniers ajouts » Jellyfin des
// bibliothèques Films et Séries (voir docs/pse/FORK.fr.md).

import "github.com/braedonsaunders/ether/internal/frontend"

func pseLatestView(view string) bool { return view == "1" || view == "2" }

// pseLatestFetch élargit la demande pour garder assez de titres après filtrage.
func pseLatestFetch(view string, count int) int {
	if !pseLatestView(view) {
		return count
	}
	if _, active := frontend.PSELatestFilter(view == "1", nil); !active {
		return count
	}
	return min(count*10, 500)
}

// pseFilterLatest retire les titres exclus (Films = vue 1, Séries = vue 2)
// puis coupe au nombre demandé.
func pseFilterLatest(view string, rows []frontend.LatestItem, count int) []frontend.LatestItem {
	if !pseLatestView(view) {
		return rows
	}
	rows, active := frontend.PSELatestFilter(view == "1", rows)
	if active && count > 0 && len(rows) > count {
		rows = rows[:count]
	}
	return rows
}
