package subtitles

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Frame-rate ratios used when a sidecar was timed for a different video
// cadence than the release (23.976, 24, 25 and the inverses).
const (
	fps23976 = 24000.0 / 1001.0
	fps24    = 24.0
	fps25    = 25.0
)

var timingRates = []float64{
	1,
	fps23976 / fps24,
	fps24 / fps23976,
	fps23976 / fps25,
	fps25 / fps23976,
	fps24 / fps25,
	fps25 / fps24,
}

const (
	timingFrameSlack    = 42 * time.Millisecond // one 24 fps frame
	timingMinInliers    = 8
	timingMinConfidence = 0.7
	timingMaxOffset     = 15 * time.Minute
	timingBin           = 20 * time.Millisecond
)

// TimingFit is a constant offset plus a frame-rate scale that maps
// candidate cue times onto a reference clock:
//
//	ref ≈ candidate*Rate + Offset
type TimingFit struct {
	Offset     time.Duration
	Rate       float64
	Confidence float64
	Inliers    int
	Residual   time.Duration
}

// Identity reports a fit that leaves the sidecar unchanged.
func (f TimingFit) Identity() bool {
	if f.Rate == 0 {
		return true
	}
	return math.Abs(f.Rate-1) < 0.0005 && absDuration(f.Offset) < timingFrameSlack
}

// FitTiming aligns candidate cue start times to reference start times.
// Matching is language-independent: only the timing pattern is used.
func FitTiming(candidate, reference []Cue) (TimingFit, bool) {
	return FitTimingOptions(candidate, reference, FitOptions{})
}

// FitOptions tunes FitTiming. Zero values use the defaults above.
type FitOptions struct {
	MinInliers    int
	MinConfidence float64
	MaxOffset     time.Duration
	FrameSlack    time.Duration
}

func (o FitOptions) withDefaults() FitOptions {
	if o.MinInliers <= 0 {
		o.MinInliers = timingMinInliers
	}
	if o.MinConfidence <= 0 {
		o.MinConfidence = timingMinConfidence
	}
	if o.MaxOffset <= 0 {
		o.MaxOffset = timingMaxOffset
	}
	if o.FrameSlack <= 0 {
		o.FrameSlack = timingFrameSlack
	}
	return o
}

// FitTimingOptions is FitTiming with explicit thresholds (tests use a
// smaller cue count).
func FitTimingOptions(candidate, reference []Cue, opt FitOptions) (TimingFit, bool) {
	opt = opt.withDefaults()
	cStarts := cueStarts(candidate)
	rStarts := cueStarts(reference)
	if len(cStarts) < 3 || len(rStarts) < 3 {
		return TimingFit{}, false
	}
	var best TimingFit
	found := false
	for _, rate := range timingRates {
		fit, ok := fitRate(cStarts, rStarts, rate, opt)
		if !ok {
			continue
		}
		if !found || betterTimingFit(fit, best) {
			best, found = fit, true
		}
	}
	need := opt.MinInliers
	if n := len(cStarts); need > n {
		need = max(3, n*2/3)
	}
	if !found || best.Inliers < need || best.Confidence < opt.MinConfidence {
		return TimingFit{}, false
	}
	return best, true
}

func betterTimingFit(cand, cur TimingFit) bool {
	if cand.Confidence != cur.Confidence {
		return cand.Confidence > cur.Confidence
	}
	if math.Abs(cur.Rate-1) < 1e-6 && math.Abs(cand.Rate-1) >= 1e-6 {
		// Keep unity scale unless a frame-rate ratio meaningfully tightens
		// the median residual rather than matching rounding noise on a short clip.
		return cand.Residual+2*time.Millisecond < cur.Residual
	}
	return cand.Residual < cur.Residual
}

