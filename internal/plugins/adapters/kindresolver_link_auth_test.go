package adapters

import (
	"bytes"
	"context"
	"fmt"
	"html"
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
	"github.com/braedonsaunders/ether/internal/kindstream"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/stream"
	"github.com/braedonsaunders/ether/internal/vault"
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

func TestKindResolverReleaseFilesAndMultiTrackShareVaultWithoutRegrab(t *testing.T) {
	ctx := context.Background()
	const secretKey = "top-secret-indexer-key"

	fetcher := &simpleMemFetcher{articles: map[string]struct {
		data []byte
		part core.ArticlePart
	}{}}
	track1Data := append([]byte("fLaC"), bytes.Repeat([]byte("track-one-audio-"), 200)...)
	track2Data := append([]byte("fLaC"), bytes.Repeat([]byte("track-two-audio-"), 200)...)
	for _, tf := range []struct {
		name string
		data []byte
	}{
		{name: "01 - Opening.flac", data: track1Data},
		{name: "02 - I'm Yours.flac", data: track2Data},
	} {
		id := strings.ReplaceAll(tf.name, " ", "_") + ".1@test"
		fetcher.articles[id] = struct {
			data []byte
			part core.ArticlePart
		}{
			data: tf.data,
			part: core.ArticlePart{Name: tf.name, FileSize: int64(len(tf.data)), Part: 1, Total: 1, Begin: 1, End: int64(len(tf.data))},
		}
	}
	albumNZB := fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`+
			`<file poster="p@test" date="1" subject="%s"><groups><group>alt.binaries.music</group></groups><segments><segment bytes="%d" number="1">%s</segment></segments></file>`+
			`<file poster="p@test" date="1" subject="%s"><groups><group>alt.binaries.music</group></groups><segments><segment bytes="%d" number="1">%s</segment></segments></file>`+
			`</nzb>`,
		html.EscapeString(`[1/2] - "01 - Opening.flac" yEnc (1/1)`), len(track1Data)+200, html.EscapeString("01_-_Opening.flac.1@test"),
		html.EscapeString(`[2/2] - "02 - I'm Yours.flac" yEnc (1/1)`), len(track2Data)+200, html.EscapeString("02_-_I'm_Yours.flac.1@test"),
	)

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
			fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><item><title>Jason Mraz - We Sing, We Dance, We Steal Things (2008)(flac)</title><guid isPermaLink="false">g-mraz-1</guid><link>%s/api?t=get&amp;id=g-mraz-1&amp;apikey=%s</link><enclosure url="%s/api?t=get&amp;id=g-mraz-1&amp;apikey=%s" length="123456" type="application/x-nzb"/></item></channel></rss>`, base, secretKey, base, secretKey)
		case "get":
			if nzbFetches.Add(1) > 1 {
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><error code="409" description="You've already grabbed this NZB within the last hour. Requesting it again is not allowed."/>`)
				return
			}
			fmt.Fprint(w, albumNZB)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	db := store.OpenTest(t)
	v, err := vault.Open(ctx, vault.Options{DB: db, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	mgr := newznab.NewManager(db, []config.Indexer{{
		ID:         "idx-prowlarr-22",
		Name:       "ProwlarrMusic",
		URL:        srv.URL,
		APIKey:     secretKey,
		Enabled:    true,
		DailyGrabs: 20,
	}}, newznab.Options{SearchCacheTTL: -1, Vault: v})

	ss := &StreamSources{inner: mgr}
	vod := &livetv.VODIndexer{Inner: ss, Service: func() *livetv.Service { return nil }}
	wrapped := debrid.NewIndexer(vod, debrid.NewService(config.Debrid{}, debrid.Options{}))

	eng := stream.New(fetcher, nil, stream.Options{}, nil)
	t.Cleanup(func() { _ = eng.Close() })

	kr := NewKindResolver(KindResolverDeps{
		Indexer:       wrapped,
		Engine:        eng,
		UsenetServing: func() bool { return true },
	})

	// 1. Search candidate returns key-less nzbUrl.
	cands, err := kr.ResolveSources(ctx, "ether.music", core.SourceQuery{
		Kind:     "indexer_search",
		Query:    "Jason Mraz",
		Category: "music",
	})
	if err != nil || len(cands) != 1 {
		t.Fatalf("indexer_search = %+v err=%v", cands, err)
	}
	cand := cands[0]

	// 2. release_files grabs the album NZB once and populates the vault.
	listed, err := kr.ResolveSources(ctx, "ether.music", core.SourceQuery{
		Kind:      "release_files",
		Candidate: &cand,
	})
	if err != nil || len(listed) != 1 || len(listed[0].Files) != 2 {
		t.Fatalf("release_files = %+v err=%v", listed, err)
	}
	if nzbFetches.Load() != 1 {
		t.Fatalf("release_files fetches = %d, want 1", nzbFetches.Load())
	}

	// 3. Playback for Track 1 and Track 2 both reuse the vaulted NZB (0 re-grabs, no 409).
	idx0, idx1 := 0, 1
	for _, tc := range []struct {
		name string
		idx  *int
		want []byte
	}{
		{name: "track 1", idx: &idx0, want: track1Data},
		{name: "track 2", idx: &idx1, want: track2Data},
	} {
		ms, err := kr.openReleaseForSource(ctx, core.PlayableSource{
			ID:      tc.name,
			Pattern: kindstream.PatternRelease,
			MIME:    "audio/flac",
			Release: &core.ReleaseHint{
				Candidate: &cand,
				FileIndex: tc.idx,
			},
		}, "audio/*", nil)
		if err != nil {
			t.Fatalf("%s openReleaseForSource: %v", tc.name, err)
		}
		got := make([]byte, ms.Size())
		n, rerr := ms.ReadAt(got, 0)
		_ = ms.Close()
		if (rerr != nil && rerr != io.EOF) || int64(n) != int64(len(tc.want)) || !bytes.Equal(got, tc.want) {
			t.Fatalf("%s read mismatch: n=%d err=%v", tc.name, n, rerr)
		}
	}
	if nzbFetches.Load() != 1 {
		t.Fatalf("multi-track playback triggered re-grab: fetches=%d, want 1", nzbFetches.Load())
	}

	// 4. Legacy keyed NZBURL strips auth from both rel.ID and rel.Target.PackID and still hits vault.
	legacyCand := cand
	legacyCand.NZBURL = srv.URL + "/api?t=get&id=g-mraz-1&apikey=" + secretKey
	legacyRel, err := releaseFromSource(core.PlayableSource{
		ID:      "legacy-track-2",
		Pattern: kindstream.PatternRelease,
		MIME:    "audio/flac",
		Release: &core.ReleaseHint{
			Candidate: &legacyCand,
			FileIndex: &idx1,
		},
	}, "", "audio/*")
	if err != nil {
		t.Fatalf("releaseFromSource(legacy): %v", err)
	}
	stripped := kr.stripReleaseAuth(legacyRel)
	if strings.Contains(stripped.ID, secretKey) || strings.Contains(stripped.ID, "apikey") {
		t.Fatal("stripped.ID retained credentials")
	}
	if stripped.Target == nil || stripped.Target.PackID == "" || strings.Contains(stripped.Target.PackID, secretKey) || strings.Contains(stripped.Target.PackID, "apikey") {
		t.Fatal("stripped.Target.PackID is missing or retained credentials")
	}
	ms, err := kr.openReleaseForSource(ctx, core.PlayableSource{
		ID:      "legacy-track-2",
		Pattern: kindstream.PatternRelease,
		MIME:    "audio/flac",
		Release: &core.ReleaseHint{
			Candidate: &legacyCand,
			FileIndex: &idx1,
		},
	}, "audio/*", nil)
	if err != nil {
		t.Fatalf("legacy openReleaseForSource: %v", err)
	}
	if strings.Contains(ms.ReleaseID(), secretKey) || strings.Contains(ms.ReleaseID(), "apikey") {
		_ = ms.Close()
		t.Fatal("media release ID retained credentials")
	}
	_ = ms.Close()
	if nzbFetches.Load() != 1 {
		t.Fatalf("legacy keyed playback triggered re-grab: fetches=%d, want 1", nzbFetches.Load())
	}
}
