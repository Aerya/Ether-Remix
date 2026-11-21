package newznab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// throttleAddon répond 429 (avec ou sans Retry-After) tant que refuse est vrai.
type throttleAddon struct {
	srv        *httptest.Server
	refuse     atomic.Bool
	retryAfter atomic.Value // string
	requests   atomic.Int32
}

func newThrottleAddon(t *testing.T) *throttleAddon {
	t.Helper()
	a := &throttleAddon{}
	a.refuse.Store(true)
	a.retryAfter.Store("")
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.requests.Add(1)
		if a.refuse.Load() {
			if ra := a.retryAfter.Load().(string); ra != "" {
				w.Header().Set("Retry-After", ra)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, fakeNZB)
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func throttledErr(t *testing.T, err error) *core.IndexerThrottledError {
	t.Helper()
	var thr *core.IndexerThrottledError
	if !errors.Is(err, core.ErrBudgetExhausted) || !errors.Is(err, core.ErrIndexerThrottled) || !errors.As(err, &thr) {
		t.Fatalf("not a service throttle: %v", err)
	}
	return thr
}

func TestServicePauseIsThrottledErrorAtEveryStage(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clock := &fakeClock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	a := newThrottleAddon(t)
	c := New(testConfig(a.srv.URL), db, Options{Now: clock.now, NZBDir: t.TempDir()})
	rel := core.Release{ID: "test", GUID: "test", Indexer: c.Name()}

	_, err = c.FetchNZB(ctx, rel)
	thr := throttledErr(t, err)
	want := clock.now().Add(15 * time.Minute)
	if !thr.RetryAt.Equal(want) || len(thr.Indexers) != 1 || thr.Indexers[0] != c.Name() {
		t.Fatalf("first refusal: %+v", thr)
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != 429 {
		t.Fatalf("original 429 lost: %v", err)
	}
	// While paused, the local refusal carries the same information and stays local.
	_, err = c.FetchNZB(ctx, rel)
	thr = throttledErr(t, err)
	if !thr.RetryAt.Equal(want) || !errors.Is(err, errLocalBudget) {
		t.Fatalf("during pause: %+v %v", thr, err)
	}
	_, err = c.Search(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "X", Year: 2000})
	throttledErr(t, err)
	if !errors.Is(err, errLocalBudget) {
		t.Fatalf("search during pause must stay local: %v", err)
	}
	if a.requests.Load() != 1 {
		t.Fatalf("requests during pause: %d", a.requests.Load())
	}
}

func TestServicePauseGrowsThenResetsOnSuccess(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	a := newThrottleAddon(t)
	c := New(testConfig(a.srv.URL), nil, Options{Now: clock.now, NZBDir: t.TempDir()})
	rel := core.Release{ID: "test", GUID: "test", Indexer: c.Name()}

	pauses := []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	for i, p := range pauses {
		_, err := c.FetchNZB(ctx, rel)
		thr := throttledErr(t, err)
		if got := thr.RetryAt.Sub(clock.now()); got != p {
			t.Fatalf("refusal %d: pause %v, want %v", i+1, got, p)
		}
		clock.add(p + time.Second)
	}
	// A refusal received during a pause (request already in flight) does not extend it.
	c.rate.mu.Lock()
	before := c.rate.pse.streak
	c.rate.mu.Unlock()
	c.recordRateLimit(ctx, "", apiError(429, "x"))
	c.recordRateLimit(ctx, "", apiError(429, "x"))
	c.rate.mu.Lock()
	after := c.rate.pse.streak
	c.rate.mu.Unlock()
	if after != before+1 {
		t.Fatalf("in-flight refusals escalated: %d -> %d", before, after)
	}
	clock.add(25 * time.Hour)
	a.refuse.Store(false)
	if b, err := c.FetchNZB(ctx, rel); err != nil || string(b) != fakeNZB {
		t.Fatalf("recovery: %v", err)
	}
	// First success: the next refusal starts again at 15 minutes.
	a.refuse.Store(true)
	if err := c.recordRateLimit(ctx, "", apiError(429, "x")); err == nil {
		t.Fatal("no error")
	}
	until, _ := c.rateLimitUntil(ctx)
	if got := until.Sub(clock.now()); got != 15*time.Minute {
		t.Fatalf("pause after success: %v", got)
	}
}

func TestRetryAfterWinsOverGrowingPause(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	a := newThrottleAddon(t)
	c := New(testConfig(a.srv.URL), nil, Options{Now: clock.now, NZBDir: t.TempDir()})
	rel := core.Release{ID: "test", GUID: "test", Indexer: c.Name()}
	for _, p := range []time.Duration{15 * time.Minute, 30 * time.Minute} {
		_, err := c.FetchNZB(ctx, rel)
		throttledErr(t, err)
		clock.add(p + time.Second)
	}
	a.retryAfter.Store("120")
	_, err := c.FetchNZB(ctx, rel)
	thr := throttledErr(t, err)
	if got := thr.RetryAt.Sub(clock.now()); got != 2*time.Minute {
		t.Fatalf("Retry-After ignored: %v", got)
	}
}

func TestLocalDailyLimitIsNotAServicePause(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig("http://127.0.0.1:1")
	cfg.DailyLimit = 1
	c := New(cfg, nil, Options{NZBDir: t.TempDir()})
	if err := c.reserve(ctx, "hit"); err != nil {
		t.Fatal(err)
	}
	err := c.reserve(ctx, "hit")
	if !errors.Is(err, core.ErrBudgetExhausted) || !errors.Is(err, errLocalBudget) {
		t.Fatalf("daily limit: %v", err)
	}
	if errors.Is(err, core.ErrIndexerThrottled) {
		t.Fatalf("daily limit reported as service pause: %v", err)
	}
	var lim *core.IndexerDailyLimitError
	if !errors.As(err, &lim) || lim.Indexers[0] != c.Name() {
		t.Fatalf("daily limit type: %v", err)
	}
	if got := c.pseExhaustedErr(ctx); errors.Is(got, core.ErrIndexerThrottled) {
		t.Fatalf("exhausted: %v", got)
	}
}

func TestManagerReportsPausedIndexerAndOthersAnswered(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	paused := newThrottleAddon(t)
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			fmt.Fprint(w, loadFixture(t, "caps.xml", "http://"+r.Host))
			return
		}
		fmt.Fprint(w, `<rss version="2.0"><channel></channel></rss>`)
	}))
	defer empty.Close()
	a, b := testConfig(paused.srv.URL), testConfig(empty.URL)
	a.ID, a.Name = "a", "Nzb A"
	b.ID, b.Name = "b", "Nzb B"
	m := NewManager(nil, []config.Indexer{a, b}, Options{Now: clock.now, NZBDir: t.TempDir()})
	req := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt0133093"}, Title: "The Signal", Year: 1999}
	var err error
	for range 2 { // the second pass is served from the pause itself
		_, err = m.Search(ctx, req)
		thr := throttledErr(t, err)
		if len(thr.Indexers) != 1 || thr.Indexers[0] != "Nzb A" || !thr.OthersAnswered || thr.RetryAt.IsZero() {
			t.Fatalf("manager error: %+v", thr)
		}
	}
	msg := core.LocalDownloadMessage(err, "")
	if msg == "" || strings.Contains(msg, "all indexers") {
		t.Fatalf("message: %q", msg)
	}
}

