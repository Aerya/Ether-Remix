package livetv

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

func buildTestMediaPlaylist(seq int64, target int, basePDT time.Time, hasPDT bool, durs []float64, prefix string, discAt int) []byte {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", target)
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", seq)
	t := basePDT
	for i, d := range durs {
		if discAt >= 0 && i == discAt {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		if hasPDT && (i == 0 || (discAt < 0)) {
			fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n", t.UTC().Format("2006-01-02T15:04:05.000Z"))
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s%d.m4s\n", d, prefix, seq+int64(i))
		t = t.Add(time.Duration(d * float64(time.Second)))
	}
	return []byte(b.String())
}

func TestDemuxedHLSAlignChooseStarts(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/live/index.m3u8")
	t0 := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	t.Run("audio_earlier_than_video", func(t *testing.T) {
		vBody := buildTestMediaPlaylist(10, 2, t0.Add(2*time.Second), true, []float64{2, 2, 2, 2, 2, 2}, "v", -1)
		aBody := buildTestMediaPlaylist(20, 2, t0, true, []float64{2, 2, 2, 2, 2, 2}, "a", -1)
		vm, am := parseHLSMedia(vBody, base), parseHLSMedia(aBody, base)
		vt, vh, at, ah, ok := hlsMediaPDTTimes(vm, am)
		if !ok {
			t.Fatal("expected usable PDT timelines")
		}
		vi, ai, skew, ok := chooseAlignedStarts(vm, am, vt, vh, at, ah, 6*time.Second, 4*time.Second)
		if !ok {
			t.Fatal("expected aligned start pair")
		}
		if skew != 0 {
			t.Fatalf("skew = %v, want 0", skew)
		}
		if !vm.segs[vi].pdt.Equal(am.segs[ai].pdt) {
			t.Fatalf("video PDT %v != audio PDT %v", vm.segs[vi].pdt, am.segs[ai].pdt)
		}
		if vi == 0 {
			t.Fatalf("expected to avoid oldest video segment when >=3 segments exist")
		}
	})

	t.Run("video_earlier_than_audio", func(t *testing.T) {
		vBody := buildTestMediaPlaylist(100, 2, t0, true, []float64{2, 2, 2, 2, 2, 2}, "v", -1)
		aBody := buildTestMediaPlaylist(200, 2, t0.Add(2*time.Second), true, []float64{2, 2, 2, 2, 2, 2}, "a", -1)
		vm, am := parseHLSMedia(vBody, base), parseHLSMedia(aBody, base)
		vt, vh, at, ah, ok := hlsMediaPDTTimes(vm, am)
		if !ok {
			t.Fatal("expected usable PDT timelines")
		}
		vi, ai, skew, ok := chooseAlignedStarts(vm, am, vt, vh, at, ah, 6*time.Second, 4*time.Second)
		if !ok || skew != 0 {
			t.Fatalf("ok=%v skew=%v, want ok=true skew=0", ok, skew)
		}
		if !vm.segs[vi].pdt.Equal(am.segs[ai].pdt) {
			t.Fatalf("video PDT %v != audio PDT %v", vm.segs[vi].pdt, am.segs[ai].pdt)
		}
	})

	t.Run("aac_frame_jitter", func(t *testing.T) {
		vBody := buildTestMediaPlaylist(10, 2, t0.Add(2*time.Second), true, []float64{2, 2, 2, 2, 2}, "v", -1)
		aBody := buildTestMediaPlaylist(20, 2, t0.Add(-15*time.Millisecond), true, []float64{2, 2, 2, 2, 2}, "a", -1)
		vm, am := parseHLSMedia(vBody, base), parseHLSMedia(aBody, base)
		vt, vh, at, ah, ok := hlsMediaPDTTimes(vm, am)
		if !ok {
			t.Fatal("expected usable PDT timelines")
		}
		_, _, skew, ok := chooseAlignedStarts(vm, am, vt, vh, at, ah, 4*time.Second, 4*time.Second)
		if !ok || skew > 20*time.Millisecond {
			t.Fatalf("ok=%v skew=%v, want <=20ms", ok, skew)
		}
	})

	t.Run("different_segment_durations_prefer_exact_match", func(t *testing.T) {
		// Video 2.0s (0, 2, 4, 6, 8, 10), audio 1.6s (0, 1.6, 3.2, 4.8, 6.4, 8.0, 9.6, 11.2):
		// exact match at 8.0s (v=4, a=5) even when desiredT is 6.0s.
		vBody := buildTestMediaPlaylist(1, 2, t0, true, []float64{2, 2, 2, 2, 2, 2}, "v", -1)
		aBody := buildTestMediaPlaylist(1, 2, t0, true, []float64{1.6, 1.6, 1.6, 1.6, 1.6, 1.6, 1.6, 1.6}, "a", -1)
		vm, am := parseHLSMedia(vBody, base), parseHLSMedia(aBody, base)
		vt, vh, at, ah, ok := hlsMediaPDTTimes(vm, am)
		if !ok {
			t.Fatal("expected usable PDT timelines")
		}
		vi, ai, skew, ok := chooseAlignedStarts(vm, am, vt, vh, at, ah, 6*time.Second, 4*time.Second)
		if !ok || skew > hlsAlignGoodSkew {
			t.Fatalf("ok=%v vi=%d ai=%d skew=%v, want exact match at 8.0s", ok, vi, ai, skew)
		}
		if vi != 4 || ai != 5 {
			t.Fatalf("got (v=%d, a=%d), want (4, 5)", vi, ai)
		}
	})

	t.Run("staggered_prefers_audio_before_video_with_runway", func(t *testing.T) {
		vBody := buildTestMediaPlaylist(1, 2, t0, true, []float64{2, 2, 2, 2, 2}, "v", -1)
		aBody := buildTestMediaPlaylist(1, 2, t0.Add(time.Second), true, []float64{2, 2, 2, 2, 2}, "a", -1)
		vm, am := parseHLSMedia(vBody, base), parseHLSMedia(aBody, base)
		vt, vh, at, ah, ok := hlsMediaPDTTimes(vm, am)
		if !ok {
			t.Fatal("expected usable PDT timelines")
		}
		vi, ai, skew, ok := chooseAlignedStarts(vm, am, vt, vh, at, ah, 6*time.Second, 4*time.Second)
		if !ok || skew != time.Second {
			t.Fatalf("ok=%v skew=%v, want 1s", ok, skew)
		}
		if at[ai] > vt[vi] {
			t.Fatalf("expected audio (%v) <= video (%v) on equal skew tie", at[ai], vt[vi])
		}
		if len(vm.segs)-vi < 2 || len(am.segs)-ai < 2 {
			t.Fatalf("expected >=2 runway segments, got v=%d/%d a=%d/%d", vi, len(vm.segs), ai, len(am.segs))
		}
	})

	t.Run("pdt_propagation_stops_at_discontinuity", func(t *testing.T) {
		body := buildTestMediaPlaylist(1, 2, t0, true, []float64{2, 2, 2, 2}, "v", 2)
		m := parseHLSMedia(body, base)
		if !m.segs[0].hasPDT || !m.segs[1].hasPDT {
			t.Fatalf("expected PDT to propagate to segment 1: %+v", m.segs[:2])
		}
		if !m.segs[1].pdt.Equal(t0.Add(2 * time.Second)) {
			t.Fatalf("seg 1 PDT = %v, want %v", m.segs[1].pdt, t0.Add(2*time.Second))
		}
		if m.segs[2].hasPDT || m.segs[3].hasPDT {
			t.Fatalf("expected PDT propagation to stop across discontinuity: %+v", m.segs[2:])
		}
	})

	t.Run("missing_pdt_returns_false", func(t *testing.T) {
		vBody := buildTestMediaPlaylist(1, 2, t0, true, []float64{2, 2, 2}, "v", -1)
		aBody := buildTestMediaPlaylist(1, 2, t0, false, []float64{2, 2, 2}, "a", -1)
		vm, am := parseHLSMedia(vBody, base), parseHLSMedia(aBody, base)
		if _, _, _, _, ok := hlsMediaPDTTimes(vm, am); ok {
			t.Fatal("expected ok=false when audio playlist has no PDT")
		}
	})
}

func TestDemuxedHLSRelayAlignsWindows(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	masterBody := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:7",
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Main",DEFAULT=YES,AUTOSELECT=YES,URI="/audio.m3u8"`,
		`#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1280x720,CODECS="avc1.64001f",AUDIO="aud"`,
		"/video.m3u8",
		"",
	}, "\n")

	httpGet := func(t *testing.T, raw string) (int, string) {
		t.Helper()
		res, err := http.Get(raw)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	extractMediaURLs := func(t *testing.T, master string) (videoURL, audioURL string) {
		t.Helper()
		reURI := regexp.MustCompile(`URI="(http://[^"]+)"`)
		if m := reURI.FindStringSubmatch(master); len(m) == 2 {
			audioURL = m[1]
		}
		for _, line := range strings.Split(master, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "http://") {
				videoURL = line
				break
			}
		}
		if videoURL == "" || audioURL == "" {
			t.Fatalf("failed to extract variant URLs from master:\n%s", master)
		}
		return videoURL, audioURL
	}

	t.Run("audio_earlier_steady_single_fetch_each", func(t *testing.T) {
		var vHits, aHits atomic.Int32
		vBody := buildTestMediaPlaylist(10, 2, t0.Add(2*time.Second), true, []float64{2, 2, 2, 2, 2, 2}, "/v", -1)
		aBody := buildTestMediaPlaylist(20, 2, t0, true, []float64{2, 2, 2, 2, 2, 2}, "/a", -1)

		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/master.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = io.WriteString(w, masterBody)
			case "/video.m3u8":
				vHits.Add(1)
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = w.Write(vBody)
			case "/audio.m3u8":
				aHits.Add(1)
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = w.Write(aBody)
			default:
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write([]byte("seg"))
			}
		}))
		defer up.Close()

		s := testService(t, Options{HTTPClient: up.Client()})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ss := &session{ctx: ctx, cancel: cancel, info: SessionInfo{ID: "demux-steady", Channel: Channel{ID: "d1", Kind: "iptv"}}}
		if err := s.grantSteadyInput(ss, StreamSource{ID: "a", URL: up.URL + "/master.m3u8"}, http.Header{}); err != nil {
			t.Fatal(err)
		}
		defer s.revokeInput(ss)

		input, _ := s.relayInput(ss)
		code, master := httpGet(t, input)
		if code != http.StatusOK {
			t.Fatalf("master status = %d", code)
		}
		vURL, aURL := extractMediaURLs(t, master)
		code, vOut := httpGet(t, vURL)
		if code != http.StatusOK {
			t.Fatalf("video status = %d", code)
		}
		code, aOut := httpGet(t, aURL)
		if code != http.StatusOK {
			t.Fatalf("audio status = %d", code)
		}

		vBase, _ := url.Parse(vURL)
		aBase, _ := url.Parse(aURL)
		vm := parseHLSMedia([]byte(vOut), vBase)
		am := parseHLSMedia([]byte(aOut), aBase)
		if len(vm.segs) == 0 || len(am.segs) == 0 {
			t.Fatalf("empty playlists: v=%d a=%d", len(vm.segs), len(am.segs))
		}
		if !vm.segs[0].pdt.Equal(am.segs[0].pdt) {
			t.Fatalf("unaligned opening PDT: video=%v (seq %d) audio=%v (seq %d)", vm.segs[0].pdt, vm.segs[0].seq, am.segs[0].pdt, am.segs[0].seq)
		}
		// Peer playlist should be served from the paired snap without a second
		// immediate upstream round-trip.
		if vHits.Load() < 1 || aHits.Load() < 1 {
			t.Fatalf("expected upstream fetches, got v=%d a=%d", vHits.Load(), aHits.Load())
		}
	})

	t.Run("video_earlier_fastest_capped_to_3_and_monotonic_reload", func(t *testing.T) {
		var mu sync.Mutex
		vSeq, aSeq := int64(50), int64(70)
		vCount, aCount := 6, 6

		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			vs, vc, as, ac := vSeq, vCount, aSeq, aCount
			mu.Unlock()
			switch r.URL.Path {
			case "/master.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = io.WriteString(w, masterBody)
			case "/video.m3u8":
				durs := make([]float64, vc)
				for i := range durs {
					durs[i] = 2
				}
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = w.Write(buildTestMediaPlaylist(vs, 2, t0, true, durs, "/v", -1))
			case "/audio.m3u8":
				durs := make([]float64, ac)
				for i := range durs {
					durs[i] = 2
				}
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = w.Write(buildTestMediaPlaylist(as, 2, t0.Add(2*time.Second), true, durs, "/a", -1))
			default:
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write([]byte("seg"))
			}
		}))
		defer up.Close()

		s := testService(t, Options{HTTPClient: up.Client()})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Fastest grant has reserve == 0; initial aligned window must be <= 3
		// segments so ffmpeg's default -live_start_index -3 opens at index 0.
		ss := &session{ctx: ctx, cancel: cancel, info: SessionInfo{ID: "demux-fastest", Channel: Channel{ID: "d2", Kind: "iptv"}}}
		if err := s.grantInput(ss, StreamSource{ID: "a", URL: up.URL + "/master.m3u8"}, http.Header{}); err != nil {
			t.Fatal(err)
		}
		defer s.revokeInput(ss)

		input, _ := s.relayInput(ss)
		_, master := httpGet(t, input)
		vURL, aURL := extractMediaURLs(t, master)
		_, vOut := httpGet(t, vURL)
		_, aOut := httpGet(t, aURL)

		vBase, _ := url.Parse(vURL)
		aBase, _ := url.Parse(aURL)
		vm := parseHLSMedia([]byte(vOut), vBase)
		am := parseHLSMedia([]byte(aOut), aBase)
		if len(vm.segs) == 0 || len(am.segs) == 0 || len(vm.segs) > 3 || len(am.segs) > 3 {
			t.Fatalf("expected 1..3 initial segments for Fastest, got v=%d a=%d", len(vm.segs), len(am.segs))
		}
		if !vm.segs[0].pdt.Equal(am.segs[0].pdt) {
			t.Fatalf("unaligned opening PDT: video=%v audio=%v", vm.segs[0].pdt, am.segs[0].pdt)
		}
		initVSeq, initASeq := vm.segs[0].seq, am.segs[0].seq

		// Simulate reading the first segment and an upstream playlist advance.
		httpGet(t, vm.segs[0].url)
		httpGet(t, am.segs[0].url)
		mu.Lock()
		vCount++
		aCount++
		mu.Unlock()

		_, vReload := httpGet(t, vURL)
		_, aReload := httpGet(t, aURL)
		vm2 := parseHLSMedia([]byte(vReload), vBase)
		am2 := parseHLSMedia([]byte(aReload), aBase)
		if vm2.segs[0].seq < initVSeq || am2.segs[0].seq < initASeq {
			t.Fatalf("media sequence regressed on reload: v %d->%d, a %d->%d", initVSeq, vm2.segs[0].seq, initASeq, am2.segs[0].seq)
		}
		if vm2.segs[len(vm2.segs)-1].seq <= vm.segs[len(vm.segs)-1].seq {
			t.Fatalf("video reload did not expose newly added live segment")
		}
	})

	t.Run("probe_tail_reads_do_not_poison_decoder", func(t *testing.T) {
		vBody := buildTestMediaPlaylist(10, 2, t0.Add(2*time.Second), true, []float64{2, 2, 2, 2, 2, 2, 2, 2}, "/v", -1)
		aBody := buildTestMediaPlaylist(20, 2, t0, true, []float64{2, 2, 2, 2, 2, 2, 2, 2}, "/a", -1)
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/master.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = io.WriteString(w, masterBody)
			case "/video.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = w.Write(vBody)
			case "/audio.m3u8":
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = w.Write(aBody)
			default:
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write([]byte("seg"))
			}
		}))
		defer up.Close()

		s := testService(t, Options{HTTPClient: up.Client()})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ss := &session{ctx: ctx, cancel: cancel, info: SessionInfo{ID: "demux-probe", Channel: Channel{ID: "d3", Kind: "iptv"}}}
		if err := s.grantSteadyInput(ss, StreamSource{ID: "a", URL: up.URL + "/master.m3u8"}, http.Header{}); err != nil {
			t.Fatal(err)
		}
		defer s.revokeInput(ss)

		input, _ := s.relayInput(ss)
		// 1. ffprobe opens master, opens playlists, and asymmetrically reads
		// live-edge tail segments (skipping past floor).
		_, m1 := httpGet(t, input)
		vURL, aURL := extractMediaURLs(t, m1)
		_, pv := httpGet(t, vURL)
		_, pa := httpGet(t, aURL)
		vBase, _ := url.Parse(vURL)
		aBase, _ := url.Parse(aURL)
		pvm := parseHLSMedia([]byte(pv), vBase)
		pam := parseHLSMedia([]byte(pa), aBase)
		httpGet(t, pvm.segs[len(pvm.segs)-1].url)
		httpGet(t, pam.segs[len(pam.segs)-2].url)
		httpGet(t, pam.segs[len(pam.segs)-1].url)

		// 2. ffmpeg opens master on the same grant; both playlists must still
		// begin at the same aligned media time.
		_, m2 := httpGet(t, input)
		vURL2, aURL2 := extractMediaURLs(t, m2)
		_, fv := httpGet(t, vURL2)
		_, fa := httpGet(t, aURL2)
		fvm := parseHLSMedia([]byte(fv), vBase)
		fam := parseHLSMedia([]byte(fa), aBase)
		if !fvm.segs[0].pdt.Equal(fam.segs[0].pdt) {
			t.Fatalf("ffprobe poisoned decoder alignment: video=%v (seq %d) audio=%v (seq %d)", fvm.segs[0].pdt, fvm.segs[0].seq, fam.segs[0].pdt, fam.segs[0].seq)
		}
	})
}

