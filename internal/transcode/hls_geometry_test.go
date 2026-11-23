package transcode

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Apple's HLS authoring spec (9.2, 9.15) requires RESOLUTION and FRAME-RATE on
// every video variant. tvOS AVPlayer enforces it for HDR: a Dolby Vision 8.1
// copy master without them was fetched and abandoned, never reaching the
// variant playlist, while the same title transcoded to SDR played.
func TestHLSVariantGeometry(t *testing.T) {
	dv := core.StreamInfo{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 1600, FrameRate: 23.976}
	info := core.MediaInfo{Streams: []core.StreamInfo{dv, {Index: 1, Type: "audio", Codec: "eac3"}}}
	cases := []struct {
		name string
		info core.MediaInfo
		d    core.Decision
		want string
	}{
		{"copy keeps source geometry", info, core.Decision{VideoCopy: true, VideoCodec: "hevc"}, ",RESOLUTION=3840x1600,FRAME-RATE=23.976"},
		{"copy ignores a height cap", info, core.Decision{VideoCopy: true, VideoCodec: "hevc", MaxHeight: 1080}, ",RESOLUTION=3840x1600,FRAME-RATE=23.976"},
		{"transcode follows the encoded size", info, core.Decision{VideoCodec: "h264", MaxHeight: 1080}, ",RESOLUTION=2592x1080,FRAME-RATE=23.976"},
		{"unknown frame rate leaves FRAME-RATE out", core.MediaInfo{Streams: []core.StreamInfo{{Type: "video", Codec: "hevc", Width: 1920, Height: 1080}}}, core.Decision{VideoCopy: true}, ",RESOLUTION=1920x1080"},
		{"unknown size writes nothing", core.MediaInfo{Streams: []core.StreamInfo{{Type: "video", Codec: "hevc", FrameRate: 25}}}, core.Decision{VideoCopy: true}, ""},
		{"audio only writes nothing", core.MediaInfo{Streams: []core.StreamInfo{{Type: "audio", Codec: "aac"}}}, core.Decision{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HLSVariantGeometry(tc.info, tc.d); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
