package adapters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
)

type stubSearchIndexer struct {
	lastReq core.SearchRequest
	rels    []core.Release
}

func (s *stubSearchIndexer) Name() string { return "stub" }

func (s *stubSearchIndexer) Search(_ context.Context, req core.SearchRequest) ([]core.Release, error) {
	s.lastReq = req
	return append([]core.Release(nil), s.rels...), nil
}

func (s *stubSearchIndexer) FetchNZB(_ context.Context, _ core.Release) ([]byte, error) {
	return nil, core.ErrNotFound
}

func TestKindResolverIndexerSearchRecentCapAndScoring(t *testing.T) {
	ctx := context.Background()

	defCfg := config.Defaults("")
	defCfg.Quality = config.Quality{
		Default: "Main",
		Profiles: []core.QualityProfile{
			{
				Name:             "Main",
				MaxHeight:        2160,
				MinHeight:        720,
				AllowHDR:         true,
				AllowDolbyVision: true,
				QualityOrder:     config.ResolutionGroups(2160, 720),
			},
			{
				Name:         "HDOnly",
				MaxHeight:    1080,
				MinHeight:    720,
				QualityOrder: config.ResolutionGroups(1080, 720),
			},
		},
	}

	base := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	rels := make([]core.Release, 0, 135)
	for i := 0; i < 135; i++ {
		title := fmt.Sprintf("Show.S01E%03d.1080p.WEB-DL.H264-GRP", i+1)
		if i == 134 {
			// Newest item is non-video to verify mixed feed handling.
			title = "Magazine Issue 135 (2026) [EPUB]"
		} else if i == 133 {
			title = "Show.S01E134.2160p.WEB-DL.HEVC-GRP"
		}
		rels = append(rels, core.Release{
			ID:        fmt.Sprintf("rel-%d", i),
			Name:      title,
			Protocol:  core.ProtocolUsenet,
			NZBURL:    fmt.Sprintf("https://idx.example/nzb/%d", i),
			Indexer:   "NZBIndex",
			IndexerID: "nzbidx",
			PubDate:   base.Add(time.Duration(i) * time.Minute),
		})
	}

	idx := &stubSearchIndexer{rels: rels}
	resSvc := resolver.New(idx, nil, nil, defCfg, resolver.Options{})
	kr := NewKindResolver(KindResolverDeps{
		Indexer:  idx,
		Resolver: resSvc,
	})

	// 1. Empty query without category is refused with ValidationError.
	var ve *plugins.ValidationError
	if _, err := kr.ResolveSources(ctx, "plugin.example", core.SourceQuery{
		Kind: "indexer_search",
	}); !errors.As(err, &ve) {
		t.Fatalf("empty query + empty category err = %v, want *plugins.ValidationError", err)
	}

	// 2. Empty query with category succeeds, sorts newest-first, and caps at 100.
	cands, err := kr.ResolveSources(ctx, "plugin.example", core.SourceQuery{
		Kind:     "indexer_search",
		Category: "7000,5000",
	})
	if err != nil {
		t.Fatalf("empty query + category err: %v", err)
	}
	if idx.lastReq.Title != "" || idx.lastReq.Category != "7000,5000" {
		t.Fatalf("forwarded SearchRequest = %+v, want empty Title and Category=7000,5000", idx.lastReq)
	}
	if len(cands) != 100 {
		t.Fatalf("len(cands) = %d, want 100", len(cands))
	}
	// Item 134 (newest) is first and non-video; item 133 (2160p) is second and scored; item 35 is 100th.
	if cands[0].Title != "Magazine Issue 135 (2026) [EPUB]" || cands[0].Score != nil || cands[0].Resolution != 0 {
		t.Fatalf("newest non-video candidate = %+v, want unscored EPUB", cands[0])
	}
	if cands[1].Resolution != 2160 || cands[1].Source != "webdl" || cands[1].Codec != "hevc" || cands[1].Score == nil {
		t.Fatalf("second candidate = %+v, want scored 2160p webdl hevc", cands[1])
	}
	if cands[2].Resolution != 1080 || cands[2].Score == nil || *cands[1].Score <= *cands[2].Score {
		t.Fatalf("Main profile should score 2160p (%v) > 1080p (%v)", cands[1].Score, cands[2].Score)
	}
	if cands[99].Title != "Show.S01E036.1080p.WEB-DL.H264-GRP" {
		t.Fatalf("100th newest candidate = %q, want Show.S01E036...", cands[99].Title)
	}

	// 3. Named profile ("HDOnly") flips ranking so 1080p outranks 2160p.
	hdCands, err := kr.ResolveSources(ctx, "plugin.example", core.SourceQuery{
		Kind:     "indexer_search",
		Category: "5000",
		Profile:  "HDOnly",
	})
	if err != nil {
		t.Fatalf("HDOnly search: %v", err)
	}
	if *hdCands[2].Score <= *hdCands[1].Score {
		t.Fatalf("HDOnly profile should score 1080p (%d) > 2160p (%d)", *hdCands[2].Score, *hdCands[1].Score)
	}

	// 4. Unknown profile is refused with ValidationError on field "profile".
	ve = nil
	if _, err := kr.ResolveSources(ctx, "plugin.example", core.SourceQuery{
		Kind:     "indexer_search",
		Category: "5000",
		Profile:  "DoesNotExist",
	}); !errors.As(err, &ve) || len(ve.Fields) == 0 || ve.Fields[0].Field != "profile" {
		t.Fatalf("unknown profile err = %v (fields=%+v), want ValidationError on profile", err, ve)
	}
}

