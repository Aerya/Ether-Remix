package sessions

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type profileTranscoder struct{ fakeBrokerTranscoder }

func (profileTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	return transcode.Decide(info, caps, opts, transcode.Policy{})
}

func TestClientProfilePairsAndDelivery(t *testing.T) {
	info := core.MediaInfo{Container: "mp4", Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080}, {Index: 1, Type: "audio", Codec: "aac"}}}
	d := core.Decision{Kind: core.DecisionDirectPlay, AudioStream: 1}
	p := &core.ClientCapabilityProfiles{DirectPlay: []core.ClientDirectPlayProfile{{MediaType: "video", Containers: []string{"mkv"}, VideoCodecs: []string{"hevc"}, AudioCodecs: []string{"aac"}}, {MediaType: "video", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}}}
	if profileDirectAllowed(p, info, d) {
		t.Fatal("accepted a cross-profile container and codec combination")
	}
	p.DirectPlay[1].VideoCodecs = []string{"hevc"}
	if !profileDirectAllowed(p, info, d) {
		t.Fatal("rejected declared direct-play pair")
	}
	p.Codecs = []core.ClientCodecProfile{{MediaType: "video", Codecs: []string{"hevc"}, Conditions: []core.ClientProfileCondition{{Property: "future", Operator: "Equals", Value: "1", Required: true}}}}
	if profileDirectAllowed(p, info, d) {
		t.Fatal("accepted unknown required condition")
	}
	p.Codecs[0].Conditions = []core.ClientProfileCondition{{Property: "VideoWidth", Operator: "LessThanEqual", Value: "1920", Required: true}}
	if !profileDirectAllowed(p, info, d) {
		t.Fatal("rejected supported passing condition")
	}
	p.Codecs[0].Conditions[0].Value = "1280"
	if profileDirectAllowed(p, info, d) {
		t.Fatal("accepted failed required condition")
	}
	p.Codecs[0].ApplyConditions = []core.ClientProfileCondition{{Property: "VideoWidth", Operator: "LessThanEqual", Value: "1280", Required: true}}
	if !profileDirectAllowed(p, info, d) {
		t.Fatal("enforced an inapplicable codec profile")
	}
	p.Codecs[0].ApplyConditions[0].Property = "unknown"
	if profileDirectAllowed(p, info, d) {
		t.Fatal("accepted an unknown required applicability condition")
	}
	p.Codecs = nil
	p.Transcoding = []core.ClientTranscodeProfile{{MediaType: "video", Delivery: "dash", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}}
	out := core.Decision{Kind: core.DecisionTranscode, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac"}
	if profileDeliveryAllowed(p, out, info, "hls") || !profileDeliveryAllowed(p, out, info, "dash") {
		t.Fatal("accepted undeclared delivery or rejected declared delivery")
	}
	p.Transcoding[0].MaxAudioChannels = "2"
	out.AudioCopy = true
	out.AudioStream = 1
	info.Streams[1].Channels = 6
	if profileDeliveryAllowed(p, out, info, "dash") {
		t.Fatal("accepted output exceeding declared audio channels")
	}
	info.Streams[1].Channels = 2
	if !profileDeliveryAllowed(p, out, info, "dash") {
		t.Fatal("rejected output within declared audio channels")
	}
	if !profileDirectAllowed(nil, info, d) || !profileDeliveryAllowed(nil, out, info, "hls") {
		t.Fatal("legacy nil profile path changed")
	}
}

