package transcode

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/mediaproc"
)

// The client rebases the first video PTS (including MP4 edits) to playlist
// zero, then adds TimelineOrigin. Long-GOP preroll and encoder delay must
// agree with that mapping, independently of the requested playhead.
func TestContinuousCopyPresentationOrigin(t *testing.T) {
	e := newRealEnv(t, WithoutHWDetection())
	e.gen(t, "origin.mkv", 16, "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-bf", "2", "-g", "192", "-keyint_min", "192", "-sc_threshold", "0", "-c:a", "ac3")
	e.gen(t, "origin-aac.mkv", 16, "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-bf", "2", "-g", "192", "-keyint_min", "192", "-sc_threshold", "0", "-c:a", "aac")
	aacInfo, err := e.m.Probe(t.Context(), e.url("origin-aac.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := e.m.Probe(t.Context(), e.url("origin.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	sourceKeys := map[string]map[string]string{}
	for _, source := range []string{"origin.mkv", "origin-aac.mkv"} {
		out, err := mediaproc.CommandContext(t.Context(), 20*time.Second, e.ffprobe, "-v", "error", "-select_streams", "v:0", "-show_packets", "-show_data_hash", "sha256", "-of", "json", filepath.Join(e.dir, source)).Output()
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Packets []struct {
				PTS   string `json:"pts_time"`
				Flags string `json:"flags"`
				Hash  string `json:"data_hash"`
			}
		}
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatal(err)
		}
		sourceKeys[source] = map[string]string{}
		for _, p := range result.Packets {
			if strings.HasPrefix(p.Flags, "K") {
				sourceKeys[source][p.PTS] = p.Hash
			}
		}
	}
	probe := func(name string, data []byte) (map[string]float64, map[string]float64) {
		t.Helper()
		file := filepath.Join(e.dir, name+".mp4")
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		out, err := mediaproc.CommandContext(t.Context(), 20*time.Second, e.ffprobe, "-v", "error", "-show_packets", "-show_data_hash", "sha256", "-of", "json", file).Output()
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Packets []struct {
				Kind string `json:"codec_type"`
				PTS  string `json:"pts_time"`
				DTS  string `json:"dts_time"`
				Hash string `json:"data_hash"`
			}
		}
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatal(err)
		}
		tracks, err := parseInitTracks(data)
		if err != nil {
			t.Fatal(err)
		}
		for _, track := range tracks {
			t.Logf("%s edit handler=%s shift=%.6f seconds", name, track.Handler, float64(track.EditShift)/float64(track.Timescale))
		}
		pts, dts := map[string]float64{}, map[string]float64{}
		for _, p := range result.Packets {
			if _, ok := pts[p.Kind]; !ok {
				if p.Kind == "video" && name != "baseline" {
					source, expected := "origin.mkv", "0.000000"
					if strings.HasPrefix(name, "true") {
						source = "origin-aac.mkv"
					}
					if strings.HasSuffix(name, "12s") {
						expected = "8.000000"
					}
					if want := sourceKeys[source][expected]; want == "" || p.Hash != want {
						t.Fatalf("first output picture is not source keyframe %s at %s: hash=%s want=%s", source, expected, p.Hash, want)
					}
				}
				pts[p.Kind], _ = strconv.ParseFloat(p.PTS, 64)
				dts[p.Kind], _ = strconv.ParseFloat(p.DTS, 64)
			}
		}
		if len(pts) != 2 {
			t.Fatalf("missing A/V: %v", pts)
		}
		return pts, dts
	}
	decision := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, VideoCodec: "h264", AudioCodec: "aac", AudioStream: 1, AudioChannels: 2, AudioSampleRate: 48000, SubtitleStream: -1, Container: ContainerFMP4}
	// Capture the actual production args before the continuous-origin policy.
	dir := t.TempDir()
	old := runSpec{Input: e.url("origin.mkv"), Info: *info, Decision: decision, Protocol: core.ProtocolHLS, Start: 4 * time.Second, SegDur: 4 * time.Second, Dir: dir, Playlist: "index.m3u8", InitName: "init.mp4"}
	if out, err := mediaproc.CommandContext(t.Context(), 30*time.Second, e.ffmpeg, buildArgs(old)...).CombinedOutput(); err != nil {
		t.Fatalf("baseline: %v %s", err, out)
	}
	init, err := os.ReadFile(filepath.Join(dir, "init.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	seg, err := os.ReadFile(filepath.Join(dir, "00000.m4s"))
	if err != nil {
		t.Fatal(err)
	}
	pts, dts := probe("baseline", append(init, seg...))
	t.Logf("BEFORE requested=4 video PTS=%f DTS=%f audio PTS=%f DTS=%f args=%v", pts["video"], dts["video"], pts["audio"], dts["audio"], buildArgs(old))
	for _, audioCopy := range []bool{false, true} {
		for _, requested := range []time.Duration{4 * time.Second, 12 * time.Second} {
			name := strconv.FormatBool(audioCopy) + requested.String()
			d := decision
			d.AudioCopy = audioCopy
			if audioCopy {
				d.AudioCodec = "aac"
			}
			source, media := e.url("origin.mkv"), info
			if audioCopy {
				source, media = e.url("origin-aac.mkv"), aacInfo
			}
			hs, err := e.m.StartHLS(t.Context(), core.HLSRequest{SessionID: name, InputURL: source, Info: *media, Decision: d, StartOffset: requested, Continuous: true})
			if err != nil {
				t.Fatal(err)
			}
			s := hs.(*Session)
			func() {
				defer s.Close()
				origin := s.TimelineOrigin()
				want := requested - 4*time.Second
				if math.Abs((origin - want).Seconds()) > .05 {
					t.Fatalf("origin %v, want near %v", origin, want)
				}
				w := httptest.NewRecorder()
				s.ServePlaylist(w, httptest.NewRequest("GET", "/index.m3u8", nil))
				var span float64
				for _, line := range strings.Split(w.Body.String(), "\n") {
					if strings.HasPrefix(line, "#EXTINF:") {
						n, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ","), 64)
						span += n
					}
				}
				if math.Abs(span-(media.Duration-origin).Seconds()) > .002 {
					t.Fatalf("playlist span %f origin %v duration %v", span, origin, media.Duration)
				}
				init := fetch(t, s, InitSegment)
				seg := fetch(t, s, 0)
				pts, dts := probe(name, append(init, seg...))
				if math.Abs(pts["audio"]-pts["video"]) > .065 {
					t.Fatalf("audio misses copy origin: PTS %v", pts)
				}
				t.Logf("AFTER copyAudio=%v requested=%v origin=%v span=%.6f first video PTS=%.6f DTS=%.6f audio PTS=%.6f DTS=%.6f", audioCopy, requested, origin, span, pts["video"], dts["video"], pts["audio"], dts["audio"])
			}()
		}
	}
	// One GOP is the stream-copy floor. Compare a 2s-GOP source against the
	// old 4s packager, at both start and a fresh seek generation. Encodes can
	// force the 0.5/0.5/1s opening cuts on the same continuous clock.
	e.gen(t, "opening.mkv", 12, "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-c:a", "aac")
	openingInfo, err := e.m.Probe(t.Context(), e.url("opening.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, copyVideo := range []bool{true, false} {
		for _, at := range []time.Duration{0, 4 * time.Second} {
			name := "opening-" + strconv.FormatBool(copyVideo) + at.String()
			d := decision
			d.VideoCopy = copyVideo
			d.AudioCopy = copyVideo
			d.MaxHeight = 180
			d.BitrateKbps = 1000
			dir := t.TempDir()
			spec := runSpec{Input: e.url("opening.mkv"), Info: *openingInfo, Decision: d, Protocol: core.ProtocolHLS, Start: at, SegDur: 4 * time.Second, Dir: dir, Playlist: "index.m3u8", InitName: "init.mp4"}
			if out, err := mediaproc.CommandContext(t.Context(), 30*time.Second, e.ffmpeg, buildArgs(spec)...).CombinedOutput(); err != nil {
				t.Fatalf("opening baseline: %v %s", err, out)
			}
			before, err := os.ReadFile(filepath.Join(dir, "00000.m4s"))
			if err != nil {
				t.Fatal(err)
			}
			playlist, err := os.ReadFile(filepath.Join(dir, "index.m3u8"))
			if err != nil {
				t.Fatal(err)
			}
			beforeDuration := 0.0
			for _, line := range strings.Split(string(playlist), "\n") {
				if strings.HasPrefix(line, "#EXTINF:") {
					beforeDuration, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ","), 64)
					break
				}
			}
			hs, err := e.m.StartHLS(t.Context(), core.HLSRequest{SessionID: name, InputURL: spec.Input, Info: *openingInfo, Decision: d, StartOffset: at, Continuous: true})
			if err != nil {
				t.Fatal(err)
			}
			session := hs.(*Session)
			func() {
				defer session.Close()
				after := fetch(t, session, 0)
				init := fetch(t, session, InitSegment)
				pts, _ := probe("baseline", append(init, after...))
				if math.Abs(pts["audio"]-pts["video"]) > .065 {
					t.Fatalf("opening priming broke A/V alignment: %v", pts)
				}
				duration := session.plan.Dur(0)
				want := 500 * time.Millisecond
				if copyVideo {
					want = 2 * time.Second
				}
				if math.Abs((duration-want).Seconds()) > .03 || len(after) >= len(before) {
					t.Fatalf("opening copy=%v at=%v: %v/%d bytes vs %.3fs/%d bytes", copyVideo, at, duration, len(after), beforeDuration, len(before))
				}
				if !copyVideo && (session.plan.Dur(1) != 500*time.Millisecond || session.plan.Dur(2) != time.Second || session.plan.Dur(3) != 4*time.Second) {
					t.Fatal("opening ladder did not return to normal cuts", session.plan.durs)
				}
				t.Logf("OPENING copyVideo=%v seek=%v before=%.6fs/%d bytes after=%v/%d bytes", copyVideo, at, beforeDuration, len(before), duration, len(after))
			}()
		}
	}

	// Unindexed transport streams must seek before the measured keyframe,
	// rather than rounding forward and silently starting at the following GOP.
	e.gen(t, "origin.ts", 16, "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-bf", "2", "-g", "192", "-keyint_min", "192", "-sc_threshold", "0", "-c:a", "ac3")
	tsInfo, err := e.m.Probe(t.Context(), e.url("origin.ts"))
	if err != nil {
		t.Fatal(err)
	}
	hs, err := e.m.StartHLS(t.Context(), core.HLSRequest{SessionID: "ts-origin", InputURL: e.url("origin.ts"), Info: *tsInfo, Decision: decision, StartOffset: 12 * time.Second, Continuous: true})
	if err != nil {
		t.Fatal(err)
	}
	tsSession := hs.(*Session)
	func() {
		defer tsSession.Close()
		init := fetch(t, tsSession, InitSegment)
		seg := fetch(t, tsSession, 0)
		// Baseline skips the hash assertion: TS Annex B conversion changes packet bytes.
		pts, dts := probe("baseline", append(init, seg...))
		origin := tsSession.TimelineOrigin()
		if math.Abs(pts["video"]-origin.Seconds()) > .2 || math.Abs(pts["audio"]-pts["video"]) > .065 {
			t.Fatalf("TS lost measured origin %v: pts=%v dts=%v", origin, pts, dts)
		}
		t.Logf("TS requested=12 origin=%v videoPTS=%f DTS=%f audioPTS=%f", origin, pts["video"], dts["video"], pts["audio"])
	}()

}
