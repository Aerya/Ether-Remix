package mediabrowser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/watchparty"
)

func TestRouteAuthenticationCoverage(t *testing.T) {
	param := regexp.MustCompile(`\{[^}]+\}`)
	for _, f := range []frontend.Frontend{Jellyfin{MachineID: "test"}, Emby{MachineID: "test"}} {
		t.Run(string(f.Describe().ID), func(t *testing.T) {
			ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return nil, errors.New("invalid") }}
			inst, err := f.Mount(context.Background(), frontend.Host{Identity: ident, WatchParty: watchparty.New(watchparty.Options{})})
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Close(context.Background())
			routes := inst.(*instance).Routes()
			for _, rt := range routes {
				if publicRoutesAllowlist[rt] {
					continue
				}
				path := param.ReplaceAllStringFunc(rt.Pattern, func(m string) string {
					if m == "{name}" {
						return "stream"
					}
					return "testvalue"
				})
				for _, prefix := range []string{"", "/" + string(f.Describe().ID)} {
					req := httptest.NewRequest(rt.Method, prefix+path, strings.NewReader(""))
					rec := httptest.NewRecorder()
					inst.Handler().ServeHTTP(rec, req)
					if rec.Code != 401 {
						t.Errorf("%s %s: got %d, want 401", rt.Method, prefix+rt.Pattern, rec.Code)
					}
				}
			}
		})
	}
}

func TestUnknownRoutesReturn404(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1, Username: "viewer"}
	ident := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token == "valid-token" {
			return viewer, nil
		}
		return nil, frontend.ErrUnauthorized
	}}
	for _, f := range []frontend.Frontend{Jellyfin{MachineID: "test"}, Emby{MachineID: "test"}} {
		id := string(f.Describe().ID)
		t.Run(id, func(t *testing.T) {
			inst, err := f.Mount(context.Background(), frontend.Host{Identity: ident, WatchParty: watchparty.New(watchparty.Options{})})
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
				{name: "unknown_valid_token", method: http.MethodGet, path: "/NoSuchPlugin/Endpoint", token: "valid-token", want: http.StatusNotFound},
				{name: "unknown_no_token", method: http.MethodGet, path: "/NoSuchPlugin/Endpoint", token: "", want: http.StatusNotFound},
				{name: "unknown_invalid_token", method: http.MethodGet, path: "/NoSuchPlugin/Endpoint", token: "bad-token", want: http.StatusNotFound},
				{name: "prefixed_unknown_no_token", method: http.MethodGet, path: "/" + id + "/NoSuchPlugin/Endpoint", token: "", want: http.StatusNotFound},
				{name: "prefixed_unknown_valid_token", method: http.MethodGet, path: "/" + id + "/NoSuchPlugin/Endpoint", token: "valid-token", want: http.StatusNotFound},
				{name: "unknown_video_subpath_no_token", method: http.MethodGet, path: "/Videos/1/not-a-stream-alias", token: "", want: http.StatusNotFound},
				{name: "unknown_video_subpath_bad_token", method: http.MethodGet, path: "/Videos/1/not-a-stream-alias", token: "bad-token", want: http.StatusNotFound},
				{name: "unknown_video_subpath_valid_token", method: http.MethodGet, path: "/Videos/1/not-a-stream-alias", token: "valid-token", want: http.StatusNotFound},
				{name: "unknown_audio_subpath_no_token", method: http.MethodGet, path: "/Audio/1/not-a-stream-alias", token: "", want: http.StatusNotFound},
			}
			if id == "jellyfin" {
				cases = append(cases,
					struct {
						name, method, path, token string
						want                      int
					}{name: "emby_only_channel_tags_no_token", method: http.MethodGet, path: "/LiveTv/ChannelTags", token: "", want: http.StatusNotFound},
					struct {
						name, method, path, token string
						want                      int
					}{name: "emby_only_channel_tags_valid_token", method: http.MethodGet, path: "/LiveTv/ChannelTags", token: "valid-token", want: http.StatusNotFound},
				)
			} else {
				cases = append(cases,
					struct {
						name, method, path, token string
						want                      int
					}{name: "jellyfin_only_items_root_no_token", method: http.MethodGet, path: "/Items/Root", token: "", want: http.StatusNotFound},
					struct {
						name, method, path, token string
						want                      int
					}{name: "jellyfin_only_items_root_valid_token", method: http.MethodGet, path: "/Items/Root", token: "valid-token", want: http.StatusNotFound},
				)
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					req := httptest.NewRequest(tc.method, tc.path, nil)
					if tc.token != "" {
						req.Header.Set("X-Emby-Token", tc.token)
					}
					rec := httptest.NewRecorder()
					inst.Handler().ServeHTTP(rec, req)
					if rec.Code != tc.want {
						t.Fatalf("%s %s (token=%q): got %d, want %d", tc.method, tc.path, tc.token, rec.Code, tc.want)
					}
				})
			}
		})
	}
}
