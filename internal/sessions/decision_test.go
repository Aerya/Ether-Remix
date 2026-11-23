package sessions

import (
	"context"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type decisionTranscoder struct{ decision core.Decision }

func (f decisionTranscoder) Probe(context.Context, string) (*core.MediaInfo, error) {
	panic("unexpected probe")
}
func (f decisionTranscoder) Decide(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision {
	return f.decision
}
func (f decisionTranscoder) StartHLS(context.Context, core.HLSRequest) (core.HLSSession, error) {
	panic("unexpected start")
}
func (f decisionTranscoder) Session(string) (core.HLSSession, bool) { panic("unexpected session") }
func (f decisionTranscoder) Stop(string)                            { panic("unexpected stop") }
func (f decisionTranscoder) HWAccel() core.HWAccel                  { panic("unexpected hw") }
func TestDecisionRemuxOverride(t *testing.T) {
	ps := &core.PlaySession{Resolution: &core.Resolution{Release: core.Release{ID: "usenet:1"}}}
	tc := decisionTranscoder{decision: core.Decision{Kind: core.DecisionDirectPlay}}
	r := DecisionRequest{Session: ps, PreferRemux: true, Options: core.DecideOptions{DirectStream: true}, Protocol: core.ProtocolDASH}
	got, err := Decide(nil, tc, r)
	if err != nil || got.Kind != core.DecisionRemux || got.Container != "fmp4" || !got.VideoCopy || !got.AudioCopy {
		t.Fatalf("decision %+v, %v", got, err)
	}
	ps.Resolution.Release.Protocol = core.ProtocolTorrent
	got, err = Decide(nil, tc, r)
	if err != nil || got.Kind != core.DecisionDirectPlay {
		t.Fatalf("torrent decision %+v, %v", got, err)
	}
	ps.RequiresRemux = true
	got, err = Decide(nil, tc, r)
	if err != nil || got.Kind != core.DecisionRemux {
		t.Fatalf("required remux %+v, %v", got, err)
	}
}

func TestHLSDecisionReconsidersClientRejection(t *testing.T) {
	called := false
	got := HLSDecision(core.Decision{Kind: core.DecisionDirectPlay}, false, func() core.Decision { called = true; return core.Decision{Kind: core.DecisionTranscode} }, nil)
	if !called || got.Kind != core.DecisionTranscode || got.Container != "mpegts" {
		t.Fatalf("decision %+v, reconsider=%v", got, called)
	}
	got = HLSDecision(core.Decision{Kind: core.DecisionDirectPlay}, true, func() core.Decision { t.Fatal("unexpected reconsider"); return core.Decision{} }, nil)
	if got.Kind != core.DecisionRemux || !got.VideoCopy || !got.AudioCopy || got.Container != "mpegts" {
		t.Fatalf("decision %+v", got)
	}
}

func TestHLSDecisionForcedTargetBranch(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		initial                    core.DecisionKind
		allowDirect                bool
		reconsidered               core.DecisionKind
		wantReconsider, wantForced bool
	}{
		{"accepted direct still needs segmented output", core.DecisionDirectPlay, true, core.DecisionDirectPlay, false, true},
		{"rejected direct becomes transcode", core.DecisionDirectPlay, false, core.DecisionTranscode, true, false},
		{"rejected direct remains direct", core.DecisionDirectPlay, false, core.DecisionDirectPlay, true, true},
		{"existing remux", core.DecisionRemux, true, core.DecisionRemux, false, false},
		{"existing transcode", core.DecisionTranscode, false, core.DecisionTranscode, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reconsidered, forced := false, false
			got := HLSDecision(core.Decision{Kind: tc.initial, AudioCodec: "eac3"}, tc.allowDirect, func() core.Decision {
				reconsidered = true
				return core.Decision{Kind: tc.reconsidered, AudioCodec: "eac3"}
			}, func(dec core.Decision) core.Decision {
				forced = true
				if dec.Kind != core.DecisionRemux || !dec.VideoCopy || !dec.AudioCopy || dec.Container != "mpegts" {
					t.Fatalf("output target ran before forced-remux facts: %+v", dec)
				}
				return core.Decision{Kind: core.DecisionTranscode, VideoCopy: true, AudioCodec: "aac"}
			})
			if reconsidered != tc.wantReconsider || forced != tc.wantForced {
				t.Fatalf("reconsider=%v forced=%v", reconsidered, forced)
			}
			if got.Container != "mpegts" {
				t.Fatalf("missing post-reconsider container default: %+v", got)
			}
			if forced && (got.AudioCodec != "aac" || got.AudioCopy || !got.VideoCopy) {
				t.Fatalf("target decision overwritten: %+v", got)
			}
		})
	}
}

