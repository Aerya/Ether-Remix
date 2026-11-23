package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type fmp4BroadcastIndex struct {
	segments []transcode.Segment
	initData []byte
	fmp4Data map[int64][]byte
	tsData   map[int64][]byte
}

func (i *fmp4BroadcastIndex) Latest() (transcode.Segment, bool) {
	if len(i.segments) == 0 {
		return transcode.Segment{}, false
	}
	return i.segments[len(i.segments)-1], true
}

func (i *fmp4BroadcastIndex) Since(seq int64, limit int) []transcode.Segment {
	var out []transcode.Segment
	for _, s := range i.segments {
		if s.Seq > seq {
			out = append(out, s)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out
}

func (i *fmp4BroadcastIndex) Window(from, to time.Time) []transcode.Segment {
	return append([]transcode.Segment(nil), i.segments...)
}

func (i *fmp4BroadcastIndex) Wait(_ context.Context, after int64) (transcode.Segment, error) {
	for _, s := range i.segments {
		if s.Seq > after {
			return s, nil
		}
	}
	return transcode.Segment{}, context.Canceled
}

func (i *fmp4BroadcastIndex) Format() (string, int, int, int) {
	return "avc1.640028,mp4a.40.2", 1920, 1080, 3000000
}

func (i *fmp4BroadcastIndex) ServeTS(w http.ResponseWriter, _ *http.Request, seq int64) {
	w.Header().Set("Content-Type", "video/mp2t")
	_, _ = w.Write(i.tsData[seq])
}

func (i *fmp4BroadcastIndex) ServeFMP4(w http.ResponseWriter, _ *http.Request, seq int64) {
	w.Header().Set("Content-Type", "video/mp4")
	_, _ = w.Write(i.fmp4Data[seq])
}

func (i *fmp4BroadcastIndex) ServeInit(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "video/mp4")
	_, _ = w.Write(i.initData)
}

func (i *fmp4BroadcastIndex) AttachTS(context.Context) (io.ReadCloser, error) {
	return nil, io.EOF
}

type realDecideTranscoder struct {
	core.Transcoder
}

func (realDecideTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	return transcode.Decide(info, caps, opts, transcode.Policy{})
}

func TestBroadcastFMP4HLSForNonTSClientAndTSForAppleTV(t *testing.T) {
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	idx := &fmp4BroadcastIndex{
		segments: []transcode.Segment{
			{Seq: 10, Start: now, Duration: 2 * time.Second, PolicyTags: []any{(*broadcastPolicyTag)(nil)}},
			{Seq: 11, Start: now.Add(2 * time.Second), Duration: 2 * time.Second, PolicyTags: []any{(*broadcastPolicyTag)(nil)}},
		},
		initData: []byte("ftyp-init-mp4-bytes"),
		fmp4Data: map[int64][]byte{10: []byte("moof-seg-10"), 11: []byte("moof-seg-11")},
		tsData:   map[int64][]byte{10: []byte("ts-seg-10"), 11: []byte("ts-seg-11")},
	}
	enc := &broadcastFakeEncoder{index: idx}
	b := &broadcast{
		format:     smallBroadcastFormat(),
		segment:    2 * time.Second,
		renditions: map[string]*broadcastRendition{"h264-720p": {id: "h264-720p", height: 720, encoder: enc, viewers: 2}},
		viewers:    2,
		lastViewer: now,
		ctx:        context.Background(),
		cancel:     func() {},
	}
	s := testService(t, Options{Now: func() time.Time { return now }})
	ch1, err := s.SaveChannel(context.Background(), testChannel())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.allChannels(context.Background()); err != nil {
		t.Fatal(err)
	}
	u := &core.User{ID: 1, Username: "alice", IsOwner: true}

	webSess := &session{
		info:        SessionInfo{ID: "web-tile-0", UserID: u.ID, Channel: ch1},
		streamGrant: "grant-web",
		user:        *u,
		ready:       true,
		caps:        core.ClientCaps{Protocol: "vela", Platform: "web", HLSContainers: []string{"fmp4"}, MultiviewGroup: "mv-1", MultiviewTile: 0},
		broadcast:   &broadcastLease{broadcast: b, rendition: "h264-720p", height: 720},
	}
	tvSess := &session{
		info:        SessionInfo{ID: "tv-tile-0", UserID: u.ID, Channel: ch1},
		streamGrant: "grant-tv",
		user:        *u,
		ready:       true,
		caps:        core.ClientCaps{Protocol: "vela", Platform: "tvOS", MultiviewGroup: "mv-2", MultiviewTile: 0},
		broadcast:   &broadcastLease{broadcast: b, rendition: "h264-720p", height: 720},
	}
	s.mu.Lock()
	s.sessions[webSess.info.ID] = webSess
	s.sessions[tvSess.info.ID] = tvSess
	s.mu.Unlock()

	// 1. Vela Web receives fMP4 HLS (#EXT-X-VERSION:7, EXT-X-MAP init.mp4, .m4s segments).
	webPL := httptest.NewRecorder()
	s.ServePlaylist(webPL, httptest.NewRequest(http.MethodGet, "/livetv/sessions/web-tile-0/0/index.m3u8", nil), webSess.info.ID, u)
	if webPL.Code != http.StatusOK {
		t.Fatalf("web ServePlaylist status=%d body=%s", webPL.Code, webPL.Body.String())
	}
	body := webPL.Body.String()
	if !strings.Contains(body, "#EXT-X-VERSION:7") ||
		!strings.Contains(body, fmt.Sprintf("#EXT-X-MAP:URI=%q", withAuth("resource/init.mp4", "grant-web"))) ||
		!strings.Contains(body, withAuth("relay/10.m4s", "grant-web")) ||
		strings.Contains(body, ".ts") {
		t.Fatalf("web playlist did not use fMP4 HLS:\n%s", body)
	}

	// ServeResource serves init.mp4 for broadcast sessions.
	initRec := httptest.NewRecorder()
	s.ServeResource(initRec, httptest.NewRequest(http.MethodGet, "/livetv/sessions/web-tile-0/0/resource/init.mp4", nil), webSess.info.ID, "init.mp4", u)
	if initRec.Code != http.StatusOK || initRec.Body.String() != "ftyp-init-mp4-bytes" || initRec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("ServeResource(init.mp4) = %d %q %q", initRec.Code, initRec.Header().Get("Content-Type"), initRec.Body.String())
	}

	// ServeRelaySegment serves .m4s fragment bytes for fMP4 sessions.
	segRec := httptest.NewRecorder()
	s.ServeRelaySegment(segRec, httptest.NewRequest(http.MethodGet, "/livetv/sessions/web-tile-0/0/relay/10.m4s", nil), webSess.info.ID, 10, u)
	if segRec.Code != http.StatusOK || segRec.Body.String() != "moof-seg-10" || segRec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("ServeRelaySegment(10.m4s) = %d %q %q", segRec.Code, segRec.Header().Get("Content-Type"), segRec.Body.String())
	}

	// 2. Apple TV keeps TS HLS (#EXT-X-VERSION:3, .ts segments) from the exact same rendition.
	tvPL := httptest.NewRecorder()
	s.ServePlaylist(tvPL, httptest.NewRequest(http.MethodGet, "/livetv/sessions/tv-tile-0/0/index.m3u8", nil), tvSess.info.ID, u)
	if tvPL.Code != http.StatusOK {
		t.Fatalf("tv ServePlaylist status=%d body=%s", tvPL.Code, tvPL.Body.String())
	}
	tvBody := tvPL.Body.String()
	if !strings.Contains(tvBody, "#EXT-X-VERSION:3") ||
		strings.Contains(tvBody, "#EXT-X-MAP") ||
		!strings.Contains(tvBody, withAuth("relay/10.ts", "grant-tv")) {
		t.Fatalf("Apple TV playlist did not preserve TS:\n%s", tvBody)
	}
	tvSeg := httptest.NewRecorder()
	s.ServeRelaySegment(tvSeg, httptest.NewRequest(http.MethodGet, "/livetv/sessions/tv-tile-0/0/relay/10.ts", nil), tvSess.info.ID, 10, u)
	if tvSeg.Code != http.StatusOK || tvSeg.Body.String() != "ts-seg-10" || tvSeg.Header().Get("Content-Type") != "video/mp2t" {
		t.Fatalf("Apple TV ServeRelaySegment(10.ts) = %d %q %q", tvSeg.Code, tvSeg.Header().Get("Content-Type"), tvSeg.Body.String())
	}
}