func TestKindResolverIndexerSearchOptionsAndStatus(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const capsXML = `<?xml version="1.0" encoding="UTF-8"?><caps><searching><search available="yes" supportedParams="q"/></searching></caps>`
	fastSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			fmt.Fprint(w, capsXML)
			return
		}
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><item><title>Fast.Track.FLAC</title><guid>f1</guid><link>http://fast.example/1.nzb</link><enclosure url="http://fast.example/1.nzb" length="1024" type="application/x-nzb"/></item></channel></rss>`)
	}))
	defer fastSrv.Close()

	releaseSlow := make(chan struct{})
	slowDone := make(chan struct{})
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			fmt.Fprint(w, capsXML)
			return
		}
		select {
		case <-releaseSlow:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><item><title>Slow.Track.FLAC</title><guid>s1</guid><link>http://slow.example/1.nzb</link><enclosure url="http://slow.example/1.nzb" length="2048" type="application/x-nzb"/></item></channel></rss>`)
		close(slowDone)
	}))
	defer slowSrv.Close()

	mgr := newznab.NewManager(db, []config.Indexer{
		{ID: "fast-idx", Name: "Fast", URL: fastSrv.URL, APIKey: "k1", Enabled: true},
		{ID: "slow-idx", Name: "Slow", URL: slowSrv.URL, APIKey: "k2", Enabled: true},
	}, newznab.Options{})
	defer mgr.Close()

	kr := NewKindResolver(KindResolverDeps{
		Indexer:  mgr,
		Resolver: resolver.New(mgr, nil, nil, config.Defaults(""), resolver.Options{}),
	})

	// Validation errors for negative numeric options and oversized filter lists.
	neg := -1
	for _, tc := range []struct {
		name  string
		q     core.SourceQuery
		field string
	}{
		{"negative settleMs", core.SourceQuery{Kind: "indexer_search", Query: "x", SettleMS: &neg}, "settleMs"},
		{"negative minResults", core.SourceQuery{Kind: "indexer_search", Query: "x", MinResults: -1}, "minResults"},
		{"negative timeoutMs", core.SourceQuery{Kind: "indexer_search", Query: "x", TimeoutMS: -1}, "timeoutMs"},
		{"too many skipIndexers", core.SourceQuery{Kind: "indexer_search", Query: "x", SkipIndexers: make([]string, plugins.MaxSourceIndexerFilters+1)}, "skipIndexers"},
	} {
		var ve *plugins.ValidationError
		if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.example", tc.q); !errors.As(err, &ve) || len(ve.Fields) == 0 || ve.Fields[0].Field != tc.field {
			t.Fatalf("%s: err = %v, want ValidationError on %q", tc.name, err, tc.field)
		}
	}

	// FirstResults with settleMs=0 returns fast candidate immediately while slow stays in flight.
	zero := 0
	res, err := kr.ResolveSourcesWithResult(ctx, "plugin.example", core.SourceQuery{
		Kind:         "indexer_search",
		Query:        "Track",
		FirstResults: true,
		SettleMS:     &zero,
	})
	if err != nil {
		t.Fatalf("ResolveSourcesWithResult(firstResults): %v", err)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Title != "Fast.Track.FLAC" {
		t.Fatalf("candidates = %+v, want only Fast.Track.FLAC", res.Candidates)
	}
	byID := map[string]core.IndexerSearchStatus{}
	for _, st := range res.Indexers {
		byID[st.IndexerID] = st
	}
	if byID["fast-idx"].Status != core.IndexerStatusAnswered || byID["slow-idx"].Status != core.IndexerStatusPending {
		t.Fatalf("statuses = %+v, want fast=answered slow=pending", res.Indexers)
	}
	if len(res.Pending) != 1 || res.Pending[0].IndexerID != "slow-idx" || res.Pending[0].Indexer != "Slow" {
		t.Fatalf("pending = %+v, want [{slow-idx Slow}]", res.Pending)
	}

	// Unblock slow and verify OnlyIndexers reads its result and skips fast.
	close(releaseSlow)
	<-slowDone
	res2, err := kr.ResolveSourcesWithResult(ctx, "plugin.example", core.SourceQuery{
		Kind:         "indexer_search",
		Query:        "Track",
		OnlyIndexers: []string{"Slow"},
	})
	if err != nil {
		t.Fatalf("ResolveSourcesWithResult(onlyIndexers): %v", err)
	}
	if len(res2.Candidates) != 1 || res2.Candidates[0].Title != "Slow.Track.FLAC" {
		t.Fatalf("onlyIndexers candidates = %+v, want Slow.Track.FLAC", res2.Candidates)
	}
	byID2 := map[string]core.IndexerSearchStatus{}
	for _, st := range res2.Indexers {
		byID2[st.IndexerID] = st
	}
	if byID2["fast-idx"].Status != core.IndexerStatusSkipped || byID2["slow-idx"].Status != core.IndexerStatusAnswered {
		t.Fatalf("statuses2 = %+v, want fast=skipped slow=answered", res2.Indexers)
	}

	// Non-indexer_search omits indexers.
	dc, err := kr.ResolveSourcesWithResult(ctx, "plugin.example", core.SourceQuery{
		Kind:     "debrid_check",
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
	})
	if err != nil {
		t.Fatalf("debrid_check: %v", err)
	}
	if len(dc.Indexers) != 0 {
		t.Fatalf("debrid_check indexers = %+v, want omitted", dc.Indexers)
	}
}