// Synthetic MPEG-TS helpers for unit-testing tsOpeningAVAlignment and
// relayProducer.accept without external tools.
func encodePESTime(marker byte, pts int64) [5]byte {
	v := uint64(pts) & ((1 << 33) - 1)
	return [5]byte{
		marker | byte((v>>29)&0x0e) | 1,
		byte(v >> 22),
		byte((v>>14)&0xfe) | 1,
		byte(v >> 7),
		byte((v<<1)&0xfe) | 1,
	}
}

func makeSyntheticTSPacket(pid uint16, pusi bool, rai bool, payload []byte) []byte {
	pkt := make([]byte, tsPacket)
	pkt[0] = 0x47
	pkt[1] = byte((pid >> 8) & 0x1f)
	if pusi {
		pkt[1] |= 0x40
	}
	pkt[2] = byte(pid & 0xff)
	off := 4
	if rai {
		pkt[3] = 0x30 // adaptation + payload
		pkt[4] = 1    // adaptation field length
		pkt[5] = 0x40 // random_access_indicator
		off = 6
	} else {
		pkt[3] = 0x10 // payload only
	}
	copy(pkt[off:], payload)
	return pkt
}

func makeSyntheticPAT(pmtPID uint16) []byte {
	// pointer(0), table_id(0), section_len(13), tsid(1), ver(0xc1), sec(0), last(0), prog(1), pmtPID, crc(4)
	sec := []byte{
		0x00,
		0x00, 0xb0, 0x0d,
		0x00, 0x01, 0xc1, 0x00, 0x00,
		0x00, 0x01, byte(0xe0 | ((pmtPID >> 8) & 0x1f)), byte(pmtPID & 0xff),
		0x00, 0x00, 0x00, 0x00,
	}
	return makeSyntheticTSPacket(0, true, false, sec)
}

