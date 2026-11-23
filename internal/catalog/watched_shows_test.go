package catalog

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestBrowseUnwatchedShowsUseEpisodeLeaves(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	aired := now.Add(-24 * time.Hour)
	future := now.Add(48 * time.Hour)

	upsert := func(items ...core.Title) {
		t.Helper()
		for _, it := range items {
			browsable := it.Type == core.TypeShow || it.Type == core.TypeMovie
			if err := c.UpsertTitle(ctx, it, browsable); err != nil {
				t.Fatalf("upsert %v: %v", it.Key, err)
			}
		}
	}
	mark := func(key core.RatingKey, count int) {
		t.Helper()
		if _, err := c.db.ExecContext(ctx,
			`INSERT INTO state_watch(user_id,rating_key,view_count) VALUES(7,?,?) ON CONFLICT(user_id,rating_key) DO UPDATE SET view_count=excluded.view_count`,
			int64(key), count); err != nil {
			t.Fatalf("mark %v: %v", key, err)
		}
	}

	// 1. Unhydrated show (zero episode rows, untouched): must stay in Unwatched.
	unhydrated := core.ShowKey(101)
	upsert(core.Title{Key: unhydrated, Type: core.TypeShow, Title: "Alpha Unhydrated", Released: aired, ChildCount: 1, LeafCount: 10})

	// 2. Partly-watched show: 1 of 2 aired episodes watched, and Plex's parent
	//    touch incremented view_count on both the show and season rows (#705).
	partly := core.ShowKey(102)
	partlyS1 := core.SeasonKey(102, 1)
	partlyE1 := core.EpisodeKey(102, 1, 1)
	partlyE2 := core.EpisodeKey(102, 1, 2)
	upsert(
		core.Title{Key: partly, Type: core.TypeShow, Title: "Bravo Partly", Released: aired, ChildCount: 1, LeafCount: 2},
		core.Title{Key: partlyS1, Type: core.TypeSeason, ShowKey: partly, Season: 1, Title: "Season 1", Released: aired, ChildCount: 2},
		core.Title{Key: partlyE1, Type: core.TypeEpisode, ShowKey: partly, Season: 1, Episode: 1, Title: "Ep 1", Released: aired},
		core.Title{Key: partlyE2, Type: core.TypeEpisode, ShowKey: partly, Season: 1, Episode: 2, Title: "Ep 2", Released: aired},
	)
	mark(partly, 1)
	mark(partlyS1, 1)
	mark(partlyE1, 1)

	// 3. Fully-watched show via episode leaves only (no show/season state_watch
	//    rows, as from Jellyfin/Vela/Trakt imports), plus an unwatched Special
	//    (S0E1) and an unaired future episode (S1E3): must be Watched.
	done := core.ShowKey(103)
	doneS0 := core.SeasonKey(103, 0)
	doneS1 := core.SeasonKey(103, 1)
	doneS0E1 := core.EpisodeKey(103, 0, 1)
	doneE1 := core.EpisodeKey(103, 1, 1)
	doneE2 := core.EpisodeKey(103, 1, 2)
	doneE3 := core.EpisodeKey(103, 1, 3)
	upsert(
		core.Title{Key: done, Type: core.TypeShow, Title: "Charlie Done", Released: aired, ChildCount: 1, LeafCount: 3},
		core.Title{Key: doneS0, Type: core.TypeSeason, ShowKey: done, Season: 0, Title: "Specials", Released: aired, ChildCount: 1},
		core.Title{Key: doneS1, Type: core.TypeSeason, ShowKey: done, Season: 1, Title: "Season 1", Released: aired, ChildCount: 3},
		core.Title{Key: doneS0E1, Type: core.TypeEpisode, ShowKey: done, Season: 0, Episode: 1, Title: "Special 1", Released: aired},
		core.Title{Key: doneE1, Type: core.TypeEpisode, ShowKey: done, Season: 1, Episode: 1, Title: "Ep 1", Released: aired},
		core.Title{Key: doneE2, Type: core.TypeEpisode, ShowKey: done, Season: 1, Episode: 2, Title: "Ep 2", Released: aired},
		core.Title{Key: doneE3, Type: core.TypeEpisode, ShowKey: done, Season: 1, Episode: 3, Title: "Ep 3 Future", Released: future},
	)
	mark(doneE1, 1)
	mark(doneE2, 1)

	// 4. Show whose S1 is fully watched, but S2 is aired and unhydrated (no
	//    episode rows yet): must stay in Unwatched.
	s2Pending := core.ShowKey(104)
	s2PendingS1 := core.SeasonKey(104, 1)
	s2PendingS2 := core.SeasonKey(104, 2)
	s2PendingE1 := core.EpisodeKey(104, 1, 1)
	upsert(
		core.Title{Key: s2Pending, Type: core.TypeShow, Title: "Delta S2 Unhydrated", Released: aired, ChildCount: 2, LeafCount: 9},
		core.Title{Key: s2PendingS1, Type: core.TypeSeason, ShowKey: s2Pending, Season: 1, Title: "Season 1", Released: aired, ChildCount: 1},
		core.Title{Key: s2PendingS2, Type: core.TypeSeason, ShowKey: s2Pending, Season: 2, Title: "Season 2", Released: aired, ChildCount: 8},
		core.Title{Key: s2PendingE1, Type: core.TypeEpisode, ShowKey: s2Pending, Season: 1, Episode: 1, Title: "Ep 1", Released: aired},
	)
	mark(s2Pending, 1)
	mark(s2PendingS1, 1)
	mark(s2PendingE1, 1)

	// 5. Movie watched & unwatched for mixed-type browse checks.
	movieWatched := core.MovieKey(201)
	movieUnwatched := core.MovieKey(202)
	upsert(
		core.Title{Key: movieWatched, Type: core.TypeMovie, Title: "Echo Movie Watched", Released: aired},
		core.Title{Key: movieUnwatched, Type: core.TypeMovie, Title: "Foxtrot Movie Unwatched", Released: aired},
	)
	mark(movieWatched, 1)

	keySet := func(items []core.Title) map[core.RatingKey]bool {
		m := make(map[core.RatingKey]bool, len(items))
		for _, it := range items {
			m[it.Key] = true
		}
		return m
	}

	unwatchedTrue := true
	unwatchedFalse := false

	// TV Unwatched: must keep unhydrated, partly-watched, and s2-unhydrated; drop done.
	unwPage, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Type: core.TypeShow, Group: &core.FilterGroup{UserID: 7, Unwatched: &unwatchedTrue}},
		Sort:   core.SortTitle,
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("browse unwatched shows: %v", err)
	}
	unwGot := keySet(unwPage.Items)
	if unwPage.Total != 3 || len(unwPage.Items) != 3 || !unwGot[unhydrated] || !unwGot[partly] || !unwGot[s2Pending] || unwGot[done] {
		t.Fatalf("unwatched shows mismatch: total=%d keys=%v", unwPage.Total, unwGot)
	}

	// TV Watched: only Charlie Done.
	wPage, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Type: core.TypeShow, Group: &core.FilterGroup{UserID: 7, Unwatched: &unwatchedFalse}},
		Sort:   core.SortTitle,
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("browse watched shows: %v", err)
	}
	wGot := keySet(wPage.Items)
	if wPage.Total != 1 || len(wPage.Items) != 1 || !wGot[done] {
		t.Fatalf("watched shows mismatch: total=%d keys=%v", wPage.Total, wGot)
	}

	// CountWatched must agree with Browse.
	cnt, err := c.CountWatched(ctx, core.Filter{Type: core.TypeShow}, 7)
	if err != nil || cnt != 1 {
		t.Fatalf("CountWatched shows=%d err=%v, want 1", cnt, err)
	}

	// WatchedTitles must agree for shows and seasons.
	flags, err := c.WatchedTitles(ctx, 7, []core.RatingKey{unhydrated, partly, done, s2Pending, partlyS1, doneS1, doneS0, s2PendingS1, s2PendingS2, movieWatched, movieUnwatched})
	if err != nil {
		t.Fatalf("WatchedTitles: %v", err)
	}
	for _, tc := range []struct {
		key  core.RatingKey
		want bool
	}{
		{unhydrated, false},
		{partly, false},
		{done, true},
		{s2Pending, false},
		{partlyS1, false},
		{doneS1, true},
		{doneS0, false},
		{s2PendingS1, true},
		{s2PendingS2, false},
		{movieWatched, true},
		{movieUnwatched, false},
	} {
		if flags[tc.key] != tc.want {
			t.Errorf("WatchedTitles[%v]=%v want %v", tc.key, flags[tc.key], tc.want)
		}
	}

	// Seasons browse (type=3): partlyS1 must stay in Unwatched; doneS1 and
	// s2PendingS1 are Watched.
	seUnw, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Type: core.TypeSeason, Group: &core.FilterGroup{UserID: 7, Unwatched: &unwatchedTrue}},
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("browse unwatched seasons: %v", err)
	}
	seUnwGot := keySet(seUnw.Items)
	if !seUnwGot[partlyS1] || !seUnwGot[s2PendingS2] || seUnwGot[doneS1] || seUnwGot[s2PendingS1] {
		t.Fatalf("unwatched seasons mismatch: total=%d keys=%v", seUnw.Total, seUnwGot)
	}
	if seUnw.Total != len(seUnw.Items) {
		t.Fatalf("unwatched seasons total=%d != len=%d", seUnw.Total, len(seUnw.Items))
	}

	seW, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Type: core.TypeSeason, Group: &core.FilterGroup{UserID: 7, Unwatched: &unwatchedFalse}},
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("browse watched seasons: %v", err)
	}
	seWGot := keySet(seW.Items)
	if seW.Total != 2 || len(seW.Items) != 2 || !seWGot[doneS1] || !seWGot[s2PendingS1] {
		t.Fatalf("watched seasons mismatch: total=%d keys=%v", seW.Total, seWGot)
	}

	// Mixed movie+show browse: movies use movie row, shows use episode leaves.
	mixUnw, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Group: &core.FilterGroup{UserID: 7, Unwatched: &unwatchedTrue}},
		Sort:   core.SortTitle,
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("browse mixed unwatched: %v", err)
	}
	mixGot := keySet(mixUnw.Items)
	if mixUnw.Total != 4 || !mixGot[unhydrated] || !mixGot[partly] || !mixGot[s2Pending] || !mixGot[movieUnwatched] || mixGot[done] || mixGot[movieWatched] {
		t.Fatalf("mixed unwatched mismatch: total=%d keys=%v", mixUnw.Total, mixGot)
	}

	// When future E3 of Charlie Done airs, Charlie Done returns to Unwatched
	// automatically without mutating state_watch.
	c.SetNow(func() time.Time { return future.Add(time.Hour) })
	afterAir, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Type: core.TypeShow, Group: &core.FilterGroup{UserID: 7, Unwatched: &unwatchedTrue}},
		Sort:   core.SortTitle,
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("browse after future episode aired: %v", err)
	}
	if afterAir.Total != 4 || !keySet(afterAir.Items)[done] {
		t.Fatalf("expected show with newly aired episode to return to Unwatched: total=%d items=%v", afterAir.Total, keySet(afterAir.Items))
	}

	// Verify SQLite query plan materializes the watched-shows subquery once
	// instead of correlating an episode walk per show.
	if store.DialectOf(c.db) == store.SQLite {
		where, args, err := c.browseWhere(core.Filter{
			Type:  core.TypeShow,
			Group: &core.FilterGroup{UserID: 7, Unwatched: &unwatchedTrue},
		})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := c.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT t.key FROM catalog_titles t WHERE `+where+` ORDER BY t.sort_title ASC, t.key ASC LIMIT 50`, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
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
		planStr := plan.String()
		if !strings.Contains(planStr, "LIST SUBQUERY") {
			t.Fatalf("expected watched shows to materialize as a one-shot LIST SUBQUERY, got:\n%s", planStr)
		}
		if strings.Contains(planStr, "CORRELATED LIST SUBQUERY") {
			t.Fatalf("watched shows must not correlate per outer show row, got:\n%s", planStr)
		}
		if strings.Contains(planStr, "SCAN catalog_titles") || strings.Contains(planStr, "SCAN e") {
			t.Fatalf("unexpected full table scan in unwatched shows plan:\n%s", planStr)
		}
	}
}
