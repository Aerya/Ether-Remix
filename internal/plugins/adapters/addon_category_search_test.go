package adapters_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/kindstream"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
)

// A source plugin that declared category 3000 answers a Music
// indexer_search, lists the release's files and opens the chosen file as a
// Range stream on its own network host; an undeclared source plugin is
// never asked (#792).
func TestCategorySearchReachesOnlyDeclaredSourcePlugins(t *testing.T) {
	env := newStreamTestEnv(t)
	ctx := context.Background()
	track := bytes.Repeat([]byte("fLaC-track-bytes-"), 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "02 Sexy Boy.flac", time.Time{}, bytes.NewReader(track))
	}))
	defer srv.Close()
	srvURL, _ := url.Parse(srv.URL)

	var mu sync.Mutex
	var asked []plugins.Request
	declared := installCategorySource(t, env, srvURL.Hostname(), func(_ context.Context, req plugins.Request) (plugins.Response, error) {
		mu.Lock()
		asked = append(asked, req)
		mu.Unlock()
		switch {
		case req.Path == "/ether/search":
			return plugins.Response{Status: 200, Body: json.RawMessage(`{"streams":[
				{"name":"Engine","ether":{"lazy":true,"id":"r1","releaseName":"Air - Moon Safari (1998) [FLAC]","size":300000000}},
				{"name":"Engine","url":"https://cdn.example/plain.flac"}]}`)}, nil
		case req.Path == "/ether/files/r1":
			return plugins.Response{Status: 200, Body: json.RawMessage(`{"files":[{"path":"01 La femme d'argent.flac","size":100},{"path":"02 Sexy Boy.flac","size":90}]}`)}, nil
		case req.Path == "/ether/open/r1" && req.Query["file"] == "02 Sexy Boy.flac":
			return plugins.Response{Status: 200, Body: json.RawMessage(`{"url":"` + srv.URL + `/stream/r1/1"}`)}, nil
		case req.Path == "/ether/open/r1":
			return plugins.Response{Status: 200, Body: json.RawMessage(`{"url":"http://elsewhere.example/stream/r1/0"}`)}, nil
		}
		return plugins.Response{Status: 404}, nil
	})
	undeclared := &streamAddon{}
	env.installSourcePlugin("community.example.titles", "Titles Only", "indexer", "", nil, undeclared.handle)

	kr := adapters.NewKindResolver(adapters.KindResolverDeps{Manager: env.mgr, Indexer: env.sources, Sources: env.sources, Resolver: env.res, Engine: env.media})
	res, err := kr.ResolveSourcesWithResult(ctx, "community.example.music", core.SourceQuery{
		Kind: "indexer_search", Query: "Air Moon Safari", Category: "3000",
		Lookup: &core.SourceLookup{Type: "album", Fields: map[string]string{"artist": "Air", "title": "Moon Safari"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates = %+v, want the one lazy release", res.Candidates)
	}
	cand := res.Candidates[0]
	if cand.PluginRelease == nil || cand.PluginRelease.Plugin != declared.ID || cand.PluginRelease.ID != "r1" || cand.Category != 3000 || cand.Title != "Air - Moon Safari (1998) [FLAC]" {
		t.Fatalf("candidate = %+v", cand)
	}
	if got := undeclared.asked(); len(got) != 0 {
		t.Fatalf("undeclared source plugin was asked %v", got)
	}
	mu.Lock()
	q := asked[0].Query
	mu.Unlock()
	if q["category"] != "3000" || q["query"] != "Air Moon Safari" || q["lookup"] != "album" || q["lookup.artist"] != "Air" {
		t.Fatalf("search query = %v", q)
	}

	files, err := kr.ResolveSources(ctx, "community.example.music", core.SourceQuery{Kind: "release_files", Candidate: &cand})
	if err != nil || len(files) != 1 || len(files[0].Files) != 2 || files[0].Files[1].Path != "02 Sexy Boy.flac" {
		t.Fatalf("release_files = %+v, %v", files, err)
	}

	ms, err := kr.OpenReleaseSource(ctx, core.PlayableSource{ID: "t2", Pattern: "release", MIME: "audio/flac", Release: &core.ReleaseHint{Candidate: &cand, File: "02 Sexy Boy.flac"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	got := make([]byte, 16)
	if _, err := ms.ReadAt(got, 17); err != nil || ms.Size() != int64(len(track)) || !bytes.Equal(got, track[17:33]) {
		t.Fatalf("read %q size %d err %v", got, ms.Size(), err)
	}

	// The plugin cannot point Ether at a host outside its permissions.
	if _, err := kr.OpenReleaseSource(ctx, core.PlayableSource{ID: "t1", Pattern: "release", Release: &core.ReleaseHint{Candidate: &cand}}); !errors.Is(err, kindstream.ErrHostNotAllowed) {
		t.Fatalf("open on an undeclared host: %v", err)
	}
}

func installCategorySource(t *testing.T, env *streamTestEnv, host string, handler func(context.Context, plugins.Request) (plugins.Response, error)) plugins.Record {
	t.Helper()
	const id = "community.example.engine"
	env.mu.Lock()
	env.rts[id] = &fakePluginRuntime{handler: handler}
	env.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{
		"id": id, "version": "1.0.0", "name": "Engine",
		"ether": map[string]any{
			"protocol": 1, "kind": "source", "summary": "Finds releases on the owner's engine",
			"capabilities": []string{"stream_source"},
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"permissions":  map[string]any{"network": []string{host}},
			"contributes": map[string]any{"source": map[string]any{
				"class": "indexer", "search": map[string]any{"categories": []int{3000}, "lookups": []string{"album"}},
			}},
		},
	})
	prev, err := env.mgr.PreviewInstall(context.Background(), plugins.Source{Kind: "studio", Manifest: raw, Files: map[string][]byte{"dist/plugin.js": []byte("// plugin")}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.mgr.Install(context.Background(), prev, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Manifest.SourceSearch() == nil {
		t.Fatal("install dropped the search declaration")
	}
	return rec
}
