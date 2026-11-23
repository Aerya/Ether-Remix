package subtitles

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func scaleAndShiftCues(ref []Cue, offset time.Duration, invRate float64) []Cue {
	out := make([]Cue, len(ref))
	for i, c := range ref {
		start := time.Duration(math.Round(float64(c.Start-offset) * invRate))
		dur := time.Duration(math.Round(float64(c.End-c.Start) * invRate))
		out[i] = Cue{Start: start, End: start + dur, Text: c.Text}
	}
	return out
}

func buildMultiTrackMKV(forced, dialogue []Cue) []byte {
	t1 := mkvEl(mkvTrackEntryID, append(append(
		mkvU(mkvTrackNumberID, 1),
		mkvU(mkvTrackTypeID, 0x11)...),
		mkvS(mkvCodecIDID, "S_TEXT/UTF8")...))
	t2 := mkvEl(mkvTrackEntryID, append(append(
		mkvU(mkvTrackNumberID, 2),
		mkvU(mkvTrackTypeID, 0x11)...),
		mkvS(mkvCodecIDID, "S_ASS")...))
	tracks := mkvEl(mkvTracksID, append(t1, t2...))
	info := mkvEl(mkvInfoID, mkvU(mkvTimecodeScaleID, 1000000))

	type cueMeta struct {
		track             uint64
		pos, rel, ms, dur uint64
	}
	var clusters []byte
	var metas []cueMeta
	addCue := func(track uint64, c Cue) {
		ms := uint64(c.Start / time.Millisecond)
		dur := uint64((c.End - c.Start) / time.Millisecond)
		if dur == 0 {
			dur = 800
		}
		block := append(vintBytes(track, false), 0, 0, 0x80)
		block = append(block, []byte(c.Text)...)
		simple := mkvEl(mkvSimpleBlockID, block)
		ts := mkvU(mkvTimestampID, ms)
		cluster := mkvEl(mkvClusterID, append(ts, simple...))
		metas = append(metas, cueMeta{
			track: track,
			pos:   uint64(len(clusters)),
			rel:   uint64(len(ts)),
			ms:    ms,
			dur:   dur,
		})
		clusters = append(clusters, cluster...)
	}
	for _, c := range forced {
		addCue(1, c)
	}
	for _, c := range dialogue {
		addCue(2, c)
	}
	var cuePoints []byte
	for _, m := range metas {
		cueTrack := append(append(append(
			mkvU(mkvCueTrackID, m.track),
			mkvU(mkvCueClusterPosID, uint64(len(info)+len(tracks))+m.pos)...),
			mkvU(mkvCueRelPosID, m.rel)...),
			mkvU(mkvCueDurationID, m.dur)...)
		cuePoints = append(cuePoints, mkvEl(mkvCuePointID, append(
			mkvU(mkvCueTimeID, m.ms),
			mkvEl(mkvCueTrackPosID, cueTrack)...))...)
	}
	cuesEl := mkvEl(mkvCuesID, cuePoints)
	body := append(append(append(info, tracks...), clusters...), cuesEl...)
	ebml := mkvEl(mkvEBMLID, mkvS(0x4282, "matroska"))
	return append(ebml, mkvEl(mkvSegmentID, body)...)
}

func buildUnindexedMKV(cues []Cue) []byte {
	track := mkvEl(mkvTrackEntryID, append(append(
		mkvU(mkvTrackNumberID, 1),
		mkvU(mkvTrackTypeID, 0x11)...),
		mkvS(mkvCodecIDID, "S_UTF8")...))
	tracks := mkvEl(mkvTracksID, track)
	info := mkvEl(mkvInfoID, mkvU(mkvTimecodeScaleID, 1000000))

	var clusters []byte
	for i, c := range cues {
		ms := uint64(c.Start / time.Millisecond)
		dur := uint64((c.End - c.Start) / time.Millisecond)
		if dur == 0 {
			dur = 800
		}
		block := append(vintBytes(1, false), 0, 0, 0x80)
		block = append(block, []byte(c.Text)...)
		ts := mkvU(mkvTimestampID, ms)
		var elem []byte
		if i%2 == 0 {
			elem = mkvEl(mkvSimpleBlockID, block)
		} else {
			group := append(mkvEl(mkvBlockID, block), mkvU(mkvBlockDurationID, dur)...)
			elem = mkvEl(mkvBlockGroupID, group)
		}
		clusters = append(clusters, mkvEl(mkvClusterID, append(ts, elem...))...)
	}
	body := append(append(info, tracks...), clusters...)
	ebml := mkvEl(mkvEBMLID, mkvS(0x4282, "matroska"))
	return append(ebml, mkvEl(mkvSegmentID, body)...)
}

