package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

type orderFetchKey struct {
	show   core.RatingKey
	order  core.EpisodeOrder
	season int // -1 is the manifest
}

type orderSeason struct {
	core.SeasonSummary
	key     core.RatingKey
	fetched int64
}

func (c *Catalog) providerShow(ctx context.Context, show *core.Title, source core.MetadataSource) (core.ProviderRef, error) {
	var id int64
	switch source.ID() {
	case core.MetadataTMDB:
		id = show.IDs.TMDB
	case core.MetadataTVDB:
		id = show.IDs.TVDB
	}
	if id > 0 {
		return core.ProviderRef{Source: source.ID(), ID: strconv.FormatInt(id, 10), Type: core.TypeShow}, nil
	}
	ref := core.ProviderRef{Source: source.ID(), Type: core.TypeShow}
	err := c.db.QueryRowContext(ctx, `SELECT provider_id FROM catalog_identity WHERE key=? AND provider=? AND kind=? ORDER BY seq LIMIT 1`, show.Key, source.ID(), core.KindShow).Scan(&ref.ID)
	if err == nil {
		return ref, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ref, err
	}
	refs, err := source.FindByExternal(ctx, show.IDs, core.TypeShow)
	if err != nil {
		return ref, err
	}
	if len(refs) != 1 || refs[0].ID == "" || refs[0].Source != source.ID() || refs[0].Type != core.TypeShow {
		return ref, core.ErrNotFound
	}
	key, err := c.saveSourceIdentity(ctx, core.KindShow, source.ID(), refs[0].ID, show.Key)
	if err != nil {
		return ref, err
	}
	if key != show.Key {
		return ref, core.ErrNotFound
	}
	return refs[0], nil
}

