package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakeLiveEventLinker struct {
	mu      sync.Mutex
	progKey string
	ref     *frontend.LiveEventRef
}

func (f *fakeLiveEventLinker) LiveEventForChannel(_ context.Context, _ *frontend.Viewer, _ string) (string, *frontend.LiveEventRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.progKey, cloneLiveEventRef(f.ref), nil
}

func (f *fakeLiveEventLinker) Set(progKey string, ref *frontend.LiveEventRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.progKey = progKey
	f.ref = cloneLiveEventRef(ref)
}

func TestVelaLiveSessionSnapshotAndSSELiveEventOnProgrammeChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	linker := &fakeLiveEventLinker{
		progKey: "ch-1|1000|2000|Game One",
		ref:     &frontend.LiveEventRef{ID: "demo:main:e1", Feed: "main"},
	}

	s := &playSession{
		id:               "live-sess",
		channelID:        "ch-1",
		liveProgrammeKey: "ch-1|1000|2000|Game One",
		liveEvent:        &frontend.LiveEventRef{ID: "demo:main:e1", Feed: "main"},
		scope:            "fake-family",
		viewer:           1,
		device:           "device",
		cursor:           1,
		revision:         1,
		signal:           make(chan struct{}),
		plan: object{
			"sessionId": "live-sess",
			"channelId": "ch-1",
			"liveEvent": object{"id": "demo:main:e1", "feed": "main"},
		},
	}

	i := &instance{
		ctx:             ctx,
		eventsHeartbeat: 50 * time.Millisecond,
		disconnectGrace: 200 * time.Millisecond,
		host: frontend.Host{
			Identity:      identityFake{},
			Sessions:      frontendtest.Sessions{},
			LiveEventLink: linker,
		},
		sessions: map[string]*playSession{"live-sess": s},
	}
	defer i.Close(ctx)

	// 1. GET /vela/v0/sessions/{id} snapshot includes liveEvent: {id, feed}.
	snapReq := httptest.NewRequest("GET", "/vela/v0/sessions/live-sess", nil).WithContext(ctx)
	snapReq.SetPathValue("id", "live-sess")
	snapReq.Header.Set("Authorization", "Bearer token")
	snapReq.Header.Set("X-Vela-Device-Id", "device")
	snapW := httptest.NewRecorder()
	i.snapshot(snapW, snapReq)
	if snapW.Code != 200 {
		t.Fatalf("snapshot status = %d, body = %s", snapW.Code, snapW.Body.String())
	}
	var snapDoc map[string]any
	if err := json.Unmarshal(snapW.Body.Bytes(), &snapDoc); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	le, _ := snapDoc["liveEvent"].(map[string]any)
	if le == nil || le["id"] != "demo:main:e1" || le["feed"] != "main" {
		t.Fatalf("snapshot liveEvent = %#v, want {id:demo:main:e1, feed:main}", snapDoc["liveEvent"])
	}

	// 2. VOD session snapshot omits liveEvent completely.
	vod := &playSession{
		id:       "vod-sess",
		titleID:  "movie-1",
		scope:    "fake-family",
		viewer:   1,
		device:   "device",
		cursor:   1,
		revision: 1,
		signal:   make(chan struct{}),
		plan:     object{"sessionId": "vod-sess", "titleId": "movie-1"},
	}
	i.sessions["vod-sess"] = vod
	vodReq := httptest.NewRequest("GET", "/vela/v0/sessions/vod-sess", nil).WithContext(ctx)
	vodReq.SetPathValue("id", "vod-sess")
	vodReq.Header.Set("Authorization", "Bearer token")
	vodReq.Header.Set("X-Vela-Device-Id", "device")
	vodW := httptest.NewRecorder()
	i.snapshot(vodW, vodReq)
	var vodDoc map[string]any
	if err := json.Unmarshal(vodW.Body.Bytes(), &vodDoc); err != nil {
		t.Fatalf("unmarshal vod snapshot: %v", err)
	}
	if _, exists := vodDoc["liveEvent"]; exists {
		t.Fatalf("VOD snapshot unexpectedly carried liveEvent: %#v", vodDoc["liveEvent"])
	}

	// 3. Programme changes on air -> refresh emits `event: liveEvent` on SSE:
	//    - first rollover: Game One -> Game Two ({id:demo:main:e2, feed:main})
	//    - second rollover: Game Two -> Evening News (null)
	//    - duplicate refresh with no change: emits nothing
	viewer := &frontend.Viewer{ID: 1}
	linker.Set("ch-1|1000|2000|Game One", &frontend.LiveEventRef{ID: "demo:main:e1", Feed: "main"})
	i.refreshSessionLiveEvent(ctx, viewer, s) // unchanged -> no event

	linker.Set("ch-1|2000|3000|Game Two", &frontend.LiveEventRef{ID: "demo:main:e2", Feed: "main"})
	i.refreshSessionLiveEvent(ctx, viewer, s)

	linker.Set("ch-1|3000|4000|Evening News", nil)
	i.refreshSessionLiveEvent(ctx, viewer, s)

	// Mark ended so SSE flushes buffered events since cursor=1 and exits cleanly.
	s.mu.Lock()
	s.ended = time.Now()
	s.publish("ended")
	s.mu.Unlock()

	sseReq := httptest.NewRequest("GET", "/vela/v0/sessions/live-sess/events?cursor=1", nil).WithContext(ctx)
	sseReq.SetPathValue("id", "live-sess")
	sseReq.Header.Set("Authorization", "Bearer token")
	sseReq.Header.Set("X-Vela-Device-Id", "device")
	sseW := httptest.NewRecorder()
	i.events(sseW, sseReq)

	body := sseW.Body.String()
	wantEvent2 := "event: liveEvent\ndata: {\"liveEvent\":{\"feed\":\"main\",\"id\":\"demo:main:e2\"}}\n\n"
	wantNull := "event: liveEvent\ndata: {\"liveEvent\":null}\n\n"
	if !strings.Contains(body, wantEvent2) {
		t.Fatalf("SSE stream missing e2 liveEvent frame;\nwant substring:\n%s\ngot:\n%s", wantEvent2, body)
	}
	if !strings.Contains(body, wantNull) {
		t.Fatalf("SSE stream missing null liveEvent frame;\nwant substring:\n%s\ngot:\n%s", wantNull, body)
	}
	if count := strings.Count(body, "event: liveEvent\n"); count != 2 {
		t.Fatalf("expected exactly 2 liveEvent SSE frames (unchanged refresh skipped), got %d:\n%s", count, body)
	}

	// Snapshot after rollover carries `"liveEvent": null`.
	snapAfter := s.snapshot()
	val, hasKey := snapAfter["liveEvent"]
	if !hasKey || val != nil {
		t.Fatalf("snapshot after rollover liveEvent = (%#v, %v), want (nil, true)", val, hasKey)
	}
}

