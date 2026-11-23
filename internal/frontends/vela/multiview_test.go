package vela

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/sessions"
)

type mvLiveGuideFake struct {
	frontendtest.LiveTV
}

func (mvLiveGuideFake) Channels(context.Context, *frontend.Viewer) ([]frontend.LiveChannel, error) {
	return nil, nil
}
func (mvLiveGuideFake) Guide(context.Context, *frontend.Viewer, frontend.LiveGuideQuery) ([]frontend.LiveProgramme, error) {
	return nil, nil
}
func (mvLiveGuideFake) GuidePage(context.Context, *frontend.Viewer, frontend.LiveGuidePageQuery) (frontend.LiveGuidePage, error) {
	return frontend.LiveGuidePage{}, nil
}

type mvBrokerLiveFake struct {
	mu      sync.Mutex
	seq     int
	active  map[string]core.ClientCaps
	stopped []string
	capErr  func(channel string, caps core.ClientCaps) error
}

func newMVBrokerLiveFake() *mvBrokerLiveFake {
	return &mvBrokerLiveFake{active: map[string]core.ClientCaps{}}
}

func (*mvBrokerLiveFake) Allowed(_ context.Context, _ *core.Viewer, ch string) error {
	if ch == "" || ch == "missing" {
		return core.ErrNotFound
	}
	return nil
}

func (l *mvBrokerLiveFake) Tune(ctx context.Context, v *core.Viewer, ch string, caps core.ClientCaps, cs string) (string, error) {
	id, _, err := l.TuneMedia(ctx, v, ch, caps, cs)
	return id, err
}

func (l *mvBrokerLiveFake) TuneMedia(_ context.Context, _ *core.Viewer, ch string, caps core.ClientCaps, _ string) (string, *core.MediaInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.capErr != nil {
		if err := l.capErr(ch, caps); err != nil {
			return "", nil, err
		}
	}
	l.seq++
	id := fmt.Sprintf("live-%d", l.seq)
	l.active[id] = caps
	h := 1080
	if caps.MaxHeight > 0 && caps.MaxHeight < h {
		h = caps.MaxHeight
	}
	return id, &core.MediaInfo{
		Container: "mpegts",
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: h * 16 / 9, Height: h},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000, Default: true},
		},
	}, nil
}

func (*mvBrokerLiveFake) Serve(w http.ResponseWriter, _ *http.Request, _ *core.Viewer, _, _, _ string) {
	w.WriteHeader(http.StatusOK)
}

func (l *mvBrokerLiveFake) Stop(_ context.Context, _ *core.Viewer, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.active, id)
	l.stopped = append(l.stopped, id)
	return nil
}

func (l *mvBrokerLiveFake) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.active)
}

type mvBrokerPlaybackFake struct {
	core.Playback
	mu       sync.Mutex
	sessions map[string]*core.PlaySession
}

func (p *mvBrokerPlaybackFake) Prepare(_ context.Context, u *core.User, key core.RatingKey, caps core.ClientCaps) (*core.PlaySession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessions == nil {
		p.sessions = map[string]*core.PlaySession{}
	}
	id := fmt.Sprintf("vod-%d-%d", u.ID, key)
	ps := &core.PlaySession{
		ID: id, UserID: u.ID, Key: key, ClientID: caps.ClientID,
		Info: core.MediaInfo{
			Container: "mp4",
			Duration:  time.Hour,
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
				{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000, Default: true},
			},
		},
	}
	p.sessions[id] = ps
	return ps, nil
}
func (p *mvBrokerPlaybackFake) Session(id string) (*core.PlaySession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[id]
	return s, ok
}
func (p *mvBrokerPlaybackFake) SessionFor(userID int64, clientID string, key core.RatingKey) (*core.PlaySession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.sessions {
		if s.UserID == userID && s.ClientID == clientID && s.Key == key {
			return s, true
		}
	}
	return nil, false
}
func (p *mvBrokerPlaybackFake) Sessions() []*core.PlaySession {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*core.PlaySession, 0, len(p.sessions))
	for _, s := range p.sessions {
		out = append(out, s)
	}
	return out
}
func (p *mvBrokerPlaybackFake) Open(string) (core.MediaSource, error) {
	return nil, core.ErrNotAvailable
}
func (p *mvBrokerPlaybackFake) Stop(id string) { p.mu.Lock(); delete(p.sessions, id); p.mu.Unlock() }
func (p *mvBrokerPlaybackFake) Estimate(context.Context, *core.Title, core.ClientCaps) core.MediaInfo {
	return core.MediaInfo{}
}
func (p *mvBrokerPlaybackFake) MarkSurfaceOpened(string) bool { return true }
func (p *mvBrokerPlaybackFake) SessionForSurface(string, int64, string, core.RatingKey, string, bool) (*core.PlaySession, bool) {
	return nil, false
}

