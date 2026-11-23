package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// slowProbeHarness lands the warm probe after probeAt (the resolver is the
// slow step of the warm-up).
func slowProbeHarness(t *testing.T, probeAt time.Duration) *harness {
	t.Helper()
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, []string{"r1"})
	h.resolver.mu.Lock()
	h.resolver.delay = probeAt
	h.resolver.mu.Unlock()
	return h
}

func TestProbeMetadataWaitsUntilCallerDeadline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		probeAt time.Duration
	}{
		{"probe-at-1s", time.Second},
		// Past the 1.5 s default a caller deadline still owns the wait.
		{"probe-at-2s", 2 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := slowProbeHarness(t, tc.probeAt)
			caps := defaultCaps("android")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := h.m.ProbeMetadata(ctx, owner, movieKey, caps); err != nil {
				t.Fatalf("probe landing inside the deadline returned %v", err)
			}
			if info, ok := h.m.WarmProbeInfo(owner, caps, movieKey); !ok || info.Estimated || len(info.Streams) == 0 {
				t.Fatalf("probed tracks not adopted: ok=%v info=%+v", ok, info)
			}
		})
	}
}

// expiredCtx reports DeadlineExceeded from Err as soon as its deadline has
// passed, so the outcome does not depend on whether the context's own timer
// or the wait's timer fires first.
type expiredCtx struct{ context.Context }

func (c expiredCtx) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	if dl, ok := c.Deadline(); ok && !time.Now().Before(dl) {
		return context.DeadlineExceeded
	}
	return nil
}

func TestProbeMetadataDeadlineEndsWaitAsSlowAndKeepsWarmup(t *testing.T) {
	t.Parallel()
	h := slowProbeHarness(t, 1200*time.Millisecond)
	caps := defaultCaps("android")
	tctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	ctx := expiredCtx{tctx}
	err := h.m.ProbeMetadata(ctx, owner, movieKey, caps)
	if !errors.Is(err, core.ErrPrepareSlow) {
		t.Fatalf("deadline shorter than the probe returned %v, want ErrPrepareSlow", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline end leaked as %v", err)
	}
	// The warm-up is not cancelled: it completes and later reads adopt it.
	waitWarm(t, h, owner.ID, caps.ClientID, movieKey)
	if info, ok := h.m.WarmProbeInfo(owner, caps, movieKey); !ok || info.Estimated || len(info.Streams) == 0 {
		t.Fatalf("warm-up not adopted after the caller gave up: ok=%v info=%+v", ok, info)
	}
	if r, p := counts(h); r != 1 || p != 1 {
		t.Fatalf("warm-up repeated: %d resolves, %d probes", r, p)
	}
}

func TestProbeMetadataPastDeadlineStillSchedulesWarmup(t *testing.T) {
	t.Parallel()
	h := slowProbeHarness(t, time.Second)
	caps := defaultCaps("android")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()
	err := h.m.ProbeMetadata(ctx, owner, movieKey, caps)
	if !errors.Is(err, core.ErrPrepareSlow) {
		t.Fatalf("past deadline returned %v, want ErrPrepareSlow", err)
	}
	// The warm-up is scheduled even though no wait remained.
	waitWarm(t, h, owner.ID, caps.ClientID, movieKey)
	if info, ok := h.m.WarmProbeInfo(owner, caps, movieKey); !ok || info.Estimated || len(info.Streams) == 0 {
		t.Fatalf("warm-up not scheduled for a past deadline: ok=%v info=%+v", ok, info)
	}
	if r, p := counts(h); r != 1 || p != 1 {
		t.Fatalf("past deadline: %d resolves, %d probes, want 1/1", r, p)
	}
}

func TestProbeMetadataWithoutDeadlineKeepsDefaultWait(t *testing.T) {
	t.Parallel()
	h := slowProbeHarness(t, 2*time.Second)
	caps := defaultCaps("desktop")
	err := h.m.ProbeMetadata(context.Background(), owner, movieKey, caps)
	if !errors.Is(err, core.ErrPrepareSlow) {
		t.Fatalf("probe at 2s without deadline returned %v, want ErrPrepareSlow", err)
	}
	waitWarm(t, h, owner.ID, caps.ClientID, movieKey)
	if _, ok := h.m.WarmProbeInfo(owner, caps, movieKey); !ok {
		t.Fatal("warm-up not adopted after the default wait ended")
	}
}

func TestProbeMetadataCancellationReturnsContextError(t *testing.T) {
	t.Parallel()
	h := slowProbeHarness(t, time.Second)
	caps := defaultCaps("android")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cctx, stop := context.WithCancel(ctx)
	time.AfterFunc(100*time.Millisecond, stop)
	if err := h.m.ProbeMetadata(cctx, owner, movieKey, caps); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait returned %v, want context.Canceled", err)
	}
	// Cancelling the caller does not cancel the warm-up.
	waitWarm(t, h, owner.ID, caps.ClientID, movieKey)
	if _, ok := h.m.WarmProbeInfo(owner, caps, movieKey); !ok {
		t.Fatal("warm-up lost after the caller cancelled")
	}
}

