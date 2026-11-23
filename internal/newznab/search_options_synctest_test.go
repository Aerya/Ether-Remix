package newznab

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type funcBackend struct {
	search func(ctx context.Context, req core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error)
}

func (f funcBackend) Search(ctx context.Context, req core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	return f.search(ctx, req, reserve)
}

func (f funcBackend) FetchNZB(context.Context, core.Release, func(context.Context, string) error) ([]byte, error) {
	return nil, nil
}

func (f funcBackend) ResolveTorrent(_ context.Context, r core.Release, _ func(context.Context, string) error) (core.Release, error) {
	return r, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestSearchWithOptionsCeilingTimeoutTripsBreakerAndSkipsPaused verifies that
// a hung built-in (Cardigann-style) indexer whose detached background search
// times out at the search ceiling trips the shared circuit breaker after
// breakerThreshold failures, is subsequently skipped with status:"skipped" and
// reason:"paused_backoff" on both SearchWithOptions and streaming SearchWith
// without polluting timingHistory, and recovers on a half-open probe once
// backoff expires.
func TestSearchWithOptionsCeilingTimeoutTripsBreakerAndSkipsPaused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()

		var hungCalls atomic.Int32
		var hungHealthy atomic.Bool
		hungDone := make(chan struct{}, 8)

		fastBackend := funcBackend{
			search: func(ctx context.Context, req core.SearchRequest, _ func(context.Context, string) error) ([]core.Release, error) {
				return []core.Release{{
					ID:        "fast-" + req.Title,
					GUID:      "fast-" + req.Title,
					Name:      req.Title + " [FLAC]",
					MagnetURL: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					Protocol:  core.ProtocolTorrent,
				}}, nil
			},
		}
		hungBackend := funcBackend{
			search: func(ctx context.Context, req core.SearchRequest, _ func(context.Context, string) error) ([]core.Release, error) {
				hungCalls.Add(1)
				if hungHealthy.Load() {
					return []core.Release{{
						ID:        "hung-" + req.Title,
						GUID:      "hung-" + req.Title,
						Name:      req.Title + " [Recovered FLAC]",
						MagnetURL: "magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
						Protocol:  core.ProtocolTorrent,
					}}, nil
				}
				<-ctx.Done()
				hungDone <- struct{}{}
				return nil, ctx.Err()
			},
		}

		cFast := config.Indexer{ID: "fast", Name: "Fast", Kind: config.IndexerKindCardigann, Protocol: "torrent", Enabled: true}
		cHung := config.Indexer{ID: "hung", Name: "LimeTorrents", Kind: config.IndexerKindCardigann, Protocol: "torrent", Enabled: true}

		m := NewManager(nil, []config.Indexer{cFast, cHung}, Options{
			Vault: &testVault{},
			Backend: func(cfg config.Indexer) (Backend, error) {
				if cfg.ID == "fast" {
					return fastBackend, nil
				}
				return hungBackend, nil
			},
		})
		defer m.Close()

		// 3 viewer-bounded searches detach hung onto the 30 s background ceiling.
		// Each returns fast results at WaitTimeout with hung pending, then hung
		// hits the 30 s ceiling and records a breaker failure.
		for i := range breakerThreshold {
			started := time.Now()
			res, err := m.SearchWithOptions(ctx, core.SearchRequest{Title: fmt.Sprintf("Track-%d", i)}, SearchOptions{
				WaitTimeout:       3250 * time.Millisecond,
				BackgroundTimeout: defaultHTTPTimeout,
			})
			if err != nil {
				t.Fatalf("search %d: %v", i, err)
			}
			if got := time.Since(started); got != 3250*time.Millisecond {
				t.Fatalf("search %d elapsed = %s, want 3.25s", i, got)
			}
			if len(res.Releases) != 1 {
				t.Fatalf("search %d releases = %d, want 1", i, len(res.Releases))
			}
			byID := statusByID(res.Indexers)
			if byID["fast"].Status != core.IndexerStatusAnswered || byID["hung"].Status != core.IndexerStatusPending {
				t.Fatalf("search %d statuses = %+v, want fast=answered hung=pending", i, res.Indexers)
			}
			if len(res.Pending) != 1 || res.Pending[0].IndexerID != "hung" {
				t.Fatalf("search %d pending = %+v, want [hung]", i, res.Pending)
			}
			<-hungDone
			synctest.Wait()
		}
		if got := hungCalls.Load(); got != breakerThreshold {
			t.Fatalf("hung calls before backoff = %d, want %d", got, breakerThreshold)
		}

		// Breaker is now open: the next viewer search skips hung immediately
		// without waiting for WaitTimeout or touching hung's backend.
		started := time.Now()
		res, err := m.SearchWithOptions(ctx, core.SearchRequest{Title: "Track-Skipped"}, SearchOptions{
			WaitTimeout:       3250 * time.Millisecond,
			BackgroundTimeout: defaultHTTPTimeout,
		})
		if err != nil {
			t.Fatalf("skipped search: %v", err)
		}
		if got := time.Since(started); got != 0 {
			t.Fatalf("skipped search waited %s, want immediate return once fast answers", got)
		}
		if got := hungCalls.Load(); got != breakerThreshold {
			t.Fatalf("hung was called while in backoff: calls=%d, want %d", got, breakerThreshold)
		}
		if len(res.Releases) != 1 {
			t.Fatalf("skipped search releases = %d, want 1", len(res.Releases))
		}
		if len(res.Pending) != 0 {
			t.Fatalf("skipped search pending = %+v, want empty", res.Pending)
		}
		byID := statusByID(res.Indexers)
		if byID["hung"].Status != core.IndexerStatusSkipped || byID["hung"].Reason != core.IndexerSkipReasonPaused || byID["hung"].MS != 0 {
			t.Fatalf("hung status = %+v, want skipped with reason %q and ms 0", byID["hung"], core.IndexerSkipReasonPaused)
		}

		// timingHistory must retain lastError:"timeout" and must not record 0 ms
		// samples for backoff skips.
		listed := statusByID(m.EnabledIndexers())
		if listed["hung"].LastError != "timeout" {
			t.Fatalf("EnabledIndexers hung lastError = %q, want timeout", listed["hung"].LastError)
		}
		if listed["hung"].LastMS == nil || *listed["hung"].LastMS < defaultHTTPTimeout.Milliseconds() {
			t.Fatalf("EnabledIndexers hung lastMs = %v, want >= %d", listed["hung"].LastMS, defaultHTTPTimeout.Milliseconds())
		}

		// Streaming SearchWith also reports the paused indexer as skipped with reason.
		var observedMu sync.Mutex
		var observed []core.IndexerSearchStatus
		obsCtx := core.WithIndexerStatusObserver(ctx, func(st core.IndexerSearchStatus) {
			observedMu.Lock()
			observed = append(observed, st)
			observedMu.Unlock()
		})
		if _, err := m.SearchWith(obsCtx, core.SearchRequest{Title: "Track-StreamSkip"}, nil); err != nil {
			t.Fatalf("SearchWith while paused: %v", err)
		}
		obsByID := statusByID(observed)
		if obsByID["hung"].Status != core.IndexerStatusSkipped || obsByID["hung"].Reason != core.IndexerSkipReasonPaused {
			t.Fatalf("SearchWith observed hung = %+v, want skipped (%s)", obsByID["hung"], core.IndexerSkipReasonPaused)
		}

		// Advance past breakerBasePause: one half-open probe is admitted and recovers.
		time.Sleep(breakerBasePause + time.Second)
		hungHealthy.Store(true)
		rec, err := m.SearchWithOptions(ctx, core.SearchRequest{Title: "Track-Recovered"}, SearchOptions{
			WaitTimeout:       3250 * time.Millisecond,
			BackgroundTimeout: defaultHTTPTimeout,
		})
		if err != nil {
			t.Fatalf("recovery search: %v", err)
		}
		if got := hungCalls.Load(); got != breakerThreshold+1 {
			t.Fatalf("hung calls after probe = %d, want %d", got, breakerThreshold+1)
		}
		recByID := statusByID(rec.Indexers)
		if recByID["hung"].Status != core.IndexerStatusAnswered || recByID["hung"].Results != 1 {
			t.Fatalf("recovered hung status = %+v, want answered with 1 result", recByID["hung"])
		}
		if len(rec.Releases) != 2 {
			t.Fatalf("recovered releases = %d, want 2", len(rec.Releases))
		}
	})
}