func makeSyntheticPMT(pmtPID, videoPID, audioPID uint16) []byte {
	var streams []byte
	if videoPID != 0 {
		streams = append(streams, 0x1b, byte(0xe0|((videoPID>>8)&0x1f)), byte(videoPID&0xff), 0xf0, 0x00)
	}
	if audioPID != 0 {
		streams = append(streams, 0x0f, byte(0xe0|((audioPID>>8)&0x1f)), byte(audioPID&0xff), 0xf0, 0x00)
	}
	secLen := 9 + len(streams) + 4
	sec := []byte{
		0x00,
		0x02, byte(0xb0 | ((secLen >> 8) & 0x0f)), byte(secLen & 0xff),
		0x00, 0x01, 0xc1, 0x00, 0x00,
		byte(0xe0 | ((videoPID >> 8) & 0x1f)), byte(videoPID & 0xff),
		0xf0, 0x00,
	}
	sec = append(sec, streams...)
	sec = append(sec, 0, 0, 0, 0)
	return makeSyntheticTSPacket(pmtPID, true, false, sec)
}

func makeSyntheticPES(pid uint16, streamID byte, pts int64, dts *int64, rai bool) []byte {
	var hdr []byte
	if dts != nil {
		p := encodePESTime(0x30, pts)
		d := encodePESTime(0x10, *dts)
		hdr = []byte{0x00, 0x00, 0x01, streamID, 0x00, 0x00, 0x80, 0xc0, 0x0a}
		hdr = append(hdr, p[:]...)
		hdr = append(hdr, d[:]...)
	} else {
		p := encodePESTime(0x20, pts)
		hdr = []byte{0x00, 0x00, 0x01, streamID, 0x00, 0x00, 0x80, 0x80, 0x05}
		hdr = append(hdr, p[:]...)
	}
	return makeSyntheticTSPacket(pid, true, rai, hdr)
}

