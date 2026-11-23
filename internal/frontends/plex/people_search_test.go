package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestRealCatalogPeopleSearchAcrossPlexEndpoints(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}

	seed := []core.Title{
		{
			Key: core.MovieKey(101), IDs: core.IDs{TMDB: 101}, Type: core.TypeMovie,
			Title: "Dune", Year: 2021, Released: fixedNow.AddDate(-3, 0, 0), Popularity: 140, VoteCount: 15000,
			PosterPath: "/dune.jpg",
			Cast:       []core.Person{{TMDB: 1640, Name: "Stellan Skarsgård", Role: "Baron Harkonnen", ThumbPath: "/stellan.jpg"}},
			Directors:  []core.Person{{TMDB: 137427, Name: "Denis Villeneuve", ThumbPath: "/denis.jpg"}},
		},
		{
			Key: core.MovieKey(102), IDs: core.IDs{TMDB: 102}, Type: core.TypeMovie,
			Title: "Late Night Sketch", Year: 2022, Released: fixedNow.AddDate(-2, 0, 0), Popularity: 260, VoteCount: 25000,
			PosterPath: "/sketch.jpg",
			Cast:       []core.Person{{TMDB: 1640, Name: "Stellan Skarsgård", Role: "Self - Guest", ThumbPath: "/stellan.jpg"}},
		},
		{
			Key: core.MovieKey(103), IDs: core.IDs{TMDB: 103}, Type: core.TypeMovie,
			Title: "Oppenheimer", Year: 2023, Released: fixedNow.AddDate(-1, 0, 0), Popularity: 180, VoteCount: 20000,
			PosterPath: "/oppenheimer.jpg",
			Cast:       []core.Person{{TMDB: 2037, Name: "Cillian Murphy", Role: "Oppenheimer"}},
		},
	}
	for _, it := range seed {
		if err := cat.UpsertTitle(ctx, it, true); err != nil {
			t.Fatal(err)
		}
	}
	// Crew credit (composer with diacritic) on Oppenheimer.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO catalog_credits(key, person_tmdb, name, role, kind, ord, thumb) VALUES (?, 935717, 'Ludwig Göransson', 'Original Music Composer', 'crew', 0, '/ludwig.jpg')`,
		int64(core.MovieKey(103)),
	); err != nil {
		t.Fatal(err)
	}

	f := newFixture(func(d *testServerDeps) {
		d.Catalog = cat
	})
	defer f.srv.Close()

	// 1. /hubs/search with unaccented "Stellan Skarsgard" returns actor hub WITH thumb headshot
	//    AND movie hub in lead-role order (Dune ahead of Self appearance) with reason="actor".
	rec := f.get(t, "/hubs/search?query=Stellan+Skarsgard&limit=10", jsonAccept...)
	if rec.Code != http.StatusOK {
		t.Fatalf("/hubs/search status = %d: %s", rec.Code, rec.Body.String())
	}
	mc := decodeJSON(t, rec)
	var actorHub, movieHub map[string]any
	for _, raw := range mc["Hub"].([]any) {
		h := raw.(map[string]any)
		switch h["hubIdentifier"] {
		case "actor":
			actorHub = h
		case "movie":
			movieHub = h
		}
	}
	if actorHub == nil || int(actorHub["size"].(float64)) != 1 {
		t.Fatalf("actor hub = %#v", actorHub)
	}
	actorDir := actorHub["Directory"].([]any)[0].(map[string]any)
	thumb, _ := actorDir["thumb"].(string)
	if actorDir["tag"] != "Stellan Skarsgård" || thumb == "" {
		t.Fatalf("actor Directory missing tag/thumb: %#v", actorDir)
	}
	if portrait := f.get(t, thumb); portrait.Code != http.StatusOK {
		t.Fatalf("actor headshot %s status = %d", thumb, portrait.Code)
	}
	movies := movieHub["Metadata"].([]any)
	if len(movies) != 2 || movies[0].(map[string]any)["title"] != "Dune" {
		t.Fatalf("movie hub did not lead with starring role Dune: %#v", movies)
	}
	if movies[0].(map[string]any)["reason"] != "actor" || movies[0].(map[string]any)["reasonTitle"] != "Stellan Skarsgård" {
		t.Fatalf("movie hub lost actor reason metadata: %#v", movies[0])
	}

	// 2. Crew credit ("Ludwig Goransson") surfaces Oppenheimer in /hubs/search, /library/search, and /search.
	for _, path := range []string{
		"/hubs/search?query=Ludwig+Goransson&limit=10",
		"/library/search?query=Ludwig+Goransson",
		"/search?query=Ludwig+Goransson",
	} {
		r := f.get(t, path, jsonAccept...)
		if r.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", path, r.Code, r.Body.String())
		}
		if !containsStringValue(decodeJSON(t, r), "Oppenheimer") {
			t.Fatalf("%s did not return Oppenheimer for crew composer Ludwig Göransson: %s", path, r.Body.String())
		}
	}

	// 3. /library/search?query=Stellan+Skarsgard returns BOTH person Directory (with thumb) AND his titles.
	libRec := f.get(t, "/library/search?query=Stellan+Skarsgard", jsonAccept...)
	if libRec.Code != http.StatusOK {
		t.Fatalf("/library/search status = %d", libRec.Code)
	}
	libMC := decodeJSON(t, libRec)
	results := libMC["SearchResult"].([]any)
	hasDir, hasMovie := false, false
	for _, raw := range results {
		sr := raw.(map[string]any)
		if d, ok := sr["Directory"].(map[string]any); ok && d["tag"] == "Stellan Skarsgård" && d["thumb"] != "" {
			hasDir = true
		}
		if m, ok := sr["Metadata"].(map[string]any); ok && m["title"] == "Dune" {
			hasMovie = true
		}
	}
	if !hasDir || !hasMovie {
		t.Fatalf("/library/search expected both person Directory (with thumb) and movie Metadata: %#v", results)
	}
}

