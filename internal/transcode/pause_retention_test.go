package transcode

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type recordingDeadlineWriter struct {
	*httptest.ResponseRecorder
	mu        sync.Mutex
	deadlines []time.Time
}

func (w *recordingDeadlineWriter) SetWriteDeadline(d time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadlines = append(w.deadlines, d)
	return nil
}

func (w *recordingDeadlineWriter) FlushError() error { return nil }

func TestTranscodePausedRetention(t *testing.T) {
	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		retention  time.Duration
		stillAlive time.Duration
		mustClose  time.Duration
	}{
		{"default-zero-30m", 0, 29 * time.Minute, 30*time.Minute + time.Second},
		{"short-5m", 5 * time.Minute, 4 * time.Minute, 5*time.Minute + time.Second},
		{"above-30m-60m", 60 * time.Minute, 35 * time.Minute, 60*time.Minute + time.Second},
		{"sub-idle-floor-2m", time.Minute, 90 * time.Second, 2*time.Minute + time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := t0
			fr := newFakeRunner()
			m := newTestManager(t, fr, config.Transcode{},
				WithPauseRetention(tc.retention),
				withNow(func() time.Time { return now }),
			)
			s := startSession(t, m, remuxRequest(t, "pause-"+tc.name, time.Hour))
			s.PlaybackState("paused")

			now = t0.Add(tc.stillAlive)
			m.reapIdle(now)
			if _, ok := m.Session(s.ID()); !ok {
				t.Fatalf("paused session closed too early at %v", tc.stillAlive)
			}

			now = t0.Add(tc.mustClose)
			m.reapIdle(now)
			if _, ok := m.Session(s.ID()); ok {
				t.Fatalf("paused session survived past %v", tc.mustClose)
			}
		})
	}

	t.Run("instance-scoped-and-live-update", func(t *testing.T) {
		now := t0
		clock := func() time.Time { return now }
		mShort := newTestManager(t, newFakeRunner(), config.Transcode{},
			WithPauseRetention(5*time.Minute), withNow(clock))
		mLong := newTestManager(t, newFakeRunner(), config.Transcode{},
			WithPauseRetention(60*time.Minute), withNow(clock))

		sShort := startSession(t, mShort, remuxRequest(t, "short", time.Hour))
		sLong := startSession(t, mLong, remuxRequest(t, "long", time.Hour))
		sShort.PlaybackState("paused")
		sLong.PlaybackState("paused")

		// At 6m: mShort closes while mLong stays alive (no shared global state).
		now = t0.Add(6 * time.Minute)
		mShort.reapIdle(now)
		mLong.reapIdle(now)
		if _, ok := mShort.Session(sShort.ID()); ok {
			t.Fatal("mShort session survived past 5m")
		}
		if _, ok := mLong.Session(sLong.ID()); !ok {
			t.Fatal("mLong session affected by mShort")
		}

		// At 35m: mLong still survives past default 30m, then resetting to 0 restores 30m and reaps it.
		now = t0.Add(35 * time.Minute)
		mLong.reapIdle(now)
		if _, ok := mLong.Session(sLong.ID()); !ok {
			t.Fatal("mLong session closed before 60m")
		}
		mLong.SetPauseRetention(0)
		mLong.reapIdle(now)
		if _, ok := mLong.Session(sLong.ID()); ok {
			t.Fatal("mLong session survived after resetting pause retention to 0 (30m)")
		}
	})
}

func TestHTTPWriteTimeoutFollowsPauseRetention(t *testing.T) {
	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	m := newTestManager(t, newFakeRunner(), config.Transcode{}, withNow(func() time.Time { return t0 }))
	if m.opts.httpWriteTimeout != 0 {
		t.Fatalf("expected default httpWriteTimeout=0, got %v", m.opts.httpWriteTimeout)
	}
	hs := &httpSession{m: m}

	check := func(name string, override time.Duration, want time.Duration) {
		t.Helper()
		w := &recordingDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
		fw := flushWriter{ResponseWriter: w, s: hs, timeout: override}
		if _, err := fw.Write([]byte("media")); err != nil {
			t.Fatalf("%s: write error: %v", name, err)
		}
		if len(w.deadlines) != 3 {
			t.Fatalf("%s: got %d deadlines, want 3 (write, flush, clear): %v", name, len(w.deadlines), w.deadlines)
		}
		wantDeadline := t0.Add(want)
		if !w.deadlines[0].Equal(wantDeadline) || !w.deadlines[1].Equal(wantDeadline) {
			t.Fatalf("%s: armed deadlines %v/%v, want %v", name, w.deadlines[0], w.deadlines[1], wantDeadline)
		}
		if !w.deadlines[2].IsZero() {
			t.Fatalf("%s: deadline not cleared on exit: %v", name, w.deadlines[2])
		}
	}

	// 0 keeps the 30-minute default.
	check("default-zero-30m", 0, core.PausedSessionRetention)

	// Short value.
	m.SetPauseRetention(5 * time.Minute)
	check("short-5m", 0, 5*time.Minute)

	// Value above 30 minutes.
	m.SetPauseRetention(60 * time.Minute)
	check("above-30m-60m", 0, 60*time.Minute)

	// Sub-idle value is floored at the manager's idle timeout (2m).
	m.SetPauseRetention(time.Minute)
	check("sub-idle-floor-2m", 0, 2*time.Minute)

	// Resetting to 0 restores 30 minutes.
	m.SetPauseRetention(0)
	check("reset-zero-30m", 0, core.PausedSessionRetention)

	// Explicit httpWriteTimeout override takes precedence.
	check("explicit-override", 250*time.Millisecond, 250*time.Millisecond)
}
