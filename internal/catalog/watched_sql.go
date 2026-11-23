package catalog

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// watchedShowKeysSQL returns a SELECT yielding a single column `key` of every
// show rating key that userID has fully watched as of nowTS.
//
// The candidate set is driven strictly from userID's own played rows in
// state_watch (indexed by (user_id, rating_key) / state_watch_played), never
// by scanning catalog_titles or walking episodes per show:
//   - A show with aired regular (season > 0) episodes stored in catalog_titles
//     is watched iff every such episode has view_count > 0 for userID and the
//     show has no unhydrated aired regular season (season > 0, child_count > 0,
//     released, with no stored episode rows).
//   - A show with no stored aired regular episodes falls back to its own
//     state_watch row (provided it has no unhydrated aired regular season),
//     preserving unhydrated show marks while keeping all untouched shows
//     unwatched.
//
// Specials (season = 0) and unaired episodes do not keep a show unwatched.
func (c *Catalog) watchedShowKeysSQL(userID int64) (string, []any) {
	nowTS := c.now().Unix()
	const sqlText = `SELECT cand.key FROM (` +
		`SELECT DISTINCT 2000000000000000+(w.rating_key-4000000000000000)/10000000 AS key, 1 AS has_ep ` +
		`FROM state_watch w ` +
		`WHERE w.user_id=? AND w.view_count>0 ` +
		`AND w.rating_key>=4000000000000000 AND w.rating_key<5000000000000000 ` +
		`AND ((w.rating_key-4000000000000000)/10000)%1000>0 ` +
		`UNION ` +
		`SELECT w.rating_key AS key, 0 AS has_ep ` +
		`FROM state_watch w ` +
		`WHERE w.user_id=? AND w.view_count>0 ` +
		`AND w.rating_key>=2000000000000000 AND w.rating_key<3000000000000000 ` +
		`AND NOT EXISTS (` +
		`SELECT 1 FROM catalog_titles e ` +
		`WHERE e.show_key=w.rating_key AND e.type='episode' AND e.season>0 ` +
		`AND e.adult=0 AND e.release_known=1 AND e.released_ts<=?` +
		`)` +
		`) cand ` +
		`WHERE (cand.has_ep=0 OR EXISTS (` +
		`SELECT 1 FROM catalog_titles e ` +
		`WHERE e.show_key=cand.key AND e.type='episode' AND e.season>0 ` +
		`AND e.adult=0 AND e.release_known=1 AND e.released_ts<=?` +
		`)) ` +
		`AND NOT EXISTS (` +
		`SELECT 1 FROM catalog_titles e ` +
		`WHERE e.show_key=cand.key AND e.type='episode' AND e.season>0 ` +
		`AND e.adult=0 AND e.release_known=1 AND e.released_ts<=? ` +
		`AND NOT EXISTS (` +
		`SELECT 1 FROM state_watch w2 ` +
		`WHERE w2.user_id=? AND w2.rating_key=e.key AND w2.view_count>0` +
		`)` +
		`) ` +
		`AND NOT EXISTS (` +
		`SELECT 1 FROM catalog_titles sn ` +
		`WHERE sn.show_key=cand.key AND sn.type='season' AND sn.season>0 ` +
		`AND sn.adult=0 AND sn.child_count>0 AND sn.release_known=1 AND sn.released_ts<=? ` +
		`AND NOT EXISTS (` +
		`SELECT 1 FROM catalog_titles se ` +
		`WHERE se.show_key=cand.key AND se.season=sn.season AND `
	sqlFull := sqlText + airedTypeColumn(c.db) + `='episode'))`
	return sqlFull, []any{userID, userID, nowTS, nowTS, nowTS, userID, nowTS}
}

// watchedSeasonKeysSQL returns a SELECT yielding a single column `key` of every
// season rating key that userID has fully watched as of nowTS.
func (c *Catalog) watchedSeasonKeysSQL(userID int64) (string, []any) {
	nowTS := c.now().Unix()
	const sqlText = `SELECT cand.key FROM (` +
		`SELECT DISTINCT 3000000000000000+((w.rating_key-4000000000000000)/10000000)*10000+(((w.rating_key-4000000000000000)/10000)%1000) AS key, ` +
		`2000000000000000+(w.rating_key-4000000000000000)/10000000 AS show_key, ` +
		`((w.rating_key-4000000000000000)/10000)%1000 AS season_num, ` +
		`1 AS has_ep ` +
		`FROM state_watch w ` +
		`WHERE w.user_id=? AND w.view_count>0 ` +
		`AND w.rating_key>=4000000000000000 AND w.rating_key<5000000000000000 ` +
		`UNION ` +
		`SELECT w.rating_key AS key, ` +
		`2000000000000000+(w.rating_key-3000000000000000)/10000 AS show_key, ` +
		`(w.rating_key-3000000000000000)%10000 AS season_num, ` +
		`0 AS has_ep ` +
		`FROM state_watch w ` +
		`WHERE w.user_id=? AND w.view_count>0 ` +
		`AND w.rating_key>=3000000000000000 AND w.rating_key<4000000000000000 ` +
		`AND NOT EXISTS (` +
		`SELECT 1 FROM catalog_titles e ` +
		`WHERE e.show_key=2000000000000000+(w.rating_key-3000000000000000)/10000 ` +
		`AND e.season=(w.rating_key-3000000000000000)%10000 ` +
		`AND e.type='episode' AND e.adult=0 AND e.release_known=1 AND e.released_ts<=?` +
		`)` +
		`) cand ` +
		`WHERE (cand.has_ep=0 OR EXISTS (` +
		`SELECT 1 FROM catalog_titles e ` +
		`WHERE e.show_key=cand.show_key AND e.season=cand.season_num ` +
		`AND e.type='episode' AND e.adult=0 AND e.release_known=1 AND e.released_ts<=?` +
		`)) ` +
		`AND NOT EXISTS (` +
		`SELECT 1 FROM catalog_titles e ` +
		`WHERE e.show_key=cand.show_key AND e.season=cand.season_num ` +
		`AND e.type='episode' AND e.adult=0 AND e.release_known=1 AND e.released_ts<=? ` +
		`AND NOT EXISTS (` +
		`SELECT 1 FROM state_watch w2 ` +
		`WHERE w2.user_id=? AND w2.rating_key=e.key AND w2.view_count>0` +
		`)` +
		`)`
	return sqlText, []any{userID, userID, nowTS, nowTS, nowTS, userID}
}

