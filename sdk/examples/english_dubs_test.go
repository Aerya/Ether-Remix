package examples_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/personalize"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/store"
)

const (
	testMalDubsPath = "/MAL-Dubs/MAL-Dubs/main/data/dubInfo.json"
	testFribbPath   = "/Fribb/anime-lists/master/anime-list-mini.json"
)

const sampleDubInfoJSON = `{
  "_license": "AGPL-3.0",
  "_origin": "https://github.com/mal-dubs/mal-dubs",
  "dubbed": [1, 5, 20, 21, 300, 301, 400, 999999],
  "incomplete": [8, 21, 500]
}`

const sampleFribbJSON = `[
  {"type":"TV","mal_id":1,"themoviedb_id":{"tv":30991},"imdb_id":["tt0213338"],"tvdb_id":76885},
  {"type":"MOVIE","mal_id":5,"themoviedb_id":{"movie":[11299]},"imdb_id":["tt0275277"]},
  {"type":"TV","mal_id":20,"themoviedb_id":{"tv":46260},"imdb_id":["tt0409591"],"tvdb_id":78857},
  {"type":"TV","mal_id":21,"themoviedb_id":{"tv":37854},"imdb_id":["tt0388629"],"tvdb_id":81797},
  {"type":"OVA","mal_id":300,"themoviedb_id":{"tv":46260},"imdb_id":["tt0409591"],"tvdb_id":78857},
  {"type":"TV","mal_id":301,"tvdb_id":123456,"imdb_id":["tt1111111"]},
  {"type":"MOVIE","mal_id":400,"imdb_id":["tt2222222"]},
  {"type":"TV","mal_id":8,"themoviedb_id":{"tv":99001},"imdb_id":["tt0489000"],"tvdb_id":99001},
  {"type":"MOVIE","mal_id":500,"themoviedb_id":{"movie":[88001]}}
]`

type englishDubsHost struct {
	plugins.Host
	mu       sync.Mutex
	kv       map[string][]byte
	settings map[string]any
	requests []plugins.FetchRequest
	fetchFn  func(plugins.FetchRequest) (plugins.FetchResponse, error)
}

func (h *englishDubsHost) Settings(context.Context) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]any, len(h.settings))
	for k, v := range h.settings {
		out[k] = v
	}
	return out, nil
}

func (h *englishDubsHost) KVGet(_ context.Context, k string) ([]byte, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.kv[k]
	return v, ok, nil
}

func (h *englishDubsHost) KVPut(_ context.Context, k string, v []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]byte, len(v))
	copy(cp, v)
	h.kv[k] = cp
	return nil
}

func (h *englishDubsHost) KVDelete(_ context.Context, k string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.kv, k)
	return nil
}

