package vela

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
)

type zipEntrySpec struct {
	name string
	data []byte
	mode fs.FileMode
}

func buildTestVSkin(t *testing.T, entries []zipEntrySpec) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{
			Name:   e.name,
			Method: zip.Deflate,
		}
		if e.mode != 0 {
			hdr.SetMode(e.mode)
		} else {
			hdr.SetMode(0o644)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if len(e.data) > 0 {
			if _, err := w.Write(e.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestVelaSkinsInstallServeAndBootstrap(t *testing.T) {
	db := store.OpenTest(t)
	skinsDir := filepath.Join(t.TempDir(), "vela-skins")
	skins := NewSkinStore(db, skinsDir)

	cfg := config.Config{Frontends: map[string]config.Frontend{}}
	fe := Frontend{
		DB:        db,
		ReplayKey: make([]byte, 32),
		Skins:     skins,
	}
	mounted, err := fe.Mount(context.Background(), frontend.Host{
		Identity:    identityFake{},
		Experience:  frontendtest.Experience{},
		Sessions:    frontendtest.Sessions{},
		ImageGrants: idleBootstrapImages{},
		HostConfig:  func() config.Config { return cfg },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounted.Close(context.Background()) })
	handler := mounted.Handler()

	// Built-in skins are listed in canonical display order and non-removable.
	initial := skins.List(context.Background(), "")
	wantBuiltins := []string{"cinematic-doc", "mochi", "marquee", "jellytwin", "index", "lounge", "latenight", "kids", "broadcast"}
	if len(initial) != len(wantBuiltins) {
		t.Fatalf("builtins len=%d, want %d", len(initial), len(wantBuiltins))
	}
	for idx, wantID := range wantBuiltins {
		if initial[idx].ID != wantID || !initial[idx].Builtin {
			t.Fatalf("builtin[%d]=%+v, want %s", idx, initial[idx], wantID)
		}
		if err := skins.Remove(context.Background(), wantID); !errors.Is(err, ErrBuiltinSkin) {
			t.Fatalf("Remove(%s)=%v, want ErrBuiltinSkin", wantID, err)
		}
	}
	if err := skins.Remove(context.Background(), "cinematic"); !errors.Is(err, ErrBuiltinSkin) {
		t.Fatalf("Remove(cinematic)=%v, want ErrBuiltinSkin", err)
	}

	// ValidateSettings accepts cinematic, cinematic-doc, and all built-ins before any installs.
	for _, builtinID := range append([]string{"cinematic"}, wantBuiltins...) {
		if err := fe.ValidateSettings(frontend.Settings{"defaultSkinId": builtinID, "force": true}); err != nil {
			t.Fatalf("ValidateSettings(%s): %v", builtinID, err)
		}
	}
	if err := fe.ValidateSettings(frontend.Settings{"defaultSkinId": "harbor"}); err == nil {
		t.Fatal("ValidateSettings accepted uninstalled skin id")
	}

	// Install a SkinPack (.vskin) with assets at root and under assets/.
	packJSON := `{
		"schema": 1,
		"id": "harbor",
		"version": "1.0.0",
		"name": "Harbor",
		"engine": "cinematic",
		"tagline": "Cool slate coastal palette",
		"palette": {
			"canvas": "#0b1320",
			"surface": "#132033",
			"ink": "#eaf2fb",
			"accent": "#4cc9f0"
		},
		"tokens": {
			"backdrop": "assets/backdrop.webp",
			"blur": "18px"
		},
		"fonts": {
			"display": {
				"family": "Harbor Sans",
				"files": [{"url": "harbor-sans.woff2", "weight": "600", "style": "normal"}]
			}
		},
		"radius": {"card": "12px"},
		"images": {
			"preview": "assets/preview.svg",
			"hero": "./assets/backdrop.webp"
		},
		"swatches": ["#0b1320", "#132033", "#4cc9f0"]
	}`
	svgBytes := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 9"><rect width="16" height="9" fill="#0b1320"/></svg>`)
	webpBytes := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	woff2Bytes := []byte("wOF2\x00\x01\x00\x00")

	packSummary, err := skins.InstallArchive(context.Background(), buildTestVSkin(t, []zipEntrySpec{
		{name: "skin.json", data: []byte(packJSON)},
		{name: "assets/preview.svg", data: svgBytes},
		{name: "assets/backdrop.webp", data: webpBytes},
		{name: "harbor-sans.woff2", data: woff2Bytes},
	}))
	if err != nil {
		t.Fatalf("InstallArchive(harbor 1.0.0): %v", err)
	}
	if packSummary.ID != "harbor" || packSummary.Kind != "pack" || packSummary.Preview != "/vela/v0/appearance/packs/harbor/1.0.0/preview.svg" {
		t.Fatalf("packSummary=%+v", packSummary)
	}

	// Install a SkinDocument (.vskin).
	docJSON := `{
		"schema": 1,
		"renderer": 1,
		"id": "studio-grid",
		"version": "1.2.0",
		"name": "Studio Grid",
		"swatches": ["#10141c", "#f4f7fb", "#38bdf8"],
		"preview": "assets/preview.png",
		"assets": {
			"badge": "assets/badge.svg"
		},
		"ui": {"density": "compact"},
		"routes": {"home": {"template": "grid"}}
	}`
	pngBytes := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	docSummary, err := skins.InstallArchive(context.Background(), buildTestVSkin(t, []zipEntrySpec{
		{name: "skin.json", data: []byte(docJSON)},
		{name: "assets/preview.png", data: pngBytes},
		{name: "assets/badge.svg", data: svgBytes},
	}))
	if err != nil {
		t.Fatalf("InstallArchive(studio-grid 1.2.0): %v", err)
	}
	if docSummary.ID != "studio-grid" || docSummary.Kind != "document" || docSummary.Preview != "/vela/v0/appearance/skins/studio-grid/1.2.0/preview.png" {
		t.Fatalf("docSummary=%+v", docSummary)
	}

	// ValidateSettings now accepts installed skins.
	if err := fe.ValidateSettings(frontend.Settings{"defaultSkinId": "harbor", "force": true}); err != nil {
		t.Fatalf("ValidateSettings(harbor): %v", err)
	}
	if err := fe.ValidateSettings(frontend.Settings{"defaultSkinId": "studio-grid", "force": false}); err != nil {
		t.Fatalf("ValidateSettings(studio-grid): %v", err)
	}

	// Configure harbor + force=true and inspect /vela/v0/appearance (GET and HEAD).
	cfg.Frontends["vela"] = config.Frontend{
		AppearanceRevision: 1,
		Settings:           map[string]any{"defaultSkinId": "harbor", "force": true},
	}

	appRec := httptest.NewRecorder()
	handler.ServeHTTP(appRec, httptest.NewRequest(http.MethodGet, "/vela/v0/appearance", nil))
	if appRec.Code != http.StatusOK {
		t.Fatalf("GET /appearance=%d: %s", appRec.Code, appRec.Body.String())
	}
	etag1 := appRec.Header().Get("ETag")
	var appPayload struct {
		Revision        uint64              `json:"revision"`
		DefaultSkinID   string              `json:"defaultSkinId"`
		DefaultEngineID string              `json:"defaultEngineId"`
		Force           bool                `json:"force"`
		Packs           []json.RawMessage   `json:"packs"`
		Documents       []SkinDocumentEntry `json:"documents"`
	}
	if err := json.Unmarshal(appRec.Body.Bytes(), &appPayload); err != nil {
		t.Fatal(err)
	}
	if appPayload.DefaultSkinID != "harbor" || appPayload.DefaultEngineID != "cinematic" || !appPayload.Force || len(appPayload.Packs) != 1 || len(appPayload.Documents) != 1 {
		t.Fatalf("appearance payload=%+v", appPayload)
	}
	rawPack := string(appPayload.Packs[0])
	for _, wantSub := range []string{
		`"/vela/v0/appearance/packs/harbor/1.0.0/preview.svg"`,
		`"/vela/v0/appearance/packs/harbor/1.0.0/backdrop.webp"`,
		`"/vela/v0/appearance/packs/harbor/1.0.0/harbor-sans.woff2"`,
	} {
		if !strings.Contains(rawPack, wantSub) {
			t.Fatalf("rewritten pack missing %s: %s", wantSub, rawPack)
		}
	}
	if appPayload.Documents[0].URL != "/vela/v0/appearance/skins/studio-grid/1.2.0.json" {
		t.Fatalf("document entry=%+v", appPayload.Documents[0])
	}

	headApp := httptest.NewRecorder()
	handler.ServeHTTP(headApp, httptest.NewRequest(http.MethodHead, "/vela/v0/appearance", nil))
	if headApp.Code != http.StatusOK || headApp.Body.Len() != 0 || headApp.Header().Get("ETag") != etag1 {
		t.Fatalf("HEAD /appearance=%d etag=%q len=%d", headApp.Code, headApp.Header().Get("ETag"), headApp.Body.Len())
	}

	// Public immutable asset serving + headers + CSP + HEAD.
	checkAsset := func(reqPath, wantType, wantCSP string, wantBody []byte) {
		t.Helper()
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(method, reqPath, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("%s %s = %d: %s", method, reqPath, w.Code, w.Body.String())
			}
			if got := w.Header().Get("Content-Type"); got != wantType {
				t.Fatalf("%s %s Content-Type=%q, want %q", method, reqPath, got, wantType)
			}
			if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
				t.Fatalf("%s %s Cache-Control=%q", method, reqPath, got)
			}
			if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("%s %s X-Content-Type-Options=%q", method, reqPath, got)
			}
			if got := w.Header().Get("Content-Security-Policy"); got != wantCSP {
				t.Fatalf("%s %s CSP=%q, want %q", method, reqPath, got, wantCSP)
			}
			if method == http.MethodHead {
				if w.Body.Len() != 0 {
					t.Fatalf("HEAD %s wrote body", reqPath)
				}
			} else if wantBody != nil && !bytes.Equal(w.Body.Bytes(), wantBody) {
				t.Fatalf("GET %s body mismatch", reqPath)
			}
		}
	}

	checkAsset("/vela/v0/appearance/packs/harbor/1.0.0/preview.svg", "image/svg+xml", "default-src 'none'; style-src 'unsafe-inline'; sandbox", svgBytes)
	checkAsset("/vela/v0/appearance/packs/harbor/1.0.0/backdrop.webp", "image/webp", "", webpBytes)
	checkAsset("/vela/v0/appearance/packs/harbor/1.0.0/harbor-sans.woff2", "font/woff2", "", woff2Bytes)
	checkAsset("/vela/v0/appearance/skins/studio-grid/1.2.0.json", "application/json", "", nil)
	checkAsset("/vela/v0/appearance/skins/studio-grid/1.2.0/badge.svg", "image/svg+xml", "default-src 'none'; style-src 'unsafe-inline'; sandbox", svgBytes)

	// Pack skin.json is embedded in /appearance and not exposed as a raw file asset.
	packSkinDirect := httptest.NewRecorder()
	handler.ServeHTTP(packSkinDirect, httptest.NewRequest(http.MethodGet, "/vela/v0/appearance/packs/harbor/1.0.0/skin.json", nil))
	if packSkinDirect.Code != http.StatusNotFound {
		t.Fatalf("GET pack skin.json=%d, want 404", packSkinDirect.Code)
	}

	// Served SkinDocument JSON has rewritten asset paths.
	docJSONRec := httptest.NewRecorder()
	handler.ServeHTTP(docJSONRec, httptest.NewRequest(http.MethodGet, "/vela/v0/appearance/skins/studio-grid/1.2.0.json", nil))
	if !strings.Contains(docJSONRec.Body.String(), `"/vela/v0/appearance/skins/studio-grid/1.2.0/badge.svg"`) {
		t.Fatalf("served document JSON missing rewritten asset: %s", docJSONRec.Body.String())
	}

	// Bootstrap includes defaultSkinId, force, packs, documents, and appearance.
	bootReq := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
	bootReq.Header.Set("Authorization", "Bearer token")
	bootReq.Header.Set("X-Vela-Device-Id", "device-1")
	bootRec := httptest.NewRecorder()
	handler.ServeHTTP(bootRec, bootReq)
	if bootRec.Code != http.StatusOK {
		t.Fatalf("GET /bootstrap=%d: %s", bootRec.Code, bootRec.Body.String())
	}
	var bootPayload struct {
		DefaultSkinID string              `json:"defaultSkinId"`
		Force         bool                `json:"force"`
		Packs         []json.RawMessage   `json:"packs"`
		Documents     []SkinDocumentEntry `json:"documents"`
		Appearance    struct {
			DefaultSkinID string `json:"defaultSkinId"`
			Force         bool   `json:"force"`
		} `json:"appearance"`
	}
	if err := json.Unmarshal(bootRec.Body.Bytes(), &bootPayload); err != nil {
		t.Fatal(err)
	}
	if bootPayload.DefaultSkinID != "harbor" || !bootPayload.Force || len(bootPayload.Packs) != 1 || len(bootPayload.Documents) != 1 || bootPayload.Appearance.DefaultSkinID != "harbor" || !bootPayload.Appearance.Force {
		t.Fatalf("bootstrap skin fields=%+v", bootPayload)
	}

	// Atomic version upgrade replaces 1.0.0 with 1.1.0, cleans disk, 404s old version, and bumps ETag.
	packV2 := strings.Replace(packJSON, `"1.0.0"`, `"1.1.0"`, 1)
	if _, err := skins.InstallArchive(context.Background(), buildTestVSkin(t, []zipEntrySpec{
		{name: "skin.json", data: []byte(packV2)},
		{name: "assets/preview.svg", data: svgBytes},
		{name: "assets/backdrop.webp", data: webpBytes},
		{name: "harbor-sans.woff2", data: woff2Bytes},
	})); err != nil {
		t.Fatalf("InstallArchive(harbor 1.1.0): %v", err)
	}
	if _, err := os.Stat(filepath.Join(skinsDir, "harbor", "1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("old version directory still exists: %v", err)
	}
	oldAsset := httptest.NewRecorder()
	handler.ServeHTTP(oldAsset, httptest.NewRequest(http.MethodGet, "/vela/v0/appearance/packs/harbor/1.0.0/preview.svg", nil))
	if oldAsset.Code != http.StatusNotFound {
		t.Fatalf("old version asset status=%d, want 404", oldAsset.Code)
	}
	checkAsset("/vela/v0/appearance/packs/harbor/1.1.0/preview.svg", "image/svg+xml", "default-src 'none'; style-src 'unsafe-inline'; sandbox", svgBytes)

	appRec2 := httptest.NewRecorder()
	handler.ServeHTTP(appRec2, httptest.NewRequest(http.MethodGet, "/vela/v0/appearance", nil))
	if appRec2.Header().Get("ETag") == etag1 {
		t.Fatal("appearance ETag did not bump after version upgrade")
	}

	// cinematic-doc selects doc engine; removing active installed skin falls back to cinematic.
	cfg.Frontends["vela"] = config.Frontend{
		AppearanceRevision: 2,
		Settings:           map[string]any{"defaultSkinId": "cinematic-doc", "force": false},
	}
	cinDocRec := httptest.NewRecorder()
	handler.ServeHTTP(cinDocRec, httptest.NewRequest(http.MethodGet, "/vela/v0/appearance", nil))
	if !strings.Contains(cinDocRec.Body.String(), `"defaultSkinId":"cinematic-doc"`) || !strings.Contains(cinDocRec.Body.String(), `"defaultEngineId":"doc"`) {
		t.Fatalf("cinematic-doc appearance=%s", cinDocRec.Body.String())
	}

	cfg.Frontends["vela"] = config.Frontend{
		AppearanceRevision: 3,
		Settings:           map[string]any{"defaultSkinId": "harbor", "force": true},
	}
	if err := skins.Remove(context.Background(), "harbor"); err != nil {
		t.Fatalf("Remove(harbor): %v", err)
	}
	afterRemove := httptest.NewRecorder()
	handler.ServeHTTP(afterRemove, httptest.NewRequest(http.MethodGet, "/vela/v0/appearance", nil))
	if !strings.Contains(afterRemove.Body.String(), `"defaultSkinId":"cinematic"`) {
		t.Fatalf("removed skin did not fall back to cinematic: %s", afterRemove.Body.String())
	}
}

func TestVelaSkinArchiveRejections(t *testing.T) {
	validPack := `{"schema":1,"id":"slate","version":"1.0.0","name":"Slate","engine":"cinematic","palette":{"canvas":"#111"},"swatches":["#111"]}`

	cases := []struct {
		name    string
		entries []zipEntrySpec
		wantErr error
	}{
		{
			name: "missing skin.json",
			entries: []zipEntrySpec{
				{name: "preview.png", data: []byte("png")},
			},
			wantErr: ErrSkinInvalid,
		},
		{
			name: "reserved builtin id",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(`{"schema":1,"id":"mochi","version":"1.0.0","name":"Mochi","engine":"cinematic","palette":{"canvas":"#111"},"swatches":["#111"]}`)},
			},
			wantErr: ErrBuiltinSkin,
		},
		{
			name: "reserved cinematic-doc id",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(`{"schema":1,"id":"cinematic-doc","version":"1.0.0","name":"Cinematic","engine":"cinematic","palette":{"canvas":"#111"},"swatches":["#111"]}`)},
			},
			wantErr: ErrBuiltinSkin,
		},
		{
			name: "path traversal",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(validPack)},
				{name: "../evil.png", data: []byte("png")},
			},
			wantErr: ErrSkinInvalid,
		},
		{
			name: "absolute path",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(validPack)},
				{name: "/etc/passwd", data: []byte("root")},
			},
			wantErr: ErrSkinInvalid,
		},
		{
			name: "symlink entry",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(validPack)},
				{name: "assets/link.png", data: []byte("../target"), mode: fs.ModeSymlink | 0o777},
			},
			wantErr: ErrSkinInvalid,
		},
		{
			name: "nested zip extension",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(validPack)},
				{name: "assets/inner.zip", data: []byte("abc")},
			},
			wantErr: ErrSkinInvalid,
		},
		{
			name: "nested archive magic bytes disguised as png",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(validPack)},
				{name: "assets/disguised.png", data: []byte("PK\x03\x04fakezip")},
			},
			wantErr: ErrSkinInvalid,
		},
		{
			name: "duplicate asset names across root and assets",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(validPack)},
				{name: "preview.png", data: []byte("one")},
				{name: "assets/preview.png", data: []byte("two")},
			},
			wantErr: ErrSkinInvalid,
		},
		{
			name: "disallowed extension",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(validPack)},
				{name: "assets/script.js", data: []byte("alert(1)")},
			},
			wantErr: ErrSkinInvalid,
		},
		{
			name: "external URL in images",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(`{"schema":1,"id":"slate","version":"1.0.0","name":"Slate","engine":"cinematic","palette":{"canvas":"#111"},"images":{"preview":"https://example.com/p.png"},"swatches":["#111"]}`)},
			},
			wantErr: ErrSkinInvalid,
		},
		{
			name: "missing referenced asset",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(`{"schema":1,"id":"slate","version":"1.0.0","name":"Slate","engine":"cinematic","palette":{"canvas":"#111"},"images":{"preview":"assets/missing.png"},"swatches":["#111"]}`)},
			},
			wantErr: ErrSkinInvalid,
		},
		{
			name: "pack skin.json exceeds 64 KB",
			entries: []zipEntrySpec{
				{name: "skin.json", data: []byte(fmt.Sprintf(`{"schema":1,"id":"slate","version":"1.0.0","name":"Slate","engine":"cinematic","palette":{"canvas":"#111"},"tokens":{"pad":%q},"swatches":["#111"]}`, strings.Repeat("a", 65*1024)))},
			},
			wantErr: ErrSkinInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := unpackAndValidateVSkin(buildTestVSkin(t, tc.entries))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
		})
	}

	t.Run("exceeds 200 files", func(t *testing.T) {
		entries := make([]zipEntrySpec, 0, 202)
		entries = append(entries, zipEntrySpec{name: "skin.json", data: []byte(validPack)})
		for i := 0; i < 201; i++ {
			entries = append(entries, zipEntrySpec{
				name: fmt.Sprintf("assets/f%03d.png", i),
				data: []byte("png"),
			})
		}
		if _, err := unpackAndValidateVSkin(buildTestVSkin(t, entries)); !errors.Is(err, ErrSkinInvalid) {
			t.Fatalf("err=%v, want ErrSkinInvalid", err)
		}
	})
}
