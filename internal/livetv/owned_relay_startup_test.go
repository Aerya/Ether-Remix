package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

func renderOwnedPlaylist(tl *ownedTimeline) string {
	return string(tl.render(
		func(seq int64, ext string) string { return strconv.FormatInt(seq, 10) + "." + ext },
		func(name string) string { return name },
	))
}

func newSyntheticRelayTimeline(now func() time.Time, cut time.Duration) *ownedTimeline {
	tl := newOwnedTimeline("relay-startup", slog.New(slog.NewTextHandler(io.Discard, nil)), now, 2*cut, nil)
	tl.windowSpan = 24 * time.Second
	tl.startBehind = 6 * time.Second
	tl.expectStart(2, relayTargetFloor(cut))
	tl.startMinMedia = 2 * cut
	tl.startMedia = max(2*cut, 4*time.Second)
	tl.startWait = relayStartWait
	return tl
}

func TestOwnedRelayStartupDepthHoldsShortOpeningSegment(t *testing.T) {
	t.Run("holds single 1.6s segment until 2.4s segment arrives", func(t *testing.T) {
		clock := time.Unix(1_000_000, 0)
		tl := newSyntheticRelayTimeline(func() time.Time { return clock }, 2*time.Second)
		advance := func(d time.Duration) {
			for end := clock.Add(d); clock.Before(end); {
				clock = clock.Add(ownedTick)
				tl.mu.Lock()
				tl.tickLocked(clock)
				tl.mu.Unlock()
			}
		}

		// Emulates post-aligned-start ingest: segment 0 was dropped, so segment 1
		// is a single short 1.6 s CMAF segment.
		tl.ingest(&ownedSegment{dur: 1600 * time.Millisecond, ext: "m4s", data: []byte("seg1")})
		if tl.started() || !tl.hasPending() {
			t.Fatalf("timeline started early after 1.6 s segment: started=%v pending=%v", tl.started(), tl.hasPending())
		}

		// Segment 2 takes 3.8 s to appear; the first playlist stays held the
		// entire time instead of publishing segments=1 after 100 ms.
		advance(3700 * time.Millisecond)
		if tl.started() {
			t.Fatal("timeline published single short segment before segment 2 arrived")
		}
		if rem := tl.startWaitRemaining(); rem != 2300*time.Millisecond {
			t.Fatalf("startWaitRemaining = %v, want 2.3s", rem)
		}

		advance(100 * time.Millisecond)
		tl.ingest(&ownedSegment{dur: 2400 * time.Millisecond, ext: "m4s", data: []byte("seg2")})
		if !tl.started() || tl.hasPending() {
			t.Fatalf("timeline did not publish once 4.0 s across 2 segments was ready: started=%v pending=%v", tl.started(), tl.hasPending())
		}

		body := renderOwnedPlaylist(tl)
		target, segs := parseOwned(t, []byte(body))
		if target != 2 || len(segs) != 2 {
			t.Fatalf("got target=%d segments=%d (want target=2 segments=2):\n%s", target, len(segs), body)
		}
		if segs[0].dur != 1.6 || segs[1].dur != 2.4 {
			t.Fatalf("unexpected segment durations: %+v", segs)
		}
		if !strings.Contains(body, "#EXT-X-START:TIME-OFFSET=-4.000,PRECISE=NO") {
			t.Fatalf("expected clamped EXT-X-START=-4.000:\n%s", body)
		}
	})

	t.Run("holds two 1.6s segments until third satisfies ~2x target depth", func(t *testing.T) {
		clock := time.Unix(1_000_000, 0)
		tl := newSyntheticRelayTimeline(func() time.Time { return clock }, 2*time.Second)

		tl.ingest(&ownedSegment{dur: 1600 * time.Millisecond, ext: "m4s", data: []byte("seg1")})
		clock = clock.Add(1600 * time.Millisecond)
		tl.ingest(&ownedSegment{dur: 1600 * time.Millisecond, ext: "m4s", data: []byte("seg2")})
		if tl.started() {
			t.Fatal("timeline published 3.2 s across 2 segments before reaching ~2x target (4 s)")
		}

		clock = clock.Add(1600 * time.Millisecond)
		tl.ingest(&ownedSegment{dur: 1600 * time.Millisecond, ext: "m4s", data: []byte("seg3")})
		if !tl.started() {
			t.Fatal("timeline did not publish after 3 x 1.6 s (4.8 s)")
		}

		body := renderOwnedPlaylist(tl)
		target, segs := parseOwned(t, []byte(body))
		if target != 2 || len(segs) != 3 {
			t.Fatalf("got target=%d segments=%d (want target=2 segments=3):\n%s", target, len(segs), body)
		}
		if !strings.Contains(body, "#EXT-X-START:TIME-OFFSET=-4.800,PRECISE=NO") {
			t.Fatalf("expected clamped EXT-X-START=-4.800:\n%s", body)
		}
	})

	t.Run("publishes what exists at bounded startup budget with clamped EXT-X-START", func(t *testing.T) {
		clock := time.Unix(1_000_000, 0)
		tl := newSyntheticRelayTimeline(func() time.Time { return clock }, 2*time.Second)
		advance := func(d time.Duration) {
			for end := clock.Add(d); clock.Before(end); {
				clock = clock.Add(ownedTick)
				tl.mu.Lock()
				tl.tickLocked(clock)
				tl.mu.Unlock()
			}
		}

		tl.ingest(&ownedSegment{dur: 1600 * time.Millisecond, ext: "m4s", data: []byte("seg1")})
		advance(relayStartWait - ownedTick)
		if tl.started() {
			t.Fatalf("timeline published before startup budget %v elapsed", relayStartWait)
		}

		advance(ownedTick)
		if !tl.started() {
			t.Fatalf("timeline failed to publish what exists after startup budget %v", relayStartWait)
		}
		body := renderOwnedPlaylist(tl)
		target, segs := parseOwned(t, []byte(body))
		if target != 2 || len(segs) != 1 {
			t.Fatalf("got target=%d segments=%d (want target=2 segments=1):\n%s", target, len(segs), body)
		}
		if !strings.Contains(body, "#EXT-X-START:TIME-OFFSET=-1.600,PRECISE=NO") {
			t.Fatalf("expected EXT-X-START clamped to held 1.6 s window:\n%s", body)
		}
	})

	t.Run("splice before first publication resets startup budget", func(t *testing.T) {
		clock := time.Unix(1_000_000, 0)
		tl := newSyntheticRelayTimeline(func() time.Time { return clock }, 2*time.Second)

		tl.ingest(&ownedSegment{dur: 1600 * time.Millisecond, ext: "m4s", data: []byte("old")})
		clock = clock.Add(4 * time.Second)
		tl.splice(true)
		if tl.hasPending() {
			t.Fatal("discard splice kept pending segment")
		}

		// Replacement producer ingests a 1.6 s segment at t=4s; at t=7.8s it is
		// past the original 6 s mark but only 3.8 s into its own budget.
		tl.ingest(&ownedSegment{dur: 1600 * time.Millisecond, ext: "m4s", data: []byte("new1")})
		clock = clock.Add(3800 * time.Millisecond)
		tl.mu.Lock()
		tl.tickLocked(clock)
		tl.mu.Unlock()
		if tl.started() {
			t.Fatal("replacement producer inherited stale startup clock from discarded producer")
		}

		tl.ingest(&ownedSegment{dur: 2400 * time.Millisecond, ext: "m4s", data: []byte("new2")})
		if !tl.started() {
			t.Fatal("replacement producer did not publish once 2 segments totaling 4 s arrived")
		}
		_, segs := parseOwned(t, []byte(renderOwnedPlaylist(tl)))
		if len(segs) != 2 {
			t.Fatalf("segments=%d, want 2", len(segs))
		}
	})

	t.Run("muxed passthrough and 4s Live TV relay openings stay fast", func(t *testing.T) {
		clock := time.Unix(1_000_000, 0)
		pt := newOwnedTimeline("passthrough", slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return clock }, 0, nil)
		pt.expectStart(2, 2*time.Second)
		pt.ingest(&ownedSegment{dur: 1600 * time.Millisecond, ext: "ts", data: []byte("pt1")})
		clock = clock.Add(ownedTick)
		pt.mu.Lock()
		pt.tickLocked(clock)
		pt.mu.Unlock()
		if !pt.started() {
			t.Fatal("muxed passthrough delayed past single tick")
		}

		relay4s := newSyntheticRelayTimeline(func() time.Time { return clock }, 2*time.Second)
		relay4s.ingest(&ownedSegment{dur: 4 * time.Second, ext: "ts", data: []byte("tv1")})
		if !relay4s.started() {
			t.Fatal("4 s opening Live TV relay segment should publish immediately")
		}
	})
}

