package adapters_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/store"
)

func musicManifestRaw(pluginID string, ttlHours int) map[string]any {
	expandCfg := map[string]any{"facets": []string{"genre"}}
	if ttlHours > 0 {
		expandCfg["ttlHours"] = ttlHours
	}
	return map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Music Plugin",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Multi-level music plugin",
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
							"tabs": []any{
								map[string]any{"key": "recommended", "label": "Recommended", "kind": "recommended"},
								map[string]any{"key": "artists", "label": "Artists", "kind": "library", "level": "artist"},
								map[string]any{"key": "albums", "label": "Albums", "kind": "library", "level": "album"},
								map[string]any{"key": "tracks", "label": "Tracks", "kind": "library", "level": "track"},
								map[string]any{"key": "album-genres", "label": "Album Genres", "kind": "collections", "level": "album", "facet": "genre"},
								map[string]any{"key": "track-composers", "label": "Composers", "kind": "people", "level": "track", "facet": "composer"},
							},
							"shelves": []any{
								map[string]any{
									"key":       "jump-back-in",
									"label":     "Jump back in",
									"level":     "album",
									"sort":      "lastViewed",
									"direction": "desc",
									"filters": []any{
										map[string]any{"field": "viewer", "value": map[string]any{"history": true}},
									},
								},
								map[string]any{
									"key":       "latest-albums",
									"label":     "Latest albums",
									"level":     "album",
									"sort":      "year",
									"direction": "desc",
								},
							},
						},
						"levels": []any{
							map[string]any{"key": "artist", "label": "Artist", "role": "artist", "container": true},
							map[string]any{"key": "album", "label": "Album", "role": "album", "container": true},
							map[string]any{"key": "track", "label": "Track", "role": "track", "playable": true},
						},
						"fields": []any{
							map[string]any{"key": "genre", "type": "strings", "label": "Genre", "indexed": true},
							map[string]any{"key": "composer", "type": "people", "label": "Composer", "indexed": true},
						},
						"present": map[string]any{
							"vela": map[string]any{"type": "shows"},
							"plex": map[string]any{"base": "artist"},
							"mediaBrowser": map[string]any{
								"collectionType": "music",
								"itemTypes": map[string]string{
									"artist": "MusicArtist",
									"album":  "MusicAlbum",
									"track":  "Audio",
								},
							},
						},
						"browse": map[string]any{
							"sorts":  []string{"title", "year", "released", "added", "lastViewed"},
							"facets": []string{"genre", "composer"},
							"search": true,
							"expand": expandCfg,
						},
						"sync": map[string]any{"mode": "push"},
					},
				},
			},
		},
	}
}

