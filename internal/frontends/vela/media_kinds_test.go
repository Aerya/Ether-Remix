package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestVelaMediaClassesTitleMediaChildrenPagingAndSyntheticSeries(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, castMembers{}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer, err := grants.Issue(ctx, 1, nil, "vela", core.Device{ID: "tv"})
	if err != nil {
		t.Fatal(err)
	}

	artistKey := core.PluginKey(101)
	albumKey := core.PluginKey(102)
	trackKeys := []core.RatingKey{
		core.PluginKey(103),
		core.PluginKey(104),
		core.PluginKey(105),
		core.PluginKey(106),
		core.PluginKey(107),
	}
	book1Key := core.PluginKey(201)
	book15Key := core.PluginKey(202)
	book2Key := core.PluginKey(203)
	authorPersonID := core.PluginPersonID("Frank Herbert")
	artistPersonID := core.PluginPersonID("Miles Davis")

	albumTitle := core.Title{
		Key:        albumKey,
		Type:       core.TypeSeason,
		ShowKey:    artistKey,
		Season:     0,
		Episode:    0,
		Title:      "Kind of Blue",
		Summary:    "Recorded in 1959.",
		Year:       1959,
		Genres:     []string{"Jazz"},
		ChildCount: len(trackKeys),
		MediaClass: core.MediaClassMusic,
		MediaRole:  core.MediaRoleAlbum,
		MediaParents: []core.TitleParent{
			{Key: artistKey, Title: "Miles Davis", Role: core.MediaRoleArtist},
		},
		MediaCredits: []core.MediaCredit{
			{Name: "Miles Davis", Role: "artist", TMDB: artistPersonID},
		},
		MediaFacts: []core.TitleFact{
			{Label: "Label", Value: "Columbia"},
		},
	}

	tracks := make([]core.Title, len(trackKeys))
	for idx, k := range trackKeys {
		tracks[idx] = core.Title{
			Key:        k,
			Type:       core.TypeEpisode,
			ShowKey:    artistKey,
			ItemIndex:  idx + 1,
			Disc:       1,
			Title:      []string{"So What", "Freddie Freeloader", "Blue in Green", "All Blues", "Flamenco Sketches"}[idx],
			Runtime:    time.Duration(idx+3) * time.Minute,
			MediaClass: core.MediaClassMusic,
			MediaRole:  core.MediaRoleTrack,
			MediaParents: []core.TitleParent{
				{Key: albumKey, Title: "Kind of Blue", Role: core.MediaRoleAlbum},
				{Key: artistKey, Title: "Miles Davis", Role: core.MediaRoleArtist},
			},
			MediaCredits: []core.MediaCredit{
				{Name: "Miles Davis", Role: "artist", TMDB: artistPersonID},
			},
		}
	}

	books := []core.Title{
		{
			Key:        book2Key,
			LibraryID:  71,
			Type:       core.TypeMovie,
			ItemIndex:  2,
			Title:      "Dune Messiah",
			SortTitle:  "dune messiah",
			Genres:     []string{"Sci-Fi"},
			MediaClass: core.MediaClassBook,
			MediaRole:  core.MediaRoleBook,
			MediaParents: []core.TitleParent{
				{FacetID: "facet:71:series:Dune", Title: "Dune", Role: core.MediaRoleSeries, Index: 2},
			},
			MediaCredits: []core.MediaCredit{
				{Name: "Frank Herbert", Role: "author", TMDB: authorPersonID},
			},
			MediaReading: &core.TitleReading{Format: "epub", Pages: 256},
			MediaFacts:   []core.TitleFact{{Label: "Publisher", Value: "Putnam"}},
		},
		{
			Key:          book15Key,
			LibraryID:    71,
			Type:         core.TypeMovie,
			ItemIndex:    1,
			IndexDisplay: "1.5",
			Title:        "Dune Interlude",
			SortTitle:    "dune interlude",
			Genres:       []string{"Sci-Fi"},
			MediaClass:   core.MediaClassBook,
			MediaRole:    core.MediaRoleBook,
			MediaParents: []core.TitleParent{
				{FacetID: "facet:71:series:Dune", Title: "Dune", Role: core.MediaRoleSeries, Index: 1, IndexDisplay: "1.5"},
			},
			MediaCredits: []core.MediaCredit{
				{Name: "Frank Herbert", Role: "author", TMDB: authorPersonID},
			},
			MediaReading: &core.TitleReading{Format: "epub", Pages: 80},
		},
		{
			Key:        book1Key,
			LibraryID:  71,
			Type:       core.TypeMovie,
			ItemIndex:  1,
			Title:      "Dune",
			SortTitle:  "dune",
			Genres:     []string{"Sci-Fi", "Classic"},
			MediaClass: core.MediaClassBook,
			MediaRole:  core.MediaRoleBook,
			MediaParents: []core.TitleParent{
				{FacetID: "facet:71:series:Dune", Title: "Dune", Role: core.MediaRoleSeries, Index: 1},
			},
			MediaCredits: []core.MediaCredit{
				{Name: "Frank Herbert", Role: "author", TMDB: authorPersonID},
			},
			MediaReading: &core.TitleReading{Format: "epub", Pages: 412},
			MediaFacts:   []core.TitleFact{{Label: "Publisher", Value: "Chilton Books"}},
		},
	}

	videoSeasonKey := core.SeasonKey(55, 2)
	videoSeason := core.Title{
		Key:     videoSeasonKey,
		Type:    core.TypeSeason,
		ShowKey: core.ShowKey(55),
		Season:  2,
		Episode: 0,
		Title:   "Season 2",
	}

	views := []frontend.View{
		{ID: "1", Name: "Movies", Type: core.TypeMovie},
		{ID: "70", LibraryID: 70, Name: "Music", Type: core.TypeShow, PluginID: "community.music", KindKey: "music", Media: core.MediaClassMusic, Card: "square"},
		{
			ID: "71", LibraryID: 71, Name: "Books", Type: core.TypeMovie, PluginID: "community.books", KindKey: "book", Media: core.MediaClassBook, Card: "poster",
			Facets: []frontend.ViewFacet{{ID: "series", Label: "Series", Type: "string", Multi: true}},
			Tabs: []frontend.ViewTab{
				{ID: "library", Label: "Books", Kind: "library"},
				{ID: "series", Label: "Series", Kind: "collections", Facet: "series", Order: "name"},
			},
		},
	}

	canPlayFalse := false
	exp := frontendtest.Experience{
		ViewsFunc: func(context.Context, *core.Viewer) ([]frontend.View, error) { return views, nil },
		ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
			switch req.Key {
			case albumKey:
				return frontend.ItemDetail{Title: albumTitle, CanPlay: &canPlayFalse}, nil
			case book15Key:
				return frontend.ItemDetail{Title: books[1]}, nil
			case videoSeasonKey:
				return frontend.ItemDetail{Title: videoSeason}, nil
			default:
				return frontend.ItemDetail{}, core.ErrNotFound
			}
		},
		ChildrenFunc: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey, q frontend.ItemQuery) (core.Page, error) {
			if k != albumKey {
				return core.Page{Items: []core.Title{}}, nil
			}
			start := min(q.Offset, len(tracks))
			end := len(tracks)
			if q.Limit > 0 && start+q.Limit < end {
				end = start + q.Limit
			}
			return core.Page{Items: tracks[start:end], Total: len(tracks)}, nil
		},
		LibraryItemsFunc: func(_ context.Context, _ *core.Viewer, _ core.Device, viewID string, q frontend.ItemQuery) (core.Page, error) {
			if viewID == "71" && reflect.DeepEqual(q.Filter.Facets["series"], []string{"Dune"}) {
				return core.Page{Items: books, Total: len(books)}, nil
			}
			return core.Page{Items: []core.Title{}}, nil
		},
	}

	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{
		ctx:     ctx,
		db:      db,
		seal:    seal,
		replays: make(map[string]*replay),
		mux:     http.NewServeMux(),
		host: frontend.Host{
			Identity:       identityFake{},
			Experience:     exp,
			ServerIdentity: frontend.ServerIdentity{MachineID: "srv-1", FriendlyName: "Ether", Version: "0.3.17"},
		},
	}
	inst.routes()

	doGet := func(path string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("X-Vela-Device-Id", "tv")
		rr := httptest.NewRecorder()
		inst.serve(rr, req)
		var body map[string]any
		if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s (status %d): %v", path, rr.Code, err)
		}
		return rr.Code, body
	}

	// 1. Capabilities advertises features.mediaClasses with all 6 classes.
	code, caps := doGet("/vela/v0/capabilities")
	if code != 200 {
		t.Fatalf("capabilities status=%d body=%v", code, caps)
	}
	features := caps["features"].(map[string]any)
	mc, ok := features["mediaClasses"].(map[string]any)
	if !ok || mc["available"] != true {
		t.Fatalf("features.mediaClasses = %v", features["mediaClasses"])
	}
	wantClasses := []any{"video", "music", "audiobook", "book", "podcast", "photo"}
	if !reflect.DeepEqual(mc["classes"], wantClasses) {
		t.Fatalf("mediaClasses.classes = %v, want %v", mc["classes"], wantClasses)
	}

	// 2. GET /libraries emits media class on plugin libraries and omits it on video built-ins.
	code, libsResp := doGet("/vela/v0/libraries")
	if code != 200 {
		t.Fatalf("libraries status=%d body=%v", code, libsResp)
	}
	libsByID := map[string]map[string]any{}
	for _, raw := range libsResp["items"].([]any) {
		m := raw.(map[string]any)
		libsByID[m["id"].(string)] = m
	}
	if _, has := libsByID["1"]["media"]; has {
		t.Fatalf("built-in video library should omit media, got %v", libsByID["1"]["media"])
	}
	if libsByID["70"]["media"] != "music" || libsByID["71"]["media"] != "book" {
		t.Fatalf("plugin library media classes = music:%v book:%v", libsByID["70"]["media"], libsByID["71"]["media"])
	}

	// 3. Album detail emits Title.media and NEVER emits showId/seasonNumber/episodeNumber ("Season 0 Episode 0").
	code, albumDetail := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(albumKey))
	if code != 200 {
		t.Fatalf("album detail status=%d body=%v", code, albumDetail)
	}
	albumWire := albumDetail["title"].(map[string]any)
	for _, forbidden := range []string{"showId", "seasonNumber", "episodeNumber"} {
		if _, has := albumWire[forbidden]; has {
			t.Fatalf("music album leaked video field %q: %v", forbidden, albumWire)
		}
	}
	albumMedia := albumWire["media"].(map[string]any)
	if albumMedia["class"] != "music" || albumMedia["role"] != "album" || albumMedia["childCount"] != float64(5) {
		t.Fatalf("unexpected album media: %v", albumMedia)
	}
	parents := albumMedia["parents"].([]any)
	if len(parents) != 1 || parents[0].(map[string]any)["id"] != ids.RatingKeyGUID(artistKey) || parents[0].(map[string]any)["role"] != "artist" {
		t.Fatalf("unexpected album parents: %v", parents)
	}
	credits := albumMedia["credits"].([]any)
	if len(credits) != 1 || credits[0].(map[string]any)["role"] != "artist" || credits[0].(map[string]any)["personId"] == "" {
		t.Fatalf("unexpected album credits: %v", credits)
	}
	facts := albumMedia["facts"].([]any)
	if len(facts) != 1 || facts[0].(map[string]any)["label"] != "Label" || facts[0].(map[string]any)["value"] != "Columbia" {
		t.Fatalf("unexpected album facts: %v", facts)
	}

	// 4. Video season emits seasonNumber but NEVER episodeNumber: 0.
	code, seasonDetail := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(videoSeasonKey))
	if code != 200 {
		t.Fatalf("season detail status=%d body=%v", code, seasonDetail)
	}
	seasonWire := seasonDetail["title"].(map[string]any)
	if seasonWire["seasonNumber"] != float64(2) {
		t.Fatalf("seasonNumber = %v, want 2", seasonWire["seasonNumber"])
	}
	if _, hasEp := seasonWire["episodeNumber"]; hasEp {
		t.Fatalf("video season should not emit episodeNumber: %v", seasonWire)
	}

	// 5. GET /titles/{albumId}/children cursor pagination: 5 tracks at limit=2 -> 3 pages, terminating cleanly.
	code, p1 := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(albumKey) + "/children?limit=2")
	if code != 200 || p1["continuation"] != "more" || len(p1["items"].([]any)) != 2 {
		t.Fatalf("children page 1 = %d %v", code, p1)
	}
	c1, _ := p1["nextCursor"].(string)
	if c1 == "" {
		t.Fatalf("children page 1 missing nextCursor: %v", p1)
	}
	tr0 := p1["items"].([]any)[0].(map[string]any)
	tr0Media := tr0["media"].(map[string]any)
	if tr0Media["class"] != "music" || tr0Media["role"] != "track" || tr0Media["index"] != float64(1) || tr0Media["disc"] != float64(1) {
		t.Fatalf("unexpected track media: %v", tr0Media)
	}
	tr0Parents := tr0Media["parents"].([]any)
	if len(tr0Parents) != 2 || tr0Parents[0].(map[string]any)["role"] != "album" || tr0Parents[1].(map[string]any)["role"] != "artist" {
		t.Fatalf("track parents must be nearest-first [album, artist], got %v", tr0Parents)
	}

	code, p2 := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(albumKey) + "/children?limit=2&cursor=" + url.QueryEscape(c1))
	if code != 200 || p2["continuation"] != "more" || len(p2["items"].([]any)) != 2 {
		t.Fatalf("children page 2 = %d %v", code, p2)
	}
	c2, _ := p2["nextCursor"].(string)
	code, p3 := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(albumKey) + "/children?limit=2&cursor=" + url.QueryEscape(c2))
	if code != 200 || p3["continuation"] != "exhausted" || len(p3["items"].([]any)) != 1 {
		t.Fatalf("children page 3 = %d %v", code, p3)
	}
	if _, hasNext := p3["nextCursor"]; hasNext {
		t.Fatalf("exhausted children page should not include nextCursor: %v", p3)
	}
	// Exact-multiple limit (limit=5 for 5 tracks) MUST return "exhausted", NEVER "unknown".
	code, pExact := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(albumKey) + "/children?limit=5")
	if code != 200 || pExact["continuation"] != "exhausted" || len(pExact["items"].([]any)) != 5 {
		t.Fatalf("exact-limit children page = %d %v", code, pExact)
	}
	// Cursor with different limit returns 409.
	if badCode, _ := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(albumKey) + "/children?limit=3&cursor=" + url.QueryEscape(c1)); badCode != 409 {
		t.Fatalf("mismatched cursor limit status=%d, want 409", badCode)
	}

	// 6. Synthetic facet-backed series parent on 1-level book + /titles/facet:series:Dune + /children.
	code, bookDetail := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(book15Key))
	if code != 200 {
		t.Fatalf("book detail status=%d body=%v", code, bookDetail)
	}
	bookMedia := bookDetail["title"].(map[string]any)["media"].(map[string]any)
	if bookMedia["class"] != "book" || bookMedia["role"] != "book" || bookMedia["index"] != float64(1) || bookMedia["indexDisplay"] != "1.5" {
		t.Fatalf("unexpected book media: %v", bookMedia)
	}
	reading := bookMedia["reading"].(map[string]any)
	if reading["format"] != "epub" || reading["pages"] != float64(80) {
		t.Fatalf("unexpected book reading: %v", reading)
	}
	bParents := bookMedia["parents"].([]any)
	if len(bParents) != 1 || bParents[0].(map[string]any)["id"] != "facet:71:series:Dune" || bParents[0].(map[string]any)["role"] != "series" || bParents[0].(map[string]any)["indexDisplay"] != "1.5" {
		t.Fatalf("unexpected book series parent: %v", bParents)
	}

	// Opening the library-scoped synthetic series ID directly returns SeriesPage metadata.
	code, seriesDetail := doGet("/vela/v0/titles/" + url.PathEscape("facet:71:series:Dune"))
	if code != 200 {
		t.Fatalf("synthetic series detail status=%d body=%v", code, seriesDetail)
	}
	seriesTitle := seriesDetail["title"].(map[string]any)
	seriesMedia := seriesTitle["media"].(map[string]any)
	if seriesTitle["id"] != "facet:71:series:Dune" || seriesTitle["name"] != "Dune" || seriesMedia["class"] != "book" || seriesMedia["role"] != "series" || seriesMedia["childCount"] != float64(3) {
		t.Fatalf("unexpected synthetic series detail: %v", seriesDetail)
	}

	// Fetching synthetic series children returns books ordered 1, 1.5, 2 with paging.
	code, sc1 := doGet("/vela/v0/titles/" + url.PathEscape("facet:71:series:Dune") + "/children?limit=2")
	if code != 200 || sc1["continuation"] != "more" {
		t.Fatalf("series children page 1 = %d %v", code, sc1)
	}
	scItems1 := sc1["items"].([]any)
	if scItems1[0].(map[string]any)["name"] != "Dune" || scItems1[1].(map[string]any)["name"] != "Dune Interlude" {
		t.Fatalf("series children page 1 order = %v", scItems1)
	}
	code, sc2 := doGet("/vela/v0/titles/" + url.PathEscape("facet:71:series:Dune") + "/children?limit=2&cursor=" + url.QueryEscape(sc1["nextCursor"].(string)))
	if code != 200 || sc2["continuation"] != "exhausted" {
		t.Fatalf("series children page 2 = %d %v", code, sc2)
	}
	scItems2 := sc2["items"].([]any)
	if len(scItems2) != 1 || scItems2[0].(map[string]any)["name"] != "Dune Messiah" {
		t.Fatalf("series children page 2 = %v", scItems2)
	}
}

