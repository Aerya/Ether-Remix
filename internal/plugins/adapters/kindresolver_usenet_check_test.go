package adapters

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/availability"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/stream"
)

type stubUsenetIndexer struct {
	mu      sync.Mutex
	nzbs    map[string][]byte
	fetchN  int
	fetchFn func(ctx context.Context, rel core.Release) ([]byte, error)
}

func (s *stubUsenetIndexer) Name() string { return "stub-usenet" }

func (s *stubUsenetIndexer) Search(_ context.Context, _ core.SearchRequest) ([]core.Release, error) {
	return nil, nil
}

func (s *stubUsenetIndexer) FetchNZB(ctx context.Context, rel core.Release) ([]byte, error) {
	s.mu.Lock()
	s.fetchN++
	fn := s.fetchFn
	data := s.nzbs[rel.NZBURL]
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, rel)
	}
	if len(data) == 0 {
		return nil, core.ErrNotFound
	}
	return data, nil
}

func (s *stubUsenetIndexer) fetches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetchN
}

type stubUsenetEngine struct {
	mu         sync.Mutex
	checkN     int
	checkBG    []bool
	checkRes   core.UsenetCheckResult
	checkErr   error
	openErr    error
	openSource core.MediaSource
}

func (e *stubUsenetEngine) Open(_ context.Context, id string, data []byte) (core.MediaSource, error) {
	e.mu.Lock()
	err := e.openErr
	src := e.openSource
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if src != nil {
		return src, nil
	}
	return stubMediaSource{id: id, data: data}, nil
}

func (e *stubUsenetEngine) Prefetch(_ context.Context, _ string, _ []byte, _, _ int64) error {
	return nil
}

func (e *stubUsenetEngine) CheckArticles(ctx context.Context, _ string, _ []byte) (core.UsenetCheckResult, error) {
	e.mu.Lock()
	e.checkN++
	e.checkBG = append(e.checkBG, core.IsBackgroundCheck(ctx))
	res := e.checkRes
	err := e.checkErr
	e.mu.Unlock()
	return res, err
}

func (e *stubUsenetEngine) checks() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.checkN
}

type failingReadMediaSource struct {
	id      string
	readErr error
}

func (s *failingReadMediaSource) ReleaseID() string { return s.id }
func (s *failingReadMediaSource) Name() string      { return "track01.flac" }
func (s *failingReadMediaSource) Size() int64       { return 4096 }
func (s *failingReadMediaSource) Close() error      { return nil }
func (s *failingReadMediaSource) ReadAt(_ []byte, _ int64) (int, error) {
	return 0, s.readErr
}
func (s *failingReadMediaSource) ReadAtContext(_ context.Context, _ []byte, _ int64) (int, error) {
	return 0, s.readErr
}

