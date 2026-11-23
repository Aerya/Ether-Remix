package livetv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

func TestMultiviewPlayoutFourTilesSupersedeAndStreamLimit(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nseg.ts\n")
	}))
	defer up.Close()

	s := testService(t, Options{HTTPClient: up.Client()})
	ctx := context.Background()

	var channels [5]Channel
	for i := range channels {
		c, err := s.SaveChannel(ctx, Channel{
			Name:      fmt.Sprintf("Ch %d", i+1),
			Number:    fmt.Sprintf("%d", i+1),
			Kind:      "iptv",
			Enabled:   true,
			StreamURL: fmt.Sprintf("%s/ch%d.m3u8", up.URL, i),
		})
		if err != nil {
			t.Fatal(err)
		}
		channels[i] = c
	}

	u := &core.User{ID: 1, StreamLimit: 1}
	groupID := "mv_group_one"
	var tileSess [4]SessionInfo
	for tile := 0; tile < 4; tile++ {
		info, err := s.Tune(ctx, u, channels[tile].ID, core.ClientCaps{
			ClientID:       "vela/apple-tv",
			MultiviewGroup: groupID,
			MultiviewTile:  tile,
		})
		if err != nil {
			t.Fatalf("tile %d failed with StreamLimit=1: %v", tile, err)
		}
		tileSess[tile] = info
	}
	if got := len(s.Sessions(ctx, u)); got != 4 {
		t.Fatalf("expected 4 concurrent multiview sessions, got %d", got)
	}

	// StreamLimit=1 counts the 4-tile group as 1, so another device is rejected.
	if _, err := s.Tune(ctx, u, channels[4].ID, core.ClientCaps{ClientID: "vela/iphone"}); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("second device bypassed StreamLimit=1: got %v, want ErrStreamLimit", err)
	}

	// With StreamLimit=2, a second device is admitted (group counts as 1, not 4),
	// and a third device is rejected.
	u2 := &core.User{ID: 1, StreamLimit: 2}
	otherDev, err := s.Tune(ctx, u2, channels[4].ID, core.ClientCaps{ClientID: "vela/iphone"})
	if err != nil {
		t.Fatalf("second device rejected with StreamLimit=2 (group should count as 1): %v", err)
	}
	if _, err := s.Tune(ctx, u2, channels[0].ID, core.ClientCaps{ClientID: "vela/ipad"}); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("third device bypassed StreamLimit=2: got %v, want ErrStreamLimit", err)
	}
	_ = s.Stop(ctx, otherDev.ID, u2)

	// Re-tuning tile 1 replaces ONLY tile 1; tiles 0, 2, 3 remain alive.
	replaced, err := s.Tune(ctx, u, channels[4].ID, core.ClientCaps{
		ClientID:       "vela/apple-tv",
		MultiviewGroup: groupID,
		MultiviewTile:  1,
	})
	if err != nil {
		t.Fatalf("replace tile 1: %v", err)
	}
	if _, err := s.Session(ctx, tileSess[1].ID, u); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("old tile 1 survived replacement: %v", err)
	}
	for _, keep := range []SessionInfo{tileSess[0], replaced, tileSess[2], tileSess[3]} {
		if _, err := s.Session(ctx, keep.ID, u); err != nil {
			t.Fatalf("expected tile session %q to survive: %v", keep.ID, err)
		}
	}

	// Non-group tune on the same device supersedes the entire 4-tile group.
	solo, err := s.Tune(ctx, u, channels[0].ID, core.ClientCaps{ClientID: "vela/apple-tv"})
	if err != nil {
		t.Fatalf("non-group tune: %v", err)
	}
	active := s.Sessions(ctx, u)
	if len(active) != 1 || active[0].ID != solo.ID {
		t.Fatalf("non-group tune did not supersede whole group: active=%d", len(active))
	}
}

