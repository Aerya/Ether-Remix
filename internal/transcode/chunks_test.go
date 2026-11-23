package transcode

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func everyKeyframe(span, every time.Duration) []core.KeyframePoint {
	var kfs []core.KeyframePoint
	for t := time.Duration(0); t < span; t += every {
		kfs = append(kfs, core.KeyframePoint{Offset: int64(t / time.Millisecond), Time: t})
	}
	return kfs
}

func boundaries(tl *Timeline) []float64 {
	var out []float64
	for n := 0; n <= tl.Total(); n++ {
		out = append(out, tl.Start(n).Seconds())
	}
	return out
}

func TestTimelineSnapsToDistantKeyframes(t *testing.T) {
	for _, tc := range []struct {
		every time.Duration
		want  []float64
	}{
		// Every boundary is the first keyframe at or after last+4s, however
		// far: the old plan gave up past one segment of drift and listed
		// 4s boundaries a copy can never cut at.
		{10 * time.Second, []float64{0, 10, 20, 30, 40, 50, 60}},
		{2500 * time.Millisecond, []float64{0, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60}},
	} {
		tl := buildTimeline(time.Minute, 4*time.Second, timelineOptions{
			steadyMode: ModeCopy, snap: true, keyframes: everyKeyframe(time.Minute, tc.every)})
		if got := boundaries(tl); fmt.Sprint(got) != fmt.Sprint(tc.want) || !tl.Keyframed() {
			t.Errorf("keyframes every %v: boundaries %v (keyframed %v), want %v", tc.every, got, tl.Keyframed(), tc.want)
		}
		if tl.TargetDuration() < tl.Dur(1) {
			t.Errorf("target duration %v below segment %v", tl.TargetDuration(), tl.Dur(1))
		}
	}
	// The index stops (truncated) or skips more than a few segments: past
	// that the boundaries are nominal, and regrouping cuts at the real
	// keyframes there.
	tl := buildTimeline(time.Minute, 4*time.Second, timelineOptions{
		steadyMode: ModeCopy, snap: true, keyframes: everyKeyframe(30*time.Second, 10*time.Second)})
	if got := boundaries(tl); fmt.Sprint(got) != "[0 10 20 24 28 32 36 40 44 48 52 56 60]" {
		t.Errorf("index ending at 30s: %v", got)
	}
	gap := []core.KeyframePoint{{Time: 0}, {Time: 5 * time.Second}, {Time: 45 * time.Second}, {Time: 50 * time.Second}}
	tl = buildTimeline(time.Minute, 4*time.Second, timelineOptions{steadyMode: ModeCopy, snap: true, keyframes: gap})
	if got := boundaries(tl); fmt.Sprint(got) != "[0 5 9 13 17 21 25 29 45 50 60]" {
		t.Errorf("index with a 40s gap: %v", got)
	}
	// A fast-start ramp keeps its short segments until copy can begin.
	tl = buildTimeline(time.Minute, 4*time.Second, timelineOptions{steadyMode: ModeCopy, snap: true,
		fastStart: true, rampSegs: 2, rampDur: 2 * time.Second, keyframes: everyKeyframe(time.Minute, 9*time.Second)})
	if got := boundaries(tl); fmt.Sprint(got) != "[0 2 4 6 8 9 18 27 36 45 54 60]" || tl.SwitchSeg != 5 ||
		tl.ModeAt(4) != ModeFastStart || tl.ModeAt(5) != ModeCopy {
		t.Errorf("ramp to a 9s keyframe: %v switch %d", got, tl.SwitchSeg)
	}
	if buildTimeline(time.Minute, 4*time.Second, timelineOptions{steadyMode: ModeCopy}).Keyframed() {
		t.Error("nominal plan reports keyframed")
	}
}