func sampleTestNZB(tag string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <file poster="p" date="1700000000" subject="%s.flac (1/1)">
    <groups><group>alt.binaries.test</group></groups>
    <segments><segment bytes="1024" number="1">%s-1@test</segment></segments>
  </file>
</nzb>`, tag, tag))
}

func TestKindResolverUsenetCheckCacheHashAndTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC)

	nzbBytes := sampleTestNZB("album-a")
	idx := &stubUsenetIndexer{
		nzbs: map[string][]byte{
			"https://idx1.example/a.nzb": nzbBytes,
			"https://idx2.example/a.nzb": nzbBytes, // different URL, identical NZB bytes
		},
	}
	eng := &stubUsenetEngine{
		checkRes: core.UsenetCheckResult{Available: true, Checked: 12, Missing: 0},
	}

	kr := NewKindResolver(KindResolverDeps{
		Indexer: idx,
		Engine:  eng,
		Now:     func() time.Time { return now },
	})

	// 1. First check fetches NZB and runs CheckArticles at background priority.
	got, err := kr.ResolveSources(ctx, "p.music", core.SourceQuery{
		Kind:      "usenet_check",
		Candidate: &core.SourceCandidate{NZBURL: "https://idx1.example/a.nzb", Indexer: "idx1"},
	})
	if err != nil {
		t.Fatalf("first usenet_check: %v", err)
	}
	if len(got) != 1 || got[0].UsenetCheck == nil {
		t.Fatalf("unexpected candidates: %+v", got)
	}
	uc := got[0].UsenetCheck
	if !uc.Available || uc.Checked != 12 || uc.Missing != 0 || uc.Cached || uc.PlaybackFailed || uc.CheckedAt != now.Unix() {
		t.Fatalf("first verdict = %+v", uc)
	}
	if idx.fetches() != 1 || eng.checks() != 1 {
		t.Fatalf("fetches=%d checks=%d, want 1, 1", idx.fetches(), eng.checks())
	}
	if len(eng.checkBG) != 1 || !eng.checkBG[0] {
		t.Fatalf("CheckArticles did not receive background priority context: %v", eng.checkBG)
	}

	// 2. Second check on same URL hits cache before FetchNZB (0 extra fetches, 0 extra NNTP checks).
	got, err = kr.ResolveSources(ctx, "p.music", core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: "https://idx1.example/a.nzb",
	})
	if err != nil {
		t.Fatalf("cached usenet_check: %v", err)
	}
	if !got[0].UsenetCheck.Cached || !got[0].UsenetCheck.Available || got[0].UsenetCheck.Checked != 12 {
		t.Fatalf("cached verdict = %+v", got[0].UsenetCheck)
	}
	if idx.fetches() != 1 || eng.checks() != 1 {
		t.Fatalf("cached hit triggered extra work: fetches=%d checks=%d", idx.fetches(), eng.checks())
	}

	// 3. Second URL carrying identical NZB bytes hits cache by NZB hash (1 FetchNZB, 0 extra NNTP checks).
	got, err = kr.ResolveSources(ctx, "p.music", core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: "https://idx2.example/a.nzb",
	})
	if err != nil {
		t.Fatalf("hash-deduped usenet_check: %v", err)
	}
	if !got[0].UsenetCheck.Cached || !got[0].UsenetCheck.Available {
		t.Fatalf("hash-deduped verdict = %+v", got[0].UsenetCheck)
	}
	if idx.fetches() != 2 || eng.checks() != 1 {
		t.Fatalf("hash dedupe expected fetches=2 checks=1, got %d, %d", idx.fetches(), eng.checks())
	}

	// 4. Subsequent check on second URL is now aliased by releaseKey (0 extra FetchNZB too).
	got, err = kr.ResolveSources(ctx, "p.music", core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: "https://idx2.example/a.nzb",
	})
	if err != nil || !got[0].UsenetCheck.Cached || idx.fetches() != 2 || eng.checks() != 1 {
		t.Fatalf("aliased check: err=%v res=%+v fetches=%d checks=%d", err, got, idx.fetches(), eng.checks())
	}

	// 5. After TTL expires, next check re-runs against the indexer and engine.
	now = now.Add(usenetCheckPositiveTTL + time.Second)
	eng.checkRes = core.UsenetCheckResult{Available: false, Checked: 4, Missing: 1, Reason: "articles_missing"}
	got, err = kr.ResolveSources(ctx, "p.music", core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: "https://idx1.example/a.nzb",
	})
	if err != nil {
		t.Fatalf("post-TTL usenet_check: %v", err)
	}
	if got[0].UsenetCheck.Cached || got[0].UsenetCheck.Available || got[0].UsenetCheck.Missing != 1 {
		t.Fatalf("post-TTL verdict = %+v", got[0].UsenetCheck)
	}
	if idx.fetches() != 3 || eng.checks() != 2 {
		t.Fatalf("post-TTL expected fetches=3 checks=2, got %d, %d", idx.fetches(), eng.checks())
	}

	// 6. Transient errors are never cached as negative verdicts.
	now = now.Add(usenetCheckNegativeTTL + time.Second)
	eng.checkErr = errors.New("provider timeout")
	if _, err := kr.ResolveSources(ctx, "p.music", core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: "https://idx1.example/a.nzb",
	}); err == nil {
		t.Fatal("expected transient error")
	}
	eng.checkErr = nil
	eng.checkRes = core.UsenetCheckResult{Available: true, Checked: 6, Missing: 0}
	got, err = kr.ResolveSources(ctx, "p.music", core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: "https://idx1.example/a.nzb",
	})
	if err != nil || got[0].UsenetCheck.Cached || !got[0].UsenetCheck.Available {
		t.Fatalf("after transient error: err=%v res=%+v", err, got)
	}
}

func TestKindResolverUsenetCheckPlaybackFailureRecording(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 16, 0, 0, 0, time.UTC)

	nzbBytes := sampleTestNZB("album-broken")
	idx := &stubUsenetIndexer{
		nzbs: map[string][]byte{
			"https://idx1.example/broken.nzb":   nzbBytes,
			"https://idx2.example/broken.nzb":   nzbBytes,
			"https://idx1.example/openfail.nzb": sampleTestNZB("openfail"),
		},
	}
	eng := &stubUsenetEngine{
		checkRes: core.UsenetCheckResult{Available: true, Checked: 12, Missing: 0},
	}

	kr := NewKindResolver(KindResolverDeps{
		Indexer: idx,
		Engine:  eng,
		Now:     func() time.Time { return now },
	})

	// 1. Sampled check initially passes for idx1 and dedupes for idx2.
	for _, u := range []string{"https://idx1.example/broken.nzb", "https://idx2.example/broken.nzb"} {
		got, err := kr.ResolveSources(ctx, "p.music", core.SourceQuery{Kind: "usenet_check", NZBURL: u})
		if err != nil || !got[0].UsenetCheck.Available {
			t.Fatalf("initial check %s = %+v, %v", u, got, err)
		}
	}

	// 2. Real playback opens with an explicit fileIndex target (non-video) and
	//    encounters a mid-stream missing article on ReadAtContext.
	eng.openSource = &failingReadMediaSource{
		id:      "rel-broken",
		readErr: fmt.Errorf("segment 42: %w", core.ErrArticleMissing),
	}
	fileIdx := 0
	ms, err := kr.openRelease(ctx, core.ReleaseHint{
		NZBURL:    "https://idx1.example/broken.nzb",
		FileIndex: &fileIdx,
	})
	if err != nil {
		t.Fatalf("openRelease: %v", err)
	}
	cr, ok := ms.(core.ContextReaderAt)
	if !ok {
		t.Fatal("wrapped Usenet MediaSource must preserve core.ContextReaderAt")
	}
	if _, err := cr.ReadAtContext(ctx, make([]byte, 16), 0); !errors.Is(err, core.ErrArticleMissing) {
		t.Fatalf("ReadAtContext err = %v, want ErrArticleMissing", err)
	}
	_ = ms.Close()

	// 3. Subsequent usenet_check on BOTH idx1 and the hash-aliased idx2 URL now
	//    agrees with playback without any extra NNTP check.
	fetchesBefore := idx.fetches()
	checksBefore := eng.checks()
	for _, u := range []string{"https://idx1.example/broken.nzb", "https://idx2.example/broken.nzb"} {
		got, err := kr.ResolveSources(ctx, "p.music", core.SourceQuery{Kind: "usenet_check", NZBURL: u})
		if err != nil {
			t.Fatalf("post-failure usenet_check %s: %v", u, err)
		}
		uc := got[0].UsenetCheck
		if uc.Available || !uc.Cached || !uc.PlaybackFailed || uc.Missing < 1 || uc.Checked < uc.Missing {
			t.Fatalf("post-failure verdict for %s = %+v, want unavailable playbackFailed cached", u, uc)
		}
		if uc.Reason != "playback_article_missing" {
			t.Fatalf("Reason = %q, want playback_article_missing", uc.Reason)
		}
	}
	if idx.fetches() != fetchesBefore || eng.checks() != checksBefore {
		t.Fatalf("post-failure checks hit indexer/engine: fetches %d->%d checks %d->%d",
			fetchesBefore, idx.fetches(), checksBefore, eng.checks())
	}

	// 4. Open-time playback failure on a never-checked NZB is also recorded and exposed.
	eng.openSource = nil
	eng.openErr = fmt.Errorf("archive damaged: %w", stream.ErrCorrupt)
	if _, err := kr.openRelease(ctx, core.ReleaseHint{NZBURL: "https://idx1.example/openfail.nzb"}); !errors.Is(err, stream.ErrCorrupt) {
		t.Fatalf("openRelease err = %v, want ErrCorrupt", err)
	}
	got, err := kr.ResolveSources(ctx, "p.music", core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: "https://idx1.example/openfail.nzb",
	})
	if err != nil {
		t.Fatalf("usenet_check after open failure: %v", err)
	}
	uc := got[0].UsenetCheck
	if uc.Available || !uc.Cached || !uc.PlaybackFailed || uc.Reason != "playback_article_corrupt" {
		t.Fatalf("open-failure verdict = %+v", uc)
	}

	// 5. Transient playback errors (cancel, EOF, provider outage) never taint the cache.
	eng.openErr = nil
	idx.nzbs["https://idx1.example/transient.nzb"] = sampleTestNZB("transient")
	for _, transient := range []error{context.Canceled, io.EOF, stream.ErrClosed, core.ErrSourceUnavailable} {
		eng.openSource = &failingReadMediaSource{id: "rel-transient", readErr: transient}
		src, err := kr.openRelease(ctx, core.ReleaseHint{NZBURL: "https://idx1.example/transient.nzb"})
		if err != nil {
			t.Fatalf("openRelease transient: %v", err)
		}
		_, _ = src.ReadAt(make([]byte, 8), 0)
		_ = src.Close()
	}
	if _, hit := kr.lookupUsenetCheck("https://idx1.example/transient.nzb", "", ""); hit {
		t.Fatal("transient playback errors must not record a playbackFailed verdict")
	}
}

func TestKindResolverUsenetCheckCacheBoundedSize(t *testing.T) {
	now := time.Date(2026, 11, 20, 17, 0, 0, 0, time.UTC)
	kr := NewKindResolver(KindResolverDeps{
		Now: func() time.Time { return now },
	})
	for i := 0; i < maxUsenetCheckCacheEntries+50; i++ {
		now = now.Add(time.Millisecond)
		kr.storeUsenetCheck(
			fmt.Sprintf("https://idx.example/%d.nzb", i),
			fmt.Sprintf("hash-%d", i),
			fmt.Sprintf("nzb:cid-%d", i),
			core.UsenetCheckResult{Available: true, Checked: 12, Missing: 0},
		)
	}
	entries, byRel, byHash, byContent := kr.verdicts().Stats()
	if entries > maxUsenetCheckCacheEntries || byRel > maxUsenetCheckCacheEntries || byHash > maxUsenetCheckCacheEntries || byContent > maxUsenetCheckCacheEntries {
		t.Fatalf("cache exceeded bound %d: entries=%d byRel=%d byHash=%d byContent=%d", maxUsenetCheckCacheEntries, entries, byRel, byHash, byContent)
	}
}

type zeroCallArticleFetcher struct {
	mu    sync.Mutex
	calls int
}

func (f *zeroCallArticleFetcher) Stat(_ context.Context, _ string) (bool, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return true, nil
}

func (f *zeroCallArticleFetcher) Body(_ context.Context, _ string, _ []string, _ core.Priority) ([]byte, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return []byte("unused"), nil
}

func (f *zeroCallArticleFetcher) BodyPart(_ context.Context, _ string, _ []string, _ core.Priority) ([]byte, core.ArticlePart, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return []byte("unused"), core.ArticlePart{}, nil
}

func TestKindResolverUsenetCheckFailsOnMissingRARVolumes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 18, 0, 0, 0, time.UTC)

	missingVolNZB := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <file poster="p" date="1700000000" subject="&quot;Album.part12.rar&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="1024" number="1">p12@test</segment></segments></file>
  <file poster="p" date="1700000000" subject="&quot;Album.part13.rar&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="1024" number="1">p13@test</segment></segments></file>
  <file poster="p" date="1700000000" subject="&quot;Album.part16.rar&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="1024" number="1">p16@test</segment></segments></file>
</nzb>`)

	idx := &stubUsenetIndexer{
		nzbs: map[string][]byte{
			"https://idx.example/missing-vol.nzb": missingVolNZB,
		},
	}
	fetcher := &zeroCallArticleFetcher{}
	eng := stream.New(fetcher, nil, stream.Options{}, nil)
	defer eng.Close()

	kr := NewKindResolver(KindResolverDeps{
		Indexer:       idx,
		Engine:        eng,
		UsenetServing: func() bool { return true },
		Now:           func() time.Time { return now },
	})

	cands, err := kr.ResolveSources(ctx, "p.comics", core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: "https://idx.example/missing-vol.nzb",
	})
	if err != nil {
		t.Fatalf("usenet_check: %v", err)
	}
	if len(cands) != 1 || cands[0].UsenetCheck == nil {
		t.Fatalf("unexpected candidates: %+v", cands)
	}
	uc := cands[0].UsenetCheck
	if uc.Available || uc.Reason != "volumes_missing" || uc.Missing < 2 || uc.Cached {
		t.Fatalf("first usenet_check = %+v, want unavailable volumes_missing", uc)
	}
	fetcher.mu.Lock()
	calls := fetcher.calls
	fetcher.mu.Unlock()
	if calls != 0 {
		t.Fatalf("NNTP calls = %d, want 0", calls)
	}

	// Second check serves the cached negative verdict without re-fetching the NZB.
	cands2, err := kr.ResolveSources(ctx, "p.comics", core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: "https://idx.example/missing-vol.nzb",
	})
	if err != nil {
		t.Fatalf("second usenet_check: %v", err)
	}
	if !cands2[0].UsenetCheck.Cached || cands2[0].UsenetCheck.Available || cands2[0].UsenetCheck.Reason != "volumes_missing" {
		t.Fatalf("cached verdict = %+v, want cached unavailable volumes_missing", cands2[0].UsenetCheck)
	}
	if idx.fetches() != 1 {
		t.Fatalf("indexer fetches = %d, want 1", idx.fetches())
	}
}

