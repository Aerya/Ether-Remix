package adapters_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontends/plex"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/sessions"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func containerExpandMusicManifest(pluginID string, expandCfg map[string]any) map[string]any {
	browse := map[string]any{
		"sorts":  []string{"title", "year", "index"},
		"facets": []string{"genre", "composer"},
		"search": true,
	}
	if expandCfg != nil {
		browse["expand"] = expandCfg
	}
	return map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Container Expand Music",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Music tree with container expand on open",
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
								map[string]any{"key": "artists", "label": "Artists", "kind": "library", "level": "artist"},
								map[string]any{"key": "albums", "label": "Albums", "kind": "library", "level": "album"},
							},
						},
						"levels": []any{
							map[string]any{"key": "artist", "label": "Artist", "container": true, "role": "artist"},
							map[string]any{"key": "album", "label": "Album", "parent": "artist", "container": true, "role": "album"},
							map[string]any{"key": "concert", "label": "Concert", "parent": "artist", "playable": true, "role": "concert"},
							map[string]any{"key": "track", "label": "Track", "parent": "album", "playable": true, "role": "track"},
						},
						"fields": []any{
							map[string]any{"key": "genre", "label": "Genre", "type": "string", "indexed": true},
							map[string]any{"key": "composer", "label": "Composer", "type": "people", "indexed": true},
						},
						"present": map[string]any{
							"vela": map[string]any{"type": "shows"},
							"plex": map[string]any{"base": "artist"},
							"mediaBrowser": map[string]any{
								"collectionType": "music",
								"itemTypes": map[string]string{
									"artist":  "MusicArtist",
									"album":   "MusicAlbum",
									"concert": "Video",
									"track":   "Audio",
								},
							},
						},
						"browse": browse,
						"sync":   map[string]any{"mode": "push"},
					},
				},
			},
		},
	}
}

func TestManifestBrowseExpandLevelsValidation(t *testing.T) {
	// Levels-only browse.expand (facets omitted) is valid when every level has container: true.
	m, err := parseAndValidateManifest(t, containerExpandMusicManifest("community.example.levels-only", map[string]any{
		"levels":   []string{"artist", "album"},
		"ttlHours": 24,
	}))
	if err != nil {
		t.Fatalf("expected valid levels-only browse.expand, got %v", err)
	}
	exp := m.Ether.Contributes.MediaKinds[0].Browse.Expand
	if !exp.ExpandsLevel("artist") || !exp.ExpandsLevel("album") || exp.ExpandsLevel("track") || exp.ExpandsLevel("concert") {
		t.Fatalf("ExpandsLevel mismatch on %+v", exp)
	}

	// Non-container level must be rejected.
	if _, err := parseAndValidateManifest(t, containerExpandMusicManifest("community.example.non-container", map[string]any{
		"levels": []string{"track"},
	})); err == nil {
		t.Fatal("expected non-container level in browse.expand.levels to fail validation")
	}

	// Playable non-container branch level must be rejected.
	if _, err := parseAndValidateManifest(t, containerExpandMusicManifest("community.example.concert-non-container", map[string]any{
		"levels": []string{"concert"},
	})); err == nil {
		t.Fatal("expected container:false branch level in browse.expand.levels to fail validation")
	}

	// Undeclared level must be rejected.
	if _, err := parseAndValidateManifest(t, containerExpandMusicManifest("community.example.unknown-level", map[string]any{
		"levels": []string{"label"},
	})); err == nil {
		t.Fatal("expected undeclared level in browse.expand.levels to fail validation")
	}

	// Duplicate level must be rejected.
	if _, err := parseAndValidateManifest(t, containerExpandMusicManifest("community.example.dup-level", map[string]any{
		"levels": []string{"artist", "artist"},
	})); err == nil {
		t.Fatal("expected duplicate level in browse.expand.levels to fail validation")
	}

	// Empty browse.expand (neither facets nor levels) must be rejected.
	if _, err := parseAndValidateManifest(t, containerExpandMusicManifest("community.example.empty-expand", map[string]any{})); err == nil {
		t.Fatal("expected empty browse.expand to fail validation")
	}
}

type containerExpandHarness struct {
	db     *sql.DB
	mgr    *plugins.Manager
	libs   *libraries.Service
	items  *pluginitems.Service
	kc     *adapters.KindCatalog
	rt     *listChannelFakeRuntime
	lib    core.Library
	plugin string
}

type containerExpandEnv struct {
	db      *sql.DB
	dataDir string
	cfg     *config.Store
	blobs   *blobcache.Cache
	blobDir string
}

