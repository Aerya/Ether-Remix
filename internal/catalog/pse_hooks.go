package catalog

import (
	"context"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/frenchcat"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

// pseCatalogWhere ajoute les conditions de catalogue de PSE : « lisible
// uniquement » (PSE_PLAYABLE=on) et catalogue en français (PSE_FRENCH=on).
// Voir docs/pse/FORK.fr.md.
//
// Les listes demandées par clé (IncludeKeys : watchlist, reprise de lecture)
// et l'explorateur de règles du propriétaire ne sont pas filtrés, sauf les
// bibliothèques à thème (PSEThemed). Les rangées de l'accueil, les
// collections et les titres similaires appliquent ces mêmes conditions dans
// pse_discovery.go.
func pseCatalogWhere(conds []string, args []any, f core.Filter, nowTS int64) ([]string, []any) {
	if (f.IncludeKeys != nil && !f.PSEThemed) || f.RuleVisibility != "" {
		return conds, args
	}
	col, typeCol := "t.key", "t.type"
	if f.Type == core.TypeEpisode || f.Type == core.TypeSeason {
		col, typeCol = "t.show_key", ""
	}
	// L'indexeur parcourt aussi les titres pas encore lisibles ni connus en
	// français : sans quoi ces filtres, fondés sur les releases indexées, ne
	// se rempliraient jamais (une série étrangère doublée ne serait jamais
	// cherchée).
	if f.PSEUnplayable {
		return conds, args
	}
	if clause, values := releasedb.Playable().PlayableSQL(col); clause != "" {
		conds, args = append(conds, "("+clause+" OR "+localmedia.KnownSQL(col)+")"), append(args, values...)
	}
	if clause, values := frenchcat.Current().SQL(col, typeCol, f.Type, nowTS); clause != "" {
		conds, args = append(conds, "("+clause+" OR "+localmedia.KnownSQL(col)+")"), append(args, values...)
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

// LocalMediaChanged invalidates browse caches after a local scan or removal.
func (c *Catalog) LocalMediaChanged() { c.localMedia.Store(true); c.writeGen.Add(1) }
func localMediaWhere() string {
	return fmt.Sprintf("(t.key<%d OR t.key>=%d OR %s)", core.LocalKey(localmedia.VideoBase), core.LocalKey(localmedia.VideoBase+localmedia.Span), localmedia.KnownSQL("t.key"))
}
func (c *Catalog) loadLocalMedia(ctx context.Context) {
	var n int
	_ = c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_titles WHERE key>=? AND key<?", core.LocalKey(localmedia.VideoBase), core.LocalKey(localmedia.VideoBase+localmedia.Span)).Scan(&n)
	c.localMedia.Store(n > 0)
}