func TestTSChunkStart(t *testing.T) {
	dir := t.TempDir()
	pat := make([]byte, 188) // PAT: unit start, not a PES
	pat[0], pat[1], pat[3], pat[5] = 0x47, 0x40, 0x10, 0x00
	audio := testTSChunk(7*time.Second, "audio")
	audio[3+4] = 0xC0 // stream id: audio
	video := testTSChunk(9500*time.Millisecond, "video")
	// Give the video packet an adaptation field (PCR) before its PES.
	withAF := make([]byte, 188)
	copy(withAF, video[:4])
	withAF[3] = 0x30
	withAF[4], withAF[5] = 7, 0x10
	copy(withAF[12:], video[4:180])
	write := func(name string, parts ...[]byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, bytes.Join(parts, nil), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got, err := tsChunkStart(write("v.ts", pat, audio, withAF)); err != nil || got != 9500*time.Millisecond {
		t.Errorf("video PTS: %v %v", got, err)
	}
	if got, err := tsChunkStart(write("a.ts", []byte{1, 2, 3}, pat, audio)); err != nil || got != 7*time.Second {
		t.Errorf("audio-only PTS after junk: %v %v", got, err)
	}
	if _, err := tsChunkStart(write("none.ts", pat)); err == nil {
		t.Error("no PES: no error")
	}
	if _, err := tsChunkStart(write("short.ts", video[:100])); err == nil {
		t.Error("truncated packet: no error")
	}
}

func TestMatroskaChunkStart(t *testing.T) {
	dir := t.TempDir()
	cluster := func(kids ...[]byte) []byte {
		body := bytes.Join(kids, nil)
		return append([]byte{0x1F, 0x43, 0xB6, 0x75, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, body...)
	}
	crc := []byte{0xBF, 0x84, 1, 2, 3, 4}
	ts := []byte{0xE7, 0x83, 0x00, 0x75, 0x4F} // 30031 ms
	block := []byte{0xA3, 0x81, 0x00}
	at := 30031*time.Millisecond - matroskaOrigin
	// matroskaenc's layout with B-frames: the audio held back for the
	// keyframe (track 2, 208 ms early) opens the Cluster; the keyframe
	// follows, then an open-GOP leading picture 125 ms before it.
	simple := func(track byte, rel int16, payload ...byte) []byte {
		b := []byte{0xA3, byte(0x80 | (4 + len(payload))), 0x80 | track, byte(uint16(rel) >> 8), byte(rel), 0x80}
		return append(b, payload...)
	}
	audio := simple(2, -208, 0xAA, 0xAA)
	key := simple(1, 0, 0x26, 0x01)
	leading := simple(1, -125, 0x02, 0x01)
	group := []byte{0xA0, 0x8C, 0x9B, 0x81, 0x10, 0xA1, 0x86, 0x81, 0x00, 0x05, 0x80, 0x26, 0x01} // BlockDuration, Block(track 1, +5)
	for _, tc := range []struct {
		b     []byte
		video uint64
		want  time.Duration
		ok    bool
	}{
		{cluster(crc, ts, block), 0, at, true},
		{cluster(ts), 0, at, true},
		{cluster(ts, audio, key, leading), 1, at, true},
		{cluster(ts, audio, leading, key), 1, at - 125*time.Millisecond, true},
		{cluster(ts, audio, group), 1, at + 5*time.Millisecond, true},
		{cluster(ts, audio), 1, at, true}, // no video: the Cluster's time
		{cluster(ts, audio, key), 0, at, true},
		{cluster(block, ts), 0, 0, false},
		{cluster(audio, ts, key), 1, 0, false},
		{[]byte{0x1A, 0x45, 0xDF, 0xA3, 0x80}, 0, 0, false},
		{cluster([]byte{0xE7, 0x89, 1, 2, 3, 4, 5, 6, 7, 8, 9}), 0, 0, false},
		{cluster(ts[:4]), 0, 0, false},
		{[]byte{0x1F, 0x43, 0xB6, 0x75}, 0, 0, false},
		{nil, 0, 0, false},
	} {
		p := filepath.Join(dir, "c.mkv")
		if err := os.WriteFile(p, tc.b, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := mkvChunkStart(p, tc.video)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("% x (video %d): %v %v, want %v ok=%v", tc.b, tc.video, got, err, tc.want, tc.ok)
		}
	}
}

func TestMatroskaVideoTrack(t *testing.T) {
	dir := t.TempDir()
	el := func(id []byte, body ...[]byte) []byte {
		b := bytes.Join(body, nil)
		return append(append(append([]byte(nil), id...), byte(0x80|len(b))), b...)
	}
	// hvcC with lengthSizeMinusOne 3 in byte 21.
	hvcC := make([]byte, 23)
	hvcC[0], hvcC[21] = 1, 0xF3
	entry := func(number, kind byte, extra ...[]byte) []byte {
		return el([]byte{0xAE}, append([][]byte{el([]byte{0xD7}, []byte{number}), el([]byte{0x83}, []byte{kind})}, extra...)...)
	}
	hevc := [][]byte{el([]byte{0x86}, []byte("V_MPEGH/ISO/HEVC")), el([]byte{0x63, 0xA2}, hvcC)}
	ebml := el([]byte{0x1A, 0x45, 0xDF, 0xA3}, el([]byte{0x42, 0x82}, []byte("matroska")))
	tracks := el([]byte{0x16, 0x54, 0xAE, 0x6B}, entry(1, 2), entry(2, 1, hevc...))
	unknown := []byte{0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	for _, tc := range []struct {
		name   string
		b      []byte
		want   uint64
		nalLen int
		ok     bool
	}{
		{"unknown-size segment, audio first", bytes.Join([][]byte{ebml, unknown, tracks}, nil), 2, 4, true},
		{"placeholder segment size", bytes.Join([][]byte{ebml, {0x18, 0x53, 0x80, 0x67, 0x01, 0, 0, 0, 0, 0, 0x10, 0}, tracks}, nil), 2, 4, true},
		{"annex b hevc", bytes.Join([][]byte{ebml, unknown, el([]byte{0x16, 0x54, 0xAE, 0x6B}, entry(1, 1, hevc[0], el([]byte{0x63, 0xA2}, []byte{0, 0, 0, 1, 0x40, 1})))}, nil), 1, 0, true},
		{"hevc without CodecPrivate", bytes.Join([][]byte{ebml, unknown, el([]byte{0x16, 0x54, 0xAE, 0x6B}, entry(1, 1, hevc[0]))}, nil), 1, -1, true},
		{"hevc with reserved length size", bytes.Join([][]byte{ebml, unknown, el([]byte{0x16, 0x54, 0xAE, 0x6B}, entry(1, 1, hevc[0], el([]byte{0x63, 0xA2}, append(append([]byte{1}, make([]byte, 20)...), 0xFE, 0))))}, nil), 1, -1, true},
		{"h264", bytes.Join([][]byte{ebml, unknown, el([]byte{0x16, 0x54, 0xAE, 0x6B}, entry(1, 1, el([]byte{0x86}, []byte("V_MPEG4/ISO/AVC")), el([]byte{0x63, 0xA2}, hvcC)))}, nil), 1, 0, true},
		{"audio only", bytes.Join([][]byte{ebml, unknown, el([]byte{0x16, 0x54, 0xAE, 0x6B}, entry(1, 2))}, nil), 0, 0, true},
		{"cluster ends the header", bytes.Join([][]byte{ebml, unknown, {0x1F, 0x43, 0xB6, 0x75, 0x80}, tracks}, nil), 0, 0, true},
		{"bad track number", bytes.Join([][]byte{ebml, unknown, el([]byte{0x16, 0x54, 0xAE, 0x6B}, el([]byte{0xAE}, el([]byte{0xD7})))}, nil), 0, 0, false},
	} {
		p := filepath.Join(dir, "h.mkv")
		if err := os.WriteFile(p, tc.b, 0o644); err != nil {
			t.Fatal(err)
		}
		got, nalLen, err := mkvVideoTrack(p)
		if (err == nil) != tc.ok || got != tc.want || nalLen != tc.nalLen {
			t.Errorf("%s: track %d NAL length %d %v, want %d %d ok=%v", tc.name, got, nalLen, err, tc.want, tc.nalLen, tc.ok)
		}
	}
}

func TestFMP4ChunkStartPicksTrack(t *testing.T) {
	dir := t.TempDir()
	// Two track fragments: audio (track 2) earlier than video (track 1).
	traf := func(id uint32, tfdt uint64) []byte {
		return mkBox("traf", mkFullBox("tfhd", 0, 0x020000, u32(id)), mkFullBox("tfdt", 1, 0, u64(tfdt)))
	}
	moof := mkBox("moof", mkFullBox("mfhd", 0, 0, u32(1)), traf(2, 48000*19), traf(1, 90000*20))
	chunk := bytes.Join([][]byte{mkBox("styp", []byte("msdh")), moof, mkBox("mdat", []byte("x"))}, nil)
	p := filepath.Join(dir, "c.m4s")
	if err := os.WriteFile(p, chunk, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := fmp4ChunkStart(p, 1, 90000); err != nil || got != 20*time.Second {
		t.Errorf("video track: %v %v", got, err)
	}
	if _, err := fmp4ChunkStart(p, 3, 90000); err == nil {
		t.Error("absent track: no error")
	}
	if err := os.WriteFile(p, chunk[:len(chunk)-len(moof)/2], 0o644); err != nil { // inside the moof
		t.Fatal(err)
	}
	if _, err := fmp4ChunkStart(p, 1, 90000); err == nil {
		t.Error("truncated chunk: no error")
	}
	tracks, err := parseInitTracks(testVideoInit(1280, 720))
	if err != nil || len(tracks) != 1 || tracks[0].Handler != "vide" || tracks[0].Timescale != 90000 || tracks[0].TrackID != 1 {
		t.Errorf("init tracks %+v %v", tracks, err)
	}
}

func TestAssembleChunks(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) fmp4Chunk {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return fmp4Chunk{path: p}
	}
	var removed []string
	rm := func(p string) { removed = append(removed, filepath.Base(p)); _ = os.Remove(p) }
	// fMP4: one styp, no sidx (each described a single chunk).
	a := write("a.m4s", testChunk(1, 0, 3750, 24, []byte("AAAA")))
	b := write("b.m4s", testChunk(2, 90000, 3750, 24, []byte("BBBB")))
	dst := filepath.Join(dir, "seg.m4s")
	if err := assembleChunks(dst, ContainerFMP4, []fmp4Chunk{a, b}, rm); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	boxes, err := readBoxes(got, 0, len(got))
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, x := range boxes {
		types = append(types, x.typ)
	}
	if strings.Join(types, ",") != "styp,moof,mdat,moof,mdat" || fmt.Sprint(removed) != "[a.m4s b.m4s]" {
		t.Errorf("boxes %v removed %v", types, removed)
	}
	// MPEG-TS: plain concatenation; a single chunk is renamed.
	c, d := write("c.ts", []byte("CC")), write("d.ts", []byte("DD"))
	if err := assembleChunks(filepath.Join(dir, "seg.ts"), ContainerMPEGTS, []fmp4Chunk{c, d}, rm); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "seg.ts")); string(b) != "CCDD" {
		t.Errorf("ts segment %q", b)
	}
	e := write("e.ts", []byte("EE"))
	if err := assembleChunks(filepath.Join(dir, "one.ts"), ContainerMPEGTS, []fmp4Chunk{e}, rm); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.path); !os.IsNotExist(err) {
		t.Error("single chunk not moved")
	}
	if err := assembleChunks(filepath.Join(dir, "x.ts"), ContainerMPEGTS, []fmp4Chunk{{path: filepath.Join(dir, "gone")}, e}, rm); err == nil {
		t.Error("missing chunk: no error")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.ts.tmp")); !os.IsNotExist(err) {
		t.Error("failed assembly left its temp file")
	}
}

