package frontend

import (
	"slices"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/pse/latestfilter"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Règles d'exclusion des derniers ajouts Jellyfin — voir docs/pse/FORK.fr.md.
// Alias pour que les frontends n'importent pas internal/pse directement.

type pseLatestCache struct {
	raw string
	c   *latestfilter.Compiled
}

var pseLatestCached atomic.Pointer[pseLatestCache]

// pseLatestCompiled renvoie le filtre compilé du réglage, recompilé seulement
// quand sa valeur change. Une valeur invalide n'exclut rien.
func pseLatestCompiled() *latestfilter.Compiled {
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

// PSELatestFilter retire des derniers ajouts les titres exclus par les règles
// du type (films si movie, sinon séries). active est faux sans règle.
func PSELatestFilter(movie bool, rows []LatestItem) (out []LatestItem, active bool) {
	c := pseLatestCompiled()
	if c.Empty() {
		return rows, false
	}
	flt := c.Show
	if movie {
		flt = c.Movie
	}
	if flt == nil {
		return rows, false
	}
	return slices.DeleteFunc(rows, func(row LatestItem) bool { return flt.Hidden(&row.Title) }), true
}
