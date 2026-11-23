package transcode

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Segmented transport must support a codec as well as the client decoder.
// TrueHD/MLP stays copyable for files, but must convert for Matroska HLS.
func TestMatroskaHLSCopiesAudioTheTargetAccepts(t *testing.T) {
	for _, codec := range []string{"dts", "truehd", "mlp"} {
		info := mustParse(t, "mkv_hevc_hdr10.json")
		for i := range info.Streams {
			if info.Streams[i].Type == "audio" {
				info.Streams[i].Codec = codec
				info.Streams[i].Channels = 8
			}
		}
		opts := allow()
		opts.DirectPlay = false
		caps := appleTV
		caps.AudioCodecs = []string{"aac", "ac3", "eac3", "dts", "truehd", "mlp"}
		caps.HLSContainers = []string{ContainerMatroska}
		wantCopy := codec == "dts"
		d := Decide(info, caps, opts, Policy{})
		if d.AudioCopy != wantCopy || !d.VideoCopy || d.Container != ContainerMatroska || (wantCopy && d.AudioCodec != codec) || (!wantCopy && d.AudioCodec != "eac3") {
			t.Fatalf("%s into Matroska HLS: %+v", codec, d)
		}
		if got := SegmentedAudioCopyOK(caps, codec); got != wantCopy {
			t.Fatalf("%s late segmented handoff: copy=%v", codec, got)
		}
		if !wantCopy {
			args := buildArgs(baseSpec(info, d))
			if argValue(args, "-ac") != "6" || argValue(args, "-c:a") != "eac3" {
				t.Fatalf("%s lost surround conversion: %v", codec, args)
			}
		}
		if d := Decide(info, caps, allow(), Policy{}); d.Kind != core.DecisionDirectPlay || !d.AudioCopy {
			t.Fatalf("%s original direct play changed: %+v", codec, d)
		}
		fileOpts := opts
		fileOpts.Protocol = core.ProtocolHTTP
		if d := Decide(info, caps, fileOpts, Policy{}); !d.AudioCopy || d.AudioCodec != codec {
			t.Fatalf("%s progressive Matroska changed: %+v", codec, d)
		}
		caps.HLSContainers = []string{ContainerFMP4}
		if d := Decide(info, caps, opts, Policy{}); d.AudioCopy {
			t.Fatalf("%s copied into fMP4 HLS: %+v", codec, d)
		}
		caps.HLSContainers = []string{ContainerMatroska}
		caps.TranscodeAudioCodecs = []string{"aac", "ac3", "eac3"}
		if d := Decide(info, caps, opts, Policy{}); d.AudioCopy {
			t.Fatalf("%s copied although the target omits it: %+v", codec, d)
		}
	}
}
