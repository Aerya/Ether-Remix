package animetosho_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/store"
)

type recordedFetch struct {
	URL     string
	Headers map[string]string
}

type fixtureHost struct {
	plugins.Host
	settings map[string]any
	calls    []recordedFetch
	respond  func(req plugins.FetchRequest) (plugins.FetchResponse, error)
}

func (h *fixtureHost) Settings(_ context.Context) (map[string]any, error) {
	if h.settings == nil {
		return map[string]any{}, nil
	}
	return h.settings, nil
}

func (h *fixtureHost) HTTPFetch(_ context.Context, req plugins.FetchRequest) (plugins.FetchResponse, error) {
	hdrs := map[string]string{}
	for k, v := range req.Headers {
		hdrs[k] = v
	}
	h.calls = append(h.calls, recordedFetch{URL: req.URL, Headers: hdrs})
	if h.respond != nil {
		return h.respond(req)
	}
	return plugins.FetchResponse{Status: 404, Body: []byte("unhandled")}, nil
}

func (h *fixtureHost) Log(context.Context, string, string, map[string]any) error { return nil }

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("tests", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type streamPayload struct {
	Streams []struct {
		Name          string   `json:"name"`
		Title         string   `json:"title"`
		InfoHash      string   `json:"infoHash"`
		NZBURL        string   `json:"nzbUrl"`
		Sources       []string `json:"sources"`
		BehaviorHints struct {
			VideoSize int64  `json:"videoSize"`
			Filename  string `json:"filename"`
		} `json:"behaviorHints"`
	} `json:"streams"`
}

func TestHostedAnimeToshoFlows(t *testing.T) {
	manBytes, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	man, err := plugins.ParseManifest(manBytes)
	if err != nil {
		t.Fatal(err)
	}

	capsXML := readFixture(t, "caps.xml")
	frierenEp1 := readFixture(t, "frieren-ep1.xml")
	frierenPacks := readFixture(t, "frieren-packs.xml")
	onePiece1071 := readFixture(t, "one-piece-1071.xml")
	aotAbs52 := readFixture(t, "aot-abs-52.xml")
	yourNameMovie := readFixture(t, "your-name-movie.xml")
	newznabOnly := readFixture(t, "newznab-only.xml")

	var overrideStatus int
	var overrideHeaders map[string]string
	var overrideBody []byte

	host := &fixtureHost{
		settings: map[string]any{"releaseTypes": "both", "category": ""},
		respond: func(req plugins.FetchRequest) (plugins.FetchResponse, error) {
			if overrideStatus != 0 {
				return plugins.FetchResponse{Status: overrideStatus, Headers: overrideHeaders, Body: overrideBody}, nil
			}
			u, err := url.Parse(req.URL)
			if err != nil {
				return plugins.FetchResponse{Status: 400}, nil
			}
			q := u.Query()
			switch {
			case q.Get("t") == "caps":
				return plugins.FetchResponse{Status: 200, Body: capsXML}, nil
			case u.Path == "/api/newznab":
				return plugins.FetchResponse{Status: 200, Body: newznabOnly}, nil
			case q.Get("tmdbid") == "37854" && q.Get("ep") == "1071":
				return plugins.FetchResponse{Status: 200, Body: onePiece1071}, nil
			case q.Get("tmdbid") == "1429" && q.Get("ep") == "52":
				return plugins.FetchResponse{Status: 200, Body: aotAbs52}, nil
			case q.Get("tmdbid") == "209867" && q.Get("ep") == "":
				return plugins.FetchResponse{Status: 200, Body: frierenPacks}, nil
			case q.Get("tmdbid") == "209867" || q.Get("aid") == "17617" || q.Get("malid") == "52991" || q.Get("kitsuid") == "46474":
				return plugins.FetchResponse{Status: 200, Body: frierenEp1}, nil
			case q.Get("imdbid") == "tt5311514" || q.Get("aid") == "11829" || q.Get("malid") == "32281":
				return plugins.FetchResponse{Status: 200, Body: yourNameMovie}, nil
			default:
				return plugins.FetchResponse{Status: 200, Body: []byte(`<?xml version="1.0" encoding="utf-8"?><rss version="2.0"><channel></channel></rss>`)}, nil
			}
		},
	}

	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer eng.Close()
	rt, err := plugins.HostedRuntimeFactory(eng)(plugins.Installed{
		ID:       man.ID,
		Manifest: man,
		Host:     host,
		Files: func(path string) ([]byte, bool) {
			b, err := os.ReadFile(path)
			return b, err == nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	call := func(req plugins.Request) plugins.Response {
		t.Helper()
		resp, err := rt.Call(context.Background(), req)
		if err != nil {
			t.Fatalf("%s %s: %v", req.Method, req.Path, err)
		}
		return resp
	}

	// 1. Health is offline and needs zero network calls.
	health := call(plugins.Request{Method: "GET", Path: "/ether/health"})
	if health.Status != 200 || !strings.Contains(string(health.Body), `"status":"ok"`) || len(host.calls) != 0 {
		t.Fatalf("health: status=%d body=%s calls=%d", health.Status, health.Body, len(host.calls))
	}

	// 2. Connection test verifies ?t=caps and sends Ether's honest User-Agent.
	testResp := call(plugins.Request{Method: "POST", Path: "/ether/test"})
	if testResp.Status != 200 || !strings.Contains(string(testResp.Body), `"ok":true`) {
		t.Fatalf("test: status=%d body=%s", testResp.Status, testResp.Body)
	}
	if len(host.calls) != 1 || host.calls[0].URL != "https://feed.animetosho.net/api?t=caps" {
		t.Fatalf("unexpected caps fetch: %+v", host.calls)
	}
	if ua := host.calls[0].Headers["User-Agent"]; !strings.HasPrefix(ua, "Ether/") || !strings.Contains(ua, "braedonsaunders/ether") {
		t.Fatalf("expected honest Ether User-Agent, got %q", ua)
	}

	// 3. Both mode on S01E01 via IMDb path + tmdbId hint prefers tmdbid, filters out S02,
	// and emits separate torrent + NZB streams for dual items.
	resp := call(plugins.Request{
		Method: "GET",
		Path:   "/stream/series/tt22248376:1:1.json",
		Query:  map[string]string{"tmdbId": "209867", "imdbId": "tt22248376", "season": "1", "episode": "1"},
	})
	if resp.Status != 200 {
		t.Fatalf("s01e01 status=%d body=%s", resp.Status, resp.Body)
	}
	lastURL := host.calls[len(host.calls)-1].URL
	if !strings.Contains(lastURL, "/api?t=tvsearch&tmdbid=209867&season=1&ep=1&limit=75") {
		t.Fatalf("expected tmdbid series query, got %s", lastURL)
	}
	var got streamPayload
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatal(err)
	}
	// Item 1 (S02E01) filtered out; Item 2 (S01E01) -> torrent + NZB; Item 3 (S01E01 torrent-only) -> torrent.
	if len(got.Streams) != 3 {
		t.Fatalf("expected 3 streams (2 torrent + 1 nzb), got %d: %s", len(got.Streams), resp.Body)
	}
	if got.Streams[0].InfoHash != "1111111111111111111111111111111111111111" || got.Streams[0].NZBURL != "" {
		t.Fatalf("stream[0] want torrent only: %+v", got.Streams[0])
	}
	if got.Streams[0].BehaviorHints.Filename != "[SubsPlease] Sousou no Frieren - 01 (1080p) [A1B2C3D4].mkv" {
		t.Fatalf("expected single-file video filename hint, got %q", got.Streams[0].BehaviorHints.Filename)
	}
	if len(got.Streams[0].Sources) != 1 || got.Streams[0].Sources[0] != "tracker:http://nyaa.tracker.wf:7777/announce" {
		t.Fatalf("expected tracker source, got %+v", got.Streams[0].Sources)
	}
	if got.Streams[1].NZBURL != "https://feed.animetosho.net/nzb/510101" || got.Streams[1].InfoHash != "" {
		t.Fatalf("stream[1] want nzb only: %+v", got.Streams[1])
	}
	if got.Streams[2].InfoHash != "2222222222222222222222222222222222222222" {
		t.Fatalf("stream[2] want second torrent: %+v", got.Streams[2])
	}

	// 4. Season 2 episode keeps S02E01, excludes S01E01, lowercases uppercase infohash, extracts both trackers.
	resp = call(plugins.Request{
		Method: "GET",
		Path:   "/stream/series/tmdb:209867:2:1.json",
		Query:  map[string]string{"season": "2", "episode": "1"},
	})
	got = streamPayload{}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Streams) != 2 {
		t.Fatalf("s02e01 expected 2 streams (torrent + nzb), got %d: %s", len(got.Streams), resp.Body)
	}
	if got.Streams[0].InfoHash != "25de198aea0add2b242ac71d62cb24cacf91dbf1" || len(got.Streams[0].Sources) != 2 {
		t.Fatalf("s02e01 torrent mismatch: %+v", got.Streams[0])
	}
	if got.Streams[1].NZBURL != "https://feed.animetosho.net/nzb/595631" {
		t.Fatalf("s02e01 nzb mismatch: %+v", got.Streams[1])
	}

	// 5. Torrents-only setting uses /api/torznab and omits NZB streams.
	host.settings = map[string]any{"releaseTypes": "torrents", "category": ""}
	resp = call(plugins.Request{
		Method: "GET",
		Path:   "/stream/series/tmdb:209867:2:1.json",
		Query:  map[string]string{"season": "2", "episode": "1"},
	})
	if !strings.Contains(host.calls[len(host.calls)-1].URL, "/api/torznab?") {
		t.Fatalf("torrents mode should query /api/torznab, got %s", host.calls[len(host.calls)-1].URL)
	}
	got = streamPayload{}
	_ = json.Unmarshal(resp.Body, &got)
	if len(got.Streams) != 1 || got.Streams[0].InfoHash == "" || got.Streams[0].NZBURL != "" {
		t.Fatalf("torrents mode unexpected streams: %+v", got.Streams)
	}

	// 6. NZBs-only setting uses /api/newznab and omits torrent streams.
	host.settings = map[string]any{"releaseTypes": "nzbs", "category": ""}
	resp = call(plugins.Request{
		Method: "GET",
		Path:   "/stream/series/tmdb:209867:2:1.json",
		Query:  map[string]string{"season": "2", "episode": "1"},
	})
	if !strings.Contains(host.calls[len(host.calls)-1].URL, "/api/newznab?") {
		t.Fatalf("nzbs mode should query /api/newznab, got %s", host.calls[len(host.calls)-1].URL)
	}
	got = streamPayload{}
	_ = json.Unmarshal(resp.Body, &got)
	if len(got.Streams) != 1 || got.Streams[0].NZBURL != "https://feed.animetosho.net/nzb/595631" || got.Streams[0].InfoHash != "" {
		t.Fatalf("nzbs mode unexpected streams: %+v", got.Streams)
	}

	// 7. Category filter passes cat=2020 and filters out items tagged only with 5070.
	host.settings = map[string]any{"releaseTypes": "both", "category": "2020"}
	resp = call(plugins.Request{
		Method: "GET",
		Path:   "/stream/movie/tmdb:37205.json",
		Query:  map[string]string{"imdbId": "tt5311514", "tmdbId": "37205"},
	})
	lastURL = host.calls[len(host.calls)-1].URL
	if !strings.Contains(lastURL, "/api?t=movie&imdbid=tt5311514&cat=2020&limit=75") {
		t.Fatalf("movie category query mismatch: %s", lastURL)
	}
	got = streamPayload{}
	_ = json.Unmarshal(resp.Body, &got)
	// Item 1 has cat=2020 + CDATA title + base32-only magneturl + nzburl; Item 2 only has cat=5070 -> filtered out.
	if len(got.Streams) != 2 {
		t.Fatalf("expected 2 streams for cat=2020 movie item, got %d: %s", len(got.Streams), resp.Body)
	}
	// Base32 EXPBTCXKBLOSWJBKY4OWFSZEZLHZDW7R decodes to 25de198aea0add2b242ac71d62cb24cacf91dbf1.
	if got.Streams[0].InfoHash != "25de198aea0add2b242ac71d62cb24cacf91dbf1" {
		t.Fatalf("base32 magnet btih fallback failed: %+v", got.Streams[0])
	}
	if !strings.Contains(got.Streams[0].Title, "Kimi no Na wa.") {
		t.Fatalf("CDATA title not decoded: %q", got.Streams[0].Title)
	}

	// 8. Explicit AniDB, MAL, and Kitsu IDs query their native Anime Tosho NEW parameters.
	host.settings = map[string]any{"releaseTypes": "torrents", "category": ""}
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/stream/series/anidb:17617:1.json", "/api/torznab?t=tvsearch&aid=17617&ep=1&limit=75"},
		{"/stream/series/mal:52991:1.json", "/api/torznab?t=tvsearch&malid=52991&ep=1&limit=75"},
		{"/stream/series/kitsu:46474:1.json", "/api/torznab?t=tvsearch&kitsuid=46474&ep=1&limit=75"},
		{"/stream/movie/anidb:11829.json", "/api/torznab?t=search&aid=11829&limit=75"},
		{"/stream/movie/mal:32281.json", "/api/torznab?t=movie&malid=32281&limit=75"},
	} {
		r := call(plugins.Request{Method: "GET", Path: tc.path, Query: map[string]string{"season": "3", "episode": "15"}})
		if r.Status != 200 {
			t.Fatalf("%s: status=%d body=%s", tc.path, r.Status, r.Body)
		}
		gotURL := host.calls[len(host.calls)-1].URL
		if !strings.HasSuffix(gotURL, tc.want) {
			t.Fatalf("%s: got URL %s, want suffix %s", tc.path, gotURL, tc.want)
		}
	}

	// 9. Absolute episode numbering (One Piece 1071 & Attack on Titan 52 cross-season).
	host.settings = map[string]any{"releaseTypes": "both", "category": ""}
	resp = call(plugins.Request{
		Method: "GET",
		Path:   "/stream/series/tmdb:37854:1:1071.json",
		Query:  map[string]string{"season": "1", "episode": "1071"},
	})
	if !strings.HasSuffix(host.calls[len(host.calls)-1].URL, "/api?t=tvsearch&tmdbid=37854&ep=1071&limit=75") {
		t.Fatalf("absolute episode should omit season, got %s", host.calls[len(host.calls)-1].URL)
	}
	got = streamPayload{}
	_ = json.Unmarshal(resp.Body, &got)
	if len(got.Streams) != 3 {
		t.Fatalf("one piece 1071 expected 3 streams, got %d: %s", len(got.Streams), resp.Body)
	}

	resp = call(plugins.Request{
		Method: "GET",
		Path:   "/stream/series/tt2560140:1:52.json",
		Query:  map[string]string{"tmdbId": "1429", "season": "1", "episode": "52"},
	})
	if !strings.HasSuffix(host.calls[len(host.calls)-1].URL, "/api?t=tvsearch&tmdbid=1429&ep=52&limit=75") {
		t.Fatalf("aot absolute alias url mismatch: %s", host.calls[len(host.calls)-1].URL)
	}
	got = streamPayload{}
	_ = json.Unmarshal(resp.Body, &got)
	if len(got.Streams) != 2 || got.Streams[0].InfoHash != "7777777777777777777777777777777777777777" {
		t.Fatalf("aot absolute alias should keep season=3 ep=52 item: %s", resp.Body)
	}

	// 10. Season packs (Ether sends :1:1 with query.season="1" and NO query.episode).
	resp = call(plugins.Request{
		Method: "GET",
		Path:   "/stream/series/tmdb:209867:1:1.json",
		Query:  map[string]string{"season": "1", "tmdbId": "209867"},
	})
	if !strings.HasSuffix(host.calls[len(host.calls)-1].URL, "/api?t=tvsearch&tmdbid=209867&season=1&limit=75") {
		t.Fatalf("season pack should omit ep=, got %s", host.calls[len(host.calls)-1].URL)
	}
	got = streamPayload{}
	_ = json.Unmarshal(resp.Body, &got)
	if len(got.Streams) != 2 {
		t.Fatalf("season 1 pack expected 2 streams (S01 torrent + S01 nzb, excluding S02 and single ep), got %d: %s", len(got.Streams), resp.Body)
	}
	if got.Streams[0].InfoHash != "05bd3a45348e1338117e89cf1e25fd76b7a30de3" || got.Streams[0].BehaviorHints.Filename != "" {
		t.Fatalf("season pack torrent mismatch: %+v", got.Streams[0])
	}
	if got.Streams[1].NZBURL != "https://feed.animetosho.net/nzb/687355" {
		t.Fatalf("season pack nzb mismatch: %+v", got.Streams[1])
	}

	// 11. Upstream HTTP 429 propagates HTTP 429 + Retry-After header.
	overrideStatus = 429
	overrideHeaders = map[string]string{"Retry-After": "120"}
	overrideBody = []byte("Too Many Requests")
	rl := call(plugins.Request{Method: "GET", Path: "/stream/movie/tt5311514.json"})
	if rl.Status != 429 || rl.Headers["Retry-After"] != "120" {
		t.Fatalf("expected 429 with Retry-After=120, got status=%d headers=%v body=%s", rl.Status, rl.Headers, rl.Body)
	}

	// 12. Upstream Torznab XML rate-limit error also propagates HTTP 429 + Retry-After.
	overrideStatus = 200
	overrideHeaders = map[string]string{"Retry-After": "45"}
	overrideBody = []byte(`<?xml version="1.0" encoding="utf-8"?><error code="429" description="Rate limit exceeded"/>`)
	rlXML := call(plugins.Request{Method: "GET", Path: "/stream/movie/tt5311514.json"})
	if rlXML.Status != 429 || rlXML.Headers["Retry-After"] != "45" {
		t.Fatalf("expected 429 from XML error, got status=%d headers=%v body=%s", rlXML.Status, rlXML.Headers, rlXML.Body)
	}
}

func TestPackAndEtherStreamIntegration(t *testing.T) {
	pkg, err := kit.LoadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	rep := kit.Check(context.Background(), pkg)
	if !rep.OK {
		t.Fatalf("kit.Check failed:\n%s", rep.Text())
	}
	rawPkg, err := kit.Pack(pkg)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := kit.LoadPkgBytes(rawPkg)
	if err != nil {
		t.Fatal(err)
	}
	for name := range packed.Files {
		if strings.HasPrefix(name, "tests/") {
			t.Fatalf("packed .etherpkg should exclude tests/, found %q", name)
		}
	}

	const nzbBody = `<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="frieren"></file></nzb>`
	nzbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-nzb")
		_, _ = io.WriteString(w, nzbBody)
	}))
	defer nzbServer.Close()

	frierenEp1 := strings.ReplaceAll(string(readFixture(t, "frieren-ep1.xml")), "https://feed.animetosho.net/nzb/510101", nzbServer.URL+"/nzb/510101")
	frierenPacks := readFixture(t, "frieren-packs.xml")
	onePiece1071 := readFixture(t, "one-piece-1071.xml")

	rateLimitNext := false
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if rateLimitNext {
				return &http.Response{
					StatusCode: 429,
					Header:     http.Header{"Retry-After": []string{"90"}},
					Body:       io.NopCloser(strings.NewReader("slow down")),
				}, nil
			}
			q := r.URL.Query()
			body := `<?xml version="1.0" encoding="utf-8"?><rss version="2.0"><channel></channel></rss>`
			switch {
			case q.Get("tmdbid") == "37854" && q.Get("ep") == "1071":
				body = string(onePiece1071)
			case q.Get("tmdbid") == "209867" && q.Get("ep") == "":
				body = string(frierenPacks)
			case q.Get("tmdbid") == "209867" && q.Get("ep") == "1":
				body = frierenEp1
			}
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"application/rss+xml"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := wasm.NewEngine(log)
	defer eng.Close()
	mgr, err := plugins.New(plugins.Deps{
		DB:          store.OpenTest(t),
		Registry:    extend.New(),
		Tools:       tools.NewCatalogue(),
		Logger:      log,
		FetchClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("hosted", plugins.HostedRuntimeFactory(eng))
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	preview, err := mgr.PreviewInstall(context.Background(), plugins.Source{Kind: "pkg", Pkg: rawPkg})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := mgr.Install(context.Background(), preview, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	if fieldErrs, err := mgr.Configure(context.Background(), rec.ID, map[string]any{"releaseTypes": "both", "category": ""}); err != nil || len(fieldErrs) > 0 {
		t.Fatalf("configure failed: errs=%v err=%v", fieldErrs, err)
	}

	// Verify Ether expands the source plugin into an IndexerKindAddon indexer automatically.
	expanded := adapters.ExpandAddonIndexers(context.Background(), mgr, nil)
	if len(expanded) != 1 || expanded[0].Kind != config.IndexerKindAddon || expanded[0].Plugin != rec.ID {
		t.Fatalf("expected 1 expanded addon indexer for %s, got %+v", rec.ID, expanded)
	}

	sources := adapters.NewStreamSources(nil)
	sources.Bind(mgr, nil)
	sources.SetNZBClient(nzbServer.Client())
	backend, err := sources.Backend(expanded[0])
	if err != nil {
		t.Fatal(err)
	}

	noReserve := func(context.Context, string) error { return nil }

	// 1. Episode search converts into both torrent and Usenet core.Release entries.
	epReq := core.SearchRequest{
		Type:    core.TypeEpisode,
		IDs:     core.IDs{TMDB: 209867, IMDB: "tt22248376"},
		Title:   "Frieren: Beyond Journey's End",
		Season:  1,
		Episode: 1,
		Anime:   true,
	}
	rels, err := backend.Search(context.Background(), epReq, noReserve)
	if err != nil {
		t.Fatalf("episode search: %v", err)
	}
	var gotTorrent, gotUsenet bool
	var nzbRel core.Release
	for _, r := range rels {
		if r.Protocol == core.ProtocolTorrent && r.InfoHash == "1111111111111111111111111111111111111111" {
			gotTorrent = true
			if r.Seeders != 112 || !strings.Contains(r.MagnetURL, "nyaa.tracker.wf") {
				t.Fatalf("unexpected torrent release fields: %+v", r)
			}
		}
		if r.Protocol == core.ProtocolUsenet && strings.HasSuffix(r.NZBURL, "/nzb/510101") {
			gotUsenet = true
			nzbRel = r
		}
	}
	if !gotTorrent || !gotUsenet {
		t.Fatalf("expected both torrent and usenet releases, got %+v", rels)
	}

	// FetchNZB downloads the NZB payload cleanly through Ether's Usenet path.
	rawNZB, err := backend.FetchNZB(context.Background(), nzbRel, noReserve)
	if err != nil || !strings.Contains(string(rawNZB), "<nzb") {
		t.Fatalf("FetchNZB failed: err=%v body=%q", err, rawNZB)
	}

	// 2. Absolute episode search triggers Ether's :1:<absolute> alias and returns One Piece 1071.
	absReq := core.SearchRequest{
		Type:            core.TypeEpisode,
		IDs:             core.IDs{TMDB: 37854, IMDB: "tt0388629"},
		Title:           "One Piece",
		Season:          21,
		Episode:         180,
		AbsoluteEpisode: 1071,
		Anime:           true,
	}
	absRels, err := backend.Search(context.Background(), absReq, noReserve)
	if err != nil {
		t.Fatalf("absolute episode search: %v", err)
	}
	if len(absRels) < 2 {
		t.Fatalf("expected absolute episode releases, got %+v", absRels)
	}

	// 3. Season pack search passes through Ether's pack pipeline.
	packReq := core.SearchRequest{
		Type:   core.TypeSeason,
		IDs:    core.IDs{TMDB: 209867, IMDB: "tt22248376"},
		Title:  "Frieren: Beyond Journey's End",
		Season: 1,
		Anime:  true,
	}
	packRels, err := backend.Search(context.Background(), packReq, noReserve)
	if err != nil {
		t.Fatalf("season pack search: %v", err)
	}
	if len(packRels) != 2 {
		t.Fatalf("expected 2 season pack releases (torrent + usenet), got %d: %+v", len(packRels), packRels)
	}

	// 4. Upstream 429 becomes newznab.RateLimitError so Ether pauses politely.
	rateLimitNext = true
	_, err = backend.Search(context.Background(), epReq, noReserve)
	var rlErr *newznab.RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *newznab.RateLimitError on 429, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLiveAnimeTosho(t *testing.T) {
	if os.Getenv("ETHER_LIVE_ANIME_TOSHO") != "1" {
		t.Skip("set ETHER_LIVE_ANIME_TOSHO=1 to run live tests against feed.animetosho.net")
	}
	ctx := context.Background()
	pkg, err := kit.LoadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	rawPkg, err := kit.Pack(pkg)
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := wasm.NewEngine(log)
	defer eng.Close()
	mgr, err := plugins.New(plugins.Deps{
		DB:       store.OpenTest(t),
		Registry: extend.New(),
		Tools:    tools.NewCatalogue(),
		Logger:   log,
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("hosted", plugins.HostedRuntimeFactory(eng))
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}

	preview, err := mgr.PreviewInstall(ctx, plugins.Source{Kind: "pkg", Pkg: rawPkg})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := mgr.Install(ctx, preview, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Configure(ctx, rec.ID, map[string]any{"releaseTypes": "both", "category": ""}); err != nil {
		t.Fatal(err)
	}

	testResp, err := mgr.Call(ctx, rec.ID, plugins.Request{Method: "POST", Path: "/ether/test"})
	if err != nil || testResp.Status != 200 || !strings.Contains(string(testResp.Body), `"ok":true`) {
		t.Fatalf("live /ether/test failed: err=%v status=%d body=%s", err, testResp.Status, testResp.Body)
	}
	t.Logf("live /ether/test: %s", testResp.Body)
	time.Sleep(400 * time.Millisecond)

	for _, p := range []string{"/stream/series/anidb:17617:1.json", "/stream/series/mal:52991:1.json"} {
		r, err := mgr.Call(ctx, rec.ID, plugins.Request{Method: "GET", Path: p})
		if err != nil || r.Status != 200 {
			t.Fatalf("live %s failed: err=%v status=%d body=%s", p, err, r.Status, r.Body)
		}
		var pl streamPayload
		if err := json.Unmarshal(r.Body, &pl); err != nil || len(pl.Streams) == 0 {
			t.Fatalf("live %s returned no streams: err=%v body=%s", p, err, r.Body)
		}
		t.Logf("live %s -> %d streams (first=%q)", p, len(pl.Streams), pl.Streams[0].Name)
		time.Sleep(400 * time.Millisecond)
	}

	expanded := adapters.ExpandAddonIndexers(ctx, mgr, nil)
	ss := adapters.NewStreamSources(nil)
	ss.Bind(mgr, nil)
	backend, err := ss.Backend(expanded[0])
	if err != nil {
		t.Fatal(err)
	}
	noReserve := func(context.Context, string) error { return nil }

	cases := []struct {
		name string
		req  core.SearchRequest
	}{
		{"Frieren S01E01", core.SearchRequest{Type: core.TypeEpisode, IDs: core.IDs{TMDB: 209867, IMDB: "tt22248376"}, Title: "Frieren: Beyond Journey's End", Season: 1, Episode: 1, Anime: true}},
		{"One Piece abs 1071", core.SearchRequest{Type: core.TypeEpisode, IDs: core.IDs{TMDB: 37854, IMDB: "tt0388629"}, Title: "One Piece", Season: 21, Episode: 180, AbsoluteEpisode: 1071, Anime: true}},
		{"Frieren S01 packs", core.SearchRequest{Type: core.TypeSeason, IDs: core.IDs{TMDB: 209867, IMDB: "tt22248376"}, Title: "Frieren: Beyond Journey's End", Season: 1, Anime: true}},
		{"Your Name movie", core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 37205, IMDB: "tt5311514"}, Title: "Your Name.", Year: 2016, Anime: true}},
	}
	var sampleNZB *core.Release
	for _, tc := range cases {
		rels, err := backend.Search(ctx, tc.req, noReserve)
		if err != nil {
			t.Fatalf("live %s: %v", tc.name, err)
		}
		torrents, nzbs := 0, 0
		for i := range rels {
			if rels[i].Protocol == core.ProtocolTorrent {
				torrents++
			}
			if rels[i].Protocol == core.ProtocolUsenet {
				nzbs++
				if sampleNZB == nil {
					r := rels[i]
					sampleNZB = &r
				}
			}
		}
		if torrents == 0 || nzbs == 0 {
			t.Fatalf("live %s expected both torrents and nzbs, got torrents=%d nzbs=%d", tc.name, torrents, nzbs)
		}
		t.Logf("live %s -> %d releases (torrents=%d, nzbs=%d) first=%q", tc.name, len(rels), torrents, nzbs, rels[0].Name)
		time.Sleep(400 * time.Millisecond)
	}

	nzbBytes, err := backend.FetchNZB(ctx, *sampleNZB, noReserve)
	if err != nil || !strings.Contains(string(nzbBytes), "<nzb") {
		t.Fatalf("live FetchNZB failed: err=%v len=%d", err, len(nzbBytes))
	}
	t.Logf("live FetchNZB (%s): %d bytes ok", sampleNZB.NZBURL, len(nzbBytes))
}