type targetDecisionTranscoder struct {
	decisionTranscoder
	options []core.DecideOptions
}

func (f *targetDecisionTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	f.options = append(f.options, opts)
	if opts.DirectPlay {
		return core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, AudioCodec: "eac3", AudioStream: 7}
	}
	if !opts.DirectStream {
		return core.Decision{Kind: core.DecisionTranscode, VideoCopy: false, VideoCodec: "h264", AudioCopy: false, AudioCodec: "aac", AudioStream: 7}
	}
	if slices.Contains(caps.TranscodeAudioCodecs, "eac3") {
		return core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, AudioCodec: "eac3", AudioStream: 7}
	}
	return core.Decision{Kind: core.DecisionTranscode, VideoCopy: true, AudioCodec: "aac", AudioStream: 7}
}

func TestDecisionForcedRemuxHonorsAudioTarget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		target    []string
		prefer    bool
		verified  bool
		wantCalls int
		wantKind  core.DecisionKind
	}{
		{"AAC target on forced stream", []string{"aac"}, true, true, 2, core.DecisionTranscode},
		{"surround target on forced stream", []string{"aac", "eac3"}, true, true, 1, core.DecisionRemux},
		{"no target", nil, true, true, 1, core.DecisionRemux},
		{"direct play exemption", []string{"aac"}, false, true, 1, core.DecisionDirectPlay},
		{"unverified health without preference remuxes", []string{"aac", "eac3"}, false, false, 2, core.DecisionRemux},
		{"unverified health preserves forced AAC target", []string{"aac"}, true, false, 2, core.DecisionTranscode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := &core.PlaySession{Resolution: &core.Resolution{HealthVerified: tc.verified, Release: core.Release{ID: "usenet:fixture"}}, Info: core.MediaInfo{Streams: []core.StreamInfo{{Type: "video", Codec: "h264"}, {Type: "audio", Index: 7, Codec: "eac3"}}}}
			transcoder := &targetDecisionTranscoder{}
			got, err := Decide(nil, transcoder, DecisionRequest{Session: ps, Caps: core.ClientCaps{TranscodeAudioCodecs: tc.target}, Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 7}, PreferRemux: tc.prefer, Protocol: core.ProtocolDASH})
			if err != nil || got.Kind != tc.wantKind || len(transcoder.options) != tc.wantCalls {
				t.Fatalf("decision %+v error %v calls %v", got, err, transcoder.options)
			}
			if tc.wantKind == core.DecisionRemux && (!got.VideoCopy || !got.AudioCopy) {
				t.Fatalf("remux must copy both streams: %+v", got)
			}
			if tc.wantKind == core.DecisionTranscode && (transcoder.options[1].DirectPlay || !transcoder.options[1].DirectStream || transcoder.options[1].AudioStream != 7 || got.AudioCodec != "aac" || got.AudioCopy || !got.VideoCopy) {
				t.Fatalf("target reconsider lost selected source facts: %+v %+v", got, transcoder.options)
			}
		})
	}
}