// Issue #653: Cast & Crew tags and people search directories must not emit a
// fabricated tmdb-person- tagKey (Discover Credits rejects it), and section
// pills advertised by people search must only name sections whose Browse
// endpoint (/library/sections/{id}/all?actor=<id>) actually returns titles.
func TestPeopleSearchSectionPillsMatchBrowseAndOmitFakeTagKey(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil,
		catalog.WithNow(func() time.Time { return fixedNow }),
		catalog.WithLibraryPolicies(catalog.LibraryPolicies{
			MovieRules: []core.LibraryExclusion{{
				Name:      "no-discovery-docs",
				Action:    "hide",
				MediaType: "movie",
				Match:     "all",
				Scope:     core.VisibilityDiscovery,
				Rules:     []core.CollectionRule{{Field: "genre", Op: "is", Value: json.RawMessage(`"Documentary"`)}},
			}},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Actor with a non-browsable (search-only tier) movie, a discovery-hidden
	// documentary movie, and a browsable TV show: only TV Shows may appear as
	// an actor section pill, never an empty Movies pill.
	milly := core.Person{TMDB: 2210188, Name: "Milly Alcock", Role: "Lead", ThumbPath: "/milly.jpg"}
	if err := cat.UpsertTitle(ctx, core.Title{
		Key: core.MovieKey(301), IDs: core.IDs{TMDB: 301}, Type: core.TypeMovie,
		Title: "Search Only Indie", Year: 2022, Released: fixedNow.AddDate(-2, 0, 0),
		Cast: []core.Person{milly},
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := cat.UpsertTitle(ctx, core.Title{
		Key: core.MovieKey(302), IDs: core.IDs{TMDB: 302}, Type: core.TypeMovie,
		Title: "Browse Hidden Doc", Year: 2023, Released: fixedNow.AddDate(-1, 0, 0),
		Genres: []string{"Documentary"},
		Cast:   []core.Person{milly},
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := cat.UpsertTitle(ctx, core.Title{
		Key: core.ShowKey(303), IDs: core.IDs{TMDB: 303}, Type: core.TypeShow,
		Title: "Dragon House", Year: 2022, Released: fixedNow.AddDate(-2, 0, 0),
		Cast: []core.Person{milly},
	}, true); err != nil {
		t.Fatal(err)
	}
	// Episode-only guest star: must not advertise an empty TV Shows pill.
	guest := core.Person{TMDB: 990123, Name: "Guest Cameo", Role: "Cameo"}
	if err := cat.UpsertTitle(ctx, core.Title{
		Key: core.EpisodeKey(303, 1, 1), IDs: core.IDs{TMDB: 303}, Type: core.TypeEpisode,
		ShowKey: core.ShowKey(303), ShowTitle: "Dragon House", Season: 1, Episode: 1,
		Title: "Pilot", Year: 2022, Released: fixedNow.AddDate(-2, 0, 0),
		Cast: []core.Person{guest},
	}, true); err != nil {
		t.Fatal(err)
	}

	f := newFixture(func(d *testServerDeps) { d.Catalog = cat })
	defer f.srv.Close()

	// 1. Detail page Role tags omit fabricated tagKey while keeping id and filter.
	det := f.get(t, "/library/metadata/"+core.ShowKey(303).String(), jsonAccept...)
	if det.Code != http.StatusOK {
		t.Fatalf("detail status = %d: %s", det.Code, det.Body.String())
	}
	detMD := decodeJSON(t, det)["Metadata"].([]any)[0].(map[string]any)
	role := detMD["Role"].([]any)[0].(map[string]any)
	if int64(role["id"].(float64)) != milly.TMDB || role["filter"] != "actor=2210188" {
		t.Fatalf("detail Role lost id/filter: %#v", role)
	}
	if _, hasTagKey := role["tagKey"]; hasTagKey {
		t.Fatalf("detail Role emitted fabricated tagKey: %#v", role)
	}

	// 2. /hubs/search and /library/search advertise ONLY TV Shows for Milly Alcock
	//    (not Movies, which Browse excludes) and omit fabricated tagKey.
	rec := f.get(t, "/hubs/search?query=Milly+Alcock&limit=10", jsonAccept...)
	if rec.Code != http.StatusOK {
		t.Fatalf("/hubs/search status = %d", rec.Code)
	}
	var actorHub map[string]any
	for _, raw := range decodeJSON(t, rec)["Hub"].([]any) {
		if h := raw.(map[string]any); h["hubIdentifier"] == "actor" {
			actorHub = h
		}
	}
	dirs, _ := actorHub["Directory"].([]any)
	if len(dirs) != 1 {
		t.Fatalf("expected only TV Shows pill (no empty Movies pill), got %#v", dirs)
	}
	dir := dirs[0].(map[string]any)
	if dir["key"] != "/library/sections/2/all?actor=2210188" || dir["librarySectionTitle"] != "TV Shows" {
		t.Fatalf("unexpected actor Directory: %#v", dir)
	}
	if _, hasTagKey := dir["tagKey"]; hasTagKey {
		t.Fatalf("actor Directory emitted fabricated tagKey: %#v", dir)
	}
	// Following every advertised section pill must return >= 1 title.
	secRec := f.get(t, dir["key"].(string), jsonAccept...)
	if secRec.Code != http.StatusOK {
		t.Fatalf("section browse %s status = %d", dir["key"], secRec.Code)
	}
	if items, _ := decodeJSON(t, secRec)["Metadata"].([]any); len(items) != 1 {
		t.Fatalf("section browse %s returned %d items, want 1", dir["key"], len(items))
	}
	// Confirm Movies browse for Milly Alcock is indeed empty.
	moviesRec := f.get(t, "/library/sections/1/all?actor=2210188", jsonAccept...)
	if items, _ := decodeJSON(t, moviesRec)["Metadata"].([]any); len(items) != 0 {
		t.Fatalf("expected empty Movies browse for non-browsable/hidden credits, got %#v", items)
	}

	// 3. Episode-only guest actor must not emit an empty TV Shows pill.
	guestRec := f.get(t, "/hubs/search?query=Guest+Cameo&limit=10", jsonAccept...)
	for _, raw := range decodeJSON(t, guestRec)["Hub"].([]any) {
		if h := raw.(map[string]any); h["hubIdentifier"] == "actor" && int(h["size"].(float64)) != 0 {
			t.Fatalf("episode-only guest advertised empty section pill: %#v", h)
		}
	}
}

func containsStringValue(v any, target string) bool {
	switch x := v.(type) {
	case string:
		return x == target
	case map[string]any:
		for _, val := range x {
			if containsStringValue(val, target) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if containsStringValue(val, target) {
				return true
			}
		}
	}
	return false
}