func (c *Catalog) orderManifest(ctx context.Context, show core.RatingKey, order core.EpisodeOrder) ([]orderSeason, int64, error) {
	var fetched int64
	err := c.db.QueryRowContext(ctx, `SELECT fetched_at FROM catalog_order_manifest WHERE show_key=? AND provider=? AND order_kind=?`, show, order.Source, order.Kind).Scan(&fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT season,season_key,title,summary,poster,air_date,episode_count,fetched_at,provider_id FROM catalog_order_seasons WHERE show_key=? AND provider=? AND order_kind=? ORDER BY season`, show, order.Source, order.Kind)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	seasons := []orderSeason{}
	for rows.Next() {
		var row orderSeason
		if err := rows.Scan(&row.Season, &row.key, &row.Title, &row.Summary, &row.PosterPath, &row.AirDate, &row.EpisodeCount, &row.fetched, &row.Ref.ID); err != nil {
			return nil, 0, err
		}
		row.Ref.Source, row.Ref.Type = order.Source, core.TypeSeason
		seasons = append(seasons, row)
	}
	return seasons, fetched, rows.Err()
}

func (c *Catalog) orderFresh(fetched int64) bool {
	return fetched != 0 && c.now().Sub(time.Unix(fetched, 0)) < c.staleAfter
}

func (c *Catalog) ensureOrderManifest(ctx context.Context, show *core.Title, order core.EpisodeOrder) ([]orderSeason, error) {
	seasons, stamp, err := c.orderManifest(ctx, show.Key, order)
	if err != nil || c.orderFresh(stamp) {
		return seasons, err
	}
	source := c.metadataSources.Get(order.Source)
	if source == nil || !source.Configured() {
		return seasons, nil
	}
	_, err = c.orderFlights.Do(ctx, orderFetchKey{show.Key, order, -1}, func(ctx context.Context) (struct{}, error) {
		_, current, err := c.orderManifest(ctx, show.Key, order)
		if err != nil || c.orderFresh(current) {
			return struct{}{}, err
		}
		ref, err := c.providerShow(ctx, show, source)
		if err != nil {
			return struct{}{}, err
		}
		rows, err := source.Seasons(ctx, ref, order)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, c.storeOrderManifest(ctx, show, order, rows)
	})
	if err != nil {
		if stamp != 0 {
			return seasons, nil
		}
		return nil, err
	}
	seasons, _, err = c.orderManifest(ctx, show.Key, order)
	return seasons, err
}

func (c *Catalog) beginOrderWrite(ctx context.Context, show core.RatingKey) (*sql.Tx, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// SQLite starts an immediate transaction. Postgres locks the one show
	// whose cross-order identities are being reconciled.
	query := `SELECT key FROM catalog_titles WHERE key=? AND type='show'`
	if store.DialectOf(c.db) == store.Postgres {
		query += " FOR UPDATE"
	}
	var key core.RatingKey
	err = tx.QueryRowContext(ctx, query, show).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		err = core.ErrNotFound
	}

	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (c *Catalog) storeOrderManifest(ctx context.Context, show *core.Title, order core.EpisodeOrder, rows []core.SeasonSummary) error {
	tx, err := c.beginOrderWrite(ctx, show.Key)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := c.now().Unix()
	seen := map[int]bool{}
	for _, row := range rows {
		if row.Season < 0 || seen[row.Season] {
			return fmt.Errorf("catalog: duplicate or invalid season in order")
		}
		seen[row.Season] = true
		var key core.RatingKey
		err := tx.QueryRowContext(ctx, `SELECT season_key FROM catalog_order_seasons WHERE show_key=? AND provider=? AND order_kind=? AND season=?`, show.Key, order.Source, order.Kind, row.Season).Scan(&key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if key == 0 {
			if order.Kind == core.OrderAired && row.Season < 1000 && show.IDs.TMDB > 0 && !show.Key.Allocated() {
				key = core.SeasonKey(show.IDs.TMDB, row.Season)
			} else {
				id := fmt.Sprintf("%s:%s:%d", show.Key, order.Kind, row.Season)
				if row.Ref.ID != "" {
					id = fmt.Sprintf("%s:%s:%s", show.Key, order.Kind, row.Ref.ID)
				}
				key, err = putSourceIdentity(ctx, tx, core.KindSeason, order.Source, id, 0, now)
				if err != nil {
					return err
				}
			}
		}
		t := orderedSeasonTitle(show, order, orderSeason{SeasonSummary: row, key: key})
		var exists int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM catalog_titles WHERE key=?`, key).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			if err = upsertOne(ctx, tx, t, upsertOpts{detail: true}, now); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if key.Allocated() {
			if err := saveAllocatedAncestry(ctx, tx, key, show.Key, show.Key, row.Season, 0); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_order_seasons(show_key,provider,order_kind,season,season_key,title,summary,poster,air_date,episode_count,provider_id)
 VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(show_key,provider,order_kind,season) DO UPDATE SET
 season_key=excluded.season_key,title=excluded.title,summary=excluded.summary,poster=excluded.poster,
 air_date=CASE WHEN excluded.air_date<>'' THEN excluded.air_date ELSE catalog_order_seasons.air_date END,
 fetched_at=CASE WHEN excluded.episode_count>=0 AND catalog_order_seasons.episode_count<>excluded.episode_count THEN 0 ELSE catalog_order_seasons.fetched_at END,
 episode_count=CASE WHEN excluded.episode_count>=0 THEN excluded.episode_count ELSE catalog_order_seasons.episode_count END,provider_id=excluded.provider_id`,
			show.Key, order.Source, order.Kind, row.Season, key, row.Title, row.Summary, row.PosterPath, row.AirDate, row.EpisodeCount, row.Ref.ID)
		if err != nil {
			return err
		}
	}
	existing, err := tx.QueryContext(ctx, `SELECT season FROM catalog_order_seasons WHERE show_key=? AND provider=? AND order_kind=?`, show.Key, order.Source, order.Kind)
	if err != nil {
		return err
	}
	var removed []int
	for existing.Next() {
		var n int
		if err := existing.Scan(&n); err != nil {
			existing.Close()
			return err
		}
		if !seen[n] {
			removed = append(removed, n)
		}
	}
	err = existing.Err()
	existing.Close()
	if err != nil {
		return err
	}
	for _, n := range removed {
		for _, table := range []string{"catalog_order_seasons", "catalog_order_episodes"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE show_key=? AND provider=? AND order_kind=? AND season=?`, show.Key, order.Source, order.Kind, n); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_order_manifest(show_key,provider,order_kind,fetched_at) VALUES (?,?,?,?)
 ON CONFLICT(show_key,provider,order_kind) DO UPDATE SET fetched_at=excluded.fetched_at`, show.Key, order.Source, order.Kind, now)
	if err != nil {
		return err
	}
	if err := c.commitOrderWrite(tx); err != nil {
		return err
	}
	c.hasEpisodeLayouts.Store(true)
	return nil
}

func (c *Catalog) commitOrderWrite(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	c.writeGen.Add(1)
	return nil
}

func (c *Catalog) ensureOrderSeason(ctx context.Context, show *core.Title, order core.EpisodeOrder, season orderSeason) error {
	if c.orderFresh(season.fetched) {
		return nil
	}
	source := c.metadataSources.Get(order.Source)
	if source == nil || !source.Configured() {
		return nil
	}
	_, err := c.orderFlights.Do(ctx, orderFetchKey{show.Key, order, season.Season}, func(ctx context.Context) (struct{}, error) {
		var stamp int64
		err := c.db.QueryRowContext(ctx, `SELECT fetched_at FROM catalog_order_seasons WHERE show_key=? AND provider=? AND order_kind=? AND season=?`, show.Key, order.Source, order.Kind, season.Season).Scan(&stamp)
		if err != nil || c.orderFresh(stamp) {
			return struct{}{}, err
		}
		ref, err := c.providerShow(ctx, show, source)
		if err != nil {
			return struct{}{}, err
		}
		rows, err := source.Episodes(ctx, ref, order, season.Season)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, c.storeOrderSeason(ctx, show, order, season, rows)
	})
	if err != nil && season.fetched != 0 {
		return nil
	}
	return err
}

type matchEpisode struct {
	key        core.RatingKey
	ids        core.IDs
	number     core.EpisodeNumber
	name, date string
}

func (c *Catalog) storeOrderSeason(ctx context.Context, show *core.Title, order core.EpisodeOrder, season orderSeason, rows []core.EpisodeDetail) error {
	tx, err := c.beginOrderWrite(ctx, show.Key)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := map[int]bool{}
	refs := map[string]bool{}
	var firstAir string
	for _, row := range rows {
		if row.Season != season.Season || row.Episode < 0 || seen[row.Episode] || refs[row.Ref.ID] || row.Ref.ID == "" || row.Ref.Source != order.Source {
			return fmt.Errorf("catalog: invalid or duplicate provider episode")
		}
		seen[row.Episode] = true
		refs[row.Ref.ID] = true
		if !metadataDate(row.AirDate).IsZero() && (firstAir == "" || row.AirDate < firstAir) {
			firstAir = row.AirDate
		}
	}
	existing, err := tx.QueryContext(ctx, `SELECT key,tmdb_id,tvdb,imdb,season,episode,title,released_ts FROM catalog_titles WHERE show_key=? AND type='episode'`, show.Key)
	if err != nil {
		return err
	}
	var episodes []matchEpisode
	for existing.Next() {
		var row matchEpisode
		var released int64
		if err := existing.Scan(&row.key, &row.ids.TMDB, &row.ids.TVDB, &row.ids.IMDB, &row.number.Season, &row.number.Episode, &row.name, &released); err != nil {
			existing.Close()
			return err
		}
		if released != 0 {
			row.date = time.Unix(released, 0).UTC().Format(time.DateOnly)
		}
		episodes = append(episodes, row)
	}
	err = existing.Err()
	existing.Close()
	if err != nil {
		return err
	}
	// Lock in exact identities before considering any heuristic match.
	assigned := make([]core.RatingKey, len(rows))
	providerKeys := make([]core.RatingKey, len(rows))
	used := map[core.RatingKey]bool{}
	taken, err := tx.QueryContext(ctx, `SELECT episode_key FROM catalog_order_episodes WHERE show_key=? AND provider=? AND order_kind=? AND season<>?`, show.Key, order.Source, order.Kind, season.Season)
	if err != nil {
		return err
	}
	for taken.Next() {
		var key core.RatingKey
		if err := taken.Scan(&key); err != nil {
			taken.Close()
			return err
		}
		used[key] = true
	}
	err = taken.Err()
	taken.Close()
	if err != nil {
		return err
	}
	facts := map[episodeMatchFact][]int{}
	pos := map[core.EpisodeNumber][]int{}
	for i, ep := range episodes {
		for _, item := range episodeFacts(ep.ids, ep.date, ep.name) {
			facts[item] = append(facts[item], i)
		}
		pos[ep.number] = append(pos[ep.number], i)
	}
	match := func(i int, candidates []int) {
		if assigned[i] != 0 {
			return
		}
		var key core.RatingKey
		for _, j := range candidates {
			e := episodes[j]
			if used[e.key] || !compatibleEpisodeIDs(e.ids, rows[i].IDs) {
				continue
			}
			if key != 0 {
				return
			}
			key = e.key
		}
		if key != 0 {
			assigned[i], used[key] = key, true
		}
	}
	for i, row := range rows {
		key, err := sourceIdentity(ctx, tx, core.KindEpisode, order.Source, row.Ref.ID)
		if err == nil {
			if used[key] {
				return fmt.Errorf("catalog: episode repeated across provider season coordinates")
			}
			assigned[i], used[key] = key, true
			providerKeys[i] = key
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	for i, row := range rows {
		for _, item := range episodeFacts(row.IDs, "", "") {
			match(i, facts[item])
		}
	}
	for i, row := range rows {
		for _, item := range episodeFacts(core.IDs{}, row.AirDate, row.Title) {
			match(i, facts[item])
		}
	}
	for i, row := range rows {
		air := row.Aired
		if air == nil && order.Kind == core.OrderAired {
			air = &core.EpisodeNumber{Season: row.Season, Episode: row.Episode}
		}
		if air != nil {
			match(i, pos[*air])
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_order_episodes WHERE show_key=? AND provider=? AND order_kind=? AND season=?`, show.Key, order.Source, order.Kind, season.Season); err != nil {
		return err
	}
	now := c.now().Unix()
	episodeByKey := make(map[core.RatingKey]matchEpisode, len(episodes))
	for _, e := range episodes {
		episodeByKey[e.key] = e
	}
	for i, row := range rows {
		preferred := assigned[i]
		if preferred == 0 && order.Source == core.MetadataTMDB && row.Aired != nil && !show.Key.Allocated() &&
			show.IDs.TMDB > 0 && show.IDs.TMDB < 100_000_000 && episodeKeyFits(row.Aired.Season, row.Aired.Episode) {
			preferred = core.EpisodeKey(show.IDs.TMDB, row.Aired.Season, row.Aired.Episode)
		}
		key := providerKeys[i]
		if key == 0 {
			key, err = putSourceIdentity(ctx, tx, core.KindEpisode, order.Source, row.Ref.ID, preferred, now)
			if err != nil {
				return err
			}
		}
		if previous, ok := episodeByKey[key]; ok {
			ids := mergeSourceIDs(previous.ids, row.IDs)
			if ids != previous.ids {
				_, err = tx.ExecContext(ctx, `UPDATE catalog_titles SET tmdb_id=?,tvdb=?,imdb=? WHERE key=?`, ids.TMDB, ids.TVDB, ids.IMDB, key)
				if err != nil {
					return err
				}
			}
		} else {
			sn, en := row.Season, row.Episode
			if row.Aired != nil {
				sn, en = row.Aired.Season, row.Aired.Episode
			}
			t := core.Title{Key: key, Type: core.TypeEpisode, IDs: row.IDs, ShowKey: show.Key, ShowIDs: show.IDs, ShowTitle: show.Title, SeasonTitle: season.Title,
				Season: sn, Episode: en, Title: row.Title, Summary: row.Summary, StillPath: row.StillPath, Released: metadataDate(row.AirDate), Runtime: time.Duration(row.RuntimeMinutes) * time.Minute}
			if err := upsertOne(ctx, tx, t, upsertOpts{detail: true}, now); err != nil {
				return err
			}
		}
		if key.Allocated() {
			sn, en := row.Season, row.Episode
			if row.Aired != nil {
				sn, en = row.Aired.Season, row.Aired.Episode
			}
			if err := saveAllocatedAncestry(ctx, tx, key, show.Key, season.key, sn, en); err != nil {
				return err
			}
		}

		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_order_episodes(show_key,provider,order_kind,season,episode,episode_key,absolute,air_date,title,summary,still,runtime_min) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			show.Key, order.Source, order.Kind, row.Season, row.Episode, key, row.Absolute, row.AirDate, row.Title, row.Summary, row.StillPath, row.RuntimeMinutes)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_order_seasons SET fetched_at=?,episode_count=?,air_date=CASE WHEN ?<>'' THEN ? ELSE air_date END
 WHERE show_key=? AND provider=? AND order_kind=? AND season=?`, now, len(rows), firstAir, firstAir, show.Key, order.Source, order.Kind, season.Season)
	if err != nil {
		return err
	}
	return c.commitOrderWrite(tx)
}

type episodeMatchFact struct {
	kind  int
	value string
}

func episodeFacts(ids core.IDs, date, name string) []episodeMatchFact {
	var facts []episodeMatchFact
	if ids.TMDB > 0 {
		facts = append(facts, episodeMatchFact{1, strconv.FormatInt(ids.TMDB, 10)})
	}
	if ids.TVDB > 0 {
		facts = append(facts, episodeMatchFact{2, strconv.FormatInt(ids.TVDB, 10)})
	}
	if ids.IMDB != "" {
		facts = append(facts, episodeMatchFact{3, ids.IMDB})
	}
	if normalized := releaseparse.NormalizeTitle(name); date != "" && normalized != "" {
		facts = append(facts, episodeMatchFact{4, date + " " + normalized})
	}
	return facts
}

func compatibleEpisodeIDs(a, b core.IDs) bool {
	return (a.TMDB == 0 || b.TMDB == 0 || a.TMDB == b.TMDB) &&
		(a.TVDB == 0 || b.TVDB == 0 || a.TVDB == b.TVDB) &&
		(a.IMDB == "" || b.IMDB == "" || a.IMDB == b.IMDB)
}

func orderedSeasonTitle(show *core.Title, order core.EpisodeOrder, row orderSeason) core.Title {
	date := metadataDate(row.AirDate)
	t := core.Title{Key: row.key, Type: core.TypeSeason, ShowKey: show.Key, ShowIDs: show.IDs, ShowTitle: show.Title, ParentKey: show.Key,
		Season: row.Season, Title: row.Title, SeasonTitle: row.Title, Summary: row.Summary, PosterPath: row.PosterPath, Released: date, ChildCount: max(row.EpisodeCount, 0), LeafCount: max(row.EpisodeCount, 0), EpisodeOrder: order}
	id, _ := strconv.ParseInt(row.Ref.ID, 10, 64)
	switch order.Source {
	case core.MetadataTMDB:
		t.IDs.TMDB = id
	case core.MetadataTVDB:
		t.IDs.TVDB = id
	}

	if !date.IsZero() {
		t.Year = date.Year()
	}
	t.ContentRating, t.ContentRatingSource, t.Rating = show.ContentRating, show.ContentRatingSource, show.Rating
	t.Adult, t.ContentRatings, t.EnforcementRating = show.Adult, show.ContentRatings, show.EnforcementRating
	t.ShowYear, t.ShowOriginalTitle, t.BackdropPath = show.Year, show.OriginalTitle, show.BackdropPath
	t.Genres, t.Studios, t.Countries = show.Genres, show.Studios, show.Countries
	return t
}

func orderedSeason(seasons []orderSeason, n int) (orderSeason, bool) {
	i := sort.Search(len(seasons), func(i int) bool { return seasons[i].Season >= n })
	if i < len(seasons) && seasons[i].Season == n {
		return seasons[i], true
	}
	return orderSeason{}, false
}

// The first established ancestry remains available after coordinate changes.
// Effective numbering is read separately from the selected layout.
func saveAllocatedAncestry(ctx context.Context, tx *sql.Tx, key, show, parent core.RatingKey, season, episode int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_allocated_child(key,show_key,parent_key,kind,season,episode)
 VALUES (?,?,?,?,?,?) ON CONFLICT(key) DO NOTHING`, key, show, parent, key.Kind(), season, episode)
	return err
}