func TestParseFFmpegPlaylistEntries(t *testing.T) {
	p := parseFFmpegPlaylist([]byte("#EXTM3U\n#EXTINF:2.502500,\nc-1/00000.ts\n#EXTINF:bad,\n00001.ts\n#EXTINF:1e300,\n00002.ts\n#EXT-X-ENDLIST\n"))
	if len(p.entries) != 3 || p.entries[0].name != "00000.ts" || p.entries[0].dur != 2502500*time.Microsecond ||
		p.entries[1].dur != 0 || p.entries[2].dur != 0 || !p.ended {
		t.Errorf("%+v", p)
	}
}

// chunkSession starts a keyframe-planned TS copy session on the fake
// runner: 60s with keyframes every gop.
func chunkSession(t *testing.T, fr *fakeRunner, gop time.Duration, opts ...Option) *Session {
	t.Helper()
	fr.chunkGOP = gop
	m := newTestManager(t, fr, config.Transcode{}, opts...)
	info := mustParse(t, "mp4_h264_aac.json")
	info.Duration = time.Minute
	info.Keyframes = everyKeyframe(time.Minute, gop)
	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMPEGTS, VideoCopy: true, AudioCopy: true, AudioStream: 1}
	hs, err := m.StartHLS(t.Context(), core.HLSRequest{SessionID: "chunks", InputURL: "http://127.0.0.1:1/source", Info: info, Decision: d})
	if err != nil {
		t.Fatal(err)
	}
	s := hs.(*Session)
	if !s.plan.Keyframed() {
		t.Fatal("plan not keyframed")
	}
	return s
}

// chunkTimesOf is the media times of the fake chunks a segment holds.
func chunkTimesOf(t *testing.T, body []byte) []float64 {
	t.Helper()
	var out []float64
	for _, m := range tsChunkMarkers(body) {
		i := strings.LastIndex(m, " t=")
		if i < 0 {
			t.Fatalf("marker %q", m)
		}
		f, err := strconv.ParseFloat(m[i+3:], 64)
		if err != nil {
			t.Fatalf("marker %q", m)
		}
		out = append(out, f)
	}
	return out
}