// watchedKeysForTypeSQL returns a SELECT yielding a single column `key` of
// titles watched by userID for a root browse filter of type typ (movie, show,
// or "" for mixed movie+show).
func (c *Catalog) watchedKeysForTypeSQL(typ core.MediaType, userID int64) (string, []any) {
	switch typ {
	case core.TypeMovie:
		return `SELECT w.rating_key AS key FROM state_watch w WHERE w.user_id=? AND w.view_count>0`, []any{userID}
	case core.TypeShow:
		return c.watchedShowKeysSQL(userID)
	default:
		showSQL, showArgs := c.watchedShowKeysSQL(userID)
		q := `SELECT w.rating_key AS key FROM state_watch w WHERE w.user_id=? AND w.view_count>0 AND NOT (w.rating_key>=2000000000000000 AND w.rating_key<4000000000000000) UNION ` + showSQL
		return q, append([]any{userID}, showArgs...)
	}
}

// watchedPredicateSQL builds the SQL predicate testing whether alias is
// watched (want=true) or unwatched (want=false) by userID.
func (c *Catalog) watchedPredicateSQL(alias string, childKind core.KeyKind, rootType core.MediaType, userID int64, want bool) (string, []any) {
	if childKind == core.KindEpisode || (childKind == 0 && rootType == core.TypeMovie) {
		if want {
			return alias + `.key IN (SELECT w.rating_key FROM state_watch w WHERE w.user_id=? AND w.view_count>0)`, []any{userID}
		}
		return `NOT EXISTS (SELECT 1 FROM state_watch w WHERE w.user_id=? AND w.rating_key=` + alias + `.key AND w.view_count>0)`, []any{userID}
	}
	var subSQL string
	var subArgs []any
	if childKind == core.KindSeason {
		subSQL, subArgs = c.watchedSeasonKeysSQL(userID)
	} else {
		subSQL, subArgs = c.watchedKeysForTypeSQL(rootType, userID)
	}
	if want {
		return alias + `.key IN (` + subSQL + `)`, subArgs
	}
	if store.DialectOf(c.db) == store.Postgres {
		// NOT EXISTS lets PostgreSQL hash-anti-join a derived subquery whose
		// computed key column the planner cannot prove NOT NULL.
		return `NOT EXISTS (SELECT 1 FROM (` + subSQL + `) ws WHERE ws.key=` + alias + `.key)`, subArgs
	}
	return alias + `.key NOT IN (` + subSQL + `)`, subArgs
}

// WatchedTitles reports which keys in keys have been fully watched by userID,
// using the exact same rules as Browse, CountWatched, and tallyTotal: movies
// and episodes check their own state_watch row; shows and seasons evaluate
// their aired episode leaves.
func (c *Catalog) WatchedTitles(ctx context.Context, userID int64, keys []core.RatingKey) (map[core.RatingKey]bool, error) {
	out := make(map[core.RatingKey]bool, len(keys))
	if c == nil || c.db == nil || userID <= 0 || len(keys) == 0 {
		return out, nil
	}
	var shows, seasons, leaves []any
	seen := make(map[core.RatingKey]bool, len(keys))
	for _, k := range keys {
		if k == 0 || seen[k] {
			continue
		}
		seen[k] = true
		switch k.Kind() {
		case core.KindShow:
			shows = append(shows, int64(k))
		case core.KindSeason:
			seasons = append(seasons, int64(k))
		default:
			leaves = append(leaves, int64(k))
		}
	}
	queryKeys := func(baseSQL string, baseArgs []any, filterKeys []any) error {
		for part := range store.Chunks(filterKeys, 200) {
			args := make([]any, 0, len(baseArgs)+len(part))
			args = append(args, baseArgs...)
			args = append(args, part...)
			rows, err := c.db.QueryContext(ctx, `SELECT ws.key FROM (`+baseSQL+`) ws WHERE ws.key IN (`+placeholders(len(part))+`)`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var key int64
				if err := rows.Scan(&key); err != nil {
					rows.Close()
					return err
				}
				out[core.RatingKey(key)] = true
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	if len(leaves) > 0 {
		if err := queryKeys(`SELECT w.rating_key AS key FROM state_watch w WHERE w.user_id=? AND w.view_count>0`, []any{userID}, leaves); err != nil {
			return nil, err
		}
	}
	if len(shows) > 0 {
		subSQL, subArgs := c.watchedShowKeysSQL(userID)
		if err := queryKeys(subSQL, subArgs, shows); err != nil {
			return nil, err
		}
	}
	if len(seasons) > 0 {
		subSQL, subArgs := c.watchedSeasonKeysSQL(userID)
		if err := queryKeys(subSQL, subArgs, seasons); err != nil {
			return nil, err
		}
	}
	return out, nil
}
