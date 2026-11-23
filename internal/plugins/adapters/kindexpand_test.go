package adapters_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	ethersdk "github.com/braedonsaunders/ether/sdk/go"
)

type testExpandProvider struct {
	last ethersdk.KindExpandRequest
}

func (p *testExpandProvider) Browse(_ *ethersdk.Context, _ ethersdk.KindBrowseRequest) (ethersdk.KindPage, error) {
	return ethersdk.KindPage{}, nil
}

func (p *testExpandProvider) Detail(_ *ethersdk.Context, _, _ string) (ethersdk.KindItem, error) {
	return ethersdk.KindItem{}, ethersdk.ErrKindNotFound
}

func (p *testExpandProvider) Expand(_ *ethersdk.Context, req ethersdk.KindExpandRequest) (ethersdk.KindExpandResponse, error) {
	p.last = req
	return ethersdk.KindExpandResponse{
		Items:  []ethersdk.KindItem{{ID: "g-exp", Title: "Expanded Game"}},
		Cursor: "",
	}, nil
}

type testBrowseOnlyProvider struct{}

func (testBrowseOnlyProvider) Browse(_ *ethersdk.Context, _ ethersdk.KindBrowseRequest) (ethersdk.KindPage, error) {
	return ethersdk.KindPage{}, nil
}

func (testBrowseOnlyProvider) Detail(_ *ethersdk.Context, _, _ string) (ethersdk.KindItem, error) {
	return ethersdk.KindItem{}, ethersdk.ErrKindNotFound
}

func parseAndValidateManifest(t *testing.T, raw map[string]any) (plugins.Manifest, error) {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, err := plugins.ParseManifest(b)
	if err != nil {
		return plugins.Manifest{}, err
	}
	return m, m.Validate()
}

func TestManifestExpandValidationAndGrantPath(t *testing.T) {
	if !wasm.KindSyncPath("/ether/v1/kinds/games/expand") {
		t.Fatal("expected /ether/v1/kinds/games/expand to use KindSyncPath granted limits")
	}

	validMan := gamesExpandManifest("community.example.valid", map[string]any{
		"facets": []string{"developers"},
	})
	m, err := parseAndValidateManifest(t, validMan)
	if err != nil {
		t.Fatalf("expected valid browse.expand, got %v", err)
	}
	if got := m.Ether.Contributes.MediaKinds[0].Browse.Expand.EffectiveTTLHours(); got != 168 {
		t.Fatalf("EffectiveTTLHours() = %d, want 168", got)
	}

	if _, err := parseAndValidateManifest(t, gamesExpandManifest("community.example.badfacet", map[string]any{
		"facets": []string{"unknown"},
	})); err == nil {
		t.Fatal("expected undeclared browse.expand facet to fail validation")
	}

	if _, err := parseAndValidateManifest(t, gamesExpandManifest("community.example.negttl", map[string]any{
		"facets":   []string{"developers"},
		"ttlHours": -1,
	})); err == nil {
		t.Fatal("expected negative ttlHours to fail validation")
	}

	noProvider := gamesExpandManifest("community.example.noprovider", map[string]any{
		"facets": []string{"developers"},
	})
	etherBlock := noProvider["ether"].(map[string]any)
	etherBlock["capabilities"] = []string{"kind_resolver"}
	// Resolver-only kind cannot declare library either; strip library/present/sync/cache so we isolate browse.expand check.
	mk := etherBlock["contributes"].(map[string]any)["mediaKinds"].([]map[string]any)[0]
	delete(mk, "library")
	delete(mk, "present")
	delete(mk, "sync")
	delete(mk, "cache")
	if _, err := parseAndValidateManifest(t, noProvider); err == nil {
		t.Fatal("expected browse.expand without catalog_provider to fail validation")
	}
}

func TestGoSDKExpandDispatch(t *testing.T) {
	plugin := ethersdk.New(ethersdk.Definition{
		Host: ethersdk.NewMockHost(),
	})
	ep := &testExpandProvider{}
	plugin.OnCatalog("games", ep)
	plugin.OnCatalog("books", testBrowseOnlyProvider{})

	resp := plugin.Dispatch(context.Background(), ethersdk.RawRequest{
		Method: "POST",
		Path:   "/ether/v1/kinds/games/expand",
		Body:   []byte(`{"facet":"developers","value":"Nintendo"}`),
	})
	if resp.Status != 200 {
		t.Fatalf("expand status = %d, body = %s", resp.Status, string(resp.Body))
	}
	if ep.last.Kind != "games" || ep.last.Facet != "developers" || ep.last.Value != "Nintendo" {
		t.Fatalf("unexpected expand request: %+v", ep.last)
	}

	resp501 := plugin.Dispatch(context.Background(), ethersdk.RawRequest{
		Method: "POST",
		Path:   "/ether/v1/kinds/books/expand",
		Body:   []byte(`{"facet":"authors","value":"Tolkien"}`),
	})
	if resp501.Status != 501 {
		t.Fatalf("unimplemented expand status = %d, want 501", resp501.Status)
	}
}

