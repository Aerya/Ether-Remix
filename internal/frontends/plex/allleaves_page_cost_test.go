package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/store"
)

// #639: a section allLeaves page must cost a small, fixed number of
// statements however large the page or deep the offset, and must never
// write. Tracearr walks whole sections twice a day; a per-episode
// statement (or a total that recounts the section) turns that walk into
// hours of CPU. Ceilings only, never equality: the exact count may
// shrink, it must not grow with page size or offset.
func TestSectionAllLeavesPageCostCeiling(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	c, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}
	released := fixedNow.Add(-365 * 24 * time.Hour)
	const shows, eps = 10, 200
	for s := 0; s < shows; s++ {
		tmdb := int64(1000 + s)
		show := core.Title{Key: core.ShowKey(tmdb), Type: core.TypeShow, IDs: core.IDs{TMDB: tmdb}, Title: fmt.Sprintf("Show %d", s), Released: released, ContentRating: "TV-14", Genres: []string{"Drama"}, Studios: []string{"Studio"}}
		if err := c.UpsertTitle(ctx, show, true); err != nil {
			t.Fatal(err)
		}
		season := core.Title{Key: core.SeasonKey(tmdb, 1), Type: core.TypeSeason, ShowKey: show.Key, Season: 1, Title: "Season 1", Released: released}
		if err := c.UpsertTitle(ctx, season, false); err != nil {
			t.Fatal(err)
		}
		for e := 1; e <= eps; e++ {
			ep := core.Title{Key: core.EpisodeKey(tmdb, 1, e), Type: core.TypeEpisode, ShowKey: show.Key, IDs: core.IDs{TMDB: tmdb}, Season: 1, Episode: e, Title: fmt.Sprintf("Ep %d", e), Released: released, Runtime: 40 * time.Minute, StillPath: "/still.jpg"}
			if err := c.UpsertTitle(ctx, ep, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`UPDATE catalog_titles SET detail=1, detail_at=?, fetched_at=?`, fixedNow.Unix(), fixedNow.Unix()); err != nil {
		t.Fatal(err)
	}
	f := newFixture(func(d *testServerDeps) { d.Catalog = c; d.DB = db })
	defer f.srv.Close()

	const ceiling = 40
	measure := func(name, path string, wantItems int) model.MediaContainer {
		t.Helper()
		qctx, stats := store.WithQueryStats(context.Background())
		req := httptest.NewRequest("GET", path, nil).WithContext(qctx)
		req.Header.Set("X-Plex-Token", ownerToken)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Plex-Client-Identifier", "allleaves-cost")
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: HTTP %d %s", name, rec.Code, rec.Body.String())
		}
		var body struct{ MediaContainer model.MediaContainer }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		mc := body.MediaContainer
		if len(mc.Metadata) != wantItems {
			t.Fatalf("%s: %d items, want %d", name, len(mc.Metadata), wantItems)
		}
		if mc.TotalSize == nil || *mc.TotalSize != shows*eps {
			t.Fatalf("%s: totalSize %v, want %d", name, mc.TotalSize, shows*eps)
		}
		t.Logf("%s: queries=%d writes=%d rows=%d", name, stats.Queries(), stats.Writes(), stats.Rows())
		if stats.Queries() > ceiling {
			t.Fatalf("%s: %d queries over ceiling %d", name, stats.Queries(), ceiling)
		}
		if stats.Writes() > 0 {
			t.Fatalf("%s: %d writes on a read page", name, stats.Writes())
		}
		return mc
	}

	measure("first page 50", "/library/sections/2/allLeaves?includeGuids=1&X-Plex-Container-Start=0&X-Plex-Container-Size=50", 50)
	measure("first page 200", "/library/sections/2/allLeaves?includeGuids=1&X-Plex-Container-Start=0&X-Plex-Container-Size=200", 200)
	// Walk sequentially like Tracearr, then prove a deep page costs the
	// same as the first and lands on the right episode.
	for start := 200; start < 1800; start += 200 {
		measure(fmt.Sprintf("walk %d", start), fmt.Sprintf("/library/sections/2/allLeaves?includeGuids=1&X-Plex-Container-Start=%d&X-Plex-Container-Size=200", start), 200)
	}
	deep := measure("deep page 200", "/library/sections/2/allLeaves?includeGuids=1&X-Plex-Container-Start=1800&X-Plex-Container-Size=200", 200)
	if want := core.EpisodeKey(1009, 1, 1).String(); deep.Metadata[0].RatingKey != want {
		t.Fatalf("deep page first key %s, want %s", deep.Metadata[0].RatingKey, want)
	}
	// The total probe must not walk the section either.
	measure("size-1 total", "/library/sections/2/allLeaves?includeGuids=1&X-Plex-Container-Start=0&X-Plex-Container-Size=1", 1)
	measure("updatedAt page", "/library/sections/2/allLeaves?includeGuids=1&sort=updatedAt:desc&X-Plex-Container-Start=0&X-Plex-Container-Size=200", 200)
}