func TestMultiviewProviderConnectionCapTypedErrorAndSameChannelSharing(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nseg.ts\n")
	}))
	defer up.Close()

	s := testService(t, Options{HTTPClient: up.Client()})
	ctx := context.Background()

	src, err := s.ImportSource(ctx, Source{
		ID:             "prov-acme",
		Name:           "Acme IPTV",
		MaxConnections: 2,
		Playlist:       fmt.Sprintf("#EXTM3U\n#EXTINF:-1,Seed\n%s/seed.m3u8\n", up.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	var chs [3]Channel
	for i := range chs {
		c, err := s.SaveChannel(ctx, Channel{
			Name:      fmt.Sprintf("Sports %d", i+1),
			Number:    fmt.Sprintf("%d", i+1),
			Kind:      "iptv",
			SourceID:  src.ID,
			Enabled:   true,
			StreamURL: fmt.Sprintf("%s/sport%d.m3u8", up.URL, i),
		})
		if err != nil {
			t.Fatal(err)
		}
		chs[i] = c
	}

	u := &core.User{ID: 3, StreamLimit: 4}
	// Tile 0 on Ch 0 and Tile 1 on Ch 1 consume both provider connections.
	if _, err := s.Tune(ctx, u, chs[0].ID, core.ClientCaps{ClientID: "vela/tv", MultiviewGroup: "mv_cap", MultiviewTile: 0}); err != nil {
		t.Fatalf("tile 0: %v", err)
	}
	if _, err := s.Tune(ctx, u, chs[1].ID, core.ClientCaps{ClientID: "vela/tv", MultiviewGroup: "mv_cap", MultiviewTile: 1}); err != nil {
		t.Fatalf("tile 1: %v", err)
	}
	// Tile 2 on Ch 0 shares the existing upstream connection and succeeds.
	if _, err := s.Tune(ctx, u, chs[0].ID, core.ClientCaps{ClientID: "vela/tv", MultiviewGroup: "mv_cap", MultiviewTile: 2}); err != nil {
		t.Fatalf("tile 2 same channel should share upstream: %v", err)
	}

	// Tile 3 on a 3rd distinct channel exceeds MaxConnections=2 with a typed error.
	_, err = s.Tune(ctx, u, chs[2].ID, core.ClientCaps{ClientID: "vela/tv", MultiviewGroup: "mv_cap", MultiviewTile: 3})
	var capErr *core.ProviderConnectionCapError
	if !errors.As(err, &capErr) {
		t.Fatalf("expected *core.ProviderConnectionCapError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrProviderBusy) || !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("ProviderConnectionCapError should preserve ErrProviderBusy/ErrStreamLimit wrapping: %v", err)
	}
	if capErr.Provider != "Acme IPTV" || capErr.Max != 2 || capErr.InUse != 2 {
		t.Fatalf("capErr = %+v, want Provider='Acme IPTV' Max=2 InUse=2", capErr)
	}
	if capErr.Error() != "Acme IPTV allows 2 connections" {
		t.Fatalf("capErr.Error() = %q", capErr.Error())
	}
	// Existing 3 tiles remain active after tile 3 rejection.
	if got := len(s.Sessions(ctx, u)); got != 3 {
		t.Fatalf("expected 3 tiles to remain active after cap rejection, got %d", got)
	}
}

func TestMultiviewTileMaxHeightCopiesWhenSourceFitsAndScalesWhenLarger(t *testing.T) {
	info720 := core.MediaInfo{
		Container: "mpegts",
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720, Profile: "high", Level: 40, PixelFormat: "yuv420p"},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000},
		},
	}
	info1080 := core.MediaInfo{
		Container: "mpegts",
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Profile: "high", Level: 41, PixelFormat: "yuv420p"},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000},
		},
	}
	opts := core.DecideOptions{DirectStream: true, AudioStream: -1, SubtitleStream: -1}
	tile720 := core.ClientCaps{
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
		MaxHeight: 720, ExplicitMaxHeight: true,
		MultiviewGroup: "mv_q", MultiviewTile: 2,
	}
	// Source fits 720p tile cap -> VideoCopy=true (no video transcode).
	dFit := transcode.Decide(info720, tile720, opts, transcode.Policy{})
	if !dFit.VideoCopy {
		t.Fatalf("expected video copy when 720p source fits 720p tile cap, got %+v", dFit)
	}
	// Source exceeds 720p tile cap -> scales down to 720p.
	dScale := transcode.Decide(info1080, tile720, opts, transcode.Policy{})
	if dScale.VideoCopy || dScale.MaxHeight != 720 {
		t.Fatalf("expected 1080p source to scale to 720p under 720p tile cap, got %+v", dScale)
	}
	// Focused-tile upgrade to 1080p -> 1080p source now copies.
	tile1080 := tile720
	tile1080.MaxHeight = 1080
	dUpgrade := transcode.Decide(info1080, tile1080, opts, transcode.Policy{})
	if !dUpgrade.VideoCopy {
		t.Fatalf("expected video copy after upgrading tile cap to 1080p, got %+v", dUpgrade)
	}
}

