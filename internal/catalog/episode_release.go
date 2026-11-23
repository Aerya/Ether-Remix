package catalog

import (
	"context"
	"database/sql"
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
	for _, key := range keys {
		if key.Kind() != core.KindEpisode {
			continue
		}
		// A playable premiere also releases its season navigation container.
		addTarget(key)
		addTarget(core.SeasonKey(key.TMDB(), key.Season()))
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

// SeasonEpisodesAll returns every mirrored regular episode of seasonKey in
// episode order without filtering unreleased rows or calling metadata providers.
func (c *Catalog) SeasonEpisodesAll(ctx context.Context, seasonKey core.RatingKey) ([]core.Title, error) {
	if seasonKey.Kind() != core.KindSeason || seasonKey.Season() <= 0 || seasonKey.TMDB() <= 0 {
		return nil, nil
	}
	return c.queryTitlesUnfiltered(ctx, `SELECT `+titleCols+` FROM catalog_titles`+c.indexedBy("catalog_titles_show")+` WHERE type='episode' AND show_key=? AND season=? AND adult=0 ORDER BY episode ASC LIMIT 1000`, int64(core.ShowKey(seasonKey.TMDB())), seasonKey.Season())
}

// LatestStartedSeason returns the highest regular season of showKey that has at
// least one released episode (by metadata date or recorded playable evidence).
func (c *Catalog) LatestStartedSeason(ctx context.Context, showKey core.RatingKey) (core.RatingKey, error) {
	if showKey.Kind() != core.KindShow || showKey.TMDB() <= 0 {
		return 0, nil
	}
	nowTS := c.now().Unix()
	var season int
	err := c.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(season), 0) FROM catalog_titles`+c.indexedBy("catalog_titles_show")+` WHERE type='episode' AND show_key=? AND season>0 AND adult=0 AND ((release_known=1 AND released_ts>0 AND released_ts<=?) OR EXISTS(SELECT 1 FROM catalog_episode_release e WHERE e.key=catalog_titles.key AND e.earliest_ts<=?))`, int64(showKey), nowTS, nowTS).Scan(&season)
	if err != nil || season <= 0 {
		return 0, err
	}
	return core.SeasonKey(showKey.TMDB(), season), nil
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
