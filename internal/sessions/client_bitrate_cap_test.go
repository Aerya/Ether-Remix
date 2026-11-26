package sessions

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// capTranscoder decides with the real decision engine and records every
// encoder start, so planning can be shown to start none.
type capTranscoder struct {
	*fakeMediaTranscoder
	starts int
}

func (*capTranscoder) ProactiveFallbackAvailable() bool { return true }
func (t *capTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	return transcode.Decide(info, caps, opts, transcode.Policy{})
}
func (t *capTranscoder) StartHLS(ctx context.Context, r core.HLSRequest) (core.HLSSession, error) {
	t.starts++
	return t.fakeMediaTranscoder.StartHLS(ctx, r)
}

var capUHD = core.MediaInfo{
	Container:   "mkv",
	Size:        49_931_122_868,
	Duration:    2*time.Hour + 18*time.Minute,
	BitrateKbps: 48_000,
	Streams: []core.StreamInfo{
		{Type: "video", Index: 0, Codec: "hevc", Width: 3840, Height: 2160},
		{Type: "audio", Index: 1, Codec: "aac", Channels: 2, BitrateKbps: 192},
	},
}

var capHDMP4 = core.MediaInfo{
	Container:   "mp4",
	Size:        2_700_000_000,
	Duration:    2 * time.Hour,
	BitrateKbps: 3_000,
	Streams: []core.StreamInfo{
		{Type: "video", Index: 0, Codec: "h264", Width: 1920, Height: 1080},
		{Type: "audio", Index: 1, Codec: "aac", Channels: 2, BitrateKbps: 192},
	},
}

func capBrokerWithMedia(t *testing.T, info core.MediaInfo, ownerKbps int, videoOff bool) (*Broker, *capTranscoder) {
	t.Helper()
	pb := &sourceTrackPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}, source: &revisionSource{Reader: bytes.NewReader(nil), id: "release", name: "video." + info.Container, size: info.Size}, info: info}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	bus := NewEventBus(nil, active)
	t.Cleanup(bus.Close)
	lookup := func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: key}, nil
	}
	tc := &capTranscoder{fakeMediaTranscoder: &fakeMediaTranscoder{sessions: map[string]*fakeMediaSession{}}}
	b, err := NewBroker(BrokerDeps{
		Transcoder:          tc,
		SurfacePolicy:       testSessionPolicy,
		Events:              bus,
		Playback:            pb,
		Progress:            &fakeProgress{},
		ActiveViewer:        active,
		Lookup:              lookup,
		MediaLookup:         lookup,
		CanPlay:             func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:        func(*core.Viewer) int { return ownerKbps },
		VideoTranscodingOff: func() bool { return videoOff },
		OnTimeline:          func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(context.Background()) })
	return b, tc
}

func capBroker(t *testing.T, ownerKbps int) (*Broker, *capTranscoder) {
	return capBrokerWithMedia(t, capUHD, ownerKbps, false)
}