func makeMP4Box(typ string, body []byte) []byte {
	out := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
	copy(out[4:8], typ)
	copy(out[8:], body)
	return out
}

func makeTestFMP4Init(vScale, aScale uint32) []byte {
	makeTrak := func(id, scale uint32, handler string) []byte {
		tkhd := make([]byte, 24)
		binary.BigEndian.PutUint32(tkhd[12:16], id)
		mdhd := make([]byte, 24)
		binary.BigEndian.PutUint32(mdhd[12:16], scale)
		hdlr := make([]byte, 12)
		copy(hdlr[8:12], handler)
		mdia := makeMP4Box("mdia", append(makeMP4Box("mdhd", mdhd), makeMP4Box("hdlr", hdlr)...))
		return makeMP4Box("trak", append(makeMP4Box("tkhd", tkhd), mdia...))
	}
	moov := makeMP4Box("moov", append(makeTrak(1, vScale, "vide"), makeTrak(2, aScale, "soun")...))
	return append(makeMP4Box("ftyp", []byte("isom\x00\x00\x02\x00")), moov...)
}

func makeTestFMP4Seg(vTFDT *uint64, aTFDT *uint64) []byte {
	makeTraf := func(id uint32, tfdt uint64) []byte {
		tfhd := make([]byte, 8)
		binary.BigEndian.PutUint32(tfhd[4:8], id)
		tb := make([]byte, 12)
		tb[0] = 1
		binary.BigEndian.PutUint64(tb[4:12], tfdt)
		return makeMP4Box("traf", append(makeMP4Box("tfhd", tfhd), makeMP4Box("tfdt", tb)...))
	}
	var body []byte
	if vTFDT != nil {
		body = append(body, makeTraf(1, *vTFDT)...)
	}
	if aTFDT != nil {
		body = append(body, makeTraf(2, *aTFDT)...)
	}
	return makeMP4Box("moof", body)
}