// timedRelayHLS simulates a demuxed CMAF LL-HLS relay output whose first
// aligned segment is 1.6 s and whose second 2.4 s segment appears after a gap.
type timedRelayHLS struct {
	mu      sync.Mutex
	seg1At  time.Time
	seg2At  time.Time
	closed  bool
	initBox []byte
}

func (f *timedRelayHLS) ID() string            { return "synthetic-relay" }
func (f *timedRelayHLS) LastAccess() time.Time { return time.Time{} }
func (f *timedRelayHLS) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
func (f *timedRelayHLS) ServePlaylist(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	f.mu.Lock()
	seg1At, seg2At := f.seg1At, f.seg2At
	f.mu.Unlock()
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n")
	if !now.Before(seg1At) {
		b.WriteString("#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:1.600,\n00000.m4s\n")
	}
	if !seg2At.IsZero() && !now.Before(seg2At) {
		b.WriteString("#EXTINF:2.400,\n00001.m4s\n")
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	_, _ = io.WriteString(w, b.String())
}
func (f *timedRelayHLS) ServeSegment(w http.ResponseWriter, r *http.Request, n int) {
	w.Header().Set("Content-Type", "video/mp4")
	fmt.Fprintf(w, "seg-%d", n)
}
func (f *timedRelayHLS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "video/mp4")
	if len(f.initBox) > 0 {
		_, _ = w.Write(f.initBox)
		return
	}
	_, _ = io.WriteString(w, "ftyp-moov")
}
func (f *timedRelayHLS) SwapInput(context.Context, string, core.MediaInfo, time.Duration) error {
	return nil
}

