package livetv

import (
	"net/url"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// Vela #49: a live start with client.maxBitrate starts at, and stays on, the
// provider variant within the cap; the viewer's master carries no higher
// variant for ABR to climb to, and an owned encode / relay is capped too.
func TestLiveClientBitrateCap(t *testing.T) {
	master := []byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1200000,RESOLUTION=640x360,CODECS="avc1.4d401e,mp4a.40.2"
low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720,CODECS="avc1.4d401f,mp4a.40.2"
mid.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=7500000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2"
high.m3u8
`)
	base, _ := url.Parse("https://provider.invalid/master.m3u8")
	capped := core.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, HLSContainers: []string{"fmp4"}, MaxHeight: 1080, MaxBitrateKbps: 3000}
	served := parseMasterPlaylist(selectedHLSMaster(master, base, capped), base)
	if len(served.variants) != 1 || served.variants[0].height != 720 || served.variants[0].bandwidth > 3_000_000 {
		t.Fatalf("capped master: %+v", served.variants)
	}
	// Without a bitrate the 1080p box alone keeps the top variant.
	if v, ok := pickVariant(parseMasterPlaylist(master, base), core.ClientCaps{VideoCodecs: []string{"h264"}, MaxHeight: 1080}); !ok || v.height != 1080 {
		t.Fatalf("decoder box treated as a bitrate cap: %+v", v)
	}
	if variantClass(capped) == variantClass(core.ClientCaps{VideoCodecs: []string{"h264"}, MaxHeight: 1080}) {
		t.Fatal("capped and uncapped viewers share one timeline")
	}
	if _, kbps := steadyRendition(transcode.RawFormat{Width: 1920, Height: 1080}, 6000, capped); kbps > 3000 {
		t.Fatalf("owned rendition above the cap: %d kbps", kbps)
	}
	// Both low height and low bitrate caps keep steadyRendition within both.
	if h, kbps := steadyRendition(transcode.RawFormat{Width: 1920, Height: 1080}, 6000, core.ClientCaps{MaxHeight: 480, MaxBitrateKbps: 1000}); h > 480 || kbps > 1000 {
		t.Fatalf("steady rendition exceeded 480p/1000k cap: %dp %d kbps", h, kbps)
	}

	// Owned IPTV relay converts a source above the client cap instead of copying it.
	svc := testService(t, Options{Transcoder: realDecideTranscoder{}})
	ss := &session{
		caps: core.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, HLSContainers: []string{"fmp4"}, MaxHeight: 720, MaxBitrateKbps: 3000},
		info: SessionInfo{Channel: Channel{ID: "ch1", Kind: "iptv"}},
	}
	src := core.MediaInfo{
		Container:   "mpegts",
		BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Type: "video", Index: 0, Codec: "h264", Width: 1920, Height: 1080, BitrateKbps: 7800},
			{Type: "audio", Index: 1, Codec: "aac", Channels: 2, BitrateKbps: 192},
		},
	}
	dec, _, _ := svc.relayPlan(ss, src)
	if dec.VideoCopy || dec.BitrateKbps <= 0 || dec.BitrateKbps > 3000 || (dec.MaxHeight > 0 && dec.MaxHeight > 720) {
		t.Fatalf("relay did not honour client cap: %+v", dec)
	}
}
