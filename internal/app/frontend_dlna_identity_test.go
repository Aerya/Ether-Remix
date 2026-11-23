package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/frontendadapter"
)

func TestLANRequiresSelectedActiveViewerAndVerifiedLAN(t *testing.T) {
	a := setupTestApp(t, true)
	local, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	id, err := local.CreateViewer(context.Background(), "viewer", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.users.RefreshViewers(context.Background()); err != nil {
		t.Fatal(err)
	}
	adapter, err := frontendadapter.New(frontendadapter.Declaration{
		PluginID: "test.lan", Meta: plugins.FrontendAdapterMeta{Key: "feed", PathPrefix: "/lan-feed", Auth: frontendadapter.AuthNoneLAN},
	}, lanAdapterManager{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.frontends.registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	inst, err := a.frontends.registry.Mount(context.Background(), adapter.ID())
	if err != nil {
		t.Fatal(err)
	}
	assertStatus := func(req *http.Request, want int) {
		t.Helper()
		response := httptest.NewRecorder()
		inst.Handler().ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("adapter status = %d, want %d", response.Code, want)
		}
	}
	settings := frontend.Settings{"viewerId": float64(id)}
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "192.168.1.20:1234"
	assertStatus(request, http.StatusUnauthorized)
	if err := a.frontends.control.PutSettings(context.Background(), adapter.ID(), settings); err != nil {
		t.Fatal(err)
	}
	assertStatus(request, http.StatusOK)
	if viewer, err := a.lanViewer(context.Background(), request, settings); err != nil || viewer.ID != id {
		t.Fatal(viewer, err)
	}
	if _, err := a.lanViewer(context.Background(), request, frontend.Settings{}); err == nil {
		t.Fatal("no selection accepted")
	}
	request.RemoteAddr = "203.0.113.10:1234"
	request.Header.Set("X-Forwarded-For", "192.168.1.20")
	if _, err := a.lanViewer(context.Background(), request, settings); err == nil {
		t.Fatal("untrusted proxy spoofed LAN")
	}
	assertStatus(request, http.StatusUnauthorized)
	if err := a.cfg.Update(func(c *config.Config) error { c.Server.TrustedProxyCIDRs = []string{"192.168.1.0/24"}; return nil }); err != nil {
		t.Fatal(err)
	}
	request.RemoteAddr = "192.168.1.2:1234"
	request.Header.Set("X-Forwarded-For", "8.8.8.8")
	if _, err := a.lanViewer(context.Background(), request, settings); err == nil {
		t.Fatal("remote viewer behind trusted proxy accepted")
	}
	assertStatus(request, http.StatusUnauthorized)
	request.Header.Set("X-Forwarded-For", "192.168.1.20, 8.8.8.8")
	assertStatus(request, http.StatusUnauthorized)
	request.Header.Set("X-Forwarded-For", "invalid")
	assertStatus(request, http.StatusUnauthorized)
	request.Header.Del("X-Forwarded-For")
	if err := a.frontends.control.PutSettings(context.Background(), adapter.ID(), frontend.Settings{}); err != nil {
		t.Fatal(err)
	}
	assertStatus(request, http.StatusUnauthorized)
}

// The mounted adapter uses the real host resolver before forwarding to this fake plugin.
type lanAdapterManager struct{}

func (lanAdapterManager) CallStream(context.Context, string, plugins.Request) (plugins.Response, io.ReadCloser, error) {
	return plugins.Response{Status: http.StatusOK}, io.NopCloser(strings.NewReader("ok")), nil
}
func (lanAdapterManager) Call(context.Context, string, plugins.Request) (plugins.Response, error) {
	return plugins.Response{Status: http.StatusOK}, nil
}
