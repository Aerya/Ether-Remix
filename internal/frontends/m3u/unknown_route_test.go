package m3u

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

func TestUnknownRoutesNeverReturn401(t *testing.T) {
	identity := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token == "valid" {
			return &frontend.Viewer{ID: 7}, nil
		}
		return nil, errors.New("bad grant")
	}}
	inst, err := (Frontend{}).Mount(context.Background(), frontend.Host{
		Identity: identity,
		LiveTV:   frontendtest.LiveTV{},
		Sessions: frontendtest.Sessions{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	cases := []struct {
		name string
		path string
		want int
	}{
		{name: "unknown_with_valid_token", path: "/m3u/valid/unknown.m3u", want: http.StatusNotFound},
		{name: "unknown_with_no_token", path: "/m3u/unknown", want: http.StatusNotFound},
		{name: "unknown_with_invalid_token", path: "/m3u/invalid/unknown.m3u", want: http.StatusNotFound},
		{name: "known_route_invalid_token", path: "/m3u/invalid/playlist.m3u", want: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.want {
				t.Fatalf("GET %s: got %d, want %d", tc.path, rec.Code, tc.want)
			}
		})
	}
}