func cueStarts(cues []Cue) []time.Duration {
	out := make([]time.Duration, 0, len(cues))
	for _, c := range cues {
		if c.End > c.Start {
			out = append(out, c.Start)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func timingBinIndex(off time.Duration) int64 {
	q := int64(off / timingBin)
	if off < 0 && off%timingBin != 0 {
		q--
	}
	return q
}

func fitRate(candidate, reference []time.Duration, rate float64, opt FitOptions) (TimingFit, bool) {
	scaled := make([]time.Duration, len(candidate))
	for i, t := range candidate {
		scaled[i] = time.Duration(math.Round(float64(t) * rate))
	}
	// Histogram of pairwise offsets. Subsample only the candidate so a
	// long film stays cheap; search the full reference.
	stepC := 1
	if len(scaled) > 48 {
		stepC = (len(scaled) + 47) / 48
	}
	bins := map[int64]int{}
	for i := 0; i < len(scaled); i += stepC {
		for j := 0; j < len(reference); j++ {
			off := reference[j] - scaled[i]
			if absDuration(off) > opt.MaxOffset {
				continue
			}
			bins[timingBinIndex(off)]++
		}
	}
	if len(bins) == 0 {
		return TimingFit{}, false
	}
	type ranked struct {
		bin  int64
		n    int
		peak int
	}
	top := make([]ranked, 0, len(bins))
	for b, peak := range bins {
		votes := bins[b-1] + peak + bins[b+1]
		top = append(top, ranked{bin: b, n: votes, peak: peak})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].n != top[j].n {
			return top[i].n > top[j].n
		}
		if top[i].peak != top[j].peak {
			return top[i].peak > top[j].peak
		}
		return abs64(top[i].bin) < abs64(top[j].bin)
	})
	if len(top) > 8 {
		top = top[:8]
	}
	var (
		offset   time.Duration
		inliers  int
		residual time.Duration
	)
	for i, cand := range top {
		coarse := time.Duration(cand.bin)*timingBin + timingBin/2
		inl, res := scoreOffset(scaled, reference, coarse, opt.FrameSlack)
		off := coarse
		if refOff, ok := refineOffset(scaled, reference, coarse, opt.FrameSlack); ok {
			rInl, rRes := scoreOffset(scaled, reference, refOff, opt.FrameSlack)
			if rInl > inl || (rInl == inl && rRes <= res) {
				off, inl, res = refOff, rInl, rRes
			}
		}
		if i == 0 || inl > inliers || (inl == inliers && res < residual) {
			offset, inliers, residual = off, inl, res
		}
	}
	need := opt.MinInliers
	if need > len(scaled) {
		need = max(3, len(scaled)*2/3)
	}
	if inliers < need {
		return TimingFit{}, false
	}
	denom := len(scaled)
	if len(reference) < denom {
		denom = len(reference)
	}
	conf := float64(inliers) / float64(denom)
	if conf > 1 {
		conf = 1
	}
	if !segmentConsistent(scaled, reference, offset, opt.FrameSlack) {
		return TimingFit{}, false
	}
	return TimingFit{Offset: offset, Rate: rate, Confidence: conf, Inliers: inliers, Residual: residual}, true
}

func refineOffset(scaled, reference []time.Duration, coarse, slack time.Duration) (time.Duration, bool) {
	mid, ok := medianSignedDelta(scaled, reference, coarse, slack*2)
	if !ok {
		return 0, false
	}
	if fine, ok := medianSignedDelta(scaled, reference, mid, slack); ok {
		return fine, true
	}
	return mid, true
}

func medianSignedDelta(scaled, reference []time.Duration, center, window time.Duration) (time.Duration, bool) {
	var deltas []time.Duration
	j := 0
	for _, t := range scaled {
		want := t + center
		for j+1 < len(reference) && reference[j+1] <= want {
			j++
		}
		best := reference[j] - t
		bestAbs := absDuration(reference[j] - want)
		if j+1 < len(reference) {
			if a := absDuration(reference[j+1] - want); a < bestAbs {
				best, bestAbs = reference[j+1]-t, a
			}
		}
		if j > 0 {
			if a := absDuration(reference[j-1] - want); a < bestAbs {
				best, bestAbs = reference[j-1]-t, a
			}
		}
		if bestAbs <= window {
			deltas = append(deltas, best)
		}
	}
	if len(deltas) == 0 {
		return 0, false
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
	return deltas[len(deltas)/2], true
}

// segmentConsistent rejects a single-offset fit when a mid-file recut
// matches only part of the timeline. All three thirds must agree.
func segmentConsistent(scaled, reference []time.Duration, offset, slack time.Duration) bool {
	if len(scaled) < 12 {
		return true
	}
	for i := 0; i < 3; i++ {
		lo := i * len(scaled) / 3
		hi := (i + 1) * len(scaled) / 3
		if hi-lo < 3 {
			return false
		}
		inl, _ := scoreOffset(scaled[lo:hi], reference, offset, slack)
		if float64(inl) < float64(hi-lo)/3 {
			return false
		}
	}
	return true
}

func scoreOffset(scaled, reference []time.Duration, offset, slack time.Duration) (intliers int, residual time.Duration) {
	var abs []time.Duration
	j := 0
	for _, t := range scaled {
		want := t + offset
		for j+1 < len(reference) && reference[j+1] <= want {
			j++
		}
		best := absDuration(reference[j] - want)
		if j+1 < len(reference) {
			if n := absDuration(reference[j+1] - want); n < best {
				best = n
			}
		}
		if j > 0 {
			if n := absDuration(reference[j-1] - want); n < best {
				best = n
			}
		}
		if best <= slack {
			intliers++
			abs = append(abs, best)
		}
	}
	if len(abs) == 0 {
		return 0, slack
	}
	sort.Slice(abs, func(i, j int) bool { return abs[i] < abs[j] })
	return intliers, abs[len(abs)/2]
}

// sidecarMeta is the persisted provider metadata for a cached subtitle.
type sidecarMeta struct {
	TitleKey  string   `json:"titleKey"`
	FileID    int64    `json:"fileId"`
	Language  string   `json:"language"`
	Release   string   `json:"release"`
	FileName  string   `json:"fileName"`
	Releases  []string `json:"releases"`
	FPS       float64  `json:"fps"`
	HashMatch bool     `json:"hashMatch"`
	Uploaded  bool     `json:"uploaded"`
}

var (
	explicitFPSRe = regexp.MustCompile(`(?i)(?:^|[^0-9])(23[.,]976|23[.,]98|24(?:[.,]0+)?|25(?:[.,]0+)?|50(?:[.,]0+)?)\s*fps(?:$|[^a-z0-9])`)
	bare23976Re   = regexp.MustCompile(`(?i)(?:^|[\s.\-_\[(])(23[.,]976|23[.,]98)(?:$|[\s.\-_\]()])`)
	palTokenRe    = regexp.MustCompile(`(?i)(?:^|[\s.\-_\[(])pal(?:$|[\s.\-_\]()])`)
	palSourceRe   = regexp.MustCompile(`(?i)(?:^|[\s.\-_\[(])(dvd|dvdrip|dvdr|dvd5|dvd9|hdtv|pdtv|tvrip|dvb|vhs|576p|576i)(?:$|[\s.\-_\]()])`)
)

// canonicalFPS maps cinema and PAL cadences to {23.976, 24, 25}. NTSC 29.97/59.94
// telecine runs at 23.976 wall-clock speed and is intentionally excluded so a
// 23.976 subtitle is never stretched by 29.97/23.976.
func canonicalFPS(fps float64) float64 {
	switch {
	case math.Abs(fps-fps23976) <= 0.015 || math.Abs(fps-23.98) <= 0.005:
		return fps23976
	case math.Abs(fps-fps24) <= 0.015:
		return fps24
	case math.Abs(fps-fps25) <= 0.05 || math.Abs(fps-50.0) <= 0.05:
		return fps25
	default:
		return 0
	}
}

func parseNameFPS(name string) float64 {
	if name == "" {
		return 0
	}
	if m := explicitFPSRe.FindStringSubmatch(name); len(m) == 2 {
		if v, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64); err == nil {
			if c := canonicalFPS(v); c > 0 {
				return c
			}
		}
	}
	if m := bare23976Re.FindStringSubmatch(name); len(m) == 2 {
		return fps23976
	}
	if palTokenRe.MatchString(name) && palSourceRe.MatchString(name) {
		return fps25
	}
	return 0
}

// ImpliedCueFPS infers a frame-locked subtitle cadence (23.976, 24, or 25 fps)
// when cue timestamps quantize tightly onto one frame grid across >= 3 minutes.
// Multiples of 100 ms are ignored so tenths-of-a-second rounding cannot bias 25 fps.
func ImpliedCueFPS(cues []Cue) (float64, bool) {
	if len(cues) < 20 {
		return 0, false
	}
	span := cues[len(cues)-1].End - cues[0].Start
	if span < 3*time.Minute {
		return 0, false
	}
	var stamps []float64
	for _, c := range cues {
		if c.End <= c.Start {
			continue
		}
		for _, d := range [2]time.Duration{c.Start, c.End} {
			ms := d.Milliseconds()
			if ms <= 0 || ms%100 == 0 {
				continue
			}
			stamps = append(stamps, float64(ms)/1000.0)
		}
	}
	if len(stamps) < 24 {
		return 0, false
	}
	candidates := [3]float64{fps23976, fps24, fps25}
	var scores [3]float64
	const tol = 0.035 // ~1.4 ms at 24 fps
	for idx, fps := range candidates {
		hits := 0
		for _, sec := range stamps {
			frames := sec * fps
			if math.Abs(frames-math.Round(frames)) <= tol {
				hits++
			}
		}
		scores[idx] = float64(hits) / float64(len(stamps))
	}
	bestIdx, second := 0, 0.0
	for i := 1; i < len(candidates); i++ {
		if scores[i] > scores[bestIdx] {
			second = scores[bestIdx]
			bestIdx = i
		} else if scores[i] > second {
			second = scores[i]
		}
	}
	if scores[bestIdx] < 0.75 || scores[bestIdx]-second < 0.35 {
		return 0, false
	}
	return candidates[bestIdx], true
}

func releaseFPS(access ReleaseAccess) float64 {
	if access.Info != nil && !access.Info.Estimated {
		if v := access.Info.Video(); v != nil && !v.VariableFrameRate {
			var probed []float64
			if v.ReferenceFrameRate != nil && *v.ReferenceFrameRate > 0 {
				probed = append(probed, *v.ReferenceFrameRate)
			}
			if v.AverageFrameRate != nil && *v.AverageFrameRate > 0 {
				probed = append(probed, *v.AverageFrameRate)
			}
			if v.FrameRate > 0 {
				probed = append(probed, v.FrameRate)
			}
			canon := 0.0
			conflict := false
			for _, f := range probed {
				c := canonicalFPS(f)
				if c == 0 {
					continue
				}
				if canon == 0 {
					canon = c
				} else if math.Abs(canon-c) > 0.01 {
					conflict = true
				}
			}
			if canon > 0 && !conflict {
				return canon
			}
		}
	}
	return parseNameFPS(access.Release)
}

func subtitleImpliedFPS(meta sidecarMeta, cues []Cue) float64 {
	if c := canonicalFPS(meta.FPS); c > 0 {
		return c
	}
	for _, name := range append([]string{meta.Release, meta.FileName}, meta.Releases...) {
		if c := parseNameFPS(name); c > 0 {
			return c
		}
	}
	if c, ok := ImpliedCueFPS(cues); ok {
		return c
	}
	return 0
}

// FitFrameRateHeuristic computes linear drift correction (subtitleFPS / releaseFPS)
// when no embedded or speech reference is available and the release FPS and
// subtitle's declared/implied FPS disagree across {23.976, 24, 25}.
func FitFrameRateHeuristic(meta sidecarMeta, cues []Cue, access ReleaseAccess) (TimingFit, bool) {
	if len(cues) < 3 || meta.HashMatch {
		return TimingFit{}, false
	}
	if canonicalFPS(meta.FPS) == 0 && access.Release != "" && meta.Release != "" {
		if sig := releaseSig(access.Release); sig != "any" && sig == releaseSig(meta.Release) {
			return TimingFit{}, false
		}
	}
	relFPS := releaseFPS(access)
	subFPS := subtitleImpliedFPS(meta, cues)
	if relFPS == 0 || subFPS == 0 || math.Abs(relFPS-subFPS) <= 0.01 {
		return TimingFit{}, false
	}
	rate := subFPS / relFPS
	if access.Duration > 0 {
		scaledEnd := time.Duration(float64(cues[len(cues)-1].End) * rate)
		if scaledEnd > access.Duration+45*time.Second {
			return TimingFit{}, false
		}
	}
	return TimingFit{
		Offset:     0,
		Rate:       rate,
		Confidence: 0.75,
		Inliers:    len(cues),
	}, true
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