func TestRelayPlanSelectsFMP4ForNonTSClientAndPreservesTSForAppleTV(t *testing.T) {
	s := testService(t, Options{Transcoder: realDecideTranscoder{}})
	info := core.MediaInfo{
		Container: "mpegts",
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000},
		},
	}

	// Non-TS web client with matching H.264/AAC source gets video/audio copy into fMP4.
	webSS := &session{
		info: SessionInfo{Channel: Channel{ID: "iptv-1", Kind: "iptv"}},
		caps: core.ClientCaps{Protocol: "vela", Platform: "web", VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, HLSContainers: []string{"fmp4"}, MaxHeight: 720},
	}
	decWeb, _, _ := s.relayPlan(webSS, info)
	if decWeb.Container != transcode.ContainerFMP4 || !decWeb.VideoCopy || !decWeb.AudioCopy {
		t.Fatalf("web relayPlan = %+v, want fmp4 copy", decWeb)
	}

	// Apple TV client keeps TS copy.
	tvSS := &session{
		info: SessionInfo{Channel: Channel{ID: "iptv-1", Kind: "iptv"}},
		caps: core.ClientCaps{Protocol: "vela", Platform: "tvOS", VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, MaxHeight: 720},
	}
	decTV, _, _ := s.relayPlan(tvSS, info)
	if decTV.Container != transcode.ContainerMPEGTS || !decTV.VideoCopy || !decTV.AudioCopy {
		t.Fatalf("tvOS relayPlan = %+v, want mpegts copy", decTV)
	}
}