func TestVideoTargetDirectPlaySurvivesOptionalRemux(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		target                []string
		required, allowDirect bool
		wantCalls             int
		wantKind              core.DecisionKind
	}{
		{"H264 HLS target keeps native 4K HEVC direct play", []string{"h264"}, false, true, 1, core.DecisionDirectPlay},
		{"required remux converts when target forbids HEVC copy", []string{"h264"}, true, true, 2, core.DecisionTranscode},
		{"HEVC HLS target still remuxes Usenet", []string{"h264", "hevc"}, false, true, 1, core.DecisionRemux},
		{"no video target still remuxes Usenet", nil, false, true, 1, core.DecisionRemux},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := &core.PlaySession{
				RequiresRemux: tc.required,
				Resolution:    &core.Resolution{Release: core.Release{ID: "usenet:mobland-4k"}},
				Info: core.MediaInfo{
					Container: "mkv",
					Streams: []core.StreamInfo{
						{Type: "video", Index: 0, Codec: "hevc", Width: 3840, Height: 2160},
						{Type: "audio", Index: 7, Codec: "eac3"},
					},
				},
			}
			transcoder := &targetDecisionTranscoder{}
			got, err := Decide(nil, transcoder, DecisionRequest{
				Session:     ps,
				Caps:        core.ClientCaps{Platform: "webOS", Product: "Plex for LG", TranscodeVideoCodecs: tc.target},
				Options:     core.DecideOptions{DirectPlay: tc.allowDirect, DirectStream: true, AudioStream: 7},
				PreferRemux: true,
				Protocol:    core.ProtocolHLS,
			})
			if err != nil || got.Kind != tc.wantKind || len(transcoder.options) != tc.wantCalls {
				t.Fatalf("decision %+v error %v calls %v", got, err, transcoder.options)
			}
			if tc.wantCalls == 2 && (transcoder.options[1].DirectPlay || transcoder.options[1].DirectStream || got.VideoCopy || got.VideoCodec != "h264") {
				t.Fatalf("required remux did not convert HEVC to target H264: %+v %+v", got, transcoder.options)
			}
		})
	}
}

func TestAndroidDVDirectPlaySurvivesOptionalRemux(t *testing.T) {
	for _, tc := range []struct {
		name, platform      string
		profile             int
		dv, required, allow bool
		want                core.DecisionKind
	}{
		{"DV8 native file", "Android", 8, true, false, true, core.DecisionDirectPlay},
		{"DV5 native file", "Android", 5, true, false, true, core.DecisionDirectPlay},
		{"required remux wins", "Android", 8, true, true, true, core.DecisionRemux},
		{"client rejection wins", "Android", 8, true, false, false, core.DecisionRemux},
		{"unsupported profile", "Android", 7, true, false, true, core.DecisionRemux},
		{"DV disabled", "Android", 8, false, false, true, core.DecisionRemux},
		{"other platform unchanged", "tvOS", 8, true, false, true, core.DecisionRemux},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := &core.PlaySession{RequiresRemux: tc.required, Resolution: &core.Resolution{Release: core.Release{ID: "usenet:dv"}}, Info: core.MediaInfo{Container: "mkv", Streams: []core.StreamInfo{{Type: "video", Codec: "hevc", DolbyVision: true, DolbyVisionProfile: tc.profile}}}}
			r := DecisionRequest{Session: ps, PreferRemux: true, Caps: core.ClientCaps{Platform: tc.platform, Device: "SHIELD Android TV", DolbyVision: tc.dv}, Options: core.DecideOptions{DirectPlay: tc.allow, DirectStream: true}}
			transcoder := decisionTranscoder{decision: core.Decision{Kind: core.DecisionDirectPlay}}
			got, err := Decide(nil, transcoder, r)
			if err != nil || got.Kind != tc.want {
				t.Fatalf("decision=%+v err=%v", got, err)
			}
		})
	}
}

// The same source health rule applies independently of the client protocol;
// explicit static-only capabilities and stable HTTP files retain direct play.
func TestDecisionHealthFailoverSafety(t *testing.T) {
	for _, tt := range []struct {
		name       string
		verified   bool
		protocol   string
		directOnly bool
		want       core.DecisionKind
	}{
		{name: "partial Usenet", want: core.DecisionRemux},
		{name: "sampled Usenet", verified: true, want: core.DecisionDirectPlay},
		{name: "stable HTTP", protocol: core.ProtocolHTTP, want: core.DecisionDirectPlay},
		{name: "stable debrid", protocol: core.ProtocolTorrent, want: core.DecisionDirectPlay},
		{name: "static only", directOnly: true, want: core.DecisionDirectPlay},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ps := &core.PlaySession{Resolution: &core.Resolution{HealthVerified: tt.verified, Release: core.Release{ID: "test", Protocol: tt.protocol}}, Info: core.MediaInfo{Container: "mkv"}}
			tr := decisionTranscoder{decision: core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true}}
			got, err := Decide(nil, tr, DecisionRequest{Session: ps, Options: core.DecideOptions{DirectPlay: true, DirectStream: !tt.directOnly}})
			if err != nil || got.Kind != tt.want {
				t.Fatalf("decision=%+v err=%v want=%s", got, err, tt.want)
			}
			if got.Kind == core.DecisionRemux && (!got.VideoCopy || !got.AudioCopy || got.Reason != "remux for failover safety: health unverified") {
				t.Fatalf("unsafe remux: %+v", got)
			}
		})
	}
}