type mvSeqRunner struct {
	mu          sync.Mutex
	segment     time.Duration
	startDelay  time.Duration
	readyIndex  bool
	requests    []transcode.EncoderRequest
	encoders    []*mvSeqEncoder
	onStartHook func(req transcode.EncoderRequest)
}

func (r *mvSeqRunner) StartDecoder(_ context.Context, req transcode.DecoderRequest) (transcode.Decoder, error) {
	return &broadcastFakeDecoder{format: req.Format, closed: make(chan struct{}), value: 42}, nil
}

func (r *mvSeqRunner) StartEncoder(ctx context.Context, req transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
	if r.onStartHook != nil {
		r.onStartHook(req)
	}
	if r.startDelay > 0 {
		select {
		case <-time.After(r.startDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	enc := &mvSeqEncoder{segment: r.segment, startSeq: req.StartSeq}
	if !r.readyIndex {
		enc.index = mvSeqIndex{e: enc}
	}
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.encoders = append(r.encoders, enc)
	r.mu.Unlock()
	return enc, nil
}

type mvSeqEncoder struct {
	broadcastFakeEncoder
	segment  time.Duration
	startSeq int64
}

type mvSeqIndex struct {
	fakeBroadcastIndex
	e *mvSeqEncoder
}

func (s mvSeqIndex) Since(seq int64, limit int) []transcode.Segment {
	frames := s.e.snapshot()
	if len(frames) == 0 {
		return nil
	}
	g := int(s.e.segment * 30 / time.Second)
	var result []transcode.Segment
	if s.e.startSeq > 0 {
		for offset := 0; (offset+1)*g <= len(frames); offset++ {
			segSeq := s.e.startSeq + int64(offset)
			if segSeq > seq {
				result = append(result, transcode.Segment{
					Seq:      segSeq,
					Start:    frames[0].OutputTime.Add(time.Duration(offset*g) * time.Second / 30),
					Duration: time.Duration(g) * time.Second / 30,
				})
				if limit > 0 && len(result) >= limit {
					break
				}
			}
		}
		return result
	}
	starts := []int{0, g / 4, g / 2, g}
	for n := 0; ; n++ {
		start, end := 0, 0
		if n < 3 {
			start, end = starts[n], starts[n+1]
		} else {
			start = g + (n-3)*g
			end = start + g
		}
		if end > len(frames) {
			break
		}
		if int64(n) > seq {
			result = append(result, transcode.Segment{
				Seq:      int64(n),
				Start:    frames[0].OutputTime.Add(time.Duration(start) * time.Second / 30),
				Duration: time.Duration(end-start) * time.Second / 30,
			})
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	return result
}

func (s mvSeqIndex) Latest() (transcode.Segment, bool) {
	a := s.Since(-1, 0)
	if len(a) == 0 {
		return transcode.Segment{}, false
	}
	return a[len(a)-1], true
}

func (s mvSeqIndex) Window(from, until time.Time) []transcode.Segment {
	var out []transcode.Segment
	for _, seg := range s.Since(-1, 0) {
		if seg.Start.Before(until) && seg.Start.Add(seg.Duration).After(from) {
			out = append(out, seg)
		}
	}
	return out
}

func (s mvSeqIndex) Wait(ctx context.Context, seq int64) (transcode.Segment, error) {
	for {
		if a := s.Since(seq, 1); len(a) > 0 {
			return a[0], nil
		}
		select {
		case <-ctx.Done():
			return transcode.Segment{}, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

// Four concurrent cold 2 s-segment multiview tiles whose opening graphics take
// longer than segment*3/4 (1.5 s) and whose encoders race with sibling capacity
// yields must all survive cold start, publish media, and report live anchors.
func TestMultiviewFourConcurrentColdBroadcastTilesAllPlay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origin := time.Now()
		f := smallBroadcastFormat()
		segment := 2 * time.Second

		var reg broadcastRegistry
		defer reg.close()

		runner := &mvSeqRunner{
			segment:    segment,
			startDelay: 400 * time.Millisecond,
			onStartHook: func(transcode.EncoderRequest) {
				// Simulate a sibling hitting capacity pressure while other
				// cold broadcasts are still inside StartEncoder (viewers==0).
				reg.yieldIdleForCapacity(t.Context(), nil)
			},
		}

		type tileRes struct {
			lease  *broadcastLease
			window time.Duration
			err    error
		}
		results := make([]tileRes, 4)
		var wg sync.WaitGroup
		for i := range 4 {
			wg.Add(1)
			go func(tile int) {
				defer wg.Done()
				// Opening title exceeds the old 1.5 s (segment*3/4) self-cancel cliff.
				openDelay := 1800*time.Millisecond + time.Duration(tile)*200*time.Millisecond
				programme := broadcastPlan{id: fmt.Sprintf("prog-%d", tile), programme: true, start: origin.Add(-time.Minute), end: origin.Add(time.Hour), open: coldHoldOpen(f, byte(80+tile), openDelay+500*time.Millisecond)}
				card := broadcastPlan{id: fmt.Sprintf("title-%d", tile), element: "tonight", open: coldHoldOpen(f, byte(30+tile), openDelay)}
				planner := tuneStartupPlanner{
					titleStartupPlanner: titleStartupPlanner{
						coldHoldPlanner: coldHoldPlanner{
							elementBroadcastPlanner: elementBroadcastPlanner{programme, broadcastPlan{id: "after", start: programme.end}},
							screenPlan:              broadcastPlan{id: "screen", open: coldHoldOpen(f, 10, 0)},
							card:                    card,
						},
					},
					segment: segment,
				}
				key := broadcastKey{channelID: fmt.Sprintf("showcase-cold-%d", tile)}
				caps := core.ClientCaps{ClientID: "vela/web", MultiviewGroup: "mv_cold_4", MultiviewTile: tile, MaxHeight: 480}
				lease, err := reg.acquire(t.Context(), key, "h264-480p", core.Decision{VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 480}, func() (*broadcast, error) {
					return newBroadcast(t.Context(), runner, planner, f)
				}, caps)
				if err != nil {
					results[tile] = tileRes{err: err}
					return
				}
				if err := awaitBroadcastCushion(t.Context(), lease, false); err != nil {
					results[tile] = tileRes{err: fmt.Errorf("awaitBroadcastCushion: %w", err)}
					return
				}
				if lease.broadcast.ctx.Err() != nil {
					results[tile] = tileRes{err: fmt.Errorf("broadcast self-cancelled: %w", lease.broadcast.ctx.Err())}
					return
				}
				idx, err := lease.output()
				if err != nil {
					results[tile] = tileRes{err: err}
					return
				}
				segs := idx.Since(-1, 0)
				if len(segs) == 0 {
					results[tile] = tileRes{err: errors.New("no segments published")}
					return
				}
				win, err := broadcastLiveWindow(t.Context(), lease, idx, segs)
				if err != nil {
					results[tile] = tileRes{err: fmt.Errorf("LiveWindow missing after cold start: %w", err)}
					return
				}
				results[tile] = tileRes{lease: lease, window: time.Duration(win.LiveEdgeSeconds * float64(time.Second))}
			}(i)
		}
		wg.Wait()
		for i, res := range results {
			if res.err != nil {
				t.Fatalf("cold tile %d failed: %v", i, res.err)
			}
			if res.window <= 0 {
				t.Fatalf("cold tile %d live edge = %v, want > 0", i, res.window)
			}
			res.lease.close()
		}
	})
}

// Replanning a running tile to a new rendition (for example 480p -> 1080p)
// must carry the broadcast's live anchor forward monotonically and gather a
// multi-segment cushion rather than resetting to {mediaSequence: 0,
// windowStartSeconds: 0} with a 2 s live edge.
func TestMultiviewReplanKeepsLiveAnchorAndCushion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origin := time.Now()
		f := smallBroadcastFormat()
		segment := 2 * time.Second
		runner := &mvSeqRunner{segment: segment}

		programme := broadcastPlan{id: "prog", programme: true, start: origin.Add(-time.Minute), end: origin.Add(time.Hour), open: coldHoldOpen(f, 90, 0)}
		planner := tuneStartupPlanner{
			titleStartupPlanner: titleStartupPlanner{
				coldHoldPlanner: coldHoldPlanner{
					elementBroadcastPlanner: elementBroadcastPlanner{programme, broadcastPlan{id: "after", start: programme.end}},
					screenPlan:              broadcastPlan{id: "screen", open: coldHoldOpen(f, 10, 0)},
					card:                    broadcastPlan{id: "card", element: "tonight", open: coldHoldOpen(f, 30, 0)},
				},
			},
			segment: segment,
		}

		var reg broadcastRegistry
		defer reg.close()
		key := broadcastKey{channelID: "showcase-replan"}
		lease480, err := reg.acquire(t.Context(), key, "h264-480p", core.Decision{VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 480}, func() (*broadcast, error) {
			return newBroadcast(t.Context(), runner, planner, f)
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := awaitBroadcastCushion(t.Context(), lease480, false); err != nil {
			t.Fatalf("awaitBroadcastCushion 480p: %v", err)
		}

		// Let the running 480p tile advance well past startup burst into steady 1x playback.
		time.Sleep(18 * time.Second)
		synctest.Wait()

		idx480, err := lease480.output()
		if err != nil {
			t.Fatal(err)
		}
		latest480, ok := idx480.Latest()
		if !ok || latest480.Seq < 6 {
			t.Fatalf("480p latest = %+v (ok=%v), want seq >= 6", latest480, ok)
		}

		// Replan to 1080p under a handover context while 480p continues running.
		handoverCtx := core.WithLiveHandover(t.Context())
		lease1080, err := reg.acquire(handoverCtx, key, "h264-1080p", core.Decision{VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 1080}, func() (*broadcast, error) {
			t.Fatal("replan must reuse running broadcast")
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := awaitBroadcastCushion(handoverCtx, lease1080, false); err != nil {
			t.Fatalf("awaitBroadcastCushion 1080p: %v", err)
		}

		idx1080, err := lease1080.output()
		if err != nil {
			t.Fatal(err)
		}
		segs1080 := idx1080.Since(-1, 0)
		if len(segs1080) < 3 {
			t.Fatalf("replanned 1080p segments = %d, want >= 3 segments buffered", len(segs1080))
		}
		win1080, err := broadcastLiveWindow(t.Context(), lease1080, idx1080, segs1080)
		if err != nil {
			t.Fatalf("replanned LiveWindow: %v", err)
		}
		if win1080.Anchor.MediaSequence <= latest480.Seq {
			t.Fatalf("replanned mediaSequence = %d, want > old 480p latest %d", win1080.Anchor.MediaSequence, latest480.Seq)
		}
		if win1080.Anchor.WindowStartSeconds <= 0 {
			t.Fatalf("replanned windowStartSeconds = %v, want > 0 (carried from broadcast anchor)", win1080.Anchor.WindowStartSeconds)
		}
		buffer := win1080.LiveEdgeSeconds - win1080.Anchor.WindowStartSeconds
		if buffer+1e-6 < (3 * segment).Seconds() {
			t.Fatalf("replanned buffer = %.2fs (edge=%.2fs start=%.2fs), want >= %.2fs", buffer, win1080.LiveEdgeSeconds, win1080.Anchor.WindowStartSeconds, (3 * segment).Seconds())
		}

		lease480.close()
		lease1080.close()
	})
}

// Rejoining a retained live session whose broadcast stopped must tie startup to
// the session context rather than a short playlist HTTP request context, so a
// timed-out or cancelled playlist poll never aborts the in-flight cold start.
func TestMultiviewBroadcastRejoinUsesSessionContextNotRequestContext(t *testing.T) {
	segment := 2 * time.Second
	blockStart := make(chan struct{})
	started := make(chan struct{})
	runner := &mvSeqRunner{
		segment:    segment,
		readyIndex: true,
		onStartHook: func(transcode.EncoderRequest) {
			close(started)
			<-blockStart
		},
	}

	s := testService(t, Options{Transcoder: &fakeContinuousTranscoder{runner: runner}})
	c, err := s.SaveChannel(t.Context(), Channel{
		Name:    "Showcase Rejoin",
		Number:  "91",
		Kind:    "virtual",
		OffAir:  true,
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	sessCtx, sessCancel := context.WithCancel(t.Context())
	defer sessCancel()
	stale := &broadcastLease{closed: true, broadcast: &broadcast{segment: segment}}
	ss := &session{
		info:      SessionInfo{ID: "sess-rejoin", Channel: c, StartedAt: time.Now()},
		user:      core.User{ID: 1, IsOwner: true},
		caps:      core.ClientCaps{ClientID: "vela/web", MaxHeight: 480, MultiviewGroup: "mv_rejoin", MultiviewTile: 0},
		ctx:       sessCtx,
		cancel:    sessCancel,
		broadcast: stale,
	}

	// First playlist request kicks off rejoin and cancels once StartEncoder is
	// in flight (like a short playlist timeout or client abort).
	reqCtx, reqCancel := context.WithCancel(t.Context())
	firstDone := make(chan error, 1)
	go func() {
		_, err := s.broadcastSegments(reqCtx, ss)
		firstDone <- err
	}()
	<-started
	reqCancel()
	if err := <-firstDone; !errors.Is(err, context.Canceled) && !errors.Is(err, core.ErrNotAvailable) {
		close(blockStart)
		t.Fatalf("first cancelled playlist poll err = %v, want canceled/unavailable", err)
	}
	close(blockStart)

	// Wait on the in-flight rejoin channel (event-driven, no wall-clock sleep)
	// and verify the subsequent playlist poll immediately receives the index.
	ss.mu.Lock()
	done := ss.rejoinDone
	ss.mu.Unlock()
	if done != nil {
		<-done
	}

	idx, err := s.broadcastSegments(t.Context(), ss)
	if err != nil || idx == nil {
		t.Fatalf("subsequent playlist poll after session-scoped rejoin failed: idx=%v err=%v", idx, err)
	}
	runner.mu.Lock()
	starts := len(runner.requests)
	runner.mu.Unlock()
	if starts != 1 {
		t.Fatalf("expected exactly 1 encoder start across cancelled poll + retry, got %d", starts)
	}
	ss.close(s)
}

// awaitBroadcastCushion must never return nil before segment 0 exists, so a
// playback endpoint can never answer "ready" with no manifest/anchor.
func TestAwaitBroadcastCushionFailsWhenNoSegmentPublished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := smallBroadcastFormat()
		// A runner whose encoder never publishes any segment.
		runner := &silentSegmentRunner{}
		planner := broadcastFakePlanner{plan: broadcastPlan{id: "silent", open: coldHoldOpen(f, 30, 0)}}
		var reg broadcastRegistry
		defer reg.close()
		lease, err := reg.acquire(t.Context(), broadcastKey{channelID: "ch-silent"}, "h264-720p", core.Decision{VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 720}, func() (*broadcast, error) {
			return newBroadcast(t.Context(), runner, planner, f)
		})
		if err != nil {
			t.Fatal(err)
		}
		defer lease.close()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		if err := awaitBroadcastCushion(ctx, lease, false); err == nil {
			t.Fatal("awaitBroadcastCushion returned nil even though no segment was ever published")
		}
	})
}

type silentSegmentRunner struct{ broadcastFakeRunner }

func (*silentSegmentRunner) StartEncoder(context.Context, transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
	enc := &mvSeqEncoder{segment: 2 * time.Second}
	return &silentSegmentEncoder{idx: mvSeqIndex{e: enc}}, nil
}

type silentSegmentEncoder struct{ idx mvSeqIndex }

func (*silentSegmentEncoder) Write(context.Context, transcode.RawFrame) error { return nil }
func (e *silentSegmentEncoder) Segments() transcode.SegmentIndex              { return e.idx }
func (*silentSegmentEncoder) Close() error                                    { return nil }

// Multiview tiles that stop fetching segments must be reaped by the normal live
// idle reaper independently of any group TTL, releasing stream slots and
// notifying SessionExpired while sibling active tiles keep playing.
func TestMultiviewIdleTileExpiresAndFiresSessionExpired(t *testing.T) {
	var clockMu sync.Mutex
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	advance := func(d time.Duration) { clockMu.Lock(); now = now.Add(d); clockMu.Unlock() }
	var expiredMu sync.Mutex
	var expiredIDs []string
	pb := &fakePlayback{}
	s := testService(t, Options{
		Now:        func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now },
		Playback:   pb,
		Transcoder: &fakeTranscoder{},
		SessionExpired: func(id string) {
			expiredMu.Lock()
			expiredIDs = append(expiredIDs, id)
			expiredMu.Unlock()
		},
	})
	u := &core.User{ID: 7, StreamLimit: 1}
	c0 := testChannel()
	c0.ID = "mv-ch-0"
	c0.Number = "10"
	c1 := testChannel()
	c1.ID = "mv-ch-1"
	c1.Number = "11"
	if _, err := s.SaveChannel(context.Background(), c0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveChannel(context.Background(), c1); err != nil {
		t.Fatal(err)
	}
	caps0 := core.ClientCaps{ClientID: "vela:atv", MultiviewGroup: "mv_idle", MultiviewTile: 0}
	caps1 := core.ClientCaps{ClientID: "vela:atv", MultiviewGroup: "mv_idle", MultiviewTile: 1}
	info0, err := s.Tune(context.Background(), u, c0.ID, caps0)
	if err != nil {
		t.Fatalf("tune tile 0: %v", err)
	}
	info1, err := s.Tune(context.Background(), u, c1.ID, caps1)
	if err != nil {
		t.Fatalf("tune tile 1: %v", err)
	}
	// Mark both tiles as having started segment playback at t=0.
	s.mu.Lock()
	ss0 := s.sessions[info0.ID]
	ss1 := s.sessions[info1.ID]
	s.mu.Unlock()
	s.touchMedia(ss0)
	s.touchMedia(ss1)

	// Advance 25s: only tile 1 keeps fetching segments.
	advance(25 * time.Second)
	s.touchMedia(ss1)
	s.expireSessions()
	if got := len(s.ViewingSessions()); got != 2 {
		t.Fatalf("before 30s idle timeout: viewing sessions = %d, want 2", got)
	}

	// Advance another 10s (tile 0 silent for 35s > 30s IdleTimeout; tile 1 silent 10s).
	advance(10 * time.Second)
	s.expireSessions()

	expiredMu.Lock()
	gotExpired := append([]string(nil), expiredIDs...)
	expiredMu.Unlock()
	if len(gotExpired) != 1 || gotExpired[0] != info0.ID {
		t.Fatalf("expired callbacks = %v, want [%s]", gotExpired, info0.ID)
	}
	views := s.ViewingSessions()
	if len(views) != 1 || views[0].ID != info1.ID {
		t.Fatalf("remaining sessions = %+v, want only tile 1 (%s)", views, info1.ID)
	}

	// Now tile 1 also stops fetching segments; 35s later it expires too,
	// freeing the viewer's stream slot cleanly.
	advance(35 * time.Second)
	s.expireSessions()
	if got := len(s.ViewingSessions()); got != 0 {
		t.Fatalf("after both tiles idle: viewing sessions = %d, want 0", got)
	}
	expiredMu.Lock()
	gotExpired = append([]string(nil), expiredIDs...)
	expiredMu.Unlock()
	if len(gotExpired) != 2 || gotExpired[1] != info1.ID {
		t.Fatalf("expired callbacks after tile 1 = %v, want [%s %s]", gotExpired, info0.ID, info1.ID)
	}
}

type fakeContinuousTranscoder struct {
	core.Transcoder
	runner transcode.ContinuousRunner
}

func (f *fakeContinuousTranscoder) HWAccel() core.HWAccel { return core.HWAccel{Name: "none"} }
func (f *fakeContinuousTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	return core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", AudioCodec: "aac", MaxHeight: caps.MaxHeight}
}
func (f *fakeContinuousTranscoder) StartDecoder(ctx context.Context, req transcode.DecoderRequest) (transcode.Decoder, error) {
	return f.runner.StartDecoder(ctx, req)
}
func (f *fakeContinuousTranscoder) StartEncoder(ctx context.Context, req transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
	return f.runner.StartEncoder(ctx, req)
}
