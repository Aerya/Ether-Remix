package catalog

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// Les rangées de l'accueil, les collections et les titres similaires ne
// passent pas par les requêtes de Browse : sans ce filtre, ils montreraient
// des titres que les pages Films et Séries masquent (pas encore sortis en
// France, sans VF, pas lisibles). Voir docs/pse/FORK.fr.md.

const pseDiscoveryBatch = 400

// pseDiscoveryAllowed renvoie les clés qui respectent pseCatalogWhere.
// active est faux quand aucune règle Remix n'est activée.
func (c *Catalog) pseDiscoveryAllowed(ctx context.Context, keys []core.RatingKey) (allowed map[core.RatingKey]bool, active bool, err error) {
	conds, condArgs := pseCatalogWhere(nil, nil, core.Filter{}, c.now().Unix())
	if len(conds) == 0 {
		return nil, false, nil
	}
	where := strings.Join(conds, " AND ")
	allowed = make(map[core.RatingKey]bool, len(keys))
	for start := 0; start < len(keys); start += pseDiscoveryBatch {
		batch := keys[start:min(start+pseDiscoveryBatch, len(keys))]
		args := make([]any, 0, len(batch)+len(condArgs))
		for _, k := range batch {
			args = append(args, int64(k))
		}
		args = append(args, condArgs...)
		rows, err := c.db.QueryContext(ctx, "SELECT t.key FROM catalog_titles t WHERE t.key IN ("+placeholders(len(batch))+") AND "+where, args...)
		if err != nil {
			return nil, true, err
		}
		for rows.Next() {
			var k int64
			if err = rows.Scan(&k); err != nil {
				rows.Close()
				return nil, true, err
			}
			allowed[core.RatingKey(k)] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, true, err
		}
	}
	return allowed, true, nil
}

// PSEDiscoveryKeys garde, dans l'ordre, les titres d'une collection que les
// pages Films et Séries afficheraient. Les titres ajoutés à la main par le
// propriétaire restent visibles.
func (c *Catalog) PSEDiscoveryKeys(ctx context.Context, keys []core.RatingKey) ([]core.RatingKey, error) {
	if len(keys) == 0 {
		return keys, nil
	}
	allowed, active, err := c.pseDiscoveryAllowed(ctx, keys)
	if err != nil || !active {
		return keys, err
	}
	out := make([]core.RatingKey, 0, len(keys))
	for _, k := range keys {
		if allowed[k] || c.isOwnerIncluded(k) {
			out = append(out, k)
		}
	}
	return out, nil
}

// PSEDiscovery applique la même règle à une liste de titres. Seuls les films
// et les séries sont jugés. Les films à venir restent visibles quand le
// propriétaire a choisi de les afficher. En cas d'erreur, rien n'est montré,
// comme FilterVisibility.
func (c *Catalog) PSEDiscovery(ctx context.Context, items []core.Title) []core.Title {
	if len(items) == 0 {
		return items
	}
	keys := make([]core.RatingKey, 0, len(items))
	for i := range items {
		if items[i].Type == core.TypeMovie || items[i].Type == core.TypeShow {
			keys = append(keys, items[i].Key)
		}
	}
	allowed, active, err := c.pseDiscoveryAllowed(ctx, keys)
	if err != nil {
		return nil
	}
	if !active {
		return items
	}
	out := make([]core.Title, 0, len(items))
	for i := range items {
		t := &items[i]
		if (t.Type != core.TypeMovie && t.Type != core.TypeShow) || allowed[t.Key] || c.upcomingMovie(t) || c.isTitleOrShowOwnerIncluded(t) {
			out = append(out, *t)
		}
	}
	return out
}
