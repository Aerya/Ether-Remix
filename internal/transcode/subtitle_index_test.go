package transcode

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ebmlEl encodes one element. width forces the size field's width (as muxers
// that reserve 8 bytes for a cluster size do); 0 picks the shortest.
func ebmlEl(id uint64, width int, body ...[]byte) []byte {
	payload := bytes.Join(body, nil)
	var idb []byte
	for v := id; v > 0; v >>= 8 {
		idb = append([]byte{byte(v)}, idb...)
	}
	if width == 0 {
		width = 1
		for uint64(len(payload)) >= (uint64(1)<<(7*width))-1 {
			width++
		}
	}
	size := make([]byte, width)
	v := uint64(len(payload))
	for i := width - 1; i >= 0; i-- {
		size[i] = byte(v)
		v >>= 8
	}
	size[0] |= 0x80 >> (width - 1)
	return append(append(idb, size...), payload...)
}

func ebmlU(id uint64, v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return ebmlEl(id, 0, b)
}

func ebmlS(id uint64, s string) []byte { return ebmlEl(id, 0, []byte(s)) }

type testLine struct {
	track    uint64
	at, dur  uint64 // milliseconds
	text     []byte
	indexed  bool
	clusterW int
}

// testMKV builds a Matroska file: a video track, a SubRip track and an ASS
// track (zlib frames), each subtitle line in its own cluster, Cues that index
// the subtitle blocks with their relative positions, and statistics tags.
func testMKV(t *testing.T, lines []testLine, frames map[uint64]int) []byte {
	t.Helper()
	tracks := ebmlEl(mkvTracks, 0,
		ebmlEl(mkvTrackEntry, 0, ebmlU(mkvTrackNumber, 1), ebmlU(mkvTrackUID, 101), ebmlU(mkvTrackType, 1), ebmlS(mkvCodecID, "V_MPEG4/ISO/AVC"),
			ebmlEl(mkvVideo, 0, ebmlU(mkvPixelWidth, 1920), ebmlU(mkvPixelHeight, 1080))),
		ebmlEl(mkvTrackEntry, 0, ebmlU(mkvTrackNumber, 2), ebmlU(mkvTrackUID, 102), ebmlU(mkvTrackType, 0x11), ebmlS(mkvCodecID, "S_TEXT/UTF8")),
		ebmlEl(mkvTrackEntry, 0, ebmlU(mkvTrackNumber, 3), ebmlU(mkvTrackUID, 103), ebmlU(mkvTrackType, 0x11), ebmlS(mkvCodecID, "S_TEXT/ASS"),
			ebmlEl(mkvEncodings, 0, ebmlEl(mkvEncoding, 0, ebmlU(mkvEncScope, 1), ebmlU(mkvEncType, 0), ebmlEl(mkvCompression, 0, ebmlU(mkvCompAlgo, 0))))),
	)
	info := ebmlEl(mkvInfo, 0, ebmlU(mkvTimecodeScl, 1000000), ebmlEl(mkvDuration, 0, []byte{0x40, 0xf8, 0x6a, 0x00, 0, 0, 0, 0}))
	var tags [][]byte
	for track, n := range frames {
		tags = append(tags, ebmlEl(mkvTag, 0, ebmlEl(mkvTargets, 0, ebmlU(mkvTagTrackUID, 100+track)),
			ebmlEl(mkvSimpleTag, 0, ebmlS(mkvTagName, "NUMBER_OF_FRAMES"), ebmlS(mkvTagString, strconv.Itoa(n)))))
	}
	tagsEl := ebmlEl(mkvTags, 0, tags...)
	// Fixed-width SeekHead so element offsets can be computed before it is
	// written: positions are filled in a second pass.
	seek := func(id uint64, pos uint64) []byte {
		idb := make([]byte, 4)
		binary.BigEndian.PutUint32(idb, uint32(id))
		p := make([]byte, 8)
		binary.BigEndian.PutUint64(p, pos)
		return ebmlEl(mkvSeek, 0, ebmlEl(mkvSeekID, 0, idb), ebmlEl(mkvSeekPosition, 0, p))
	}
	build := func(infoPos, tracksPos, cuesPos, tagsPos uint64, cues []byte) ([]byte, []uint64, []uint64) {
		seekHead := ebmlEl(mkvSeekHead, 0, seek(mkvInfo, infoPos), seek(mkvTracks, tracksPos), seek(mkvCues, cuesPos), seek(mkvTags, tagsPos))
		body := append(append(append([]byte(nil), seekHead...), info...), tracks...)
		var clusterPos, relPos []uint64
		for i, l := range lines {
			lead := ebmlEl(mkvSimpleBlock, 0, []byte{0x81, 0, 0, 0x80, 1, 2, 3}) // a video frame first
			frame := l.text
			block := ebmlEl(mkvBlockGroup, 0, ebmlEl(mkvBlock, 0, append([]byte{0x80 | byte(l.track), 0, 0, 0}, frame...)), ebmlU(mkvBlockDuration, l.dur))
			cluster := ebmlEl(mkvCluster, lines[i].clusterW, ebmlU(mkvTimestamp, l.at), lead, block)
			clusterPos = append(clusterPos, uint64(len(body)))
			relPos = append(relPos, uint64(len(ebmlU(mkvTimestamp, l.at))+len(lead)))
			body = append(body, cluster...)
		}
		_ = cues
		return body, clusterPos, relPos
	}
	// First pass for positions, second with the Cues and Tags appended.
	body, clusterPos, relPos := build(0, 0, 0, 0, nil)
	var points [][]byte
	for i, l := range lines {
		if !l.indexed {
			continue
		}
		points = append(points, ebmlEl(mkvCuePoint, 0, ebmlU(mkvCueTime, l.at),
			ebmlEl(mkvCueTrackPos, 0, ebmlU(mkvCueTrack, l.track), ebmlU(mkvCueClusterPos, clusterPos[i]), ebmlU(mkvCueRelPos, relPos[i]), ebmlU(mkvCueDuration, l.dur))))
	}
	cues := ebmlEl(mkvCues, 0, points...)
	seekLen := uint64(len(ebmlEl(mkvSeekHead, 0, seek(0, 0), seek(0, 0), seek(0, 0), seek(0, 0))))
	infoPos := seekLen
	tracksPos := infoPos + uint64(len(info))
	cuesPos := uint64(len(body))
	tagsPos := cuesPos + uint64(len(cues))
	body, _, _ = build(infoPos, tracksPos, cuesPos, tagsPos, cues)
	body = append(append(body, cues...), tagsEl...)
	header := ebmlEl(mkvEBML, 0, ebmlS(mkvDocType, "matroska"))
	return append(header, ebmlEl(mkvSegment, 8, body)...)
}

