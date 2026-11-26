package livetv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// A provider's program change (an ad or a new encoder: a discontinuity,
// new PIDs, a restarted clock) inside one ffmpeg run leaves the run's mapped
// streams silent, so the relay stops cutting and airs slate. The relay must
// instead splice the new program into the same playlist: no slate, nothing
// lost or repeated across the boundary.
func TestOwnedRelaySplicesProviderProgramChange(t *testing.T) {
	// Muxed, and demuxed like Pluto: separate video and audio TS renditions
	// whose discontinuities share one program date-time.
	for _, demuxed := range []bool{false, true} {
		t.Run(fmt.Sprintf("demuxed=%v", demuxed), func(t *testing.T) { testProgramChangeSplice(t, demuxed) })
	}
}

func testProgramChangeSplice(t *testing.T, demuxed bool) {
	if testing.Short() {
		t.Skip("integration test (-short)")
	}
	if raceEnabled {
		// This fixture runs an external encoder against a real-time
		// provider window and checks uninterrupted publication.
		t.Skip("real-time playback fixture under -race")
	}
	ffmpegPath, err1 := exec.LookPath("ffmpeg")
	ffprobePath, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	// Program A then program B, from different "encoders": other PIDs and
	// PMT, another picture size, timestamps restarting at the same origin.
	program := func(name, src, size string, pid, pmt string, seconds int, streams ...string) {
		t.Helper()
		args := []string{"-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", src + "=s=" + size + ":r=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-t", fmt.Sprint(seconds), "-c:v", "libx264", "-preset", "ultrafast", "-g", "25", "-keyint_min", "25", "-sc_threshold", "0",
			"-c:a", "aac"}
		args = append(args, streams...)
		args = append(args, "-f", "hls", "-hls_time", "2", "-hls_list_size", "0",
			"-hls_segment_options", "mpegts_start_pid="+pid+":mpegts_pmt_start_pid="+pmt,
			"-hls_segment_filename", filepath.Join(dir, name+"%03d.ts"), filepath.Join(dir, name+".m3u8"))
		if out, err := exec.CommandContext(t.Context(), ffmpegPath, args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture %s: %v\n%s", name, err, out)
		}
	}
	type seg struct {
		name string
		disc bool
	}
	list := func(prefixes ...string) []seg {
		var out []seg
		for _, p := range prefixes {
			matches, _ := filepath.Glob(filepath.Join(dir, p+"[0-9][0-9][0-9].ts"))
			if p[0] == 'a' {
				// An audio-only cut can leave a sliver past 12 s; both
				// renditions list program A's six 2 s segments.
				matches = matches[:min(len(matches), 6)]
			}
			for i, m := range matches {
				out = append(out, seg{name: filepath.Base(m), disc: p[0] == 'b' && i == 0})
			}
		}
		return out
	}
	lists := map[string][]seg{}
	firstB := "b000.ts"
	if demuxed {
		program("av", "testsrc2", "640x360", "0x100", "0x1000", 12, "-an")
		program("aa", "testsrc2", "640x360", "0x100", "0x1000", 12, "-vn")
		program("bv", "smptebars", "480x270", "0x200", "0x1100", 40, "-an")
		program("ba", "smptebars", "480x270", "0x200", "0x1100", 40, "-vn")
		lists["/video.m3u8"], lists["/audio.m3u8"] = list("av", "bv"), list("aa", "ba")
		firstB = "bv000.ts"
	} else {
		program("a", "testsrc2", "640x360", "0x100", "0x1000", 12)
		program("b", "smptebars", "480x270", "0x200", "0x1100", 40)
		lists["/live.m3u8"] = list("a", "b")
	}
	pdt := time.Date(2026, 11, 24, 6, 0, 0, 0, time.UTC)
	// A live window of five 2 s segments, advancing in real time from six
	// listed (a 12 s backlog, the relay's reserve).
	start := time.Now()
	var fetchMu sync.Mutex
	var fetched []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/master.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n"+
				`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Main",DEFAULT=YES,AUTOSELECT=YES,URI="/audio.m3u8"`+"\n"+
				`#EXT-X-STREAM-INF:BANDWIDTH=1500000,CODECS="avc1.64001e,mp4a.40.2",AUDIO="aud"`+"\n/video.m3u8\n")
			return
		}
		segs, ok := lists[r.URL.Path]
		if !ok {
			fetchMu.Lock()
			fetched = append(fetched, filepath.Base(r.URL.Path))
			fetchMu.Unlock()
			http.ServeFile(w, r, filepath.Join(dir, filepath.Base(r.URL.Path)))
			return
		}
		last := min(len(segs)-1, 5+int(time.Since(start)/(2*time.Second)))
		first := max(0, last-5)
		var b strings.Builder
		fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:%d\n", first)
		for i := first; i <= last; i++ {
			if segs[i].disc {
				b.WriteString("#EXT-X-DISCONTINUITY\n")
			}
			fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:2.000,\n/%s\n", pdt.Add(time.Duration(2*i)*time.Second).Format("2006-01-02T15:04:05.000Z"), segs[i].name)
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, b.String())
	}))
	defer up.Close()

	var logBuf bytes.Buffer
	var logMu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&syncWriter{mu: &logMu, w: &logBuf}, nil))
	tm, err := transcode.New(config.Transcode{FFmpegPath: ffmpegPath, FFprobePath: ffprobePath, TempDir: filepath.Join(dir, "hls")},
		logger, transcode.WithoutHWDetection())
	if err != nil {
		t.Fatal(err)
	}
	defer tm.Close()
	s := testService(t, Options{HTTPClient: up.Client(), Transcoder: tm, Logger: logger})
	u := &core.User{ID: 1, Username: "viewer", StreamLimit: 2}
	caps := core.ClientCaps{Product: "Vela", Protocol: "vela", ClientID: "vela-program-change", VideoCodecs: []string{"h264"},
		AudioCodecs: []string{"aac"}, HLSContainers: []string{"fmp4"}, MaxHeight: 1080}
	input := "/live.m3u8"
	if demuxed {
		input = "/master.m3u8"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	info, err := s.TuneDirectStream(ctx, u, DirectStream{ID: "directory:program-change", Title: "Program change", URL: up.URL + input}, caps, "sess-program-change")
	if err != nil {
		t.Fatalf("tune: %v", err)
	}
	defer func() { _ = s.Stop(context.Background(), info.ID, u) }()

	// Read the playlist as a player would until program B has aired, then
	// account for every published segment.
	seen := map[string]float64{} // segment URI -> duration
	aired := map[string]string{} // segment URI -> init in force
	var inits []string
	mapRE := regexp.MustCompile(`#EXT-X-MAP:URI="[^"]*/resource/([^"?]+)`)
	for time.Since(start) < 45*time.Second {
		rec := httptest.NewRecorder()
		s.ServePlaylist(rec, httptest.NewRequest(http.MethodGet, "/index.m3u8", nil).WithContext(ctx), info.ID, u)
		cur, dur := "", 0.0
		for _, line := range strings.Split(rec.Body.String(), "\n") {
			switch {
			case strings.HasPrefix(line, "#EXT-X-MAP:"):
				if m := mapRE.FindStringSubmatch(line); m != nil {
					cur = m[1]
					if !slices.Contains(inits, cur) {
						inits = append(inits, cur)
					}
				}
			case strings.HasPrefix(line, "#EXTINF:"):
				fmt.Sscanf(strings.TrimPrefix(line, "#EXTINF:"), "%f", &dur)
			case line != "" && !strings.HasPrefix(line, "#"):
				name, _, _ := strings.Cut(line, "?")
				seen[name], aired[name] = dur, cur
			}
		}
		if len(inits) >= 2 && airedMedia(seen) >= 22 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	logMu.Lock()
	logs := logBuf.String()
	logMu.Unlock()
	if strings.Contains(logs, "owned live timeline publishing slate") ||
		strings.Contains(logs, "owned relay switching producer to re-encode") {
		t.Fatalf("relay fell back during the program change (inits %v); logs:\n%s", inits, logs)
	}
	if !strings.Contains(logs, `reason="program change"`) {
		t.Fatalf("no program-change restart; logs:\n%s", logs)
	}
	if len(inits) < 2 {
		t.Fatalf("inits %v: program B never aired; logs:\n%s", inits, logs)
	}
	// The reserve can prefetch B before ffmpeg has drained A. Verify that
	// B's first segment was retrieved and check the splice on published media.
	fetchMu.Lock()
	order := append([]string(nil), fetched...)
	fetchMu.Unlock()
	if !slices.Contains(order, firstB) {
		t.Fatalf("first segment of program B was never fetched: %v; logs:\n%s", order, logs)
	}
	// A aired once: at most the 10 s from the earliest join (A1..A5), at
	// least its last segment before B.
	var aSeconds float64
	for name, d := range seen {
		if aired[name] == inits[0] {
			aSeconds += d
		}
	}
	if aSeconds < 2 || aSeconds > 10.1 {
		t.Fatalf("program A aired %.2f s, want 2..10: %d segments, inits %v, fetches %v; logs:\n%s", aSeconds, len(seen), inits, order, logs)
	}
	rec := httptest.NewRecorder()
	s.ServeResource(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx), info.ID, inits[len(inits)-1], u)
	if w, h := avc1Size(rec.Body.Bytes()); w != 480 || h != 270 {
		t.Fatalf("last init is %dx%d, want program B's 480x270: fetches %v; logs:\n%s", w, h, order, logs)
	}
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "aligned demuxed") || strings.Contains(line, "restarting live encoder") || strings.Contains(line, "timeline closed") {
			t.Log(line)
		}
	}
}

func airedMedia(seen map[string]float64) float64 {
	var sum float64
	for _, d := range seen {
		sum += d
	}
	return sum
}

// avc1Size reads the picture size of an init's avc1 sample entry.
func avc1Size(b []byte) (int, int) {
	i := bytes.Index(b, []byte("avc1"))
	if i < 0 || i+4+32 > len(b) {
		return 0, 0
	}
	p := b[i+4:]
	return int(p[24])<<8 | int(p[25]), int(p[26])<<8 | int(p[27])
}
