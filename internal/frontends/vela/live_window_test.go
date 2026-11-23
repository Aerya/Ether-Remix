package vela

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type liveWindowFake struct {
	livePageFake
	available bool
	response  frontend.LiveGuideWindow
	err       error
	lastQuery frontend.LiveGuideWindowQuery
	calls     int
}

func (f *liveWindowFake) LiveGuideWindowAvailable() bool { return f.available }

func (f *liveWindowFake) GuideWindowBatch(_ context.Context, _ *frontend.Viewer, q frontend.LiveGuideWindowQuery) (frontend.LiveGuideWindow, error) {
	f.calls++
	f.lastQuery = q
	if f.err != nil {
		return frontend.LiveGuideWindow{}, f.err
	}
	return f.response, nil
}

func newVelaWindowInstance(t *testing.T, live frontend.LiveTV, grants frontend.ImageGrants) (*instance, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE vela_replays(key TEXT PRIMARY KEY,digest TEXT NOT NULL,response BLOB,expires INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	inst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(context.Background(), frontend.Host{
		Identity:    identityFake{},
		Experience:  frontendtest.Experience{},
		Sessions:    frontendtest.Sessions{},
		LiveTV:      live,
		ImageGrants: grants,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(context.Background()) })
	return inst.(*instance), db
}

func assertSameJSONShape(t *testing.T, path string, got, want any) {
	t.Helper()
	if got == nil || want == nil {
		if got != want {
			t.Fatalf("%s: got %T, want %T", path, got, want)
		}
		return
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("%s: got %T, want object", path, got)
		}
		for k := range w {
			if _, exists := g[k]; !exists {
				t.Fatalf("%s: missing key %q", path, k)
			}
		}
		for k := range g {
			if _, exists := w[k]; !exists {
				t.Fatalf("%s: unexpected key %q", path, k)
			}
		}
		for k, wv := range w {
			assertSameJSONShape(t, path+"."+k, g[k], wv)
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			t.Fatalf("%s: got %T, want array", path, got)
		}
		if len(g) != len(w) {
			t.Fatalf("%s: array len = %d, want %d", path, len(g), len(w))
		}
		for idx := range w {
			assertSameJSONShape(t, fmt.Sprintf("%s[%d]", path, idx), g[idx], w[idx])
		}
	default:
		if reflect.TypeOf(got) != reflect.TypeOf(want) {
			t.Fatalf("%s: got %T, want %T", path, got, want)
		}
	}
}

