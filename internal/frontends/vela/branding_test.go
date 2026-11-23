package vela

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
)

func samplePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.NRGBA{R: 20, G: 40, B: 80, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Minimal valid 1x1 lossless VP8L WebP image.
func sampleWebP(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVelaWordmarkUploadAndPublicServing(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()

	pngBytes := samplePNG(t, 320, 80)
	lightWM, err := UploadWordmark(ctx, db, bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("upload PNG: %v", err)
	}
	if lightWM.Width != 320 || lightWM.Height != 80 || !strings.HasPrefix(lightWM.URL, "/vela/v0/branding/") || !strings.HasSuffix(lightWM.URL, ".png") {
		t.Fatalf("unexpected PNG wordmark: %+v", lightWM)
	}

	svgDoc := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 240 60"><style>.mark{fill:#fff}</style><text class="mark" x="10" y="40">Northlight</text></svg>`
	darkWM, err := UploadWordmark(ctx, db, strings.NewReader(svgDoc))
	if err != nil {
		t.Fatalf("upload SVG: %v", err)
	}
	if darkWM.Width != 240 || darkWM.Height != 60 || !strings.HasSuffix(darkWM.URL, ".svg") {
		t.Fatalf("unexpected SVG wordmark: %+v", darkWM)
	}

	webpWM, err := UploadWordmark(ctx, db, bytes.NewReader(sampleWebP(t)))
	if err != nil {
		t.Fatalf("upload WebP: %v", err)
	}
	if webpWM.Width != 1 || webpWM.Height != 1 || !strings.HasSuffix(webpWM.URL, ".webp") {
		t.Fatalf("unexpected WebP wordmark: %+v", webpWM)
	}

	// Reject > 512 KB.
	if _, err := UploadWordmark(ctx, db, bytes.NewReader(bytes.Repeat([]byte("a"), MaxWordmarkBytes+1))); !errors.Is(err, ErrWordmarkTooLarge) {
		t.Fatalf("expected ErrWordmarkTooLarge, got %v", err)
	}
	// Reject oversized dimensions.
	if _, err := UploadWordmark(ctx, db, bytes.NewReader(samplePNG(t, 1025, 64))); err == nil {
		t.Fatal("expected width > 1024 rejection")
	}
	if _, err := UploadWordmark(ctx, db, bytes.NewReader(samplePNG(t, 200, 257))); err == nil {
		t.Fatal("expected height > 256 rejection")
	}
	// Reject JPEG.
	var jpgBuf bytes.Buffer
	_ = jpeg.Encode(&jpgBuf, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil)
	if _, err := UploadWordmark(ctx, db, bytes.NewReader(jpgBuf.Bytes())); err == nil {
		t.Fatal("expected JPEG rejection")
	}
	// Reject dangerous SVGs.
	for name, badSVG := range map[string]string{
		"script":  `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"><script>alert(1)</script></svg>`,
		"onclick": `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40" onload="alert(1)"/>`,
		"href":    `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"><use href="https://evil.example/x.svg#a"/></svg>`,
		"import":  `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"><style>@import url("https://evil.example/x.css");</style></svg>`,
		"doctype": `<!DOCTYPE svg [<!ENTITY xxe "bad">]><svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"/>`,
	} {
		if _, err := UploadWordmark(ctx, db, strings.NewReader(badSVG)); err == nil {
			t.Fatalf("expected dangerous SVG %s to be rejected", name)
		}
	}

	// Mount Vela and verify unauthenticated public immutable serving, CSP, ETag,appearance, bootstrap, and hosted shell.
	assetsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetsDir, "index.html"), []byte(`<!doctype html><html><head><title>Vela</title><link rel="manifest" href="/vela/manifest.webmanifest"></head><body><div id="root"></div></body></html>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "manifest.webmanifest"), []byte(`{"id":"/vela","name":"Vela","short_name":"Vela","start_url":"/vela","display":"standalone"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(assetsDir, "install"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "install", "index.html"), []byte(`<html><head><title>Install Vela</title></head></html>`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Frontends: map[string]config.Frontend{}}
	fe := Frontend{AssetsDir: assetsDir, DB: db, ReplayKey: make([]byte, 32)}
	mounted, err := fe.Mount(ctx, frontend.Host{
		Identity:   identityFake{},
		Experience: frontendtest.Experience{},
		Sessions:   frontendtest.Sessions{},
		HostConfig: func() config.Config { return cfg },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounted.Close(ctx) })
	handler := mounted.Handler()

	// Unauthenticated SVG GET + conditional 304 + strict CSP.
	svgResp := httptest.NewRecorder()
	handler.ServeHTTP(svgResp, httptest.NewRequest(http.MethodGet, darkWM.URL, nil))
	if svgResp.Code != http.StatusOK || svgResp.Body.String() != svgDoc {
		t.Fatalf("SVG serve: %d %q", svgResp.Code, svgResp.Body.String())
	}
	if ct := svgResp.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("SVG Content-Type = %q", ct)
	}
	if cc := svgResp.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("SVG Cache-Control = %q", cc)
	}
	if csp := svgResp.Header().Get("Content-Security-Policy"); csp != "default-src 'none'; style-src 'unsafe-inline'" {
		t.Fatalf("SVG CSP = %q", csp)
	}
	if svgResp.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff")
	}
	condReq := httptest.NewRequest(http.MethodGet, darkWM.URL, nil)
	condReq.Header.Set("If-None-Match", svgResp.Header().Get("ETag"))
	condResp := httptest.NewRecorder()
	handler.ServeHTTP(condResp, condReq)
	if condResp.Code != http.StatusNotModified || condResp.Body.Len() != 0 {
		t.Fatalf("expected 304, got %d", condResp.Code)
	}

	// Default appearance + bootstrap branding = {} and hosted shell = "Vela".
	getJSON := func(path string, authed bool) (map[string]any, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if authed {
			req.Header.Set("Authorization", "Bearer token")
			req.Header.Set("X-Vela-Device-Id", "dev-1")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out, rec.Header().Get("ETag")
	}

	appDoc0, appETag0 := getJSON("/vela/v0/appearance", false)
	bootDoc0, bootETag0 := getJSON("/vela/v0/bootstrap", true)
	if !reflect.DeepEqual(appDoc0["branding"], map[string]any{}) || !reflect.DeepEqual(bootDoc0["branding"], map[string]any{}) {
		t.Fatalf("default branding mismatch: appearance=%#v bootstrap=%#v", appDoc0["branding"], bootDoc0["branding"])
	}

	shell0 := httptest.NewRecorder()
	handler.ServeHTTP(shell0, httptest.NewRequest(http.MethodGet, "/vela", nil))
	if !strings.Contains(shell0.Body.String(), "<title>Vela</title>") {
		t.Fatalf("default shell title: %s", shell0.Body.String())
	}
	man0 := httptest.NewRecorder()
	handler.ServeHTTP(man0, httptest.NewRequest(http.MethodGet, "/vela/manifest.webmanifest", nil))
	var manDoc0 map[string]any
	if err := json.Unmarshal(man0.Body.Bytes(), &manDoc0); err != nil {
		t.Fatal(err)
	}
	if manDoc0["name"] != "Vela" || manDoc0["short_name"] != "Vela" {
		t.Fatalf("default manifest: %#v", manDoc0)
	}

	// Apply owner branding (with HTML-sensitive characters in name) and bump revision.
	settings := frontend.Settings{
		"defaultSkinId": "cinematic",
		"appName":       "  Rassaun & Co  ",
		"wordmark": map[string]any{
			"url":    lightWM.URL,
			"width":  lightWM.Width,
			"height": lightWM.Height,
			"dark":   darkWM.URL,
		},
	}
	if err := fe.ValidateSettings(settings); err != nil {
		t.Fatalf("ValidateSettings: %v", err)
	}
	cfg.Frontends["vela"] = config.Frontend{AppearanceRevision: 1, Settings: NormalizeSettings(settings)}

	wantBranding := map[string]any{
		"name": "Rassaun & Co",
		"wordmark": map[string]any{
			"url":    lightWM.URL,
			"width":  float64(lightWM.Width),
			"height": float64(lightWM.Height),
			"dark":   darkWM.URL,
		},
	}
	appDoc1, appETag1 := getJSON("/vela/v0/appearance", false)
	bootDoc1, bootETag1 := getJSON("/vela/v0/bootstrap", true)
	if appETag1 == appETag0 || bootETag1 == bootETag0 {
		t.Fatalf("expected ETags to advance: appearance %q->%q, bootstrap %q->%q", appETag0, appETag1, bootETag0, bootETag1)
	}
	if !reflect.DeepEqual(appDoc1["branding"], wantBranding) {
		t.Fatalf("appearance branding = %#v, want %#v", appDoc1["branding"], wantBranding)
	}
	if !reflect.DeepEqual(bootDoc1["branding"], appDoc1["branding"]) {
		t.Fatalf("bootstrap branding = %#v, want %#v", bootDoc1["branding"], appDoc1["branding"])
	}

	// Hosted shell /vela, SPA fallback /vela/titles/42, and manifest update immediately; nested installer stays untouched.
	for _, route := range []string{"/vela", "/vela/titles/42"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>Rassaun &amp; Co</title>") {
			t.Fatalf("%s shell: %d %s", route, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("ETag") == shell0.Header().Get("ETag") {
			t.Fatalf("%s shell ETag did not update", route)
		}
	}
	man1 := httptest.NewRecorder()
	handler.ServeHTTP(man1, httptest.NewRequest(http.MethodGet, "/vela/manifest.webmanifest", nil))
	var manDoc1 map[string]any
	if err := json.Unmarshal(man1.Body.Bytes(), &manDoc1); err != nil {
		t.Fatal(err)
	}
	if manDoc1["name"] != "Rassaun & Co" || manDoc1["short_name"] != "Rassaun & Co" || manDoc1["start_url"] != "/vela" {
		t.Fatalf("branded manifest: %#v", manDoc1)
	}
	installer := httptest.NewRecorder()
	handler.ServeHTTP(installer, httptest.NewRequest(http.MethodGet, "/vela/install", nil))
	if !strings.Contains(installer.Body.String(), "<title>Install Vela</title>") {
		t.Fatalf("nested installer should not be rewritten: %s", installer.Body.String())
	}
}

func TestVelaBrandingAndInstalledSkinsTogether(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	skins := NewSkinStore(db, filepath.Join(t.TempDir(), "vela-skins"))

	wm, err := UploadWordmark(ctx, db, strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg" width="240" height="60"><rect width="240" height="60"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	archive := buildTestVSkin(t, []zipEntrySpec{
		{
			name: "skin.json",
			data: []byte(`{"schema":1,"id":"harbor","version":"1.0.0","name":"Harbor","engine":"cinematic","palette":{"canvas":"#0b1320","ink":"#eef4fb"},"swatches":["#0b1320","#4cc9f0"]}`),
		},
	})
	if _, err := skins.InstallArchive(ctx, archive); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Frontends: map[string]config.Frontend{}}
	fe := Frontend{DB: db, ReplayKey: make([]byte, 32), Skins: skins}
	settings := frontend.Settings{
		"defaultSkinId": "harbor",
		"force":         true,
		"appName":       "  Harbor Cinema  ",
		"wordmark": map[string]any{
			"url":    wm.URL,
			"width":  wm.Width,
			"height": wm.Height,
		},
	}
	if err := fe.ValidateSettings(settings); err != nil {
		t.Fatalf("ValidateSettings: %v", err)
	}
	cfg.Frontends["vela"] = config.Frontend{AppearanceRevision: 2, Settings: NormalizeSettings(settings)}

	mounted, err := fe.Mount(ctx, frontend.Host{
		Identity:   identityFake{},
		Experience: frontendtest.Experience{},
		Sessions:   frontendtest.Sessions{},
		HostConfig: func() config.Config { return cfg },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounted.Close(ctx) })
	handler := mounted.Handler()

	appRec := httptest.NewRecorder()
	handler.ServeHTTP(appRec, httptest.NewRequest(http.MethodGet, "/vela/v0/appearance", nil))
	if appRec.Code != http.StatusOK {
		t.Fatalf("GET /vela/v0/appearance = %d: %s", appRec.Code, appRec.Body.String())
	}
	var appDoc map[string]any
	if err := json.Unmarshal(appRec.Body.Bytes(), &appDoc); err != nil {
		t.Fatal(err)
	}
	if appDoc["defaultSkinId"] != "harbor" || appDoc["force"] != true {
		t.Fatalf("appearance skin/force = %#v", appDoc)
	}
	packs, _ := appDoc["packs"].([]any)
	docs, _ := appDoc["documents"].([]any)
	if len(packs) != 1 || docs == nil {
		t.Fatalf("appearance packs/documents = %#v / %#v", appDoc["packs"], appDoc["documents"])
	}
	branding, _ := appDoc["branding"].(map[string]any)
	if branding["name"] != "Harbor Cinema" || branding["wordmark"] == nil {
		t.Fatalf("appearance branding = %#v", appDoc["branding"])
	}

	bootReq := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
	bootReq.Header.Set("Authorization", "Bearer token")
	bootReq.Header.Set("X-Vela-Device-Id", "dev-1")
	bootRec := httptest.NewRecorder()
	handler.ServeHTTP(bootRec, bootReq)
	if bootRec.Code != http.StatusOK {
		t.Fatalf("GET /vela/v0/bootstrap = %d: %s", bootRec.Code, bootRec.Body.String())
	}
	var bootDoc map[string]any
	if err := json.Unmarshal(bootRec.Body.Bytes(), &bootDoc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bootDoc["branding"], appDoc["branding"]) || bootDoc["defaultSkinId"] != "harbor" || bootDoc["force"] != true {
		t.Fatalf("bootstrap merged contract = %#v", bootDoc)
	}
}
