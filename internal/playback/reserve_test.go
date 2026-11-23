package playback

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestReserveWarmsNinetySecondsWithoutDelayingPlay(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000_000, 100*time.Second)}, []string{"A"})
	ps := h.prepare(owner, movieKey, "reserve")
	h.m.PlaybackProgress(owner.ID, "reserve", movieKey, 10*time.Second, "playing")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		h.engine.mu.Lock()
		calls := append([]prefetchCall(nil), h.engine.prefetch...)
		h.engine.mu.Unlock()
		for _, c := range calls {
			if c.Length >= 90_000_000 && c.Length <= 256<<20 {
				if h.m.lookup(ps.ID).current().releaseID() != "A" {
					t.Fatal("prefetch changed source")
				}
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no 90-second reserve requested")
}

func TestDetailReserveUsesMediaDuration(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000_000, 100*time.Second)}, []string{"A"})
	if !h.m.Prewarm(t.Context(), owner, movieKey, defaultCaps("detail-reserve"), core.PrewarmOptions{Reason: core.PrewarmDetail}) {
		t.Fatal("warm skipped")
	}
	waitWarm(t, h, owner.ID, "detail-reserve", movieKey)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		h.engine.mu.Lock()
		calls := append([]prefetchCall(nil), h.engine.prefetch...)
		h.engine.mu.Unlock()
		for _, c := range calls {
			if c.Length == 25_000_000 {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("detail warming did not request 25 seconds")
}

func TestSwapLatencyWindowIsBoundedAndExpiresOldSamples(t *testing.T) {
	var w swapLatencyWindow
	if got := w.target(); got != 90*time.Second {
		t.Fatalf("default target = %v", got)
	}
	w.observe(50 * time.Second)
	if got := w.target(); got != 2*time.Minute {
		t.Fatalf("slow handoff target = %v", got)
	}
	for range w.samples {
		w.observe(time.Second)
	}
	if got := w.target(); got != 90*time.Second {
		t.Fatalf("old sample survived bounded window: %v", got)
	}
}

func TestPreparedSpeedAlternateReusesOpenAndProbe(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000_000, 100*time.Second), mkv("B", 20_000_000, 100*time.Second)}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "standby")
	s := h.m.lookup(ps.ID)
	g := s.current()
	h.tr.addHLS(ps.ID)
	h.m.prepareSpeedAlternate(g)
	deadline := time.Now().Add(time.Second)
	for {
		g.standby.mu.Lock()
		ready := g.standby.ready != nil
		g.standby.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("alternate not prepared")
		}
		time.Sleep(time.Millisecond)
	}
	if s.current() != g {
		t.Fatal("preparation replaced playing source")
	}
	_, probes := counts(h)
	next, err := h.m.failoverWith(context.Background(), s, g, 10*time.Second, failoverOpts{down: true, preserveBuffer: true})
	if err != nil || next.releaseID() != "B" {
		t.Fatalf("takeover failed %v", err)
	}
	_, after := counts(h)
	if after != probes {
		t.Fatalf("takeover reprobed: %d -> %d", probes, after)
	}
}

func TestBufferRunwayAccountsForOngoingDelivery(t *testing.T) {
	for _, tc := range []struct {
		buffer time.Duration
		ratio  float64
		want   time.Duration
	}{
		{20 * time.Second, .5, 40 * time.Second}, {10 * time.Second, 0, 10 * time.Second}, {0, .8, 0}, {40 * time.Second, 1.2, time.Hour},
	} {
		if got := bufferRunway(tc.buffer, tc.ratio); got != tc.want {
			t.Fatalf("runway(%v,%v)=%v want %v", tc.buffer, tc.ratio, got, tc.want)
		}
	}
}