func TestOwnedRelayTuneReadyAndPlaylistTiming(t *testing.T) {
	for _, bestEffort := range []bool{false, true} {
		name := "standard"
		if bestEffort {
			name = "best-effort"
		}
		t.Run("relay-"+name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				origin := time.Now()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				s := &Service{ctx: ctx, o: Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}

				ss := &session{
					info:          SessionInfo{ID: "direct-live", UserID: 1, Channel: Channel{ID: directChannelPrefix + "1", Name: "Public Live Stream", Kind: "iptv"}},
					caps:          core.ClientCaps{BestEffortLiveStart: bestEffort},
					ctx:           ctx,
					ready:         true,
					startDeadline: origin.Add(8 * time.Second),
				}
				// Mark the upstream HLS input healthy and flowing so the copy
				// producer's keyframe-starvation timer is active during startup.
				g := &inputGrant{ctx: ctx}
				g.snap.Store(&healthSnap{h: UpstreamHealth{Kind: "hls"}})
				s.inputRelay.health.Store(ss, g)

				info := core.MediaInfo{Container: "hls", Streams: []core.StreamInfo{{Type: "video", Codec: "h264"}, {Type: "audio", Codec: "aac"}}}
				dec := core.Decision{Container: transcode.ContainerFMP4, VideoCopy: true, AudioCopy: true}
				p := s.newRelayProducer(ss, info, ss.caps, dec, 2*time.Second)
				p.tl.slates = nil
				hls := &timedRelayHLS{
					seg1At: origin.Add(1 * time.Second),
					seg2At: origin.Add(4800 * time.Millisecond), // 3.8 s after seg 1
				}
				if err := p.adopt("synthetic-relay", hls, func() {}, info, dec, 2*time.Second); err != nil {
					t.Fatal(err)
				}
				lease := s.runRelay(ss, p)
				defer lease.release()
				ss.owned = lease

				type tuneResult struct {
					elapsed time.Duration
					err     error
				}
				done := make(chan tuneResult, 1)
				go func() {
					err := s.awaitOwnedStart(ctx, ss)
					done <- tuneResult{elapsed: time.Since(origin), err: err}
				}()

				// Advance past seg 1 (1.0 s), past BestEffortLiveStart's 700 ms
				// passthrough cap, and past the steady-state 3.0 s starvation
				// window (4.1 s): tune must still be waiting and copy must not
				// have escalated.
				time.Sleep(4500 * time.Millisecond)
				synctest.Wait()
				select {
				case r := <-done:
					t.Fatalf("awaitOwnedStart returned early at %v (err=%v) with only 1.6 s ingested", r.elapsed, r.err)
				default:
				}
				p.mu.Lock()
				escalated := p.escalated
				p.mu.Unlock()
				if escalated {
					t.Fatal("relay copy producer prematurely escalated to re-encode while holding first segment for startup depth")
				}

				// Advance past seg 2 (4.8 s); tune becomes ready and index.m3u8
				// returns immediately with both segments and clamped EXT-X-START.
				time.Sleep(500 * time.Millisecond)
				synctest.Wait()
				select {
				case r := <-done:
					if r.err != nil {
						t.Fatalf("awaitOwnedStart failed: %v", r.err)
					}
					if r.elapsed < 4800*time.Millisecond || r.elapsed > 5200*time.Millisecond {
						t.Fatalf("tune ready elapsed = %v, want ~4.8s", r.elapsed)
					}
				default:
					t.Fatal("awaitOwnedStart still blocked after segment 2 arrived")
				}

				reqStart := time.Now()
				w := httptest.NewRecorder()
				s.serveOwnedPlaylist(w, httptest.NewRequest(http.MethodGet, "/index.m3u8", nil), ss)
				if w.Code != http.StatusOK {
					t.Fatalf("serveOwnedPlaylist status=%d body=%s", w.Code, w.Body.String())
				}
				if d := time.Since(reqStart); d != 0 {
					t.Fatalf("serveOwnedPlaylist waited %v after awaitOwnedStart returned ready", d)
				}
				body := w.Body.String()
				target, segs := parseOwned(t, []byte(body))
				if target != 2 || len(segs) != 2 {
					t.Fatalf("playlist target=%d segs=%d (want target=2 segs=2):\n%s", target, len(segs), body)
				}
				if !strings.Contains(body, "#EXT-X-START:TIME-OFFSET=-4.000,PRECISE=NO") {
					t.Fatalf("expected clamped EXT-X-START=-4.000:\n%s", body)
				}
			})
		})
	}

	t.Run("pending segment finishes startup past tune deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			origin := time.Now()
			s := &Service{o: Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			tl := newSyntheticRelayTimeline(time.Now, 2*time.Second)
			go tl.run(ctx)
			ss := &session{
				info:          SessionInfo{ID: "late-seg1", UserID: 1},
				ctx:           ctx,
				ready:         true,
				startDeadline: origin.Add(5 * time.Second),
				owned:         &ownedLease{tl: tl, relay: &relayProducer{}},
			}

			done := make(chan error, 1)
			go func() { done <- s.awaitOwnedStart(ctx, ss) }()

			// Segment 1 arrives at t=4.0 s, before the 5.0 s tune deadline.
			time.Sleep(4 * time.Second)
			tl.ingest(&ownedSegment{dur: 1600 * time.Millisecond, ext: "m4s", data: []byte("seg1")})
			synctest.Wait()

			// The 5.0 s tune deadline fires while segment 1 is pending: do not
			// fail the tune while waiting for segment 2 inside relayStartWait.
			time.Sleep(2 * time.Second)
			synctest.Wait()
			select {
			case err := <-done:
				t.Fatalf("awaitOwnedStart exited early at t=6s with err=%v", err)
			default:
			}

			// Segment 2 arrives at t=7.8 s (3.8 s after seg 1).
			time.Sleep(1800 * time.Millisecond)
			tl.ingest(&ownedSegment{dur: 2400 * time.Millisecond, ext: "m4s", data: []byte("seg2")})
			synctest.Wait()

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("awaitOwnedStart failed: %v", err)
				}
			default:
				t.Fatal("awaitOwnedStart did not finish once segment 2 published")
			}

			w := httptest.NewRecorder()
			s.serveOwnedPlaylist(w, httptest.NewRequest(http.MethodGet, "/index.m3u8", nil), ss)
			if w.Code != http.StatusOK {
				t.Fatalf("playlist status=%d body=%s", w.Code, w.Body.String())
			}
			_, segs := parseOwned(t, w.Body.Bytes())
			if len(segs) != 2 {
				t.Fatalf("segments=%d, want 2", len(segs))
			}
		})
	})

	t.Run("startup budget expiry publishes single segment cleanly", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			origin := time.Now()
			s := &Service{o: Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			tl := newSyntheticRelayTimeline(time.Now, 2*time.Second)
			go tl.run(ctx)
			ss := &session{
				info:          SessionInfo{ID: "budget-cap", UserID: 1},
				ctx:           ctx,
				ready:         true,
				startDeadline: origin.Add(5 * time.Second),
				owned:         &ownedLease{tl: tl, relay: &relayProducer{}},
			}

			done := make(chan error, 1)
			go func() { done <- s.awaitOwnedStart(ctx, ss) }()

			time.Sleep(1 * time.Second)
			tl.ingest(&ownedSegment{dur: 1600 * time.Millisecond, ext: "m4s", data: []byte("seg1")})
			synctest.Wait()

			time.Sleep(relayStartWait - ownedTick)
			synctest.Wait()
			select {
			case err := <-done:
				t.Fatalf("published before startup budget expired: err=%v", err)
			default:
			}

			time.Sleep(2 * ownedTick)
			synctest.Wait()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("awaitOwnedStart failed on budget expiry: %v", err)
				}
			default:
				t.Fatal("awaitOwnedStart did not return after bounded startup budget expired")
			}

			w := httptest.NewRecorder()
			s.serveOwnedPlaylist(w, httptest.NewRequest(http.MethodGet, "/index.m3u8", nil), ss)
			if w.Code != http.StatusOK {
				t.Fatalf("playlist status=%d body=%s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			_, segs := parseOwned(t, []byte(body))
			if len(segs) != 1 || !strings.Contains(body, "#EXT-X-START:TIME-OFFSET=-1.600,PRECISE=NO") {
				t.Fatalf("unexpected budget-cap playlist:\n%s", body)
			}
		})
	})

	t.Run("dead relay with zero segments still times out at tune deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			origin := time.Now()
			s := &Service{o: Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			tl := newSyntheticRelayTimeline(time.Now, 2*time.Second)
			go tl.run(ctx)
			ss := &session{
				info:          SessionInfo{ID: "dead-relay", UserID: 1},
				ctx:           ctx,
				startDeadline: origin.Add(5 * time.Second),
				owned:         &ownedLease{tl: tl, relay: &relayProducer{}},
			}

			err := s.awaitOwnedStart(ctx, ss)
			var te *TuneError
			if !errors.As(err, &te) || te.Code != "timeout" || te.Stage != "owned_start" {
				t.Fatalf("err=%v, want TuneError{timeout, owned_start}", err)
			}
			if elapsed := time.Since(origin); elapsed != 5*time.Second {
				t.Fatalf("dead relay timed out at %v, want 5s", elapsed)
			}
		})
	})

	t.Run("muxed passthrough best-effort start keeps 700ms cap", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			origin := time.Now()
			s := &Service{o: Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			tl := newOwnedTimeline("pt", slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now, 0, nil)
			tl.expectStart(2, 2*time.Second)
			go tl.run(ctx)
			ss := &session{
				info:          SessionInfo{ID: "passthrough", UserID: 1},
				caps:          core.ClientCaps{BestEffortLiveStart: true},
				ctx:           ctx,
				startDeadline: origin.Add(8 * time.Second),
				owned:         &ownedLease{tl: tl}, // relay == nil
			}

			if err := s.awaitOwnedStart(ctx, ss); err != nil {
				t.Fatalf("passthrough best-effort returned err=%v", err)
			}
			if elapsed := time.Since(origin); elapsed != 700*time.Millisecond {
				t.Fatalf("passthrough best-effort waited %v, want 700ms", elapsed)
			}
		})
	})
}