func gamesExpandManifest(pluginID string, expand map[string]any) map[string]any {
	browse := map[string]any{
		"sorts":  []string{"title", "released"},
		"facets": []string{"developers", "platforms", "voiceCast"},
		"search": true,
	}
	if expand != nil {
		browse["expand"] = expand
	}
	return map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Expandable Games",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Expandable games catalogue",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider"},
			"strings":      map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"mediaKinds": []map[string]any{
					{
						"key":   "games",
						"label": "Games",
						"levels": []map[string]any{
							{"key": "game", "label": "Game", "playable": true},
						},
						"fields": []map[string]any{
							{"key": "developers", "type": "strings", "label": "Developers", "indexed": true},
							{"key": "platforms", "type": "strings", "label": "Platforms", "indexed": true},
							{"key": "voiceCast", "type": "people", "label": "Voice cast"},
						},
						"browse":  browse,
						"library": map[string]any{"name": "$t:lib_name"},
						"present": map[string]any{
							"plex":         map[string]any{"base": "movie"},
							"mediaBrowser": map[string]any{"collectionType": "movies", "itemTypes": map[string]any{"game": "Movie"}},
						},
						"sync":  map[string]any{"mode": "push"},
						"cache": map[string]any{"maxBytes": 16 << 20, "ttl": "24h"},
					},
				},
			},
		},
	}
}

func gamesExpandLocaleFiles() map[string][]byte {
	return map[string][]byte{
		"i18n/en.json": []byte(`{"lib_name":"Games"}`),
	}
}

