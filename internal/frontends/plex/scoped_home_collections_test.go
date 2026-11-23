package plex

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/personalize"
)

type homeCollectionFixture struct {
	rows []core.CollectionRow
}

func (c *homeCollectionFixture) HomeRowsFor(context.Context, *core.User, core.HubContext) ([]core.CollectionRow, error) {
	return append([]core.CollectionRow(nil), c.rows...), nil
}

// Regression test for #710: Plex iOS / Apple TV requests
// /hubs/promoted?contentDirectoryID=1&pinnedContentDirectoryID=1,2&excludeContinueWatching=1&count=20
// and contentDirectoryID=2. Custom collections published with HomeRow=true and
// SectionRow=false must appear on their matching contentDirectoryID without
// leaking across directories or into non-scoped library Recommended.
func TestScopedHomePromotedIncludesHomeOnlyCollectionRows(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	var latestMovies, trendingMovies, latestShows, trendingShows []core.Title
	for i := int64(1); i <= 10; i++ {
		m1 := fightClub
		m1.Key = core.MovieKey(30000 + i)
		m1.Title = fmt.Sprintf("Latest Movie %d", i)
		m1.Genres = []string{"Drama"}
		m1.Rating = 8.2
		m1.VoteCount = 5000
		m1.Popularity = float64(100 - i)
		f.catalog.titles[m1.Key] = m1
		latestMovies = append(latestMovies, m1)

		m2 := fightClub
		m2.Key = core.MovieKey(31000 + i)
		m2.Title = fmt.Sprintf("Trending Movie %d", i)
		m2.Genres = []string{"Action"}
		m2.Rating = 8.1
		m2.VoteCount = 5000
		m2.Popularity = float64(90 - i)
		f.catalog.titles[m2.Key] = m2
		trendingMovies = append(trendingMovies, m2)

		sh1 := breakingBad
		sh1.Key = core.ShowKey(40000 + i)
		sh1.Title = fmt.Sprintf("Latest Series %d", i)
		sh1.Genres = []string{"Drama"}
		sh1.Rating = 8.5
		sh1.VoteCount = 5000
		sh1.Popularity = float64(100 - i)
		f.catalog.titles[sh1.Key] = sh1
		latestShows = append(latestShows, sh1)

		sh2 := breakingBad
		sh2.Key = core.ShowKey(41000 + i)
		sh2.Title = fmt.Sprintf("Trending Series %d", i)
		sh2.Genres = []string{"Crime"}
		sh2.Rating = 8.4
		sh2.VoteCount = 5000
		sh2.Popularity = float64(90 - i)
		f.catalog.titles[sh2.Key] = sh2
		trendingShows = append(trendingShows, sh2)
	}

	cols := &homeCollectionFixture{
		rows: []core.CollectionRow{
			{
				CollectionID: "latest-movies",
				HomeRow:      true,
				SectionRow:   false,
				Hub: core.Hub{
					ID: "collection.latest-movies", Title: "Latest Movies", Type: core.TypeMovie,
					Promoted: true, Context: "hub.home.collection", Key: core.CollectionKey(101), Items: latestMovies,
				},
			},
			{
				CollectionID: "trending-movies",
				HomeRow:      true,
				SectionRow:   false,
				Hub: core.Hub{
					ID: "collection.trending-movies", Title: "Trending Movies", Type: core.TypeMovie,
					Promoted: true, Context: "hub.home.collection", Key: core.CollectionKey(102), Items: trendingMovies,
				},
			},
			{
				CollectionID: "latest-series",
				HomeRow:      true,
				SectionRow:   false,
				Hub: core.Hub{
					ID: "collection.latest-series", Title: "Latest Series", Type: core.TypeShow,
					Promoted: true, Context: "hub.home.collection", Key: core.CollectionKey(201), Items: latestShows,
				},
			},
			{
				CollectionID: "trending-series",
				HomeRow:      true,
				SectionRow:   false,
				Hub: core.Hub{
					ID: "collection.trending-series", Title: "Trending Series", Type: core.TypeShow,
					Promoted: true, Context: "hub.home.collection", Key: core.CollectionKey(202), Items: trendingShows,
				},
			},
		},
	}

	eng, err := personalize.New(personalize.Options{
		DB:            discoveryDB(t),
		Catalog:       f.catalog,
		State:         f.srv.state(),
		Collections:   cols,
		OwnerRowsWait: time.Second,
		RefreshEvery:  -1,
		FlushEvery:    time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	f.srv.setRecommender(eng)
	if err := eng.Rebuild(context.Background(), &ownerUser); err != nil {
		t.Fatal(err)
	}

	movieRes := f.get(t, "/hubs/promoted?contentDirectoryID=1&pinnedContentDirectoryID=1,2&excludeContinueWatching=1&count=20", iosHomeHeaders()...)
	movieRows := hubRows(t, movieRes.Body.Bytes())
	movieIDs := map[string]int{}
	for _, r := range movieRows {
		movieIDs[r.ID] = r.Size
		if strings.HasPrefix(r.ID, "home.") {
			t.Fatalf("contentDirectoryID=1 leaked unscoped home hub %q", r.ID)
		}
		if strings.Contains(r.ID, "series") {
			t.Fatalf("contentDirectoryID=1 leaked show collection %q", r.ID)
		}
	}
	for _, want := range []string{"movie.collection.latest-movies", "movie.collection.trending-movies"} {
		if movieIDs[want] == 0 {
			t.Fatalf("contentDirectoryID=1 missing %q; got %+v", want, movieRows)
		}
	}

	showRes := f.get(t, "/hubs/promoted?contentDirectoryID=2&pinnedContentDirectoryID=1,2&excludeContinueWatching=1&count=20", iosHomeHeaders()...)
	showRows := hubRows(t, showRes.Body.Bytes())
	showIDs := map[string]int{}
	for _, r := range showRows {
		showIDs[r.ID] = r.Size
		if strings.HasPrefix(r.ID, "home.") {
			t.Fatalf("contentDirectoryID=2 leaked unscoped home hub %q", r.ID)
		}
		if strings.Contains(r.ID, "movies") {
			t.Fatalf("contentDirectoryID=2 leaked movie collection %q", r.ID)
		}
	}
	for _, want := range []string{"tv.collection.latest-series", "tv.collection.trending-series"} {
		if showIDs[want] == 0 {
			t.Fatalf("contentDirectoryID=2 missing %q; got %+v", want, showRows)
		}
	}

	// Non-scoped library Recommended (/hubs/sections/1) still respects SectionRow=false.
	secRes := f.get(t, "/hubs/sections/1?count=20", iosHomeHeaders()...)
	for _, r := range hubRows(t, secRes.Body.Bytes()) {
		if r.ID == "movie.collection.latest-movies" || r.ID == "movie.collection.trending-movies" {
			t.Fatalf("HomeRow-only collection %q leaked into /hubs/sections/1", r.ID)
		}
	}
}