func zlibBytes(t *testing.T, s string) []byte {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Bytes()
}

// The track is read through the Matroska index, one block per line, and
// matches what FFmpeg would extract; an index that cannot be trusted is never
// used, so FFmpeg reads the track instead.
func TestIndexedSubtitleReadsTrackThroughCues(t *testing.T) {
	lines := []testLine{
		{track: 2, at: 1000, dur: 1500, text: []byte("Hello,\r\n<i>world</i>"), indexed: true, clusterW: 8},
		{track: 3, at: 1200, dur: 900, text: zlibBytes(t, `0,0,Default,,0,0,0,,{\i1}Sign{\i0}\Nhere`), indexed: true, clusterW: 8},
		{track: 2, at: 4000, dur: 2000, text: []byte("Second line"), indexed: true, clusterW: 2},
		{track: 3, at: 5000, dur: 500, text: zlibBytes(t, `0,0,Default,,0,0,0,,{\p1}m 0 0 l 10 10{\p0}`), indexed: true, clusterW: 8},
	}
	file := testMKV(t, lines, map[uint64]int{2: 2, 3: 2})
	ctx := context.Background()
	srt, err := indexedSubtitleVTT(ctx, bytes.NewReader(file), int64(len(file)), 1, "subrip")
	if err != nil {
		t.Fatal(err)
	}
	if want := "WEBVTT\n\n00:00:01.000 --> 00:00:02.500\nHello,\n<i>world</i>\n\n00:00:04.000 --> 00:00:06.000\nSecond line\n\n"; string(srt) != want {
		t.Fatalf("subrip track:\n%q\nwant\n%q", srt, want)
	}
	ass, err := indexedSubtitleVTT(ctx, bytes.NewReader(file), int64(len(file)), 2, "ass")
	if err != nil {
		t.Fatal(err)
	}
	if want := "WEBVTT\n\n00:00:01.200 --> 00:00:02.100\n<i>Sign</i>\nhere\n\n"; string(ass) != want {
		t.Fatalf("ass track:\n%q\nwant\n%q", ass, want)
	}
	for name, tc := range map[string]struct {
		file  []byte
		index int
		codec string
	}{
		"probe disagrees":       {file, 1, "ass"},
		"not a subtitle stream": {file, 0, "subrip"},
		"stats count a block the index lacks": {testMKV(t, []testLine{
			{track: 2, at: 1000, dur: 500, text: []byte("a"), indexed: true},
			{track: 2, at: 2000, dur: 500, text: []byte("b")},
		}, map[uint64]int{2: 2}), 1, "subrip"},
		"no subtitle cues": {testMKV(t, []testLine{{track: 2, at: 1000, dur: 500, text: []byte("a")}}, nil), 1, "subrip"},
	} {
		if _, err := indexedSubtitleVTT(ctx, bytes.NewReader(tc.file), int64(len(tc.file)), tc.index, tc.codec); !errors.Is(err, errNoSubtitleIndex) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if !strings.HasPrefix(string(srt), "WEBVTT") {
		t.Fatal("not WebVTT")
	}
}

func TestExtractIndexedSubtitleCachesAndSkipsFFmpegWhenUnindexed(t *testing.T) {
	indexed := testMKV(t, []testLine{
		{track: 2, at: 1000, dur: 1500, text: []byte("Hello"), indexed: true, clusterW: 8},
	}, map[uint64]int{2: 1})
	unindexed := testMKV(t, []testLine{
		{track: 2, at: 1000, dur: 1500, text: []byte("Hello")},
	}, nil)

	r := &countingSubtitleRunner{}
	m := &Manager{runner: r, ffmpeg: "ffmpeg"}

	ctx := WithSubtitleSource(context.Background(), "src-indexed")
	ctx = WithSubtitleReader(ctx, func() (io.ReaderAt, int64, func(), bool) {
		return bytes.NewReader(indexed), int64(len(indexed)), func() {}, true
	})
	got, ok := m.ExtractIndexedSubtitle(ctx, 1, "subrip")
	if !ok || !strings.Contains(string(got), "Hello") {
		t.Fatalf("indexed extract ok=%v body=%q", ok, got)
	}
	if _, cached := m.CachedSubtitle(ctx, 1, "subrip"); !cached {
		t.Fatal("indexed extract did not populate subtitle cache")
	}

	uctx := WithSubtitleSource(context.Background(), "src-unindexed")
	uctx = WithSubtitleReader(uctx, func() (io.ReaderAt, int64, func(), bool) {
		return bytes.NewReader(unindexed), int64(len(unindexed)), func() {}, true
	})
	if _, ok := m.ExtractIndexedSubtitle(uctx, 1, "subrip"); ok {
		t.Fatal("unindexed MKV unexpectedly succeeded")
	}
	if got := r.runs.Load(); got != 0 {
		t.Fatalf("ExtractIndexedSubtitle invoked ffmpeg %d times, want 0", got)
	}
}

func TestSubtitleStreamArgsFormats(t *testing.T) {
	for _, tc := range []struct {
		format string
		want   []string
	}{
		{"srt", []string{"-c:s", "srt", "-f", "srt", "-flush_packets", "1", "pipe:1"}},
		{"vtt", []string{"-c:s", "webvtt", "-f", "webvtt", "-flush_packets", "1", "pipe:1"}},
		{"ass", []string{"-c:s", "ass", "-f", "ass", "-ignore_readorder", "1", "-flush_packets", "1", "pipe:1"}},
	} {
		args := subtitleStreamArgs("http://127.0.0.1/file.mkv", 2, tc.format, 90*time.Second, true)
		joined := strings.Join(args, " ")
		wantTail := strings.Join(tc.want, " ")
		if !strings.HasSuffix(joined, wantTail) {
			t.Errorf("format=%s args=%q want suffix %q", tc.format, joined, wantTail)
		}
		if !strings.Contains(joined, "-copyts") || !strings.Contains(joined, "-ss 90.000") {
			t.Errorf("format=%s args missing seek/copyts: %q", tc.format, joined)
		}
	}
}
