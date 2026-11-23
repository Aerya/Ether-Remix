package newznab

import (
	"context"
	"errors"
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

func TestBreakerStateMachine(t *testing.T) {
	var b breaker
	now := time.Unix(1_000_000, 0)
	fail := func(tk breakerTicket) time.Duration { return b.done(now, tk, true, 0) }

	// Closed: failures below the threshold keep it closed; an answer resets.
	fail(breakerTicket{})
	fail(breakerTicket{})
	b.done(now, breakerTicket{}, false, 0)
	fail(breakerTicket{})
	fail(breakerTicket{})
	if b.blocked(now) {
		t.Fatal("opened below the threshold")
	}
	if pause := fail(breakerTicket{}); pause != breakerBasePause || !b.blocked(now) {
		t.Fatalf("third consecutive failure: pause %v blocked %v", pause, b.blocked(now))
	}
	if _, err := b.admit(now); !errors.Is(err, ErrIndexerPaused) {
		t.Fatalf("admitted while open: %v", err)
	}

	// Half-open: exactly one trial; others stay paused while it runs.
	now = now.Add(breakerBasePause)
	tk, err := b.admit(now)
	if err != nil || !tk.probe {
		t.Fatalf("no trial after the pause: %+v %v", tk, err)
	}
	if _, err := b.admit(now); !errors.Is(err, ErrIndexerPaused) || !b.blocked(now) {
		t.Fatal("second request admitted during the trial")
	}
	// A trial whose caller went away gives no verdict.
	b.neutral(tk)
	if tk, err = b.admit(now); err != nil || !tk.probe {
		t.Fatalf("trial not re-offered after a neutral end: %+v %v", tk, err)
	}
	// A failed trial reopens with a doubled pause, capped.
	if pause := fail(tk); pause != 2*breakerBasePause {
		t.Fatalf("failed trial pause %v", pause)
	}
	for range 10 {
		now = now.Add(breakerMaxPause)
		if tk, _ = b.admit(now); !tk.probe {
			t.Fatal("no trial after the pause")
		}
		if pause := fail(tk); pause > breakerMaxPause {
			t.Fatalf("pause %v above the cap", pause)
		}
	}

	// Gradual recovery after a successful probe: 1 (probe) -> 2 -> 4 -> full.
	now = now.Add(breakerMaxPause)
	tk, _ = b.admit(now)
	b.done(now, tk, false, 0)
	if b.blocked(now) {
		t.Fatal("blocked with zero in-flight after a successful probe")
	}

	// Tier 1 allows at most 2 concurrent requests.
	t1, err1 := b.admit(now)
	t2, err2 := b.admit(now)
	if err1 != nil || err2 != nil || t1.probe || t2.probe {
		t.Fatalf("ramp tier 1 admit: %v %v (%+v %+v)", err1, err2, t1, t2)
	}
	if _, err := b.admit(now); !errors.Is(err, ErrIndexerPaused) || !b.blocked(now) {
		t.Fatal("third concurrent request admitted during ramp tier 1 (cap 2)")
	}
	b.done(now, t1, false, 0)
	b.done(now, t2, false, 0)

	// Tier 2 allows at most 4 concurrent requests.
	var tier2 [4]breakerTicket
	for i := range tier2 {
		tier2[i], err = b.admit(now)
		if err != nil {
			t.Fatalf("ramp tier 2 admit %d: %v", i, err)
		}
	}
	if _, err := b.admit(now); !errors.Is(err, ErrIndexerPaused) || !b.blocked(now) {
		t.Fatal("fifth concurrent request admitted during ramp tier 2 (cap 4)")
	}
	for _, slot := range tier2 {
		b.done(now, slot, false, 0)
	}

	// Fully closed: concurrency is uncapped again and back-off is reset.
	var closedSlots [8]breakerTicket
	for i := range closedSlots {
		closedSlots[i], err = b.admit(now)
		if err != nil {
			t.Fatalf("closed admit %d: %v", i, err)
		}
	}
	for _, slot := range closedSlots {
		b.done(now, slot, false, 0)
	}
	fail(breakerTicket{})
	fail(breakerTicket{})
	if pause := fail(breakerTicket{}); pause != breakerBasePause {
		t.Fatalf("back-off not reset after completing ramp: %v", pause)
	}
}

func TestBreakerRampFailureReopensImmediately(t *testing.T) {
	var b breaker
	now := time.Unix(1_000_000, 0)

	// Trip 4 times so trips == 4 (pause was 16m).
	for range breakerThreshold {
		b.done(now, breakerTicket{}, true, 0)
	}
	for range 3 {
		now = now.Add(breakerMaxPause)
		tk, _ := b.admit(now)
		b.done(now, tk, true, 0)
	}

	// Pass the single probe -> enters ramp tier 1 with halved trips (4 -> 2).
	now = now.Add(breakerMaxPause)
	probe, err := b.admit(now)
	if err != nil || !probe.probe {
		t.Fatalf("probe admit: %+v %v", probe, err)
	}
	b.done(now, probe, false, 0)

	// Two requests enter ramp tier 1 concurrently; the first fails and must
	// reopen immediately at stepped-down backoff (2<<2 = 8m) without needing
	// 3 strikes, and the sibling in-flight failure must not double-trip it.
	r1, _ := b.admit(now)
	r2, _ := b.admit(now)
	pause := b.done(now, r1, true, 0)
	if want := breakerBasePause << 2; pause != want || !b.blocked(now) {
		t.Fatalf("ramp failure pause = %v (blocked=%v), want %v", pause, b.blocked(now), want)
	}
	if extra := b.done(now, r2, true, 0); extra != 0 {
		t.Fatalf("sibling in-flight failure from prior epoch re-tripped pause %v", extra)
	}
}

func TestClientGradualRecoveryUnderConcurrentLoad(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	var clockMu sync.Mutex
	clock := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		now = now.Add(d)
		clockMu.Unlock()
	}

	var failing atomic.Bool
	failing.Store(true)
	var active, wavePeak atomic.Int32
	gate := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><caps><searching><search available="yes" supportedParams="q"/></searching></caps>`))
			return
		}
		if failing.Load() {
			http.Error(w, "upstream down", http.StatusBadGateway)
			return
		}
		cur := active.Add(1)
		defer active.Add(-1)
		for {
			prev := wavePeak.Load()
			if cur <= prev || wavePeak.CompareAndSwap(prev, cur) {
				break
			}
		}
		<-gate
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel><item><title>Film.2024.1080p</title><guid>1</guid><link>http://example.invalid/1</link><enclosure url="http://example.invalid/1" length="1000" type="application/x-nzb"/></item></channel></rss>`))
	}))
	defer srv.Close()

	cfg := config.Indexer{ID: "ix", Name: "IX", URL: srv.URL, APIKey: "k", Enabled: true}
	c := New(cfg, nil, Options{HTTPClient: srv.Client(), Now: clock, SearchCacheTTL: -1, MaxPerHost: -1})

	// Trip the breaker on 3 consecutive 502s.
	for i := range breakerThreshold {
		_, err := c.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: fmt.Sprintf("Fail%d", i), Year: 2024})
		if err == nil {
			t.Fatal("expected failure while upstream is down")
		}
	}
	if _, err := c.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "Blocked", Year: 2024}); !errors.Is(err, ErrIndexerPaused) {
		t.Fatalf("expected ErrIndexerPaused while open, got %v", err)
	}

	// Upstream recovers and pause elapses: fire 6 concurrent searches for each
	// stage and verify peak concurrency ramps 1 -> 2 -> 4 -> 6.
	failing.Store(false)
	advance(breakerBasePause)

	runWave := func(label string, total, wantAdmitted int) {
		t.Helper()
		wavePeak.Store(0)
		gate = make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, total)
		for i := range total {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = c.Search(context.Background(), core.SearchRequest{
					Type:  core.TypeMovie,
					Title: fmt.Sprintf("%s-%d", label, i),
					Year:  2024,
				})
			}(i)
		}
		deadline := time.Now().Add(5 * time.Second)
		for active.Load() < int32(wantAdmitted) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(15 * time.Millisecond)
		close(gate)
		wg.Wait()
		okCount, pausedCount := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				okCount++
			case errors.Is(err, ErrIndexerPaused):
				pausedCount++
			default:
				t.Fatalf("%s unexpected error: %v", label, err)
			}
		}
		if got := int(wavePeak.Load()); got != wantAdmitted || okCount != wantAdmitted || pausedCount != total-wantAdmitted {
			t.Fatalf("%s: peak=%d ok=%d paused=%d, want %d admitted of %d", label, got, okCount, pausedCount, wantAdmitted, total)
		}
	}

	runWave("probe", 6, 1)
	runWave("ramp2", 6, 2)
	runWave("ramp4", 6, 4)
	runWave("closed", 6, 6)
}

