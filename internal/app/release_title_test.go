package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func TestReleaseTitleIDs(t *testing.T) {
	for _, tc := range []struct {
		name, search, detail string
		kind                 core.MediaType
		want                 core.IDs
		wantErr              bool
	}{
		{"TV alias", `{"total_pages":1,"total_results":1,"results":[{"id":456,"name":"Los Simpson"}]}`, `{"id":456,"external_ids":{"imdb_id":"tt0096697","tvdb_id":71663}}`, core.TypeShow, core.IDs{TMDB: 456, IMDB: "tt0096697", TVDB: 71663}, false},
		{"movie", `{"total_pages":1,"total_results":1,"results":[{"id":348}]}`, `{"id":348,"imdb_id":"tt0078748"}`, core.TypeMovie, core.IDs{TMDB: 348, IMDB: "tt0078748"}, false},
		{"ambiguous", `{"total_pages":1,"total_results":2,"results":[{"id":456},{"id":999}]}`, "", core.TypeShow, core.IDs{}, false},
		{"more pages", `{"total_pages":2,"total_results":21,"results":[{"id":456}]}`, "", core.TypeShow, core.IDs{}, false},
		{"missing count", `{"results":[{"id":456}]}`, "", core.TypeShow, core.IDs{}, false},
		{"empty", `{"total_pages":0,"total_results":0,"results":[]}`, "", core.TypeShow, core.IDs{}, false},
		{"invalid ID", `{"total_pages":1,"total_results":1,"results":[{"id":0}]}`, "", core.TypeShow, core.IDs{}, false},
		{"wrong detail identity", `{"total_pages":1,"total_results":1,"results":[{"id":456}]}`, `{"id":999}`, core.TypeShow, core.IDs{}, true},
		{"search error", "error", "", core.TypeShow, core.IDs{}, true},
		{"detail error", `{"total_pages":1,"total_results":1,"results":[{"id":456}]}`, "error", core.TypeShow, core.IDs{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			searches, details := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := tc.detail
				namespace := "tv"
				if tc.kind == core.TypeMovie {
					namespace = "movie"
				}
				if r.URL.Path == "/search/"+namespace {
					searches++
					if r.URL.Query().Get("query") != "Los Simpsons" {
						t.Errorf("query = %q", r.URL.RawQuery)
					}
					if r.URL.Query().Has("year") || r.URL.Query().Has("first_air_date_year") {
						t.Error("lookup constrained by requested year")
					}
					body = tc.search
				} else {
					details++
				}
				if body == "error" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if body == "" {
					t.Error("unexpected detail fetch")
				}
				fmt.Fprint(w, body)
			}))
			defer srv.Close()
			a := &App{tmdb: tmdb.New("test-key", quietLog(), tmdb.WithBaseURL(srv.URL), tmdb.WithMaxRetries(0))}
			got, err := a.releaseTitleIDs(t.Context(), tc.kind, "Los Simpsons")
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("IDs=%+v err=%v, want %+v error=%v", got, err, tc.want, tc.wantErr)
			}
			if searches != 1 || (tc.detail == "" && details != 0) {
				t.Fatalf("searches=%d details=%d", searches, details)
			}
		})
	}
}

func TestVerifyTitleIDs(t *testing.T) {
	expected := core.IDs{TMDB: 65282, IMDB: "tt7997010", TVDB: 298704}
	cases := []struct {
		name     string
		kind     core.MediaType
		reported core.IDs
		expected core.IDs
		routes   map[string]string
		want     resolver.TitleIDVerdict
	}{
		{
			name:     "live external IDs updated on TMDB",
			kind:     core.TypeShow,
			reported: core.IDs{TVDB: 298704},
			expected: core.IDs{TMDB: 65282, TVDB: 111111}, // stale local TVDB
			routes: map[string]string{
				"/tv/65282": `{"id":65282,"external_ids":{"imdb_id":"tt7997010","tvdb_id":298704}}`,
			},
			want: resolver.TitleIDVerified,
		},
		{
			name:     "alternate TVDB ID resolves to expected TV show via /find",
			kind:     core.TypeShow,
			reported: core.IDs{TVDB: 998877},
			expected: expected,
			routes: map[string]string{
				"/tv/65282":    `{"id":65282,"external_ids":{"imdb_id":"tt7997010","tvdb_id":298704}}`,
				"/find/998877": `{"tv_results":[{"id":65282,"name":"Home Alone"}]}`,
			},
			want: resolver.TitleIDVerified,
		},
		{
			name:     "episode IMDb ID resolves to expected show_id via /find",
			kind:     core.TypeShow,
			reported: core.IDs{IMDB: "tt12345678"},
			expected: expected,
			routes: map[string]string{
				"/tv/65282":        `{"id":65282,"external_ids":{"imdb_id":"tt7997010","tvdb_id":298704}}`,
				"/find/tt12345678": `{"tv_episode_results":[{"id":555,"show_id":65282}]}`,
			},
			want: resolver.TitleIDVerified,
		},
		{
			name:     "conflicting TVDB ID resolves to different TV show",
			kind:     core.TypeShow,
			reported: core.IDs{TVDB: 77961},
			expected: expected,
			routes: map[string]string{
				"/tv/65282":   `{"id":65282,"external_ids":{"imdb_id":"tt7997010","tvdb_id":298704}}`,
				"/find/77961": `{"tv_results":[{"id":99999,"name":"Number 96"}]}`,
			},
			want: resolver.TitleIDConflict,
		},
		{
			name:     "movie IMDb ID mis-tagged on TV episode returns WrongMedia",
			kind:     core.TypeShow,
			reported: core.IDs{IMDB: "tt0099785"},
			expected: expected,
			routes: map[string]string{
				"/tv/65282":       `{"id":65282,"external_ids":{"imdb_id":"tt7997010","tvdb_id":298704}}`,
				"/find/tt0099785": `{"movie_results":[{"id":771,"title":"Home Alone"}]}`,
			},
			want: resolver.TitleIDWrongMedia,
		},
		{
			name:     "unmapped external ID remains Conflict",
			kind:     core.TypeShow,
			reported: core.IDs{TVDB: 555555},
			expected: expected,
			routes: map[string]string{
				"/tv/65282":    `{"id":65282,"external_ids":{"imdb_id":"tt7997010","tvdb_id":298704}}`,
				"/find/555555": `{"movie_results":[],"tv_results":[],"tv_episode_results":[]}`,
			},
			want: resolver.TitleIDConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, ok := tc.routes[r.URL.Path]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				fmt.Fprint(w, body)
			}))
			defer srv.Close()
			a := &App{tmdb: tmdb.New("test-key", quietLog(), tmdb.WithBaseURL(srv.URL), tmdb.WithMaxRetries(0))}
			got, err := a.verifyTitleIDs(t.Context(), tc.kind, tc.reported, tc.expected)
			if err != nil {
				t.Fatalf("verifyTitleIDs err = %v", err)
			}
			if got != tc.want {
				t.Fatalf("verifyTitleIDs = %v, want %v", got, tc.want)
			}
		})
	}
}
