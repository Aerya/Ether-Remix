package mediabrowser

// Ajout Remix : règles d'exclusion des « derniers ajouts » Jellyfin des
// bibliothèques Films et Séries (voir docs/pse/FORK.fr.md).

import (
	"slices"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/pse/latestfilter"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

type pseLatestCache struct {
	raw string
	c   *latestfilter.Compiled
}

var pseLatestCached atomic.Pointer[pseLatestCache]

// pseLatestFilter renvoie le filtre compilé du réglage, recompilé seulement
// quand sa valeur change. Une valeur invalide n'exclut rien.
func pseLatestFilter() *latestfilter.Compiled {
	raw := pseenv.Get(latestfilter.Key)
	if raw == "" {
		return nil
	}
	if c := pseLatestCached.Load(); c != nil && c.raw == raw {
		return c.c
	}
	c, err := latestfilter.Parse(raw)
	if err != nil {
		c = nil
	}
	pseLatestCached.Store(&pseLatestCache{raw: raw, c: c})
	return c
}

// pseLatestForView : bibliothèque Films (1) = films, Séries (2) = séries.
func pseLatestForView(view string) *latestfilter.Compiled {
	if view != "1" && view != "2" {
		return nil
	}
	return pseLatestFilter()
}

// pseLatestFetch élargit la demande pour garder assez de titres après filtrage.
func pseLatestFetch(view string, count int) int {
	c := pseLatestForView(view)
	if c.Empty() || (view == "1" && c.Movie == nil) || (view == "2" && c.Show == nil) {
		return count
	}
	return min(count*10, 500)
}

// pseFilterLatest retire les titres exclus puis coupe au nombre demandé.
func pseFilterLatest(view string, rows []frontend.LatestItem, count int) []frontend.LatestItem {
	c := pseLatestForView(view)
	if c.Empty() {
		return rows
	}
	flt := c.Movie
	if view == "2" {
		flt = c.Show
	}
	if flt == nil {
		return rows
	}
	rows = slices.DeleteFunc(rows, func(row frontend.LatestItem) bool { return flt.Hidden(&row.Title) })
	if count > 0 && len(rows) > count {
		rows = rows[:count]
	}
	return rows
}
