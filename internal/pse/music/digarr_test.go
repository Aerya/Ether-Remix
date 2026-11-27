package music

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDigarrIsolationAndImport(t *testing.T) {
	a, _ := flowHarness(t)
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer viewer-seven" {
			t.Error("unexpected method or token")
		}
		rec := `{"id":12,"kind":"album","artist":{"name":"Example"},"recommendedReleaseGroupTitle":"Album"}`
		if r.URL.Path == "/api/v1/recommendations" {
			if r.URL.Query().Get("offset") != "20" || r.URL.Query().Get("status") != "pending" {
				t.Error(r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"items":[` + rec + `],"total":21}`))
		} else if r.URL.Path == "/api/v1/recommendations/12" {
			_, _ = w.Write([]byte(rec))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()
	a.Digarr = func(v int64) DigarrConfig {
		if v == 7 {
			return DigarrConfig{remote.URL, "viewer-seven"}
		}
		return DigarrConfig{}
	}
	var incomplete atomic.Bool
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/album":
			_, _ = w.Write([]byte(`{"data":[{"id":31,"title":"Album","artist":{"name":"Example"}},{"id":32,"title":"Album Live","artist":{"name":"Example"}},{"id":33,"title":"Album","artist":{"name":"Other"}}]}`))
		case "/album/31":
			_, _ = w.Write([]byte(`{"id":31,"title":"Album","nb_tracks":1,"artist":{"name":"Example"}}`))
		case "/album/31/tracks":
			if incomplete.Load() {
				_, _ = w.Write([]byte(`{"data":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":41,"title":"Track","artist":{"name":"Example"}}]}`))
		default:
			t.Error("unexpected catalog path", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer catalog.Close()
	a.Sync.Deezer = &DeezerSource{API: catalog.URL, HTTP: catalog.Client()}
	h := a.Handler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	w := request("GET", "/digarr?viewer=8", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) || calls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/digarr?viewer=99", "")
	if w.Code != 400 || calls.Load() != 0 {
		t.Fatal(w.Code)
	}
	w = request("GET", "/digarr?viewer=7&offset=-1", "")
	if w.Code != 400 || calls.Load() != 0 {
		t.Fatal(w.Code)
	}
	w = request("GET", "/digarr?viewer=7&offset=20", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "viewer-seven") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/digarr/albums?viewer=7&id=12", "")
	var albums []Album
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &albums) != nil || len(albums) != 1 || albums[0].ID != 31 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/digarr/import", `{"viewer":7,"recommendation":12,"album":32}`)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/digarr/import", `{"viewer":8,"recommendation":12,"album":31}`)
	if w.Code == 201 {
		t.Fatal("cross viewer import")
	}
	incomplete.Store(true)
	w = request("POST", "/digarr/import", `{"viewer":7,"recommendation":12,"album":31}`)
	if w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	empty, _ := a.Sync.Store.List(t.Context(), 7)
	if len(empty) != 0 {
		t.Fatal("failed import created playlist")
	}
	incomplete.Store(false)
	w = request("POST", "/digarr/import", `{"viewer":7,"recommendation":12,"album":31}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	pls, err := a.Sync.Store.List(t.Context(), 7)
	if err != nil || len(pls) != 1 || len(pls[0].Tracks) != 1 || pls[0].Tracks[0].ID != 41 {
		t.Fatal(pls, err)
	}
	other, _ := a.Sync.Store.List(t.Context(), 8)
	if len(other) != 0 {
		t.Fatal("playlist leak")
	}
}
func TestDigarrRejectsRedirectsAndUnsafeResponses(t *testing.T) {
	a, _ := flowHarness(t)
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	for _, kind := range []string{"redirect", "failure", "malformed", "oversize", "null"} {
		t.Run(kind, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "redirect":
					http.Redirect(w, r, target.URL, 302)
				case "failure":
					http.Error(w, "secret-token", 401)
				case "malformed":
					_, _ = w.Write([]byte("secret-token"))
				case "oversize":
					_, _ = w.Write([]byte(strings.Repeat(" ", 1<<20) + "{}"))
				case "null":
					_, _ = w.Write([]byte("null"))
				}
			}))
			defer remote.Close()
			a.Digarr = func(int64) DigarrConfig { return DigarrConfig{remote.URL, "secret-token"} }
			_, err := a.digarrRecommendation(t.Context(), 7, 12)
			if err == nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatal(err)
			}
		})
	}
	if leaked.Load() != 0 {
		t.Fatal("redirect followed")
	}
}
func TestDigarrConfiguration(t *testing.T) {
	a, _ := flowHarness(t)
	t.Setenv("PSE_DIGARR_URL", "https://digarr.example")
	t.Setenv("PSE_DIGARR_TOKENS", `{"7":"personal-test-value"}`)
	if a.digarrConfig(7).Token != "personal-test-value" || a.digarrConfig(8).Token != "" {
		t.Fatal("token isolation")
	}
	for _, raw := range []string{"ftp://host", "https://user:pass@host", "https://host?token=x", "https://host#x", ""} {
		if _, err := digarrURL(DigarrConfig{raw, "test"}); err == nil {
			t.Fatal(raw)
		}
	}
}
