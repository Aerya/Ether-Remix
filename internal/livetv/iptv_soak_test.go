package livetv

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/transcode"
)

const (
	soakVideoLive  byte   = 80
	soakVideoBlack byte   = 16
	soakFrameMagic uint64 = 0x534f414b46524d31 // "SOAKFRM1"
)

// soakFault describes one scripted upstream fault relative to stream start.
type soakFault struct {
	at            time.Duration
	kind          string        // "pause" (quiet open socket) or "drop" (EOF disconnect)
	duration      time.Duration // silence duration for "pause"
	reconnectWait time.Duration // outage duration before first byte on subsequent opens
	flushStale    bool          // if true on "pause", flush paused frames in a burst instead of skipping live
	originWide    bool          // if true on "pause", all open connections pause until the outage ends
}

type soakScript struct {
	name             string
	stability        string
	serverStability  string
	channelStability string
	width            int
	height           int
	fps              int
	maxConnections   int
	tuneGap          time.Duration
	startupBurst     time.Duration
	burstEvery       time.Duration // if >0, deliver media in bursts separated by quiet gaps (Mad Net style)
	http2SinglePkt   bool          // send first TS packet alone in initial read (HTTP/2 small DATA frame)
	decoderGapAt     time.Duration // if >0, inject a decoder stall without transport failure
	decoderGap       time.Duration
	duration         time.Duration
	faults           []soakFault
}

type soakMetrics struct {
	tunedIn            time.Duration
	framesTotal        int
	liveFrames         int
	heldFrames         int
	holdEpisodes       int
	trailingHoldFrames int
	screenFrames       int
	blackFrames        int
	droppedFrames      int
	fastOutputTicks    int
	inputLostLogs      int
	adaptedStallAfter  time.Duration
	segmentsPublished  int
	maxSegmentGap      time.Duration
	maxPublishLag      time.Duration
	latePublishes      int // lag > 500ms (the old 1s margin/2 threshold)
	lateOverMargin     int // lag > continuous client margin/2
	teardowns          int // decoder opens after initial tune
	providerOpens      int
	maxConcurrentOpen  int
	minOpenGap         time.Duration
	peakReserve        time.Duration
	minPostPeakRes     time.Duration
	endReserve         time.Duration
	postDrainReserve   time.Duration
	endLiveLag         time.Duration
	playlistGaps       int
	avSyncDrift        time.Duration
}

type soakLogState struct {
	mu                sync.Mutex
	inputLost         int
	adaptedStallAfter time.Duration
	burstFinishedAt   time.Time
}

type soakLogHandler struct {
	state *soakLogState
}

func (h *soakLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *soakLogHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *soakLogHandler) WithGroup(string) slog.Handler            { return h }
func (h *soakLogHandler) Handle(_ context.Context, r slog.Record) error {
	switch r.Message {
	case "live input lost":
		h.state.mu.Lock()
		h.state.inputLost++
		h.state.mu.Unlock()
	case "live provider stall threshold adapted":
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "stall_after" {
				h.state.mu.Lock()
				h.state.adaptedStallAfter = a.Value.Resolve().Duration()
				h.state.mu.Unlock()
			}
			return true
		})
	case "broadcast startup burst finished":
		h.state.mu.Lock()
		if h.state.burstFinishedAt.IsZero() {
			h.state.burstFinishedAt = time.Now()
		}
		h.state.mu.Unlock()
	}
	return nil
}

// soakProvider simulates an MPEG-TS over HTTP/2 IPTV provider in synctest virtual time.
type soakProvider struct {
	origin          time.Time
	cfg             soakScript
	mu              sync.Mutex
	opens           int
	openTimes       []time.Time
	concurrent      int
	maxConcurrent   int
	faultTriggered  map[int]bool
	outageUntil     time.Time
	originPauseTill time.Time
}

func newSoakProvider(origin time.Time, cfg soakScript) *soakProvider {
	return &soakProvider{
		origin:         origin,
		cfg:            cfg,
		faultTriggered: map[int]bool{},
	}
}

func (p *soakProvider) fps() int {
	if p.cfg.fps > 0 {
		return p.cfg.fps
	}
	return 25
}

func (p *soakProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL == nil || r.URL.Host != "madnet.invalid" {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	}
	now := time.Now()
	p.mu.Lock()
	p.opens++
	connNo := p.opens
	p.openTimes = append(p.openTimes, now)
	p.concurrent++
	if p.concurrent > p.maxConcurrent {
		p.maxConcurrent = p.concurrent
	}
	var waitFirst time.Duration
	if p.outageUntil.After(now) {
		waitFirst = p.outageUntil.Sub(now)
	}
	if p.originPauseTill.After(now) {
		if d := p.originPauseTill.Sub(now); d > waitFirst {
			waitFirst = d
		}
	}
	p.mu.Unlock()

	pr, pw := io.Pipe()
	go p.stream(r.Context(), pw, connNo, waitFirst)

	hdr := make(http.Header)
	hdr.Set("Content-Type", "video/mp2t")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     hdr,
		Body:       pr,
		Request:    r,
	}, nil
}

func (p *soakProvider) stream(ctx context.Context, pw *io.PipeWriter, connNo int, waitFirst time.Duration) {
	defer func() {
		p.mu.Lock()
		p.concurrent--
		p.mu.Unlock()
		_ = pw.Close()
	}()
	stop := context.AfterFunc(ctx, func() { _ = pw.Close() })
	defer stop()

	if waitFirst > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(waitFirst):
		}
	}

	fps := p.fps()
	frameDur := time.Second / time.Duration(fps)
	dtsStep := int64(tsClockHz / fps)
	gop := 12
	if fps >= 50 {
		gop = 30
	}
	const baseMedia = 20 * time.Second

	liveFrameAt := func(now time.Time) int {
		return int((baseMedia + now.Sub(p.origin)) / frameDur)
	}

	now := time.Now()
	burstFrames := int(p.cfg.startupBurst / frameDur)
	curFrame := max(0, liveFrameAt(now)-burstFrames)
	curFrame -= curFrame % gop

	gen := &tsGen{
		cc:        map[int]byte{},
		dts:       int64(curFrame)*dtsStep + 90000,
		frame:     curFrame,
		gop:       gop,
		audioLead: 0,
	}

	emitFrame := func(seq int, singleFirst bool) bool {
		gen.frame = seq
		gen.dts = int64(seq)*dtsStep + 90000
		raw := soakTSFrame(gen, uint64(seq))
		if singleFirst && len(raw) > tsPacket {
			if _, err := pw.Write(raw[:tsPacket]); err != nil {
				return false
			}
			select {
			case <-ctx.Done():
				return false
			case <-time.After(time.Millisecond):
			}
			raw = raw[tsPacket:]
		}
		_, err := pw.Write(raw)
		return err == nil
	}

	sentFirst := false
	nextBurstRelease := now
	for {
		now = time.Now()
		elapsed := now.Sub(p.origin)

		var activeFault *soakFault
		var originHold time.Duration
		p.mu.Lock()
		for idx := range p.cfg.faults {
			f := &p.cfg.faults[idx]
			if !p.faultTriggered[idx] && elapsed >= f.at {
				p.faultTriggered[idx] = true
				if f.reconnectWait > 0 {
					until := now.Add(f.reconnectWait)
					if until.After(p.outageUntil) {
						p.outageUntil = until
					}
				}
				if f.kind == "pause" && f.originWide && f.duration > 0 {
					until := now.Add(f.duration)
					if until.After(p.originPauseTill) {
						p.originPauseTill = until
					}
				}
				copied := *f
				activeFault = &copied
				break
			}
		}
		if activeFault == nil && p.originPauseTill.After(now) {
			originHold = p.originPauseTill.Sub(now)
		}
		p.mu.Unlock()

		if activeFault != nil {
			switch activeFault.kind {
			case "drop":
				return
			case "pause":
				select {
				case <-ctx.Done():
					return
				case <-time.After(activeFault.duration):
				}
				now = time.Now()
				if !activeFault.flushStale {
					live := liveFrameAt(now)
					live -= live % gop
					if live > curFrame {
						curFrame = live
					}
				}
			}
		} else if originHold > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(originHold):
			}
			now = time.Now()
			live := liveFrameAt(now)
			live -= live % gop
			if live > curFrame {
				curFrame = live
			}
		}
		_ = connNo

		if p.cfg.burstEvery > 0 && now.Before(nextBurstRelease) && sentFirst {
			select {
			case <-ctx.Done():
				return
			case <-time.After(min(frameDur, time.Until(nextBurstRelease))):
			}
			continue
		}

		target := liveFrameAt(time.Now())
		if p.cfg.burstEvery > 0 && sentFirst {
			target = liveFrameAt(time.Now().Add(p.cfg.burstEvery))
			nextBurstRelease = time.Now().Add(p.cfg.burstEvery)
		}

		for curFrame <= target {
			if !emitFrame(curFrame, !sentFirst && p.cfg.http2SinglePkt) {
				return
			}
			sentFirst = true
			curFrame++
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(frameDur):
		}
	}
}