type sharedVerdictIndexer struct {
	mu      sync.Mutex
	results []core.Release
	nzbs    map[string][]byte
	fetches int
}

func (s *sharedVerdictIndexer) Name() string { return "shared-usenet" }

func (s *sharedVerdictIndexer) Search(_ context.Context, _ core.SearchRequest) ([]core.Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.Release(nil), s.results...), nil
}

func (s *sharedVerdictIndexer) FetchNZB(_ context.Context, rel core.Release) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetches++
	if data, ok := s.nzbs[rel.NZBURL]; ok {
		return data, nil
	}
	return nil, core.ErrNotFound
}

func (s *sharedVerdictIndexer) fetchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetches
}

type sharedVerdictHealth struct {
	mu      sync.Mutex
	healthy map[string]bool
	calls   int
}

func (h *sharedVerdictHealth) Check(ctx context.Context, nzb []byte) (bool, error) {
	return h.CheckRelease(ctx, "", nzb)
}

func (h *sharedVerdictHealth) CheckRelease(_ context.Context, releaseID string, _ []byte) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	if ok, exists := h.healthy[releaseID]; exists {
		return ok, nil
	}
	return true, nil
}

type noopAvailFeeds struct{}

func (noopAvailFeeds) FeedSources() []core.ReleaseFeed { return nil }
func (noopAvailFeeds) Recent(context.Context, string, int, int) ([]core.Release, error) {
	return nil, nil
}

