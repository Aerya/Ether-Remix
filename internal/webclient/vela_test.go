package webclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/veladist"
)

func writeVelaFixtureFile(t *testing.T, root, rel, content string, modTime time.Time) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if !modTime.IsZero() {
		if err := os.Chtimes(full, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServeVelaBrandCacheHeadersAndRevalidation(t *testing.T) {
	dir := t.TempDir()
	baseTime := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	files := map[string]string{
		"index.html":                 `<!doctype html><html><head><title>Vela</title></head><body>shell</body></html>`,
		"manifest.webmanifest":       `{"name":"Vela","short_name":"Vela","start_url":"/vela"}`,
		"manifest.json":              `{"name":"Vela","short_name":"Vela","start_url":"/vela"}`,
		"sw.js":                      `self.addEventListener("fetch", () => {});`,
		"pwa.js":                     `window.__velaPWA = 1;`,
		"boot-handoff.js":            `window.__velaBoot = 1;`,
		"vela-icon-192.png":          "png-icon-192",
		"vela-icon-maskable-512.png": "png-icon-maskable-512",
		"favicon-32.png":             "png-favicon-32",
		"skins/catalog.json":         `{"skins":[]}`,
		"install/index.html":         `<!doctype html><html><head><title>Install Vela</title></head></html>`,
		"index-a1b2c3d4e5f60718293a4b5c6d7e8f90.html":                                         `<html>hashed html</html>`,
		"app-a1b2c3d4e5f60718293a4b5c6d7e8f90.webmanifest":                                    `{"name":"Vela"}`,
		"_expo/static/js/web/entry-a1b2c3d4e5f60718293a4b5c6d7e8f90.js":                       `console.log("entry");`,
		"_expo/static/css/web-9c8a7b6e5d4f3a2b1c0d9e8f7a6b5c4d.css":                           `body{color:#fff}`,
		"assets/fonts/Inter.a1b2c3d4e5f67890a1b2c3d4e5f67890.woff2":                           `woff2-bytes`,
		"assets/images/logo.8c2e4f1a9b3d7e6f5a4b3c2d1e0f9a8b.png":                             `hashed-png-bytes`,
		"assets/chunk-9a4b7c2e.js":                                                            `export const x = 1;`,
		"loader-0123456789abcdef0123.js":                                                      `export const loader = 1;`,
		"skins/assets/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.woff2": `skin-font`,
		"player/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/engine.wasm": `wasm-bytes`,
		"skins/cinematic/1.0.0/skin.json":                                                     `{"id":"cinematic"}`,
	}
	for rel, content := range files {
		writeVelaFixtureFile(t, dir, rel, content, baseTime)
	}

	// Trailing slash redirect /vela/ -> /vela gets no-cache, never no-store.
	redir := httptest.NewRecorder()
	ServeVelaBrand(redir, httptest.NewRequest(http.MethodGet, "/vela/?tab=live", nil), dir, "Cinema")
	if redir.Code != http.StatusPermanentRedirect || redir.Header().Get("Location") != "/vela?tab=live" {
		t.Fatalf("/vela/ redirect = %d location=%q", redir.Code, redir.Header().Get("Location"))
	}
	if cc := redir.Header().Get("Cache-Control"); cc != veladist.CacheControlRevalidate {
		t.Fatalf("/vela/ Cache-Control = %q, want %q", cc, veladist.CacheControlRevalidate)
	}

	table := []struct {
		urlPath     string
		wantCC      string
		wantCT      string
		wantSWScope string
	}{
		// Stable shell files -> no-cache + ETag + Last-Modified.
		{urlPath: "/vela", wantCC: veladist.CacheControlRevalidate, wantCT: "text/html"},
		{urlPath: "/vela/index.html", wantCC: veladist.CacheControlRevalidate, wantCT: "text/html"},
		{urlPath: "/vela/live", wantCC: veladist.CacheControlRevalidate, wantCT: "text/html"},
		{urlPath: "/vela/manifest.webmanifest", wantCC: veladist.CacheControlRevalidate, wantCT: "application/manifest+json"},
		{urlPath: "/vela/manifest.json", wantCC: veladist.CacheControlRevalidate, wantCT: "application/manifest+json"},
		{urlPath: "/vela/sw.js", wantCC: veladist.CacheControlRevalidate, wantCT: "text/javascript", wantSWScope: "/vela"},
		{urlPath: "/vela/pwa.js", wantCC: veladist.CacheControlRevalidate, wantCT: "text/javascript"},
		{urlPath: "/vela/boot-handoff.js", wantCC: veladist.CacheControlRevalidate, wantCT: "text/javascript"},
		{urlPath: "/vela/vela-icon-192.png", wantCC: veladist.CacheControlRevalidate},
		{urlPath: "/vela/vela-icon-maskable-512.png", wantCC: veladist.CacheControlRevalidate},
		{urlPath: "/vela/favicon-32.png", wantCC: veladist.CacheControlRevalidate},
		{urlPath: "/vela/skins/catalog.json", wantCC: veladist.CacheControlRevalidate},
		{urlPath: "/vela/install", wantCC: veladist.CacheControlRevalidate, wantCT: "text/html"},
		{urlPath: "/vela/install/index.html", wantCC: veladist.CacheControlRevalidate, wantCT: "text/html"},
		{urlPath: "/vela/index-a1b2c3d4e5f60718293a4b5c6d7e8f90.html", wantCC: veladist.CacheControlRevalidate, wantCT: "text/html"},
		{urlPath: "/vela/app-a1b2c3d4e5f60718293a4b5c6d7e8f90.webmanifest", wantCC: veladist.CacheControlRevalidate, wantCT: "application/manifest+json"},

		// Content-hashed and version-addressed assets -> 1-year immutable.
		{urlPath: "/vela/_expo/static/js/web/entry-a1b2c3d4e5f60718293a4b5c6d7e8f90.js", wantCC: veladist.CacheControlImmutable},
		{urlPath: "/vela/_expo/static/css/web-9c8a7b6e5d4f3a2b1c0d9e8f7a6b5c4d.css", wantCC: veladist.CacheControlImmutable},
		{urlPath: "/vela/assets/fonts/Inter.a1b2c3d4e5f67890a1b2c3d4e5f67890.woff2", wantCC: veladist.CacheControlImmutable},
		{urlPath: "/vela/assets/images/logo.8c2e4f1a9b3d7e6f5a4b3c2d1e0f9a8b.png", wantCC: veladist.CacheControlImmutable},
		{urlPath: "/vela/assets/chunk-9a4b7c2e.js", wantCC: veladist.CacheControlImmutable},
		{urlPath: "/vela/loader-0123456789abcdef0123.js", wantCC: veladist.CacheControlImmutable},
		{urlPath: "/vela/skins/assets/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.woff2", wantCC: veladist.CacheControlImmutable},
		{urlPath: "/vela/player/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/engine.wasm", wantCC: veladist.CacheControlImmutable, wantCT: "application/wasm"},
		{urlPath: "/vela/skins/cinematic/1.0.0/skin.json", wantCC: veladist.CacheControlImmutable},
	}

	for _, tc := range table {
		t.Run(tc.urlPath, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ServeVelaBrand(rec, httptest.NewRequest(http.MethodGet, tc.urlPath, nil), dir, "Cinema")
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d body=%q", tc.urlPath, rec.Code, rec.Body.String())
			}
			cc := rec.Header().Get("Cache-Control")
			if cc != tc.wantCC {
				t.Fatalf("GET %s Cache-Control = %q, want %q", tc.urlPath, cc, tc.wantCC)
			}
			if strings.Contains(cc, "no-store") {
				t.Fatalf("GET %s unexpectedly set no-store: %q", tc.urlPath, cc)
			}
			if tc.wantCT != "" && !strings.Contains(rec.Header().Get("Content-Type"), tc.wantCT) {
				t.Fatalf("GET %s Content-Type = %q, want substring %q", tc.urlPath, rec.Header().Get("Content-Type"), tc.wantCT)
			}
			if tc.wantSWScope != "" && rec.Header().Get("Service-Worker-Allowed") != tc.wantSWScope {
				t.Fatalf("GET %s Service-Worker-Allowed = %q, want %q", tc.urlPath, rec.Header().Get("Service-Worker-Allowed"), tc.wantSWScope)
			}
			etag := rec.Header().Get("ETag")
			lastMod := rec.Header().Get("Last-Modified")
			if etag == "" || lastMod == "" {
				t.Fatalf("GET %s missing validators: ETag=%q Last-Modified=%q", tc.urlPath, etag, lastMod)
			}

			// Matching If-None-Match revalidates cleanly with 304 Not Modified.
			req304 := httptest.NewRequest(http.MethodGet, tc.urlPath, nil)
			req304.Header.Set("If-None-Match", etag)
			rec304 := httptest.NewRecorder()
			ServeVelaBrand(rec304, req304, dir, "Cinema")
			if rec304.Code != http.StatusNotModified {
				t.Fatalf("GET %s with If-None-Match=%s = %d, want 304", tc.urlPath, etag, rec304.Code)
			}
			if rec304.Header().Get("Cache-Control") != tc.wantCC {
				t.Fatalf("304 %s Cache-Control = %q, want %q", tc.urlPath, rec304.Header().Get("Cache-Control"), tc.wantCC)
			}
		})
	}

	// Verify /vela/index.html receives branded <title> just like /vela.
	idxRec := httptest.NewRecorder()
	ServeVelaBrand(idxRec, httptest.NewRequest(http.MethodGet, "/vela/index.html", nil), dir, "Cinema")
	if !strings.Contains(idxRec.Body.String(), "<title>Cinema</title>") {
		t.Fatalf("/vela/index.html body = %q, want branded title", idxRec.Body.String())
	}

	// Verify a stable shell script invalidates its ETag when bytes change even with identical size and mtime.
	pwaFirst := httptest.NewRecorder()
	ServeVelaBrand(pwaFirst, httptest.NewRequest(http.MethodGet, "/vela/pwa.js", nil), dir, "Cinema")
	writeVelaFixtureFile(t, dir, "pwa.js", `window.__velaPWA = 2;`, baseTime)
	pwaSecondReq := httptest.NewRequest(http.MethodGet, "/vela/pwa.js", nil)
	pwaSecondReq.Header.Set("If-None-Match", pwaFirst.Header().Get("ETag"))
	pwaSecond := httptest.NewRecorder()
	ServeVelaBrand(pwaSecond, pwaSecondReq, dir, "Cinema")
	if pwaSecond.Code != http.StatusOK || pwaSecond.Header().Get("ETag") == pwaFirst.Header().Get("ETag") {
		t.Fatalf("pwa.js did not revalidate after in-place byte update: code=%d old=%q new=%q", pwaSecond.Code, pwaFirst.Header().Get("ETag"), pwaSecond.Header().Get("ETag"))
	}

	// Missing sw.js returns 404 with no-cache, never the SPA shell.
	noSWDir := t.TempDir()
	writeVelaFixtureFile(t, noSWDir, "index.html", `<html><title>Vela</title></html>`, baseTime)
	sw404 := httptest.NewRecorder()
	ServeVelaBrand(sw404, httptest.NewRequest(http.MethodGet, "/vela/sw.js", nil), noSWDir, "")
	if sw404.Code != http.StatusNotFound || sw404.Header().Get("Cache-Control") != veladist.CacheControlRevalidate {
		t.Fatalf("missing sw.js = %d cc=%q", sw404.Code, sw404.Header().Get("Cache-Control"))
	}
}

