package adapters_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/markers"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
)

func TestMarkerPluginBackgroundAndLifecycle(t *testing.T) {
	h := newListTestHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := extend.PluginID("community.marker.test", "markers")
	svc := markers.New(markers.Options{DB: h.db, Config: func() markers.Lookup { return markers.Lookup{SourceOrder: []string{source, "chapter", "theintrodb"}} }})
	bridge := adapters.AttachMarkers(ctx, h.mgr, svc, h.cat, adapters.MarkerBridgeOptions{Selected: func(id string) bool { return id == source }})
	t.Cleanup(func() { cancel(); bridge.Wait() })
	man := map[string]any{"id": "community.marker.test", "version": "1.0.0", "name": "Test markers", "ether": map[string]any{"protocol": 1, "kind": "extension", "summary": "Marker provider for testing", "runtime": map[string]any{"kind": "hosted", "entry": "dist/plugin.js"}, "capabilities": []string{"markers"}, "contributes": map[string]any{"markers": map[string]any{"kinds": []string{"intro", "preview"}}}}}
	raw, _ := json.Marshal(man)
	p, err := h.mgr.PreviewInstall(ctx, plugins.Source{Kind: "studio", Manifest: raw, Files: map[string][]byte{"dist/plugin.js": []byte("// fixture")}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := h.mgr.Install(ctx, p, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	h.rt.routes["/ether/markers/"] = func(_ plugins.Request) (plugins.Response, error) {
		return plugins.Response{Status: 200, Body: json.RawMessage(`{"markers":[{"kind":"intro","start":10,"end":60},{"kind":"preview","start":90,"end":95}]}`)}, nil
	}
	key := core.MovieKey(101)
	info := core.MediaInfo{Duration: 120 * time.Second}
	before, err := svc.CachedMarkers(ctx, key, info)
	if err != nil || len(before) != 0 {
		t.Fatalf("before: %+v %v", before, err)
	}
	<-bridge.Schedule(key, info.Duration)
	got, err := svc.CachedMarkers(ctx, key, info)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Type != "preview" {
		t.Fatalf("markers: %+v", got)
	}
	if err := h.mgr.SetEnabled(ctx, rec.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err = svc.CachedMarkers(ctx, key, info)
	if err != nil || len(got) != 0 {
		t.Fatalf("disabled markers: %+v %v", got, err)
	}
}

type stubMarkerCatalog struct {
	core.Catalog
	titles map[core.RatingKey]*core.Title
}

func (c stubMarkerCatalog) Get(_ context.Context, k core.RatingKey) (*core.Title, error) {
	if t, ok := c.titles[k]; ok {
		return t, nil
	}
	return nil, core.ErrNotFound
}

func (c stubMarkerCatalog) NextEpisode(context.Context, core.RatingKey) (*core.Title, error) {
	return nil, core.ErrNotFound
}

func TestMarkerBridgeLogsSilentPathsOnce(t *testing.T) {
	h := newListTestHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	skipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("imdb_id") {
		case "tt1111111":
			io.WriteString(w, `{"segments":{"intro":null,"outro":null}}`)
		case "tt2222222":
			http.Error(w, "unavailable", http.StatusBadGateway)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer skipSrv.Close()

	noIMDBKey := core.MovieKey(501)
	emptyKey := core.MovieKey(502)
	errKey := core.MovieKey(503)
	cat := stubMarkerCatalog{titles: map[core.RatingKey]*core.Title{
		noIMDBKey: {Key: noIMDBKey, Type: core.TypeMovie, Title: "No IMDb Film"},
		emptyKey:  {Key: emptyKey, Type: core.TypeMovie, Title: "Empty SkipDB", IDs: core.IDs{IMDB: "tt1111111"}},
		errKey:    {Key: errKey, Type: core.TypeMovie, Title: "Error SkipDB", IDs: core.IDs{IMDB: "tt2222222"}},
	}}

	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := markers.New(markers.Options{DB: h.db, Logger: log})
	bridge := adapters.AttachMarkers(ctx, h.mgr, svc, cat, adapters.MarkerBridgeOptions{
		Logger: log,
		SkipDB: func() (bool, string, string) { return true, "", skipSrv.URL },
	})
	t.Cleanup(func() { cancel(); bridge.Wait() })

	// Schedule each title twice; each silent path must log exactly once.
	for range 2 {
		<-bridge.Schedule(noIMDBKey, 10*time.Minute)
		<-bridge.Schedule(emptyKey, 10*time.Minute)
		<-bridge.Schedule(errKey, 10*time.Minute)
	}
	logs := logBuf.String()
	for _, want := range []string{
		"marker refresh skipped: title has no IMDb id",
		"skipdb returned no markers",
		"skipdb marker fetch failed",
	} {
		if n := strings.Count(logs, want); n != 1 {
			t.Fatalf("expected %q once, got %d in logs:\n%s", want, n, logs)
		}
	}
}