func TestLowBufferHonoursGraceBeforeRecovering(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 80_000_000, 100*time.Second), mkv("B", 10_000_000, 100*time.Second)}, []string{"A", "B"}, func(h *harness, o *Options) { o.SpeedCheckInterval = time.Hour })
	ps := h.prepare(owner, movieKey, "deadline")
	s := h.m.lookup(ps.ID)
	f := &repairHLS{fakeHLS: h.tr.addHLS(ps.ID), buffer: core.RecoveryBuffer{Position: 10 * time.Second, ReadyUntil: 14 * time.Second}}
	h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }
	h.clock.Advance(7 * time.Second)
	feedFetches(s, 10, 64<<10, 200*time.Millisecond, h.clock.Now())
	h.m.checkSpeeds()
	h.m.checkSpeeds()
	select {
	case <-f.swapC:
		t.Fatal("low buffer bypassed grace")
	default:
	}
	h.clock.Advance(14 * time.Second)
	feedFetches(s, 10, 64<<10, 200*time.Millisecond, h.clock.Now())
	h.m.checkSpeeds()
	h.m.checkSpeeds()
	select {
	case <-f.swapC:
	case <-time.After(3 * time.Second):
		t.Fatal("emptying buffer did not recover after grace")
	}
}

func TestPreparedAlternateIsClosedWhenPlaybackStops(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000_000, 100*time.Second), mkv("B", 20_000_000, 100*time.Second)}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "stop-standby")
	g := h.m.lookup(ps.ID).current()
	h.tr.addHLS(ps.ID)
	h.m.prepareSpeedAlternate(g)
	var next *generation
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		g.standby.mu.Lock()
		next = g.standby.ready
		g.standby.mu.Unlock()
		if next != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if next == nil {
		t.Fatal("standby not prepared")
	}
	h.m.Stop(ps.ID)
	select {
	case <-next.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("unused standby survived Stop")
	}
	h.m.mu.Lock()
	_, retained := h.m.tokens[ps.ID+"/"+next.token]
	h.m.mu.Unlock()
	if retained {
		t.Fatal("unused standby loopback grant survived Stop")
	}
}

func TestSeekSettlingAndPausedPlaybackDoNotTriggerRecovery(t *testing.T) {
	for _, paused := range []bool{false, true} {
		h := newHarness(t, []*fakeRelease{mkv("A", 80_000_000, 100*time.Second), mkv("B", 10_000_000, 100*time.Second)}, []string{"A", "B"}, func(h *harness, o *Options) { o.SpeedCheckInterval = time.Hour })
		ps := h.prepare(owner, movieKey, "seek")
		s := h.m.lookup(ps.ID)
		f := &repairHLS{fakeHLS: h.tr.addHLS(ps.ID), buffer: core.RecoveryBuffer{Position: 10 * time.Second, ReadyUntil: 11 * time.Second}}
		h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }
		h.clock.Advance(30 * time.Second)
		if paused {
			s.mu.Lock()
			s.viewerState = "paused"
			s.mu.Unlock()
		} else {
			s.noteReadPos(0, 100, h.clock.Now(), h.m.opts.SpeedGrace)
			s.noteReadPos(9999, 100, h.clock.Now(), h.m.opts.SpeedGrace)
		}
		feedFetches(s, 10, 64<<10, 200*time.Millisecond, h.clock.Now())
		h.m.checkSpeeds()
		h.m.checkSpeeds()
		s.current().standby.mu.Lock()
		started := s.current().standby.started
		s.current().standby.mu.Unlock()
		if started {
			t.Fatal("seek/paused playback started speculative recovery")
		}
		select {
		case <-f.swapC:
			t.Fatal("seek/paused playback switched")
		default:
		}
	}
}

func TestBlockedSourceRecoversWithoutCompletedFetchSamples(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 80_000_000, 100*time.Second), mkv("B", 10_000_000, 100*time.Second)}, []string{"A", "B"}, func(h *harness, o *Options) { o.SpeedCheckInterval = time.Hour })
	ps := h.prepare(owner, movieKey, "blocked")
	s := h.m.lookup(ps.ID)
	f := &repairHLS{fakeHLS: h.tr.addHLS(ps.ID), buffer: core.RecoveryBuffer{Position: 10 * time.Second, ReadyUntil: 14 * time.Second}}
	h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }
	end := s.current().reads.begin(h.clock.Now())
	defer end()
	h.clock.Advance(21 * time.Second)
	h.m.checkSpeeds()
	h.m.checkSpeeds()
	select {
	case <-f.swapC:
	case <-time.After(3 * time.Second):
		t.Fatal("outage waited for a completed read")
	}
}

