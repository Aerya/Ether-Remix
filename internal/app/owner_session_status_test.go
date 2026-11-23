package app

import (
	"net/netip"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
)

func TestOwnerSessionDetails(t *testing.T) {
	restore := clientnet.OverrideHostPrefixes([]netip.Prefix{
		netip.MustParsePrefix("2001:db8:abcd:1::/64"),
	})
	t.Cleanup(restore)

	p := &core.PlaySession{Key: core.MovieKey(1), Client: core.ClientCaps{ClientIP: "192.0.2.1", Remote: true}, Decision: core.DecisionTranscode, Info: core.MediaInfo{Container: "mkv", Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Height: 2160, ColorTransfer: "smpte2084"}, {Index: 1, Type: "audio", Codec: "eac3", Channels: 6, ChannelLayout: "5.1"}, {Index: 2, Type: "subtitle", Codec: "ass", Language: "eng"}}}, DeliveryDecision: &core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", MaxHeight: 1080, ToneMap: true, AudioCodec: "aac", AudioChannels: 2, AudioStream: 1, SubtitleStream: 2, BurnSubtitles: true, Container: "mpegts", BitrateKbps: 5000}}
	got := ownerSessionDetails(p)
	if got.IP != "192.0.2.1" || got.NetworkReason != "Remote network (source 192.0.2.1)" || got.SourceVideo != "HEVC 2160p HDR10" || got.OutputVideo != "H.264 1080p SDR" || got.SourceAudio != "EAC3 5.1" || got.OutputAudio != "AAC 2.0" || got.Subtitle != "eng ASS burned" || got.Container != "mpegts" || got.BitrateKbps != 5000 || got.RatingKey != p.Key {
		t.Fatalf("details: %+v", got)
	}
	lan := ownerSessionDetails(&core.PlaySession{Client: core.ClientCaps{ClientIP: "192.168.3.10", Remote: false}})
	if lan.NetworkReason != "Home network (source 192.168.3.10)" {
		t.Fatalf("LAN reason = %q, want %q", lan.NetworkReason, "Home network (source 192.168.3.10)")
	}
	lanV6 := ownerSessionDetails(&core.PlaySession{Client: core.ClientCaps{ClientIP: "2001:db8:abcd:1::42", Remote: false}})
	if lanV6.NetworkReason != "Home network (source 2001:db8:abcd:1::42, local subnet 2001:db8:abcd:1::/64)" {
		t.Fatalf("IPv6 host subnet reason = %q", lanV6.NetworkReason)
	}
	p.DeliveryDecision.VideoCopy = true
	p.DeliveryDecision.AudioCopy = true
	p.Decision = core.DecisionRemux
	got = ownerSessionDetails(p)
	if got.OutputVideo != got.SourceVideo || got.OutputAudio != got.SourceAudio {
		t.Fatalf("copy: %+v", got)
	}
}

func TestOwnerSessionBitrateIsSourceNotDeliverySpeed(t *testing.T) {
	burst := &core.PlaySession{Decision: core.DecisionDirectPlay, SpeedBps: 44_625_000, Info: core.MediaInfo{Container: "mkv", Size: 4_500_000_000, Duration: 2 * time.Hour}}
	if got := ownerSessionDetails(burst).BitrateKbps; got != 5000 {
		t.Fatalf("direct play bitrate = %d kbps, want 5000 from size/duration", got)
	}
	probed := &core.PlaySession{Decision: core.DecisionDirectPlay, Info: core.MediaInfo{BitrateKbps: 12000, Size: 1, Duration: time.Hour}}
	if got := ownerSessionDetails(probed).BitrateKbps; got != 12000 {
		t.Fatalf("probed bitrate = %d kbps, want 12000", got)
	}
	idle := &core.PlaySession{Decision: core.DecisionDirectPlay, Info: core.MediaInfo{BitrateKbps: 8000}}
	if got := ownerSessionDetails(idle).BitrateKbps; got != 8000 {
		t.Fatalf("idle buffer bitrate = %d kbps, want 8000", got)
	}
}

func TestOwnerSessionDirectPlayAudioFallback(t *testing.T) {
	p := &core.PlaySession{
		Decision: core.DecisionDirectPlay,
		Info: core.MediaInfo{
			Container: "mkv",
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "hevc", Height: 2160, ColorTransfer: "smpte2084"},
				{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
				{Index: 2, Type: "audio", Codec: "truehd", Channels: 8, ChannelLayout: "7.1", Default: true},
			},
		},
	}
	got := ownerSessionDetails(p)
	if got.SourceAudio != "TRUEHD 7.1" || got.OutputAudio != "TRUEHD 7.1" {
		t.Fatalf("direct play audio fallback: got source=%q output=%q, want TRUEHD 7.1", got.SourceAudio, got.OutputAudio)
	}
}
