package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestVelaDedicatedPortListenersAndValidation(t *testing.T) {
	p := Frontend{}
	desc := p.Describe()
	if desc.DefaultPort != DefaultPort || DefaultPort != 8098 {
		t.Fatalf("DefaultPort = %d, want 8098", desc.DefaultPort)
	}
	schema, ok := desc.SettingsSchema.(frontend.SettingsSchema)
	if !ok {
		t.Fatalf("SettingsSchema type = %T, want frontend.SettingsSchema", desc.SettingsSchema)
	}
	var sawPort, sawServeMain bool
	for _, sec := range schema.Sections {
		if sec.Key != "find" {
			continue
		}
		for _, f := range sec.Fields {
			if f.Key == "port" && f.Kind == "port" {
				sawPort = true
			}
			if f.Key == "serveOnMainPort" && f.Kind == "bool" {
				sawServeMain = true
			}
		}
	}
	if !sawPort || !sawServeMain {
		t.Fatalf("find schema missing port/serveOnMainPort: %+v", desc.SettingsSchema)
	}

	// Default (no dedicated port): shared /vela + /sessions/media only.
	instDefault := &instance{ctx: context.Background(), host: frontend.Host{}}
	if got := instDefault.Listeners(); len(got) != 2 || !got[0].Shared || got[0].PathPrefix != "/vela" || !got[1].Shared || got[1].PathPrefix != "/sessions/media" {
		t.Fatalf("default listeners = %+v, want shared /vela and /sessions/media", got)
	}

	// Dedicated port + serveOnMainPort default (true): dedicated + shared.
	instBoth := &instance{
		ctx: context.Background(),
		host: frontend.Host{
			Config: func() frontend.Settings { return frontend.Settings{"port": float64(8098)} },
		},
	}
	gotBoth := instBoth.Listeners()
	if len(gotBoth) != 3 || gotBoth[0].Shared || gotBoth[0].Address != ":8098" || !gotBoth[1].Shared || gotBoth[1].PathPrefix != "/vela" {
		t.Fatalf("dedicated+shared listeners = %+v", gotBoth)
	}

	// Dedicated port + serveOnMainPort=false: dedicated ONLY (no /vela on main port).
	instDedicatedOnly := &instance{
		ctx: context.Background(),
		host: frontend.Host{
			Config: func() frontend.Settings {
				return frontend.Settings{"port": float64(8098), "serveOnMainPort": false}
			},
		},
	}
	gotOnly := instDedicatedOnly.Listeners()
	if len(gotOnly) != 1 || gotOnly[0].Shared || gotOnly[0].Address != ":8098" {
		t.Fatalf("dedicated-only listeners = %+v, want single :8098 listener", gotOnly)
	}

	// Safety invariant: serveOnMainPort=false without dedicated port still keeps shared listeners
	// and is rejected by ValidateSettings.
	instSafety := &instance{
		ctx: context.Background(),
		host: frontend.Host{
			Config: func() frontend.Settings { return frontend.Settings{"serveOnMainPort": false} },
		},
	}
	if got := instSafety.Listeners(); len(got) != 2 || !got[0].Shared {
		t.Fatalf("safety invariant broken: %+v", got)
	}

	// ValidateSettings checks.
	if err := p.ValidateSettings(map[string]any{"port": float64(8098), "serveOnMainPort": false}); err != nil {
		t.Fatalf("valid dedicated-only settings rejected: %v", err)
	}
	if err := p.ValidateSettings(map[string]any{"serveOnMainPort": false}); err == nil {
		t.Fatal("expected error when serveOnMainPort=false without dedicated port")
	}
	for _, bad := range []map[string]any{
		{"port": float64(0)},
		{"port": float64(70000)},
		{"port": "8098"},
		{"serveOnMainPort": "no"},
	} {
		if err := p.ValidateSettings(bad); err == nil {
			t.Fatalf("expected validation error for %#v", bad)
		}
	}
}

