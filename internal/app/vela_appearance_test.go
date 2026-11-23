package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
	"github.com/braedonsaunders/ether/internal/safefetch"
	"github.com/braedonsaunders/ether/internal/store"
)

func buildTestSkinArchive(t *testing.T, id, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	skinJSON := `{"schema":1,"id":"` + id + `","version":"` + version + `","name":"Harbor","engine":"cinematic","palette":{"canvas":"#0b1320","ink":"#eef4fb"},"images":{"preview":"assets/preview.svg"},"swatches":["#0b1320","#4cc9f0"]}`
	w, err := zw.Create("skin.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(skinJSON)); err != nil {
		t.Fatal(err)
	}
	aw, err := zw.Create("assets/preview.svg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := aw.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 9"><rect width="16" height="9" fill="#0b1320"/></svg>`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The revision and owner choice must commit together and survive restarts.
func TestVelaAppearanceRevision(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenTest(t)
	registry := frontend.NewRegistry(func(frontend.Protocol) frontend.Host { return frontend.Host{} })
	if err := registry.Register(vela.Frontend{DB: db}); err != nil {
		t.Fatal(err)
	}
	control := newFrontendControl(registry, cfg)
	control.db = db
	for _, tc := range []struct {
		skin     string
		force    bool
		revision uint64
	}{
		{"cinematic", false, 0},
		{"lounge", false, 1},
		{"lounge", false, 1},
		{"lounge", true, 2},
		{"lounge", true, 2},
		{"cinematic-doc", true, 3},
		{"cinematic", false, 4},
	} {
		if err := control.PutSettings(context.Background(), "vela", frontend.Settings{"defaultSkinId": tc.skin, "force": tc.force}); err != nil {
			t.Fatal(err)
		}
		reopened, err := config.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		got := reopened.Read().Frontends["vela"]
		if got.AppearanceRevision != tc.revision || got.Settings["defaultSkinId"] != tc.skin || got.Settings["force"] != tc.force {
			t.Fatalf("persisted: %#v", got)
		}
	}
	if err := control.PutSettings(context.Background(), "vela", frontend.Settings{"defaultSkinId": "../secret"}); err == nil {
		t.Fatal("invalid skin accepted")
	}

	// Branding name + uploaded wordmarks also advance AppearanceRevision and normalize whitespace.
	wm, err := control.UploadVelaWordmark(context.Background(), strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg" width="300" height="75"><rect width="300" height="75"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	if err := control.PutSettings(context.Background(), "vela", frontend.Settings{
		"defaultSkinId": "cinematic",
		"force":         false,
		"appName":       "  Home Theatre  ",
		"wordmark":      map[string]any{"url": wm.URL, "width": float64(wm.Width), "height": float64(wm.Height)},
	}); err != nil {
		t.Fatal(err)
	}
	got := cfg.Read().Frontends["vela"]
	if got.AppearanceRevision != 5 || got.Settings["appName"] != "Home Theatre" {
		t.Fatalf("after branding save: %#v", got)
	}
	// Saving identical trimmed values does not bump revision.
	if err := control.PutSettings(context.Background(), "vela", frontend.Settings{
		"defaultSkinId": "cinematic",
		"force":         false,
		"appName":       "Home Theatre",
		"wordmark":      map[string]any{"url": wm.URL, "width": float64(wm.Width), "height": float64(wm.Height)},
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Read().Frontends["vela"].AppearanceRevision != 5 {
		t.Fatalf("expected revision 5 on no-op branding save, got %d", cfg.Read().Frontends["vela"].AppearanceRevision)
	}
	// Unknown digest is rejected when DB is present.
	if err := control.PutSettings(context.Background(), "vela", frontend.Settings{
		"wordmark": map[string]any{"url": "/vela/v0/branding/" + strings.Repeat("f", 64) + ".svg", "width": 300.0, "height": 75.0},
	}); err == nil {
		t.Fatal("expected unknown wordmark digest to be rejected")
	}

	// Uploaded square app icon advances AppearanceRevision and persists; unknown digest is rejected.
	icon, err := control.UploadVelaIcon(context.Background(), strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128"><rect width="128" height="128" fill="#111"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	if err := control.PutSettings(context.Background(), "vela", frontend.Settings{
		"defaultSkinId": "cinematic",
		"force":         false,
		"appName":       "Home Theatre",
		"wordmark":      map[string]any{"url": wm.URL, "width": float64(wm.Width), "height": float64(wm.Height)},
		"appIcon":       map[string]any{"url": icon.URL, "width": float64(icon.Width), "height": float64(icon.Height)},
	}); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Read().Frontends["vela"]; got.AppearanceRevision != 6 || got.CapabilitiesRevision != 0 {
		t.Fatalf("after appIcon save: appearance=%d caps=%d", got.AppearanceRevision, got.CapabilitiesRevision)
	}
	if err := control.PutSettings(context.Background(), "vela", frontend.Settings{
		"appIcon": map[string]any{"url": "/vela/v0/branding/icon-" + strings.Repeat("e", 64) + ".svg", "width": 128.0, "height": 128.0},
	}); err == nil {
		t.Fatal("expected unknown app icon digest to be rejected")
	}

	// Sign-in method changes advance CapabilitiesRevision across restarts without bumping AppearanceRevision.
	if err := control.PutSettings(context.Background(), "vela", frontend.Settings{
		"defaultSkinId":  "cinematic",
		"force":          false,
		"appName":        "Home Theatre",
		"wordmark":       map[string]any{"url": wm.URL, "width": float64(wm.Width), "height": float64(wm.Height)},
		"appIcon":        map[string]any{"url": icon.URL, "width": float64(icon.Width), "height": float64(icon.Height)},
		"passwordSignIn": false,
		"plexSignIn":     false,
		"quickConnect":   false,
	}); err != nil {
		t.Fatal(err)
	}
	reopenedCaps, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopenedCaps.Read().Frontends["vela"]; got.CapabilitiesRevision != 1 || got.AppearanceRevision != 6 {
		t.Fatalf("after sign-in toggle: %#v", got)
	}
	// No-op re-save does not bump CapabilitiesRevision.
	if err := control.PutSettings(context.Background(), "vela", frontend.Settings{
		"defaultSkinId":  "cinematic",
		"force":          false,
		"appName":        "Home Theatre",
		"wordmark":       map[string]any{"url": wm.URL, "width": float64(wm.Width), "height": float64(wm.Height)},
		"appIcon":        map[string]any{"url": icon.URL, "width": float64(icon.Width), "height": float64(icon.Height)},
		"passwordSignIn": false,
		"plexSignIn":     false,
		"quickConnect":   false,
	}); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Read().Frontends["vela"].CapabilitiesRevision; got != 1 {
		t.Fatalf("expected CapabilitiesRevision 1 on no-op save, got %d", got)
	}
}

func TestVelaSkinsAdminAndSSRF(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenTest(t)
	a := &App{
		opts:         Options{DataDir: dir, Version: "0.3.16-test"},
		cfg:          cfg,
		db:           db,
		velaSkinHTTP: (&safefetch.Guard{}).Client(&http.Client{}),
	}
	registry := frontend.NewRegistry(func(frontend.Protocol) frontend.Host { return frontend.Host{} })
	if err := registry.Register(vela.Frontend{DB: db, Skins: a.velaSkinsStore()}); err != nil {
		t.Fatal(err)
	}
	control := newFrontendControl(registry, cfg)
	h := admin.NewHandler(admin.Deps{
		Config:    cfg,
		Frontends: control,
		VelaSkins: a.velaSkinsAdmin(),
		IsOwnerToken: func(_ context.Context, tok string) bool {
			return tok == "owner-token"
		},
	})

	// Unauthenticated request is rejected.
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/vela/skins", nil))
	if unauth.Code != http.StatusForbidden {
		t.Fatalf("unauth GET /api/vela/skins=%d, want 403", unauth.Code)
	}

	// Authenticated list returns the 9 non-removable built-in skin documents.
	listReq := httptest.NewRequest(http.MethodGet, "/api/vela/skins", nil)
	listReq.Header.Set("X-Plex-Token", "owner-token")
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/vela/skins=%d: %s", listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Skins []admin.VelaSkin `json:"skins"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Skins) != 9 || listed.Skins[0].ID != "cinematic-doc" || !listed.Skins[0].Builtin {
		t.Fatalf("initial skins=%+v", listed.Skins)
	}

	// Multipart upload installs harbor and bumps AppearanceRevision.
	archive := buildTestSkinArchive(t, "harbor", "1.0.0")
	var mpBuf bytes.Buffer
	mw := multipart.NewWriter(&mpBuf)
	fw, err := mw.CreateFormFile("file", "harbor.vskin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(archive); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()

	upReq := httptest.NewRequest(http.MethodPost, "/api/vela/skins", &mpBuf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upReq.Header.Set("X-Ether-Request", "1")
	upReq.Header.Set("X-Plex-Token", "owner-token")
	upRec := httptest.NewRecorder()
	h.ServeHTTP(upRec, upReq)
	if upRec.Code != http.StatusCreated {
		t.Fatalf("POST multipart /api/vela/skins=%d: %s", upRec.Code, upRec.Body.String())
	}
	if got := cfg.Read().Frontends["vela"].AppearanceRevision; got != 1 {
		t.Fatalf("AppearanceRevision=%d, want 1", got)
	}

	// Owner can select harbor as defaultSkinId with force=true.
	if err := control.PutSettings(context.Background(), "vela", frontend.Settings{"defaultSkinId": "harbor", "force": true}); err != nil {
		t.Fatalf("PutSettings(harbor): %v", err)
	}
	if got := cfg.Read().Frontends["vela"].AppearanceRevision; got != 2 {
		t.Fatalf("AppearanceRevision=%d, want 2", got)
	}

	// Removing built-in is rejected; removing harbor resets defaultSkinId to cinematic and bumps revision.
	delBuiltin := httptest.NewRequest(http.MethodDelete, "/api/vela/skins/mochi", nil)
	delBuiltin.Header.Set("X-Ether-Request", "1")
	delBuiltin.Header.Set("X-Plex-Token", "owner-token")
	delBuiltinRec := httptest.NewRecorder()
	h.ServeHTTP(delBuiltinRec, delBuiltin)
	if delBuiltinRec.Code != http.StatusBadRequest {
		t.Fatalf("DELETE builtin=%d, want 400", delBuiltinRec.Code)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/vela/skins/harbor", nil)
	delReq.Header.Set("X-Ether-Request", "1")
	delReq.Header.Set("X-Plex-Token", "owner-token")
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/vela/skins/harbor=%d: %s", delRec.Code, delRec.Body.String())
	}
	afterDel := cfg.Read().Frontends["vela"]
	if afterDel.AppearanceRevision != 3 || afterDel.Settings["defaultSkinId"] != "cinematic" {
		t.Fatalf("after DELETE harbor: %#v", afterDel)
	}

	// URL install SSRF guard: local 127.0.0.1 server blocked when lan=false, allowed when lan=true;
	// link-local metadata IP blocked even when lan=true.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	postJSON := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/vela/skins", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Ether-Request", "1")
		req.Header.Set("X-Plex-Token", "owner-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	blockedLAN := postJSON(`{"url":"` + srv.URL + `/harbor.vskin","lan":false}`)
	if blockedLAN.Code != http.StatusBadRequest || !strings.Contains(blockedLAN.Body.String(), `"ssrf_blocked"`) {
		t.Fatalf("lan=false local URL=%d: %s", blockedLAN.Code, blockedLAN.Body.String())
	}

	blockedMeta := postJSON(`{"url":"http://169.254.169.254/latest/meta-data","lan":true}`)
	if blockedMeta.Code != http.StatusBadRequest || !strings.Contains(blockedMeta.Body.String(), `"ssrf_blocked"`) {
		t.Fatalf("link-local metadata URL=%d: %s", blockedMeta.Code, blockedMeta.Body.String())
	}

	allowedLAN := postJSON(`{"url":"` + srv.URL + `/harbor.vskin","lan":true}`)
	if allowedLAN.Code != http.StatusCreated {
		t.Fatalf("lan=true local URL=%d: %s", allowedLAN.Code, allowedLAN.Body.String())
	}
}

func TestVelaLivePreviewAdminPersistenceAndPolicyBump(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenTest(t)
	a := &App{
		opts: Options{DataDir: dir, Version: "0.3.17-test"},
		cfg:  cfg,
		db:   db,
	}
	cfg.SetPolicyEpoch(a.publishPolicyConfig)

	registry := frontend.NewRegistry(func(frontend.Protocol) frontend.Host { return frontend.Host{} })
	if err := registry.Register(vela.Frontend{DB: db, Skins: a.velaSkinsStore()}); err != nil {
		t.Fatal(err)
	}
	control := newFrontendControl(registry, cfg)
	control.db = db
	h := admin.NewHandler(admin.Deps{
		Config:    cfg,
		Frontends: control,
		IsOwnerToken: func(_ context.Context, tok string) bool {
			return tok == "owner-token"
		},
	})

	readPolicyEpoch := func() int64 {
		t.Helper()
		var gen int64
		if err := db.QueryRow(`SELECT generation FROM viewer_policy_epoch WHERE id=1`).Scan(&gen); err != nil {
			t.Fatal(err)
		}
		return gen
	}
	putSettings := func(payload string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/frontends/vela/settings", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Ether-Request", "1")
		req.Header.Set("X-Plex-Token", "owner-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	epoch0 := readPolicyEpoch()

	// Invalid mode is rejected with 400 and does not bump policy epoch.
	if bad := putSettings(`{"livePreview":"invalid"}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid livePreview = %d: %s", bad.Code, bad.Body.String())
	}
	if readPolicyEpoch() != epoch0 {
		t.Fatalf("invalid livePreview bumped policy epoch")
	}

	// Saving on-click persists across restart, bumps viewer_policy_epoch, and does not bump AppearanceRevision.
	if ok := putSettings(`{"defaultSkinId":"cinematic","livePreview":"on-click"}`); ok.Code != http.StatusOK {
		t.Fatalf("PUT on-click = %d: %s", ok.Code, ok.Body.String())
	}
	epoch1 := readPolicyEpoch()
	if epoch1 != epoch0+1 {
		t.Fatalf("policy epoch after on-click = %d, want %d", epoch1, epoch0+1)
	}
	if got := cfg.Read().Frontends["vela"]; got.AppearanceRevision != 0 || got.Settings["livePreview"] != "on-click" {
		t.Fatalf("after on-click save: %#v", got)
	}

	// Reopening config from disk preserves livePreview.
	reopened, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Read().Frontends["vela"].Settings["livePreview"] != "on-click" {
		t.Fatalf("reopened config lost livePreview: %#v", reopened.Read().Frontends["vela"])
	}

	// GET /api/frontends/vela/settings returns the persisted livePreview value.
	getReq := httptest.NewRequest(http.MethodGet, "/api/frontends/vela/settings", nil)
	getReq.Header.Set("X-Plex-Token", "owner-token")
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET settings = %d: %s", getRec.Code, getRec.Body.String())
	}
	var gotSettings map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &gotSettings); err != nil {
		t.Fatal(err)
	}
	if gotSettings["livePreview"] != "on-click" {
		t.Fatalf("GET /api/frontends/vela/settings = %#v, want livePreview=on-click", gotSettings)
	}

	// Switching to off bumps viewer_policy_epoch again; repeating off does not.
	if ok := putSettings(`{"defaultSkinId":"cinematic","livePreview":"off"}`); ok.Code != http.StatusOK {
		t.Fatalf("PUT off = %d: %s", ok.Code, ok.Body.String())
	}
	epoch2 := readPolicyEpoch()
	if epoch2 != epoch1+1 {
		t.Fatalf("policy epoch after off = %d, want %d", epoch2, epoch1+1)
	}
	if ok := putSettings(`{"defaultSkinId":"cinematic","livePreview":"off"}`); ok.Code != http.StatusOK {
		t.Fatalf("PUT repeat off = %d: %s", ok.Code, ok.Body.String())
	}
	if readPolicyEpoch() != epoch2 {
		t.Fatalf("repeat off save unexpectedly bumped policy epoch")
	}
}