func TestChunkedSessionRegroupsOntoPlan(t *testing.T) {
	fr := newFakeRunner()
	fr.chunkEnd = time.Minute
	s := chunkSession(t, fr, 2500*time.Millisecond, WithBuffering(8*time.Second, 0, 0))
	if s.total != 12 {
		t.Fatalf("total %d", s.total)
	}
	for n := 0; n < 12; n++ {
		r := get(t, s, n)
		if r.Code != 200 {
			t.Fatalf("segment %d: %d %s", n, r.Code, r.Body.String())
		}
		got := fmt.Sprint(chunkTimesOf(t, r.Body.Bytes()))
		if want := fmt.Sprint([]float64{float64(5 * n), float64(5*n) + 2.5}); got != want {
			t.Errorf("segment %d holds chunks %s, want %s", n, got, want)
		}
	}
	if r := get(t, s, 12); r.Code != 404 {
		t.Errorf("segment past the plan: %d", r.Code)
	}
	args := fr.lastStart()
	if argValue(args, "-hls_time") != "0.500" || argValue(args, "-start_number") != "0" || hasArg(args, "-force_key_frames") {
		t.Errorf("chunked copy args %v", args)
	}
}

func TestChunkedSessionRestartDropsPreroll(t *testing.T) {
	fr := newFakeRunner()
	fr.segDelay = 20 * time.Millisecond
	s := chunkSession(t, fr, 10*time.Second, WithBuffering(4*time.Second, 0, 0))
	if s.total != 6 {
		t.Fatalf("total %d", s.total)
	}
	// Far ahead of the encoder: restart at 40s; ffmpeg's seek lands on the
	// 30s keyframe, whose interval belongs to segment 3.
	r := get(t, s, 4)
	if r.Code != 200 || fmt.Sprint(chunkTimesOf(t, r.Body.Bytes())) != "[40]" {
		t.Fatalf("segment 4 after restart: %d %v", r.Code, tsChunkMarkers(r.Body.Bytes()))
	}
	if !strings.Contains(tsChunkMarkers(r.Body.Bytes())[0], "ss=40.000") {
		t.Errorf("segment 4 not from the restart: %v", tsChunkMarkers(r.Body.Bytes()))
	}
	s.mu.Lock()
	done3 := s.done[0][3]
	s.mu.Unlock()
	if done3 {
		t.Error("the restart's pre-roll was published as segment 3")
	}
	// Behind the run: restart again.
	if r := get(t, s, 1); r.Code != 200 || fmt.Sprint(chunkTimesOf(t, r.Body.Bytes())) != "[10]" {
		t.Fatalf("segment 1: %d %v", r.Code, tsChunkMarkers(r.Body.Bytes()))
	}
	// No chunk directory of a finished run survives it.
	s.mu.Lock()
	err := s.startRunLocked(5, false)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "finished runs' chunk directories removed", func() bool {
		dirs, _ := filepath.Glob(filepath.Join(s.dir, "c-*"))
		return len(dirs) == 1
	})
}

// A GOP longer than several plan segments (an index from another release
// after failover): the segments it spans are served its media rather than
// never produced.
func TestChunkedSessionAliasesSegmentsInsideAGOP(t *testing.T) {
	fr := newFakeRunner()
	fr.chunkEnd = time.Minute
	s := chunkSession(t, fr, 2500*time.Millisecond, WithBuffering(30*time.Second, 0, 0))
	fr.mu.Lock()
	fr.chunkGOP = 20 * time.Second // the new release's keyframes
	fr.mu.Unlock()
	if err := s.SwapInput(t.Context(), "http://127.0.0.1:1/other", s.Info(), 0); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 12; n++ {
		r := get(t, s, n)
		if r.Code != 200 {
			t.Fatalf("segment %d: %d", n, r.Code)
		}
		times := chunkTimesOf(t, r.Body.Bytes())
		want := math.Floor(float64(5*n)/20) * 20
		if len(times) != 1 || times[0] != want {
			t.Errorf("segment %d holds %v, want the GOP at %v", n, times, want)
		}
	}
}

