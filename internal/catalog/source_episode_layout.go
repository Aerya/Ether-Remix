package catalog

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type EpisodeCoordinate struct {
	Key, ParentKey            core.RatingKey
	IDs                       core.IDs
	Season, Episode, Absolute int
	AirDate                   time.Time
}

type EpisodeLayout struct {
	Order    core.EpisodeOrder
	Seasons  map[int]core.RatingKey
	Episodes []EpisodeCoordinate
	Complete map[int]bool
}

func providerLayout(show *core.Title, order core.EpisodeOrder) bool {
	return order != tmdbAired || show.Key.Allocated()
}

func (c *Catalog) CachedEpisodeLayout(ctx context.Context, show core.RatingKey) (EpisodeLayout, bool, error) {
	if show.Kind() != core.KindShow {
		return EpisodeLayout{}, false, core.ErrNotFound
	}
	t, err := c.storedTitle(ctx, show)
	if err != nil {
		return EpisodeLayout{}, false, err
	}
	effective, err := c.effectiveEpisodeOrder(ctx, t)
	if err != nil {
		return EpisodeLayout{}, false, err
	}
	return c.readEpisodeLayout(ctx, t, effective.Order)
}

func (c *Catalog) EpisodeLayout(ctx context.Context, show core.RatingKey) (EpisodeLayout, error) {
	if show.Kind() != core.KindShow {
		return EpisodeLayout{}, core.ErrNotFound
	}
	t, err := c.storedTitle(ctx, show)
	if err != nil {
		return EpisodeLayout{}, err
	}
	effective, err := c.effectiveEpisodeOrder(ctx, t)
	if err != nil {
		return EpisodeLayout{}, err
	}
	if providerLayout(t, effective.Order) {
		if _, err := c.ensureOrderManifest(ctx, t, effective.Order); err != nil {
			return EpisodeLayout{}, err
		}
	}
	layout, _, err := c.readEpisodeLayout(ctx, t, effective.Order)
	return layout, err
}

func (c *Catalog) readEpisodeLayout(ctx context.Context, show *core.Title, order core.EpisodeOrder) (EpisodeLayout, bool, error) {
	out := EpisodeLayout{Order: order, Seasons: map[int]core.RatingKey{}, Complete: map[int]bool{}}
	if !providerLayout(show, order) {
		legacy, err := c.canonicalReleaseEpisodeLayout(ctx, show.Key)
		if err != nil {
			return out, false, err
		}
		rows, err := c.db.QueryContext(ctx, `SELECT t.key,t.tmdb_id,t.tvdb,t.imdb,t.season,t.episode,
 COALESCE(p.air_ts,CASE WHEN evidence.earliest_ts=t.released_ts THEN 0 ELSE t.released_ts END)
 FROM catalog_titles t
 LEFT JOIN catalog_episode_provider_air p ON p.key=t.key
 LEFT JOIN catalog_episode_release evidence ON evidence.key=t.key
 WHERE t.show_key=? AND t.type='episode' AND t.key<5000000000000000 ORDER BY t.season,t.episode,t.key`, show.Key)
		if err != nil {
			return out, false, err
		}
		defer rows.Close()
		for rows.Next() {
			var ep EpisodeCoordinate
			var stamp int64
			if err := rows.Scan(&ep.Key, &ep.IDs.TMDB, &ep.IDs.TVDB, &ep.IDs.IMDB, &ep.Season, &ep.Episode, &stamp); err != nil {
				return out, false, err
			}
			if stamp != 0 {
				ep.AirDate = time.Unix(stamp, 0).UTC()
			}
			ep.ParentKey = core.SeasonKey(show.IDs.TMDB, ep.Season)
			ep.Absolute = legacy.Numbers[core.EpisodeNumber{Season: ep.Season, Episode: ep.Episode}]
			out.Seasons[ep.Season] = ep.ParentKey
			out.Episodes = append(out.Episodes, ep)
		}
		for n := range legacy.Seasons {
			if n >= 0 && n < 1000 {
				out.Seasons[n] = core.SeasonKey(show.IDs.TMDB, n)
			}
		}
		out.Complete = legacy.Complete
		return out, len(out.Seasons) > 0, rows.Err()
	}
	seasons, stamp, err := c.orderManifest(ctx, show.Key, order)
	if err != nil || stamp == 0 {
		return out, false, err
	}
	for _, season := range seasons {
		out.Seasons[season.Season] = season.key
		out.Complete[season.Season] = season.fetched != 0
	}
	rows, err := c.db.QueryContext(ctx, `SELECT e.episode_key,e.season,e.episode,e.absolute,e.air_date,t.tmdb_id,t.tvdb,t.imdb
 FROM catalog_order_episodes e JOIN catalog_titles t ON t.key=e.episode_key
 WHERE e.show_key=? AND e.provider=? AND e.order_kind=? ORDER BY e.season,e.episode`, show.Key, order.Source, order.Kind)
	if err != nil {
		return out, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var ep EpisodeCoordinate
		var date string
		if err := rows.Scan(&ep.Key, &ep.Season, &ep.Episode, &ep.Absolute, &date, &ep.IDs.TMDB, &ep.IDs.TVDB, &ep.IDs.IMDB); err != nil {
			return out, false, err
		}
		ep.AirDate = metadataDate(date)
		ep.ParentKey = out.Seasons[ep.Season]
		out.Episodes = append(out.Episodes, ep)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, false, err
	}
	if order.Source != core.MetadataTMDB && show.IDs.TMDB > 0 && !show.Key.Allocated() {
		if err := c.addCanonicalSpecials(ctx, show, order, &out); err != nil {
			return out, false, err
		}
	}
	return out, true, nil
}

