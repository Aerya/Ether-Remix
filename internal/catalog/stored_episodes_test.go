package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestStoredEpisodesByKey(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	past, future := now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := c.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []struct {
		id      int64
		adult   int
		rating  string
		rank    int
		release int64
	}{{1, 0, "TV-PG", 2, past}, {2, 1, "TV-PG", 2, past}, {3, 0, "TV-MA", 5, past}, {4, 0, "TV-PG", 2, future}} {
		exec(`INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult,content_rating,content_rating_rank) VALUES(?,'show','s','s',?,1,?,?,?)`, int64(core.ShowKey(s.id)), s.release, s.adult, s.rating, s.rank)
		for e := 1; e <= 3; e++ {
			released := past
			if e == 3 {
				released = future
			}
			exec(`INSERT INTO catalog_titles(key,type,show_key,season,episode,title,sort_title,released_ts,adult) VALUES(?,'episode',?,1,?,'e','e',?,0)`, int64(core.EpisodeKey(s.id, 1, e)), int64(core.ShowKey(s.id)), e, released)
		}
	}
	got, err := c.StoredEpisodesByKey(ctx, core.Filter{}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.RatingKey{core.EpisodeKey(1, 1, 1), core.EpisodeKey(1, 1, 2), core.EpisodeKey(3, 1, 1), core.EpisodeKey(3, 1, 2)}
	if len(got) != len(want) {
		t.Fatalf("episodes %v", got)
	}
	for i := range want {
		if got[i].Key != want[i] {
			t.Fatalf("episode %d: %v want %v", i, got[i].Key, want[i])
		}
	}
	if page, _ := c.StoredEpisodesByKey(ctx, core.Filter{}, core.EpisodeKey(1, 1, 2), 1); len(page) != 1 || page[0].Key != core.EpisodeKey(3, 1, 1) {
		t.Fatalf("after a key: %v", page)
	}
	if page, _ := c.StoredEpisodesByKey(ctx, core.Filter{MaxContentRating: "TV-14"}, 0, 10); len(page) != 2 {
		t.Fatalf("rating cap: %v", page)
	}
	if n, err := c.CountStoredEpisodes(ctx, core.Filter{}); err != nil || n != 4 {
		t.Fatalf("count %d %v", n, err)
	}
	if ok, err := c.EpisodesUnranked(ctx); err != nil || !ok {
		t.Fatalf("unranked %v %v", ok, err)
	}
	exec(`UPDATE catalog_titles SET popularity=1.5 WHERE key=?`, int64(core.EpisodeKey(3, 1, 1)))
	if ok, err := c.EpisodesUnranked(ctx); err != nil || ok {
		t.Fatalf("a ranked episode: %v %v", ok, err)
	}
}

func TestBrowseStoredSeasonsAndEpisodesAndStableFetchedAt(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })

	t1 := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 2, 10, 0, 0, 0, 0, time.UTC)
	show := core.Title{
		Key: core.ShowKey(77), Type: core.TypeShow, IDs: core.IDs{TMDB: 77},
		Title: "Signal", Released: t1, Genres: []string{"Drama"},
	}
	if err := c.UpsertDetailedTitle(ctx, show); err != nil {
		t.Fatal(err)
	}
	s1 := core.Title{
		Key: core.SeasonKey(77, 1), Type: core.TypeSeason, ShowKey: show.Key,
		IDs: core.IDs{TMDB: 77}, Season: 1, Title: "Season 1", Released: t1,
	}
	e1 := core.Title{
		Key: core.EpisodeKey(77, 1, 1), Type: core.TypeEpisode, ShowKey: show.Key,
		IDs: core.IDs{TMDB: 77, IMDB: "tt0000001"}, Season: 1, Episode: 1, Title: "Ep 1", Released: t1,
	}
	e2 := core.Title{
		Key: core.EpisodeKey(77, 1, 2), Type: core.TypeEpisode, ShowKey: show.Key,
		IDs: core.IDs{TMDB: 77, IMDB: "tt0000002"}, Season: 1, Episode: 2, Title: "Ep 2", Released: t2,
	}
	for _, it := range []core.Title{s1, e1, e2} {
		if err := c.UpsertTitle(ctx, it, false); err != nil {
			t.Fatal(err)
		}
	}

	// Advancing clock and re-upserting with only popularity/vote_count jitter
	// must NOT advance fetched_at.
	later := now.Add(2 * time.Hour)
	c.SetNow(func() time.Time { return later })
	jitter := show
	jitter.Popularity = 999.9
	jitter.VoteCount = 5000
	if err := c.UpsertTitle(ctx, jitter, true); err != nil {
		t.Fatal(err)
	}
	gotShow, err := c.Get(ctx, show.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !gotShow.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt moved on popularity jitter: got %v want %v", gotShow.UpdatedAt, now)
	}

	// Changing material metadata advances fetched_at.
	changed := e2
	changed.Summary = "Updated synopsis"
	if err := c.UpsertTitle(ctx, changed, false); err != nil {
		t.Fatal(err)
	}

	// Browse episodes in key order and by updatedAt:desc.
	epPage, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeEpisode}, Limit: 10})
	if err != nil || epPage.Total != 2 || len(epPage.Items) != 2 || epPage.Items[0].Key != e1.Key || epPage.Items[1].Key != e2.Key {
		t.Fatalf("episode browse by key: %+v (%v)", epPage, err)
	}
	epUpdated, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Type: core.TypeEpisode, UpdatedAfter: later.Unix()},
		Sort:   core.SortUpdated, Desc: true, Limit: 10,
	})
	if err != nil || epUpdated.Total != 1 || len(epUpdated.Items) != 1 || epUpdated.Items[0].Key != e2.Key {
		t.Fatalf("episode incremental browse: %+v (%v)", epUpdated, err)
	}

	// Browse seasons in key order: ChildCount reflects aired episodes (2), AddedAt reflects latest aired episode (t2).
	sePage, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeSeason}, Limit: 10})
	if err != nil || sePage.Total != 1 || len(sePage.Items) != 1 {
		t.Fatalf("season browse: %+v (%v)", sePage, err)
	}
	if sePage.Items[0].ChildCount != 2 || !sePage.Items[0].AddedAt.Equal(t2) {
		t.Fatalf("season counts/dates: %+v", sePage.Items[0])
	}
}

