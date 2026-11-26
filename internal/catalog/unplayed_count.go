package catalog

import (
	"context"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
)

// CountUnplayedAired counts aired episodes a viewer has not played, for each
// show or season key. It uses the aired index and the watch primary key.
// It does not load episode rows or refresh metadata. An empty max rating
// counts every aired episode. A set rating drops unrated and stricter ones.
func (c *Catalog) CountUnplayedAired(ctx context.Context, userID int64, keys []core.RatingKey, maxRating string) (map[core.RatingKey]int, error) {
	out := map[core.RatingKey]int{}
	if c == nil || c.db == nil || userID == 0 || len(keys) == 0 {
		return out, nil
	}
	var shows, seasons []core.RatingKey
	for _, key := range keys {
		out[key] = 0
		switch key.Kind() {
		case core.KindShow:
			shows = append(shows, key)
		case core.KindSeason:
			seasons = append(seasons, key)
		}
	}
	if len(seasons) > 0 {
		filter, args := c.keyMembership("key", false, uniqueRatingKeys(seasons))
		rows, err := c.db.QueryContext(ctx, `SELECT DISTINCT show_key FROM catalog_titles WHERE type='season' AND `+filter, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var show core.RatingKey
			if err := rows.Scan(&show); err != nil {
				rows.Close()
				return nil, err
			}
			shows = append(shows, show)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, season := range seasons {
			if !season.Allocated() {
				shows = append(shows, core.ShowKey(season.TMDB()))
			}
		}
	}
	shows = uniqueRatingKeys(shows)
	if len(shows) == 0 {
		return out, nil
	}
	restrict := strings.TrimSpace(maxRating) != ""
	rank := 0
	if restrict {
		rank = int(contentrating.EnforceLevelOf(maxRating))
		if rank == 0 {
			return out, nil
		}
	}
	now := c.now().Unix()
	showTotals := map[core.RatingKey]int{}
	seasonTotals := map[core.RatingKey]int{}
	const chunk = 200
	for start := 0; start < len(shows); start += chunk {
		part := shows[start:min(start+chunk, len(shows))]
		scope, args, err := c.currentEpisodeScope(ctx, part)
		if err != nil {
			return nil, err
		}
		args = append(args, now, time.Unix(now, 0).UTC().Format(time.DateOnly), userID)
		ratingSQL := ""
		if restrict {
			ratingSQL = ` AND t.content_rating_rank>0 AND t.content_rating_rank<=? AND t.enforcement_rating<>'XXX'`
			args = append(args, rank)
			if core.PlexSaferRating(ctx) && c.saferRating != nil {
				ratingSQL += ` AND ` + c.saferRating.HideExceeding("t.key")
				args = append(args, rank)
			}
		}
		rows, err := c.db.QueryContext(ctx, scope+`SELECT e.show_key,e.season_key,COUNT(*)
 FROM scoped_episodes e JOIN catalog_titles t ON t.key=e.key
 WHERE t.adult=0 AND `+scopedAired+`
 AND NOT EXISTS (SELECT 1 FROM state_watch w WHERE w.user_id=? AND w.rating_key=e.key AND w.view_count>0)`+ratingSQL+`
 GROUP BY e.show_key,e.season_key`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var show, season core.RatingKey
			var count int
			if err := rows.Scan(&show, &season, &count); err != nil {
				rows.Close()
				return nil, err
			}
			showTotals[show] += count
			seasonTotals[season] += count
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, key := range keys {
		switch key.Kind() {
		case core.KindShow:
			out[key] = showTotals[key]
		case core.KindSeason:
			out[key] = seasonTotals[key]
		}
	}
	return out, nil
}