func TestMeasuredColdAndPreparedPlay(t *testing.T) {
	// Controlled source-selection delay, not an Internet or Apple TV benchmark.
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000_000, 100*time.Second)}, []string{"A"})
	h.resolver.delay = 250 * time.Millisecond
	start := time.Now()
	cold := h.prepare(owner, movieKey, "cold-measure")
	coldTime := time.Since(start)
	h.m.Stop(cold.ID)
	if !h.m.Prewarm(t.Context(), owner, movieKey, defaultCaps("warm-measure"), core.PrewarmOptions{Reason: core.PrewarmDetail}) {
		t.Fatal("prewarm skipped")
	}
	waitWarm(t, h, owner.ID, "warm-measure", movieKey)
	calls, probes := counts(h)
	start = time.Now()
	h.prepare(owner, movieKey, "warm-measure")
	warmTime := time.Since(start)
	afterCalls, afterProbes := counts(h)
	if calls != afterCalls || probes != afterProbes {
		t.Fatal("prepared Play repeated discovery/probe")
	}
	t.Logf("controlled 250ms discovery: cold=%s prepared=%s; preparation paid before Play", coldTime, warmTime)
}

type bufferedReserveHLS struct {
	*repairHLS
	preserved bool
}

func (h *bufferedReserveHLS) SwapInputBuffered(ctx context.Context, url string, info core.MediaInfo, at time.Duration) error {
	h.preserved = true
	return h.SwapInput(ctx, url, info, at)
}
func TestBufferedRecoveryAllowsTrailingDurationButRejectsPALMapping(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration time.Duration
		preserve bool
	}{
		{"trailing-credits", 99 * time.Second, true}, {"pal-speed", 96 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, []*fakeRelease{mkv("A", 80_000_000, 100*time.Second), mkv("B", 10_000_000, tc.duration)}, []string{"A", "B"})
			ps := h.prepare(owner, movieKey, "buffer-boundary")
			s := h.m.lookup(ps.ID)
			f := &bufferedReserveHLS{repairHLS: &repairHLS{fakeHLS: h.tr.addHLS(ps.ID), buffer: core.RecoveryBuffer{Position: 0, ReadyUntil: 40 * time.Second}}}
			h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }
			if _, err := h.m.failoverWith(t.Context(), s, s.current(), 0, failoverOpts{down: true, preserveBuffer: true}); err != nil {
				t.Fatal(err)
			}
			if f.preserved != tc.preserve {
				t.Fatalf("preserved=%v want %v", f.preserved, tc.preserve)
			}
		})
	}
}

type coldStartOnceSource struct {
	core.MediaSource
	slowed atomic.Bool
}

func (c *coldStartOnceSource) ReadAt(p []byte, off int64) (int, error) {
	if c.slowed.CompareAndSwap(false, true) {
		time.Sleep(1100 * time.Millisecond)
	}
	return c.MediaSource.ReadAt(p, off)
}