func TestBroadcastRegistryYieldsIdleForCapacityAndAllowsFourMultiviewTiles(t *testing.T) {
	// Simulate MaxTranscodes=2 with group-shared slot accounting (matching transcode.Manager).
	var mu sync.Mutex
	maxSlots := 2
	type heldSlot struct {
		group  string
		closed bool
	}
	var slots []*heldSlot
	usedSlotsLocked := func() int {
		n := 0
		groups := map[string]bool{}
		for _, sl := range slots {
			if sl.closed {
				continue
			}
			if sl.group != "" {
				if !groups[sl.group] {
					groups[sl.group] = true
					n++
				}
				continue
			}
			n++
		}
		return n
	}
	groupHasLocked := func(g string) bool {
		if g == "" {
			return false
		}
		for _, sl := range slots {
			if !sl.closed && sl.group == g {
				return true
			}
		}
		return false
	}

	type trackedEncoder struct {
		broadcastFakeEncoder
		slot *heldSlot
	}
	closeTracked := func(e *trackedEncoder) {
		mu.Lock()
		e.slot.closed = true
		mu.Unlock()
		_ = e.broadcastFakeEncoder.Close()
	}

	format := smallBroadcastFormat()
	runner := &slotAwareFakeRunner{
		format: format,
		startEnc: func(req transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
			mu.Lock()
			defer mu.Unlock()
			g := req.ClientCaps.MultiviewGroup
			if !groupHasLocked(g) && usedSlotsLocked() >= maxSlots {
				return nil, transcode.ErrTooManyTranscodes
			}
			sl := &heldSlot{group: g}
			slots = append(slots, sl)
			te := &trackedEncoder{slot: sl}
			return &closableEncoder{tracked: te, onClose: func() { closeTracked(te) }}, nil
		},
	}

	reg := &broadcastRegistry{}
	defer reg.close()
	makeBroadcast := func(ch string) func() (*broadcast, error) {
		return func() (*broadcast, error) {
			plan := broadcastPlan{id: ch, open: func(context.Context) (transcode.Decoder, func(), error) {
				return &broadcastFakeDecoder{format: format, closed: make(chan struct{})}, nil, nil
			}}
			return newBroadcast(context.Background(), runner, broadcastFakePlanner{plan: plan, screenPlan: plan}, format)
		}
	}

	// 1. Occupy both slots with two ungrouped broadcasts, then release both so they are idle (warm).
	l1, err := reg.acquire(context.Background(), broadcastKey{channelID: "idle-1"}, "h264-720p", core.Decision{MaxHeight: 720}, makeBroadcast("idle-1"))
	if err != nil {
		t.Fatalf("acquire idle-1: %v", err)
	}
	l2, err := reg.acquire(context.Background(), broadcastKey{channelID: "idle-2"}, "h264-720p", core.Decision{MaxHeight: 720}, makeBroadcast("idle-2"))
	if err != nil {
		t.Fatalf("acquire idle-2: %v", err)
	}
	l1.close()
	l2.close()

	// 2. Open 4 distinct multiview channels in group "mv-1" plus a 5th same-channel tile.
	// First tile must synchronously yield the idle broadcasts instead of failing with capacity/cancelled!
	var leases []*broadcastLease
	for i := 0; i < 4; i++ {
		ch := fmt.Sprintf("mv-ch-%d", i)
		l, err := reg.acquire(context.Background(), broadcastKey{channelID: ch}, "h264-480p", core.Decision{MaxHeight: 480}, makeBroadcast(ch), core.ClientCaps{MultiviewGroup: "mv-1", MultiviewTile: i})
		if err != nil {
			t.Fatalf("multiview tile %d (%s) failed: %v", i, ch, err)
		}
		leases = append(leases, l)
	}
	// Tile sharing channel 0 reuses its broadcast & rendition without starting a 5th encoder.
	shared, err := reg.acquire(context.Background(), broadcastKey{channelID: "mv-ch-0"}, "h264-480p", core.Decision{MaxHeight: 480}, makeBroadcast("mv-ch-0"), core.ClientCaps{MultiviewGroup: "mv-1", MultiviewTile: 0})
	if err != nil {
		t.Fatalf("shared channel tile failed: %v", err)
	}
	leases = append(leases, shared)

	// Upgrading tile 0 from 480p -> 1080p while all 4 tiles are active must also succeed (handover).
	upgraded, err := reg.acquire(context.Background(), broadcastKey{channelID: "mv-ch-0"}, "h264-1080p", core.Decision{MaxHeight: 1080}, makeBroadcast("mv-ch-0"), core.ClientCaps{MultiviewGroup: "mv-1", MultiviewTile: 0})
	if err != nil {
		t.Fatalf("multiview upgrade 480p->1080p failed: %v", err)
	}
	leases = append(leases, upgraded)

	for _, l := range leases {
		l.close()
	}
}