func TestVelaLiveGuideWindowContractFixture(t *testing.T) {
	fixtureBytes, err := os.ReadFile(filepath.Join("testdata", "live_guide_window.json"))
	if err != nil {
		t.Fatal(err)
	}
	var wantDoc map[string]any
	if err := json.Unmarshal(fixtureBytes, &wantDoc); err != nil {
		t.Fatal(err)
	}

	// Verify the committed fixture itself satisfies the coordinator's contract invariants.
	fixtureFrom, err := time.Parse(time.RFC3339, wantDoc["from"].(string))
	if err != nil {
		t.Fatal(err)
	}
	fixtureUntil, err := time.Parse(time.RFC3339, wantDoc["until"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, wantDoc["validUntil"].(string)); err != nil {
		t.Fatalf("fixture validUntil invalid: %v", err)
	}
	fixtureChannels := wantDoc["channels"].([]any)
	if len(fixtureChannels) != 3 {
		t.Fatalf("fixture channels len = %d, want 3", len(fixtureChannels))
	}
	row0 := fixtureChannels[0].(map[string]any)
	row0Progs := row0["programmes"].([]any)
	firstStart, _ := time.Parse(time.RFC3339, row0Progs[0].(map[string]any)["start"].(string))
	firstEnd, _ := time.Parse(time.RFC3339, row0Progs[0].(map[string]any)["end"].(string))
	lastProg := row0Progs[len(row0Progs)-1].(map[string]any)
	lastStart, _ := time.Parse(time.RFC3339, lastProg["start"].(string))
	lastEnd, _ := time.Parse(time.RFC3339, lastProg["end"].(string))
	if row0["state"] != "ready" || !firstStart.Before(fixtureFrom) || !firstEnd.After(fixtureFrom) || !lastStart.Before(fixtureUntil) || !lastEnd.After(fixtureUntil) {
		t.Fatalf("fixture row 0 must be ready and cross both edges")
	}
	row1 := fixtureChannels[1].(map[string]any)
	if row1["state"] != "ready" || len(row1["programmes"].([]any)) != 0 {
		t.Fatalf("fixture row 1 must be ready with []")
	}
	row2 := fixtureChannels[2].(map[string]any)
	if row2["state"] != "unavailable" || len(row2["programmes"].([]any)) != 0 {
		t.Fatalf("fixture row 2 must be unavailable with []")
	}

	sharedImage := frontend.Image{Kind: frontend.ImageStill, Path: "opaque-news-card", Subject: "ch-news-1795023000000", Tag: "epg"}
	secondImage := frontend.Image{Kind: frontend.ImageStill, Path: "opaque-news-card", Subject: "ch-news-1795026600000", Tag: "epg"}
	source := &liveWindowFake{
		available: true,
		response: frontend.LiveGuideWindow{
			From:       fixtureFrom,
			Until:      fixtureUntil,
			Revision:   "rev-window-1",
			ValidUntil: fixtureFrom.Add(60 * time.Second),
			Channels: []frontend.LiveGuideWindowChannel{
				{
					ChannelID: "ch-news",
					State:     frontend.LiveGuideWindowReady,
					Programmes: []frontend.LiveProgramme{
						{
							ID:        "ch-news-1795023000000",
							ChannelID: "ch-news",
							Title:     "Afternoon Edition",
							Summary:   "Live headlines before the evening hour.",
							Start:     time.Date(2026, 11, 18, 17, 30, 0, 0, time.UTC),
							End:       time.Date(2026, 11, 18, 18, 30, 0, 0, time.UTC),
							Rating:    "TV-PG",
							Genres:    []string{"News"},
							Images:    []frontend.Image{sharedImage},
						},
						{
							ID:           "ch-news-1795026600000",
							ChannelID:    "ch-news",
							Title:        "World Tonight",
							Summary:      "International reporting.",
							Start:        time.Date(2026, 11, 18, 18, 30, 0, 0, time.UTC),
							End:          time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC),
							EpisodeTitle: "Wednesday",
							Season:       12,
							Episode:      4,
							Rating:       "TV-PG",
							Genres:       []string{"News", "Documentary"},
							Images:       []frontend.Image{secondImage},
						},
						{
							ID:        "ch-news-1795032000000",
							ChannelID: "ch-news",
							Title:     "Late Briefing",
							Summary:   "Overnight analysis.",
							Start:     time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC),
							End:       time.Date(2026, 11, 18, 21, 30, 0, 0, time.UTC),
							Rating:    "TV-14",
						},
					},
				},
				{
					ChannelID:  "ch-quiet",
					State:      frontend.LiveGuideWindowReady,
					Programmes: []frontend.LiveProgramme{},
				},
				{
					ChannelID:  "ch-down",
					State:      frontend.LiveGuideWindowUnavailable,
					Programmes: []frontend.LiveProgramme{},
				},
			},
		},
	}

	grantCalls := 0
	grants := frontendtest.ImageGrants{
		IssueFunc: func(_ context.Context, req frontend.ImageGrantRequest) (string, error) {
			if req.Channel != "ch-news" || req.Item != 0 || req.Credential != "token" || req.Device.ID != "tv-1" {
				t.Fatalf("unexpected image grant request: %+v", req)
			}
			grantCalls++
			return "grant-news-card", nil
		},
	}

	inst, db := newVelaWindowInstance(t, source, grants)
	body := `{"channelIds":["ch-news","ch-quiet","ch-down"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T21:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/vela/v0/live/guide/window", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Vela-Device-Id", "tv-1")
	w := httptest.NewRecorder()
	inst.serve(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if grantCalls != 1 {
		t.Fatalf("expected liveArtCache to deduplicate repeated channel artwork to 1 grant, got %d", grantCalls)
	}
	var replayRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vela_replays`).Scan(&replayRows); err != nil || replayRows != 0 {
		t.Fatalf("read-only guide window must not persist vela_replays rows: count=%d err=%v", replayRows, err)
	}

	var gotDoc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &gotDoc); err != nil {
		t.Fatal(err)
	}
	assertSameJSONShape(t, "$", gotDoc, wantDoc)
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		gotPretty, _ := json.MarshalIndent(gotDoc, "", "  ")
		t.Fatalf("handler output mismatch against fixture:\ngot:\n%s\nwant:\n%s", gotPretty, fixtureBytes)
	}
}