func openContainerExpandEnv(t *testing.T) *containerExpandEnv {
	t.Helper()
	dataDir := t.TempDir()
	cfg, err := config.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	blobDir := filepath.Join(dataDir, "blobcache")
	blobs, err := blobcache.Open(blobDir, blobcache.Options{Logger: log, SweepPeriod: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	return &containerExpandEnv{
		db:      db,
		dataDir: dataDir,
		cfg:     cfg,
		blobs:   blobs,
		blobDir: blobDir,
	}
}

func newContainerExpandHarnessWithEnv(t *testing.T, env *containerExpandEnv, pluginID string, ttlHours int, now func() time.Time) (*containerExpandHarness, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	libs, err := libraries.New(ctx, env.db, libraries.Options{Logger: log})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	rt := &listChannelFakeRuntime{routes: map[string]func(plugins.Request) (plugins.Response, error){}}
	mgr, err := plugins.New(plugins.Deps{
		DB:       env.db,
		Sealer:   env.cfg,
		Registry: extend.New(),
		Tools:    tools.NewCatalogue(),
		Logger:   log,
		IsLeader: func() bool { return true },
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	mgr.RegisterRuntime("hosted", func(_ plugins.Installed) (plugins.Runtime, error) { return rt, nil })

	items, err := pluginitems.New(ctx, env.db, pluginitems.Options{
		Caller:   mgr,
		Health:   mgr,
		Logger:   log,
		IsLeader: func() bool { return true },
		Now:      now,
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	kc := adapters.AttachKindCatalog(ctx, adapters.KindCatalogDeps{
		Manager:   mgr,
		Libraries: libs,
		Items:     items,
		Blobs:     env.blobs,
		BlobDir:   env.blobDir,
		Logger:    log,
	})

	installStudioManifest(t, mgr, containerExpandMusicManifest(pluginID, map[string]any{
		"levels":   []string{"artist", "album"},
		"ttlHours": ttlHours,
	}), nil)
	if err := mgr.Start(ctx); err != nil {
		items.Close()
		cancel()
		t.Fatalf("mgr.Start: %v", err)
	}

	var lib core.Library
	for _, l := range mustListLibs(t, libs) {
		if l.PluginID == pluginID {
			lib = l
		}
	}
	if lib.ID == 0 {
		items.Close()
		cancel()
		t.Fatalf("library for %s not created", pluginID)
	}
	cleanup := func() {
		items.Close()
		cancel()
	}
	return &containerExpandHarness{
		db:     env.db,
		mgr:    mgr,
		libs:   libs,
		items:  items,
		kc:     kc,
		rt:     rt,
		lib:    lib,
		plugin: pluginID,
	}, cleanup
}

func newContainerExpandHarness(t *testing.T, pluginID string, ttlHours int, now func() time.Time) *containerExpandHarness {
	t.Helper()
	h, cleanup := newContainerExpandHarnessWithEnv(t, openContainerExpandEnv(t), pluginID, ttlHours, now)
	t.Cleanup(cleanup)
	return h
}

func (h *containerExpandHarness) mustItem(t *testing.T, extID string) core.KindItem {
	t.Helper()
	it, _, err := h.items.GetByExternal(context.Background(), h.plugin, "music", extID)
	if err != nil {
		t.Fatalf("GetByExternal(%q): %v", extID, err)
	}
	return it
}

func TestContainerExpandOnOpen(t *testing.T) {
	t.Run("first open expands children, refreshes container, ignores plugin childCount, preserves additive rows, updates tallies, validates tree", func(t *testing.T) {
		h := newContainerExpandHarness(t, "community.example.w306.first", 24, nil)
		ctx := context.Background()

		// Seed artist with bogus plugin-supplied ChildCount=99 and 1 pre-seeded album
		// that will be omitted by /expand to verify additive-only semantics.
		if _, err := h.items.KindUpsert(ctx, h.plugin, "music", []core.KindItem{
			{ID: "ar1", Level: "artist", Title: "Artist Initial", Summary: "Old bio", ChildCount: 99},
			{ID: "al-seeded", Level: "album", ParentID: "ar1", Title: "Seeded Album", Index: 1},
			{ID: "ar-bad", Level: "artist", Title: "Bad Expand Artist"},
		}); err != nil {
			t.Fatal(err)
		}

		ar1 := h.mustItem(t, "ar1")
		arBad := h.mustItem(t, "ar-bad")

		var (
			reqMu    sync.Mutex
			requests []core.KindExpandRequest
		)
		h.rt.routes["/ether/v1/kinds/music/expand"] = func(req plugins.Request) (plugins.Response, error) {
			var in core.KindExpandRequest
			_ = json.Unmarshal(req.Body, &in)
			reqMu.Lock()
			requests = append(requests, in)
			reqMu.Unlock()

			switch in.ParentID {
			case "ar1":
				if in.Level != "artist" || in.Facet != "" || in.Value != "" || in.ValueID != 0 {
					t.Errorf("unexpected artist expand request: %+v", in)
				}
				body, _ := json.Marshal(core.KindExpandResponse{
					Items: []core.KindItem{
						// Optional self-refresh of the container with bogus ChildCount=500.
						{ID: "ar1", Level: "artist", Title: "Artist Refreshed", Summary: "New bio", ChildCount: 500},
						{ID: "al1", Level: "album", ParentID: "ar1", Title: "Album One", Index: 2, ChildCount: 42},
						{ID: "al2", Level: "album", ParentID: "ar1", Title: "Album Two", Index: 3},
						{ID: "c1", Level: "concert", ParentID: "ar1", Title: "Live at Red Rocks", Playable: true, Index: 1},
					},
				})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil

			case "al1":
				if in.Level != "album" || in.Facet != "" || in.Value != "" {
					t.Errorf("unexpected album expand request: %+v", in)
				}
				body, _ := json.Marshal(core.KindExpandResponse{
					Items: []core.KindItem{
						{
							ID: "tr1", Level: "track", ParentID: "al1", Title: "Track One", Playable: true, Index: 1,
							Fields: map[string]json.RawMessage{
								"genre":    json.RawMessage(`"Rock"`),
								"composer": json.RawMessage(`["Alice"]`),
							},
						},
						{
							ID: "tr2", Level: "track", ParentID: "al1", Title: "Track Two", Playable: true, Index: 2,
							Fields: map[string]json.RawMessage{
								"genre":    json.RawMessage(`"Rock"`),
								"composer": json.RawMessage(`["Alice"]`),
							},
						},
					},
				})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil

			case "ar-bad":
				// Invalid tree link: track cannot have artist as parent (LevelParent("track") == "album").
				body, _ := json.Marshal(core.KindExpandResponse{
					Items: []core.KindItem{
						{ID: "tr-illegal", Level: "track", ParentID: "ar-bad", Title: "Illegal Track", Playable: true},
					},
				})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil
			}
			return plugins.Response{Status: http.StatusNotFound}, nil
		}

		// Browse, Search, ItemsMany, and suppressed Item must NEVER trigger expand,
		// and ChildCount on tallied levels must reflect only stored tallies (1, not 99).
		page, err := h.kc.Browse(ctx, h.lib, frontend.ItemQuery{Limit: 20}, "", 20)
		if err != nil {
			t.Fatalf("Browse: %v", err)
		}
		for _, it := range page.Items {
			if it.Key == ar1.Key {
				if it.ChildCount != 1 {
					t.Fatalf("Browse ar1 ChildCount = %d, want 1 (stored tally, ignoring plugin 99)", it.ChildCount)
				}
				if it.LeafCount != 0 {
					t.Fatalf("Browse ar1 LeafCount = %d, want 0", it.LeafCount)
				}
			}
		}
		if _, err := h.kc.Search(ctx, h.lib, "Artist", frontend.ItemQuery{Limit: 10}, 10); err != nil {
			t.Fatalf("Search: %v", err)
		}
		if _, err := h.kc.ItemsMany(ctx, []core.RatingKey{ar1.Key}); err != nil {
			t.Fatalf("ItemsMany: %v", err)
		}
		suppressed, err := h.kc.Item(core.WithNoContainerExpand(ctx), ar1.Key)
		if err != nil {
			t.Fatalf("suppressed Item: %v", err)
		}
		if suppressed.Title.ChildCount != 1 {
			t.Fatalf("suppressed Item ChildCount = %d, want 1", suppressed.Title.ChildCount)
		}
		reqMu.Lock()
		if len(requests) != 0 {
			t.Fatalf("expected 0 expand calls before container open, got %d: %+v", len(requests), requests)
		}
		reqMu.Unlock()

		// First open of ar1 via Item: expands artist, refreshes metadata in place,
		// keeps al-seeded (additive), and reports exact tallied counts (ignoring bogus 500).
		piv, err := h.kc.Item(ctx, ar1.Key)
		if err != nil {
			t.Fatalf("Item(ar1): %v", err)
		}
		if piv.Title.Title != "Artist Refreshed" || piv.Title.Summary != "New bio" {
			t.Fatalf("expected container self-refresh, got title=%q summary=%q", piv.Title.Title, piv.Title.Summary)
		}
		if piv.Title.ChildCount != 4 {
			t.Fatalf("Item(ar1) ChildCount = %d, want 4 (al-seeded + al1 + al2 + c1)", piv.Title.ChildCount)
		}
		if piv.Title.ChildCounts["album"] != 3 || piv.Title.ChildCounts["concert"] != 1 {
			t.Fatalf("Item(ar1) ChildCounts = %+v, want album=3 concert=1", piv.Title.ChildCounts)
		}

		// Filtered children (?role=album and ?role=concert) see all expanded + seeded rows
		// without re-calling /expand because ar1 is fresh.
		albums, err := h.kc.ChildrenFiltered(ctx, ar1.Key, false, "", "album", "", 50)
		if err != nil {
			t.Fatalf("ChildrenFiltered(album): %v", err)
		}
		if len(albums.Items) != 3 {
			t.Fatalf("album children = %d, want 3 (al-seeded, al1, al2)", len(albums.Items))
		}
		concerts, err := h.kc.ChildrenFiltered(ctx, ar1.Key, false, "", "concert", "", 50)
		if err != nil {
			t.Fatalf("ChildrenFiltered(concert): %v", err)
		}
		if len(concerts.Items) != 1 || concerts.Items[0].Title != "Live at Red Rocks" {
			t.Fatalf("concert children = %+v, want 1 concert", concerts.Items)
		}

		// Open al1 via Children: expands tracks and keeps parent + field + person tallies exact.
		al1 := h.mustItem(t, "al1")
		tracks, err := h.kc.Children(ctx, al1.Key, false, "", 50)
		if err != nil {
			t.Fatalf("Children(al1): %v", err)
		}
		if len(tracks.Items) != 2 {
			t.Fatalf("al1 tracks = %d, want 2", len(tracks.Items))
		}
		al1View, err := h.kc.Item(ctx, al1.Key)
		if err != nil {
			t.Fatalf("Item(al1): %v", err)
		}
		if al1View.Title.ChildCount != 2 || al1View.Title.LeafCount != 2 || al1View.Title.ChildCounts["track"] != 2 {
			t.Fatalf("al1 counts = child:%d leaf:%d byRole:%+v, want 2/2/{track:2}",
				al1View.Title.ChildCount, al1View.Title.LeafCount, al1View.Title.ChildCounts)
		}
		ar1After, err := h.kc.Item(ctx, ar1.Key)
		if err != nil {
			t.Fatalf("Item(ar1 after album expand): %v", err)
		}
		// LeafCount of ar1 = 1 playable concert + 2 playable tracks = 3.
		if ar1After.Title.ChildCount != 4 || ar1After.Title.LeafCount != 3 {
			t.Fatalf("ar1 after album expand: ChildCount=%d LeafCount=%d, want 4/3",
				ar1After.Title.ChildCount, ar1After.Title.LeafCount)
		}
		// Field/person tallies (migration 17) must include the expanded tracks.
		genres, err := h.kc.FacetsAtLevel(ctx, h.lib, "track", "genre")
		if err != nil {
			t.Fatalf("FacetsAtLevel(track, genre): %v", err)
		}
		if len(genres) != 1 || genres[0].Title != "Rock" || genres[0].Count != 2 {
			t.Fatalf("genre facet tally = %+v, want [{Title:Rock Count:2}]", genres)
		}
		composers, err := h.kc.FacetsAtLevel(ctx, h.lib, "track", "composer")
		if err != nil {
			t.Fatalf("FacetsAtLevel(track, composer): %v", err)
		}
		if len(composers) != 1 || composers[0].Title != "Alice" || composers[0].Count != 2 {
			t.Fatalf("composer person tally = %+v, want [{Title:Alice Count:2}]", composers)
		}

		// Invalid hierarchy item in /expand response must be rejected cleanly.
		badKids, err := h.kc.Children(ctx, arBad.Key, false, "", 50)
		if err != nil {
			t.Fatalf("Children(ar-bad): %v", err)
		}
		if len(badKids.Items) != 0 {
			t.Fatalf("expected illegal track under artist to be rejected, got %+v", badKids.Items)
		}
		if _, _, err := h.items.GetByExternal(ctx, h.plugin, "music", "tr-illegal"); err == nil {
			t.Fatal("illegal track should not have been stored")
		}

		reqMu.Lock()
		gotReqs := slices.Clone(requests)
		reqMu.Unlock()
		if len(gotReqs) != 3 {
			t.Fatalf("expected exactly 3 expand calls (ar1, al1, ar-bad), got %d: %+v", len(gotReqs), gotReqs)
		}
	})

	t.Run("concurrent opens share one single-flight call", func(t *testing.T) {
		if os.Getenv("ETHER_TEST_POSTGRES") != "" {
			t.Skip("synctest bubbles cannot own PostgreSQL connections; the race is store-independent")
		}
		env := openContainerExpandEnv(t)
		synctest.Test(t, func(t *testing.T) {
			h, cleanup := newContainerExpandHarnessWithEnv(t, env, "community.example.w306.concurrent", 24, time.Now)
			defer func() {
				cleanup()
				synctest.Wait()
			}()
			ctx := context.Background()
			if _, err := h.items.KindUpsert(ctx, h.plugin, "music", []core.KindItem{
				{ID: "ar1", Level: "artist", Title: "Concurrent Artist"},
			}); err != nil {
				t.Fatal(err)
			}
			ar1 := h.mustItem(t, "ar1")

			var calls atomic.Int32
			expandStarted := make(chan struct{}, 2)
			releaseExpand := make(chan struct{})

			h.rt.routes["/ether/v1/kinds/music/expand"] = func(req plugins.Request) (plugins.Response, error) {
				calls.Add(1)
				expandStarted <- struct{}{}
				<-releaseExpand
				body, _ := json.Marshal(core.KindExpandResponse{
					Items: []core.KindItem{
						{ID: "al1", Level: "album", ParentID: "ar1", Title: "Album One", Index: 1},
						{ID: "al2", Level: "album", ParentID: "ar1", Title: "Album Two", Index: 2},
					},
				})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil
			}

			var wg sync.WaitGroup
			var itemCount int
			var childrenCount int
			var filteredCount int

			wg.Add(3)
			go func() {
				defer wg.Done()
				piv, err := h.kc.Item(ctx, ar1.Key)
				if err != nil {
					t.Errorf("Item: %v", err)
					return
				}
				itemCount = piv.Title.ChildCount
			}()
			go func() {
				defer wg.Done()
				p, err := h.kc.Children(ctx, ar1.Key, false, "", 50)
				if err != nil {
					t.Errorf("Children: %v", err)
					return
				}
				childrenCount = len(p.Items)
			}()
			go func() {
				defer wg.Done()
				p, err := h.kc.ChildrenFiltered(ctx, ar1.Key, false, "", "album", "", 50)
				if err != nil {
					t.Errorf("ChildrenFiltered: %v", err)
					return
				}
				filteredCount = len(p.Items)
			}()

			<-expandStarted
			synctest.Wait()
			close(releaseExpand)
			wg.Wait()
			synctest.Wait()

			if got := calls.Load(); got != 1 {
				t.Fatalf("concurrent opens triggered %d expand calls, want 1", got)
			}
			if itemCount != 2 || childrenCount != 2 || filteredCount != 2 {
				t.Fatalf("concurrent callers saw itemCount=%d childrenCount=%d filteredCount=%d, want 2/2/2",
					itemCount, childrenCount, filteredCount)
			}
		})
	})

	t.Run("fresh expansion within TTL does not call again and re-expands after TTL expiry", func(t *testing.T) {
		if os.Getenv("ETHER_TEST_POSTGRES") != "" {
			t.Skip("synctest bubbles cannot own PostgreSQL connections; the race is store-independent")
		}
		env := openContainerExpandEnv(t)
		synctest.Test(t, func(t *testing.T) {
			h, cleanup := newContainerExpandHarnessWithEnv(t, env, "community.example.w306.ttl", 24, time.Now)
			defer func() {
				cleanup()
				synctest.Wait()
			}()
			ctx := context.Background()
			if _, err := h.items.KindUpsert(ctx, h.plugin, "music", []core.KindItem{
				{ID: "ar1", Level: "artist", Title: "TTL Artist"},
			}); err != nil {
				t.Fatal(err)
			}
			ar1 := h.mustItem(t, "ar1")

			var calls atomic.Int32
			h.rt.routes["/ether/v1/kinds/music/expand"] = func(req plugins.Request) (plugins.Response, error) {
				n := calls.Add(1)
				items := []core.KindItem{
					{ID: "al1", Level: "album", ParentID: "ar1", Title: "Album One", Index: 1},
				}
				if n >= 2 {
					items = append(items, core.KindItem{ID: "al2", Level: "album", ParentID: "ar1", Title: "Album Two", Index: 2})
				}
				body, _ := json.Marshal(core.KindExpandResponse{Items: items})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil
			}

			// First open expands.
			p1, err := h.kc.Children(ctx, ar1.Key, false, "", 50)
			if err != nil {
				t.Fatalf("first Children: %v", err)
			}
			synctest.Wait()
			if len(p1.Items) != 1 || calls.Load() != 1 {
				t.Fatalf("first open: items=%d calls=%d, want 1/1", len(p1.Items), calls.Load())
			}

			// Advance 12h (< 24h TTL): both Item and Children skip /expand.
			time.Sleep(12 * time.Hour)
			if _, err := h.kc.Item(ctx, ar1.Key); err != nil {
				t.Fatalf("Item within TTL: %v", err)
			}
			if _, err := h.kc.Children(ctx, ar1.Key, false, "", 50); err != nil {
				t.Fatalf("Children within TTL: %v", err)
			}
			synctest.Wait()
			if calls.Load() != 1 {
				t.Fatalf("expected no re-expand within TTL, got %d calls", calls.Load())
			}

			// Advance another 13h (25h total > 24h TTL): next open re-expands.
			time.Sleep(13 * time.Hour)
			p2, err := h.kc.Children(ctx, ar1.Key, false, "", 50)
			if err != nil {
				t.Fatalf("Children after TTL: %v", err)
			}
			synctest.Wait()
			if calls.Load() != 2 || len(p2.Items) != 2 {
				t.Fatalf("after TTL expiry: calls=%d items=%d, want 2/2", calls.Load(), len(p2.Items))
			}
		})
	})

	t.Run("zero stored children re-expands even within TTL", func(t *testing.T) {
		if os.Getenv("ETHER_TEST_POSTGRES") != "" {
			t.Skip("synctest bubbles cannot own PostgreSQL connections; the race is store-independent")
		}
		env := openContainerExpandEnv(t)
		synctest.Test(t, func(t *testing.T) {
			h, cleanup := newContainerExpandHarnessWithEnv(t, env, "community.example.w306.zero", 24, time.Now)
			defer func() {
				cleanup()
				synctest.Wait()
			}()
			ctx := context.Background()
			if _, err := h.items.KindUpsert(ctx, h.plugin, "music", []core.KindItem{
				{ID: "ar-empty", Level: "artist", Title: "Empty Then Populated Artist"},
			}); err != nil {
				t.Fatal(err)
			}
			arEmpty := h.mustItem(t, "ar-empty")

			var calls atomic.Int32
			h.rt.routes["/ether/v1/kinds/music/expand"] = func(req plugins.Request) (plugins.Response, error) {
				n := calls.Add(1)
				if n == 1 {
					// First expand completes cleanly with 0 children.
					body, _ := json.Marshal(core.KindExpandResponse{Items: nil})
					return plugins.Response{Status: http.StatusOK, Body: body}, nil
				}
				body, _ := json.Marshal(core.KindExpandResponse{
					Items: []core.KindItem{
						{ID: "al-late", Level: "album", ParentID: "ar-empty", Title: "Arrived Later", Index: 1},
					},
				})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil
			}

			// Open 1: expands, stores 0 children, marks expansion done.
			p1, err := h.kc.Children(ctx, arEmpty.Key, false, "", 50)
			if err != nil {
				t.Fatalf("open 1: %v", err)
			}
			synctest.Wait()
			if len(p1.Items) != 0 || calls.Load() != 1 {
				t.Fatalf("open 1: items=%d calls=%d, want 0/1", len(p1.Items), calls.Load())
			}

			// Open 2 (immediately, well within TTL): 0 stored children must re-expand.
			p2, err := h.kc.Children(ctx, arEmpty.Key, false, "", 50)
			if err != nil {
				t.Fatalf("open 2: %v", err)
			}
			synctest.Wait()
			if len(p2.Items) != 1 || calls.Load() != 2 {
				t.Fatalf("open 2 (0 stored children): items=%d calls=%d, want 1/2", len(p2.Items), calls.Load())
			}

			// Open 3 (within TTL and now has 1 stored child): must NOT re-expand.
			p3, err := h.kc.Children(ctx, arEmpty.Key, false, "", 50)
			if err != nil {
				t.Fatalf("open 3: %v", err)
			}
			synctest.Wait()
			if len(p3.Items) != 1 || calls.Load() != 2 {
				t.Fatalf("open 3 (fresh + non-empty): items=%d calls=%d, want 1/2", len(p3.Items), calls.Load())
			}
		})
	})

	t.Run("slow expand returns stored page after bounded wait and later pages finish in background", func(t *testing.T) {
		if os.Getenv("ETHER_TEST_POSTGRES") != "" {
			t.Skip("synctest bubbles cannot own PostgreSQL connections; the race is store-independent")
		}
		env := openContainerExpandEnv(t)
		synctest.Test(t, func(t *testing.T) {
			h, cleanup := newContainerExpandHarnessWithEnv(t, env, "community.example.w306.slow", 24, time.Now)
			defer func() {
				cleanup()
				synctest.Wait()
			}()
			if _, err := h.items.KindUpsert(context.Background(), h.plugin, "music", []core.KindItem{
				{ID: "ar-slow", Level: "artist", Title: "Slow Artist"},
				{ID: "al-initial", Level: "album", ParentID: "ar-slow", Title: "Initial Stored Album", Index: 1},
			}); err != nil {
				t.Fatal(err)
			}
			arSlow := h.mustItem(t, "ar-slow")

			var calls atomic.Int32
			releasePage1 := make(chan struct{})
			page2Started := make(chan struct{}, 1)
			releasePage2 := make(chan struct{})

			h.rt.routes["/ether/v1/kinds/music/expand"] = func(req plugins.Request) (plugins.Response, error) {
				var in core.KindExpandRequest
				_ = json.Unmarshal(req.Body, &in)
				calls.Add(1)
				if in.Cursor == "" {
					<-releasePage1
					body, _ := json.Marshal(core.KindExpandResponse{
						Items:  []core.KindItem{{ID: "al-p1", Level: "album", ParentID: "ar-slow", Title: "Page 1 Album", Index: 2}},
						Cursor: "p2",
					})
					return plugins.Response{Status: http.StatusOK, Body: body}, nil
				}
				page2Started <- struct{}{}
				<-releasePage2
				body, _ := json.Marshal(core.KindExpandResponse{
					Items: []core.KindItem{{ID: "al-p2", Level: "album", ParentID: "ar-slow", Title: "Page 2 Album", Index: 3}},
				})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil
			}

			// First open hits firstOpenWait (virtual clock advances 1.8s immediately)
			// and returns the 1 stored album while page 1 is still blocked.
			detailCtx, pending := core.WithDetailPending(context.Background(), arSlow.Key)
			p1, err := h.kc.Children(detailCtx, arSlow.Key, false, "", 50)
			if err != nil {
				t.Fatalf("slow Children: %v", err)
			}
			if len(p1.Items) != 1 || p1.Items[0].Title != "Initial Stored Album" {
				t.Fatalf("expected stored page on bounded wait timeout, got %+v", p1.Items)
			}
			if !slices.Contains(pending.Fields(), "children") {
				t.Fatalf("expected DetailPending to report children on timeout, got %v", pending.Fields())
			}

			// Release page 1: background job upserts page 1 and starts page 2.
			close(releasePage1)
			<-page2Started
			synctest.Wait()

			// While page 2 is still blocked in background, another open joins the
			// in-flight job without starting a duplicate call and sees page 1 immediately.
			pMid, err := h.kc.Children(context.Background(), arSlow.Key, false, "", 50)
			if err != nil {
				t.Fatalf("mid Children: %v", err)
			}
			if len(pMid.Items) != 2 {
				t.Fatalf("expected initial + page 1 (2 albums) while page 2 runs, got %d", len(pMid.Items))
			}

			// Release page 2 and let the background expand job finish cleanly.
			close(releasePage2)
			synctest.Wait()

			// Next open sees all 3 albums with accurate count and no extra expand call.
			view, err := h.kc.Item(context.Background(), arSlow.Key)
			if err != nil {
				t.Fatalf("final Item: %v", err)
			}
			if view.Title.ChildCount != 3 || view.Title.ChildCounts["album"] != 3 {
				t.Fatalf("final Item counts = child:%d byRole:%+v, want 3/{album:3}", view.Title.ChildCount, view.Title.ChildCounts)
			}
			pFinal, err := h.kc.Children(context.Background(), arSlow.Key, false, "", 50)
			if err != nil {
				t.Fatalf("final Children: %v", err)
			}
			if len(pFinal.Items) != 3 {
				t.Fatalf("final Children = %d, want 3", len(pFinal.Items))
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("expected exactly 2 expand HTTP calls (page 1 + page 2), got %d", got)
			}
		})
	})

	t.Run("experience scope triggers on container opens across frontends and never on artwork or non-cold StoredItem", func(t *testing.T) {
		h := newContainerExpandHarness(t, "community.example.w306.scope", 24, nil)
		ctx := context.Background()

		if _, err := h.items.KindUpsert(ctx, h.plugin, "music", []core.KindItem{
			{ID: "ar-plex", Level: "artist", Title: "Plex Artist"},
			{ID: "ar-vela", Level: "artist", Title: "Vela Artist"},
			{ID: "ar-jf", Level: "artist", Title: "Jellyfin Artist"},
		}); err != nil {
			t.Fatal(err)
		}
		arPlex := h.mustItem(t, "ar-plex")
		arVela := h.mustItem(t, "ar-vela")
		arJF := h.mustItem(t, "ar-jf")

		var calls atomic.Int32
		h.rt.routes["/ether/v1/kinds/music/expand"] = func(req plugins.Request) (plugins.Response, error) {
			var in core.KindExpandRequest
			_ = json.Unmarshal(req.Body, &in)
			calls.Add(1)
			body, _ := json.Marshal(core.KindExpandResponse{
				Items: []core.KindItem{
					{ID: "al-" + in.ParentID, Level: "album", ParentID: in.ParentID, Title: "Album of " + in.ParentID, Index: 1},
				},
			})
			return plugins.Response{Status: http.StatusOK, Body: body}, nil
		}

		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		st := state.New(h.db, state.Options{Logger: log})
		owner := &core.User{ID: 1, IsOwner: true}
		getNone := func(context.Context, *core.User, core.RatingKey) (*core.Title, error) {
			return nil, core.ErrNotFound
		}
		vis := experience.BindVisibility(nil, func(ctx context.Context, k core.RatingKey) (*core.Title, error) {
			return getNone(ctx, nil, k)
		}, nil, nil, nil)
		exp, err := experience.NewService(experience.ServiceDeps{
			Active: func(context.Context, int64) (*core.User, error) {
				cp := *owner
				return &cp, nil
			},
			State:      st,
			Rows:       &experience.RowsService{},
			Protocol:   "vela",
			Visibility: vis,
			LibraryPresentation: func(p core.Presentations, mt core.MediaType) (core.MediaType, string, bool) {
				if p.AdapterOnly {
					return "", "", false
				}
				return mt, "shows", true
			},
			PluginLibraries: h.kc,
			Get:             getNone,
			Children: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
				return nil, nil
			},
			Search: experience.Search{
				Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) {
					return nil, nil
				},
			},
		})
		if err != nil {
			t.Fatalf("experience.NewService: %v", err)
		}
		viewer := &core.Viewer{ID: 1, IsOwner: true}

		// Non-cold StoredItem (Plex Home hubs, batch metadata, artwork) must NOT expand.
		if _, err := exp.StoredItem(ctx, frontend.ItemRequest{Viewer: viewer, Key: arPlex.Key}); err != nil {
			t.Fatalf("non-cold StoredItem: %v", err)
		}
		// Artwork authorization (Home, Search, library grids) must NOT expand.
		if err := exp.AuthorizeArtworkAs(ctx, viewer, core.Device{}, arVela.Key); err != nil {
			t.Fatalf("AuthorizeArtworkAs: %v", err)
		}
		if calls.Load() != 0 {
			t.Fatalf("non-open paths triggered %d expand calls, want 0", calls.Load())
		}

		// Plex /library/metadata/{id} sets WithColdDetail on StoredItem -> MUST expand.
		plexDetail, err := exp.StoredItem(core.WithColdDetail(ctx, arPlex.Key), frontend.ItemRequest{Viewer: viewer, Key: arPlex.Key})
		if err != nil {
			t.Fatalf("cold StoredItem: %v", err)
		}
		if plexDetail.Title.ChildCount != 1 {
			t.Fatalf("cold StoredItem ChildCount = %d, want 1", plexDetail.Title.ChildCount)
		}

		// Vela /titles/{id} & Jellyfin /Items/{id} call Item -> MUST expand.
		velaDetail, err := exp.Item(core.WithColdDetail(ctx, arVela.Key), frontend.ItemRequest{Viewer: viewer, Key: arVela.Key})
		if err != nil {
			t.Fatalf("Item: %v", err)
		}
		if velaDetail.Title.ChildCount != 1 {
			t.Fatalf("Item ChildCount = %d, want 1", velaDetail.Title.ChildCount)
		}

		// Jellyfin Items?ParentId= / Vela /children?role=album call Children -> MUST expand.
		jfKids, err := exp.Children(ctx, viewer, core.Device{}, arJF.Key, frontend.ItemQuery{Role: "album", Limit: 20})
		if err != nil {
			t.Fatalf("Children: %v", err)
		}
		if len(jfKids.Items) != 1 || jfKids.Items[0].Title != "Album of ar-jf" {
			t.Fatalf("Children(ar-jf) = %+v, want 1 expanded album", jfKids.Items)
		}
		if got := calls.Load(); got != 3 {
			t.Fatalf("expected 3 expand calls (Plex cold StoredItem, Vela/JF Item, JF/Vela Children), got %d", got)
		}
	})

	t.Run("self-refresh with different parentId preserves stored parent and keeps children", func(t *testing.T) {
		env := openContainerExpandEnv(t)
		synctest.Test(t, func(t *testing.T) {
			h, cleanup := newContainerExpandHarnessWithEnv(t, env, "community.example.w320.parent-mismatch", 24, time.Now)
			defer func() {
				cleanup()
				synctest.Wait()
			}()
			ctx := context.Background()

			// Seed featured artist ar-246791 with two collaboration albums:
			// - al-6877261 whose self-refresh names an unstored primary artist (ar-4050205)
			// - al-stored-primary whose self-refresh names a different stored artist (ar-primary)
			if _, err := h.items.KindUpsert(ctx, h.plugin, "music", []core.KindItem{
				{ID: "ar-246791", Level: "artist", Title: "Featured Artist"},
				{ID: "ar-primary", Level: "artist", Title: "Primary Artist"},
				{ID: "al-6877261", Level: "album", ParentID: "ar-246791", Title: "Collab Initial", Summary: "Old summary", Year: 2020, Index: 1},
				{ID: "al-stored-primary", Level: "album", ParentID: "ar-246791", Title: "Split Initial", Summary: "Old split", Index: 2},
			}); err != nil {
				t.Fatal(err)
			}

			arFeatured := h.mustItem(t, "ar-246791")
			arPrimary := h.mustItem(t, "ar-primary")
			alCollab := h.mustItem(t, "al-6877261")
			alSplit := h.mustItem(t, "al-stored-primary")

			var calls atomic.Int32
			h.rt.routes["/ether/v1/kinds/music/expand"] = func(req plugins.Request) (plugins.Response, error) {
				var in core.KindExpandRequest
				_ = json.Unmarshal(req.Body, &in)
				calls.Add(1)
				switch in.ParentID {
				case "al-6877261":
					body, _ := json.Marshal(core.KindExpandResponse{
						Items: []core.KindItem{
							// Self-refresh names an unstored primary artist ar-4050205.
							{ID: "al-6877261", Level: "album", ParentID: "ar-4050205", Title: "Collab Deluxe", Summary: "Full liner notes", Year: 2024},
							{ID: "tr-1", Level: "track", ParentID: "al-6877261", Title: "Track One", Playable: true, Index: 1},
							{ID: "tr-2", Level: "track", ParentID: "al-6877261", Title: "Track Two", Playable: true, Index: 2},
						},
					})
					return plugins.Response{Status: http.StatusOK, Body: body}, nil
				case "al-stored-primary":
					body, _ := json.Marshal(core.KindExpandResponse{
						Items: []core.KindItem{
							// Self-refresh names a different stored artist ar-primary.
							{ID: "al-stored-primary", Level: "album", ParentID: "ar-primary", Title: "Split Deluxe", Summary: "Refreshed split"},
							{ID: "tr-3", Level: "track", ParentID: "al-stored-primary", Title: "Track Three", Playable: true, Index: 1},
						},
					})
					return plugins.Response{Status: http.StatusOK, Body: body}, nil
				}
				return plugins.Response{Status: http.StatusNotFound}, nil
			}

			// Opening al-6877261 must refresh its metadata, keep stored parent ar-246791, and store both tracks.
			piv, err := h.kc.Item(ctx, alCollab.Key)
			if err != nil {
				t.Fatalf("Item(al-6877261): %v", err)
			}
			synctest.Wait()
			if piv.Title.Title != "Collab Deluxe" || piv.Title.Summary != "Full liner notes" || piv.Title.Year != 2024 {
				t.Fatalf("expected refreshed metadata on al-6877261, got title=%q summary=%q year=%d",
					piv.Title.Title, piv.Title.Summary, piv.Title.Year)
			}
			if piv.Title.ChildCount != 2 || piv.Title.LeafCount != 2 || piv.Title.ChildCounts["track"] != 2 {
				t.Fatalf("al-6877261 counts = child:%d leaf:%d byRole:%+v, want 2/2/{track:2}",
					piv.Title.ChildCount, piv.Title.LeafCount, piv.Title.ChildCounts)
			}
			storedCollab := h.mustItem(t, "al-6877261")
			if storedCollab.ParentID != "ar-246791" {
				t.Fatalf("al-6877261 stored parentId = %q, want stored parent %q", storedCollab.ParentID, "ar-246791")
			}
			tracks, err := h.kc.Children(ctx, alCollab.Key, false, "", 50)
			if err != nil {
				t.Fatalf("Children(al-6877261): %v", err)
			}
			if len(tracks.Items) != 2 {
				t.Fatalf("al-6877261 tracks = %d, want 2", len(tracks.Items))
			}
			expRec, ok, err := h.items.GetFacetExpansion(ctx, h.plugin, "music", "__container__", "al-6877261")
			if err != nil || !ok || expRec.State != pluginitems.ExpansionStateDone {
				t.Fatalf("al-6877261 expansion record = (%+v, ok=%v, err=%v), want state=done", expRec, ok, err)
			}

			// Opening al-stored-primary must likewise keep ar-246791 and leave ar-primary untouched.
			splitView, err := h.kc.Item(ctx, alSplit.Key)
			if err != nil {
				t.Fatalf("Item(al-stored-primary): %v", err)
			}
			synctest.Wait()
			if splitView.Title.Title != "Split Deluxe" || splitView.Title.Summary != "Refreshed split" || splitView.Title.ChildCount != 1 {
				t.Fatalf("al-stored-primary after expand = title:%q summary:%q childCount:%d",
					splitView.Title.Title, splitView.Title.Summary, splitView.Title.ChildCount)
			}
			if got := h.mustItem(t, "al-stored-primary").ParentID; got != "ar-246791" {
				t.Fatalf("al-stored-primary stored parentId = %q, want %q", got, "ar-246791")
			}

			// Featured artist still owns both albums and sees all 3 expanded leaf tracks; primary artist is untouched.
			featuredView, err := h.kc.Item(core.WithNoContainerExpand(ctx), arFeatured.Key)
			if err != nil {
				t.Fatalf("Item(ar-246791): %v", err)
			}
			if featuredView.Title.ChildCount != 2 || featuredView.Title.LeafCount != 3 {
				t.Fatalf("ar-246791 counts = child:%d leaf:%d, want 2/3", featuredView.Title.ChildCount, featuredView.Title.LeafCount)
			}
			primaryView, err := h.kc.Item(core.WithNoContainerExpand(ctx), arPrimary.Key)
			if err != nil {
				t.Fatalf("Item(ar-primary): %v", err)
			}
			if primaryView.Title.ChildCount != 0 || primaryView.Title.LeafCount != 0 {
				t.Fatalf("ar-primary counts = child:%d leaf:%d, want 0/0", primaryView.Title.ChildCount, primaryView.Title.LeafCount)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("expected exactly 2 expand calls (one per album), got %d", got)
			}
		})
	})

	t.Run("failed expansion backs off across opens and single-flights across detail and children", func(t *testing.T) {
		env := openContainerExpandEnv(t)
		synctest.Test(t, func(t *testing.T) {
			h, cleanup := newContainerExpandHarnessWithEnv(t, env, "community.example.w320.backoff", 24, time.Now)
			defer func() {
				cleanup()
				synctest.Wait()
			}()
			ctx := context.Background()

			if _, err := h.items.KindUpsert(ctx, h.plugin, "music", []core.KindItem{
				{ID: "ar-err", Level: "artist", Title: "Failing Artist"},
				{ID: "al-err", Level: "album", ParentID: "ar-err", Title: "Failing Album", Index: 1},
			}); err != nil {
				t.Fatal(err)
			}
			alErr := h.mustItem(t, "al-err")

			var (
				calls      atomic.Int32
				shouldFail atomic.Bool
			)
			shouldFail.Store(true)
			firstStarted := make(chan struct{}, 1)
			releaseFirst := make(chan struct{})

			h.rt.routes["/ether/v1/kinds/music/expand"] = func(req plugins.Request) (plugins.Response, error) {
				n := calls.Add(1)
				if n == 1 {
					firstStarted <- struct{}{}
					<-releaseFirst
				}
				if shouldFail.Load() {
					return plugins.Response{Status: http.StatusInternalServerError}, nil
				}
				body, _ := json.Marshal(core.KindExpandResponse{
					Items: []core.KindItem{
						{ID: "tr-ok-1", Level: "track", ParentID: "al-err", Title: "Recovered Track 1", Playable: true, Index: 1},
						{ID: "tr-ok-2", Level: "track", ParentID: "al-err", Title: "Recovered Track 2", Playable: true, Index: 2},
					},
				})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil
			}

			// Concurrent detail + children of one page view share one single-flight failing expand.
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				if _, err := h.kc.Item(ctx, alErr.Key); err != nil {
					t.Errorf("concurrent Item: %v", err)
				}
			}()
			go func() {
				defer wg.Done()
				if _, err := h.kc.Children(ctx, alErr.Key, false, "", 50); err != nil {
					t.Errorf("concurrent Children: %v", err)
				}
			}()

			<-firstStarted
			synctest.Wait()
			close(releaseFirst)
			wg.Wait()
			synctest.Wait()

			if got := calls.Load(); got != 1 {
				t.Fatalf("concurrent detail+children on failing expand triggered %d calls, want 1", got)
			}
			expRec, ok, err := h.items.GetFacetExpansion(ctx, h.plugin, "music", "__container__", "al-err")
			if err != nil || !ok || expRec.State != pluginitems.ExpansionStateError || expRec.FinishedAt.IsZero() || expRec.Error == "" {
				t.Fatalf("expected persisted error record after failed expand, got (%+v, ok=%v, err=%v)", expRec, ok, err)
			}

			// Immediate sequential calls (same page view / rapid reopens) must NOT retry even with 0 stored children.
			if _, err := h.kc.Item(ctx, alErr.Key); err != nil {
				t.Fatalf("immediate Item: %v", err)
			}
			if _, err := h.kc.Children(ctx, alErr.Key, false, "", 50); err != nil {
				t.Fatalf("immediate Children: %v", err)
			}
			if _, err := h.kc.ChildrenFiltered(ctx, alErr.Key, false, "", "track", "", 50); err != nil {
				t.Fatalf("immediate ChildrenFiltered: %v", err)
			}
			synctest.Wait()
			if got := calls.Load(); got != 1 {
				t.Fatalf("immediate opens during error backoff triggered %d calls, want 1", got)
			}

			// Still within error cooldown at +5m and +14m: must stay suppressed.
			time.Sleep(5 * time.Minute)
			if _, err := h.kc.Item(ctx, alErr.Key); err != nil {
				t.Fatalf("Item at +5m: %v", err)
			}
			if _, err := h.kc.Children(ctx, alErr.Key, false, "", 50); err != nil {
				t.Fatalf("Children at +5m: %v", err)
			}
			time.Sleep(9 * time.Minute)
			if _, err := h.kc.Children(ctx, alErr.Key, false, "", 50); err != nil {
				t.Fatalf("Children at +14m: %v", err)
			}
			synctest.Wait()
			if got := calls.Load(); got != 1 {
				t.Fatalf("opens within 15m error cooldown triggered %d calls, want 1", got)
			}

			// Once error cooldown has elapsed (+16m total), the next open retries cleanly and succeeds.
			shouldFail.Store(false)
			time.Sleep(2 * time.Minute)
			kids, err := h.kc.Children(ctx, alErr.Key, false, "", 50)
			if err != nil {
				t.Fatalf("Children after cooldown: %v", err)
			}
			synctest.Wait()
			if got := calls.Load(); got != 2 || len(kids.Items) != 2 {
				t.Fatalf("after cooldown retry: calls=%d items=%d, want 2/2", got, len(kids.Items))
			}
			view, err := h.kc.Item(ctx, alErr.Key)
			if err != nil {
				t.Fatalf("Item after recovery: %v", err)
			}
			if view.Title.ChildCount != 2 {
				t.Fatalf("recovered Item ChildCount = %d, want 2", view.Title.ChildCount)
			}
			expDone, ok, err := h.items.GetFacetExpansion(ctx, h.plugin, "music", "__container__", "al-err")
			if err != nil || !ok || expDone.State != pluginitems.ExpansionStateDone {
				t.Fatalf("expected state=done after recovery, got (%+v, ok=%v, err=%v)", expDone, ok, err)
			}
		})
	})
}

// TestPlexAudioPlayQueueExpandsNeverOpenedArtist proves end-to-end against real
// KindCatalog + experience.Service + plex.Server:
//  1. Concurrent POST /playQueues?type=audio and GET /library/metadata/{artist}/allLeaves
//     on a never-opened artist share one single-flight /expand call and queue ONLY audio
//     tracks (excluding expanded video concerts).
//  2. A failing artist /expand enters the 15-minute cooldown so repeated play-queue
//     requests do not retry or storm.
//  3. Section shuffle (/library/sections/{id}/all) never expands unopened artists.
func TestPlexAudioPlayQueueExpandsNeverOpenedArtist(t *testing.T) {
	if os.Getenv("ETHER_TEST_POSTGRES") != "" {
		t.Skip("synctest bubbles cannot own PostgreSQL connections; the race is store-independent")
	}
	env := openContainerExpandEnv(t)
	synctest.Test(t, func(t *testing.T) {
		h, cleanup := newContainerExpandHarnessWithEnv(t, env, "community.example.w323.plex", 24, time.Now)
		defer func() {
			cleanup()
			synctest.Wait()
		}()
		ctx := context.Background()
		if _, err := h.items.KindUpsert(ctx, h.plugin, "music", []core.KindItem{
			{ID: "ar-cold", Level: "artist", Title: "Cold Artist"},
			{ID: "ar-fail", Level: "artist", Title: "Failing Artist"},
			{ID: "ar-idle", Level: "artist", Title: "Idle Artist"},
		}); err != nil {
			t.Fatal(err)
		}
		arCold := h.mustItem(t, "ar-cold")
		arFail := h.mustItem(t, "ar-fail")

		var coldCalls, failCalls, idleCalls atomic.Int32
		coldStarted := make(chan struct{}, 1)
		releaseCold := make(chan struct{})
		h.rt.routes["/ether/v1/kinds/music/expand"] = func(req plugins.Request) (plugins.Response, error) {
			var in core.KindExpandRequest
			_ = json.Unmarshal(req.Body, &in)
			switch in.ParentID {
			case "ar-cold":
				n := coldCalls.Add(1)
				if n == 1 {
					select {
					case coldStarted <- struct{}{}:
					default:
					}
					<-releaseCold
				}
				body, _ := json.Marshal(core.KindExpandResponse{
					Items: []core.KindItem{
						{ID: "al-cold", Level: "album", ParentID: "ar-cold", Title: "Expanded Album", Index: 1},
						{ID: "tr-cold-1", Level: "track", ParentID: "al-cold", Title: "Expanded Track 1", Playable: true, Index: 1},
						{ID: "tr-cold-2", Level: "track", ParentID: "al-cold", Title: "Expanded Track 2", Playable: true, Index: 2},
						{ID: "con-cold-1", Level: "concert", ParentID: "ar-cold", Title: "Expanded Concert", Playable: true, Index: 1},
					},
				})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil
			case "ar-fail":
				failCalls.Add(1)
				return plugins.Response{Status: http.StatusBadGateway, Body: []byte(`{"error":"upstream"}`)}, nil
			default:
				idleCalls.Add(1)
				return plugins.Response{Status: http.StatusOK, Body: []byte(`{"items":[]}`)}, nil
			}
		}

		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		st := state.New(h.db, state.Options{Logger: log})
		owner := &core.User{ID: 1, Username: "owner", Title: "Owner", IsOwner: true}
		getNone := func(context.Context, *core.User, core.RatingKey) (*core.Title, error) {
			return nil, core.ErrNotFound
		}
		vis := experience.BindVisibility(nil, func(ctx context.Context, k core.RatingKey) (*core.Title, error) {
			return getNone(ctx, nil, k)
		}, nil, nil, nil)
		exp, err := experience.NewService(experience.ServiceDeps{
			Active: func(context.Context, int64) (*core.User, error) {
				cp := *owner
				return &cp, nil
			},
			State:               st,
			Rows:                &experience.RowsService{},
			Protocol:            "plex",
			Visibility:          vis,
			LibraryPresentation: plex.ProjectLibrary,
			PluginLibraries:     h.kc,
			Get:                 getNone,
			Children: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
				return nil, nil
			},
			Search: experience.Search{
				Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) {
					return nil, nil
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		rt := sessions.NewLocalRuntime(sessions.LocalRuntimeDeps{})
		cfg := config.Config{}
		cfg.Server.FriendlyName = "Test"
		cfg.Server.MachineIdentifier = "test-machine-id"
		srv := plex.NewServer(plex.Deps{
			DisableEnrichment: true,
			Host: frontend.Host{
				Log:        log,
				Now:        time.Now,
				HostConfig: func() config.Config { return cfg },
				Identity: frontendtest.Identity{
					ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) {
						cp := *owner
						return &cp, nil
					},
				},
				Experience: exp,
				Sessions:   rt,
			},
		})
		defer func() {
			srv.Close()
			synctest.Wait()
		}()

		doReq := func(method, target string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, target, nil)
			r.Header.Set("Accept", "application/json")
			r.Header.Set("X-Plex-Token", "owner-tok")
			r.Header.Set("X-Plex-Client-Identifier", "plexamp-w323")
			r.Header.Set("X-Plex-Product", "Plexamp")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			return w
		}
		extractKeys := func(rec *httptest.ResponseRecorder) []string {
			var body struct {
				MediaContainer struct {
					Metadata []struct {
						RatingKey string `json:"ratingKey"`
					} `json:"Metadata"`
				} `json:"MediaContainer"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %s: %v", rec.Body.String(), err)
			}
			out := make([]string, 0, len(body.MediaContainer.Metadata))
			for _, m := range body.MediaContainer.Metadata {
				out = append(out, m.RatingKey)
			}
			return out
		}

		// 1. Concurrent play queue + /allLeaves on cold artist -> 1 single-flight expand,
		//    2 audio tracks returned, expanded video concert excluded.
		coldURI := "server://test-machine-id/com.plexapp.plugins.library/library/metadata/" + arCold.Key.String()
		var (
			wg           sync.WaitGroup
			pqRec, alRec *httptest.ResponseRecorder
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			pqRec = doReq(http.MethodPost, "/playQueues?type=audio&uri="+url.QueryEscape(coldURI))
		}()
		go func() {
			defer wg.Done()
			alRec = doReq(http.MethodGet, "/library/metadata/"+arCold.Key.String()+"/allLeaves")
		}()
		<-coldStarted
		synctest.Wait()
		close(releaseCold)
		wg.Wait()
		synctest.Wait()

		if coldCalls.Load() != 1 {
			t.Fatalf("concurrent playQueue+/allLeaves triggered %d expands, want 1", coldCalls.Load())
		}
		tr1 := h.mustItem(t, "tr-cold-1").Key.String()
		tr2 := h.mustItem(t, "tr-cold-2").Key.String()
		con := h.mustItem(t, "con-cold-1").Key.String()
		for name, rec := range map[string]*httptest.ResponseRecorder{"playQueue": pqRec, "allLeaves": alRec} {
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status=%d body=%s", name, rec.Code, rec.Body.String())
			}
			keys := extractKeys(rec)
			if !slices.Equal(keys, []string{tr1, tr2}) || slices.Contains(keys, con) {
				t.Fatalf("%s keys = %v, want [%s %s] (concert %s excluded)", name, keys, tr1, tr2, con)
			}
		}

		// 2. Failing artist expand backs off for 15 minutes across repeated play queues.
		failURI := "server://test-machine-id/com.plexapp.plugins.library/library/metadata/" + arFail.Key.String()
		for i := range 3 {
			rec := doReq(http.MethodPost, "/playQueues?type=audio&uri="+url.QueryEscape(failURI))
			if rec.Code != http.StatusOK {
				t.Fatalf("failing artist playQueue %d status=%d body=%s", i, rec.Code, rec.Body.String())
			}
			synctest.Wait()
		}
		if failCalls.Load() != 1 {
			t.Fatalf("failing artist playQueue retries triggered %d expands, want 1 (15m backoff)", failCalls.Load())
		}

		// 3. Section shuffle never triggers expansion on unopened ar-idle.
		secRec := doReq(http.MethodGet, "/library/sections")
		var secResp struct {
			MediaContainer struct {
				Directory []struct {
					Key  string `json:"key"`
					Type string `json:"type"`
				} `json:"Directory"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(secRec.Body.Bytes(), &secResp); err != nil {
			t.Fatalf("decode sections: %v", err)
		}
		musicSec := ""
		for _, d := range secResp.MediaContainer.Directory {
			if d.Type == "artist" {
				musicSec = d.Key
				break
			}
		}
		if musicSec == "" {
			t.Fatalf("music section not found in %s", secRec.Body.String())
		}
		shufURI := "server://test-machine-id/com.plexapp.plugins.library/library/sections/" + musicSec + "/all"
		shufRec := doReq(http.MethodPost, "/playQueues?type=audio&shuffle=1&uri="+url.QueryEscape(shufURI))
		if shufRec.Code != http.StatusOK {
			t.Fatalf("section shuffle status=%d body=%s", shufRec.Code, shufRec.Body.String())
		}
		synctest.Wait()
		if idleCalls.Load() != 0 {
			t.Fatalf("section shuffle triggered %d expands on unopened ar-idle, want 0", idleCalls.Load())
		}
		if keys := extractKeys(shufRec); len(keys) != 2 || slices.Contains(keys, con) {
			t.Fatalf("section shuffle keys = %v, want 2 stored audio tracks only (no concert)", keys)
		}
	})
}
