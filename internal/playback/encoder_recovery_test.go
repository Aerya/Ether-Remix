package playback

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type observedEncoderHLS struct {
	*repairHLS
	statusMu sync.Mutex
	status   core.EncoderStatus
}

func (h *observedEncoderHLS) EncoderStatus() *core.EncoderStatus {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	e := h.status
	return &e
}
func (h *observedEncoderHLS) SwapWouldCopyVideo(info core.MediaInfo) bool {
	v := info.Video()
	return v != nil && v.ColorTransfer != "smpte2084"
}
func (h *observedEncoderHLS) update(at time.Time, speed float64) {
	h.statusMu.Lock()
	h.status.UpdatedAt = at
	h.status.Speed = speed
	h.statusMu.Unlock()
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestSlowEncoderRecoversToCopyWithoutBlamingProvider(t *testing.T) {
	b := mkv("B", 100_000, time.Minute)
	b.HDR = true
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute), b, mkv("C", 100_000, time.Minute)}, []string{"A", "B", "C"})
	ps := h.prepare(owner, movieKey, "encoder-recovery")
	s := h.m.lookup(ps.ID)
	g := s.current()
	now := time.Now()
	f := &observedEncoderHLS{repairHLS: &repairHLS{fakeHLS: h.tr.addHLS(ps.ID), buffer: core.RecoveryBuffer{Position: 10 * time.Second, ReadyUntil: 12 * time.Second}}, status: core.EncoderStatus{Decision: core.Decision{Kind: core.DecisionTranscode, VideoCopy: false}, Phase: "running", Processor: "gpu", Filters: "cpu", StartedAt: now.Add(-20 * time.Second), UpdatedAt: now.Add(-time.Second), Speed: 0.85}}
	h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }
	s.mu.Lock()
	s.viewerAt = now
	s.viewerPosition = 10 * time.Second
	s.viewerState = "playing"
	s.mu.Unlock()
	got, ok := h.m.Session(ps.ID)
	if !ok || got.Decision != core.DecisionTranscode || got.DecisionVideoCopy || got.Encoder == nil || got.Encoder.Filters != "cpu" || got.Encoder.Processor != "gpu" {
		t.Fatalf("actual encoder status: %+v", got)
	}
	if !h.m.checkEncoderSpeed(s, g, f, now) {
		t.Fatal("slow conversion not detected")
	}
	if g.backgroundFailover.Load() {
		t.Fatal("recovered on a single sample")
	}
	f.update(now, 0.85)
	if !h.m.checkEncoderSpeed(s, g, f, now) {
		t.Fatal("second slow sample lost")
	}
	select {
	case sw := <-f.swapC:
		if v := sw.Info.Video(); v == nil || v.ColorTransfer == "smpte2084" {
			t.Fatalf("selected conversion: %+v", sw.Info)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no compatible encoder recovery")
	}
	if h.resolver.isBad("A") || h.resolver.isBad("B") {
		t.Fatal("slow encoder blamed provider or release")
	}
}

func TestEncoderRunwayAndFreshSamples(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute)}, []string{"A"})
	ps := h.prepare(owner, movieKey, "encoder-runway")
	s := h.m.lookup(ps.ID)
	g := s.current()
	now := time.Now()
	f := &observedEncoderHLS{repairHLS: &repairHLS{fakeHLS: h.tr.addHLS(ps.ID), buffer: core.RecoveryBuffer{Position: 10 * time.Second, ReadyUntil: 50 * time.Second}}, status: core.EncoderStatus{Decision: core.Decision{Kind: core.DecisionTranscode}, Phase: "running", StartedAt: now.Add(-20 * time.Second), UpdatedAt: now, Speed: 0.85}}
	s.mu.Lock()
	s.viewerAt = now
	s.viewerPosition = 10 * time.Second
	s.viewerState = "playing"
	s.mu.Unlock()
	h.m.checkEncoderSpeed(s, g, f, now)
	h.m.checkEncoderSpeed(s, g, f, now)
	s.speedMu.Lock()
	count := s.encoderSlowChecks
	s.speedMu.Unlock()
	if count != 1 || g.backgroundFailover.Load() {
		t.Fatalf("duplicate sample triggered recovery: count %d", count)
	}
	f.update(now.Add(time.Second), 0.85)
	h.m.checkEncoderSpeed(s, g, f, now.Add(time.Second))
	if g.backgroundFailover.Load() {
		t.Fatal("ample contiguous output triggered recovery")
	}
	f.update(now.Add(2*time.Second), 1.2)
	h.m.checkEncoderSpeed(s, g, f, now.Add(2*time.Second))
	s.speedMu.Lock()
	count = s.encoderSlowChecks
	s.speedMu.Unlock()
	if count != 0 {
		t.Fatalf("speed recovery did not reset hysteresis: %d", count)
	}
	f.update(now.Add(3*time.Second), 0.85)
	h.m.checkEncoderSpeed(s, g, f, now.Add(3*time.Second))
	f.mu.Lock()
	f.buffer.SeekAt = now.Add(4 * time.Second)
	f.mu.Unlock()
	s.mu.Lock()
	s.viewerAt = now.Add(5 * time.Second)
	s.mu.Unlock()
	f.update(now.Add(5*time.Second), 0.85)
	h.m.checkEncoderSpeed(s, g, f, now.Add(5*time.Second))
	s.speedMu.Lock()
	count = s.encoderSlowChecks
	s.speedMu.Unlock()
	if count != 1 {
		t.Fatalf("seek carried old samples into a new position: %d", count)
	}
}

