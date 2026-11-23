package resolver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestRefillAndSpeculativeSearchPauseAcrossTitlesWhenBudgetExhausted(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{t: time.Now()}
	idx := &refillIndexer{
		started: make(chan refillSearch, 8),
		err:     core.ErrBudgetExhausted,
	}
	s := New(idx, nil, nil, testCfg(), Options{Now: clock.now})
	defer s.Close()

	// Plugin keys must never enter built-in search cache refill.
	pluginTitle := &core.Title{
		Key:   core.PluginKey(9001),
		Type:  core.TypeMovie,
		Title: "Plugin Album",
		Year:  2024,
	}
	if s.RefillSearchCache(pluginTitle) != nil || idx.searches.Load() != 0 {
		t.Fatalf("plugin title triggered built-in refill: searches=%d", idx.searches.Load())
	}

	// First built-in title hits ErrBudgetExhausted under detail purpose.
	t1 := matrixTitle()
	waitRefill(t, s.RefillSearchCache(t1))
	<-idx.started
	if got := idx.searches.Load(); got != 1 {
		t.Fatalf("after first budget exhaustion searches = %d, want 1", got)
	}

	// Different titles browsed during the cooloff must NOT fan out to the indexer.
	t2 := *t1
	t2.Key = core.MovieKey(604)
	t2.Title = "The Matrix Reloaded"
	t2.Year = 2003
	t2.IDs.TMDB = 604
	t3 := *t1
	t3.Key = core.MovieKey(605)
	t3.Title = "The Matrix Revolutions"
	t3.Year = 2003
	t3.IDs.TMDB = 605
	if s.RefillSearchCache(&t2) != nil || s.RefillSearchCache(&t3) != nil {
		t.Fatal("expected refills for other titles to be suppressed during budget cooloff")
	}
	if got := idx.searches.Load(); got != 1 {
		t.Fatalf("browsing other titles during cooloff ran %d searches, want 1", got)
	}

	// Speculative detail searchCached also short-circuits without hitting the indexer.
	detailCtx := core.WithIndexerPurpose(context.Background(), core.IndexerPurposeDetail)
	if _, _, err := s.searchCached(detailCtx, searchRequestFor(&t2), false); !errors.Is(err, core.ErrBudgetExhausted) {
		t.Fatalf("speculative searchCached err = %v, want ErrBudgetExhausted", err)
	}
	if got := idx.searches.Load(); got != 1 {
		t.Fatalf("speculative searchCached during cooloff hit indexer (%d searches)", got)
	}

	// Foreground playback is NEVER blocked by the speculative cooloff.
	playCtx := core.WithIndexerPurpose(context.Background(), core.IndexerPurposePlayback)
	_, _, _ = s.searchCached(playCtx, searchRequestFor(&t2), false)
	<-idx.started
	if got := idx.searches.Load(); got != 2 {
		t.Fatalf("foreground playback did not reach indexer during speculative cooloff: searches=%d", got)
	}

	// Once refillRetryDelay elapses, detail discovery probes once again.
	clock.add(refillRetryDelay)
	waitRefill(t, s.RefillSearchCache(&t2))
	<-idx.started
	if got := idx.searches.Load(); got != 3 {
		t.Fatalf("after cooloff searches = %d, want 3", got)
	}
}