func TestVelaDedicatedRootAndManifestFollowSurfaceURL(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"index.html":           `<!doctype html><html><head><title>Vela</title><link rel="manifest" href="/vela/manifest.webmanifest"><link rel="icon" href="/vela/favicon-32.png"></head><body></body></html>`,
		"manifest.webmanifest": `{"id":"/vela","name":"Vela","short_name":"Vela","start_url":"/vela","scope":"/vela","icons":[{"src":"/vela/vela-icon-192.png","sizes":"192x192","type":"image/png"}]}`,
		"sw.js":                `self.addEventListener("fetch", () => {});`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Case 1: Dedicated subdomain (https://vela.example.com) -> basePath /vela.
	subInst := &instance{
		ctx:    context.Background(),
		assets: dir,
		host: frontend.Host{
			SurfaceURL: func(context.Context, *http.Request) (string, error) {
				return "https://vela.example.com", nil
			},
		},
	}
	rootSub := httptest.NewRecorder()
	subInst.serve(rootSub, httptest.NewRequest(http.MethodGet, "/?tab=live", nil))
	if rootSub.Code != http.StatusPermanentRedirect || rootSub.Header().Get("Location") != "/vela?tab=live" {
		t.Fatalf("dedicated root redirect = %d %q, want 308 /vela?tab=live", rootSub.Code, rootSub.Header().Get("Location"))
	}
	// Unrelated path on dedicated Vela port returns 404 (never SPA shell).
	otherRec := httptest.NewRecorder()
	subInst.serve(otherRec, httptest.NewRequest(http.MethodGet, "/ether/", nil))
	if otherRec.Code != http.StatusNotFound {
		t.Fatalf("GET /ether/ on Vela handler = %d, want 404", otherRec.Code)
	}

	// Case 2: Reverse proxy with path prefix via W189 SurfaceURL (https://home.example.com/watch).
	prefInst := &instance{
		ctx:    context.Background(),
		assets: dir,
		host: frontend.Host{
			SurfaceURL: func(context.Context, *http.Request) (string, error) {
				return "https://home.example.com/watch", nil
			},
		},
	}
	rootPref := httptest.NewRecorder()
	prefInst.serve(rootPref, httptest.NewRequest(http.MethodGet, "/", nil))
	if rootPref.Code != http.StatusPermanentRedirect || rootPref.Header().Get("Location") != "/watch/vela" {
		t.Fatalf("prefixed root redirect = %d %q, want 308 /watch/vela", rootPref.Code, rootPref.Header().Get("Location"))
	}
	manRec := httptest.NewRecorder()
	prefInst.serve(manRec, httptest.NewRequest(http.MethodGet, "/vela/manifest.webmanifest", nil))
	if manRec.Code != http.StatusOK {
		t.Fatalf("manifest = %d %s", manRec.Code, manRec.Body)
	}
	var man struct {
		ID       string `json:"id"`
		StartURL string `json:"start_url"`
		Scope    string `json:"scope"`
		Icons    []struct {
			Src string `json:"src"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(manRec.Body.Bytes(), &man); err != nil {
		t.Fatal(err)
	}
	if man.ID != "/watch/vela" || man.StartURL != "/watch/vela" || man.Scope != "/watch/vela" {
		t.Fatalf("manifest base paths = %+v, want /watch/vela", man)
	}
	if len(man.Icons) != 1 || man.Icons[0].Src != "/watch/vela/vela-icon-192.png" {
		t.Fatalf("manifest icons = %+v, want /watch/vela/vela-icon-192.png", man.Icons)
	}
	swRec := httptest.NewRecorder()
	prefInst.serve(swRec, httptest.NewRequest(http.MethodGet, "/vela/sw.js", nil))
	if swRec.Code != http.StatusOK || swRec.Header().Get("Service-Worker-Allowed") != "/watch/vela" {
		t.Fatalf("sw.js = %d scope=%q, want 200 /watch/vela", swRec.Code, swRec.Header().Get("Service-Worker-Allowed"))
	}
	htmlRec := httptest.NewRecorder()
	prefInst.serve(htmlRec, httptest.NewRequest(http.MethodGet, "/vela", nil))
	if htmlRec.Code != http.StatusOK || !strings.Contains(htmlRec.Body.String(), `href="/watch/vela/manifest.webmanifest"`) {
		t.Fatalf("html did not rewrite manifest href: %s", htmlRec.Body.String())
	}
}