func soakTSFrame(g *tsGen, seq uint64) []byte {
	pkt := g.next()
	video2 := tsPacket
	if len(pkt) == 6*tsPacket {
		video2 = 3 * tsPacket
	}
	binary.BigEndian.PutUint64(pkt[video2+8:video2+16], 0x534f414b00000000|(seq&0xffffffff))
	binary.BigEndian.PutUint64(pkt[video2+16:video2+24], seq)
	return pkt
}

type soakRelayWriter struct {
	hdr    http.Header
	pw     *io.PipeWriter
	status int
	ready  chan struct{}
	once   sync.Once
}

func (w *soakRelayWriter) Header() http.Header { return w.hdr }
func (w *soakRelayWriter) WriteHeader(code int) {
	w.once.Do(func() {
		w.status = code
		close(w.ready)
	})
}
func (w *soakRelayWriter) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(p)
}
func (w *soakRelayWriter) Flush() { w.WriteHeader(http.StatusOK) }

func openInMemoryRelay(ctx context.Context, s *Service, rawURL string) (io.ReadCloser, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.RemoteAddr = "127.0.0.1:12345"
	pr, pw := io.Pipe()
	rw := &soakRelayWriter{hdr: make(http.Header), pw: pw, ready: make(chan struct{})}
	go func() {
		defer pw.Close()
		s.serveInputRelay(rw, req)
	}()
	stop := context.AfterFunc(ctx, func() { _ = pr.Close(); _ = pw.Close() })
	select {
	case <-rw.ready:
		stop()
		if rw.status != http.StatusOK {
			_ = pr.Close()
			return nil, rw.status, fmt.Errorf("relay HTTP %d", rw.status)
		}
		return pr, rw.status, nil
	case <-ctx.Done():
		stop()
		_ = pr.Close()
		return nil, 0, ctx.Err()
	}
}

type soakSegmentIndex struct {
	mu           sync.Mutex
	start        time.Time
	segments     []transcode.Segment
	curTags      []any
	changed      chan struct{}
	lastReceived time.Time
	lag          time.Duration
	maxLag       time.Duration
	late500      int
	lateMargin   int
	maxGap       time.Duration
	req          transcode.EncoderRequest
}

func newSoakSegmentIndex(req transcode.EncoderRequest) *soakSegmentIndex {
	return &soakSegmentIndex{req: req, changed: make(chan struct{})}
}

func (s *soakSegmentIndex) recordTick(tick int64, outTime time.Time, tags []any) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if tick == 0 {
		if outTime.IsZero() {
			outTime = now
		}
		s.start = outTime
	}
	for _, t := range tags {
		found := false
		for _, cur := range s.curTags {
			if cur == t {
				found = true
				break
			}
		}
		if !found {
			s.curTags = append(s.curTags, t)
		}
	}
	seq := int64(len(s.segments))
	segStart, frames := soakSegmentFrames(s.req.Source, s.req.SegmentDuration, s.req.LiveSource, seq)
	if tick < segStart+frames {
		return
	}
	segTags := s.curTags
	if len(segTags) == 0 {
		segTags = []any{(*broadcastPolicyTag)(nil)}
	}
	s.curTags = nil
	if len(tags) > 0 {
		s.curTags = append([]any(nil), tags...)
	}
	dur := time.Duration(frames*int64(s.req.Source.FPSDen)) * time.Second / time.Duration(s.req.Source.FPSNum)
	seg := transcode.Segment{
		Seq:        seq,
		Start:      s.start.Add(time.Duration(segStart*int64(s.req.Source.FPSDen)) * time.Second / time.Duration(s.req.Source.FPSNum)),
		Duration:   dur,
		PolicyTags: segTags,
	}
	if !s.lastReceived.IsZero() {
		gap := now.Sub(s.lastReceived)
		if gap > s.maxGap {
			s.maxGap = gap
		}
		lag := max(0, gap-dur)
		s.lag = lag
		if lag > s.maxLag {
			s.maxLag = lag
		}
		if lag > 500*time.Millisecond {
			s.late500++
		}
		segDur := s.req.SegmentDuration
		if segDur <= 0 {
			segDur = 2 * time.Second
		}
		margin := max(segDur, time.Second)
		if lag > margin/2 {
			s.lateMargin++
		}
	}
	s.lastReceived = now
	s.segments = append(s.segments, seg)
	close(s.changed)
	s.changed = make(chan struct{})
}

func soakSegmentFrames(f transcode.RawFormat, segment time.Duration, live bool, seq int64) (int64, int64) {
	if segment <= 0 {
		segment = 2 * time.Second
	}
	g := (int64(segment)*int64(f.FPSNum) + int64(time.Second)*int64(f.FPSDen)/2) / (int64(time.Second) * int64(f.FPSDen))
	startup := []int64{0, g / 4, g / 2}
	steady := g
	if live {
		startup = append(startup, g, g+g/2)
		steady = 2 * g
	}
	if seq < int64(len(startup)) {
		end := steady
		if seq+1 < int64(len(startup)) {
			end = startup[seq+1]
		}
		return startup[seq], end - startup[seq]
	}
	return steady + (seq-int64(len(startup)))*g, g
}

func (s *soakSegmentIndex) Latest() (transcode.Segment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.segments) == 0 {
		return transcode.Segment{}, false
	}
	return s.segments[len(s.segments)-1], true
}

func (s *soakSegmentIndex) Window(from, until time.Time) []transcode.Segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []transcode.Segment
	for _, seg := range s.segments {
		if seg.Start.Before(until) && seg.Start.Add(seg.Duration).After(from) {
			out = append(out, seg)
		}
	}
	return out
}

