package transcode

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// A demuxed HLS source whose audio starts after the HLS muxer's first cut
// (a lagging audio playlist, an ad break) makes FFmpeg write the fMP4 init
// before any AAC packet: without the session completing it, the init has no
// AudioSpecificConfig and MSE players refuse it. Every published init must
// carry every track's decoder configuration.
func TestIntegrationLateAudioFMP4InitCarriesAACConfig(t *testing.T) {
	e := newRealEnv(t, WithoutHWDetection())
	ff := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), e.ffmpeg, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...)
		cmd.Dir = e.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v\n%s", err, out)
		}
	}
	ff("-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-t", "16", "-c:v", "libx264", "-preset", "ultrafast",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-an", "-f", "hls", "-hls_time", "2", "-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(e.dir, "v%03d.ts"), filepath.Join(e.dir, "v.m3u8"))
	// Mono LC 48 kHz, starting 6 s into the video's timeline.
	ff("-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "10", "-c:a", "aac", "-ac", "1",
		"-output_ts_offset", "6", "-f", "hls", "-hls_time", "2", "-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(e.dir, "a%03d.ts"), filepath.Join(e.dir, "a.m3u8"))
	e.writeTemp(t, "master.m3u8", []byte("#EXTM3U\n"+
		"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=\"en\",DEFAULT=YES,AUTOSELECT=YES,URI=\"a.m3u8\"\n"+
		"#EXT-X-STREAM-INF:BANDWIDTH=2000000,CODECS=\"avc1.64001e,mp4a.40.2\",AUDIO=\"aud\"\nv.m3u8\n"))

	ctx := context.Background()
	info, err := e.m.ProbeLive(ctx, e.url("master.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	audio := -1
	for _, st := range info.Streams {
		if st.Type == "audio" {
			audio = st.Index
		}
	}
	if info.Container != "hls" || audio < 0 {
		t.Fatalf("fixture probe: %+v", info)
	}
	hs, err := e.m.StartHLS(ctx, core.HLSRequest{SessionID: "late-audio", LiveInput: true, InputURL: e.url("master.m3u8"), Info: *info,
		Decision: core.Decision{Kind: core.DecisionRemux, Container: ContainerFMP4, VideoCopy: true, AudioCopy: true,
			VideoCodec: "h264", AudioCodec: "aac", AudioStream: audio}})
	if err != nil {
		t.Fatal(err)
	}
	init := fetch(t, hs, InitSegment)
	if err := CheckInitCodecConfig(init); err != nil {
		t.Fatalf("published init: %v", err)
	}
	// The configuration aac_adtstoasc derives: AAC LC, 48 kHz, mono.
	if !bytes.Contains(init, []byte{0x05, 0x80, 0x80, 0x80, 0x02, 0x11, 0x88}) {
		t.Fatalf("init lacks the source's AudioSpecificConfig 1188: % x", init)
	}
	seg := fetch(t, hs, 1)
	ps := e.probeFile(t, e.writeTemp(t, "late.mp4", append(append([]byte{}, init...), seg...)))
	var codecs []string
	for _, s := range ps {
		codecs = append(codecs, s.CodecType+":"+s.CodecName)
	}
	if !slices.Contains(codecs, "audio:aac") || !slices.Contains(codecs, "video:h264") {
		t.Fatalf("init+segment streams %v", codecs)
	}
}
