package catalog

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/frenchcat"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

// pseCatalogWhere ajoute les conditions de catalogue de PSE : « lisible
// uniquement » (PSE_PLAYABLE=on) et catalogue en français (PSE_FRENCH=on).
// Voir docs/pse/FORK.fr.md.
//
// Les listes demandées par clé (IncludeKeys : watchlist, collections,
// reprise de lecture) et l'explorateur de règles du propriétaire ne sont
// pas filtrés, sauf les bibliothèques à thème (PSEThemed).
func pseCatalogWhere(conds []string, args []any, f core.Filter, nowTS int64) ([]string, []any) {
	if (f.IncludeKeys != nil && !f.PSEThemed) || f.RuleVisibility != "" {
		return conds, args
	}
	col, typeCol := "t.key", "t.type"
	if f.Type == core.TypeEpisode || f.Type == core.TypeSeason {
		col, typeCol = "t.show_key", ""
	}
	// L'indexeur parcourt aussi les titres pas encore lisibles : sans quoi
	// le filtre, vide au départ, ne se remplirait jamais.
	if clause, values := releasedb.Playable().PlayableSQL(col); clause != "" && !f.PSEUnplayable {
		conds, args = append(conds, clause), append(args, values...)
	}
	if clause, values := frenchcat.Current().SQL(col, typeCol, f.Type, nowTS); clause != "" {
		conds, args = append(conds, clause), append(args, values...)
	}
	return conds, args
}

// pseFiltersCatalog indique si pseCatalogWhere restreint ce filtre : le
// décompte précalculé d'Ether (browse_tally.go) ignore ces conditions et
// doit alors céder la place au COUNT(*) complet.
func pseFiltersCatalog(f core.Filter) bool {
	conds, _ := pseCatalogWhere(nil, nil, f, 0)
	return len(conds) > 0
}

// pseNeedsHomeReleases demande la récupération des dates de sortie
// numériques pour le catalogue en français.
func pseNeedsHomeReleases() bool { return frenchcat.Current().NeedsHomeReleases() }