func TestVelaLiveGuideWindowCapabilityAndCaching(t *testing.T) {
	now := time.Date(2026, 11, 18, 18, 0, 0, 0, time.UTC)
	source := &liveWindowFake{
		livePageFake: livePageFake{
			snapshot:   "snap-1",
			programmes: []frontend.LiveProgramme{{ID: "ch1-1", ChannelID: "ch1", Title: "News", Start: now, End: now.Add(time.Hour)}},
		},
		available: true,
		response: frontend.LiveGuideWindow{
			From:       now,
			Until:      now.Add(2 * time.Hour),
			Revision:   "rev-a",
			ValidUntil: now.Add(6 * time.Hour),
			Channels: []frontend.LiveGuideWindowChannel{
				{ChannelID: "ch1", State: frontend.LiveGuideWindowReady, Programmes: []frontend.LiveProgramme{}},
			},
		},
	}
	inst, _ := newVelaWindowInstance(t, source, nil)

	// 1. Capabilities & bootstrap advertise liveGuideWindow when Live TV is available.
	capsReq := httptest.NewRequest(http.MethodGet, "/vela/v0/capabilities", nil)
	capsRec := httptest.NewRecorder()
	inst.serve(capsRec, capsReq)
	var caps map[string]any
	if err := json.Unmarshal(capsRec.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	wantCap := map[string]any{"maxChannels": float64(128), "maxWindowMinutes": float64(180)}
	if !reflect.DeepEqual(caps["liveGuideWindow"], wantCap) {
		t.Fatalf("capabilities.liveGuideWindow = %#v, want %#v", caps["liveGuideWindow"], wantCap)
	}

	bootReq := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
	bootReq.Header.Set("Authorization", "Bearer token")
	bootReq.Header.Set("X-Vela-Device-Id", "tv-1")
	bootRec := httptest.NewRecorder()
	inst.serve(bootRec, bootReq)
	if bootRec.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d %s", bootRec.Code, bootRec.Body.String())
	}
	var boot map[string]any
	if err := json.Unmarshal(bootRec.Body.Bytes(), &boot); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(boot["capabilities"].(map[string]any)["liveGuideWindow"], wantCap) {
		t.Fatalf("bootstrap capabilities.liveGuideWindow = %#v", boot["capabilities"])
	}

	// 2. Existing GET /vela/v0/live/guide remains unchanged.
	legacyReq := httptest.NewRequest(http.MethodGet, "/vela/v0/live/guide?from=2026-11-18T18:00:00Z&until=2026-11-18T19:00:00Z", nil)
	legacyReq.Header.Set("Authorization", "Bearer token")
	legacyReq.Header.Set("X-Vela-Device-Id", "tv-1")
	legacyRec := httptest.NewRecorder()
	inst.serve(legacyRec, legacyReq)
	if legacyRec.Code != http.StatusOK {
		t.Fatalf("GET /live/guide broken: %d %s", legacyRec.Code, legacyRec.Body.String())
	}

	// 3. ETag / If-None-Match 304 and optional idempotencyKey.
	post := func(payload string, ifNoneMatch string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/vela/v0/live/guide/window", strings.NewReader(payload))
		r.Header.Set("Authorization", "Bearer token")
		r.Header.Set("X-Vela-Device-Id", "tv-1")
		if ifNoneMatch != "" {
			r.Header.Set("If-None-Match", ifNoneMatch)
		}
		w := httptest.NewRecorder()
		inst.serve(w, r)
		return w
	}

	first := post(`{"channelIds":["ch1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T20:00:00Z","idempotencyKey":"optional-key-1"}`, "")
	if first.Code != http.StatusOK || first.Header().Get("ETag") == "" {
		t.Fatalf("first window = %d etag=%q body=%s", first.Code, first.Header().Get("ETag"), first.Body.String())
	}
	etag := first.Header().Get("ETag")

	notModified := post(`{"channelIds":["ch1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T20:00:00Z"}`, etag)
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("expected 304 with matching If-None-Match, got %d", notModified.Code)
	}

	source.response.Revision = "rev-b"
	changed := post(`{"channelIds":["ch1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T20:00:00Z"}`, etag)
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") == etag {
		t.Fatalf("expected 200 and new ETag after revision change, got %d etag=%q", changed.Code, changed.Header().Get("ETag"))
	}

	// 4. When Live TV is unavailable, capabilities omit liveGuideWindow and POST returns 403 feature_unavailable.
	noLiveInst, _ := newVelaWindowInstance(t, nil, nil)
	noLiveCapsRec := httptest.NewRecorder()
	noLiveInst.serve(noLiveCapsRec, httptest.NewRequest(http.MethodGet, "/vela/v0/capabilities", nil))
	var noLiveCaps map[string]any
	_ = json.Unmarshal(noLiveCapsRec.Body.Bytes(), &noLiveCaps)
	if _, exists := noLiveCaps["liveGuideWindow"]; exists {
		t.Fatalf("expected liveGuideWindow omitted when Live TV unavailable, got %#v", noLiveCaps["liveGuideWindow"])
	}
	unavailReq := httptest.NewRequest(http.MethodPost, "/vela/v0/live/guide/window", strings.NewReader(`{"channelIds":["ch1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T19:00:00Z"}`))
	unavailReq.Header.Set("Authorization", "Bearer token")
	unavailReq.Header.Set("X-Vela-Device-Id", "tv-1")
	unavailRec := httptest.NewRecorder()
	noLiveInst.serve(unavailRec, unavailReq)
	if unavailRec.Code != http.StatusForbidden || !strings.Contains(unavailRec.Body.String(), `"feature_unavailable"`) {
		t.Fatalf("expected 403 feature_unavailable, got %d %s", unavailRec.Code, unavailRec.Body.String())
	}
}