func (h *englishDubsHost) KVList(_ context.Context, p string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for k := range h.kv {
		if strings.HasPrefix(k, p) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (h *englishDubsHost) KVUsage(context.Context) (plugins.KVUsage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var used int64
	for _, v := range h.kv {
		used += int64(len(v))
	}
	keys := int64(len(h.kv))
	return plugins.KVUsage{
		Bytes:      used + keys*plugins.KVKeyChargeBytes,
		Keys:       keys,
		QuotaBytes: plugins.KVQuotaBytes,
		QuotaKeys:  plugins.KVQuotaKeys,
	}, nil
}

func (h *englishDubsHost) HTTPFetch(_ context.Context, r plugins.FetchRequest) (plugins.FetchResponse, error) {
	h.mu.Lock()
	h.requests = append(h.requests, r)
	fn := h.fetchFn
	h.mu.Unlock()
	return fn(r)
}

func (h *englishDubsHost) Log(context.Context, string, string, map[string]any) error { return nil }

func TestEnglishDubsPackAndHostedLifecycle(t *testing.T) {
	ctx := context.Background()

	pkg, err := kit.LoadDir("english-dubs")
	if err != nil {
		t.Fatal(err)
	}
	if rep := kit.Check(ctx, pkg); !rep.OK {
		t.Fatalf("source check failed: %+v", rep.Errors)
	}
	tr, err := kit.RunTests(ctx, pkg, "")
	if err != nil || !tr.OK {
		t.Fatalf("source fixtures failed: err=%v report=%s", err, tr.Text())
	}

	raw, err := kit.Pack(pkg)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := kit.LoadPkgBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if packed.Manifest.Ether.Runtime.Entry != "dist/plugin.js" {
		t.Fatalf("packed entry = %q, want dist/plugin.js", packed.Manifest.Ether.Runtime.Entry)
	}
	if rep := kit.Check(ctx, packed); !rep.OK {
		t.Fatalf("packed check failed: %+v", rep.Errors)
	}

	const (
		dubEtag = `"dub-etag-1"`
		mapEtag = `"map-etag-1"`
		dubLM   = "Thu, 19 Nov 2026 00:00:00 GMT"
		mapLM   = "Thu, 19 Nov 2026 01:00:00 GMT"
	)

	var failUpstream bool
	host := &englishDubsHost{
		kv:       map[string][]byte{},
		settings: map[string]any{"includeIncomplete": false, "typeFilter": "both"},
	}
	host.fetchFn = func(r plugins.FetchRequest) (plugins.FetchResponse, error) {
		if failUpstream {
			return plugins.FetchResponse{Status: 503, Body: []byte("unavailable")}, nil
		}
		switch {
		case strings.HasSuffix(r.URL, testMalDubsPath):
			if r.Headers["If-None-Match"] == dubEtag {
				return plugins.FetchResponse{
					Status:  304,
					Headers: map[string]string{"Etag": dubEtag, "Last-Modified": dubLM},
				}, nil
			}
			return plugins.FetchResponse{
				Status:  200,
				Headers: map[string]string{"Etag": dubEtag, "Last-Modified": dubLM},
				Body:    []byte(sampleDubInfoJSON),
			}, nil
		case strings.HasSuffix(r.URL, testFribbPath):
			if r.Headers["If-None-Match"] == mapEtag {
				return plugins.FetchResponse{
					Status:  304,
					Headers: map[string]string{"Etag": mapEtag, "Last-Modified": mapLM},
				}, nil
			}
			return plugins.FetchResponse{
				Status:  200,
				Headers: map[string]string{"Etag": mapEtag, "Last-Modified": mapLM},
				Body:    []byte(sampleFribbJSON),
			}, nil
		default:
			return plugins.FetchResponse{Status: 404, Body: []byte("not found")}, nil
		}
	}

	engine := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer engine.Close()

	rt, err := plugins.HostedRuntimeFactory(engine)(plugins.Installed{
		ID:       packed.Manifest.ID,
		Manifest: packed.Manifest,
		Host:     host,
		Files:    func(p string) ([]byte, bool) { v, ok := packed.Files[p]; return v, ok },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	call := func(method, path, body string) plugins.Response {
		t.Helper()
		resp, err := rt.Call(ctx, plugins.Request{
			Method: method,
			Path:   path,
			Body:   json.RawMessage(body),
		})
		if err != nil || resp.Status != 200 {
			t.Fatalf("%s %s: status=%d body=%s err=%v", method, path, resp.Status, resp.Body, err)
		}
		return resp
	}

	// Cold read populates KV and maps series & movies with deduplication.
	type catResp struct {
		Metas []struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Name   string `json:"name"`
			TMDBID int    `json:"tmdb_id"`
			IMDBID string `json:"imdb_id"`
			TVDBID int    `json:"tvdb_id"`
		} `json:"metas"`
		HasMore bool `json:"hasMore"`
	}

	var seriesCat catResp
	if err := json.Unmarshal(call("GET", "/catalog/series/dubbed.json", "").Body, &seriesCat); err != nil {
		t.Fatal(err)
	}
	if len(seriesCat.Metas) != 4 || seriesCat.HasMore {
		t.Fatalf("series catalog = %+v", seriesCat)
	}
	wantSeries := []string{"tmdb:30991", "tmdb:46260", "tmdb:37854", "tvdb:123456"}
	for i, want := range wantSeries {
		if seriesCat.Metas[i].ID != want {
			t.Fatalf("series[%d] = %q, want %q", i, seriesCat.Metas[i].ID, want)
		}
	}
	if seriesCat.Metas[0].TMDBID != 30991 || seriesCat.Metas[0].IMDBID != "tt0213338" || seriesCat.Metas[0].TVDBID != 76885 {
		t.Fatalf("secondary IDs missing on first series meta: %+v", seriesCat.Metas[0])
	}

	// Subsequent reads hit warm KV cache with zero extra HTTP fetches.
	var movieCat catResp
	if err := json.Unmarshal(call("GET", "/catalog/movie/dubbed.json", "").Body, &movieCat); err != nil {
		t.Fatal(err)
	}
	if len(movieCat.Metas) != 2 || movieCat.Metas[0].ID != "tmdb:11299" || movieCat.Metas[1].ID != "tt2222222" {
		t.Fatalf("movie catalog = %+v", movieCat)
	}
	if len(host.requests) != 2 {
		t.Fatalf("expected 2 initial fetches, got %d", len(host.requests))
	}

	// Scheduled refresh sends ETag + If-Modified-Since and honors 304 Not Modified.
	jobResp := call("POST", "/ether/job/refresh", `{}`)
	if !strings.Contains(string(jobResp.Body), `"changed":false`) {
		t.Fatalf("expected 304 refresh to report changed=false, got %s", jobResp.Body)
	}
	if len(host.requests) != 4 {
		t.Fatalf("expected 4 fetches after scheduled refresh, got %d", len(host.requests))
	}
	if got := host.requests[2].Headers["If-None-Match"]; got != dubEtag {
		t.Fatalf("dubs If-None-Match = %q, want %q", got, dubEtag)
	}
	if got := host.requests[2].Headers["If-Modified-Since"]; got != dubLM {
		t.Fatalf("dubs If-Modified-Since = %q, want %q", got, dubLM)
	}
	if got := host.requests[3].Headers["If-None-Match"]; got != mapEtag {
		t.Fatalf("map If-None-Match = %q, want %q", got, mapEtag)
	}

	// Settings: includeIncomplete merges deduplicated incomplete titles into dubbed.
	host.mu.Lock()
	host.settings["includeIncomplete"] = true
	host.mu.Unlock()
	var mergedSeries catResp
	if err := json.Unmarshal(call("GET", "/catalog/series/dubbed.json", "").Body, &mergedSeries); err != nil {
		t.Fatal(err)
	}
	if len(mergedSeries.Metas) != 5 || mergedSeries.Metas[4].ID != "tmdb:99001" {
		t.Fatalf("includeIncomplete merged series = %+v", mergedSeries)
	}

	// Settings: typeFilter=series hides movie catalogs.
	host.mu.Lock()
	host.settings["typeFilter"] = "series"
	host.mu.Unlock()
	var hiddenMovies catResp
	if err := json.Unmarshal(call("GET", "/catalog/movie/dubbed.json", "").Body, &hiddenMovies); err != nil {
		t.Fatal(err)
	}
	if len(hiddenMovies.Metas) != 0 {
		t.Fatalf("typeFilter=series should return empty movies, got %+v", hiddenMovies)
	}

	// Status view reflects cached stats offline without making network calls.
	beforeView := len(host.requests)
	viewResp := call("GET", "/ether/view/status", "")
	if len(host.requests) != beforeView {
		t.Fatalf("status view made network calls: %d -> %d", beforeView, len(host.requests))
	}
	if !strings.Contains(string(viewResp.Body), `"mappedSeries":"4"`) ||
		!strings.Contains(string(viewResp.Body), `"mappedMovies":"2"`) {
		t.Fatalf("unexpected status view payload: %s", viewResp.Body)
	}

	// Stale cache fallback when upstream errors on refresh.
	failUpstream = true
	actionResp := call("POST", "/ether/action/refresh", `{}`)
	if !strings.Contains(string(actionResp.Body), "Kept cached") {
		t.Fatalf("expected stale cache toast on upstream error, got %s", actionResp.Body)
	}
}

type githubRawMockTransport struct {
	dubs  []byte
	fribb []byte
}

func (m githubRawMockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() != "raw.githubusercontent.com" {
		return nil, errors.New("unexpected outbound host: " + req.URL.Hostname())
	}
	var body []byte
	switch req.URL.Path {
	case testMalDubsPath:
		body = m.dubs
	case testFribbPath:
		body = m.fribb
	default:
		return &http.Response{
			StatusCode: 404,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    req,
		}, nil
	}
	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	hdr.Set("ETag", `"mock-etag"`)
	return &http.Response{
		StatusCode: 200,
		Header:     hdr,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

func TestEnglishDubsManagerAndCollectionsEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dataDir := t.TempDir()
	cfg, err := config.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cat, err := catalog.New(db, nil, config.Catalog{
		Language:      "en-US",
		Region:        "US",
		MinPopularity: 1,
		MinVotes:      1,
	}, log)
	if err != nil {
		t.Fatal(err)
	}
	seed := []core.Title{
		{
			Key:        core.ShowKey(30991),
			Type:       core.TypeShow,
			IDs:        core.IDs{TMDB: 30991, IMDB: "tt0213338", TVDB: 76885},
			Title:      "Cowboy Bebop",
			Year:       1998,
			Released:   time.Date(1998, 4, 3, 0, 0, 0, 0, time.UTC),
			Rating:     8.5,
			Popularity: 80,
			VoteCount:  1500,
		},
		{
			Key:        core.MovieKey(11299),
			Type:       core.TypeMovie,
			IDs:        core.IDs{TMDB: 11299, IMDB: "tt0275277"},
			Title:      "Cowboy Bebop: The Movie",
			Year:       2001,
			Released:   time.Date(2001, 9, 1, 0, 0, 0, 0, time.UTC),
			Rating:     7.8,
			Popularity: 60,
			VoteCount:  900,
		},
		{
			Key:        core.ShowKey(99001),
			Type:       core.TypeShow,
			IDs:        core.IDs{TMDB: 99001, IMDB: "tt0489000", TVDB: 99001},
			Title:      "Ongoing Dub Anime",
			Year:       2026,
			Released:   time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
			Rating:     8.1,
			Popularity: 70,
			VoteCount:  400,
		},
		{
			Key:        core.ShowKey(77777),
			Type:       core.TypeShow,
			IDs:        core.IDs{TMDB: 77777, IMDB: "tt9999999"},
			Title:      "Sub Only Anime",
			Year:       2024,
			Released:   time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
			Rating:     8.0,
			Popularity: 50,
			VoteCount:  200,
		},
	}
	for _, title := range seed {
		if err := cat.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}

	cols, err := collections.New(db, cat, log, collections.WithIndexTTL(time.Hour), collections.WithLiveTTL(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	pers, err := personalize.New(personalize.Options{
		DB:           db,
		Catalog:      cat,
		Collections:  cols,
		Logger:       log,
		RefreshEvery: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pers.Close() })

	engine := wasm.NewEngine(log)
	defer engine.Close()

	reg := extend.New()
	mgr, err := plugins.New(plugins.Deps{
		DB:           db,
		Sealer:       cfg,
		Registry:     reg,
		Tools:        tools.NewCatalogue(),
		Logger:       log,
		IsLeader:     func() bool { return true },
		CheckPackage: kit.CheckInstall,
		FetchClient: &http.Client{
			Transport: githubRawMockTransport{
				dubs:  []byte(sampleDubInfoJSON),
				fribb: []byte(sampleFribbJSON),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("hosted", plugins.HostedRuntimeFactory(engine))
	adapters.Attach(mgr, cols, pers, nil)
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}

	pkg, err := kit.LoadDir("english-dubs")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := kit.Pack(pkg)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := mgr.PreviewInstall(ctx, plugins.Source{Kind: "pkg", Pkg: raw})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := mgr.Install(ctx, preview, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}

	// Verify both list sources registered on Collections, Home Designer, and extend registry.
	dubbedKind := extend.PluginID(rec.ID, "dubbed")
	incompleteKind := extend.PluginID(rec.ID, "incomplete")

	for _, kindID := range []string{dubbedKind, incompleteKind} {
		sk, ok := cols.KindRegistry().Lookup(kindID)
		if !ok {
			t.Fatalf("expected collection SourceKind %s to be registered", kindID)
		}
		if gotTypes := sk.Describe().MediaTypes; len(gotTypes) != 2 {
			t.Fatalf("SourceKind %s MediaTypes = %v, want [movie show]", kindID, gotTypes)
		}
		if _, ok := pers.RowKindRegistry().Lookup(kindID); !ok {
			t.Fatalf("expected personalize RowSourceKind %s to be registered", kindID)
		}
		if _, ok := reg.Get(extend.KindCollectionSource, kindID); !ok {
			t.Fatalf("expected extend collectionSource descriptor for %s", kindID)
		}
		if _, ok := reg.Get(extend.KindRowSource, kindID); !ok {
			t.Fatalf("expected extend rowSource descriptor for %s", kindID)
		}
	}

	// Mixed preview of "Anime with an English dub" matches both dubbed show and dubbed movie.
	dubbedPrev, err := cols.Preview(ctx, core.CollectionSpec{
		Name:      "English dubbed anime",
		MediaType: "mixed",
		Sources:   []core.CollectionSource{{Kind: dubbedKind}},
		Sort:      "source",
		Refresh:   "daily",
		Enabled:   true,
	}, 20)
	if err != nil {
		t.Fatalf("Preview dubbed failed: %v", err)
	}
	gotKeys := map[core.RatingKey]bool{}
	for _, it := range dubbedPrev.Items {
		gotKeys[it.Key] = true
	}
	if !gotKeys[core.ShowKey(30991)] || !gotKeys[core.MovieKey(11299)] {
		t.Fatalf("dubbed preview missing expected titles: %+v (warnings: %v)", gotKeys, dubbedPrev.Warnings)
	}
	if gotKeys[core.ShowKey(99001)] || gotKeys[core.ShowKey(77777)] {
		t.Fatalf("dubbed preview should not contain incomplete or sub-only titles: %+v", gotKeys)
	}

	// Show preview of "Dub in progress" matches ongoing dubbed show.
	incPrev, err := cols.Preview(ctx, core.CollectionSpec{
		Name:      "Dub in progress",
		MediaType: "show",
		Sources:   []core.CollectionSource{{Kind: incompleteKind}},
		Sort:      "source",
		Refresh:   "daily",
		Enabled:   true,
	}, 20)
	if err != nil {
		t.Fatalf("Preview incomplete failed: %v", err)
	}
	if len(incPrev.Items) != 1 || incPrev.Items[0].Key != core.ShowKey(99001) {
		t.Fatalf("incomplete preview items = %+v, want show 99001", incPrev.Items)
	}

	// Enabling includeIncomplete adds the ongoing dub into the main list.
	if _, err := mgr.Configure(ctx, rec.ID, map[string]any{
		"includeIncomplete": true,
		"typeFilter":        "both",
	}); err != nil {
		t.Fatalf("Configure includeIncomplete: %v", err)
	}
	mergedPrev, err := cols.Preview(ctx, core.CollectionSpec{
		Name:      "English dubbed anime + ongoing",
		MediaType: "mixed",
		Sources:   []core.CollectionSource{{Kind: dubbedKind}},
		Sort:      "source",
		Refresh:   "daily",
		Enabled:   true,
	}, 20)
	if err != nil {
		t.Fatalf("Preview merged failed: %v", err)
	}
	mergedKeys := map[core.RatingKey]bool{}
	for _, it := range mergedPrev.Items {
		mergedKeys[it.Key] = true
	}
	if !mergedKeys[core.ShowKey(30991)] || !mergedKeys[core.MovieKey(11299)] || !mergedKeys[core.ShowKey(99001)] {
		t.Fatalf("merged preview missing expected titles: %+v", mergedKeys)
	}

	// Switching typeFilter to movies returns only the dubbed movie.
	if _, err := mgr.Configure(ctx, rec.ID, map[string]any{
		"includeIncomplete": false,
		"typeFilter":        "movies",
	}); err != nil {
		t.Fatalf("Configure typeFilter=movies: %v", err)
	}
	moviesPrev, err := cols.Preview(ctx, core.CollectionSpec{
		Name:      "English dubbed movies",
		MediaType: "mixed",
		Sources:   []core.CollectionSource{{Kind: dubbedKind}},
		Sort:      "source",
		Refresh:   "daily",
		Enabled:   true,
	}, 20)
	if err != nil {
		t.Fatalf("Preview movies-only failed: %v", err)
	}
	if len(moviesPrev.Items) != 1 || moviesPrev.Items[0].Key != core.MovieKey(11299) {
		t.Fatalf("movies-only preview = %+v, want only movie 11299", moviesPrev.Items)
	}
}

func TestEnglishDubsLiveUpstream(t *testing.T) {
	if os.Getenv("ETHER_LIVE_TEST") == "" {
		t.Skip("set ETHER_LIVE_TEST=1 to probe live GitHub MAL-Dubs and Fribb datasets")
	}
	ctx := context.Background()

	pkg, err := kit.LoadDir("english-dubs")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := kit.Pack(pkg)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := kit.LoadPkgBytes(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	var logMu sync.Mutex
	var statuses []string
	host := &englishDubsHost{
		kv:       map[string][]byte{},
		settings: map[string]any{"includeIncomplete": false, "typeFilter": "both"},
		fetchFn: func(r plugins.FetchRequest) (plugins.FetchResponse, error) {
			req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, nil)
			if err != nil {
				return plugins.FetchResponse{}, err
			}
			for k, v := range r.Headers {
				req.Header.Set(k, v)
			}
			resp, err := client.Do(req)
			if err != nil {
				return plugins.FetchResponse{}, err
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
			if err != nil {
				return plugins.FetchResponse{}, err
			}
			hdrs := map[string]string{}
			for k, vals := range resp.Header {
				if len(vals) > 0 {
					hdrs[k] = vals[0]
				}
			}
			logMu.Lock()
			statuses = append(statuses, fmt.Sprintf("%d %s (%d bytes)", resp.StatusCode, r.URL, len(body)))
			logMu.Unlock()
			return plugins.FetchResponse{Status: resp.StatusCode, Headers: hdrs, Body: body}, nil
		},
	}

	engine := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer engine.Close()
	rt, err := plugins.HostedRuntimeFactory(engine)(plugins.Installed{
		ID:       packed.Manifest.ID,
		Manifest: packed.Manifest,
		Host:     host,
		Files:    func(p string) ([]byte, bool) { v, ok := packed.Files[p]; return v, ok },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	t0 := time.Now()
	r1, err := rt.Call(ctx, plugins.Request{Method: "POST", Path: "/ether/job/refresh", Body: json.RawMessage(`{}`)})
	if err != nil || r1.Status != 200 {
		t.Fatalf("live refresh 1 failed (%v): %d %s %v", time.Since(t0), r1.Status, r1.Body, err)
	}
	t.Logf("live refresh 1 (%v): %s", time.Since(t0), r1.Body)

	t1 := time.Now()
	r2, err := rt.Call(ctx, plugins.Request{Method: "POST", Path: "/ether/job/refresh", Body: json.RawMessage(`{}`)})
	if err != nil || r2.Status != 200 {
		t.Fatalf("live refresh 2 failed (%v): %d %s %v", time.Since(t1), r2.Status, r2.Body, err)
	}
	t.Logf("live refresh 2 (%v): %s", time.Since(t1), r2.Body)
	if !strings.Contains(string(r2.Body), `"changed":false`) {
		t.Fatalf("expected 304 Not Modified on immediate live revalidation, got %s", r2.Body)
	}

	t2 := time.Now()
	cat, err := rt.Call(ctx, plugins.Request{Method: "GET", Path: "/catalog/series/dubbed.json"})
	if err != nil || cat.Status != 200 {
		t.Fatalf("live catalog failed: %d %s %v", cat.Status, cat.Body, err)
	}
	var doc struct {
		Metas   []map[string]any `json:"metas"`
		HasMore bool             `json:"hasMore"`
	}
	if err := json.Unmarshal(cat.Body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Metas) != 200 || !doc.HasMore {
		t.Fatalf("unexpected live series page 1: len=%d hasMore=%v", len(doc.Metas), doc.HasMore)
	}
	u, _ := host.KVUsage(ctx)
	t.Logf("live catalog series page 1 (%v): len=%d sample=%v; KV=%d bytes across %d keys", time.Since(t2), len(doc.Metas), doc.Metas[0], u.Bytes, u.Keys)
	for _, s := range statuses {
		t.Log("HTTP:", s)
	}
}