func buildMP4TextTrak(cues []Cue, fourcc string) []byte {
	const scale = uint32(1000)
	var sizes []uint32
	var deltas []uint32
	if len(cues) > 0 && cues[0].Start > 0 {
		// 2-byte empty clear sample from t=0 to cues[0].Start.
		sizes = append(sizes, 2)
		deltas = append(deltas, uint32(cues[0].Start/time.Millisecond))
	}
	for i, c := range cues {
		text := []byte(c.Text)
		if len(text) == 0 {
			text = []byte("x")
		}
		sizes = append(sizes, uint32(2+len(text)))
		if i+1 < len(cues) {
			d := uint32((cues[i+1].Start - c.Start) / time.Millisecond)
			if d == 0 {
				d = 1
			}
			deltas = append(deltas, d)
		} else {
			d := uint32((c.End - c.Start) / time.Millisecond)
			if d == 0 {
				d = 800
			}
			deltas = append(deltas, d)
		}
	}
	stsdPay := make([]byte, 8)
	binary.BigEndian.PutUint32(stsdPay[4:], 1)
	entry := make([]byte, 40)
	binary.BigEndian.PutUint32(entry, uint32(len(entry)))
	copy(entry[4:8], fourcc)
	stsd := mp4Full("stsd", 0, 0, append(stsdPay, entry...))

	sttsPay := make([]byte, 4+8*len(deltas))
	binary.BigEndian.PutUint32(sttsPay[0:], uint32(len(deltas)))
	for i, d := range deltas {
		binary.BigEndian.PutUint32(sttsPay[4+i*8:], 1)
		binary.BigEndian.PutUint32(sttsPay[4+i*8+4:], d)
	}
	stts := mp4Full("stts", 0, 0, sttsPay)

	stszPay := make([]byte, 8+4*len(sizes))
	binary.BigEndian.PutUint32(stszPay[4:], uint32(len(sizes)))
	for i, sz := range sizes {
		binary.BigEndian.PutUint32(stszPay[8+i*4:], sz)
	}
	stsz := mp4Full("stsz", 0, 0, stszPay)
	stbl := mp4Make("stbl", append(append(stsd, stts...), stsz...))
	minf := mp4Make("minf", append(mp4Make("nmhd", nil), stbl...))
	hdlrPay := make([]byte, 20)
	copy(hdlrPay[4:8], "sbtl")
	hdlr := mp4Full("hdlr", 0, 0, hdlrPay)
	mdhdPay := make([]byte, 20)
	binary.BigEndian.PutUint32(mdhdPay[8:], scale)
	mdhd := mp4Full("mdhd", 0, 0, mdhdPay)
	mdia := mp4Make("mdia", append(append(mdhd, hdlr...), minf...))
	tkhd := mp4Full("tkhd", 0, 0x1, make([]byte, 80))
	return mp4Make("trak", append(tkhd, mdia...))
}

func buildMultiTrackMP4(forced, dialogue []Cue) []byte {
	t1 := buildMP4TextTrak(forced, "tx3g")
	t2 := buildMP4TextTrak(dialogue, "wvtt")
	mvhdPay := make([]byte, 96)
	binary.BigEndian.PutUint32(mvhdPay[12:], 1000)
	mvhd := mp4Full("mvhd", 0, 0, mvhdPay)
	moov := mp4Make("moov", append(append(mvhd, t1...), t2...))
	ftyp := mp4Make("ftyp", []byte("isom\x00\x00\x00\x00isommp41"))
	return append(ftyp, moov...)
}

