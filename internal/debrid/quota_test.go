package debrid

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func quotaEntry() *entry {
	return &entry{cfg: config.DebridProvider{ID: "ad", Name: "AllDebrid", Enabled: true}, sem: newLimiter(16)}
}

func TestQuotaMessageClassified(t *testing.T) {
	for _, msg := range []string{"You have reached your daily download limit", "Daily quota exceeded"} {
		for _, status := range []int{502, 403, 429} {
			err := statusError("alldebrid", status, msg)
			if !errors.Is(err, ErrQuotaExceeded) || errors.Is(err, ErrAuth) {
				t.Errorf("%d %q: %v", status, msg, err)
			}
		}
	}
	if err := statusError("alldebrid", 502, "bad gateway"); errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("plain 502 taken for a quota: %v", err)
	}
	if err := adError(200, "SOME_CODE", "You have reached your daily download limit"); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("adError: %v", err)
	}
}

func TestQuotaPausesAnHourThenSingleProbe(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	e := quotaEntry()
	qerr := fmt.Errorf("alldebrid: HTTP 502: daily limit: %w", ErrQuotaExceeded)
	ev := e.observe(now, qerr)
	if ev == nil || ev.State != stateQuota || e.state != stateQuota || !e.until.Equal(now.Add(quotaPause)) {
		t.Fatalf("pause: ev=%+v state=%q until=%v", ev, e.state, e.until)
	}
	err := e.blocked(now.Add(59 * time.Minute))
	if err == nil || !errors.Is(err, ErrQuotaExceeded) || !strings.Contains(err.Error(), "11:00") {
		t.Fatalf("blocked during pause: %v", err)
	}

	// At expiry exactly one request is let through; the others wait.
	at := now.Add(quotaPause)
	if err := e.blocked(at); err != nil {
		t.Fatalf("blocked is read-only and lets the test through: %v", err)
	}
	var passed atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if probe, err := e.begin(context.Background(), at); err == nil && probe {
				passed.Add(1)
			}
		}()
	}
	wg.Wait()
	if passed.Load() != 1 {
		t.Fatalf("%d concurrent requests passed as the test, want 1", passed.Load())
	}
	if e.blocked(at.Add(time.Second)) == nil {
		t.Fatal("blocked does not show the test in progress")
	}

	// The test hits the quota again: another hour.
	ev = e.observe(at.Add(time.Second), qerr)
	if ev == nil || !e.until.Equal(at.Add(time.Second+quotaPause)) || e.blocked(at.Add(time.Minute)) == nil {
		t.Fatalf("second pause: ev=%+v until=%v", ev, e.until)
	}

	// Next window: the test succeeds and the provider is normal again.
	at = e.until
	if probe, err := e.begin(context.Background(), at); err != nil || !probe {
		t.Fatalf("no test request after the second pause: %v", err)
	}
	if _, err := e.begin(context.Background(), at); err == nil {
		t.Fatal("second concurrent request passed during the test")
	}
	ev = e.observe(at.Add(time.Second), nil)
	if ev == nil || ev.State != "" || e.state != "" {
		t.Fatalf("recovery: ev=%+v state=%q", ev, e.state)
	}
	if e.blocked(at.Add(2*time.Second)) != nil || e.blocked(at.Add(2*time.Second)) != nil {
		t.Fatal("still blocked after recovery")
	}
}

func TestQuotaProbeLeaseAndOtherFailure(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	e := quotaEntry()
	e.observe(now, ErrQuotaExceeded)
	at := now.Add(quotaPause)
	ctx := context.Background()
	if probe, err := e.begin(ctx, at); err != nil || !probe {
		t.Fatalf("test not granted: %v", err)
	}
	if _, err := e.begin(ctx, at.Add(time.Second)); err == nil {
		t.Fatal("single test not enforced")
	}
	// A test whose caller never answered does not block the provider forever.
	late := at.Add(quotaProbeLease)
	if probe, err := e.begin(ctx, late); err != nil || !probe {
		t.Fatalf("stuck test never expires: %v", err)
	}
	// A non-quota failure of the test keeps the quota state and allows a new test.
	e.observe(late.Add(time.Second), errors.New("network down"))
	e.finish(true)
	if e.state != stateQuota || e.blocked(late.Add(2*time.Second)) != nil {
		t.Fatalf("state=%q", e.state)
	}
	// A test that made no call (finish without observe) frees the reservation.
	if probe, err := e.begin(ctx, late.Add(3*time.Second)); err != nil || !probe {
		t.Fatalf("new test refused: %v", err)
	}
	e.finish(true)
	if probe, err := e.begin(ctx, late.Add(4*time.Second)); err != nil || !probe {
		t.Fatalf("freed test not granted again: %v", err)
	}
}