type slotAwareFakeRunner struct {
	format   transcode.RawFormat
	startEnc func(transcode.EncoderRequest) (transcode.ContinuousEncoder, error)
}

func (r *slotAwareFakeRunner) StartEncoder(_ context.Context, req transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
	return r.startEnc(req)
}

func (r *slotAwareFakeRunner) StartDecoder(context.Context, transcode.DecoderRequest) (transcode.Decoder, error) {
	return &broadcastFakeDecoder{format: r.format, closed: make(chan struct{})}, nil
}

type closableEncoder struct {
	tracked transcode.ContinuousEncoder
	onClose func()
}

func (c *closableEncoder) Write(ctx context.Context, f transcode.RawFrame) error {
	return c.tracked.Write(ctx, f)
}
func (c *closableEncoder) Segments() transcode.SegmentIndex { return c.tracked.Segments() }
func (c *closableEncoder) Close() error {
	c.onClose()
	return nil
}

func TestLiveDiagnosticsClassifiesTooManyTranscodesAsPlaybackCapacity(t *testing.T) {
	diag := Diagnose("tune", fmt.Errorf("encoder start: %w", transcode.ErrTooManyTranscodes))
	if diag.Code != "playback_capacity" {
		t.Fatalf("Diagnose(ErrTooManyTranscodes).Code = %q, want playback_capacity", diag.Code)
	}
	if diag.RetryAfter <= 0 {
		t.Fatalf("Diagnose(ErrTooManyTranscodes).RetryAfter = %v, want > 0", diag.RetryAfter)
	}
}

