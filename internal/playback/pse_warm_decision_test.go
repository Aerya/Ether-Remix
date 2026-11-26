package playback

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestWaitWarmDecisionNoRelease(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, nil)
	caps := defaultCaps("client")
	if got := h.m.WaitWarmDecision(context.Background(), owner, movieKey, caps, time.Second); got != core.WarmUnknown {
		t.Fatalf("no warm-up: got %v", got)
	}
	if !h.m.Prewarm(context.Background(), owner, movieKey, caps, core.PrewarmOptions{Reason: core.PrewarmDetail}) {
		t.Fatal("warm skipped")
	}
	if got := h.m.WaitWarmDecision(context.Background(), owner, movieKey, caps, 4*time.Second); got != core.WarmNoSource {
		t.Fatalf("no release: got %v", got)
	}
	// A repeated list for the same title keeps the verdict.
	if got := h.m.WaitWarmDecision(context.Background(), owner, movieKey, caps, time.Second); got != core.WarmNoSource {
		t.Fatalf("repeat: got %v", got)
	}
	// The verdict expires: a release may have appeared since.
	h.clock.Advance(pseNoReleaseTTL + time.Second)
	if got := h.m.WaitWarmDecision(context.Background(), owner, movieKey, caps, time.Second); got != core.WarmUnknown {
		t.Fatalf("expired: got %v", got)
	}
}

func TestWaitWarmDecisionHasSource(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, []string{"r1"})
	caps := defaultCaps("client")
	if !h.m.Prewarm(context.Background(), owner, movieKey, caps, core.PrewarmOptions{Reason: core.PrewarmDetail}) {
		t.Fatal("warm skipped")
	}
	if got := h.m.WaitWarmDecision(context.Background(), owner, movieKey, caps, 4*time.Second); got != core.WarmHasSource {
		t.Fatalf("release: got %v", got)
	}
}