// Cold first-byte reads, seek warm-up reads, and bursts shorter than the
// minimum observation window must not trigger premature speed down-failover (#385).
func TestWarmupReadsAndShortWindowDoNotTriggerSpeedFailover(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 80_000_000, 100*time.Second), mkv("B", 10_000_000, 100*time.Second)}, []string{"A", "B"}, func(h *harness, o *Options) { o.SpeedCheckInterval = time.Hour })
	ps := h.prepare(owner, movieKey, "warmup-window")
	s := h.m.lookup(ps.ID)
	f := &repairHLS{fakeHLS: h.tr.addHLS(ps.ID), buffer: core.RecoveryBuffer{Position: 10 * time.Second, ReadyUntil: 12 * time.Second}}
	h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }

	// Cold read #1 (0..512 KiB) takes 3 seconds: noteReadPos marks warmup=true.
	if !s.noteReadPos(0, 512<<10, h.clock.Now(), h.m.opts.SpeedGrace) {
		t.Fatal("initial read was not marked warmup")
	}
	// Advance past initial SpeedGrace and record 4 slow post-warmup reads inside
	// a 2-second span (< 6s minObservationWindow).
	h.clock.Advance(21 * time.Second)
	for i := int64(0); i < 4; i++ {
		off := (512 << 10) + i*(64<<10)
		if s.noteReadPos(off, 64<<10, h.clock.Now(), h.m.opts.SpeedGrace) {
			t.Fatalf("sequential read %d marked warmup", i)
		}
		s.noteFetch(64<<10, 400*time.Millisecond, h.clock.Now())
	}
	h.clock.Advance(2 * time.Second)
	for range 3 {
		h.m.checkSpeeds()
	}
	if s.current().backgroundFailover.Load() {
		t.Fatal("short post-warmup observation window started failover")
	}
	select {
	case <-f.swapC:
		t.Fatal("short post-warmup observation window triggered failover")
	default:
	}

	// Seeking to a new offset marks warmup=true again and resets evidence.
	if !s.noteReadPos(40_000_000, 512<<10, h.clock.Now(), h.m.opts.SpeedGrace) {
		t.Fatal("seek read was not marked warmup")
	}

	// End-to-end HTTP range read: one 1.1s cold first-byte read followed by
	// fast sequential chunks must never trigger down-failover.
	h2 := newHarness(t, []*fakeRelease{mkv("A", 980_000_000, 100*time.Second), mkv("B", 10_000_000, 100*time.Second)}, []string{"A", "B"}, func(h *harness, o *Options) { speedOpts(o) })
	ps2 := h2.prepare(owner, movieKey, "cold-http")
	h2.m.SetDecision(ps2.ID, core.Decision{Kind: core.DecisionRemux})
	hls2 := h2.tr.addHLS(ps2.ID)
	s2 := h2.m.lookup(ps2.ID)
	g2 := s2.current()
	g2.src = &coldStartOnceSource{MediaSource: g2.src}
	srv := directPlayServer(t, h2.m, ps2.ID)
	if _, _, err := get(t, srv.URL, map[string]string{"Range": "bytes=0-4718591"}); err != nil {
		t.Fatal(err)
	}
	h2.clock.Advance(2 * time.Second)
	for range 4 {
		h2.m.checkSpeeds()
	}
	if g2.backgroundFailover.Load() {
		t.Fatal("single slow cold read started down-failover")
	}
	h2.m.Close()
	select {
	case <-hls2.swapC:
		t.Fatal("single slow cold read swapped HLS input")
	default:
	}
}

type reportingSource struct {
	core.MediaSource
	bytes   atomic.Int64
	fetches atomic.Int64
}

func (r *reportingSource) SourceDelivery() core.SourceDelivery {
	return core.SourceDelivery{
		Bytes:         r.bytes.Load(),
		Fetches:       r.fetches.Load(),
		Parallel:      28,
		InFlight:      12,
		PipelineDepth: 2,
	}
}

