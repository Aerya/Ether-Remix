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
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

const testCapsXML = `<?xml version="1.0" encoding="UTF-8"?><caps><searching><search available="yes" supportedParams="q"/></searching></caps>`

func rssItem(title, guid, link, infoHash string) string {
	var extra string
	if infoHash != "" {
		extra = fmt.Sprintf(`<torznab:attr name="infohash" value="%s"/>`, infoHash)
	}
	return fmt.Sprintf(`<item><title>%s</title><guid>%s</guid><link>%s</link><enclosure url="%s" length="1048576" type="application/x-nzb"/>%s</item>`, title, guid, link, link, extra)
}

func rssEnvelope(items ...string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>` +
		strings.Join(items, "") +
		`</channel></rss>`
}

func newznabSearchHandler(fn func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			fmt.Fprint(w, testCapsXML)
			return
		}
		fn(w, r)
	})
}

func statusByID(statuses []core.IndexerSearchStatus) map[string]core.IndexerSearchStatus {
	out := make(map[string]core.IndexerSearchStatus, len(statuses))
	for _, s := range statuses {
		out[s.IndexerID] = s
	}
	return out
}

// FirstResults with settle=0 returns as soon as the fast indexer answers while
// keeping the slow indexer alive in the background to populate Ether's search
// cache (and coalesce a follow-up search targeting the slow indexer).
func TestManagerSearchWithOptionsFirstResultsBackgroundCacheAndCoalesce(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fastSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssEnvelope(rssItem("Fast.Release.1080p", "fast-1", "http://fast.example/1.nzb", "")))
	}))
	defer fastSrv.Close()

	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var slowSearchHits atomic.Int32
	slowSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		if slowSearchHits.Add(1) == 1 {
			close(slowStarted)
			select {
			case <-releaseSlow:
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprint(w, rssEnvelope(rssItem("Slow.Release.1080p", "slow-1", "http://slow.example/1.nzb", "")))
	}))
	defer slowSrv.Close()

	cFast := testConfig(fastSrv.URL)
	cFast.ID, cFast.Name = "fast-id", "Fast Indexer"
	cSlow := testConfig(slowSrv.URL)
	cSlow.ID, cSlow.Name = "slow-id", "Slow Indexer"

	m := NewManager(db, []config.Indexer{cFast, cSlow}, Options{Vault: &testVault{}})
	defer m.Close()

	req := core.SearchRequest{Title: "Dune"}
	res, err := m.SearchWithOptions(ctx, req, SearchOptions{
		FirstResults:      true,
		Settle:            0,
		SettleSet:         true,
		BackgroundTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("SearchWithOptions(firstResults): %v", err)
	}
	if len(res.Releases) != 1 || res.Releases[0].Name != "Fast.Release.1080p" {
		t.Fatalf("first call releases = %+v, want only Fast.Release.1080p", res.Releases)
	}
	byID := statusByID(res.Indexers)
	if byID["fast-id"].Status != core.IndexerStatusAnswered || byID["fast-id"].Results != 1 {
		t.Fatalf("fast status = %+v, want answered/1", byID["fast-id"])
	}
	if byID["slow-id"].Status != core.IndexerStatusPending {
		t.Fatalf("slow status = %+v, want pending while still in flight", byID["slow-id"])
	}
	if len(res.Pending) != 1 || res.Pending[0].IndexerID != "slow-id" || res.Pending[0].Indexer != "Slow Indexer" {
		t.Fatalf("pending = %+v, want [{slow-id Slow Indexer}]", res.Pending)
	}

	// Wait until the detached slow search is in flight, then launch a follow-up
	// query restricted to the slow indexer. It must coalesce with the detached
	// in-flight search rather than issuing a second upstream search.
	<-slowStarted
	followDone := make(chan SearchResult, 1)
	followErr := make(chan error, 1)
	go func() {
		r2, e2 := m.SearchWithOptions(ctx, req, SearchOptions{
			OnlyIndexers: []string{"Slow Indexer"},
		})
		followDone <- r2
		followErr <- e2
	}()

	// Unblock the single in-flight slow request; both the detached background
	// cache-fill and the coalesced follow-up search complete from it.
	close(releaseSlow)
	r2 := <-followDone
	if e2 := <-followErr; e2 != nil {
		t.Fatalf("follow-up search err: %v", e2)
	}
	if len(r2.Releases) != 1 || r2.Releases[0].Name != "Slow.Release.1080p" {
		t.Fatalf("follow-up releases = %+v, want Slow.Release.1080p", r2.Releases)
	}
	byID2 := statusByID(r2.Indexers)
	if byID2["fast-id"].Status != core.IndexerStatusSkipped {
		t.Fatalf("follow-up fast status = %+v, want skipped", byID2["fast-id"])
	}
	if byID2["slow-id"].Status != core.IndexerStatusAnswered || byID2["slow-id"].Results != 1 {
		t.Fatalf("follow-up slow status = %+v, want answered/1", byID2["slow-id"])
	}

	// A third call hits the populated search cache without touching slowSrv again.
	r3, err := m.SearchWithOptions(ctx, req, SearchOptions{OnlyIndexers: []string{"slow-id"}})
	if err != nil {
		t.Fatalf("cached follow-up search err: %v", err)
	}
	if len(r3.Releases) != 1 || r3.Releases[0].Name != "Slow.Release.1080p" {
		t.Fatalf("cached follow-up releases = %+v, want Slow.Release.1080p", r3.Releases)
	}
	if got := slowSearchHits.Load(); got != 1 {
		t.Fatalf("slow upstream search hits = %d, want 1 (coalesced + cached)", got)
	}
}

// MinResults counts deduplicated releases across indexers, not raw duplicate hits.
func TestManagerSearchWithOptionsMinResultsDeduplicated(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const sharedHash = "0123456789abcdef0123456789abcdef01234567"
	const secondHash = "fedcba9876543210fedcba9876543210fedcba98"

	var abDone atomic.Int32
	abReady := make(chan struct{})
	releaseC := make(chan struct{})
	releaseD := make(chan struct{})
	defer close(releaseD)

	mkDupSrv := func(name string) *httptest.Server {
		return httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, rssEnvelope(rssItem("Shared.Release", name+"-1", "http://dup.example/1.torrent", sharedHash)))
			if abDone.Add(1) == 2 {
				close(abReady)
			}
		}))
	}
	srvA := mkDupSrv("a")
	defer srvA.Close()
	srvB := mkDupSrv("b")
	defer srvB.Close()

	srvC := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-releaseC:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, rssEnvelope(rssItem("Distinct.Release", "c-1", "http://c.example/1.torrent", secondHash)))
	}))
	defer srvC.Close()

	srvD := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-releaseD:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, rssEnvelope(rssItem("Never.Waited.Release", "d-1", "http://d.example/1.torrent", "")))
	}))
	defer srvD.Close()

	mkCfg := func(id, name, u string) config.Indexer {
		c := testConfig(u)
		c.ID, c.Name, c.Protocol = id, name, "torrent"
		return c
	}
	m := NewManager(db, []config.Indexer{
		mkCfg("a", "A", srvA.URL),
		mkCfg("b", "B", srvB.URL),
		mkCfg("c", "C", srvC.URL),
		mkCfg("d", "D", srvD.URL),
	}, Options{Vault: &testVault{}})
	defer m.Close()

	done := make(chan SearchResult, 1)
	errCh := make(chan error, 1)
	go func() {
		r, e := m.SearchWithOptions(ctx, core.SearchRequest{Title: "Matrix"}, SearchOptions{
			MinResults:        2,
			BackgroundTimeout: 5 * time.Second,
		})
		done <- r
		errCh <- e
	}()

	// After both A and B finish with the same infoHash, deduplicated count is 1 < 2,
	// so SearchWithOptions must still be waiting on C.
	<-abReady
	select {
	case r := <-done:
		t.Fatalf("returned early before 2 unique releases: %+v", r)
	default:
	}

	// Unblock C; deduplicated count hits 2 and SearchWithOptions returns while D is still blocked.
	close(releaseC)
	res := <-done
	if err := <-errCh; err != nil {
		t.Fatalf("SearchWithOptions(minResults): %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("len(releases) = %d, want 2 deduplicated releases", len(res.Releases))
	}
	byID := statusByID(res.Indexers)
	if byID["c"].Status != core.IndexerStatusAnswered || byID["d"].Status != core.IndexerStatusPending {
		t.Fatalf("statuses c=%+v d=%+v, want c=answered d=pending", byID["c"], byID["d"])
	}
	// A and B's servers have answered, but the manager may not have read their
	// replies yet, so either can still be pending. D always is; C never is.
	var pendingD bool
	for _, p := range res.Pending {
		switch p.IndexerID {
		case "d":
			pendingD = p.Indexer == "D"
		case "c":
			t.Fatalf("pending = %+v, C answered", res.Pending)
		}
	}
	if !pendingD {
		t.Fatalf("pending = %+v, want it to include {d D}", res.Pending)
	}
}

// SkipIndexers takes precedence over OnlyIndexers and matches both IDs and display names case-insensitively.
func TestManagerSearchWithOptionsSkipAndOnlyIndexers(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var hitsA, hitsB, hitsC atomic.Int32
	mkSrv := func(counter *atomic.Int32, title string) *httptest.Server {
		return httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			fmt.Fprint(w, rssEnvelope(rssItem(title, title, "http://idx.example/"+title+".nzb", "")))
		}))
	}
	srvA := mkSrv(&hitsA, "Alpha.Release")
	defer srvA.Close()
	srvB := mkSrv(&hitsB, "Beta.Release")
	defer srvB.Close()
	srvC := mkSrv(&hitsC, "Gamma.Release")
	defer srvC.Close()

	cA := testConfig(srvA.URL)
	cA.ID, cA.Name = "idx-a", "Alpha Index"
	cB := testConfig(srvB.URL)
	cB.ID, cB.Name = "idx-b", "Beta Index"
	cC := testConfig(srvC.URL)
	cC.ID, cC.Name = "idx-c", "Gamma Index"

	m := NewManager(db, []config.Indexer{cA, cB, cC}, Options{Vault: &testVault{}})
	defer m.Close()

	res, err := m.SearchWithOptions(ctx, core.SearchRequest{Title: "Arrival"}, SearchOptions{
		OnlyIndexers: []string{"alpha index", "IDX-B"},
		SkipIndexers: []string{"BETA INDEX"},
	})
	if err != nil {
		t.Fatalf("SearchWithOptions(only+skip): %v", err)
	}
	if hitsA.Load() != 1 || hitsB.Load() != 0 || hitsC.Load() != 0 {
		t.Fatalf("search hits a=%d b=%d c=%d, want 1/0/0", hitsA.Load(), hitsB.Load(), hitsC.Load())
	}
	if len(res.Releases) != 1 || res.Releases[0].Name != "Alpha.Release" {
		t.Fatalf("releases = %+v, want only Alpha.Release", res.Releases)
	}
	byID := statusByID(res.Indexers)
	if byID["idx-a"].Status != core.IndexerStatusAnswered || byID["idx-a"].Results != 1 {
		t.Fatalf("idx-a status = %+v, want answered/1", byID["idx-a"])
	}
	if byID["idx-b"].Status != core.IndexerStatusSkipped || byID["idx-b"].MS != 0 {
		t.Fatalf("idx-b status = %+v, want skipped/0ms", byID["idx-b"])
	}
	if byID["idx-c"].Status != core.IndexerStatusSkipped || byID["idx-c"].MS != 0 {
		t.Fatalf("idx-c status = %+v, want skipped/0ms", byID["idx-c"])
	}
}

// Per-indexer error status uses short sanitized error codes (never leaking API keys or URLs),
// and default SearchOptions{} waits for all enabled indexers.
func TestManagerSearchWithOptionsErrorSanitizationAndDefaultWaitAll(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fastAnswered := make(chan struct{})
	var closeOnce sync.Once
	goodSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssEnvelope(rssItem("Good.Release", "g-1", "http://good.example/1.nzb", "")))
		closeOnce.Do(func() { close(fastAnswered) })
	}))
	defer goodSrv.Close()

	badSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		<-fastAnswered
		http.Error(w, "invalid apikey super-secret-key-999", http.StatusUnauthorized)
	}))
	defer badSrv.Close()

	cGood := testConfig(goodSrv.URL)
	cGood.ID, cGood.Name = "good", "Good"
	cBad := testConfig(badSrv.URL)
	cBad.ID, cBad.Name, cBad.APIKey = "bad", "Bad", "super-secret-key-999"

	m := NewManager(db, []config.Indexer{cGood, cBad}, Options{Vault: &testVault{}})
	defer m.Close()

	res, err := m.SearchWithOptions(ctx, core.SearchRequest{Title: "Interstellar"}, SearchOptions{})
	if err != nil {
		t.Fatalf("SearchWithOptions(default): %v", err)
	}
	if len(res.Releases) != 1 || res.Releases[0].Name != "Good.Release" {
		t.Fatalf("releases = %+v, want Good.Release", res.Releases)
	}
	byID := statusByID(res.Indexers)
	if byID["good"].Status != core.IndexerStatusAnswered || byID["good"].Results != 1 {
		t.Fatalf("good status = %+v, want answered/1", byID["good"])
	}
	if byID["bad"].Status != core.IndexerStatusError {
		t.Fatalf("bad status = %+v, want error", byID["bad"])
	}
	if byID["bad"].Error != "auth_failed" {
		t.Fatalf("bad error code = %q, want auth_failed", byID["bad"].Error)
	}
	if strings.Contains(byID["bad"].Error, "super-secret-key-999") || strings.Contains(byID["bad"].Error, "http") {
		t.Fatalf("bad error leaked secret/URL: %q", byID["bad"].Error)
	}
}

// Early cutoffs shorter than BackgroundTimeout report unfinished indexers as
// pending (and list them in Pending); hitting the ceiling (WaitTimeout >=
// BackgroundTimeout or parent deadline) cancels them and reports timed_out.
func TestManagerSearchWithOptionsPendingVsTimedOut(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fastSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssEnvelope(rssItem("Fast.Item", "fast-1", "http://fast.example/1.nzb", "")))
	}))
	defer fastSrv.Close()

	// 1. WaitTimeout shorter than BackgroundTimeout -> pending, continues filling cache.
	slowRelease := make(chan struct{})
	slowDone := make(chan struct{})
	slowSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-slowRelease:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, rssEnvelope(rssItem("Slow.Item", "slow-1", "http://slow.example/1.nzb", "")))
		close(slowDone)
	}))
	defer slowSrv.Close()

	cFast := testConfig(fastSrv.URL)
	cFast.ID, cFast.Name = "fast", "Fast"
	cSlow := testConfig(slowSrv.URL)
	cSlow.ID, cSlow.Name = "slow", "Slow"

	m := NewManager(db, []config.Indexer{cFast, cSlow}, Options{Vault: &testVault{}})
	defer m.Close()

	res, err := m.SearchWithOptions(ctx, core.SearchRequest{Title: "PendingCase"}, SearchOptions{
		WaitTimeout:       20 * time.Millisecond,
		BackgroundTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("SearchWithOptions(short wait): %v", err)
	}
	byID := statusByID(res.Indexers)
	if byID["slow"].Status != core.IndexerStatusPending {
		t.Fatalf("slow status = %+v, want pending", byID["slow"])
	}
	if len(res.Pending) != 1 || res.Pending[0].IndexerID != "slow" {
		t.Fatalf("pending = %+v, want [slow]", res.Pending)
	}
	close(slowRelease)
	<-slowDone

	// 2. WaitTimeout == BackgroundTimeout (hits ceiling) -> timed_out, NOT pending, request cancelled.
	ceilCanceled := make(chan struct{})
	var ceilOnce sync.Once
	ceilSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		ceilOnce.Do(func() { close(ceilCanceled) })
	}))
	defer ceilSrv.Close()

	cCeil := testConfig(ceilSrv.URL)
	cCeil.ID, cCeil.Name = "ceil", "Ceiling"
	mCeil := NewManager(db, []config.Indexer{cFast, cCeil}, Options{Vault: &testVault{}})
	defer mCeil.Close()

	ceilRes, err := mCeil.SearchWithOptions(ctx, core.SearchRequest{Title: "CeilingCase"}, SearchOptions{
		WaitTimeout:       25 * time.Millisecond,
		BackgroundTimeout: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("SearchWithOptions(ceiling): %v", err)
	}
	byCeil := statusByID(ceilRes.Indexers)
	if byCeil["ceil"].Status != core.IndexerStatusTimedOut {
		t.Fatalf("ceiling status = %+v, want timed_out", byCeil["ceil"])
	}
	if len(ceilRes.Pending) != 0 {
		t.Fatalf("ceiling pending = %+v, want empty", ceilRes.Pending)
	}
	<-ceilCanceled

	// 3. Parent context deadline hit (even with FirstResults:true) -> timed_out, not error:"canceled".
	parentCanceled := make(chan struct{})
	var parentOnce sync.Once
	stuckSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		parentOnce.Do(func() { close(parentCanceled) })
	}))
	defer stuckSrv.Close()

	cStuck := testConfig(stuckSrv.URL)
	cStuck.ID, cStuck.Name = "stuck", "Stuck"
	mStuck := NewManager(db, []config.Indexer{cStuck}, Options{Vault: &testVault{}})
	defer mStuck.Close()

	pctx, pcancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer pcancel()
	stuckRes, err := mStuck.SearchWithOptions(pctx, core.SearchRequest{Title: "ParentDeadline"}, SearchOptions{
		FirstResults:      true,
		BackgroundTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatalf("expected parent deadline error, got nil (res=%+v)", stuckRes)
	}
	byStuck := statusByID(stuckRes.Indexers)
	if byStuck["stuck"].Status != core.IndexerStatusTimedOut {
		t.Fatalf("parent deadline status = %+v, want timed_out", byStuck["stuck"])
	}
	if len(stuckRes.Pending) != 0 {
		t.Fatalf("parent deadline pending = %+v, want empty", stuckRes.Pending)
	}
	<-parentCanceled
}

// EnabledIndexers lists only enabled indexers with kind, recent timing stats,
// and sanitized lastError, never leaking API keys, URLs, or cookies.
func TestManagerEnabledIndexersListingAndTimings(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	okSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssEnvelope(rssItem("Listing.Item", "l-1", "http://ok.example/1.nzb", "")))
	}))
	defer okSrv.Close()

	errSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad apikey secret-token-abc cookie=session=xyz", http.StatusUnauthorized)
	}))
	defer errSrv.Close()

	cNewz := testConfig(okSrv.URL)
	cNewz.ID, cNewz.Name, cNewz.APIKey = "idx-nzb", "Geek", "secret-key-1"

	cTorz := testConfig(errSrv.URL)
	cTorz.ID, cTorz.Name, cTorz.Protocol, cTorz.APIKey = "idx-tor", "Torznabber", "torrent", "secret-key-2"

	cBuiltin := config.Indexer{
		ID: "idx-builtin", Name: "Site", Kind: config.IndexerKindCardigann,
		URL: "https://private.example/secret-path", Enabled: true,
		Settings:       map[string]string{"cookie": "uid=1; pass=supersecret"},
		SecretSettings: map[string]string{"password": "pw-secret"},
	}
	cStremio := config.Indexer{ID: "idx-stremio", Name: "Addon", Kind: config.IndexerKindStremio, URL: "https://addon.example/tok/manifest.json", Enabled: true}
	cZilean := config.Indexer{ID: "idx-zilean", Name: "Zilean", Kind: config.IndexerKindZilean, URL: "https://zilean.example", Enabled: true}
	cPlugin := config.Indexer{ID: "idx-addon", Name: "PluginSrc", Kind: config.IndexerKindAddon, Enabled: true}
	cDisabled := config.Indexer{ID: "idx-off", Name: "Disabled", URL: "https://off.example", APIKey: "off-secret", Enabled: false}

	m := NewManager(db, []config.Indexer{cNewz, cTorz, cBuiltin, cStremio, cZilean, cPlugin, cDisabled}, Options{Vault: &testVault{}})
	defer m.Close()

	// Before any search: kinds match and timing fields are nil/omitted.
	before := m.EnabledIndexers()
	if len(before) != 6 {
		t.Fatalf("len(EnabledIndexers) = %d, want 6 (disabled omitted)", len(before))
	}
	wantKinds := map[string]string{
		"idx-nzb":     "newznab",
		"idx-tor":     "torznab",
		"idx-builtin": "builtin",
		"idx-stremio": "stremio",
		"idx-zilean":  "zilean",
		"idx-addon":   "addon",
	}
	for _, entry := range before {
		if entry.Kind != wantKinds[entry.IndexerID] {
			t.Fatalf("%s kind = %q, want %q", entry.IndexerID, entry.Kind, wantKinds[entry.IndexerID])
		}
		if entry.MedianMS != nil || entry.LastMS != nil || entry.LastError != "" {
			t.Fatalf("%s before search had unexpected timing/error: %+v", entry.IndexerID, entry)
		}
	}

	// Run a search across the two HTTP indexers.
	_, _ = m.SearchWithOptions(ctx, core.SearchRequest{Title: "Check"}, SearchOptions{
		OnlyIndexers: []string{"idx-nzb", "idx-tor"},
	})

	// A cancelled timing call must not pollute lastError with "canceled".
	m.recordIndexerTiming("idx-nzb", 5*time.Second, context.Canceled)

	after := m.EnabledIndexers()
	byID := statusByID(after)
	if byID["idx-nzb"].MedianMS == nil || byID["idx-nzb"].LastMS == nil || byID["idx-nzb"].LastError != "" {
		t.Fatalf("idx-nzb after search = %+v, want timing set and empty lastError", byID["idx-nzb"])
	}
	if byID["idx-tor"].MedianMS == nil || byID["idx-tor"].LastMS == nil || byID["idx-tor"].LastError != "auth_failed" {
		t.Fatalf("idx-tor after search = %+v, want timing set and lastError=auth_failed", byID["idx-tor"])
	}

	// Verify wire JSON contains no secrets, URLs, cookies, or bogus search status/ms fields.
	raw, err := core.SourceResolveResult{Candidates: []core.SourceCandidate{}, Indexers: after}.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	wire := string(raw)
	for _, forbidden := range []string{"secret-key-1", "secret-key-2", "pw-secret", "supersecret", "private.example", "addon.example", "off-secret", `"status"`, `"ms"`} {
		if strings.Contains(wire, forbidden) {
			t.Fatalf("indexers listing wire JSON contained forbidden %q: %s", forbidden, wire)
		}
	}
}

// SearchWith emits per-indexer status via WithIndexerStatusObserver as each
// indexer finishes (before slower indexers unblock) and honours WithIndexerSkip.
func TestManagerSearchWithStatusObserverAndSkip(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fastSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssEnvelope(rssItem("Fast.Stream", "fs-1", "http://fast.example/1.nzb", "")))
	}))
	defer fastSrv.Close()

	releaseSlow := make(chan struct{})
	slowSrv := httptest.NewServer(newznabSearchHandler(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-releaseSlow:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, rssEnvelope(rssItem("Slow.Stream", "ss-1", "http://slow.example/1.nzb", "")))
	}))
	defer slowSrv.Close()

	cFast := testConfig(fastSrv.URL)
	cFast.ID, cFast.Name = "fast", "Fast"
	cSlow := testConfig(slowSrv.URL)
	cSlow.ID, cSlow.Name = "slow", "Slow"
	cSkip := testConfig(fastSrv.URL)
	cSkip.ID, cSkip.Name = "skip", "Skipped"

	m := NewManager(db, []config.Indexer{cFast, cSlow, cSkip}, Options{Vault: &testVault{}})
	defer m.Close()

	fastStatusCh := make(chan core.IndexerSearchStatus, 1)
	slowStatusCh := make(chan core.IndexerSearchStatus, 1)
	skipStatusCh := make(chan core.IndexerSearchStatus, 1)
	arrivedCh := make(chan []core.Release, 4)

	sctx := core.WithIndexerSkip(ctx, map[string]bool{"skip": true})
	sctx = core.WithIndexerStatusObserver(sctx, func(st core.IndexerSearchStatus) {
		switch st.IndexerID {
		case "fast":
			fastStatusCh <- st
		case "slow":
			slowStatusCh <- st
		case "skip":
			skipStatusCh <- st
		}
	})

	done := make(chan error, 1)
	go func() {
		_, err := m.SearchWith(sctx, core.SearchRequest{Title: "Stream"}, func(batch []core.Release) {
			arrivedCh <- batch
		})
		done <- err
	}()

	// Skipped and fast arrive while slow is still blocked on releaseSlow.
	if st := <-skipStatusCh; st.Status != core.IndexerStatusSkipped {
		t.Fatalf("skip status = %+v, want skipped", st)
	}
	if batch := <-arrivedCh; len(batch) != 1 || batch[0].Name != "Fast.Stream" {
		t.Fatalf("first arrival = %+v, want Fast.Stream", batch)
	}
	if st := <-fastStatusCh; st.Status != core.IndexerStatusAnswered || st.Results != 1 {
		t.Fatalf("fast status = %+v, want answered/1", st)
	}
	select {
	case st := <-slowStatusCh:
		t.Fatalf("slow finished before release: %+v", st)
	default:
	}

	close(releaseSlow)
	if batch := <-arrivedCh; len(batch) != 1 || batch[0].Name != "Slow.Stream" {
		t.Fatalf("second arrival = %+v, want Slow.Stream", batch)
	}
	if st := <-slowStatusCh; st.Status != core.IndexerStatusAnswered || st.Results != 1 {
		t.Fatalf("slow status = %+v, want answered/1", st)
	}
	if err := <-done; err != nil {
		t.Fatalf("SearchWith err: %v", err)
	}
}