func TestFitTimingFrameRatePairsAndNegativeSubBinOffsets(t *testing.T) {
	base := irregularCues(36, 4*time.Second, 2*time.Second)
	cases := []struct {
		name   string
		rate   float64
		offset time.Duration
	}{
		{"25_to_23976_neg_offset", fps23976 / fps25, -1635 * time.Millisecond},
		{"23976_to_25_pos_offset", fps25 / fps23976, 1425 * time.Millisecond},
		{"25_to_24_neg_subbin", fps24 / fps25, -17 * time.Millisecond},
		{"24_to_25_pos_offset", fps25 / fps24, 830 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cand := scaleAndShiftCues(base, tc.offset, 1.0/tc.rate)
			fit, ok := FitTiming(cand, base)
			if !ok {
				t.Fatal("FitTiming failed")
			}
			if math.Abs(fit.Rate-tc.rate) > 0.001 {
				t.Fatalf("rate = %.6f, want %.6f", fit.Rate, tc.rate)
			}
			if absDuration(fit.Offset-tc.offset) > timingFrameSlack {
				t.Fatalf("offset = %s, want %s", fit.Offset, tc.offset)
			}
			shifted, err := Shift(cand, fit.Offset, fit.Rate)
			if err != nil {
				t.Fatal(err)
			}
			for _, idx := range []int{0, len(base) / 2, len(base) - 1} {
				if absDuration(shifted[idx].Start-base[idx].Start) > timingFrameSlack {
					t.Fatalf("cue %d start = %s, want %s", idx, shifted[idx].Start, base[idx].Start)
				}
			}
		})
	}
}

func TestStartCorrectionEmbeddedDriftCachesOnceAndServesEveryClient(t *testing.T) {
	var logs lockedBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	dir := t.TempDir()
	svc := New(Options{CacheDir: dir, Logger: logger})

	ref := irregularCues(28, 3500*time.Millisecond, 3*time.Second)
	for i := range ref {
		ref[i].Text = "Reference dialogue"
	}
	wantRate := fps23976 / fps25
	wantOffset := -1750 * time.Millisecond
	cand := scaleAndShiftCues(ref, wantOffset, 1.0/wantRate)

	id := "dl-drift-1"
	srt, vtt, err := Normalize([]byte(RenderSRT(cand)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".srt"), []byte(srt), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".vtt"), []byte(vtt), 0o600); err != nil {
		t.Fatal(err)
	}

	mkv := buildTestMKV(ref)
	title := &core.Title{Key: core.MovieKey(501), Type: core.TypeMovie, Title: "Drift Movie"}
	svc.StartCorrection(context.Background(), title, URLPrefix+id, ReleaseAccess{
		Reader:   bytes.NewReader(mkv),
		Size:     int64(len(mkv)),
		Duration: 3 * time.Minute,
		Source:   "rel-23976",
	})

	// OpenFormat (empty source -> default fit) and OpenFormatFrom (explicit release)
	// must both serve the aligned SRT and WebVTT files from cache.
	checkServed := func(rc io.ReadCloser, label string) {
		t.Helper()
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("%s read: %v", label, err)
		}
		got, err := ParseCues(string(raw))
		if err != nil || len(got) != len(ref) {
			t.Fatalf("%s parse (%d cues): %v", label, len(got), err)
		}
		for _, idx := range []int{0, 14, len(ref) - 1} {
			if absDuration(got[idx].Start-ref[idx].Start) > timingFrameSlack {
				t.Fatalf("%s cue %d = %s, want %s", label, idx, got[idx].Start, ref[idx].Start)
			}
		}
	}

	rc, _, err := svc.OpenFormatFrom(URLPrefix+id, "srt", "rel-23976")
	if err != nil {
		t.Fatal(err)
	}
	checkServed(rc, "source-srt")

	rc, _, err = svc.OpenFormatFrom(URLPrefix+id, "vtt", "rel-23976")
	if err != nil {
		t.Fatal(err)
	}
	checkServed(rc, "source-vtt")

	rc, _, err = svc.OpenFormat(URLPrefix+id, "srt")
	if err != nil {
		t.Fatal(err)
	}
	checkServed(rc, "default-srt")

	rc, _, err = svc.OpenFormat(URLPrefix+id, "vtt")
	if err != nil {
		t.Fatal(err)
	}
	checkServed(rc, "default-vtt")

	// Pre-rendered cache files exist on disk for both the release key and default.
	for _, srcKey := range []string{SourceKey("rel-23976"), defaultSource} {
		for _, ext := range []string{"json", "srt", "vtt"} {
			if _, err := os.Stat(svc.correctionFilePath(id, srcKey, ext)); err != nil {
				t.Fatalf("missing cached %s/%s: %v", srcKey, ext, err)
			}
		}
	}

	// Logged applied offset and scale.
	logText := logs.String()
	for _, token := range []string{"subtitle alignment applied", "method=embedded_cues", "offset=", "scale="} {
		if !strings.Contains(logText, token) {
			t.Fatalf("log missing %q: %s", token, logText)
		}
	}

	// Runs once per download/source: subsequent StartCorrection must not hold or re-read source.
	if !svc.HasCorrection(URLPrefix+id, "rel-23976") {
		t.Fatal("expected HasCorrection=true after alignment")
	}
	var held atomic.Bool
	svc.StartCorrection(context.Background(), title, URLPrefix+id, ReleaseAccess{
		Source: "rel-23976",
		Hold: func() (io.ReaderAt, int64, func(), bool) {
			held.Store(true)
			return bytes.NewReader(mkv), int64(len(mkv)), func() {}, true
		},
	})
	if held.Load() {
		t.Fatal("second StartCorrection re-held source instead of using cached correction")
	}
}