type mvBrokerProgressFake struct{}

func (mvBrokerProgressFake) Progress(context.Context, int64, core.RatingKey, time.Duration, time.Duration, string) error {
	return nil
}

type mvMultiIdentityFake struct{ frontend.SurfaceIdentity }

func (mvMultiIdentityFake) ResolveDevice(_ context.Context, token string, _ frontend.Device) (*frontend.Viewer, time.Time, error) {
	if token == "tok-2" {
		return &frontend.Viewer{ID: 2, Username: "bob"}, time.Now().Add(time.Hour), nil
	}
	return &frontend.Viewer{ID: 1, Username: "alice"}, time.Now().Add(time.Hour), nil
}
func (mvMultiIdentityFake) GrantScope(_ context.Context, token string, d frontend.Device) (string, error) {
	return token + ":" + d.ID, nil
}
func (mvMultiIdentityFake) GrantScopeCurrent(_ context.Context, scope string, _ frontend.Device) (*frontend.Viewer, error) {
	if len(scope) >= 5 && scope[:5] == "tok-2" {
		return &frontend.Viewer{ID: 2, Username: "bob"}, nil
	}
	return &frontend.Viewer{ID: 1, Username: "alice"}, nil
}

func newVelaMultiviewHarness(t *testing.T, live *mvBrokerLiveFake) (http.Handler, *sessions.Broker) {
	t.Helper()
	ctx := context.Background()
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := sessions.NewEventBus(nil, active)
	t.Cleanup(events.Close)
	broker, err := sessions.NewBroker(sessions.BrokerDeps{
		Live:         live,
		Events:       events,
		Playback:     &mvBrokerPlaybackFake{},
		Progress:     mvBrokerProgressFake{},
		ActiveViewer: active,
		CanPlay:      func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit: func(*core.Viewer) int { return 0 },
		OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k, Type: core.TypeMovie, Title: "Movie"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { broker.Close(ctx) })

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
		Identity:     mvMultiIdentityFake{},
		Experience:   frontendtest.Experience{},
		Sessions:     sessions.BindSurface(broker, "vela"),
		LiveTV:       mvLiveGuideFake{},
		Capabilities: frontendtest.Capabilities{},
	}
	inst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(ctx) })
	return inst.Handler(), broker
}