func TestProbeMetadataDisabledWarmupDoesNoSourceIOUnderDeadline(t *testing.T) {
	for _, mode := range []string{"detail-off", "speculation-off", "cache-off"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			h := slowProbeHarness(t, time.Second)
			cfg := h.m.config()
			switch mode {
			case "detail-off":
				cfg.Cache.DetailWarmup = "off"
			case "speculation-off":
				cfg.Cache.DisableSpeculation = true
			case "cache-off":
				cfg.Cache.Mode = "off"
			}
			h.m.SetConfig(cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := h.m.ProbeMetadata(ctx, owner, movieKey, defaultCaps("android")); !errors.Is(err, core.ErrPrepareSlow) {
				t.Fatalf("disabled warm-up returned %v, want ErrPrepareSlow", err)
			}
			if r, p := counts(h); r != 0 || p != 0 {
				t.Fatalf("disabled warm-up resolved/probed: %d/%d", r, p)
			}
		})
	}
}

func TestPlayProbeMetadataWaitCap(t *testing.T) {
	t.Parallel()
	// Browsing ProbeMetadata remains capped at 4 s.
	longCtx, cancelLong := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelLong()
	if got := probeMetadataWait(longCtx); got != maxProbeMetadataWait {
		t.Fatalf("browsing probeMetadataWait(30s) = %s, want %s", got, maxProbeMetadataWait)
	}
	// Play-intent re-ask wait allows up to 12 s (exceeding the 4 s browsing cap).
	if got := playProbeMetadataWait(longCtx); got != maxPlayProbeMetadataWait {
		t.Fatalf("playProbeMetadataWait(30s) = %s, want %s", got, maxPlayProbeMetadataWait)
	}
	if got := playProbeMetadataWait(context.Background()); got != maxPlayProbeMetadataWait {
		t.Fatalf("playProbeMetadataWait(background) = %s, want %s", got, maxPlayProbeMetadataWait)
	}
	midCtx, cancelMid := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancelMid()
	if got := playProbeMetadataWait(midCtx); got <= maxProbeMetadataWait || got > 8*time.Second {
		t.Fatalf("playProbeMetadataWait(8s) = %s, want in (%s, 8s]", got, maxProbeMetadataWait)
	}
}

type playProbeGatingResolver struct {
	core.Resolver
	rejectSpec bool
	gate       <-chan struct{}
	entered    chan struct{}
	lastSpec   bool
	lastPurp   string
}