func TestMultiLevelMusicBrowseFacetsSkipResumeAndHistoryRollup(t *testing.T) {
	ctx := context.Background()
	h := newKindTestHarness(t, 2*time.Second, nil)
	h.state.SetSkipResume(h.catalog.SkipResume)

	const pluginID = "community.example.music"
	installStudioManifest(t, h.mgr, musicManifestRaw(pluginID, 168), nil)
	if err := h.mgr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var musicLib core.Library
	for _, lib := range mustListLibs(t, h.libs) {
		if lib.PluginID == pluginID {
			musicLib = lib
		}
	}
	if musicLib.ID == 0 {
		t.Fatal("expected music plugin library")
	}

	// Verify Presentation resolves MediaClassMusic and carries level + shelf TabFilter.
	pres, ok := h.catalog.Presentation(ctx, musicLib)
	if !ok {
		t.Fatal("Presentation returned ok=false")
	}
	if pres.Media != core.MediaClassMusic {
		t.Fatalf("pres.Media = %q, want music", pres.Media)
	}
	if len(pres.Tabs) != 6 || pres.Tabs[2].Level != "album" || pres.Tabs[3].Level != "track" || pres.Tabs[4].Level != "album" || pres.Tabs[5].Level != "track" {
		t.Fatalf("pres.Tabs levels = %+v", pres.Tabs)
	}
	if len(pres.Shelves) != 2 || pres.Shelves[0].Level != "album" || pres.Shelves[0].Sort != core.SortLastViewed || pres.Shelves[0].TabFilter == nil {
		t.Fatalf("pres.Shelves = %+v", pres.Shelves)
	}

	// Populate 3-level music items with distinct genres & composers per level.
	_, err := h.items.KindUpsert(ctx, pluginID, "music", []core.KindItem{
		{
			ID: "ar1", Level: "artist", Title: "Miles Davis", ChildCount: 2,
			Fields: map[string]json.RawMessage{"genre": json.RawMessage(`["ArtistGenreOnly"]`)},
		},
		{
			ID: "al1", Level: "album", ParentID: "ar1", Title: "Kind of Blue", Year: 1959, ChildCount: 2,
			Fields: map[string]json.RawMessage{"genre": json.RawMessage(`["Modal Jazz"]`)},
		},
		{
			ID: "al2", Level: "album", ParentID: "ar1", Title: "Bitches Brew", Year: 1970, ChildCount: 1,
			Fields: map[string]json.RawMessage{"genre": json.RawMessage(`["Fusion"]`)},
		},
		{
			ID: "al3", Level: "album", ParentID: "ar1", Title: "In a Silent Way", Year: 1969, ChildCount: 1,
			Fields: map[string]json.RawMessage{"genre": json.RawMessage(`["Ambient Jazz"]`)},
		},
		{
			ID: "tr1", Level: "track", ParentID: "al1", Title: "So What", Playable: true, Index: 1,
			Fields: map[string]json.RawMessage{
				"genre":    json.RawMessage(`["TrackGenreOnly"]`),
				"composer": json.RawMessage(`["Miles Davis"]`),
			},
		},
		{
			ID: "tr2", Level: "track", ParentID: "al2", Title: "Pharaoh's Dance", Playable: true, Index: 1,
			Fields: map[string]json.RawMessage{
				"genre":    json.RawMessage(`["TrackGenreOnly"]`),
				"composer": json.RawMessage(`["Joe Zawinul"]`),
			},
		},
	})
	if err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}

	// 1. Multi-level Browse: explicit q.Level, Plex Types, and Jellyfin UnservedTypes.
	albumsByLevel, err := h.catalog.Browse(ctx, musicLib, frontend.ItemQuery{Level: "album", Sort: core.SortTitle}, "", 20)
	if err != nil || len(albumsByLevel.Items) != 3 {
		t.Fatalf("Browse(level=album) = %+v, err=%v", albumsByLevel, err)
	}
	tracksByPlexType, err := h.catalog.Browse(ctx, musicLib, frontend.ItemQuery{Types: []core.MediaType{"track"}, Recursive: true}, "", 20)
	if err != nil || len(tracksByPlexType.Items) != 2 {
		t.Fatalf("Browse(Plex type=track) = %+v, err=%v", tracksByPlexType, err)
	}
	albumsByJBType, err := h.catalog.Browse(ctx, musicLib, frontend.ItemQuery{UnservedTypes: []string{"musicalbum"}, Recursive: true}, "", 20)
	if err != nil || len(albumsByJBType.Items) != 3 {
		t.Fatalf("Browse(MB MusicAlbum) = %+v, err=%v", albumsByJBType, err)
	}
	unknownLevel, err := h.catalog.Browse(ctx, musicLib, frontend.ItemQuery{Level: "nonexistent"}, "", 20)
	if err != nil || len(unknownLevel.Items) != 0 {
		t.Fatalf("Browse(unknown level) = %+v, err=%v, want empty", unknownLevel, err)
	}

	// 2. Level-scoped facets, buckets, collections, and people reflect the requested level (not root).
	albumBuckets, err := h.catalog.FacetBucketsAtLevel(ctx, musicLib, "album", "genre")
	if err != nil || len(albumBuckets) != 3 {
		t.Fatalf("FacetBucketsAtLevel(album, genre) = %+v, err=%v (want 3 album genres)", albumBuckets, err)
	}
	for _, b := range albumBuckets {
		if b.Label == "ArtistGenreOnly" || b.Label == "TrackGenreOnly" {
			t.Fatalf("album facet leaked non-album genre: %+v", b)
		}
	}
	albumCols, err := h.catalog.FacetCollectionsAtLevel(ctx, musicLib, "album", "genre", "name", "", 0, 20)
	if err != nil || len(albumCols.Items) != 3 {
		t.Fatalf("FacetCollectionsAtLevel(album, genre) = %+v, err=%v", albumCols, err)
	}
	trackPeople, err := h.catalog.FacetPeopleAtLevel(ctx, musicLib, "track", "composer", "name", "", 0, 20)
	if err != nil || len(trackPeople.Items) != 2 {
		t.Fatalf("FacetPeopleAtLevel(track, composer) = %+v, err=%v", trackPeople, err)
	}
	mbAlbumFacets, err := h.catalog.FacetsAtLevel(ctx, musicLib, "MusicAlbum", "genre")
	if err != nil || len(mbAlbumFacets) != 3 {
		t.Fatalf("FacetsAtLevel(MusicAlbum, genre) = %+v, err=%v", mbAlbumFacets, err)
	}

	// 3. SkipResume + Experience end-to-end:
	//    Play tr2 (al2) at 50% then tr1 (al1) at 50%:
	//    - ViewOffset stays 0 on both tracks (music skips resume)
	//    - LastViewedAt is recorded
	//    - No Continue Watching shelf on music library
	//    - "Jump back in" album history shelf rolls tracks up to [al1, al2] and excludes unviewed al3!
	tr1, _, err := h.items.GetByExternal(ctx, pluginID, "music", "tr1")
	if err != nil {
		t.Fatal(err)
	}
	tr2, _, err := h.items.GetByExternal(ctx, pluginID, "music", "tr2")
	if err != nil {
		t.Fatal(err)
	}
	if !h.catalog.SkipResume(ctx, tr1.Key) {
		t.Fatal("SkipResume must return true for music track")
	}

	// Play tr2 first, then tr1 slightly later.
	if err := h.state.Progress(ctx, 1, tr2.Key, 100*time.Second, 200*time.Second, "playing"); err != nil {
		t.Fatalf("Progress(tr2): %v", err)
	}
	// Ensure distinct nanosecond timestamps even on fast clocks.
	st2, _ := h.state.Get(ctx, 1, tr2.Key)
	if st2.ViewOffset != 0 || st2.LastViewedAt.IsZero() {
		t.Fatalf("tr2 state = %+v, want ViewOffset=0 and LastViewedAt>0", st2)
	}
	// Force tr1 timestamp strictly newer via direct second progress after advancing row timestamp of tr2 slightly back.
	if _, err := h.db.ExecContext(ctx, `UPDATE state_watch SET last_viewed_at = last_viewed_at - 1000000000 WHERE user_id = 1 AND rating_key = ?`, int64(tr2.Key)); err != nil {
		t.Fatal(err)
	}
	if err := h.state.Progress(ctx, 1, tr1.Key, 100*time.Second, 200*time.Second, "playing"); err != nil {
		t.Fatalf("Progress(tr1): %v", err)
	}

	exp := h.experienceFor(t, "jellyfin")
	owner := &core.Viewer{ID: 1, IsOwner: true}
	libIDStr := strconv.FormatInt(int64(musicLib.ID), 10)
	rows, err := exp.PluginLibraryRows(ctx, owner, core.Device{Product: "Vela"}, libIDStr)
	if err != nil {
		t.Fatalf("PluginLibraryRows: %v", err)
	}
	for _, r := range rows {
		if r.Purpose == frontend.RowContinue {
			t.Fatalf("music library must not have a Continue row: %+v", r)
		}
	}
	var jumpRow *frontend.Row
	for idx := range rows {
		if rows[idx].ID == "plugin:"+libIDStr+":shelf:jump-back-in" {
			jumpRow = &rows[idx]
			break
		}
	}
	if jumpRow == nil {
		t.Fatalf("expected jump-back-in shelf in %+v", rows)
	}
	if len(jumpRow.Items) != 2 {
		t.Fatalf("jump-back-in items = %d (want 2 rolled-up albums, no unviewed al3): %+v", len(jumpRow.Items), jumpRow.Items)
	}
	if jumpRow.Items[0].Title != "Kind of Blue" || jumpRow.Items[1].Title != "Bitches Brew" {
		t.Fatalf("jump-back-in order = [%q, %q], want [Kind of Blue, Bitches Brew]", jumpRow.Items[0].Title, jumpRow.Items[1].Title)
	}
}