func (s *soakSegmentIndex) Since(seq int64, limit int) []transcode.Segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []transcode.Segment
	for _, seg := range s.segments {
		if seg.Seq > seq {
			out = append(out, seg)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func (s *soakSegmentIndex) Wait(ctx context.Context, after int64) (transcode.Segment, error) {
	for {
		s.mu.Lock()
		for _, seg := range s.segments {
			if seg.Seq == after+1 {
				s.mu.Unlock()
				return seg, nil
			}
		}
		ch := s.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return transcode.Segment{}, ctx.Err()
		case <-ch:
		}
	}
}

func (s *soakSegmentIndex) Format() (string, int, int, int) {
	return "avc1.64001f,mp4a.40.2", s.req.Source.Width, s.req.Source.Height, 4_000_000
}
func (s *soakSegmentIndex) ServeTS(w http.ResponseWriter, _ *http.Request, _ int64) {
	w.WriteHeader(200)
}
func (s *soakSegmentIndex) ServeFMP4(w http.ResponseWriter, _ *http.Request, _ int64) {
	w.WriteHeader(200)
}
func (s *soakSegmentIndex) ServeInit(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }
func (s *soakSegmentIndex) AttachTS(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

type soakFrameKind uint8

const (
	soakKindLive soakFrameKind = iota
	soakKindHold
	soakKindScreen
	soakKindBlack
)

type soakFrameSample struct {
	kind      soakFrameKind
	seq       uint64
	sourcePTS time.Duration
	at        time.Time
	pcmSilent bool
}

type soakEncoder struct {
	req        transcode.EncoderRequest
	index      *soakSegmentIndex
	mu         sync.Mutex
	tick       int64
	samples    []soakFrameSample
	pcmSamples int64
}

func (e *soakEncoder) Write(ctx context.Context, f transcode.RawFrame) error {
	return e.WriteShared(ctx, f)
}

func (e *soakEncoder) WriteShared(ctx context.Context, f transcode.RawFrame) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	var kind soakFrameKind
	var seq uint64
	isLiveVideo := len(f.Video) >= 17 && f.Video[0] == soakVideoLive && binary.BigEndian.Uint64(f.Video[1:9]) == soakFrameMagic
	switch {
	case isLiveVideo && f.SourcePTSValid:
		kind = soakKindLive
		seq = binary.BigEndian.Uint64(f.Video[9:17])
	case isLiveVideo && !f.SourcePTSValid:
		kind = soakKindHold
		seq = binary.BigEndian.Uint64(f.Video[9:17])
	case len(f.Video) >= 9 && f.Video[0] == soakVideoBlack && binary.BigEndian.Uint64(f.Video[1:9]) == 0x1010101010101010:
		kind = soakKindBlack
	default:
		kind = soakKindScreen
	}
	silent := true
	for _, b := range f.PCM {
		if b != 0 {
			silent = false
			break
		}
	}
	e.mu.Lock()
	tick := e.tick
	e.tick++
	e.pcmSamples += int64(len(f.PCM) / (e.req.Source.Channels * 2))
	e.samples = append(e.samples, soakFrameSample{
		kind:      kind,
		seq:       seq,
		sourcePTS: f.SourcePTS,
		at:        time.Now(),
		pcmSilent: silent,
	})
	e.mu.Unlock()
	e.index.recordTick(tick, f.OutputTime, f.PolicyTags)
	return nil
}

func (e *soakEncoder) Segments() transcode.SegmentIndex { return e.index }
func (e *soakEncoder) Close() error                     { return nil }
func (e *soakEncoder) RecentPublishLag() time.Duration {
	e.index.mu.Lock()
	defer e.index.mu.Unlock()
	return e.index.maxLag
}

type soakTranscoder struct {
	*fakeTranscoder
	origin       time.Time
	cfg          soakScript
	channelID    atomic.Pointer[string]
	svc          atomic.Pointer[Service]
	gapTriggered atomic.Bool
	mu           sync.Mutex
	encoders     []*soakEncoder
	liveDecoders int
}

func (t *soakTranscoder) ContinuousBudget() (float64, bool) { return 100, false }

func (t *soakTranscoder) ProbeLive(ctx context.Context, input string) (*core.MediaInfo, error) {
	return t.Probe(ctx, input)
}

func (t *soakTranscoder) Probe(ctx context.Context, input string) (*core.MediaInfo, error) {
	s := t.svc.Load()
	if s != nil && strings.HasPrefix(input, "http://127.0.0.1:1/") {
		rc, _, err := openInMemoryRelay(ctx, s, input)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, 3*tsPacket)
		_, err = io.ReadFull(rc, buf)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
	}
	w, h, fps := t.cfg.width, t.cfg.height, t.cfg.fps
	if w <= 0 {
		w = 64
	}
	if h <= 0 {
		h = 36
	}
	if fps <= 0 {
		fps = 25
	}
	return &core.MediaInfo{
		Container: "mpegts",
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: w, Height: h, FrameRate: float64(fps)},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000},
		},
	}, nil
}

func (t *soakTranscoder) StartEncoder(_ context.Context, req transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
	enc := &soakEncoder{req: req, index: newSoakSegmentIndex(req)}
	t.mu.Lock()
	t.encoders = append(t.encoders, enc)
	t.mu.Unlock()
	return enc, nil
}

func (t *soakTranscoder) StartDecoder(ctx context.Context, req transcode.DecoderRequest) (transcode.Decoder, error) {
	if !strings.HasPrefix(req.InputURL, "http://127.0.0.1:1/") {
		base := blackFrame(req.Format, 0).Video
		if len(base) > 0 {
			base[0] = 32
		}
		return &soakScreenDecoder{format: req.Format, video: base, closed: make(chan struct{})}, nil
	}
	s := t.svc.Load()
	rc, _, err := openInMemoryRelay(ctx, s, req.InputURL)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.liveDecoders++
	t.mu.Unlock()
	return newSoakTSDecoder(ctx, rc, req.Format, t), nil
}

type soakScreenDecoder struct {
	format transcode.RawFormat
	video  []byte
	tick   int64
	closed chan struct{}
	once   sync.Once
}

func (d *soakScreenDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	select {
	case <-ctx.Done():
		return transcode.RawFrame{}, ctx.Err()
	case <-d.closed:
		return transcode.RawFrame{}, io.EOF
	default:
	}
	samples := tickSamples(d.format, d.tick)
	d.tick++
	return transcode.RawFrame{
		Video: d.video,
		PCM:   make([]byte, samples*d.format.Channels*2),
	}, nil
}

func (d *soakScreenDecoder) Close() error {
	d.once.Do(func() { close(d.closed) })
	return nil
}

const (
	soakVideoSlots  = 512
	soakVideoStride = 32
)

type soakTSDecoder struct {
	rc         io.ReadCloser
	br         *bufio.Reader
	format     transcode.RawFormat
	tc         *soakTranscoder
	frameBytes int
	videoArena []byte
	tick       int64
	phase      float64
	once       sync.Once
}

func newSoakTSDecoder(ctx context.Context, rc io.ReadCloser, format transcode.RawFormat, tc *soakTranscoder) *soakTSDecoder {
	base := blackFrame(format, 0).Video
	frameBytes := len(base)
	// Share one compact backing slice across 512 disjoint 32-byte header slots
	// so 1080p60 synctest runs use ~3.1 MB instead of allocating multi-GB buffers.
	arena := make([]byte, soakVideoSlots*soakVideoStride+frameBytes)
	for i := range arena {
		arena[i] = soakVideoBlack
	}
	d := &soakTSDecoder{
		rc:         rc,
		br:         bufio.NewReaderSize(rc, 32<<10),
		format:     format,
		tc:         tc,
		frameBytes: frameBytes,
		videoArena: arena,
	}
	context.AfterFunc(ctx, func() { _ = d.Close() })
	return d
}

