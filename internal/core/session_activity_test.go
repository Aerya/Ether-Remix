package core

import (
	"testing"
	"time"
)

func TestEvaluateSessionLiveness(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 22, 0, 0, 0, time.UTC)

	t.Run("direct play window and idle transition", func(t *testing.T) {
		in := SessionLivenessInput{
			StartedAt:   t0,
			OpenedAt:    t0,
			LastMediaAt: t0.Add(5 * time.Second),
			MediaBytes:  64 * 1024,
		}
		got := EvaluateSessionLiveness(in, t0.Add(5*time.Second+PlayingEvidenceWindowDirect))
		if got.State != SessionLivenessPlaying || !got.Active() || !got.Visible() || !got.CountsForLimit() || got.Expired {
			t.Fatalf("at direct window edge: %+v", got)
		}
		got = EvaluateSessionLiveness(in, t0.Add(5*time.Second+PlayingEvidenceWindowDirect+time.Second))
		if got.State != SessionLivenessIdle || got.Active() || got.Visible() || got.CountsForLimit() || got.Expired {
			t.Fatalf("past direct window: %+v", got)
		}
		got = EvaluateSessionLiveness(in, t0.Add(5*time.Second+AbandonedSessionTimeout+time.Second))
		if !got.Expired || got.Active() || got.Visible() {
			t.Fatalf("past idle timeout: %+v", got)
		}
	})

	t.Run("hls segment window", func(t *testing.T) {
		in := SessionLivenessInput{
			StartedAt:   t0,
			OpenedAt:    t0,
			IsHLS:       true,
			LastMediaAt: t0.Add(2 * time.Second),
			MediaBytes:  1,
		}
		if got := EvaluateSessionLiveness(in, t0.Add(2*time.Second+PlayingEvidenceWindowHLS)); !got.Active() || got.State != SessionLivenessPlaying {
			t.Fatalf("at HLS window edge: %+v", got)
		}
		if got := EvaluateSessionLiveness(in, t0.Add(2*time.Second+PlayingEvidenceWindowHLS+time.Second)); got.Active() || got.Visible() || got.State != SessionLivenessIdle {
			t.Fatalf("past HLS window: %+v", got)
		}
	})

	t.Run("live tv window", func(t *testing.T) {
		in := SessionLivenessInput{
			StartedAt:   t0,
			OpenedAt:    t0,
			IsLive:      true,
			LastMediaAt: t0.Add(time.Second),
			MediaBytes:  4096,
		}
		if got := EvaluateSessionLiveness(in, t0.Add(time.Second+PlayingEvidenceWindowLive)); !got.Active() {
			t.Fatalf("at live window edge: %+v", got)
		}
		if got := EvaluateSessionLiveness(in, t0.Add(time.Second+PlayingEvidenceWindowLive+time.Second)); got.Active() || got.Visible() {
			t.Fatalf("past live window: %+v", got)
		}
	})

	t.Run("long in-flight read never expires", func(t *testing.T) {
		in := SessionLivenessInput{
			StartedAt:   t0,
			OpenedAt:    t0,
			ActiveReads: 1,
		}
		got := EvaluateSessionLiveness(in, t0.Add(time.Hour))
		if got.State != SessionLivenessPlaying || !got.Active() || !got.Visible() || got.Expired {
			t.Fatalf("in-flight direct read: %+v", got)
		}
	})

	t.Run("never received position and no media bytes expires at 2m", func(t *testing.T) {
		in := SessionLivenessInput{
			StartedAt:  t0,
			OpenedAt:   t0,
			LastActive: t0.Add(119 * time.Second), // simulated SSE keepalive
		}
		// Immediately after open: starting (not visible, not active for restart/budget).
		if got := EvaluateSessionLiveness(in, t0.Add(5*time.Second)); got.State != SessionLivenessStarting || got.Visible() || got.Active() || !got.CountsForLimit() {
			t.Fatalf("at 5s starting: %+v", got)
		}
		// Past startup grace: idle, not counting for limits either.
		if got := EvaluateSessionLiveness(in, t0.Add(StartupGraceWindow+time.Second)); got.State != SessionLivenessIdle || got.Visible() || got.Active() || got.CountsForLimit() || got.Expired {
			t.Fatalf("past startup grace: %+v", got)
		}
		// Past 2m: expired despite LastActive keepalive.
		if got := EvaluateSessionLiveness(in, t0.Add(AbandonedSessionTimeout+time.Second)); !got.Expired || got.Visible() || got.Active() {
			t.Fatalf("past abandoned timeout: %+v", got)
		}
	})

	t.Run("explicit paused is visible as Paused, excluded from active and limits, expires after retention", func(t *testing.T) {
		in := SessionLivenessInput{
			StartedAt:      t0,
			OpenedAt:       t0,
			ViewerState:    "paused",
			PausedAt:       t0.Add(10 * time.Second),
			LastProgressAt: t0.Add(10 * time.Second),
			HasPosition:    true,
			LastMediaAt:    t0.Add(9 * time.Second),
			MediaBytes:     1024 * 1024,
		}
		got := EvaluateSessionLiveness(in, t0.Add(27*time.Minute))
		if got.State != SessionLivenessPaused || !got.Visible() || got.Active() || got.CountsForLimit() || got.Expired {
			t.Fatalf("at 27m paused: %+v", got)
		}
		got = EvaluateSessionLiveness(in, t0.Add(10*time.Second+PausedSessionRetention+time.Second))
		if !got.Expired || got.Visible() || got.Active() || got.CountsForLimit() {
			t.Fatalf("past paused retention: %+v", got)
		}
	})

	t.Run("custom pause retention (default 0, short, long, sub-idle floor)", func(t *testing.T) {
		base := SessionLivenessInput{
			StartedAt:      t0,
			OpenedAt:       t0,
			ViewerState:    "paused",
			PausedAt:       t0,
			LastProgressAt: t0,
			HasPosition:    true,
			IdleTimeout:    2 * time.Minute,
		}
		cases := []struct {
			name       string
			retention  time.Duration
			stillAlive time.Duration
			mustExpire time.Duration
		}{
			{"default-zero-keeps-30m", 0, 30 * time.Minute, 30*time.Minute + time.Second},
			{"short-5m", 5 * time.Minute, 5 * time.Minute, 5*time.Minute + time.Second},
			{"above-30m-60m", 60 * time.Minute, 35 * time.Minute, 60*time.Minute + time.Second},
			{"below-idle-floored-to-2m", time.Minute, 2 * time.Minute, 2*time.Minute + time.Second},
		}
		for _, tc := range cases {
			in := base
			in.PauseRetention = tc.retention
			if got := EvaluateSessionLiveness(in, t0.Add(tc.stillAlive)); got.Expired || got.State != SessionLivenessPaused {
				t.Fatalf("%s at %v: got %+v, want paused & alive", tc.name, tc.stillAlive, got)
			}
			if got := EvaluateSessionLiveness(in, t0.Add(tc.mustExpire)); !got.Expired {
				t.Fatalf("%s at %v: got %+v, want expired", tc.name, tc.mustExpire, got)
			}
		}
	})

	t.Run("playing timeline keeps session playing without reads", func(t *testing.T) {
		in := SessionLivenessInput{
			StartedAt:      t0,
			OpenedAt:       t0,
			ViewerState:    "playing",
			LastProgressAt: t0.Add(30 * time.Second),
			HasPosition:    true,
		}
		if got := EvaluateSessionLiveness(in, t0.Add(30*time.Second+PlayingEvidenceWindowDirect)); got.State != SessionLivenessPlaying || !got.Active() || !got.Visible() {
			t.Fatalf("recent playing timeline: %+v", got)
		}
		// Silent past window becomes idle (never falsely "paused").
		if got := EvaluateSessionLiveness(in, t0.Add(30*time.Second+PlayingEvidenceWindowDirect+time.Second)); got.State != SessionLivenessIdle || got.Visible() || got.Active() {
			t.Fatalf("silent past playing timeline: %+v", got)
		}
	})
}
