package vela

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/liveevents"
	"github.com/braedonsaunders/ether/internal/plugins"
)

type multiViewerIdentityFake struct {
	frontend.SurfaceIdentity
	mu      sync.Mutex
	viewers map[string]*frontend.Viewer
}

func (f *multiViewerIdentityFake) ResolveDevice(_ context.Context, token string, _ frontend.Device) (*frontend.Viewer, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.viewers[token]; ok {
		return v, time.Now().Add(time.Hour), nil
	}
	return nil, time.Time{}, frontend.ErrUnauthorized
}

func (f *multiViewerIdentityFake) Resolve(_ context.Context, token string) (*frontend.Viewer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.viewers[token]; ok {
		return v, nil
	}
	return nil, frontend.ErrUnauthorized
}

func (f *multiViewerIdentityFake) RevokeToken(token string) {
	f.mu.Lock()
	delete(f.viewers, token)
	f.mu.Unlock()
}

func (f *multiViewerIdentityFake) GrantScope(_ context.Context, token string, _ frontend.Device) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.viewers[token]; ok {
		return "scope:" + v.Username, nil
	}
	return "", frontend.ErrUnauthorized
}

func (f *multiViewerIdentityFake) GrantScopeCurrent(_ context.Context, scope string, _ frontend.Device) (*frontend.Viewer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.viewers {
		if "scope:"+v.Username == scope {
			return v, nil
		}
	}
	return nil, frontend.ErrUnauthorized
}

func (f *multiViewerIdentityFake) Revoke(context.Context, string) error { return nil }
func (f *multiViewerIdentityFake) Issue(context.Context, *frontend.Viewer, frontend.Device) (string, error) {
	return "", nil
}

type velaLiveEventsPluginFake struct {
	mu      sync.Mutex
	feeds   []liveevents.FeedInfo
	events  []core.LiveEvent
	details map[string]core.LiveEventDetail
}

func (p *velaLiveEventsPluginFake) ActiveLiveFeeds(context.Context) []liveevents.FeedInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]liveevents.FeedInfo(nil), p.feeds...)
}

func (p *velaLiveEventsPluginFake) SetFeeds(feeds ...liveevents.FeedInfo) {
	p.mu.Lock()
	p.feeds = append([]liveevents.FeedInfo(nil), feeds...)
	p.mu.Unlock()
}

func (p *velaLiveEventsPluginFake) SetEvents(evs ...core.LiveEvent) {
	p.mu.Lock()
	p.events = append([]core.LiveEvent(nil), evs...)
	p.mu.Unlock()
}

func (p *velaLiveEventsPluginFake) Call(_ context.Context, _ string, req plugins.Request) (plugins.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.HasPrefix(req.Path, "/ether/v1/live/scores/events/") {
		id := strings.TrimPrefix(req.Path, "/ether/v1/live/scores/events/")
		if d, ok := p.details[id]; ok {
			b, _ := json.Marshal(d)
			return plugins.Response{Status: 200, Body: b}, nil
		}
		return plugins.Response{Status: 404}, nil
	}
	b, _ := json.Marshal(core.LiveEventsResponse{Events: p.events, NextPollSec: 15})
	return plugins.Response{Status: 200, Body: b}, nil
}

type hubFrontendBridge struct {
	hub *liveevents.Hub
}

func (b hubFrontendBridge) Available(ctx context.Context) bool { return b.hub.HasLiveFeeds(ctx) }
func (b hubFrontendBridge) Subscribe(ctx context.Context, v *frontend.Viewer, req frontend.LiveEventStreamRequest) (frontend.LiveEventStream, error) {
	sub, err := b.hub.Subscribe(ctx, liveevents.SubscribeRequest{Viewer: v, FilterSpoilers: true, Alerts: req.Alerts})
	if err != nil {
		return nil, err
	}
	return &bridgeStream{sub: sub}, nil
}
func (b hubFrontendBridge) SpoilerFree(ctx context.Context, v *frontend.Viewer) (bool, error) {
	return b.hub.SpoilerFree(ctx, v.ID)
}

type bridgeStream struct {
	sub *liveevents.Subscription
}

func (s *bridgeStream) Alerts() <-chan frontend.LiveEventAlert { return s.sub.Alerts() }
func (s *bridgeStream) Close()                                 { s.sub.Close() }

type sseFrame struct {
	Event string
	Data  string
}

func readSSEFrame(t *testing.T, frames <-chan sseFrame) sseFrame {
	t.Helper()
	select {
	case f, ok := <-frames:
		if !ok {
			t.Fatal("SSE stream closed before expected frame")
		}
		return f
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SSE frame")
		return sseFrame{}
	}
}

func startSSEReader(r io.Reader) <-chan sseFrame {
	out := make(chan sseFrame, 32)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(r)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if cur.Event != "" || cur.Data != "" {
					out <- cur
					cur = sseFrame{}
				}
				continue
			}
			if ev, ok := strings.CutPrefix(line, "event: "); ok {
				cur.Event = ev
			} else if data, ok := strings.CutPrefix(line, "data: "); ok {
				cur.Data = data
			}
		}
	}()
	return out
}

type cachedLiveTestExperience struct {
	frontendtest.Experience
}

