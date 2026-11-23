package debrid

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

type quotaFakeProvider struct {
	Provider
	acc     Account
	acqErr  error
	callErr error
}

func (q *quotaFakeProvider) Account(ctx context.Context) (Account, error) {
	if q.callErr != nil {
		return Account{}, q.callErr
	}
	return q.acc, nil
}

func (q *quotaFakeProvider) Acquire(ctx context.Context, req AcquireRequest) (*Acquired, error) {
	if q.acqErr != nil {
		return nil, q.acqErr
	}
	return q.Provider.Acquire(ctx, req)
}

func TestDebridEmitsProviderEvents(t *testing.T) {
	ctx := context.Background()
	bus, rec := events.NewTestBus(t)
	reg := events.NewBuiltinRegistry()

	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	media := newMediaServer(t, 4<<20)
	baseFake := newFakeProvider(config.DebridRealDebrid, media, []File{{ID: "1", Path: "The.Matrix.1999.2160p.mkv", Size: 4 << 20}})
	const cachedHash = "0123456789abcdef0123456789abcdef01234567"
	baseFake.cached[cachedHash] = true

	wrapped := &quotaFakeProvider{
		Provider: baseFake,
		acc: Account{
			User:    "owner",
			Premium: true,
			Expires: now.Add(48 * time.Hour),
		},
	}

	pc := config.DebridProvider{
		ID:      "rd-1",
		Name:    "Real-Debrid",
		Kind:    config.DebridRealDebrid,
		APIKey:  "key-1",
		Enabled: true,
	}
	svc := NewService(config.Debrid{
		CachedOnly: false,
		Providers:  []config.DebridProvider{pc},
	}, Options{
		Now:    clock,
		Events: bus,
		NewProvider: func(cfg config.DebridProvider, opts ProviderOptions) (Provider, error) {
			return wrapped, nil
		},
	})

	// 1. Account expiring within 7 days -> provider.quota.warning.
	if err := svc.Test(ctx, pc); err != nil {
		t.Fatalf("Test: %v", err)
	}
	evWarn := rec.WaitFor(t, events.EventProviderQuotaWarning, time.Second)
	if err := reg.ValidatePayload(evWarn.Type, evWarn.Payload); err != nil {
		t.Fatalf("quota warning payload invalid: %v", err)
	}
	warn, err := events.DecodePayload[events.ProviderQuotaWarningPayload](evWarn)
	if err != nil {
		t.Fatal(err)
	}
	if warn.ProviderID != "rd-1" || warn.Metric != "premium_subscription" || warn.PercentUsed <= 0 {
		t.Fatalf("unexpected quota warning payload: %+v", warn)
	}

	// 2. Cached release acquisition -> provider.debrid.cached.
	rel := core.Release{
		ID:       "rel-1",
		Name:     "The.Matrix.1999.2160p.mkv",
		Protocol: core.ProtocolTorrent,
		InfoHash: cachedHash,
		TitleIDs: core.IDs{TMDB: 603},
	}
	if _, err := svc.Acquire(ctx, rel); err != nil {
		t.Fatalf("Acquire cached: %v", err)
	}
	evCached := rec.WaitFor(t, events.EventProviderDebridCached, time.Second)
	if err := reg.ValidatePayload(evCached.Type, evCached.Payload); err != nil {
		t.Fatalf("debrid cached payload invalid: %v", err)
	}
	cached, err := events.DecodePayload[events.ProviderDebridCachedPayload](evCached)
	if err != nil {
		t.Fatal(err)
	}
	if cached.ProviderID != "rd-1" || cached.InfoHash != cachedHash || cached.TitleKey != "tmdb:603" {
		t.Fatalf("unexpected debrid cached payload: %+v", cached)
	}

	// 3. Active download limit hit -> provider.quota.exhausted & provider.debrid.failed.
	wrapped.acqErr = fmt.Errorf("slots full: %w", ErrDownloadLimit)
	relLimit := core.Release{
		ID:       "rel-2",
		Name:     "Limit.Movie.2020.mkv",
		Protocol: core.ProtocolTorrent,
		InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		TitleIDs: core.IDs{TMDB: 604},
	}
	if _, err := svc.Acquire(ctx, relLimit); !errors.Is(err, core.ErrDownloadLimit) {
		t.Fatalf("expected ErrDownloadLimit, got %v", err)
	}
	evExhausted := rec.WaitFor(t, events.EventProviderQuotaExhausted, time.Second)
	if err := reg.ValidatePayload(evExhausted.Type, evExhausted.Payload); err != nil {
		t.Fatalf("quota exhausted payload invalid: %v", err)
	}
	evFailed := rec.WaitFor(t, events.EventProviderDebridFailed, time.Second)
	if err := reg.ValidatePayload(evFailed.Type, evFailed.Payload); err != nil {
		t.Fatalf("debrid failed payload invalid: %v", err)
	}

	// 4. Outage + recovery + auth rejected via observe().
	e := svc.entryByID("rd-1")
	svc.observe(e, &rateLimitError{retryAfter: 45 * time.Second})
	evOutage := rec.WaitFor(t, events.EventProviderServiceOutage, time.Second)
	if err := reg.ValidatePayload(evOutage.Type, evOutage.Payload); err != nil {
		t.Fatalf("outage payload invalid: %v", err)
	}

	now = now.Add(time.Minute)
	svc.observe(e, nil)
	evRec := rec.WaitFor(t, events.EventProviderServiceRecovered, time.Second)
	if err := reg.ValidatePayload(evRec.Type, evRec.Payload); err != nil {
		t.Fatalf("recovered payload invalid: %v", err)
	}

	svc.observe(e, fmt.Errorf("bad token: %w", ErrAuth))
	evAuth := rec.WaitFor(t, events.EventProviderAuthRejected, time.Second)
	if err := reg.ValidatePayload(evAuth.Type, evAuth.Payload); err != nil {
		t.Fatalf("auth rejected payload invalid: %v", err)
	}

	// 5. Connection saturation on entry limiter -> provider.connections.saturated.
	e.sem.resize(1)
	if err := e.sem.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- e.sem.acquire(wctx) }()
	evSat := rec.WaitFor(t, events.EventProviderConnectionsSaturated, time.Second)
	cancel()
	<-done
	e.sem.release()
	if err := reg.ValidatePayload(evSat.Type, evSat.Payload); err != nil {
		t.Fatalf("saturated payload invalid: %v", err)
	}
}