func TestContainerHydrationOnOpenDedupeTTLFallbackAndNeverOnListRender(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Build a harness with an injectable clock so TTL expiry is tested deterministically.
	nowVal := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	clockNow := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return nowVal
	}
	advanceClock := func(d time.Duration) {
		nowMu.Lock()
		nowVal = nowVal.Add(d)
		nowMu.Unlock()
	}

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
	rt := &listChannelFakeRuntime{routes: map[string]func(plugins.Request) (plugins.Response, error){}}
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
		Now:      clockNow,
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

	const pluginID = "community.example.hydrate"
	installStudioManifest(t, mgr, musicManifestRaw(pluginID, 24), nil)
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var musicLib core.Library
	for _, lib := range mustListLibs(t, libs) {
		if lib.PluginID == pluginID {
			musicLib = lib
		}
	}

	// Seed containers:
	// - al-expand (childCount=2, no stored children): hydrated via /expand (with multi-page continuation)
	// - al-browse (childCount=1, no stored children): /expand returns 404 -> falls back to /browse
	// - al-empty  (childCount=0, no stored children): must NEVER trigger hydration
	_, err = items.KindUpsert(ctx, pluginID, "music", []core.KindItem{
		{ID: "ar1", Level: "artist", Title: "Artist One", ChildCount: 3},
		{ID: "al-expand", Level: "album", ParentID: "ar1", Title: "Expand Album", ChildCount: 2},
		{ID: "al-browse", Level: "album", ParentID: "ar1", Title: "Browse Fallback Album", ChildCount: 1},
		{ID: "al-empty", Level: "album", ParentID: "ar1", Title: "Empty Album", ChildCount: 0},
	})
	if err != nil {
		t.Fatal(err)
	}

	var expandCalls atomic.Int32
	var browseCalls atomic.Int32
	expandStarted := make(chan struct{}, 4)
	releaseExpand := make(chan struct{})
	page2Done := make(chan struct{}, 2)

	rt.routes["/ether/v1/kinds/music/expand"] = func(req plugins.Request) (plugins.Response, error) {
		var in core.KindExpandRequest
		_ = json.Unmarshal(req.Body, &in)
		if in.ParentID == "al-browse" {
			return plugins.Response{Status: http.StatusNotFound}, nil
		}
		if in.ParentID == "al-expand" {
			if in.Cursor == "" {
				n := expandCalls.Add(1)
				if n == 1 {
					expandStarted <- struct{}{}
					<-releaseExpand
				}
				body, _ := json.Marshal(core.KindExpandResponse{
					Items:      []core.KindItem{{ID: "t1", Level: "track", ParentID: "al-expand", Title: "Track 1", Playable: true, Index: 1}},
					NextCursor: "p2",
				})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil
			}
			// Second page continues in background.
			body, _ := json.Marshal(core.KindExpandResponse{
				Items: []core.KindItem{{ID: "t2", Level: "track", ParentID: "al-expand", Title: "Track 2", Playable: true, Index: 2}},
			})
			select {
			case page2Done <- struct{}{}:
			default:
			}
			return plugins.Response{Status: http.StatusOK, Body: body}, nil
		}
		return plugins.Response{Status: http.StatusNotFound}, nil
	}

	rt.routes["/ether/v1/kinds/music/browse"] = func(req plugins.Request) (plugins.Response, error) {
		var in core.KindBrowseRequest
		_ = json.Unmarshal(req.Body, &in)
		if in.ParentID == "al-browse" {
			browseCalls.Add(1)
			body, _ := json.Marshal(core.KindPage{
				Items: []core.KindItem{{ID: "tb1", Title: "Fallback Track", Playable: true, Index: 1}},
			})
			return plugins.Response{Status: http.StatusOK, Body: body}, nil
		}
		return plugins.Response{Status: http.StatusNotFound}, nil
	}

	alExpand, _, _ := items.GetByExternal(ctx, pluginID, "music", "al-expand")
	alBrowse, _, _ := items.GetByExternal(ctx, pluginID, "music", "al-browse")
	alEmpty, _, _ := items.GetByExternal(ctx, pluginID, "music", "al-empty")

	// 1. List render (Browse, Item, ItemsMany) must NEVER trigger container hydration.
	if _, err := kc.Browse(ctx, musicLib, frontend.ItemQuery{Level: "album"}, "", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Item(ctx, alExpand.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.ItemsMany(ctx, []core.RatingKey{alExpand.Key, alBrowse.Key}); err != nil {
		t.Fatal(err)
	}
	if expandCalls.Load() != 0 || browseCalls.Load() != 0 {
		t.Fatalf("list/item reads triggered hydration: expand=%d browse=%d", expandCalls.Load(), browseCalls.Load())
	}

	// 2. Opening empty container (childCount=0) must NOT trigger hydration.
	emptyKids, err := kc.Children(ctx, alEmpty.Key, false, "", 20)
	if err != nil || len(emptyKids.Items) != 0 {
		t.Fatalf("empty container children = %+v, err=%v", emptyKids, err)
	}
	if expandCalls.Load() != 0 || browseCalls.Load() != 0 {
		t.Fatalf("childCount=0 container triggered hydration: expand=%d browse=%d", expandCalls.Load(), browseCalls.Load())
	}

	// 3. Concurrent opens of al-expand coalesce into a single /expand call and
	//    both callers see hydrated children immediately.
	var wg sync.WaitGroup
	results := make([]experience.PluginBrowsePage, 2)
	errs := make([]error, 2)
	for idx := range 2 {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			results[slot], errs[slot] = kc.Children(ctx, alExpand.Key, false, "", 20)
		}(idx)
	}
	<-expandStarted
	close(releaseExpand)
	wg.Wait()

	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("concurrent Children errs = %v, %v", errs[0], errs[1])
	}
	if expandCalls.Load() != 1 {
		t.Fatalf("concurrent opens must coalesce into 1 /expand call, got %d", expandCalls.Load())
	}
	if len(results[0].Items) < 1 || len(results[1].Items) < 1 {
		t.Fatalf("concurrent opens must see hydrated first page: %+v / %+v", results[0], results[1])
	}

	// Wait for background page 2 to commit and verify both tracks are stored.
	<-page2Done
	// Poll briefly for background page-2 upsert + FinishFacetExpansion commit.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		expRec, ok, _ := items.GetFacetExpansion(ctx, pluginID, "music", "__container__", "al-expand")
		if ok && expRec.State == pluginitems.ExpansionStateDone {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fullKids, err := kc.Children(ctx, alExpand.Key, false, "", 20)
	if err != nil || len(fullKids.Items) != 2 {
		t.Fatalf("after background continuation: children = %+v, err=%v (want 2 tracks)", fullKids, err)
	}

	// 4. Re-opening within TTL must NOT call /expand again.
	if expandCalls.Load() != 1 {
		t.Fatalf("re-open within TTL must not re-call /expand, got %d", expandCalls.Load())
	}

	// 5. Advancing clock past TTL (24h) triggers a fresh hydration on next open.
	advanceClock(25 * time.Hour)
	if _, err := kc.Children(ctx, alExpand.Key, false, "", 20); err != nil {
		t.Fatal(err)
	}
	if expandCalls.Load() != 2 {
		t.Fatalf("open past TTL must trigger fresh hydration, expandCalls=%d, want 2", expandCalls.Load())
	}

	// 6. /expand returning 404 falls back to /browse with {parentId, level} and
	//    normalizes missing level/parentId on returned items.
	fallbackKids, err := kc.Children(ctx, alBrowse.Key, false, "", 20)
	if err != nil {
		t.Fatalf("Children(al-browse): %v", err)
	}
	if browseCalls.Load() != 1 {
		t.Fatalf("expected 1 fallback /browse call, got %d", browseCalls.Load())
	}
	if len(fallbackKids.Items) != 1 || fallbackKids.Items[0].Title != "Fallback Track" {
		t.Fatalf("fallback children = %+v, want [Fallback Track]", fallbackKids.Items)
	}
}

