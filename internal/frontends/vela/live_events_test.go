package vela

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
func (b hubFrontendBridge) Events(ctx context.Context, v *frontend.Viewer, ids []string) ([]frontend.LiveEvent, error) {
	return b.hub.GetEvents(ctx, v, ids)
}
func (b hubFrontendBridge) Detail(ctx context.Context, v *frontend.Viewer, id string) (*frontend.LiveEventDetail, error) {
	d, err := b.hub.GetDetail(ctx, v, id)
	if errors.Is(err, liveevents.ErrNotFound) {
		return nil, frontend.ErrNotFound
	}
	if errors.Is(err, liveevents.ErrInvalidRequest) {
		return nil, frontend.ErrInvalidPreferences
	}
	return d, err
}
func (b hubFrontendBridge) Subscribe(ctx context.Context, v *frontend.Viewer, req frontend.LiveEventStreamRequest) (frontend.LiveEventStream, error) {
	sub, err := b.hub.Subscribe(ctx, liveevents.SubscribeRequest{
		EventIDs:       req.EventIDs,
		Screens:        req.Screens,
		Viewer:         v,
		FilterSpoilers: true,
		Alerts:         req.Alerts,
	})
	if err != nil {
		return nil, err
	}
	init, err := b.hub.SubscriptionSnapshot(ctx, sub)
	if err != nil {
		sub.Close()
		return nil, err
	}
	return &bridgeStream{sub: sub, initial: init}, nil
}
func (b hubFrontendBridge) Reveal(ctx context.Context, v *frontend.Viewer, id string) error {
	_, err := b.hub.RevealEvent(ctx, v, id)
	if errors.Is(err, liveevents.ErrInvalidRequest) {
		return frontend.ErrInvalidPreferences
	}
	return err
}
func (b hubFrontendBridge) SpoilerFree(ctx context.Context, v *frontend.Viewer) (bool, error) {
	return b.hub.SpoilerFree(ctx, v.ID)
}
func (b hubFrontendBridge) SetSpoilerFree(ctx context.Context, v *frontend.Viewer, enabled bool) error {
	return b.hub.SetSpoilerFree(ctx, v.ID, enabled)
}

type bridgeStream struct {
	sub     *liveevents.Subscription
	initial []frontend.LiveEvent
}

func (s *bridgeStream) Initial() []frontend.LiveEvent          { return s.initial }
func (s *bridgeStream) Updates() <-chan []frontend.LiveEvent   { return s.sub.Updates() }
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

