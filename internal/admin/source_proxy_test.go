package admin_test

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"strings"
	"testing"
)

func TestSourceProxyPasswordsRedactedRestoredAndDestinationBound(t *testing.T) {
	p := config.SourceProxy{Enabled: true, URL: "http://proxy:8888", Username: "user", Password: "private-proxy-secret"}
	cfg := config.Config{Debrid: config.Debrid{Providers: []config.DebridProvider{{ID: "a", Kind: "alldebrid", Proxy: p}}}, Indexers: []config.Indexer{{ID: "s", Kind: "stremio", URL: "https://addon.example/manifest.json", RequestProxy: p, StreamProxy: p}}}
	masked := admin.RedactConfig(cfg)
	b, _ := json.Marshal(masked)
	if strings.Contains(string(b), p.Password) {
		t.Fatal("API leaks password")
	}
	next, errs := admin.UnredactConfig(masked, cfg)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if next.Debrid.Providers[0].Proxy != p || next.Indexers[0].RequestProxy != p || next.Indexers[0].StreamProxy != p {
		t.Fatal("roundtrip lost proxy credentials")
	}
	masked.Debrid.Providers[0].Proxy.URL = "http://other:8888"
	masked.Indexers[0].StreamProxy.URL = "http://other:8888"
	_, errs = admin.UnredactConfig(masked, cfg)
	if len(errs) < 2 {
		t.Fatal("masked secrets forwarded to another destination")
	}
}
