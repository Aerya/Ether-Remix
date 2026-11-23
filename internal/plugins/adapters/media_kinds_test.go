package adapters_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestKindCatalogParentsSyntheticSeriesAndPartialContainerHydration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var hydrateCalls atomic.Int32
	rt := &listChannelFakeRuntime{routes: map[string]func(plugins.Request) (plugins.Response, error){
		"/ether/v1/kinds/music/browse": func(req plugins.Request) (plugins.Response, error) {
			var body struct {
				ParentID string `json:"parentId"`
			}
			_ = json.Unmarshal(req.Body, &body)
			if body.ParentID == "al1" {
				hydrateCalls.Add(1)
				b, _ := json.Marshal(core.KindPage{
					Items: []core.KindItem{
						{ID: "tr1", Level: "track", ParentID: "al1", Title: "So What", Playable: true, Index: 1},
						{ID: "tr2", Level: "track", ParentID: "al1", Title: "Freddie Freeloader", Playable: true, Index: 2},
						{ID: "tr3", Level: "track", ParentID: "al1", Title: "Blue in Green", Playable: true, Index: 3},
					},
				})
				return plugins.Response{Status: 200, Body: b}, nil
			}
			return plugins.Response{Status: 404}, nil
		},
	}}

	dataDir := t.TempDir()
	cfg, err := config.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	libs, err := libraries.New(ctx, db, libraries.Options{Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := plugins.New(plugins.Deps{
		DB:       db,
		Sealer:   cfg,
		Registry: extend.New(),
		Tools:    tools.NewCatalogue(),
		Logger:   log,
		IsLeader: func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("hosted", func(_ plugins.Installed) (plugins.Runtime, error) { return rt, nil })

	items, err := pluginitems.New(ctx, db, pluginitems.Options{
		Caller:   mgr,
		Health:   mgr,
		Logger:   log,
		IsLeader: func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(items.Close)

	blobDir := filepath.Join(dataDir, "blobcache")
	blobs, err := blobcache.Open(blobDir, blobcache.Options{Logger: log, SweepPeriod: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })

	kc := adapters.AttachKindCatalog(ctx, adapters.KindCatalogDeps{
		Manager:   mgr,
		Libraries: libs,
		Items:     items,
		Blobs:     blobs,
		BlobDir:   blobDir,
		Logger:    log,
	})

	musicPlugin := "community.music-hydrate"
	musicMan := map[string]any{
		"id":      musicPlugin,
		"name":    "Music",
		"version": "1.0.0",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Music test plugin",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"contributes": map[string]any{
				"mediaKinds": []any{
					map[string]any{
						"key":   "music",
						"label": "Music",
						"media": "music",
						"library": map[string]any{
							"name": "Music",
							"card": "square",
						},
						"levels": []any{
							map[string]any{"key": "artist", "label": "Artist", "role": "artist", "container": true},
							map[string]any{"key": "album", "label": "Album", "role": "album", "container": true},
							map[string]any{"key": "track", "label": "Track", "role": "track", "playable": true},
						},
						"fields": []any{
							map[string]any{"key": "genre", "type": "strings", "label": "Genre", "indexed": true},
						},
						"browse": map[string]any{
							"sorts":  []string{"title"},
							"facets": []string{"genre"},
							"expand": map[string]any{"facets": []string{"genre"}, "ttlHours": 24},
						},
						"sync": map[string]any{"mode": "push"},
					},
				},
			},
		},
	}

	bookPlugin := "community.books-series"
	bookMan := map[string]any{
		"id":      bookPlugin,
		"name":    "Books",
		"version": "1.0.0",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Books test plugin",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"contributes": map[string]any{
				"mediaKinds": []any{
					map[string]any{
						"key":   "book",
						"label": "Books",
						"media": "book",
						"library": map[string]any{
							"name": "Books",
							"tabs": []any{
								map[string]any{"key": "library", "label": "Books", "kind": "library"},
								map[string]any{"key": "series", "label": "Series", "kind": "collections", "facet": "series"},
							},
						},
						"levels": []any{
							map[string]any{"key": "book", "label": "Book", "role": "book", "playable": true},
						},
						"fields": []any{
							map[string]any{"key": "author", "type": "people", "label": "Author", "indexed": true},
							map[string]any{"key": "series", "type": "string", "label": "Series", "indexed": true},
							map[string]any{"key": "series_number", "type": "string", "label": "Number"},
							map[string]any{"key": "format", "type": "string", "label": "Format"},
							map[string]any{"key": "pages", "type": "int", "label": "Pages"},
							map[string]any{"key": "publisher", "type": "string", "label": "Publisher", "fact": true},
						},
						"browse": map[string]any{"sorts": []string{"title", "index"}, "facets": []string{"series", "author"}},
						"sync":   map[string]any{"mode": "push"},
					},
				},
			},
		},
	}

	installStudioManifest(t, mgr, musicMan, nil)
	installStudioManifest(t, mgr, bookMan, nil)
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var musicLib, bookLib core.Library
	for _, lib := range mustListLibs(t, libs) {
		switch lib.PluginID {
		case musicPlugin:
			musicLib = lib
		case bookPlugin:
			bookLib = lib
		}
	}
	if musicLib.ID == 0 || bookLib.ID == 0 {
		t.Fatalf("missing libraries: music=%+v book=%+v", musicLib, bookLib)
	}

	// Seed an album declaring ChildCount=3 with only 1 track pre-stored (partial container).
	if _, err := items.KindUpsert(ctx, musicPlugin, "music", []core.KindItem{
		{ID: "ar1", Level: "artist", Title: "Miles Davis", ChildCount: 1},
		{ID: "al1", Level: "album", ParentID: "ar1", Title: "Kind of Blue", ChildCount: 3},
		{ID: "tr1", Level: "track", ParentID: "al1", Title: "So What", Playable: true, Index: 1},
	}); err != nil {
		t.Fatalf("KindUpsert music: %v", err)
	}

	alRec, _, err := items.GetByExternal(ctx, musicPlugin, "music", "al1")
	if err != nil {
		t.Fatalf("GetByExternal al1: %v", err)
	}

	// Children MUST hydrate the remaining 2 tracks because stored count (1) < ChildCount (3).
	chPage, err := kc.Children(ctx, alRec.Key, false, "", 50)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	// Wait briefly if hydration ran asynchronously, or verify immediate result.
	deadline := time.Now().Add(2 * time.Second)
	for len(chPage.Items) < 3 && time.Now().Before(deadline) {
		time.Sleep(15 * time.Millisecond)
		chPage, err = kc.Children(ctx, alRec.Key, false, "", 50)
		if err != nil {
			t.Fatalf("Children poll: %v", err)
		}
	}
	if hydrateCalls.Load() != 1 {
		t.Fatalf("hydrateCalls = %d, want 1", hydrateCalls.Load())
	}
	if len(chPage.Items) != 3 {
		t.Fatalf("hydrated children len = %d, want 3", len(chPage.Items))
	}
	// Verify nearest-first MediaParents on hydrated tracks: [album, artist].
	tr := chPage.Items[0]
	if tr.MediaClass != core.MediaClassMusic || tr.MediaRole != core.MediaRoleTrack {
		t.Fatalf("track media class/role = %q/%q", tr.MediaClass, tr.MediaRole)
	}
	if len(tr.MediaParents) != 2 || tr.MediaParents[0].Role != core.MediaRoleAlbum || tr.MediaParents[0].Title != "Kind of Blue" ||
		tr.MediaParents[1].Role != core.MediaRoleArtist || tr.MediaParents[1].Title != "Miles Davis" {
		t.Fatalf("track MediaParents = %+v, want [album, artist]", tr.MediaParents)
	}

	// Subsequent Children call while fresh must NOT re-hydrate.
	if _, err := kc.Children(ctx, alRec.Key, false, "", 50); err != nil {
		t.Fatalf("Children second call: %v", err)
	}
	if hydrateCalls.Load() != 1 {
		t.Fatalf("hydrateCalls after fresh read = %d, want 1", hydrateCalls.Load())
	}

	// 1-level book with series facet + fractional series_number synthesizes facet-backed series parent.
	if _, err := items.KindUpsert(ctx, bookPlugin, "book", []core.KindItem{
		{
			ID:       "b1",
			Level:    "book",
			Title:    "The Churn",
			Summary:  "A novella set in Baltimore.",
			Playable: true,
			Fields: map[string]json.RawMessage{
				"author":        json.RawMessage(`["James S. A. Corey"]`),
				"series":        json.RawMessage(`"The Expanse"`),
				"series_number": json.RawMessage(`"3.5"`),
				"format":        json.RawMessage(`"epub"`),
				"pages":         json.RawMessage(`96`),
				"publisher":     json.RawMessage(`"Orbit"`),
			},
		},
	}); err != nil {
		t.Fatalf("KindUpsert book: %v", err)
	}
	bPage, err := kc.Browse(ctx, bookLib, frontend.ItemQuery{Sort: core.SortTitle}, "", 20)
	if err != nil || len(bPage.Items) != 1 {
		t.Fatalf("Browse books = %+v, err=%v", bPage, err)
	}
	bk := bPage.Items[0]
	if bk.MediaClass != core.MediaClassBook || bk.MediaRole != core.MediaRoleBook {
		t.Fatalf("book media class/role = %q/%q", bk.MediaClass, bk.MediaRole)
	}
	if bk.ItemIndex != 3 || bk.IndexDisplay != "3.5" {
		t.Fatalf("book index/indexDisplay = %d/%q, want 3/3.5", bk.ItemIndex, bk.IndexDisplay)
	}
	wantSeriesFacetID := "facet:" + strconv.FormatInt(int64(bookLib.ID), 10) + ":series:The Expanse"
	if len(bk.MediaParents) != 1 || bk.MediaParents[0].FacetID != wantSeriesFacetID || bk.MediaParents[0].Role != core.MediaRoleSeries || bk.MediaParents[0].Index != 3 || bk.MediaParents[0].IndexDisplay != "3.5" {
		t.Fatalf("book MediaParents = %+v, want FacetID %q", bk.MediaParents, wantSeriesFacetID)
	}
	if bk.MediaReading == nil || bk.MediaReading.Format != "epub" || bk.MediaReading.Pages != 96 {
		t.Fatalf("book MediaReading = %+v", bk.MediaReading)
	}
	// Publisher is marked fact:true and kept; series/series_number/format/pages/author are NOT duplicated.
	if len(bk.MediaFacts) != 1 || bk.MediaFacts[0].Label != "Publisher" || bk.MediaFacts[0].Value != "Orbit" {
		t.Fatalf("book MediaFacts = %+v, want [Publisher: Orbit]", bk.MediaFacts)
	}
}

func TestMusicFactsArtistReleaseOrderingAndExactSearchRanking(t *testing.T) {
	h := newKindTestHarness(t, 0, nil)
	ctx := context.Background()

	const musicPlugin = "community.music-ranking"
	musicMan := map[string]any{
		"id":      musicPlugin,
		"name":    "Music",
		"version": "1.0.0",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Music test plugin",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"contributes": map[string]any{
				"mediaKinds": []any{
					map[string]any{
						"key":     "music",
						"label":   "Music",
						"media":   "music",
						"library": map[string]any{"name": "Music", "card": "square"},
						"levels": []any{
							map[string]any{"key": "artist", "label": "Artist", "role": "artist", "container": true},
							map[string]any{"key": "album", "label": "Album", "role": "album", "container": true},
							map[string]any{"key": "track", "label": "Track", "role": "track", "playable": true},
						},
						"fields": []any{
							map[string]any{"key": "genres", "type": "strings", "label": "Genres", "indexed": true},
							map[string]any{"key": "decade", "type": "enum", "label": "Era", "indexed": true, "enum": []string{"1970s", "1980s", "2010s", "2020s"}},
							map[string]any{"key": "curation", "type": "strings", "label": "Featured", "indexed": true},
							map[string]any{"key": "releaseType", "type": "enum", "label": "Release type", "indexed": true, "enum": []string{"album", "ep", "single", "compilation", "remix"}},
							map[string]any{"key": "popularity", "type": "int", "label": "Popularity"},
							map[string]any{"key": "artist", "type": "string", "label": "Artist"},
							map[string]any{"key": "album", "type": "string", "label": "Album"},
							map[string]any{"key": "label", "type": "string", "label": "Label", "fact": true},
							map[string]any{"key": "tracks", "type": "int", "label": "Tracks"},
							map[string]any{"key": "discs", "type": "int", "label": "Discs"},
							map[string]any{"key": "upc", "type": "string", "label": "UPC", "fact": true},
							map[string]any{"key": "parentEcho", "type": "string", "label": "Echo", "fact": true},
						},
						"browse": map[string]any{
							"sorts":  []string{"title", "rating", "released"},
							"facets": []string{"genres", "releaseType", "decade", "curation"},
							"search": true,
						},
						"sync": map[string]any{"mode": "push"},
					},
				},
			},
		},
	}

	installStudioManifest(t, h.mgr, musicMan, nil)
	var musicLib core.Library
	for _, lib := range mustListLibs(t, h.libs) {
		if lib.PluginID == musicPlugin {
			musicLib = lib
		}
	}
	if musicLib.ID == 0 {
		t.Fatal("expected music library")
	}

	// Seed AC/DC with mixed release types inserted remix/B-side first, plus tribute & cover matches for "Back In Black".
	if _, err := h.items.KindUpsert(ctx, musicPlugin, "music", []core.KindItem{
		{
			ID: "ar-acdc", Level: "artist", Title: "AC/DC", ChildCount: 5,
			Fields: map[string]json.RawMessage{
				"genres":     json.RawMessage(`["Rock"]`),
				"decade":     json.RawMessage(`"1980s"`),
				"curation":   json.RawMessage(`["Classic"]`),
				"popularity": json.RawMessage(`5000000`),
				"artist":     json.RawMessage(`"AC/DC"`),
			},
		},
		{
			ID: "al-remix", Level: "album", ParentID: "ar-acdc", Title: "Back In Black (Club Remixes)", Year: 2024, Released: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC).Unix(),
			Fields: map[string]json.RawMessage{"releaseType": json.RawMessage(`"remix"`)},
		},
		{
			ID: "al-comp", Level: "album", ParentID: "ar-acdc", Title: "Greatest Hits", Year: 2023, Released: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
			Fields: map[string]json.RawMessage{"releaseType": json.RawMessage(`"compilation"`)},
		},
		{
			ID: "al-single", Level: "album", ParentID: "ar-acdc", Title: "Thunderstruck (Single)", Year: 1990, Released: time.Date(1990, 9, 10, 0, 0, 0, 0, time.UTC).Unix(),
			Fields: map[string]json.RawMessage{"releaseType": json.RawMessage(`"single"`)},
		},
		{
			ID: "al-ep", Level: "album", ParentID: "ar-acdc", Title: "74 Jailbreak", Year: 1984, Released: time.Date(1984, 10, 15, 0, 0, 0, 0, time.UTC).Unix(),
			Fields: map[string]json.RawMessage{"releaseType": json.RawMessage(`"ep"`)},
		},
		{
			ID: "al-hth", Level: "album", ParentID: "ar-acdc", Title: "Highway to Hell", Year: 1979, Released: time.Date(1979, 7, 27, 0, 0, 0, 0, time.UTC).Unix(),
			Fields: map[string]json.RawMessage{"releaseType": json.RawMessage(`"album"`), "label": json.RawMessage(`"Atlantic"`)},
		},
		{
			ID: "al-bib", Level: "album", ParentID: "ar-acdc", Title: "Back In Black", Year: 1980, Released: time.Date(1980, 7, 25, 0, 0, 0, 0, time.UTC).Unix(),
			Fields: map[string]json.RawMessage{
				"genres":      json.RawMessage(`["Rock","Metal"]`),
				"decade":      json.RawMessage(`"1980s"`),
				"curation":    json.RawMessage(`["Classic"]`),
				"releaseType": json.RawMessage(`"album"`),
				"popularity":  json.RawMessage(`331817`),
				"artist":      json.RawMessage(`"AC/DC"`),
				"album":       json.RawMessage(`"Back In Black"`),
				"label":       json.RawMessage(`"Columbia"`),
				"tracks":      json.RawMessage(`10`),
				"discs":       json.RawMessage(`1`),
				"upc":         json.RawMessage(`"886444889841"`),
				"parentEcho":  json.RawMessage(`"AC/DC"`),
			},
		},
		{
			ID: "tr-bib", Level: "track", ParentID: "al-bib", Title: "Back In Black", Playable: true, Index: 6, Year: 1980,
			Fields: map[string]json.RawMessage{
				"releaseType": json.RawMessage(`"album"`),
				"popularity":  json.RawMessage(`966294`),
			},
		},
		// Competing matches for "Back In Black": tribute artist + obscure cover singles + less popular album.
		{
			ID: "ar-tribute", Level: "artist", Title: "Back In Black", Year: 2025,
			Fields: map[string]json.RawMessage{"popularity": json.RawMessage(`3591`)},
		},
		{
			ID: "ar-cover1", Level: "artist", Title: "Tony & the Kiki",
		},
		{
			ID: "al-cover1", Level: "album", ParentID: "ar-cover1", Title: "Back In Black",
			Fields: map[string]json.RawMessage{"releaseType": json.RawMessage(`"single"`)},
		},
		{
			ID: "ar-whodini", Level: "artist", Title: "Whodini",
		},
		{
			ID: "al-whodini", Level: "album", ParentID: "ar-whodini", Title: "Back in Black", Year: 1986,
			Fields: map[string]json.RawMessage{"releaseType": json.RawMessage(`"album"`), "popularity": json.RawMessage(`621`)},
		},
	}); err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}

	// 1. Facts cleanliness (#7): ONLY explicit fact:true fields that are not internal IDs or repeated title/parent/credits.
	bibRec, _, err := h.items.GetByExternal(ctx, musicPlugin, "music", "al-bib")
	if err != nil {
		t.Fatal(err)
	}
	bibView, err := h.catalog.Item(ctx, bibRec.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(bibView.Title.MediaFacts) != 1 || bibView.Title.MediaFacts[0].Label != "Label" || bibView.Title.MediaFacts[0].Value != "Columbia" {
		t.Fatalf("album MediaFacts = %+v, want ONLY [Label: Columbia]", bibView.Title.MediaFacts)
	}
	if bibView.Title.Popularity != 331817 {
		t.Fatalf("album Popularity = %v, want 331817", bibView.Title.Popularity)
	}

	// 2. Artist children ordering (#8): studio albums (newest first) > EP > single > compilation > remix.
	acdcRec, _, err := h.items.GetByExternal(ctx, musicPlugin, "music", "ar-acdc")
	if err != nil {
		t.Fatal(err)
	}
	chPage, err := h.catalog.Children(ctx, acdcRec.Key, false, "", 50)
	if err != nil {
		t.Fatalf("Children(artist): %v", err)
	}
	gotTitles := make([]string, len(chPage.Items))
	for i, it := range chPage.Items {
		gotTitles[i] = it.Title
	}
	wantTitles := []string{
		"Back In Black",                // album 1980
		"Highway to Hell",              // album 1979
		"74 Jailbreak",                 // EP 1984
		"Thunderstruck (Single)",       // single 1990
		"Greatest Hits",                // compilation 2023
		"Back In Black (Club Remixes)", // remix 2024
	}
	if len(gotTitles) != len(wantTitles) {
		t.Fatalf("artist children = %v, want %v", gotTitles, wantTitles)
	}
	for i := range wantTitles {
		if gotTitles[i] != wantTitles[i] {
			t.Fatalf("artist children[%d] = %q, want %q (all=%v)", i, gotTitles[i], wantTitles[i], gotTitles)
		}
	}

	// 3. Search ranking (#9): "Back In Black" ranks AC/DC track & album at #1 and #2, ahead of cover singles and tribute artist.
	hits, err := h.catalog.Search(ctx, musicLib, "Back In Black", frontend.ItemQuery{Limit: 20}, 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) < 4 {
		t.Fatalf("expected >= 4 search hits, got %d", len(hits))
	}
	topTwo := map[core.MediaRole]bool{}
	for i := 0; i < 2; i++ {
		if hits[i].Popularity < 300000 {
			t.Fatalf("hit[%d] = %q (role=%q pop=%v score=%v), expected AC/DC Back In Black in top 2", i, hits[i].Title, hits[i].MediaRole, hits[i].Popularity, hits[i].SearchScore)
		}
		topTwo[hits[i].MediaRole] = true
	}
	if !topTwo[core.MediaRoleAlbum] || !topTwo[core.MediaRoleTrack] {
		t.Fatalf("expected AC/DC album and track in top 2, got roles %+v (%+v, %+v)", topTwo, hits[0].Title, hits[1].Title)
	}
	if hits[0].SearchScore <= 1.05 || hits[1].SearchScore <= 1.05 {
		t.Fatalf("expected AC/DC exact album/track scores > 1.05 to beat low-popularity TV episodes, got %.4f / %.4f", hits[0].SearchScore, hits[1].SearchScore)
	}
}