func TestVelaLiveGuideWindowErrors(t *testing.T) {
	source := &liveWindowFake{available: true}
	inst, _ := newVelaWindowInstance(t, source, nil)

	do := func(auth bool, rawBody string) (int, map[string]any) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/vela/v0/live/guide/window", bytes.NewBufferString(rawBody))
		if auth {
			r.Header.Set("Authorization", "Bearer token")
			r.Header.Set("X-Vela-Device-Id", "tv-1")
		}
		w := httptest.NewRecorder()
		inst.serve(w, r)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal error body %q: %v", w.Body.String(), err)
		}
		return w.Code, out
	}

	var ids129 []string
	for n := range 129 {
		ids129 = append(ids129, fmt.Sprintf("ch-%d", n))
	}
	tooManyBody, _ := json.Marshal(map[string]any{
		"channelIds": ids129,
		"from":       "2026-11-18T18:00:00Z",
		"until":      "2026-11-18T19:00:00Z",
	})

	cases := []struct {
		name      string
		auth      bool
		body      string
		wantCode  int
		wantClass string
		wantErr   string
	}{
		{"unauthorized", false, `{"channelIds":["c1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T19:00:00Z"}`, 401, "auth", "unauthorized"},
		{"malformed json", true, `{"channelIds":[`, 400, "request", "invalid_json"},
		{"trailing json", true, `{"channelIds":["c1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T19:00:00Z"} {}`, 400, "request", "invalid_json"},
		{"null json", true, `null`, 400, "request", "invalid_request"},
		{"empty channels", true, `{"channelIds":[],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T19:00:00Z"}`, 400, "request", "invalid_request"},
		{"blank channel id", true, `{"channelIds":[""],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T19:00:00Z"}`, 400, "request", "invalid_request"},
		{"duplicate channel id", true, `{"channelIds":["c1","c1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T19:00:00Z"}`, 400, "request", "invalid_request"},
		{"empty idempotencyKey", true, `{"channelIds":["c1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T19:00:00Z","idempotencyKey":""}`, 400, "request", "invalid_request"},
		{"too many channels", true, string(tooManyBody), 400, "request", "too_many_channels"},
		{"bad timestamp", true, `{"channelIds":["c1"],"from":"not-a-date","until":"2026-11-18T19:00:00Z"}`, 400, "request", "invalid_guide_window"},
		{"until not after from", true, `{"channelIds":["c1"],"from":"2026-11-18T19:00:00Z","until":"2026-11-18T19:00:00Z"}`, 400, "request", "invalid_guide_window"},
		{"window over 3h", true, `{"channelIds":["c1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T21:00:01Z"}`, 400, "request", "invalid_guide_window"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, doc := do(tc.auth, tc.body)
			errObj, _ := doc["error"].(map[string]any)
			if code != tc.wantCode || errObj["class"] != tc.wantClass || errObj["code"] != tc.wantErr {
				t.Fatalf("%s: got %d %#v, want %d %s/%s", tc.name, code, doc, tc.wantCode, tc.wantClass, tc.wantErr)
			}
		})
	}

	source.err = &frontend.LiveGuideWindowTooDenseError{MaxChannels: 64, MaxWindowMinutes: 90}
	code, doc := do(true, `{"channelIds":["c1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T21:00:00Z"}`)
	errObj, _ := doc["error"].(map[string]any)
	suggest, _ := errObj["suggest"].(map[string]any)
	if code != 400 || errObj["class"] != "request" || errObj["code"] != "guide_window_too_dense" || suggest["maxChannels"] != float64(64) || suggest["maxWindowMinutes"] != float64(90) {
		t.Fatalf("guide_window_too_dense shape mismatch: %d %#v", code, doc)
	}
}