// fakeStreamingIndexer simulates two indexers with channel gates so streaming
// batching, cancellation, isolation, and expiry tests never sleep.
type fakeStreamingIndexer struct {
	mu      sync.Mutex
	entries []core.IndexerSearchStatus
	run     func(ctx context.Context, req core.SearchRequest, arrived func([]core.Release)) ([]core.Release, error)
}

func (f *fakeStreamingIndexer) Name() string { return "fake-stream" }

func (f *fakeStreamingIndexer) Search(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
	return f.SearchWith(ctx, req, nil)
}

func (f *fakeStreamingIndexer) SearchWith(ctx context.Context, req core.SearchRequest, arrived func([]core.Release)) ([]core.Release, error) {
	return f.run(ctx, req, arrived)
}

func (f *fakeStreamingIndexer) FetchNZB(context.Context, core.Release) ([]byte, error) {
	return nil, core.ErrNotFound
}

func (f *fakeStreamingIndexer) EnabledIndexerStatuses(status string) []core.IndexerSearchStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]core.IndexerSearchStatus, len(f.entries))
	for i, e := range f.entries {
		out[i] = core.IndexerSearchStatus{
			IndexerID: e.IndexerID,
			Indexer:   e.Indexer,
			Status:    status,
		}
	}
	return out
}

func (f *fakeStreamingIndexer) EnabledIndexers() []core.IndexerSearchStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.IndexerSearchStatus(nil), f.entries...)
}

