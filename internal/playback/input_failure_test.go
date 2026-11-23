package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type recordingFailoverHLS struct {
	*fakeHLS
	mu      sync.Mutex
	input   string
	swaps   []string
	failOn  map[string]error
	skipErr error
	skips   []string
}

func (h *recordingFailoverHLS) SwapInput(_ context.Context, input string, _ core.MediaInfo, _ time.Duration) error {
	h.mu.Lock()
	h.input = input
	h.swaps = append(h.swaps, input)
	h.mu.Unlock()
	return nil
}

func (h *recordingFailoverHLS) SwapInputBuffered(ctx context.Context, input string, info core.MediaInfo, at time.Duration) error {
	return h.SwapInput(ctx, input, info, at)
}

func (h *recordingFailoverHLS) CurrentSwapInput() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.input
}

func (h *recordingFailoverHLS) WaitForReplacementOutput(_ context.Context, input string) error {
	h.mu.Lock()
	err := h.failOn[input]
	h.mu.Unlock()
	return err
}

func (h *recordingFailoverHLS) SkipDamagedSegment(_ context.Context, input string, _ core.MediaInfo, _ time.Duration) error {
	h.mu.Lock()
	h.skips = append(h.skips, input)
	err := h.skipErr
	h.mu.Unlock()
	return err
}

func TestEncoderFailureRecoversOnlyObservedSource(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 10000, time.Minute), mkv("B", 10000, time.Minute)}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "encoder-failure")
	hls := h.tr.addHLS(ps.ID)
	old := h.m.InputURL(ps.ID)
	if err := h.m.FailoverInput(t.Context(), ps.ID, old, 12*time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-hls.swapC:
	default:
		t.Fatal("encoder was not moved to replacement")
	}
	if err := h.m.FailoverInput(t.Context(), ps.ID, old, 12*time.Second); err != nil {
		t.Fatal(err)
	}
	current, ok := h.m.Session(ps.ID)
	if !ok || current.Resolution.Release.ID != "B" || current.Failovers != 1 {
		t.Fatalf("stale failure displaced new source: %+v", current)
	}
	if h.resolver.isBad("A") {
		t.Fatal("encoder failure permanently blacklisted a source")
	}
}

func TestExhaustedEncoderRecoveryClosesPlayback(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 10000, time.Minute)}, []string{"A"})
	ps := h.prepare(owner, movieKey, "encoder-exhausted")
	h.tr.addHLS(ps.ID)
	if err := h.m.FailoverInput(t.Context(), ps.ID, h.m.InputURL(ps.ID), 0); err == nil {
		t.Fatal("exhausted recovery succeeded")
	}
	if _, ok := h.m.Session(ps.ID); ok {
		t.Fatal("exhausted session retained phantom playback")
	}
	// Retirement cancels background index/read work, but the source closes
	// only after those readers release their generation references.
	eventually(t, "exhausted source closed", func() bool { return h.engine.sources[0].closed.Load() })
}