func TestProfileDecisionChoosesDeclaredFallbackAndSubtitle(t *testing.T) {
	info := core.MediaInfo{Container: "mp4", Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080}, {Index: 1, Type: "audio", Codec: "aac"}, {Index: 2, Type: "subtitle", Codec: "hdmv_pgs_subtitle"}}}
	profiles := &core.ClientCapabilityProfiles{DirectPlay: []core.ClientDirectPlayProfile{{MediaType: "video", Containers: []string{"mkv"}, VideoCodecs: []string{"hevc"}, AudioCodecs: []string{"aac"}}}, Transcoding: []core.ClientTranscodeProfile{{MediaType: "video", Delivery: "hls", Containers: []string{"mpegts"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}}, Subtitles: []core.ClientSubtitleProfile{}}
	caps := core.ClientCaps{Profiles: profiles, Containers: []string{"mp4", "mkv"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac"}, Device: "NVIDIA Shield"}
	applySubtitleProfiles(&caps)
	req := frontend.PlanRequest{Caps: caps, Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: 2}}}
	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Transcoder: profileTranscoder{}}}
	d, err := b.decideProfile(&core.PlaySession{Info: info}, req)
	if err != nil || d.Kind != core.DecisionTranscode || d.VideoCodec != "h264" || !d.BurnSubtitles {
		t.Fatalf("decision %+v err=%v", d, err)
	}
	if decisionEncodes(core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: false, AudioStream: 1}, info) != true {
		t.Fatal("audio conversion must count as transcoding")
	}
}

func TestUnknownHEVCConditionTriesDeclaredH264Transcode(t *testing.T) {
	info := core.MediaInfo{Container: "mp4", Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160}, {Index: 1, Type: "audio", Codec: "ac3", Channels: 6}}}
	profiles := &core.ClientCapabilityProfiles{
		DirectPlay:  []core.ClientDirectPlayProfile{{MediaType: "video", Containers: []string{"mp4"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"ac3"}}},
		Codecs:      []core.ClientCodecProfile{{MediaType: "video", Codecs: []string{"hevc"}, Conditions: []core.ClientProfileCondition{{Property: "VideoRangeType", Operator: "NotEquals", Value: "DOVI", Required: true}}}},
		Transcoding: []core.ClientTranscodeProfile{{MediaType: "video", Delivery: "hls", Containers: []string{"mpegts"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3"}, MaxAudioChannels: "6"}},
	}
	caps := core.ClientCaps{Profiles: profiles, Containers: []string{"mp4"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3"}, HLSContainers: []string{"mpegts"}}
	req := frontend.PlanRequest{Caps: caps, Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}}}
	var events []ProfileDiagnostic
	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Transcoder: profileTranscoder{}, OnProfileDiagnostic: func(event ProfileDiagnostic) { events = append(events, event) }}}
	if profileDirectAllowed(profiles, info, core.Decision{Kind: core.DecisionDirectPlay, AudioStream: 1}) {
		t.Fatal("unknown required HEVC predicate was accepted")
	}
	d, err := b.decideProfile(&core.PlaySession{Info: info}, req)
	if err != nil || d.Kind != core.DecisionTranscode || d.VideoCodec != "h264" || !profileDeliveryAllowed(profiles, d, info, "hls") {
		t.Fatalf("declared H.264 fallback: %+v %v", d, err)
	}
	if len(events) != 3 || events[0] != ProfileDirectRejected || events[1] != ProfileRequiredConditionUnknown || events[2] != ProfileFallbackTranscode {
		t.Fatalf("unexpected redacted decision diagnostics: %v", events)
	}
	denied := false
	req.Intent.AllowTranscoding = &denied
	if _, err = b.decideProfile(&core.PlaySession{Info: info}, req); err == nil {
		t.Fatal("transcoding prohibition ignored")
	}
	req.Intent.AllowTranscoding = nil
	profiles.Transcoding[0].VideoCodecs = []string{"hevc"}
	if _, err = b.decideProfile(&core.PlaySession{Info: info}, req); err == nil {
		t.Fatal("undeclared H.264 fallback was selected")
	}
	profiles.Transcoding[0].VideoCodecs = []string{"h264", "hevc"}
	// A declared channel limit is negotiated into the fallback (downmix),
	// never ignored.
	profiles.Transcoding[0].MaxAudioChannels = "1"
	if d, err = b.decideProfile(&core.PlaySession{Info: info}, req); err == nil && (d.AudioCopy || d.AudioChannels == 0 || d.AudioChannels > 1) {
		t.Fatalf("fallback ignored declared audio channel limit: %+v", d)
	}
}