type noopAvailCatalog struct{}

func (noopAvailCatalog) ReleaseMatches(context.Context, []core.Release) (map[string][]core.Title, error) {
	return nil, nil
}
func (noopAvailCatalog) CachedTitle(context.Context, core.RatingKey) (*core.Title, error) {
	return nil, nil
}
func (noopAvailCatalog) CachedTitles(context.Context, []core.RatingKey) ([]core.Title, error) {
	return nil, nil
}

type noopAvailValidator struct{}

func (noopAvailValidator) FeedCandidate(context.Context, *core.Title, core.Release) (core.Release, string, error) {
	return core.Release{}, "", nil
}
func (noopAvailValidator) VerifyFeedCandidate(context.Context, *core.Title, core.Release) error {
	return nil
}

func (h *sharedVerdictHealth) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// TestBackgroundUsenetCheckSharedVerdictsAndAllowance verifies the full W295
// end-to-end flow across availability.Service, plugins.Manager, KindResolver,
// and resolver.Service:
//  1. A background plugin usenet_check feeds the shared verdict cache (good
//     15 min, bad 30 min) and is reused at Play with 0 NNTP health checks.
//  2. A bad verdict recorded by Ether's own availability verifier
//     (VerifyFeedCandidate) is answered from cache on plugin usenet_check
//     without calling NNTP or spending the plugin's daily allowance.
//  3. Uncached background checks count against the plugin's daily allowance in
//     availability_budget, answer rate_limited with UTC midnight retry time
//     once spent, and never affect on-demand viewer checks.
func TestBackgroundUsenetCheckSharedVerdictsAndAllowance(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	wantReset := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Defaults(t.TempDir())
	cfg.Availability.Verify = false
	cfg.Availability.PluginUsenetChecks = 2

	availSvc, err := availability.New(availability.Deps{
		DB:        db,
		Config:    func() config.Config { return cfg },
		Feeds:     noopAvailFeeds{},
		Catalog:   noopAvailCatalog{},
		Validator: noopAvailValidator{},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	goodURL := "https://idx1.example/good-movie.nzb"
	goodMirrorURL := "https://idx2.example/good-movie-mirror.nzb"
	goodNZB := sampleTestNZB("signal-good")
	// Slightly different XML comment, identical message IDs -> same nzb:<sha256> content ID.
	goodMirrorNZB := append([]byte("<!-- mirror -->\n"), goodNZB...)

	badURL := "https://idx1.example/bad-avail.nzb"
	badMirrorURL := "https://idx2.example/bad-avail-mirror.nzb"
	badNZB := sampleTestNZB("signal-bad")
	badMirrorNZB := append([]byte("<!-- mirror -->\n"), badNZB...)

	extraURL1 := "https://idx1.example/extra-1.nzb"
	viewerURL := "https://idx1.example/viewer-only.nzb"

	movie := &core.Title{
		Key:   core.MovieKey(100),
		Type:  core.TypeMovie,
		Title: "The Signal",
		Year:  2026,
		IDs:   core.IDs{TMDB: 100},
	}
	goodRel := core.Release{
		ID:       "rel-good-mirror",
		Name:     "The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-GRP",
		Info:     releaseparse.Parse("The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-GRP"),
		Size:     4 << 30,
		Protocol: core.ProtocolUsenet,
		Indexer:  "idx2",
		NZBURL:   goodMirrorURL,
		PubDate:  now.Add(-time.Hour),
	}
	badRel := core.Release{
		ID:       "rel-bad-avail",
		Name:     "The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-BAD",
		Info:     releaseparse.Parse("The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-BAD"),
		Size:     4 << 30,
		Protocol: core.ProtocolUsenet,
		Indexer:  "idx1",
		NZBURL:   badURL,
		PubDate:  now.Add(-time.Hour),
	}

	idx := &sharedVerdictIndexer{
		results: []core.Release{goodRel},
		nzbs: map[string][]byte{
			goodURL:       goodNZB,
			goodMirrorURL: goodMirrorNZB,
			badURL:        badNZB,
			badMirrorURL:  badMirrorNZB,
			extraURL1:     sampleTestNZB("extra-1"),
			viewerURL:     sampleTestNZB("viewer-only"),
		},
	}
	health := &sharedVerdictHealth{
		healthy: map[string]bool{
			badRel.ID:  false,
			goodRel.ID: true,
		},
	}
	resSvc := resolver.New(idx, health, db, cfg, resolver.Options{
		Now: func() time.Time { return now },
	})
	defer resSvc.Close()

	eng := &stubUsenetEngine{
		checkRes: core.UsenetCheckResult{Available: true, Checked: 10, Missing: 0},
	}
	kr := NewKindResolver(KindResolverDeps{
		Indexer:  idx,
		Resolver: resSvc,
		Engine:   eng,
		Now:      func() time.Time { return now },
	})

	// Install a plugin declaring permissions.sources: "search+check".
	manJSON := []byte(`{
		"id": "community.example.bgusenet",
		"version": "1.0.0",
		"name": "BG Usenet",
		"description": "Tests background usenet checks.",
		"ether": {
			"protocol": 1,
			"kind": "content",
			"summary": "Tests background usenet checks",
			"runtime": { "kind": "hosted", "entry": "dist/plugin.js" },
			"capabilities": ["kind_resolver"],
			"permissions": { "sources": "search+check" },
			"contributes": {
				"mediaKinds": [{
					"key": "audio",
					"label": "Audio",
					"media": "music",
					"levels": [{ "key": "track", "label": "Track", "playable": true }]
				}]
			}
		}
	}`)

	mgr, err := plugins.New(plugins.Deps{
		DB:  db,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.SetKindBackends(plugins.KindBackends{
		ResolveSources:             kr.ResolveSources,
		ResolveSourcesResult:       kr.ResolveSourcesWithResult,
		PeekUsenetCheck:            kr.PeekUsenetCheck,
		AdmitBackgroundUsenetCheck: availSvc.AdmitPluginUsenetCheck,
	})
	prev, err := mgr.PreviewInstall(ctx, plugins.Source{
		Kind:     "studio",
		Manifest: manJSON,
		Files:    map[string][]byte{"dist/plugin.js": []byte("// bg")},
	})
	if err != nil {
		t.Fatalf("preview install: %v", err)
	}
	rec, err := mgr.Install(ctx, prev, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	h := mgr.HostFor(rec.ID)

	// 1. Background usenet_check checks goodURL (spends 1 of 2 daily allowance).
	cands, err := h.ResolveSources(plugins.WithBackgroundCall(ctx), core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: goodURL,
	})
	if err != nil {
		t.Fatalf("bg check 1: %v", err)
	}
	if len(cands) != 1 || cands[0].UsenetCheck == nil || !cands[0].UsenetCheck.Available || cands[0].UsenetCheck.Cached {
		t.Fatalf("bg check 1 verdict = %+v", cands)
	}
	if eng.checks() != 1 {
		t.Fatalf("eng.checks = %d, want 1", eng.checks())
	}

	// 2. Play (resolver.Service.Resolve) on a cross-indexer mirror with the same
	// NZB message IDs reuses the plugin's background GOOD verdict (0 NNTP health checks!).
	resolved, err := resSvc.Resolve(ctx, core.ResolveRequest{Title: movie})
	if err != nil || resolved.Release.ID != goodRel.ID {
		t.Fatalf("Play Resolve = (%+v, %v), want %s", resolved, err, goodRel.ID)
	}
	if health.count() != 0 {
		t.Fatalf("Play ran %d NNTP health checks, want 0 (should reuse background verdict)", health.count())
	}

	// 3. Ether's own availability verifier (VerifyFeedCandidate) marks badRel as BAD.
	if err := resSvc.VerifyFeedCandidate(ctx, movie, badRel); !errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatalf("VerifyFeedCandidate badRel err = %v, want ErrSourceUnavailable", err)
	}
	if health.count() != 1 {
		t.Fatalf("VerifyFeedCandidate health checks = %d, want 1", health.count())
	}

	// 4. Background plugin usenet_check on badURL answers from the shared cache
	// without fetching, calling NNTP, or spending the plugin's daily allowance.
	fetchesBefore := idx.fetchCount()
	cands, err = h.ResolveSources(plugins.WithBackgroundCall(ctx), core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: badURL,
	})
	if err != nil {
		t.Fatalf("bg check badURL from cache: %v", err)
	}
	if len(cands) != 1 || cands[0].UsenetCheck == nil || cands[0].UsenetCheck.Available || !cands[0].UsenetCheck.Cached {
		t.Fatalf("bg check badURL verdict = %+v, want cached unavailable", cands)
	}
	if idx.fetchCount() != fetchesBefore || eng.checks() != 1 {
		t.Fatalf("cached badURL triggered work: fetches %d->%d, eng.checks=%d", fetchesBefore, idx.fetchCount(), eng.checks())
	}

	// 5. Cross-indexer mirror of badRel (different URL, same message-ID contentID)
	// spends the 2nd daily slot for FetchNZB, then hits the shared bad verdict
	// by contentID with 0 NNTP article checks!
	cands, err = h.ResolveSources(plugins.WithBackgroundCall(ctx), core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: badMirrorURL,
	})
	if err != nil {
		t.Fatalf("bg check badMirrorURL: %v", err)
	}
	if len(cands) != 1 || cands[0].UsenetCheck == nil || cands[0].UsenetCheck.Available || !cands[0].UsenetCheck.Cached {
		t.Fatalf("bg check badMirrorURL verdict = %+v, want cached unavailable", cands)
	}
	if eng.checks() != 1 {
		t.Fatalf("badMirrorURL should reuse shared bad verdict without NNTP check; eng.checks=%d", eng.checks())
	}

	// 6. Daily allowance (2/2) is now exhausted -> 3rd uncached background check
	// returns RateLimitError (ErrRateLimited) with UTC midnight retry time.
	_, err = h.ResolveSources(plugins.WithBackgroundCall(ctx), core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: extraURL1,
	})
	if !errors.Is(err, plugins.ErrRateLimited) {
		t.Fatalf("exhausted bg check err = %v, want ErrRateLimited", err)
	}
	var rl *plugins.RateLimitError
	if !errors.As(err, &rl) || !rl.RetryAt.Equal(wantReset) {
		t.Fatalf("exhausted RateLimitError = %#v (err=%v), want retryAt=%v", rl, err, wantReset)
	}

	// 7. On-demand (viewer) usenet_check STILL succeeds and does not count
	// against the plugin's daily background allowance.
	cands, err = h.ResolveSources(ctx, core.SourceQuery{
		Kind:   "usenet_check",
		NZBURL: viewerURL,
	})
	if err != nil || len(cands) != 1 || cands[0].UsenetCheck == nil || !cands[0].UsenetCheck.Available {
		t.Fatalf("on-demand viewer check after bg exhausted = (%+v, %v)", cands, err)
	}
	if eng.checks() != 2 {
		t.Fatalf("viewer check eng.checks = %d, want 2", eng.checks())
	}

	// 8. Verify availability_budget recorded exactly 2 used for this plugin.
	st, err := availSvc.Status(ctx)
	if err != nil {
		t.Fatalf("availability.Status: %v", err)
	}
	if got := st.Usage[availability.PluginUsenetCheckBucket(rec.ID)]; got != 2 {
		t.Fatalf("availability_budget usage = %d, want 2 (status usage=%+v)", got, st.Usage)
	}

	// 9. Verify TTLs: after 16 min positive verdict expires while negative (30m)
	// stays cached; after 31 min negative expires too.
	now = now.Add(16 * time.Minute)
	if _, ok := kr.PeekUsenetCheck(core.SourceQuery{Kind: "usenet_check", NZBURL: goodURL}); ok {
		t.Fatal("positive verdict should expire after 15 min")
	}
	if cand, ok := kr.PeekUsenetCheck(core.SourceQuery{Kind: "usenet_check", NZBURL: badURL}); !ok || cand.UsenetCheck.Available {
		t.Fatalf("negative verdict should remain cached at 16 min: ok=%v cand=%+v", ok, cand)
	}
	now = now.Add(15 * time.Minute)
	if _, ok := kr.PeekUsenetCheck(core.SourceQuery{Kind: "usenet_check", NZBURL: badURL}); ok {
		t.Fatal("negative verdict should expire after 30 min")
	}
}

