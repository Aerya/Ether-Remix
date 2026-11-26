package adapters_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
)

// W348: a client that queues a cold plugin album (Plexamp) and opens its
// tracks at once gets every track. The plugin's staged album resolve runs
// one /resolve at a time for the album, each open waits server-side while
// it answers pending, and an album that is not on the sources still answers
// not available at once, without polling.
func TestKindOpenWaitsForSharedStagedAlbumResolve(t *testing.T) {
	mediaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		http.ServeContent(w, r, "t.flac", time.Unix(1_700_000_000, 0), bytes.NewReader([]byte("audio:"+r.URL.Path)))
	}))
	defer mediaSrv.Close()
	httpClient := &http.Client{Transport: hostRewriteTransport{target: mediaSrv.URL, base: mediaSrv.Client().Transport}}
	h := newKindTestHarness(t, 0, httpClient)
	ctx := context.Background()

	const pendingSlices = 3
	var (
		mu         sync.Mutex
		inFlight   = map[string]int{}
		maxFlight  = map[string]int{}
		calls      = map[string]int{}
		albumCalls atomic.Int32
	)
	h.rt.callContext = func(_ context.Context, req plugins.Request) (plugins.Response, error) {
		switch req.Path {
		case "/ether/health":
			return plugins.Response{Status: 200, Body: json.RawMessage(`{"status":"ok"}`)}, nil
		case "/ether/v1/kinds/recordings/resolve":
			var body core.KindResolveRequest
			if err := json.Unmarshal(req.Body, &body); err != nil {
				return plugins.Response{Status: 400}, nil
			}
			album := body.Item.ParentID
			mu.Lock()
			inFlight[album]++
			calls[body.Item.ID]++
			if inFlight[album] > maxFlight[album] {
				maxFlight[album] = inFlight[album]
			}
			mu.Unlock()
			defer func() {
				mu.Lock()
				inFlight[album]--
				mu.Unlock()
			}()
			// Hold the slice long enough that ungated siblings would overlap.
			time.Sleep(20 * time.Millisecond)
			if album == "alb-missing" {
				return plugins.Response{Status: 200, Body: json.RawMessage(`{"sources":[]}`)}, nil
			}
			if albumCalls.Add(1) <= pendingSlices {
				// An immediate "still preparing" answer Manager.Call does not
				// re-poll itself (as when the album's host indexer search is
				// still running): Ether must wait it out, not hand it to a
				// client that skips the track.
				return plugins.Response{Status: http.StatusAccepted, Body: json.RawMessage(`{"retryInMs":250}`)}, nil
			}
			return plugins.Response{Status: 200, Body: json.RawMessage(fmt.Sprintf(
				`{"sources":[{"id":"%[1]s","pattern":"range","mime":"audio/flac","url":"https://media.example/%[1]s.flac"}]}`, body.Item.ID))}, nil
		default:
			return plugins.Response{Status: 200, Body: json.RawMessage(`{"items":[]}`)}, nil
		}
	}

	const pluginID = "community.example.albums"
	decl := recordingsKindDecl(true)
	decl["sync"] = map[string]any{"mode": "push"}
	installStudioManifest(t, h.mgr, map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Albums",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Staged album resolver",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"permissions":  map[string]any{"network": []string{"media.example"}},
			"strings":      map[string]any{"en": "i18n/en.json"},
			"contributes":  map[string]any{"mediaKinds": []map[string]any{decl}},
		},
	}, map[string][]byte{"i18n/en.json": []byte(`{"lib_name":"Albums"}`)})

	tracks := []string{"t1", "t2", "t3", "t4"}
	upsert := []core.KindItem{
		{ID: "alb-cold", Level: "album", Title: "Cold Album"},
		{ID: "alb-missing", Level: "album", Title: "Missing Album"},
		{ID: "m1", Level: "track", ParentID: "alb-missing", Index: 1, Title: "Missing Track", Playable: true},
	}
	for i, id := range tracks {
		upsert = append(upsert, core.KindItem{ID: id, Level: "track", ParentID: "alb-cold", Index: i + 1, Title: "Track " + id, Playable: true})
	}
	if _, err := h.mgr.HostFor(pluginID).KindUpsert(ctx, "recordings", upsert); err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}
	kr := adapters.NewKindResolver(adapters.KindResolverDeps{
		Manager:       h.mgr,
		Libraries:     h.libs,
		Items:         h.items,
		Blobs:         h.blobs,
		Catalog:       h.catalog,
		HTTP:          httpClient,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		PreparingWait: 5 * time.Minute, // generous: the test asserts outcomes, never durations
	})
	viewer := &core.Viewer{ID: 1, IsOwner: true}

	var wg sync.WaitGroup
	errs := make([]error, len(tracks))
	bodies := make([]string, len(tracks))
	for i, id := range tracks {
		item, _, err := h.items.GetByExternal(ctx, pluginID, "recordings", id)
		if err != nil {
			t.Fatalf("GetByExternal(%s): %v", id, err)
		}
		wg.Add(1)
		go func(i int, key core.RatingKey) {
			defer wg.Done()
			media, err := kr.OpenKind(ctx, viewer, key, nil)
			if err != nil {
				errs[i] = err
				return
			}
			defer media.Closer.Close()
			b, err := io.ReadAll(io.NewSectionReader(media.ReaderAt, 0, media.Size))
			errs[i], bodies[i] = err, string(b)
		}(i, item.Key)
	}
	wg.Wait()
	for i, id := range tracks {
		if errs[i] != nil {
			t.Fatalf("open %s while the album resolve was staged: %v (want the open to wait it out)", id, errs[i])
		}
		if !strings.HasSuffix(bodies[i], "/"+id+".flac") {
			t.Fatalf("track %s read %q", id, bodies[i])
		}
	}
	mu.Lock()
	if maxFlight["alb-cold"] != 1 {
		t.Fatalf("album /resolve calls in flight at once = %d, want 1 (siblings share the staged resolve)", maxFlight["alb-cold"])
	}
	mu.Unlock()

	missing, _, err := h.items.GetByExternal(ctx, pluginID, "recordings", "m1")
	if err != nil {
		t.Fatalf("GetByExternal(m1): %v", err)
	}
	_, err = kr.OpenKind(ctx, viewer, missing.Key, nil)
	if !errors.Is(err, core.ErrNotAvailable) || errors.Is(err, core.ErrPreparing) {
		t.Fatalf("album not on the sources: err = %v, want not available", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["m1"] != 1 {
		t.Fatalf("not-available track resolved %d times, want 1 (no polling on a real answer)", calls["m1"])
	}
}