func TestEncoderRecoveryKeepsCurrentWhenOnlyConversionExists(t *testing.T) {
	b := mkv("B", 100_000, time.Minute)
	b.HDR = true
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute), b}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "encoder-no-copy")
	s := h.m.lookup(ps.ID)
	g := s.current()
	f := &observedEncoderHLS{repairHLS: &repairHLS{fakeHLS: h.tr.addHLS(ps.ID)}}
	h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }
	_, err := h.m.failoverWith(t.Context(), s, g, 10*time.Second, failoverOpts{encoder: true, quietFailure: true})
	if err == nil {
		t.Fatal("conversion accepted as encoder recovery")
	}
	if s.current() != g || h.resolver.isBad("A") || h.resolver.isBad("B") {
		t.Fatal("encoder recovery blamed provider or replaced current source")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.tried {
		if id == "A" || id == "B" {
			t.Fatalf("healthy source excluded: %v", s.tried)
		}
	}
}

func TestSlowEncoderRecoveryBacksOffAndStops(t *testing.T) {
	b := mkv("B", 100_000, time.Minute)
	b.HDR = true
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute), b}, []string{"A", "B"})
	var logBuf syncBuf
	h.m.log = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ps := h.prepare(owner, movieKey, "encoder-backoff")
	s := h.m.lookup(ps.ID)
	g := s.current()
	base := time.Now()
	f := &observedEncoderHLS{
		repairHLS: &repairHLS{
			fakeHLS: h.tr.addHLS(ps.ID),
			buffer:  core.RecoveryBuffer{Position: 10 * time.Second, ReadyUntil: 12 * time.Second},
		},
		status: core.EncoderStatus{
			Decision:  core.Decision{Kind: core.DecisionTranscode, VideoCopy: false},
			Phase:     "running",
			StartedAt: base.Add(-20 * time.Second),
			UpdatedAt: base.Add(-time.Second),
			Speed:     0.80,
		},
	}
	h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }

	step := func(at time.Time) {
		t.Helper()
		s.mu.Lock()
		s.viewerAt = at
		s.viewerPosition = 10 * time.Second
		s.viewerState = "playing"
		s.mu.Unlock()
		f.update(at.Add(-500*time.Millisecond), 0.80)
		h.m.checkEncoderSpeed(s, g, f, at)
		f.update(at, 0.80)
		h.m.checkEncoderSpeed(s, g, f, at)
	}
	waitMisses := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			s.speedMu.Lock()
			got := s.encoderMisses
			s.speedMu.Unlock()
			if got == want && !g.backgroundFailover.Load() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		s.speedMu.Lock()
		got := s.encoderMisses
		s.speedMu.Unlock()
		t.Fatalf("encoderMisses = %d, want %d", got, want)
	}

	// Attempt 1 triggers immediately and schedules a 30s retry window.
	step(base)
	waitMisses(1)

	// Before 30s, slow samples must not trigger another attempt.
	step(base.Add(20 * time.Second))
	if g.backgroundFailover.Load() {
		t.Fatal("re-triggered before 30s backoff elapsed")
	}

	// Attempt 2 triggers after 30s and backs off to 60s.
	t2 := base.Add(31 * time.Second)
	step(t2)
	waitMisses(2)

	// 40s after attempt 2 (< 60s backoff), no attempt may run.
	step(t2.Add(40 * time.Second))
	if g.backgroundFailover.Load() {
		t.Fatal("re-triggered before 60s exponential backoff elapsed")
	}

	// Attempt 3 triggers after 60s and exhausts recovery.
	t3 := t2.Add(61 * time.Second)
	step(t3)
	waitMisses(3)

	// Across 40 minutes of continued slow encoding, no further failover runs.
	for _, dt := range []time.Duration{3 * time.Minute, 15 * time.Minute, 40 * time.Minute} {
		step(t3.Add(dt))
		if g.backgroundFailover.Load() {
			t.Fatalf("triggered after exhaustion at +%v", dt)
		}
	}
	s.speedMu.Lock()
	finalMisses := s.encoderMisses
	s.speedMu.Unlock()
	if finalMisses != maxEncoderRecoveryAttempts {
		t.Fatalf("final encoderMisses = %d, want %d", finalMisses, maxEncoderRecoveryAttempts)
	}

	logs := logBuf.String()
	if strings.Contains(logs, "failover found no working release") {
		t.Fatalf("quiet encoder failover logged noisy error: %s", logs)
	}
	if got := strings.Count(logs, "stopping slow-encoder failover recovery after bounded attempts"); got != 1 {
		t.Fatalf("stop log count = %d, want 1; logs:\n%s", got, logs)
	}
}