func TestVelaLivePlaybackPopulatesInitialLiveEventAndWatchesChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	linker := &fakeLiveEventLinker{
		progKey: "ch-sports|1000|2000|Live Match",
		ref:     &frontend.LiveEventRef{ID: "demo:main:m42", Feed: "main"},
	}

	sessions := frontendtest.Sessions{
		PlanFunc: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
			return frontend.PlayPlan{
				ID:           "plan-sports-1",
				HLS:          true,
				HLSContainer: "mpegts",
			}, nil
		},
		OpenFunc: func(_ context.Context, _ frontend.OpenRequest) (frontend.Session, error) {
			return frontend.Session{
				ID:        "sess-sports-1",
				MediaPath: "/sessions/media/sess-sports-1/grant/live/index.m3u8",
				Channel:   "ch-sports",
			}, nil
		},
		StartStreamFunc: func(_ context.Context, _ frontend.SessionAccess, id string, _ frontend.StreamRequest) (string, error) {
			return "/sessions/media/" + id + "/grant/live/index.m3u8", nil
		},
	}

	db := store.OpenTest(t)
	mounted, err := (Frontend{AssetsDir: t.TempDir(), DB: db, ReplayKey: make([]byte, 32)}).Mount(ctx, frontend.Host{
		Identity:   identityFake{},
		Experience: frontendtest.Experience{},
		Sessions:   sessions,
		LiveTV:     &liveWindowFake{available: true},
		Capabilities: frontendtest.Capabilities{
			BuildFunc: func(_ context.Context, _ frontend.CapabilityRequest) (frontend.CapabilityResult, error) {
				return frontend.CapabilityResult{}, nil
			},
		},
		LiveEventLink: linker,
	})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	i := mounted.(*instance)
	i.liveEventInterval = 10 * time.Millisecond
	defer i.Close(ctx)

	playBody := `{"idempotencyKey":"play-sports-1","channelId":"ch-sports","client":{"device":{"id":"dev-1","name":"TV","platform":"web","appVersion":"0.3.19"},"hls":true}}`
	req := httptest.NewRequest(http.MethodPost, "/vela/v0/playback", strings.NewReader(playBody)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Vela-Device-Id", "dev-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	i.serve(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /vela/v0/playback status = %d, body = %s", w.Code, w.Body.String())
	}
	var playDoc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &playDoc); err != nil {
		t.Fatalf("unmarshal playback response: %v", err)
	}
	le, _ := playDoc["liveEvent"].(map[string]any)
	if le == nil || le["id"] != "demo:main:m42" || le["feed"] != "main" {
		t.Fatalf("playback plan liveEvent = %#v, want {id:demo:main:m42, feed:main}", playDoc["liveEvent"])
	}

	// Verify GET /vela/v0/sessions/{id} snapshot carries the same liveEvent.
	snapReq := httptest.NewRequest(http.MethodGet, "/vela/v0/sessions/sess-sports-1", nil).WithContext(ctx)
	snapReq.Header.Set("Authorization", "Bearer token")
	snapReq.Header.Set("X-Vela-Device-Id", "dev-1")
	snapW := httptest.NewRecorder()
	i.serve(snapW, snapReq)
	if snapW.Code != http.StatusOK {
		t.Fatalf("GET /vela/v0/sessions/sess-sports-1 status = %d, body = %s", snapW.Code, snapW.Body.String())
	}
	var snapDoc map[string]any
	if err := json.Unmarshal(snapW.Body.Bytes(), &snapDoc); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	snapLE, _ := snapDoc["liveEvent"].(map[string]any)
	if snapLE == nil || snapLE["id"] != "demo:main:m42" || snapLE["feed"] != "main" {
		t.Fatalf("snapshot liveEvent = %#v, want {id:demo:main:m42, feed:main}", snapDoc["liveEvent"])
	}

	// Simulate programme change while watchSession is active; wait for session signal.
	i.mu.Lock()
	ps := i.sessions["sess-sports-1"]
	i.mu.Unlock()
	if ps == nil {
		t.Fatal("session sess-sports-1 missing")
	}
	ps.mu.Lock()
	sig := ps.signal
	ps.mu.Unlock()

	linker.Set("ch-sports|2000|3000|Postgame News", nil)
	select {
	case <-sig:
	case <-ctx.Done():
		t.Fatal("timed out waiting for watchSession to emit liveEvent on programme change")
	}

	ps.mu.Lock()
	gotAfter := ps.liveEvent
	ps.mu.Unlock()
	if gotAfter != nil {
		t.Fatalf("after watchSession rollover liveEvent = %+v, want nil", gotAfter)
	}
}
