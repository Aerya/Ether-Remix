package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestWaitWarmDecisionNoRelease(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, nil)
	caps := defaultCaps("client")
	// No warm-up at all (never started, or still queued): the resolver is
	// asked directly.
	if got := h.m.WaitWarmDecision(context.Background(), owner, movieKey, caps, 4*time.Second); got != core.WarmNoSource {
		t.Fatalf("no release: got %v", got)
	}
	// A repeated list for the same title keeps the verdict.
	if got := h.m.WaitWarmDecision(context.Background(), owner, movieKey, caps, time.Second); got != core.WarmNoSource {
		t.Fatalf("repeat: got %v", got)
	}
	calls := h.resolver.callCount()
	// The verdict expires: a release may have appeared since.
	h.clock.Advance(pseNoReleaseTTL + time.Second)
	h.resolver.mu.Lock()
	h.resolver.candidates = []core.Release{{ID: "r1", Name: "Release.r1"}}
	h.resolver.mu.Unlock()
	if got := h.m.WaitWarmDecision(context.Background(), owner, movieKey, caps, 4*time.Second); got != core.WarmHasSource || h.resolver.callCount() != calls+1 {
		t.Fatalf("expired: got %v", got)
	}
}

func TestWaitWarmDecisionHasSource(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, []string{"r1"})
	caps := defaultCaps("client")
	if got := h.m.WaitWarmDecision(context.Background(), owner, movieKey, caps, 4*time.Second); got != core.WarmHasSource {
		t.Fatalf("release: got %v", got)
	}
}

func TestNoReleaseVerdictIgnoresUncachedAndTransientFailures(t *testing.T) {
	if !pseNoReleaseErr(core.ErrNotAvailable) {
		t.Fatal("plain not available is a verdict")
	}
	for _, err := range []error{core.ErrNotCached, core.ErrDownloadQueued, core.ErrSourceUnavailable, core.ErrForegroundOnly, errors.Join(core.ErrNotAvailable, core.ErrBudgetExhausted), context.DeadlineExceeded, nil} {
		if pseNoReleaseErr(err) {
			t.Fatalf("%v must not be a verdict", err)
		}
	}
}

func TestWaitWarmDecisionBudget(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, nil)
	h.resolver.mu.Lock()
	h.resolver.delay = time.Minute
	h.resolver.mu.Unlock()
	start := time.Now()
	if got := h.m.WaitWarmDecision(context.Background(), owner, movieKey, defaultCaps("client"), 300*time.Millisecond); got != core.WarmUnknown || time.Since(start) > 3*time.Second {
		t.Fatalf("got %v after %s", got, time.Since(start))
	}
}
