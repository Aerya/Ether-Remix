package egress

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSourceProxyOverridesGlobalAndMediaFlowThroughRedirect(t *testing.T) {
	old := Current()
	t.Cleanup(func() { Set(old) })
	Set(Policy{Enabled: true, Debrid: true, Stremio: true, MediaFlow: config.MediaFlow{Enabled: true, URL: "http://127.0.0.1:1", Password: "unused"}})
	for _, cap := range []Capability{CapDebrid, CapStremio} {
		calls := 0
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Header.Get("Proxy-Authorization") == "" {
				t.Error("missing proxy authentication")
			}
			if r.URL.Path == "/first" {
				http.Redirect(w, r, "http://other.invalid/final", 302)
				return
			}
			io.WriteString(w, "source-route")
		}))
		c := SourceClient(Client(cap, time.Second), config.SourceProxy{Enabled: true, URL: proxy.URL, Username: "user", Password: "secret"})
		req, _ := http.NewRequestWithContext(WithAllDebrid(context.Background()), "GET", "http://source.invalid/first", nil)
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if string(b) != "source-route" || calls != 2 {
			t.Fatalf("route/redirect lost: %s, calls %d", b, calls)
		}
		proxy.Close()
		c.CloseIdleConnections()
	}
}
func TestSourceProxyFailsClosedAndInherits(t *testing.T) {
	old := Current()
	t.Cleanup(func() { Set(old) })
	Set(Policy{})
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; io.WriteString(w, "direct") }))
	defer target.Close()
	for _, address := range []string{"", "http://127.0.0.1:1"} {
		c := SourceClient(Client(CapStremio, time.Second), config.SourceProxy{Enabled: true, URL: address})
		if res, err := c.Get(target.URL); err == nil {
			res.Body.Close()
			t.Fatal("unavailable proxy silently bypassed")
		}
	}
	if hits != 0 {
		t.Fatal("direct request escaped")
	}
	c := SourceClient(Client(CapStremio, time.Second), config.SourceProxy{})
	res, err := c.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if hits != 1 {
		t.Fatal("disabled source override did not inherit")
	}
}
func TestStreamRoutesSeparateAccountAndAddon(t *testing.T) {
	old := streamRouting.Load()
	t.Cleanup(func() { streamRouting.Store(old) })
	a := config.SourceProxy{Enabled: true, URL: "http://account:8000"}
	b := config.SourceProxy{Enabled: true, URL: "http://addon:9000"}
	cfg := config.Config{Debrid: config.Debrid{Providers: []config.DebridProvider{{ID: "account", Proxy: a}}}, Indexers: []config.Indexer{{ID: "addon", Kind: "stremio", StreamProxy: b}}}
	UseSources(cfg)
	for _, tt := range []struct{ account, addon, want string }{{"account", "addon", a.URL}, {"", "addon", b.URL}} {
		req, _ := http.NewRequestWithContext(WithStreamSource(context.Background(), tt.account, tt.addon), "GET", "https://cdn.invalid/file", nil)
		p, ok, err := selectedSourceProxy(req)
		if err != nil || !ok || p.String() != tt.want {
			t.Fatal("wrong stream route")
		}
	}
	cfg.Indexers[0].StreamProxy.URL = "http://changed:9000"
	UseSources(cfg)
	p, _ := sourceProxy(WithStreamSource(context.Background(), "", "addon"))
	if !strings.Contains(p.URL, "changed") {
		t.Fatal("route not refreshed")
	}
}

func TestLegacyAddonPayloadNeverChoosesAnAmbiguousRoute(t *testing.T) {
	old := streamRouting.Load()
	t.Cleanup(func() { streamRouting.Store(old) })
	route := config.SourceProxy{Enabled: true, URL: "http://proxy:8888"}
	cfg := config.Config{Indexers: []config.Indexer{{ID: "one", Kind: "stremio", URL: "https://addon.example/a/manifest.json", StreamProxy: route}}}
	UseSources(cfg)
	p, ok := sourceProxy(WithAddonStream(context.Background(), "", "https://addon.example"))
	if !ok || p != route {
		t.Fatal("legacy origin did not recover route")
	}
	cfg.Indexers = append(cfg.Indexers, config.Indexer{ID: "two", Kind: "stremio", URL: "https://addon.example/b/manifest.json"})
	UseSources(cfg)
	req, _ := http.NewRequestWithContext(WithAddonStream(context.Background(), "", "https://addon.example"), "GET", "https://cdn.example/video", nil)
	if _, selected, err := selectedSourceProxy(req); !selected || err == nil {
		t.Fatal("ambiguous legacy route did not fail closed")
	}
	p, ok = sourceProxy(WithAddonStream(context.Background(), "one", "https://addon.example"))
	if !ok || p != route {
		t.Fatal("explicit addon ID lost precedence")
	}
}