func TestKindCatalogFacetAndPersonExpansion(t *testing.T) {
	h := newKindTestHarness(t, 0, nil)
	ctx := context.Background()
	const pluginID = "community.example.expandgames"

	enterExpand := make(chan core.KindExpandRequest, 8)
	releaseExpand := make(chan struct{})
	// The facet fill below is held open by the test, so the first open must
	// give up waiting and show the stored items; the person fill later runs
	// freely and the first open waits for it.
	defer adapters.SetFirstOpenWait(0)()
	var expandCalls atomic.Int32

	h.rt.mu.Lock()
	h.rt.routes["/ether/v1/kinds/games/expand"] = func(req plugins.Request) (plugins.Response, error) {
		var er core.KindExpandRequest
		if err := json.Unmarshal(req.Body, &er); err != nil {
			return plugins.Response{Status: http.StatusBadRequest}, nil
		}
		expandCalls.Add(1)
		switch {
		case er.Facet == "developers" && er.Value == "Nintendo" && er.Cursor == "":
			enterExpand <- er
			<-releaseExpand
			body, _ := json.Marshal(core.KindExpandResponse{
				Items: []core.KindItem{
					{
						ID:       "g2",
						Level:    "game",
						Title:    "Metroid Prime",
						Playable: true,
						Fields: map[string]json.RawMessage{
							"developers": json.RawMessage(`["Nintendo"]`),
							"platforms":  json.RawMessage(`["GameCube"]`),
						},
					},
				},
				Cursor: "page-2",
			})
			return plugins.Response{Status: http.StatusOK, Body: body}, nil
		case er.Facet == "developers" && er.Value == "Nintendo" && er.Cursor == "page-2":
			body, _ := json.Marshal(core.KindExpandResponse{
				Items: []core.KindItem{
					{
						ID:       "g3",
						Level:    "game",
						Title:    "Pikmin",
						Playable: true,
						Fields: map[string]json.RawMessage{
							"developers": json.RawMessage(`["Nintendo"]`),
							"platforms":  json.RawMessage(`["GameCube"]`),
						},
					},
				},
			})
			return plugins.Response{Status: http.StatusOK, Body: body}, nil
		case er.Facet == "voiceCast":
			enterExpand <- er
			body, _ := json.Marshal(core.KindExpandResponse{
				Items: []core.KindItem{
					{
						ID:       "g4",
						Level:    "game",
						Title:    "Luigi's Mansion",
						Playable: true,
						Fields: map[string]json.RawMessage{
							"developers": json.RawMessage(`["Nintendo"]`),
							"voiceCast":  json.RawMessage(`[{"name":"Charles Martinet"}]`),
						},
					},
				},
			})
			return plugins.Response{Status: http.StatusOK, Body: body}, nil
		default:
			return plugins.Response{Status: http.StatusOK, Body: []byte(`{"items":[]}`)}, nil
		}
	}
	h.rt.mu.Unlock()

	installStudioManifest(t, h.mgr, gamesExpandManifest(pluginID, map[string]any{
		"facets":   []string{"developers", "voiceCast"},
		"ttlHours": 24,
	}), gamesExpandLocaleFiles())

	var gameLib core.Library
	for _, lib := range mustListLibs(t, h.libs) {
		if lib.PluginID == pluginID {
			gameLib = lib
		}
	}
	if gameLib.ID == 0 {
		t.Fatal("expected games library")
	}
	viewID := strconv.FormatInt(int64(gameLib.ID), 10)

	// Seed 1 initial item in the store.
	if _, err := h.items.KindUpsert(ctx, pluginID, "games", []core.KindItem{
		{
			ID:       "g1",
			Level:    "game",
			Title:    "Super Mario 64",
			Playable: true,
			Fields: map[string]json.RawMessage{
				"developers": json.RawMessage(`["Nintendo"]`),
				"platforms":  json.RawMessage(`["N64"]`),
				"voiceCast":  json.RawMessage(`[{"name":"Charles Martinet"}]`),
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	exp := h.experienceFor(t, "plex")
	viewer := h.users[1]

	// 4. Undeclared facet ("platforms") and library rows / collections never expand.
	platPage, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Facets: map[string][]string{"platforms": {"N64"}}},
		Limit:  20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if platPage.Expanding || expandCalls.Load() != 0 || len(platPage.Items) != 1 {
		t.Fatalf("undeclared facet triggered expand or wrong items: expanding=%v calls=%d items=%d", platPage.Expanding, expandCalls.Load(), len(platPage.Items))
	}
	if _, err := exp.PluginLibraryRows(ctx, viewer, core.Device{}, viewID); err != nil {
		t.Fatal(err)
	}
	if expandCalls.Load() != 0 {
		t.Fatalf("PluginLibraryRows must not trigger expand, calls=%d", expandCalls.Load())
	}

	// 1. Opening a declared facet bucket starts 1 background expansion and returns stored items immediately without blocking.
	genBefore := h.items.Generation()
	page1, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Facets: map[string][]string{"developers": {"Nintendo"}}},
		Limit:  20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !page1.Expanding || len(page1.Items) != 1 || page1.Items[0].Title != "Super Mario 64" {
		t.Fatalf("initial open = %+v, want Expanding=true and 1 stored item immediately", page1)
	}

	select {
	case req := <-enterExpand:
		if req.Facet != "developers" || req.Value != "Nintendo" || req.ValueID != 0 {
			t.Fatalf("unexpected expand req: %+v", req)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for background /expand call")
	}

	// Concurrent open while running still reports Expanding=true and does NOT start a second job.
	pageWhileRunning, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Facets: map[string][]string{"developers": {"Nintendo"}}},
		Limit:  20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !pageWhileRunning.Expanding || expandCalls.Load() != 1 {
		t.Fatalf("while running: Expanding=%v calls=%d, want true and 1", pageWhileRunning.Expanding, expandCalls.Load())
	}

	// Release the blocked job and wait for durable completion.
	close(releaseExpand)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok, _ := h.items.GetFacetExpansion(ctx, pluginID, "games", "developers", "Nintendo")
		if ok && rec.State == pluginitems.ExpansionStateDone && !h.mgr.IsExpandJobRunning(pluginID, "games\x00developers\x00Nintendo") {
			if rec.ItemsAdded != 2 {
				t.Fatalf("ItemsAdded = %d, want 2", rec.ItemsAdded)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.items.Generation() <= genBefore {
		t.Fatalf("expected sync generation bump after expansion: before=%d after=%d", genBefore, h.items.Generation())
	}

	// 2 & 3. Second open within TTL does not start another expansion, omits Expanding, and returns all 3 items.
	callsAfterFirst := expandCalls.Load()
	if callsAfterFirst != 2 { // page 1 + page 2 cursor
		t.Fatalf("expected 2 page calls for first expansion, got %d", callsAfterFirst)
	}
	pageAfter, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Facets: map[string][]string{"developers": {"Nintendo"}}},
		Limit:  20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pageAfter.Expanding || len(pageAfter.Items) != 3 {
		t.Fatalf("after expansion: Expanding=%v len=%d, want false and 3 items", pageAfter.Expanding, len(pageAfter.Items))
	}
	if expandCalls.Load() != callsAfterFirst {
		t.Fatalf("second open within TTL started another call: %d vs %d", expandCalls.Load(), callsAfterFirst)
	}

	// 6. Opening a plugin person's titles triggers expansion with value + valueId.
	fp, err := h.catalog.FacetPeople(ctx, gameLib, "voiceCast", "name", "", 0, 10)
	if err != nil || len(fp.Items) != 1 {
		t.Fatalf("FacetPeople = %+v, err=%v", fp, err)
	}
	personID := fp.Items[0].PersonID
	defer adapters.SetFirstOpenWait(time.Minute)()
	personPage, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Person: personID},
		Limit:  20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if personPage.Expanding || len(personPage.Items) != 2 {
		t.Fatalf("person first open = %+v, want the filled catalogue (Expanding=false, 2 items)", personPage)
	}
	select {
	case req := <-enterExpand:
		if req.Facet != "voiceCast" || req.Value != "Charles Martinet" || req.ValueID != personID {
			t.Fatalf("unexpected person expand req: %+v (want valueId=%d)", req, personID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for person /expand call")
	}
	for time.Now().Before(time.Now().Add(5 * time.Second)) {
		rec, ok, _ := h.items.GetFacetExpansion(ctx, pluginID, "games", "voiceCast", strconv.FormatInt(personID, 10))
		if ok && rec.State == pluginitems.ExpansionStateDone && !h.mgr.IsExpandJobRunning(pluginID, "games\x00voiceCast\x00"+strconv.FormatInt(personID, 10)) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	personAfter, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Person: personID},
		Limit:  20,
	})
	if err != nil || personAfter.Expanding || len(personAfter.Items) != 2 {
		t.Fatalf("person after expansion = %+v, err=%v, want Expanding=false and 2 items", personAfter, err)
	}
}

func TestKindCatalogExpandMissingRouteIsNoOp(t *testing.T) {
	h := newKindTestHarness(t, 0, nil)
	ctx := context.Background()
	const pluginID = "community.example.noexpandroute"

	var calls atomic.Int32
	h.rt.mu.Lock()
	h.rt.routes["/ether/v1/kinds/games/expand"] = func(_ plugins.Request) (plugins.Response, error) {
		calls.Add(1)
		return plugins.Response{Status: http.StatusNotImplemented}, nil
	}
	h.rt.mu.Unlock()

	installStudioManifest(t, h.mgr, gamesExpandManifest(pluginID, map[string]any{
		"facets": []string{"developers"},
	}), gamesExpandLocaleFiles())

	var gameLib core.Library
	for _, lib := range mustListLibs(t, h.libs) {
		if lib.PluginID == pluginID {
			gameLib = lib
		}
	}
	viewID := strconv.FormatInt(int64(gameLib.ID), 10)
	exp := h.experienceFor(t, "plex")
	viewer := h.users[1]

	if _, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Facets: map[string][]string{"developers": {"Nintendo"}}},
		Limit:  20,
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok, _ := h.items.GetFacetExpansion(ctx, pluginID, "games", "developers", "Nintendo")
		if ok && rec.State == pluginitems.ExpansionStateDone && !h.mgr.IsExpandJobRunning(pluginID, "games\x00developers\x00Nintendo") {
			if rec.Error != "" || rec.ItemsAdded != 0 {
				t.Fatalf("expected clean no-op record, got %+v", rec)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Second open within TTL must not invoke the 501 route again.
	page2, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Facets: map[string][]string{"developers": {"Nintendo"}}},
		Limit:  20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page2.Expanding || calls.Load() != 1 {
		t.Fatalf("after 501 no-op: Expanding=%v calls=%d, want false and 1", page2.Expanding, calls.Load())
	}
}

func TestKindCatalogMaybeExpandBoundsSlowFirstOpenAndContinuesInBackground(t *testing.T) {
	// Default first-open wait must stay bounded (~1.8s, never 20s).
	if got := adapters.FirstOpenWait(); got <= 0 || got > 2*time.Second {
		t.Fatalf("default FirstOpenWait = %v, want bounded (0, 2s]", got)
	}

	h := newKindTestHarness(t, 0, nil)
	ctx := context.Background()
	const pluginID = "community.example.slowpersonexpand"

	releaseExpand := make(chan struct{})
	startedExpand := make(chan core.KindExpandRequest, 2)
	h.rt.mu.Lock()
	h.rt.routes["/ether/v1/kinds/games/expand"] = func(req plugins.Request) (plugins.Response, error) {
		var er core.KindExpandRequest
		if err := json.Unmarshal(req.Body, &er); err != nil {
			return plugins.Response{Status: http.StatusBadRequest}, nil
		}
		startedExpand <- er
		<-releaseExpand
		body, _ := json.Marshal(core.KindExpandResponse{
			Items: []core.KindItem{{
				ID:       "g-expanded",
				Level:    "game",
				Title:    "Luigi's Mansion",
				Playable: true,
				Fields: map[string]json.RawMessage{
					"voiceCast": json.RawMessage(`["Charles Martinet"]`),
				},
			}},
		})
		return plugins.Response{Status: http.StatusOK, Body: body}, nil
	}
	h.rt.mu.Unlock()

	installStudioManifest(t, h.mgr, gamesExpandManifest(pluginID, map[string]any{
		"facets": []string{"voiceCast"},
	}), gamesExpandLocaleFiles())

	var gameLib core.Library
	for _, lib := range mustListLibs(t, h.libs) {
		if lib.PluginID == pluginID {
			gameLib = lib
		}
	}
	if _, err := h.mgr.HostFor(pluginID).KindUpsert(ctx, "games", []core.KindItem{{
		ID:       "g1",
		Level:    "game",
		Title:    "Super Mario 64",
		Playable: true,
		Fields: map[string]json.RawMessage{
			"voiceCast": json.RawMessage(`["Charles Martinet"]`),
		},
	}}); err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}
	fp, err := h.catalog.FacetPeople(ctx, gameLib, "voiceCast", "name", "", 0, 10)
	if err != nil || len(fp.Items) != 1 {
		t.Fatalf("FacetPeople = %+v, err=%v", fp, err)
	}
	personID := fp.Items[0].PersonID

	// Bound first-open wait to 120ms while the plugin /expand stays blocked:
	// cold open must return promptly with Expanding=true and existing stored item.
	defer adapters.SetFirstOpenWait(120 * time.Millisecond)()
	exp := h.experienceFor(t, "vela")
	viewer := h.users[1]
	viewID := strconv.FormatInt(int64(gameLib.ID), 10)

	start := time.Now()
	coldPage, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Person: personID},
		Limit:  1,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("cold LibraryItems: %v", err)
	}
	if !coldPage.Expanding || len(coldPage.Items) != 1 {
		t.Fatalf("cold page = %+v, want Expanding=true and 1 stored item", coldPage)
	}
	if elapsed > time.Second {
		t.Fatalf("cold LibraryItems took %v, expected bounded wait ~120ms", elapsed)
	}
	select {
	case <-startedExpand:
	case <-time.After(2 * time.Second):
		t.Fatal("expected background expand to start")
	}

	// Unblock background expansion and confirm it finishes and populates warm reads.
	close(releaseExpand)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok, _ := h.items.GetFacetExpansion(ctx, pluginID, "games", "voiceCast", strconv.FormatInt(personID, 10))
		if ok && rec.State == pluginitems.ExpansionStateDone && !h.mgr.IsExpandJobRunning(pluginID, "games\x00voiceCast\x00"+strconv.FormatInt(personID, 10)) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	warmStart := time.Now()
	warmPage, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Person: personID},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("warm LibraryItems: %v", err)
	}
	if warmPage.Expanding || len(warmPage.Items) != 2 {
		t.Fatalf("warm page = %+v, want Expanding=false and 2 items", warmPage)
	}
	if time.Since(warmStart) > 500*time.Millisecond {
		t.Fatalf("warm LibraryItems took %v, expected fast local read", time.Since(warmStart))
	}
}