func (r *playProbeGatingResolver) Resolve(ctx context.Context, req core.ResolveRequest) (*core.Resolution, error) {
	spec := core.IsSpeculative(ctx) || core.SpeculativeIndexerWork(ctx)
	if r.rejectSpec && spec {
		return nil, core.ErrForegroundOnly
	}
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	r.lastSpec = core.IsSpeculative(ctx)
	r.lastPurp = core.IndexerPurpose(ctx)
	return r.Resolver.Resolve(ctx, req)
}

func TestProbePlayMetadataIgnoresLocalResponseWarmMissAndSpeculationOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, []string{"r1"})
	caps := defaultCaps("plezy-tv")
	id := identity{userID: owner.ID, clientID: caps.ClientID, key: movieKey, surface: h.m.playbackSurface(string(caps.Protocol))}

	// Populate a warmMiss from a failed speculative detail warm-up.
	gating := &playProbeGatingResolver{Resolver: h.resolver, rejectSpec: true}
	h.m.deps.Resolver = gating
	if !h.m.Prewarm(context.Background(), owner, movieKey, caps, core.PrewarmOptions{Reason: core.PrewarmDetail, PlayIntent: true}) {
		t.Fatal("initial speculative prewarm skipped")
	}
	waitWarmGone(t, h, id)

	// Turn off speculation/detail warm-up and pass LocalResponse, matching a
	// detail handler with warm-ups disabled and a cached negative.
	cfg := h.m.config()
	cfg.Cache.DisableSpeculation = true
	cfg.Cache.DetailWarmup = "off"
	h.m.SetConfig(cfg)

	ctx, cancel := context.WithTimeout(core.WithLocalResponse(context.Background()), 12*time.Second)
	defer cancel()
	if err := h.m.ProbePlayMetadata(ctx, owner, movieKey, caps); err != nil {
		t.Fatalf("ProbePlayMetadata failed: %v", err)
	}
	if gating.lastSpec || gating.lastPurp != core.IndexerPurposePlayback {
		t.Fatalf("play probe ran with speculative=%v purpose=%q, want false/%q", gating.lastSpec, gating.lastPurp, core.IndexerPurposePlayback)
	}
	if info, ok := h.m.WarmProbeInfo(owner, caps, movieKey); !ok || info.Estimated || len(info.Streams) == 0 {
		t.Fatalf("WarmProbeInfo missing probed streams: ok=%v info=%+v", ok, info)
	}
	// The following Prepare must adopt the same warm entry without re-resolving or re-probing.
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, caps); err != nil {
		t.Fatalf("Prepare after ProbePlayMetadata: %v", err)
	}
	if r, p := counts(h); r != 1 || p != 1 {
		t.Fatalf("Prepare repeated work after ProbePlayMetadata: resolves=%d probes=%d", r, p)
	}
}