// Issue #694: when FailoverInput tries an alternate that also fails
// WaitForReplacementOutput, playback must never roll back to the already-failed
// source (which caused an infinite ping-pong loop). It records the failed
// candidate, advances to the next untried alternate, or falls back to bounded
// damaged-segment skipping / clean session stop.
func TestEncoderFailoverDoesNotRollbackOrLoop(t *testing.T) {
	t.Run("advances-past-bad-alternate-to-working-third", func(t *testing.T) {
		var lookup *synchronizedHLSLookup
		h := newHarness(t, []*fakeRelease{
			mkv("A", 100_000, time.Minute),
			mkv("B", 100_000, time.Minute),
			mkv("C", 100_000, time.Minute),
		}, []string{"A", "B", "C"}, func(h *harness, _ *Options) {
			h.testEngine = &contextEngine{h.engine}
			lookup = &synchronizedHLSLookup{Transcoder: h.tr}
			h.testTranscode = lookup
		})
		ps := h.prepare(owner, movieKey, "no-rollback-advance")
		oldURL := h.m.InputURL(ps.ID)
		hls := &recordingFailoverHLS{
			fakeHLS: h.tr.addHLS(ps.ID),
			input:   oldURL,
			failOn:  map[string]error{},
			skipErr: errors.New("should not skip when C works"),
		}
		lookup.set(hls)
		// Fail whichever first replacement URL is swapped in (B); allow C.
		firstSwapFailed := false
		hlsFailWrap := &firstFailHLS{recordingFailoverHLS: hls, failFirst: &firstSwapFailed}
		lookup.set(hlsFailWrap)

		if err := h.m.FailoverInput(t.Context(), ps.ID, oldURL, 12*time.Second); err != nil {
			t.Fatalf("expected recovery on third release, got: %v", err)
		}
		hls.mu.Lock()
		swaps := append([]string(nil), hls.swaps...)
		skips := append([]string(nil), hls.skips...)
		hls.mu.Unlock()
		if len(swaps) != 2 {
			t.Fatalf("swaps=%d (%v), want exactly 2 (B then C, no rollback to A)", len(swaps), swaps)
		}
		for _, u := range swaps {
			if u == oldURL {
				t.Fatalf("rolled back to already-failed source %q: swaps=%v", oldURL, swaps)
			}
		}
		if len(skips) != 0 {
			t.Fatalf("skipped segment despite working alternate: %v", skips)
		}
		cur, ok := h.m.Session(ps.ID)
		if !ok || cur.Resolution.Release.ID != "C" {
			t.Fatalf("current session = %+v, want C", cur)
		}
	})

	t.Run("stops-cleanly-when-all-alternates-fail-and-skip-fails", func(t *testing.T) {
		var lookup *synchronizedHLSLookup
		h := newHarness(t, []*fakeRelease{
			mkv("A", 100_000, time.Minute),
			mkv("B", 100_000, time.Minute),
		}, []string{"A", "B"}, func(h *harness, _ *Options) {
			h.testEngine = &contextEngine{h.engine}
			lookup = &synchronizedHLSLookup{Transcoder: h.tr}
			h.testTranscode = lookup
		})
		ps := h.prepare(owner, movieKey, "no-rollback-stop")
		oldURL := h.m.InputURL(ps.ID)
		hls := &recordingFailoverHLS{
			fakeHLS: h.tr.addHLS(ps.ID),
			input:   oldURL,
			skipErr: errors.New("skip exhausted"),
		}
		hlsFailAll := &alwaysFailHLS{recordingFailoverHLS: hls}
		lookup.set(hlsFailAll)

		if err := h.m.FailoverInput(t.Context(), ps.ID, oldURL, 12*time.Second); err == nil {
			t.Fatal("expected failover failure when all alternates and skip fail")
		}
		hls.mu.Lock()
		swaps := append([]string(nil), hls.swaps...)
		skips := append([]string(nil), hls.skips...)
		hls.mu.Unlock()
		if len(swaps) != 1 || swaps[0] == oldURL {
			t.Fatalf("expected single swap to B without rollback to A, got %v", swaps)
		}
		if len(skips) != 1 || skips[0] != oldURL {
			t.Fatalf("expected one SkipDamagedSegment attempt on A after B failed, got %v", skips)
		}
		if _, ok := h.m.Session(ps.ID); ok {
			t.Fatal("exhausted session remained active")
		}
	})

	t.Run("skips-damaged-segment-when-no-alternate-recovers", func(t *testing.T) {
		var lookup *synchronizedHLSLookup
		h := newHarness(t, []*fakeRelease{
			mkv("A", 100_000, time.Minute),
			mkv("B", 100_000, time.Minute),
		}, []string{"A", "B"}, func(h *harness, _ *Options) {
			h.testEngine = &contextEngine{h.engine}
			lookup = &synchronizedHLSLookup{Transcoder: h.tr}
			h.testTranscode = lookup
		})
		ps := h.prepare(owner, movieKey, "no-rollback-skip")
		oldURL := h.m.InputURL(ps.ID)
		hls := &recordingFailoverHLS{
			fakeHLS: h.tr.addHLS(ps.ID),
			input:   oldURL,
		}
		hlsFailAll := &alwaysFailHLS{recordingFailoverHLS: hls}
		lookup.set(hlsFailAll)

		if err := h.m.FailoverInput(t.Context(), ps.ID, oldURL, 12*time.Second); err != nil {
			t.Fatalf("expected skip recovery when B failed, got: %v", err)
		}
		hls.mu.Lock()
		swaps := append([]string(nil), hls.swaps...)
		skips := append([]string(nil), hls.skips...)
		hls.mu.Unlock()
		if len(swaps) != 1 || swaps[0] == oldURL {
			t.Fatalf("expected single swap to B without rollback, got %v", swaps)
		}
		if len(skips) != 1 || skips[0] != oldURL {
			t.Fatalf("expected SkipDamagedSegment on A, got %v", skips)
		}
		if _, ok := h.m.Session(ps.ID); !ok {
			t.Fatal("session closed despite successful damaged-segment skip")
		}
	})
}

type firstFailHLS struct {
	*recordingFailoverHLS
	failFirst *bool
}

func (h *firstFailHLS) WaitForReplacementOutput(_ context.Context, _ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !*h.failFirst {
		*h.failFirst = true
		return errors.New("transcode: replacement input failed")
	}
	return nil
}

type alwaysFailHLS struct {
	*recordingFailoverHLS
}

func (h *alwaysFailHLS) WaitForReplacementOutput(_ context.Context, _ string) error {
	return errors.New("transcode: replacement input failed")
}