func (c *Catalog) addCanonicalSpecials(ctx context.Context, show *core.Title, order core.EpisodeOrder, out *EpisodeLayout) error {
	rows, err := c.db.QueryContext(ctx, `SELECT e.key,`+canonicalSpecialNumber+`,e.tmdb_id,e.tvdb,e.imdb,e.released_ts
 FROM catalog_titles e CROSS JOIN (SELECT CAST(? AS TEXT) AS provider,CAST(? AS TEXT) AS order_kind) o
 WHERE e.show_key=? AND e.type='episode' AND e.season=0 AND e.key>=? AND e.key<?
 AND NOT EXISTS (SELECT 1 FROM catalog_order_episodes m
 WHERE m.show_key=e.show_key AND m.provider=o.provider AND m.order_kind=o.order_kind AND m.episode_key=e.key)`,
		order.Source, order.Kind, show.Key, core.EpisodeKey(show.IDs.TMDB, 0, 0), core.EpisodeKey(show.IDs.TMDB, 1, 0))

	if err != nil {
		return err
	}
	defer rows.Close()
	parent := out.Seasons[0]
	if parent == 0 {
		parent = core.SeasonKey(show.IDs.TMDB, 0)
	}
	for rows.Next() {
		ep := EpisodeCoordinate{ParentKey: parent}
		var date int64
		if err := rows.Scan(&ep.Key, &ep.Episode, &ep.IDs.TMDB, &ep.IDs.TVDB, &ep.IDs.IMDB, &date); err != nil {
			return err
		}
		if date != 0 {
			ep.AirDate = time.Unix(date, 0).UTC()
		}
		out.Seasons[0] = parent
		out.Episodes = append(out.Episodes, ep)
	}
	sort.Slice(out.Episodes, func(i, j int) bool {
		a, b := out.Episodes[i], out.Episodes[j]
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		return a.Episode < b.Episode
	})
	return rows.Err()
}

func (l EpisodeLayout) EpisodeAt(season, episode int) (EpisodeCoordinate, bool) {
	i := sort.Search(len(l.Episodes), func(i int) bool {
		e := l.Episodes[i]
		return e.Season > season || e.Season == season && e.Episode >= episode
	})
	if i < len(l.Episodes) && l.Episodes[i].Season == season && l.Episodes[i].Episode == episode {
		return l.Episodes[i], true
	}
	return EpisodeCoordinate{}, false
}