func TestVelaMultiviewCapabilitiesEndpointsPlaybackReplanAndCapError(t *testing.T) {
	live := newMVBrokerLiveFake()
	h, broker := newVelaMultiviewHarness(t, live)

	do := func(token, devID, method, path string, payload any) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		var body *bytes.Reader
		if payload != nil {
			b, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(b)
		} else {
			body = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, path, body)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Vela-Device-Id", devID)
		r.Header.Set("X-Vela-Version", "0.2.0")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var doc map[string]any
		if w.Body.Len() > 0 {
			_ = json.Unmarshal(w.Body.Bytes(), &doc)
		}
		return w, doc
	}

	// 1. Capabilities advertises features.multiview: {available: true, maxTiles: 4}.
	w, capsDoc := do("tok-1", "tv-1", http.MethodGet, "/vela/v0/capabilities", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d", w.Code)
	}
	mvCap, ok := capsDoc["features"].(map[string]any)["multiview"].(map[string]any)
	if !ok || mvCap["available"] != true || mvCap["maxTiles"] != float64(4) {
		t.Fatalf("features.multiview = %#v, want {available: true, maxTiles: 4}", mvCap)
	}

	// 2. POST /vela/v0/multiview creates a group (both with object device + idempotencyKey and without idempotencyKey).
	w, groupDoc := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/multiview", map[string]any{
		"idempotencyKey": "mv-create-1",
		"device":         map[string]any{"id": "tv-1", "name": "Apple TV", "platform": "tvOS", "appVersion": "0.2.0"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create multiview status = %d body=%s", w.Code, w.Body.String())
	}
	groupID, _ := groupDoc["groupId"].(string)
	if groupID == "" || groupDoc["maxTiles"] != float64(4) {
		t.Fatalf("create multiview body = %#v", groupDoc)
	}
	// Idempotency replay returns the exact same groupId.
	wReplay, replayDoc := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/multiview", map[string]any{
		"idempotencyKey": "mv-create-1",
		"device":         map[string]any{"id": "tv-1", "name": "Apple TV", "platform": "tvOS", "appVersion": "0.2.0"},
	})
	if wReplay.Code != http.StatusOK || replayDoc["groupId"] != groupID {
		t.Fatalf("idempotency replay = %d %#v", wReplay.Code, replayDoc)
	}

	// Mismatched device is rejected.
	wBadDev, _ := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/multiview", map[string]any{
		"device": map[string]any{"id": "other-dev"},
	})
	if wBadDev.Code != http.StatusBadRequest {
		t.Fatalf("mismatched device status = %d, want 400", wBadDev.Code)
	}

	// 3. Open 4 live tiles in the group; tile 0 passes explicit maxHeight=540 (rounds up to 720),
	// tiles 1-3 omit maxHeight (group-size rule: 2 tiles -> 1080, 3-4 tiles -> 720).
	clientDoc := map[string]any{
		"device":    map[string]any{"id": "tv-1", "name": "Apple TV", "platform": "tvOS", "appVersion": "0.2.0"},
		"hls":       true,
		"maxHeight": 2160,
	}
	var sessIDs [4]string
	var firstURLs [4]string
	for tile := 0; tile < 4; tile++ {
		mvReq := map[string]any{"groupId": groupID, "tile": tile}
		if tile == 0 {
			mvReq["maxHeight"] = 540
		}
		w, playDoc := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/playback", map[string]any{
			"idempotencyKey": fmt.Sprintf("tile-%d", tile),
			"channelId":      fmt.Sprintf("ch-%d", tile),
			"client":         clientDoc,
			"multiview":      mvReq,
		})
		if w.Code != http.StatusOK {
			t.Fatalf("open tile %d status = %d body=%s", tile, w.Code, w.Body.String())
		}
		sessIDs[tile], _ = playDoc["sessionId"].(string)
		paths := playDoc["paths"].([]any)
		firstURLs[tile], _ = paths[0].(map[string]any)["url"].(string)
		mvOut := playDoc["multiview"].(map[string]any)
		wantH := float64(720)
		if tile == 1 {
			wantH = 1080
		}
		if mvOut["maxHeight"] != wantH {
			t.Fatalf("tile %d maxHeight = %v, want %v", tile, mvOut["maxHeight"], wantH)
		}
	}
	if live.count() != 4 {
		t.Fatalf("expected 4 surviving live tiles, got %d", live.count())
	}

	// 4. Focused-tile upgrade replans tile 3 with maxHeight=1080 via session command.
	w, replanDoc := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/sessions/"+sessIDs[3]+"/commands", map[string]any{
		"idempotencyKey":   "upgrade-tile-3",
		"kind":             "replan",
		"maxHeight":        1080,
		"sourceGeneration": 1,
		"seekGeneration":   1,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("replan tile 3 status = %d body=%s", w.Code, w.Body.String())
	}
	if replanDoc["sourceGeneration"] != float64(2) {
		t.Fatalf("replan sourceGeneration = %v, want 2", replanDoc["sourceGeneration"])
	}
	newURL := replanDoc["paths"].([]any)[0].(map[string]any)["url"].(string)
	if newURL == firstURLs[3] {
		t.Fatalf("replan did not rotate stream URL: %q", newURL)
	}
	if replanDoc["multiview"].(map[string]any)["maxHeight"] != float64(1080) {
		t.Fatalf("replanned multiview maxHeight = %#v, want 1080", replanDoc["multiview"])
	}
	if live.count() != 4 {
		t.Fatalf("replan affected sibling tiles: active=%d, want 4", live.count())
	}

	// 5. Provider connection cap returns HTTP 409 with exact binding error wire shape.
	live.mu.Lock()
	live.capErr = func(ch string, _ core.ClientCaps) error {
		if ch == "ch-capped" {
			return &core.ProviderConnectionCapError{Provider: "Acme IPTV", Max: 2, InUse: 2, Cause: core.ErrStreamLimit}
		}
		return nil
	}
	live.mu.Unlock()
	wCap, capDoc := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/playback", map[string]any{
		"idempotencyKey": "tile-cap",
		"channelId":      "ch-capped",
		"client":         clientDoc,
		"multiview":      map[string]any{"groupId": groupID, "tile": 2},
	})
	if wCap.Code != http.StatusConflict {
		t.Fatalf("provider cap status = %d, want 409; body=%s", wCap.Code, wCap.Body.String())
	}
	errObj := capDoc["error"].(map[string]any)
	details := errObj["details"].(map[string]any)
	retry := errObj["retry"].(map[string]any)
	if errObj["class"] != "capacity" || errObj["code"] != "provider_connection_cap" || errObj["message"] != "Acme IPTV allows 2 connections" ||
		details["provider"] != "Acme IPTV" || details["max"] != float64(2) || details["inUse"] != float64(2) ||
		retry["allowed"] != false {
		t.Fatalf("unexpected provider cap error shape: %#v", capDoc)
	}
	live.mu.Lock()
	live.capErr = nil
	live.mu.Unlock()

	// 6. DELETE /vela/v0/multiview/{groupId} with empty body returns 204 and ends every tile.
	wDel, _ := do("tok-1", "tv-1", http.MethodDelete, "/vela/v0/multiview/"+groupID, nil)
	if wDel.Code != http.StatusNoContent {
		t.Fatalf("DELETE multiview status = %d, want 204; body=%s", wDel.Code, wDel.Body.String())
	}
	if live.count() != 0 {
		t.Fatalf("expected 0 live tiles after DELETE, got %d", live.count())
	}
	list, _ := broker.Sessions(context.Background(), frontend.SessionAccess{Viewer: &core.Viewer{ID: 1}, Device: core.Device{ID: "tv-1"}, Protocol: "vela"})
	if len(list) != 0 {
		t.Fatalf("expected 0 broker sessions after DELETE, got %d", len(list))
	}

	// 7. Non-group play supersedes a fresh multiview group on the same device.
	w2, g2Doc := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/multiview", map[string]any{"device": "tv-1"})
	if w2.Code != http.StatusOK {
		t.Fatalf("create second group (no idempotencyKey, string device) = %d %s", w2.Code, w2.Body.String())
	}
	g2ID := g2Doc["groupId"].(string)
	for tile := 0; tile < 2; tile++ {
		w, _ := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/playback", map[string]any{
			"idempotencyKey": fmt.Sprintf("g2-tile-%d", tile),
			"channelId":      fmt.Sprintf("ch-%d", tile),
			"client":         clientDoc,
			"multiview":      map[string]any{"groupId": g2ID, "tile": tile},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("g2 tile %d status = %d %s", tile, w.Code, w.Body.String())
		}
	}
	if live.count() != 2 {
		t.Fatalf("expected 2 active tiles in g2, got %d", live.count())
	}
	wSolo, _ := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/playback", map[string]any{
		"idempotencyKey": "solo-play",
		"channelId":      "ch-solo",
		"client":         clientDoc,
	})
	if wSolo.Code != http.StatusOK {
		t.Fatalf("solo play status = %d %s", wSolo.Code, wSolo.Body.String())
	}
	if live.count() != 1 {
		t.Fatalf("solo play did not supersede g2: active=%d, want 1", live.count())
	}

	// 8. DELETE with idempotencyKey also returns 204 and replays 204.
	wDel2, _ := do("tok-1", "tv-1", http.MethodDelete, "/vela/v0/multiview/"+g2ID, map[string]any{"idempotencyKey": "del-g2"})
	if wDel2.Code != http.StatusNoContent {
		t.Fatalf("DELETE with idempotencyKey status = %d, want 204", wDel2.Code)
	}
	wDel2Replay, _ := do("tok-1", "tv-1", http.MethodDelete, "/vela/v0/multiview/"+g2ID, map[string]any{"idempotencyKey": "del-g2"})
	if wDel2Replay.Code != http.StatusNoContent {
		t.Fatalf("DELETE idempotencyKey replay status = %d, want 204", wDel2Replay.Code)
	}

	// 9. Validation errors: out-of-range tile, non-live target, cross-viewer/device access.
	wBadTile, _ := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/playback", map[string]any{
		"idempotencyKey": "bad-tile",
		"channelId":      "ch-1",
		"client":         clientDoc,
		"multiview":      map[string]any{"groupId": g2ID, "tile": 4},
	})
	if wBadTile.Code != http.StatusBadRequest {
		t.Fatalf("tile=4 status = %d, want 400", wBadTile.Code)
	}
	wVOD, _ := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/playback", map[string]any{
		"idempotencyKey": "vod-mv",
		"titleId":        "101",
		"client":         clientDoc,
		"multiview":      map[string]any{"groupId": g2ID, "tile": 0},
	})
	if wVOD.Code != http.StatusBadRequest {
		t.Fatalf("VOD with multiview status = %d, want 400", wVOD.Code)
	}
	w3, g3Doc := do("tok-1", "tv-1", http.MethodPost, "/vela/v0/multiview", map[string]any{"device": "tv-1"})
	if w3.Code != http.StatusOK {
		t.Fatalf("create g3 status = %d", w3.Code)
	}
	g3ID := g3Doc["groupId"].(string)
	wOtherUser, _ := do("tok-2", "tv-1", http.MethodDelete, "/vela/v0/multiview/"+g3ID, nil)
	if wOtherUser.Code != http.StatusUnauthorized {
		t.Fatalf("other viewer DELETE status = %d, want 401", wOtherUser.Code)
	}
	wOtherDev, _ := do("tok-1", "tv-2", http.MethodDelete, "/vela/v0/multiview/"+g3ID, nil)
	if wOtherDev.Code != http.StatusUnauthorized {
		t.Fatalf("other device DELETE status = %d, want 401", wOtherDev.Code)
	}
	wUnknown, _ := do("tok-1", "tv-1", http.MethodDelete, "/vela/v0/multiview/mv_missing", nil)
	if wUnknown.Code != http.StatusNotFound {
		t.Fatalf("unknown group DELETE status = %d, want 404", wUnknown.Code)
	}
}