type richUsenetMediaSource struct {
	id             string
	delivery       core.SourceDelivery
	damageAttached bool
	repairAttached bool
	analysisCalls  int
	duration       time.Duration
}

func (s *richUsenetMediaSource) ReadAt(p []byte, _ int64) (int, error) { return len(p), nil }
func (s *richUsenetMediaSource) ReadAtAnalysisContext(_ context.Context, p []byte, _ int64) (int, error) {
	s.analysisCalls++
	return len(p), nil
}
func (s *richUsenetMediaSource) Size() int64       { return 1024 }
func (s *richUsenetMediaSource) Name() string      { return "zimmer.mkv" }
func (s *richUsenetMediaSource) ReleaseID() string { return s.id }
func (s *richUsenetMediaSource) Close() error      { return nil }
func (s *richUsenetMediaSource) SourceDelivery() core.SourceDelivery {
	return s.delivery
}
func (s *richUsenetMediaSource) SetDamageHandler(func(int64, int64) bool) {
	s.damageAttached = true
}
func (s *richUsenetMediaSource) SetRepairHandler(func(core.RepairEvent) core.RepairWindow) {
	s.repairAttached = true
}
func (s *richUsenetMediaSource) SetPlaybackDuration(d time.Duration) { s.duration = d }
func (s *richUsenetMediaSource) BufferedAhead() int64                { return 8 << 20 }
func (s *richUsenetMediaSource) Origin() string                      { return "usenet" }
func (s *richUsenetMediaSource) MIME() string                        { return "video/x-matroska" }

