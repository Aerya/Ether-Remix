package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
	"sort"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type deferAiredCountsKey struct{}

type airedSeasonID struct {
	show   core.RatingKey
	season int
}

type showAired struct{ seasons, episodes int }

type airedResult struct {
	seasons map[airedSeasonID]seasonAired
	shows   map[core.RatingKey]showAired
}

type airedMemo struct {
	mu      sync.Mutex
	loaded  map[core.RatingKey]bool
	seasons map[airedSeasonID]seasonAired
	shows   map[core.RatingKey]showAired
}

// seasonAired is what the mirror knows about one season's aired episodes.
type seasonAired struct {
	date       int64 // zero means unknown; negative values are historical dates
	released   bool  // the season's own air date is known and not in the future
	stored     int   // TMDB episode_count from the season summary
	mirrored   bool  // the season's episodes are in the mirror
	airedCount int   // mirrored episodes with a known, past air date
}

// count is the season's aired episodes: exact once its episodes are
// mirrored, else TMDB's episode count for a season that has premiered.
// A season without its own past air date counts nothing, as it is not
// listed (Children filters it the same way).
func (s seasonAired) count() int {
	if !s.released {
		return 0
	}
	if s.mirrored {
		return s.airedCount
	}
	return s.stored
}

// applyAiredCounts replaces the child and leaf counts of shows and seasons,
// which TMDB reports including announced but unaired episodes, with the
// episodes viewers can actually list and play. It is applied when titles are
// read rather than stored, so an episode moves into the counts the moment
// its air date passes, without waiting for a refresh. A show the mirror
// holds no seasons for keeps its stored counts.
func applyAiredCounts(ctx context.Context, db *sql.DB, now time.Time, items []*core.Title, generation ...uint64) error {
	if deferred, _ := ctx.Value(deferAiredCountsKey{}).(bool); deferred {
		return nil
	}
	var showKeys []core.RatingKey
	seen := map[core.RatingKey]bool{}
	for _, t := range items {
		var show core.RatingKey
		switch t.Type {
		case core.TypeShow:
			show = t.Key
		case core.TypeSeason:
			show = core.ShowKey(t.Key.TMDB())
		default:
			continue
		}
		if show.Kind() == core.KindShow && !seen[show] {
			seen[show] = true
			showKeys = append(showKeys, show)
		}
	}
	if len(showKeys) == 0 {
		return nil
	}
	sort.Slice(showKeys, func(i, j int) bool { return showKeys[i] < showKeys[j] })

	// The same show is hydrated through several rows on home, queue and postplay
	// requests with overlapping subsets of shows. Share tallies per show for
	// this short-lived request read so subsets never repeat the season/episode scans.
	if reqmemo.Active(ctx) {
		memo, err := reqmemo.Do(ctx, fmt.Sprintf("catalog:aired-memo:%p:%v:%d", db, generation, now.Unix()), func() (*airedMemo, error) {
			return &airedMemo{
				loaded:  map[core.RatingKey]bool{},
				seasons: map[airedSeasonID]seasonAired{},
				shows:   map[core.RatingKey]showAired{},
			}, nil
		})
		if err != nil {
			return err
		}
		var missing []core.RatingKey
		memo.mu.Lock()
		for _, k := range showKeys {
			if !memo.loaded[k] {
				missing = append(missing, k)
			}
		}
		memo.mu.Unlock()
		if len(missing) > 0 {
			res, err := queryAiredCounts(ctx, db, now, missing)
			if err != nil {
				return err
			}
			memo.mu.Lock()
			for _, k := range missing {
				memo.loaded[k] = true
			}
			for id, s := range res.seasons {
				memo.seasons[id] = s
			}
			for k, sh := range res.shows {
				memo.shows[k] = sh
			}
			memo.mu.Unlock()
		}
		memo.mu.Lock()
		applyAiredResult(items, memo.seasons, memo.shows)
		memo.mu.Unlock()
		return nil
	}

	res, err := queryAiredCounts(ctx, db, now, showKeys)
	if err != nil {
		return err
	}
	applyAiredResult(items, res.seasons, res.shows)
	return nil
}