func TestVelaSyntheticSeriesScopedToLibrary(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, castMembers{}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer, err := grants.Issue(ctx, 1, nil, "vela", core.Device{ID: "web"})
	if err != nil {
		t.Fatal(err)
	}

	bookExpanseKey := core.PluginKey(3501)
	bookOnlyKey := core.PluginKey(3502)
	audioExpanseKey := core.PluginKey(3601)
	audioColonKey := core.PluginKey(3602)

	// Deliberately pass unscoped FacetIDs on Titles with LibraryID set to verify
	// titleMediaWire also scopes them to the item's library.
	bookExpanse := core.Title{
		Key:        bookExpanseKey,
		LibraryID:  35,
		Type:       core.TypeMovie,
		ItemIndex:  1,
		Title:      "Leviathan Wakes (Book)",
		SortTitle:  "leviathan wakes book",
		MediaClass: core.MediaClassBook,
		MediaRole:  core.MediaRoleBook,
		MediaParents: []core.TitleParent{
			{FacetID: "facet:series:The Expanse", Title: "The Expanse", Role: core.MediaRoleSeries, Index: 1},
		},
	}
	bookOnly := core.Title{
		Key:        bookOnlyKey,
		LibraryID:  35,
		Type:       core.TypeMovie,
		ItemIndex:  1,
		Title:      "Books Only Vol 1",
		SortTitle:  "books only vol 1",
		MediaClass: core.MediaClassBook,
		MediaRole:  core.MediaRoleBook,
		MediaParents: []core.TitleParent{
			{FacetID: "facet:35:series:BooksOnly", Title: "BooksOnly", Role: core.MediaRoleSeries, Index: 1},
		},
	}
	audioExpanse := core.Title{
		Key:        audioExpanseKey,
		LibraryID:  36,
		Type:       core.TypeMovie,
		ItemIndex:  1,
		Title:      "Leviathan Wakes (Audiobook)",
		SortTitle:  "leviathan wakes audiobook",
		MediaClass: core.MediaClassAudiobook,
		MediaRole:  core.MediaRoleAudiobook,
		MediaParents: []core.TitleParent{
			{FacetID: "facet:series:The Expanse", Title: "The Expanse", Role: core.MediaRoleSeries, Index: 1},
		},
	}
	audioColon := core.Title{
		Key:        audioColonKey,
		LibraryID:  36,
		Type:       core.TypeMovie,
		ItemIndex:  1,
		Title:      "Thrawn (Audiobook)",
		SortTitle:  "thrawn audiobook",
		MediaClass: core.MediaClassAudiobook,
		MediaRole:  core.MediaRoleAudiobook,
		MediaParents: []core.TitleParent{
			{FacetID: "facet:36:series:Star Wars: Thrawn", Title: "Star Wars: Thrawn", Role: core.MediaRoleSeries, Index: 1},
		},
	}

	// Books (lib 35) appears BEFORE Audiobooks (lib 36) in Views(), matching the lab repro.
	views := []frontend.View{
		{
			ID: "35", LibraryID: 35, Name: "Books", Type: core.TypeMovie, PluginID: "community.books", KindKey: "book", Media: core.MediaClassBook,
			Facets: []frontend.ViewFacet{{ID: "series", Label: "Series", Type: "string"}},
		},
		{
			ID: "36", LibraryID: 36, Name: "Audiobooks", Type: core.TypeMovie, PluginID: "community.books", KindKey: "audiobook", Media: core.MediaClassAudiobook,
			Facets: []frontend.ViewFacet{{ID: "series", Label: "Series", Type: "string"}},
		},
	}

	exp := frontendtest.Experience{
		ViewsFunc: func(context.Context, *core.Viewer) ([]frontend.View, error) { return views, nil },
		ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
			switch req.Key {
			case bookExpanseKey:
				return frontend.ItemDetail{Title: bookExpanse}, nil
			case audioExpanseKey:
				return frontend.ItemDetail{Title: audioExpanse}, nil
			default:
				return frontend.ItemDetail{}, core.ErrNotFound
			}
		},
		LibraryItemsFunc: func(_ context.Context, _ *core.Viewer, _ core.Device, viewID string, q frontend.ItemQuery) (core.Page, error) {
			vals := q.Filter.Facets["series"]
			if len(vals) != 1 {
				return core.Page{}, nil
			}
			switch viewID {
			case "35":
				if vals[0] == "The Expanse" {
					return core.Page{Items: []core.Title{bookExpanse}, Total: 1}, nil
				}
				if vals[0] == "BooksOnly" {
					return core.Page{Items: []core.Title{bookOnly}, Total: 1}, nil
				}
			case "36":
				if vals[0] == "The Expanse" {
					return core.Page{Items: []core.Title{audioExpanse}, Total: 1}, nil
				}
				if vals[0] == "Star Wars: Thrawn" {
					return core.Page{Items: []core.Title{audioColon}, Total: 1}, nil
				}
			}
			return core.Page{}, nil
		},
	}

	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{
		ctx:     ctx,
		db:      db,
		seal:    seal,
		replays: make(map[string]*replay),
		mux:     http.NewServeMux(),
		host: frontend.Host{
			Identity:       identityFake{},
			Experience:     exp,
			ServerIdentity: frontend.ServerIdentity{MachineID: "srv-1", FriendlyName: "Ether", Version: "0.3.17"},
		},
	}
	inst.routes()

	doGet := func(path string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("X-Vela-Device-Id", "web")
		rec := httptest.NewRecorder()
		inst.serve(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// 1. Audiobook in lib 36 emits library-scoped parent id facet:36:series:The Expanse.
	code, audioDetail := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(audioExpanseKey))
	if code != 200 {
		t.Fatalf("audiobook detail status=%d body=%v", code, audioDetail)
	}
	audioParents := audioDetail["title"].(map[string]any)["media"].(map[string]any)["parents"].([]any)
	if len(audioParents) != 1 || audioParents[0].(map[string]any)["id"] != "facet:36:series:The Expanse" {
		t.Fatalf("audiobook series parent = %v, want facet:36:series:The Expanse", audioParents)
	}

	// Book in lib 35 emits facet:35:series:The Expanse.
	code, bookDetail := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(bookExpanseKey))
	if code != 200 {
		t.Fatalf("book detail status=%d body=%v", code, bookDetail)
	}
	bookParents := bookDetail["title"].(map[string]any)["media"].(map[string]any)["parents"].([]any)
	if len(bookParents) != 1 || bookParents[0].(map[string]any)["id"] != "facet:35:series:The Expanse" {
		t.Fatalf("book series parent = %v, want facet:35:series:The Expanse", bookParents)
	}

	// 2. Opening facet:36:series:The Expanse resolves ONLY against Audiobooks (lib 36).
	code, audioSeries := doGet("/vela/v0/titles/" + url.PathEscape("facet:36:series:The Expanse"))
	if code != 200 {
		t.Fatalf("audiobook series detail status=%d body=%v", code, audioSeries)
	}
	aTitle := audioSeries["title"].(map[string]any)
	aMedia := aTitle["media"].(map[string]any)
	if aTitle["id"] != "facet:36:series:The Expanse" || aTitle["name"] != "The Expanse" || aMedia["class"] != "audiobook" || aMedia["childCount"] != float64(1) {
		t.Fatalf("unexpected audiobook series detail: %v", audioSeries)
	}
	code, audioChildren := doGet("/vela/v0/titles/" + url.PathEscape("facet:36:series:The Expanse") + "/children")
	if code != 200 {
		t.Fatalf("audiobook series children status=%d body=%v", code, audioChildren)
	}
	aItems := audioChildren["items"].([]any)
	if len(aItems) != 1 || aItems[0].(map[string]any)["name"] != "Leviathan Wakes (Audiobook)" || aItems[0].(map[string]any)["media"].(map[string]any)["class"] != "audiobook" {
		t.Fatalf("expected only audiobook children from lib 36, got %v", aItems)
	}

	// 3. Opening facet:35:series:The Expanse resolves ONLY against Books (lib 35).
	code, bookChildren := doGet("/vela/v0/titles/" + url.PathEscape("facet:35:series:The Expanse") + "/children")
	if code != 200 {
		t.Fatalf("book series children status=%d body=%v", code, bookChildren)
	}
	bItems := bookChildren["items"].([]any)
	if len(bItems) != 1 || bItems[0].(map[string]any)["name"] != "Leviathan Wakes (Book)" || bItems[0].(map[string]any)["media"].(map[string]any)["class"] != "book" {
		t.Fatalf("expected only book children from lib 35, got %v", bItems)
	}

	// 4. A series that exists ONLY in Books (lib 35) MUST return 404 when requested under lib 36 (no cross-library fallback).
	if code404, body404 := doGet("/vela/v0/titles/" + url.PathEscape("facet:36:series:BooksOnly")); code404 != 404 {
		t.Fatalf("cross-library series detail status=%d body=%v, want 404", code404, body404)
	}
	if code404, body404 := doGet("/vela/v0/titles/" + url.PathEscape("facet:36:series:BooksOnly") + "/children"); code404 != 404 {
		t.Fatalf("cross-library series children status=%d body=%v, want 404", code404, body404)
	}

	// 5. Series names containing colons parse cleanly when library-scoped.
	code, colonSeries := doGet("/vela/v0/titles/" + url.PathEscape("facet:36:series:Star Wars: Thrawn"))
	if code != 200 {
		t.Fatalf("colon series detail status=%d body=%v", code, colonSeries)
	}
	cTitle := colonSeries["title"].(map[string]any)
	if cTitle["id"] != "facet:36:series:Star Wars: Thrawn" || cTitle["name"] != "Star Wars: Thrawn" {
		t.Fatalf("unexpected colon series title: %v", cTitle)
	}
}