type archiveUsenetMediaSource struct {
	*richUsenetMediaSource
	vols []core.MediaSource
}

func (a *archiveUsenetMediaSource) ArchiveVolumes() ([]core.MediaSource, string, string) {
	return a.vols, "rar", "concert.mkv"
}

func TestWrapUsenetPlaybackSourcePreservesOptionalInterfaces(t *testing.T) {
	kr := NewKindResolver(KindResolverDeps{})
	defer kr.Close()

	inner := &richUsenetMediaSource{
		id:       "nzb-zimmer",
		delivery: core.SourceDelivery{Bytes: 4 << 20, Fetches: 12, Parallel: 6},
	}
	wrapped := kr.wrapUsenetPlaybackSource(inner, "rel-zimmer", "hash-zimmer", "cid-zimmer")

	rep, ok := wrapped.(core.SourceDeliveryReporter)
	if !ok {
		t.Fatalf("wrapUsenetPlaybackSource dropped core.SourceDeliveryReporter")
	}
	if got := rep.SourceDelivery(); got.Bytes != 4<<20 || got.Parallel != 6 {
		t.Fatalf("SourceDelivery = %+v, want Bytes=4MiB Parallel=6", got)
	}
	wrapped.(core.DamageReporter).SetDamageHandler(func(int64, int64) bool { return true })
	wrapped.(core.RepairReporter).SetRepairHandler(func(core.RepairEvent) core.RepairWindow { return core.RepairWindow{} })
	if !inner.damageAttached || !inner.repairAttached {
		t.Fatalf("damage=%v repair=%v, want both attached", inner.damageAttached, inner.repairAttached)
	}
	if _, err := wrapped.(core.AnalysisReaderAt).ReadAtAnalysisContext(context.Background(), make([]byte, 16), 0); err != nil {
		t.Fatalf("ReadAtAnalysisContext: %v", err)
	}
	if inner.analysisCalls != 1 {
		t.Fatalf("analysisCalls = %d, want 1", inner.analysisCalls)
	}
	if got := wrapped.(core.OriginSource).Origin(); got != "usenet" {
		t.Fatalf("Origin = %q, want usenet", got)
	}
	if got := wrapped.(core.MIMESource).MIME(); got != "video/x-matroska" {
		t.Fatalf("MIME = %q, want video/x-matroska", got)
	}

	// Archive volumes must also preserve SourceDeliveryReporter on child volumes.
	child := &richUsenetMediaSource{id: "vol-1", delivery: core.SourceDelivery{Bytes: 1 << 20, Parallel: 2}}
	archWrapped := kr.wrapUsenetPlaybackSource(&archiveUsenetMediaSource{
		richUsenetMediaSource: inner,
		vols:                  []core.MediaSource{child},
	}, "rel-rar", "hash-rar", "cid-rar")
	vols, format, member := archWrapped.(core.ArchiveVolumeSource).ArchiveVolumes()
	if format != "rar" || member != "concert.mkv" || len(vols) != 1 {
		t.Fatalf("ArchiveVolumes = (%d, %q, %q)", len(vols), format, member)
	}
	if vrep, ok := vols[0].(core.SourceDeliveryReporter); !ok || vrep.SourceDelivery().Parallel != 2 {
		t.Fatalf("archive volume lost SourceDeliveryReporter: ok=%v", ok)
	}

	// Plain MediaSource without SourceDeliveryReporter must not falsely claim it.
	plain := &failingReadMediaSource{id: "plain"}
	plainWrapped := kr.wrapUsenetPlaybackSource(plain, "rel-plain", "hash-plain", "cid-plain")
	if _, ok := plainWrapped.(core.SourceDeliveryReporter); ok {
		t.Fatal("wrapUsenetPlaybackSource falsely advertised core.SourceDeliveryReporter")
	}
}
