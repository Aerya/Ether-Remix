package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/veladist"
)

type livePreviewRevisionExperience struct {
	frontendtest.Experience
	policyRev *int64
}

func (e livePreviewRevisionExperience) HomeRevision(context.Context, *frontend.Viewer) (frontend.HomeRevision, bool, error) {
	return frontend.HomeRevision{Policy: *e.policyRev}, true, nil
}

func TestVelaLivePreviewSettingsValidationAndNormalization(t *testing.T) {
	fe := Frontend{}
	reg := frontend.NewRegistry(func(frontend.Protocol) frontend.Host { return frontend.Host{} })
	if err := reg.Register(fe); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{LivePreviewAuto, LivePreviewOnClick, LivePreviewOff} {
		if _, err := reg.ValidateSettings("vela", frontend.Settings{"livePreview": mode}); err != nil {
			t.Fatalf("ValidateSettings(%q): %v", mode, err)
		}
		norm := NormalizeSettings(map[string]any{"livePreview": mode})
		if norm["livePreview"] != mode {
			t.Fatalf("NormalizeSettings(%q) = %#v", mode, norm)
		}
	}

	for _, bad := range []any{"", "always", "ON-CLICK", "manual", 123, true, nil} {
		if _, err := reg.ValidateSettings("vela", frontend.Settings{"livePreview": bad}); err == nil {
			t.Fatalf("expected ValidateSettings to reject %#v", bad)
		}
	}

	if norm := NormalizeSettings(map[string]any{"livePreview": "bogus", "defaultSkinId": "cinematic"}); norm["livePreview"] != nil {
		t.Fatalf("NormalizeSettings should drop invalid livePreview: %#v", norm)
	}
	if norm := NormalizeSettings(map[string]any{"defaultSkinId": "cinematic"}); len(norm) != 1 || norm["defaultSkinId"] != "cinematic" {
		t.Fatalf("NormalizeSettings should not synthesize livePreview when omitted: %#v", norm)
	}
}

