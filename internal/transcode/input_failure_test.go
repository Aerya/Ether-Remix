package transcode

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestFailedInputCannotRestartAcrossClientRetries(t *testing.T) {
	fr := newFakeRunner()
	fr.failStart = func(args []string) (string, bool) {
		return "input unavailable", argValue(args, "-i") == "http://127.0.0.1:1/source/a"
	}
	failed := make(chan string, 8)
	m := newTestManager(t, fr, config.Transcode{}, WithInputFailureHandler(func(id, input string, at time.Duration, err error) {
		failed <- input
	}))
	req := remuxRequest(t, "bounded-input", time.Hour)
	req.Decision.Container = ContainerFMP4
	s := startSession(t, m, req)
	if r := get(t, s, 0); r.Code != http.StatusBadGateway {
		t.Fatalf("failed media: %d", r.Code)
	}
	select {
	case input := <-failed:
		if input != req.InputURL {
			t.Fatalf("wrong failed input: %q", input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input failure not reported")
	}
	starts := fr.startCount()
	if starts != 2 {
		t.Fatalf("starts=%d, want initial + one probe retry", starts)
	}
	for range 5 {
		for _, segment := range []int{0, InitSegment} {
			if r := get(t, s, segment); r.Code != http.StatusBadGateway {
				t.Fatalf("retry status=%d", r.Code)
			}
		}
	}
	if fr.startCount() != starts {
		t.Fatal("client retries restarted exhausted input")
	}
	select {
	case <-failed:
		t.Fatal("duplicate failure callback")
	default:
	}
	if err := s.SwapInput(context.Background(), "http://127.0.0.1:1/source/b", req.Info, 0); err != nil {
		t.Fatal(err)
	}
	if r := get(t, s, 0); r.Code != http.StatusOK {
		t.Fatalf("replacement cannot recover: %d", r.Code)
	}
}

func TestRetryBackoffExponentialCap(t *testing.T) {
	base, maxDelay := 200*time.Millisecond, 5*time.Second
	want := []time.Duration{
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
		3200 * time.Millisecond,
		5 * time.Second,
		5 * time.Second,
	}
	for i, w := range want {
		if got := retryBackoff(i+1, base, maxDelay); got != w {
			t.Fatalf("retryBackoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

// Issue #694: a failed input URL cannot be swapped back in, and repeated
// failures across swaps on the same segment or session hit hard caps instead
// of relaunching ffmpeg indefinitely.
func TestFailedInputSwapRejectedAndHardCapped(t *testing.T) {
	fr := newFakeRunner()
	fr.failStart = func([]string) (string, bool) {
		return "Invalid NAL unit size\nError splitting the input into NAL units", true
	}
	failed := make(chan string, 32)
	m := newTestManager(t, fr, config.Transcode{HWAccel: "none"},
		withRetryBackoff(time.Millisecond, 5*time.Millisecond),
		WithInputFailureHandler(func(_, input string, _ time.Duration, _ error) { failed <- input }),
	)
	req := remuxRequest(t, "hard-cap", time.Hour)
	req.Decision.Container = ContainerMatroska
	s := startSession(t, m, req)

	// Wait for initial input to exhaust (initial + 1 retry = 2 starts).
	select {
	case input := <-failed:
		if input != req.InputURL {
			t.Fatalf("failed input=%q, want %q", input, req.InputURL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initial input failure not reported")
	}

	// Swapping back to the already-failed input must be rejected.
	if err := s.SwapInput(context.Background(), req.InputURL, req.Info, 0); err == nil {
		t.Fatal("SwapInput allowed reusing failed input URL")
	}

	// Swapping across distinct failing inputs on segment 0 must hit the
	// per-segment hard cap (maxSegmentFailures = 8).
	var capErr error
	for i := 0; i < maxSessionFailures; i++ {
		url := fmt.Sprintf("http://127.0.0.1:1/source/alt-%d", i)
		err := s.SwapInput(context.Background(), url, req.Info, 0)
		if err != nil {
			capErr = err
			break
		}
		select {
		case <-failed:
		case <-time.After(2 * time.Second):
			t.Fatalf("alt-%d failure not reported", i)
		}
	}
	if !errors.Is(capErr, core.ErrEncoderExhausted) {
		t.Fatalf("expected ErrEncoderExhausted on cap, got %v", capErr)
	}
	if got := fr.startCount(); got > maxSegmentFailures {
		t.Fatalf("started %d processes on segment 0, exceeds cap %d", got, maxSegmentFailures)
	}
}

// Issue #694: a localized mid-stream corrupt H.264 segment can skip ahead past
// the damaged range (publishing a valid placeholder and resuming at seg+1),
// while persistent corruption stops cleanly after maxDamagedSkipsIncident.
func TestSkipDamagedSegmentBounded(t *testing.T) {
	t.Run("recovers-past-localized-damage", func(t *testing.T) {
		fr := newFakeRunner()
		fr.failStart = func(args []string) (string, bool) {
			if ss, err := strconv.ParseFloat(argValue(args, "-ss"), 64); err == nil && ss >= 12 && ss < 20 {
				return "Invalid NAL unit size\nmatroska: no Cluster Timestamp in 00000.mkv", true
			}
			return "", false
		}
		fr.failAfter = 3 // produces segments 0, 1, 2 then fails before 3 (12s)
		failed := make(chan time.Duration, 4)
		m := newTestManager(t, fr, config.Transcode{HWAccel: "none"},
			withRetryBackoff(time.Millisecond, 5*time.Millisecond),
			WithInputFailureHandler(func(_, _ string, at time.Duration, _ error) { failed <- at }),
		)
		req := remuxRequest(t, "skip-ok", time.Hour)
		req.Decision.Container = ContainerMPEGTS
		s := startSession(t, m, req)

		var failAt time.Duration
		select {
		case failAt = <-failed:
		case <-time.After(2 * time.Second):
			t.Fatal("mid-stream failure not reported")
		}
		// Simulate corrupt H.264 stderr on the mid-stream failure.
		s.mu.Lock()
		s.lastFailStderr = "Invalid NAL unit size\nDecoding error: Invalid data found when processing input"
		s.mu.Unlock()
		fr.mu.Lock()
		fr.failAfter = 0
		fr.mu.Unlock()

		// Segments 3 (12s) and 4 (16s) are corrupt; segment 5 (20s) recovers.
		if err := s.SkipDamagedSegment(context.Background(), req.InputURL, req.Info, failAt); err != nil {
			t.Fatalf("SkipDamagedSegment failed: %v", err)
		}
		for _, seg := range []int{3, 4, 5} {
			if r := get(t, s, seg); r.Code != http.StatusOK {
				t.Fatalf("segment %d after skip: %d", seg, r.Code)
			}
		}
	})

	t.Run("bounded-on-persistent-corruption", func(t *testing.T) {
		fr := newFakeRunner()
		fr.failStart = func(args []string) (string, bool) {
			if ss, err := strconv.ParseFloat(argValue(args, "-ss"), 64); err == nil && ss >= 12 {
				return "Invalid NAL unit size\nError splitting the input into NAL units", true
			}
			return "", false
		}
		fr.failAfter = 3
		failed := make(chan time.Duration, 4)
		m := newTestManager(t, fr, config.Transcode{HWAccel: "none"},
			withRetryBackoff(time.Millisecond, 5*time.Millisecond),
			WithInputFailureHandler(func(_, _ string, at time.Duration, _ error) { failed <- at }),
		)
		req := remuxRequest(t, "skip-cap", time.Hour)
		req.Decision.Container = ContainerMPEGTS
		s := startSession(t, m, req)

		var failAt time.Duration
		select {
		case failAt = <-failed:
		case <-time.After(2 * time.Second):
			t.Fatal("mid-stream failure not reported")
		}
		s.mu.Lock()
		s.lastFailStderr = "Invalid NAL unit size"
		s.mu.Unlock()

		err := s.SkipDamagedSegment(context.Background(), req.InputURL, req.Info, failAt)
		if !errors.Is(err, core.ErrEncoderExhausted) {
			t.Fatalf("expected ErrEncoderExhausted on persistent damage, got %v", err)
		}
		// Initial run (1) + maxDamagedSkipsIncident (3) probe runs = 4.
		if got := fr.startCount(); got != 1+maxDamagedSkipsIncident {
			t.Fatalf("starts=%d, want %d", got, 1+maxDamagedSkipsIncident)
		}
	})
}

// Issue #694: when the client has stopped or gone idle, a failing run must end
// the session cleanly instead of retrying ffmpeg or triggering failover.
func TestClientGoneEndsSessionCleanlyOnFailure(t *testing.T) {
	fr := newFakeRunner()
	fr.failStart = func([]string) (string, bool) {
		return "Invalid NAL unit size", true
	}
	failed := make(chan string, 4)
	m := newTestManager(t, fr, config.Transcode{HWAccel: "none"},
		WithIdleTimeout(50*time.Millisecond),
		withRetryBackoff(10*time.Millisecond, 20*time.Millisecond),
		WithInputFailureHandler(func(_, input string, _ time.Duration, _ error) { failed <- input }),
	)
	req := remuxRequest(t, "client-gone", time.Hour)
	s := startSession(t, m, req)
	s.PlaybackState("stopped")

	waitFor(t, "session retired after client stop", func() bool {
		_, ok := m.Session(s.ID())
		return !ok
	})
	select {
	case input := <-failed:
		t.Fatalf("onInputFailure fired after client stopped: %q", input)
	default:
	}
	if got := fr.startCount(); got > 2 {
		t.Fatalf("stopped session retried ffmpeg %d times", got)
	}
}
