package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakePluginLiveCaller struct {
	liveCalls    int
	resolveCalls int
	liveStatus   int
	liveBody     string
	resolveMap   map[string]plugins.Response
}

func (f *fakePluginLiveCaller) Call(_ context.Context, _ string, req plugins.Request) (plugins.Response, error) {
	if strings.HasSuffix(req.Path, "/live") {
		f.liveCalls++
		status := f.liveStatus
		if status == 0 {
			status = http.StatusOK
		}
		return plugins.Response{Status: status, Body: json.RawMessage(f.liveBody)}, nil
	}
	if strings.HasSuffix(req.Path, "/live/resolve") {
		f.resolveCalls++
		var in plugins.KindLiveResolveRequest
		_ = json.Unmarshal(req.Body, &in)
		if resp, ok := f.resolveMap[in.ID]; ok {
			return resp, nil
		}
		return plugins.Response{Status: http.StatusNotFound}, nil
	}
	return plugins.Response{Status: http.StatusNotFound}, nil
}

type fakeURLImages struct {
	data map[string][]byte
}

func (f fakeURLImages) GetUntrustedURL(_ context.Context, rawURL string) ([]byte, string, error) {
	b, ok := f.data[rawURL]
	if !ok {
		return nil, "", core.ErrNotFound
	}
	return b, "image/jpeg", nil
}