func TestProbePlayMetadataJoinsInFlightAndRetriesSpeculativeFailure(t *testing.T) {
	t.Run("joins-and-promotes-inflight", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, []string{"r1"})
		gate := make(chan struct{})
		entered := make(chan struct{}, 1)
		gating := &playProbeGatingResolver{Resolver: h.resolver, gate: gate, entered: entered}
		h.m.deps.Resolver = gating
		caps := defaultCaps("plezy-join")

		if !h.m.Prewarm(context.Background(), owner, movieKey, caps, core.PrewarmOptions{Reason: core.PrewarmDetail, PlayIntent: true}) {
			t.Fatal("speculative prewarm skipped")
		}
		<-entered

		done := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			done <- h.m.ProbePlayMetadata(ctx, owner, movieKey, caps)
		}()

		// Wait until ProbePlayMetadata promotes the in-flight entry before releasing Resolve.
		id := identity{userID: owner.ID, clientID: caps.ClientID, key: movieKey, surface: h.m.playbackSurface(string(caps.Protocol))}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			h.m.mu.Lock()
			e := h.m.warm[id]
			h.m.mu.Unlock()
			if e != nil && e.isPromoted() {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(gate)

		if err := <-done; err != nil {
			t.Fatalf("ProbePlayMetadata join failed: %v", err)
		}
		if r, p := counts(h); r != 1 || p != 1 {
			t.Fatalf("joined flight repeated work: resolves=%d probes=%d", r, p)
		}
	})

	t.Run("retries-after-speculative-failure", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, []string{"r1"})
		specGate := make(chan struct{})
		specEntered := make(chan struct{}, 1)
		// First resolve (speculative) blocks until Play joins, then fails;
		// the foreground retry must run and succeed.
		h.m.deps.Resolver = &playProbeFuncResolver{
			Resolver: h.resolver,
			fn: func(ctx context.Context, req core.ResolveRequest) (*core.Resolution, error) {
				if core.IndexerPurpose(ctx) == core.IndexerPurposeWarmup {
					select {
					case specEntered <- struct{}{}:
					default:
					}
					<-specGate
					return nil, core.ErrForegroundOnly
				}
				return h.resolver.Resolve(ctx, req)
			},
		}
		caps := defaultCaps("plezy-retry")

		if !h.m.Prewarm(context.Background(), owner, movieKey, caps, core.PrewarmOptions{Reason: core.PrewarmDetail, PlayIntent: true}) {
			t.Fatal("speculative prewarm skipped")
		}
		<-specEntered

		done := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			done <- h.m.ProbePlayMetadata(ctx, owner, movieKey, caps)
		}()

		id := identity{userID: owner.ID, clientID: caps.ClientID, key: movieKey, surface: h.m.playbackSurface(string(caps.Protocol))}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			h.m.mu.Lock()
			e := h.m.warm[id]
			h.m.mu.Unlock()
			if e != nil && e.isPromoted() {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(specGate)

		if err := <-done; err != nil {
			t.Fatalf("ProbePlayMetadata retry failed: %v", err)
		}
		if info, ok := h.m.WarmProbeInfo(owner, caps, movieKey); !ok || info.Estimated || len(info.Streams) == 0 {
			t.Fatalf("retry did not publish probed tracks: ok=%v info=%+v", ok, info)
		}
	})
}

type playProbeFuncResolver struct {
	core.Resolver
	fn func(context.Context, core.ResolveRequest) (*core.Resolution, error)
}

func (r *playProbeFuncResolver) Resolve(ctx context.Context, req core.ResolveRequest) (*core.Resolution, error) {
	return r.fn(ctx, req)
}

func TestProbePlayMetadataCancellationKeepsFlight(t *testing.T) {
	t.Parallel()
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, []string{"r1"})
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	h.m.deps.Resolver = &playProbeGatingResolver{Resolver: h.resolver, gate: gate, entered: entered}
	caps := defaultCaps("plezy-cancel")

	cctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- h.m.ProbePlayMetadata(cctx, owner, movieKey, caps)
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ProbePlayMetadata returned %v, want context.Canceled", err)
	}
	close(gate)
	waitWarm(t, h, owner.ID, caps.ClientID, movieKey)
	if _, ok := h.m.WarmProbeInfo(owner, caps, movieKey); !ok {
		t.Fatal("shared flight lost after caller cancelled")
	}
}

