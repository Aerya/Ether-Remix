package livetv

import (
	"encoding/binary"
	"math"
	"time"

	"github.com/braedonsaunders/ether/internal/transcode"
)

const (
	// steadyReserveTempo is the imperceptible playout rate (~3.5% slower than
	// real time) used on Balanced and Rock solid while the stability reserve
	// is below its target. Video frame cadence stretches to match WSOLA
	// pitch-preserved audio so A/V sync never drifts.
	steadyReserveTempo      = 0.965
	steadyReserveHysteresis = 500 * time.Millisecond
)

type reservePaceSource interface {
	reserveTarget() (depth, target time.Duration, ok bool)
	setInputPlayRate(rate float64)
}

type pacedVideo struct {
	video     []byte
	tags      []any
	pts       time.Duration
	valid     bool
	endSample int64
	emitted   bool
}

// reservePacer stretches decoded RawFrames inside one continuous broadcast
// output when a channel's stability reserve is below its preset target, and
// returns smoothly to 1.0x once the target is reached. It never runs faster
// than 1.0x.
type reservePacer struct {
	format     transcode.RawFormat
	input      *broadcastInput
	hop        int // synthesis hop in sample-frames (<= 1/4 video tick)
	win        int // 2 * hop
	search     int // WSOLA correlation search radius in sample-frames
	fade       []float32
	active     bool    // WSOLA pipeline primed and running
	stretching bool    // currently below target (steadyReserveTempo)
	reached    bool    // latched when reserve reaches target; cleared below target-hysteresis
	lastRate   float64 // last rate sent to upstream tap

	baseIdx    int64   // absolute input sample-frame index of pcmIn[0]
	pcmIn      []int16 // interleaved input PCM (len is multiple of Channels)
	overlap    []int16 // trailing hop of previous grain (hop * Channels)
	prevChosen int64
	nextIdeal  float64
	pcmOut     []byte

	vidQueue []pacedVideo
}

func newReservePacer(f transcode.RawFormat) *reservePacer {
	if f.SampleRate <= 0 || f.Channels <= 0 || f.FPSNum <= 0 || f.FPSDen <= 0 {
		return nil
	}
	minTick := (f.SampleRate * f.FPSDen) / f.FPSNum
	hop := min(f.SampleRate/100, max(64, minTick/4))
	win := 2 * hop
	search := max(16, hop/2)
	fade := make([]float32, hop)
	for i := range hop {
		fade[i] = float32(0.5 * (1.0 - math.Cos(math.Pi*float64(i)/float64(hop))))
	}
	return &reservePacer{
		format:   f,
		hop:      hop,
		win:      win,
		search:   search,
		fade:     fade,
		overlap:  make([]int16, hop*f.Channels),
		lastRate: 1.0,
	}
}

func (p *reservePacer) reset(in *broadcastInput) {
	if p == nil {
		return
	}
	p.input = in
	p.active = false
	p.stretching = false
	p.reached = false
	p.baseIdx = 0
	p.pcmIn = p.pcmIn[:0]
	p.pcmOut = p.pcmOut[:0]
	p.vidQueue = p.vidQueue[:0]
}

// desiredTempo decides whether to stretch at steadyReserveTempo or play at 1.0x.
func (p *reservePacer) desiredTempo(depth, target time.Duration, ok bool) float64 {
	if !ok || target <= 0 {
		p.stretching = false
		p.reached = false
		return 1.0
	}
	if depth >= target {
		p.stretching = false
		p.reached = true
		return 1.0
	}
	if !p.reached || depth <= target-steadyReserveHysteresis {
		p.stretching = true
		p.reached = false
		return steadyReserveTempo
	}
	if p.stretching {
		return steadyReserveTempo
	}
	return 1.0
}

func (p *reservePacer) syncRate(src reservePaceSource, rate float64) {
	rate = min(rate, 1.0)
	if src == nil || math.Abs(rate-p.lastRate) < 1e-6 {
		return
	}
	p.lastRate = rate
	src.setInputPlayRate(rate)
}

func (p *reservePacer) endIdx() int64 {
	return p.baseIdx + int64(len(p.pcmIn)/p.format.Channels)
}

