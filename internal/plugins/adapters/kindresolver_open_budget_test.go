package adapters

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type blockingTestIndexer struct {
	searchFn func(ctx context.Context, req core.SearchRequest) ([]core.Release, error)
	fetchFn  func(ctx context.Context, rel core.Release) ([]byte, error)
}

func (b *blockingTestIndexer) Name() string { return "blocking-test-indexer" }

func (b *blockingTestIndexer) Search(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
	if b.searchFn != nil {
		return b.searchFn(ctx, req)
	}
	return nil, nil
}

func (b *blockingTestIndexer) FetchNZB(ctx context.Context, rel core.Release) ([]byte, error) {
	return b.fetchFn(ctx, rel)
}

type blockingTestEngine struct {
	openFn func(ctx context.Context, releaseID string, nzbData []byte) (core.MediaSource, error)
}

func (b *blockingTestEngine) Open(ctx context.Context, releaseID string, nzbData []byte) (core.MediaSource, error) {
	return b.openFn(ctx, releaseID, nzbData)
}

func (b *blockingTestEngine) Prefetch(context.Context, string, []byte, int64, int64) error {
	return nil
}

type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestOpenPreparedReleaseHonorsFetchAndEngineBudgetsAndLogs(t *testing.T) {
	t.Parallel()

	t.Run("fetch_nzb_bounded_and_logged", func(t *testing.T) {
		var logs syncLogBuffer
		r := NewKindResolver(KindResolverDeps{
			Indexer: &blockingTestIndexer{
				fetchFn: func(ctx context.Context, _ core.Release) ([]byte, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				},
			},
			Engine: &blockingTestEngine{
				openFn: func(context.Context, string, []byte) (core.MediaSource, error) {
					t.Fatal("engine.Open should not be reached when FetchNZB times out")
					return nil, nil
				},
			},
			NZBFetchTimeout: 40 * time.Millisecond,
			Logger:          slog.New(slog.NewTextHandler(&logs, nil)),
		})

		_, err := r.openPreparedRelease(context.Background(), core.Release{
			ID:       "rel-fetch-hang",
			Name:     "Album One",
			Protocol: core.ProtocolUsenet,
			NZBURL:   "https://indexer.example/nzb/1",
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
		out := logs.String()
		if !strings.Contains(out, "opening release") || !strings.Contains(out, "stage=fetch_nzb") {
			t.Fatalf("expected opening and fetch_nzb failure logs, got:\n%s", out)
		}
	})

	t.Run("engine_open_bounded_and_logged", func(t *testing.T) {
		var logs syncLogBuffer
		r := NewKindResolver(KindResolverDeps{
			Indexer: &blockingTestIndexer{
				fetchFn: func(context.Context, core.Release) ([]byte, error) {
					return []byte("<nzb></nzb>"), nil
				},
			},
			Engine: &blockingTestEngine{
				openFn: func(ctx context.Context, _ string, _ []byte) (core.MediaSource, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				},
			},
			NZBFetchTimeout:   time.Second,
			EngineOpenTimeout: 40 * time.Millisecond,
			Logger:            slog.New(slog.NewTextHandler(&logs, nil)),
		})

		_, err := r.openPreparedRelease(context.Background(), core.Release{
			ID:       "rel-engine-hang",
			Name:     "Album Two",
			Protocol: core.ProtocolUsenet,
			NZBURL:   "https://indexer.example/nzb/2",
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
		out := logs.String()
		if !strings.Contains(out, "opening release") || !strings.Contains(out, "stage=engine_open") {
			t.Fatalf("expected opening and engine_open failure logs, got:\n%s", out)
		}
	})
}

func TestKindResolverInvalidatePluginCancelsFlightsAndClearsCache(t *testing.T) {
	t.Parallel()
	r := NewKindResolver(KindResolverDeps{})

	refA := core.NewItemRef("plugin-a", "music", "album-1")
	refB := core.NewItemRef("plugin-b", "music", "album-2")
	fkA := flightKeyForItem(refA, nil)
	fkB := flightKeyForItem(refB, nil)

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()

	r.flightMu.Lock()
	r.flights[fkA] = &resolveFlight{done: make(chan struct{}), cancel: cancelA}
	r.flights[fkB] = &resolveFlight{done: make(chan struct{}), cancel: cancelB}
	r.flightMu.Unlock()

	r.storeCandidates(fkA, []core.PlayableSource{{ID: "src-a", Pattern: "release"}})
	r.storeCandidates(fkB, []core.PlayableSource{{ID: "src-b", Pattern: "release"}})

	r.InvalidatePlugin("plugin-a")

	if ctxA.Err() == nil {
		t.Fatal("plugin-a flight context was not cancelled on InvalidatePlugin")
	}
	if ctxB.Err() != nil {
		t.Fatal("unrelated plugin-b flight context was cancelled")
	}
	r.flightMu.Lock()
	_, hasFlightA := r.flights[fkA]
	_, hasFlightB := r.flights[fkB]
	r.flightMu.Unlock()
	if hasFlightA || !hasFlightB {
		t.Fatalf("flights after InvalidatePlugin: hasA=%v hasB=%v, want false/true", hasFlightA, hasFlightB)
	}
	if _, ok := r.cachedCandidates(fkA); ok {
		t.Fatal("plugin-a candidate cache was not cleared")
	}
	if _, ok := r.cachedCandidates(fkB); !ok {
		t.Fatal("unrelated plugin-b candidate cache was cleared")
	}
}

func TestResolveSourcesSpeculativeBudgetCooloffDoesNotBlockPlayback(t *testing.T) {
	t.Parallel()
	now := time.Now()
	var searches int
	r := NewKindResolver(KindResolverDeps{
		Now: func() time.Time { return now },
		Indexer: &blockingTestIndexer{
			searchFn: func(ctx context.Context, _ core.SearchRequest) ([]core.Release, error) {
				searches++
				if core.SpeculativeIndexerWork(ctx) {
					return nil, core.ErrBudgetExhausted
				}
				return []core.Release{{ID: "r1", Name: "Album FLAC", NZBURL: "https://indexer.example/nzb/1"}}, nil
			},
		},
	})

	detailCtx := core.WithIndexerPurpose(context.Background(), core.IndexerPurposeDetail)
	if _, err := r.ResolveSources(detailCtx, "music", core.SourceQuery{Kind: "indexer_search", Query: "Album 1"}); err != nil {
		t.Fatalf("first detail search err = %v", err)
	}
	if searches != 1 {
		t.Fatalf("after first detail search: searches=%d, want 1", searches)
	}

	// Second speculative detail search during cooloff must not hit the indexer again.
	if _, err := r.ResolveSources(detailCtx, "music", core.SourceQuery{Kind: "indexer_search", Query: "Album 2"}); err != nil {
		t.Fatalf("second detail search err = %v", err)
	}
	if searches != 1 {
		t.Fatalf("second detail search during cooloff hit indexer: searches=%d, want 1", searches)
	}

	// Foreground playback search must bypass the speculative cooloff immediately.
	playCtx := core.WithIndexerPurpose(context.Background(), core.IndexerPurposePlayback)
	cands, err := r.ResolveSources(playCtx, "music", core.SourceQuery{Kind: "indexer_search", Query: "Album 2"})
	if err != nil || len(cands) != 1 {
		t.Fatalf("playback search = (%d cands, %v), want (1, nil)", len(cands), err)
	}
	if searches != 2 {
		t.Fatalf("playback search count = %d, want 2", searches)
	}
}
