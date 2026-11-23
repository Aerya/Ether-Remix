package uploadbudget

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/sockerr"
)

const (
	// Minimum size and duration for a single transfer to count as real link
	// throughput rather than socket-buffer absorption or header timing noise.
	minObservationBytes = 64 << 10
	minObservationDur   = 100 * time.Millisecond

	// A transfer at least this large taking at least this long exceeds typical
	// TCP send-buffer burst absorption. Only used alongside explicit starvation
	// or slow-segment evidence (never alone, since client-paced direct play also
	// produces multi-MB, multi-second writes).
	sustainedBurstBytes = 512 << 10
	sustainedBurstDur   = 250 * time.Millisecond

	// Non-trivial TCP kernel send queue indicates active send-buffer saturation.
	backpressureBacklogBytes = 32 << 10

	// Delivery rate thresholds distinguishing starvation from smooth client pacing:
	// - A discrete HLS/DASH segment taking >= 115% of its own media duration is
	//   delivering clearly slower than real time.
	// - A continuous stream delivering <= 85% of media bitrate under send-queue
	//   saturation is falling behind client consumption.
	// - A non-starved, non-congested stream delivering >= 95% of media bitrate
	//   is playing smoothly.
	segStarvedPct       = 115
	belowConsumptionPct = 85
	smoothDeliveryPct   = 95

	// Sustained non-starved playback required before a stream establishes a
	// smooth-playback floor on the learned budget.
	minSmoothWindows  = 3
	minSmoothSpan     = 10 * time.Second
	smoothSessionGap  = 30 * time.Second
	smoothSessionTTL  = 60 * time.Second
	starvedSessionTTL = 30 * time.Second

	// Minimum passive evidence required before Ether enforces an automatic
	// upload budget. Below this, Ether assumes no limit (existing behaviour).
	minEvidenceSamples    = 4
	minEvidenceSpan       = 10 * time.Second
	minStarvedSamples     = 2
	minStarvedSpan        = 5 * time.Second
	maxEstimatorSamples   = 64
	estimatorSampleMaxAge = 24 * time.Hour

	// Conservative scaling and safety floor for learned upload capacity.
	conservativeFactorPct = 95
	minEstimatedKbps      = 1500
)

// Observation captures one passive throughput measurement from an active
// remote Ether stream. LAN streams must never be passed to the estimator.
type Observation struct {
	At                time.Time
	SessionID         string
	Bytes             int64
	Duration          time.Duration
	SegDuration       time.Duration
	StreamRateKbps    int
	AggregateKbps     int
	MediaBitrateKbps  int
	TCPBacklogBytes   int
	TCPStall          sockerr.TCPStallKind
	BufferFilling     bool
	Starved           bool
	ConcurrentStreams int
}

type sample struct {
	at                time.Time
	aggregateKbps     int
	starved           bool
	backpressured     bool
	concurrentStreams int
}

type smoothSession struct {
	firstAt     time.Time
	lastAt      time.Time
	windows     int
	bitrateKbps int
	proven      bool
}

// Estimator learns a conservative rolling estimate of the server's usable
// internet upload capacity strictly from real remote streams (never via
// third-party speed tests). Until sufficient evidence exists, Estimate
// returns 0 so Ether keeps its existing unlimited default.
type Estimator struct {
	mu                 sync.Mutex
	now                func() time.Time
	samples            []sample
	smoothSessions     map[string]*smoothSession
	starvedSessions    map[string]time.Time
	smoothFloorKbps    int
	smoothFloorAt      time.Time
	smoothFloorSession string
}

// NewEstimator creates a passive upload capacity estimator.
func NewEstimator(now func() time.Time) *Estimator {
	if now == nil {
		now = time.Now
	}
	return &Estimator{
		now:             now,
		smoothSessions:  map[string]*smoothSession{},
		starvedSessions: map[string]time.Time{},
	}
}