func TestMultiviewHandoverPreservesOldTileSessionOnFailure(t *testing.T) {
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	u := &core.User{ID: 7, Username: "alice"}

	deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer deadSrv.Close()

	chGood := Channel{ID: "ch-good", Name: "Good", Number: "201", Kind: "iptv", Enabled: true, StreamURL: deadSrv.URL + "/good"}
	chFail := Channel{ID: "ch-fail", Name: "Fail", Number: "202", Kind: "iptv", Enabled: true, StreamURL: deadSrv.URL + "/dead"}
	s := testService(t, Options{Now: func() time.Time { return now }, HTTPClient: deadSrv.Client()})
	var err error
	if chGood, err = s.SaveChannel(context.Background(), chGood); err != nil {
		t.Fatal(err)
	}
	if chFail, err = s.SaveChannel(context.Background(), chFail); err != nil {
		t.Fatal(err)
	}

	// Seed an active tile 0 session in multiview group "mv-handover".
	oldSess := &session{
		info:        SessionInfo{ID: "tile-old", UserID: u.ID, Channel: chGood},
		streamGrant: "grant-old",
		user:        *u,
		caps:        core.ClientCaps{ClientID: "web-1", MultiviewGroup: "mv-handover", MultiviewTile: 0, MaxHeight: 480},
		ready:       true,
	}
	s.mu.Lock()
	s.sessions[oldSess.info.ID] = oldSess
	s.mu.Unlock()

	// Attempt to replace tile 0 with a failing channel (handover): must fail AND keep tile-old alive!
	_, err = s.Tune(core.WithLiveHandover(context.Background()), u, chFail.ID, core.ClientCaps{
		ClientID:       "web-1",
		MultiviewGroup: "mv-handover",
		MultiviewTile:  0,
		MaxHeight:      1080,
	})
	if err == nil {
		t.Fatal("expected failing tune to return error")
	}

	s.mu.Lock()
	stillAlive := s.sessions[oldSess.info.ID] == oldSess && !oldSess.closed
	s.mu.Unlock()
	if !stillAlive {
		t.Fatal("failed tile handover killed the existing tile session; old tile must keep playing")
	}

	// Direct non-multiview tune on the same device MUST still supersede immediately.
	_, err = s.Tune(context.Background(), u, chFail.ID, core.ClientCaps{ClientID: "web-1"})
	if err == nil {
		t.Fatal("expected non-group failing tune to return error")
	}
	s.mu.Lock()
	_, stillPresent := s.sessions[oldSess.info.ID]
	s.mu.Unlock()
	if stillPresent {
		t.Fatal("non-multiview tune should still supersede prior multiview group immediately")
	}
}

func TestBroadcastAddRenditionPreservesDeadlineExceededNotCanceled(t *testing.T) {
	b := &broadcast{
		runner: &slotAwareFakeRunner{
			startEnc: func(transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
				return nil, context.Canceled
			},
		},
		renditions: map[string]*broadcastRendition{},
		ctx:        context.Background(),
		cancel:     func() {},
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := b.addRendition(ctx, "h264-1080p", core.Decision{MaxHeight: 1080})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("addRendition under expired deadline: err=%v, want context.DeadlineExceeded", err)
	}
}
