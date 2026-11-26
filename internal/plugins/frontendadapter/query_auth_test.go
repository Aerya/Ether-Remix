package frontendadapter

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// appLoginIdentity accepts one app login by password or by md5 token.
type appLoginIdentity struct {
	frontend.SurfaceIdentity
	user, password string
	viewer         *core.Viewer
}

func (a appLoginIdentity) AuthenticateAppPassword(ctx context.Context, user, password string) (*core.Viewer, error) {
	if _, ok := frontend.ClientIP(ctx); !ok {
		return nil, core.ErrUnauthorized
	}
	if user == a.user && password == a.password {
		return a.viewer, nil
	}
	return nil, core.ErrUnauthorized
}

func (a appLoginIdentity) AuthenticateAppToken(ctx context.Context, user, token, salt string) (*core.Viewer, error) {
	sum := md5.Sum([]byte(a.password + salt))
	if user == a.user && token == hex.EncodeToString(sum[:]) {
		return a.viewer, nil
	}
	return nil, core.ErrUnauthorized
}

// TestQueryCredentialsNeverReachPlugin proves the query_credentials mode
// accepts plain, enc: and token+salt app logins, strips p, t and s before the
// plugin sees the request, and relays requests without valid credentials to
// the reserved unauthenticated path with no viewer identity at all.
func TestQueryCredentialsNeverReachPlugin(t *testing.T) {
	viewer := viewerWithLibraries(7, "Alice", core.LibraryMovies)
	mgr := &fakeManager{handler: func(req plugins.Request) (plugins.Response, io.ReadCloser, error) {
		return plugins.Response{Status: 200}, io.NopCloser(strings.NewReader("ok")), nil
	}}
	decl := testDecl(core.LibraryMovies)
	decl.Meta.Auth = AuthQueryCredentials
	decl.Meta.PathPrefix = "/subsonic"
	f, err := New(decl, mgr)
	if err != nil {
		t.Fatal(err)
	}
	host := frontend.Host{
		Identity: appLoginIdentity{SurfaceIdentity: frontend.BindIdentity(fakeIdentity{}, "plugin:plug1:feed"), user: "alice01", password: "abcde-fghjk-mnpqr", viewer: viewer},
		ClientIP: func(*http.Request) (netip.Addr, bool) { return netip.MustParseAddr("192.0.2.10"), true },
	}
	inst, err := f.Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	salt := "c19b2d"
	sum := md5.Sum([]byte("abcde-fghjk-mnpqr" + salt))
	for _, query := range []string{
		"u=alice01&p=abcde-fghjk-mnpqr&f=json",
		"u=alice01&p=enc:" + hex.EncodeToString([]byte("abcde-fghjk-mnpqr")) + "&f=json",
		"u=alice01&t=" + hex.EncodeToString(sum[:]) + "&s=" + salt + "&f=json",
	} {
		mgr.calls = nil
		rec := httptest.NewRecorder()
		inst.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/subsonic/rest/ping.view?"+query, nil))
		if rec.Code != 200 || len(mgr.calls) != 1 {
			t.Fatalf("%s: status %d calls %d", query, rec.Code, len(mgr.calls))
		}
		got := mgr.calls[0]
		if got.Path != "/ether/v1/frontend/feed/rest/ping.view" || got.Headers[HeaderViewer] != "7" {
			t.Fatalf("%s: path %q viewer %q", query, got.Path, got.Headers[HeaderViewer])
		}
		for _, k := range []string{"p", "t", "s"} {
			if _, ok := got.Query[k]; ok {
				t.Fatalf("%s: credential parameter %q reached the plugin", query, k)
			}
		}
		if got.Query["u"] != "alice01" || got.Query["f"] != "json" {
			t.Fatalf("%s: query %v", query, got.Query)
		}
	}

	// Subsonic clients may POST every parameter form-encoded instead.
	mgr.calls = nil
	post := httptest.NewRequest(http.MethodPost, "/subsonic/rest/star.view?f=json", strings.NewReader("u=alice01&p=abcde-fghjk-mnpqr&id=tr-1&id=tr-2"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, post)
	if rec.Code != 200 || len(mgr.calls) != 1 {
		t.Fatalf("form post: status %d calls %d", rec.Code, len(mgr.calls))
	}
	if got := mgr.calls[0]; got.Query["p"] != "" || len(got.RepeatedQuery["id"]) != 2 || got.BodyBase64 != "" {
		t.Fatalf("form post reached the plugin as %+v", got)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("query-credential adapters allow any origin")
	}

	for _, query := range []string{"u=alice01&p=wrong&f=json", "f=json"} {
		mgr.calls = nil
		rec := httptest.NewRecorder()
		inst.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/subsonic/rest/ping.view?"+query, nil))
		if len(mgr.calls) != 1 {
			t.Fatalf("%s: calls %d, want one unauthenticated relay", query, len(mgr.calls))
		}
		got := mgr.calls[0]
		if got.Path != "/ether/v1/frontend/feed/.unauthenticated/rest/ping.view" {
			t.Fatalf("%s: relay path %q", query, got.Path)
		}
		for name := range got.Headers {
			if isEtherHeader(name) {
				t.Fatalf("%s: unauthenticated relay carried %s", query, name)
			}
		}
		if _, ok := got.Query["p"]; ok {
			t.Fatalf("%s: credential parameter reached the plugin", query)
		}
	}
}
