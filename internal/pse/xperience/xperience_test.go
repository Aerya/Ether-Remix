package xperience

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/stremio"
)

const testLink = "https://xperience-app.com/manifest/profile/private-test-token/manifest.json"
const fixture = `{"id":"xperience.fixture","name":"Xperience","version":"1.0.0","resources":["catalog","meta"],"types":["movie","series"],"catalogs":[{"id":"animation","name":"Animated","type":"movie","extra":[{"name":"genre","isRequired":true,"options":["None","Animation"]},{"name":"skip"}]},{"id":"animation","name":"Animated","type":"series","extra":[{"name":"genre","isRequired":true,"options":["None","Animation"]},{"name":"skip"}]},{"id":"trending","name":"Trending","type":"movie","extra":[{"name":"skip"}]},{"id":"search","type":"movie","extra":[{"name":"search","isRequired":true}]}]}`

type fakeBackend struct {
	man      stremio.Manifest
	installs int
	hours    int
	err      error
}

func (b *fakeBackend) Preview(context.Context, string) (stremio.Manifest, error) { return b.man, b.err }
func (b *fakeBackend) Install(_ context.Context, _ string, hours int) (string, stremio.Manifest, error) {
	b.installs++
	b.hours = hours
	return "xperience-fixture", b.man, b.err
}

type fakeCols struct {
	specs []core.CollectionSpec
	fail  int
}