func TestMultiPartAudiobookSyntheticPartsAndChapters(t *testing.T) {
	ctx := context.Background()
	partBytes := map[string][]byte{
		"/part0.mp3": []byte("PART-ZERO-AUDIO"),
		"/part1.mp3": []byte("PART-ONE-AUDIO-BYTES"),
		"/part2.mp3": []byte("PART-TWO-AUDIO"),
	}
	mediaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := partBytes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		http.ServeContent(w, r, r.URL.Path, time.Unix(1_700_000_000, 0), bytes.NewReader(data))
	}))
	defer mediaSrv.Close()
	httpClient := &http.Client{
		Transport: hostRewriteTransport{target: mediaSrv.URL, base: mediaSrv.Client().Transport},
	}
	h := newKindTestHarness(t, 0, httpClient)

	var (
		mu        sync.Mutex
		seenParts []int
	)
	h.rt.callContext = func(_ context.Context, req plugins.Request) (plugins.Response, error) {
		switch req.Path {
		case "/ether/health":
			return plugins.Response{Status: 200, Body: json.RawMessage(`{"status":"ok"}`)}, nil
		case "/ether/v1/kinds/audiobook/resolve":
			var body core.KindResolveRequest
			if err := json.Unmarshal(req.Body, &body); err != nil {
				return plugins.Response{Status: 400}, nil
			}
			pIdx := -1
			if body.PartIndex != nil {
				pIdx = *body.PartIndex
			}
			mu.Lock()
			seenParts = append(seenParts, pIdx)
			mu.Unlock()
			data := partBytes["/part"+strconv.Itoa(pIdx)+".mp3"]
			respBody, _ := json.Marshal(map[string]any{
				"sources": []map[string]any{{
					"id":      "src-" + strconv.Itoa(pIdx),
					"pattern": "range",
					"mime":    "audio/mpeg",
					"size":    len(data),
					"url":     "https://librivox.example/part" + strconv.Itoa(pIdx) + ".mp3",
				}},
			})
			return plugins.Response{Status: 200, Body: respBody}, nil
		default:
			return plugins.Response{Status: 404}, nil
		}
	}

	const pluginID = "community.librivox"
	man := map[string]any{
		"id":      pluginID,
		"name":    "Audiobooks",
		"version": "1.0.0",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Audiobooks test plugin",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"permissions":  map[string]any{"network": []string{"librivox.example"}},
			"contributes": map[string]any{
				"mediaKinds": []any{
					map[string]any{
						"key":   "audiobook",
						"label": "Audiobooks",
						"media": "audiobook",
						"library": map[string]any{
							"name": "Audiobooks",
							"card": "square",
						},
						"levels": []any{
							map[string]any{"key": "audiobook", "label": "Audiobook", "role": "audiobook", "playable": true},
						},
						"fields": []any{
							map[string]any{"key": "author", "label": "Author", "type": "people", "role": "author"},
							map[string]any{"key": "narrator", "label": "Narrator", "type": "people", "role": "narrator"},
						},
						"browse": map[string]any{"sorts": []string{"title"}},
						"sync":   map[string]any{"mode": "pull", "intervalSec": 3600},
					},
				},
			},
		},
	}
	installStudioManifest(t, h.mgr, man, nil)
	rec, err := h.items.GetKind(ctx, pluginID, "audiobook")
	if err != nil {
		t.Fatalf("GetKind: %v", err)
	}
	lib, err := h.libs.Get(ctx, rec.LibraryID)
	if err != nil {
		t.Fatalf("libs.Get: %v", err)
	}

	_, err = h.items.UpsertItems(ctx, pluginID, "audiobook", []core.KindItem{
		{
			ID:       "northanger-abbey",
			Level:    "audiobook",
			Title:    "Northanger Abbey",
			Year:     1817,
			Playable: true,
			Fields: map[string]json.RawMessage{
				"author":   json.RawMessage(`["Jane Austen"]`),
				"narrator": json.RawMessage(`["Kara Shallenberg"]`),
			},
			Hints: json.RawMessage(`{"parts":[
				{"name":"01_northanger_abbey.mp3","mime":"audio/mpeg","durationSec":600,"size":15},
				{"name":"Chapter 02 - Bath","mime":"audio/mpeg","durationSec":900,"size":20},
				{"name":"Chapter 03 - The Abbey","mime":"audio/mpeg","durationSec":750,"size":14}
			]}`),
		},
	})
	if err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}
	bookItem, _, err := h.items.GetByExternal(ctx, pluginID, "audiobook", "northanger-abbey")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Parent audiobook Item carries ChildCount=3, summed Runtime, and 3 synthesized chapters.
	piv, err := h.catalog.Item(ctx, bookItem.Key)
	if err != nil {
		t.Fatalf("Item(book): %v", err)
	}
	if piv.Title.ChildCount != 3 || piv.Title.Runtime != 2250*time.Second {
		t.Fatalf("parent ChildCount/Runtime = %d/%v, want 3/2250s", piv.Title.ChildCount, piv.Title.Runtime)
	}
	if len(piv.Chapters) != 3 || piv.Chapters[0].Title != "01 northanger abbey" || piv.Chapters[0].Start != 0 || piv.Chapters[1].Title != "Chapter 02 - Bath" || piv.Chapters[1].Start != 600*time.Second || piv.Chapters[2].Start != 1500*time.Second || piv.Chapters[2].End != 2250*time.Second {
		t.Fatalf("unexpected synthesized chapters: %+v", piv.Chapters)
	}

	// 2. Children exposes all 3 playable part titles with pagination, role="part", 1-based index, and parent link.
	p1, err := h.catalog.Children(ctx, bookItem.Key, false, "", 2)
	if err != nil || len(p1.Items) != 2 || p1.Total != 3 || p1.NextCursor != "part:2" {
		t.Fatalf("Children page 1 = %+v, err=%v", p1, err)
	}
	p2, err := h.catalog.Children(ctx, bookItem.Key, false, p1.NextCursor, 2)
	if err != nil || len(p2.Items) != 1 || p2.NextCursor != "" {
		t.Fatalf("Children page 2 = %+v, err=%v", p2, err)
	}
	part1 := p1.Items[1]
	if !part1.Key.IsPluginPartKey() || part1.MediaRole != core.MediaRolePart || part1.MediaClass != core.MediaClassAudiobook || part1.ItemIndex != 2 || part1.Title != "Chapter 02 - Bath" || part1.Runtime != 900*time.Second {
		t.Fatalf("unexpected synthetic part 1: %+v", part1)
	}
	if len(part1.MediaParents) != 1 || part1.MediaParents[0].Key != bookItem.Key || part1.MediaParents[0].Role != core.MediaRoleAudiobook {
		t.Fatalf("unexpected part 1 parents: %+v", part1.MediaParents)
	}

	// 3. Item, ItemsMany, and Ancestors resolve synthetic part keys seamlessly.
	partView, err := h.catalog.Item(ctx, part1.Key)
	if err != nil || !partView.Playable || partView.Title.Key != part1.Key || partView.Library.ID != lib.ID {
		t.Fatalf("Item(part1) = %+v, err=%v", partView, err)
	}
	many, err := h.catalog.ItemsMany(ctx, []core.RatingKey{bookItem.Key, part1.Key})
	if err != nil || len(many) != 2 || many[part1.Key].Title.Title != "Chapter 02 - Bath" {
		t.Fatalf("ItemsMany = %+v, err=%v", many, err)
	}
	anc, _, err := h.catalog.Ancestors(ctx, part1.Key)
	if err != nil || len(anc) != 1 || anc[0].Key != bookItem.Key {
		t.Fatalf("Ancestors(part1) = %+v, err=%v", anc, err)
	}

	// 4. KindResolver.OpenKind(part1.Key) opens ONLY part index 1 without concatenating all parts.
	viewer := &core.Viewer{ID: 1, IsOwner: true}
	media, err := h.resolver.OpenKind(ctx, viewer, part1.Key, []string{"range"})
	if err != nil {
		t.Fatalf("OpenKind(part1): %v", err)
	}
	defer media.Close()
	buf := make([]byte, media.Size)
	if _, err := io.ReadFull(io.NewSectionReader(media, 0, media.Size), buf); err != nil {
		t.Fatalf("ReadFull(part1): %v", err)
	}
	if string(buf) != "PART-ONE-AUDIO-BYTES" {
		t.Fatalf("part1 bytes = %q, want PART-ONE-AUDIO-BYTES", string(buf))
	}
	if len(seenParts) != 1 || seenParts[0] != 1 {
		t.Fatalf("expected only partIndex=1 resolved, got %v", seenParts)
	}
}
