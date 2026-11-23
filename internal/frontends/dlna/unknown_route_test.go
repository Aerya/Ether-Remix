package dlna

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
	inst, err := (Frontend{MachineID: "machine"}).Mount(context.Background(), frontend.Host{
		LANViewer: func(_ context.Context, r *http.Request) (*frontend.Viewer, error) {
			if r.Header.Get("X-Remote") != "" {
				return nil, errors.New("remote")
			}
			return &frontend.Viewer{ID: 1}, nil
		},
		Experience: frontendtest.Experience{},
		Sessions:   frontendtest.Sessions{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	cases := []struct {
		name   string
		path   string
		token  string
		remote bool
		want   int
	}{
		{name: "unknown_lan_with_token", path: "/dlna/unknown/route", token: "Bearer some-token", remote: false, want: http.StatusNotFound},
		{name: "unknown_lan_no_token", path: "/dlna/unknown/route", token: "", remote: false, want: http.StatusNotFound},
		{name: "unknown_remote_no_token", path: "/dlna/unknown/route", token: "", remote: true, want: http.StatusNotFound},
		{name: "known_route_remote_forbidden", path: "/dlna/description.xml", token: "", remote: true, want: http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", tc.token)
			}
			if tc.remote {
				req.Header.Set("X-Remote", "1")
			}
			rec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("GET %s (remote=%v): got %d, want %d", tc.path, tc.remote, rec.Code, tc.want)
			}
		})
	}
}
