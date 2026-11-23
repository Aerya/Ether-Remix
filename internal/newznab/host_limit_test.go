package newznab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestPerHostConcurrencyLimit(t *testing.T) {
	const feedCount = 24
	var active, peak atomic.Int32
	var reachedCap sync.Once
	atCap := make(chan struct{})
	release := make(chan struct{})

	prowlarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><caps><searching><search available="yes" supportedParams="q"/></searching></caps>`))
			return
		}
		cur := active.Add(1)
		defer active.Add(-1)
		for {
			prev := peak.Load()
			if cur <= prev || peak.CompareAndSwap(prev, cur) {
				break
			}
		}
		if cur == DefaultMaxPerHost {
			reachedCap.Do(func() { close(atCap) })
		}
		<-release
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0"?><rss><channel><item><title>Movie.2024.1080p.%s</title><guid>%s</guid><link>http://example.invalid/nzb/%s</link><enclosure url="http://example.invalid/nzb/%s" length="1000" type="application/x-nzb"/></item></channel></rss>`,
			r.URL.Path[1:], r.URL.Path[1:], r.URL.Path[1:], r.URL.Path[1:])
	}))
	defer prowlarr.Close()

	cfgs := make([]config.Indexer, feedCount)
	for i := range cfgs {
		cfgs[i] = config.Indexer{
			ID:      fmt.Sprintf("prowlarr-%d", i+1),
			Name:    fmt.Sprintf("Prowlarr %d", i+1),
			URL:     fmt.Sprintf("%s/%d", prowlarr.URL, i+1),
			APIKey:  "key",
			Enabled: true,
		}
	}

	// Another host is not held back by the full Prowlarr host queue.
	otherReady := make(chan struct{})
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") != "caps" {
			select {
			case <-otherReady:
			default:
				close(otherReady)
			}
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel><item><title>Movie.2024.1080p.Other</title><guid>other</guid><link>http://example.invalid/nzb/other</link><enclosure url="http://example.invalid/nzb/other" length="2000" type="application/x-nzb"/></item></channel></rss>`))
	}))
	defer other.Close()

	allCfgs := append(cfgs, config.Indexer{
		ID:      "standalone",
		Name:    "Standalone",
		URL:     other.URL,
		APIKey:  "key",
		Enabled: true,
	})
	m := NewManager(nil, allCfgs, Options{HTTPClient: prowlarr.Client(), SearchCacheTTL: -1})

	done := make(chan struct{})
	var results []core.Release
	var searchErr error
	go func() {
		defer close(done)
		results, searchErr = m.Search(context.Background(), core.SearchRequest{
			Type:  core.TypeMovie,
			Title: "Movie",
			Year:  2024,
		})
	}()

	select {
	case <-atCap:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Prowlarr host to reach concurrency cap")
	}
	select {
	case <-otherReady:
	case <-time.After(5 * time.Second):
		t.Fatal("distinct indexer host was blocked by Prowlarr host limit")
	}
	// Hold the first wave briefly so queued indexers wait locally: none may
	// slip past DefaultMaxPerHost.
	const holdWait = 80 * time.Millisecond
	time.Sleep(holdWait)
	if got := peak.Load(); got > DefaultMaxPerHost {
		t.Fatalf("peak concurrent requests to Prowlarr = %d, want <= %d", got, DefaultMaxPerHost)
	}
	close(release)
	<-done

	if searchErr != nil {
		t.Fatalf("Search error: %v", searchErr)
	}
	if len(results) != feedCount+1 {
		t.Fatalf("merged results = %d, want %d", len(results), feedCount+1)
	}
	if got := peak.Load(); got != DefaultMaxPerHost {
		t.Fatalf("peak concurrent requests = %d, want %d", got, DefaultMaxPerHost)
	}
	m.hosts.mu.Lock()
	leftover := len(m.hosts.hosts)
	m.hosts.mu.Unlock()
	if leftover != 0 {
		t.Fatalf("idle host gates retained = %d, want 0", leftover)
	}
}
