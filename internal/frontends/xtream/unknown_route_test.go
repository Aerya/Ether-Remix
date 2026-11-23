package xtream

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnknownRoutesNeverReturn401(t *testing.T) {
	host, _ := testHost(t)
	h := mount(t, host)

	cases := []struct {
		name string
		path string
		want int
	}{
		{name: "unknown_with_valid_token", path: "/xtream/unknown/route?" + authQuery(), want: http.StatusNotFound},
		{name: "unknown_with_no_token", path: "/xtream/unknown/route", want: http.StatusNotFound},
		{name: "unknown_with_invalid_token", path: "/xtream/unknown/route?username=bad&password=bad", want: http.StatusNotFound},
		{name: "known_route_no_token", path: "/xtream/player_api.php", want: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.want {
				t.Fatalf("GET %s: got %d, want %d", tc.path, rec.Code, tc.want)
			}
		})
	}
}
