package catalog

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// currentEpisodeScope limits both branches to the requested, indexed series.
// Canonical coordinates stay in their existing rows; provider coordinates are
// selected from their own order's primary key and preserve the same watch key.
func (c *Catalog) currentEpisodeScope(ctx context.Context, shows []core.RatingKey) (string, []any, error) {
	chosen, err := c.readShowOrders(ctx, shows)
	if err != nil {
		return "", nil, err
	}
	sql, args := episodeScope(shows, chosen)
	return sql, args, nil
}

// An unmatched canonical special remains playable. When its number is
// occupied, place it after the provider's numbered specials.
const canonicalSpecialNumber = `CASE WHEN EXISTS (SELECT 1 FROM catalog_order_episodes m
 WHERE m.show_key=e.show_key AND m.provider=o.provider AND m.order_kind=o.order_kind AND m.season=0 AND m.episode=e.episode)
 THEN COALESCE((SELECT MAX(m.episode) FROM catalog_order_episodes m
 WHERE m.show_key=e.show_key AND m.provider=o.provider AND m.order_kind=o.order_kind AND m.season=0),0)+e.episode
 ELSE e.episode END`

func episodeScope(shows []core.RatingKey, chosen map[core.RatingKey]showOrder) (string, []any) {
	var args []any
	parts := make([]string, 0, len(shows))
	for _, show := range shows {
		choice := chosen[show]
		layout := 0
		if choice.order.Source != "" && providerLayout(&choice.title, choice.order) {
			layout = 1
		}
		parts = append(parts, "(CAST(? AS BIGINT),CAST(? AS TEXT),CAST(? AS TEXT),CAST(? AS BIGINT))")
		args = append(args, show, choice.order.Source, choice.order.Kind, layout)
	}
	sql := `WITH chosen_orders(show_key,provider,order_kind,uses_layout) AS (VALUES ` + strings.Join(parts, ",") + `),
 scoped_episodes(key,show_key,season,episode,season_key,air_ts,air_date,uses_layout) AS (
 SELECT e.episode_key,e.show_key,e.season,e.episode,s.season_key,0,e.air_date,1
 FROM chosen_orders o JOIN catalog_order_episodes e ON e.show_key=o.show_key AND e.provider=o.provider AND e.order_kind=o.order_kind
 JOIN catalog_order_seasons s ON s.show_key=e.show_key AND s.provider=e.provider AND s.order_kind=e.order_kind AND s.season=e.season
 WHERE o.uses_layout=1
 UNION ALL
 SELECT e.key,e.show_key,e.season,CASE WHEN o.uses_layout=1 THEN ` + canonicalSpecialNumber + ` ELSE e.episode END,
 COALESCE(NULLIF(s.season_key,0),3000000000000000+e.show_tmdb*10000+e.season),
 e.released_ts,'',0
 FROM chosen_orders o JOIN catalog_titles e ON e.show_key=o.show_key AND e.type='episode' AND e.key<5000000000000000
 LEFT JOIN catalog_order_seasons s ON o.uses_layout=1 AND s.show_key=o.show_key AND s.provider=o.provider AND s.order_kind=o.order_kind AND s.season=0
 WHERE o.uses_layout=0 OR (o.provider<>'tmdb' AND e.season=0
 AND NOT EXISTS (SELECT 1 FROM catalog_order_episodes m WHERE m.show_key=o.show_key AND m.provider=o.provider AND m.order_kind=o.order_kind
 AND m.episode_key=e.key))
 ) `
	return sql, args
}

const scopedAired = `((e.uses_layout=0 AND e.air_ts<>0 AND e.air_ts<=?) OR (e.uses_layout=1 AND e.air_date<>'' AND e.air_date<=?))`
const scopedAirAfter = `((e.uses_layout=0 AND e.air_ts>?) OR (e.uses_layout=1 AND e.air_date>?))`