type segmentedDecisionTranscoder struct{ segmented core.Decision }

func (f segmentedDecisionTranscoder) Decide(_ core.MediaInfo, _ core.ClientCaps, opts core.DecideOptions) core.Decision {
	if opts.DirectPlay {
		return core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true}
	}
	return f.segmented
}
func (segmentedDecisionTranscoder) StartHLS(context.Context, core.HLSRequest) (core.HLSSession, error) {
	return nil, core.ErrNotAvailable
}
func (segmentedDecisionTranscoder) Session(string) (core.HLSSession, bool) { return nil, false }
func (segmentedDecisionTranscoder) Stop(string)                            {}
func (segmentedDecisionTranscoder) HWAccel() core.HWAccel                  { return core.HWAccel{} }
func (segmentedDecisionTranscoder) Probe(context.Context, string) (*core.MediaInfo, error) {
	return nil, core.ErrNotAvailable
}

// The optional remux for an unverified source only ever trades the file for
// a lossless copy. A cold 4K Dolby Vision TrueHD remux (Totoro) kept direct
// play: its segmented copy would strip Dolby Vision and could not carry
// TrueHD, and the Apple TV refused it.
func TestUnverifiedSourceNeverTakesLossyOptionalRemux(t *testing.T) {
	ps := &core.PlaySession{Resolution: &core.Resolution{Release: core.Release{ID: "usenet:dv"}}, Info: core.MediaInfo{Container: "mkv", Streams: []core.StreamInfo{{Type: "video", Codec: "hevc", DolbyVision: true, DolbyVisionProfile: 8}, {Type: "audio", Codec: "truehd"}}}}
	for name, tc := range map[string]struct {
		segmented core.Decision
		want      core.DecisionKind
	}{
		"audio not carried":     {core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCodec: "eac3"}, core.DecisionDirectPlay},
		"dolby vision stripped": {core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, StripDolbyVision: true}, core.DecisionDirectPlay},
		"lossless copy":         {core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true}, core.DecisionRemux},
	} {
		got, err := Decide(nil, segmentedDecisionTranscoder{tc.segmented}, DecisionRequest{Session: ps, Options: core.DecideOptions{DirectPlay: true, DirectStream: true}})
		if err != nil || got.Kind != tc.want {
			t.Fatalf("%s: decision %+v err %v", name, got, err)
		}
	}
}

// A declared streaming profile that cannot take the optional copy (H.264-only
// TS: Wholphin, Jellyfin for Android) keeps the file it direct-plays instead
// of letting the profile check turn the copy into a full encode.
func TestUnverifiedSourceOptionalRemuxHonoursDeclaredStreamingProfile(t *testing.T) {
	ps := &core.PlaySession{Resolution: &core.Resolution{Release: core.Release{ID: "usenet:hevc"}}, Info: core.MediaInfo{Container: "mkv", Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc"}, {Index: 1, Type: "audio", Codec: "eac3"}}}}
	copyDecision := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, VideoCodec: "hevc", AudioCodec: "eac3", Container: "mpegts", AudioStream: 1}
	for name, tc := range map[string]struct {
		video []string
		want  core.DecisionKind
	}{
		"h264-only streaming": {[]string{"h264"}, core.DecisionDirectPlay},
		"hevc streaming":      {[]string{"h264", "hevc"}, core.DecisionRemux},
	} {
		profiles := &core.ClientCapabilityProfiles{
			DirectPlay:  []core.ClientDirectPlayProfile{{MediaType: "video", Containers: []string{"mkv"}, VideoCodecs: []string{"hevc"}, AudioCodecs: []string{"eac3"}}},
			Transcoding: []core.ClientTranscodeProfile{{MediaType: "video", Delivery: "hls", Containers: []string{"ts", "mp4"}, VideoCodecs: tc.video, AudioCodecs: []string{"eac3"}}},
		}
		got, err := Decide(nil, profileDecisionTranscoder{segmentedDecisionTranscoder{copyDecision}}, DecisionRequest{Session: ps, Caps: core.ClientCaps{Profiles: profiles, Containers: []string{"mkv", "mpegts"}, VideoCodecs: []string{"hevc"}, AudioCodecs: []string{"eac3"}}, Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1}})
		if err != nil || got.Kind != tc.want {
			t.Fatalf("%s: decision %+v err %v", name, got, err)
		}
	}
}