func TestKindResolverStreamingArrivalBatchingAndCancel(t *testing.T) {
	ctx := context.Background()

	fastDone := make(chan struct{})
	releaseSlow := make(chan struct{})
	slowStarted := make(chan struct{})
	slowCanceled := make(chan struct{})

	var callNum int
	var callMu sync.Mutex
	idx := &fakeStreamingIndexer{
		entries: []core.IndexerSearchStatus{
			{IndexerID: "fast", Indexer: "Fast", Kind: "newznab"},
			{IndexerID: "slow", Indexer: "Slow", Kind: "builtin"},
			{IndexerID: "skip", Indexer: "SkipMe", Kind: "torznab"},
		},
		run: func(sctx context.Context, _ core.SearchRequest, arrived func([]core.Release)) ([]core.Release, error) {
			callMu.Lock()
			callNum++
			n := callNum
			callMu.Unlock()

			if !core.IndexerSkipped(sctx, "skip") {
				t.Errorf("expected skip indexer to be marked skipped via WithIndexerSkip")
			}
			core.ObserveIndexerStatus(sctx, core.IndexerSearchStatus{
				IndexerID: "skip", Indexer: "SkipMe", Status: core.IndexerStatusSkipped, MS: 0,
			})

			if n == 1 {
				// First streaming search: fast arrives immediately, slow waits for releaseSlow.
				fastRel := core.Release{
					ID: "r-fast", Name: "Fast.Album.FLAC", Protocol: core.ProtocolUsenet,
					NZBURL: "https://fast.example/1.nzb", Indexer: "Fast", IndexerID: "fast",
				}
				arrived([]core.Release{fastRel})
				core.ObserveIndexerStatus(sctx, core.IndexerSearchStatus{
					IndexerID: "fast", Indexer: "Fast", Status: core.IndexerStatusAnswered, Results: 1, MS: 12,
				})
				close(fastDone)

				select {
				case <-releaseSlow:
				case <-sctx.Done():
					return nil, sctx.Err()
				}
				// Slow returns one duplicate of fast (must be deduplicated) and one new item.
				slowRel := core.Release{
					ID: "r-slow", Name: "Slow.Album.FLAC", Protocol: core.ProtocolUsenet,
					NZBURL: "https://slow.example/2.nzb", Indexer: "Slow", IndexerID: "slow",
				}
				arrived([]core.Release{fastRel, slowRel})
				core.ObserveIndexerStatus(sctx, core.IndexerSearchStatus{
					IndexerID: "slow", Indexer: "Slow", Status: core.IndexerStatusAnswered, Results: 2, MS: 45,
				})
				return []core.Release{fastRel, slowRel}, nil
			}

			// Second streaming search: blocks until cancelled.
			close(slowStarted)
			<-sctx.Done()
			close(slowCanceled)
			return nil, sctx.Err()
		},
	}

	kr := NewKindResolver(KindResolverDeps{
		Indexer:  idx,
		Resolver: resolver.New(idx, nil, nil, config.Defaults(""), resolver.Options{}),
	})
	defer kr.Close()

	// Start streaming search; returns immediately with searchId and initial pending list.
	start, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{
		Kind:         "indexer_search",
		Query:        "Album",
		Stream:       true,
		SkipIndexers: []string{"SkipMe"},
	})
	if err != nil {
		t.Fatalf("start streaming search: %v", err)
	}
	if start.SearchID == "" || start.Done == nil || *start.Done {
		t.Fatalf("start = %+v, want non-empty searchId and done=false", start)
	}
	if len(start.Candidates) != 0 || len(start.Pending) != 2 {
		t.Fatalf("start candidates=%d pending=%+v, want 0 candidates and 2 pending", len(start.Candidates), start.Pending)
	}

	// Wait until fast indexer finished, then poll #1: gets only fast, slow still pending.
	<-fastDone
	poll1, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{
		Kind:     "indexer_search_results",
		SearchID: start.SearchID,
		WaitMS:   5000,
	})
	if err != nil {
		t.Fatalf("poll1: %v", err)
	}
	if poll1.Done == nil || *poll1.Done {
		t.Fatalf("poll1 done = %v, want false", poll1.Done)
	}
	if len(poll1.Candidates) != 1 || poll1.Candidates[0].Title != "Fast.Album.FLAC" {
		t.Fatalf("poll1 candidates = %+v, want only Fast.Album.FLAC", poll1.Candidates)
	}
	if len(poll1.Pending) != 1 || poll1.Pending[0].IndexerID != "slow" {
		t.Fatalf("poll1 pending = %+v, want [slow]", poll1.Pending)
	}

	// Launch poll #2 with waitMs before releasing slow so waitMs blocks on arrival and wakes up cleanly.
	type pollOut struct {
		res core.SourceResolveResult
		err error
	}
	poll2Ch := make(chan pollOut, 1)
	go func() {
		r2, e2 := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{
			Kind:     "indexer_search_results",
			SearchID: start.SearchID,
			WaitMS:   5000,
		})
		poll2Ch <- pollOut{r2, e2}
	}()

	close(releaseSlow)
	p2 := <-poll2Ch
	if p2.err != nil {
		t.Fatalf("poll2: %v", p2.err)
	}
	if len(p2.res.Candidates) != 1 || p2.res.Candidates[0].Title != "Slow.Album.FLAC" {
		t.Fatalf("poll2 candidates = %+v, want only deduplicated Slow.Album.FLAC", p2.res.Candidates)
	}
	// If poll2 woke on arrived() right before goroutine marked done, one final poll observes done=true with 0 new candidates.
	finalRes := p2.res
	if finalRes.Done == nil || !*finalRes.Done {
		finalRes, err = kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{
			Kind:     "indexer_search_results",
			SearchID: start.SearchID,
			WaitMS:   5000,
		})
		if err != nil {
			t.Fatalf("poll3(done): %v", err)
		}
		if len(finalRes.Candidates) != 0 {
			t.Fatalf("poll3 candidates = %+v, want 0", finalRes.Candidates)
		}
	}
	if finalRes.Done == nil || !*finalRes.Done || len(finalRes.Pending) != 0 {
		t.Fatalf("final streaming result = %+v, want done=true pending=[]", finalRes)
	}
	// Session is cleaned up once done=true is consumed.
	if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{
		Kind:     "indexer_search_results",
		SearchID: start.SearchID,
	}); !errors.Is(err, plugins.ErrNotFound) {
		t.Fatalf("poll after done err = %v, want ErrNotFound", err)
	}

	// Cancel stops unfinished indexers immediately and cleans up the session.
	start2, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{
		Kind:         "indexer_search",
		Query:        "CancelMe",
		Stream:       true,
		SkipIndexers: []string{"SkipMe"},
	})
	if err != nil {
		t.Fatalf("start2: %v", err)
	}
	<-slowStarted
	cancelRes, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{
		Kind:     "indexer_search_cancel",
		SearchID: start2.SearchID,
	})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelRes.Done == nil || !*cancelRes.Done || len(cancelRes.Pending) != 0 {
		t.Fatalf("cancelRes = %+v, want done=true pending=[]", cancelRes)
	}
	<-slowCanceled
	if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{
		Kind:     "indexer_search_results",
		SearchID: start2.SearchID,
	}); !errors.Is(err, plugins.ErrNotFound) {
		t.Fatalf("poll after cancel err = %v, want ErrNotFound", err)
	}
}