func TestHumanErrorStillShowsDailyLimit(t *testing.T) {
	err := humanError("Fake", apiError(429, "too many requests"))
	if err == nil || err.Error() != `indexer "Fake": daily API limit reached` {
		t.Fatalf("humanError: %v", err)
	}
	// The setup wizard probe keeps the original *APIError, not the wrapper.
	a := newThrottleAddon(t)
	got := TestWithOptions(context.Background(), testConfig(a.srv.URL), Options{})
	if got == nil || got.Error() != `indexer "Fake": daily API limit reached` {
		t.Fatalf("wizard probe: %v", got)
	}
}

// stubBackend simule un addon : refuse (RateLimitError), répond sans rien
// interroger (nil, nil) ou répond avec une requête réellement partie.
type stubBackend struct{ mode string }

func (b *stubBackend) Search(ctx context.Context, _ core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	switch b.mode {
	case "noid":
		return nil, nil
	case "ok":
		if err := reserve(ctx, "hit"); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err := reserve(ctx, "hit"); err != nil {
		return nil, err
	}
	return nil, &RateLimitError{Err: apiError(429, "too many requests")}
}
func (b *stubBackend) FetchNZB(context.Context, core.Release, func(context.Context, string) error) ([]byte, error) {
	return nil, errors.New("unused")
}
func (b *stubBackend) ResolveTorrent(context.Context, core.Release, func(context.Context, string) error) (core.Release, error) {
	return core.Release{}, errors.New("unused")
}

func TestBackendPathThrottleGrowsAndNoRequestKeepsStep(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	b := &stubBackend{mode: "limit"}
	cfg := testConfig("http://127.0.0.1:1")
	c := New(cfg, nil, Options{Now: clock.now, NZBDir: t.TempDir(), Backend: func(config.Indexer) (Backend, error) { return b, nil }})
	search := func() error {
		_, err := c.stampBackend(ctx, "search", func(ctx context.Context, reserve func(context.Context, string) error) ([]core.Release, error) {
			return b.Search(ctx, core.SearchRequest{}, reserve)
		})
		return err
	}
	for _, p := range []time.Duration{15 * time.Minute, 30 * time.Minute} {
		thr := throttledErr(t, search())
		if got := thr.RetryAt.Sub(clock.now()); got != p {
			t.Fatalf("pause %v, want %v", got, p)
		}
		clock.add(p + time.Second)
	}
	// A search without identifier sends nothing: the step must not reset.
	b.mode = "noid"
	if err := search(); err != nil {
		t.Fatal(err)
	}
	b.mode = "limit"
	thr := throttledErr(t, search())
	if got := thr.RetryAt.Sub(clock.now()); got != time.Hour {
		t.Fatalf("step reset by a search that sent nothing: %v", got)
	}
	// A real request that succeeds resets it.
	clock.add(time.Hour + time.Second)
	b.mode = "ok"
	if err := search(); err != nil {
		t.Fatal(err)
	}
	b.mode = "limit"
	thr = throttledErr(t, search())
	if got := thr.RetryAt.Sub(clock.now()); got != 15*time.Minute {
		t.Fatalf("no reset after success: %v", got)
	}
}

func TestManagerNamesPausedAndDailyLimited(t *testing.T) {
	e := &core.IndexerThrottledError{Indexers: []string{"A"}, DailyLimited: []string{"B"}, Err: core.ErrBudgetExhausted}
	got := core.LocalDownloadMessage(e, "")
	if !strings.Contains(got, "A limite") || !strings.Contains(got, "atteinte pour B.") {
		t.Fatalf("message: %q", got)
	}
}
