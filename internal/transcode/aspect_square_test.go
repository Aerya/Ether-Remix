package transcode

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestIntegrationTranscodeSquarePixels pins W162: a transcoded picture is
// square-pixel, sized by its display aspect, and the DASH manifest (the
// Samsung TV path) reports that same size. Before the fix an anamorphic
// source kept its sample aspect on a coded-size scale (720x480 SAR 32:27
// became 540x360 SAR 32:27) while the manifest said only 540x360, so a
// player sizing from the manifest showed 3:2 instead of 16:9; a scaled
// square source also carried a near-1 SAR from even-width rounding.
func TestIntegrationTranscodeSquarePixels(t *testing.T) {
	cases := []struct {
		name, vf string
		wantW    int
		wantH    int
		wantDAR  string
	}{
		{"scope239", "scale=1280:536,setsar=1", 860, 360, "43:18"},
		{"hd169", "scale=1280:720,setsar=1", 640, 360, "16:9"},
		{"anamorphic169", "scale=720:480,setsar=32/27", 640, 360, "16:9"},
	}
	e := newRealEnv(t, WithoutHWDetection())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := tc.name + ".mkv"
			e.gen(t, file, 6, "-map", "0:v", "-map", "1:a", "-vf", tc.vf, "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac")
			ctx := context.Background()
			info, err := e.m.Probe(ctx, e.url(file))
			if err != nil {
				t.Fatal(err)
			}
			d := core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 360, Container: ContainerFMP4, AudioStream: 1, SubtitleStream: -1, BitrateKbps: 2000}
			if w, h := DecisionOutputSize(info.Video(), d); w != tc.wantW || h != tc.wantH {
				t.Fatalf("DecisionOutputSize = %dx%d, want %dx%d", w, h, tc.wantW, tc.wantH)
			}
			ds := startDASH(t, e.m, core.HLSRequest{SessionID: "square-" + tc.name, InputURL: e.url(file), Info: *info, Decision: d})
			defer ds.Close()
			doc := dashManifest(t, ds)
			mpdW, mpdH := 0, 0
			for _, s := range doc.Period.Sets {
				for _, r := range s.Reps {
					if r.MimeType == "video/mp4" {
						mpdW, mpdH = r.Width, r.Height
					}
				}
			}
			if mpdW != tc.wantW || mpdH != tc.wantH {
				t.Fatalf("MPD video Representation = %dx%d, want %dx%d", mpdW, mpdH, tc.wantW, tc.wantH)
			}
			path := e.writeTemp(t, "square-"+tc.name+".mp4", append(dashGet(t, ds, 0, InitSegment), dashGet(t, ds, 0, 0)...))
			out, err := exec.CommandContext(ctx, e.ffprobe, "-v", "error", "-select_streams", "v:0",
				"-show_entries", "stream=width,height,sample_aspect_ratio,display_aspect_ratio", "-of", "default=nw=1", path).CombinedOutput()
			if err != nil {
				t.Fatalf("ffprobe: %v %s", err, out)
			}
			fields := map[string]string{}
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok {
					fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			if fields["sample_aspect_ratio"] != "1:1" {
				t.Errorf("output SAR = %q, want 1:1 (probe: %s)", fields["sample_aspect_ratio"], out)
			}
			if fields["display_aspect_ratio"] != tc.wantDAR {
				t.Errorf("output DAR = %q, want %q", fields["display_aspect_ratio"], tc.wantDAR)
			}
			if w, _ := strconv.Atoi(fields["width"]); w != tc.wantW {
				t.Errorf("output width = %d, want %d", w, tc.wantW)
			}
			if h, _ := strconv.Atoi(fields["height"]); h != tc.wantH {
				t.Errorf("output height = %d, want %d", h, tc.wantH)
			}
		})
	}
}
