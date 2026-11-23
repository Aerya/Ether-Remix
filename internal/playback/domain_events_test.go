package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

func TestPlaybackEmitsAllDomainEvents(t *testing.T) {
	a := mkv("Movie.1999.2160p.WEB-DL", 128<<10, 10*time.Minute)
	b := mkv("Movie.1999.1080p.WEB-DL", 128<<10, 10*time.Minute)

	bus, rec := events.NewTestBus(t)
	reg := events.NewBuiltinRegistry()

	h := newHarness(t, []*fakeRelease{a, b}, []string{a.ID, b.ID})
	_ = h.m.Close()

	cfg := h.cfg
	cfg.Server.MaxStreams = 1

	m, err := New(Deps{
		Resolver:      h.resolver,
		Engine:        h.engine,
		Transcoder:    h.tr,
		Catalog:       h.catalog,
		Cache:         h.cache,
		State:         h.state,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return cfg.Quality.Profiles[0] },
		Events:        bus,
		EnrichEventContext: func(_ *core.PlaySession, _ *core.Title, base events.PlaybackSessionContext) events.PlaybackSessionContext {
			base.LibraryID = "movies"
			base.LibraryName = "Movies"
			base.Viewer.Tags = []string{"household"}
			return base
		},
		CachedMarkers: func(core.RatingKey, core.MediaInfo) []MarkerSpan {
			return []MarkerSpan{
				{Kind: "intro", StartMS: 30_000, EndMS: 90_000},
			}
		},
	}, cfg, Options{DataDir: h.dataDir, Now: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	caps := core.ClientCaps{
		ClientID:   "shield-tv",
		DeviceName: "Living Room Shield",
		Product:    "Vela",
		Platform:   "Android TV",
		Protocol:   "vela",
		ClientIP:   "203.0.113.42",
		Remote:     true,
		MaxHeight:  2160,
	}

	// 1. Prepare -> playback.session.started.
	ps, err := m.Prepare(context.Background(), owner, movieKey, caps)
	if err != nil {
		t.Fatal(err)
	}
	evStart := rec.WaitFor(t, events.EventPlaybackSessionStarted, time.Second)
	if err := reg.ValidatePayload(evStart.Type, evStart.Payload); err != nil {
		t.Fatalf("started invalid: %v", err)
	}
	startP, err := events.DecodePayload[events.PlaybackSessionStartedPayload](evStart)
	if err != nil {
		t.Fatal(err)
	}
	if startP.SessionID != ps.ID || startP.LibraryName != "Movies" || startP.Network.Location != "wan" || startP.Viewer.Role != "owner" {
		t.Fatalf("unexpected started payload: %+v", startP)
	}

	// 2. Concurrency exceeded -> playback.concurrency.exceeded (MaxStreams = 1).
	friend := &core.User{ID: 77, Username: "sam", StreamLimit: 1}
	if _, err := m.Prepare(context.Background(), friend, movie2, core.ClientCaps{ClientID: "phone-1", Product: "Vela iOS", ClientIP: "198.51.100.10", Remote: true}); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("expected ErrStreamLimit, got %v", err)
	}
	evConc := rec.WaitFor(t, events.EventPlaybackConcurrencyExceeded, time.Second)
	if err := reg.ValidatePayload(evConc.Type, evConc.Payload); err != nil {
		t.Fatalf("concurrency exceeded invalid: %v", err)
	}
	concP, err := events.DecodePayload[events.PlaybackConcurrencyExceededPayload](evConc)
	if err != nil {
		t.Fatal(err)
	}
	if concP.Limit != 1 || concP.ActiveStreams < 1 || concP.Viewer.Username != "sam" {
		t.Fatalf("unexpected concurrency exceeded payload: %+v", concP)
	}

	// 3. Initial decision -> playback.stream.decision.
	m.SetDecision(ps.ID, core.Decision{
		Kind:        core.DecisionTranscode,
		MaxHeight:   1080,
		BitrateKbps: 8000,
		Reason:      "audio codec truehd not supported",
	})
	evDec := rec.WaitFor(t, events.EventPlaybackStreamDecision, time.Second)
	if err := reg.ValidatePayload(evDec.Type, evDec.Payload); err != nil {
		t.Fatalf("stream decision invalid: %v", err)
	}
	decP, err := events.DecodePayload[events.PlaybackStreamDecisionPayload](evDec)
	if err != nil {
		t.Fatal(err)
	}
	if decP.Delivery != "transcode" || decP.MaxHeight != 1080 || decP.BitrateKbps != 8000 {
		t.Fatalf("unexpected stream decision payload: %+v", decP)
	}

	// 4. Quality change -> playback.stream.quality_changed.
	h.clock.Advance(2 * time.Second)
	m.SetDecision(ps.ID, core.Decision{
		Kind:        core.DecisionTranscode,
		MaxHeight:   720,
		BitrateKbps: 4000,
		Reason:      "viewer lowered remote quality",
	})
	evQual := rec.WaitFor(t, events.EventPlaybackStreamQualityChanged, time.Second)
	if err := reg.ValidatePayload(evQual.Type, evQual.Payload); err != nil {
		t.Fatalf("quality changed invalid: %v", err)
	}
	qualP, err := events.DecodePayload[events.PlaybackStreamQualityChangedPayload](evQual)
	if err != nil {
		t.Fatal(err)
	}
	if qualP.FromHeight != 1080 || qualP.ToHeight != 720 || qualP.FromBitrateKbps != 8000 || qualP.ToBitrateKbps != 4000 {
		t.Fatalf("unexpected quality changed payload: %+v", qualP)
	}

	// 5. Playing -> paused -> resumed.
	h.clock.Advance(time.Second)
	m.PlaybackProgress(owner.ID, "shield-tv", movieKey, 10*time.Second, "playing")
	h.clock.Advance(2 * time.Second)
	m.PlaybackProgress(owner.ID, "shield-tv", movieKey, 12*time.Second, "paused")
	evPaused := rec.WaitFor(t, events.EventPlaybackSessionPaused, time.Second)
	if err := reg.ValidatePayload(evPaused.Type, evPaused.Payload); err != nil {
		t.Fatalf("paused invalid: %v", err)
	}

	h.clock.Advance(5 * time.Second)
	m.PlaybackProgress(owner.ID, "shield-tv", movieKey, 12*time.Second, "playing")
	evResumed := rec.WaitFor(t, events.EventPlaybackSessionResumed, time.Second)
	if err := reg.ValidatePayload(evResumed.Type, evResumed.Payload); err != nil {
		t.Fatalf("resumed invalid: %v", err)
	}
	resP, err := events.DecodePayload[events.PlaybackSessionResumedPayload](evResumed)
	if err != nil {
		t.Fatal(err)
	}
	if resP.PausedForMS != 5000 {
		t.Fatalf("pausedForMs = %d, want 5000", resP.PausedForMS)
	}

	// 6. Seek into intro marker -> playback.stream.seeked + playback.marker.entered.
	h.clock.Advance(time.Second)
	m.PlaybackProgress(owner.ID, "shield-tv", movieKey, 45*time.Second, "playing")
	evSeek := rec.WaitFor(t, events.EventPlaybackStreamSeeked, time.Second)
	if err := reg.ValidatePayload(evSeek.Type, evSeek.Payload); err != nil {
		t.Fatalf("seeked invalid: %v", err)
	}
	seekP, err := events.DecodePayload[events.PlaybackStreamSeekedPayload](evSeek)
	if err != nil {
		t.Fatal(err)
	}
	if seekP.FromMS != 12_000 || seekP.ToMS != 45_000 || seekP.DeltaMS != 33_000 {
		t.Fatalf("unexpected seek payload: %+v", seekP)
	}

	evEnter := rec.WaitFor(t, events.EventPlaybackMarkerEntered, time.Second)
	if err := reg.ValidatePayload(evEnter.Type, evEnter.Payload); err != nil {
		t.Fatalf("marker entered invalid: %v", err)
	}
	enterP, err := events.DecodePayload[events.PlaybackMarkerEnteredPayload](evEnter)
	if err != nil {
		t.Fatal(err)
	}
	if enterP.MarkerKind != "intro" || enterP.StartMS != 30_000 || enterP.EndMS != 90_000 {
		t.Fatalf("unexpected marker entered payload: %+v", enterP)
	}

	// 7. Exit intro marker -> playback.marker.exited.
	h.clock.Advance(50 * time.Second)
	m.PlaybackProgress(owner.ID, "shield-tv", movieKey, 95*time.Second, "playing")
	evExit := rec.WaitFor(t, events.EventPlaybackMarkerExited, time.Second)
	if err := reg.ValidatePayload(evExit.Type, evExit.Payload); err != nil {
		t.Fatalf("marker exited invalid: %v", err)
	}
	exitP, err := events.DecodePayload[events.PlaybackMarkerExitedPayload](evExit)
	if err != nil {
		t.Fatal(err)
	}
	if exitP.MarkerKind != "intro" || exitP.PositionMS != 95_000 {
		t.Fatalf("unexpected marker exited payload: %+v", exitP)
	}

	// 8. Stall/rebuffer -> playback.stream.stalled.
	h.clock.Advance(time.Second)
	m.PlaybackProgress(owner.ID, "shield-tv", movieKey, 96*time.Second, "buffering")
	h.clock.Advance(4 * time.Second)
	m.PlaybackProgress(owner.ID, "shield-tv", movieKey, 96*time.Second, "playing")
	evStall := rec.WaitFor(t, events.EventPlaybackStreamStalled, time.Second)
	if err := reg.ValidatePayload(evStall.Type, evStall.Payload); err != nil {
		t.Fatalf("stalled invalid: %v", err)
	}

	// 9. Source failover -> playback.source.failover.
	h.clock.Advance(time.Second)
	if _, err := m.Failover(context.Background(), ps.ID, 96*time.Second); err != nil {
		t.Fatal(err)
	}
	evFO := rec.WaitFor(t, events.EventPlaybackSourceFailover, time.Second)
	if err := reg.ValidatePayload(evFO.Type, evFO.Payload); err != nil {
		t.Fatalf("failover invalid: %v", err)
	}
	foP, err := events.DecodePayload[events.PlaybackSourceFailoverPayload](evFO)
	if err != nil {
		t.Fatal(err)
	}
	if foP.FromReleaseID != a.ID || foP.ToReleaseID != b.ID || foP.Attempt < 1 {
		t.Fatalf("unexpected failover payload: %+v", foP)
	}

	// 10. NoteFailedStart -> playback.session.failed.
	m.NoteFailedStart(ps.ID, "client never rendered first frame")
	evFail := rec.WaitFor(t, events.EventPlaybackSessionFailed, time.Second)
	if err := reg.ValidatePayload(evFail.Type, evFail.Payload); err != nil {
		t.Fatalf("failed invalid: %v", err)
	}
	failP, err := events.DecodePayload[events.PlaybackSessionFailedPayload](evFail)
	if err != nil {
		t.Fatal(err)
	}
	if failP.Stage != "start" || failP.SessionID != ps.ID {
		t.Fatalf("unexpected failed payload: %+v", failP)
	}

	// 11. Stop -> playback.session.stopped.
	h.clock.Advance(3 * time.Second)
	m.Stop(ps.ID)
	evStop := rec.WaitFor(t, events.EventPlaybackSessionStopped, time.Second)
	if err := reg.ValidatePayload(evStop.Type, evStop.Payload); err != nil {
		t.Fatalf("stopped invalid: %v", err)
	}
	stopP, err := events.DecodePayload[events.PlaybackSessionStoppedPayload](evStop)
	if err != nil {
		t.Fatal(err)
	}
	if stopP.SessionID != ps.ID || stopP.PositionMS != 96_000 {
		t.Fatalf("unexpected stopped payload: %+v", stopP)
	}
}