// capRequest is what the Vela frontend sends for client.maxBitrate (bits/s)
// with maxWidth/maxHeight.
func capRequest(maxBitrate, maxWidth, maxHeight int) frontend.PlanRequest {
	kbps := maxBitrate / 1000
	return frontend.PlanRequest{
		Protocol: "vela",
		Viewer:   &core.Viewer{ID: 1},
		Device:   core.Device{ID: "phone"},
		Item:     core.MovieKey(1),
		Caps:     core.ClientCaps{Product: "Vela", Platform: "ios", Containers: []string{"mp4"}, HLSContainers: []string{"fmp4"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac"}, MaxHeight: maxHeight, MaxBitrateKbps: kbps},
		Intent: frontend.PlayIntent{
			DeclaredCaps:     true,
			OriginalPlayback: &frontend.OriginalPlaybackCapabilities{Version: 1, Range: true, DemuxContainers: []string{"mkv", "mp4"}, VideoDecoders: []string{"h264", "hevc"}, AudioDecoders: []string{"aac"}},
			Options:          core.DecideOptions{DirectPlay: true, DirectStream: true, MaxWidth: maxWidth, MaxHeight: maxHeight, MaxBitrateKbps: kbps, AudioStream: -1, SubtitleStream: -1},
		},
	}
}

// Contract 1: an original above the client's maxBitrate (and/or outside its
// maxWidth/maxHeight box when maxBitrate is present) is never the selected
// path; a converted path within the cap is, the original stays an explicit
// choice, and planning starts no encoder.
func TestClientBitrateCapSelectsConvertedAndKeepsOriginalChoice(t *testing.T) {
	// Typical Vela quality preset: "1080p 8 Mbps" on a 4K 48 Mbps source.
	b, tc := capBroker(t, 0)
	plan, err := b.Plan(context.Background(), capRequest(8_000_000, 1920, 1080))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision.Kind != core.DecisionTranscode || plan.Decision.Reason == "original_runtime" {
		t.Fatalf("selected path is not converted: %+v", plan.Decision)
	}
	if plan.Decision.BitrateKbps <= 0 || plan.Decision.BitrateKbps > 8_000 {
		t.Fatalf("converted path above the cap: %d kbps", plan.Decision.BitrateKbps)
	}
	if plan.Decision.MaxHeight > 1080 {
		t.Fatalf("converted path exceeds cap height: %d", plan.Decision.MaxHeight)
	}
	if plan.OriginalOverClientCap == nil || plan.OriginalOverClientCap.Reason != "original_runtime" {
		t.Fatalf("original not kept as a choice when both bitrate and box exceeded: %+v", plan.OriginalOverClientCap)
	}
	if tc.starts != 0 {
		t.Fatalf("planning started %d encoder(s)", tc.starts)
	}

	// Nothing else can play (transcoding refused): the original stays
	// selected instead of failing, even when both bitrate and box exceeded.
	b, _ = capBroker(t, 0)
	req := capRequest(8_000_000, 1920, 1080)
	no := false
	req.Intent.AllowTranscoding = &no
	plan, err = b.Plan(context.Background(), req)
	if err != nil || plan.Decision.Reason != "original_runtime" || plan.OriginalOverClientCap != nil {
		t.Fatalf("original did not stay selected with AllowTranscoding=false: %+v %v", plan.Decision, err)
	}

	// Server-wide video transcoding off: original still stays selected rather
	// than falling back to an unconverted HLS remux or failing.
	b, _ = capBrokerWithMedia(t, capUHD, 0, true)
	plan, err = b.Plan(context.Background(), capRequest(8_000_000, 1920, 1080))
	if err != nil || plan.Decision.Reason != "original_runtime" || plan.OriginalOverClientCap != nil {
		t.Fatalf("original did not stay selected with video transcoding off: %+v %v", plan.Decision, err)
	}

	// Bitrate under cap, but outside the width/height box (with maxBitrate
	// present): converted path within 720p is selected, plain direct-play MP4
	// original is kept as an explicit choice, and stays selected if transcoding
	// is disabled.
	b, tc = capBrokerWithMedia(t, capHDMP4, 0, false)
	boxReq := capRequest(4_000_000, 1280, 720)
	boxReq.Intent.OriginalPlayback = nil
	plan, err = b.Plan(context.Background(), boxReq)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision.Kind != core.DecisionTranscode || plan.Decision.MaxHeight > 720 || plan.Decision.BitrateKbps > 4_000 {
		t.Fatalf("box-capped path not converted within 720p/4M: %+v", plan.Decision)
	}
	if plan.OriginalOverClientCap == nil || plan.OriginalOverClientCap.Kind != core.DecisionDirectPlay {
		t.Fatalf("plain direct-play original not kept as choice: %+v", plan.OriginalOverClientCap)
	}
	if tc.starts != 0 {
		t.Fatalf("planning started %d encoder(s)", tc.starts)
	}
	boxReqNoTC := boxReq
	boxReqNoTC.Intent.AllowTranscoding = &no
	plan, err = b.Plan(context.Background(), boxReqNoTC)
	if err != nil || plan.Decision.Kind != core.DecisionDirectPlay || plan.OriginalOverClientCap != nil {
		t.Fatalf("plain direct-play original did not stay selected: %+v %v", plan.Decision, err)
	}

	// Only the client's cap is lifted: an owner bitrate cap the original also
	// exceeds keeps it out of the choices and out of fallback.
	b, _ = capBroker(t, 8_000)
	plan, err = b.Plan(context.Background(), capRequest(8_000_000, 1920, 1080))
	if err != nil || plan.OriginalOverClientCap != nil {
		t.Fatalf("owner bitrate cap lifted: %+v %v", plan.OriginalOverClientCap, err)
	}
	ownerNoTC := capRequest(8_000_000, 1920, 1080)
	ownerNoTC.Intent.AllowTranscoding = &no
	if _, err := b.Plan(context.Background(), ownerNoTC); err == nil {
		t.Fatal("owner bitrate cap bypassed when transcoding disabled")
	}

	// An owner height cap (e.g., app compatibility override / quality rule)
	// also stays enforced when the client's streaming cap is lifted.
	b, _ = capBroker(t, 0)
	ownerHeightReq := capRequest(8_000_000, 1920, 1080)
	ownerHeightReq.Caps.MaxHeight = 720
	ownerHeightReq.Caps.MaxHeightSource = "App compatibility override"
	plan, err = b.Plan(context.Background(), ownerHeightReq)
	if err != nil || plan.OriginalOverClientCap != nil {
		t.Fatalf("owner height cap lifted: %+v %v", plan.OriginalOverClientCap, err)
	}
	ownerHeightReq.Intent.AllowTranscoding = &no
	if _, err := b.Plan(context.Background(), ownerHeightReq); err == nil {
		t.Fatal("owner height cap bypassed when transcoding disabled")
	}
}

// Contract 1 for plugin VOD (planKind): mirrors catalogue VOD behaviour.
func TestOriginalChoiceKeepsTitleRuleAboveClientPreset(t *testing.T) {
	b, _ := capBroker(t, 0)
	b.deps.ApplyRuleLimits = func(_ context.Context, caps core.ClientCaps, _ *core.Viewer, title *core.Title) core.ClientCaps {
		if title != nil {
			caps.MaxBitrateKbps = stricterLimit(caps.MaxBitrateKbps, 16000)
		}
		return caps
	}
	req := capRequest(8_000_000, 1920, 1080)
	plan, err := b.Plan(context.Background(), req)
	if err != nil || plan.OriginalOverClientCap != nil || plan.Decision.Kind != core.DecisionTranscode {
		t.Fatalf("expected a converted path within the rule: %+v, %v", plan.Decision, err)
	}
}

func TestClientBitrateCapPluginKindVOD(t *testing.T) {
	ctx := context.Background()
	key := core.PluginKey(42)
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	bus := NewEventBus(nil, active)
	t.Cleanup(bus.Close)
	pb := &kindPlayback{fakePlayback: fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}, info: capUHD}
	tc := &capTranscoder{fakeMediaTranscoder: &fakeMediaTranscoder{sessions: map[string]*fakeMediaSession{}}}
	b, err := NewBroker(BrokerDeps{
		Transcoder:    tc,
		SurfacePolicy: testSessionPolicy,
		Events:        bus,
		Playback:      pb,
		Progress:      &fakeProgress{},
		ActiveViewer:  active,
		CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:  func(*core.Viewer) int { return 0 },
		OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Lookup: func(context.Context, *core.Viewer, core.Device, core.RatingKey) (*core.Title, error) {
			return nil, core.ErrNotFound
		},
		KindSource: func(_ context.Context, _ *core.Viewer, k core.RatingKey) (*core.KindInput, error) {
			return &core.KindInput{Title: &core.Title{Key: k, Type: core.TypeMovie, Title: "Plugin UHD"}, SourceID: "uhd-1"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(ctx) })

	req := capRequest(8_000_000, 1920, 1080)
	req.Item = key
	req.Intent.OriginalPlayback = nil // test plain direct-play fallback on mp4 too
	pb.info = capHDMP4
	req = capRequest(4_000_000, 1280, 720)
	req.Item = key
	req.Intent.OriginalPlayback = nil

	plan, err := b.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision.Kind != core.DecisionTranscode || plan.Decision.MaxHeight > 720 || plan.Decision.BitrateKbps > 4_000 {
		t.Fatalf("plugin VOD not converted within cap: %+v", plan.Decision)
	}
	if plan.OriginalOverClientCap == nil || plan.OriginalOverClientCap.Kind != core.DecisionDirectPlay {
		t.Fatalf("plugin VOD did not keep original choice: %+v", plan.OriginalOverClientCap)
	}
	no := false
	req.Intent.AllowTranscoding = &no
	plan, err = b.Plan(ctx, req)
	if err != nil || plan.Decision.Kind != core.DecisionDirectPlay || plan.OriginalOverClientCap != nil {
		t.Fatalf("plugin VOD did not fall back to original when transcoding disabled: %+v %v", plan.Decision, err)
	}
}

// Contract 2: the Vela HLS ladder stays within the cap; rungs above it are
// reported as qualityLimitations with reason bitrate_limit or resolution_limit.
func TestClientBitrateCapBoundsHLSLadder(t *testing.T) {
	info := core.MediaInfo{Container: "mkv", Duration: time.Hour, BitrateKbps: 30_000, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}, {Index: 1, Type: "audio", Codec: "aac", Channels: 2, BitrateKbps: 192}}}
	playback := &fakePlayback{sessions: map[string]*core.PlaySession{"source": {ID: "source", Info: info}}}
	tc := &capTranscoder{fakeMediaTranscoder: &fakeMediaTranscoder{sessions: map[string]*fakeMediaSession{}}}
	b := &Broker{deps: BrokerDeps{Playback: playback, Transcoder: tc, Now: time.Now, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k}, nil
	}}}
	req := capRequest(5_000_000, 1280, 720)
	dec, err := b.decideProfile(&core.PlaySession{Info: info}, req)
	if err != nil || dec.Kind != core.DecisionTranscode || dec.BitrateKbps > 5_000 || dec.MaxHeight > 720 {
		t.Fatalf("initial rendition above the cap: %+v %v", dec, err)
	}
	s := &brokerSession{public: frontend.Session{Protocol: "vela", ViewerID: 1, Item: core.MovieKey(1)}, playID: "source", caps: req.Caps, opts: req.Intent.Options, decision: dec}
	variants, limits, err := b.qualityPlan(context.Background(), s, &core.Viewer{ID: 1})
	if err != nil || len(variants) == 0 {
		t.Fatal(variants, limits, err)
	}
	for _, v := range variants {
		if v.Rung.Width > 1280 || v.Rung.Height > 720 || v.Rung.Bandwidth > 5_000_000 || v.Rung.AverageBandwidth > 5_000_000 {
			t.Fatalf("rung above the cap: %+v", v.Rung)
		}
	}
	var sawBitrate, sawRes bool
	for _, l := range limits {
		sawBitrate = sawBitrate || l.Reason == "bitrate_limit"
		sawRes = sawRes || l.Reason == "resolution_limit"
	}
	if !sawBitrate && !sawRes {
		t.Fatalf("higher rungs not marked: %+v", limits)
	}
	if tc.starts != 0 {
		t.Fatal("ladder planning started an encoder")
	}
}