func TestPluginLiveServiceCacheThumbsResolveAndConsole(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()

	lib, err := a.libraries.UpsertPluginLibrary(ctx, "esports", "matches", libraries.PluginLibraryDecl{
		Name: "Esports",
		Present: core.Presentations{
			Vela: &core.VelaPresentation{Type: "movies"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.libraries.SetTabs(ctx, lib.ID, []core.LibraryTab{
		{Key: "live", Label: "Live", Kind: core.TabLive, Card: core.CardLandscape},
		{Key: "library", Label: "VODs", Kind: core.TabLibrary},
	}, nil); err != nil {
		t.Fatal(err)
	}
	lib, err = a.libraries.Get(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	caller := &fakePluginLiveCaller{
		liveBody: `{"items":[{"id":"m1","title":"Grand Finals","subtitle":"Arena","badge":"1080p","viewers":900,"tags":["EN"],"thumb":"https://thumbs.example.invalid/live/m1.jpg"}],"filters":[{"key":"all","label":"All"}],"nextCursor":"c2"}`,
		resolveMap: map[string]plugins.Response{
			"m1":  {Status: 200, Body: json.RawMessage(`{"url":"https://edge.example.invalid/live/m1.m3u8?token=secret","headers":{"Referer":"https://esports.example.invalid","Bad\r\nKey":"drop"},"title":"Grand Finals"}`)},
			"bad": {Status: 200, Body: json.RawMessage(`{"url":"file:///etc/passwd"}`)},
		},
	}
	svc := NewPluginLiveService(a)
	svc.caller = caller
	svc.now = func() time.Time { return now }
	a.pluginLive = svc
	a.opts.urlImages = fakeURLImages{data: map[string][]byte{"https://thumbs.example.invalid/live/m1.jpg": []byte("JPEGDATA")}}

	// 1. List caches within 45s TTL and registers opaque library-scoped thumb ref.
	page1, err := svc.List(ctx, lib, frontend.LibraryLiveRequest{Tab: "live", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != 1 || !strings.HasPrefix(page1.Items[0].ThumbRef, frontend.LibraryLiveThumbPrefix) || strings.Contains(page1.Items[0].ThumbRef, "thumbs.example.invalid") {
		t.Fatalf("thumb must be opaque library-live ref, got %+v", page1.Items)
	}
	if !page1.Items[0].Live || page1.NextCursor != "c2" || caller.liveCalls != 1 {
		t.Fatalf("unexpected page1: %+v calls=%d", page1, caller.liveCalls)
	}

	// Second call within TTL hits cache.
	if _, err := svc.List(ctx, lib, frontend.LibraryLiveRequest{Tab: "live", Limit: 20}); err != nil {
		t.Fatal(err)
	}
	if caller.liveCalls != 1 {
		t.Fatalf("expected cache hit within TTL, got %d calls", caller.liveCalls)
	}

	// Advance clock past TTL -> refreshes.
	now = now.Add(pluginLiveCacheTTL + time.Second)
	if _, err := svc.List(ctx, lib, frontend.LibraryLiveRequest{Tab: "live", Limit: 20}); err != nil {
		t.Fatal(err)
	}
	if caller.liveCalls != 2 {
		t.Fatalf("expected cache refresh after TTL, got %d calls", caller.liveCalls)
	}

	// Cross-library thumb resolution must fail.
	if _, err := svc.ResolveThumb(lib.ID+1, page1.Items[0].ThumbRef); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-library thumb resolve err = %v, want ErrNotFound", err)
	}
	resolvedThumb, err := svc.ResolveThumb(lib.ID, page1.Items[0].ThumbRef)
	if err != nil || resolvedThumb != "https://thumbs.example.invalid/live/m1.jpg" {
		t.Fatalf("thumb resolve = %q, err=%v", resolvedThumb, err)
	}

	// 2. Resolve is NEVER cached, rejects non-http(s) URLs, sanitizes headers, maps 404 to ErrNotFound.
	s1, err := svc.Resolve(ctx, lib, "m1")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := svc.Resolve(ctx, lib, "m1")
	if err != nil || caller.resolveCalls != 2 || s1.URL != s2.URL {
		t.Fatalf("resolve must not be cached: calls=%d err=%v", caller.resolveCalls, err)
	}
	if len(s1.Headers) != 1 || s1.Headers["Referer"] != "https://esports.example.invalid" {
		t.Fatalf("headers not sanitized: %+v", s1.Headers)
	}
	if _, err := svc.Resolve(ctx, lib, "bad"); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("non-http stream URL err = %v, want ErrNotAvailable", err)
	}
	if _, err := svc.Resolve(ctx, lib, "offline"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing item err = %v, want ErrNotFound", err)
	}

	// 3. AllowViewer checks active plugin, viewer access, protocol publication, and visible live tab.
	owner := &core.Viewer{ID: 0, IsOwner: true}
	// With pluginsRT present and plugin not installed/active, AllowViewer rejects.
	if a.pluginsRT != nil && a.pluginsRT.kindCatalog != nil {
		if _, _, err := svc.AllowViewer(core.WithLiveApp(ctx, "vela"), owner, lib.ID, ""); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("inactive plugin must fail: %v", err)
		}
		a.pluginsRT = nil
	}
	if _, tab, err := svc.AllowViewer(core.WithLiveApp(ctx, "vela"), owner, lib.ID, ""); err != nil || tab != "live" {
		t.Fatalf("AllowViewer vela failed: tab=%q err=%v", tab, err)
	}
	if _, _, err := svc.AllowViewer(core.WithLiveApp(ctx, "plex"), owner, lib.ID, ""); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unpublished protocol must fail: %v", err)
	}
	restricted := &core.Viewer{ID: 0, AllowedLibraries: []core.LibraryID{core.LibraryMovies}}
	if _, _, err := svc.AllowViewer(core.WithLiveApp(ctx, "vela"), restricted, lib.ID, ""); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("disallowed library must fail: %v", err)
	}

	// 4. Console live list & thumb proxy endpoints work and never expose raw thumb URLs.
	c := &librariesConsole{app: a}
	h := c.handler()
	idStr := strconv.FormatInt(int64(lib.ID), 10)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+idStr+"/live?tab=live", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("console GET /live status = %d body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "thumbs.example.invalid") {
		t.Fatalf("console /live leaked raw upstream thumb URL: %s", w.Body.String())
	}
	var dto consoleLivePageDTO
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Items) != 1 || !strings.HasPrefix(dto.Items[0].Thumb, "/ether/api/libraries/"+idStr+"/live/thumb/") {
		t.Fatalf("unexpected console live dto: %+v", dto)
	}
	thumbPath := strings.TrimPrefix(dto.Items[0].Thumb, "/ether/api/libraries")
	wThumb := httptest.NewRecorder()
	h.ServeHTTP(wThumb, httptest.NewRequest(http.MethodGet, thumbPath, nil))
	if wThumb.Code != http.StatusOK || wThumb.Body.String() != "JPEGDATA" {
		t.Fatalf("thumb proxy status=%d body=%q", wThumb.Code, wThumb.Body.String())
	}

	// 5. Console tab preview accepts kind="live".
	wPrev := httptest.NewRecorder()
	h.ServeHTTP(wPrev, httptest.NewRequest(http.MethodPost, "/"+idStr+"/tabs/preview", strings.NewReader(`{"tab":{"key":"live","label":"Live","kind":"live","card":"landscape"}}`)))
	if wPrev.Code != http.StatusOK || !strings.Contains(wPrev.Body.String(), `"kind":"live"`) {
		t.Fatalf("tab preview live status=%d body=%s", wPrev.Code, wPrev.Body.String())
	}
}

type rotatingPluginLiveCaller struct {
	mu      sync.Mutex
	calls   int
	answers []plugins.Response
}

func (r *rotatingPluginLiveCaller) Call(_ context.Context, _ string, req plugins.Request) (plugins.Response, error) {
	if !strings.HasSuffix(req.Path, "/live/resolve") {
		return plugins.Response{Status: http.StatusNotFound}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.calls
	r.calls++
	if idx < len(r.answers) {
		return r.answers[idx], nil
	}
	return plugins.Response{Status: http.StatusNotFound}, nil
}

func TestPluginLiveDirectStreamReResolvePlumbing(t *testing.T) {
	a := setupTestApp(t, true)
	a.pluginsRT = nil
	ctx := context.Background()

	lib, err := a.libraries.UpsertPluginLibrary(ctx, "directory", "live", libraries.PluginLibraryDecl{
		Name: "Live Directory",
		Present: core.Presentations{
			Vela: &core.VelaPresentation{Type: "movies"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.libraries.SetTabs(ctx, lib.ID, []core.LibraryTab{
		{Key: "live", Label: "Live", Kind: core.TabLive, Card: core.CardLandscape},
	}, nil); err != nil {
		t.Fatal(err)
	}

	// Valid minimal MPEG-TS packet payload for variant segments.
	tsSeg := make([]byte, 3*188)
	for i := 0; i < 3; i++ {
		tsSeg[i*188] = 0x47
		tsSeg[i*188+1] = 0x40
	}

	var expiredHits, freshHits, consoleExpiredHits, consoleFreshHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/expired/master.m3u8":
			expiredHits.Add(1)
			http.Error(w, "expired single-use url", http.StatusForbidden)
		case "/fresh/master.m3u8":
			if r.Header.Get("X-Stream-Auth") != "fresh-tok" {
				http.Error(w, "missing header", http.StatusForbidden)
				return
			}
			if freshHits.Add(1) > 1 {
				http.Error(w, "fresh token already spent", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-STREAM-INF:BANDWIDTH=2500000,RESOLUTION=1280x720,CODECS=\"avc1.64001f,mp4a.40.2\"\nvariant.m3u8\n")
		case "/fresh/variant.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:2.0,\nseg1.ts\n")
		case "/fresh/seg1.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			_, _ = w.Write(tsSeg)
		case "/console-expired/master.m3u8":
			consoleExpiredHits.Add(1)
			http.Error(w, "console expired", http.StatusGone)
		case "/console-fresh/master.m3u8":
			if consoleFreshHits.Add(1) > 1 {
				http.Error(w, "console token spent", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-STREAM-INF:BANDWIDTH=2500000,RESOLUTION=1280x720,CODECS=\"avc1.64001f,mp4a.40.2\"\nvariant.m3u8\n")
		case "/console-fresh/variant.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:2.0,\nseg1.ts\n")
		case "/console-fresh/seg1.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			_, _ = w.Write(tsSeg)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	caller := &rotatingPluginLiveCaller{
		answers: []plugins.Response{
			// 1. Re-resolve invoked by sessionLive.TuneDirect after /expired/master.m3u8 returns 403.
			{Status: http.StatusOK, Body: json.RawMessage(`{"url":"` + upstream.URL + `/fresh/master.m3u8","headers":{"X-Stream-Auth":"fresh-tok"},"title":"Live Room"}`)},
			// 2. Initial resolve invoked by console handleLivePlay (returns an expired 410 URL).
			{Status: http.StatusOK, Body: json.RawMessage(`{"url":"` + upstream.URL + `/console-expired/master.m3u8","title":"Console Room"}`)},
			// 3. Re-resolve invoked by TuneDirectStream's Resolve hook from handleLivePlay.
			{Status: http.StatusOK, Body: json.RawMessage(`{"url":"` + upstream.URL + `/console-fresh/master.m3u8","title":"Console Room"}`)},
		},
	}
	pLive := NewPluginLiveService(a)
	pLive.caller = caller
	a.pluginLive = pLive

	ltDB := store.OpenTest(t)
	ltSvc, err := livetv.New(livetv.Options{DB: ltDB, HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer ltSvc.Close()
	a.livetv = ltSvc

	owner := &core.Viewer{ID: 0, IsOwner: true, StreamLimit: 4}
	adapter := sessionLive{service: ltSvc, app: a}
	sid, _, err := adapter.TuneDirect(core.WithLiveApp(ctx, "vela"), owner, &frontend.DirectLiveTarget{
		LibraryID: lib.ID,
		Tab:       "live",
		ItemID:    "room-1",
		Title:     "Live Room",
		URL:       upstream.URL + "/expired/master.m3u8",
	}, core.ClientCaps{
		Product:       "Vela",
		Protocol:      "vela",
		ClientID:      "vela-app-test",
		HLSContainers: []string{"mpegts"},
	}, "vela-app-sess")
	if err != nil {
		t.Fatalf("sessionLive.TuneDirect failed to recover via /live/resolve: %v", err)
	}
	defer func() { _ = ltSvc.Stop(ctx, sid, owner) }()

	if expiredHits.Load() != 1 || freshHits.Load() != 1 {
		t.Fatalf("expected expiredHits=1 freshHits=1, got expired=%d fresh=%d", expiredHits.Load(), freshHits.Load())
	}

	// Verify librariesConsole.handleLivePlay also plumbs /live/resolve.
	c := &librariesConsole{app: a}
	h := c.handler()
	idStr := strconv.FormatInt(int64(lib.ID), 10)
	req := httptest.NewRequest(http.MethodPost, "/"+idStr+"/live/play", strings.NewReader(`{"itemId":"room-1","tab":"live"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("console handleLivePlay status=%d body=%s", rec.Code, rec.Body.String())
	}
	if consoleExpiredHits.Load() != 1 || consoleFreshHits.Load() != 1 {
		t.Fatalf("expected consoleExpiredHits=1 consoleFreshHits=1, got expired=%d fresh=%d", consoleExpiredHits.Load(), consoleFreshHits.Load())
	}
	caller.mu.Lock()
	gotCalls := caller.calls
	caller.mu.Unlock()
	if gotCalls != 3 {
		t.Fatalf("expected 3 total /live/resolve calls, got %d", gotCalls)
	}
}
