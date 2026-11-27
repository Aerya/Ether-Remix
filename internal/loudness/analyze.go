// Package loudness measures how loud an audio item is (EBU R128) and where it
// starts, ends and fades, so every client can level playback (ReplayGain 2.0)
// and time crossfades. Measurement happens once per source, only after a
// viewer has played the item through, from the copy playback just fetched;
// there is no library walk.
package loudness

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/mediaproc"
)

// ReferenceLUFS is the ReplayGain 2.0 target loudness.
const ReferenceLUFS = -18.0

// silenceLUFS is the momentary loudness below which audio counts as silence.
const silenceLUFS = -60.0

// fadeLU is how far below the integrated loudness the music may sit and still
// count as playing rather than fading.
const fadeLU = 10.0

// Result is one source's measurement.
type Result struct {
	LUFS           float64 // integrated loudness
	PeakDBFS       float64 // true peak
	StartMs        int64   // first non-silent moment
	EndMs          int64   // last non-silent moment
	FadeInEndMs    int64   // first moment within fadeLU of the integrated loudness
	FadeOutStartMs int64   // last such moment before the end fades away for good
	DurationMs     int64
}

// GainDB is the ReplayGain 2.0 gain for this loudness.
func (r Result) GainDB() float64 { return round1(ReferenceLUFS - r.LUFS) }

// PeakLinear is the true peak as a linear amplitude (1.0 = 0 dBTP).
func (r Result) PeakLinear() float64 {
	return math.Round(math.Pow(10, r.PeakDBFS/20)*10000) / 10000
}

// ErrNoAudio means the source had no measurable audio.
var ErrNoAudio = errors.New("loudness: no audio measured")

// Analyze reads src to the end through ffmpeg's ebur128 filter, at most for
// timeout, and returns the measurement. It never plays anything.
func Analyze(ctx context.Context, ffmpeg string, src io.Reader, timeout time.Duration) (Result, error) {
	cmd := mediaproc.CommandContext(ctx, timeout, ffmpeg,
		"-hide_banner", "-nostdin", "-nostats", "-v", "info", "-threads", "1",
		"-i", "pipe:0", "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-af", "ebur128=peak=true:framelog=info", "-f", "null", "-")
	cmd.Stdin = src
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	res, parseErr := parse(stderr)
	_, _ = io.Copy(io.Discard, stderr)
	waitErr := cmd.Wait()
	if parseErr != nil {
		return Result{}, parseErr
	}
	if waitErr != nil && res.DurationMs == 0 {
		return Result{}, fmt.Errorf("loudness: ffmpeg: %w", waitErr)
	}
	return res, nil
}

var (
	frameLine   = regexp.MustCompile(`\bt:\s*([0-9.]+)\s.*?\bM:\s*(-?[0-9.]+|-inf)`)
	summaryI    = regexp.MustCompile(`^\s+I:\s*(-?[0-9.]+|-inf)\s+LUFS`)
	summaryPeak = regexp.MustCompile(`^\s+Peak:\s*(-?[0-9.]+|-inf)\s+dBFS`)
)

type frame struct {
	ms int64
	m  float64
}

// parse reads ebur128's 100 ms frame log and its summary.
func parse(r io.Reader) (Result, error) {
	var (
		frames  []frame
		lufs    = math.Inf(-1)
		peak    = math.Inf(-1)
		haveSum bool
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "Summary:") {
			haveSum = true
			continue
		}
		if haveSum {
			if m := summaryI.FindStringSubmatch(line); m != nil {
				lufs = number(m[1])
			} else if m := summaryPeak.FindStringSubmatch(line); m != nil {
				peak = number(m[1])
			}
			continue
		}
		if m := frameLine.FindStringSubmatch(line); m != nil {
			t, _ := strconv.ParseFloat(m[1], 64)
			frames = append(frames, frame{ms: int64(math.Round(t * 1000)), m: number(m[2])})
		}
	}
	if err := sc.Err(); err != nil {
		return Result{}, err
	}
	if len(frames) == 0 || math.IsInf(lufs, 0) || lufs <= -70 {
		return Result{}, ErrNoAudio
	}
	res := Result{LUFS: round1(lufs), PeakDBFS: peak, DurationMs: frames[len(frames)-1].ms}
	if math.IsInf(peak, 0) {
		res.PeakDBFS = lufs
	}
	res.StartMs, res.EndMs = 0, res.DurationMs
	if i := firstFrame(frames, func(f frame) bool { return f.m > silenceLUFS }); i >= 0 {
		// The momentary window is 400 ms wide; sound begins up to that much earlier.
		res.StartMs = max(0, frames[i].ms-400)
	}
	if i := lastFrame(frames, func(f frame) bool { return f.m > silenceLUFS }); i >= 0 {
		res.EndMs = frames[i].ms
	}
	playing := func(f frame) bool { return f.m >= lufs-fadeLU }
	res.FadeInEndMs, res.FadeOutStartMs = res.StartMs, res.EndMs
	if i := firstFrame(frames, playing); i >= 0 {
		res.FadeInEndMs = frames[i].ms
	}
	if i := lastFrame(frames, playing); i >= 0 {
		res.FadeOutStartMs = frames[i].ms
	}
	return res, nil
}

func firstFrame(fs []frame, ok func(frame) bool) int {
	for i, f := range fs {
		if ok(f) {
			return i
		}
	}
	return -1
}

func lastFrame(fs []frame, ok func(frame) bool) int {
	for i := len(fs) - 1; i >= 0; i-- {
		if ok(fs[i]) {
			return i
		}
	}
	return -1
}

func number(s string) float64 {
	if s == "-inf" {
		return math.Inf(-1)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.Inf(-1)
	}
	return v
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// AlbumLUFS combines tracks' integrated loudness into the album's, weighting
// each by its duration (an energy mean, as R128 gating would over the album).
func AlbumLUFS(lufs []float64, durationsMs []int64) float64 {
	var energy, total float64
	for i, l := range lufs {
		d := float64(max(durationsMs[i], 1))
		energy += d * math.Pow(10, l/10)
		total += d
	}
	if total == 0 || energy == 0 {
		return math.Inf(-1)
	}
	return round1(10 * math.Log10(energy/total))
}