func (f *fakeCols) ListSpecs(context.Context) ([]core.CollectionSpec, error) {
	return append([]core.CollectionSpec{}, f.specs...), nil
}
func (f *fakeCols) CreateSpec(_ context.Context, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	if f.fail > 0 && len(f.specs)+1 == f.fail {
		return nil, fmt.Errorf("private error %s", testLink)
	}
	sp.ID = fmt.Sprint(len(f.specs) + 1)
	// Round-trip like persisted specs: empty optional maps become nil.
	raw, _ := json.Marshal(sp)
	_ = json.Unmarshal(raw, &sp)
	if len(sp.Sources[0].Params) == 0 {
		sp.Sources[0].Params = nil
	}
	f.specs = append(f.specs, sp)
	return &sp, nil
}
func setup(t *testing.T) (*Service, *fakeCols, *libraries.Service, *fakeBackend) {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenPath(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	libs, err := libraries.New(ctx, db, libraries.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBackend{}
	if err = json.Unmarshal([]byte(fixture), &b.man); err != nil {
		t.Fatal(err)
	}
	cols := &fakeCols{}
	return New(cols, libs, b), cols, libs, b
}
func request() Request {
	return Request{Manifest: testLink, Mode: "both", MaxItems: 500, Catalogs: []Choice{{ID: "animation", Type: "movie", Name: "Dessins animés", Params: map[string]string{"genre": "None"}}, {ID: "animation", Type: "series", Name: "Séries animées", Params: map[string]string{"genre": "Animation"}}, {ID: "trending", Type: "movie", Name: "Tendances"}}}
}
func TestPreviewAndURLValidation(t *testing.T) {
	s, cols, _, b := setup(t)
	cats, err := s.Preview(context.Background(), testLink)
	if err != nil || len(cats) != 3 {
		t.Fatalf("preview %+v %v", cats, err)
	}
	if cats[0].Defaults["genre"] != "None" || len(cats[0].Extras) != 1 || b.installs != 0 || len(cols.specs) != 0 {
		t.Fatal("preview writes or loses extras")
	}
	for _, link := range []string{"http://xperience-app.com/manifest/a/manifest.json", "https://xperience-app.com.evil/manifest/a/manifest.json", "https://xperience-app.com@evil/manifest/a/manifest.json", "https://xperience-app.com:443/manifest/a/manifest.json", testLink + "?token=secret", "https://127.0.0.1/manifest/a/manifest.json"} {
		if _, err := Normalize(link); err == nil {
			t.Fatalf("accepted %s", link)
		}
	}
	if v, err := Normalize(strings.Replace(testLink, "https://", "stremio://", 1)); err != nil || v != testLink {
		t.Fatal(v, err)
	}
}
func TestImportConcurrentPreservesEditsAndNoSecrets(t *testing.T) {
	s, cols, libs, _ := setup(t)
	ctx := context.Background()
	req := request()
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.Apply(ctx, req)
			if err != nil || len(out.Collections) != 3 || len(out.Libraries) != 3 {
				t.Errorf("apply %+v %v", out, err)
			}
		}()
	}
	wg.Wait()
	if len(cols.specs) != 3 {
		t.Fatal("duplicated collections")
	}
	all, _ := libs.List(ctx)
	if len(all) != 5 {
		t.Fatal("duplicated libraries")
	}
	for i, sp := range cols.specs {
		if sp.Refresh != "daily" || sp.Sort != "source" || sp.Rotate != nil || !sp.Enabled || !sp.BrowseInApps || sp.Sources[0].MaxItems != 500 {
			t.Fatalf("defaults %+v", sp)
		}
		if all[i+2].Present.Plex == nil || all[i+2].Present.MediaBrowser == nil {
			t.Fatal("missing presentations")
		}
		raw, _ := json.Marshal(sp)
		if strings.Contains(string(raw), "private-test-token") || strings.Contains(string(raw), "https://") {
			t.Fatal("manifest leaked into spec")
		}
	}
	cols.specs[0].Name = "My edit"
	cols.specs[0].Sort = "rating"
	cols.specs[0].Enabled = false
	_ = libs.Rename(ctx, all[2].ID, "My library")
	if _, err := s.Apply(ctx, req); err != nil || cols.specs[0].Name != "My edit" || cols.specs[0].Sort != "rating" || cols.specs[0].Enabled {
		t.Fatal("overwritten edits", err)
	}
	lib, _ := libs.Get(ctx, all[2].ID)
	if lib.Name != "My library" {
		t.Fatal("renamed existing library")
	}
}
func TestRetryModesAndChangedSource(t *testing.T) {
	s, cols, libs, _ := setup(t)
	ctx := context.Background()
	req := request()
	cols.fail = 2
	out, err := s.Apply(ctx, req)
	if err == nil || len(out.Collections) != 1 || len(out.Libraries) != 1 || strings.Contains(err.Error(), "private-test-token") {
		t.Fatal(out, err)
	}
	cols.fail = 0
	req.Mode = "collections"
	out, err = s.Apply(ctx, req)
	if err != nil || len(cols.specs) != 3 || len(out.Libraries) != 0 {
		t.Fatal(out, err)
	}
	all, _ := libs.List(ctx)
	if len(all) != 3 {
		t.Fatal("collections-only added libraries")
	}
	req.Mode = "both"
	out, err = s.Apply(ctx, req)
	if err != nil || len(out.Libraries) != 3 || len(cols.specs) != 3 {
		t.Fatal(out, err)
	}
	cols.specs[0].OwnerUserID = 42
	cols.specs[1].Sources[0].Kind = "changed"
	_, err = s.Apply(ctx, req)
	if err != nil || len(cols.specs) != 5 {
		t.Fatal("reused private/changed selection", err)
	}
	s2, cols2, _, _ := setup(t)
	req.Mode = "libraries"
	out, err = s2.Apply(ctx, req)
	if err != nil || len(out.Libraries) != 3 {
		t.Fatal(out, err)
	}
	for _, sp := range cols2.specs {
		if sp.BrowseInApps {
			t.Fatal("backing collection published")
		}
	}
}
func TestValidateBatchBeforeInstallation(t *testing.T) {
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Catalogs[1].ID = "missing" }, func(r *Request) { r.Catalogs[1].Params = nil }, func(r *Request) { r.Catalogs[1].Params["genre"] = "unknown" }, func(r *Request) { r.Catalogs[1].Params["skip"] = "50" }, func(r *Request) { r.Catalogs[1].Type = "movie"; r.Catalogs[1].ID = "search" }, func(r *Request) { r.MaxItems = 2001 }, func(r *Request) { r.Mode = "bad" }, func(r *Request) { r.Catalogs[1].Name = "" },
	} {
		s, cols, _, b := setup(t)
		req := request()
		mutate(&req)
		if _, err := s.Apply(context.Background(), req); err == nil || len(cols.specs) > 0 || b.installs > 0 {
			t.Fatal("invalid batch wrote state", err)
		}
	}
}
func TestHandlerRedactsErrors(t *testing.T) {
	s, _, _, b := setup(t)
	b.err = fmt.Errorf("GET %s: network error", testLink)
	for _, path := range []string{"/preview", "/"} {
		raw, _ := json.Marshal(request())
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(string(raw))))
		if w.Code != 400 || strings.Contains(w.Body.String(), "private-test-token") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/preview", nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
}

func TestRefreshIntervals(t *testing.T) {
	s, cols, _, b := setup(t)
	req := request()
	req.ManifestRefreshHours = 6
	req.CollectionRefreshHours = 12
	if _, err := s.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if b.hours != 6 || cols.specs[0].Refresh != "12h" {
		t.Fatalf("schedules lost: %d %s", b.hours, cols.specs[0].Refresh)
	}
	req.CollectionRefreshHours = 48
	if _, err := s.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(cols.specs) != 3 || cols.specs[0].Refresh != "12h" {
		t.Fatal("reimport changed existing collection")
	}
	for _, hours := range []int{-1, 169} {
		req.ManifestRefreshHours = hours
		before := b.installs
		if _, err := s.Apply(context.Background(), req); err == nil || b.installs != before {
			t.Fatal("invalid interval installed")
		}
	}
}