// Observe records one remote delivery observation. Client-paced steady-state
// windows and receiver-window-limited or app-limited stalls are never counted
// as upload capacity evidence, though sustained smooth playback raises a floor
// so an existing learned budget never caps below smoothly playing streams.
func (e *Estimator) Observe(obs Observation) bool {
	if e == nil || obs.Bytes < minObservationBytes || obs.Duration < minObservationDur {
		return false
	}
	at := obs.At
	if at.IsZero() {
		at = e.now()
	}

	streamRate := obs.StreamRateKbps
	if streamRate <= 0 {
		streamRate = int(math.Round(float64(obs.Bytes*8) / obs.Duration.Seconds() / 1000))
	}
	agg := obs.AggregateKbps
	if agg < streamRate {
		agg = streamRate
	}
	if agg <= 0 {
		return false
	}

	mediaKbps := obs.MediaBitrateKbps
	if mediaKbps <= 0 && obs.SegDuration > 0 {
		mediaKbps = int(math.Round(float64(obs.Bytes*8) / obs.SegDuration.Seconds() / 1000))
	}

	rwndLimited := obs.TCPStall == sockerr.TCPStallRwnd
	appLimited := obs.TCPStall == sockerr.TCPStallAppLimited
	congestion := obs.TCPStall == sockerr.TCPStallCongestion
	sendQueueSaturated := congestion || obs.TCPBacklogBytes >= backpressureBacklogBytes

	segStarved := obs.SegDuration > 0 && obs.Duration >= obs.SegDuration*segStarvedPct/100
	belowConsumption := obs.SegDuration == 0 && mediaKbps > 0 && streamRate*100 <= mediaKbps*belowConsumptionPct
	sustained := obs.Bytes >= sustainedBurstBytes && obs.Duration >= sustainedBurstDur

	// Smooth steady-state playback tracking (independent of whether the window
	// qualifies as bottleneck evidence). A receiver-window-limited client-paced
	// stream delivering at >= 95% of media bitrate without buffering proves the
	// uplink can sustain at least that stream's bitrate smoothly.
	smoothWindow := !obs.Starved &&
		!segStarved &&
		!congestion &&
		!obs.BufferFilling &&
		mediaKbps > 0 &&
		streamRate*100 >= mediaKbps*smoothDeliveryPct &&
		(obs.SegDuration == 0 || obs.Duration <= obs.SegDuration)

	sessionKey := obs.SessionID
	if sessionKey == "" {
		sessionKey = "_default"
	}

	// Receiver-window-limited (client pacing / full player buffer) and
	// application-limited (slow upstream source / encoder) windows are NEVER
	// evidence of a server upload bottleneck.
	if rwndLimited || appLimited {
		e.mu.Lock()
		e.pruneLocked(at)
		if obs.Starved {
			delete(e.smoothSessions, sessionKey)
			if obs.SessionID != "" {
				if e.starvedSessions == nil {
					e.starvedSessions = map[string]time.Time{}
				}
				e.starvedSessions[obs.SessionID] = at
			}
		} else if smoothWindow {
			e.recordSmoothLocked(sessionKey, obs.SessionID, at, mediaKbps)
		} else if rwndLimited {
			// A quiet VBR scene on an already-proven smooth client-paced stream
			// keeps the session alive without breaking its smooth status.
			if ss, ok := e.smoothSessions[sessionKey]; ok && ss.proven {
				ss.lastAt = at
			}
		}
		e.mu.Unlock()
		return false
	}

	// Only count windows where the client demonstrably wanted more data than
	// it received:
	// 1. Explicit client buffer starvation (BufferEvents increased), with
	//    send-queue saturation or sustained transfer.
	// 2. Discrete HLS/DASH segment took clearly longer than its own media
	//    duration (>= 1.15x SegDuration), with send-queue saturation or
	//    sustained transfer.
	// 3. Continuous stream delivery rate clearly below consumption rate
	//    (<= 85% of media bitrate) WITH send-queue saturation.
	// 4. Active buffer fill (initial fill or post-seek fill) WITH kernel
	//    TCP_INFO confirming congestion-limited (not rwnd/app-limited) writes.
	var capacityEvidence bool
	switch {
	case obs.Starved:
		capacityEvidence = sendQueueSaturated || sustained
	case segStarved:
		capacityEvidence = sendQueueSaturated || sustained
	case belowConsumption:
		capacityEvidence = sendQueueSaturated
	case obs.BufferFilling && congestion:
		capacityEvidence = true
	}

	if !capacityEvidence {
		if smoothWindow {
			e.mu.Lock()
			e.pruneLocked(at)
			e.recordSmoothLocked(sessionKey, obs.SessionID, at, mediaKbps)
			e.mu.Unlock()
		}
		return false
	}

	starved := obs.Starved || segStarved || (belowConsumption && sendQueueSaturated)
	concurrent := obs.ConcurrentStreams
	if concurrent < 1 {
		concurrent = 1
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneLocked(at)

	if starved {
		e.clearSmoothLocked(sessionKey, obs.SessionID, at, agg)
	}

	// If multiple concurrent streams are repeatedly starved at a lower rate,
	// retire stale higher observations older than 10 minutes so an ISP slowdown
	// or link change adapts downward without reacting to a single slow client.
	e.samples = append(e.samples, sample{
		at:                at,
		aggregateKbps:     agg,
		starved:           starved,
		backpressured:     true,
		concurrentStreams: concurrent,
	})
	if len(e.samples) > maxEstimatorSamples {
		copy(e.samples, e.samples[len(e.samples)-maxEstimatorSamples:])
		e.samples = e.samples[:maxEstimatorSamples]
	}
	e.adaptDownIfMultiStreamSaturatedLocked(at)
	return true
}

func (e *Estimator) recordSmoothLocked(sessionKey, rawSessionID string, at time.Time, bitrateKbps int) {
	if e.smoothSessions == nil {
		e.smoothSessions = map[string]*smoothSession{}
	}
	ss := e.smoothSessions[sessionKey]
	if ss == nil || at.Before(ss.lastAt) || at.Sub(ss.lastAt) > smoothSessionGap {
		e.smoothSessions[sessionKey] = &smoothSession{
			firstAt:     at,
			lastAt:      at,
			windows:     1,
			bitrateKbps: bitrateKbps,
		}
		return
	}
	ss.lastAt = at
	ss.windows++
	if bitrateKbps > 0 {
		ss.bitrateKbps = bitrateKbps
	}
	if ss.windows >= minSmoothWindows && at.Sub(ss.firstAt) >= minSmoothSpan {
		ss.proven = true
	}
	if !ss.proven {
		return
	}
	activeSum := 0
	provenCount := 0
	for _, other := range e.smoothSessions {
		if other.proven && !at.Before(other.lastAt) && at.Sub(other.lastAt) <= smoothSessionGap {
			activeSum += other.bitrateKbps
			provenCount++
		}
	}
	if activeSum > e.smoothFloorKbps {
		e.smoothFloorKbps = activeSum
		e.smoothFloorAt = at
		if rawSessionID != "" && provenCount == 1 {
			e.smoothFloorSession = rawSessionID
		} else {
			e.smoothFloorSession = ""
		}
	} else if activeSum > 0 && activeSum == e.smoothFloorKbps {
		e.smoothFloorAt = at
	}
}

func (e *Estimator) clearSmoothLocked(sessionKey, rawSessionID string, at time.Time, starvedRateKbps int) {
	delete(e.smoothSessions, sessionKey)
	if rawSessionID != "" {
		if e.starvedSessions == nil {
			e.starvedSessions = map[string]time.Time{}
		}
		e.starvedSessions[rawSessionID] = at
	}
	// If the exact session that established the smooth floor is now itself
	// starving at a lower rate, retire its floor so adaptation isn't blocked.
	if e.smoothFloorKbps > 0 && rawSessionID != "" && e.smoothFloorSession == rawSessionID && (starvedRateKbps <= 0 || starvedRateKbps < e.smoothFloorKbps) {
		e.smoothFloorKbps = 0
		e.smoothFloorAt = time.Time{}
		e.smoothFloorSession = ""
		activeSum := 0
		for _, other := range e.smoothSessions {
			if other.proven && !at.Before(other.lastAt) && at.Sub(other.lastAt) <= smoothSessionGap {
				activeSum += other.bitrateKbps
			}
		}
		if activeSum > 0 {
			e.smoothFloorKbps = activeSum
			e.smoothFloorAt = at
		}
	}
}

// IsSessionSmooth reports whether sessionID has recently sustained smooth,
// non-starved delivery in the estimator.
func (e *Estimator) IsSessionSmooth(sessionID string, now time.Time) bool {
	if e == nil || sessionID == "" {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ss, ok := e.smoothSessions[sessionID]
	if !ok || !ss.proven {
		return false
	}
	return !now.Before(ss.lastAt) && now.Sub(ss.lastAt) <= smoothSessionGap
}

// IsSessionStarved reports whether sessionID recently experienced delivery
// starvation in the estimator.
func (e *Estimator) IsSessionStarved(sessionID string, now time.Time) bool {
	if e == nil || sessionID == "" {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	at, ok := e.starvedSessions[sessionID]
	if !ok {
		return false
	}
	return !now.Before(at) && now.Sub(at) <= starvedSessionTTL
}

func (e *Estimator) pruneLocked(now time.Time) {
	cutoff := now.Add(-estimatorSampleMaxAge)
	kept := e.samples[:0]
	for _, s := range e.samples {
		if !s.at.Before(cutoff) {
			kept = append(kept, s)
		}
	}
	e.samples = kept

	if e.smoothFloorKbps > 0 && !e.smoothFloorAt.IsZero() && e.smoothFloorAt.Before(cutoff) {
		e.smoothFloorKbps = 0
		e.smoothFloorAt = time.Time{}
		e.smoothFloorSession = ""
	}
	for id, ss := range e.smoothSessions {
		if now.Sub(ss.lastAt) > smoothSessionTTL {
			delete(e.smoothSessions, id)
		}
	}
	for id, at := range e.starvedSessions {
		if now.Sub(at) > starvedSessionTTL {
			delete(e.starvedSessions, id)
		}
	}
}

func (e *Estimator) adaptDownIfMultiStreamSaturatedLocked(now time.Time) {
	recentWindow := now.Add(-5 * time.Minute)
	recentCount := 0
	recentMax := 0
	for _, s := range e.samples {
		if !s.at.Before(recentWindow) && s.concurrentStreams >= 2 && s.starved {
			recentCount++
			if s.aggregateKbps > recentMax {
				recentMax = s.aggregateKbps
			}
		}
	}
	if recentCount < 3 || recentMax <= 0 {
		return
	}
	staleCutoff := now.Add(-10 * time.Minute)
	kept := e.samples[:0]
	for _, s := range e.samples {
		if s.at.Before(staleCutoff) && s.aggregateKbps > recentMax*120/100 {
			continue
		}
		kept = append(kept, s)
	}
	e.samples = kept
	if e.smoothFloorKbps > recentMax {
		e.smoothFloorKbps = 0
		e.smoothFloorAt = time.Time{}
		e.smoothFloorSession = ""
	}
}

// minCapacityForUsable returns the smallest rounded (100 kbps) upload capacity
// whose UsableBudgetKbps (~85%) is at least usableKbps.
func minCapacityForUsable(usableKbps int) int {
	if usableKbps <= 0 {
		return 0
	}
	raw := (usableKbps*100 + TargetUtilizationPct - 1) / TargetUtilizationPct
	est := ((raw + 99) / 100) * 100
	if est < minEstimatedKbps {
		est = minEstimatedKbps
	}
	return est
}

// Estimate returns the conservative rolling upload capacity estimate in kbps
// (0 when insufficient evidence exists) and the count of active samples.
func (e *Estimator) Estimate() (kbps int, samples int) {
	if e == nil {
		return 0, 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	e.pruneLocked(now)
	n := len(e.samples)
	if n == 0 {
		return 0, 0
	}

	var starvedCount int
	var firstAll, lastAll time.Time
	var firstStarved, lastStarved time.Time
	for _, s := range e.samples {
		if firstAll.IsZero() || s.at.Before(firstAll) {
			firstAll = s.at
		}
		if s.at.After(lastAll) {
			lastAll = s.at
		}
		if s.starved || s.concurrentStreams >= 2 {
			starvedCount++
			if firstStarved.IsZero() || s.at.Before(firstStarved) {
				firstStarved = s.at
			}
			if s.at.After(lastStarved) {
				lastStarved = s.at
			}
		}
	}

	enoughGeneral := n >= minEvidenceSamples && lastAll.Sub(firstAll) >= minEvidenceSpan
	enoughStarved := starvedCount >= minStarvedSamples && lastStarved.Sub(firstStarved) >= minStarvedSpan
	if !enoughGeneral && !enoughStarved {
		return 0, n
	}

	// Weighted 75th percentile of aggregate throughput. Using the upper
	// quartile avoids letting a single viewer on slow mobile/hotel Wi-Fi drag
	// down the server's estimated uplink, while multi-stream saturation and
	// conservative 95% scaling keep the budget safely below saturation.
	type weighted struct {
		kbps   int
		weight float64
	}
	items := make([]weighted, 0, n)
	totalW := 0.0
	for _, s := range e.samples {
		ageHours := now.Sub(s.at).Hours()
		if ageHours < 0 {
			ageHours = 0
		}
		// Exponential time decay with 6-hour half-life.
		w := math.Pow(0.5, ageHours/6.0)
		if s.starved {
			w *= 1.5
		}
		if s.concurrentStreams >= 2 {
			w *= 1.5
		}
		items = append(items, weighted{kbps: s.aggregateKbps, weight: w})
		totalW += w
	}
	sort.Slice(items, func(i, j int) bool { return items[i].kbps < items[j].kbps })

	target := totalW * 0.75
	acc := 0.0
	p75 := items[len(items)-1].kbps
	for _, it := range items {
		acc += it.weight
		if acc >= target {
			p75 = it.kbps
			break
		}
	}

	est := p75 * conservativeFactorPct / 100
	if est < minEstimatedKbps {
		est = minEstimatedKbps
	}
	// Round to nearest 100 kbps for clean display and stability.
	est = ((est + 50) / 100) * 100

	// Never lower the learned budget below:
	// 1. The combined bitrate of remote streams currently playing smoothly, or
	// 2. A stream that played smoothly more recently than the bottleneck samples
	//    (so an older low estimate from a slow client cannot cap a stream that
	//    subsequently proved smooth playback on the server's uplink).
	floorKbps := e.activeSmoothKbpsLocked(now)
	if e.smoothFloorKbps > floorKbps && !e.smoothFloorAt.IsZero() && !e.smoothFloorAt.Before(lastAll) {
		floorKbps = e.smoothFloorKbps
	}
	if floorKbps > 0 && UsableBudgetKbps(est) < floorKbps {
		est = minCapacityForUsable(floorKbps)
	}
	return est, n
}

func (e *Estimator) activeSmoothKbpsLocked(now time.Time) int {
	sum := 0
	for _, ss := range e.smoothSessions {
		if ss.proven && !now.Before(ss.lastAt) && now.Sub(ss.lastAt) <= smoothSessionGap {
			sum += ss.bitrateKbps
		}
	}
	return sum
}