// Healthy network delivery reported through SourceDeliveryReporter (including
// background read-ahead and disk play-ahead) must prevent false speed failover
// when foreground demand reads briefly look slow (#385).
func TestSourceDeliveryReporterPreventsFalseSpeedFailover(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 80_000_000, 100*time.Second), mkv("B", 10_000_000, 100*time.Second)}, []string{"A", "B"}, func(h *harness, o *Options) { o.SpeedCheckInterval = time.Hour })
	ps := h.prepare(owner, movieKey, "net-delivery")
	s := h.m.lookup(ps.ID)
	g := s.current()
	// The background index fill reads g.src; swap the source only after it
	// has finished.
	select {
	case <-g.indexReady:
	case <-time.After(10 * time.Second):
		t.Fatal("index fill never finished")
	}
	rep := &reportingSource{MediaSource: g.src}
	g.src = rep
	f := &repairHLS{fakeHLS: h.tr.addHLS(ps.ID), buffer: core.RecoveryBuffer{Position: 10 * time.Second, ReadyUntil: 12 * time.Second}}
	h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }

	s.recordDelivery(rep.SourceDelivery(), h.clock.Now())
	h.clock.Advance(14 * time.Second)
	rep.bytes.Store(18_000_000)
	rep.fetches.Store(28)
	s.recordDelivery(rep.SourceDelivery(), h.clock.Now())
	feedFetches(s, 6, 64<<10, 300*time.Millisecond, h.clock.Now())

	h.clock.Advance(8 * time.Second) // t=22s: 14s..22s span inside 20s window
	rep.bytes.Store(30_000_000)
	rep.fetches.Store(45)
	feedFetches(s, 6, 64<<10, 300*time.Millisecond, h.clock.Now())

	for range 4 {
		h.m.checkSpeeds()
	}
	if g.backgroundFailover.Load() {
		t.Fatal("down-failover started despite healthy SourceDelivery throughput")
	}
	h.m.Close()
	select {
	case <-f.swapC:
		t.Fatal("down-failed despite healthy SourceDelivery throughput")
	default:
	}
}

// A previously built cushion (>= 12s) that drains nearly dry mid-grace may
// recover urgently before stalling, whereas a cold low buffer never bypasses grace.
func TestBuiltBufferDrainingDryRecoversInsideGrace(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 80_000_000, 100*time.Second), mkv("B", 10_000_000, 100*time.Second)}, []string{"A", "B"}, func(h *harness, o *Options) { o.SpeedCheckInterval = time.Hour })
	ps := h.prepare(owner, movieKey, "built-drain")
	s := h.m.lookup(ps.ID)
	f := &repairHLS{fakeHLS: h.tr.addHLS(ps.ID), buffer: core.RecoveryBuffer{Position: 0, ReadyUntil: 16 * time.Second}}
	h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }

	// Observe healthy 16s cushion early in grace, then source stalls and cushion drains to 2s.
	h.clock.Advance(4 * time.Second)
	feedFetches(s, 6, 64<<10, 300*time.Millisecond, h.clock.Now())
	h.m.checkSpeeds()

	f.buffer = core.RecoveryBuffer{Position: 14 * time.Second, ReadyUntil: 16 * time.Second}
	h.clock.Advance(8 * time.Second) // t=12s (< 20s SpeedGrace, >= 6s span from t=4s)
	feedFetches(s, 6, 64<<10, 300*time.Millisecond, h.clock.Now())
	h.m.checkSpeeds()
	h.m.checkSpeeds()
	select {
	case <-f.swapC:
	case <-time.After(3 * time.Second):
		t.Fatal("draining built buffer did not recover before running dry")
	}
}

type observingResolver struct {
	core.Resolver
	mu         sync.Mutex
	deliveries []float64
	stalls     []float64
}

func (o *observingResolver) ObserveSourceDelivery(_ core.Release, bps float64) {
	o.mu.Lock()
	o.deliveries = append(o.deliveries, bps)
	o.mu.Unlock()
}

func (o *observingResolver) ObserveSourceStall(_ core.Release, bps float64) {
	o.mu.Lock()
	o.stalls = append(o.stalls, bps)
	o.mu.Unlock()
}

type controlledDeliverySource struct {
	core.MediaSource
	mu       sync.Mutex
	delivery core.SourceDelivery
}

func (c *controlledDeliverySource) set(d core.SourceDelivery) {
	c.mu.Lock()
	c.delivery = d
	c.mu.Unlock()
}

