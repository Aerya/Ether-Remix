package debrid

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/egress"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountProxyAppliesToAPI(t *testing.T) {
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer api-key" {
			t.Error("missing provider key")
		}
		fmt.Fprint(w, `{"username":"test","type":"premium"}`)
	}))
	defer proxy.Close()
	p, err := NewProvider(config.DebridProvider{Kind: "realdebrid", APIKey: "api-key", Proxy: config.SourceProxy{Enabled: true, URL: proxy.URL}}, ProviderOptions{BaseURL: "http://api.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.Account(context.Background())
	if err != nil || a.User != "test" || hits.Load() != 1 {
		t.Fatalf("account route: %+v %v hits=%d", a, err, hits.Load())
	}
}
func TestAddonProxyAppliesToProbeAndStreamingRanges(t *testing.T) {
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "video/x-matroska")
		http.ServeContent(w, r, "Movie.mkv", time.Time{}, &patterned{size: directSize})
	}))
	defer proxy.Close()
	egress.UseSources(config.Config{Indexers: []config.Indexer{{ID: "addon", Kind: "stremio", StreamProxy: config.SourceProxy{Enabled: true, URL: proxy.URL}}}})
	t.Cleanup(func() { egress.UseSources(config.Config{}) })
	rig := newDirectRig(t, config.Debrid{})
	rel := rig.release("resolve")
	rel.IndexerID = "addon"
	raw, err := rig.svc.DirectPayload(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	p, err := DecodePayload(raw)
	if err != nil || p.AddonID != "addon" {
		t.Fatal("payload lost source", err)
	}
	if hits.Load() != 1 || rig.cdnHits.Load() != 0 {
		t.Fatal("probe bypassed proxy")
	}
	engine := NewEngine(rig.svc, EngineOptions{})
	source, err := engine.Open(context.Background(), rel.ID, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	buf := make([]byte, 1024)
	_, err = source.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if hits.Load() < 2 || rig.cdnHits.Load() != 0 {
		t.Fatal("media bypassed proxy")
	}
}

func TestAccountRouteEditRebuildsProvider(t *testing.T) {
	made := 0
	cfg := config.Debrid{Providers: []config.DebridProvider{{ID: "rd", Kind: "realdebrid", APIKey: "key", Enabled: true}}}
	svc := NewService(cfg, Options{NewProvider: func(c config.DebridProvider, o ProviderOptions) (Provider, error) { made++; return NewProvider(c, o) }})
	before := made
	cfg.Providers[0].Proxy = config.SourceProxy{Enabled: true, URL: "http://proxy:8888"}
	svc.UpdateConfig(cfg)
	if made != before+1 {
		t.Fatal("provider reused its previous API route")
	}
}