func TestConstrainedH264OutputRespectsLevelAndProfile(t *testing.T) {
	info := core.MediaInfo{Streams: []core.StreamInfo{{Type: "video", Codec: "hevc", Width: 3840, Height: 2160, FrameRate: 30}}}
	d := core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", BitrateKbps: 8000}
	got, ok := constrainedH264(d, info, core.ClientCaps{}, true)
	if !ok || got.H264Level != 51 {
		t.Fatalf("4K decision: %+v, allowed %v", got, ok)
	}
	caps := core.ClientCaps{VideoLevels: map[string]int{"h264": 41}}
	if _, ok := constrainedH264(d, info, caps, true); ok {
		t.Fatal("4K accepted for level 4.1")
	}
	d.MaxHeight = 1080
	got, ok = constrainedH264(d, info, caps, true)
	if !ok || got.H264Level > 41 {
		t.Fatalf("1080p decision: %+v, allowed %v", got, ok)
	}
	caps.VideoProfiles = map[string][]string{"h264": {"Baseline"}}
	if _, ok := constrainedH264(d, info, caps, true); ok {
		t.Fatal("High accepted for a Baseline-only client")
	}
	info.Streams[0].FrameRate = 0
	caps.VideoProfiles = nil
	if _, ok := constrainedH264(d, info, caps, true); ok {
		t.Fatal("unknown frame rate accepted against level restriction")
	}
}

func TestProfileFallback4KRespectsH264Level(t *testing.T) {
	info := core.MediaInfo{Container: "mp4", BitrateKbps: 12000, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, FrameRate: 30}, {Index: 1, Type: "audio", Codec: "ac3", Channels: 6}}}
	profiles := &core.ClientCapabilityProfiles{
		DirectPlay:  []core.ClientDirectPlayProfile{{MediaType: "video", Containers: []string{"mp4"}, VideoCodecs: []string{"hevc"}, AudioCodecs: []string{"ac3"}}},
		Codecs:      []core.ClientCodecProfile{{MediaType: "video", Codecs: []string{"hevc"}, Conditions: []core.ClientProfileCondition{{Property: "VideoRangeType", Operator: "NotEquals", Value: "DOVI", Required: true}}}, {MediaType: "video", Codecs: []string{"h264"}, Conditions: []core.ClientProfileCondition{{Property: "VideoProfile", Operator: "Equals", Value: "High", Required: true}, {Property: "VideoLevel", Operator: "LessThanEqual", Value: "41", Required: true}, {Property: "VideoWidth", Operator: "LessThanEqual", Value: "1920", Required: true}, {Property: "VideoFramerate", Operator: "LessThanEqual", Value: "30", Required: true}}}},
		Transcoding: []core.ClientTranscodeProfile{{MediaType: "video", Delivery: "hls", Containers: []string{"mpegts"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac", "ac3"}, MaxAudioChannels: "6"}},
	}
	req := frontend.PlanRequest{Caps: core.ClientCaps{Profiles: profiles, Containers: []string{"mp4"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3"}, HLSContainers: []string{"mpegts"}, VideoLevels: map[string]int{"h264": 41}}, Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}}}
	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Transcoder: profileTranscoder{}}}
	d, err := b.decideProfile(&core.PlaySession{Info: info}, req)
	if err != nil || d.VideoCodec != "h264" || d.VideoCopy || d.MaxHeight <= 0 || d.MaxHeight > 1080 || d.H264Level == 0 || d.H264Level > 41 {
		t.Fatalf("expected bounded 4K fallback: %+v, %v", d, err)
	}
	profiles.Codecs[1].Conditions[3].Value = "29"
	if _, err := b.decideProfile(&core.PlaySession{Info: info}, req); err == nil {
		t.Fatal("accepted output exceeding required client frame rate")
	}
	profiles.Codecs[1].Conditions[3].Value = "30"
	req.Caps.VideoProfiles = map[string][]string{"h264": {"Baseline"}}
	if _, err := b.decideProfile(&core.PlaySession{Info: info}, req); err == nil {
		t.Fatal("accepted unsupported High for Baseline-only device")
	}
}

type profileHWTranscoder struct {
	profileTranscoder
	hw core.HWAccel
}

func (t profileHWTranscoder) HWAccel() core.HWAccel { return t.hw }

func TestH264OutputContractOnlyForVerifiedEncoderPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		hw   core.HWAccel
		want bool
	}{
		{"software", core.HWAccel{}, true},
		{"nvenc", core.HWAccel{Name: "nvenc", Encoders: []string{"h264_nvenc"}}, true},
		{"qsv", core.HWAccel{Name: "qsv", Encoders: []string{"h264_qsv"}}, false},
		{"vaapi", core.HWAccel{Name: "vaapi", Encoders: []string{"h264_vaapi"}}, false},
		{"videotoolbox", core.HWAccel{Name: "videotoolbox", Encoders: []string{"h264_videotoolbox"}}, false},
		{"hardware without h264 falls back to software", core.HWAccel{Name: "qsv"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Transcoder: profileHWTranscoder{hw: tc.hw}}}
			if b.h264EncoderVerified() != tc.want {
				t.Fatal("incorrect encoder qualification")
			}
		})
	}
	info := core.MediaInfo{Streams: []core.StreamInfo{{Type: "video", Width: 1920, Height: 1080, FrameRate: 30}}}
	d := core.Decision{VideoCodec: "h264", BitrateKbps: 8000}
	caps := core.ClientCaps{VideoLevels: map[string]int{"h264": 41}}
	if _, ok := constrainedH264(d, info, caps, false); ok {
		t.Fatal("unverified encoder satisfied required level")
	}
	got, ok := constrainedH264(d, info, core.ClientCaps{}, false)
	if !ok || got.H264Level != 0 {
		t.Fatalf("unverified encoder claimed a level: %+v", got)
	}
}

