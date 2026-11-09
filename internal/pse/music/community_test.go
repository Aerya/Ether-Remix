package music

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestCommunityPlaybackAndDiagnosticOrder(t *testing.T) {
	addon, _ := fakeEclipse(t, "fLaC")
	cfg := Settings{CommunityURLs: [6]string{addon.URL + "/u/tok/manifest.json"}, Debrid: true}
	if !cfg.Enabled() || cfg.Mode() != "eclipse+alldebrid" {
		t.Fatalf("not enabled: %+v", cfg)
	}
	rs, plan := chain(cfg, nil, addon.Client())
	if len(rs) != 1 || rs[0].name != "Tido" || !plan[0].Active {
		t.Fatalf("chain: %+v %+v", rs, plan)
	}
	stream, err := rs[0].r.Resolve(context.Background(), Track{ID: 1, Title: "One More Time", Artist: Artist{Name: "Daft Punk"}, Duration: 320})
	if err != nil || stream.Provider != "Tido" {
		t.Fatalf("stream: %+v %v", stream, err)
	}
	cfg.EclipseURLs = cfg.CommunityURLs[0]
	if len(cfg.eclipseSources(nil)) != 1 {
		t.Fatal("duplicate resolver")
	}
	for i, a := range CommunityAddons() {
		s := Settings{}
		s.CommunityURLs[i] = addon.URL + "/u/tok/manifest.json"
		if !s.Enabled() || s.NewSource(nil) == nil || s.eclipseSources(nil)[0].name != a.Name {
			t.Fatalf("missing %s", a.Name)
		}
	}
}

func TestRegistryRefreshPersistenceAndFailure(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	fail := false
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.URL.Path != "/addonstore/registry.json" || req.URL.RawQuery != "" {
			t.Error("unexpected request")
		}
		if fail {
			http.Error(w, "private upstream error", 503)
			return
		}
		fmt.Fprint(w, `{"addons":[{"id":"com.eclipse.community.tido","setupUrl":"https://new.example.org/"},{"id":"unknown","setupUrl":"https://unknown.example.org/"}]}`)
	}))
	defer srv.Close()
	hc := srv.Client()
	hc.Transport = registryTransport{base: srv.URL, transport: hc.Transport}
	var r AddonRegistry
	if err := r.Load(ctx, db); err != nil {
		t.Fatal(err)
	}
	r.client = hc
	r.Refresh(ctx)
	got := r.Snapshot()
	if len(got.Addons) != 6 || !got.Addons[0].Changed || got.Addons[0].SetupURL != "https://new.example.org/" || got.Error != "" {
		t.Fatalf("%+v", got)
	}
	// The public cache contains neither configured manifests nor credentials.
	var raw string
	if err := db.QueryRow("SELECT snapshot FROM pse_music_addon_registry WHERE id=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "manifest.json") {
		t.Fatal(raw)
	}
	var restored AddonRegistry
	if err := restored.Load(ctx, db); err != nil {
		t.Fatal(err)
	}
	if restored.Snapshot().Addons[0].SetupURL != got.Addons[0].SetupURL {
		t.Fatal("cache lost")
	}
	r.Refresh(ctx)
	if calls != 1 {
		t.Fatal("retry not throttled")
	}
	fail = true
	r.mu.Lock()
	r.state.CheckedAt = time.Now().Add(-2 * time.Minute)
	r.mu.Unlock()
	r.Refresh(ctx)
	got = r.Snapshot()
	if got.Error == "" || got.Addons[0].SetupURL != "https://new.example.org/" || strings.Contains(got.Error, "private") {
		t.Fatalf("%+v", got)
	}
	// Snapshots must not share writable slices with the cache.
	got.Addons[0].SetupURL = "bad"
	if r.Snapshot().Addons[0].SetupURL == "bad" {
		t.Fatal("shared snapshot")
	}
}

type registryTransport struct {
	base      string
	transport http.RoundTripper
}

func (r registryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u, _ := http.NewRequest(req.Method, r.base+req.URL.RequestURI(), nil)
	clone.URL = u.URL
	return r.transport.RoundTrip(clone)
}

func TestRegistryRejectsUnsafeLinks(t *testing.T) {
	for _, u := range []string{"http://example.org/", "https://localhost/", "https://127.0.0.1/", "https://user:secret@example.org/", "https://example.org/?token=private", "javascript:alert(1)"} {
		body := fmt.Sprintf(`{"addons":[{"id":"com.eclipse.community.tido","setupUrl":%q}]}`, u)
		if _, err := parseAddonRegistry(strings.NewReader(body)); err == nil {
			t.Errorf("accepted %s", u)
		}
	}
	if _, err := parseAddonRegistry(strings.NewReader(`{"addons":[]}`)); err == nil {
		t.Fatal("empty")
	}
}

func TestRegistryRejectsMethods(t *testing.T) {
	var r AddonRegistry
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/", nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	body, _ := io.ReadAll(w.Result().Body)
	if w.Code != 200 || !strings.Contains(string(body), "Internet Archive") {
		t.Fatal(string(body))
	}
}

func TestCommunityFallsBackAfterMissingTrack(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"tracks":[]}`) }))
	defer bad.Close()
	good, _ := fakeEclipse(t, "fLaC")
	cfg := Settings{CommunityURLs: [6]string{bad.URL + "/manifest.json", good.URL + "/u/tok/manifest.json"}}
	var resolvers Resolvers
	for _, n := range cfg.eclipseSources(nil) {
		resolvers = append(resolvers, n.r)
	}
	stream, err := resolvers.Resolve(context.Background(), Track{ID: 1, Title: "One More Time", Artist: Artist{Name: "Daft Punk"}, Duration: 320})
	if err != nil || stream.Provider != "Internet Archive" {
		t.Fatalf("%+v %v", stream, err)
	}
}