func (cachedLiveTestExperience) HomeRevision(context.Context, *frontend.Viewer) (frontend.HomeRevision, bool, error) {
	return frontend.HomeRevision{Viewer: "v1", Policy: 1}, true, nil
}

func TestBootstrapViewerSpoilerFreeAndETag(t *testing.T) {
	pluginSrc := &velaLiveEventsPluginFake{
		feeds: []liveevents.FeedInfo{{PluginID: "plug.a", FeedID: "scores", Title: "Scores", MinIntervalSec: 15}},
	}
	hub := liveevents.NewHub(liveevents.Options{Source: pluginSrc})
	defer hub.Close()

	ident := &multiViewerIdentityFake{
		viewers: map[string]*frontend.Viewer{
			"tok-alice": {ID: 1, Username: "alice"},
			"tok-bob":   {ID: 2, Username: "bob"},
		},
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE vela_replays(key TEXT PRIMARY KEY,digest TEXT NOT NULL,response BLOB,expires INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	host := frontend.Host{
		Identity:     ident,
		Experience:   cachedLiveTestExperience{},
		Sessions:     frontendtest.Sessions{},
		Capabilities: frontendtest.Capabilities{},
		LiveEvents:   hubFrontendBridge{hub: hub},
	}
	inst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(context.Background()) })
	h := inst.Handler()

	getBootstrap := func(token, ifNoneMatch string) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Vela-Device-Id", "dev-"+token)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var doc map[string]any
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
				t.Fatalf("decode bootstrap: %v", err)
			}
		}
		return w, doc
	}

	// Spoiler-free is set through the ether.spoilerFree screen action; the
	// bootstrap only mirrors it, so set it on the hub directly.
	setPref := func(token string, enabled bool) {
		t.Helper()
		ids := map[string]int64{"tok-alice": 1, "tok-bob": 2}
		if err := hub.SetSpoilerFree(context.Background(), ids[token], enabled); err != nil {
			t.Fatalf("SetSpoilerFree: %v", err)
		}
	}

	// 1. Default bootstrap carries viewer.spoilerFree: false and a strong ETag.
	w1, doc1 := getBootstrap("tok-alice", "")
	if w1.Code != http.StatusOK {
		t.Fatalf("GET /bootstrap = %d %s", w1.Code, w1.Body.String())
	}
	etag1 := w1.Header().Get("ETag")
	if etag1 == "" {
		t.Fatal("missing bootstrap ETag")
	}
	v1 := doc1["viewer"].(map[string]any)
	if sf, ok := v1["spoilerFree"].(bool); !ok || sf {
		t.Fatalf("default bootstrap viewer.spoilerFree = %#v (ok=%v), want false", v1["spoilerFree"], ok)
	}

	// 2. Unchanged preference returns 304 Not Modified on If-None-Match.
	w304, _ := getBootstrap("tok-alice", etag1)
	if w304.Code != http.StatusNotModified {
		t.Fatalf("unchanged bootstrap with If-None-Match = %d, want 304", w304.Code)
	}

	// 3. Enabling spoilerFree flips bootstrap.viewer.spoilerFree to true and changes ETag (no stale 304).
	setPref("tok-alice", true)
	w2, doc2 := getBootstrap("tok-alice", etag1)
	if w2.Code != http.StatusOK {
		t.Fatalf("bootstrap after enabling spoilerFree = %d, want 200 (ETag must change)", w2.Code)
	}
	etag2 := w2.Header().Get("ETag")
	if etag2 == "" || etag2 == etag1 {
		t.Fatalf("bootstrap ETag did not change after spoilerFree=true: old=%q new=%q", etag1, etag2)
	}
	v2 := doc2["viewer"].(map[string]any)
	if sf, ok := v2["spoilerFree"].(bool); !ok || !sf {
		t.Fatalf("bootstrap viewer.spoilerFree after PUT true = %#v (ok=%v), want true", v2["spoilerFree"], ok)
	}

	// 4. Bob's bootstrap remains false (viewer-scoped).
	wBob, docBob := getBootstrap("tok-bob", "")
	if wBob.Code != http.StatusOK {
		t.Fatalf("Bob GET /bootstrap = %d", wBob.Code)
	}
	if sf, ok := docBob["viewer"].(map[string]any)["spoilerFree"].(bool); !ok || sf {
		t.Fatalf("Bob bootstrap viewer.spoilerFree = %#v, want false", docBob["viewer"])
	}

	// 5. Disabling spoilerFree flips back to false and changes ETag again.
	setPref("tok-alice", false)
	w3, doc3 := getBootstrap("tok-alice", etag2)
	if w3.Code != http.StatusOK {
		t.Fatalf("bootstrap after disabling spoilerFree = %d, want 200", w3.Code)
	}
	if etag3 := w3.Header().Get("ETag"); etag3 == "" || etag3 == etag2 {
		t.Fatalf("bootstrap ETag did not change after spoilerFree=false: old=%q new=%q", etag2, etag3)
	}
	if sf, ok := doc3["viewer"].(map[string]any)["spoilerFree"].(bool); !ok || sf {
		t.Fatalf("bootstrap viewer.spoilerFree after PUT false = %#v, want false", doc3["viewer"])
	}
}
