package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestAudioOnlyFLACOnVelaWebGetsHTTPAndHLSNot422(t *testing.T) {
	flacInfo := core.MediaInfo{
		Container:   "flac",
		Duration:    4 * time.Minute,
		Size:        30 << 20,
		BitrateKbps: 1000,
		Streams: []core.StreamInfo{{
			Index:       0,
			Type:        "audio",
			Codec:       "flac",
			Channels:    2,
			SampleRate:  44100,
			BitrateKbps: 1000,
			Default:     true,
		}},
	}

	// Vela Web declares aac + opus (no flac) for audio direct play and http/hls audio transcode.
	caps := core.ClientCaps{
		Product:              "Vela Web",
		Containers:           []string{"mp4", "mp3", "ogg", "webm"},
		AudioCodecs:          []string{"aac", "opus", "mp3"},
		TranscodeAudioCodecs: []string{"aac", "opus"},
		HLSContainers:        []string{"mp4"},
		Profiles: &core.ClientCapabilityProfiles{
			DirectPlay: []core.ClientDirectPlayProfile{
				{MediaType: "audio", Containers: []string{"mp4", "m4a", "mp3", "ogg", "webm"}, AudioCodecs: []string{"aac", "opus", "mp3"}},
			},
			Transcoding: []core.ClientTranscodeProfile{
				{MediaType: "audio", Delivery: "http", Containers: []string{"mp4", "aac", "mp3", "ogg"}, AudioCodecs: []string{"aac", "opus", "mp3"}},
				{MediaType: "audio", Delivery: "hls", Containers: []string{"mp4", "mpegts"}, AudioCodecs: []string{"aac"}},
			},
		},
	}

	source := &core.PlaySession{ID: "play-1", Info: flacInfo}
	pb := &fakePlayback{sessions: map[string]*core.PlaySession{"play-1": source}}
	b := &Broker{
		deps: BrokerDeps{
			SurfacePolicy: testSessionPolicy,
			Playback:      pb,
			Transcoder:    profileTranscoder{},
		},
		bindings: NewBindingTable[frontend.PlayIntent](),
	}
	req := frontend.PlanRequest{
		Caps: caps,
		Intent: frontend.PlayIntent{
			Options: core.DecideOptions{DirectPlay: true, DirectStream: true},
		},
	}

	// 1. decideProfile succeeds (does NOT fail with unsupported transport / 422).
	dec, err := b.decideProfile(source, req)
	if err != nil {
		t.Fatalf("decideProfile(FLAC on Vela Web) = %v, want valid transcode decision", err)
	}
	if dec.Kind != core.DecisionTranscode || dec.AudioCodec != "aac" || dec.VideoCodec != "" {
		t.Fatalf("decideProfile = %+v", dec)
	}

	// 2. Progressive HTTP audio transcode is admitted and uses audio AAC in mp4/fmp4 (.m4a).
	httpOK, httpContainer := b.httpPlanOutput(source, req, dec)
	if !httpOK || httpStreamExt(httpContainer, dec.AudioCodec) != "m4a" {
		t.Fatalf("httpPlanOutput = (%v, %q, ext=%q), want (true, mp4/fmp4, m4a)", httpOK, httpContainer, httpStreamExt(httpContainer, dec.AudioCodec))
	}

	// 3. On-demand HLS audio transcode is ALSO admitted (knownHLSStream allows probed audio-only).
	hlsOK, _ := b.hlsPlanOutput(source, req, dec)
	if !hlsOK {
		t.Fatal("hlsPlanOutput returned false for probed audio-only FLAC on Vela Web")
	}

	// 4. DirectPlay is NOT allowed for FLAC when client only supports aac/opus/mp3.
	if profileDirectAllowed(caps.Profiles, flacInfo, core.Decision{Kind: core.DecisionDirectPlay}) {
		t.Fatal("FLAC should not direct-play on aac/opus-only audio profile")
	}

	// 5. Master playlist for audio-only HLS uses decision bitrate for BANDWIDTH and emits valid M3U8.
	hs := &fakeMediaSession{}
	b.bindings.Put(&Binding[frontend.PlayIntent]{ID: "sess1", Decision: dec})
	sess := &brokerSession{
		playID: "play-1",
		public: frontend.Session{ID: "sess1", Protocol: "vela"},
	}
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodGet, "/sessions/media/sess1/grant1/master.m3u8", nil)
	b.serveMaster(rec, httpReq, sess, hs)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "#EXT-X-STREAM-INF:BANDWIDTH=192000") || !strings.Contains(body, "base/index.m3u8") {
		t.Fatalf("audio-only master playlist = (%d) %q", rec.Code, body)
	}
	if strings.Contains(dec.Reason, "direct play not allowed by client") {
		t.Fatalf("transcode reason falsely claimed direct play not allowed when DirectPlay=true: %q", dec.Reason)
	}
	_ = context.Background()
}