func TestH264OnlyProfileNegotiates4KEncoderContract(t *testing.T) {
	info := core.MediaInfo{Container: "mp4", BitrateKbps: 3000, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, FrameRate: 30, BitrateKbps: 2462}, {Index: 1, Type: "audio", Codec: "ac3", Channels: 6}}}
	profiles := &core.ClientCapabilityProfiles{
		DirectPlay:  []core.ClientDirectPlayProfile{{MediaType: "video", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"ac3"}}},
		Transcoding: []core.ClientTranscodeProfile{{MediaType: "video", Delivery: "hls", Containers: []string{"mpegts"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"ac3"}, MaxAudioChannels: "6"}},
	}
	caps := core.ClientCaps{Profiles: profiles, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"ac3"}, HLSContainers: []string{"mpegts"}}
	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Transcoder: profileTranscoder{}}}
	d, err := b.decideProfile(&core.PlaySession{Info: info}, frontend.PlanRequest{Caps: caps, Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}}})
	if err != nil || d.VideoCopy || d.VideoCodec != "h264" || d.H264Level != 51 || d.H264FrameRate != 30 {
		t.Fatalf("4K H.264 contract: %+v, %v", d, err)
	}
}

func TestH264ContractRecognizesInactiveEnhancement(t *testing.T) {
	info := core.MediaInfo{Streams: []core.StreamInfo{{Type: "video", Width: 3840, Height: 2160, FrameRate: 30}}}
	caps := core.ClientCaps{VideoLevels: map[string]int{"h264": 51}}
	for _, state := range []string{"off", "ineligible", "unavailable", "fallback"} {
		d := core.Decision{VideoCodec: "h264", BitrateKbps: 8000, Enhancement: core.VideoEnhancement{Backend: "nvenc-ngx-vsr", State: state}}
		got, ok := constrainedH264(d, info, caps, true)
		if !ok || got.H264Level != 51 {
			t.Errorf("inactive state %s: %+v allowed=%v", state, got, ok)
		}
	}
	for _, state := range []string{"selected", "starting", "running", "unknown"} {
		d := core.Decision{VideoCodec: "h264", BitrateKbps: 8000, Enhancement: core.VideoEnhancement{Backend: "nvenc-ngx-vsr", State: state}}
		if _, ok := constrainedH264(d, info, caps, true); ok {
			t.Errorf("unverified state %s accepted", state)
		}
	}
}