func TestBreakerAdaptiveTimeout(t *testing.T) {
	var b breaker
	now := time.Unix(1_000_000, 0)
	const ceiling = 30 * time.Second
	for i := range minLatencySamples - 1 {
		b.done(now, breakerTicket{}, false, time.Duration(i+1)*100*time.Millisecond)
	}
	if got := b.timeout(ceiling); got != 0 {
		t.Fatalf("adaptive timeout with too few samples: %v", got)
	}
	b.done(now, breakerTicket{}, false, time.Second)
	if got := b.timeout(ceiling); got != adaptiveFloor {
		t.Fatalf("fast indexer: %v, want the floor", got)
	}
	// A slow indexer gets its p95 plus a quarter.
	for range latencySamples {
		b.done(now, breakerTicket{}, false, 16*time.Second)
	}
	if got := b.timeout(ceiling); got != 20*time.Second {
		t.Fatalf("slow indexer: %v", got)
	}
	// Past the ceiling the ordinary timeout stands.
	for range latencySamples {
		b.done(now, breakerTicket{}, false, 28*time.Second)
	}
	if got := b.timeout(ceiling); got != 0 {
		t.Fatalf("above the ceiling: %v", got)
	}
	// After a failure the next request gets the full ceiling.
	for range latencySamples {
		b.done(now, breakerTicket{}, false, time.Second)
	}
	b.done(now, breakerTicket{}, true, 0)
	if got := b.timeout(ceiling); got != 0 {
		t.Fatalf("adaptive timeout right after a failure: %v", got)
	}
}

func TestTransientFailure(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{context.DeadlineExceeded, true},
		{fmt.Errorf("x: %w", core.ErrSourceUnavailable), true},
		{ErrIndexerPaused, true},
		{&APIError{Code: 503, Description: "Service Unavailable"}, true},
		{&APIError{Code: 404, Description: "Not Found"}, false},
		{&APIError{Code: 201, Description: "Incorrect parameter"}, false},
		{apiError(429, "too many requests"), false},
		{&APIError{Code: 100, Description: "Incorrect user credentials"}, false},
		{context.Canceled, false},
		{errors.New("search request has no title or supported identifier"), false},
	} {
		if got := transientFailure(tc.err); got != tc.want {
			t.Errorf("transientFailure(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