func TestKindResolverStreamingIsolationExpiryAndCap(t *testing.T) {
	ctx := context.Background()

	var clockMu sync.Mutex
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	getNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		now = now.Add(d)
		clockMu.Unlock()
	}

	var cancelMu sync.Mutex
	cancelChs := map[string]chan struct{}{}
	registerCancel := func(title string) chan struct{} {
		cancelMu.Lock()
		defer cancelMu.Unlock()
		ch := make(chan struct{})
		cancelChs[title] = ch
		return ch
	}

	idx := &fakeStreamingIndexer{
		entries: []core.IndexerSearchStatus{{IndexerID: "idx-1", Indexer: "One", Kind: "newznab"}},
		run: func(sctx context.Context, req core.SearchRequest, _ func([]core.Release)) ([]core.Release, error) {
			<-sctx.Done()
			cancelMu.Lock()
			ch := cancelChs[req.Title]
			cancelMu.Unlock()
			if ch != nil {
				close(ch)
			}
			return nil, sctx.Err()
		},
	}

	kr := NewKindResolver(KindResolverDeps{
		Indexer:  idx,
		Resolver: resolver.New(idx, nil, nil, config.Defaults(""), resolver.Options{}),
		Now:      getNow,
	})
	kr.streamMaxPerPlugin = 2
	kr.streamFirstPollTTL = 10 * time.Second
	kr.streamIdleTTL = 2 * time.Minute
	defer kr.Close()

	// 1. Cross-plugin isolation: plugin.b cannot poll or cancel plugin.a's searchId.
	c1 := registerCancel("iso")
	s1, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{Kind: "indexer_search", Query: "iso", Stream: true})
	if err != nil {
		t.Fatalf("start s1: %v", err)
	}
	if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.b", core.SourceQuery{Kind: "indexer_search_results", SearchID: s1.SearchID}); !errors.Is(err, plugins.ErrNotFound) {
		t.Fatalf("cross-plugin poll err = %v, want ErrNotFound", err)
	}
	if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.b", core.SourceQuery{Kind: "indexer_search_cancel", SearchID: s1.SearchID}); !errors.Is(err, plugins.ErrNotFound) {
		t.Fatalf("cross-plugin cancel err = %v, want ErrNotFound", err)
	}
	// plugin.a still owns it.
	if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{Kind: "indexer_search_results", SearchID: s1.SearchID}); err != nil {
		t.Fatalf("owner poll s1 err = %v", err)
	}

	// 2. Per-plugin live cap: plugin.a cap is 2; 3rd fails with ErrRateLimited, plugin.b still allowed.
	c2 := registerCancel("unpolled")
	s2, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{Kind: "indexer_search", Query: "unpolled", Stream: true})
	if err != nil {
		t.Fatalf("start s2: %v", err)
	}
	if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{Kind: "indexer_search", Query: "overcap", Stream: true}); !errors.Is(err, plugins.ErrRateLimited) {
		t.Fatalf("over-cap start err = %v, want ErrRateLimited", err)
	}
	cB := registerCancel("other-plugin")
	sB, err := kr.ResolveSourcesWithResult(ctx, "plugin.b", core.SourceQuery{Kind: "indexer_search", Query: "other-plugin", Stream: true})
	if err != nil {
		t.Fatalf("plugin.b start under plugin.a cap: %v", err)
	}

	// 3. Unpolled expiry: advancing past firstPollTTL drops s2 (unpolled) and cancels its indexer,
	// while s1 (already polled) stays alive until idleTTL.
	advance(15 * time.Second)
	if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{Kind: "indexer_search_results", SearchID: s2.SearchID}); !errors.Is(err, plugins.ErrNotFound) {
		t.Fatalf("unpolled s2 after firstPollTTL err = %v, want ErrNotFound", err)
	}
	<-c2
	<-cB
	if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.b", core.SourceQuery{Kind: "indexer_search_results", SearchID: sB.SearchID}); !errors.Is(err, plugins.ErrNotFound) {
		t.Fatalf("unpolled sB after firstPollTTL err = %v, want ErrNotFound", err)
	}
	// s1 was polled so it is still alive.
	if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{Kind: "indexer_search_results", SearchID: s1.SearchID}); err != nil {
		t.Fatalf("polled s1 after firstPollTTL err = %v, want alive", err)
	}

	// 4. Idle expiry after last poll: advancing past idleTTL drops s1 and cancels its indexer.
	advance(3 * time.Minute)
	if _, err := kr.ResolveSourcesWithResult(ctx, "plugin.a", core.SourceQuery{Kind: "indexer_search_results", SearchID: s1.SearchID}); !errors.Is(err, plugins.ErrNotFound) {
		t.Fatalf("s1 after idleTTL err = %v, want ErrNotFound", err)
	}
	<-c1
}