func TestKindCatalogProjectSummaryResolvesPluginStrings(t *testing.T) {
	ctx := context.Background()
	h := newKindTestHarness(t, 2*time.Second, nil)

	const pluginID = "community.example.books"
	rawMan := map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Books Plugin",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "$t:summary",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"strings":      map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"mediaKinds": []any{
					map[string]any{
						"key":   "book",
						"label": "$t:kind.books",
						"media": "book",
						"library": map[string]any{
							"name": "$t:library.books",
						},
						"levels": []any{
							map[string]any{"key": "book", "label": "$t:level.book", "role": "book", "playable": true},
						},
						"fields": []any{
							map[string]any{"key": "authors", "type": "people", "label": "$t:field.authors", "indexed": true},
							map[string]any{"key": "language", "type": "string", "label": "$t:field.language", "indexed": false, "fact": true},
							map[string]any{"key": "publisher", "type": "string", "label": "$t:field.missing", "indexed": true, "fact": true},
						},
						"present": map[string]any{
							"vela": map[string]any{"type": "movies"},
						},
						"browse": map[string]any{
							"sorts":  []string{"title"},
							"facets": []string{"authors", "publisher"},
						},
						"sync": map[string]any{"mode": "push"},
					},
				},
			},
		},
	}
	enStrings := []byte(`{
		"summary": "A book library plugin",
		"kind.books": "Books",
		"library.books": "Books",
		"level.book": "Book",
		"field.authors": "Authors",
		"field.language": "Language"
	}`)
	installStudioManifest(t, h.mgr, rawMan, map[string][]byte{"i18n/en.json": enStrings})
	if err := h.mgr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var bookLib core.Library
	for _, lib := range mustListLibs(t, h.libs) {
		if lib.PluginID == pluginID {
			bookLib = lib
		}
	}
	if bookLib.ID == 0 {
		t.Fatal("expected book library")
	}

	_, err := h.items.KindUpsert(ctx, pluginID, "book", []core.KindItem{
		{
			ID:       "b1",
			Level:    "book",
			Title:    "Dune",
			Summary:  "Desert planet saga.",
			Playable: true,
			Fields: map[string]json.RawMessage{
				"authors":   json.RawMessage(`["Frank Herbert"]`),
				"language":  json.RawMessage(`"English"`),
				"publisher": json.RawMessage(`"Chilton Books"`),
			},
		},
	})
	if err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}

	// Browse projection keeps clean Summary, maps people into MediaCredits, and
	// resolves $t:field.language -> "Language" while falling back to "publisher"
	// when $t:field.missing is absent from en.json.
	page, err := h.catalog.Browse(ctx, bookLib, frontend.ItemQuery{Sort: core.SortTitle}, "", 20)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("Browse = %+v, err=%v", page, err)
	}
	checkProjected := func(label string, title core.Title) {
		t.Helper()
		if title.Summary != "Desert planet saga." {
			t.Fatalf("%s summary = %q, want clean summary", label, title.Summary)
		}
		if len(title.MediaCredits) != 1 || title.MediaCredits[0].Name != "Frank Herbert" || title.MediaCredits[0].Role != "author" {
			t.Fatalf("%s MediaCredits = %+v", label, title.MediaCredits)
		}
		facts := map[string]string{}
		for _, f := range title.MediaFacts {
			if strings.Contains(f.Label, "$t:") || strings.Contains(f.Value, "$t:") {
				t.Fatalf("%s MediaFacts leaked raw $t: token: %+v", label, f)
			}
			facts[f.Label] = f.Value
		}
		if facts["Language"] != "English" {
			t.Fatalf("%s MediaFacts did not resolve $t:field.language: %+v", label, title.MediaFacts)
		}
		if facts["publisher"] != "Chilton Books" {
			t.Fatalf("%s MediaFacts did not fall back to field key for missing $t: key: %+v", label, title.MediaFacts)
		}
	}
	checkProjected("Browse", page.Items[0])

	// Single-item read and Presentation facet labels also resolve without leaking "$t:".
	piv, err := h.catalog.Item(ctx, page.Items[0].Key)
	if err != nil {
		t.Fatalf("Item: %v", err)
	}
	checkProjected("Item", piv.Title)
	pres, ok := h.catalog.Presentation(ctx, bookLib)
	if !ok {
		t.Fatal("Presentation returned ok=false")
	}
	for _, f := range pres.Facets {
		if strings.Contains(f.Label, "$t:") {
			t.Fatalf("Presentation facet %q leaked $t: label %q", f.ID, f.Label)
		}
		if f.ID == "authors" && f.Label != "Authors" {
			t.Fatalf("authors facet label = %q, want Authors", f.Label)
		}
		if f.ID == "publisher" && f.Label != "publisher" {
			t.Fatalf("missing $t: facet label = %q, want field key publisher", f.Label)
		}
	}
}