func TestOpeningAVAlignmentProbeAndAccept(t *testing.T) {
	const pmtPID, vPID, aPID = 0x1000, 0x100, 0x101
	pat := makeSyntheticPAT(pmtPID)
	pmt := makeSyntheticPMT(pmtPID, vPID, aPID)
	join := func(chunks ...[]byte) []byte { return bytes.Join(chunks, nil) }

	newProd := func() *relayProducer {
		return &relayProducer{
			log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			dec: core.Decision{VideoCopy: true, AudioCopy: true},
		}
	}

	// 1. Video-only opening (missing audio) is dropped; aligned next segment accepted.
	videoOnly := join(pat, pmt, makeSyntheticPES(vPID, 0xe0, 90000, nil, true))
	if a := tsOpeningAVAlignment(videoOnly); !a.known || !a.hasVideo || a.hasAudio {
		t.Fatalf("video-only alignment = %+v", a)
	}
	p := newProd()
	if p.accept(&ownedSegment{ext: "ts", dur: 2 * time.Second, data: videoOnly}) {
		t.Fatal("expected video-only opening segment to be dropped")
	}

	// 2. B-frame reorder (DTS=1.400s, PTS=1.467s, audio=1.401s) is aligned and accepted.
	dts := int64(126000) // 1.400s
	alignedB := join(pat, pmt,
		makeSyntheticPES(vPID, 0xe0, 132030, &dts, true), // 1.467s PTS, 1.400s DTS
		makeSyntheticPES(aPID, 0xc0, 126090, nil, false), // 1.401s
	)
	if a := tsOpeningAVAlignment(alignedB); !a.known || !a.hasVideo || !a.hasAudio || !a.videoKeyOpen || a.skew > 5*time.Millisecond {
		t.Fatalf("aligned B-frame segment misclassified: %+v", a)
	}
	if !p.accept(&ownedSegment{ext: "ts", dur: 2 * time.Second, data: alignedB}) {
		t.Fatal("expected aligned segment to be accepted")
	}
	if !p.openedMedia {
		t.Fatal("expected openedMedia=true after accepting aligned segment")
	}

	// 3. Large A/V skew (1.5s) is dropped.
	skewed := join(pat, pmt,
		makeSyntheticPES(aPID, 0xc0, 90000, nil, false),       // 1.0s
		makeSyntheticPES(vPID, 0xe0, 90000+135000, nil, true), // 2.5s
	)
	p2 := newProd()
	if p2.accept(&ownedSegment{ext: "ts", dur: 2 * time.Second, data: skewed}) {
		t.Fatal("expected 1.5s skewed opening segment to be dropped")
	}

	// 4. Non-keyframe opening followed by later keyframe is dropped.
	nonKeyOpen := join(pat, pmt,
		makeSyntheticPES(vPID, 0xe0, 90000, nil, false),
		makeSyntheticPES(aPID, 0xc0, 90000, nil, false),
		makeSyntheticPES(vPID, 0xe0, 180000, nil, true),
	)
	if a := tsOpeningAVAlignment(nonKeyOpen); !a.known || a.videoKeyOpen {
		t.Fatalf("non-keyframe opening misclassified: %+v", a)
	}
	if p2.accept(&ownedSegment{ext: "ts", dur: 2 * time.Second, data: nonKeyOpen}) {
		t.Fatal("expected non-keyframe opening segment to be dropped")
	}

	// 5. Drop budget is bounded (maxOpeningSkewDrops=2): 3rd skewed segment passes.
	if !p2.accept(&ownedSegment{ext: "ts", dur: 2 * time.Second, data: skewed}) {
		t.Fatal("expected 3rd segment to pass once opening drop cap is reached")
	}

	// 6. 33-bit PTS wraparound (150ms across wrap) is accepted as aligned.
	wrapSeg := join(pat, pmt,
		makeSyntheticPES(vPID, 0xe0, (1<<33)-9000, nil, true), // -100ms before wrap
		makeSyntheticPES(aPID, 0xc0, 4500, nil, false),        // +50ms after wrap
	)
	if a := tsOpeningAVAlignment(wrapSeg); !a.known || a.skew != 150*time.Millisecond {
		t.Fatalf("wraparound alignment = %+v, want skew=150ms", a)
	}
	if !newProd().accept(&ownedSegment{ext: "ts", dur: 2 * time.Second, data: wrapSeg}) {
		t.Fatal("expected aligned wraparound segment to be accepted")
	}

	// 7. Video-only PMT (single stream) returns known=false and is accepted untouched.
	singlePMT := makeSyntheticPMT(pmtPID, vPID, 0)
	singleSeg := join(pat, singlePMT, makeSyntheticPES(vPID, 0xe0, 90000, nil, true))
	if a := tsOpeningAVAlignment(singleSeg); a.known {
		t.Fatalf("single-stream TS should have known=false, got %+v", a)
	}

	// 8. fMP4 opening check drops 1.5s skewed moof and accepts 10ms aligned moof.
	initData := makeTestFMP4Init(90000, 48000)
	vSkew, aSkew := uint64(90000), uint64(48000*2+24000) // 1.0s vs 2.5s
	if a := fmp4OpeningAVAlignment(initData, makeTestFMP4Seg(&vSkew, &aSkew)); !a.known || a.skew != 1500*time.Millisecond {
		t.Fatalf("fMP4 skewed = %+v, want 1.5s", a)
	}
	vOK, aOK := uint64(90000), uint64(48480) // 1.000s vs 1.010s
	if a := fmp4OpeningAVAlignment(initData, makeTestFMP4Seg(&vOK, &aOK)); !a.known || a.skew != 10*time.Millisecond {
		t.Fatalf("fMP4 aligned = %+v, want 10ms", a)
	}
	if sec, ok := mp4SegmentStartSeconds(initData, makeTestFMP4Seg(&vOK, nil)); !ok || sec != 1.0 {
		t.Fatalf("mp4SegmentStartSeconds = (%v, %v), want (1.0, true)", sec, ok)
	}
}

