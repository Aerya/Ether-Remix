package catalog

import (
	"context"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func (c *Catalog) sourceScheduleRows(ctx context.Context, show core.RatingKey, order core.EpisodeOrder, tail string, args ...any) ([]DatedEpisode, error) {
	bind := append([]any{show, order.Source, order.Kind}, args...)
	rows, err := c.db.QueryContext(ctx, `SELECT e.episode_key,e.season,e.episode,e.title,e.summary,e.air_date,e.runtime_min,e.still,t.rating
 FROM catalog_order_episodes e JOIN catalog_titles t ON t.key=e.episode_key
 WHERE e.show_key=? AND e.provider=? AND e.order_kind=? AND t.adult=0 `+tail, bind...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DatedEpisode
	for rows.Next() {
		var e DatedEpisode
		var date string
		var runtime int
		if err := rows.Scan(&e.Key, &e.Season, &e.Episode, &e.Title, &e.Summary, &date, &runtime, &e.Still, &e.Rating); err != nil {
			return nil, err
		}
		e.Released, e.Runtime = metadataDate(date), time.Duration(runtime)*time.Minute
		out = append(out, e)
	}
	return out, rows.Err()
}

func (c *Catalog) providerTitleSchedule(ctx context.Context, show *core.Title, order core.EpisodeOrder, since time.Time, limit int) (*ShowSchedule, error) {
	if err := c.refreshProviderSchedule(ctx, show, order); err != nil {
		return nil, err
	}
	day := c.now().UTC().Format(time.DateOnly)
	out := &ShowSchedule{}
	seasons, _, err := c.orderManifest(ctx, show.Key, order)
	if err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT e.season,COUNT(*) FROM catalog_order_episodes e JOIN catalog_titles t ON t.key=e.episode_key
 WHERE e.show_key=? AND e.provider=? AND e.order_kind=? AND e.air_date<>'' AND e.air_date<=? AND t.adult=0 GROUP BY e.season`,
		show.Key, order.Source, order.Kind, day)
	if err != nil {
		return nil, err
	}
	aired := map[int]int{}
	for rows.Next() {
		var season, count int
		if err := rows.Scan(&season, &count); err != nil {
			rows.Close()
			return nil, err
		}
		aired[season] = count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, season := range seasons {
		out.Seasons = append(out.Seasons, SeasonSummary{Season: season.Season, Title: season.Title, Summary: season.Summary,
			Released: metadataDate(season.AirDate), Poster: season.PosterPath, Episodes: max(season.EpisodeCount, 0),
			Aired: aired[season.Season], Unmirrored: season.fetched == 0})
	}
	sort.SliceStable(out.Seasons, func(i, j int) bool {
		a, b := out.Seasons[i].Season, out.Seasons[j].Season
		return b == 0 || a != 0 && a < b
	})
	out.Window, err = c.sourceScheduleRows(ctx, show.Key, order, ` AND e.season>0 AND (e.air_date='' OR e.air_date>=?)
 ORDER BY CASE WHEN e.air_date='' THEN 1 ELSE 0 END,e.air_date,e.season,e.episode LIMIT ?`, orderDateBound(since), limit)
	if err != nil {
		return nil, err
	}
	last, err := c.sourceScheduleRows(ctx, show.Key, order, ` AND e.season>0 AND e.air_date<>'' AND e.air_date<=?
 ORDER BY e.air_date DESC,e.season DESC,e.episode DESC LIMIT 1`, day)
	if err != nil {
		return nil, err
	}
	if len(last) > 0 {
		out.Last = &last[0]
	}
	next, err := c.sourceScheduleRows(ctx, show.Key, order, ` AND e.season>0 AND e.air_date>?
 ORDER BY e.air_date,e.season,e.episode LIMIT 1`, day)
	if err != nil {
		return nil, err
	}
	if len(next) > 0 {
		out.Next = &next[0]
	} else {
		for i := range out.Window {
			e := &out.Window[i]
			if e.Released.IsZero() && (out.Last == nil || e.Season > out.Last.Season || e.Season == out.Last.Season && e.Episode > out.Last.Episode) {
				out.Next = e
				break
			}
		}
	}
	return out, nil
}

func orderDateBound(t time.Time) string {
	t = t.UTC()
	if t != t.Truncate(24*time.Hour) {
		t = t.AddDate(0, 0, 1)
	}
	return t.Format(time.DateOnly)
}

func (c *Catalog) providerSeasonSchedule(ctx context.Context, show *core.Title, order core.EpisodeOrder, season int) ([]DatedEpisode, error) {
	seasons, err := c.ensureOrderManifest(ctx, show, order)
	if err != nil {
		return nil, err
	}
	if selected, ok := orderedSeason(seasons, season); ok {
		if err := c.ensureOrderSeason(ctx, show, order, selected); err != nil {
			return nil, err
		}
	}
	return c.sourceScheduleRows(ctx, show.Key, order, ` AND e.season=? ORDER BY e.episode LIMIT 1000`, season)
}
