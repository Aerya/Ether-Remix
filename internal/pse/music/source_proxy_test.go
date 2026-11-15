package music

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/egress"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeMusicAccountProxyCarriesAudio(t *testing.T) {
	hits := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Range") != "bytes=0-99" {
			t.Error("lost range")
		}
		fmt.Fprint(w, "audio-via-proxy")
	}))
	defer proxy.Close()
	egress.UseSources(config.Config{Debrid: config.Debrid{Providers: []config.DebridProvider{{ID: "music", Proxy: config.SourceProxy{Enabled: true, URL: proxy.URL}}}}})
	t.Cleanup(func() { egress.UseSources(config.Config{}) })
	s := NewServer(Options{})
	req := httptest.NewRequest("GET", "http://remix.invalid/rest/stream", nil)
	req.Header.Set("Range", "bytes=0-99")
	ctx := egress.WithStreamSource(context.Background(), "music", "")
	// Public literal needs no external DNS; the proxy answers without contacting it.
	res, err := s.openAudio(ctx, req, "http://203.0.113.1/audio")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if hits != 1 || string(b) != "audio-via-proxy" {
		t.Fatal("audio bypassed account proxy")
	}
}