func TestVelaMultiviewUnavailableWithoutLiveTV(t *testing.T) {
	ctx := context.Background()
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
		Identity:     mvMultiIdentityFake{},
		Experience:   frontendtest.Experience{},
		Sessions:     frontendtest.Sessions{},
		Capabilities: frontendtest.Capabilities{},
	}
	inst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(ctx) })

	r := httptest.NewRequest(http.MethodGet, "/vela/v0/capabilities", nil)
	r.Header.Set("Authorization", "Bearer tok-1")
	r.Header.Set("X-Vela-Device-Id", "tv-1")
	r.Header.Set("X-Vela-Version", "0.2.0")
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d", w.Code)
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	mvCap := doc["features"].(map[string]any)["multiview"].(map[string]any)
	if mvCap["available"] != false || mvCap["maxTiles"] != float64(4) {
		t.Fatalf("features.multiview without liveTV = %#v, want {available: false, maxTiles: 4}", mvCap)
	}
}

func TestVelaWebClientCapabilitiesRequestFMP4NotTS(t *testing.T) {
	web := client{
		Device:     device{ID: "web-1", Platform: "web"},
		Containers: []string{"mp4", "webm"},
		HLS:        false,
		MSE:        true,
	}
	if velaSupportsTS(web) {
		t.Fatal("Vela web declaring mp4/webm must not claim TS support")
	}
	safari := client{
		Device:     device{ID: "web-2", Platform: "web"},
		Containers: []string{"mp4", "webm"},
		HLS:        true,
		MSE:        true,
	}
	if velaSupportsTS(safari) {
		t.Fatal("Vela web Safari declaring mp4/webm without ts must use fMP4")
	}
	appleTV := client{
		Device:     device{ID: "tv-1", Platform: "tvOS"},
		Containers: []string{"mp4", "mov"},
		HLS:        true,
	}
	if !velaSupportsTS(appleTV) {
		t.Fatal("Apple TV tvOS with native HLS must keep TS support")
	}
	explicitTS := client{
		Device:     device{ID: "web-ts", Platform: "web"},
		Containers: []string{"mp4", "mpegts"},
		MSE:        true,
	}
	if !velaSupportsTS(explicitTS) {
		t.Fatal("client explicitly declaring mpegts must keep TS support")
	}
}

