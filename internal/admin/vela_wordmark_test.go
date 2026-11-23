package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
)

func TestVelaWordmarkUploadRequiresOwnerAndCSRF(t *testing.T) {
	cfg := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = true })
	h := admin.NewHandler(admin.Deps{
		Config:       cfg,
		IsOwnerToken: storedOwner(cfg),
		UploadVelaWordmark: func(_ context.Context, r io.Reader) (admin.VelaWordmark, error) {
			b, err := io.ReadAll(r)
			if err != nil {
				return admin.VelaWordmark{}, err
			}
			if len(b) > 512<<10 {
				return admin.VelaWordmark{}, admin.ErrVelaWordmarkTooLarge
			}
			if !strings.HasPrefix(string(b), "<svg") {
				return admin.VelaWordmark{}, &admin.VelaWordmarkValidationError{Message: "invalid wordmark"}
			}
			return admin.VelaWordmark{
				URL:    "/vela/v0/branding/" + strings.Repeat("a", 64) + ".svg",
				Width:  240,
				Height: 60,
				Digest: strings.Repeat("a", 64),
			}, nil
		},
	})

	var mpBuf bytes.Buffer
	mw := multipart.NewWriter(&mpBuf)
	part, err := mw.CreateFormFile("file", "wordmark.svg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="240" height="60"/>`))
	_ = mw.Close()

	makeMultipart := func() *http.Request {
		r, err := http.NewRequest(http.MethodPost, "/ether/api/frontends/vela/wordmark", bytes.NewReader(mpBuf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		r.Host = "ether.example"
		r.RemoteAddr = "203.0.113.5:4000"
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.Header.Set("X-Ether-Request", "1")
		return r
	}

	// Unauthenticated -> 401.
	if got := serve(h, makeMultipart()).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: got %d, want 401", got)
	}

	// Missing X-Ether-Request -> 403.
	noCSRF := makeMultipart()
	noCSRF.Header.Del("X-Ether-Request")
	noCSRF.Header.Set("X-Plex-Token", ownerToken)
	if got := serve(h, noCSRF).Code; got != http.StatusForbidden {
		t.Fatalf("missing CSRF header: got %d, want 403", got)
	}

	// Cross-origin -> 403.
	crossOrigin := makeMultipart()
	crossOrigin.Header.Set("Origin", "https://evil.example")
	crossOrigin.Header.Set("X-Plex-Token", ownerToken)
	if got := serve(h, crossOrigin).Code; got != http.StatusForbidden {
		t.Fatalf("cross-origin: got %d, want 403", got)
	}

	// Authenticated owner multipart upload -> 200.
	authed := makeMultipart()
	authed.Header.Set("Origin", "https://ether.example")
	authed.Header.Set("X-Plex-Token", ownerToken)
	res := serve(h, authed)
	if res.Code != http.StatusOK {
		t.Fatalf("owner multipart upload: %d %s", res.Code, res.Body.String())
	}
	var wm admin.VelaWordmark
	if err := json.Unmarshal(res.Body.Bytes(), &wm); err != nil {
		t.Fatal(err)
	}
	if wm.Width != 240 || wm.Height != 60 || !strings.HasPrefix(wm.URL, "/vela/v0/branding/") {
		t.Fatalf("unexpected response: %+v", wm)
	}

	// Setup-open local tokenless caller still cannot upload wordmark -> 403.
	openCfg := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = false })
	openHandler := admin.NewHandler(admin.Deps{
		Config:             openCfg,
		IsOwnerToken:       storedOwner(openCfg),
		UploadVelaWordmark: func(context.Context, io.Reader) (admin.VelaWordmark, error) { return wm, nil },
	})
	setupReq := makeMultipart()
	setupReq.Host = "localhost"
	setupReq.RemoteAddr = "127.0.0.1:4000"
	if got := serve(openHandler, setupReq).Code; got != http.StatusForbidden {
		t.Fatalf("setup-open tokenless upload: got %d, want 403", got)
	}
}