func TestRecentAirSkipsExcludedTitles(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	for i := 1; i <= 5; i++ {
		if _, err := c.db.ExecContext(ctx, `INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult,detail) VALUES(?,'movie','m','m',?,1,0,1)`, int64(core.MovieKey(int64(i))), now.Add(-time.Duration(i)*time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := c.RecentAirExcluding(ctx, core.TypeMovie, []core.RatingKey{core.MovieKey(1), core.MovieKey(2)}, 0, 2)
	if err != nil || len(got) != 2 || got[0].Key != core.MovieKey(3) || got[1].Key != core.MovieKey(4) {
		t.Fatalf("window %v %v", got, err)
	}
}

// A large section total is served stale while it recounts: background
// catalogue writes bump the write generation constantly, and a page's
// total must not recount the whole section on every page (#639). The
// stale value is the previous total, before the write that bumped the
// generation.
func TestCountStoredEpisodesServesStaleLargeTotal(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	past := now.Add(-time.Hour).Unix()
	const total = staleCountMin + 5
	if _, err := c.db.ExecContext(ctx, `INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult) VALUES(?,'show','s','s',?,1,0)`, int64(core.ShowKey(1)), past); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.ExecContext(ctx, `WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n< ?) INSERT INTO catalog_titles(key,type,show_key,season,episode,title,sort_title,released_ts,adult) SELECT ?+n,'episode',?,1,n,'e','e',?,0 FROM seq`, total, int64(core.EpisodeKey(1, 1, 0)), int64(core.ShowKey(1)), past); err != nil {
		t.Fatal(err)
	}
	n, err := c.CountStoredEpisodes(ctx, core.Filter{})
	if err != nil || n != total {
		t.Fatalf("first count %d %v, want %d", n, err, total)
	}
	// A catalogue write bumps the generation; the next total is the stale
	// one (without the new episode), served without a recount.
	extra := core.Title{Key: core.EpisodeKey(1, 2, 1), Type: core.TypeEpisode, ShowKey: core.ShowKey(1), Season: 2, Episode: 1, Title: "new", Released: now.Add(-time.Hour)}
	if err := c.UpsertTitle(ctx, extra, false); err != nil {
		t.Fatal(err)
	}
	n, err = c.CountStoredEpisodes(ctx, core.Filter{})
	if err != nil || n != total {
		t.Fatalf("stale count %d %v, want previous total %d", n, err, total)
	}
}

func TestCollectionBrowseSortAddedAndScopedEpisodes(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := c.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE IF NOT EXISTS collections_members (spec_id INTEGER NOT NULL, pos INTEGER NOT NULL, key INTEGER NOT NULL, shared INTEGER NOT NULL DEFAULT 1, PRIMARY KEY (spec_id, pos))`)
	exec(`CREATE INDEX IF NOT EXISTS collections_members_profile_lookup ON collections_members(spec_id, key, shared)`)

	// Older-premiering show in the collection has the newest aired episode;
	// newer-premiering show outside the collection must never leak.
	showOlderPremiere := core.ShowKey(101)
	showNewerPremiere := core.ShowKey(102)
	showPrivateMember := core.ShowKey(103)
	showOutside := core.ShowKey(104)
	for _, s := range []core.Title{
		{Key: showOlderPremiere, Type: core.TypeShow, Title: "Older Premiere", Released: now.Add(-500 * time.Hour), Genres: []string{"Drama"}},
		{Key: showNewerPremiere, Type: core.TypeShow, Title: "Newer Premiere", Released: now.Add(-100 * time.Hour), Genres: []string{"Comedy"}},
		{Key: showPrivateMember, Type: core.TypeShow, Title: "Private Member", Released: now.Add(-10 * time.Hour), Genres: []string{"Drama"}},
		{Key: showOutside, Type: core.TypeShow, Title: "Outside Show", Released: now.Add(-5 * time.Hour), Genres: []string{"Drama"}},
	} {
		if err := c.UpsertTitle(ctx, s, true); err != nil {
			t.Fatal(err)
		}
	}
	epOlderShowLatest := core.EpisodeKey(101, 2, 1)
	epNewerShowOlder := core.EpisodeKey(102, 1, 1)
	epOutsideLatest := core.EpisodeKey(104, 1, 1)
	for _, e := range []core.Title{
		{Key: epOlderShowLatest, ShowKey: showOlderPremiere, Type: core.TypeEpisode, Season: 2, Episode: 1, Title: "Newest In Collection", Released: now.Add(-2 * time.Hour)},
		{Key: epNewerShowOlder, ShowKey: showNewerPremiere, Type: core.TypeEpisode, Season: 1, Episode: 1, Title: "Older In Collection", Released: now.Add(-24 * time.Hour)},
		{Key: epOutsideLatest, ShowKey: showOutside, Type: core.TypeEpisode, Season: 1, Episode: 1, Title: "Outside Episode", Released: now.Add(-1 * time.Hour)},
	} {
		if err := c.UpsertTitle(ctx, e, false); err != nil {
			t.Fatal(err)
		}
	}
	// Populate show_added_ts explicitly against the frozen clock (SQLite trigger
	// uses real strftime('%s','now')).
	exec(`UPDATE catalog_titles SET show_added_ts=? WHERE key=?`, now.Add(-2*time.Hour).Unix(), int64(showOlderPremiere))
	exec(`UPDATE catalog_titles SET show_added_ts=? WHERE key=?`, now.Add(-24*time.Hour).Unix(), int64(showNewerPremiere))
	exec(`UPDATE catalog_titles SET show_added_ts=? WHERE key=?`, now.Add(-1*time.Hour).Unix(), int64(showOutside))

	exec(`INSERT INTO collections_members(spec_id,pos,key,shared) VALUES (7,0,?,1),(7,1,?,1),(7,2,?,0)`,
		int64(showOlderPremiere), int64(showNewerPremiere), int64(showPrivateMember))
	exec(`INSERT INTO libraries_catalogue_members(source_id,key) VALUES ('custom-tv',?)`, int64(showNewerPremiere))

	// Grouped shows in a collection must order by show_added_ts (latest aired
	// episode), not premiere date, and exclude non-members and shared=0 rows.
	shows, err := c.Browse(ctx, core.BrowseQuery{
		Filter:  core.Filter{Type: core.TypeShow, KnownAddedDate: true, CollectionMembers: 7},
		Sort:    core.SortAdded,
		Desc:    true,
		Limit:   10,
		NoTotal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(shows.Items) != 2 || shows.Items[0].Key != showOlderPremiere || shows.Items[1].Key != showNewerPremiere {
		t.Fatalf("collection shows = %+v, want [%v, %v]", shows.Items, showOlderPremiere, showNewerPremiere)
	}

	// Ungrouped episodes in a collection must stay within the collection's
	// shows and populate AddedAt.
	eps, err := c.Browse(ctx, core.BrowseQuery{
		Filter:  core.Filter{Type: core.TypeEpisode, KnownAddedDate: true, CollectionMembers: 7},
		Sort:    core.SortAdded,
		Desc:    true,
		Limit:   10,
		NoTotal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(eps.Items) != 2 || eps.Items[0].Key != epOlderShowLatest || eps.Items[1].Key != epNewerShowOlder {
		t.Fatalf("collection episodes = %+v, want [%v, %v]", eps.Items, epOlderShowLatest, epNewerShowOlder)
	}
	if eps.Items[0].AddedAt.Unix() != now.Add(-2*time.Hour).Unix() {
		t.Fatalf("episode AddedAt = %v, want %v", eps.Items[0].AddedAt, now.Add(-2*time.Hour))
	}

	// CatalogueSources and ExtraRules also scope episode queries.
	srcEps, err := c.Browse(ctx, core.BrowseQuery{
		Filter:  core.Filter{Type: core.TypeEpisode, KnownAddedDate: true, CatalogueSources: []string{"custom-tv"}},
		Sort:    core.SortAdded,
		Desc:    true,
		Limit:   10,
		NoTotal: true,
	})
	if err != nil || len(srcEps.Items) != 1 || srcEps.Items[0].Key != epNewerShowOlder {
		t.Fatalf("source-scoped episodes = %+v err=%v, want [%v]", srcEps.Items, err, epNewerShowOlder)
	}
	ruleEps, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{
			Type:           core.TypeEpisode,
			KnownAddedDate: true,
			ExtraFallback:  "hide",
			ExtraRules: []core.LibraryExclusion{{
				Name:      "Only Comedy",
				Action:    "show",
				MediaType: "show",
				Match:     "all",
				Rules:     []core.CollectionRule{{Field: "genre", Op: "is", Value: json.RawMessage(`"Comedy"`)}},
			}},
		},
		Sort:    core.SortAdded,
		Desc:    true,
		Limit:   10,
		NoTotal: true,
	})
	if err != nil || len(ruleEps.Items) != 1 || ruleEps.Items[0].Key != epNewerShowOlder {
		t.Fatalf("rule-scoped episodes = %+v err=%v, want [%v]", ruleEps.Items, err, epNewerShowOlder)
	}

	// Query plans for collection shows and episodes must drive from
	// collections_members rather than scanning the full catalogue.
	showWhere, showArgs, err := c.browseWhere(core.Filter{Type: core.TypeShow, KnownAddedDate: true, CollectionMembers: 7})
	if err != nil {
		t.Fatal(err)
	}
	epWhere, epArgs, err := c.storedEpisodesWhere(core.Filter{Type: core.TypeEpisode, KnownAddedDate: true, CollectionMembers: 7})
	if err != nil {
		t.Fatal(err)
	}
	epCrossArgs := append([]any{showKeyStart, showKeyEnd, episodeKeyStart, episodeKeyEnd}, epArgs...)
	for _, tc := range []struct {
		name string
		sql  string
		args []any
	}{
		{"collection shows", `SELECT t.key FROM catalog_titles t WHERE ` + showWhere + ` ORDER BY t.show_added_ts DESC, t.key ASC LIMIT 10`, showArgs},
		{"collection episodes", `SELECT e.key FROM catalog_titles t CROSS JOIN catalog_titles e WHERE t.key>? AND t.key<? AND e.show_key=t.key AND e.key>? AND e.key<? AND ` + epWhere + ` ORDER BY e.released_ts DESC, e.key ASC LIMIT 10`, epCrossArgs},
	} {
		rows, err := c.db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+tc.sql, tc.args...)
		if err != nil {
			t.Fatalf("%s explain: %v", tc.name, err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail)
			plan.WriteByte('\n')
		}
		rows.Close()
		text := plan.String()
		if !strings.Contains(text, "collections_members") || !strings.Contains(text, "LIST SUBQUERY") || (!strings.Contains(text, "SEARCH t USING INTEGER PRIMARY KEY") && !strings.Contains(text, "key=?")) {
			t.Fatalf("%s plan must drive from collections_members into keyed catalog_titles lookup:\n%s", tc.name, text)
		}
	}
}

// Scoped stored episode and season reads (#671): library rules, empty
// hide-default libraries, viewer-state tab rules, catalogue sources and
// collection membership must narrow BrowseStoredEpisodes/Seasons and their
// totals without breaking key-order cursor pagination or leaking across
// viewers/rebuilds.
func TestBrowseStoredChildrenRespectLibraryScoping(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	t1 := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 2, 10, 0, 0, 0, 0, time.UTC)

	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS libraries_catalogue_members (source_id TEXT NOT NULL, key INTEGER NOT NULL, PRIMARY KEY (source_id, key))`,
		`CREATE TABLE IF NOT EXISTS collections_members (spec_id INTEGER NOT NULL, pos INTEGER NOT NULL, key INTEGER NOT NULL, shared INTEGER NOT NULL DEFAULT 1, PRIMARY KEY (spec_id, pos))`,
		`CREATE TABLE IF NOT EXISTS favorites_items (viewer_id INTEGER NOT NULL, kind TEXT NOT NULL, subject_id TEXT NOT NULL, added_at INTEGER NOT NULL, sort_order INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (viewer_id, kind, subject_id))`,
	} {
		if _, err := c.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}

	shows := []struct {
		id     int64
		title  string
		lang   string
		genres []string
	}{
		{10, "Anime One", "ja", []string{"Animation"}},
		{20, "Drama One", "en", []string{"Drama"}},
		{30, "Anime Two", "ja", []string{"Animation"}},
	}
	for _, s := range shows {
		show := core.Title{
			Key: core.ShowKey(s.id), Type: core.TypeShow, IDs: core.IDs{TMDB: s.id},
			Title: s.title, Language: s.lang, Genres: s.genres, Released: t1,
		}
		if err := c.UpsertDetailedTitle(ctx, show); err != nil {
			t.Fatal(err)
		}
		se := core.Title{
			Key: core.SeasonKey(s.id, 1), Type: core.TypeSeason, ShowKey: show.Key,
			IDs: core.IDs{TMDB: s.id}, Season: 1, Title: "Season 1", Released: t1,
		}
		e1 := core.Title{
			Key: core.EpisodeKey(s.id, 1, 1), Type: core.TypeEpisode, ShowKey: show.Key,
			IDs: core.IDs{TMDB: s.id}, Season: 1, Episode: 1, Title: "Ep 1", Released: t1,
		}
		e2 := core.Title{
			Key: core.EpisodeKey(s.id, 1, 2), Type: core.TypeEpisode, ShowKey: show.Key,
			IDs: core.IDs{TMDB: s.id}, Season: 1, Episode: 2, Title: "Ep 2", Released: t2,
		}
		for _, it := range []core.Title{se, e1, e2} {
			if err := c.UpsertTitle(ctx, it, false); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Plain unscoped section still sees all 3 shows' 6 episodes and 3 seasons.
	if page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeEpisode}, Limit: 10}); err != nil || page.Total != 6 || len(page.Items) != 6 {
		t.Fatalf("unscoped episodes = total %d len %d err %v", page.Total, len(page.Items), err)
	}
	if page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeSeason}, Limit: 10}); err != nil || page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("unscoped seasons = total %d len %d err %v", page.Total, len(page.Items), err)
	}

	// 1. Rule-based Anime library (ExtraRules + ExtraFallback="hide"), including
	// sequential key-order cursor pagination and descending release sort.
	animeRules := []core.LibraryExclusion{{
		Action:    "show",
		MediaType: "show",
		Match:     "all",
		Rules: []core.CollectionRule{
			{Field: "genre", Op: "is", Value: json.RawMessage(`"Animation"`)},
			{Field: "language", Op: "is", Value: json.RawMessage(`"ja"`)},
		},
	}}
	animeEp := core.Filter{Type: core.TypeEpisode, ExtraRules: animeRules, ExtraFallback: "hide"}
	p1, err := c.Browse(ctx, core.BrowseQuery{Filter: animeEp, Offset: 0, Limit: 2})
	if err != nil || p1.Total != 4 || len(p1.Items) != 2 || p1.Items[0].Key != core.EpisodeKey(10, 1, 1) || p1.Items[1].Key != core.EpisodeKey(10, 1, 2) {
		t.Fatalf("anime episodes p1 = %+v (%v)", p1, err)
	}
	p2, err := c.Browse(ctx, core.BrowseQuery{Filter: animeEp, Offset: 2, Limit: 2})
	if err != nil || p2.Total != 4 || len(p2.Items) != 2 || p2.Items[0].Key != core.EpisodeKey(30, 1, 1) || p2.Items[1].Key != core.EpisodeKey(30, 1, 2) {
		t.Fatalf("anime episodes p2 (cursor) = %+v (%v)", p2, err)
	}
	rel, err := c.Browse(ctx, core.BrowseQuery{Filter: animeEp, Sort: core.SortReleased, Desc: true, Limit: 10})
	if err != nil || rel.Total != 4 || len(rel.Items) != 4 {
		t.Fatalf("anime episodes released desc = %+v (%v)", rel, err)
	}
	for _, it := range rel.Items {
		if it.ShowKey == core.ShowKey(20) {
			t.Fatalf("drama episode leaked into anime released browse: %+v", it)
		}
	}
	animeSeason := core.Filter{Type: core.TypeSeason, ExtraRules: animeRules, ExtraFallback: "hide"}
	sp1, err := c.Browse(ctx, core.BrowseQuery{Filter: animeSeason, Offset: 0, Limit: 1})
	if err != nil || sp1.Total != 2 || len(sp1.Items) != 1 || sp1.Items[0].Key != core.SeasonKey(10, 1) {
		t.Fatalf("anime seasons p1 = %+v (%v)", sp1, err)
	}
	sp2, err := c.Browse(ctx, core.BrowseQuery{Filter: animeSeason, Offset: 1, Limit: 1})
	if err != nil || sp2.Total != 2 || len(sp2.Items) != 1 || sp2.Items[0].Key != core.SeasonKey(30, 1) {
		t.Fatalf("anime seasons p2 (cursor) = %+v (%v)", sp2, err)
	}

	// 2. Empty hide-default library (no rules, ExtraFallback="hide") returns 0.
	if p, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeEpisode, ExtraFallback: "hide"}, Limit: 10}); err != nil || p.Total != 0 || len(p.Items) != 0 {
		t.Fatalf("empty hide-default episodes = %+v (%v)", p, err)
	}
	if p, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeSeason, ExtraFallback: "hide"}, Limit: 10}); err != nil || p.Total != 0 || len(p.Items) != 0 {
		t.Fatalf("empty hide-default seasons = %+v (%v)", p, err)
	}

	// 3. Viewer-state TabRules must isolate results across viewers (no cross-viewer cache leak).
	if _, err := c.db.ExecContext(ctx, `INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at) VALUES (1,'title',?,1),(2,'title',?,1)`, core.ShowKey(10).String(), core.ShowKey(20).String()); err != nil {
		t.Fatal(err)
	}
	favTab := []core.LibraryExclusion{{
		Action:    "show",
		MediaType: "show",
		Match:     "all",
		Rules:     []core.CollectionRule{{Field: "viewer", Op: "is", Value: json.RawMessage(`{"favorite":true}`)}},
	}}
	ctx1 := core.WithCatalogViewer(ctx, &core.User{ID: 1})
	ctx2 := core.WithCatalogViewer(ctx, &core.User{ID: 2})
	v1Ep, err := c.Browse(ctx1, core.BrowseQuery{Filter: core.Filter{Type: core.TypeEpisode, TabRules: favTab, TabFallback: "hide"}, Limit: 10})
	if err != nil || v1Ep.Total != 2 || len(v1Ep.Items) != 2 || v1Ep.Items[0].ShowKey != core.ShowKey(10) {
		t.Fatalf("viewer 1 tab episodes = %+v (%v)", v1Ep, err)
	}
	v2Ep, err := c.Browse(ctx2, core.BrowseQuery{Filter: core.Filter{Type: core.TypeEpisode, TabRules: favTab, TabFallback: "hide"}, Limit: 10})
	if err != nil || v2Ep.Total != 2 || len(v2Ep.Items) != 2 || v2Ep.Items[0].ShowKey != core.ShowKey(20) {
		t.Fatalf("viewer 2 tab episodes = %+v (%v)", v2Ep, err)
	}

	// 4. CatalogueSources triggers source loader and filters episodes & seasons.
	loaded := false
	c.SetCatalogueSourceLoader(func(ctx context.Context, sources []string) error {
		loaded = true
		_, err := c.db.ExecContext(ctx, `INSERT OR IGNORE INTO libraries_catalogue_members(source_id,key) VALUES ('custom-src',?)`, int64(core.ShowKey(20)))
		return err
	})
	srcEp, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeEpisode, CatalogueSources: []string{"custom-src"}}, Limit: 10})
	if err != nil || !loaded || srcEp.Total != 2 || len(srcEp.Items) != 2 || srcEp.Items[0].ShowKey != core.ShowKey(20) {
		t.Fatalf("source episodes loaded=%v page=%+v err=%v", loaded, srcEp, err)
	}
	srcSe, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeSeason, CatalogueSources: []string{"custom-src"}}, Limit: 10})
	if err != nil || srcSe.Total != 1 || len(srcSe.Items) != 1 || srcSe.Items[0].Key != core.SeasonKey(20, 1) {
		t.Fatalf("source seasons page=%+v err=%v", srcSe, err)
	}

	// 5. CollectionMembers filters episodes & seasons and reflects rebuilds without writeGen bump.
	if _, err := c.db.ExecContext(ctx, `INSERT INTO collections_members(spec_id,pos,key,shared) VALUES (99,0,?,1)`, int64(core.ShowKey(30))); err != nil {
		t.Fatal(err)
	}
	colEp, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeEpisode, CollectionMembers: 99}, Limit: 10})
	if err != nil || colEp.Total != 2 || len(colEp.Items) != 2 || colEp.Items[0].ShowKey != core.ShowKey(30) {
		t.Fatalf("collection episodes = %+v (%v)", colEp, err)
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM collections_members WHERE spec_id=99`); err != nil {
		t.Fatal(err)
	}
	colEpEmpty, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeEpisode, CollectionMembers: 99}, Limit: 10})
	if err != nil || colEpEmpty.Total != 0 || len(colEpEmpty.Items) != 0 {
		t.Fatalf("collection episodes after rebuild = %+v (%v)", colEpEmpty, err)
	}
}