func TestQuotaPauseReachesOtherNodes(t *testing.T) {
	db := store.OpenTest(t)
	hub := clusterbus.NewHub()
	clkA, clkB := newClock(), newClock()
	prov := newFakeProvider("realdebrid", newMediaServer(t, 1<<10), movieFiles(1<<10))
	a := clusterNodeSvc(t, db, hub.Node("a"), prov, clkA.now)
	b := clusterNodeSvc(t, db, hub.Node("b"), prov, clkB.now)
	ea, eb := a.active()[0], b.active()[0]
	a.observe(ea, ErrQuotaExceeded)
	if st := b.Stats()[0]; st.State != stateQuota {
		t.Fatalf("B state %+v", st)
	}
	if err := eb.blocked(clkB.now().Add(59 * time.Minute)); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("B not paused for the hour: %v", err)
	}
	eb.applyRemote(clkB.now(), backoffEvent{Provider: "rd", State: stateQuota, Until: clkB.now().Add(48 * time.Hour).UnixMilli()})
	if eb.until.After(clkB.now().Add(quotaPause)) {
		t.Fatal("remote quota pause not capped")
	}
}

// quotaService is a Service over fake providers with an injected clock.
func quotaService(t *testing.T, clk *testClock, kinds ...string) (*Service, map[string]*fakeProvider) {
	t.Helper()
	media := newMediaServer(t, 1<<20)
	provs := map[string]*fakeProvider{}
	cfg := config.Debrid{CachedOnly: true, RemoveUncached: true}
	for i, k := range kinds {
		id := fmt.Sprintf("%s-%d", k, i)
		provs[id] = newFakeProvider(k, media, movieFiles(1<<20))
		provs[id].cached[testHash] = true
		cfg.Providers = append(cfg.Providers, config.DebridProvider{ID: id, Name: id, Kind: k, APIKey: "k", Priority: i, Enabled: true})
	}
	s := NewService(cfg, Options{Now: clk.now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewProvider: func(c config.DebridProvider, _ ProviderOptions) (Provider, error) { return provs[c.ID], nil }})
	return s, provs
}

// A cache check on a provider without bulk verification (AllDebrid) makes no
// call: it must not use up the single test request after a quota pause.
func TestCacheCheckDoesNotSpendTheQuotaTest(t *testing.T) {
	clk := newClock()
	s, provs := quotaService(t, clk, "alldebrid")
	ad := provs["alldebrid-0"]
	s.observe(s.active()[0], ErrQuotaExceeded)
	clk.add(quotaPause + time.Second)
	for range 3 {
		s.CheckCached(context.Background(), []string{testHash})
	}
	if _, err := s.Acquire(context.Background(), torrentRelease("a")); err != nil {
		t.Fatalf("the test request did not go through: %v", err)
	}
	if ad.callCount("acquire") != 1 {
		t.Fatalf("provider asked %d times, want the test request", ad.callCount("acquire"))
	}
	if st := s.Stats()[0]; st.State == stateQuota {
		t.Fatalf("still in quota after a successful test: %+v", st)
	}
}

func TestQuotaStaysClassifiedOnlyWhenEveryProviderIsOut(t *testing.T) {
	clk := newClock()
	s, provs := quotaService(t, clk, "alldebrid", "torbox")
	provs["alldebrid-0"].quotaFail = true
	provs["torbox-1"].failErr = errors.New("torbox: HTTP 503: maintenance")
	_, err := s.Acquire(context.Background(), torrentRelease("a"))
	if err == nil || errors.Is(err, ErrQuotaExceeded) || !strings.Contains(err.Error(), "daily download limit") {
		t.Fatalf("quota on one provider only must stay text: %v", err)
	}
	// Both accounts out: the cause is classifiable.
	clk.add(quotaPause + time.Second)
	provs["torbox-1"].failErr = nil
	provs["torbox-1"].quotaFail = true
	_, err = s.Acquire(context.Background(), torrentRelease("b"))
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("every provider out of quota must be classifiable: %v", err)
	}
}

func TestLastErrorClearedOnSuccess(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	e := quotaEntry()
	e.observe(now, fmt.Errorf("alldebrid: HTTP 502: daily limit: %w", ErrQuotaExceeded))
	if e.lastError == "" {
		t.Fatal("error not recorded")
	}
	// A call already in flight that succeeds during the pause keeps it.
	e.observe(now.Add(time.Minute), nil)
	if e.lastError == "" || e.state != stateQuota {
		t.Fatalf("pause ended early: state=%q err=%q", e.state, e.lastError)
	}
	e.observe(now.Add(quotaPause+time.Second), nil)
	if e.lastError != "" || e.state != "" {
		t.Fatalf("stale error kept: state=%q err=%q", e.state, e.lastError)
	}
	// Release-level outcomes are not a success of the account.
	e.observe(now.Add(2*quotaPause), fmt.Errorf("x: %w", ErrQuotaExceeded))
	e.observe(now.Add(3*quotaPause), core.ErrNotCached)
	if e.lastError == "" {
		t.Fatal("a miss must not clear the last error")
	}
}
