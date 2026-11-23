package vela

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
)

func makeSquarePNG(t *testing.T, w, h int, bg, center color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := bg
			if x >= w/4 && x < 3*w/4 && y >= h/4 && y < 3*h/4 {
				c = center
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAppIconUploadValidationAndPruning(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()

	// Valid square PNG (transparent background with opaque center emblem).
	pngData := makeSquarePNG(t, 256, 256, color.RGBA{0, 0, 0, 0}, color.RGBA{0xe8, 0x5d, 0x04, 0xff})
	iconPNG, err := UploadAppIcon(ctx, db, bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("UploadAppIcon(png): %v", err)
	}
	if iconPNG.Width != 256 || iconPNG.Height != 256 || !strings.HasPrefix(iconPNG.URL, "/vela/v0/branding/icon-") || !strings.HasSuffix(iconPNG.URL, ".png") {
		t.Fatalf("unexpected PNG icon: %+v", iconPNG)
	}

	// Valid square SVG with vector paths.
	svgData := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128"><rect width="128" height="128" fill="#1d4ed8"/><circle cx="64" cy="64" r="36" fill="#ffffff"/></svg>`
	iconSVG, err := UploadAppIcon(ctx, db, strings.NewReader(svgData))
	if err != nil {
		t.Fatalf("UploadAppIcon(svg): %v", err)
	}
	if iconSVG.Width != 128 || iconSVG.Height != 128 || !strings.HasSuffix(iconSVG.URL, ".svg") {
		t.Fatalf("unexpected SVG icon: %+v", iconSVG)
	}

	// Valid square WebP.
	iconWebP, err := UploadAppIcon(ctx, db, bytes.NewReader(sampleWebP(t)))
	if err != nil {
		t.Fatalf("UploadAppIcon(webp): %v", err)
	}
	if iconWebP.Width != 1 || iconWebP.Height != 1 || !strings.HasSuffix(iconWebP.URL, ".webp") {
		t.Fatalf("unexpected WebP icon: %+v", iconWebP)
	}

	// Reject non-square PNG.
	if _, err := UploadAppIcon(ctx, db, bytes.NewReader(makeSquarePNG(t, 200, 100, color.RGBA{10, 20, 30, 255}, color.RGBA{255, 255, 255, 255}))); err == nil {
		t.Fatal("expected non-square PNG to fail")
	}

	// Reject oversized dimensions (> 1024x1024).
	if _, err := UploadAppIcon(ctx, db, bytes.NewReader(makeSquarePNG(t, 1025, 1025, color.RGBA{10, 20, 30, 255}, color.RGBA{255, 255, 255, 255}))); err == nil {
		t.Fatal("expected >1024x1024 PNG to fail")
	}

	// Reject > 512 KB upload.
	if _, err := UploadAppIcon(ctx, db, bytes.NewReader(make([]byte, MaxAppIconBytes+1))); !errors.Is(err, ErrAppIconTooLarge) {
		t.Fatalf("expected ErrAppIconTooLarge, got %v", err)
	}

	// Reject unsafe SVGs and empty unrenderable SVGs.
	for name, bad := range map[string]string{
		"script":    `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><script>alert(1)</script><rect width="64" height="64"/></svg>`,
		"onload":    `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" onload="alert(1)"><rect width="64" height="64"/></svg>`,
		"external":  `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><use href="https://evil.example/x.svg#a"/></svg>`,
		"nonsquare": `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 60"><rect width="120" height="60"/></svg>`,
		"no-paths":  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><text x="10" y="32">Hi</text></svg>`,
	} {
		if _, err := UploadAppIcon(ctx, db, strings.NewReader(bad)); err == nil {
			t.Fatalf("expected %s SVG icon to be rejected", name)
		}
	}

	// PruneAppIcons drops old unreferenced icons and their variants while keeping referenced ones.
	stale := time.Now().Add(-2 * time.Hour).Unix()
	if _, err := db.Exec(`UPDATE vela_app_icons SET created_at = ?`, stale); err != nil {
		t.Fatal(err)
	}
	PruneAppIcons(ctx, db, map[string]any{
		"appIcon": map[string]any{"url": iconPNG.URL, "width": iconPNG.Width, "height": iconPNG.Height},
	})
	var keptCount, droppedVariants int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vela_app_icons WHERE digest = ?`, iconPNG.Digest).Scan(&keptCount); err != nil || keptCount != 1 {
		t.Fatalf("referenced icon pruned: count=%d err=%v", keptCount, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM vela_app_icon_variants WHERE digest = ?`, iconSVG.Digest).Scan(&droppedVariants); err != nil || droppedVariants != 0 {
		t.Fatalf("unreferenced icon variants not pruned: count=%d err=%v", droppedVariants, err)
	}
}

func TestVelaBrandedPWAManifestAndIcons(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()

	// Seed a realistic release directory with release manifest and release icon files.
	assetsDir := t.TempDir()
	releaseHTML := `<!doctype html><html><head><title>Release Vela</title><link rel="manifest" href="/vela/manifest.webmanifest" /><link rel="icon" href="/vela/vela-icon.svg" /><link rel="apple-touch-icon" href="/vela/vela-icon-180.png" /></head><body></body></html>`
	releaseManifest := `{"id":"/vela","name":"Release Vela","short_name":"RelVela","start_url":"/vela","scope":"/vela","display":"standalone","theme_color":"#090B0E","background_color":"#090B0E","icons":[{"src":"/vela/vela-icon-192.png","sizes":"192x192","type":"image/png","purpose":"any"}]}`
	releaseIconSVG := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" fill="#111"/></svg>`
	release180 := makeSquarePNG(t, 180, 180, color.RGBA{0x11, 0x22, 0x33, 0xff}, color.RGBA{0x11, 0x22, 0x33, 0xff})
	release192 := makeSquarePNG(t, 192, 192, color.RGBA{0x11, 0x22, 0x33, 0xff}, color.RGBA{0x11, 0x22, 0x33, 0xff})

	for name, content := range map[string][]byte{
		"index.html":           []byte(releaseHTML),
		"manifest.webmanifest": []byte(releaseManifest),
		"vela-icon.svg":        []byte(releaseIconSVG),
		"vela-icon-180.png":    release180,
		"vela-icon-192.png":    release192,
	} {
		if err := os.WriteFile(filepath.Join(assetsDir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
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
	h := mounted.Handler()

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	// 1. With no appName and no appIcon, manifest falls back to the release file's values and serves release icons with no-cache + ETag.
	man0 := get("/vela/manifest.webmanifest")
	if man0.Code != http.StatusOK || man0.Header().Get("Cache-Control") != "no-cache" || man0.Header().Get("ETag") == "" {
		t.Fatalf("default manifest headers: code=%d cc=%q etag=%q", man0.Code, man0.Header().Get("Cache-Control"), man0.Header().Get("ETag"))
	}
	var manDoc0 map[string]any
	if err := json.Unmarshal(man0.Body.Bytes(), &manDoc0); err != nil {
		t.Fatal(err)
	}
	if manDoc0["name"] != "Release Vela" || manDoc0["short_name"] != "RelVela" {
		t.Fatalf("expected fallback to release manifest name/short_name, got %#v", manDoc0)
	}
	if icons0, ok := manDoc0["icons"].([]any); !ok || len(icons0) != 1 {
		t.Fatalf("expected release manifest icons preserved when no owner icon is set: %#v", manDoc0["icons"])
	}
	relIconResp := get("/vela/vela-icon-180.png")
	if relIconResp.Code != http.StatusOK || !bytes.Equal(relIconResp.Body.Bytes(), release180) {
		t.Fatalf("expected release 180 icon when unset: code=%d len=%d", relIconResp.Code, relIconResp.Body.Len())
	}
	if relIconResp.Header().Get("Cache-Control") != "no-cache" || relIconResp.Header().Get("ETag") == "" {
		t.Fatalf("release icon must carry no-cache and ETag: cc=%q etag=%q", relIconResp.Header().Get("Cache-Control"), relIconResp.Header().Get("ETag"))
	}
	// Conditional 304 on release icon.
	condRel := httptest.NewRequest(http.MethodGet, "/vela/vela-icon-180.png", nil)
	condRel.Header.Set("If-None-Match", relIconResp.Header().Get("ETag"))
	condRelRec := httptest.NewRecorder()
	h.ServeHTTP(condRelRec, condRel)
	if condRelRec.Code != http.StatusNotModified {
		t.Fatalf("release icon 304: got %d", condRelRec.Code)
	}

	// 2. Upload a transparent square PNG icon and apply branded name + icon.
	ownerPNG := makeSquarePNG(t, 256, 256, color.RGBA{0, 0, 0, 0}, color.RGBA{0x22, 0xc5, 0x5e, 0xff})
	uploadedPNG, err := UploadAppIcon(ctx, db, bytes.NewReader(ownerPNG))
	if err != nil {
		t.Fatal(err)
	}
	// Preview asset endpoint serves uploaded icon immutably.
	previewRec := get(uploadedPNG.URL)
	if previewRec.Code != http.StatusOK || !bytes.Equal(previewRec.Body.Bytes(), ownerPNG) {
		t.Fatalf("icon preview asset failed: code=%d", previewRec.Code)
	}

	settings := frontend.Settings{
		"appName": "Starlight Cinema",
		"appIcon": map[string]any{"url": uploadedPNG.URL, "width": uploadedPNG.Width, "height": uploadedPNG.Height},
	}
	if err := fe.ValidateSettings(settings); err != nil {
		t.Fatalf("ValidateSettings: %v", err)
	}
	cfg.Frontends["vela"] = config.Frontend{AppearanceRevision: 1, Settings: NormalizeSettings(settings)}

	// Branded manifest has owner name/short_name, 4 PNG icons (192/512 any + maskable), no-cache, and updated ETag.
	man1 := get("/vela/manifest.webmanifest")
	if man1.Code != http.StatusOK || man1.Header().Get("Cache-Control") != "no-cache" || man1.Header().Get("ETag") == man0.Header().Get("ETag") {
		t.Fatalf("branded manifest headers: code=%d cc=%q etag=%q", man1.Code, man1.Header().Get("Cache-Control"), man1.Header().Get("ETag"))
	}
	var manDoc1 struct {
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
		Icons     []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(man1.Body.Bytes(), &manDoc1); err != nil {
		t.Fatal(err)
	}
	if manDoc1.Name != "Starlight Cinema" || manDoc1.ShortName != "Starlight Cinema" {
		t.Fatalf("branded manifest name = %q / %q", manDoc1.Name, manDoc1.ShortName)
	}
	if len(manDoc1.Icons) != 4 {
		t.Fatalf("branded manifest icons len = %d, want 4: %+v", len(manDoc1.Icons), manDoc1.Icons)
	}

	// Verify all generated PNG dimensions, headers, and safe-zone padding on maskable icons.
	for _, tc := range []struct {
		path     string
		size     int
		maskable bool
		opaque   bool
	}{
		{"/vela/vela-icon-192.png", 192, false, false},
		{"/vela/vela-icon-512.png", 512, false, false},
		{"/vela/vela-icon-maskable-192.png", 192, true, true},
		{"/vela/vela-icon-maskable-512.png", 512, true, true},
		{"/vela/vela-icon-180.png", 180, false, true},
		{"/vela/favicon-16.png", 16, false, false},
		{"/vela/favicon-32.png", 32, false, false},
		{"/vela/favicon-48.png", 48, false, false},
	} {
		rec := get(tc.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", tc.path, rec.Code)
		}
		if rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("ETag") == "" {
			t.Fatalf("%s headers: ct=%q cc=%q etag=%q", tc.path, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"), rec.Header().Get("ETag"))
		}
		img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatalf("decode %s: %v", tc.path, err)
		}
		if img.Bounds().Dx() != tc.size || img.Bounds().Dy() != tc.size {
			t.Fatalf("%s size = %dx%d, want %dx%d", tc.path, img.Bounds().Dx(), img.Bounds().Dy(), tc.size, tc.size)
		}
		corner := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA)
		center := color.RGBAModel.Convert(img.At(tc.size/2, tc.size/2)).(color.RGBA)
		if tc.opaque && corner.A != 255 {
			t.Fatalf("%s expected opaque corner, got %+v", tc.path, corner)
		}
		if tc.maskable {
			// Safe-zone border must be filled with fallback #090B0E, while center holds the green emblem.
			if corner != velaIconFallbackBG {
				t.Fatalf("%s maskable safe-zone corner = %+v, want %+v", tc.path, corner, velaIconFallbackBG)
			}
			if center.G < 180 || center.A != 255 {
				t.Fatalf("%s maskable center should contain icon artwork, got %+v", tc.path, center)
			}
		}
	}

	// index.html rewrites the SVG favicon link to PNG when a PNG icon is configured.
	htmlPNG := get("/vela")
	if !strings.Contains(htmlPNG.Body.String(), `<title>Starlight Cinema</title>`) {
		t.Fatalf("expected branded title in HTML: %s", htmlPNG.Body.String())
	}
	if !strings.Contains(htmlPNG.Body.String(), `href="/vela/favicon-32.png"`) {
		t.Fatalf("expected PNG favicon rewrite in HTML: %s", htmlPNG.Body.String())
	}

	// 3. Switch to a solid-background SVG icon: maskable icon extends the solid corner color and /vela/vela-icon.svg serves owner's SVG.
	ownerSVG := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128"><rect width="128" height="128" fill="#dc2626"/><circle cx="64" cy="64" r="32" fill="#ffffff"/></svg>`
	uploadedSVG, err := UploadAppIcon(ctx, db, strings.NewReader(ownerSVG))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Frontends["vela"] = config.Frontend{
		AppearanceRevision: 2,
		Settings: NormalizeSettings(frontend.Settings{
			"appName": "Crimson Cinema",
			"appIcon": map[string]any{"url": uploadedSVG.URL, "width": uploadedSVG.Width, "height": uploadedSVG.Height},
		}),
	}
	htmlSVG := get("/vela")
	if !strings.Contains(htmlSVG.Body.String(), `href="/vela/vela-icon.svg"`) {
		t.Fatalf("SVG icon should keep /vela/vela-icon.svg link: %s", htmlSVG.Body.String())
	}
	svgServed := get("/vela/vela-icon.svg")
	if svgServed.Code != http.StatusOK || svgServed.Header().Get("Content-Type") != "image/svg+xml" || svgServed.Header().Get("Content-Security-Policy") == "" || svgServed.Body.String() != ownerSVG {
		t.Fatalf("served SVG icon mismatch: code=%d ct=%q csp=%q", svgServed.Code, svgServed.Header().Get("Content-Type"), svgServed.Header().Get("Content-Security-Policy"))
	}
	maskSVG := get("/vela/vela-icon-maskable-192.png")
	maskImg, err := png.Decode(bytes.NewReader(maskSVG.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	corner := color.RGBAModel.Convert(maskImg.At(0, 0)).(color.RGBA)
	if corner.R < 200 || corner.G > 60 || corner.B > 60 || corner.A != 255 {
		t.Fatalf("solid-color icon maskable border should bleed red corner color, got %+v", corner)
	}

	// 4. Clearing appIcon reverts immediately to the release icons and release manifest icons.
	cfg.Frontends["vela"] = config.Frontend{AppearanceRevision: 3, Settings: map[string]any{}}
	back180 := get("/vela/vela-icon-180.png")
	if !bytes.Equal(back180.Body.Bytes(), release180) {
		t.Fatal("clearing appIcon did not revert to release icon")
	}
	backMan := get("/vela/manifest.webmanifest")
	var backDoc map[string]any
	_ = json.Unmarshal(backMan.Body.Bytes(), &backDoc)
	if backDoc["name"] != "Release Vela" || len(backDoc["icons"].([]any)) != 1 {
		t.Fatalf("clearing branding did not revert manifest to release values: %#v", backDoc)
	}
}