func TestPlaybackMarkerTransitionPicksUpWarmedCacheInSameSession(t *testing.T) {
	a := mkv("Movie.1999.2160p.WEB-DL", 128<<10, 10*time.Minute)
	bus, rec := events.NewTestBus(t)
	h := newHarness(t, []*fakeRelease{a}, []string{a.ID})
	_ = h.m.Close()

	var warmed bool
	m, err := New(Deps{
		Resolver:      h.resolver,
		Engine:        h.engine,
		Transcoder:    h.tr,
		Catalog:       h.catalog,
		Cache:         h.cache,
		State:         h.state,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return h.cfg.Quality.Profiles[0] },
		Events:        bus,
		CachedMarkers: func(core.RatingKey, core.MediaInfo) []MarkerSpan {
			if !warmed {
				return nil
			}
			return []MarkerSpan{{Kind: "intro", StartMS: 30_000, EndMS: 90_000}}
		},
	}, h.cfg, Options{DataDir: h.dataDir, Now: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	caps := core.ClientCaps{ClientID: "jf-tv", Product: "Jellyfin", Protocol: "jellyfin", MaxHeight: 2160}
	if _, err := m.Prepare(context.Background(), owner, movieKey, caps); err != nil {
		t.Fatal(err)
	}
	// First playing tick sees an empty cache while background warm is in flight.
	h.clock.Advance(time.Second)
	m.PlaybackProgress(owner.ID, "jf-tv", movieKey, 5*time.Second, "playing")

	// Background warm completes and populates markers_cache; the same session picks it up.
	warmed = true
	h.clock.Advance(10 * time.Second)
	m.PlaybackProgress(owner.ID, "jf-tv", movieKey, 45*time.Second, "playing")

	evEnter := rec.WaitFor(t, events.EventPlaybackMarkerEntered, time.Second)
	enterP, err := events.DecodePayload[events.PlaybackMarkerEnteredPayload](evEnter)
	if err != nil {
		t.Fatal(err)
	}
	if enterP.MarkerKind != "intro" || enterP.StartMS != 30_000 || enterP.EndMS != 90_000 {
		t.Fatalf("unexpected marker entered payload: %+v", enterP)
	}
}
