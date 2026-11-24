package peerclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestAmuleSearchDownloadAndRange(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef"
	name := "Example.2024.FRENCH.1080p.mkv"
	var adds atomic.Int32
	var complete atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/auth/login" {
			if r.URL.Query().Get("include_token") != "true" {
				t.Error("missing token opt-in")
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["password"] != "fixture-password" {
				t.Error("password body")
			}
			io.WriteString(w, `{"token":"fixture-session","expires_at":9999999999}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-session" {
			t.Error("missing auth")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/search":
			io.WriteString(w, `{"search_id":42}`)
		case "GET /api/v1/search/42/results":
			json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"hash": hash, "name": name, "size_bytes": 10}}, "progress": map[string]string{"state": "finished"}})
		case "DELETE /api/v1/search/42":
			w.WriteHeader(204)
		case "POST /api/v1/downloads":
			adds.Add(1)
			io.WriteString(w, `{"results":[{"ok":true}]}`)
		case "GET /api/v1/downloads/" + hash, "GET /api/v1/shared/" + hash:
			if adds.Load() == 0 {
				http.NotFound(w, r)
				return
			}
			state := "downloading"
			n := 3
			if complete.Load() {
				state = "completed"
				n = 10
			}
			json.NewEncoder(w).Encode(map[string]any{"hash": hash, "name": name, "size_bytes": 10, "completed_bytes": n, "status": state, "directory": "/incoming"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, name), []byte("0123456789"), 0600)
	cfg := Settings{Kind: "amule", Enabled: true, URL: srv.URL, Secret: "fixture-password", Root: root, RemoteRoot: "/incoming"}
	s := &Service{DB: store.OpenTest(t), Client: &Client{Settings: cfg}}
	defer s.Close()
	releases, err := s.SearchReleases(t.Context(), core.SearchRequest{Type: core.TypeMovie, Title: "Example", Year: 2024}, nil)
	if err != nil || len(releases) != 1 {
		t.Fatalf("search: %v %v", releases, err)
	}
	for _, ctx := range []context.Context{core.WithoutOnDemandStreams(t.Context()), core.WithOnDemandStreamsForAvailability(t.Context())} {
		if _, _, err = s.Prepare(ctx, releases[0]); !errors.Is(err, core.ErrForegroundOnly) {
			t.Fatalf("background: %v", err)
		}
	}
	if adds.Load() != 0 {
		t.Fatal("background downloaded")
	}
	if _, _, err = s.Prepare(t.Context(), releases[0]); !errors.Is(err, core.ErrDownloadQueued) {
		t.Fatal(err)
	}
	// A new service uses persisted intent and never adds the same transfer again.
	restarted := &Service{DB: s.DB, Client: &Client{Settings: cfg}}
	defer restarted.Close()
	if _, _, err = restarted.Prepare(t.Context(), releases[0]); !errors.Is(err, core.ErrDownloadQueued) {
		t.Fatal(err)
	}
	if adds.Load() != 1 {
		t.Fatalf("duplicate downloads: %d", adds.Load())
	}
	complete.Store(true)
	rel, handled, err := restarted.Prepare(t.Context(), releases[0])
	if err != nil || !handled {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", rel.StreamURL, nil)
	req.Header.Set("Range", "bytes=2-5")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 206 || string(data) != "2345" {
		t.Fatalf("range: %d %q", resp.StatusCode, data)
	}
	bad := releases[0]
	bad.Size++
	if _, _, err = restarted.Prepare(t.Context(), bad); !errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatal("identity mismatch accepted")
	}
}
func TestSlskdProtocol(t *testing.T) {
	h := Hit{Name: "01 - Song.flac", Filename: `Artist\Album\01 - Song.flac`, User: "fixture-user", Size: 10}
	h.ID = hitID(h)
	var queued bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "fixture-key" {
			t.Error("missing API key")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v0/searches":
			io.WriteString(w, `{"id":"11111111-1111-1111-1111-111111111111"}`)
		case "GET /api/v0/searches/11111111-1111-1111-1111-111111111111":
			if r.URL.Query().Get("includeResponses") != "true" {
				t.Error("no responses")
			}
			json.NewEncoder(w).Encode(map[string]any{"isComplete": true, "responses": []any{map[string]any{"username": h.User, "files": []any{map[string]any{"filename": h.Filename, "size": 10}}}}})
		case "DELETE /api/v0/searches/11111111-1111-1111-1111-111111111111":
			w.WriteHeader(204)
		case "POST /api/v0/transfers/downloads/batches":
			var body struct {
				ID, Username string
				Options      struct{ Destination string }
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.ID != batchID(h) || body.Username != h.User || body.Options.Destination != "remix/"+h.ID {
				t.Errorf("wrong batch: %+v", body)
			}
			queued = true
			json.NewEncoder(w).Encode(map[string]any{"failures": []any{}, "batch": map[string]any{"id": batchID(h), "transfers": []any{map[string]any{"filename": h.Filename, "size": 10}}}})
		case "GET /api/v0/transfers/downloads/batches/" + batchID(h):
			if !queued {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"username": h.User, "options": map[string]string{"destination": "remix/" + h.ID}, "transfers": []any{map[string]any{"filename": h.Filename, "size": 10, "bytesTransferred": 10, "state": "Completed, Succeeded"}}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := &Client{Settings: Settings{Kind: "slskd", URL: srv.URL, Secret: "fixture-key"}}
	hits, err := c.Search(t.Context(), "Artist Song")
	if err != nil || len(hits) != 1 || hits[0] != h {
		t.Fatalf("hits: %+v %v", hits, err)
	}
	if err = c.Enqueue(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	tr, err := c.Transfer(t.Context(), h)
	if err != nil || tr.State != "completed" || tr.Relative != filepath.Join("remix", h.ID, h.Name) {
		t.Fatalf("transfer: %+v %v", tr, err)
	}
}
func TestRootConfinement(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "private"), []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	s := &Service{Client: &Client{Settings: Settings{Root: root}}}
	for _, p := range []string{"../private", "/etc/passwd", "escape/private"} {
		if f, e := s.Open(p, 6); e == nil {
			f.Close()
			t.Fatalf("escaped: %s", p)
		}
	}
	os.WriteFile(filepath.Join(root, "ok"), []byte("123"), 0600)
	if f, e := s.Open("ok", 4); e == nil {
		f.Close()
		t.Fatal("wrong size accepted")
	}
}
func TestHTTPDoesNotLeakCredentials(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	c := &Client{Settings: Settings{Kind: "slskd", URL: redirect.URL, Secret: "private-fixture"}}
	err := c.Test(t.Context())
	if err == nil || leaked.Load() || strings.Contains(err.Error(), "private-fixture") {
		t.Fatal("redirect or secret leakage")
	}
}