func TestReadEmbeddedTextCueTracksMultiTrackAndUnindexedMKV(t *testing.T) {
	forced := []Cue{
		{Start: 10 * time.Second, End: 11 * time.Second, Text: "Sign"},
		{Start: 40 * time.Second, End: 41 * time.Second, Text: "Title"},
	}
	dialogue := irregularCues(20, 3*time.Second, 2*time.Second)
	for i := range dialogue {
		dialogue[i].Text = "Full dialogue"
	}

	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"mkv_forced_then_full", buildMultiTrackMKV(forced, dialogue)},
		{"mp4_forced_then_wvtt", buildMultiTrackMP4(forced, dialogue)},
		{"mkv_unindexed_clusters", buildUnindexedMKV(dialogue)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadEmbeddedTextCues(context.Background(), bytes.NewReader(tc.raw), int64(len(tc.raw)))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(dialogue) {
				t.Fatalf("got %d cues, want %d", len(got), len(dialogue))
			}
			cand := shiftCues(dialogue, 1400*time.Millisecond)
			fit, ok := FitTiming(cand, got)
			if !ok || absDuration(fit.Offset+1400*time.Millisecond) > timingFrameSlack {
				t.Fatalf("fit = %+v ok=%v", fit, ok)
			}
		})
	}
}

func TestStartCorrectionAlignsAgainstKnownReferenceSubtitle(t *testing.T) {
	dir := t.TempDir()
	svc := New(Options{CacheDir: dir})
	title := &core.Title{Key: core.MovieKey(612), Type: core.TypeMovie, Title: "Reference Movie"}

	ref := irregularCues(22, 3*time.Second, 2*time.Second)
	for i := range ref {
		ref[i].Text = "English ref"
	}
	if _, err := svc.StoreUpload(title, "english.srt", "srt", "en", []byte(RenderSRT(ref))); err != nil {
		t.Fatal(err)
	}

	cand := scaleAndShiftCues(ref, -2100*time.Millisecond, fps25/fps23976)
	id := "dl-es"
	srt, vtt, err := Normalize([]byte(RenderSRT(cand)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".srt"), []byte(srt), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".vtt"), []byte(vtt), 0o600); err != nil {
		t.Fatal(err)
	}

	// Container bytes contain no embedded subtitles, so alignment must use the uploaded reference.
	svc.StartCorrection(context.Background(), title, id, ReleaseAccess{
		Reader:   bytes.NewReader(make([]byte, 64)),
		Size:     64,
		Duration: 2 * time.Minute,
		Source:   "rel-no-embedded",
	})

	rc, _, err := svc.OpenFormat(id, "srt")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()
	got, err := ParseCues(string(raw))
	if err != nil || len(got) != len(ref) {
		t.Fatalf("parse: %v (%d)", err, len(got))
	}
	if absDuration(got[len(got)-1].Start-ref[len(ref)-1].Start) > timingFrameSlack {
		t.Fatalf("last cue = %s, want %s", got[len(got)-1].Start, ref[len(ref)-1].Start)
	}
	corr, ok := svc.loadCorrection(id, "rel-no-embedded")
	if !ok || corr.Method != "reference_subtitle" || !corr.Applied {
		t.Fatalf("unexpected correction: %+v ok=%v", corr, ok)
	}
}