func TestKindCatalogMaybeExpandDoesNotReblockInFlightAndCoolsDownErrors(t *testing.T) {
	h := newKindTestHarness(t, 0, nil)
	ctx := context.Background()
	const pluginID = "community.example.expandcooldown"

	var calls atomic.Int32
	var shouldFail atomic.Bool
	shouldFail.Store(true)
	started := make(chan struct{}, 4)
	release := make(chan struct{})

	h.rt.mu.Lock()
	h.rt.routes["/ether/v1/kinds/games/expand"] = func(_ plugins.Request) (plugins.Response, error) {
		n := calls.Add(1)
		started <- struct{}{}
		if n == 1 {
			<-release
		}
		if shouldFail.Load() {
			return plugins.Response{Status: http.StatusInternalServerError}, nil
		}
		body, _ := json.Marshal(core.KindExpandResponse{
			Items: []core.KindItem{{
				ID:       "g-recovered",
				Level:    "game",
				Title:    "Recovered Game",
				Playable: true,
				Fields: map[string]json.RawMessage{
					"voiceCast": json.RawMessage(`["Jordan Lee"]`),
				},
			}},
		})
		return plugins.Response{Status: http.StatusOK, Body: body}, nil
	}
	h.rt.mu.Unlock()

	installStudioManifest(t, h.mgr, gamesExpandManifest(pluginID, map[string]any{
		"facets":   []string{"voiceCast"},
		"ttlHours": 24,
	}), gamesExpandLocaleFiles())

	var gameLib core.Library
	for _, lib := range mustListLibs(t, h.libs) {
		if lib.PluginID == pluginID {
			gameLib = lib
		}
	}
	if _, err := h.mgr.HostFor(pluginID).KindUpsert(ctx, "games", []core.KindItem{{
		ID:       "g1",
		Level:    "game",
		Title:    "Original Game",
		Playable: true,
		Fields: map[string]json.RawMessage{
			"voiceCast": json.RawMessage(`["Jordan Lee"]`),
		},
	}}); err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}
	fp, err := h.catalog.FacetPeople(ctx, gameLib, "voiceCast", "name", "", 0, 10)
	if err != nil || len(fp.Items) != 1 {
		t.Fatalf("FacetPeople = %+v, err=%v", fp, err)
	}
	personID := fp.Items[0].PersonID
	viewID := strconv.FormatInt(int64(gameLib.ID), 10)
	exp := h.experienceFor(t, "vela")
	viewer := h.users[1]

	// 1. First open waits once (10ms) while the failing /expand job stays blocked.
	restoreWait := adapters.SetFirstOpenWait(10 * time.Millisecond)
	defer restoreWait()
	restoreCooldown := adapters.SetExpansionErrorCooldown(15 * time.Minute)
	defer restoreCooldown()

	p1, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Person: personID},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if !p1.Expanding || len(p1.Items) != 1 {
		t.Fatalf("first open = %+v, want Expanding=true and 1 item", p1)
	}
	<-started

	// 2. While the same job is still running past firstOpenWait, a subsequent
	// open must return Expanding=true immediately without launching a second
	// job or waiting for another firstOpenWait window.
	p2, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Person: personID},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("in-flight second open: %v", err)
	}
	if !p2.Expanding || calls.Load() != 1 {
		t.Fatalf("in-flight second open: Expanding=%v calls=%d, want true and 1", p2.Expanding, calls.Load())
	}

	// 3. Release the failing job and wait for state='error' to persist.
	close(release)
	dedupKey := "games\x00voiceCast\x00" + strconv.FormatInt(personID, 10)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok, _ := h.items.GetFacetExpansion(ctx, pluginID, "games", "voiceCast", strconv.FormatInt(personID, 10))
		if ok && rec.State == pluginitems.ExpansionStateError && !h.mgr.IsExpandJobRunning(pluginID, dedupKey) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 4. Within error cooldown, re-opening the same person must NOT re-launch
	// /expand or stall even with a long firstOpenWait.
	restoreWait()
	defer adapters.SetFirstOpenWait(time.Minute)()
	pCooldown, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Person: personID},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("cooldown open: %v", err)
	}
	if pCooldown.Expanding || calls.Load() != 1 || len(pCooldown.Items) != 1 {
		t.Fatalf("cooldown open: Expanding=%v calls=%d items=%d, want false, 1, 1", pCooldown.Expanding, calls.Load(), len(pCooldown.Items))
	}

	// 5. Once error cooldown has elapsed, the next open retries /expand,
	// succeeds, and returns both items.
	shouldFail.Store(false)
	defer adapters.SetExpansionErrorCooldown(0)()
	pRetry, err := exp.LibraryItems(ctx, viewer, core.Device{}, viewID, frontend.ItemQuery{
		Filter: core.Filter{Person: personID},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("post-cooldown retry: %v", err)
	}
	if pRetry.Expanding || calls.Load() != 2 || len(pRetry.Items) != 2 {
		t.Fatalf("post-cooldown retry: Expanding=%v calls=%d items=%d, want false, 2, 2", pRetry.Expanding, calls.Load(), len(pRetry.Items))
	}
}