func (c *controlledDeliverySource) SourceDelivery() core.SourceDelivery {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.delivery
}

func TestSourceDeliveryObserverIgnoresConsumerPacingAndReportsBurstAndStall(t *testing.T) {
	// Media rate is 800_000 B/s (6.4 Mbps).
	h := newHarness(t, []*fakeRelease{mkv("A", 80_000_000, 100*time.Second), mkv("B", 10_000_000, 100*time.Second)}, []string{"A", "B"}, func(h *harness, o *Options) { o.SpeedCheckInterval = time.Hour })
	obs := &observingResolver{Resolver: h.m.deps.Resolver}
	h.m.deps.Resolver = obs

	ps := h.prepare(owner, movieKey, "observer-pacing")
	s := h.m.lookup(ps.ID)
	g := s.current()
	select {
	case <-g.indexReady:
	case <-time.After(10 * time.Second):
		t.Fatal("index fill never finished")
	}
	src := &controlledDeliverySource{MediaSource: g.src}
	g.src = src
	f := &repairHLS{fakeHLS: h.tr.addHLS(ps.ID), buffer: core.RecoveryBuffer{Position: 0, ReadyUntil: 4 * time.Second}}
	h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return f, true }

	// 1. Initial unthrottled fill burst: 20 MB fetched in 4s Busy across an 8s
	// wall-clock span -> active burst rate is 5 MB/s, not 2.5 MB/s wall-clock.
	src.set(core.SourceDelivery{Bytes: 0, Fetches: 0, Busy: 0, Parallel: 8, InFlight: 8})
	s.recordDelivery(src.SourceDelivery(), h.clock.Now())
	h.clock.Advance(8 * time.Second)
	src.set(core.SourceDelivery{Bytes: 20_000_000, Fetches: 28, Busy: 4 * time.Second, Parallel: 8, InFlight: 8})
	h.m.checkSpeeds()

	obs.mu.Lock()
	if len(obs.deliveries) != 1 || math.Abs(obs.deliveries[0]-5_000_000) > 1 {
		t.Fatalf("burst ObserveSourceDelivery = %v, want [5_000_000]", obs.deliveries)
	}
	obs.deliveries = nil
	obs.mu.Unlock()

	// 2. Steady-state consumer-paced playback: past SpeedGrace, 30s HLS buffer
	// built, single-article top-up trickle at 1x media rate (800_000 B/s).
	f.buffer = core.RecoveryBuffer{Position: 10 * time.Second, ReadyUntil: 40 * time.Second}
	h.clock.Advance(25 * time.Second)
	src.set(core.SourceDelivery{Bytes: 40_000_000, Fetches: 56, Busy: 8 * time.Second, Parallel: 8, InFlight: 1})
	s.recordDelivery(src.SourceDelivery(), h.clock.Now())
	h.clock.Advance(10 * time.Second)
	src.set(core.SourceDelivery{Bytes: 48_000_000, Fetches: 68, Busy: 10 * time.Second, Parallel: 8, InFlight: 1})
	for range 3 {
		h.m.checkSpeeds()
	}

	obs.mu.Lock()
	if len(obs.deliveries) != 0 || len(obs.stalls) != 0 {
		t.Fatalf("steady-state consumer pacing reported deliveries=%v stalls=%v, want none", obs.deliveries, obs.stalls)
	}
	obs.mu.Unlock()

	// 3. Real starvation: buffer drains to 2s while saturated fetches deliver
	// only 250_000 B/s (< 800_000 B/s media). Down-failover records ObserveSourceStall.
	f.buffer = core.RecoveryBuffer{Position: 38 * time.Second, ReadyUntil: 40 * time.Second}
	h.clock.Advance(25 * time.Second)
	src.set(core.SourceDelivery{Bytes: 50_000_000, Fetches: 72, Busy: 12 * time.Second, Parallel: 8, InFlight: 8})
	s.recordDelivery(src.SourceDelivery(), h.clock.Now())
	feedFetches(s, 3, 64<<10, 400*time.Millisecond, h.clock.Now())
	h.clock.Advance(10 * time.Second)
	src.set(core.SourceDelivery{Bytes: 52_500_000, Fetches: 80, Busy: 22 * time.Second, Parallel: 8, InFlight: 8})
	feedFetches(s, 4, 64<<10, 400*time.Millisecond, h.clock.Now())
	h.m.checkSpeeds()
	h.m.checkSpeeds()

	obs.mu.Lock()
	stalls := append([]float64(nil), obs.stalls...)
	obs.mu.Unlock()
	if len(stalls) == 0 || math.Abs(stalls[0]-250_000) > 1 {
		t.Fatalf("starvation ObserveSourceStall = %v, want [250_000]", stalls)
	}
	h.m.Close()

	// 4. Direct-play viewer timeline stall (no HLS): mid-play buffering >= 3s
	// while source delivery is below media rate records ObserveSourceStall,
	// whereas post-seek rebuffering does not.
	h2 := newHarness(t, []*fakeRelease{mkv("A", 80_000_000, 100*time.Second)}, []string{"A"}, func(h *harness, o *Options) { o.SpeedCheckInterval = time.Hour })
	obs2 := &observingResolver{Resolver: h2.m.deps.Resolver}
	h2.m.deps.Resolver = obs2
	ps2 := h2.prepare(owner, movieKey, "observer-timeline")
	s2 := h2.m.lookup(ps2.ID)
	g2 := s2.current()
	select {
	case <-g2.indexReady:
	case <-time.After(10 * time.Second):
		t.Fatal("index fill never finished")
	}
	src2 := &controlledDeliverySource{MediaSource: g2.src}
	g2.src = src2
	src2.set(core.SourceDelivery{Bytes: 0, Fetches: 0, Busy: 0, Parallel: 8, InFlight: 8})
	s2.recordDelivery(src2.SourceDelivery(), h2.clock.Now())
	h2.m.PlaybackProgress(owner.ID, "observer-timeline", movieKey, 2*time.Second, "playing")
	h2.clock.Advance(6 * time.Second)
	src2.set(core.SourceDelivery{Bytes: 1_800_000, Fetches: 8, Busy: 6 * time.Second, Parallel: 8, InFlight: 8}) // 300_000 B/s < 800_000 B/s
	// Post-seek rebuffering (position jumps to 40s) is not a stall.
	h2.m.PlaybackProgress(owner.ID, "observer-timeline", movieKey, 40*time.Second, "buffering")
	h2.clock.Advance(4 * time.Second)
	src2.set(core.SourceDelivery{Bytes: 3_000_000, Fetches: 12, Busy: 10 * time.Second, Parallel: 8, InFlight: 8})
	h2.m.PlaybackProgress(owner.ID, "observer-timeline", movieKey, 40*time.Second, "playing")
	obs2.mu.Lock()
	if len(obs2.stalls) != 0 {
		t.Fatalf("post-seek rebuffering recorded ObserveSourceStall: %v", obs2.stalls)
	}
	obs2.mu.Unlock()
	// Mid-play buffering at the current playhead for 4s records ObserveSourceStall.
	h2.m.PlaybackProgress(owner.ID, "observer-timeline", movieKey, 40*time.Second, "buffering")
	h2.clock.Advance(4 * time.Second)
	src2.set(core.SourceDelivery{Bytes: 4_200_000, Fetches: 16, Busy: 14 * time.Second, Parallel: 8, InFlight: 8})
	h2.m.PlaybackProgress(owner.ID, "observer-timeline", movieKey, 40*time.Second, "playing")
	obs2.mu.Lock()
	stalls2 := append([]float64(nil), obs2.stalls...)
	obs2.mu.Unlock()
	if len(stalls2) != 1 || math.Abs(stalls2[0]-300_000) > 1 {
		t.Fatalf("timeline ObserveSourceStall = %v, want [300_000]", stalls2)
	}
	h2.m.Close()
}
