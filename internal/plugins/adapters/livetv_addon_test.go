package adapters_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/plugins/remote"
	"github.com/braedonsaunders/ether/internal/store"
)

// addonTV is Live TV and the plugin host wired as in the server, with a
// real URL runtime: the add-on is an httptest server on loopback, which
// InstallAddon trusts as a LAN add-on the owner pasted.
type addonTV struct {
	tv   *livetv.Service
	mgr  *plugins.Manager
	reg  *extend.Registry
	rec  plugins.Record
	kind string
	link string
}

func newAddonTV(t *testing.T, now time.Time, handler http.HandlerFunc) *addonTV {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	db := store.OpenTest(t)
	tv, err := livetv.New(livetv.Options{DB: db, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tv.Close() })
	reg := extend.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := plugins.New(plugins.Deps{DB: db, Registry: reg, Tools: tools.NewCatalogue(), Logger: log, IsLeader: func() bool { return true }, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	factory := &remote.Factory{TrustedLAN: remote.GrantsLAN(mgr.GrantsOf), Logger: log}
	mgr.RegisterRuntime("remote", factory.RuntimeFactory())
	adapters.Attach(mgr, nil, nil, tv)
	link := srv.URL + "/secret/manifest.json"
	rec, err := mgr.InstallAddon(context.Background(), plugins.AddonInstall{Link: link, Roles: []plugins.Capability{plugins.CapLiveTV}})
	if err != nil {
		t.Fatal(err)
	}
	return &addonTV{tv: tv, mgr: mgr, reg: reg, rec: rec, kind: extend.PluginID(rec.ID, plugins.StremioChannelsKey), link: link}
}

var owner = &core.User{IsOwner: true}

// legacyID is how the built-in Stremio source minted ids.
func legacyID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:10])
}

