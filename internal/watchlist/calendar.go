package watchlist

import (
	"context"
	"database/sql"

	"github.com/braedonsaunders/ether/internal/core"
)

// Watchlisted returns every viewer's watchlisted titles by viewer id,
// released or not, from every source. A season counts as its show.
func (s *Service) Watchlisted(ctx context.Context) (map[int64][]core.RatingKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT user_id,item_key FROM watchlist_items ORDER BY user_id,item_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]core.RatingKey{}
	seen := map[int64]map[core.RatingKey]bool{}
	for rows.Next() {
		var uid, raw int64
		if err := rows.Scan(&uid, &raw); err != nil {
			return nil, err
		}
		key := core.RatingKey(raw)
		switch key.Kind() {
		case core.KindSeason, core.KindEpisode:
			key = core.ShowKey(key.TMDB())
		case core.KindMovie, core.KindShow:
		default:
			continue
		}
		if seen[uid] == nil {
			seen[uid] = map[core.RatingKey]bool{}
		}
		if !seen[uid][key] {
			seen[uid][key] = true
			out[uid] = append(out[uid], key)
		}
	}
	return out, rows.Err()
}

// ListedTitleKeys returns distinct watchlist identities across all viewers,
// including unreleased entries. Consumers resolve parents with the catalogue.
func ListedTitleKeys(ctx context.Context, db *sql.DB) ([]core.RatingKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT item_key FROM watchlist_items`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []core.RatingKey
	for rows.Next() {
		var key core.RatingKey
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}