func TestVelaAudiobookDetailProbedChaptersAndMultiPartChildren(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, castMembers{}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer, err := grants.Issue(ctx, 1, nil, "vela", core.Device{ID: "web"})
	if err != nil {
		t.Fatal(err)
	}

	atomicKey := core.PluginKey(9001)
	libriKey := core.PluginKey(9002)
	part0Key, _ := core.PluginPartKey(libriKey, 0)
	part1Key, _ := core.PluginPartKey(libriKey, 1)

	atomic := core.Title{
		Key:        atomicKey,
		Type:       core.TypeMovie,
		LibraryID:  36,
		Title:      "Atomic Habits",
		Runtime:    20120 * time.Second,
		MediaClass: core.MediaClassAudiobook,
		MediaRole:  core.MediaRoleAudiobook,
	}
	libri := core.Title{
		Key:        libriKey,
		Type:       core.TypeMovie,
		LibraryID:  36,
		Title:      "Northanger Abbey",
		ChildCount: 2,
		Runtime:    1500 * time.Second,
		MediaClass: core.MediaClassAudiobook,
		MediaRole:  core.MediaRoleAudiobook,
	}
	part0 := core.Title{
		Key:          part0Key,
		Type:         core.TypeEpisode,
		LibraryID:    36,
		Title:        "Chapter 01",
		ItemIndex:    1,
		Runtime:      600 * time.Second,
		MediaClass:   core.MediaClassAudiobook,
		MediaRole:    core.MediaRolePart,
		MediaParents: []core.TitleParent{{Key: libriKey, Title: "Northanger Abbey", Role: core.MediaRoleAudiobook}},
	}
	part1 := core.Title{
		Key:          part1Key,
		Type:         core.TypeEpisode,
		LibraryID:    36,
		Title:        "Chapter 02",
		ItemIndex:    2,
		Runtime:      900 * time.Second,
		MediaClass:   core.MediaClassAudiobook,
		MediaRole:    core.MediaRolePart,
		MediaParents: []core.TitleParent{{Key: libriKey, Title: "Northanger Abbey", Role: core.MediaRoleAudiobook}},
	}

	canPlay := true
	details := map[core.RatingKey]frontend.ItemDetail{
		atomicKey: {
			Title:   atomic,
			CanPlay: &canPlay,
			Chapters: []core.Chapter{
				{ID: 0, Title: "Opening Credits", Start: 0, End: 16 * time.Second},
				{ID: 1, Title: "Epigraph", Start: 16 * time.Second, End: 44 * time.Second},
			},
		},
		libriKey: {
			Title:   libri,
			CanPlay: &canPlay,
			Chapters: []core.Chapter{
				{ID: 0, Title: "Chapter 01", Start: 0, End: 600 * time.Second},
				{ID: 1, Title: "Chapter 02", Start: 600 * time.Second, End: 1500 * time.Second},
			},
		},
		part0Key: {Title: part0, CanPlay: &canPlay},
		part1Key: {Title: part1, CanPlay: &canPlay},
	}

	exp := frontendtest.Experience{
		ViewsFunc: func(context.Context, *core.Viewer) ([]frontend.View, error) {
			return []frontend.View{
				{ID: "36", LibraryID: 36, Name: "Audiobooks", Type: core.TypeMovie, PluginID: "community.books", KindKey: "audiobook", Media: core.MediaClassAudiobook},
			}, nil
		},
		ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
			if d, ok := details[req.Key]; ok {
				return d, nil
			}
			return frontend.ItemDetail{}, core.ErrNotFound
		},
		ChildrenFunc: func(_ context.Context, _ *core.Viewer, _ core.Device, parent core.RatingKey, q frontend.ItemQuery) (core.Page, error) {
			if parent == libriKey {
				return core.Page{Items: []core.Title{part0, part1}, Total: 2}, nil
			}
			return core.Page{}, nil
		},
	}

	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{
		ctx:     ctx,
		db:      db,
		seal:    seal,
		replays: make(map[string]*replay),
		mux:     http.NewServeMux(),
		host: frontend.Host{
			Identity:       identityFake{},
			Experience:     exp,
			ServerIdentity: frontend.ServerIdentity{MachineID: "srv-1", FriendlyName: "Ether", Version: "0.3.17"},
		},
	}
	inst.routes()

	doGet := func(path string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("X-Vela-Device-Id", "web")
		rec := httptest.NewRecorder()
		inst.serve(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	// 1. Single-file audiobook detail surfaces probed chapters instead of chapters: [].
	code, atomicDetail := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(atomicKey))
	if code != 200 {
		t.Fatalf("atomic detail status=%d body=%v", code, atomicDetail)
	}
	chs, _ := atomicDetail["chapters"].([]any)
	if len(chs) != 2 {
		t.Fatalf("atomic detail chapters = %v, want 2 chapters", atomicDetail["chapters"])
	}
	ch0 := chs[0].(map[string]any)
	ch1 := chs[1].(map[string]any)
	if ch0["name"] != "Opening Credits" || ch0["startSeconds"] != float64(0) || ch1["name"] != "Epigraph" || ch1["startSeconds"] != float64(16) {
		t.Fatalf("unexpected atomic chapters: %v", chs)
	}

	// 2. Multi-part audiobook detail exposes childCount=2, chapters, and /children returns playable role="part" items.
	code, libriDetail := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(libriKey))
	if code != 200 {
		t.Fatalf("libri detail status=%d body=%v", code, libriDetail)
	}
	lMedia := libriDetail["title"].(map[string]any)["media"].(map[string]any)
	if lMedia["childCount"] != float64(2) {
		t.Fatalf("libri media.childCount = %v, want 2", lMedia["childCount"])
	}
	if lChs, _ := libriDetail["chapters"].([]any); len(lChs) != 2 {
		t.Fatalf("libri detail chapters = %v, want 2", libriDetail["chapters"])
	}
	code, libriChildren := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(libriKey) + "/children")
	if code != 200 {
		t.Fatalf("libri children status=%d body=%v", code, libriChildren)
	}
	items := libriChildren["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("libri children count = %d, want 2", len(items))
	}
	firstPart := items[0].(map[string]any)
	fpMedia := firstPart["media"].(map[string]any)
	if firstPart["id"] != ids.RatingKeyGUID(part0Key) || fpMedia["role"] != "part" || fpMedia["index"] != float64(1) {
		t.Fatalf("unexpected first part child: %v", firstPart)
	}
}