func TestAddonChannelsImportRefreshAndXMLTVGuide(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	var guideFetches atomic.Int32
	a := newAddonTV(t, now, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/secret/manifest.json":
			fmt.Fprint(w, `{"id":"tv.example","version":"1.0.0","name":"Example TV","resources":["catalog","stream"],"types":["tv"],"catalogs":[{"type":"tv","id":"live","name":"News"}],"behaviorHints":{"epgUrl":"/guide.xml"}}`)
		case "/secret/catalog/tv/live.json":
			fmt.Fprint(w, `{"metas":[{"id":"channel1","name":"Example","logo":"https://img.example/logo.png"},{"id":"channel2","name":"Second"}]}`)
		case "/secret/stream/tv/channel1.json":
			fmt.Fprint(w, `{"streams":[{"name":"HD","url":"https://stream.example/one?key=secret","behaviorHints":{"proxyHeaders":{"request":{"referer":"https://stream.example/","Host":"evil.example"}}}},{"name":"SD\nbackup","url":"https://stream.example/two"},{"url":"https://stream.example/one?key=secret"},{"url":"rtmp://stream.example/x"},{"infoHash":"0123456789abcdef0123456789abcdef01234567"}]}`)
		case "/secret/stream/tv/channel2.json":
			fmt.Fprint(w, `{"streams":[{"url":"https://stream.example/second"}]}`)
		case "/guide.xml":
			guideFetches.Add(1)
			fmt.Fprint(w, `<tv><channel id="channel1"><display-name>Example</display-name></channel><programme channel="channel1" start="20261102090000 +0000" stop="20261102100000 +0000"><title>News</title></programme><programme channel="elsewhere" start="20261102090000 +0000" stop="20261102100000 +0000"><title>Other</title></programme></tv>`)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	if !a.rec.Manifest.HasCapability(plugins.CapLiveTV) {
		t.Fatalf("the tv add-on has no Live TV role: %+v", a.rec.Manifest.EffectiveEther().Capabilities)
	}
	kind, ok := a.tv.KindRegistry().Lookup(a.kind)
	if !ok {
		t.Fatalf("kind %s not registered", a.kind)
	}
	if d := kind.Describe(); d.Editor != "addon" || d.Label.Resolved != "Example TV" {
		t.Fatalf("describe = %+v", d)
	}
	src, err := a.tv.ImportSource(ctx, livetv.Source{ID: "s", Name: "Example TV", Kind: a.kind})
	if err != nil {
		t.Fatal(err)
	}
	if src.Kind != a.kind || src.RefreshHours != 6 || strings.Contains(src.PlaylistURL, "secret") {
		t.Fatalf("source = %+v", src)
	}
	cs, err := a.tv.SourceChannels(ctx, "s")
	if err != nil || len(cs) != 2 {
		t.Fatalf("channels=%+v err=%v", cs, err)
	}
	var c livetv.Channel
	for _, ch := range cs {
		if ch.GuideID == "channel1" {
			c = ch
		}
	}
	if c.ID != legacyID("s", "channel1") || c.Name != "Example" || c.Group != "News" || c.Logo != "https://img.example/logo.png" || c.Number == "" {
		t.Fatalf("channel = %+v", c)
	}
	in := c.StreamSources
	if len(in) != 2 || in[0].ID != "stremio-"+legacyID(c.ID, "0") || in[1].ID != "stremio-"+legacyID(c.ID, "1") ||
		in[0].Name != "HD" || in[1].Name != "SD" || in[1].URL != "https://stream.example/two" {
		t.Fatalf("inputs = %+v", in)
	}
	if len(in[0].Headers) != 1 || in[0].Headers["Referer"] != "https://stream.example/" {
		t.Fatalf("proxy headers = %+v", in[0].Headers)
	}
	guide, err := a.tv.Guide(ctx, owner, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	titles := 0
	for _, p := range guide {
		if p.ChannelID == c.ID && p.Item.Title == "News" {
			titles++
		}
	}
	if titles != 1 || guideFetches.Load() != 1 {
		t.Fatalf("guide programmes=%d fetches=%d %+v", titles, guideFetches.Load(), guide)
	}

	// The owner switches the first input off; a refresh keeps that.
	c.StreamSources[0].Enabled = false
	if _, err := a.tv.SaveChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.tv.RefreshSource(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	after, _ := a.tv.Channel(ctx, c.ID)
	if len(after.StreamSources) != 2 || after.StreamSources[0].Enabled || after.StreamURL != "https://stream.example/two" {
		t.Fatalf("refresh lost owner choices: %+v", after)
	}
	if _, err := a.tv.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
}

func TestAddonChannelsNativeGuide(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	var days atomic.Int32
	a := newAddonTV(t, now, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/secret/manifest.json":
			fmt.Fprint(w, `{"id":"tv.guide.example","version":"1.0.0","name":"Guide TV","resources":["catalog","stream"],"types":["tv"],"catalogs":[{"type":"tv","id":"live","extra":[{"name":"date"}]}],"behaviorHints":{"epgProvider":true}}`)
		case strings.Contains(p, "skip="):
			fmt.Fprint(w, `{"metasDetailed":[]}`)
		case strings.Contains(p, "date=2026-11-02"):
			days.Add(1)
			fmt.Fprint(w, `{"metasDetailed":[{"id":"c","name":"Channel","videos":[{"id":"p","title":"News","startTime":"2026-11-02T09:00:00Z","endTime":"2026-11-02T10:00:00Z"},{"id":"q","title":"Long ago","startTime":"2026-10-01T09:00:00Z","endTime":"2026-10-01T10:00:00Z"}]},{"id":"unknown","name":"Not imported","videos":[{"id":"r","title":"X","startTime":"2026-11-02T09:00:00Z","endTime":"2026-11-02T10:00:00Z"}]}]}`)
		case strings.Contains(p, "date="):
			days.Add(1)
			if strings.Contains(p, "date=2026-11-05") {
				http.Error(w, "busy", http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, `{"metasDetailed":[]}`)
		case strings.HasPrefix(p, "/secret/catalog/"):
			fmt.Fprint(w, `{"metas":[{"id":"c","name":"Channel"}]}`)
		case strings.HasPrefix(p, "/secret/stream/"):
			fmt.Fprint(w, `{"streams":[{"url":"https://example.test/live"}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	if _, err := a.tv.ImportSource(ctx, livetv.Source{ID: "native", Name: "Native", Kind: a.kind}); err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, at := range []time.Time{now, time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)} {
		guide, err := a.tv.Guide(ctx, owner, at.Add(-2*time.Hour), at.Add(2*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range guide {
			titles = append(titles, p.Item.Title)
		}
	}
	// One page per day of the window (Nov 1 to Nov 16); a failed day is skipped.
	if len(titles) != 1 || titles[0] != "News" || days.Load() != 16 {
		t.Fatalf("programmes=%v days=%d", titles, days.Load())
	}
}

func TestAddonChannelsOneFailedChannelDoesNotFailImport(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	var broken atomic.Bool
	a := newAddonTV(t, now, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/secret/manifest.json":
			fmt.Fprint(w, `{"id":"tv.flaky.example","version":"1.0.0","name":"Flaky TV","resources":["catalog","stream"],"types":["tv"],"catalogs":[{"type":"tv","id":"a","name":"A"},{"type":"tv","id":"needs","extra":[{"name":"genre","isRequired":true}]}]}`)
		case "/secret/catalog/tv/a.json":
			fmt.Fprint(w, `{"metas":[{"id":"one","name":"One"},{"id":"two","name":"Two"},{"id":"three","name":"Three"}]}`)
		case "/secret/stream/tv/one.json":
			fmt.Fprint(w, `{"streams":[{"url":"https://example.test/one"}]}`)
		case "/secret/stream/tv/two.json":
			if broken.Load() {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			fmt.Fprint(w, `{"streams":[{"url":"https://example.test/two"}]}`)
		case "/secret/stream/tv/three.json":
			http.Error(w, "down", http.StatusBadGateway)
		case "/secret/catalog/tv/a/skip=3.json":
			fmt.Fprint(w, `{"metas":[]}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	if _, err := a.tv.ImportSource(ctx, livetv.Source{ID: "s", Name: "Flaky", Kind: a.kind}); err != nil {
		t.Fatal(err)
	}
	names := func() map[string]livetv.Channel {
		cs, _ := a.tv.SourceChannels(ctx, "s")
		out := map[string]livetv.Channel{}
		for _, c := range cs {
			out[c.Name] = c
		}
		return out
	}
	got := names()
	if len(got) != 2 || got["Three"].ID != "" {
		t.Fatalf("a new channel without streams was imported, or a good one dropped: %+v", got)
	}
	two := got["Two"]
	two.Number, two.Favorite = "42", true
	if _, err := a.tv.SaveChannel(ctx, two); err != nil {
		t.Fatal(err)
	}
	broken.Store(true)
	if _, err := a.tv.RefreshSource(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	got = names()
	if c := got["Two"]; c.Number != "42" || !c.Favorite || len(c.StreamSources) != 1 || c.StreamURL != "https://example.test/two" {
		t.Fatalf("a channel whose lookup failed lost its inputs or owner choices: %+v", c)
	}
}

func TestAddonChannelsWithNoStreamsAnywhereFailsImport(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	a := newAddonTV(t, now, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/secret/manifest.json":
			fmt.Fprint(w, `{"id":"tv.down.example","version":"1.0.0","name":"Down TV","resources":["catalog","stream"],"types":["tv"],"catalogs":[{"type":"tv","id":"a"}]}`)
		case "/secret/catalog/tv/a.json":
			fmt.Fprint(w, `{"metas":[{"id":"one","name":"One"}]}`)
		default:
			http.Error(w, "down", http.StatusBadGateway)
		}
	})
	if _, err := a.tv.ImportSource(context.Background(), livetv.Source{ID: "s", Name: "Down", Kind: a.kind}); err == nil {
		t.Fatal("an import with no readable streams succeeded")
	}
}

type addonTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *addonTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *addonTestClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

func TestEventAddonImportGuideAndTune(t *testing.T) {
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	clock := &addonTestClock{now: now}

	var event1Stream2Dead atomic.Bool
	var hitsMu sync.Mutex
	var mediaHits []string
	var sawReferer string
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsMu.Lock()
		mediaHits = append(mediaHits, r.URL.Path)
		if r.URL.Path == "/event1-s2.m3u8" {
			sawReferer = r.Header.Get("Referer")
		}
		hitsMu.Unlock()
		switch r.URL.Path {
		case "/event1-s1.m3u8":
			http.Error(w, "offline", http.StatusForbidden)
		case "/event1-s2.m3u8":
			if event1Stream2Dead.Load() {
				http.Error(w, "offline", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nseg.ts\n")
		case "/event2-s1.m3u8", "/livenow-s1.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nseg.ts\n")
		case "/seg.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			pkt := make([]byte, 188)
			pkt[0] = 0x47
			_, _ = w.Write(pkt)
		default:
			http.NotFound(w, r)
		}
	}))
	defer media.Close()

	var catalogHits atomic.Int32
	var metaHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/key/manifest.json":
			fmt.Fprint(w, `{
				"id": "community.sports.fake",
				"version": "1.3.0",
				"name": "Sports Streams",
				"logo": "https://cdn.example/sports.png",
				"types": ["sport"],
				"resources": [
					{"name": "catalog", "types": ["sport"]},
					{"name": "meta", "types": ["sport"], "idPrefixes": ["streamed", "sf"]},
					{"name": "stream", "types": ["sport"], "idPrefixes": ["streamed", "sf"]}
				],
				"catalogs": [
					{"type": "sport", "id": "sports_live", "name": "Live Now", "extra": [{"name": "search", "isRequired": false}, {"name": "skip", "isRequired": false}]},
					{"type": "sport", "id": "sports_football", "name": "Football", "extra": [{"name": "skip", "isRequired": false}]},
					{"type": "sport", "id": "sports_basketball", "name": "Basketball"},
					{"type": "sport", "id": "sports_search", "name": "Search", "extra": [{"name": "search", "isRequired": true}]}
				]
			}`)
		case "/key/catalog/sport/sports_live.json":
			catalogHits.Add(1)
			fmt.Fprint(w, `{"metas":[{"id":"streamed:main-event","type":"sport","name":"Main Event Live"}]}`)
		case "/key/catalog/sport/sports_football.json":
			catalogHits.Add(1)
			fmt.Fprint(w, `{"metas":[
				{"id":"streamed:arsenal-chelsea","type":"sport","name":"Arsenal vs Chelsea","released":"2026-11-21T14:00:00Z","runtime":"120 min","description":"Premier League derby","behaviorHints":{"defaultVideoId":"sf:arsenal-main"}},
				{"id":"streamed:madrid-barca","type":"sport","name":"Real Madrid vs Barcelona","released":"2026-11-21T14:00:00Z"},
				{"id":"streamed:bayern-dortmund","type":"sport","name":"Bayern vs Dortmund","released":"2026-11-21T18:00:00Z","runtime":"90 min","description":"Bundesliga"}
			]}`)
		case "/key/catalog/sport/sports_basketball.json":
			catalogHits.Add(1)
			fmt.Fprint(w, `{"metas":[{"id":"streamed:lakers-celtics","type":"sport","name":"Lakers vs Celtics","releaseInfo":"19:30 UTC","description":"NBA tip-off"}]}`)
		case "/key/meta/sport/streamed:madrid-barca.json":
			metaHits.Add(1)
			fmt.Fprint(w, `{"meta":{"id":"streamed:madrid-barca","type":"sport","name":"Real Madrid vs Barcelona","released":"2026-11-21T14:00:00Z","runtime":"2h","description":"El Clasico"}}`)
		case "/key/meta/sport/streamed:main-event.json":
			metaHits.Add(1)
			fmt.Fprint(w, `{"meta":{"id":"streamed:main-event","type":"sport","name":"Main Event Live"}}`)
		case "/key/stream/sport/sf:arsenal-main.json":
			fmt.Fprintf(w, `{"streams":[
				{"name":"HD 1","url":"%s/event1-s1.m3u8"},
				{"name":"HD 2","url":"%s/event1-s2.m3u8","behaviorHints":{"proxyHeaders":{"request":{"Referer":"https://sports.example/"}}}}
			]}`, media.URL, media.URL)
		case "/key/stream/sport/streamed:madrid-barca.json":
			fmt.Fprintf(w, `{"streams":[{"name":"Main Feed","url":"%s/event2-s1.m3u8"}]}`, media.URL)
		case "/key/stream/sport/streamed:main-event.json":
			fmt.Fprintf(w, `{"streams":[{"name":"Live Feed","url":"%s/livenow-s1.m3u8"}]}`, media.URL)
		default:
			if strings.Contains(r.URL.Path, "skip=") {
				fmt.Fprint(w, `{"metas":[]}`)
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db := store.OpenTest(t)
	tv, err := livetv.New(livetv.Options{DB: db, Now: clock.Now, HTTPClient: media.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tv.Close() })
	reg := extend.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := plugins.New(plugins.Deps{DB: db, Registry: reg, Tools: tools.NewCatalogue(), Logger: log, IsLeader: func() bool { return true }, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	factory := &remote.Factory{TrustedLAN: remote.GrantsLAN(mgr.GrantsOf), Logger: log}
	mgr.RegisterRuntime("remote", factory.RuntimeFactory())
	adapters.Attach(mgr, nil, nil, tv)

	ctx := context.Background()
	rec, err := mgr.InstallAddon(ctx, plugins.AddonInstall{Link: srv.URL + "/key/manifest.json", Roles: []plugins.Capability{plugins.CapLiveTV}})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Manifest.HasCapability(plugins.CapLiveTV) {
		t.Fatalf("event add-on missing CapLiveTV: %+v", rec.Manifest.EffectiveEther().Capabilities)
	}
	kindID := extend.PluginID(rec.ID, plugins.StremioChannelsKey)
	if _, err := tv.ImportSource(ctx, livetv.Source{ID: "sports", Name: "Sports Streams", Kind: kindID}); err != nil {
		t.Fatal(err)
	}

	// Channels: one per browsable sport catalog; search-only catalog skipped.
	cs, err := tv.SourceChannels(ctx, "sports")
	if err != nil || len(cs) != 3 {
		t.Fatalf("channels = %+v err=%v", cs, err)
	}
	byName := map[string]livetv.Channel{}
	for _, c := range cs {
		byName[c.Name] = c
	}
	football := byName["Football"]
	liveNow := byName["Live Now"]
	basketball := byName["Basketball"]
	if football.ID == "" || liveNow.ID == "" || basketball.ID == "" {
		t.Fatalf("missing expected catalog channels: %+v", byName)
	}
	if !strings.HasPrefix(football.StreamURL, "plugin://") || !strings.Contains(football.StreamURL, "/event/sport/sports_football") {
		t.Fatalf("unexpected event channel stream url: %q", football.StreamURL)
	}

	// Guide: simultaneous live events on Football, enriched /meta/, upcoming events, and untimed Live Now fallback.
	guide, err := tv.Guide(ctx, owner, now.Add(-2*time.Hour), now.Add(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	footProgs := map[string]livetv.Programme{}
	var liveNowProgs, basketProgs []livetv.Programme
	for _, p := range guide {
		switch p.ChannelID {
		case football.ID:
			footProgs[p.Item.Title] = p
		case liveNow.ID:
			liveNowProgs = append(liveNowProgs, p)
		case basketball.ID:
			basketProgs = append(basketProgs, p)
		}
	}
	if len(footProgs) != 3 {
		t.Fatalf("football programmes = %+v, want 3 (2 simultaneous live + 1 upcoming)", footProgs)
	}
	if p := footProgs["Arsenal vs Chelsea"]; p.Item.Summary != "Premier League derby" || p.End.Sub(p.Start) != 2*time.Hour {
		t.Fatalf("arsenal programme = %+v", p)
	}
	if p := footProgs["Real Madrid vs Barcelona"]; p.Item.Summary != "El Clasico" || p.End.Sub(p.Start) != 2*time.Hour {
		t.Fatalf("madrid programme (meta enriched) = %+v", p)
	}
	if p := footProgs["Bayern vs Dortmund"]; !p.Start.Equal(now.Add(3*time.Hour)) || p.End.Sub(p.Start) != 90*time.Minute {
		t.Fatalf("upcoming bayern programme = %+v", p)
	}
	if len(liveNowProgs) != 1 || !liveNowProgs[0].Item.Live || liveNowProgs[0].Start.After(now) || !liveNowProgs[0].End.After(now) {
		t.Fatalf("live now fallback programme = %+v", liveNowProgs)
	}
	if len(basketProgs) != 1 || basketProgs[0].Start.Hour() != 19 || basketProgs[0].Start.Minute() != 30 {
		t.Fatalf("basketball releaseInfo programme = %+v", basketProgs)
	}
	if metaHits.Load() == 0 {
		t.Fatal("expected /meta/ enrichment for item missing runtime/description")
	}

	// Idle after import: no eager NextRefresh is scheduled.
	srcs, err := tv.Sources(ctx)
	if err != nil || len(srcs) != 1 || srcs[0].NextRefresh != nil {
		t.Fatalf("idle event source should not schedule background refresh: %+v err=%v", srcs, err)
	}

	// Tune 1: Football tries Event 1 stream 1 (503) -> fails over to Event 1 stream 2 (HLS) with proxy headers.
	info1, err := tv.Tune(ctx, owner, football.ID, core.ClientCaps{})
	if err != nil {
		t.Fatalf("tune football (stream failover): %v", err)
	}
	if info1.Programme == nil || info1.Programme.Item.Title != "Arsenal vs Chelsea" {
		t.Fatalf("tune 1 programme = %+v", info1.Programme)
	}
	if sawReferer != "https://sports.example/" {
		t.Fatalf("proxy header Referer = %q", sawReferer)
	}
	// While watching, the source IS in use and schedules the short 5m refresh.
	srcs, err = tv.Sources(ctx)
	if err != nil || srcs[0].NextRefresh == nil || !srcs[0].NextRefresh.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("watched event source NextRefresh = %+v, want %v", srcs, now.Add(5*time.Minute))
	}
	tv.Stop(ctx, info1.ID, owner)

	// Tune 2: All streams of Event 1 fail -> fails over to next live event (Real Madrid vs Barcelona).
	event1Stream2Dead.Store(true)
	info2, err := tv.Tune(ctx, owner, football.ID, core.ClientCaps{})
	if err != nil {
		t.Fatalf("tune football (next live event failover): %v", err)
	}
	if info2.Programme == nil || info2.Programme.Item.Title != "Real Madrid vs Barcelona" {
		t.Fatalf("tune 2 programme after event failover = %+v", info2.Programme)
	}
	tv.Stop(ctx, info2.ID, owner)

	// Tune 3: Live Now catalog plays its untimed live event.
	info3, err := tv.Tune(ctx, owner, liveNow.ID, core.ClientCaps{})
	if err != nil {
		t.Fatalf("tune live now: %v", err)
	}
	if info3.Programme == nil || info3.Programme.Item.Title != "Main Event Live" {
		t.Fatalf("tune 3 programme = %+v", info3.Programme)
	}
	tv.Stop(ctx, info3.ID, owner)

	// Tune 4: Basketball has only an upcoming event (19:30 UTC), so tuning at 15:00 fails honestly.
	if _, err := tv.Tune(ctx, owner, basketball.ID, core.ClientCaps{}); !errors.Is(err, core.ErrNotAvailable) || !strings.Contains(err.Error(), "no live event") {
		t.Fatalf("tuning channel with no live event = %v, want ErrNotAvailable with honest message", err)
	}

	// Verify hit order: event1-s1 was tried before event1-s2, and event2-s1 was tried once event1 failed.
	hitsMu.Lock()
	gotHits := strings.Join(mediaHits, ",")
	hitsMu.Unlock()
	if !strings.Contains(gotHits, "/event1-s1.m3u8,/event1-s2.m3u8") || !strings.Contains(gotHits, "/event2-s1.m3u8") {
		t.Fatalf("unexpected media attempt order: %s", gotHits)
	}

	// Once nobody is watching and the idle window passes, NextRefresh clears again.
	clock.Set(now.Add(20 * time.Minute))
	srcs, err = tv.Sources(ctx)
	if err != nil || srcs[0].NextRefresh != nil {
		t.Fatalf("returned-to-idle event source still had NextRefresh: %+v", srcs)
	}
}

func TestEventAddonHonestErrorsWhenNothingPlayable(t *testing.T) {
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)

	t.Run("empty catalogs", func(t *testing.T) {
		a := newAddonTV(t, now, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/secret/manifest.json":
				fmt.Fprint(w, `{"id":"sport.empty.example","version":"1.0.0","name":"Empty Sports","types":["sport"],"resources":["catalog","stream"],"catalogs":[{"type":"sport","id":"live","name":"Live Now"}]}`)
			case "/secret/catalog/sport/live.json":
				fmt.Fprint(w, `{"metas":[]}`)
			default:
				http.NotFound(w, r)
			}
		})
		_, err := a.tv.ImportSource(context.Background(), livetv.Source{ID: "s", Name: "Empty", Kind: a.kind})
		if err == nil || !strings.Contains(err.Error(), "no playable live events") {
			t.Fatalf("err = %v, want honest empty-events error", err)
		}
	})

	t.Run("auth required catalogs", func(t *testing.T) {
		a := newAddonTV(t, now, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/secret/manifest.json":
				fmt.Fprint(w, `{"id":"sport.auth.example","version":"1.0.0","name":"Keyed Sports","types":["sport"],"resources":["catalog","stream"],"catalogs":[{"type":"sport","id":"live","name":"Live Now"}]}`)
			default:
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}
		})
		_, err := a.tv.ImportSource(context.Background(), livetv.Source{ID: "s", Name: "Keyed", Kind: a.kind})
		if err == nil || !strings.Contains(err.Error(), "live event catalogs could not be read") {
			t.Fatalf("err = %v, want honest catalog-read error", err)
		}
	})
}