func TestInfuseConvertSurroundAudioRuleCopiesVideoAndTranscodesAudioToAAC(t *testing.T) {
	info := core.MediaInfo{
		Container:   "mkv",
		BitrateKbps: 15000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, ColorTransfer: "smpte2084"},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Default: true},
		},
	}
	infuseCaps := func() core.ClientCaps {
		return core.ClientCaps{
			Protocol:      "jellyfin",
			Product:       "Infuse-Direct",
			Platform:      "tvOS",
			MaxHeight:     2160,
			HDR10:         true,
			DolbyVision:   true,
			Containers:    []string{"mkv", "mp4", "mov", "m4v", "mpegts", "avi"},
			HLSContainers: []string{"mp4", "mpegts"},
			VideoCodecs:   []string{"hevc", "h264", "av1", "vp9"},
			AudioCodecs:   []string{"aac", "ac3", "eac3", "truehd", "dts", "dtshd", "flac", "alac", "opus", "mp3", "pcm"},
			Profiles: &core.ClientCapabilityProfiles{
				DirectPlay: []core.ClientDirectPlayProfile{{
					MediaType:   "video",
					Containers:  []string{"mkv", "mp4", "mov", "m4v", "ts", "mpegts", "avi"},
					VideoCodecs: []string{"hevc", "h264", "av1", "vp9"},
					AudioCodecs: []string{"aac", "ac3", "eac3", "truehd", "dts", "dca", "flac", "alac", "opus", "mp3", "pcm"},
				}},
				Transcoding: []core.ClientTranscodeProfile{
					{
						MediaType:        "video",
						Delivery:         "hls",
						Containers:       []string{"mp4"},
						VideoCodecs:      []string{"hevc", "h264"},
						AudioCodecs:      []string{"aac", "ac3", "eac3"},
						MaxAudioChannels: "6",
					},
					{
						MediaType:        "video",
						Delivery:         "hls",
						Containers:       []string{"ts"},
						VideoCodecs:      []string{"h264"},
						AudioCodecs:      []string{"aac", "ac3"},
						MaxAudioChannels: "2",
					},
				},
			},
		}
	}

	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Transcoder: profileTranscoder{}}}

	// Without rule: EAC3 5.1 MKV direct-plays on Infuse-Direct.
	qOff := config.Quality{
		Default:  "4K",
		Profiles: []core.QualityProfile{{Name: "4K", MaxHeight: 2160}},
	}
	capsOff := resolver.ApplyClientRules(qOff, infuseCaps(), nil)
	decOff, err := b.decideProfile(&core.PlaySession{Info: info}, frontend.PlanRequest{
		Caps:   capsOff,
		Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}},
	})
	if err != nil || decOff.Kind != core.DecisionDirectPlay || !decOff.VideoCopy || !decOff.AudioCopy {
		t.Fatalf("without toggle: decision=%+v err=%v, want directplay", decOff, err)
	}

	// With ConvertSurroundAudio rule: video is copied and audio is transcoded to AAC.
	qOn := config.Quality{
		Default:  "4K",
		Profiles: []core.QualityProfile{{Name: "4K", MaxHeight: 2160}},
		Rules: []config.QualityRule{{
			Protocol:             "jellyfin",
			Product:              "Infuse",
			Profile:              "4K",
			ConvertSurroundAudio: true,
		}},
	}
	capsOn := resolver.ApplyClientRules(qOn, infuseCaps(), nil)
	decOn, err := b.decideProfile(&core.PlaySession{Info: info}, frontend.PlanRequest{
		Caps:   capsOn,
		Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}},
	})
	if err != nil {
		t.Fatalf("with toggle: unexpected err=%v", err)
	}
	if decOn.Kind != core.DecisionRemux || !decOn.VideoCopy || decOn.AudioCopy || decOn.AudioCodec != "aac" {
		t.Fatalf("with toggle: decision=%+v, want Kind=copy VideoCopy=true AudioCopy=false AudioCodec=aac", decOn)
	}
}

type hevcProfileTranscoder struct{ fakeBrokerTranscoder }

func (hevcProfileTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	return transcode.Decide(info, caps, opts, transcode.Policy{HEVCEncoder: true, HEVCHardware: true})
}

