package sessions

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
)

// Swiftfin's AVPlayer profile (MediaPlayerType+AVPlayer.swift): HEVC is
// accepted only with an hvc1 or dvh1 sample entry, a required condition.
const swiftfinAVPlayerProfile = `{
	"DirectPlayProfiles": [
		{"Type": "Video", "Container": "mp4,m4v", "VideoCodec": "h264,mpeg4,hevc", "AudioCodec": "aac,ac3,alac,eac3,flac"},
		{"Type": "Video", "Container": "mov", "VideoCodec": "h264,mjpeg,mpeg4,hevc", "AudioCodec": "aac,ac3,alac,eac3,mp3,pcm_s16be,pcm_s16le,pcm_s24be,pcm_s24le"}
	],
	"TranscodingProfiles": [
		{"Type": "Video", "Container": "mp4", "Protocol": "hls", "Context": "Streaming", "VideoCodec": "hevc,h264,mpeg4", "AudioCodec": "aac,ac3,alac,eac3,flac", "MaxAudioChannels": "8", "MinSegments": 2, "BreakOnNonKeyFrames": true, "EnableSubtitlesInManifest": true}
	],
	"CodecProfiles": [
		{"Type": "Video", "Codec": "h264", "Conditions": [
			{"Condition": "EqualsAny", "Property": "VideoRangeType", "Value": "SDR|DOVIWithSDR", "IsRequired": false},
			{"Condition": "NotEquals", "Property": "IsInterlaced", "Value": "true", "IsRequired": false}
		]},
		{"Type": "Video", "Codec": "hevc", "Conditions": [
			{"Condition": "NotEquals", "Property": "IsAnamorphic", "Value": "true", "IsRequired": false},
			{"Condition": "EqualsAny", "Property": "VideoProfile", "Value": "main|main 10", "IsRequired": false},
			{"Condition": "LessThanEqual", "Property": "VideoLevel", "Value": "175", "IsRequired": false},
			{"Condition": "NotEquals", "Property": "IsInterlaced", "Value": "true", "IsRequired": false},
			{"Condition": "EqualsAny", "Property": "VideoRangeType", "Value": "SDR|DOVIWithSDR|HLG|DOVIWithHLG|HDR10|HDR10Plus|DOVIWithHDR10|DOVIWithHDR10Plus|DOVIWithEL|DOVIWithELHDR10Plus|DOVI", "IsRequired": false},
			{"Condition": "EqualsAny", "Property": "VideoCodecTag", "Value": "hvc1|dvh1", "IsRequired": true},
			{"Condition": "LessThanEqual", "Property": "VideoFramerate", "Value": "60", "IsRequired": true}
		]}
	],
	"SubtitleProfiles": [
		{"Format": "cc_dec", "Method": "Embed"}, {"Format": "ttml", "Method": "Embed"},
		{"Format": "pgssub", "Method": "Encode"}, {"Format": "vtt", "Method": "Hls"}
	]
}`

// An HDR10 HEVC MKV has no sample entry tag. Segmented into fMP4 it is
// written as hvc1, so it satisfies Swiftfin's required VideoCodecTag and is
// copied, not re-encoded to SDR H.264.
func TestSwiftfinAVPlayerCopiesHEVCIntoFMP4(t *testing.T) {
	var profile caps.DeviceProfile
	if err := json.Unmarshal([]byte(swiftfinAVPlayerProfile), &profile); err != nil {
		t.Fatal(err)
	}
	client, err := (caps.Builder{}).Build(context.Background(), caps.DeviceInfo{Product: "Swiftfin tvOS", Platform: "tvOS", Device: "Apple TV"}, profile, nil, "Swiftfin/1.7.1")
	if err != nil {
		t.Fatal(err)
	}
	no := false
	hdr10 := core.StreamInfo{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", Level: 153, BitDepth: 10, PixelFormat: "yuv420p10le",
		ColorTransfer: "smpte2084", ColorPrimaries: "bt2020", ColorSpace: "bt2020nc", Width: 3840, Height: 2160, FrameRate: 23.976,
		DolbyVisionConfigurationPresent: &no, HDR10Plus: &no}
	info := core.MediaInfo{Container: "mkv", BitrateKbps: 40000, Streams: []core.StreamInfo{
		hdr10,
		{Index: 1, Type: "audio", Codec: "dts", Channels: 6, SampleRate: 48000},
	}}
	broker := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Transcoder: profileTranscoder{}}}
	req := frontend.PlanRequest{Protocol: "jellyfin", Caps: client, Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}}}
	d, err := broker.decideProfile(&core.PlaySession{Info: info}, req)
	if err != nil {
		t.Fatal(err)
	}
	if !d.VideoCopy || d.VideoCodec != "hevc" || d.ToneMap || d.Container != "fmp4" {
		t.Fatalf("HDR10 HEVC MKV: %s (%+v)", d.Reason, d)
	}
	// A source already tagged hev1 is rewritten to hvc1 as well.
	hev1 := info
	hev1.Container = "mp4"
	hev1.Streams = []core.StreamInfo{hdr10, info.Streams[1]}
	hev1.Streams[0].CodecTag = "hev1"
	if d, err := broker.decideProfile(&core.PlaySession{Info: hev1}, req); err != nil || !d.VideoCopy || d.VideoCodec != "hevc" {
		t.Fatalf("hev1 HEVC: %v %s (%+v)", err, d.Reason, d)
	}
}