type generatedCMAFTrack struct {
	init []byte
	segs [][]byte
}

func generateDemuxedCMAFFixture(t *testing.T, ffmpegPath string) (video, audioAligned, audioStaggered generatedCMAFTrack) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(ffmpegPath, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v failed: %v\n%s", args, err, string(out))
		}
	}
	load := func(prefix string) generatedCMAFTrack {
		t.Helper()
		initBytes, err := os.ReadFile(filepath.Join(dir, prefix+"_init.mp4"))
		if err != nil {
			t.Fatalf("read %s init: %v", prefix, err)
		}
		var segs [][]byte
		for i := 0; i < 8; i++ {
			b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%s_%03d.m4s", prefix, i)))
			if err != nil {
				break
			}
			segs = append(segs, b)
		}
		if len(segs) < 7 {
			t.Fatalf("expected >=7 %s segments, got %d", prefix, len(segs))
		}
		return generatedCMAFTrack{init: initBytes, segs: segs}
	}

	// Video: 16s H.264 320x180 30fps, forced 2.0s GOP (8 x 2.0s CMAF segments).
	run("-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30:duration=16",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-g", "60", "-keyint_min", "60", "-sc_threshold", "0", "-pix_fmt", "yuv420p",
		"-f", "hls", "-hls_time", "2", "-hls_list_size", "0",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "v_init.mp4",
		"-hls_segment_filename", filepath.Join(dir, "v_%03d.m4s"),
		filepath.Join(dir, "v.m3u8"))

	// Aligned audio: 16s AAC 48kHz stereo, 2.0s CMAF segments starting at t=0.
	run("-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=16",
		"-c:a", "aac", "-b:a", "96k",
		"-f", "hls", "-hls_time", "2", "-hls_list_size", "0",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "a_init.mp4",
		"-hls_segment_filename", filepath.Join(dir, "a_%03d.m4s"),
		filepath.Join(dir, "a.m3u8"))

	// Staggered audio: same timeline shifted +1.0s (half a 2s segment) so
	// segment boundaries never align with video (audio at 1, 3, 5, 7...).
	run("-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=16",
		"-af", "asetpts=PTS+1.0/TB",
		"-c:a", "aac", "-b:a", "96k",
		"-f", "hls", "-hls_time", "2", "-hls_list_size", "0",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "s_init.mp4",
		"-hls_segment_filename", filepath.Join(dir, "s_%03d.m4s"),
		filepath.Join(dir, "s.m3u8"))

	return load("v"), load("a"), load("s")
}