func TestProfileDecisionPreservesHDROnHEVCDownscale(t *testing.T) {
	hdrPlus := true
	info := core.MediaInfo{
		Container:   "mkv",
		BitrateKbps: 24000,
		Streams: []core.StreamInfo{
			{
				Index:                      0,
				Type:                       "video",
				Codec:                      "hevc",
				Profile:                    "Main 10",
				Width:                      3840,
				Height:                     2160,
				BitDepth:                   10,
				PixelFormat:                "yuv420p10le",
				ColorPrimaries:             "bt2020",
				ColorTransfer:              "smpte2084",
				ColorSpace:                 "bt2020nc",
				DolbyVision:                true,
				DolbyVisionProfile:         8,
				DolbyVisionCompatibilityID: 1,
				HDR10Plus:                  &hdrPlus,
			},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
		},
	}
	profiles := &core.ClientCapabilityProfiles{
		DirectPlay: []core.ClientDirectPlayProfile{
			{MediaType: "video", Containers: []string{"mp4", "mkv"}, VideoCodecs: []string{"hevc", "h264"}, AudioCodecs: []string{"aac", "eac3"}},
		},
		Transcoding: []core.ClientTranscodeProfile{
			{MediaType: "video", Delivery: "hls", Containers: []string{"mp4"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "eac3"}},
		},
		Codecs: []core.ClientCodecProfile{
			{
				MediaType: "video",
				Codecs:    []string{"hevc"},
				Conditions: []core.ClientProfileCondition{
					{Property: "VideoProfile", Operator: "EqualsAny", Value: "main|main 10", Required: true},
					{Property: "VideoBitDepth", Operator: "LessThanEqual", Value: "10", Required: true},
					{Property: "VideoWidth", Operator: "LessThanEqual", Value: "1280", Required: true},
					{Property: "VideoRangeType", Operator: "EqualsAny", Value: "SDR|HDR10|HLG", Required: true},
				},
			},
		},
	}
	caps := core.ClientCaps{
		Product:     "iOS HEVC HDR Client",
		Containers:  []string{"mp4", "mkv"},
		VideoCodecs: []string{"h264", "hevc"},
		AudioCodecs: []string{"aac", "eac3"},
		Profiles:    profiles,
	}
	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Transcoder: hevcProfileTranscoder{}}}
	d, err := b.decideProfile(&core.PlaySession{Info: info}, frontend.PlanRequest{
		Caps:   caps,
		Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}},
	})
	if err != nil {
		t.Fatalf("decideProfile failed: %v", err)
	}
	if d.Kind != core.DecisionTranscode || d.VideoCopy || d.VideoCodec != "hevc" || d.ToneMap || d.MaxHeight != 720 {
		t.Fatalf("expected 720p HDR-preserving HEVC transcode, got %+v", d)
	}
}