type rowViewsRejectingExperience struct {
	frontendtest.Experience
	t *testing.T
}

func (r rowViewsRejectingExperience) ListRowViews(context.Context, frontend.RowViewRequest) ([]frontend.RowView, error) {
	r.t.Helper()
	r.t.Fatalf("ListRowViews must not be called for plugin PlacementDetail requests")
	return nil, nil
}

func (r rowViewsRejectingExperience) ResolveRowView(context.Context, frontend.RowViewPageRequest) (frontend.RowViewPage, error) {
	return frontend.RowViewPage{}, core.ErrNotFound
}

func TestVelaMusicArtistDetailTopTracksRow(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, castMembers{}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer, err := grants.Issue(ctx, 1, nil, "vela", core.Device{ID: "web"})
	if err != nil {
		t.Fatal(err)
	}

	artistKey := core.PluginKey(7100)
	track1Key := core.PluginKey(7101)
	track2Key := core.PluginKey(7102)
	track1 := core.Title{
		Key:        track1Key,
		Type:       core.TypeEpisode,
		LibraryID:  38,
		Title:      "One More Time",
		Runtime:    320 * time.Second,
		MediaClass: core.MediaClassMusic,
		MediaRole:  core.MediaRoleTrack,
	}
	track2 := core.Title{
		Key:        track2Key,
		Type:       core.TypeEpisode,
		LibraryID:  38,
		Title:      "Digital Love",
		Runtime:    301 * time.Second,
		MediaClass: core.MediaClassMusic,
		MediaRole:  core.MediaRoleTrack,
	}

	exp := rowViewsRejectingExperience{
		t: t,
		Experience: frontendtest.Experience{
			RowsFunc: func(_ context.Context, req frontend.RowRequest) ([]frontend.Row, error) {
				if req.Placement != frontend.PlacementDetail || req.Item != artistKey {
					t.Fatalf("unexpected RowRequest: %+v", req)
				}
				return []frontend.Row{{
					ID:      "plugin:38:detail:top",
					Title:   "Top Tracks",
					Purpose: frontend.RowRecommendation,
					Kind:    "detail",
					Style:   "shelf",
					Items:   []core.Title{track1, track2},
				}}, nil
			},
		},
	}

	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{
		ctx:     ctx,
		db:      db,
		seal:    seal,
		replays: make(map[string]*replay),
		mux:     http.NewServeMux(),
		host: frontend.Host{
			Identity:       identityFake{},
			Experience:     exp,
			ServerIdentity: frontend.ServerIdentity{MachineID: "srv-1", FriendlyName: "Ether", Version: "0.3.17"},
		},
	}
	inst.routes()

	req := httptest.NewRequest(http.MethodGet, "/vela/v0/rows?placement=detail&titleId="+url.QueryEscape(ids.RatingKeyGUID(artistKey)), nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-Vela-Device-Id", "web")
	rec := httptest.NewRecorder()
	inst.serve(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /vela/v0/rows?placement=detail status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rows, _ := out["items"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want 1 Top Tracks row", out)
	}
	row := rows[0].(map[string]any)
	if row["id"] != "plugin:38:detail:top" || row["name"] != "Top Tracks" || row["purpose"] != "recommendation" || row["continuation"] != "exhausted" {
		t.Fatalf("unexpected row wire: %v", row)
	}
	entries := row["items"].([]any)
	if len(entries) != 2 {
		t.Fatalf("row items len = %d, want 2", len(entries))
	}
	firstTitle := entries[0].(map[string]any)["title"].(map[string]any)
	if firstTitle["name"] != "One More Time" || firstTitle["media"].(map[string]any)["role"] != "track" {
		t.Fatalf("unexpected first track item: %v", firstTitle)
	}
}

type peopleReaderExperience struct {
	frontendtest.Experience
	person frontend.PersonDetail
}

func (p peopleReaderExperience) People(context.Context, *frontend.Viewer, frontend.Device, frontend.PeopleQuery) ([]frontend.PersonDetail, error) {
	return []frontend.PersonDetail{p.person}, nil
}

func (p peopleReaderExperience) Person(context.Context, *frontend.Viewer, frontend.Device, string, frontend.PersonReadOptions) (frontend.PersonDetail, error) {
	return p.person, nil
}

func (p peopleReaderExperience) PersonCredit(context.Context, *frontend.Viewer, frontend.Device, frontend.RatingKey, int64) (frontend.PersonDetail, error) {
	return p.person, nil
}

func TestVelaPersonTitlesFansOutMultiplePluginViewsConcurrently(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, castMembers{}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer, err := grants.Issue(ctx, 1, nil, "vela", core.Device{ID: "web"})
	if err != nil {
		t.Fatal(err)
	}

	personTMDB := core.PluginPersonID("James Clear")
	personID := "52737b19test"
	bookKey := core.PluginKey(8101)
	audioKey := core.PluginKey(8102)

	// Rendezvous barrier proves both plugin views run concurrently rather than sequentially.
	entered := make(chan string, 2)
	release := make(chan struct{})

	exp := peopleReaderExperience{
		person: frontend.PersonDetail{
			ID:   personID,
			TMDB: personTMDB,
			Name: "James Clear",
		},
		Experience: frontendtest.Experience{
			ViewsFunc: func(context.Context, *core.Viewer) ([]frontend.View, error) {
				return []frontend.View{
					{ID: "35", LibraryID: 35, Name: "Books", Type: core.TypeMovie, PluginID: "community.books", KindKey: "book", Media: core.MediaClassBook},
					{ID: "36", LibraryID: 36, Name: "Audiobooks", Type: core.TypeMovie, PluginID: "community.books", KindKey: "audiobook", Media: core.MediaClassAudiobook},
				}, nil
			},
			LibraryItemsFunc: func(_ context.Context, _ *core.Viewer, _ core.Device, viewID string, _ frontend.ItemQuery) (core.Page, error) {
				entered <- viewID
				<-release
				switch viewID {
				case "35":
					return core.Page{
						Items:     []core.Title{{Key: bookKey, LibraryID: 35, Type: core.TypeMovie, Title: "Atomic Habits", Year: 2018, Released: time.Date(2018, 10, 16, 0, 0, 0, 0, time.UTC), MediaClass: core.MediaClassBook, MediaRole: core.MediaRoleBook}},
						Expanding: true,
					}, nil
				case "36":
					return core.Page{
						Items:     []core.Title{{Key: audioKey, LibraryID: 36, Type: core.TypeMovie, Title: "Atomic Habits (Audio)", Year: 2018, Released: time.Date(2018, 10, 17, 0, 0, 0, 0, time.UTC), MediaClass: core.MediaClassAudiobook, MediaRole: core.MediaRoleAudiobook}},
						Expanding: false,
					}, nil
				default:
					return core.Page{}, nil
				}
			},
		},
	}

	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{
		ctx:     ctx,
		db:      db,
		seal:    seal,
		replays: make(map[string]*replay),
		mux:     http.NewServeMux(),
		host: frontend.Host{
			Identity:       identityFake{},
			Experience:     exp,
			ServerIdentity: frontend.ServerIdentity{MachineID: "srv-1", FriendlyName: "Ether", Version: "0.3.17"},
		},
	}
	inst.routes()

	go func() {
		// Both views MUST arrive concurrently before we release either.
		<-entered
		<-entered
		close(release)
	}()

	req := httptest.NewRequest(http.MethodGet, "/vela/v0/people/"+personID+"/titles?limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-Vela-Device-Id", "web")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		inst.serve(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("person titles did not fan out plugin views concurrently (deadlocked waiting for 2nd view)")
	}
	if rec.Code != 200 {
		t.Fatalf("GET /vela/v0/people/{id}/titles status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["expanding"] != true {
		t.Fatalf("expected expanding=true when any view is expanding, got %v", out)
	}
	if out["continuation"] != "more" {
		t.Fatalf("expected continuation=more for limit=1 over 2 titles, got %v", out)
	}
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"].(map[string]any)["name"] != "Atomic Habits (Audio)" {
		t.Fatalf("unexpected first person title: %v", items)
	}
}
