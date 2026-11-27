package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// AiredEpisodeTallies counts, for each show, the aired episodes
// CachedAiredEpisodes lists and how many of them userID has played (a
// watch row with a view), by season, without reading the episodes. Aired
// episodes released after settled, or named in judge, are not counted:
// their keys are returned for the caller to judge one by one. A zero
// settled counts every aired episode. One statement per airedManyBatch
// shows.
func (c *Catalog) AiredEpisodeTallies(ctx context.Context, userID int64, shows []core.RatingKey, settled time.Time, judge []core.RatingKey) (map[core.RatingKey]core.AiredEpisodeTally, error) {
	out := make(map[core.RatingKey]core.AiredEpisodeTally, len(shows))
	var keys []core.RatingKey
	for _, show := range shows {
		if show == 0 || show.Kind() != core.KindShow {
			return nil, errors.New("catalog: invalid aired episode query")
		}
		if _, ok := out[show]; !ok {
			out[show] = core.AiredEpisodeTally{Seasons: map[int]core.EpisodeTally{}}
			keys = append(keys, show)
		}
	}
	var named []core.RatingKey
	for _, key := range uniqueRatingKeys(judge) {
		if key.Kind() == core.KindEpisode {
			named = append(named, key)
		}
	}
	asOf := c.now().Unix()
	settle := asOf
	if !settled.IsZero() && settled.Unix() < settle {
		settle = settled.Unix()
	}
	day := time.Unix(asOf, 0).UTC().Format(time.DateOnly)
	settledDay := time.Unix(settle, 0).UTC().Format(time.DateOnly)
	for part := range store.Chunks(keys, airedManyBatch) {
		scope, args, err := c.currentEpisodeScope(ctx, part)
		if err != nil {
			return nil, err
		}
		counted, listed := "", ""
		if len(named) > 0 {
			counted, _ = c.keyMembership("e.key", true, named)
			listed, _ = c.keyMembership("e.key", false, named)
			counted = " AND " + counted
			listed = " OR " + listed
		}
		args = append(args, userID, asOf, day, settle, settledDay)
		if len(named) > 0 {
			_, bind := c.keyMembership("e.key", true, named)
			args = append(args, bind...)
		}
		args = append(args, asOf, day, settle, settledDay)
		if len(named) > 0 {
			_, bind := c.keyMembership("e.key", false, named)
			args = append(args, bind...)
		}
		rows, err := c.db.QueryContext(ctx, scope+`SELECT e.show_key,e.season,CAST(0 AS BIGINT),COUNT(*),COUNT(w.rating_key)
 FROM scoped_episodes e JOIN catalog_titles t ON t.key=e.key
 LEFT JOIN state_watch w ON w.user_id=? AND w.rating_key=e.key AND w.view_count>0
 WHERE t.adult=0 AND `+scopedAired+` AND `+scopedAired+counted+`
 GROUP BY e.show_key,e.season
 UNION ALL
 SELECT e.show_key,e.season,e.key,1,0
 FROM scoped_episodes e JOIN catalog_titles t ON t.key=e.key
 WHERE t.adult=0 AND `+scopedAired+` AND (`+scopedAirAfter+listed+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var show, key int64
			var season, n, played int
			if err := rows.Scan(&show, &season, &key, &n, &played); err != nil {
				rows.Close()
				return nil, err
			}
			tally, ok := out[core.RatingKey(show)]
			if !ok {
				continue
			}
			tally.Total += n
			if key != 0 {
				tally.Judge = append(tally.Judge, core.RatingKey(key))
			} else {
				tally.Seasons[season] = core.EpisodeTally{Aired: n, Played: played}
			}
			out[core.RatingKey(show)] = tally
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