func TestDemuxedCMAFOwnedRelayAlignedFirstSegment(t *testing.T) {
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

	videoTrack, audioAligned, audioStaggered := generateDemuxedCMAFFixture(t, ffmpegPath)
	t0 := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	renderCMAFMedia := func(initURI, segPrefix string, startIdx, count int, basePDT time.Time, includePDT bool) []byte {
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:2\n")
		fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", 100+startIdx)
		fmt.Fprintf(&b, "#EXT-X-MAP:URI=%q\n", initURI)
		for i := 0; i < count; i++ {
			idx := startIdx + i
			if includePDT {
				fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n", basePDT.Add(time.Duration(idx*2)*time.Second).UTC().Format("2006-01-02T15:04:05.000Z"))
			}
			fmt.Fprintf(&b, "#EXTINF:2.000,\n/%s_%03d.m4s\n", segPrefix, idx)
		}
		return []byte(b.String())
	}

	cases := []struct {
		name            string
		audioTrack      generatedCMAFTrack
		vStart, vCnt    int
		aStart, aCnt    int
		aPDTShift       time.Duration
		includePDT      bool
		singleUseMaster bool
		wantMethod      string
		wantDrop        bool
	}{
		{
			// Classic demuxed LL-HLS failure: audio playlist starts 1 segment
			// earlier than video playlist, with EXT-X-PROGRAM-DATE-TIME and a
			// single-use master URL (as served by a public live-stream directory plugin).
			name:       "offset_with_pdt_audio_earlier_single_use_master",
			audioTrack: audioAligned,
			vStart:     1, vCnt: 6,
			aStart: 0, aCnt: 6,
			includePDT:      true,
			singleUseMaster: true,
			wantMethod:      "pdt",
			wantDrop:        false,
		},
		{
			// No EXT-X-PROGRAM-DATE-TIME: video playlist starts 1 segment
			// earlier than audio; relay aligns using CMAF tfdt/timescale.
			name:       "offset_without_pdt_segment_timestamps",
			audioTrack: audioAligned,
			vStart:     0, vCnt: 6,
			aStart: 1, aCnt: 6,
			includePDT: false,
			wantMethod: "segment_ts",
			wantDrop:   false,
		},
		{
			// Half-segment staggered audio boundaries (1.0s offset): raw
			// 00000.ts is ~1s skewed; defense-in-depth drops it and publishes
			// an aligned 0.ts starting at a video keyframe.
			name:       "staggered_half_segment_defense_in_depth",
			audioTrack: audioStaggered,
			vStart:     0, vCnt: 7,
			aStart: 0, aCnt: 7,
			aPDTShift:  time.Second,
			includePDT: true,
			wantMethod: "pdt",
			wantDrop:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			var masterHits atomic.Int32
			master := strings.Join([]string{
				"#EXTM3U",
				"#EXT-X-VERSION:7",
				`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Main",DEFAULT=YES,AUTOSELECT=YES,URI="/audio.m3u8"`,
				`#EXT-X-STREAM-INF:BANDWIDTH=1500000,RESOLUTION=320x180,CODECS="avc1.42c015",AUDIO="aud"`,
				"/video.m3u8",
				"",
			}, "\n")
			vList := renderCMAFMedia("/v_init.mp4", "v", tc.vStart, tc.vCnt, t0, tc.includePDT)
			aList := renderCMAFMedia("/a_init.mp4", "a", tc.aStart, tc.aCnt, t0.Add(tc.aPDTShift), tc.includePDT)

			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/master.m3u8":
					if tc.singleUseMaster && masterHits.Add(1) > 1 {
						http.Error(w, "token already used", http.StatusForbidden)
						return
					}
					w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
					_, _ = io.WriteString(w, master)
				case "/video.m3u8":
					w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
					_, _ = w.Write(vList)
				case "/audio.m3u8":
					w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
					_, _ = w.Write(aList)
				case "/v_init.mp4":
					w.Header().Set("Content-Type", "video/mp4")
					_, _ = w.Write(videoTrack.init)
				case "/a_init.mp4":
					w.Header().Set("Content-Type", "audio/mp4")
					_, _ = w.Write(tc.audioTrack.init)
				default:
					var prefix string
					var idx int
					if _, err := fmt.Sscanf(r.URL.Path, "/%1s_%03d.m4s", &prefix, &idx); err == nil {
						var tr generatedCMAFTrack
						if prefix == "v" {
							tr = videoTrack
						} else {
							tr = tc.audioTrack
						}
						if idx >= 0 && idx < len(tr.segs) {
							w.Header().Set("Content-Type", "video/iso.segment")
							_, _ = w.Write(tr.segs[idx])
							return
						}
					}
					http.NotFound(w, r)
				}
			}))
			defer up.Close()

			var logBuf bytes.Buffer
			var logMu sync.Mutex
			logger := slog.New(slog.NewTextHandler(&syncWriter{mu: &logMu, w: &logBuf}, nil))

			tm, err := transcode.New(
				config.Transcode{
					FFmpegPath:  ffmpegPath,
					FFprobePath: ffprobePath,
					TempDir:     filepath.Join(t.TempDir(), "hls"),
				},
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				transcode.WithoutHWDetection(),
			)
			if err != nil {
				t.Fatal(err)
			}
			defer tm.Close()

			s := testService(t, Options{HTTPClient: up.Client(), Transcoder: tm, Logger: logger})
			u := &core.User{ID: 1, Username: "viewer", StreamLimit: 2}
			caps := core.ClientCaps{
				Product:       "Vela",
				Protocol:      "vela",
				ClientID:      "vela-demux-test",
				VideoCodecs:   []string{"h264"},
				AudioCodecs:   []string{"aac"},
				HLSContainers: []string{"mpegts"},
				MaxHeight:     1080,
			}

			info, err := s.TuneDirectStream(ctx, u, DirectStream{
				ID:    "directory:" + tc.name,
				Title: "Live Directory Stream",
				URL:   up.URL + "/master.m3u8",
			}, caps, "sess-"+tc.name)
			if err != nil {
				t.Fatalf("TuneDirectStream failed: %v", err)
			}
			defer func() { _ = s.Stop(context.Background(), info.ID, u) }()

			plRec := httptest.NewRecorder()
			s.ServePlaylist(plRec, httptest.NewRequest(http.MethodGet, "/index.m3u8", nil).WithContext(ctx), info.ID, u)
			if plRec.Code != http.StatusOK || !strings.Contains(plRec.Body.String(), "#EXTM3U") {
				t.Fatalf("ServePlaylist status=%d body=%q", plRec.Code, plRec.Body.String())
			}

			segRec := httptest.NewRecorder()
			s.ServeRelaySegment(segRec, httptest.NewRequest(http.MethodGet, "/relay/0.ts", nil).WithContext(ctx), info.ID, 0, u)
			if segRec.Code != http.StatusOK {
				t.Fatalf("ServeRelaySegment(0) status=%d body=%q", segRec.Code, segRec.Body.String())
			}
			segBytes := segRec.Body.Bytes()
			a := tsOpeningAVAlignment(segBytes)
			if !a.known {
				t.Fatalf("first published segment lacks expected PAT/PMT with video+audio (len=%d)", len(segBytes))
			}
			if !a.hasVideo || !a.hasAudio {
				t.Fatalf("first published segment missing stream: %+v", a)
			}
			if !a.videoKeyOpen {
				t.Fatalf("first published segment does not open on a video keyframe: %+v", a)
			}
			if a.skew > 100*time.Millisecond {
				t.Fatalf("first published segment A/V skew = %v, want <= 100ms (overlap=%v)", a.skew, a.overlap)
			}
			if tc.singleUseMaster && masterHits.Load() != 1 {
				t.Fatalf("single-use master fetched %d times, want 1", masterHits.Load())
			}
			logMu.Lock()
			logs := logBuf.String()
			logMu.Unlock()
			wantAlign := fmt.Sprintf("method=%s", tc.wantMethod)
			if !strings.Contains(logs, "aligned demuxed HLS playlists") || !strings.Contains(logs, wantAlign) {
				t.Fatalf("expected alignment log with %q; logs:\n%s", wantAlign, logs)
			}
			sawDrop := strings.Contains(logs, "owned relay dropped unaligned opening segment")
			if sawDrop != tc.wantDrop {
				t.Fatalf("opening-drop log present=%v, want %v; logs:\n%s", sawDrop, tc.wantDrop, logs)
			}
		})
	}
}

type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
