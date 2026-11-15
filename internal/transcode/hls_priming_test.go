package transcode

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/mediaproc"
)

// Exercise the nested muxer with real encoder delay, video reorder, and
// absolute-clock restarts. Check raw tfdt as well as the edited packet clock:
// ffprobe alone can hide wrapped decode times behind an edit list.
func TestHLSMuxedEncoderPriming(t *testing.T) {
	e := newRealEnv(t, WithoutHWDetection())
	for _, bframes := range []int{0, 2} {
		source := filepath.Join(e.dir, fmt.Sprintf("source-%d.mkv", bframes))
		args := []string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=size=64x64:rate=24", "-f", "lavfi", "-i", "sine=sample_rate=48000", "-t", "6", "-c:v", "libx264", "-preset", "ultrafast", "-threads", "1", "-bf", strconv.Itoa(bframes), "-g", "24", "-c:a", "ac3", source}
		if out, err := mediaproc.CommandContext(t.Context(), 30*time.Second, e.ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
		info, err := e.m.Probe(t.Context(), source)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name, container, codec string
			copyVideo, rtx         bool
		}{
			{"copy-aac", ContainerFMP4, "aac", true, false},
			{"encode-aac", ContainerFMP4, "aac", false, false},
			{"copy-opus", ContainerFMP4, "opus", true, false},
			{"ts-aac", ContainerMPEGTS, "aac", true, false},
			{"ts-rtx-clock-aac", ContainerMPEGTS, "aac", true, true},
		} {
			t.Run(fmt.Sprintf("b%d-%s", bframes, tc.name), func(t *testing.T) {
				var retained map[string]trackEdit
				for _, start := range []time.Duration{0, 2 * time.Second, 4 * time.Second} {
					dir := t.TempDir()
					s := runSpec{Input: source, Info: *info, Protocol: core.ProtocolHLS, Decision: core.Decision{Container: tc.container, VideoCopy: tc.copyVideo, VideoCodec: "h264", AudioCodec: tc.codec, AudioStream: 1, AudioChannels: 2, AudioSampleRate: 48000, SubtitleStream: -1}, Start: start, SegDur: 250 * time.Millisecond, Dir: dir, Playlist: "index.m3u8", InitName: "init.mp4", RTXTimeline: tc.rtx}
					args := buildArgs(s)
					if out, err := mediaproc.CommandContext(t.Context(), 30*time.Second, e.ffmpeg, args...).CombinedOutput(); err != nil {
						t.Fatalf("mux %v: %v %s", start, err, out)
					}
					ext := segmentExt(tc.container)
					files, err := filepath.Glob(filepath.Join(dir, "*."+ext))
					if err != nil || len(files) == 0 {
						t.Fatalf("segments: %v %v", files, err)
					}
					var combined []byte
					if tc.container == ContainerFMP4 {
						r := &run{spec: s}
						if err := prepareRunEditsForPath(r, retained, files[0]); err != nil {
							t.Fatal(err)
						}
						for _, f := range files {
							if err := r.edits.shift(f); err != nil {
								t.Fatal(err)
							}
						}
						retained, err = checkPublishedEdits(retained, filepath.Join(dir, s.InitName))
						if err != nil {
							t.Fatal(err)
						}
						combined, err = os.ReadFile(filepath.Join(dir, s.InitName))
						if err != nil {
							t.Fatal(err)
						}
						for _, f := range files {
							b, err := os.ReadFile(f)
							if err != nil {
								t.Fatal(err)
							}
							checkPrimingTFDT(t, b)
						}
					}
					for _, f := range files {
						b, err := os.ReadFile(f)
						if err != nil {
							t.Fatal(err)
						}
						combined = append(combined, b...)
					}
					capture := filepath.Join(dir, "capture."+map[bool]string{true: "mp4", false: "ts"}[tc.container == ContainerFMP4])
					if err := os.WriteFile(capture, combined, 0600); err != nil {
						t.Fatal(err)
					}
					out, err := mediaproc.CommandContext(t.Context(), 30*time.Second, e.ffprobe, "-v", "error", "-show_packets", "-of", "json", capture).Output()
					if err != nil {
						t.Fatal(err)
					}
					var result struct {
						Packets []struct {
							CodecType string `json:"codec_type"`
							PTS       string `json:"pts_time"`
							DTS       string `json:"dts_time"`
							Duration  string `json:"duration_time"`
						}
					}
					if err := json.Unmarshal(out, &result); err != nil {
						t.Fatal(err)
					}
					first, end := map[string]float64{}, map[string]float64{}
					for _, p := range result.Packets {
						pts, err := strconv.ParseFloat(p.PTS, 64)
						if err != nil {
							t.Fatal(err)
						}
						dts, err := strconv.ParseFloat(p.DTS, 64)
						if err != nil {
							t.Fatal(err)
						}
						duration, err := strconv.ParseFloat(p.Duration, 64)
						if err != nil {
							t.Fatal(err)
						}
						// TS encodes negative times modulo 2^33; both demuxed negatives and
						// a raw-wrap-sized positive timestamp must fail this bound.
						if pts < -0.000001 || pts > 10 || dts < -0.000001 || dts > 10 {
							t.Fatalf("invalid %s packet pts=%f dts=%f", p.CodecType, pts, dts)
						}
						if v, ok := first[p.CodecType]; !ok || pts < v {
							first[p.CodecType] = pts
						}
						end[p.CodecType] = math.Max(end[p.CodecType], pts+duration)
					}
					a, aok := first["audio"]
					v, vok := first["video"]
					frame := 1024.0 / 48000
					if tc.codec == "opus" {
						frame = 0.020
					}
					if !aok || !vok {
						t.Fatalf("missing A/V: %v", first)
					}
					if start == 0 && math.Abs(a-v) > frame+0.001 {
						t.Fatalf("start %v A/V %v", start, first)
					}
					// Restart runs intentionally include decoder/encoder preroll.
					// Compare the ends on the title clock, rather than mistaking
					// the earlier decode-only video keyframe for an A/V offset.
					if math.Abs(end["audio"]-end["video"]) > frame+1.0/24+0.001 {
						t.Fatalf("drift start=%v end=%v", first, end)
					}
					if tc.container == ContainerFMP4 && start > 0 {
						// libx264 retains reorder preroll; AC3 input seeking rounds
						// to 1536-sample packets. Both remain on the absolute clock.
						expected := start.Seconds()
						if !tc.copyVideo {
							// The restart cut is on the unshifted sample grid;
							// every run uses the same planned mux-clock origin.
							expected = hlsAudioRestartCut(s) + hlsEncodedClockLead(s).Seconds()
						}
						if math.Abs(a-expected) > 1536.0/48000+frame {
							t.Fatalf("absolute audio clock lost: %v %v expected=%f", start, first, expected)
						}
					}
					if tc.container == ContainerFMP4 && (v < start.Seconds()-1.01 || v > start.Seconds()+0.15) {
						t.Fatalf("absolute clock lost: %v %v", start, first)
					}
					t.Logf("start=%v packets first=%v end=%v", start, first, end)
				}
			})
		}
	}
}

func checkPrimingTFDT(t *testing.T, b []byte) {
	t.Helper()
	top, err := readBoxes(b, 0, len(b))
	if err != nil {
		t.Fatal(err)
	}
	for _, moof := range top {
		if moof.typ != "moof" {
			continue
		}
		trafs, err := readBoxes(b, moof.start+moof.hdr, moof.end)
		if err != nil {
			t.Fatal(err)
		}
		for _, traf := range trafs {
			if traf.typ != "traf" {
				continue
			}
			box, ok := child(b, traf, "tfdt")
			if !ok {
				t.Fatal("missing tfdt")
			}
			p := box.payload(b)
			var ticks uint64
			if len(p) >= 12 && p[0] == 1 {
				ticks = binary.BigEndian.Uint64(p[4:])
			} else if len(p) >= 8 && p[0] == 0 {
				ticks = uint64(binary.BigEndian.Uint32(p[4:]))
			} else {
				t.Fatal("invalid tfdt")
			}
			if ticks > 10*48000 {
				t.Fatalf("wrapped/out of range raw tfdt: %d", ticks)
			}
		}
	}
}
