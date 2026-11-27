package loudness

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os/exec"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

// wav builds a mono 16-bit tone: lead silence, a fade in, steady, a fade out,
// trailing silence (seconds each).
func wav(lead, in, steady, out, tail float64) []byte {
	const rate = 44100
	var pcm []int16
	add := func(secs float64, amp func(f float64) float64) {
		n := int(secs * rate)
		for i := 0; i < n; i++ {
			a := amp(float64(i) / float64(max(n, 1)))
			pcm = append(pcm, int16(a*0.5*32767*math.Sin(2*math.Pi*440*float64(len(pcm))/rate)))
		}
	}
	add(lead, func(float64) float64 { return 0 })
	add(in, func(f float64) float64 { return f * f })
	add(steady, func(float64) float64 { return 1 })
	add(out, func(f float64) float64 { return (1 - f) * (1 - f) })
	add(tail, func(float64) float64 { return 0 })
	var b bytes.Buffer
	data := len(pcm) * 2
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+data))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(data))
	_ = binary.Write(&b, binary.LittleEndian, pcm)
	return b.Bytes()
}

type media struct{ *bytes.Reader }

func (media) Close() error { return nil }

func TestParseFrameLogAndSummary(t *testing.T) {
	log := `[Parsed_ebur128_0 @ 0x1] t: 0.1  TARGET:-23 LUFS    M:-120.7 S:-120.7     I: -70.0 LUFS
[Parsed_ebur128_0 @ 0x1] t: 0.5  TARGET:-23 LUFS    M: -40.0 S:-120.7     I: -70.0 LUFS
[Parsed_ebur128_0 @ 0x1] t: 1.0  TARGET:-23 LUFS    M: -12.0 S:-120.7     I: -12.0 LUFS
[Parsed_ebur128_0 @ 0x1] t: 9.0  TARGET:-23 LUFS    M: -11.0 S:-12.0     I: -11.5 LUFS
[Parsed_ebur128_0 @ 0x1] t: 9.5  TARGET:-23 LUFS    M: -30.0 S:-12.0     I: -11.5 LUFS
[Parsed_ebur128_0 @ 0x1] t: 10.0  TARGET:-23 LUFS    M:-90.0 S:-30.0     I: -11.5 LUFS
[Parsed_ebur128_0 @ 0x1] Summary:

  Integrated loudness:
    I:         -11.5 LUFS
    Threshold: -21.5 LUFS

  True peak:
    Peak:       -0.4 dBFS
`
	r, err := parse(bytes.NewBufferString(log))
	if err != nil {
		t.Fatal(err)
	}
	if r.LUFS != -11.5 || r.GainDB() != -6.5 || r.PeakDBFS != -0.4 || r.DurationMs != 10000 {
		t.Fatalf("summary: %+v gain %v", r, r.GainDB())
	}
	if r.StartMs != 100 || r.EndMs != 9500 || r.FadeInEndMs != 1000 || r.FadeOutStartMs != 9000 {
		t.Fatalf("trim: %+v", r)
	}
	if p := (Result{PeakDBFS: 0}).PeakLinear(); p != 1 {
		t.Fatalf("0 dBTP = %v", p)
	}
	if _, err := parse(bytes.NewBufferString("no audio here")); err != ErrNoAudio {
		t.Fatalf("empty log: %v", err)
	}
}

func TestAlbumLUFSIsDurationWeightedEnergy(t *testing.T) {
	if got := AlbumLUFS([]float64{-10, -10}, []int64{1000, 3000}); got != -10 {
		t.Fatalf("equal tracks = %v", got)
	}
	// A long quiet track pulls the album down less than an average of dB would.
	if got := AlbumLUFS([]float64{-8, -20}, []int64{1000, 1000}); got <= -14 || got > -10 {
		t.Fatalf("energy mean = %v", got)
	}
}

// End to end with the real ffmpeg: a played-through item is measured once,
// answered per page (with album gain once its whole album is measured) and
// for its exact source.
func TestServiceMeasuresPlayedItems(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	db := store.OpenTest(t)
	ctx := context.Background()
	clip := wav(1, 2, 6, 3, 1)
	album := core.PluginKey(900)
	tracks := []core.RatingKey{core.PluginKey(901), core.PluginKey(902)}
	opened := 0
	svc := New(Options{
		DB:     db,
		FFmpeg: func() string { return ff },
		Open: func(context.Context, *core.Viewer, core.RatingKey) (*frontend.KindMedia, error) {
			opened++
			return &frontend.KindMedia{ReaderAt: media{bytes.NewReader(clip)}, Closer: media{}, Size: int64(len(clip)), MIME: "audio/wav", Name: "01 - Tone.wav"}, nil
		},
		AlbumOf: func(context.Context, core.RatingKey) (core.RatingKey, []core.RatingKey, bool) {
			return album, tracks, true
		},
	})
	for _, k := range tracks {
		if err := svc.measure(ctx, job{viewer: 1, key: k}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.measure(ctx, job{viewer: 1, key: tracks[0]}); err != nil || opened != 2 {
		t.Fatalf("a measured item is not opened again: opened=%d err=%v", opened, err)
	}
	page := svc.PageLoudness(ctx, []frontend.LoudnessRef{{Key: tracks[0], Parent: album}, {Key: core.PluginKey(999)}})
	l, ok := page[tracks[0]]
	if !ok || len(page) != 1 {
		t.Fatalf("page = %+v", page)
	}
	if l.Source != core.LoudnessMeasured || l.LUFS > -5 || l.LUFS < -30 || l.TrackPeak <= 0 || l.TrackPeak > 1.01 {
		t.Fatalf("measurement: %+v", l)
	}
	if l.AlbumGainDB == nil || *l.AlbumGainDB != l.TrackGainDB {
		t.Fatalf("album gain of two equal tracks: %+v", l)
	}
	tr := l.Trim
	near := func(got, want int64) bool { return got >= want-700 && got <= want+700 }
	if tr == nil || !near(tr.StartMs, 1000) || !near(tr.EndMs, 12000) || tr.FadeInEndMs <= tr.StartMs || tr.FadeInEndMs > 3200 || tr.FadeOutStartMs < 8800 || tr.FadeOutStartMs >= tr.EndMs {
		t.Fatalf("trim: %+v", tr)
	}
	media := &frontend.KindMedia{Size: int64(len(clip)), Name: "01 - Tone.wav"}
	if _, ok := svc.ForSource(ctx, tracks[0], SourceID(media)); !ok {
		t.Fatal("exact source not answered")
	}
	if _, ok := svc.ForSource(ctx, tracks[0], "other-source"); ok {
		t.Fatal("another source answered")
	}
}
