package playback

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type trackedMediaHLS struct {
	*fakeHLS
	mu        sync.Mutex
	lastMedia time.Time
}

func (h *trackedMediaHLS) LastMediaAccess() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastMedia
}

func (h *trackedMediaHLS) setMediaAccess(at time.Time) {
	h.mu.Lock()
	h.lastMedia = at
	h.mu.Unlock()
}

func TestLivenessAbandonedSessionExpiresCleanlyAndRecordsHistory(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute)}, []string{"A"},
		func(_ *harness, o *Options) { o.IdleTimeout = 5 * time.Minute })

	var mu sync.Mutex
	var stops []string
	h.m.Subscribe(func(ev Event) {
		if ev.Kind == "stop" && ev.Session != nil {
			mu.Lock()
			stops = append(stops, ev.Session.ID)
			mu.Unlock()
		}
	})

	ps := h.prepare(owner, movieKey, "vela-api-test")
	// Never served media bytes and never reported a position: not visible or active.
	snap, ok := h.m.Session(ps.ID)
	if !ok {
		t.Fatal("prepared session missing")
	}
	if core.SessionVisible(snap) || core.SessionActiveStream(snap) {
		t.Fatalf("unplayed session must not be visible or active: liveness=%q", snap.Liveness)
	}
	if h.m.SessionVisible(ps.ID) {
		t.Fatal("SessionVisible reported true for never-played session")
	}

	// HEAD requests serve zero media bytes and must not mark the session playing.
	srv := directPlayServer(t, h.m, ps.ID)
	req, _ := http.NewRequest(http.MethodHead, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if snap, _ := h.m.Session(ps.ID); core.SessionVisible(snap) || core.SessionActiveStream(snap) {
		t.Fatalf("HEAD request falsely marked session playing: liveness=%q", snap.Liveness)
	}

	// Repeated SSE KeepAlive pings must not keep a never-started session alive past 2m.
	for range 9 {
		h.clock.Advance(15 * time.Second)
		h.m.KeepAlive(ps.ID)
		h.m.expire()
	}
	if _, ok := h.m.Session(ps.ID); ok {
		t.Fatal("abandoned session survived past 2m despite zero bytes and zero position")
	}
	eventually(t, "abandoned session source closed", func() bool { return h.engine.sources[0].closed.Load() })
	if h.cache.pinned("A", PinReasonLive) {
		t.Fatal("abandoned session left live pin behind")
	}
	mu.Lock()
	gotStops := append([]string(nil), stops...)
	mu.Unlock()
	if len(gotStops) != 1 || gotStops[0] != ps.ID {
		t.Fatalf("expected clean stop history event for expired session, got %v", gotStops)
	}
}

func TestLivenessDirectPlayWindowAndLongInFlightRead(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute)}, []string{"A"},
		func(_ *harness, o *Options) { o.IdleTimeout = 2 * time.Minute })

	ps := h.prepare(owner, movieKey, "direct-client")
	srv := directPlayServer(t, h.m, ps.ID)

	// Serve a byte range: session becomes playing immediately.
	resp, _, err := get(t, srv.URL, map[string]string{"Range": "bytes=0-1023"})
	if err != nil || resp.StatusCode != 206 {
		t.Fatalf("range read: %d %v", resp.StatusCode, err)
	}
	if snap, _ := h.m.Session(ps.ID); snap.Liveness != core.SessionLivenessPlaying || !core.SessionActiveStream(snap) {
		t.Fatalf("expected playing after range read, got %+v", snap.Liveness)
	}

	// Still playing at 45s (PlayingEvidenceWindowDirect).
	h.clock.Advance(core.PlayingEvidenceWindowDirect)
	if snap, _ := h.m.Session(ps.ID); snap.Liveness != core.SessionLivenessPlaying {
		t.Fatalf("dropped playing too early at 45s: %q", snap.Liveness)
	}

	// Silent at 46s: no longer playing or visible (like an abandoned API test 1 minute later).
	h.clock.Advance(time.Second)
	if snap, _ := h.m.Session(ps.ID); snap.Liveness != core.SessionLivenessIdle || core.SessionVisible(snap) || core.SessionActiveStream(snap) {
		t.Fatalf("stale direct session still playing/visible at 46s: %q", snap.Liveness)
	}

	// Simulate a long in-flight direct-play range read: must stay playing and never expire even past IdleTimeout.
	s := h.m.lookup(ps.ID)
	s.activeReads.Add(1)
	h.clock.Advance(10 * time.Minute)
	h.m.expire()
	if snap, ok := h.m.Session(ps.ID); !ok || snap.Liveness != core.SessionLivenessPlaying || !core.SessionActiveStream(snap) {
		t.Fatalf("long in-flight read was dropped or not playing: ok=%v liveness=%v", ok, snap)
	}
	s.activeReads.Add(-1)

	// Once the in-flight read ends and time passes beyond IdleTimeout with no new reads, it expires cleanly.
	h.clock.Advance(2*time.Minute + time.Second)
	h.m.expire()
	if _, ok := h.m.Session(ps.ID); ok {
		t.Fatal("idle session did not expire after in-flight read ended")
	}
}

