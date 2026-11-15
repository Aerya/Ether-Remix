package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Appearance is a public immutable snapshot, shared across viewer bootstraps;
// conditional requests and older in-flight config reads must not regress it.
func TestAppearanceSnapshot(t *testing.T) {
	cfg := config.Config{Frontends: map[string]config.Frontend{}}
	i := &instance{ctx: context.Background(), mux: http.NewServeMux(), host: frontend.Host{HostConfig: func() config.Config { return cfg }}}
	i.routes()
	get := func(tag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/vela/v0/appearance", nil)
		r.Header.Set("If-None-Match", tag)
		w := httptest.NewRecorder()
		i.Handler().ServeHTTP(w, r)
		return w
	}
	w := get("")
	var doc struct {
		Revision         uint64
		DefaultSkinID    string
		DefaultEngineID  string
		Packs, Documents []json.RawMessage
		Force            bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || doc.Revision != 0 || doc.DefaultSkinID != "cinematic" || doc.DefaultEngineID != "cinematic" || doc.Force || doc.Packs == nil || doc.Documents == nil {
		t.Fatalf("default: %d %s", w.Code, w.Body.String())
	}
	cached := i.appearanceDocument()
	for _, tag := range []string{cached.etag, "W/" + cached.etag, `"other", ` + cached.etag, "*"} {
		if w := get(tag); w.Code != 304 || w.Body.Len() != 0 {
			t.Fatalf("conditional %s: %d", tag, w.Code)
		}
	}
	if cached != i.appearanceDocument() {
		t.Fatal("snapshot rebuilt without changes")
	}
	cfg.Frontends["vela"] = config.Frontend{AppearanceRevision: 1, Settings: map[string]any{"defaultSkinId": "lounge", "secret": "never public"}}
	w = get(cached.etag)
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || doc.Revision != 1 || doc.DefaultSkinID != "lounge" || doc.DefaultEngineID != "doc" || w.Header().Get("ETag") == cached.etag {
		t.Fatalf("changed: %d %s", w.Code, w.Body.String())
	}
	newer := i.appearanceDocument()
	cfg.Frontends["vela"] = config.Frontend{}
	if i.appearanceDocument() != newer {
		t.Fatal("older snapshot replaced newer revision")
	}
	w = httptest.NewRecorder()
	i.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/vela/v0/events", nil))
	if w.Code != 401 {
		t.Fatalf("events must authenticate: %d", w.Code)
	}
}