// Matroska without a keyframe index, GOPs longer than a segment: in order,
// a segment inside a GOP is an empty Cluster (the joined stream must not
// repeat the GOP); asked for out of order (a seek), it is the GOP.
func TestChunkedMatroskaAliasServedForSeeks(t *testing.T) {
	fr := newFakeRunner()
	fr.chunkGOP = 10 * time.Second
	fr.chunkEnd = time.Minute
	m := newTestManager(t, fr, config.Transcode{}, WithBuffering(60*time.Second, 0, 0))
	info := mustParse(t, "mp4_h264_aac.json")
	info.Duration = time.Minute
	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioCopy: true, AudioStream: 1}
	hs, err := m.StartHLS(t.Context(), core.HLSRequest{SessionID: "mkv-alias", InputURL: "http://127.0.0.1:1/source", Info: info, Decision: d})
	if err != nil {
		t.Fatal(err)
	}
	s := hs.(*Session)
	if s.plan.Keyframed() || s.total != 15 {
		t.Fatalf("plan keyframed=%v total=%d", s.plan.Keyframed(), s.total)
	}
	body := func(n int) []byte {
		t.Helper()
		r := get(t, s, n)
		if r.Code != 200 {
			t.Fatalf("segment %d: %d", n, r.Code)
		}
		return r.Body.Bytes()
	}
	// A probe requests metadata only: the next GET still needs its GOP.
	head := httptest.NewRecorder()
	s.ServeSegment(head, mediaRequest(s, httptest.NewRequest(http.MethodHead, "/0.mkv", nil), 0), 0)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD segment 0: %d %q", head.Code, head.Body.Bytes())
	}
	if b := body(1); !bytes.Contains(b, []byte("t=0.000")) {
		t.Errorf("first GET after HEAD: %q", b)
	}
	for n := 0; n < 4; n++ {
		b := body(n)
		if empty := len(b) <= emptyClusterSize; empty != (n == 1 || n == 3) {
			t.Errorf("in order, segment %d is %d bytes", n, len(b))
		}
	}
	// Segment 1 [4s,8s) lies inside the GOP at 0s; segment 3 [12s,16s)
	// inside the one at 10s, which starts segment 2.
	b := body(1)
	if !bytes.Contains(b, []byte("t=0.000")) {
		t.Errorf("seek to segment 1: %q", b)
	}
	if retry := body(1); !bytes.Equal(retry, b) {
		t.Errorf("retry changed segment 1: %q -> %q", b, retry)
	}
	req := httptest.NewRequest(http.MethodGet, "/1.mkv", nil)
	req.Header.Set("Range", "bytes=4-")
	rec := httptest.NewRecorder()
	s.ServeSegment(rec, mediaRequest(s, req, 1), 1)
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), b[4:]) {
		t.Errorf("range retry changed segment 1: status=%d body=%q", rec.Code, rec.Body.Bytes())
	}
	if b := body(3); !bytes.Contains(b, []byte("t=10.000")) {
		t.Errorf("seek to segment 3: %q", b)
	}
	if b := body(4); len(b) > emptyClusterSize {
		t.Errorf("segment 4 after 3 (in order) is %d bytes, want the empty Cluster", len(b))
	}
	if b := body(4); len(b) > emptyClusterSize {
		t.Errorf("retry of sequential segment 4 is %d bytes, want the empty Cluster", len(b))
	}
	_ = body(0)
	// Fetching the init starts a new decoder, even if its first segment
	// happens to follow the previous decoder's last request.
	if init := get(t, s, InitSegment); init.Code != http.StatusOK {
		t.Fatalf("init: %d", init.Code)
	}
	if b := body(1); !bytes.Contains(b, []byte("t=0.000")) {
		t.Errorf("first GET after init: %q", b)
	}
}

// A restart behind the encoder whose copy reads a GOP the player already
// holds (here from 0s to 10s, past the restart's segment [4s,8s)) drops
// that GOP and keeps the plan: the segments inside it are aliases as on
// the first run, and the next keyframe starts its own segment, not the
// restart's.
func TestChunkedRestartInsidePublishedGOPKeepsPlan(t *testing.T) {
	for _, container := range []string{ContainerMPEGTS, ContainerFMP4, ContainerMatroska} {
		t.Run(container, func(t *testing.T) {
			fr := newFakeRunner()
			fr.chunkGOP = 10 * time.Second
			fr.chunkEnd = time.Minute
			m := newTestManager(t, fr, config.Transcode{}, WithBuffering(60*time.Second, 0, 0))
			info := mustParse(t, "mp4_h264_aac.json")
			info.Duration = time.Minute
			d := core.Decision{Kind: core.DecisionRemux, Container: container, VideoCopy: true, AudioCopy: true, AudioStream: 1}
			hs, err := m.StartHLS(t.Context(), core.HLSRequest{SessionID: "restart-in-gop-" + container, InputURL: "http://127.0.0.1:1/source", Info: info, Decision: d})
			if err != nil {
				t.Fatal(err)
			}
			s := hs.(*Session)
			body := func(n int) []byte {
				t.Helper()
				r := get(t, s, n)
				if r.Code != 200 {
					t.Fatalf("segment %d: %d", n, r.Code)
				}
				return r.Body.Bytes()
			}
			// The fake's marker names the GOP a segment holds.
			gop := func(b []byte) string {
				if i := bytes.Index(b, []byte(" t=")); i >= 0 {
					end := i + 3
					for end < len(b) && (b[end] == '.' || b[end] >= '0' && b[end] <= '9') {
						end++
					}
					return string(b[i+1 : end])
				}
				return fmt.Sprintf("%d bytes, no GOP", len(b))
			}
			if b := body(0); gop(b) != "t=0.000" {
				t.Fatalf("segment 0: %s", gop(b))
			}
			s.mu.Lock()
			for n := range s.done[0] {
				if n > 0 {
					delete(s.done[0], n)
				}
			}
			err = s.startRunLocked(1, false)
			s.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			b := body(1)
			switch {
			case container == ContainerMatroska && len(b) > emptyClusterSize:
				t.Errorf("segment 1 after 0 is %d bytes, want the empty Cluster inside the GOP at 0s", len(b))
			case container == ContainerFMP4 && !isEmptyFMP4(b):
				t.Errorf("segment 1 after 0 is %d bytes, want the empty fragment inside the GOP at 0s", len(b))
			case container == ContainerMPEGTS && gop(b) != "t=0.000":
				t.Errorf("segment 1: %s, want the GOP at 0s again", gop(b))
			}
			if b := body(2); gop(b) != "t=10.000" {
				t.Errorf("segment 2: %s, want the GOP at 10s", gop(b))
			}
			if starts := fr.startCount(); starts != 2 {
				t.Errorf("%d ffmpeg runs, want 2", starts)
			}
		})
	}
}

