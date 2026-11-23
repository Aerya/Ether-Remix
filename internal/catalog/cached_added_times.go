package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
)

// airAddedSQL is the owner date-added timestamp. Movies and episodes use
// their own release or air date. A series uses its latest episode aired at
// or before the bound time, then its first-air date. A season uses the
// latest episode aired in that season, then the season premiere. Episodes
// with no air date or a future air date do not count. Catalogue insertion
// (added_at), metadata refresh (fetched_at), publication and observation
// times are not date added. All five placeholders bind the same Unix time.
const airAddedSQL = `COALESCE(CASE WHEN t.type='show' THEN COALESCE((SELECT e.released_ts FROM catalog_titles e WHERE e.type='episode' AND e.show_key=t.key AND e.release_known=1 AND e.released_ts<=? ORDER BY e.released_ts DESC LIMIT 1), CASE WHEN t.release_known=1 AND t.released_ts<=? THEN t.released_ts END) WHEN t.type='season' THEN COALESCE((SELECT e.released_ts FROM catalog_titles e WHERE e.type='episode' AND e.show_key=t.show_key AND e.season=t.season AND e.release_known=1 AND e.released_ts<=? ORDER BY e.released_ts DESC LIMIT 1), CASE WHEN t.release_known=1 AND t.released_ts<=? THEN t.released_ts END) WHEN t.type IN ('movie','episode') AND t.release_known=1 AND t.released_ts<=? THEN t.released_ts END,0)`

func airAddedArgs(now time.Time) []any {
	unix := now.Unix()
	return []any{unix, unix, unix, unix, unix}
}

// CachedAddedTimes reads date added without fetching or resolving metadata.
// Missing, unknown and future dates are omitted. A later aired episode
// replaces a series or season premiere; an unaired episode does not.
func (c *Catalog) CachedAddedTimes(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]time.Time, error) {
	return cachedAirAddedTimes(ctx, c.db, keys, c.now(), c.WriteGeneration())
}

type airAddedMemo struct {
	mu     sync.Mutex
	loaded map[core.RatingKey]bool
	times  map[core.RatingKey]time.Time
}

func cachedAirAddedTimes(ctx context.Context, db *sql.DB, keys []core.RatingKey, now time.Time, generation ...uint64) (map[core.RatingKey]time.Time, error) {
	result := make(map[core.RatingKey]time.Time)
	if len(keys) == 0 {
		return result, nil
	}
	unique := make([]core.RatingKey, 0, len(keys))
	seen := make(map[core.RatingKey]bool, len(keys))
	for _, key := range keys {
		if !seen[key] {
			seen[key] = true
			unique = append(unique, key)
		}
	}
	if reqmemo.Active(ctx) {
		memo, err := reqmemo.Do(ctx, fmt.Sprintf("catalog:air-added-memo:%p:%v:%d", db, generation, now.Unix()), func() (*airAddedMemo, error) {
			return &airAddedMemo{
				loaded: map[core.RatingKey]bool{},
				times:  map[core.RatingKey]time.Time{},
			}, nil
		})
		if err != nil {
			return nil, err
		}
		var missing []core.RatingKey
		memo.mu.Lock()
		for _, k := range unique {
			if !memo.loaded[k] {
				missing = append(missing, k)
			}
		}
		memo.mu.Unlock()
		if len(missing) > 0 {
			sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
			fetched, err := queryAirAddedTimes(ctx, db, missing, now)
			if err != nil {
				return nil, err
			}
			memo.mu.Lock()
			for _, k := range missing {
				memo.loaded[k] = true
				if ts, ok := fetched[k]; ok {
					memo.times[k] = ts
				}
			}
			memo.mu.Unlock()
		}
		memo.mu.Lock()
		for _, k := range unique {
			if ts, ok := memo.times[k]; ok {
				result[k] = ts
			}
		}
		memo.mu.Unlock()
		return result, nil
	}
	return queryAirAddedTimes(ctx, db, unique, now)
}

func queryAirAddedTimes(ctx context.Context, db *sql.DB, keys []core.RatingKey, now time.Time) (map[core.RatingKey]time.Time, error) {
	result := make(map[core.RatingKey]time.Time)
	const chunk = 400
	for start := 0; start < len(keys); start += chunk {
		end := min(start+chunk, len(keys))
		args := airAddedArgs(now)
		for _, key := range keys[start:end] {
			args = append(args, int64(key))
		}
		rows, err := db.QueryContext(ctx, `SELECT t.key,`+airAddedSQL+` FROM catalog_titles t WHERE t.key IN (`+placeholders(end-start)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key, added int64
			if err := rows.Scan(&key, &added); err != nil {
				rows.Close()
				return nil, err
			}
			if added != 0 {
				result[core.RatingKey(key)] = time.Unix(added, 0).UTC()
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func applyAirAddedTimes(ctx context.Context, db *sql.DB, items []core.Title, now time.Time, generation ...uint64) error {
	// Movies and episodes already carry the exact date used by airAddedSQL.
	// Only containers need another query to find their latest aired child.
	keys := make([]core.RatingKey, 0, len(items))
	for i := range items {
		if items[i].Type == core.TypeMovie || items[i].Type == core.TypeEpisode {
			items[i].AddedAt = time.Time{}
			if released := items[i].Released; !released.IsZero() && released.Unix() != 0 && released.Unix() <= now.Unix() {
				items[i].AddedAt = time.Unix(released.Unix(), 0).UTC()
			}
		} else {
			keys = append(keys, items[i].Key)
		}
	}
	times, err := cachedAirAddedTimes(ctx, db, keys, now, generation...)
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].Type != core.TypeMovie && items[i].Type != core.TypeEpisode {
			items[i].AddedAt = times[items[i].Key]
		}
	}
	return nil
}