// EpisodeOn returns only an unambiguous match on the provider's calendar date.
func (l EpisodeLayout) EpisodeOn(date time.Time) (EpisodeCoordinate, bool) {
	var found EpisodeCoordinate
	if date.IsZero() {
		return found, false
	}
	day := date.Format(time.DateOnly)
	for _, ep := range l.Episodes {
		if !ep.AirDate.IsZero() && ep.AirDate.Format(time.DateOnly) == day {
			if found.Key != 0 {
				return EpisodeCoordinate{}, false
			}
			found = ep
		}
	}
	return found, found.Key != 0
}

type EpisodeGap struct {
	Order             core.EpisodeOrder
	Count, Difference int
	Known             bool
}

func (c *Catalog) EpisodeGap(ctx context.Context, show core.RatingKey) ([]EpisodeGap, error) {
	current, err := c.EffectiveEpisodeOrder(ctx, show)
	if err != nil {
		return nil, err
	}
	orders := []core.EpisodeOrder{current.Order}
	for _, source := range c.metadataSources.Configured() {
		if !source.Capabilities().Shows || !validSourceOrder(source, core.OrderAired) {
			continue
		}
		order := core.EpisodeOrder{Source: source.ID(), Kind: core.OrderAired}
		if order != current.Order {
			orders = append(orders, order)
		}
	}
	out := make([]EpisodeGap, 0, len(orders))
	for _, order := range orders {
		item := EpisodeGap{Order: order}
		seasons, stamp, err := c.orderManifest(ctx, show, order)
		if err != nil {
			return nil, err
		}
		if stamp != 0 {
			item.Known = true
			for _, season := range seasons {
				if season.EpisodeCount < 0 {
					item.Known = false
					break
				}
				item.Count += season.EpisodeCount
			}
		} else if order == tmdbAired && !show.Allocated() {
			var n int
			err = c.db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(child_count),0) FROM catalog_titles WHERE show_key=? AND type='season'`, show).Scan(&n, &item.Count)
			if err != nil {
				return nil, err
			}
			item.Known = n > 0
		}
		if !item.Known {
			item.Count = 0
		}
		if len(out) > 0 && item.Known && out[0].Known {
			item.Difference = item.Count - out[0].Count
		}
		out = append(out, item)
	}
	return out, nil
}

// EpisodeMatchRanges maps known children by external identity, then by their
// effective coordinates. Each identity is assigned at most once.
func (c *Catalog) EpisodeMatchRanges(ctx context.Context, from, to core.RatingKey) ([]core.TitleKeyRange, error) {
	out := []core.TitleKeyRange{{First: int64(from), Last: int64(from), Delta: int64(to - from)}}
	if from == to || from.Kind() != core.KindShow || to.Kind() != core.KindShow {
		return out, nil
	}
	old, _, err := c.CachedEpisodeLayout(ctx, from)
	if err != nil {
		return nil, err
	}
	next, _, err := c.CachedEpisodeLayout(ctx, to)
	if err != nil {
		return nil, err
	}
	appendPair := func(a, b core.RatingKey) {
		if a != 0 && b != 0 && a != b {
			out = append(out, core.TitleKeyRange{First: int64(a), Last: int64(a), Delta: int64(b - a)})
		}
	}
	for n, key := range old.Seasons {
		appendPair(key, next.Seasons[n])
	}
	used := map[core.RatingKey]bool{}
	mapped := map[core.RatingKey]bool{}
	facts := map[episodeMatchFact][]EpisodeCoordinate{}
	for _, ep := range next.Episodes {
		for _, fact := range episodeFacts(ep.IDs, "", "") {
			facts[fact] = append(facts[fact], ep)
		}
	}
	for _, ep := range old.Episodes {
		for _, fact := range episodeFacts(ep.IDs, "", "") {
			matches := facts[fact]
			if len(matches) != 1 || used[matches[0].Key] || !compatibleEpisodeIDs(ep.IDs, matches[0].IDs) {
				continue
			}
			appendPair(ep.Key, matches[0].Key)
			mapped[ep.Key], used[matches[0].Key] = true, true
			break
		}
	}
	for _, ep := range old.Episodes {
		if mapped[ep.Key] {
			continue
		}
		if target, ok := next.EpisodeAt(ep.Season, ep.Episode); ok && !used[target.Key] {
			appendPair(ep.Key, target.Key)
			used[target.Key] = true
		}
	}
	sort.Slice(out[1:], func(i, j int) bool { return out[i+1].First < out[j+1].First })
	return out, nil
}

// HydrateEpisodeLayout loads the selected layout on explicit use, including
// the destination tree of a user-requested match correction.
func (c *Catalog) HydrateEpisodeLayout(ctx context.Context, show core.RatingKey) (EpisodeLayout, error) {
	if show.Kind() != core.KindShow {
		return EpisodeLayout{}, core.ErrNotFound
	}
	t, err := c.storedTitle(ctx, show)
	if err != nil {
		return EpisodeLayout{}, err
	}
	effective, err := c.effectiveEpisodeOrder(ctx, t)
	if err != nil {
		return EpisodeLayout{}, err
	}
	if !providerLayout(t, effective.Order) {
		seasons, err := c.seasonsOfShow(ctx, t.IDs.TMDB)
		if err != nil {
			return EpisodeLayout{}, err
		}
		if err := c.loadSeasons(ctx, t.IDs.TMDB, seasons); err != nil {
			return EpisodeLayout{}, err
		}
	} else {
		seasons, err := c.ensureOrderManifest(ctx, t, effective.Order)
		if err != nil {
			return EpisodeLayout{}, err
		}
		for _, season := range seasons {
			if err := c.ensureOrderSeason(ctx, t, effective.Order, season); err != nil {
				return EpisodeLayout{}, err
			}
		}
	}
	layout, _, err := c.readEpisodeLayout(ctx, t, effective.Order)
	return layout, err
}

// ResolveEpisodeAt opens one season on use, then resolves a displayed episode
// coordinate to its stable identity. Feed matching can instead use the
// cache-only layout and its EpisodeAt and EpisodeOn methods.
func (c *Catalog) ResolveEpisodeAt(ctx context.Context, show core.RatingKey, season, episode int) (EpisodeCoordinate, error) {
	if show.Kind() != core.KindShow || season < 0 || episode < 0 {
		return EpisodeCoordinate{}, core.ErrNotFound
	}
	parent, err := c.Get(ctx, show)
	if err != nil {
		return EpisodeCoordinate{}, err
	}
	order := parent.EpisodeOrder
	if providerLayout(parent, order) {
		seasons, err := c.ensureOrderManifest(ctx, parent, order)
		if err != nil {
			return EpisodeCoordinate{}, err
		}
		if selected, ok := orderedSeason(seasons, season); ok {
			if err := c.ensureOrderSeason(ctx, parent, order, selected); err != nil {
				return EpisodeCoordinate{}, err
			}
		}
	} else {
		if !episodeKeyFits(season, episode) {
			return EpisodeCoordinate{}, core.ErrNotFound
		}
		if _, err := c.fetchSeason(ctx, parent.IDs.TMDB, season); err != nil && !errors.Is(err, core.ErrNotFound) {
			return EpisodeCoordinate{}, err
		}
	}
	scope, args := episodeScope([]core.RatingKey{parent.Key}, map[core.RatingKey]showOrder{parent.Key: {title: *parent, order: order}})
	args = append(args, season, episode)
	var key core.RatingKey
	err = c.db.QueryRowContext(ctx, scope+`SELECT e.key FROM scoped_episodes e WHERE e.season=? AND e.episode=? LIMIT 1`, args...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return EpisodeCoordinate{}, core.ErrNotFound
	}
	if err != nil {
		return EpisodeCoordinate{}, err
	}
	t, err := c.CachedTitle(ctx, key)
	if err != nil {
		return EpisodeCoordinate{}, err
	}
	return EpisodeCoordinate{Key: t.Key, ParentKey: t.ParentKey, IDs: t.IDs, Season: t.Season, Episode: t.Episode, Absolute: t.AbsoluteEpisode, AirDate: t.EpisodeAirDate}, nil
}
