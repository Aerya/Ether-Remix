package newznab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// An indexer that serves Newznab at /api/newznab works from its bare
// host URL and from the full endpoint URL.
func TestNewznabEndpointAtAPINewznab(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimRight(r.URL.Path, "/") != "/api/newznab" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<caps><searching><search available="yes" supportedParams="q"/></searching><categories><category id="2000" name="Movies"/></categories></caps>`))
	}))
	defer srv.Close()
	for _, base := range []string{srv.URL, srv.URL + "/api/newznab", srv.URL + "/api/newznab/"} {
		c := New(testConfig(base), nil, Options{})
		if _, err := c.fetchCaps(context.Background()); err != nil {
			t.Fatalf("%s: caps: %v", base, err)
		}
	}
}

// Streaming searches (SearchWith) must discover /api/newznab even when cached
// caps came from before newznabPath was persisted, learn <newznab:apilimits>
// from the streamed body, survive concurrent 404 races, and persist the path
// across restarts so subsequent startups never hit /api -> 404.
func TestNewznabPathStreamingPersistenceAndConcurrency(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	var api404s atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimRight(r.URL.Path, "/") != "/api/newznab" {
			api404s.Add(1)
			// Empty 404 body verifies getSearchStream converts non-200 before Peek(1).
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Query().Get("t") {
		case "caps":
			_, _ = w.Write([]byte(`<caps><searching><search available="yes" supportedParams="q"/><movie-search available="yes" supportedParams="q,imdbid"/></searching><categories><category id="2000" name="Movies"><subcat id="2040" name="Movies/HD"/></category></categories></caps>`))
		case "get":
			_, _ = w.Write([]byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="x"><segments><segment bytes="1" number="1">a@b</segment></segments></file></nzb>`))
		default:
			_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><newznab:apilimits apimax="500" grabmax="100"/><item><title>Arco.2025.1080p.WEB-DL.DUAL-BiOMA</title><guid>g-%s</guid><link>%s/dl/1.nzb</link><newznab:attr name="category" value="2040"/></item></channel></rss>`, r.URL.Query().Get("q"), "http://"+r.Host)
		}
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	// Seed a legacy caps row without newznabPath, matching an upgraded database.
	c1 := New(cfg, db, Options{SearchCacheTTL: -1})
	c1.capsToDB(ctx, &Caps{
		SearchAvail: true, SearchParams: []string{"q"},
		MovieAvail: true, MovieParams: []string{"q", "imdbid"},
		Categories: []int{2000, 2040},
	}, c1.capsEndpoint())

	// Concurrent streaming searches racing on /api must all succeed.
	var wg sync.WaitGroup
	errs := make([]error, 4)
	counts := make([]int, 4)
	for i := range 4 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var arrived int
			rs, err := c1.SearchWith(ctx, core.SearchRequest{
				Type:  core.TypeMovie,
				Title: fmt.Sprintf("Arco %d", i),
				Year:  2025,
			}, func(batch []core.Release) {
				arrived += len(batch)
			})
			errs[i] = err
			counts[i] = min(len(rs), arrived)
		}(i)
	}
	wg.Wait()
	for i := range 4 {
		if errs[i] != nil || counts[i] == 0 {
			t.Fatalf("concurrent SearchWith[%d]: err=%v count=%d", i, errs[i], counts[i])
		}
	}
	if got := c1.DailyAPILimit(); got != 500 {
		t.Fatalf("streamed apilimits: DailyAPILimit() = %d, want 500", got)
	}

	// A fresh client after restart restores newznabPath from newznab_caps and
	// sends zero requests to /api.
	api404s.Store(0)
	c2 := New(cfg, db, Options{SearchCacheTTL: -1})
	var got []core.Release
	rs, err := c2.SearchWith(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "Arco", Year: 2025}, func(b []core.Release) {
		got = append(got, b...)
	})
	if err != nil || len(rs) == 0 || len(got) == 0 {
		t.Fatalf("post-restart SearchWith: err=%v rs=%d got=%d", err, len(rs), len(got))
	}
	if api404s.Load() != 0 {
		t.Fatalf("post-restart hit /api %d times, want 0", api404s.Load())
	}

	// GUID-only FetchNZB on a fresh uncached client also falls back to /api/newznab.
	c3 := New(cfg, nil, Options{SearchCacheTTL: -1})
	nzb, err := c3.FetchNZB(ctx, core.Release{ID: "r1", GUID: "g-1", Name: "Arco.2025.1080p.WEB-DL.DUAL-BiOMA"})
	if err != nil || len(nzb) == 0 {
		t.Fatalf("GUID FetchNZB /api/newznab fallback: err=%v len=%d", err, len(nzb))
	}
}

// Indexers whose parent category (cat=2000 / cat=5000) returns empty must
// automatically discover either caps-expanded subcategories or unfiltered
// search, persist that mode across restarts, filter non-video items when
// unfiltered, and respect owner-configured categories.
func TestDefaultSearchCategoryFallbackAndPersistence(t *testing.T) {
	ctx := context.Background()
	capsXML := `<caps><searching><search available="yes" supportedParams="q"/><movie-search available="yes" supportedParams="q,imdbid"/></searching><categories><category id="2000" name="Movies"><subcat id="2040" name="Movies/HD"/><subcat id="2045" name="Movies/UHD"/></category></categories></caps>`
	movieItem := `<item><title>Arco.2025.2160p.WEB-DL.DUAL-BiOMA</title><guid>m1</guid><link>http://example.invalid/1.nzb</link><newznab:attr name="category" value="2045"/></item>`
	audioItem := `<item><title>Arco.2025.OST.FLAC-GRP</title><guid>a1</guid><link>http://example.invalid/2.nzb</link><newznab:attr name="category" value="3010"/></item>`

	t.Run("expanded subcategories", func(t *testing.T) {
		db := store.OpenTest(t)
		var seenCats []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			if r.URL.Query().Get("t") == "caps" {
				_, _ = w.Write([]byte(capsXML))
				return
			}
			cat := r.URL.Query().Get("cat")
			seenCats = append(seenCats, cat)
			if cat == "2000,2040,2045" {
				_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>%s</channel></rss>`, movieItem)
				return
			}
			_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel></channel></rss>`))
		}))
		defer srv.Close()

		cfg := testConfig(srv.URL)
		c1 := New(cfg, db, Options{SearchCacheTTL: -1})
		rs, err := c1.SearchWith(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "Arco", Year: 2025, IDs: core.IDs{IMDB: "tt1234567"}}, func([]core.Release) {})
		if err != nil || len(rs) != 1 {
			t.Fatalf("first search: err=%v rs=%d cats=%v", err, len(rs), seenCats)
		}

		// After restart, the expanded category list is used immediately.
		seenCats = nil
		c2 := New(cfg, db, Options{SearchCacheTTL: -1})
		rs, err = c2.SearchWith(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "Arco", Year: 2025, IDs: core.IDs{IMDB: "tt1234567"}}, func([]core.Release) {})
		if err != nil || len(rs) != 1 {
			t.Fatalf("post-restart search: err=%v rs=%d cats=%v", err, len(rs), seenCats)
		}
		if len(seenCats) != 1 || seenCats[0] != "2000,2040,2045" {
			t.Fatalf("post-restart cats = %v, want [2000,2040,2045]", seenCats)
		}
	})

	t.Run("unfiltered fallback filters non-video", func(t *testing.T) {
		db := store.OpenTest(t)
		var seenCats []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			if r.URL.Query().Get("t") == "caps" {
				_, _ = w.Write([]byte(capsXML))
				return
			}
			cat := r.URL.Query().Get("cat")
			seenCats = append(seenCats, cat)
			if cat == "" {
				_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>%s%s</channel></rss>`, audioItem, movieItem)
				return
			}
			_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel></channel></rss>`))
		}))
		defer srv.Close()

		cfg := testConfig(srv.URL)
		c1 := New(cfg, db, Options{SearchCacheTTL: -1})
		var emitted []core.Release
		rs, err := c1.SearchWith(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "Arco", Year: 2025}, func(b []core.Release) {
			emitted = append(emitted, b...)
		})
		if err != nil || len(rs) != 1 || rs[0].Category != 2045 || len(emitted) != 1 {
			t.Fatalf("unfiltered search: err=%v rs=%+v emitted=%d cats=%v", err, rs, len(emitted), seenCats)
		}

		seenCats = nil
		c2 := New(cfg, db, Options{SearchCacheTTL: -1})
		rs, err = c2.Search(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "Arco", Year: 2025})
		if err != nil || len(rs) != 1 || rs[0].Category != 2045 {
			t.Fatalf("post-restart unfiltered search: err=%v rs=%+v", err, rs)
		}
		if len(seenCats) != 1 || seenCats[0] != "" {
			t.Fatalf("post-restart cats = %v, want [\"\"]", seenCats)
		}
	})

	t.Run("explicit owner categories are not overridden", func(t *testing.T) {
		var seenCats []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			if r.URL.Query().Get("t") == "caps" {
				_, _ = w.Write([]byte(capsXML))
				return
			}
			seenCats = append(seenCats, r.URL.Query().Get("cat"))
			_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel></channel></rss>`))
		}))
		defer srv.Close()

		cfg := testConfig(srv.URL)
		cfg.MovieCategories = []int{2040}
		c := New(cfg, nil, Options{SearchCacheTTL: -1})
		if _, err := c.Search(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "Arco", Year: 2025}); err != nil {
			t.Fatal(err)
		}
		if len(seenCats) != 1 || seenCats[0] != "2040" {
			t.Fatalf("explicit MovieCategories overridden: %v", seenCats)
		}
	})
}