func TestVelaLivePreviewCapabilitiesRevisionAndWatchLive(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()

	assetsDir := t.TempDir()
	for rel, content := range map[string]string{
		"index.html":           `<!doctype html><html><head><title>Vela</title></head><body>ok</body></html>`,
		"manifest.webmanifest": `{"name":"Vela","short_name":"Vela"}`,
		"sw.js":                `self.addEventListener("fetch",()=>{})`,
		"pwa.js":               `window.__pwa=1`,
		"boot-handoff.js":      `window.__boot=1`,
		"vela-icon-192.png":    "icon-bytes",
		"_expo/static/js/web/entry-a1b2c3d4e5f60718293a4b5c6d7e8f90.js": `console.log(1)`,
		"_expo/static/css/web-9c8a7b6e5d4f3a2b1c0d9e8f7a6b5c4d.css":     `body{}`,
	} {
		full := filepath.Join(assetsDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Config{Frontends: map[string]config.Frontend{}}
	policyEpoch := int64(1)
	var plannedChannel string

	sessions := frontendtest.Sessions{
		PlanFunc: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
			plannedChannel = req.Channel
			return frontend.PlayPlan{
				ID:           "plan-live-1",
				HLS:          true,
				HLSContainer: "mpegts",
			}, nil
		},
		OpenFunc: func(_ context.Context, req frontend.OpenRequest) (frontend.Session, error) {
			return frontend.Session{
				ID:        "sess-live-1",
				MediaPath: "/sessions/media/sess-live-1/live.m3u8",
				Channel:   plannedChannel,
			}, nil
		},
		StartStreamFunc: func(context.Context, frontend.SessionAccess, string, frontend.StreamRequest) (string, error) {
			return "/sessions/media/sess-live-1/live.m3u8", nil
		},
	}

	mounted, err := (Frontend{AssetsDir: assetsDir, DB: db, ReplayKey: make([]byte, 32)}).Mount(ctx, frontend.Host{
		Identity:   identityFake{},
		Experience: livePreviewRevisionExperience{policyRev: &policyEpoch},
		Sessions:   sessions,
		Capabilities: frontendtest.Capabilities{
			BuildFunc: func(_ context.Context, req frontend.CapabilityRequest) (frontend.CapabilityResult, error) {
				return frontend.CapabilityResult{}, nil
			},
		},
		LiveTV:     &liveWindowFake{available: true},
		HostConfig: func() config.Config { return cfg },
		ViewerCapabilities: func(context.Context, *frontend.Viewer, frontend.Device, netip.Addr) (frontend.ViewerCapabilities, error) {
			return frontend.ViewerCapabilities{PolicyRevision: policyEpoch}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounted.Close(ctx) })
	h := mounted.Handler()

	doAuth := func(method, path, body, ifNoneMatch string) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		req.Header.Set("X-Vela-Device-Id", "dev-1")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotModified || rec.Body.Len() == 0 {
			return rec, nil
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("%s %s invalid json (%d): %v: %s", method, path, rec.Code, err, rec.Body.String())
		}
		return rec, doc
	}

	assertMode := func(wantMode string, expectPolicyChangeFrom string, prevCapsETag, prevBootETag string) (capsETag, bootETag, policyRev string) {
		t.Helper()
		wantFeature := map[string]any{"available": true, "mode": wantMode}

		// 1. GET /vela/v0/capabilities
		capsRec, capsDoc := doAuth(http.MethodGet, "/vela/v0/capabilities", "", prevCapsETag)
		if capsRec.Code != http.StatusOK {
			t.Fatalf("GET /capabilities (%s) = %d, want 200", wantMode, capsRec.Code)
		}
		if capsRec.Header().Get("Cache-Control") != "private, max-age=0, must-revalidate" {
			t.Fatalf("GET /capabilities Cache-Control = %q", capsRec.Header().Get("Cache-Control"))
		}
		if capsRec.Header().Get("Vary") != "Authorization, X-Vela-Device-Id" {
			t.Fatalf("GET /capabilities Vary = %q", capsRec.Header().Get("Vary"))
		}
		capsETag = capsRec.Header().Get("ETag")
		if capsETag == "" || capsETag == prevCapsETag {
			t.Fatalf("GET /capabilities ETag did not advance: old=%q new=%q", prevCapsETag, capsETag)
		}
		gotCaps := capsDoc["features"].(map[string]any)["livePreview"]
		if !reflect.DeepEqual(gotCaps, wantFeature) {
			t.Fatalf("GET /capabilities features.livePreview = %#v, want %#v", gotCaps, wantFeature)
		}
		// Matching ETag returns 304.
		caps304, _ := doAuth(http.MethodGet, "/vela/v0/capabilities", "", capsETag)
		if caps304.Code != http.StatusNotModified {
			t.Fatalf("GET /capabilities conditional = %d, want 304", caps304.Code)
		}

		// 2. POST /vela/v0/negotiate
		negBody := `{"idempotencyKey":"neg-` + wantMode + `","client":{"protocolVersions":[0],"device":{"id":"dev-1","name":"TV","platform":"web","appVersion":"0.2.0"}}}`
		negRec, negDoc := doAuth(http.MethodPost, "/vela/v0/negotiate", negBody, "")
		if negRec.Code != http.StatusOK {
			t.Fatalf("POST /negotiate (%s) = %d: %s", wantMode, negRec.Code, negRec.Body.String())
		}
		if negRec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("POST /negotiate Cache-Control = %q, want no-store", negRec.Header().Get("Cache-Control"))
		}
		gotNeg := negDoc["capabilities"].(map[string]any)["features"].(map[string]any)["livePreview"]
		if !reflect.DeepEqual(gotNeg, wantFeature) {
			t.Fatalf("POST /negotiate capabilities.features.livePreview = %#v, want %#v", gotNeg, wantFeature)
		}

		// 3. GET /vela/v0/bootstrap
		bootRec, bootDoc := doAuth(http.MethodGet, "/vela/v0/bootstrap", "", prevBootETag)
		if bootRec.Code != http.StatusOK {
			t.Fatalf("GET /bootstrap (%s) = %d, want 200", wantMode, bootRec.Code)
		}
		if bootRec.Header().Get("Cache-Control") != "private, max-age=0, must-revalidate" {
			t.Fatalf("GET /bootstrap Cache-Control = %q", bootRec.Header().Get("Cache-Control"))
		}
		bootETag = bootRec.Header().Get("ETag")
		if bootETag == "" || bootETag == prevBootETag {
			t.Fatalf("GET /bootstrap ETag did not advance: old=%q new=%q", prevBootETag, bootETag)
		}
		gotBoot := bootDoc["capabilities"].(map[string]any)["features"].(map[string]any)["livePreview"]
		if !reflect.DeepEqual(gotBoot, wantFeature) {
			t.Fatalf("GET /bootstrap capabilities.features.livePreview = %#v, want %#v", gotBoot, wantFeature)
		}
		policyRev = bootDoc["viewer"].(map[string]any)["policyRevision"].(string)
		if policyRev == "" || (expectPolicyChangeFrom != "" && policyRev == expectPolicyChangeFrom) {
			t.Fatalf("bootstrap viewer.policyRevision did not change: old=%q new=%q", expectPolicyChangeFrom, policyRev)
		}
		// Matching ETag hits cached bootstrap and returns 304.
		boot304, _ := doAuth(http.MethodGet, "/vela/v0/bootstrap", "", bootETag)
		if boot304.Code != http.StatusNotModified {
			t.Fatalf("GET /bootstrap conditional = %d, want 304", boot304.Code)
		}

		return capsETag, bootETag, policyRev
	}

	// Default when unset is "auto".
	capsETag0, bootETag0, pol0 := assertMode(LivePreviewAuto, "", "", "")

	// Switch to "on-click" + policy epoch bump -> invalidates capabilities & bootstrap ETags and policyRevision.
	cfg.Frontends["vela"] = config.Frontend{Settings: map[string]any{"livePreview": LivePreviewOnClick}}
	policyEpoch++
	capsETag1, bootETag1, pol1 := assertMode(LivePreviewOnClick, pol0, capsETag0, bootETag0)

	// Switch to "off" + policy epoch bump -> invalidates again.
	cfg.Frontends["vela"] = config.Frontend{Settings: map[string]any{"livePreview": LivePreviewOff}}
	policyEpoch++
	assertMode(LivePreviewOff, pol1, capsETag1, bootETag1)

	// Explicit Watch live playback still works when livePreview == "off".
	playBody := `{"idempotencyKey":"watch-live-off","channelId":"ch-news","client":{"device":{"id":"dev-1","name":"TV","platform":"web","appVersion":"0.2.0"},"hls":true}}`
	playRec, playDoc := doAuth(http.MethodPost, "/vela/v0/playback", playBody, "")
	if playRec.Code != http.StatusOK {
		t.Fatalf("POST /vela/v0/playback Watch live with livePreview=off failed: %d %s", playRec.Code, playRec.Body.String())
	}
	if playRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("POST /vela/v0/playback Cache-Control = %q, want no-store", playRec.Header().Get("Cache-Control"))
	}
	if plannedChannel != "ch-news" || playDoc["sessionId"] != "sess-live-1" || playDoc["channelId"] != "ch-news" {
		t.Fatalf("Watch live playback response unexpected: planned=%q doc=%#v", plannedChannel, playDoc)
	}

	// Verify mounted handler serves static /vela/* with proper Cache-Control (never no-store)
	// while keeping /vela/v0/* API auth/privacy headers untouched.
	for _, tc := range []struct {
		path   string
		wantCC string
	}{
		{"/vela", veladist.CacheControlRevalidate},
		{"/vela/index.html", veladist.CacheControlRevalidate},
		{"/vela/manifest.webmanifest", veladist.CacheControlRevalidate},
		{"/vela/sw.js", veladist.CacheControlRevalidate},
		{"/vela/pwa.js", veladist.CacheControlRevalidate},
		{"/vela/boot-handoff.js", veladist.CacheControlRevalidate},
		{"/vela/vela-icon-192.png", veladist.CacheControlRevalidate},
		{"/vela/_expo/static/js/web/entry-a1b2c3d4e5f60718293a4b5c6d7e8f90.js", veladist.CacheControlImmutable},
		{"/vela/_expo/static/css/web-9c8a7b6e5d4f3a2b1c0d9e8f7a6b5c4d.css", veladist.CacheControlImmutable},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != tc.wantCC {
			t.Fatalf("static GET %s = %d cc=%q, want 200 %q", tc.path, rec.Code, rec.Header().Get("Cache-Control"), tc.wantCC)
		}
		if rec.Header().Get("ETag") == "" || rec.Header().Get("Last-Modified") == "" {
			t.Fatalf("static GET %s missing ETag/Last-Modified: etag=%q lm=%q", tc.path, rec.Header().Get("ETag"), rec.Header().Get("Last-Modified"))
		}
	}

	// Unauthenticated protected API route still returns 401 with Cache-Control: no-store.
	unauthRec := httptest.NewRecorder()
	h.ServeHTTP(unauthRec, httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil))
	if unauthRec.Code != http.StatusUnauthorized || unauthRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauth GET /vela/v0/bootstrap = %d cc=%q, want 401 no-store", unauthRec.Code, unauthRec.Header().Get("Cache-Control"))
	}
}
