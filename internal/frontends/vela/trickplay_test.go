package vela

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/sessions"
	"github.com/braedonsaunders/ether/internal/transcode"
	"github.com/braedonsaunders/ether/internal/trickplay"
	_ "modernc.org/sqlite"
)

type tpFakeRunner struct {
	frame []byte
	calls atomic.Int32
}

func (r *tpFakeRunner) Run(_ context.Context, cmd transcode.Command) error {
	r.calls.Add(1)
	_, err := cmd.Stdout.Write(r.frame)
	return err
}

func (r *tpFakeRunner) Start(transcode.Command) (transcode.Process, error) {
	return nil, core.ErrNotAvailable
}

type tpBrokerPlaybackFake struct {
	core.Playback
	mu        sync.Mutex
	sessions  map[string]*core.PlaySession
	durations map[core.RatingKey]time.Duration
	releases  map[core.RatingKey]string
}

func (p *tpBrokerPlaybackFake) Prepare(_ context.Context, u *core.User, key core.RatingKey, caps core.ClientCaps) (*core.PlaySession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessions == nil {
		p.sessions = map[string]*core.PlaySession{}
	}
	rel := p.releases[key]
	if rel == "" {
		rel = fmt.Sprintf("rel-%d", key)
	}
	id := fmt.Sprintf("vod-%d-%d-%s", u.ID, key, rel)
	ps := &core.PlaySession{
		ID:         id,
		UserID:     u.ID,
		Key:        key,
		ClientID:   caps.ClientID,
		Resolution: &core.Resolution{Release: core.Release{ID: rel}},
		Info: core.MediaInfo{
			Container: "mp4",
			Duration:  p.durations[key],
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
				{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000, Default: true},
			},
		},
	}
	p.sessions[id] = ps
	return ps, nil
}

func (p *tpBrokerPlaybackFake) Session(id string) (*core.PlaySession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[id]
	return s, ok
}

func (p *tpBrokerPlaybackFake) InputURL(id string) string { return "http://127.0.0.1/input/" + id }
func (p *tpBrokerPlaybackFake) Stop(id string) {
	p.mu.Lock()
	delete(p.sessions, id)
	p.mu.Unlock()
}
func (p *tpBrokerPlaybackFake) MarkSurfaceOpened(string) bool   { return true }
func (p *tpBrokerPlaybackFake) MarkSurfacePrepared(string) bool { return true }
func (p *tpBrokerPlaybackFake) PrepareSurface(ctx context.Context, u *core.User, key core.RatingKey, caps core.ClientCaps, _ string, _ bool) (*core.PlaySession, error) {
	return p.Prepare(ctx, u, key, caps)
}
func (p *tpBrokerPlaybackFake) SessionForSurface(string, int64, string, core.RatingKey, string, bool) (*core.PlaySession, bool) {
	return nil, false
}

