package stremio

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAddonRequestProxyManifestSearchAndTest(t *testing.T) {
	manifests, streams := 0, 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest.json" {
			manifests++
			fmt.Fprint(w, `{"id":"test","version":"1","name":"Test","resources":["stream"],"types":["movie"],"idPrefixes":["tt"]}`)
		} else {
			streams++
			fmt.Fprint(w, `{"streams":[]}`)
		}
	}))
	defer proxy.Close()
	s := NewService(nil)
	cfg := config.Indexer{ID: "addon", Kind: "stremio", URL: "http://addon.invalid/manifest.json", RequestProxy: config.SourceProxy{Enabled: true, URL: proxy.URL}}
	if err := s.Test(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	b, err := s.Backend(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt1234567"}}, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if manifests < 2 || streams != 1 {
		t.Fatalf("manifest=%d stream=%d", manifests, streams)
	}
	cfg.RequestProxy.URL = "http://127.0.0.1:1"
	changed, err := s.Backend(cfg)
	if err != nil || changed == b {
		t.Fatal("stale backend route retained")
	}
	if err = s.Test(context.Background(), cfg); err == nil {
		t.Fatal("test bypassed failed proxy")
	}
}