func TestCheapSpeechGateAndSpeechEnergyDefaults(t *testing.T) {
	// 1. Remote HTTP sources are not cheap speech sources and must never hit URL for audio decode.
	var httpHits atomic.Int32
	mediaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer mediaSrv.Close()

	if isCheapSpeechSource(ReleaseAccess{URL: mediaSrv.URL + "/stream.mkv", Source: "usenet:rel-1"}) {
		t.Fatal("remote HTTP source misclassified as cheap speech")
	}
	if !isCheapSpeechSource(ReleaseAccess{URL: "/volumes/movies/film.mkv", Source: "rel-1"}) {
		t.Fatal("local path should be cheap speech")
	}
	if !isCheapSpeechSource(ReleaseAccess{Source: "local:film-1"}) {
		t.Fatal("local: source should be cheap speech")
	}

	dir := t.TempDir()
	svc := New(Options{CacheDir: dir, FFmpeg: "ffmpeg"})
	id := "remote-no-speech"
	cues := synthCues(10, 2*time.Second, 0, 1)
	srt, vtt, _ := Normalize([]byte(RenderSRT(cues)))
	_ = os.WriteFile(filepath.Join(dir, id+".srt"), []byte(srt), 0o600)
	_ = os.WriteFile(filepath.Join(dir, id+".vtt"), []byte(vtt), 0o600)

	svc.StartCorrection(context.Background(), &core.Title{Key: core.MovieKey(7)}, id, ReleaseAccess{
		URL:      mediaSrv.URL + "/stream.mkv",
		Duration: 10 * time.Minute,
		Source:   "usenet:rel-1",
	})
	_, _ = svc.correctFlights.wait(id+"\x00"+SourceKey("usenet:rel-1"), 2*time.Second), true
	if httpHits.Load() != 0 {
		t.Fatalf("remote stream fetched %d times during correction", httpHits.Load())
	}

	// 2. FitSpeechEnergy works with default thresholds and rejects continuous loud noise.
	bursts := [][2]time.Duration{
		{1 * time.Second, 2 * time.Second},
		{4 * time.Second, 5 * time.Second},
		{8 * time.Second, 9 * time.Second},
		{11 * time.Second, 12 * time.Second},
	}
	speechCues := []Cue{
		{Start: 1600 * time.Millisecond, End: 2600 * time.Millisecond, Text: "a"},
		{Start: 4600 * time.Millisecond, End: 5600 * time.Millisecond, Text: "b"},
		{Start: 8600 * time.Millisecond, End: 9600 * time.Millisecond, Text: "c"},
		{Start: 11600 * time.Millisecond, End: 12600 * time.Millisecond, Text: "d"},
	}
	w1 := toneWindow(0, 6*time.Second, bursts)
	w2 := toneWindow(7*time.Second, 6*time.Second, bursts)
	fit, ok := FitSpeechEnergy(speechCues, []SpeechWindow{w1, w2})
	if !ok || absDuration(fit.Offset+600*time.Millisecond) > 80*time.Millisecond {
		t.Fatalf("default FitSpeechEnergy = %+v ok=%v", fit, ok)
	}

	noise := make([]int16, 16000*6)
	for i := range noise {
		noise[i] = int16(((i*37 + 13) % 19) * 1500)
	}
	noisyW1 := SpeechWindow{Start: 0, Rate: 16000, PCM: noise}
	noisyW2 := SpeechWindow{Start: 7 * time.Second, Rate: 16000, PCM: noise}
	if badFit, ok := FitSpeechEnergy(speechCues, []SpeechWindow{noisyW1, noisyW2}); ok {
		t.Fatalf("continuous noise accepted: %+v", badFit)
	}
}