func TestVelaMultiviewUpgradeInPlaceHandoverAndCapacityErrors(t *testing.T) {
	live := newMVBrokerLiveFake()
	h, _ := newVelaMultiviewHarness(t, live)

	do := func(method, path string, payload any) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		var body *bytes.Reader
		if payload != nil {
			b, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(b)
		} else {
			body = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, path, body)
		r.Header.Set("Authorization", "Bearer tok-1")
		r.Header.Set("X-Vela-Device-Id", "web-1")
		r.Header.Set("X-Vela-Version", "0.2.0")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var doc map[string]any
		if w.Body.Len() > 0 {
			_ = json.Unmarshal(w.Body.Bytes(), &doc)
		}
		return w, doc
	}

	webClient := map[string]any{
		"device":      map[string]any{"id": "web-1", "name": "Chrome", "platform": "web", "appVersion": "0.2.0"},
		"containers":  []string{"mp4", "webm"},
		"videoCodecs": []string{"h264"},
		"audioCodecs": []string{"aac"},
		"hls":         false,
		"mse":         true,
		"maxHeight":   1080,
	}

	w, groupDoc := do(http.MethodPost, "/vela/v0/multiview", map[string]any{"device": "web-1"})
	if w.Code != http.StatusOK {
		t.Fatalf("POST /multiview = %d %s", w.Code, w.Body.String())
	}
	groupID := groupDoc["groupId"].(string)

	// 1. Open tile 0 at 480p -> session A ready, and client caps received fmp4 HLS container!
	w, sessADoc := do(http.MethodPost, "/vela/v0/playback", map[string]any{
		"idempotencyKey": "tile-0-480",
		"channelId":      "showcase-chroma-80",
		"client":         webClient,
		"multiview":      map[string]any{"groupId": groupID, "tile": 0, "maxHeight": 480},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("open tile 0 = %d %s", w.Code, w.Body.String())
	}
	sessAID := sessADoc["sessionId"].(string)
	sessAURL := sessADoc["paths"].([]any)[0].(map[string]any)["url"].(string)

	live.mu.Lock()
	var seenCaps core.ClientCaps
	for _, caps := range live.active {
		seenCaps = caps
	}
	live.mu.Unlock()
	if len(seenCaps.HLSContainers) != 1 || seenCaps.HLSContainers[0] != "fmp4" {
		t.Fatalf("web tile HLSContainers = %v, want [fmp4]", seenCaps.HLSContainers)
	}

	// 2. Upgrade in place using binding spec shorthand {kind: "replan", maxHeight: 1080}
	// (no idempotencyKey / generations required on replan).
	w, replanDoc := do(http.MethodPost, "/vela/v0/sessions/"+sessAID+"/commands", map[string]any{
		"kind":      "replan",
		"maxHeight": 1080,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("replan 480->1080 = %d %s", w.Code, w.Body.String())
	}
	if replanDoc["sessionId"] != sessAID {
		t.Fatalf("replan changed sessionId: got %v, want %q", replanDoc["sessionId"], sessAID)
	}
	if replanDoc["sourceGeneration"] != float64(2) {
		t.Fatalf("replan sourceGeneration = %v, want 2", replanDoc["sourceGeneration"])
	}
	upgradedURL := replanDoc["paths"].([]any)[0].(map[string]any)["url"].(string)
	if upgradedURL == sessAURL {
		t.Fatalf("replan 480->1080 did not rotate URL")
	}

	// 3. Replan again when source cannot go higher (already at 1080p -> ask 2160p):
	// returns 200 with plan COMPLETELY unchanged (same revision, sourceGeneration, URL).
	revBefore := replanDoc["revision"]
	w, unchangedDoc := do(http.MethodPost, "/vela/v0/sessions/"+sessAID+"/commands", map[string]any{
		"kind":      "replan",
		"maxHeight": 2160,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("replan unchanged = %d %s", w.Code, w.Body.String())
	}
	if unchangedDoc["revision"] != revBefore ||
		unchangedDoc["sourceGeneration"] != float64(2) ||
		unchangedDoc["paths"].([]any)[0].(map[string]any)["url"] != upgradedURL {
		t.Fatalf("replan when source cannot go higher mutated plan: before rev=%v url=%q, got %#v", revBefore, upgradedURL, unchangedDoc)
	}

	// Stale generation when explicitly sent is still rejected with 409 stale_generation.
	wStale, staleDoc := do(http.MethodPost, "/vela/v0/sessions/"+sessAID+"/commands", map[string]any{
		"idempotencyKey":   "stale-replan",
		"kind":             "replan",
		"maxHeight":        480,
		"sourceGeneration": 1,
		"seekGeneration":   1,
	})
	if wStale.Code != http.StatusConflict || staleDoc["error"].(map[string]any)["code"] != "stale_generation" {
		t.Fatalf("stale replan = %d %#v, want 409 stale_generation", wStale.Code, staleDoc)
	}

	// 4. Capacity failure on handover POST /playback returns typed playback_capacity (NEVER "cancelled"),
	// and keeps session A alive and ready!
	live.mu.Lock()
	live.capErr = func(_ string, _ core.ClientCaps) error {
		return frontend.ErrTooManyTranscodes
	}
	live.mu.Unlock()
	wCap, capDoc := do(http.MethodPost, "/vela/v0/playback", map[string]any{
		"idempotencyKey": "handover-cap-fail",
		"channelId":      "showcase-chroma-80",
		"client":         webClient,
		"multiview":      map[string]any{"groupId": groupID, "tile": 0, "maxHeight": 1080},
	})
	if wCap.Code != http.StatusServiceUnavailable {
		t.Fatalf("capacity error status = %d, want 503; body=%s", wCap.Code, wCap.Body.String())
	}
	errObj := capDoc["error"].(map[string]any)
	if errObj["class"] != "capacity" || errObj["code"] != "playback_capacity" {
		t.Fatalf("capacity error = %#v, want class=capacity code=playback_capacity (never cancelled)", errObj)
	}
	sessionState := func(doc map[string]any) any {
		if plan, ok := doc["plan"].(map[string]any); ok {
			return plan["state"]
		}
		return doc["state"]
	}

	// Session A must STILL be ready (not ended)!
	wA, getADoc := do(http.MethodGet, "/vela/v0/sessions/"+sessAID, nil)
	if wA.Code != http.StatusOK || sessionState(getADoc) != "ready" {
		t.Fatalf("GET /sessions/A after failed handover = %d state=%v, want 200 ready; body=%s", wA.Code, sessionState(getADoc), wA.Body.String())
	}

	// 5. Successful handover POST /playback keeps session A ready WHILE session B tunes,
	// and ends session A only AFTER session B is ready!
	inTune := make(chan struct{})
	releaseTune := make(chan struct{})
	live.mu.Lock()
	live.capErr = func(_ string, _ core.ClientCaps) error {
		close(inTune)
		<-releaseTune
		return nil
	}
	live.mu.Unlock()

	type resp struct {
		w   *httptest.ResponseRecorder
		doc map[string]any
	}
	handoverDone := make(chan resp, 1)
	go func() {
		wb, docb := do(http.MethodPost, "/vela/v0/playback", map[string]any{
			"idempotencyKey": "handover-ok",
			"channelId":      "showcase-chroma-80",
			"client":         webClient,
			"multiview":      map[string]any{"groupId": groupID, "tile": 0, "maxHeight": 1080},
		})
		handoverDone <- resp{wb, docb}
	}()

	<-inTune
	// While session B is tuning, session A MUST still be ready!
	wDuring, duringDoc := do(http.MethodGet, "/vela/v0/sessions/"+sessAID, nil)
	if wDuring.Code != http.StatusOK || sessionState(duringDoc) != "ready" {
		close(releaseTune)
		t.Fatalf("GET /sessions/A DURING handover tune = %d state=%v, want 200 ready; body=%s", wDuring.Code, sessionState(duringDoc), wDuring.Body.String())
	}
	close(releaseTune)

	resB := <-handoverDone
	if resB.w.Code != http.StatusOK || resB.doc["state"] != "ready" {
		t.Fatalf("handover POST /playback = %d %s", resB.w.Code, resB.w.Body.String())
	}
	if resB.doc["sessionId"] == sessAID {
		t.Fatal("handover POST /playback should create a new session B")
	}
	if live.count() != 1 {
		t.Fatalf("expected 1 active live backend after handover commit, got %d", live.count())
	}
}

func TestVelaMultiviewDeadIPTVChannelMatchesSingleChannelErrorAndOmitsFocused(t *testing.T) {
	live := newMVBrokerLiveFake()
	h, _ := newVelaMultiviewHarness(t, live)

	webClient := map[string]any{
		"device":           map[string]any{"id": "web-1", "name": "Vela browser", "platform": "web", "appVersion": "0.2.45"},
		"protocolVersions": []int{0},
		"containers":       []string{"mp4"},
		"videoCodecs":      []string{"h264"},
		"audioCodecs":      []string{"aac"},
		"hls":              true,
		"maxHeight":        1080,
	}
	do := func(method, path string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
		var rbody *bytes.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rbody = bytes.NewReader(raw)
		} else {
			rbody = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rbody)
		req.Header.Set("Authorization", "Bearer tok-1")
		req.Header.Set("X-Vela-Device-Id", "web-1")
		req.Header.Set("X-Vela-Version", "0.2.45")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var doc map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &doc)
		return w, doc
	}

	w, gDoc := do(http.MethodPost, "/vela/v0/multiview", map[string]any{"device": map[string]any{"id": "web-1"}})
	if w.Code != http.StatusOK {
		t.Fatalf("create multiview = %d %s", w.Code, w.Body.String())
	}
	groupID := gDoc["groupId"].(string)

	// Open tile 0 on a healthy channel and verify plan.multiview omits "focused".
	w, tile0 := do(http.MethodPost, "/vela/v0/playback", map[string]any{
		"idempotencyKey": "tile0-ok",
		"channelId":      "showcase-chroma-80",
		"client":         webClient,
		"multiview":      map[string]any{"groupId": groupID, "tile": 0, "maxHeight": 480},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("tile 0 = %d %s", w.Code, w.Body.String())
	}
	sess0ID := tile0["sessionId"].(string)
	if _, hasFocused := tile0["multiview"].(map[string]any)["focused"]; hasFocused {
		t.Fatalf("initial plan.multiview unexpectedly included focused: %#v", tile0["multiview"])
	}

	// Replan tile 0 to 1080p and verify plan.multiview still omits "focused".
	w, replanDoc := do(http.MethodPost, "/vela/v0/sessions/"+sess0ID+"/commands", map[string]any{
		"kind":      "replan",
		"maxHeight": 1080,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("replan tile 0 = %d %s", w.Code, w.Body.String())
	}
	if _, hasFocused := replanDoc["multiview"].(map[string]any)["focused"]; hasFocused {
		t.Fatalf("replanned plan.multiview unexpectedly included focused: %#v", replanDoc["multiview"])
	}

	// Switch tile 0 to dead IPTV channels whose Tune errors wrap ErrNotFound,
	// ErrUnauthorized, or a network error: multiview MUST return the exact same
	// typed 503 source error as single-channel playback (never 404 not_found).
	cases := []struct {
		name string
		err  error
	}{
		{name: "wrapped_not_found", err: fmt.Errorf("provider returned 404: %w", core.ErrNotFound)},
		{name: "wrapped_unauthorized", err: fmt.Errorf("provider rejected auth: %w", core.ErrUnauthorized)},
		{name: "network_unavailable", err: core.ErrNotAvailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live.mu.Lock()
			live.capErr = func(ch string, _ core.ClientCaps) error {
				if ch == "iptv-dead" {
					return tc.err
				}
				return nil
			}
			live.mu.Unlock()

			wSingle, singleDoc := do(http.MethodPost, "/vela/v0/playback", map[string]any{
				"idempotencyKey": "single-" + tc.name,
				"channelId":      "iptv-dead",
				"client":         webClient,
			})
			wTile, tileDoc := do(http.MethodPost, "/vela/v0/playback", map[string]any{
				"idempotencyKey": "tile-" + tc.name,
				"channelId":      "iptv-dead",
				"client":         webClient,
				"multiview":      map[string]any{"groupId": groupID, "tile": 0, "maxHeight": 480},
			})
			if wSingle.Code != http.StatusServiceUnavailable {
				t.Fatalf("single-channel status = %d, want 503; body=%s", wSingle.Code, wSingle.Body.String())
			}
			if wTile.Code != wSingle.Code {
				t.Fatalf("multiview dead-channel status = %d, want %d (matching single-channel); body=%s", wTile.Code, wSingle.Code, wTile.Body.String())
			}
			singleErr := singleDoc["error"].(map[string]any)
			tileErr := tileDoc["error"].(map[string]any)
			if tileErr["class"] != "source" || tileErr["code"] != singleErr["code"] || tileErr["message"] != singleErr["message"] {
				t.Fatalf("multiview error %#v != single-channel error %#v", tileErr, singleErr)
			}
		})
	}
	live.mu.Lock()
	live.capErr = nil
	live.mu.Unlock()

	// Healthy tile 0 must still be alive after the failed dead-channel switches.
	wGet, getDoc := do(http.MethodGet, "/vela/v0/sessions/"+sess0ID, nil)
	if wGet.Code != http.StatusOK || getDoc["plan"].(map[string]any)["state"] != "ready" {
		t.Fatalf("tile 0 did not survive failed dead-channel switch: status=%d body=%s", wGet.Code, wGet.Body.String())
	}

	// An actually unknown/expired multiview group STILL returns 404 not_found.
	wMissing, missingDoc := do(http.MethodPost, "/vela/v0/playback", map[string]any{
		"idempotencyKey": "missing-group",
		"channelId":      "showcase-chroma-80",
		"client":         webClient,
		"multiview":      map[string]any{"groupId": "mv_does_not_exist", "tile": 0, "maxHeight": 480},
	})
	if wMissing.Code != http.StatusNotFound || missingDoc["error"].(map[string]any)["code"] != "not_found" {
		t.Fatalf("missing multiview group = %d %#v, want 404 not_found", wMissing.Code, missingDoc)
	}
}
