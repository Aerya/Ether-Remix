package sessions

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type originalAudioTranscoder struct {
	*fakeMediaTranscoder
	requests []core.HLSRequest
}

func (t *originalAudioTranscoder) AudioFallbackAvailable() bool { return true }
func (t *originalAudioTranscoder) StartHLS(ctx context.Context, r core.HLSRequest) (core.HLSSession, error) {
	t.requests = append(t.requests, r)
	return t.fakeMediaTranscoder.StartHLS(ctx, r)
}

func TestOriginalPlaybackNegotiationAndGeneration(t *testing.T) {
	runtime := &frontend.OriginalPlaybackCapabilities{Version: 1, Range: true, DemuxContainers: []string{"mkv"}, VideoDecoders: []string{"hevc"}, AudioDecoders: []string{"truehd"}, WebGPU: true}
	info := core.MediaInfo{Container: "mkv", Size: 100, Streams: []core.StreamInfo{{Type: "video", Index: 0, Codec: "hevc", Height: 2160}, {Type: "audio", Index: 4, Codec: "truehd"}}}
	b := &Broker{}
	request := frontend.PlanRequest{Protocol: "vela", Caps: core.ClientCaps{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, MaxHeight: 480, MaxBitrateKbps: 100}, Intent: frontend.PlayIntent{OriginalPlayback: runtime, Options: core.DecideOptions{DirectPlay: true, AudioStream: 4, SubtitleStream: -1}}}
	source := &core.PlaySession{Info: info}
	d, err := b.decideProfile(source, request)
	if err != nil || d.Kind != core.DecisionDirectPlay || d.Reason != "original_runtime" || d.MaxHeight != 0 || d.BitrateKbps != 0 || d.ToneMap {
		t.Fatalf("original budget altered: %+v %v", d, err)
	}
	// Original decoder admission uses observed codecs and the selected/default
	// track. HEVC Main10 plus DTS remains playable with conversion disabled,
	// even when an unselected commentary track uses another codec.
	for _, selected := range []int{-1, 4} {
		req := request
		c := *runtime
		c.AudioDecoders = []string{"dts"}
		req.Intent.OriginalPlayback = &c
		req.Intent.Options.AudioStream = selected
		no := false
		req.Intent.AllowTranscoding = &no
		media := info
		media.Streams = []core.StreamInfo{{Type: "video", Index: 0, Codec: "hevc", Profile: "Main 10", BitDepth: 10}, {Type: "audio", Index: 4, Codec: "dts"}, {Type: "audio", Index: 5, Codec: "aac"}}
		d, err := b.decideProfile(&core.PlaySession{Info: media}, req)
		if err != nil || d.Reason != "original_runtime" || d.AudioStream != 4 || d.AudioCodec != "dts" {
			t.Fatalf("original DTS admission: %+v %v", d, err)
		}
	}
	for _, change := range []func(*frontend.PlanRequest){
		func(r *frontend.PlanRequest) { r.Intent.OriginalPlayback = nil },
		func(r *frontend.PlanRequest) { c := *runtime; c.Version = 2; r.Intent.OriginalPlayback = &c },
		func(r *frontend.PlanRequest) { c := *runtime; c.Range = false; r.Intent.OriginalPlayback = &c },
		func(r *frontend.PlanRequest) {
			c := *runtime
			c.AudioDecoders = []string{"aac"}
			r.Intent.OriginalPlayback = &c
		},
		func(r *frontend.PlanRequest) { r.Channel = "live" },
		func(r *frontend.PlanRequest) { r.Protocol = "plex" },
	} {
		req := request
		change(&req)
		d, _ := b.decideProfile(source, req)
		if d.Reason == "original_runtime" {
			t.Fatalf("unnegotiated original: %+v", req)
		}
	}
	ctx := context.Background()
	pb := &sourceTrackPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 2}, source: &revisionSource{Reader: bytes.NewReader(nil), id: "release", name: "video.mkv", size: 100}, info: info}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	bus := NewEventBus(nil, active)
	defer bus.Close()
	lookup := func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: key}, nil
	}
	tc := &originalAudioTranscoder{fakeMediaTranscoder: &fakeMediaTranscoder{sessions: map[string]*fakeMediaSession{}}}
	gated := &Broker{deps: BrokerDeps{Transcoder: tc}}
	candidate := request
	audioRuntime := *runtime
	audioRuntime.AudioDecoders = []string{"aac"}
	candidate.Intent.OriginalPlayback = &audioRuntime
	if d, err := gated.decideProfile(source, candidate); err != nil || d.Reason != "original_runtime" {
		t.Fatalf("real audio fallback not admitted: %+v %v", d, err)
	}
	audioRuntime.AudioDecoders = []string{"opus"}
	if d, _ := gated.decideProfile(source, candidate); d.Reason == "original_runtime" {
		t.Fatal("AAC fallback admitted without installed AAC decoder")
	}
	broker, err := NewBroker(BrokerDeps{Transcoder: tc, SurfacePolicy: testSessionPolicy, Events: bus, Playback: pb, Progress: &fakeProgress{}, ActiveViewer: active, Lookup: lookup, MediaLookup: lookup, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close(ctx)
	viewer, device := &core.Viewer{ID: 1}, core.Device{ID: "runtime"}
	request.Viewer, request.Device, request.Item = viewer, device, core.MovieKey(1)
	// A rejected resolved source must release its pin and its owned session.
	t.Run("rejected source lease", func(t *testing.T) {
		rejected := request
		rejected.Intent.OriginalPlayback = nil
		rejected.Caps.Profiles = &core.ClientCapabilityProfiles{DirectPlay: []core.ClientDirectPlayProfile{}, Transcoding: []core.ClientTranscodeProfile{}}
		_, err := broker.Plan(ctx, rejected)
		var noPath *frontend.NoCompatiblePathError
		if !errors.As(err, &noPath) || len(noPath.RejectedPaths) == 0 {
			t.Fatalf("untyped rejection: %v", err)
		}
		if len(broker.plans) != 0 || len(broker.sessions) != 0 || len(broker.sourceLeases) != 0 || len(pb.sessions) != 0 || pb.pins != 0 {
			t.Fatal("rejected source lingered")
		}
	})
	plan, err := broker.Plan(ctx, request)
	if err != nil || pb.pins != 1 || plan.ETag == "" {
		t.Fatalf("plan %+v pins %d %v", plan, pb.pins, err)
	}
	session, err := broker.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "vela", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	access := frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "vela"}
	path, err := broker.StartStream(ctx, access, session.ID, frontend.StreamRequest{Format: frontend.StreamDirect})
	if err != nil {
		t.Fatal(err)
	}
	// Audio is a separate rendition: no video encode, unchanged direct URL,
	// independently cancelable/replaced generations, and the same source pin.
	audio, err := broker.StartAudioFallback(ctx, access, session.ID, 4, 33*time.Second)
	if err != nil || audio.Generation != 1 || len(tc.requests) != 1 {
		t.Fatalf("audio %+v %v", audio, err)
	}
	req := tc.requests[0]
	if len(req.Info.Streams) != 1 || req.Info.Streams[0].Type != "audio" || req.Decision.AudioStream != 4 || !req.Decision.VideoCopy || req.Decision.AudioCopy || req.StartOffset != 33*time.Second || req.SegmentDuration != transcode.AudioFallbackSegmentDuration {
		t.Fatalf("audio crossed video/track boundary: %+v", req)
	}
	currentPath, err := broker.StartStream(ctx, access, session.ID, frontend.StreamRequest{Format: frontend.StreamDirect})
	if err != nil || currentPath != path || len(tc.sessions) != 1 {
		t.Fatal("audio changed direct video", err)
	}
	replacement, err := broker.StartAudioFallback(ctx, access, session.ID, 4, 60*time.Second)
	if err != nil || replacement.Generation != 2 || replacement.ID == audio.ID || len(tc.sessions) != 1 {
		t.Fatalf("replacement %+v %v", replacement, err)
	}
	if err = broker.StopAudioFallback(ctx, access, session.ID, audio.ID); err != nil || len(tc.sessions) != 1 {
		t.Fatal("stale stop canceled replacement")
	}
	if err = broker.StopAudioFallback(ctx, access, session.ID, replacement.ID); err != nil || len(tc.sessions) != 0 || pb.pins != 1 {
		t.Fatal("audio stop released video/source pin", err)
	}
	pb.source.id = "replacement"
	out := httptest.NewRecorder()
	broker.ServeMediaFor("vela", out, httptest.NewRequest(http.MethodGet, path, nil))
	if out.Code == 200 || out.Code == 206 {
		t.Fatal("mixed bytes from changed source", out.Code)
	}
	if _, err = broker.StartStream(ctx, access, session.ID, frontend.StreamRequest{Format: frontend.StreamDirect}); !errors.Is(err, frontend.ErrSourceGone) {
		t.Fatalf("reopened replacement: %v", err)
	}
}

