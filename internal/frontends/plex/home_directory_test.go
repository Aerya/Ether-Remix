package plex

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/discovery"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/overlays"
	"github.com/braedonsaunders/ether/internal/watchlist"
)

// Per-directory responses are independently fetched and stacked by Plex clients.
func TestHomeDirectoryPartition(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.srv.setRecommender(&fakeRecommender{home: []core.Hub{
		{ID: "home.trending", Title: "Trending", Style: "hero", Promoted: true, Items: []core.Title{fightClub, breakingBad}},
		{ID: "home.top10", Title: "Top", Promoted: true, Items: []core.Title{alien, breakingBad}},
	}, section: map[core.MediaType][]core.Hub{
		core.TypeMovie: {{ID: "movie.popular", Title: "Movies", Type: core.TypeMovie, Items: []core.Title{fightClub}}},
		core.TypeShow:  {{ID: "tv.popular", Title: "TV", Type: core.TypeShow, Items: []core.Title{breakingBad}}},
	}})
	if _, err := f.pls.Create(context.Background(), ownerUser.ID, "List", []core.RatingKey{fightClub.Key}); err != nil {
		t.Fatal(err)
	}
	for _, pin := range []string{"1,2,playlists", "playlists,2", "1,2"} {
		seen := map[string]string{}
		for _, dir := range strings.Split(pin, ",") {
			path := "/hubs/promoted?excludeContinueWatching=1&contentDirectoryID=" + url.QueryEscape(dir) + "&pinnedContentDirectoryID=" + url.QueryEscape(pin)
			rec := f.get(t, path, jsonAccept...)
			var body struct {
				MediaContainer struct {
					Hub []model.Hub `json:"Hub"`
				} `json:"MediaContainer"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			for _, h := range body.MediaContainer.Hub {
				if prior := seen[h.HubIdentifier]; prior != "" {
					t.Fatalf("pin=%s duplicate %s in %s and %s", pin, h.HubIdentifier, prior, dir)
				}
				seen[h.HubIdentifier] = dir
				if h.HubIdentifier == "home.continue" {
					t.Fatalf("unexpected row in %s: %+v", dir, h)
				}
			}
		}
		for _, prefix := range []string{"movie", "tv"} {
			dir := "1"
			if prefix == "tv" {
				dir = "2"
			}
			if !strings.Contains(pin, dir) {
				continue
			}
			for _, row := range []string{"home.trending", "home.top10"} {
				if seen[prefix+"."+row] != dir {
					t.Fatalf("pin=%s row %s: %v", pin, row, seen)
				}
			}
		}
		if seen["movie.popular"] != "" || seen["tv.popular"] != "" {
			t.Fatalf("pin=%s library recommendations leaked into Home: %v", pin, seen)
		}
		if strings.Contains(pin, "playlists") && seen["home.playlists"] != "playlists" {
			t.Fatalf("pin=%s playlist placement: %v", pin, seen)
		}
	}
	if rec := f.get(t, "/hubs/continueWatching?contentDirectoryID=playlists", jsonAccept...); rec.Code != 404 {
		t.Fatalf("playlist-only continue: %d", rec.Code)
	}
	for _, path := range []string{"/hubs?contentDirectoryID=unknown", "/hubs/promoted?contentDirectoryID=unknown"} {
		if ids := hubIDs(t, f.get(t, path, jsonAccept...).Body.Bytes()); len(ids) != 0 {
			t.Fatalf("%s leaked rows: %v", path, ids)
		}
	}
}

type noScanCustomLibraryExperience struct {
	frontend.Experience
	t *testing.T
}

func (e *noScanCustomLibraryExperience) PublishedViews(context.Context) ([]frontend.View, error) {
	return []frontend.View{
		{ID: "1", LibraryID: core.LibraryMovies, Name: "Movies", Type: core.TypeMovie, Native: plexBaseMovie},
		{ID: "2", LibraryID: core.LibraryShows, Name: "TV Shows", Type: core.TypeShow, Native: plexBaseShow},
		{ID: "10", LibraryID: 10, Name: "Kids Movies", Type: core.TypeMovie, Virtual: true, Native: plexBaseMovie},
	}, nil
}

func (e *noScanCustomLibraryExperience) LibraryItems(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (core.Page, error) {
	e.t.Fatal("hub request path must never scan LibraryItems")
	return core.Page{}, nil
}

type scopedCustomRecommender struct {
	*fakeRecommender
}

func (r *scopedCustomRecommender) HomeHubsFor(_ context.Context, _ *core.User, _ core.HubContext, _ int) ([]core.Hub, error) {
	return r.home, nil
}

func (r *scopedCustomRecommender) SectionHubsFor(_ context.Context, _ *core.User, typ core.MediaType, hc core.HubContext, _ int) ([]core.Hub, error) {
	if hc.SectionID == 10 {
		return []core.Hub{{ID: "movie.top10", Title: "Top Kids Movies", Type: core.TypeMovie, Promoted: true, Items: []core.Title{alien, breakingBad}}}, nil
	}
	return r.section[typ], nil
}

// W27: When both Movies and TV Shows are pinned on Plex Home, no recommendation
// row appears twice across the two sections' responses, each section's rows
// contain only its media type and visibility, cross-library Continue Watching
// appears at most once on Home, and custom (#549) libraries stay scoped.
func TestPinnedMovieAndShowHomeNoDuplicatesAndStrictType(t *testing.T) {
	const kidToken = "kid-token"
	f := newFixture(func(d *testServerDeps) {
		setDepsAuth(d, &fakeAuth{users: map[string]core.User{
			ownerToken:  ownerUser,
			friendToken: friendUser,
			kidToken:    {ID: 77, Username: "kid", AllowedLibraries: []core.LibraryID{10}},
		}})
	})
	defer f.srv.Close()

	ctx := context.Background()
	db := discoveryDB(t)
	wl := watchlist.New(db, watchlist.Options{Catalog: f.catalog})
	if err := wl.SaveSettings(ctx, watchlist.Settings{Enabled: true, CheckHours: 6}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []core.RatingKey{fightClub.Key, bbE1.Key} {
		if err := wl.Remind(ctx, &ownerUser, k); err != nil {
			t.Fatal(err)
		}
	}
	f.srv.setDiscovery((&discovery.Features{Watchlist: wl, Overlays: overlays.New(db, overlays.Options{})}).Frontend())
	f.state.set(ownerUser.ID, core.WatchStatus{Key: fightClub.Key, ViewOffset: time.Minute, LastViewedAt: fixedNow})
	f.state.set(ownerUser.ID, core.WatchStatus{Key: bbE1.Key, ViewOffset: time.Minute, LastViewedAt: fixedNow})

	f.srv.setRecommender(&scopedCustomRecommender{&fakeRecommender{
		home: []core.Hub{
			{ID: "home.top10", Title: "Top 10 for You", Promoted: true, Items: []core.Title{fightClub, breakingBad}},
			{ID: "home.trending", Title: "Trending Now", Promoted: true, Items: []core.Title{alien, breakingBad}},
		},
		section: map[core.MediaType][]core.Hub{
			core.TypeMovie: {
				{ID: "movie.top10", Title: "Top 10 Movies", Type: core.TypeMovie, Promoted: true, Items: []core.Title{fightClub, breakingBad}},
				{ID: "collection.shared", Title: "Staff Picks", Promoted: true, Items: []core.Title{alien, breakingBad}},
			},
			core.TypeShow: {
				{ID: "tv.top10", Title: "Top 10 Shows", Type: core.TypeShow, Promoted: true, Items: []core.Title{breakingBad, fightClub}},
				{ID: "collection.shared", Title: "Staff Picks", Promoted: true, Items: []core.Title{alien, breakingBad}},
			},
		},
	}})
	f.srv.bindHost(frontend.Host{Experience: &noScanCustomLibraryExperience{Experience: f.srv.exp(), t: t}})

	decodeHubs := func(path string, extraHeaders ...string) []model.Hub {
		t.Helper()
		hdrs := append([]string{}, jsonAccept...)
		hdrs = append(hdrs, extraHeaders...)
		rec := f.get(t, path, hdrs...)
		if rec.Code != 200 {
			t.Fatalf("%s: status %d: %s", path, rec.Code, rec.Body.String())
		}
		var body struct {
			MediaContainer model.MediaContainer `json:"MediaContainer"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return body.MediaContainer.Hub
	}

	assertMovieOnly := func(where string, hubs []model.Hub) {
		t.Helper()
		if len(hubs) == 0 {
			t.Fatalf("%s returned no hubs", where)
		}
		for _, h := range hubs {
			if h.HubIdentifier == "home.continue" || h.HubIdentifier == "home.ondeck" {
				continue
			}
			if h.Type != "movie" {
				t.Fatalf("%s hub %s type = %q, want movie", where, h.HubIdentifier, h.Type)
			}
			for _, m := range h.Metadata {
				if m.Type != "movie" {
					t.Fatalf("%s hub %s leaked %q item %s", where, h.HubIdentifier, m.Type, m.RatingKey)
				}
			}
		}
	}

	assertShowOnly := func(where string, hubs []model.Hub) {
		t.Helper()
		if len(hubs) == 0 {
			t.Fatalf("%s returned no hubs", where)
		}
		for _, h := range hubs {
			if h.HubIdentifier == "home.continue" || h.HubIdentifier == "home.ondeck" {
				continue
			}
			for _, m := range h.Metadata {
				if m.Type != "show" && m.Type != "season" && m.Type != "episode" {
					t.Fatalf("%s hub %s leaked %q item %s", where, h.HubIdentifier, m.Type, m.RatingKey)
				}
			}
		}
	}

	for _, endpoints := range [][2]string{
		{
			"/hubs/promoted?excludeContinueWatching=1&contentDirectoryID=1&pinnedContentDirectoryID=1,2",
			"/hubs/promoted?excludeContinueWatching=1&contentDirectoryID=2&pinnedContentDirectoryID=1,2",
		},
		{
			"/hubs/sections/1/promoted",
			"/hubs/sections/2/promoted",
		},
	} {
		movieHubs := decodeHubs(endpoints[0])
		showHubs := decodeHubs(endpoints[1])
		assertMovieOnly(endpoints[0], movieHubs)
		assertShowOnly(endpoints[1], showHubs)

		seen := map[string]string{}
		for _, h := range movieHubs {
			if strings.HasPrefix(h.HubIdentifier, "movie.inprogress.") || strings.HasPrefix(h.HubIdentifier, "home.") {
				t.Fatalf("%s returned unexpected hub %s", endpoints[0], h.HubIdentifier)
			}
			seen[h.HubIdentifier] = endpoints[0]
			if strings.HasPrefix(h.Key, "/hubs/items/") {
				more := f.get(t, h.Key, jsonAccept...)
				var body struct{ MediaContainer model.MediaContainer }
				if err := json.Unmarshal(more.Body.Bytes(), &body); err != nil || len(body.MediaContainer.Metadata) == 0 {
					t.Fatalf("more fetch %s failed: %d %s", h.Key, more.Code, more.Body.String())
				}
				for _, m := range body.MediaContainer.Metadata {
					if m.Type != "movie" {
						t.Fatalf("more fetch %s leaked %q", h.Key, m.Type)
					}
				}
			}
		}
		for _, h := range showHubs {
			if strings.HasPrefix(h.HubIdentifier, "tv.inprogress.") || strings.HasPrefix(h.HubIdentifier, "home.") {
				t.Fatalf("%s returned unexpected hub %s", endpoints[1], h.HubIdentifier)
			}
			if prior := seen[h.HubIdentifier]; prior != "" {
				t.Fatalf("duplicate hubIdentifier %s across %s and %s", h.HubIdentifier, prior, endpoints[1])
			}
		}
	}

	// Without excludeContinueWatching, cross-library Continue Watching appears
	// once on Home (on the first pinned library), never once per library, even
	// when pinnedContentDirectoryID is omitted.
	for _, pinQuery := range []string{"&pinnedContentDirectoryID=1,2", ""} {
		mHubs := decodeHubs("/hubs/promoted?contentDirectoryID=1" + pinQuery)
		sHubs := decodeHubs("/hubs/promoted?contentDirectoryID=2" + pinQuery)
		cwCount := 0
		for _, h := range append(append([]model.Hub{}, mHubs...), sHubs...) {
			if h.HubIdentifier == "home.continue" {
				cwCount++
			}
		}
		if cwCount != 1 {
			t.Fatalf("pinQuery=%q home.continue count = %d, want 1", pinQuery, cwCount)
		}
		assertMovieOnly("movies"+pinQuery, mHubs)
		assertShowOnly("shows"+pinQuery, sHubs)
	}

	// Custom library (#549): restricted viewer allowed only library 10 gets
	// Kids Movies recommendations without colliding with section 1 or scanning LibraryItems.
	kidHubs := decodeHubs("/hubs/promoted?excludeContinueWatching=1&contentDirectoryID=10&pinnedContentDirectoryID=10", "X-Plex-Token", kidToken)
	assertMovieOnly("custom-library-10", kidHubs)
	for _, h := range kidHubs {
		for _, m := range h.Metadata {
			if m.LibrarySectionID != 10 {
				t.Fatalf("custom library item %s librarySectionID = %d, want 10", m.RatingKey, m.LibrarySectionID)
			}
		}
	}
	if denied := decodeHubs("/hubs/promoted?excludeContinueWatching=1&contentDirectoryID=1&pinnedContentDirectoryID=1,10", "X-Plex-Token", kidToken); len(denied) != 0 {
		t.Fatalf("restricted viewer leaked section 1 hubs: %+v", denied)
	}
	ownerBoth := decodeHubs("/hubs/promoted?excludeContinueWatching=1&contentDirectoryID=1,10&pinnedContentDirectoryID=1,10")
	seenCustom := map[string]bool{}
	for _, h := range ownerBoth {
		if seenCustom[h.HubIdentifier] {
			t.Fatalf("duplicate hubIdentifier %s across movie libraries 1 and 10", h.HubIdentifier)
		}
		seenCustom[h.HubIdentifier] = true
	}
}

func TestHomeDirectoryContinueFilter(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.state.set(ownerUser.ID, ws(fightClub.Key, 60000, 0))
	f.state.set(ownerUser.ID, ws(bbE1.Key, 60000, 0))
	for _, tc := range []struct {
		dir  string
		want map[string]bool
	}{
		{"1", map[string]bool{fightClub.Key.String(): true}},
		{"2", map[string]bool{bbE1.Key.String(): true}},
		{"playlists,2", map[string]bool{bbE1.Key.String(): true}},
		{"1,2,playlists", map[string]bool{fightClub.Key.String(): true, bbE1.Key.String(): true}},
	} {
		mc := decodeJSON(t, f.get(t, "/hubs/continueWatching?contentDirectoryID="+url.QueryEscape(tc.dir), jsonAccept...))
		hubs := mc["Hub"].([]any)
		items, _ := hubs[0].(map[string]any)["Metadata"].([]any)
		got := map[string]bool{}
		for _, item := range items {
			got[item.(map[string]any)["ratingKey"].(string)] = true
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %v want %v", tc.dir, got, tc.want)
		}
		for key := range tc.want {
			if !got[key] {
				t.Fatalf("%s: got %v want %v", tc.dir, got, tc.want)
			}
		}
	}
}

type hiddenHomeRows struct{ *fakeRecommender }

func (*hiddenHomeRows) HomeRowEnabled(context.Context, *core.User, core.HubContext, string) bool {
	return false
}

func (*hiddenHomeRows) FinalizeNeutralHome(_ context.Context, _ *core.User, _ core.HomeSituation, _ string, rows []core.NeutralHomeRow) ([]core.NeutralHomeRow, error) {
	out := rows[:0]
	for _, row := range rows {
		if row.ID != "home.trending" && row.ID != "movie.trending" && row.ID != "home.continue" {
			out = append(out, row)
		}
	}
	return out, nil
}

func TestHomeDirectoryRespectsHiddenRows(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.srv.setRecommender(&hiddenHomeRows{&fakeRecommender{
		home: []core.Hub{
			{ID: "home.trending", Title: "Trending", Promoted: true, Items: []core.Title{fightClub}},
			{ID: "home.top10", Title: "Top", Promoted: true, Items: []core.Title{alien}},
		},
		section: map[core.MediaType][]core.Hub{
			core.TypeMovie: {
				{ID: "movie.trending", Title: "Trending", Type: core.TypeMovie, Promoted: true, Items: []core.Title{fightClub}},
				{ID: "movie.top10", Title: "Top", Type: core.TypeMovie, Promoted: true, Items: []core.Title{alien}},
			},
		},
	}})
	f.state.set(ownerUser.ID, ws(fightClub.Key, 60000, 0))
	for _, dir := range []string{"1", "2", "playlists"} {
		ids := hubIDs(t, f.get(t, "/hubs/promoted?contentDirectoryID="+dir+"&pinnedContentDirectoryID=1,2,playlists", jsonAccept...).Body.Bytes())
		for _, id := range ids {
			if id == "home.trending" || id == "movie.trending" || id == "home.continue" {
				t.Fatalf("hidden %s in %s", id, dir)
			}
		}
	}
	if rec := f.get(t, "/hubs/continueWatching?contentDirectoryID=1,2", jsonAccept...); rec.Code != 404 {
		t.Fatalf("hidden continue endpoint returned %d", rec.Code)
	}
}