func (d *soakTSDecoder) maybeInjectDecoderGap(ctx context.Context) error {
	if d.tc == nil || d.tc.cfg.decoderGap <= 0 || d.tc.cfg.decoderGapAt <= 0 {
		return nil
	}
	if time.Since(d.tc.origin) < d.tc.cfg.decoderGapAt {
		return nil
	}
	if !d.tc.gapTriggered.CompareAndSwap(false, true) {
		return nil
	}
	// Let already-queued frames in broadcastInput.frames drain first so the
	// broadcast clock experiences the full scripted decoder gap without any
	// upstream transport fault.
	if s := d.tc.svc.Load(); s != nil {
		if chID := d.tc.channelID.Load(); chID != nil {
			if b := s.broadcasts.running(broadcastKey{channelID: *chID}); b != nil && b.current != nil {
				ch := b.current.frames
				for len(ch) > 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d.tc.cfg.decoderGap):
		return nil
	}
}

func (d *soakTSDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	if err := d.maybeInjectDecoderGap(ctx); err != nil {
		return transcode.RawFrame{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = d.rc.Close() })
	defer stop()
	var pkt [tsPacket]byte
	var haveVideo bool
	var pts int64
	var seq uint64
	for {
		if _, err := io.ReadFull(d.br, pkt[:]); err != nil {
			if ctx.Err() != nil {
				return transcode.RawFrame{}, ctx.Err()
			}
			return transcode.RawFrame{}, err
		}
		if pkt[0] != 0x47 {
			return transcode.RawFrame{}, fmt.Errorf("lost TS packet sync: %#x", pkt[0])
		}
		id := pid(pkt[:])
		pusi := pkt[1]&0x40 != 0
		switch {
		case id == 0x100 && pusi:
			at := payloadAt(pkt[:])
			if pOff, _ := pesTimes(pkt[:], at); pOff >= 0 {
				pts = readTS(pkt[pOff:])
				haveVideo = true
			}
		case id == 0x100 && !pusi && haveVideo:
			if binary.BigEndian.Uint64(pkt[8:16])>>32 == 0x534f414b {
				seq = binary.BigEndian.Uint64(pkt[16:24])
			}
		case id == 0x101 && !pusi && haveVideo:
			samples := tickSamples(d.format, d.tick)
			slot := int(d.tick%soakVideoSlots) * soakVideoStride
			vid := d.videoArena[slot : slot+d.frameBytes]
			vid[0] = soakVideoLive
			if len(vid) >= 17 {
				binary.BigEndian.PutUint64(vid[1:9], soakFrameMagic)
				binary.BigEndian.PutUint64(vid[9:17], seq)
			}
			pcm := make([]byte, samples*d.format.Channels*2)
			step := 2 * math.Pi * 440.0 / float64(d.format.SampleRate)
			for i := range samples {
				v := int16(math.Round(12000 * math.Sin(d.phase)))
				d.phase += step
				for ch := range d.format.Channels {
					off := (i*d.format.Channels + ch) * 2
					binary.LittleEndian.PutUint16(pcm[off:], uint16(v))
				}
			}
			d.tick++
			return transcode.RawFrame{
				Video:          vid,
				PCM:            pcm,
				SourcePTS:      time.Duration(pts) * time.Second / tsClockHz,
				SourcePTSValid: true,
			}, nil
		}
	}
}

func (d *soakTSDecoder) Close() error {
	d.once.Do(func() { _ = d.rc.Close() })
	return nil
}

func runSoakScenario(t *testing.T, sc soakScript) soakMetrics {
	t.Helper()
	db, err := store.OpenPath(context.Background(), filepath.Join(t.TempDir(), "ether.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var m soakMetrics
	synctest.Test(t, func(t *testing.T) {
		origin := time.Now()
		prov := newSoakProvider(origin, sc)
		tc := &soakTranscoder{fakeTranscoder: &fakeTranscoder{}, origin: origin, cfg: sc}
		logState := &soakLogState{}
		srvStab := sc.serverStability
		if srvStab == "" {
			srvStab = sc.stability
		}
		if srvStab == "" {
			srvStab = stabilityBalanced
		}
		s := testService(t, Options{
			DB:         db,
			Logger:     slog.New(&soakLogHandler{state: logState}),
			HTTPClient: &http.Client{Transport: prov},
			Transcoder: tc,
			Stability:  func() string { return srvStab },
		})
		tc.svc.Store(s)
		s.inputRelay.host = "127.0.0.1:1"
		s.inputRelay.grants = map[[sha256.Size]byte]*inputGrant{}
		s.inputRelay.bySess = map[*session]*inputGrant{}

		ctx := context.Background()
		if _, err := s.ImportSource(ctx, Source{
			ID:             "madnet",
			Name:           "Mad Net",
			MaxConnections: sc.maxConnections,
			TuneGapSeconds: int(sc.tuneGap / time.Second),
			Playlist:       "#EXTM3U\n#EXTINF:-1 tvg-id=\"fox\",Fox News\nhttps://madnet.invalid/live/fox.ts\n",
		}); err != nil {
			s.Close()
			t.Fatal(err)
		}
		e, err := s.sourceEntry(ctx, "madnet")
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		e.limit = sc.maxConnections
		e.gap = sc.tuneGap
		s.setSourceEntry("madnet", e)

		channels, err := s.Channels(ctx)
		if err != nil || len(channels) != 1 {
			s.Close()
			t.Fatalf("channels = %d, err = %v", len(channels), err)
		}
		ch := channels[0]
		chID := ch.ID
		tc.channelID.Store(&chID)
		chStab := sc.channelStability
		if chStab == "" && sc.serverStability == "" {
			chStab = sc.stability
		}
		ch.Transcode = true
		if chStab != "" {
			ch.Stability = chStab
		}
		ch, err = s.SaveChannel(ctx, ch)
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		if _, _, err = s.channelSnapshot(ctx); err != nil {
			s.Close()
			t.Fatal(err)
		}

		user := &core.User{ID: 1, IsOwner: true}
		tuneStart := time.Now()
		info, err := s.Tune(ctx, user, ch.ID, core.ClientCaps{})
		if err != nil {
			s.Close()
			t.Fatalf("Tune failed: %v", err)
		}
		m.tunedIn = time.Since(tuneStart)

		lastMediaSeq := int64(-1)
		lastFetchedSeq := int64(-1)
		steps := int(sc.duration / time.Second)
		var sawPeak bool
		var sawDrain bool
		m.minPostPeakRes = time.Hour
		for step := 0; step < steps; step++ {
			time.Sleep(time.Second)
			ss, gerr := s.getSession(ctx, info.ID, user)
			if gerr != nil {
				m.playlistGaps++
				continue
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/livetv/sessions/"+info.ID+"/index.m3u8", nil)
			s.serveBroadcastPlaylist(rec, req, ss)
			if rec.Code != http.StatusOK {
				m.playlistGaps++
			} else {
				body := rec.Body.String()
				if !strings.Contains(body, "#EXTINF:") || strings.Contains(body, "#EXT-X-DISCONTINUITY") {
					m.playlistGaps++
				}
				var maxSeqInPlaylist int64 = -1
				for _, line := range strings.Split(body, "\n") {
					if strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:") {
						var seq int64
						fmt.Sscan(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), &seq)
						if lastMediaSeq >= 0 && seq < lastMediaSeq {
							m.playlistGaps++
						}
						lastMediaSeq = seq
					}
					if strings.HasPrefix(line, "relay/") {
						var seq int64
						if _, err := fmt.Sscanf(line, "relay/%d.ts", &seq); err == nil && seq > maxSeqInPlaylist {
							maxSeqInPlaylist = seq
						}
					}
				}
				if maxSeqInPlaylist > lastFetchedSeq {
					segRec := httptest.NewRecorder()
					segReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/livetv/sessions/%s/relay/%d.ts", info.ID, maxSeqInPlaylist), nil)
					s.serveBroadcastTS(segRec, segReq, ss, maxSeqInPlaylist)
					if segRec.Code != http.StatusOK {
						m.playlistGaps++
					}
					lastFetchedSeq = maxSeqInPlaylist
				}
			}

			if b := s.broadcasts.running(broadcastKey{channelID: ch.ID}); b != nil {
				if pl, ok := b.planner.(*providerBroadcastPlanner); ok {
					pl.mu.Lock()
					att := pl.attempt
					pl.mu.Unlock()
					if att != nil && att.link != nil {
						if h, ok := s.inputHealth(att.link.ss); ok {
							if h.ReserveDepth > m.peakReserve {
								m.peakReserve = h.ReserveDepth
							}
							m.endReserve = h.ReserveDepth
							if m.peakReserve >= 11*time.Second {
								sawPeak = true
							}
							if sawPeak && h.ReserveDepth < m.minPostPeakRes {
								m.minPostPeakRes = h.ReserveDepth
							}
							if sawPeak && h.ReserveDepth < 7*time.Second {
								sawDrain = true
							}
							if sawDrain && h.ReserveDepth > m.postDrainReserve {
								m.postDrainReserve = h.ReserveDepth
							}
						}
					}
				}
			}
		}
		if !sawPeak {
			m.minPostPeakRes = 0
		}

		endTime := time.Now()
		_ = s.Stop(ctx, info.ID, user)
		s.Close()
		synctest.Wait()

		logState.mu.Lock()
		m.inputLostLogs = logState.inputLost
		m.adaptedStallAfter = logState.adaptedStallAfter
		burstDoneAt := logState.burstFinishedAt
		logState.mu.Unlock()

		tc.mu.Lock()
		m.teardowns = max(0, tc.liveDecoders-1)
		var enc *soakEncoder
		if len(tc.encoders) > 0 {
			enc = tc.encoders[0]
		}
		tc.mu.Unlock()

		prov.mu.Lock()
		m.providerOpens = prov.opens
		m.maxConcurrentOpen = prov.maxConcurrent
		if len(prov.openTimes) >= 2 {
			m.minOpenGap = time.Hour
			for i := 1; i < len(prov.openTimes); i++ {
				if g := prov.openTimes[i].Sub(prov.openTimes[i-1]); g < m.minOpenGap {
					m.minOpenGap = g
				}
			}
		}
		prov.mu.Unlock()

		if enc != nil {
			fps := prov.fps()
			frameDur := time.Second / time.Duration(fps)
			minTickSpacing := frameDur - 2*time.Microsecond
			postBurstAfter := origin.Add(3 * time.Second)
			if !burstDoneAt.IsZero() {
				postBurstAfter = burstDoneAt.Add(frameDur)
			}
			trailingWindowStart := endTime.Add(-8 * time.Second)

			enc.mu.Lock()
			m.framesTotal = len(enc.samples)
			var haveLiveSeq bool
			var lastLiveSeq uint64
			var lastLiveAt time.Time
			inHold := false
			for i, smp := range enc.samples {
				if i > 0 && smp.at.After(postBurstAfter) {
					if smp.at.Sub(enc.samples[i-1].at) < minTickSpacing {
						m.fastOutputTicks++
					}
				}
				switch smp.kind {
				case soakKindLive:
					m.liveFrames++
					if haveLiveSeq && smp.seq > lastLiveSeq+1 {
						m.droppedFrames += int(smp.seq - lastLiveSeq - 1)
					}
					haveLiveSeq = true
					lastLiveSeq = smp.seq
					lastLiveAt = smp.at
					inHold = false
				case soakKindHold:
					m.heldFrames++
					if !inHold {
						m.holdEpisodes++
						inHold = true
					}
					if smp.at.After(trailingWindowStart) {
						m.trailingHoldFrames++
					}
				case soakKindScreen:
					m.screenFrames++
					inHold = false
				case soakKindBlack:
					m.blackFrames++
					inHold = false
				}
			}
			if !lastLiveAt.IsZero() {
				expectedMedia := 20*time.Second + lastLiveAt.Sub(origin)
				actualMedia := time.Duration(lastLiveSeq) * frameDur
				m.endLiveLag = expectedMedia - actualMedia
			}
			expectedPCM := int64(m.framesTotal) * int64(enc.req.Source.SampleRate) * int64(enc.req.Source.FPSDen) / int64(enc.req.Source.FPSNum)
			diffSamples := enc.pcmSamples - expectedPCM
			if diffSamples < 0 {
				diffSamples = -diffSamples
			}
			m.avSyncDrift = time.Duration(diffSamples) * time.Second / time.Duration(enc.req.Source.SampleRate)
			enc.mu.Unlock()

			enc.index.mu.Lock()
			m.segmentsPublished = len(enc.index.segments)
			m.maxSegmentGap = enc.index.maxGap
			m.maxPublishLag = enc.index.maxLag
			m.latePublishes = enc.index.late500
			m.lateOverMargin = enc.index.lateMargin
			enc.index.mu.Unlock()
		}
	})
	return m
}

func TestIPTVSoakSuite(t *testing.T) {
	cases := []struct {
		sc    soakScript
		check func(t *testing.T, m soakMetrics)
	}{
		{
			sc: soakScript{
				name:           "madnet-1080p60-balanced-3.9s-burst-decoder-gap-and-4s-hold",
				stability:      stabilityBalanced,
				width:          1920,
				height:         1080,
				fps:            60,
				maxConnections: 2,
				tuneGap:        time.Second,
				startupBurst:   3900 * time.Millisecond,
				burstEvery:     3900 * time.Millisecond,
				http2SinglePkt: true,
				decoderGapAt:   20 * time.Second,
				decoderGap:     1750 * time.Millisecond,
				duration:       62 * time.Second,
				faults: []soakFault{
					// Drains accrued reserve and holds the last picture for ~4 s,
					// then flushes every paused frame so zero upstream frames are
					// skipped and Ether must resume from held+1 without rejoin drops.
					{at: 34 * time.Second, kind: "pause", duration: 9500 * time.Millisecond, reconnectWait: 10500 * time.Millisecond, flushStale: true},
				},
			},
			check: func(t *testing.T, m soakMetrics) {
				if m.adaptedStallAfter < 5500*time.Millisecond {
					t.Fatalf("expected 3.9s Mad Net bursts to adapt stall_after >= 5.5s, got %v", m.adaptedStallAfter)
				}
				if m.inputLostLogs != 0 || m.teardowns != 0 {
					t.Fatalf("expected zero input-lost logs and zero decoder teardowns, got inputLost=%d teardowns=%d", m.inputLostLogs, m.teardowns)
				}
				if m.screenFrames != 0 {
					t.Fatalf("expected zero reconnecting screen frames across 1.75s decoder gap and 4s hold, got %d", m.screenFrames)
				}
				if m.holdEpisodes != 2 {
					t.Fatalf("expected exactly 2 hold episodes (1.75s decoder gap + ~4s hold), got %d (heldFrames=%d)", m.holdEpisodes, m.heldFrames)
				}
				if m.heldFrames < 280 || m.heldFrames > 440 {
					t.Fatalf("expected ~5.75s total held frames (~345 at 60fps) across 1.75s decoder gap + ~4s hold, got %d", m.heldFrames)
				}
				if m.trailingHoldFrames != 0 {
					t.Fatalf("expected zero held frames after recovery, got %d", m.trailingHoldFrames)
				}
				if m.droppedFrames != 0 {
					t.Fatalf("expected zero dropped frames across 4s hold and 1.75s decoder gap, got %d", m.droppedFrames)
				}
				if m.fastOutputTicks != 0 {
					t.Fatalf("expected zero output ticks faster than real time, got %d", m.fastOutputTicks)
				}
			},
		},
		{
			sc: soakScript{
				name:           "madnet-bursty-pause-4s-2conn",
				stability:      stabilityFastest,
				maxConnections: 2,
				tuneGap:        time.Second,
				startupBurst:   3900 * time.Millisecond,
				burstEvery:     1200 * time.Millisecond,
				http2SinglePkt: true,
				duration:       45 * time.Second,
				faults: []soakFault{
					{at: 18 * time.Second, kind: "pause", duration: 4 * time.Second},
				},
			},
			check: func(t *testing.T, m soakMetrics) {
				if m.screenFrames != 0 {
					t.Fatalf("expected zero reconnecting screen frames on 4s socket stall, got %d", m.screenFrames)
				}
				if m.teardowns != 0 {
					t.Fatalf("expected zero decoder teardowns during parallel race splice, got %d", m.teardowns)
				}
				if m.endLiveLag > 2500*time.Millisecond {
					t.Fatalf("expected Fastest stream to stay near live edge without burst replay lag, got endLiveLag=%v", m.endLiveLag)
				}
			},
		},
		{
			sc: soakScript{
				name:           "quiet-pause-5s-1conn",
				stability:      stabilityFastest,
				maxConnections: 1,
				startupBurst:   0,
				duration:       35 * time.Second,
				faults: []soakFault{
					{at: 10 * time.Second, kind: "pause", duration: 5 * time.Second, flushStale: true},
				},
			},
			check: func(t *testing.T, m soakMetrics) {
				if m.screenFrames != 0 {
					t.Fatalf("expected 5s quiet pause to hold last picture without reconnecting screen, got screenFrames=%d", m.screenFrames)
				}
				if m.heldFrames == 0 {
					t.Fatalf("expected held frames during 5s quiet pause on zero-reserve stream, got 0")
				}
				if m.droppedFrames != 0 {
					t.Fatalf("expected zero dropped frames after 5s quiet pause, got %d", m.droppedFrames)
				}
				if m.fastOutputTicks != 0 {
					t.Fatalf("expected zero output ticks faster than real time, got %d", m.fastOutputTicks)
				}
				if m.providerOpens != 1 {
					t.Fatalf("expected quiet open socket on 1-conn source not to tear down at 2s, got providerOpens=%d", m.providerOpens)
				}
				if m.maxConcurrentOpen > 1 {
					t.Fatalf("expected maxConcurrentOpen <= 1 on 1-conn source, got %d", m.maxConcurrentOpen)
				}
			},
		},
		{
			sc: soakScript{
				name:           "midstream-drop-slow-reconnect-3.5s",
				stability:      stabilityFastest,
				maxConnections: 2,
				startupBurst:   2 * time.Second,
				duration:       35 * time.Second,
				faults: []soakFault{
					{at: 12 * time.Second, kind: "drop", reconnectWait: 3500 * time.Millisecond},
				},
			},
			check: func(t *testing.T, m soakMetrics) {
				if m.screenFrames != 0 {
					t.Fatalf("expected 3.5s mid-stream drop + reconnect to hold picture without screen cut, got screenFrames=%d", m.screenFrames)
				}
				if m.teardowns != 0 {
					t.Fatalf("expected tap splice to recover without decoder teardown, got teardowns=%d", m.teardowns)
				}
				if m.fastOutputTicks != 0 {
					t.Fatalf("expected zero output ticks faster than real time, got %d", m.fastOutputTicks)
				}
			},
		},
		{
			sc: soakScript{
				name:           "balanced-mpegts-builds-and-rebuilds-reserve",
				stability:      stabilityBalanced,
				maxConnections: 2,
				startupBurst:   3900 * time.Millisecond,
				duration:       560 * time.Second,
				faults: []soakFault{
					{at: 290 * time.Second, kind: "pause", duration: 7 * time.Second, originWide: true},
				},
			},
			check: func(t *testing.T, m soakMetrics) {
				if m.peakReserve < 11500*time.Millisecond {
					t.Fatalf("expected Balanced MPEG-TS to build ~12s reserve, got peakReserve=%v", m.peakReserve)
				}
				if m.minPostPeakRes > 7*time.Second {
					t.Fatalf("expected 7s origin-wide stall to drain reserve below 7s, got minPostPeakRes=%v", m.minPostPeakRes)
				}
				if m.postDrainReserve < 11500*time.Millisecond {
					t.Fatalf("expected Balanced MPEG-TS to rebuild ~12s reserve after stall drain, got postDrainReserve=%v", m.postDrainReserve)
				}
				if m.heldFrames != 0 || m.screenFrames != 0 {
					t.Fatalf("expected 12s reserve to absorb 7s origin stall with zero holds/screens, got held=%d screen=%d", m.heldFrames, m.screenFrames)
				}
				if m.fastOutputTicks != 0 {
					t.Fatalf("expected zero output ticks faster than real time, got %d", m.fastOutputTicks)
				}
			},
		},
		{
			sc: soakScript{
				name:           "extended-outage-14s-screen-after-10s-hold",
				stability:      stabilityFastest,
				maxConnections: 1,
				startupBurst:   0,
				duration:       40 * time.Second,
				faults: []soakFault{
					{at: 8 * time.Second, kind: "drop", reconnectWait: 14 * time.Second},
				},
			},
			check: func(t *testing.T, m soakMetrics) {
				if m.heldFrames < 220 {
					t.Fatalf("expected ~10s (~250 frames) of held picture before fallback screen, got heldFrames=%d", m.heldFrames)
				}
				if m.screenFrames == 0 {
					t.Fatalf("expected prewarmed fallback screen after 10s hold expired during 14s outage, got 0")
				}
				if m.maxConcurrentOpen > 1 {
					t.Fatalf("expected maxConcurrentOpen <= 1 on 1-conn source, got %d", m.maxConcurrentOpen)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.sc.name, func(t *testing.T) {
			m := runSoakScenario(t, tc.sc)
			t.Logf("METRICS %s: frames=%d (live=%d held=%d episodes=%d screen=%d black=%d dropped=%d fastTicks=%d) segs=%d maxGap=%v maxLag=%v late500=%d lateMargin=%d teardowns=%d inputLost=%d opens=%d maxConc=%d stallAfter=%v peakRes=%v minPostPeak=%v postDrainRes=%v endRes=%v endLag=%v playlistGaps=%d avDrift=%v",
				tc.sc.name, m.framesTotal, m.liveFrames, m.heldFrames, m.holdEpisodes, m.screenFrames, m.blackFrames, m.droppedFrames, m.fastOutputTicks,
				m.segmentsPublished, m.maxSegmentGap, m.maxPublishLag, m.latePublishes, m.lateOverMargin,
				m.teardowns, m.inputLostLogs, m.providerOpens, m.maxConcurrentOpen, m.adaptedStallAfter,
				m.peakReserve, m.minPostPeakRes, m.postDrainReserve, m.endReserve, m.endLiveLag, m.playlistGaps, m.avSyncDrift)

			if m.playlistGaps != 0 {
				t.Fatalf("expected zero playlist/segment gaps, got %d", m.playlistGaps)
			}
			if m.blackFrames != 0 {
				t.Fatalf("expected zero black frames, got %d", m.blackFrames)
			}
			if m.avSyncDrift != 0 {
				t.Fatalf("expected zero A/V sample drift, got %v", m.avSyncDrift)
			}
			if m.lateOverMargin != 0 {
				t.Fatalf("expected zero segments published past client margin/2, got %d (maxLag=%v)", m.lateOverMargin, m.maxPublishLag)
			}
			if tc.sc.maxConnections > 0 && m.maxConcurrentOpen > tc.sc.maxConnections {
				t.Fatalf("expected maxConcurrentOpen <= %d, got %d", tc.sc.maxConnections, m.maxConcurrentOpen)
			}
			tc.check(t, m)
		})
	}
}

type gatedRaceDecoder struct {
	format      transcode.RawFormat
	video       []byte
	id          byte
	stallAt     time.Duration
	firstDelay  time.Duration
	burstFrames int
	origin      time.Time
	started     time.Time
	tick        int64
	closed      atomic.Bool
	closeAt     atomic.Int64
	done        chan struct{}
	once        sync.Once
}

func (d *gatedRaceDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	if d.tick == 0 && d.firstDelay > 0 {
		select {
		case <-ctx.Done():
			return transcode.RawFrame{}, ctx.Err()
		case <-d.done:
			return transcode.RawFrame{}, io.EOF
		case <-time.After(d.firstDelay):
		}
	}
	if d.stallAt > 0 && time.Since(d.origin) >= d.stallAt {
		select {
		case <-ctx.Done():
			return transcode.RawFrame{}, ctx.Err()
		case <-d.done:
			return transcode.RawFrame{}, io.EOF
		}
	}
	fps := d.format.FPSNum / d.format.FPSDen
	frameDur := time.Second / time.Duration(fps)
	if int(d.tick) >= d.burstFrames {
		due := d.started.Add(d.firstDelay + time.Duration(int(d.tick)-d.burstFrames)*frameDur)
		if wait := time.Until(due); wait > 0 {
			select {
			case <-ctx.Done():
				return transcode.RawFrame{}, ctx.Err()
			case <-d.done:
				return transcode.RawFrame{}, io.EOF
			case <-time.After(wait):
			}
		}
	}
	samples := tickSamples(d.format, d.tick)
	vid := make([]byte, len(d.video))
	copy(vid, d.video)
	vid[0] = soakVideoLive
	binary.BigEndian.PutUint64(vid[1:9], soakFrameMagic)
	binary.BigEndian.PutUint64(vid[9:17], uint64(d.id)*1_000_000+uint64(d.tick))
	pts := time.Duration(d.tick) * frameDur
	d.tick++
	return transcode.RawFrame{
		Video:          vid,
		PCM:            make([]byte, samples*d.format.Channels*2),
		SourcePTS:      pts,
		SourcePTSValid: true,
	}, nil
}

func (d *gatedRaceDecoder) Close() error {
	d.once.Do(func() {
		d.closed.Store(true)
		d.closeAt.Store(time.Now().UnixNano())
		close(d.done)
	})
	return nil
}

type parallelReplacePlanner struct {
	format transcode.RawFormat
	opens  atomic.Int32
	first  *gatedRaceDecoder
	second *gatedRaceDecoder
}

func (p *parallelReplacePlanner) coldWait() time.Duration        { return 4 * time.Second }
func (p *parallelReplacePlanner) holdStalled() time.Duration     { return steadyHoldPicture }
func (p *parallelReplacePlanner) startupLead() time.Duration     { return 2 * time.Second }
func (p *parallelReplacePlanner) segmentDuration() time.Duration { return 2 * time.Second }
func (p *parallelReplacePlanner) keepStalled(d time.Duration) bool {
	return d < 3*time.Second
}

func (p *parallelReplacePlanner) plans(time.Time) (broadcastPlan, *broadcastPlan) {
	return broadcastPlan{
		id:        "provider-race",
		element:   "programme",
		programme: true,
		open: func(context.Context) (transcode.Decoder, func(), error) {
			n := p.opens.Add(1)
			if n == 1 {
				p.first.started = time.Now()
				return p.first, nil, nil
			}
			p.second.started = time.Now()
			return p.second, nil, nil
		},
		failed: func(error) time.Time { return time.Now() },
	}, nil
}

func TestSteadyParallelReplacementKeepsOldInputUntilPicture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origin := time.Now()
		format := transcode.RawFormat{
			Width:       64,
			Height:      36,
			FPSNum:      25,
			FPSDen:      1,
			SampleRate:  48000,
			Channels:    2,
			PixelFormat: "yuv420p",
		}
		baseVid := blackFrame(format, 0).Video
		first := &gatedRaceDecoder{
			format:      format,
			video:       baseVid,
			id:          1,
			stallAt:     6 * time.Second,
			burstFrames: 50,
			origin:      origin,
			done:        make(chan struct{}),
		}
		second := &gatedRaceDecoder{
			format:      format,
			video:       baseVid,
			id:          2,
			firstDelay:  1800 * time.Millisecond,
			burstFrames: 75, // 3s startup burst on replacement must NOT publish faster than real time
			origin:      origin,
			done:        make(chan struct{}),
		}
		planner := &parallelReplacePlanner{format: format, first: first, second: second}
		tc := &soakTranscoder{fakeTranscoder: &fakeTranscoder{}, origin: origin}
		b, err := newBroadcast(t.Context(), tc, planner, format)
		if err != nil {
			t.Fatal(err)
		}
		defer b.close()
		if _, err := b.addRendition(t.Context(), "main", core.Decision{}); err != nil {
			t.Fatal(err)
		}
		b.start()

		// Primary stalls at t=6s; keepStalled holds until +3s (t=9s). Replacement
		// opens at t=9s and takes 1.8s to deliver its first picture (t=10.8s).
		// At t=10s, replacement is running in parallel while primary MUST still be open!
		time.Sleep(10 * time.Second)
		if planner.opens.Load() < 2 {
			t.Fatalf("expected replacement open in flight by t=10s, opens=%d", planner.opens.Load())
		}
		if first.closed.Load() {
			t.Fatalf("old input was closed before replacement delivered its first picture")
		}

		// Advance past t=13.8s so replacement delivers its first picture and splices.
		time.Sleep(5 * time.Second)
		if !first.closed.Load() {
			t.Fatalf("expected old input to close once replacement spliced its first picture")
		}
		closeAt := time.Unix(0, first.closeAt.Load())
		secondPictureAt := second.started.Add(second.firstDelay)
		if closeAt.Before(secondPictureAt) {
			t.Fatalf("old input closed at %v before replacement's first picture at %v", closeAt.Sub(origin), secondPictureAt.Sub(origin))
		}

		b.close()
		synctest.Wait()

		enc := tc.encoders[0]
		enc.mu.Lock()
		defer enc.mu.Unlock()
		minSpacing := (time.Second / 25) - 2*time.Microsecond
		var sawSecond bool
		var fastAfterSplice int
		for i, smp := range enc.samples {
			if smp.kind == soakKindLive && smp.seq >= 2_000_000 {
				if sawSecond && i > 0 && smp.at.Sub(enc.samples[i-1].at) < minSpacing {
					fastAfterSplice++
				}
				sawSecond = true
			}
		}
		if !sawSecond {
			t.Fatalf("expected spliced frames from replacement decoder")
		}
		if fastAfterSplice != 0 {
			t.Fatalf("expected replacement's 3s burst to stay buffered without fast output ticks, got %d fast ticks", fastAfterSplice)
		}
	})
}

func TestIPTVSoakStabilityPresetsAndChannelOverride(t *testing.T) {
	t.Run("rock-solid-builds-30s-reserve", func(t *testing.T) {
		m := runSoakScenario(t, soakScript{
			name:            "rock-solid-builds-30s-reserve",
			serverStability: stabilityRockSolid,
			maxConnections:  2,
			startupBurst:    3900 * time.Millisecond,
			duration:        830 * time.Second,
		})
		if m.peakReserve < 29500*time.Millisecond {
			t.Fatalf("expected Rock solid MPEG-TS to build ~30s reserve, got peakReserve=%v (end=%v)", m.peakReserve, m.endReserve)
		}
		if m.screenFrames != 0 || m.playlistGaps != 0 || m.avSyncDrift != 0 {
			t.Fatalf("unexpected artifacts: screen=%d gaps=%d avDrift=%v", m.screenFrames, m.playlistGaps, m.avSyncDrift)
		}
	})

	t.Run("channel-override-fastest-on-balanced-server", func(t *testing.T) {
		m := runSoakScenario(t, soakScript{
			name:             "channel-override-fastest-on-balanced-server",
			serverStability:  stabilityBalanced,
			channelStability: stabilityFastest,
			maxConnections:   2,
			startupBurst:     0,
			duration:         90 * time.Second,
		})
		if m.peakReserve > 1500*time.Millisecond {
			t.Fatalf("expected Fastest channel override on Balanced server not to slow playout, got peakReserve=%v", m.peakReserve)
		}
		if m.endLiveLag > 2*time.Second {
			t.Fatalf("expected Fastest channel override to remain at 1.0x live edge, got endLiveLag=%v", m.endLiveLag)
		}
	})

	t.Run("channel-override-balanced-on-fastest-server", func(t *testing.T) {
		m := runSoakScenario(t, soakScript{
			name:             "channel-override-balanced-on-fastest-server",
			serverStability:  stabilityFastest,
			channelStability: stabilityBalanced,
			maxConnections:   2,
			startupBurst:     2 * time.Second,
			duration:         340 * time.Second,
		})
		if m.peakReserve < 11500*time.Millisecond {
			t.Fatalf("expected Balanced channel override on Fastest server to build ~12s reserve, got peakReserve=%v", m.peakReserve)
		}
	})
}

func TestIPTVSoakParallelRaceRespectsLimitsAndTuneGap(t *testing.T) {
	m := runSoakScenario(t, soakScript{
		name:           "parallel-race-respects-tunegap",
		stability:      stabilityFastest,
		maxConnections: 2,
		tuneGap:        5 * time.Second,
		startupBurst:   500 * time.Millisecond,
		duration:       30 * time.Second,
		faults: []soakFault{
			{at: time.Second, kind: "pause", duration: 7 * time.Second},
		},
	})
	if m.maxConcurrentOpen != 2 {
		t.Fatalf("expected parallel race to open a 2nd concurrent connection within limit=2, got maxConcurrentOpen=%d", m.maxConcurrentOpen)
	}
	if m.providerOpens < 2 {
		t.Fatalf("expected at least 2 provider opens during parallel race, got %d", m.providerOpens)
	}
	if m.minOpenGap < 5*time.Second {
		t.Fatalf("expected tuneGap=5s to space provider opens by >= 5s (stall detected at ~3s, waited until 5s), got minOpenGap=%v", m.minOpenGap)
	}
	if m.screenFrames != 0 || m.playlistGaps != 0 {
		t.Fatalf("expected seamless parallel splice, got screenFrames=%d playlistGaps=%d", m.screenFrames, m.playlistGaps)
	}
}

func TestReservePacerPreservesPitchAndSync(t *testing.T) {
	format := transcode.RawFormat{
		Width:      64,
		Height:     36,
		FPSNum:     25,
		FPSDen:     1,
		SampleRate: 48000,
		Channels:   2,
	}
	pacer := newReservePacer(format)
	if pacer == nil {
		t.Fatal("expected non-nil reservePacer")
	}
	in := &broadcastInput{}

	const freqHz = 440.0
	const amp = 12000.0
	stepRad := 2 * math.Pi * freqHz / float64(format.SampleRate)
	var srcPhase float64
	var srcPulled int

	pullSource := func() (transcode.RawFrame, bool) {
		samples := 1920 // 48000 / 25
		f := blackFrame(format, samples)
		f.Video[0] = soakVideoLive
		for i := range samples {
			v := int16(math.Round(amp * math.Sin(srcPhase)))
			srcPhase += stepRad
			for ch := range format.Channels {
				off := (i*format.Channels + ch) * 2
				binary.LittleEndian.PutUint16(f.PCM[off:], uint16(v))
			}
		}
		f.SourcePTS = time.Duration(srcPulled) * (time.Second / 25)
		f.SourcePTSValid = true
		srcPulled++
		return f, true
	}

	var stretchedMono []int16
	var allMono []int16

	runTicks := func(ticks int, tempo float64, recordStretched bool) {
		for range ticks {
			out, _, ok := pacer.next(in, 1920, tempo, pullSource)
			if !ok || len(out.Video) == 0 {
				t.Fatalf("pacer.next failed at tempo=%v", tempo)
			}
			if len(out.PCM) != 1920*format.Channels*2 {
				t.Fatalf("expected %d PCM bytes, got %d", 1920*format.Channels*2, len(out.PCM))
			}
			for i := range 1920 {
				sL := int16(binary.LittleEndian.Uint16(out.PCM[i*4:]))
				sR := int16(binary.LittleEndian.Uint16(out.PCM[i*4+2:]))
				if sL != sR {
					t.Fatalf("stereo channel mismatch at sample %d: L=%d R=%d", i, sL, sR)
				}
				allMono = append(allMono, sL)
				if recordStretched {
					stretchedMono = append(stretchedMono, sL)
				}
			}
		}
	}

	// Phase 1: 150 ticks (6.0s) at steadyReserveTempo (0.965x).
	runTicks(150, steadyReserveTempo, true)
	pulledAfterStretch := srcPulled
	// 150 * 0.965 = 144.75 source frames (+1 lookahead frame).
	if pulledAfterStretch < 143 || pulledAfterStretch > 147 {
		t.Fatalf("expected ~145 source frames pulled over 150 stretched ticks, got %d", pulledAfterStretch)
	}

	// Phase 2: 100 ticks (4.0s) at 1.0x once reserve target is reached.
	runTicks(100, 1.0, false)
	pulledAt1x := srcPulled - pulledAfterStretch
	if pulledAt1x < 99 || pulledAt1x > 101 {
		t.Fatalf("expected ~100 source frames pulled over 100 1.0x ticks, got %d", pulledAt1x)
	}

	// Phase 3: 100 ticks (4.0s) back at steadyReserveTempo (0.965x) simulating post-stall rebuild.
	beforeRebuild := srcPulled
	runTicks(100, steadyReserveTempo, true)
	pulledRebuild := srcPulled - beforeRebuild
	if pulledRebuild < 95 || pulledRebuild > 98 {
		t.Fatalf("expected ~96-97 source frames pulled over 100 rebuild ticks, got %d", pulledRebuild)
	}

	// Verify pitch preservation on stretched audio (must stay ~440 Hz, NOT 440*0.965 = 424.6 Hz).
	var crossings int
	firstCross, lastCross := -1, -1
	for i := 1; i < len(stretchedMono); i++ {
		if stretchedMono[i-1] < 0 && stretchedMono[i] >= 0 {
			if firstCross < 0 {
				firstCross = i
			}
			lastCross = i
			crossings++
		}
	}
	periods := crossings - 1
	if periods < 100 {
		t.Fatalf("too few zero-crossing periods: %d", periods)
	}
	measuredHz := float64(periods) * float64(format.SampleRate) / float64(lastCross-firstCross)
	if math.Abs(measuredHz-freqHz) > 2.0 {
		t.Fatalf("WSOLA did not preserve pitch: expected %.1f Hz, got %.2f Hz", freqHz, measuredHz)
	}

	// Verify click-free waveform continuity across all crossfades and 0.965x <-> 1.0x transitions.
	// Analytic max step of 12000*sin(2*pi*440*n/48000) is ~691; disallow step clicks.
	var maxStep int
	for i := 1; i < len(allMono); i++ {
		d := int(allMono[i]) - int(allMono[i-1])
		if d < 0 {
			d = -d
		}
		if d > maxStep {
			maxStep = d
		}
	}
	if maxStep > 1100 {
		t.Fatalf("detected waveform discontinuity/click across WSOLA grains: maxStep=%d", maxStep)
	}
}
