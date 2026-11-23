package sessions

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type qualityTranscoder struct{ fakeMediaTranscoder }

func (*qualityTranscoder) ProactiveFallbackAvailable() bool { return true }

func TestQualityPolicyInvariants(t *testing.T) {
	info := core.MediaInfo{Container: "mp4", Duration: time.Hour, BitrateKbps: 15000, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}, {Index: 1, Type: "audio", Codec: "aac", BitrateKbps: 192}}}
	playback := &fakePlayback{sessions: map[string]*core.PlaySession{"source": {ID: "source", Info: info}}}
	b := &Broker{deps: BrokerDeps{Playback: playback, Transcoder: &qualityTranscoder{}, Now: time.Now, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k}, nil
	}}}
	s := &brokerSession{public: frontend.Session{Protocol: "vela", ViewerID: 1, Item: core.MovieKey(1)}, playID: "source", caps: core.ClientCaps{VideoCodecs: []string{"h264"}, MaxHeight: 720, MaxBitrateKbps: 2000}, opts: core.DecideOptions{DirectPlay: true, DirectStream: true}, decision: core.Decision{Kind: core.DecisionRemux, Container: "fmp4", VideoCopy: true, AudioCopy: true, AudioStream: 1}}
	viewer := &core.Viewer{ID: 1}
	rungs, err := b.qualityRungs(context.Background(), s, viewer)
	if err != nil || len(rungs) == 0 {
		t.Fatal(rungs, err)
	}
	for _, r := range rungs {
		if r.Height > 720 || r.Bandwidth > 2000000 {
			t.Fatal("session caps escaped", r)
		}
	}
	denied := false
	s.allowTranscoding = &denied
	if r, err := b.qualityRungs(context.Background(), s, viewer); err != nil || len(r) != 0 {
		t.Fatal("allowTranscode=false", r, err)
	}
	s.allowTranscoding = nil
	for _, protocol := range []frontend.Protocol{"plex", "jellyfin"} {
		s.public.Protocol = protocol
		if r, err := b.qualityRungs(context.Background(), s, viewer); err != nil || len(r) != 0 {
			t.Fatal("changed another surface", r, err)
		}
	}
	s.public.Protocol = "vela"
	s.live = true
	if r, err := b.qualityRungs(context.Background(), s, viewer); err != nil || len(r) != 0 {
		t.Fatal("file ladder on live", r, err)
	}
	if playback.prepares != 0 {
		t.Fatal("quality planning resolved a source")
	}
}

type qualityProfileTranscoder struct{ profileTranscoder }

func (qualityProfileTranscoder) ProactiveFallbackAvailable() bool { return true }

// TestQualityPlanDirectPlayReDecidesForHLSLadder locks the fix for a plain
// direct-play decision (reason "") re-deciding for the HLS ladder with real
// client capability profiles, for both catalogue titles and plugin items.
// Without re-deciding, empty VideoCodec/AudioCodec fail profileDeliveryAllowed
// and would drop the ladder or emit a broken Height-0 limitation.
func TestQualityPlanDirectPlayReDecidesForHLSLadder(t *testing.T) {
	info := core.MediaInfo{
		Container:   "mp4",
		Duration:    time.Hour,
		BitrateKbps: 15000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Profile: "high", Level: 41},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, BitrateKbps: 192},
		},
	}
	caps := core.ClientCaps{
		Product:       "Vela",
		Platform:      "Web",
		Containers:    []string{"mp4"},
		HLSContainers: []string{"fmp4"},
		VideoCodecs:   []string{"h264"},
		AudioCodecs:   []string{"aac"},
		Profiles: &core.ClientCapabilityProfiles{
			DirectPlay: []core.ClientDirectPlayProfile{
				{MediaType: "video", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
			},
			Transcoding: []core.ClientTranscodeProfile{
				{MediaType: "video", Delivery: "hls", Containers: []string{"fmp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
			},
		},
	}
	opts := core.DecideOptions{DirectPlay: true, DirectStream: true}
	viewer := &core.Viewer{ID: 1}

	for _, tc := range []struct {
		name string
		key  core.RatingKey
	}{
		{name: "catalogue", key: core.MovieKey(1)},
		{name: "plugin", key: core.PluginKey(42)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			playback := &fakePlayback{sessions: map[string]*core.PlaySession{"source": {ID: "source", Info: info}}}
			tx := qualityProfileTranscoder{}
			b := &Broker{deps: BrokerDeps{
				Playback:   playback,
				Transcoder: tx,
				Now:        time.Now,
				Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
					return &core.Title{Key: k, Type: core.TypeMovie}, nil
				},
			}}
			// Exercise BOTH a plain DirectPlay decision (reason "", empty output
			// codecs — as produced when direct-play profiles short-circuit) and
			// the real transcode.Decide DirectPlay output.
			plainDec := core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, Container: "mp4", AudioStream: 1}
			decidedDec := tx.Decide(info, caps, opts)
			if decidedDec.Kind != core.DecisionDirectPlay {
				t.Fatalf("unexpected initial decision: %+v", decidedDec)
			}

			for _, d := range []core.Decision{plainDec, decidedDec} {
				s := &brokerSession{
					public:   frontend.Session{Protocol: "vela", ViewerID: viewer.ID, Item: tc.key},
					playID:   "source",
					caps:     caps,
					opts:     opts,
					decision: d,
				}

				variants, limits, err := b.qualityPlan(context.Background(), s, viewer)
				if err != nil {
					t.Fatal(err)
				}
				if len(variants) == 0 {
					t.Fatalf("direct-play qualityPlan (reason=%q) yielded no variants; limits=%+v", d.Reason, limits)
				}
				for _, v := range variants {
					if v.Rung.Height <= 0 {
						t.Fatalf("direct-play qualityPlan returned a Height-0 rung: %+v", v)
					}
				}
				for _, l := range limits {
					if l.Height <= 0 {
						t.Fatalf("direct-play qualityPlan emitted a Height-0 limitation: %+v", l)
					}
				}
				if ok, container := b.hlsPlanOutput(playback.sessions["source"], frontend.PlanRequest{
					Viewer: viewer, Protocol: "vela", Item: tc.key, Caps: caps, Intent: frontend.PlayIntent{Options: opts},
				}, d); !ok || container != "fmp4" {
					t.Fatalf("hlsPlanOutput (reason=%q) = (%v, %q), want (true, fmp4)", d.Reason, ok, container)
				}
			}
			if playback.prepares != 0 {
				t.Fatal("quality planning resolved a source")
			}

			s := &brokerSession{
				public:   frontend.Session{Protocol: "vela", ViewerID: viewer.ID, Item: tc.key},
				playID:   "source",
				caps:     caps,
				opts:     opts,
				decision: plainDec,
			}

			// Denied cases must never emit a Height-0 limitation.
			denied := false
			s.allowTranscoding = &denied
			if v, l, err := b.qualityPlan(context.Background(), s, viewer); err != nil || len(v) != 0 || len(l) != 0 {
				t.Fatalf("allowTranscoding=false: got (%v, %v, %v), want empty with no height-0 limitation", v, l, err)
			}
			s.allowTranscoding = nil
			noHLS := caps
			noHLS.Profiles = &core.ClientCapabilityProfiles{
				DirectPlay: caps.Profiles.DirectPlay,
			}
			s.caps = noHLS
			if v, l, err := b.qualityPlan(context.Background(), s, viewer); err != nil || len(v) != 0 || len(l) != 0 {
				t.Fatalf("incompatible HLS profile: got (%v, %v, %v), want empty with no height-0 limitation", v, l, err)
			}
		})
	}
}