func queryAiredCounts(ctx context.Context, db *sql.DB, now time.Time, showKeys []core.RatingKey) (airedResult, error) {
	showArgs := make([]any, len(showKeys))
	for i, k := range showKeys {
		showArgs[i] = int64(k)
	}
	seasons := map[airedSeasonID]*seasonAired{}
	hasSeasonRows := map[core.RatingKey]bool{}
	var order []airedSeasonID
	nowTS := now.Unix()
	// Chunk the deduplicated showArgs (same 200 as hydrateSlice): a single
	// IN (...) over a large slice overflows SQLite's bound-variable limit.
	// Each show lands in exactly one chunk, so seasons/order and the
	// per-season GROUP BY aggregates accumulate exactly as unchunked.
	const batch = 200
	for chunk := range store.Chunks(showArgs, batch) {
		part := chunk
		rows, err := db.QueryContext(ctx, `SELECT show_key,season,released_ts,child_count FROM catalog_titles WHERE `+airedTypeColumn(db)+`='season' AND show_key IN (`+placeholders(len(part))+`)`, part...)
		if err != nil {
			return airedResult{}, err
		}
		for rows.Next() {
			var show, released int64
			var season, child int
			if err := rows.Scan(&show, &season, &released, &child); err != nil {
				rows.Close()
				return airedResult{}, err
			}
			hasSeasonRows[core.RatingKey(show)] = true
			if !episodeKeyFits(season, 0) {
				continue
			}
			id := airedSeasonID{core.RatingKey(show), season}
			// Negative Unix timestamps are valid pre-1970 air dates; only zero is unknown.
			seasons[id] = &seasonAired{date: released, released: released != 0 && released <= nowTS, stored: child}
			order = append(order, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return airedResult{}, err
		}
	}
	for chunk := range store.Chunks(showArgs, batch) {
		part := chunk
		// COUNT(CASE ...) rather than SUM so both SQLite and PostgreSQL return
		// an integer.
		rows, err := db.QueryContext(ctx, `SELECT show_key,season,COUNT(*),COUNT(CASE WHEN release_known=1 AND released_ts<=? AND adult=0 THEN 1 END),
			COALESCE(MIN(CASE WHEN release_known=1 AND released_ts<=? AND adult=0 THEN released_ts END),0)
			FROM catalog_titles WHERE `+airedTypeColumn(db)+`='episode' AND show_key IN (`+placeholders(len(part))+`) GROUP BY show_key,season`, append([]any{nowTS, nowTS}, part...)...)
		if err != nil {
			return airedResult{}, err
		}
		for rows.Next() {
			var show int64
			var earliest int64
			var season, total, aired int
			if err := rows.Scan(&show, &season, &total, &aired, &earliest); err != nil {
				rows.Close()
				return airedResult{}, err
			}
			id := airedSeasonID{core.RatingKey(show), season}
			s := seasons[id]
			if s == nil && !hasSeasonRows[core.RatingKey(show)] && aired > 0 && episodeKeyFits(season, 0) {
				s = &seasonAired{date: earliest, released: earliest != 0 && earliest <= nowTS}
				seasons[id] = s
				order = append(order, id)
			}
			if s != nil && total > 0 {
				s.mirrored, s.airedCount = true, aired
				// Seasons can lack a premiere. A dated aired child is
				// sufficient to open this container, but never overrides a future date.
				if s.date == 0 && earliest != 0 {
					s.date, s.released = earliest, true
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return airedResult{}, err
		}
	}
	shows := map[core.RatingKey]showAired{}
	seasonVals := make(map[airedSeasonID]seasonAired, len(seasons))
	for _, id := range order {
		s := *seasons[id]
		seasonVals[id] = s
		sh := shows[id.show]
		// Show totals keep TMDB's meaning (number_of_seasons/episodes):
		// regular seasons only. Specials are listed, but "3 seasons" and
		// a limited series' single season must not count them.
		if n := s.count(); n > 0 && id.season > 0 {
			sh.seasons++
			sh.episodes += n
		}
		shows[id.show] = sh
	}
	return airedResult{seasons: seasonVals, shows: shows}, nil
}

func applyAiredResult(items []*core.Title, seasons map[airedSeasonID]seasonAired, shows map[core.RatingKey]showAired) {
	for _, t := range items {
		switch t.Type {
		case core.TypeShow:
			if sh, ok := shows[t.Key]; ok {
				t.ChildCount, t.LeafCount = sh.seasons, sh.episodes
			}
		case core.TypeSeason:
			if s, ok := seasons[airedSeasonID{core.ShowKey(t.Key.TMDB()), t.Key.Season()}]; ok {
				t.ChildCount = s.count()
				if t.Released.IsZero() && s.date != 0 {
					t.Released = time.Unix(s.date, 0).UTC()
				}
			}
		}
	}
}

// airedCounts is applyAiredCounts for a slice of catalogue titles.
func (c *Catalog) airedCounts(ctx context.Context, items []core.Title) error {
	ptrs := make([]*core.Title, 0, len(items))
	for i := range items {
		if items[i].Type == core.TypeShow || items[i].Type == core.TypeSeason {
			ptrs = append(ptrs, &items[i])
		}
	}
	if len(ptrs) == 0 {
		return nil
	}
	return applyAiredCounts(ctx, c.db, c.now(), ptrs, c.WriteGeneration())
}

// listableSeason reports whether a season belongs in listings and navigation:
// its own air date has passed and at least one of its episodes has aired.
// t must carry aired counts (applyAiredCounts).
func listableSeason(t *core.Title, now time.Time) bool {
	return t.IsReleased(now) && t.ChildCount > 0
}

// SQLite's type-leading indexes can walk every episode in the library to
// tally one show. Disqualify that leading term so the show/season index
// supplies the bounded lookup. PostgreSQL retains its ordinary predicate.
func airedTypeColumn(db *sql.DB) string {
	if store.DialectOf(db) == store.Postgres {
		return "type"
	}
	return "+type"
}