// An Android TV profile that excludes only unsupported ranges (Wholphin /
// Jellyfin Android TV) must direct-play unexcluded Dolby Vision 8.1 instead
// of tripping Android's hdr10BaseReason and falling back to an H.264 encode.
func TestApplyProfileRangeCapsAndroidTVDolbyVision(t *testing.T) {
	no := false
	info := core.MediaInfo{
		Container:   "mkv",
		BitrateKbps: 24000,
		Streams: []core.StreamInfo{
			{
				Index:                      0,
				Type:                       "video",
				Codec:                      "hevc",
				Profile:                    "Main 10",
				Width:                      3840,
				Height:                     2160,
				FrameRate:                  24,
				BitDepth:                   10,
				PixelFormat:                "yuv420p10le",
				ColorPrimaries:             "bt2020",
				ColorTransfer:              "smpte2084",
				ColorSpace:                 "bt2020nc",
				DolbyVision:                true,
				DolbyVisionProfile:         8,
				DolbyVisionCompatibilityID: 1,
				DolbyVisionELPresent:       &no,
			},
			{Index: 1, Type: "audio", Codec: "truehd", Channels: 8, Default: true},
		},
	}
	makeCaps := func(excluded string) core.ClientCaps {
		var codecs []core.ClientCodecProfile
		if excluded != "" {
			codecs = []core.ClientCodecProfile{{
				MediaType:       "video",
				Codecs:          []string{"hevc"},
				ApplyConditions: []core.ClientProfileCondition{{Property: "VideoRangeType", Operator: "EqualsAny", Value: excluded}},
				Conditions:      []core.ClientProfileCondition{{Property: "VideoRangeType", Operator: "NotEquals", Value: excluded}},
			}}
		}
		return core.ClientCaps{
			Protocol:    "jellyfin",
			Product:     "Wholphin",
			Platform:    "Android",
			Device:      "SHIELD Android TV",
			Containers:  []string{"mkv", "mp4"},
			VideoCodecs: []string{"h264", "hevc"},
			AudioCodecs: []string{"aac", "ac3", "eac3", "truehd"},
			Profiles: &core.ClientCapabilityProfiles{
				DirectPlay:  []core.ClientDirectPlayProfile{{MediaType: "video", Containers: []string{"mkv"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3", "eac3", "truehd"}}},
				Codecs:      codecs,
				Transcoding: []core.ClientTranscodeProfile{{MediaType: "video", Delivery: "hls", Containers: []string{"ts"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"ac3", "aac"}, MaxAudioChannels: "6"}},
			},
		}
	}
	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Transcoder: profileTranscoder{}}}

	// Only HDR10Plus excluded: DV 8.1 stays direct play.
	d, err := b.decideProfile(&core.PlaySession{Info: info}, frontend.PlanRequest{
		Caps:   makeCaps("HDR10Plus"),
		Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}},
	})
	if err != nil || d.Kind != core.DecisionDirectPlay || !d.VideoCopy || !d.AudioCopy {
		t.Fatalf("DV-capable Android TV should direct-play DV 8.1, got %+v (err=%v)", d, err)
	}

	// DV explicitly excluded: profile does not claim DV and rejects DV direct play.
	noDVCaps := applyProfileRangeCaps(makeCaps("DOVI|DOVIWithHDR10"))
	if noDVCaps.DolbyVision || !noDVCaps.HDR10 {
		t.Fatalf("non-DV HDR10 TV caps = HDR10:%v DV:%v, want HDR10:true DV:false", noDVCaps.HDR10, noDVCaps.DolbyVision)
	}
}

// When direct play cannot be used (for example an audio-track switch that
// needs an audio transcode) and an earlier streaming profile is H.264-only
// while a later profile accepts HEVC, decideProfile must prefer copying the
// video over encoding it.
func TestDecideProfilePrefersVideoCopyAcrossTranscodingProfiles(t *testing.T) {
	info := core.MediaInfo{
		Container:   "mkv",
		BitrateKbps: 20000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, ColorTransfer: "smpte2084"},
			{Index: 1, Type: "audio", Codec: "truehd", Channels: 8},
			{Index: 2, Type: "audio", Codec: "dts", Channels: 6},
		},
	}
	profiles := &core.ClientCapabilityProfiles{
		DirectPlay: []core.ClientDirectPlayProfile{{MediaType: "video", Containers: []string{"mkv"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"truehd"}}},
		Transcoding: []core.ClientTranscodeProfile{
			{MediaType: "video", Delivery: "hls", Containers: []string{"ts"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"ac3", "aac"}, MaxAudioChannels: "6"},
			{MediaType: "video", Delivery: "hls", Containers: []string{"mp4"}, VideoCodecs: []string{"hevc", "h264"}, AudioCodecs: []string{"eac3", "ac3", "aac"}, MaxAudioChannels: "6"},
		},
	}
	caps := core.ClientCaps{
		Protocol:    "jellyfin",
		Product:     "Wholphin",
		Platform:    "Android",
		HDR10:       true,
		Containers:  []string{"mkv"},
		VideoCodecs: []string{"h264", "hevc"},
		AudioCodecs: []string{"truehd", "eac3", "ac3", "aac"},
		Profiles:    profiles,
	}
	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Transcoder: profileTranscoder{}}}
	d, err := b.decideProfile(&core.PlaySession{Info: info}, frontend.PlanRequest{
		Caps:   caps,
		Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 2, SubtitleStream: -1}},
	})
	if err != nil || d.Kind != core.DecisionRemux || !d.VideoCopy || d.VideoCodec != "hevc" || d.AudioCopy {
		t.Fatalf("audio switch across transcoding profiles = %+v (err=%v), want HEVC video copy + audio transcode", d, err)
	}
}
