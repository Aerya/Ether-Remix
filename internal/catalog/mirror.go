package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"sort"
	"time"
)

// Mirror exposes bounded local reads for catalogue consumers. It owns schema
// knowledge; callers never compose SQL against catalog_* tables.
type Mirror struct {
	db  *sql.DB
	now func() time.Time
}

func NewMirror(db *sql.DB, now func() time.Time) *Mirror {
	if now == nil {
		now = time.Now
	}
	return &Mirror{db: db, now: now}
}

type MirrorFingerprint struct {
	Count            int
	MaxFetch, MaxAdd int64
}

func (m *Mirror) Fingerprint(ctx context.Context) (MirrorFingerprint, error) {
	var fp MirrorFingerprint
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(fetched_at),0),COALESCE(MAX(added_at),0) FROM catalog_titles t WHERE `+mirrorBrowsableWhere).Scan(&fp.Count, &fp.MaxFetch, &fp.MaxAdd)
	return fp, err
}

type MirrorItem struct {
	Title    core.Title
	AddedAt  int64
	Detailed bool
}

// mirrorBrowsableWhere is the browsable set as Browse serves it: adult
// titles are never shown, whatever the sync settings.
const mirrorBrowsableWhere = `t.type IN ('movie','show') AND t.browsable=1 AND t.adult=0`

// mirrorSnapshotChunk bounds the titles one snapshot statement reads. A
// whole-catalogue statement holds its read snapshot until it finishes, and
// while it does the WAL can neither be checkpointed past it nor restart, so
// every concurrent read searches a growing WAL index. Each key range is read
// in its own short statements instead.
const mirrorSnapshotChunk = 4000

// Snapshot reads every browsable title with its facets and credits, most
// popular first. Ranges are read one after another, so the result is not one
// point in time; the index that consumes it rebuilds when the fingerprint
// moves.
func (m *Mirror) Snapshot(ctx context.Context) ([]MirrorItem, error) {
	var items []*MirrorItem
	byKey := map[core.RatingKey]*MirrorItem{}
	for lo, done := int64(-1<<63), false; !done; {
		var hi int64
		err := m.db.QueryRowContext(ctx, `SELECT key FROM catalog_titles WHERE key>? ORDER BY key LIMIT 1 OFFSET ?`, lo, mirrorSnapshotChunk-1).Scan(&hi)
		if errors.Is(err, sql.ErrNoRows) {
			hi, done = 1<<63-1, true
		} else if err != nil {
			return nil, err
		}
		if err := m.snapshotRange(ctx, lo, hi, &items, byKey); err != nil {
			return nil, err
		}
		lo = hi
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Title.Popularity != items[j].Title.Popularity {
			return items[i].Title.Popularity > items[j].Title.Popularity
		}
		return items[i].Title.Key < items[j].Title.Key
	})
	out := make([]MirrorItem, len(items))
	for i, e := range items {
		out[i] = *e
	}
	titles := make([]core.Title, len(out))
	for i := range out {
		titles[i] = out[i].Title
	}
	if err := applyAirAddedTimes(ctx, m.db, titles, m.now()); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Title.AddedAt = titles[i].AddedAt
	}
	return out, nil
}

// snapshotRange reads the titles with lo < key <= hi and their facets.
func (m *Mirror) snapshotRange(ctx context.Context, lo, hi int64, items *[]*MirrorItem, byKey map[core.RatingKey]*MirrorItem) error {
	const in = ` AND t.key>? AND t.key<=?`
	rows, err := m.db.QueryContext(ctx, `SELECT t.key,t.type,t.title,t.sort_title,t.year,t.released_ts,t.added_at,t.runtime_sec,t.rating,t.vote_count,t.popularity,t.language,t.content_rating,t.enforcement_rating,t.collection_id,t.poster,t.backdrop,t.detail FROM catalog_titles t WHERE `+mirrorBrowsableWhere+in+` ORDER BY t.key`, lo, hi)
	if err != nil {
		return err
	}
	for rows.Next() {
		e := &MirrorItem{}
		var released, runtime int64
		t := &e.Title
		if err := rows.Scan(&t.Key, &t.Type, &t.Title, &t.SortTitle, &t.Year, &released, &e.AddedAt, &runtime, &t.Rating, &t.VoteCount, &t.Popularity, &t.Language, &t.ContentRating, &t.EnforcementRating, &t.CollectionID, &t.PosterPath, &t.BackdropPath, &e.Detailed); err != nil {
			rows.Close()
			return err
		}
		if released != 0 {
			t.Released = time.Unix(released, 0).UTC()
		}
		t.Runtime = time.Duration(runtime) * time.Second
		byKey[t.Key] = e
		*items = append(*items, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	pairs := func(query string, apply func(*core.Title, string)) error {
		rows, err := m.db.QueryContext(ctx, query, lo, hi)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k core.RatingKey
			var v string
			if err := rows.Scan(&k, &v); err != nil {
				return err
			}
			if e, ok := byKey[k]; ok {
				apply(&e.Title, v)
			}
		}
		return rows.Err()
	}
	for _, pair := range []struct {
		query string
		apply func(*core.Title, string)
	}{
		{`SELECT g.key,g.genre FROM catalog_genres g JOIN catalog_titles t ON t.key=g.key WHERE ` + mirrorBrowsableWhere + in + ` ORDER BY g.key,g.genre`, func(t *core.Title, v string) { t.Genres = append(t.Genres, v) }},
		{`SELECT g.key,g.country FROM catalog_countries g JOIN catalog_titles t ON t.key=g.key WHERE ` + mirrorBrowsableWhere + in, func(t *core.Title, v string) { t.Countries = append(t.Countries, v) }},
		{`SELECT g.key,g.studio FROM catalog_studios g JOIN catalog_titles t ON t.key=g.key WHERE ` + mirrorBrowsableWhere + in, func(t *core.Title, v string) { t.Studios = append(t.Studios, v) }},
		{`SELECT g.key,g.keyword FROM catalog_keywords g JOIN catalog_titles t ON t.key=g.key WHERE ` + mirrorBrowsableWhere + in, func(t *core.Title, v string) { t.Keywords = append(t.Keywords, v) }},
	} {
		if err := pairs(pair.query, pair.apply); err != nil {
			return err
		}
	}
	rows, err = m.db.QueryContext(ctx, `SELECT c.key,c.person_tmdb,c.kind,COALESCE(p.imdb,'') FROM catalog_credits c JOIN catalog_titles t ON t.key=c.key LEFT JOIN catalog_people p ON p.person_tmdb=c.person_tmdb WHERE `+mirrorBrowsableWhere+in, lo, hi)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key core.RatingKey
		var pid int64
		var kind, imdb string
		if err := rows.Scan(&key, &pid, &kind, &imdb); err != nil {
			return err
		}
		e, ok := byKey[key]
		if !ok {
			continue
		}
		t := &e.Title
		p := core.Person{TMDB: pid, IMDB: imdb}
		switch kind {
		case "director":
			t.Directors = append(t.Directors, p)
		case "writer":
			t.Writers = append(t.Writers, p)
		case "crew":
			t.Crew = append(t.Crew, p)
		default:
			t.Cast = append(t.Cast, p)
		}
	}
	return rows.Err()
}

func (m *Mirror) Franchises(ctx context.Context) ([]core.Collection, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT f.collection_id,
		       COALESCE(NULLIF(f.name,''), cc.name, ''),
		       f.member_count,
		       COALESCE(cc.poster,''), COALESCE(cc.backdrop,''),
		       COALESCE(cc.summary,'')
		FROM catalog_franchises f
		LEFT JOIN catalog_collections cc ON cc.tmdb_id = f.collection_id
		WHERE f.member_count>=2
		ORDER BY f.member_count DESC, f.collection_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFranchiseRows(rows)
}

// FranchiseTotal returns the number of franchises with at least two browsable
// movies in O(1) from catalog_franchise_counts.
func (m *Mirror) FranchiseTotal(ctx context.Context) (int, error) {
	return franchiseTotalDB(ctx, m.db)
}

// FranchisesPage returns one page of franchises with at least two browsable
// movies in (member_count DESC, collection_id ASC) order.
func (m *Mirror) FranchisesPage(ctx context.Context, offset, limit int) ([]core.Collection, error) {
	return franchisesPageDB(ctx, m.db, offset, limit)
}

// FranchisesCursor returns the next keyset page of franchises with at least
// two browsable movies in (member_count DESC, collection_id ASC) order.
func (m *Mirror) FranchisesCursor(ctx context.Context, afterCount, afterID, limit int) ([]core.Collection, error) {
	return franchisesCursorDB(ctx, m.db, 2, afterCount, afterID, limit)
}

// NextFranchise reads one candidate for lazy collection-view discovery. Unlike
// Franchises it does not count, rank, or materialize every franchise first.
func (m *Mirror) NextFranchise(ctx context.Context, after int64) (*core.Collection, error) {
	var id int64
	var name, poster, backdrop, summary string
	var n int
	err := m.db.QueryRowContext(ctx, `SELECT f.collection_id,
 COALESCE(NULLIF(f.name,''),cc.name,''),f.member_count,
 COALESCE(cc.poster,''),COALESCE(cc.backdrop,''),COALESCE(cc.summary,'')
 FROM catalog_franchises f LEFT JOIN catalog_collections cc ON cc.tmdb_id=f.collection_id
 WHERE f.member_count>=2 AND f.collection_id>?
 ORDER BY f.collection_id LIMIT 1`, after).Scan(&id, &name, &n, &poster, &backdrop, &summary)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = fmt.Sprintf("Collection %d", id)
	}
	return &core.Collection{Key: core.CollectionKey(id), Title: name, Summary: summary, Kind: "franchise", Type: core.TypeMovie, PosterPath: poster, BackdropPath: backdrop, ChildCount: n}, nil
}

// FranchiseRatedCountsBatch is FranchiseRatedCounts scoped to one batch of
// franchise IDs, probing catalog_titles_collection instead of scanning the
// library.
func (m *Mirror) FranchiseRatedCountsBatch(ctx context.Context, level int, collectionIDs []int) (map[int64]int, error) {
	if len(collectionIDs) == 0 {
		return nil, nil
	}
	out := make(map[int64]int, len(collectionIDs))
	for part := range store.Chunks(collectionIDs, 400) {
		args := make([]any, 0, len(part)+1)
		args = append(args, level)
		for _, id := range part {
			args = append(args, id)
		}
		rows, err := m.db.QueryContext(ctx, `SELECT collection_id, COUNT(*)
			FROM catalog_titles
			WHERE type='movie' AND browsable=1 AND adult=0
			  AND content_rating_rank>0 AND content_rating_rank<=? AND enforcement_rating<>'XXX'
			  AND collection_id IN (`+placeholders(len(part))+`)
			GROUP BY collection_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				rows.Close()
				return nil, err
			}
			if n > 0 {
				out[id] = n
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// FranchiseRatedCounts is, per franchise Franchises lists, how many of its
// browsable movies a viewer capped at level may watch (rated, at or below
// the cap; contentrating.EnforceExceeds), in one grouped read over the
// franchises' members.
func (m *Mirror) FranchiseRatedCounts(ctx context.Context, level int) (map[int64]int, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT collection_id, COUNT(CASE WHEN content_rating_rank>0 AND content_rating_rank<=? AND enforcement_rating<>'XXX' THEN 1 END)
		FROM catalog_titles WHERE type='movie' AND browsable=1 AND adult=0 AND collection_id>0
		GROUP BY collection_id HAVING COUNT(*)>=2`, level)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (m *Mirror) CollectionArt(ctx context.Context, id int64) (core.Collection, error) {
	c := core.Collection{Key: core.CollectionKey(id)}
	err := m.db.QueryRowContext(ctx, `SELECT poster,backdrop,summary FROM catalog_collections WHERE tmdb_id=?`, id).Scan(&c.PosterPath, &c.BackdropPath, &c.Summary)
	return c, err
}
