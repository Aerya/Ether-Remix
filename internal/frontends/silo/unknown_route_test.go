package silo

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
	v := &frontend.Viewer{ID: 7, Username: "alice"}
	ident := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token == "access" {
			return v, nil
		}
		return nil, errors.New("unauthorized")
	}}
	inst, err := (Frontend{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: testDeviceIdentity(ident, v)})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	cases := []struct {
		name   string
		method string
		path   string
		token  string
		want   int
	}{
		{name: "unknown_valid_token", method: http.MethodGet, path: "/api/v2/unknown/route", token: "access", want: http.StatusNotFound},
		{name: "unknown_no_token", method: http.MethodGet, path: "/api/v2/unknown/route", token: "", want: http.StatusNotFound},
		{name: "unknown_invalid_token", method: http.MethodGet, path: "/api/v2/unknown/route", token: "bad", want: http.StatusNotFound},
		{name: "prefixed_unknown_no_token", method: http.MethodGet, path: "/silo/api/v2/unknown/route", token: "", want: http.StatusNotFound},
		{name: "prefixed_unknown_valid_token", method: http.MethodGet, path: "/silo/api/v2/unknown/route", token: "access", want: http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
				req.Header.Set("X-Silo-Device-Id", "tv")
			}
			rec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s %s (token=%q): got %d, want %d", tc.method, tc.path, tc.token, rec.Code, tc.want)
			}
		})
	}
}