func (p *reservePacer) appendPCM(raw []byte) {
	ch := p.format.Channels
	frames := len(raw) / (ch * 2)
	need := frames * ch
	off := len(p.pcmIn)
	if cap(p.pcmIn)-off < need {
		grown := make([]int16, off, max(off+need, 8*p.win*ch))
		copy(grown, p.pcmIn)
		p.pcmIn = grown
	}
	p.pcmIn = p.pcmIn[:off+need]
	for i := range need {
		p.pcmIn[off+i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
}

func (p *reservePacer) trimInput() {
	keepFrom := int64(math.Floor(p.nextIdeal)) - int64(p.search) - 4
	if keepFrom <= p.baseIdx {
		return
	}
	drop := min(int(keepFrom-p.baseIdx), len(p.pcmIn)/p.format.Channels)
	if drop <= 0 {
		return
	}
	ch := p.format.Channels
	copy(p.pcmIn, p.pcmIn[drop*ch:])
	p.pcmIn = p.pcmIn[:len(p.pcmIn)-drop*ch]
	p.baseIdx += int64(drop)
}

func (p *reservePacer) emitSamples(samples []int16) {
	off := len(p.pcmOut)
	need := len(samples) * 2
	if cap(p.pcmOut)-off < need {
		grown := make([]byte, off, max(off+need, 8*p.win*p.format.Channels*2))
		copy(grown, p.pcmOut)
		p.pcmOut = grown
	}
	p.pcmOut = p.pcmOut[:off+need]
	for i, s := range samples {
		binary.LittleEndian.PutUint16(p.pcmOut[off+i*2:], uint16(s))
	}
}

// prime initializes WSOLA from a single decoded RawFrame (at least 4*hop
// samples) with one 10 ms phase-matched overlap step, leaving hop samples of
// lookahead so zero-burst 1.0x streams never stall waiting for a second frame.
func (p *reservePacer) prime() bool {
	avail := int(p.endIdx() - p.baseIdx)
	if avail < 4*p.hop {
		return false
	}
	ch := p.format.Channels
	// Emit first hop [0..hop), set overlap to [hop..2*hop), then crossfade back
	// near 0 to establish a 1-hop lookahead cushion without silence or clicks.
	p.emitSamples(p.pcmIn[:p.hop*ch])
	copy(p.overlap, p.pcmIn[p.hop*ch:2*p.hop*ch])
	cand := p.bestCandidate(p.baseIdx, p.baseIdx, min(p.baseIdx+int64(p.hop), p.endIdx()-int64(p.win)))
	p.crossfadeAt(cand)
	p.prevChosen = cand
	p.nextIdeal = float64(cand)
	p.active = true
	return true
}

// bestCandidate finds the sample-frame offset in [lo, hi] whose first hop best
// correlates in-phase with p.overlap (the natural continuation of the previous grain).
func (p *reservePacer) bestCandidate(prefer, lo, hi int64) int64 {
	if hi <= lo {
		return lo
	}
	ch := p.format.Channels
	hop := p.hop
	var ovEnergy int64
	for i := range hop * ch {
		v := int64(p.overlap[i])
		ovEnergy += v * v
	}
	if ovEnergy == 0 {
		return min(max(prefer, lo), hi)
	}

	scoreAt := func(cand int64) float64 {
		base := int(cand-p.baseIdx) * ch
		var dot, cEnergy int64
		// Subsample every 2nd sample-frame for speed; full resolution on refinement.
		for k := 0; k < hop; k += 2 {
			idx := base + k*ch
			ovIdx := k * ch
			for c := range ch {
				a := int64(p.overlap[ovIdx+c])
				b := int64(p.pcmIn[idx+c])
				dot += a * b
				cEnergy += b * b
			}
		}
		if cEnergy == 0 {
			return 0
		}
		if dot <= 0 {
			return -float64(dot*dot) / float64(cEnergy)
		}
		return float64(dot) * float64(dot) / float64(cEnergy)
	}

	best := min(max(prefer, lo), hi)
	bestScore := scoreAt(best)
	// Coarse scan by 2 sample-frames, then 1-sample refinement around winner.
	for cand := lo; cand <= hi; cand += 2 {
		sc := scoreAt(cand)
		if sc > bestScore {
			bestScore = sc
			best = cand
		}
	}
	rLo := max(lo, best-2)
	rHi := min(hi, best+2)
	for cand := rLo; cand <= rHi; cand++ {
		base := int(cand-p.baseIdx) * ch
		var dot, cEnergy int64
		for i := range hop * ch {
			a := int64(p.overlap[i])
			b := int64(p.pcmIn[base+i])
			dot += a * b
			cEnergy += b * b
		}
		var sc float64
		if cEnergy > 0 {
			if dot >= 0 {
				sc = float64(dot) * float64(dot) / float64(cEnergy)
			} else {
				sc = -float64(dot*dot) / float64(cEnergy)
			}
		}
		if cand == rLo || sc > bestScore {
			bestScore = sc
			best = cand
		}
	}
	return best
}

func (p *reservePacer) crossfadeAt(cand int64) {
	ch := p.format.Channels
	hop := p.hop
	base := int(cand-p.baseIdx) * ch
	off := len(p.pcmOut)
	need := hop * ch * 2
	if cap(p.pcmOut)-off < need {
		grown := make([]byte, off, max(off+need, 8*p.win*ch*2))
		copy(grown, p.pcmOut)
		p.pcmOut = grown
	}
	p.pcmOut = p.pcmOut[:off+need]
	for k := range hop {
		w := p.fade[k]
		inv := 1.0 - w
		for c := range ch {
			ov := float32(p.overlap[k*ch+c])
			in := float32(p.pcmIn[base+k*ch+c])
			mixed := int32(math.Round(float64(inv*ov + w*in)))
			if mixed > 32767 {
				mixed = 32767
			} else if mixed < -32768 {
				mixed = -32768
			}
			binary.LittleEndian.PutUint16(p.pcmOut[off+(k*ch+c)*2:], uint16(int16(mixed)))
		}
	}
	copy(p.overlap, p.pcmIn[base+hop*ch:base+2*hop*ch])
}

func (p *reservePacer) synthesizeStep(tempo float64) bool {
	maxStart := p.endIdx() - int64(p.win)
	if maxStart < p.baseIdx {
		return false
	}
	target := p.nextIdeal + float64(p.hop)*tempo
	center := int64(math.Round(target))
	// Require enough input ahead of center so we don't clamp into the tail prematurely.
	if center > maxStart {
		return false
	}
	var chosen int64
	natural := p.prevChosen + int64(p.hop)
	if tempo >= 0.9999 && natural >= p.baseIdx && natural <= maxStart && math.Abs(target-float64(natural)) <= float64(p.search) {
		// At 1.0x, natural continuation crossfades overlap with itself (exact identity)
		// and smoothly walks back to zero phase offset.
		chosen = natural
	} else {
		lo := max(p.baseIdx, center-int64(p.search))
		hi := min(maxStart, center+int64(p.search))
		chosen = p.bestCandidate(natural, lo, hi)
	}
	p.crossfadeAt(chosen)
	p.prevChosen = chosen
	p.nextIdeal = target
	p.trimInput()
	return true
}

// next produces one output tick of (Video, PCM) at tempo. pulledNew reports
// whether a fresh source video buffer is airing on this tick (as opposed to a
// stretched cadence tick reusing the current picture with continuous WSOLA audio).
func (p *reservePacer) next(in *broadcastInput, wantSamples int, tempo float64, pull func() (transcode.RawFrame, bool)) (frame transcode.RawFrame, pulledNew bool, ok bool) {
	tempo = min(tempo, 1.0)
	if p == nil || in == nil {
		f, got := pull()
		return f, got, got
	}
	if p.input != in {
		p.reset(in)
	}
	if !p.active && len(p.vidQueue) == 0 && tempo >= 0.9999 {
		f, got := pull()
		return f, got, got
	}

	wantBytes := wantSamples * p.format.Channels * 2
	for len(p.pcmOut) < wantBytes {
		if !p.active {
			if int(p.endIdx()-p.baseIdx) < 4*p.hop {
				f, got := pull()
				if !got {
					return transcode.RawFrame{}, false, false
				}
				p.acceptFrame(f)
			}
			if !p.prime() {
				return transcode.RawFrame{}, false, false
			}
			continue
		}
		if p.synthesizeStep(tempo) {
			continue
		}
		f, got := pull()
		if !got {
			return transcode.RawFrame{}, false, false
		}
		p.acceptFrame(f)
	}

	queuedOut := len(p.pcmOut) / (p.format.Channels * 2)
	remOut := queuedOut - wantSamples
	emittedEnd := p.nextIdeal + float64(p.hop) - float64(remOut)*tempo
	emittedMid := emittedEnd - 0.5*float64(wantSamples)*tempo
	if len(p.vidQueue) > 1 && int64(math.Round(emittedMid)) >= p.vidQueue[0].endSample {
		p.vidQueue = p.vidQueue[1:]
	}

	pcm := make([]byte, wantBytes)
	copy(pcm, p.pcmOut[:wantBytes])
	copy(p.pcmOut, p.pcmOut[wantBytes:])
	p.pcmOut = p.pcmOut[:len(p.pcmOut)-wantBytes]

	if len(p.vidQueue) == 0 {
		return transcode.RawFrame{}, false, false
	}
	cur := &p.vidQueue[0]
	freshVideo := !cur.emitted
	cur.emitted = true
	out := transcode.RawFrame{
		Video:          cur.video,
		PCM:            pcm,
		SourcePTS:      cur.pts,
		SourcePTSValid: cur.valid,
		PolicyTags:     cur.tags,
	}
	return out, freshVideo, out.Video != nil
}

func (p *reservePacer) acceptFrame(f transcode.RawFrame) {
	p.appendPCM(f.PCM)
	p.vidQueue = append(p.vidQueue, pacedVideo{
		video:     f.Video,
		tags:      f.PolicyTags,
		pts:       f.SourcePTS,
		valid:     f.SourcePTSValid,
		endSample: p.endIdx(),
	})
}
