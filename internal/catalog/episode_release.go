package catalog

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("catalog_episode_release", "catalog")
	store.Register("catalog_episode_release", 1, `CREATE TABLE catalog_episode_release(key BIGINT PRIMARY KEY, earliest_ts BIGINT NOT NULL);`)
}

// RecordPlayableEpisode remembers actual playable evidence independently of
// metadata refreshes. released_ts is the earliest known effective release date;
// the evidence survives a later linear air date returned by the metadata source.
func (c *Catalog) RecordPlayableEpisode(ctx context.Context, key core.RatingKey, at time.Time) error {
	if key.Kind() != core.KindEpisode || at.IsZero() {
		return nil
	}
	return c.RecordPlayableEpisodes(ctx, []core.RatingKey{key}, at)
}

// RecordPlayableEpisodes records playable evidence for a batch of episodes (and
// their parent season containers) in a single transaction using bounded
// set-based SQL statements instead of one transaction per episode.
func (c *Catalog) RecordPlayableEpisodes(ctx context.Context, keys []core.RatingKey, at time.Time) error {
	if len(keys) == 0 || at.IsZero() {
		return nil
	}
	seen := make(map[core.RatingKey]bool, len(keys)*2)
	targets := make([]core.RatingKey, 0, len(keys)*2)
	addTarget := func(k core.RatingKey) {
		if k > 0 && !seen[k] {
			seen[k] = true
			targets = append(targets, k)
		}
	}
	var episodes []core.RatingKey
	for _, key := range keys {
		if key.Kind() == core.KindEpisode && !seen[key] {
			addTarget(key)
			episodes = append(episodes, key)
		}
	}
	stored, err := c.releaseEpisodeRows(ctx, episodes)
	if err != nil {
		return err
	}
	if err := c.ProjectEpisodeTitles(ctx, stored); err != nil {
		return err
	}
	parents := make(map[core.RatingKey]core.RatingKey, len(stored))
	for _, episode := range stored {
		parents[episode.Key] = episode.ParentKey
	}
	for _, key := range episodes {
		parent := parents[key]
		if parent == 0 {
			parent = key.Parent()
		}
		addTarget(parent)
	}
	if len(targets) == 0 {
		return nil
	}

	atTS := at.Unix()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const batch = 500
	for chunk := range store.Chunks(targets, batch) {
		var sb strings.Builder
		sb.Grow(64 + len(chunk)*6)
		sb.WriteString(`INSERT INTO catalog_episode_release(key,earliest_ts) VALUES `)
		insertArgs := make([]any, 0, len(chunk)*2)
		updateArgs := make([]any, len(chunk))
		for i, k := range chunk {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?)")
			insertArgs = append(insertArgs, int64(k), atTS)
			updateArgs[i] = int64(k)
		}
		sb.WriteString(` ON CONFLICT(key) DO UPDATE SET earliest_ts=CASE WHEN excluded.earliest_ts<catalog_episode_release.earliest_ts THEN excluded.earliest_ts ELSE catalog_episode_release.earliest_ts END`)
		if _, err := tx.ExecContext(ctx, sb.String(), insertArgs...); err != nil {
			return err
		}
		// release_known is a generated column (released_ts <> 0), so updating
		// released_ts automatically marks previously undated rows as known.
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET released_ts=(SELECT earliest_ts FROM catalog_episode_release e WHERE e.key=catalog_titles.key),browsable=1 WHERE key IN (`+placeholders(len(chunk))+`) AND EXISTS(SELECT 1 FROM catalog_episode_release e WHERE e.key=catalog_titles.key AND (catalog_titles.released_ts=0 OR catalog_titles.release_known=0 OR e.earliest_ts<catalog_titles.released_ts))`, updateArgs...); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	c.writeGen.Add(1)
	return nil
}

func applyEpisodeRelease(ctx context.Context, tx *sql.Tx, key core.RatingKey) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET released_ts=(SELECT earliest_ts FROM catalog_episode_release WHERE key=?),browsable=1 WHERE key=? AND EXISTS(SELECT 1 FROM catalog_episode_release e WHERE e.key=catalog_titles.key AND (catalog_titles.released_ts=0 OR catalog_titles.release_known=0 OR e.earliest_ts<catalog_titles.released_ts))`, int64(key), int64(key))
	return err
}

// ResolveEpisode resolves a frontend coordinate to its stable episode key.
func (c *Catalog) ResolveEpisode(ctx context.Context, show core.RatingKey, season, episode int) (core.RatingKey, error) {
	coordinate, err := c.ResolveEpisodeAt(ctx, show, season, episode)
	return coordinate.Key, err
}