func solidJPEG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestVelaTrickplayPlanAndSheetRoute(t *testing.T) {
	ctx := context.Background()
	const thumbW, thumbH = 32, 18
	fallbackJPG := solidJPEG(t, thumbW, thumbH, color.RGBA{R: 120, G: 30, B: 30, A: 255})
	realJPG := solidJPEG(t, thumbW, thumbH, color.RGBA{R: 30, G: 140, B: 60, A: 255})

	runner := &tpFakeRunner{frame: realJPG}
	thumbs := transcode.NewThumbnails(transcode.ThumbOptions{
		Runner:     runner,
		FFmpeg:     "ffmpeg",
		Interval:   10 * time.Second,
		Width:      thumbW,
		BatchPause: time.Millisecond,
	})
	owner := trickplay.New(thumbs)
	t.Cleanup(func() { _ = owner.Close() })

	keyCold := core.MovieKey(101)
	keyUnknownDur := core.MovieKey(102)
	keyReady := core.MovieKey(103)

	pb := &tpBrokerPlaybackFake{
		durations: map[core.RatingKey]time.Duration{
			keyCold:       25 * time.Second,
			keyUnknownDur: 0,
			keyReady:      25 * time.Second,
		},
		releases: map[core.RatingKey]string{
			keyCold:       "rel-cold",
			keyUnknownDur: "rel-nodur",
			keyReady:      "rel-ready",
		},
	}

	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := sessions.NewEventBus(nil, active)
	t.Cleanup(events.Close)
	broker, err := sessions.NewBroker(sessions.BrokerDeps{
		Trickplay:    owner.Borrow(),
		Events:       events,
		Playback:     pb,
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
		Capabilities: frontendtest.Capabilities{},
	}
	inst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(ctx) })
	velaInst := inst.(*instance)
	velaInst.trickplayPollInterval = 10 * time.Millisecond
	handler := inst.Handler()

	var idemSeq int
	callJSON := func(token, devID, method, path string, payload object) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		var body *bytes.Reader
		if payload != nil {
			if method == http.MethodPost {
				idemSeq++
				payload["idempotencyKey"] = fmt.Sprintf("idem-%d", idemSeq)
			}
			b, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(b)
		} else {
			body = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, body)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if devID != "" {
			req.Header.Set("X-Vela-Device-Id", devID)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		var out map[string]any
		if len(w.Body.Bytes()) > 0 {
			_ = json.Unmarshal(w.Body.Bytes(), &out)
		}
		return w, out
	}

	clientPayload := func(devID string) object {
		return object{
			"device":           object{"id": devID},
			"protocolVersions": []int{1},
			"containers":       []string{"mp4"},
			"videoCodecs":      []string{"h264"},
			"audioCodecs":      []string{"aac"},
			"hls":              true,
			"mse":              true,
		}
	}

	// 1. Cold title: no eager generation on plan; plan.trickplay omitted.
	w, coldPlan := callJSON("tok-1", "dev-cold", "POST", "/vela/v0/playback", object{
		"titleId": ids.RatingKeyGUID(keyCold),
		"client":  clientPayload("dev-cold"),
	})
	if w.Code != 200 {
		t.Fatalf("cold playback status=%d body=%s", w.Code, w.Body.String())
	}
	if _, exists := coldPlan["trickplay"]; exists {
		t.Fatalf("expected plan.trickplay omitted before thumbnails exist, got %v", coldPlan["trickplay"])
	}
	if got := runner.calls.Load(); got != 0 {
		t.Fatalf("plan request started thumbnail generation: %d ffmpeg calls", got)
	}

	// 2. Unknown duration: omitted even if fallback exists.
	noDurKey := core.TrickplayCacheKey(&core.PlaySession{Key: keyUnknownDur, Resolution: &core.Resolution{Release: core.Release{ID: "rel-nodur"}}})
	thumbs.SetTrickplayFallback(noDurKey, fallbackJPG)
	w, noDurPlan := callJSON("tok-1", "dev-nodur", "POST", "/vela/v0/playback", object{
		"titleId": ids.RatingKeyGUID(keyUnknownDur),
		"client":  clientPayload("dev-nodur"),
	})
	if w.Code != 200 {
		t.Fatalf("unknown-duration playback status=%d body=%s", w.Code, w.Body.String())
	}
	if _, exists := noDurPlan["trickplay"]; exists {
		t.Fatalf("expected plan.trickplay omitted when duration unknown, got %v", noDurPlan["trickplay"])
	}

	// 3. Fallback already cached: plan.trickplay present immediately at plan time
	// without starting generation or requiring observations.
	readyKey := core.TrickplayCacheKey(&core.PlaySession{Key: keyReady, Resolution: &core.Resolution{Release: core.Release{ID: "rel-ready"}}})
	thumbs.SetTrickplayFallback(readyKey, fallbackJPG)

	w, plan := callJSON("tok-1", "dev-1", "POST", "/vela/v0/playback", object{
		"titleId": ids.RatingKeyGUID(keyReady),
		"client":  clientPayload("dev-1"),
	})
	if w.Code != 200 {
		t.Fatalf("ready playback status=%d body=%s", w.Code, w.Body.String())
	}
	sessionID, _ := plan["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("missing sessionId in plan: %v", plan)
	}
	if got := runner.calls.Load(); got != 0 {
		t.Fatalf("ready plan started thumbnail generation: %d ffmpeg calls", got)
	}
	tp, ok := plan["trickplay"].(map[string]any)
	if !ok {
		t.Fatalf("expected plan.trickplay from plan time, got %v", plan)
	}
	wantInt := func(m map[string]any, field string, want float64) {
		t.Helper()
		if got, ok := m[field].(float64); !ok || got != want {
			t.Fatalf("trickplay.%s = %v, want %v (full=%v)", field, m[field], want, m)
		}
	}
	wantInt(tp, "width", thumbW)
	wantInt(tp, "height", thumbH)
	wantInt(tp, "intervalMs", 10000)
	wantInt(tp, "count", 3) // ceil(25000 / 10000)
	wantInt(tp, "cols", 10)
	wantInt(tp, "rows", 10)
	wantInt(tp, "coverageUntilMs", 0)
	initialRev, _ := tp["revision"].(float64)
	if initialRev < 1 {
		t.Fatalf("expected positive revision, got %v", tp["revision"])
	}

	// Snapshot carries plan.trickplay too.
	w, snap := callJSON("tok-1", "dev-1", "GET", "/vela/v0/sessions/"+sessionID, nil)
	if w.Code != 200 {
		t.Fatalf("snapshot status=%d body=%s", w.Code, w.Body.String())
	}
	snapPlan, _ := snap["plan"].(map[string]any)
	if _, ok := snapPlan["trickplay"].(map[string]any); !ok {
		t.Fatalf("expected SessionSnapshot.plan.trickplay, got %v", snapPlan)
	}

	getSheet := func(token, devID, path, inm string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if devID != "" {
			req.Header.Set("X-Vela-Device-Id", devID)
		}
		if inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	sheetURL := fmt.Sprintf("/vela/v0/sessions/%s/trickplay/%d/0.jpg", sessionID, thumbW)

	// 4. Route 200 with fallback header, strong ETag, private cache-control, valid JPEG.
	res200 := getSheet("tok-1", "dev-1", sheetURL, "")
	if res200.Code != http.StatusOK {
		t.Fatalf("sheet 200 status=%d body=%s", res200.Code, res200.Body.String())
	}
	if ct := res200.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type=%q, want image/jpeg", ct)
	}
	if cc := res200.Header().Get("Cache-Control"); cc != "private, no-cache" {
		t.Fatalf("Cache-Control=%q, want private, no-cache", cc)
	}
	if fb := res200.Header().Get("X-Ether-Trickplay-Fallback"); fb != "true" {
		t.Fatalf("X-Ether-Trickplay-Fallback=%q, want true", fb)
	}
	etag1 := res200.Header().Get("ETag")
	if !strings.HasPrefix(etag1, `"`) || !strings.HasSuffix(etag1, `"`) {
		t.Fatalf("expected strong ETag, got %q", etag1)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(res200.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode sheet JPEG: %v", err)
	}
	if cfg.Width != thumbW*10 || cfg.Height != thumbH*10 {
		t.Fatalf("sheet dimensions = %dx%d, want %dx%d", cfg.Width, cfg.Height, thumbW*10, thumbH*10)
	}

	// 5. Conditional 304 with matching If-None-Match.
	res304 := getSheet("tok-1", "dev-1", sheetURL, etag1)
	if res304.Code != http.StatusNotModified {
		t.Fatalf("conditional status=%d, want 304", res304.Code)
	}
	if res304.Body.Len() != 0 {
		t.Fatalf("304 returned non-empty body (%d bytes)", res304.Body.Len())
	}
	if res304.Header().Get("ETag") != etag1 || res304.Header().Get("Cache-Control") != "private, no-cache" || res304.Header().Get("X-Ether-Trickplay-Fallback") != "true" {
		t.Fatalf("304 headers=%v", res304.Header())
	}

	// 6. Auth 401 and isolation/bounds 404.
	if w := getSheet("", "dev-1", sheetURL, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer status=%d, want 401", w.Code)
	}
	if w := getSheet("tok-1", "", sheetURL, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing device status=%d, want 401", w.Code)
	}
	for name, tc := range map[string]struct{ tok, dev, path string }{
		"other viewer":     {"tok-2", "dev-1", sheetURL},
		"other device":     {"tok-1", "dev-other", sheetURL},
		"unknown session":  {"tok-1", "dev-1", fmt.Sprintf("/vela/v0/sessions/missing/trickplay/%d/0.jpg", thumbW)},
		"wrong width":      {"tok-1", "dev-1", fmt.Sprintf("/vela/v0/sessions/%s/trickplay/%d/0.jpg", sessionID, thumbW+1)},
		"sheet past count": {"tok-1", "dev-1", fmt.Sprintf("/vela/v0/sessions/%s/trickplay/%d/1.jpg", sessionID, thumbW)},
		"bad extension":    {"tok-1", "dev-1", fmt.Sprintf("/vela/v0/sessions/%s/trickplay/%d/0.png", sessionID, thumbW)},
	} {
		if w := getSheet(tc.tok, tc.dev, tc.path, ""); w.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d, want 404", name, w.Code)
		}
	}

	// 7. Playback observation triggers progressive extraction -> real frames fill
	// all 3 slots, advancing coverageUntilMs to duration and revision monotonically,
	// flipping X-Ether-Trickplay-Fallback to false, and invalidating the old ETag.
	w, _ = callJSON("tok-1", "dev-1", "POST", "/vela/v0/sessions/"+sessionID+"/observations", object{
		"sourceGeneration": 1,
		"seekGeneration":   1,
		"sequence":         1,
		"positionSeconds":  25.0,
		"state":            "playing",
		"selectedPathId":   "primary",
	})
	if w.Code != 200 {
		t.Fatalf("observation status=%d body=%s", w.Code, w.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		st, ok := thumbs.TrickplayState(readyKey, 25*time.Second)
		if ok && st.CoverageUntil == 25*time.Second && !st.Generating {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for real trickplay frames (calls=%d)", runner.calls.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}

	w, snap2 := callJSON("tok-1", "dev-1", "GET", "/vela/v0/sessions/"+sessionID, nil)
	if w.Code != 200 {
		t.Fatalf("snapshot after extraction status=%d", w.Code)
	}
	tp2 := snap2["plan"].(map[string]any)["trickplay"].(map[string]any)
	wantInt(tp2, "coverageUntilMs", 25000)
	if rev2, _ := tp2["revision"].(float64); rev2 <= initialRev {
		t.Fatalf("expected monotonic revision > %v, got %v", initialRev, rev2)
	}

	// Old ETag must now return 200 with a fresh ETag and Fallback=false.
	resUpdated := getSheet("tok-1", "dev-1", sheetURL, etag1)
	if resUpdated.Code != http.StatusOK {
		t.Fatalf("stale ETag after real frames status=%d, want 200", resUpdated.Code)
	}
	etag2 := resUpdated.Header().Get("ETag")
	if etag2 == "" || etag2 == etag1 {
		t.Fatalf("expected updated ETag != %q, got %q", etag1, etag2)
	}
	if fb := resUpdated.Header().Get("X-Ether-Trickplay-Fallback"); fb != "false" {
		t.Fatalf("X-Ether-Trickplay-Fallback after real frames=%q, want false", fb)
	}
	if res304New := getSheet("tok-1", "dev-1", sheetURL, etag2); res304New.Code != http.StatusNotModified || res304New.Header().Get("X-Ether-Trickplay-Fallback") != "false" {
		t.Fatalf("new ETag conditional status=%d fb=%q", res304New.Code, res304New.Header().Get("X-Ether-Trickplay-Fallback"))
	}

	// 8. Cold session started with no plan.trickplay: once playback triggers
	// generation, watchSession publishes a full plan event carrying trickplay.
	coldSessionID, _ := coldPlan["sessionId"].(string)
	w, _ = callJSON("tok-1", "dev-cold", "POST", "/vela/v0/sessions/"+coldSessionID+"/observations", object{
		"sourceGeneration": 1,
		"seekGeneration":   1,
		"sequence":         1,
		"positionSeconds":  25.0,
		"state":            "playing",
		"selectedPathId":   "primary",
	})
	if w.Code != 200 {
		t.Fatalf("cold observation status=%d body=%s", w.Code, w.Body.String())
	}
	coldSess := velaInst.sessions[coldSessionID]
	sseDeadline := time.Now().Add(3 * time.Second)
	var publishedTP map[string]any
	for publishedTP == nil {
		coldSess.mu.Lock()
		for _, ev := range coldSess.events {
			if ev["type"] != "plan" {
				continue
			}
			if evPlan, ok := ev["plan"].(object); ok {
				if tpObj, ok := evPlan["trickplay"].(map[string]any); ok {
					publishedTP = tpObj
				}
			}
		}
		coldSess.mu.Unlock()
		if publishedTP != nil {
			break
		}
		if time.Now().After(sseDeadline) {
			t.Fatalf("timed out waiting for full plan event carrying trickplay")
		}
		time.Sleep(5 * time.Millisecond)
	}
	wantInt(publishedTP, "width", thumbW)
	wantInt(publishedTP, "height", thumbH)
	wantInt(publishedTP, "count", 3)

	// 9. Stopping the session invalidates the route even with a matching If-None-Match.
	w, _ = callJSON("tok-1", "dev-1", "POST", "/vela/v0/sessions/"+sessionID+"/commands", object{
		"kind":             "stop",
		"sourceGeneration": 1,
		"seekGeneration":   1,
	})
	if w.Code != 200 {
		t.Fatalf("stop status=%d body=%s", w.Code, w.Body.String())
	}
	if resAfterStop := getSheet("tok-1", "dev-1", sheetURL, etag2); resAfterStop.Code != http.StatusNotFound {
		t.Fatalf("stopped session conditional status=%d, want 404", resAfterStop.Code)
	}
}