func TestFrameRateHeuristicsAndProviderFPSMetadata(t *testing.T) {
	// 1. ImpliedCueFPS detects 25 fps and 23.976 fps frame grids, and ignores 100ms-quantized cues.
	palCues := make([]Cue, 30)
	ntscCues := make([]Cue, 30)
	tenthCues := make([]Cue, 30)
	for i := 0; i < 30; i++ {
		fStart := 220*i + (i*7)%23 + 1
		fEnd := fStart + 29
		palCues[i] = Cue{
			Start: time.Duration(fStart*40) * time.Millisecond,
			End:   time.Duration(fEnd*40) * time.Millisecond,
			Text:  "pal",
		}
		sMs := int64(math.Round(float64(fStart) * 1000.0 / fps23976))
		eMs := int64(math.Round(float64(fEnd) * 1000.0 / fps23976))
		ntscCues[i] = Cue{
			Start: time.Duration(sMs) * time.Millisecond,
			End:   time.Duration(eMs) * time.Millisecond,
			Text:  "film",
		}
		tenthCues[i] = Cue{
			Start: time.Duration(i*8000+100) * time.Millisecond,
			End:   time.Duration(i*8000+1900) * time.Millisecond,
			Text:  "tenths",
		}
	}
	if fps, ok := ImpliedCueFPS(palCues); !ok || math.Abs(fps-fps25) > 0.01 {
		t.Fatalf("pal ImpliedCueFPS = %v, %v", fps, ok)
	}
	if fps, ok := ImpliedCueFPS(ntscCues); !ok || math.Abs(fps-fps23976) > 0.01 {
		t.Fatalf("23.976 ImpliedCueFPS = %v, %v", fps, ok)
	}
	if fps, ok := ImpliedCueFPS(tenthCues); ok {
		t.Fatalf("100ms-rounded cues falsely implied %v fps", fps)
	}

	// 2. End-to-end provider download with attributes.fps=25 on a 23.976 fps release
	// (no embedded subtitles) scales cues by 25 / 23.976.
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/subtitles", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{
			map[string]any{"attributes": map[string]any{
				"language": "en", "download_count": 500, "fps": 25.0,
				"release":  "Movie.2024.PAL.DVDRip",
				"uploader": map[string]any{"name": "u", "rank": "Trusted"},
				"files":    []any{map[string]any{"file_id": 77, "file_name": "movie.pal.srt"}},
			}},
		}})
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"link": srv.URL + "/file.srt", "remaining": 50})
	})
	mux.HandleFunc("/file.srt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, RenderSRT([]Cue{
			{Start: 10 * time.Second, End: 12 * time.Second, Text: "One"},
			{Start: 500 * time.Second, End: 502 * time.Second, Text: "Two"},
			{Start: 1000 * time.Second, End: 1002 * time.Second, Text: "Three"},
		}))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	svc := New(Options{
		Config:   config.OpenSubtitles{Enabled: true, APIKey: "k"},
		CacheDir: t.TempDir(),
		BaseURL:  srv.URL,
	})
	title := &core.Title{Key: core.MovieKey(888), Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt0888888", TMDB: 888}, Title: "Heuristic"}
	if _, err := svc.SearchOnDemand(context.Background(), title, "en", "Movie.2024.1080p.BluRay"); err != nil {
		t.Fatal(err)
	}
	st, err := svc.DownloadOnDemand(context.Background(), title, 77)
	if err != nil {
		t.Fatal(err)
	}

	info23976 := &core.MediaInfo{
		Duration: 2 * time.Hour,
		Streams:  []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264", FrameRate: fps23976}},
	}
	svc.StartCorrection(context.Background(), title, st.ExternalURL, ReleaseAccess{
		Info:     info23976,
		Duration: 2 * time.Hour,
		Release:  "Movie.2024.1080p.BluRay",
		Source:   "rel-bluray-23976",
	})
	rc, _, err := svc.OpenFormat(st.ExternalURL, "srt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	aligned, err := ParseCues(string(body))
	if err != nil || len(aligned) != 3 {
		t.Fatalf("parse aligned: %v (%d)", err, len(aligned))
	}
	want1000 := time.Duration(math.Round(float64(1000*time.Second) * (fps25 / fps23976)))
	if absDuration(aligned[2].Start-want1000) > 10*time.Millisecond {
		t.Fatalf("1000s PAL cue on 23.976 release = %s, want %s", aligned[2].Start, want1000)
	}

	// 3. NTSC 29.97 telecine and HashMatch=true must never stretch a subtitle.
	info2997 := &core.MediaInfo{
		Duration: 2 * time.Hour,
		Streams:  []core.StreamInfo{{Index: 0, Type: "video", Codec: "mpeg2video", FrameRate: 30000.0 / 1001.0}},
	}
	if fit, ok := FitFrameRateHeuristic(sidecarMeta{FPS: fps23976}, ntscCues, ReleaseAccess{Info: info2997, Duration: 2 * time.Hour}); ok {
		t.Fatalf("29.97 NTSC triggered frame-rate stretch: %+v", fit)
	}
	if fit, ok := FitFrameRateHeuristic(sidecarMeta{FPS: fps25, HashMatch: true}, palCues, ReleaseAccess{Info: info23976, Duration: 2 * time.Hour}); ok {
		t.Fatalf("moviehash match triggered heuristic stretch: %+v", fit)
	}
}