// TestSearchWithOptionsDeadCapsServerTripsBreaker verifies that a completely
// dead Newznab indexer that never answers even t=caps still trips the breaker
// at the search ceiling and is skipped with reason:"paused_backoff" afterward.
func TestSearchWithOptionsDeadCapsServerTripsBreaker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()

		var hungReqs atomic.Int32
		transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "127.0.0.1:18001" {
				body := `<?xml version="1.0"?><caps/>`
				if req.URL.Query().Get("t") != "caps" {
					body = rssEnvelope(rssItem("Fast.Release.FLAC", "f-1", "http://127.0.0.1:18001/1.nzb", ""))
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/xml"}},
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}
			hungReqs.Add(1)
			<-req.Context().Done()
			return nil, req.Context().Err()
		})

		cFast := config.Indexer{ID: "fast", Name: "Fast", URL: "http://127.0.0.1:18001", APIKey: "k1", Enabled: true}
		cHung := config.Indexer{ID: "dead", Name: "Dead", URL: "http://127.0.0.1:18002", APIKey: "k2", Enabled: true}
		m := NewManager(nil, []config.Indexer{cFast, cHung}, Options{})
		for _, cl := range m.Clients() {
			cl.guarded = &http.Client{Transport: transport}
		}
		defer m.Close()

		for i := range breakerThreshold {
			res, err := m.SearchWithOptions(ctx, core.SearchRequest{Title: fmt.Sprintf("Dead-%d", i)}, SearchOptions{
				WaitTimeout:       defaultHTTPTimeout,
				BackgroundTimeout: defaultHTTPTimeout,
			})
			if err != nil {
				t.Fatalf("ceiling search %d: %v", i, err)
			}
			byID := statusByID(res.Indexers)
			if byID["dead"].Status != core.IndexerStatusTimedOut {
				t.Fatalf("ceiling search %d dead status = %+v, want timed_out", i, byID["dead"])
			}
		}
		beforeSkip := hungReqs.Load()
		if beforeSkip != breakerThreshold {
			t.Fatalf("dead server requests = %d, want %d (caps cached as failed after first timeout)", beforeSkip, breakerThreshold)
		}

		// 4th search skips dead immediately without touching HTTP.
		started := time.Now()
		res, err := m.SearchWithOptions(ctx, core.SearchRequest{Title: "Dead-AfterBackoff"}, SearchOptions{
			WaitTimeout:       3250 * time.Millisecond,
			BackgroundTimeout: defaultHTTPTimeout,
		})
		if err != nil {
			t.Fatalf("post-backoff search: %v", err)
		}
		if got := time.Since(started); got != 0 {
			t.Fatalf("post-backoff search waited %s, want 0", got)
		}
		if got := hungReqs.Load(); got != beforeSkip {
			t.Fatalf("dead server received request during backoff: %d -> %d", beforeSkip, got)
		}
		byID := statusByID(res.Indexers)
		if byID["dead"].Status != core.IndexerStatusSkipped || byID["dead"].Reason != core.IndexerSkipReasonPaused {
			t.Fatalf("post-backoff dead status = %+v, want skipped (%s)", byID["dead"], core.IndexerSkipReasonPaused)
		}
	})
}