func TestOriginalPlaybackRespectsClientAndOwnerBudgets(t *testing.T) {
	runtime := &frontend.OriginalPlaybackCapabilities{
		Version:         1,
		Range:           true,
		DemuxContainers: []string{"mkv", "mp4"},
		VideoDecoders:   []string{"h264", "hevc"},
		AudioDecoders:   []string{"aac", "truehd"},
	}
	uhdInfo := core.MediaInfo{
		Container:   "mkv",
		Size:        49_931_122_868,
		Duration:    2*time.Hour + 18*time.Minute,
		BitrateKbps: 48_000,
		Streams: []core.StreamInfo{
			{Type: "video", Index: 0, Codec: "hevc", Width: 3840, Height: 2160, BitDepth: 10, ColorTransfer: "smpte2084"},
			{Type: "audio", Index: 1, Codec: "truehd", Channels: 8},
		},
	}
	hdInfo := core.MediaInfo{
		Container:   "mkv",
		Size:        4_000_000_000,
		Duration:    2 * time.Hour,
		BitrateKbps: 4_500,
		Streams: []core.StreamInfo{
			{Type: "video", Index: 0, Codec: "hevc", Width: 1920, Height: 1080},
			{Type: "audio", Index: 1, Codec: "aac", Channels: 2},
		},
	}
	b := &Broker{deps: BrokerDeps{RemoteBitrateLimit: func() int { return 12_000 }}}
	baseReq := frontend.PlanRequest{
		Protocol: "vela",
		Caps: core.ClientCaps{
			Platform:    "Android",
			Product:     "Vela",
			Containers:  []string{"mp4"},
			VideoCodecs: []string{"h264", "hevc"},
			AudioCodecs: []string{"aac"},
		},
		Intent: frontend.PlayIntent{
			OriginalPlayback: runtime,
			Options:          core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1},
		},
	}

	// Within client's 1080p hardware ceiling: original_runtime is admitted.
	okReq := baseReq
	okReq.Intent.Options.MaxWidth = 1920
	okReq.Intent.Options.MaxHeight = 1080
	if d, err := b.decideProfile(&core.PlaySession{Info: hdInfo}, okReq); err != nil || d.Reason != "original_runtime" {
		t.Fatalf("1080p within 1080p ceiling = (%+v, %v), want original_runtime", d, err)
	}

	rejectCases := []struct {
		name   string
		mutate func(*frontend.PlanRequest)
	}{
		{"client maxHeight 1080 rejects 2160p", func(r *frontend.PlanRequest) { r.Intent.Options.MaxHeight = 1080 }},
		{"client maxWidth 1920 rejects 3840px", func(r *frontend.PlanRequest) { r.Intent.Options.MaxWidth = 1920 }},
		{"client maxBitrate rejects 48 Mbps", func(r *frontend.PlanRequest) { r.Intent.Options.MaxBitrateKbps = 15_000 }},
		{"per-codec video maxHeight rejects 2160p hevc", func(r *frontend.PlanRequest) { r.Caps.VideoMaxHeights = map[string]int{"hevc": 1080} }},
		{"owner AlwaysEncodeVideo rejects original_runtime", func(r *frontend.PlanRequest) { r.Caps.AlwaysEncodeVideo = true }},
		{"owner app compatibility MaxHeight override rejects 2160p", func(r *frontend.PlanRequest) {
			r.Caps.MaxHeight = 1080
			r.Caps.MaxHeightSource = "App compatibility override"
		}},
		{"remote client respects RemoteBitrateLimit", func(r *frontend.PlanRequest) { r.Caps.Remote = true }},
	}
	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseReq
			tc.mutate(&req)
			d, _ := b.decideProfile(&core.PlaySession{Info: uhdInfo}, req)
			if d.Reason == "original_runtime" {
				t.Fatalf("expected original_runtime rejection, got %+v", d)
			}
		})
	}
}
