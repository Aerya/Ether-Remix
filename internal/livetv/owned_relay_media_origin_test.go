package livetv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// TestOwnedRelayFirstSegmentMediaStartsAtWindowStart is the invariant an MSE
// player relies on: the media time of every segment an owned relay publishes
// is its playlist position, the plan's windowStartSeconds for the first one.
// The fixture's audio rendition claims the video's program date-time but its
// media starts one segment later, so ffmpeg's first cut has no audio and the
// relay drops it; the segment published first then began 2 s into the media.
func TestOwnedRelayFirstSegmentMediaStartsAtWindowStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the relay writes its init segment after the playlist on Windows runners")
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(ffmpegPath, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
		}
	}
	cmaf := func(prefix string) []string {
		return []string{"-f", "hls", "-hls_time", "2", "-hls_list_size", "0", "-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", prefix + "_init.mp4", "-hls_segment_filename", filepath.Join(dir, prefix+"_%03d.m4s"), filepath.Join(dir, prefix+".m3u8")}
	}
	run(append([]string{"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30:duration=16",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "60", "-keyint_min", "60", "-sc_threshold", "0", "-pix_fmt", "yuv420p"}, cmaf("v")...)...)
	run(append([]string{"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=14",
		"-af", "asetpts=PTS+2.0/TB", "-c:a", "aac", "-b:a", "96k"}, cmaf("a")...)...)

	t0 := time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC)
	media := func(prefix string, count int) string {
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:100\n")
		fmt.Fprintf(&b, "#EXT-X-MAP:URI=\"/%s_init.mp4\"\n", prefix)
		for i := range count {
			// Both renditions claim the same wall clock, as the provider's did.
			fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:2.000,\n/%s_%03d.m4s\n", t0.Add(time.Duration(2*i)*time.Second).Format("2006-01-02T15:04:05.000Z"), prefix, i)
		}
		return b.String()
	}
	master := "#EXTM3U\n#EXT-X-VERSION:7\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Main",DEFAULT=YES,AUTOSELECT=YES,URI="/audio.m3u8"` + "\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=1500000,RESOLUTION=320x180,CODECS="avc1.42c015,mp4a.40.2",AUDIO="aud"` + "\n/video.m3u8\n"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			_, _ = io.WriteString(w, master)
		case "/video.m3u8":
			_, _ = io.WriteString(w, media("v", 7))
		case "/audio.m3u8":
			_, _ = io.WriteString(w, media("a", 6))
		default:
			b, err := os.ReadFile(filepath.Join(dir, filepath.Base(r.URL.Path)))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		}
	}))
	defer up.Close()

	var logBuf bytes.Buffer
	var logMu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&syncWriter{mu: &logMu, w: &logBuf}, nil))
	tm, err := transcode.New(config.Transcode{FFmpegPath: ffmpegPath, FFprobePath: ffprobePath, TempDir: filepath.Join(t.TempDir(), "hls")},
		slog.New(slog.NewTextHandler(io.Discard, nil)), transcode.WithoutHWDetection())
	if err != nil {
		t.Fatal(err)
	}
	defer tm.Close()
	s := testService(t, Options{HTTPClient: up.Client(), Transcoder: tm, Logger: logger})
	u := &core.User{ID: 1, Username: "viewer", StreamLimit: 2}
	caps := core.ClientCaps{Product: "Vela", Protocol: "vela", Platform: "web", ClientID: "vela-origin-test",
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, HLSContainers: []string{"fmp4"}, MaxHeight: 1080}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := s.TuneDirectStream(ctx, u, DirectStream{ID: "directory:origin", Title: "Origin", URL: up.URL + "/master.m3u8"}, caps, "sess-origin")
	if err != nil {
		t.Fatalf("TuneDirectStream: %v", err)
	}
	defer func() { _ = s.Stop(context.Background(), info.ID, u) }()
	rec := httptest.NewRecorder()
	s.ServePlaylist(rec, httptest.NewRequest(http.MethodGet, "/index.m3u8", nil).WithContext(ctx), info.ID, u)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "#EXT-X-MAP") {
		t.Fatalf("playlist status=%d body=%q", rec.Code, rec.Body.String())
	}
	logMu.Lock()
	logs := logBuf.String()
	logMu.Unlock()
	if !strings.Contains(logs, "owned relay dropped unaligned opening segment") {
		t.Fatalf("the fixture did not drop the relay's opening segment; logs:\n%s", logs)
	}

	window, err := s.LiveWindow(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	tl := s.sessions[info.ID].owned.tl
	s.mu.Unlock()
	tl.mu.Lock()
	segs := append([]*ownedSegment(nil), tl.window...)
	tl.mu.Unlock()
	if len(segs) == 0 || segs[0].seq != window.Anchor.MediaSequence {
		t.Fatalf("window does not open on the anchor's segment %d", window.Anchor.MediaSequence)
	}
	for i, seg := range segs {
		init, ok := tl.resource(seg.mapRes)
		if !ok {
			t.Fatalf("segment %d: init %q missing", seg.seq, seg.mapRes)
		}
		start, ok := mp4SegmentStartSeconds(init.data, seg.data)
		if !ok {
			t.Fatalf("segment %d has no decode time", seg.seq)
		}
		want := seg.start.Seconds()
		if i == 0 {
			want = window.Anchor.WindowStartSeconds
		}
		if math.Abs(start-want) > 0.05 {
			t.Fatalf("segment %d starts its media at %.3fs, its playlist position is %.3fs", seg.seq, start, want)
		}
	}
}