// profileDecisionTranscoder direct-plays with the codecs named, as the real
// decision does, and returns segmented otherwise.
type profileDecisionTranscoder struct{ segmentedDecisionTranscoder }

func (f profileDecisionTranscoder) Decide(_ core.MediaInfo, _ core.ClientCaps, opts core.DecideOptions) core.Decision {
	if opts.DirectPlay {
		return core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, VideoCodec: "hevc", AudioCodec: "eac3", Container: "mkv", AudioStream: 1}
	}
	return f.segmented
}

type realDecideTranscoder struct{}

func (realDecideTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	return transcode.Decide(info, caps, opts, transcode.Policy{})
}
func (realDecideTranscoder) StartHLS(context.Context, core.HLSRequest) (core.HLSSession, error) {
	return nil, core.ErrNotAvailable
}
func (realDecideTranscoder) Session(string) (core.HLSSession, bool) { return nil, false }
func (realDecideTranscoder) Stop(string)                            {}
func (realDecideTranscoder) HWAccel() core.HWAccel                  { return core.HWAccel{} }
func (realDecideTranscoder) Probe(context.Context, string) (*core.MediaInfo, error) {
	return nil, core.ErrNotAvailable
}

func TestAlwaysEncodeVideoAcrossSessionsAndHLSOutput(t *testing.T) {
	info := core.MediaInfo{
		Container: "mkv",
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
		},
	}
	caps := core.ClientCaps{
		Containers:        []string{"mkv"},
		VideoCodecs:       []string{"h264"},
		AudioCodecs:       []string{"aac"},
		MaxHeight:         2160,
		AlwaysEncodeVideo: true,
	}
	tr := realDecideTranscoder{}

	// Required remux or PreferRemux encodes video rather than copying it.
	ps := &core.PlaySession{
		Resolution: &core.Resolution{HealthVerified: true, Release: core.Release{ID: "usenet:1"}},
		Info:       info,
	}
	got, err := Decide(nil, tr, DecisionRequest{
		Session:     ps,
		Caps:        caps,
		Options:     core.DecideOptions{DirectPlay: true, DirectStream: true, Protocol: core.ProtocolHLS},
		PreferRemux: true,
	})
	if err != nil || got.Kind != core.DecisionTranscode || got.VideoCopy {
		t.Fatalf("PreferRemux with AlwaysEncodeVideo: got %+v err=%v", got, err)
	}

	// Optional health-safety remux without PreferRemux still keeps direct play.
	unverified := &core.PlaySession{
		Resolution: &core.Resolution{HealthVerified: false, Release: core.Release{ID: "usenet:2"}},
		Info:       info,
	}
	got, err = Decide(nil, tr, DecisionRequest{
		Session: unverified,
		Caps:    caps,
		Options: core.DecideOptions{DirectPlay: true, DirectStream: true, Protocol: core.ProtocolHLS},
	})
	if err != nil || got.Kind != core.DecisionDirectPlay {
		t.Fatalf("optional healthSafety should keep direct play: got %+v err=%v", got, err)
	}

	// Late DirectPlay -> HLS output promotion encodes video instead of copying.
	b := &Broker{deps: BrokerDeps{Transcoder: tr}}
	direct := core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac"}
	out, ok := b.hlsOutputDecision(ps, caps, core.DecideOptions{DirectPlay: true, DirectStream: true, Protocol: core.ProtocolHLS}, nil, direct)
	if !ok || out.Kind != core.DecisionTranscode || out.VideoCopy {
		t.Fatalf("hlsOutputDecision should encode video: ok=%v dec=%+v", ok, out)
	}
}
