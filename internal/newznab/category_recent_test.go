package newznab

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestCategorySearchWithEmptyQueryRequestsRecentWithoutQ(t *testing.T) {
	var searchHits atomic.Int32
	var lastQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("t") == "caps" {
			fmt.Fprint(w, `<caps><searching><search available="yes" supportedParams="q"/></searching></caps>`)
			return
		}
		searchHits.Add(1)
		lastQuery = q
		fmt.Fprint(w, `<rss version="2.0"><channel>
			<item>
				<title>Show.S01E03.1080p.WEB-DL.H264-GRP</title>
				<guid>g-1</guid>
				<link>https://idx.example/getnzb/1.nzb</link>
				<pubDate>Mon, 18 Nov 2026 08:00:00 +0000</pubDate>
			</item>
		</channel></rss>`)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 18, 8, 5, 0, 0, time.UTC)
	m := NewManager(db, []config.Indexer{cfg}, Options{
		Now: func() time.Time { return now },
	})

	rels, err := m.Search(t.Context(), core.SearchRequest{Category: "tv"})
	if err != nil {
		t.Fatalf("Search(category=tv, empty query): %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("len(rels) = %d, want 1", len(rels))
	}
	if searchHits.Load() != 1 {
		t.Fatalf("searchHits = %d, want 1", searchHits.Load())
	}
	if lastQuery.Get("t") != "search" || lastQuery.Get("cat") != "5000" {
		t.Fatalf("unexpected query params: %v", lastQuery)
	}
	if lastQuery.Has("q") {
		t.Fatalf("category-only recent search must omit q, got %v", lastQuery)
	}

	// Cached briefly (within 3m), then refreshes so recent/RSS feeds do not freeze for 6h.
	now = now.Add(1 * time.Minute)
	if _, err := m.Search(t.Context(), core.SearchRequest{Category: "tv"}); err != nil {
		t.Fatal(err)
	}
	if searchHits.Load() != 1 {
		t.Fatalf("searchHits within short cache window = %d, want 1", searchHits.Load())
	}

	now = now.Add(5 * time.Minute)
	if _, err := m.Search(t.Context(), core.SearchRequest{Category: "tv"}); err != nil {
		t.Fatal(err)
	}
	if searchHits.Load() != 2 {
		t.Fatalf("searchHits after recent cache expiry = %d, want 2", searchHits.Load())
	}
}