func TestLivenessHLSSegmentWindow(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute)}, []string{"A"},
		func(_ *harness, o *Options) { o.IdleTimeout = 2 * time.Minute })

	ps := h.prepare(owner, movieKey, "hls-client")
	hls := &trackedMediaHLS{fakeHLS: &fakeHLS{id: ps.ID}}
	h.m.deps.HLSLookup = func(id string) (core.HLSSession, bool) {
		if id == ps.ID {
			return hls, true
		}
		return nil, false
	}

	// Before any segment fetch, HLS session is not playing.
	if snap, _ := h.m.Session(ps.ID); core.SessionVisible(snap) || core.SessionActiveStream(snap) {
		t.Fatalf("HLS session before segment fetch should not be playing: %q", snap.Liveness)
	}

	// Segment fetch marks it playing for PlayingEvidenceWindowHLS (60s).
	hls.setMediaAccess(h.clock.Now())
	h.clock.Advance(core.PlayingEvidenceWindowHLS)
	if snap, _ := h.m.Session(ps.ID); snap.Liveness != core.SessionLivenessPlaying || !core.SessionActiveStream(snap) {
		t.Fatalf("HLS session should stay playing through 60s window: %q", snap.Liveness)
	}

	// Past 60s without another segment fetch or progress report: idle & hidden.
	h.clock.Advance(time.Second)
	if snap, _ := h.m.Session(ps.ID); snap.Liveness != core.SessionLivenessIdle || core.SessionVisible(snap) || core.SessionActiveStream(snap) {
		t.Fatalf("HLS session should be idle past 60s: %q", snap.Liveness)
	}

	// Past 2m: expires cleanly and stops transcoder.
	h.clock.Advance(time.Minute)
	h.m.expire()
	if _, ok := h.m.Session(ps.ID); ok {
		t.Fatal("stale HLS session did not expire")
	}
}

func TestLivenessPausedVisibleButExcludedFromLimitsAndExpiresAfterRetention(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Hour)}, []string{"A"}, func(h *harness, o *Options) {
		h.cfg.Server.MaxStreams = 1
		o.IdleTimeout = 2 * time.Minute
	})
	ps := h.prepare(owner, movieKey, "plezy-android")
	// Start playing, then explicitly pause (force-stopped Plezy scenario).
	h.m.PlaybackProgress(owner.ID, "plezy-android", movieKey, 5*time.Minute, "playing")
	if !h.m.SessionActive(ps.ID) {
		t.Fatal("playing session should count for stream limit")
	}
	if _, err := h.m.Prepare(context.Background(), friend, movie2, defaultCaps("other")); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("expected ErrStreamLimit while playing, got %v", err)
	}

	// Client reports paused: visible as Paused, excluded from stream limits and active stream counts.
	h.m.PlaybackProgress(owner.ID, "plezy-android", movieKey, 5*time.Minute, "paused")
	snap, ok := h.m.Session(ps.ID)
	if !ok {
		t.Fatal("paused session disappeared immediately")
	}
	if snap.Liveness != core.SessionLivenessPaused || !core.SessionVisible(snap) || core.SessionActiveStream(snap) || core.SessionCountsForLimit(snap) {
		t.Fatalf("paused session liveness wrong: %+v", snap.Liveness)
	}
	if h.m.SessionActive(ps.ID) {
		t.Fatal("paused session must not count for stream limits")
	}
	if !h.m.SessionVisible(ps.ID) {
		t.Fatal("paused session must remain visible")
	}

	// Another viewer is NOT blocked by the paused session.
	other, err := h.m.Prepare(context.Background(), friend, movie2, defaultCaps("other"))
	if err != nil {
		t.Fatalf("paused session blocked another viewer: %v", err)
	}
	h.m.Stop(other.ID)

	// Retained up to PausedSessionRetention (27m still paused, not active).
	h.clock.Advance(27 * time.Minute)
	h.m.expire()
	if snap, ok := h.m.Session(ps.ID); !ok || snap.Liveness != core.SessionLivenessPaused || core.SessionActiveStream(snap) {
		t.Fatalf("at 27m paused session: ok=%v snap=%v", ok, snap)
	}

	// Past 30m retention: expires cleanly.
	h.clock.Advance(4 * time.Minute)
	h.m.expire()
	if _, ok := h.m.Session(ps.ID); ok {
		t.Fatal("paused session survived past PausedSessionRetention")
	}
}