type liveCapCapture struct {
	tunedCaps core.ClientCaps
}

func (l *liveCapCapture) Allowed(context.Context, *core.Viewer, string) error { return nil }
func (l *liveCapCapture) Tune(_ context.Context, _ *core.Viewer, _ string, caps core.ClientCaps, _ string) (string, error) {
	l.tunedCaps = caps
	return "live-1", nil
}
func (*liveCapCapture) Serve(http.ResponseWriter, *http.Request, *core.Viewer, string, string, string) {
}
func (*liveCapCapture) Stop(context.Context, *core.Viewer, string) error { return nil }

// Contract 3: a live start from Vela never lets an owner quality rule with a
// higher MaxHeight raise Caps above Vela's declared maxHeight or maxBitrate.
func TestClientCapLiveStartTightensCapsBelowOwnerRule(t *testing.T) {
	ctx := context.Background()
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	bus := NewEventBus(nil, active)
	t.Cleanup(bus.Close)
	live := &liveCapCapture{}
	b, err := NewBroker(BrokerDeps{
		SurfacePolicy: testSessionPolicy,
		Events:        bus,
		Playback:      &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4},
		Progress:      &fakeProgress{},
		Lookup: func(context.Context, *core.Viewer, core.Device, core.RatingKey) (*core.Title, error) {
			return nil, core.ErrNotFound
		},
		Live:         live,
		ActiveViewer: active,
		CanPlay:      func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit: func(*core.Viewer) int { return 0 },
		OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(ctx) })

	// Simulate resolver.ApplyClientRules having lifted Caps.MaxHeight to 1080
	// while Vela declared maxHeight=720 and maxBitrate=3_000_000.
	plan, err := b.Plan(ctx, frontend.PlanRequest{
		Protocol: "vela",
		Viewer:   &core.Viewer{ID: 1},
		Device:   core.Device{ID: "phone"},
		Channel:  "ch1",
		Caps:     core.ClientCaps{Product: "Vela", Platform: "ios", HLSContainers: []string{"fmp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, MaxHeight: 1080, MaxHeightSource: "client quality rule (maxHeight=1080p)", MaxBitrateKbps: 3_000},
		Intent:   frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, MaxWidth: 1280, MaxHeight: 720, MaxBitrateKbps: 3_000, AudioStream: -1, SubtitleStream: -1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(ctx, frontend.OpenRequest{Protocol: "vela", PlanID: plan.ID, Viewer: &core.Viewer{ID: 1}, Device: core.Device{ID: "phone"}}); err != nil {
		t.Fatal(err)
	}
	if live.tunedCaps.MaxHeight != 720 || live.tunedCaps.MaxBitrateKbps != 3_000 {
		t.Fatalf("live tune did not preserve Vela client cap: height=%d kbps=%d", live.tunedCaps.MaxHeight, live.tunedCaps.MaxBitrateKbps)
	}
}

// Contract 4: maxWidth/maxHeight without maxBitrate are decoder limits, not a
// viewer cap: no original choice and no original fallback, as before.
func TestClientBoxWithoutBitrateIsNotACap(t *testing.T) {
	b, _ := capBroker(t, 0)
	plan, err := b.Plan(context.Background(), capRequest(0, 1920, 1080))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision.Reason == "original_runtime" || plan.OriginalOverClientCap != nil {
		t.Fatalf("decoder box treated as a cap: %+v %+v", plan.Decision, plan.OriginalOverClientCap)
	}
	b, _ = capBroker(t, 0)
	req := capRequest(0, 1920, 1080)
	no := false
	req.Intent.AllowTranscoding = &no
	_, err = b.Plan(context.Background(), req)
	var noPath *frontend.NoCompatiblePathError
	if !errors.As(err, &noPath) {
		t.Fatalf("decoder limit bypassed: %v", err)
	}
}
