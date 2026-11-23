package adapters

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/newznab"
)

type keylessCheckEngine struct {
	mu           sync.Mutex
	openIDs      []string
	checkIDs     []string
	articleCalls atomic.Int32
}

func (e *keylessCheckEngine) Open(_ context.Context, releaseID string, _ []byte) (core.MediaSource, error) {
	e.mu.Lock()
	e.openIDs = append(e.openIDs, releaseID)
	e.mu.Unlock()
	return &fakeMediaSource{id: releaseID, data: []byte("audio-bytes")}, nil
}

func (e *keylessCheckEngine) Prefetch(_ context.Context, _ string, _ []byte, _, _ int64) error {
	return nil
}

func (e *keylessCheckEngine) CheckArticles(_ context.Context, releaseID string, _ []byte) (core.UsenetCheckResult, error) {
	e.articleCalls.Add(1)
	e.mu.Lock()
	e.checkIDs = append(e.checkIDs, releaseID)
	e.mu.Unlock()
	return core.UsenetCheckResult{Available: true, Checked: 2, Missing: 0}, nil
}

type fakeMediaSource struct {
	id   string
	data []byte
}

func (f *fakeMediaSource) Name() string      { return "Track01.flac" }
func (f *fakeMediaSource) ReleaseID() string { return f.id }

func (f *fakeMediaSource) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if off+int64(n) >= int64(len(f.data)) {
		return n, io.EOF
	}
	return n, nil
}
func (f *fakeMediaSource) Size() int64  { return int64(len(f.data)) }
func (f *fakeMediaSource) Close() error { return nil }

func TestKindResolverIndexerSearchKeylessNZBCacheAndPlayback(t *testing.T) {
	const secretKey = "top-secret-indexer-key"
	const sampleNZB = `<?xml version="1.0"?><nzb><file poster="a" date="1" subject="Track01.flac"><groups><group>alt.binaries.music</group></groups><segments><segment bytes="100" number="1">m1@test</segment></segments></file></nzb>`

	var nzbFetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("apikey") != secretKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		base := "http://" + r.Host
		switch q.Get("t") {
		case "caps":
			fmt.Fprint(w, `<caps><searching><search available="yes" supportedParams="q"/></searching></caps>`)
		case "search":
			fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><item><title>Artist - Album (FLAC)</title><guid isPermaLink="false">g-album-1</guid><link>%s/api?t=get&amp;id=g-album-1&amp;apikey=%s</link><enclosure url="%s/api?t=get&amp;id=g-album-1&amp;apikey=%s" length="123456" type="application/x-nzb"/></item></channel></rss>`, base, secretKey, base, secretKey)
		case "get":
			nzbFetches.Add(1)
			fmt.Fprint(w, sampleNZB)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mgr := newznab.NewManager(nil, []config.Indexer{{
		ID:         "idx-music",
		Name:       "MusicIndexer",
		URL:        srv.URL,
		APIKey:     secretKey,
		Enabled:    true,
		DailyGrabs: 20,
	}}, newznab.Options{SearchCacheTTL: -1})

	// Mirror production wrapper chain: debrid.Indexer -> livetv.VODIndexer -> StreamSources -> newznab.Manager.
	ss := &StreamSources{inner: mgr}
	vod := &livetv.VODIndexer{Inner: ss, Service: func() *livetv.Service { return nil }}
	wrapped := debrid.NewIndexer(vod, debrid.NewService(config.Debrid{}, debrid.Options{}))

	eng := &keylessCheckEngine{}
	kr := NewKindResolver(KindResolverDeps{
		Indexer:       wrapped,
		Engine:        eng,
		UsenetServing: func() bool { return true },
	})
	ctx := context.Background()

	// 1. indexer_search returns key-less nzbUrl alongside indexerId.
	cands, err := kr.ResolveSources(ctx, "community.music", core.SourceQuery{
		Kind:     "indexer_search",
		Query:    "Artist Album",
		Category: "music",
	})
	if err != nil {
		t.Fatalf("ResolveSources(indexer_search): %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("candidates = %d, want 1", len(cands))
	}
	cand := cands[0]
	if cand.IndexerID != "idx-music" {
		t.Fatalf("IndexerID = %q, want idx-music", cand.IndexerID)
	}
	if cand.NZBURL == "" || strings.Contains(cand.NZBURL, secretKey) || strings.Contains(cand.NZBURL, "apikey") {
		t.Fatalf("indexer_search leaked API key in nzbUrl: %q", cand.NZBURL)
	}

	// 2. usenet_check accepts the key-less candidate, fetches authenticated NZB,
	// and shares its cache entry with a legacy keyed URL.
	checked, err := kr.ResolveSources(ctx, "community.music", core.SourceQuery{
		Kind:      "usenet_check",
		Candidate: &cand,
	})
	if err != nil || len(checked) != 1 || checked[0].UsenetCheck == nil || !checked[0].UsenetCheck.Available {
		t.Fatalf("usenet_check(key-less) = %+v err=%v", checked, err)
	}
	if nzbFetches.Load() != 1 || eng.articleCalls.Load() != 1 {
		t.Fatalf("fetches=%d checks=%d, want 1/1", nzbFetches.Load(), eng.articleCalls.Load())
	}
	legacyCand := cand
	legacyCand.NZBURL = srv.URL + "/api?t=get&id=g-album-1&apikey=" + secretKey
	cached, err := kr.ResolveSources(ctx, "community.music", core.SourceQuery{
		Kind:      "usenet_check",
		Candidate: &legacyCand,
	})
	if err != nil || len(cached) != 1 || cached[0].UsenetCheck == nil || !cached[0].UsenetCheck.Cached {
		t.Fatalf("usenet_check(legacy keyed) should hit key-less cache: %+v err=%v", cached, err)
	}
	if nzbFetches.Load() != 1 || eng.articleCalls.Load() != 1 {
		t.Fatalf("legacy keyed usenet_check re-ran fetch/check: fetches=%d checks=%d", nzbFetches.Load(), eng.articleCalls.Load())
	}

	// 3. Playback opens the key-less candidate (re-attaching apikey at fetch
	// time) and keeps the release ID passed to engine.Open key-less.
	ms, err := kr.openRelease(ctx, core.ReleaseHint{Candidate: &cand})
	if err != nil {
		t.Fatalf("openRelease(key-less): %v", err)
	}
	_ = ms.Close()

	eng.mu.Lock()
	defer eng.mu.Unlock()
	for _, id := range append(eng.checkIDs, eng.openIDs...) {
		if strings.Contains(id, secretKey) || strings.Contains(id, "apikey") {
			t.Fatalf("release ID leaked API key: %q", id)
		}
	}
	if len(eng.openIDs) != 1 {
		t.Fatalf("openIDs = %v, want 1 entry", eng.openIDs)
	}
}