func TestLivenessDirectPlayPausedRetention(t *testing.T) {
	cases := []struct {
		name      string
		minutes   int
		stillOpen time.Duration
		expireAt  time.Duration
	}{
		{"default-zero-30m", 0, 29 * time.Minute, 30*time.Minute + time.Second},
		{"short-5m", 5, 4 * time.Minute, 5*time.Minute + time.Second},
		{"above-30m-60m", 60, 35 * time.Minute, 60*time.Minute + time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Hour)}, []string{"A"}, func(h *harness, o *Options) {
				h.cfg.Server.PausedStreamMinutes = tc.minutes
				o.IdleTimeout = 2 * time.Minute
			})
			// Direct play only: no transcoder session.
			h.m.deps.HLSLookup = func(string) (core.HLSSession, bool) { return nil, false }

			ps := h.prepare(owner, movieKey, "direct-client")
			srv := directPlayServer(t, h.m, ps.ID)
			resp, _, err := get(t, srv.URL, map[string]string{"Range": "bytes=0-1023"})
			if err != nil || resp.StatusCode != 206 {
				t.Fatalf("direct range read: %d %v", resp.StatusCode, err)
			}
			if snap, ok := h.m.Session(ps.ID); !ok || snap.Decision != core.DecisionDirectPlay {
				t.Fatalf("expected direct-play session, got ok=%v decision=%v", ok, snap)
			}

			h.m.PlaybackProgress(owner.ID, "direct-client", movieKey, 5*time.Minute, "paused")

			h.clock.Advance(tc.stillOpen)
			h.m.expire()
			if snap, ok := h.m.Session(ps.ID); !ok || snap.Liveness != core.SessionLivenessPaused {
				t.Fatalf("direct-play paused session expired too early at %v: ok=%v snap=%v", tc.stillOpen, ok, snap)
			}

			h.clock.Advance(tc.expireAt - tc.stillOpen)
			h.m.expire()
			if _, ok := h.m.Session(ps.ID); ok {
				t.Fatalf("direct-play paused session survived past %v", tc.expireAt)
			}
		})
	}

	t.Run("instance-scoped-and-live-reload", func(t *testing.T) {
		hShort := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Hour)}, []string{"A"}, func(h *harness, o *Options) {
			h.cfg.Server.PausedStreamMinutes = 5
			o.IdleTimeout = 2 * time.Minute
		})
		hLong := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Hour)}, []string{"A"}, func(h *harness, o *Options) {
			h.cfg.Server.PausedStreamMinutes = 60
			o.IdleTimeout = 2 * time.Minute
		})
		noHLS := func(string) (core.HLSSession, bool) { return nil, false }
		hShort.m.deps.HLSLookup = noHLS
		hLong.m.deps.HLSLookup = noHLS

		psShort := hShort.prepare(owner, movieKey, "direct-short")
		psLong := hLong.prepare(owner, movieKey, "direct-long")
		hShort.m.PlaybackProgress(owner.ID, "direct-short", movieKey, time.Minute, "paused")
		hLong.m.PlaybackProgress(owner.ID, "direct-long", movieKey, time.Minute, "paused")

		// Advance both past 5m: only hShort expires.
		hShort.clock.Advance(5*time.Minute + time.Second)
		hLong.clock.Advance(35 * time.Minute)
		hShort.m.expire()
		hLong.m.expire()
		if _, ok := hShort.m.Session(psShort.ID); ok {
			t.Fatal("hShort session survived past 5m")
		}
		if snap, ok := hLong.m.Session(psLong.ID); !ok || snap.Liveness != core.SessionLivenessPaused {
			t.Fatalf("hLong session affected by hShort: ok=%v snap=%v", ok, snap)
		}

		// Live config reload back to 0 restores 30m default on hLong, expiring the 35m-paused session.
		cfg := hLong.cfg
		cfg.Server.PausedStreamMinutes = 0
		hLong.m.SetConfig(cfg)
		hLong.m.expire()
		if _, ok := hLong.m.Session(psLong.ID); ok {
			t.Fatal("hLong session survived after live reload back to 30m default")
		}
	})
}