func TestTakeWarmResolutionAdoptsPlayProbeForClientlessStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, []string{"r1"})
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	h.m.deps.Resolver = &playProbeGatingResolver{Resolver: h.resolver, gate: gate, entered: entered}
	plezyCaps := defaultCaps("plezy-tv")

	// Metadata re-ask starts the shared playProbe flight and then gives up its wait.
	cctx, cancel := context.WithCancel(context.Background())
	metaDone := make(chan error, 1)
	go func() {
		metaDone <- h.m.ProbePlayMetadata(cctx, owner, movieKey, plezyCaps)
	}()
	<-entered
	cancel()
	if err := <-metaDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ProbePlayMetadata returned %v, want context.Canceled", err)
	}

	// Clientless libmpv file.mkv arrives under a synthetic native-* device ID
	// while the flight is still resolving and must join and adopt it directly.
	nativeCaps := defaultCaps("native-deadbeef")
	nativeID := identity{
		userID:   owner.ID,
		clientID: nativeCaps.ClientID,
		key:      movieKey,
		surface:  h.m.playbackSurface(string(nativeCaps.Protocol)),
	}
	profile := h.m.selectProfile(nativeCaps, owner, movieKey)
	takeDone := make(chan *warmEntry, 1)
	go func() {
		takeDone <- h.m.takeWarmResolution(context.Background(), nativeID, profile, nativeCaps, "", "")
	}()
	close(gate)

	warm := <-takeDone
	if warm == nil || warm.res == nil || warm.probe == nil {
		t.Fatalf("clientless stream did not adopt in-flight playProbe warm entry: %+v", warm)
	}
	if r, p := counts(h); r != 1 || p != 1 {
		t.Fatalf("clientless stream repeated work: resolves=%d probes=%d, want 1/1", r, p)
	}

	// A non-playProbe, non-promoted warm entry stays scoped to its own clientID.
	h2 := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, []string{"r1"})
	otherCaps := defaultCaps("other-client")
	if !h2.m.Prewarm(context.Background(), owner, movieKey, otherCaps, core.PrewarmOptions{Reason: core.PrewarmDetail}) {
		t.Fatal("speculative prewarm did not start")
	}
	waitWarm(t, h2, owner.ID, otherCaps.ClientID, movieKey)
	otherNative := defaultCaps("native-other")
	otherNativeID := identity{
		userID:   owner.ID,
		clientID: otherNative.ClientID,
		key:      movieKey,
		surface:  h2.m.playbackSurface(string(otherNative.Protocol)),
	}
	if got := h2.m.takeWarmResolution(context.Background(), otherNativeID, h2.m.selectProfile(otherNative, owner, movieKey), otherNative, "", ""); got != nil {
		t.Fatalf("non-playProbe speculative entry cross-adopted by another clientID: %+v", got)
	}
}

func TestProbePlayMetadataJoinsActivePrepareFlight(t *testing.T) {
	t.Parallel()
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, []string{"r1"})
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	h.m.deps.Resolver = &playProbeGatingResolver{Resolver: h.resolver, gate: gate, entered: entered}
	caps := defaultCaps("plex-linux-758")

	prepDone := make(chan error, 1)
	go func() {
		_, err := h.m.Prepare(context.Background(), owner, movieKey, caps)
		prepDone <- err
	}()
	<-entered
	if !h.m.HasActivePrepare(owner, movieKey, caps) {
		t.Fatal("HasActivePrepare = false while Prepare flight is active")
	}

	// Cancelling a metadata waiter must not cancel the live Prepare flight.
	cctx, cancel := context.WithCancel(context.Background())
	cancelDone := make(chan error, 1)
	go func() {
		cancelDone <- h.m.ProbePlayMetadata(cctx, owner, movieKey, caps)
	}()
	cancel()
	if err := <-cancelDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ProbePlayMetadata returned %v, want context.Canceled", err)
	}

	// Another metadata waiter joins the in-flight Prepare and succeeds once it lands.
	probeDone := make(chan error, 1)
	go func() {
		probeDone <- h.m.ProbePlayMetadata(context.Background(), owner, movieKey, caps)
	}()
	close(gate)

	if err := <-prepDone; err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := <-probeDone; err != nil {
		t.Fatalf("ProbePlayMetadata did not join active Prepare flight: %v", err)
	}
	if h.m.HasActivePrepare(owner, movieKey, caps) {
		t.Fatal("HasActivePrepare = true after Prepare completed")
	}
	if r, p := counts(h); r != 1 || p != 1 {
		t.Fatalf("ProbePlayMetadata repeated work alongside Prepare: resolves=%d probes=%d, want 1/1", r, p)
	}
}