// Issue #36: when a title's video track ends earlier than its container and
// audio duration (e.g. Lupin S1E1: video ends at 46:19.88, segment 694, while
// audio runs to 48:39.20, segment 729), ssegment muxes all trailing audio
// into the final video chunk and reports only the video duration in EXTINF.
// Extending the final chunk's end to its last Cluster/block timestamp lets
// the slotter alias the trailing audio-only segments without triggering
// empty restarts at 695/696 or returning 502.
func TestChunkedMatroskaTailAudioOutlastsVideo(t *testing.T) {
	fr := newFakeRunner()
	fr.chunkGOP = 4 * time.Second
	fr.chunkEnd = 44 * time.Second      // video ends at segment 10 (40..44s)
	fr.chunkAudioEnd = 60 * time.Second // audio and container continue to segment 14 (56..60s)
	// Use a 16s ahead limit (4 segments) to also exercise a multi-segment
	// batch completing at EOF past the throttle window without discarding
	// the tail segments.
	m := newTestManager(t, fr, config.Transcode{}, WithBuffering(8*time.Second, 16*time.Second, 3*time.Minute))
	info := mustParse(t, "mp4_h264_aac.json")
	info.Duration = 60 * time.Second
	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioCopy: true, AudioStream: 1}
	hs, err := m.StartHLS(t.Context(), core.HLSRequest{SessionID: "mkv-tail-audio", InputURL: "http://127.0.0.1:1/source", Info: info, Decision: d})
	if err != nil {
		t.Fatal(err)
	}
	s := hs.(*Session)
	if s.total != 15 {
		t.Fatalf("total = %d, want 15", s.total)
	}
	// Resume at segment 8 (32s), like a tail resume.
	for n := 8; n < s.total; n++ {
		r := get(t, s, n)
		if r.Code != http.StatusOK {
			t.Fatalf("segment %d: status %d (%s)", n, r.Code, r.Body.String())
		}
		if n <= 10 && len(r.Body.Bytes()) <= emptyClusterSize {
			t.Errorf("segment %d is %d bytes, want video+audio cluster", n, len(r.Body.Bytes()))
		}
		if n > 10 && len(r.Body.Bytes()) > emptyClusterSize {
			t.Errorf("sequential tail segment %d is %d bytes, want empty Cluster alias", n, len(r.Body.Bytes()))
		}
	}
	if starts := fr.startCount(); starts != 2 {
		t.Errorf("ffmpeg starts = %d, want 2 (initial + seek to 8, no empty restarts at 11/12)", starts)
	}
}

// Issue #36: when a single poll collects a batch of chunks that crosses the
// aheadLimit and throttleLocked pauses (rather than stops) the run, the
// remaining segments already consumed from cr.next and cr.sl must still be
// published so no hole (like 00655.mkv) is left when the run resumes.
func TestChunkedPauseMidBatchPublishesConsumedSegments(t *testing.T) {
	fr := newFakeRunner()
	fr.segDelay = time.Millisecond
	fr.chunkGOP = 4 * time.Second
	fr.chunkEnd = 80 * time.Second
	// Disable background polling so one collectChunks(r, false) sees a
	// multi-segment batch that crosses the 4-segment aheadLimit.
	m := newTestManager(t, fr, config.Transcode{}, WithBuffering(4*time.Second, 16*time.Second, 3*time.Minute), withPollInterval(time.Hour))
	info := mustParse(t, "mp4_h264_aac.json")
	info.Duration = 80 * time.Second
	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioCopy: true, AudioStream: 1}
	hs, err := m.StartHLS(t.Context(), core.HLSRequest{SessionID: "mkv-pause-batch", InputURL: "http://127.0.0.1:1/source", Info: info, Decision: d})
	if err != nil {
		t.Fatal(err)
	}
	s := hs.(*Session)
	s.mu.Lock()
	r := s.cur
	s.mu.Unlock()
	// Wait until fakeProc has written at least 9 chunks (segments 0..7 closed).
	waitFor(t, "8 chunks written Before first poll", func() bool {
		_, err := os.Stat(filepath.Join(r.chunks.dir, "00008.mkv"))
		return err == nil
	})
	s.collectChunks(r, false)
	s.mu.Lock()
	paused := r.paused
	stopped := r.stopped
	maxDone := r.maxDone
	pausedDone := r.pausedDone
	var missing []int
	for n := 0; n <= maxDone; n++ {
		if !s.done[0][n] {
			missing = append(missing, n)
		}
	}
	s.mu.Unlock()
	if !paused || stopped {
		t.Fatalf("paused=%v stopped=%v, want paused && !stopped", paused, stopped)
	}
	if maxDone < 7 {
		t.Fatalf("maxDone = %d, want >= 7 (entire consumed batch published)", maxDone)
	}
	if pausedDone != maxDone {
		t.Fatalf("pausedDone = %d, want maxDone (%d)", pausedDone, maxDone)
	}
	if len(missing) > 0 {
		t.Fatalf("unpublished segments in 0..%d: %v", maxDone, missing)
	}
}

