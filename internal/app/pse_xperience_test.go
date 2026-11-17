package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/pse/xperience"
	"github.com/braedonsaunders/ether/internal/store"
)

type xperienceTransport struct {
	mu    sync.Mutex
	paths []string
}

func (f *xperienceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.mu.Unlock()
	body := `{"metas":[]}`
	if strings.HasSuffix(r.URL.Path, "/manifest.json") {
		body = `{"id":"xperience-test","name":"Xperience test","version":"1.0.0","resources":["catalog","meta"],"types":["movie"],"catalogs":[{"id":"animation","name":"Animated","type":"movie","extra":[{"name":"genre","isRequired":true,"options":["None","Animation"]},{"name":"skip"}]}]}`
	} else if strings.HasSuffix(r.URL.Path, "/catalog/movie/animation/genre=None.json") {
		// Reverse popularity order to prove source ordering survives.
		body = `{"metas":[{"id":"tt0000102","type":"movie","name":"Second"},{"id":"tt0000101","type":"movie","name":"First"}]}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}
func TestXperienceImportThroughPluginAndCollections(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.New(db, nil, config.Catalog{Language: "en-US", Region: "US"}, log)
	if err != nil {
		t.Fatal(err)
	}
	for i, imdb := range []string{"tt0000101", "tt0000102"} {
		title := core.Title{Key: core.MovieKey(int64(101 + i)), Type: core.TypeMovie, IDs: core.IDs{TMDB: int64(101 + i), IMDB: imdb}, Title: imdb, Year: 2000, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Popularity: float64(100 - i), VoteCount: 100}
		if err = cat.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}
	cols, err := collections.New(db, cat, log, collections.WithSealer(cfg))
	if err != nil {
		t.Fatal(err)
	}
	libs, err := libraries.New(ctx, db, libraries.Options{})
	if err != nil {
		t.Fatal(err)
	}
	transport := &xperienceTransport{}
	client := &http.Client{Transport: transport}
	mgr, err := plugins.New(plugins.Deps{DB: db, Registry: extend.New(), Logger: log, IsLeader: func() bool { return true }, FetchClient: client, Sealer: cfg})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("remote", func(inst plugins.Installed) (plugins.Runtime, error) {
		return linkRuntime{base: inst.SourceURL, client: client}, nil
	})
	adapters.Attach(mgr, cols, nil, nil)
	app := &App{db: db, cfg: cfg, log: log, catalog: cat, collections: cols, libraries: libs, pluginsRT: &pluginRuntime{mgr: mgr}}
	service := xperience.New(cols, libs, xperienceBackend{app: app})
	link := "https://xperience-app.com/manifest/test/private-fixture/manifest.json"
	if _, err = service.Preview(ctx, link); err != nil {
		t.Fatal(err)
	}
	before, _ := mgr.List(ctx)
	if len(before) != 0 {
		t.Fatal("preview installed source")
	}
	req := xperience.Request{Manifest: link, Mode: "both", MaxItems: 100, ManifestRefreshHours: 6, CollectionRefreshHours: 12, Catalogs: []xperience.Choice{{ID: "animation", Type: "movie", Name: "Dessins animés", Params: map[string]string{"genre": "None"}}}}
	out, err := service.Apply(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Collections) != 1 || len(out.Libraries) != 1 {
		t.Fatal(out)
	}
	sp, err := cols.Rebuild(ctx, out.Collections[0])
	if err != nil || sp.ItemCount != 2 {
		t.Fatalf("build %+v %v", sp, err)
	}
	ratingKey, err := core.ParseRatingKey(sp.RatingKey)
	if err != nil {
		t.Fatal(err)
	}
	page, err := cols.SnapshotItems(ctx, ratingKey, 0, 10)
	if err != nil || len(page.Items) != 2 || page.Items[0].Key != core.MovieKey(102) || page.Items[1].Key != core.MovieKey(101) {
		t.Fatalf("source order: %+v %v", page, err)
	}
	if sp.Refresh != "12h" {
		t.Fatalf("collection interval = %s", sp.Refresh)
	}
	raw, _ := json.Marshal(sp)
	if strings.Contains(string(raw), "private-fixture") {
		t.Fatal("private link in collection")
	}
	records, _ := mgr.List(ctx)
	if len(records) != 1 || !records[0].Manifest.HasCapability(plugins.CapListSource) || records[0].Manifest.HasCapability(plugins.CapMetadata) {
		t.Fatal("unexpected add-on roles")
	}
	if records[0].ManifestRefreshHours != 6 {
		t.Fatal("manifest interval not stored")
	}
	var sourceURL string
	if err = db.QueryRow(`SELECT source_url FROM plugins_installed WHERE id=?`, records[0].ID).Scan(&sourceURL); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sourceURL, "private-fixture") {
		t.Fatal("manifest URL not sealed")
	}
	// An existing add-on with metadata enabled keeps that owner choice on reimport.
	if _, err = mgr.SetRoles(ctx, records[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	// Older catalogue-only installs retain disabled roles not currently offered.
	if _, err = db.Exec(`UPDATE plugins_installed SET roles_off=? WHERE id=?`, `["stream-source"]`, records[0].ID); err != nil {
		t.Fatal(err)
	}
	req.ManifestRefreshHours = 48
	req.CollectionRefreshHours = 24
	again, err := service.Apply(ctx, req)
	if err != nil || again.Collections[0] != out.Collections[0] || again.Libraries[0] != out.Libraries[0] {
		t.Fatal("reimport", again, err)
	}
	rec, _ := mgr.Get(ctx, records[0].ID)
	if rec.ManifestRefreshHours != 48 {
		t.Fatal("manifest interval not updated")
	}
	kept, err := cols.GetSpec(ctx, out.Collections[0])
	if err != nil || kept.Refresh != "12h" {
		t.Fatal("existing collection interval overwritten", err)
	}
	if !rec.Manifest.HasCapability(plugins.CapMetadata) {
		t.Fatal("existing roles overwritten")
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	saw := false
	for _, p := range transport.paths {
		if strings.HasSuffix(p, "/catalog/movie/animation/genre=None.json") {
			saw = true
		}
		if strings.Contains(p, "/stream/") {
			t.Fatal("unexpected playback")
		}
	}
	if !saw {
		t.Fatal("required genre not sent to source")
	}
}