// Verify KindResolver works end-to-end when Indexer is the production wrapper
// chain (debrid.Indexer -> livetv.VODIndexer -> StreamSources -> newznab.Manager):
// controlled options, pending vs timed_out, streaming, and kind:"indexers".
func TestKindResolverProductionWrapperStackPendingTimedOutAndIndexers(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const capsXML = `<?xml version="1.0" encoding="UTF-8"?><caps><searching><search available="yes" supportedParams="q"/></searching></caps>`
	fastSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			fmt.Fprint(w, capsXML)
			return
		}
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><item><title>Wrapped.Fast.FLAC</title><guid>wf1</guid><link>http://fast.example/wf1.nzb</link><enclosure url="http://fast.example/wf1.nzb" length="1024" type="application/x-nzb"/></item></channel></rss>`)
	}))
	defer fastSrv.Close()

	releaseSlow := make(chan struct{})
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			fmt.Fprint(w, capsXML)
			return
		}
		select {
		case <-releaseSlow:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><item><title>Wrapped.Slow.FLAC</title><guid>ws1</guid><link>http://slow.example/ws1.nzb</link><enclosure url="http://slow.example/ws1.nzb" length="2048" type="application/x-nzb"/></item></channel></rss>`)
	}))
	defer slowSrv.Close()

	mgr := newznab.NewManager(db, []config.Indexer{
		{ID: "fast-w", Name: "FastW", URL: fastSrv.URL, APIKey: "top-secret-key", Enabled: true},
		{ID: "slow-w", Name: "SlowW", URL: slowSrv.URL, APIKey: "other-secret-key", Protocol: "torrent", Enabled: true},
	}, newznab.Options{})
	defer mgr.Close()

	ss := NewStreamSources(mgr)
	vod := &livetv.VODIndexer{Inner: ss, Service: func() *livetv.Service { return nil }}
	debSvc := debrid.NewService(config.Debrid{}, debrid.Options{})
	wrapped := debrid.NewIndexer(vod, debSvc)

	kr := NewKindResolver(KindResolverDeps{
		Indexer:  wrapped,
		Resolver: resolver.New(wrapped, nil, nil, config.Defaults(""), resolver.Options{}),
	})
	defer kr.Close()

	// 1. Early return reports slow-w as pending (and in top-level Pending).
	zero := 0
	early, err := kr.ResolveSourcesWithResult(ctx, "plug.wrap", core.SourceQuery{
		Kind:         "indexer_search",
		Query:        "Wrapped",
		FirstResults: true,
		SettleMS:     &zero,
	})
	if err != nil {
		t.Fatalf("wrapped early return: %v", err)
	}
	if len(early.Candidates) != 1 || early.Candidates[0].Title != "Wrapped.Fast.FLAC" {
		t.Fatalf("wrapped early candidates = %+v", early.Candidates)
	}
	if len(early.Pending) != 1 || early.Pending[0].IndexerID != "slow-w" {
		t.Fatalf("wrapped early pending = %+v, want [slow-w]", early.Pending)
	}
	close(releaseSlow)

	// 2. kind:"indexers" returns both enabled indexers with kind and no secrets.
	list, err := kr.ResolveSourcesWithResult(ctx, "plug.wrap", core.SourceQuery{Kind: "indexers"})
	if err != nil {
		t.Fatalf("wrapped kind=indexers: %v", err)
	}
	if len(list.Indexers) != 2 {
		t.Fatalf("wrapped indexers = %+v, want 2", list.Indexers)
	}
	if list.Indexers[0].Kind != "newznab" || list.Indexers[1].Kind != "torznab" {
		t.Fatalf("wrapped indexer kinds = (%q, %q), want (newznab, torznab)", list.Indexers[0].Kind, list.Indexers[1].Kind)
	}
	raw, err := list.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if strings.Contains(string(raw), "top-secret-key") || strings.Contains(string(raw), "other-secret-key") {
		t.Fatalf("kind=indexers leaked secret: %s", string(raw))
	}

	// 3. Ceiling timeout reports timed_out (not pending).
	stuckSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			fmt.Fprint(w, capsXML)
			return
		}
		<-r.Context().Done()
	}))
	defer stuckSrv.Close()
	mgrCeil := newznab.NewManager(db, []config.Indexer{
		{ID: "fast-c", Name: "FastC", URL: fastSrv.URL, APIKey: "k1", Enabled: true},
		{ID: "stuck-c", Name: "StuckC", URL: stuckSrv.URL, APIKey: "k2", Enabled: true},
	}, newznab.Options{})
	defer mgrCeil.Close()
	wrappedCeil := debrid.NewIndexer(&livetv.VODIndexer{Inner: NewStreamSources(mgrCeil), Service: func() *livetv.Service { return nil }}, debSvc)
	krCeil := NewKindResolver(KindResolverDeps{
		Indexer:  wrappedCeil,
		Resolver: resolver.New(wrappedCeil, nil, nil, config.Defaults(""), resolver.Options{}),
	})
	krCeil.streamCeiling = 25 * time.Millisecond
	defer krCeil.Close()

	ceilRes, err := krCeil.ResolveSourcesWithResult(ctx, "plug.wrap", core.SourceQuery{
		Kind:      "indexer_search",
		Query:     "Ceiling",
		TimeoutMS: 25,
	})
	if err != nil {
		t.Fatalf("wrapped ceiling search: %v", err)
	}
	if len(ceilRes.Pending) != 0 {
		t.Fatalf("ceiling search pending = %+v, want empty", ceilRes.Pending)
	}
	byID := map[string]core.IndexerSearchStatus{}
	for _, st := range ceilRes.Indexers {
		byID[st.IndexerID] = st
	}
	if byID["stuck-c"].Status != core.IndexerStatusTimedOut {
		t.Fatalf("stuck-c status = %+v, want timed_out", byID["stuck-c"])
	}
}