func TestVelaAppIconUploadRequiresOwnerAndCSRF(t *testing.T) {
	cfg := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = true })
	h := admin.NewHandler(admin.Deps{
		Config:       cfg,
		IsOwnerToken: storedOwner(cfg),
		UploadVelaIcon: func(_ context.Context, r io.Reader) (admin.VelaAppIcon, error) {
			b, err := io.ReadAll(r)
			if err != nil {
				return admin.VelaAppIcon{}, err
			}
			if len(b) > 512<<10 {
				return admin.VelaAppIcon{}, admin.ErrVelaAppIconTooLarge
			}
			if !strings.HasPrefix(string(b), "<svg") {
				return admin.VelaAppIcon{}, &admin.VelaAppIconValidationError{Message: "app icon must be square"}
			}
			return admin.VelaAppIcon{
				URL:    "/vela/v0/branding/icon-" + strings.Repeat("b", 64) + ".svg",
				Width:  128,
				Height: 128,
				Digest: strings.Repeat("b", 64),
			}, nil
		},
	})

	makeUpload := func(payload string) *http.Request {
		var mpBuf bytes.Buffer
		mw := multipart.NewWriter(&mpBuf)
		part, err := mw.CreateFormFile("file", "icon.svg")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte(payload))
		_ = mw.Close()
		r, err := http.NewRequest(http.MethodPost, "/ether/api/frontends/vela/icon", bytes.NewReader(mpBuf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		r.Host = "ether.example"
		r.RemoteAddr = "203.0.113.5:4000"
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.Header.Set("X-Ether-Request", "1")
		return r
	}

	validSVG := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128"><rect width="128" height="128"/></svg>`

	// Unauthenticated -> 401.
	if got := serve(h, makeUpload(validSVG)).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: got %d, want 401", got)
	}

	// Missing CSRF -> 403.
	noCSRF := makeUpload(validSVG)
	noCSRF.Header.Del("X-Ether-Request")
	noCSRF.Header.Set("X-Plex-Token", ownerToken)
	if got := serve(h, noCSRF).Code; got != http.StatusForbidden {
		t.Fatalf("missing CSRF: got %d, want 403", got)
	}

	// Cross-origin -> 403.
	cross := makeUpload(validSVG)
	cross.Header.Set("Origin", "https://evil.example")
	cross.Header.Set("X-Plex-Token", ownerToken)
	if got := serve(h, cross).Code; got != http.StatusForbidden {
		t.Fatalf("cross-origin: got %d, want 403", got)
	}

	// Validation error -> 400 invalid_app_icon.
	bad := makeUpload("not-an-image")
	bad.Header.Set("Origin", "https://ether.example")
	bad.Header.Set("X-Plex-Token", ownerToken)
	if res := serve(h, bad); res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"invalid_app_icon"`) {
		t.Fatalf("bad upload: code=%d body=%s", res.Code, res.Body.String())
	}

	// Authenticated owner multipart upload -> 200.
	authed := makeUpload(validSVG)
	authed.Header.Set("Origin", "https://ether.example")
	authed.Header.Set("X-Plex-Token", ownerToken)
	res := serve(h, authed)
	if res.Code != http.StatusOK {
		t.Fatalf("owner multipart upload: %d %s", res.Code, res.Body.String())
	}
	var icon admin.VelaAppIcon
	if err := json.Unmarshal(res.Body.Bytes(), &icon); err != nil {
		t.Fatal(err)
	}
	if icon.Width != 128 || icon.Height != 128 || !strings.HasPrefix(icon.URL, "/vela/v0/branding/icon-") {
		t.Fatalf("unexpected response: %+v", icon)
	}

	// Setup-open local tokenless caller still cannot upload icon -> 403.
	openCfg := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = false })
	openHandler := admin.NewHandler(admin.Deps{
		Config:         openCfg,
		IsOwnerToken:   storedOwner(openCfg),
		UploadVelaIcon: func(context.Context, io.Reader) (admin.VelaAppIcon, error) { return icon, nil },
	})
	setupReq := makeUpload(validSVG)
	setupReq.Host = "localhost"
	setupReq.RemoteAddr = "127.0.0.1:4000"
	if got := serve(openHandler, setupReq).Code; got != http.StatusForbidden {
		t.Fatalf("setup-open tokenless upload: got %d, want 403", got)
	}
}
