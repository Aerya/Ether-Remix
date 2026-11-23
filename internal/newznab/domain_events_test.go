package newznab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

func TestIndexerEmitsAllEvents(t *testing.T) {
	ctx := context.Background()
	bus, rec := events.NewTestBus(t)
	reg := events.NewBuiltinRegistry()

	var status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	var retryAfter atomic.Value
	retryAfter.Store("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<caps><searching><search available="yes"/><movie-search available="yes"/></searching></caps>`))
			return
		}
		if ra, _ := retryAfter.Load().(string); ra != "" {
			w.Header().Set("Retry-After", ra)
		}
		code := int(status.Load())
		if code != http.StatusOK {
			w.WriteHeader(code)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<rss version="2.0"><channel></channel></rss>`))
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	nowFn := func() time.Time { return time.Unix(0, clock.Load()).UTC() }

	cl := New(config.Indexer{
		ID:         "idx-geek",
		Name:       "NZBGeek",
		URL:        srv.URL,
		APIKey:     "secret-key",
		Enabled:    true,
		DailyLimit: 10,
	}, nil, Options{
		HTTPClient:     srv.Client(),
		SearchCacheTTL: -1,
		Now:            nowFn,
		Events:         bus,
	})

	// 1. Trigger 3 consecutive 503 failures -> indexer.request.failed & indexer.service.outage.
	for i := range breakerThreshold {
		_, err := cl.Search(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "matrix " + string(rune('a'+i))})
		if err == nil {
			t.Fatalf("expected failure on attempt %d", i+1)
		}
	}
	evFail := rec.WaitFor(t, events.EventIndexerRequestFailed, time.Second)
	if err := reg.ValidatePayload(evFail.Type, evFail.Payload); err != nil {
		t.Fatalf("request failed payload invalid: %v", err)
	}
	evOutage := rec.WaitFor(t, events.EventIndexerServiceOutage, time.Second)
	if err := reg.ValidatePayload(evOutage.Type, evOutage.Payload); err != nil {
		t.Fatalf("outage payload invalid: %v", err)
	}
	outage, err := events.DecodePayload[events.IndexerServiceOutagePayload](evOutage)
	if err != nil {
		t.Fatal(err)
	}
	if outage.IndexerID != "idx-geek" || outage.ConsecutiveFailures < breakerThreshold {
		t.Fatalf("unexpected outage payload: %+v", outage)
	}

	// 2. Advance clock past breaker pause and succeed on probe -> indexer.service.recovered.
	clock.Store(now.Add(breakerBasePause + time.Minute).UnixNano())
	status.Store(http.StatusOK)
	if _, err := cl.Search(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "probe"}); err != nil {
		t.Fatalf("probe search failed: %v", err)
	}
	evRecovered := rec.WaitFor(t, events.EventIndexerServiceRecovered, time.Second)
	if err := reg.ValidatePayload(evRecovered.Type, evRecovered.Payload); err != nil {
		t.Fatalf("recovered payload invalid: %v", err)
	}
	recovered, err := events.DecodePayload[events.IndexerServiceRecoveredPayload](evRecovered)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.IndexerID != "idx-geek" || recovered.OutageDurationMS <= 0 {
		t.Fatalf("unexpected recovered payload: %+v", recovered)
	}

	// 3. Consume hits up to 80% warning and 100% exhaustion -> indexer.budget.warning & indexer.budget.exhausted.
	for i := range 12 {
		err := cl.reserve(ctx, "hit")
		if i < 10 && err != nil {
			// Earlier searches spent hits too; stop once exhausted.
			break
		}
	}
	evWarn := rec.WaitFor(t, events.EventIndexerBudgetWarning, time.Second)
	if err := reg.ValidatePayload(evWarn.Type, evWarn.Payload); err != nil {
		t.Fatalf("budget warning payload invalid: %v", err)
	}
	warn, err := events.DecodePayload[events.IndexerBudgetWarningPayload](evWarn)
	if err != nil {
		t.Fatal(err)
	}
	if warn.Metric != "hits" || warn.Limit != 10 || warn.PercentUsed < 80 {
		t.Fatalf("unexpected budget warning payload: %+v", warn)
	}

	// One more reserve guarantees exhaustion.
	_ = cl.reserve(ctx, "hit")
	evExhausted := rec.WaitFor(t, events.EventIndexerBudgetExhausted, time.Second)
	if err := reg.ValidatePayload(evExhausted.Type, evExhausted.Payload); err != nil {
		t.Fatalf("budget exhausted payload invalid: %v", err)
	}
	exhausted, err := events.DecodePayload[events.IndexerBudgetExhaustedPayload](evExhausted)
	if err != nil {
		t.Fatal(err)
	}
	if exhausted.Metric != "hits" || exhausted.Limit != 10 || exhausted.ResetsAt == nil {
		t.Fatalf("unexpected budget exhausted payload: %+v", exhausted)
	}

	// 4. Advance to next day and trigger HTTP 429 -> indexer.rate.limited.
	clock.Store(now.Add(25 * time.Hour).UnixNano())
	status.Store(http.StatusTooManyRequests)
	retryAfter.Store("60")
	_, _ = cl.Search(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "rate-limited"})
	evRL := rec.WaitFor(t, events.EventIndexerRateLimited, time.Second)
	if err := reg.ValidatePayload(evRL.Type, evRL.Payload); err != nil {
		t.Fatalf("rate limited payload invalid: %v", err)
	}
	rl, err := events.DecodePayload[events.IndexerRateLimitedPayload](evRL)
	if err != nil {
		t.Fatal(err)
	}
	if rl.IndexerID != "idx-geek" || rl.StatusCode != 429 || rl.RetryAfterMS < 59_000 {
		t.Fatalf("unexpected rate limited payload: %+v", rl)
	}
}