func (c *Catalog) releaseEpisodeRows(ctx context.Context, keys []core.RatingKey) ([]core.Title, error) {
	var out []core.Title
	for chunk := range store.Chunks(keys, 200) {
		args := make([]any, len(chunk))
		for i, key := range chunk {
			args[i] = key
		}
		rows, err := c.queryTitlesUnfiltered(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE type='episode' AND key IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func layoutSeasonEpisodes(layout EpisodeLayout, number int) []EpisodeCoordinate {
	start := sort.Search(len(layout.Episodes), func(i int) bool { return layout.Episodes[i].Season >= number })
	episodes := layout.Episodes[start:]
	end := sort.Search(len(episodes), func(i int) bool { return episodes[i].Season > number })
	return episodes[:end]
}

func (c *Catalog) releaseTitlesForCoordinates(ctx context.Context, layout EpisodeLayout, coords []EpisodeCoordinate) ([]core.Title, error) {
	keys := make([]core.RatingKey, 0, len(coords))
	for _, ep := range coords {
		keys = append(keys, ep.Key)
	}
	rows, err := c.releaseEpisodeRows(ctx, keys)
	if err != nil {
		return nil, err
	}
	var knownAirDates []core.RatingKey
	now := c.now()
	for _, ep := range coords {
		if !ep.AirDate.IsZero() && ep.AirDate.After(now) {
			knownAirDates = append(knownAirDates, ep.Key)
		}
	}
	evidence, err := c.PlayableEpisodeReleases(ctx, knownAirDates)
	if err != nil {
		return nil, err
	}
	index := make(map[core.RatingKey]core.Title, len(rows))
	for _, t := range rows {
		index[t.Key] = t
	}
	var out []core.Title
	for _, ep := range coords {
		t, ok := index[ep.Key]
		if !ok {
			continue
		}
		t.EpisodeOrder, t.EpisodeAirDate = layout.Order, ep.AirDate
		t.ParentKey, t.Season, t.Episode, t.AbsoluteEpisode = ep.ParentKey, ep.Season, ep.Episode, ep.Absolute
		if !ep.AirDate.IsZero() {
			t.Released = ep.AirDate
			if at := evidence[ep.Key]; !at.IsZero() && at.Before(t.Released) {
				t.Released = at
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// SeasonEpisodesAll returns mirrored regular episodes in effective order,
// including unaired episodes, without contacting metadata providers.
func (c *Catalog) SeasonEpisodesAll(ctx context.Context, seasonKey core.RatingKey) ([]core.Title, error) {
	if seasonKey.Kind() != core.KindSeason {
		return nil, nil
	}
	parent, err := c.StoredTitle(ctx, seasonKey)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	showKey := parent.ShowKey
	if showKey == 0 {
		showKey = seasonKey.Parent()
	}
	layout, ok, err := c.CachedEpisodeLayout(ctx, showKey)
	if err != nil || !ok {
		return nil, err
	}
	for number, key := range layout.Seasons {
		if key == seasonKey && number > 0 {
			return c.releaseTitlesForCoordinates(ctx, layout, layoutSeasonEpisodes(layout, number))
		}
	}
	return nil, nil
}

// LatestStartedSeason returns the highest regular effective season with an
// aired episode or verified playable evidence.
func (c *Catalog) LatestStartedSeason(ctx context.Context, showKey core.RatingKey) (core.RatingKey, error) {
	if showKey.Kind() != core.KindShow {
		return 0, nil
	}
	layout, ok, err := c.CachedEpisodeLayout(ctx, showKey)
	if err != nil || !ok {
		return 0, err
	}
	now := c.now()
	started := 0
	var pending []core.RatingKey
	for _, ep := range layout.Episodes {
		if ep.Season > started && !ep.AirDate.IsZero() && !ep.AirDate.After(now) {
			started = ep.Season
		}
	}
	for _, ep := range layout.Episodes {
		if ep.Season > started {
			pending = append(pending, ep.Key)
		}
	}
	evidence, err := c.PlayableEpisodeReleases(ctx, pending)
	if err != nil {
		return 0, err
	}
	for _, ep := range layout.Episodes {
		if at := evidence[ep.Key]; !at.IsZero() && !at.After(now) && ep.Season > started {
			started = ep.Season
		}
	}
	if started == 0 {
		return 0, nil
	}
	return layout.Seasons[started], nil
}

// PlayableEpisodeReleases returns recorded earliest playable timestamps for keys.
func (c *Catalog) PlayableEpisodeReleases(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]time.Time, error) {
	out := make(map[core.RatingKey]time.Time, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	const batch = 500
	for chunk := range store.Chunks(keys, batch) {
		args := make([]any, len(chunk))
		for i, k := range chunk {
			args[i] = int64(k)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT key,earliest_ts FROM catalog_episode_release WHERE key IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k, ts int64
			if err := rows.Scan(&k, &ts); err != nil {
				rows.Close()
				return nil, err
			}
			if ts > 0 {
				out[core.RatingKey(k)] = time.Unix(ts, 0).UTC()
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}