func TestServeVelaWithBrandManifestAndHeaders(t *testing.T) {
	dir := t.TempDir()
	htmlDoc := `<!doctype html><html><head><title>Default Release Title</title><link rel="manifest" href="/vela/manifest.webmanifest" /><link rel="icon" href="/vela/vela-icon.svg" /><link rel="apple-touch-icon" href="/vela/vela-icon-180.png" /></head><body></body></html>`
	manifestDoc := `{"id":"/vela","name":"Default Release","short_name":"DefRel","start_url":"/vela","scope":"/vela","display":"standalone","icons":[{"src":"/vela/vela-icon-192.png","sizes":"192x192","type":"image/png","purpose":"any"}]}`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(htmlDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.webmanifest"), []byte(manifestDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vela-icon-180.png"), []byte("release-180"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Empty brand keeps release title, manifest name/short_name, and release icons with no-cache + ETag.
	w := httptest.NewRecorder()
	ServeVelaWithBrand(w, httptest.NewRequest(http.MethodGet, "/vela/manifest.webmanifest", nil), dir, VelaBrand{})
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("ETag") == "" {
		t.Fatalf("manifest headers: code=%d cc=%q etag=%q", w.Code, w.Header().Get("Cache-Control"), w.Header().Get("ETag"))
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "Default Release" || m["short_name"] != "DefRel" {
		t.Fatalf("expected release manifest fallback, got %#v", m)
	}

	// Custom brand patches name, short_name, icons, HTML title, and PNG favicon link.
	brand := VelaBrand{
		AppName:     "Cinema House",
		IconVersion: "v1",
		IconIsSVG:   false,
		IconAsset: func(name string) (VelaIconAsset, bool) {
			return VelaIconAsset{
				ContentType: "image/png",
				ETag:        `"icon-` + name + `"`,
				Data:        []byte("custom-" + name),
			}, true
		},
	}

	wHTML := httptest.NewRecorder()
	ServeVelaWithBrand(wHTML, httptest.NewRequest(http.MethodGet, "/vela/index.html", nil), dir, brand)
	if !strings.Contains(wHTML.Body.String(), "<title>Cinema House</title>") || !strings.Contains(wHTML.Body.String(), `href="/vela/favicon-32.png"`) {
		t.Fatalf("patched index.html unexpected: %s", wHTML.Body.String())
	}

	wIcon := httptest.NewRecorder()
	ServeVelaWithBrand(wIcon, httptest.NewRequest(http.MethodGet, "/vela/vela-icon-180.png", nil), dir, brand)
	if wIcon.Code != http.StatusOK || wIcon.Body.String() != "custom-vela-icon-180.png" || wIcon.Header().Get("Cache-Control") != "no-cache" || wIcon.Header().Get("ETag") != `"icon-vela-icon-180.png"` {
		t.Fatalf("custom icon serve: code=%d cc=%q etag=%q body=%q", wIcon.Code, wIcon.Header().Get("Cache-Control"), wIcon.Header().Get("ETag"), wIcon.Body.String())
	}

	condReq := httptest.NewRequest(http.MethodGet, "/vela/vela-icon-180.png", nil)
	condReq.Header.Set("If-None-Match", wIcon.Header().Get("ETag"))
	w304 := httptest.NewRecorder()
	ServeVelaWithBrand(w304, condReq, dir, brand)
	if w304.Code != http.StatusNotModified {
		t.Fatalf("conditional icon status = %d, want 304", w304.Code)
	}
}
