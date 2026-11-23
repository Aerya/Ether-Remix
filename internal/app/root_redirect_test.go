package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	conformplex "github.com/braedonsaunders/ether/internal/conform/plex"
	"github.com/braedonsaunders/ether/internal/plextv"
)

const browserAcceptHeader = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"

func TestBrowserRootRedirectsToConsole(t *testing.T) {
	a := setupTestApp(t, true)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/", nil)
			req.Header.Set("Accept", browserAcceptHeader)
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusFound {
				t.Fatalf("%s / status = %d, want %d (%s)", method, w.Code, http.StatusFound, w.Body.String())
			}
			if got := w.Header().Get("Location"); got != "/ether/" {
				t.Fatalf("%s / Location = %q, want /ether/", method, got)
			}
			if method == http.MethodHead && w.Body.Len() != 0 {
				t.Fatalf("HEAD / wrote body %q", w.Body.String())
			}
		})
	}

	// Non-GET/HEAD methods and non-root paths must not redirect even with browser Accept.
	t.Run("OPTIONS / stays on Plex", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Accept", browserAcceptHeader)
		req.Header.Set("Origin", "https://app.plex.tv")
		req.Header.Set("Access-Control-Request-Method", "GET")
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK || w.Header().Get("X-Plex-Protocol") != "1.0" {
			t.Fatalf("OPTIONS / = %d, headers=%v", w.Code, w.Header())
		}
	})

	t.Run("POST / stays on Plex", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Accept", browserAcceptHeader)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		// POST / is not a Plex route: it reaches Plex (no console redirect)
		// and answers 404, never 401 (an unknown route must not sign clients out).
		if w.Code != http.StatusNotFound || w.Header().Get("Location") != "" {
			t.Fatalf("POST / = %d (Location %q), want 404 from Plex", w.Code, w.Header().Get("Location"))
		}
	})

	t.Run("GET /identity with browser Accept stays on Plex", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/identity", nil)
		req.Header.Set("Accept", browserAcceptHeader)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "machineIdentifier=") {
			t.Fatalf("GET /identity = %d: %s", w.Code, w.Body.String())
		}
	})
}

func TestPlexRootBehaviourUnchanged(t *testing.T) {
	ctx := context.Background()
	a := setupTestApp(t, true)
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "owner-token"); err != nil {
		t.Fatal(err)
	}
	inst, ok := a.frontends.registry.Instance("plex")
	if !ok {
		t.Fatal("Plex frontend not mounted")
	}
	direct := inst.Handler()
	mounted := a.Handler()

	assertMatchesDirectPlex := func(t *testing.T, makeReq func() *http.Request) *httptest.ResponseRecorder {
		t.Helper()
		want := httptest.NewRecorder()
		direct.ServeHTTP(want, makeReq())
		got := httptest.NewRecorder()
		mounted.ServeHTTP(got, makeReq())
		// updatedAt is the wall clock in seconds and can tick between the two calls.
		stamp := regexp.MustCompile(`updatedAt="\d+"`)
		gotBody := stamp.ReplaceAll(got.Body.Bytes(), nil)
		wantBody := stamp.ReplaceAll(want.Body.Bytes(), nil)
		if got.Code != want.Code || !bytes.Equal(gotBody, wantBody) {
			t.Fatalf("mounted / diverged from Plex: got=%d %q want=%d %q", got.Code, got.Body.String(), want.Code, want.Body.String())
		}
		if got.Header().Get("X-Plex-Protocol") != want.Header().Get("X-Plex-Protocol") {
			t.Fatalf("X-Plex-Protocol = %q, want %q", got.Header().Get("X-Plex-Protocol"), want.Header().Get("X-Plex-Protocol"))
		}
		return got
	}

	// Unauthenticated non-HTML probes to / keep returning Plex's 401.
	for _, accept := range []string{"", "*/*", "application/xml", "application/json"} {
		t.Run("unauthenticated_accept_"+accept, func(t *testing.T) {
			rec := assertMatchesDirectPlex(t, func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				if accept != "" {
					r.Header.Set("Accept", accept)
				}
				return r
			})
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}

	// Browser Accept with any X-Plex-* header stays on Plex (401 without token, 200 with token).
	t.Run("X-Plex header without token", func(t *testing.T) {
		rec := assertMatchesDirectPlex(t, func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("Accept", browserAcceptHeader)
			r.Header.Set("X-Plex-Client-Identifier", "plex-probe")
			return r
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("X-Plex-Token header with browser Accept", func(t *testing.T) {
		rec := assertMatchesDirectPlex(t, func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("Accept", browserAcceptHeader)
			r.Header.Set("X-Plex-Token", "owner-token")
			return r
		})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "machineIdentifier=") {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
	})

	// Identity probes with X-Plex-Token query parameter (valid, lowercase, and invalid) stay on Plex even with browser Accept.
	for _, tc := range []struct {
		name  string
		query string
		code  int
	}{
		{"query X-Plex-Token valid", "/?X-Plex-Token=owner-token", http.StatusOK},
		{"query x-plex-token lowercase", "/?x-plex-token=owner-token", http.StatusOK},
		{"query X-Plex-Token invalid", "/?X-Plex-Token=bad-token", http.StatusUnauthorized},
		{"query X-Plex-Token empty", "/?X-Plex-Token=", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := assertMatchesDirectPlex(t, func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, tc.query, nil)
				r.Header.Set("Accept", browserAcceptHeader)
				return r
			})
			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.code, rec.Body.String())
			}
		})
	}

	// Representative Plex conformance clients (Web, iOS, Apple TV) in XML and JSON modes.
	for name, client := range conformplex.Clients() {
		for _, accept := range []string{"application/xml", "application/json"} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				t.Run("conform_"+name+"_"+method+"_"+accept, func(t *testing.T) {
					rec := assertMatchesDirectPlex(t, func() *http.Request {
						r := httptest.NewRequest(method, "/", nil)
						r.Header = client.Headers.Clone()
						r.Header.Set("X-Plex-Token", "owner-token")
						r.Header.Set("Accept", accept)
						return r
					})
					if rec.Code != http.StatusOK {
						t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
					}
				})
			}
		}
	}
}
