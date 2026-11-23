package newznab

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// nzbWaiters reports how many callers are attached to the in-flight
// fetch for id (0 when none is in flight).
func nzbWaiters(c *Client, id string) int {
	c.nzbFlightMu.Lock()
	defer c.nzbFlightMu.Unlock()
	if f, ok := c.nzbFlights[id]; ok {
		return f.waiters
	}
	return 0
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// blockingNZBServer serves one large NZB body per hit, holding every
// response until release is closed.
func blockingNZBServer(t *testing.T, hits *atomic.Int32, release chan struct{}) (*httptest.Server, []byte) {
	t.Helper()
	body := make([]byte, 0, (4<<20)+64)
	body = append(body, []byte(`<?xml version="1.0"?><nzb>`)...)
	body = append(body, bytes.Repeat([]byte{'A'}, 4<<20)...)
	body = append(body, []byte(`</nzb>`)...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-release:
		case <-time.After(15 * time.Second):
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, body
}

// Concurrent FetchNZB calls for the same release id (warm-up, playback,
// validation, two devices) share one indexer request and one grab. The
// handler holds the response until all N callers are attached to the
// flight. Each caller owns its returned bytes and the vault serves replays.
func TestFetchNZBConcurrentSameID(t *testing.T) {
	const n = 8
	v := &testVault{}
	var hits atomic.Int32
	release := make(chan struct{})
	srv, served := blockingNZBServer(t, &hits, release)

	cfg := testConfig(srv.URL)
	cfg.DailyGrabs = 100
	c := New(cfg, nil, Options{Vault: v})
	rel := core.Release{ID: "concurrent1", GUID: "g1", Name: "X", NZBURL: srv.URL + "/nzb?id=g1"}

	var wg sync.WaitGroup
	errs := make([]error, n)
	bodies := make([][]byte, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bodies[i], errs[i] = c.FetchNZB(context.Background(), rel)
		}(i)
	}
	waitFor(t, "all callers to join the flight", func() bool { return nzbWaiters(c, rel.ID) == n })
	close(release)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("FetchNZB %d: %v", i, err)
		}
		if !bytes.Equal(bodies[i], served) {
			t.Fatalf("FetchNZB %d returned %d bytes, not the served NZB", i, len(bodies[i]))
		}
	}
	// Each caller owns its slice: no two share a backing array, so one
	// caller modifying its NZB cannot race another still reading.
	seen := map[*byte]int{}
	for i, b := range bodies {
		if j, dup := seen[&b[0]]; dup {
			t.Fatalf("callers %d and %d share one NZB slice", j, i)
		}
		seen[&b[0]] = i
	}
	body, grabbed, err := c.fetchNZBReporting(context.Background(), rel)
	if err != nil || grabbed || !bytes.Equal(body, served) {
		t.Fatal("vault replay failed", err)
	}
}

// The caller that started the shared fetch gives up (its viewer left):
// it returns its own context error at once, while the fetch carries on
// for the callers still waiting and still lands in the vault.
func TestFetchNZBLeaderCancelOthersSucceed(t *testing.T) {
	const n = 4
	v := &testVault{}
	var hits atomic.Int32
	release := make(chan struct{})
	srv, served := blockingNZBServer(t, &hits, release)
	c := New(testConfig(srv.URL), nil, Options{Vault: v})
	rel := core.Release{ID: "cancel1", GUID: "g1", Name: "X", NZBURL: srv.URL + "/nzb?id=g1"}

	leaderCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	leaderErr := make(chan error, 1)
	go func() {
		_, err := c.FetchNZB(leaderCtx, rel)
		leaderErr <- err
	}()
	waitFor(t, "the leader's request", func() bool { return hits.Load() == 1 })

	var wg sync.WaitGroup
	errs := make([]error, n)
	bodies := make([][]byte, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bodies[i], errs[i] = c.FetchNZB(context.Background(), rel)
		}(i)
	}
	waitFor(t, "followers to join the flight", func() bool { return nzbWaiters(c, rel.ID) == n+1 })

	cancel()
	select {
	case err := <-leaderErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled leader err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled leader did not return while the fetch was still in flight")
	}
	close(release)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("follower %d: %v", i, err)
		}
		if !bytes.Equal(bodies[i], served) {
			t.Fatalf("follower %d got %d bytes, not the served NZB", i, len(bodies[i]))
		}
	}
}

// A failed shared fetch is handed to every caller in the flight but not
// remembered: the next call goes back to the indexer.
func TestFetchNZBErrorNotCachedPastFlight(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`<?xml version="1.0"?><nzb></nzb>`))
	}))
	defer srv.Close()
	c := New(testConfig(srv.URL), nil, Options{Vault: &testVault{}})
	rel := core.Release{ID: "err1", GUID: "g1", Name: "X", NZBURL: srv.URL + "/nzb?id=g1"}
	if _, err := c.FetchNZB(context.Background(), rel); err == nil {
		t.Fatal("first fetch: want the indexer error")
	}
	if _, err := c.FetchNZB(context.Background(), rel); err != nil {
		t.Fatalf("second fetch: %v (an error must not outlive its flight)", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server hits = %d, want 2", got)
	}
}

// A background check (warm-up) runs on a reduced grab share. When it is
// refused, a foreground play that joined its flight must not inherit the
// refusal: it fetches on its own budget.
func TestFetchNZBForegroundRetriesBackgroundBudgetRefusal(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><nzb></nzb>`))
	}))
	defer srv.Close()
	c := New(testConfig(srv.URL), nil, Options{Vault: &testVault{}})
	rel := core.Release{ID: "bg1", GUID: "g1", Name: "X", NZBURL: srv.URL + "/nzb?id=g1"}

	gate := make(chan struct{})
	bgCtx := core.WithBackgroundBudget(context.Background(), func(context.Context, string, string, int64) error {
		<-gate
		return core.ErrBudgetExhausted
	})
	bgErr := make(chan error, 1)
	go func() {
		_, err := c.FetchNZB(bgCtx, rel)
		bgErr <- err
	}()
	waitFor(t, "the background flight", func() bool { return nzbWaiters(c, rel.ID) == 1 })
	fgErr := make(chan error, 1)
	go func() {
		_, err := c.FetchNZB(context.Background(), rel)
		fgErr <- err
	}()
	waitFor(t, "the foreground caller to join", func() bool { return nzbWaiters(c, rel.ID) == 2 })
	close(gate)
	if err := <-bgErr; !errors.Is(err, core.ErrBudgetExhausted) {
		t.Fatalf("background err = %v, want ErrBudgetExhausted", err)
	}
	if err := <-fgErr; err != nil {
		t.Fatalf("foreground err = %v, want its own successful fetch", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("server hits = %d, want 1", got)
	}
}

// A losing health check's NZB fetch is cancelled once its last caller
// leaves: it is not left running detached to finish a grab nobody reads.
func TestAbandonedNZBFetchIsCancelled(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer srv.Close()
	c := New(testConfig(srv.URL), nil, Options{HTTPClient: srv.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.FetchNZB(ctx, core.Release{ID: "loser", Name: "loser", NZBURL: srv.URL + "/nzb"})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("fetch error %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the abandoned NZB request kept running")
	}
}