func TestVelaLiveEventsEndpointsSpoilerRevealAndSSE(t *testing.T) {
	evLive := core.LiveEvent{
		ID:       "g1",
		Category: "ball",
		League:   core.LiveEventLeague{ID: "top", Name: "Top League", Abbr: "TL"},
		Title:    "Alpha vs Beta",
		Start:    "2026-11-22T18:00:00Z",
		Status:   core.LiveEventStatus{State: core.LiveEventStateIn, Detail: "P4 · 2:14", Period: 4, Clock: "2:14", Close: true},
		Competitors: []core.LiveEventCompetitor{
			{ID: "a", Name: "Alpha", Abbr: "ALP", Score: "24", Home: false},
			{ID: "b", Name: "Beta", Abbr: "BET", Score: "21", Home: true},
		},
		Situation:   map[string]string{"Possession": "ALP"},
		Probability: &core.LiveEventProbability{Label: "Win", Value: 0.68, CompetitorID: "a"},
		UpdatedAt:   "2026-11-22T18:10:00Z",
	}
	evDetail := core.LiveEventDetail{
		LiveEvent: evLive,
		Tables:    []core.LiveEventTable{{Title: "Linescore", Columns: []string{"T"}, Rows: [][]string{{"24"}, {"21"}}}},
		Plays:     []core.LiveEventPlay{{Clock: "2:14", Period: 4, Text: "Score", Emphasis: core.LiveEventPlayEmphasisScore}},
		ProbabilitySeries: []core.LiveEventProbabilityPoint{
			{T: core.ProbFraction(0.5), Value: 0.55},
			{T: core.ProbTimestamp("2026-11-22T18:10:00Z"), Value: 0.68},
		},
	}

	pluginSrc := &velaLiveEventsPluginFake{
		feeds:   []liveevents.FeedInfo{{PluginID: "plug.a", FeedID: "scores", Title: "Scores", MinIntervalSec: 15}},
		events:  []core.LiveEvent{evLive},
		details: map[string]core.LiveEventDetail{"g1": evDetail},
	}
	hub := liveevents.NewHub(liveevents.Options{
		Source: pluginSrc,
		ScreenFeedResolver: func(_ context.Context, _ *core.Viewer, screenID string) ([]liveevents.FeedRef, error) {
			if screenID == "sports" {
				return []liveevents.FeedRef{{PluginID: "plug.a", FeedID: "scores"}}, nil
			}
			return nil, nil
		},
	})
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
		Experience:   frontendtest.Experience{},
		Sessions:     frontendtest.Sessions{},
		Capabilities: frontendtest.Capabilities{},
		LiveEvents:   hubFrontendBridge{hub: hub},
	}
	inst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(context.Background()) })

	heartbeatCh := make(chan time.Time, 4)
	inst.(*instance).liveHeartbeatCh = heartbeatCh
	h := inst.Handler()

	doReq := func(token, method, target string, payload any) *httptest.ResponseRecorder {
		t.Helper()
		var body io.Reader
		if payload != nil {
			b, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(b)
		}
		r := httptest.NewRequest(method, target, body)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("X-Vela-Device-Id", "dev-1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// 1. Capabilities advertises features.liveEvents.available == true when feeds exist, false when none exist.
	w := doReq("tok-alice", http.MethodGet, "/vela/v0/capabilities", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d", w.Code)
	}
	var caps map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	features := caps["features"].(map[string]any)
	liveEvCap, ok := features["liveEvents"].(map[string]any)
	if !ok || liveEvCap["available"] != true {
		t.Fatalf("features.liveEvents = %#v, want available=true", features["liveEvents"])
	}

	pluginSrc.SetFeeds()
	hub.SyncFeeds(context.Background())
	w = doReq("tok-alice", http.MethodGet, "/vela/v0/capabilities", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &caps)
	if caps["features"].(map[string]any)["liveEvents"].(map[string]any)["available"] != false {
		t.Fatalf("expected features.liveEvents.available=false when no active feeds")
	}
	pluginSrc.SetFeeds(liveevents.FeedInfo{PluginID: "plug.a", FeedID: "scores", Title: "Scores", MinIntervalSec: 15})
	hub.SyncFeeds(context.Background())

	// 2. Unauthenticated requests return 401.
	if w := doReq("", http.MethodGet, "/vela/v0/live/events?ids=plug.a:scores:g1", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth /live/events status = %d, want 401", w.Code)
	}

	// 3. GET /vela/v0/live/events?ids= returns snapshot.
	w = doReq("tok-alice", http.MethodGet, "/vela/v0/live/events?ids=plug.a:scores:g1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /live/events status = %d body=%s", w.Code, w.Body.String())
	}
	var snapResp struct {
		Events []core.LiveEvent `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snapResp); err != nil {
		t.Fatal(err)
	}
	if len(snapResp.Events) != 1 || snapResp.Events[0].ID != "plug.a:scores:g1" || snapResp.Events[0].Competitors[0].Score != "24" || snapResp.Events[0].Spoiler {
		t.Fatalf("unexpected snapshot: %+v", snapResp.Events)
	}

	// 4. GET /vela/v0/live/events/{id} preserves numeric and timestamp probabilitySeries[].t.
	w = doReq("tok-alice", http.MethodGet, "/vela/v0/live/events/plug.a:scores:g1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /live/events/{id} status = %d body=%s", w.Code, w.Body.String())
	}
	var rawDetail map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rawDetail); err != nil {
		t.Fatal(err)
	}
	series := rawDetail["probabilitySeries"].([]any)
	if _, ok := series[0].(map[string]any)["t"].(float64); !ok {
		t.Fatalf("expected probabilitySeries[0].t to be JSON number, got %T", series[0].(map[string]any)["t"])
	}
	if _, ok := series[1].(map[string]any)["t"].(string); !ok {
		t.Fatalf("expected probabilitySeries[1].t to be JSON string, got %T", series[1].(map[string]any)["t"])
	}
	if w := doReq("tok-alice", http.MethodGet, "/vela/v0/live/events/plug.a:scores:missing", nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing event detail status = %d, want 404", w.Code)
	}

	// 5. Spoiler-free preference (PUT + PATCH, with or without idempotencyKey) masks scores, probability, plays, tables.
	w = doReq("tok-alice", http.MethodGet, "/vela/v0/live/preferences", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"spoilerFree":false`) {
		t.Fatalf("default prefs = %d %s", w.Code, w.Body.String())
	}
	// PUT without idempotencyKey
	w = doReq("tok-alice", http.MethodPut, "/vela/v0/live/preferences", object{"spoilerFree": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"spoilerFree":true`) {
		t.Fatalf("PUT prefs (no key) = %d %s", w.Code, w.Body.String())
	}
	// PUT with idempotencyKey + replay + conflict
	w = doReq("tok-alice", http.MethodPut, "/vela/v0/live/preferences", object{"idempotencyKey": "alice-sf-1", "spoilerFree": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"spoilerFree":true`) {
		t.Fatalf("PUT prefs (with key) = %d %s", w.Code, w.Body.String())
	}
	w = doReq("tok-alice", http.MethodPut, "/vela/v0/live/preferences", object{"idempotencyKey": "alice-sf-1", "spoilerFree": false})
	if w.Code != http.StatusConflict {
		t.Fatalf("PUT prefs (key conflict) = %d, want 409", w.Code)
	}
	// PATCH without idempotencyKey
	w = doReq("tok-bob", http.MethodPatch, "/vela/v0/live/preferences", object{"spoilerFree": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"spoilerFree":true`) {
		t.Fatalf("PATCH prefs (no key) = %d %s", w.Code, w.Body.String())
	}
	// PATCH with idempotencyKey
	w = doReq("tok-bob", http.MethodPatch, "/vela/v0/live/preferences", object{"idempotencyKey": "bob-sf-1", "spoilerFree": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"spoilerFree":true`) {
		t.Fatalf("PATCH prefs (with key) = %d %s", w.Code, w.Body.String())
	}

	w = doReq("tok-alice", http.MethodGet, "/vela/v0/live/events/plug.a:scores:g1", nil)
	var maskedDetail core.LiveEventDetail
	if err := json.Unmarshal(w.Body.Bytes(), &maskedDetail); err != nil {
		t.Fatal(err)
	}
	if !maskedDetail.Spoiler || maskedDetail.Competitors[0].Score != "" || len(maskedDetail.Plays) != 0 || len(maskedDetail.Tables) != 0 || len(maskedDetail.ProbabilitySeries) != 0 {
		t.Fatalf("expected masked detail for Alice, got %+v", maskedDetail)
	}

	// 6. SSE stream: initial update on connect, alert delivery + spoiler filter, reveal push, heartbeat, and unsubscribe on close.
	srv := httptest.NewServer(h)
	defer srv.Close()

	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	sseReq, err := http.NewRequestWithContext(streamCtx, http.MethodGet, srv.URL+"/vela/v0/live/stream?screens=sports&alerts=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	sseReq.Header.Set("Authorization", "Bearer tok-alice")
	sseReq.Header.Set("X-Vela-Device-Id", "dev-1")
	sseResp, err := srv.Client().Do(sseReq)
	if err != nil {
		t.Fatal(err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK || !strings.HasPrefix(sseResp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE response status=%d ct=%q", sseResp.StatusCode, sseResp.Header.Get("Content-Type"))
	}
	frames := startSSEReader(sseResp.Body)

	// 6a. Initial `event: update` arrives immediately on connect (masked for spoilerFree Alice).
	initFrame := readSSEFrame(t, frames)
	if initFrame.Event != "update" {
		t.Fatalf("first SSE event = %q, want update", initFrame.Event)
	}
	var initPayload struct {
		Events []core.LiveEvent `json:"events"`
	}
	if err := json.Unmarshal([]byte(initFrame.Data), &initPayload); err != nil {
		t.Fatal(err)
	}
	if len(initPayload.Events) != 1 || !initPayload.Events[0].Spoiler || initPayload.Events[0].Competitors[0].Score != "" {
		t.Fatalf("expected masked initial update, got %+v", initPayload.Events)
	}

	// 6b. Alerts: spoiler-free Alice gets `started` alert ONLY (`close` and `final` suppressed).
	hub.PublishAlert(context.Background(), 1, core.LiveEventAlert{
		ID: "al-close", Kind: core.LiveEventAlertClose, EventID: "plug.a:scores:g1", EntityKey: "plug.a:scores:team:a", Title: "Close", Body: "Within 3",
	})
	hub.PublishAlert(context.Background(), 1, core.LiveEventAlert{
		ID: "al-start", Kind: core.LiveEventAlertStarted, EventID: "plug.a:scores:g1", EntityKey: "plug.a:scores:team:a", Title: "Kickoff", Body: "Alpha vs Beta started",
	})
	alertFrame := readSSEFrame(t, frames)
	if alertFrame.Event != "alert" {
		t.Fatalf("expected alert frame, got %+v", alertFrame)
	}
	var gotAlert core.LiveEventAlert
	if err := json.Unmarshal([]byte(alertFrame.Data), &gotAlert); err != nil {
		t.Fatal(err)
	}
	if gotAlert.ID != "al-start" || gotAlert.Kind != core.LiveEventAlertStarted || gotAlert.EventID != "plug.a:scores:g1" {
		t.Fatalf("unexpected alert payload: %+v", gotAlert)
	}

	// 6c. POST /vela/v0/live/events/{id}/reveal returns 204 (both with empty body and with idempotencyKey) and pushes unmasked update!
	wReveal := doReq("tok-alice", http.MethodPost, "/vela/v0/live/events/plug.a:scores:g1/reveal", nil)
	if wReveal.Code != http.StatusNoContent {
		t.Fatalf("POST reveal (empty body) status = %d, want 204", wReveal.Code)
	}
	wRevealIdem := doReq("tok-alice", http.MethodPost, "/vela/v0/live/events/plug.a:scores:g1/reveal", object{"idempotencyKey": "rev-1"})
	if wRevealIdem.Code != http.StatusNoContent {
		t.Fatalf("POST reveal (idempotencyKey) status = %d, want 204", wRevealIdem.Code)
	}
	wRevealReplay := doReq("tok-alice", http.MethodPost, "/vela/v0/live/events/plug.a:scores:g1/reveal", object{"idempotencyKey": "rev-1"})
	if wRevealReplay.Code != http.StatusNoContent {
		t.Fatalf("POST reveal (replay) status = %d, want 204", wRevealReplay.Code)
	}

	revFrame := readSSEFrame(t, frames)
	if revFrame.Event != "update" {
		t.Fatalf("expected update after reveal, got %+v", revFrame)
	}
	var revPayload struct {
		Events []core.LiveEvent `json:"events"`
	}
	_ = json.Unmarshal([]byte(revFrame.Data), &revPayload)
	if len(revPayload.Events) != 1 || revPayload.Events[0].Spoiler || revPayload.Events[0].Competitors[0].Score != "24" {
		t.Fatalf("expected unmasked event on stream after reveal, got %+v", revPayload.Events)
	}
	if revPayload.Events[0].UpdatedAt <= initPayload.Events[0].UpdatedAt {
		t.Fatalf("reveal update did not advance updatedAt: init=%q rev=%q", initPayload.Events[0].UpdatedAt, revPayload.Events[0].UpdatedAt)
	}

	// Bob is still spoiler-masked!
	wBob := doReq("tok-bob", http.MethodGet, "/vela/v0/live/events/plug.a:scores:g1", nil)
	var bobDetail core.LiveEventDetail
	_ = json.Unmarshal(wBob.Body.Bytes(), &bobDetail)
	if !bobDetail.Spoiler || bobDetail.Competitors[0].Score != "" {
		t.Fatalf("Bob leaked Alice's reveal: %+v", bobDetail)
	}

	// 6d. Heartbeat emits `event: heartbeat` with `{}`.
	heartbeatCh <- time.Now()
	hbFrame := readSSEFrame(t, frames)
	if hbFrame.Event != "heartbeat" || hbFrame.Data != "{}" {
		t.Fatalf("unexpected heartbeat frame: %+v", hbFrame)
	}

	// 6e. Revoking token closes stream cleanly on next heartbeat check.
	ident.RevokeToken("tok-alice")
	heartbeatCh <- time.Now()
	select {
	case _, ok := <-frames:
		if ok {
			t.Fatal("expected SSE stream to close after token revocation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SSE stream did not close after token revocation")
	}

	// 7. Closing a client SSE connection unsubscribes cleanly from the hub.
	ctx2, cancel2 := context.WithCancel(context.Background())
	req2, err := http.NewRequestWithContext(ctx2, http.MethodGet, srv.URL+"/vela/v0/live/stream?events=plug.a:scores:g1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Authorization", "Bearer tok-bob")
	req2.Header.Set("X-Vela-Device-Id", "dev-2")
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	frames2 := startSSEReader(resp2.Body)
	f2 := readSSEFrame(t, frames2)
	if f2.Event != "update" {
		t.Fatalf("second stream initial event = %q, want update", f2.Event)
	}
	cancel2()
	_ = resp2.Body.Close()
	// Drain until reader goroutine finishes.
	for range frames2 {
	}
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

	setPref := func(token string, enabled bool) {
		t.Helper()
		b, _ := json.Marshal(object{"spoilerFree": enabled})
		req := httptest.NewRequest(http.MethodPut, "/vela/v0/live/preferences", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Vela-Device-Id", "dev-"+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT /live/preferences = %d %s", w.Code, w.Body.String())
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
