package vela

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type strictIdentityFake struct{ frontend.SurfaceIdentity }

func (strictIdentityFake) ResolveDevice(_ context.Context, token string, d frontend.Device) (*frontend.Viewer, time.Time, error) {
	if token == "valid-token" && d.ID == "dev-1" {
		return &frontend.Viewer{ID: 1, Username: "viewer"}, time.Now().Add(time.Hour), nil
	}
	return nil, time.Time{}, frontend.ErrUnauthorized
}

func (strictIdentityFake) GrantScope(_ context.Context, token string, d frontend.Device) (string, error) {
	if token == "valid-token" && d.ID == "dev-1" {
		return "family-1", nil
	}
	return "", frontend.ErrUnauthorized
}

func TestUnknownRoutesNeverReturn401(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE vela_replays(key TEXT PRIMARY KEY,digest TEXT NOT NULL,response BLOB,expires INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	inst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(context.Background(), frontend.Host{
		Identity:   strictIdentityFake{},
		Experience: frontendtest.Experience{},
		Sessions:   frontendtest.Sessions{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	cases := []struct {
		name   string
		method string
		path   string
		token  string
		body   string
		want   int
	}{
		{name: "get_unknown_valid_token", method: http.MethodGet, path: "/vela/v0/unknown/route", token: "valid-token", want: http.StatusNotFound},
		{name: "get_unknown_no_token", method: http.MethodGet, path: "/vela/v0/unknown/route", token: "", want: http.StatusNotFound},
		{name: "get_unknown_invalid_token", method: http.MethodGet, path: "/vela/v0/unknown/route", token: "bad-token", want: http.StatusNotFound},
		{name: "post_unknown_no_token_empty_body", method: http.MethodPost, path: "/vela/v0/unknown/route", token: "", body: "", want: http.StatusNotFound},
		{name: "post_unknown_no_token_with_key", method: http.MethodPost, path: "/vela/v0/unknown/route", token: "", body: `{"idempotencyKey":"k1"}`, want: http.StatusNotFound},
		{name: "post_unknown_valid_token_with_key", method: http.MethodPost, path: "/vela/v0/unknown/route", token: "valid-token", body: `{"idempotencyKey":"k2"}`, want: http.StatusNotFound},
		{name: "delete_unknown_no_token", method: http.MethodDelete, path: "/vela/v0/unknown/route", token: "", body: `{"idempotencyKey":"k3"}`, want: http.StatusNotFound},
		{name: "delete_unknown_valid_token", method: http.MethodDelete, path: "/vela/v0/unknown/route", token: "valid-token", body: `{"idempotencyKey":"k4"}`, want: http.StatusNotFound},
		{name: "patch_unknown_no_token", method: http.MethodPatch, path: "/vela/v0/unknown/route", token: "", body: `{"idempotencyKey":"k5"}`, want: http.StatusNotFound},
		{name: "patch_unknown_valid_token", method: http.MethodPatch, path: "/vela/v0/unknown/route", token: "valid-token", body: `{"idempotencyKey":"k6"}`, want: http.StatusNotFound},
		{name: "known_protected_route_no_token", method: http.MethodGet, path: "/vela/v0/libraries", token: "", want: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
				req.Header.Set("X-Vela-Device-Id", "dev-1")
			}
			rec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s %s (token=%q): got %d, want %d (%s)", tc.method, tc.path, tc.token, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