func TestPlanKindEstimateAudioSkipsPrepareKind(t *testing.T) {
	ctx := context.Background()
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	key := core.PluginKey(42)
	kp := &kindPlayback{
		fakePlayback: fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4},
		info: core.MediaInfo{
			Container: "flac",
			Duration:  4 * time.Minute,
			Streams:   []core.StreamInfo{{Index: 0, Type: "audio", Codec: "flac", Channels: 2}},
		},
	}
	events := NewEventBus(nil, active)
	t.Cleanup(events.Close)
	b, err := NewBroker(BrokerDeps{
		ActiveViewer:  active,
		Events:        events,
		SurfacePolicy: testSessionPolicy,
		Playback:      kp,
		Transcoder:    profileTranscoder{},
		Progress:      &fakeProgress{},
		CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:  func(*core.Viewer) int { return 0 },
		OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Lookup: func(context.Context, *core.Viewer, core.Device, core.RatingKey) (*core.Title, error) {
			return nil, core.ErrNotFound
		},
		KindSource: func(_ context.Context, _ *core.Viewer, k core.RatingKey) (*core.KindInput, error) {
			if k != key {
				return nil, core.ErrNotFound
			}
			return &core.KindInput{
				Title: &core.Title{
					Key:        key,
					Type:       core.TypeMovie,
					MediaClass: core.MediaClassMusic,
					MBItemType: "Audio",
					Title:      "5 Years",
					Runtime:    4*time.Minute + 29*time.Second,
				},
				SourceID: "track-5-years",
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(ctx) })

	plan, err := b.Plan(ctx, frontend.PlanRequest{
		Protocol: "jellyfin",
		Viewer:   &core.Viewer{ID: 1},
		Device:   core.Device{ID: "jf-web"},
		Item:     key,
		Intent:   frontend.PlayIntent{Estimate: true, Options: core.DecideOptions{DirectPlay: true, DirectStream: true}},
	})
	if err != nil {
		t.Fatalf("Plan(Estimate) plugin track = %v", err)
	}
	if !plan.Estimated {
		t.Fatal("expected Estimated=true plan")
	}
	if kp.prepared != 0 {
		t.Fatalf("Estimate plan must never call PrepareKind, called %d times", kp.prepared)
	}
	if kp.estimates != 1 {
		t.Fatalf("Estimate plan should call Playback.Estimate once, got %d", kp.estimates)
	}
}

func TestVelaAudioDirectPlayAllDeclaredFormatsAndAliases(t *testing.T) {
	caps := core.ClientCaps{
		Product:     "Vela Web",
		Containers:  []string{"mp4", "webm", "flac", "mp3", "ogg", "wav", "m4a", "m4b", "aac", "opus"},
		AudioCodecs: []string{"aac", "opus", "flac", "mp3", "vorbis", "alac", "pcm"},
		Profiles: &core.ClientCapabilityProfiles{
			DirectPlay: []core.ClientDirectPlayProfile{
				{
					MediaType:   "audio",
					Containers:  []string{"mp4", "m4a", "m4b", "webm", "flac", "mp3", "ogg", "opus", "wav", "aac"},
					AudioCodecs: []string{"aac", "opus", "flac", "mp3", "vorbis", "alac", "pcm"},
				},
			},
			Transcoding: []core.ClientTranscodeProfile{
				{MediaType: "audio", Delivery: "http", Containers: []string{"mp4", "aac", "mp3", "ogg", "webm"}, AudioCodecs: []string{"aac", "opus", "mp3"}},
			},
		},
	}
	cases := []struct {
		name      string
		container string
		codec     string
	}{
		{"mp3", "mp3", "mp3"},
		{"flac", "flac", "flac"},
		{"adts_aac", "adts", "aac"},
		{"m4a_aac", "m4a", "aac"},
		{"m4b_alac", "m4b", "alac"},
		{"mp4_aac", "mp4", "aac"},
		{"ogg_opus", "ogg", "opus"},
		{"opus_opus", "opus", "opus"},
		{"wav_pcm_s16le", "wav", "pcm_s16le"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := core.MediaInfo{
				Container: tc.container,
				Duration:  4 * time.Minute,
				Streams:   []core.StreamInfo{{Index: 0, Type: "audio", Codec: tc.codec, Channels: 2, SampleRate: 44100, Default: true}},
			}
			source := &core.PlaySession{ID: "play-" + tc.name, Info: info}
			b := &Broker{
				deps: BrokerDeps{
					SurfacePolicy: testSessionPolicy,
					Playback:      &fakePlayback{sessions: map[string]*core.PlaySession{source.ID: source}},
					Transcoder:    profileTranscoder{},
				},
				bindings: NewBindingTable[frontend.PlayIntent](),
			}
			dec, err := b.decideProfile(source, frontend.PlanRequest{
				Caps:   caps,
				Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true}},
			})
			if err != nil {
				t.Fatalf("decideProfile(%s) = %v", tc.name, err)
			}
			if dec.Kind != core.DecisionDirectPlay || dec.VideoCopy {
				t.Fatalf("%s expected direct play with VideoCopy=false, got %+v", tc.name, dec)
			}
		})
	}
}
