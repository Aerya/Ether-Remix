package webdav

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

func TestUnknownPathsAndClientAuthContract(t *testing.T) {
	identity := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token == "grant" {
			return &frontend.Viewer{ID: 7}, nil
		}
		return nil, errors.New("bad grant")
	}}
	experience := frontendtest.Experience{
		ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
			return []frontend.View{{ID: "movies", Type: "movie"}}, nil
		},
	}
	inst, err := (Frontend{}).Mount(context.Background(), frontend.Host{
		Identity:   identity,
		Experience: experience,
		Sessions:   frontendtest.Sessions{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	// RFC 4918 WebDAV mounts a viewer-scoped virtual filesystem across /{path...},
	// so unauthenticated GET/PROPFIND must challenge with 401 Basic, while unknown
	// paths with a valid token return 404 and unsupported methods without a token
	// return 405 (never 401).
	cases := []struct {
		name   string
		method string
		path   string
		user   string
		token  string
		want   int
	}{
		{name: "unknown_path_valid_token", method: http.MethodGet, path: "/webdav/NoSuchFolder/missing.mkv", user: "7", token: "grant", want: http.StatusNotFound},
		{name: "propfind_unknown_path_valid_token", method: "PROPFIND", path: "/webdav/NoSuchFolder/", user: "7", token: "grant", want: http.StatusNotFound},
		{name: "unsupported_method_no_token", method: http.MethodPost, path: "/webdav/NoSuchFolder/missing.mkv", user: "", token: "", want: http.StatusMethodNotAllowed},
		{name: "webdav_contract_basic_challenge_no_token", method: http.MethodGet, path: "/webdav/NoSuchFolder/missing.mkv", user: "", token: "", want: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				req.SetBasicAuth(tc.user, tc.token)
			}
			rec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s %s: got %d, want %d", tc.method, tc.path, rec.Code, tc.want)
			}
		})
	}
}
