package plex

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnknownRoutesNeverReturn401(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	cases := []struct {
		name   string
		method string
		path   string
		token  string
		want   int
	}{
		{name: "unknown_valid_token", method: http.MethodGet, path: "/unknown/route", token: ownerToken, want: http.StatusNotFound},
		{name: "unknown_no_token", method: http.MethodGet, path: "/unknown/route", token: "", want: http.StatusNotFound},
		{name: "unknown_invalid_token", method: http.MethodGet, path: "/unknown/route", token: "invalid-token", want: http.StatusNotFound},
		{name: "method_not_allowed_no_token", method: http.MethodPost, path: "/library/sections", token: "", want: http.StatusNotFound},
		{name: "method_not_allowed_valid_token", method: http.MethodPost, path: "/library/sections", token: ownerToken, want: http.StatusNotFound},
		{name: "resources_no_token", method: http.MethodGet, path: "/:/resources/missing.png", token: "", want: http.StatusNotFound},
		{name: "resources_valid_token", method: http.MethodGet, path: "/:/resources/missing.png", token: ownerToken, want: http.StatusNotFound},
		{name: "unknown_metadata_element_no_token", method: http.MethodGet, path: "/library/metadata/1/unknownElement", token: "", want: http.StatusNotFound},
		{name: "unknown_metadata_element_valid_token", method: http.MethodGet, path: "/library/metadata/1/unknownElement", token: ownerToken, want: http.StatusNotFound},
		{name: "unknown_section_sub_no_token", method: http.MethodGet, path: "/library/sections/1/unknownSub", token: "", want: http.StatusNotFound},
		{name: "unknown_section_sub_valid_token", method: http.MethodGet, path: "/library/sections/1/unknownSub", token: ownerToken, want: http.StatusNotFound},
		{name: "unknown_section_query_subkey_no_token", method: http.MethodGet, path: "/library/sections/1/query/unknown", token: "", want: http.StatusNotFound},
		{name: "unknown_section_query_subkey_valid_token", method: http.MethodGet, path: "/library/sections/1/query/unknown", token: ownerToken, want: http.StatusNotFound},
		{name: "known_protected_route_no_token", method: http.MethodGet, path: "/library/sections", token: "", want: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.RemoteAddr = "192.168.1.50:51000"
			if tc.token != "" {
				req.Header.Set("X-Plex-Token", tc.token)
			}
			rec := httptest.NewRecorder()
			f.srv.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s %s (token=%q): got %d, want %d", tc.method, tc.path, tc.token, rec.Code, tc.want)
			}
		})
	}
}