func TestCollectChunksFMP4ReinitAndInBandMoov(t *testing.T) {
	ftyp := mkBox("ftyp", []byte("iso5"), u32(512), []byte("iso5iso6mp41"))
	makeTrak := func(id, scale uint32, handler string, entry []byte) []byte {
		tkhd := mkFullBox("tkhd", 0, 3, u32(0), u32(0), u32(id), u32(0), u32(0), make([]byte, 60))
		mdhd := mkFullBox("mdhd", 0, 0, u32(0), u32(0), u32(scale), u32(0), u16(0x55c4), u16(0))
		hdlr := mkFullBox("hdlr", 0, 0, u32(0), []byte(handler), make([]byte, 12), []byte("Handler\x00"))
		stbl := mkBox("stbl", mkFullBox("stsd", 0, 0, u32(1), entry))
		return mkBox("trak", tkhd, mkBox("mdia", mdhd, hdlr, mkBox("minf", stbl)))
	}
	mvhd := mkFullBox("mvhd", 0, 0, u32(0), u32(0), u32(1000), u32(0), make([]byte, 80))
	vTrak1080 := makeTrak(1, 90000, "vide", avc1Entry(1920, 1080, 0x64, 0x00, 0x28))
	vTrak720 := makeTrak(1, 90000, "vide", avc1Entry(1280, 720, 0x64, 0x00, 0x28))
	ec3Incomplete := makeTrak(2, 48000, "soun", audioEntry("ec-3", 6, 48000))
	ec3Complete := makeTrak(2, 48000, "soun", audioEntry("ec-3", 6, 48000, mkBox("dec3", []byte{0x0c, 0x00, 0x20, 0x0f, 0x00})))
	mvexDual := mkBox("mvex",
		mkFullBox("trex", 0, 0, u32(1), u32(1), u32(0), u32(0), u32(0)),
		mkFullBox("trex", 0, 0, u32(2), u32(1), u32(0), u32(0), u32(0)),
	)
	mvexSingle := mkBox("mvex", mkFullBox("trex", 0, 0, u32(1), u32(1), u32(0), u32(0), u32(0)))

	// Unclosed moov (size 0, no mvex, ec-3 without dec3), matching FFmpeg's
	// aborted mov_write_moov_tag before EAC3 packets are parsed.
	unclosedMoovBody := bytes.Join([][]byte{mvhd, vTrak1080, ec3Incomplete}, nil)
	unclosedMoov := append(append(u32(0), "moov"...), unclosedMoovBody...)
	completeDualMoov := mkBox("moov", mvhd, vTrak1080, ec3Complete, mvexDual)

	newChunkSession := func(t *testing.T, startAt time.Duration, startSeg, total int) (*Session, *run, string, string) {
		t.Helper()
		dir := t.TempDir()
		chunks := filepath.Join(dir, "c-1")
		if err := os.MkdirAll(chunks, 0o755); err != nil {
			t.Fatal(err)
		}
		p := &blockedProc{done: make(chan struct{})}
		t.Cleanup(p.Stop)
		initPath := filepath.Join(dir, "init-1.mp4")
		r := &run{
			seq:      1,
			startSeg: startSeg,
			proc:     p,
			maxDone:  -1,
			stderr:   newTailBuffer(10),
			spec:     runSpec{Dir: dir, Playlist: "run-1.m3u8", InitName: "init-1.mp4", Start: startAt, StartSeg: startSeg},
			chunks: &chunkRun{
				dir:       chunks,
				container: ContainerFMP4,
				initPath:  initPath,
				startAt:   startAt,
				sl:        newSlotter(0, 4*time.Second, total, startSeg),
			},
		}
		s := &Session{
			m:         &Manager{},
			dir:       dir,
			container: ContainerFMP4,
			segDur:    4 * time.Second,
			total:     total,
			log:       testLogger(t),
			sessionEncoding: sessionEncoding{
				cur:    r,
				endSeg: -1,
			},
			sessionDelivery: sessionDelivery{
				done:    []map[int]bool{{}},
				changed: make(chan struct{}),
			},
		}
		return s, r, dir, chunks
	}

	t.Run("moov_without_moof_then_moov_with_moof", func(t *testing.T) {
		// Run restarted at segment 1 (4s). Chunk 0 is an aborted moov-only cut;
		// chunk 1 holds the complete moov plus buffered samples [2s..6s] with a
		// 2s EXTINF that would falsely look like preroll (2s+2s <= 4s) without
		// reading the chunk's real sample end; chunk 2 closes segment 1 at 8s.
		s, r, dir, chunks := newChunkSession(t, 4*time.Second, 1, 3)
		if err := os.WriteFile(filepath.Join(dir, "init-1.mp4"), append(append([]byte(nil), ftyp...), unclosedMoov...), 0o644); err != nil {
			t.Fatal(err)
		}
		styp := mkBox("styp", []byte("msdh"), u32(0), []byte("msdhmsix"))
		c0 := bytes.Join([][]byte{styp, ftyp, unclosedMoov}, nil)
		// Strip styp/sidx from testChunk so we can place ftyp+completeDualMoov before moof.
		raw1 := testChunk(1, 2*90000, 90000, 4, []byte("gop0gop1"))
		top1, err := readBoxes(raw1, 0, len(raw1))
		if err != nil {
			t.Fatal(err)
		}
		var frag1 [][]byte
		frag1 = append(frag1, styp, ftyp, completeDualMoov)
		for _, b := range top1 {
			if b.typ == "moof" || b.typ == "mdat" {
				frag1 = append(frag1, raw1[b.start:b.end])
			}
		}
		c1 := bytes.Join(frag1, nil)
		c2 := testChunk(2, 8*90000, 90000, 4, []byte("gop2"))

		for name, data := range map[string][]byte{
			filepath.Join(chunks, "00000.m4s"): c0,
			filepath.Join(chunks, "00001.m4s"): c1,
			filepath.Join(chunks, "00002.m4s"): c2,
			filepath.Join(dir, "run-1.m3u8"):   []byte("#EXTM3U\n#EXTINF:2.0,\n00000.m4s\n#EXTINF:2.0,\n00001.m4s\n#EXTINF:4.0,\n00002.m4s\n"),
		} {
			if err := os.WriteFile(name, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}

		s.collectChunks(r, false)
		if r.chunks.failed {
			t.Fatalf("run failed unexpectedly: %v", r.proc.Err())
		}
		if !s.done[0][1] {
			t.Fatalf("segment 1 not published: done=%v next=%d", s.done[0], r.chunks.next)
		}
		if _, err := os.Stat(filepath.Join(chunks, "00000.m4s")); !os.IsNotExist(err) {
			t.Errorf("skipped re-init chunk 00000.m4s still on disk: %v", err)
		}
		initBytes, err := os.ReadFile(filepath.Join(dir, "init-1.mp4"))
		if err != nil || !fmp4InitFileComplete(filepath.Join(dir, "init-1.mp4")) {
			t.Fatalf("rebuilt init-1.mp4 not complete: err=%v len=%d", err, len(initBytes))
		}
		tracks, err := parseInitTracks(initBytes)
		if err != nil || len(tracks) != 2 || tracks[1].Codecs != "ec-3" {
			t.Fatalf("rebuilt init tracks = %+v, err=%v", tracks, err)
		}
		segBytes, err := os.ReadFile(s.trackSegPath(0, 1))
		if err != nil {
			t.Fatal(err)
		}
		boxes, err := readBoxes(segBytes, 0, len(segBytes))
		if err != nil {
			t.Fatalf("read published segment boxes: %v", err)
		}
		for _, b := range boxes {
			if b.typ == "ftyp" || b.typ == "moov" {
				t.Errorf("published segment 1 retained in-band %s box", b.typ)
			}
		}
	})

	t.Run("takes_first_complete_moov_and_skips_reinit_chunks", func(t *testing.T) {
		s, r, dir, chunks := newChunkSession(t, 0, 0, 2)
		noMvexInit := append(append([]byte(nil), ftyp...), mkBox("moov", mvhd, vTrak720)...)
		if err := os.WriteFile(filepath.Join(dir, "init-1.mp4"), noMvexInit, 0o644); err != nil {
			t.Fatal(err)
		}
		firstComplete := mkBox("moov", mvhd, vTrak1080, mvexSingle)
		secondComplete := mkBox("moov", mvhd, vTrak720, mvexSingle)
		for name, data := range map[string][]byte{
			filepath.Join(chunks, "00000.m4s"): noMvexInit,
			filepath.Join(chunks, "00001.m4s"): append(append([]byte(nil), ftyp...), firstComplete...),
			filepath.Join(chunks, "00002.m4s"): append(append([]byte(nil), ftyp...), secondComplete...),
			filepath.Join(chunks, "00003.m4s"): testChunk(1, 0, 90000, 4, []byte("seg0")),
			filepath.Join(chunks, "00004.m4s"): testChunk(2, 4*90000, 90000, 4, []byte("seg1")),
			filepath.Join(dir, "run-1.m3u8"):   []byte("#EXTM3U\n#EXTINF:0,\n00000.m4s\n#EXTINF:0,\n00001.m4s\n#EXTINF:0,\n00002.m4s\n#EXTINF:4.0,\n00003.m4s\n#EXTINF:4.0,\n00004.m4s\n"),
		} {
			if err := os.WriteFile(name, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}

		s.collectChunks(r, false)
		if r.chunks.failed || !s.done[0][0] {
			t.Fatalf("failed=%v done=%v", r.chunks.failed, s.done[0])
		}
		initBytes, err := os.ReadFile(filepath.Join(dir, "init-1.mp4"))
		if err != nil {
			t.Fatal(err)
		}
		ti, err := parseInit(initBytes)
		if err != nil || ti.Width != 1920 || ti.Height != 1080 {
			t.Fatalf("init track = %+v (err=%v), want 1920x1080 from first complete moov", ti, err)
		}
	})
}

func TestCollectChunksBoundsLiveParseRetries(t *testing.T) {
	newLiveSession := func(t *testing.T) (*Session, *run, *blockedProc, string, string) {
		t.Helper()
		dir := t.TempDir()
		chunks := filepath.Join(dir, "c-0")
		if err := os.MkdirAll(chunks, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "init-0.mp4"), testVideoInit(1280, 720), 0o644); err != nil {
			t.Fatal(err)
		}
		p := &blockedProc{done: make(chan struct{})}
		t.Cleanup(p.Stop)
		r := &run{
			proc:    p,
			maxDone: -1,
			stderr:  newTailBuffer(10),
			spec:    runSpec{Dir: dir, Playlist: "run-0.m3u8", InitName: "init-0.mp4"},
			chunks: &chunkRun{
				dir:       chunks,
				container: ContainerFMP4,
				initPath:  filepath.Join(dir, "init-0.mp4"),
				sl:        newSlotter(0, 4*time.Second, 2, 0),
			},
		}
		s := &Session{
			m:         &Manager{},
			dir:       dir,
			container: ContainerFMP4,
			segDur:    4 * time.Second,
			total:     2,
			log:       testLogger(t),
			sessionEncoding: sessionEncoding{
				cur:    r,
				endSeg: -1,
			},
			sessionDelivery: sessionDelivery{
				done:    []map[int]bool{{}},
				changed: make(chan struct{}),
			},
		}
		return s, r, p, dir, chunks
	}

	t.Run("transient_error_recovers", func(t *testing.T) {
		s, r, _, dir, chunks := newLiveSession(t)
		c0 := filepath.Join(chunks, "00000.m4s")
		if err := os.WriteFile(c0, []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "run-0.m3u8"), []byte("#EXTM3U\n#EXTINF:4.0,\n00000.m4s\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s.collectChunks(r, false)
		if r.chunks.failed || r.chunks.next != 0 {
			t.Fatalf("after 1 transient poll: failed=%v next=%d", r.chunks.failed, r.chunks.next)
		}
		if err := os.WriteFile(c0, testChunk(1, 0, 90000, 4, []byte("ok0")), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(chunks, "00001.m4s"), testChunk(2, 4*90000, 90000, 4, []byte("ok1")), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "run-0.m3u8"), []byte("#EXTM3U\n#EXTINF:4.0,\n00000.m4s\n#EXTINF:4.0,\n00001.m4s\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s.collectChunks(r, false)
		if r.chunks.failed || !s.done[0][0] {
			t.Fatalf("after recovery: failed=%v done=%v", r.chunks.failed, s.done[0])
		}
	})

	t.Run("persistent_error_fails_live_run", func(t *testing.T) {
		s, r, p, dir, chunks := newLiveSession(t)
		if err := os.WriteFile(filepath.Join(chunks, "00000.m4s"), []byte("styp-only-broken"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "run-0.m3u8"), []byte("#EXTM3U\n#EXTINF:4.0,\n00000.m4s\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for i := 1; i < maxChunkParseRetries; i++ {
			s.collectChunks(r, false)
			if r.chunks.failed {
				t.Fatalf("run failed early at poll %d/%d", i, maxChunkParseRetries)
			}
		}
		s.collectChunks(r, false)
		if !r.chunks.failed {
			t.Fatal("run did not fail after reaching maxChunkParseRetries")
		}
		select {
		case <-p.Done():
		default:
			t.Fatal("failChunkRun did not stop live producer")
		}
		if err := r.proc.Err(); err == nil {
			t.Fatal("failed run proc returned nil error")
		}
	})
}
