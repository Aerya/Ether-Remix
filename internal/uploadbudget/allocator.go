package uploadbudget

import (
	"sort"
	"sync"
	"time"
)

const (
	// TargetUtilizationPct leaves ~15% headroom for TCP/TLS overhead, ACKs,
	// metadata browsing and segment bitrate spikes.
	TargetUtilizationPct = 85

	// MinStreamBitrateKbps prevents extreme crowding from crushing a stream to
	// an unplayable rate.
	MinStreamBitrateKbps = 800

	// DefaultLiveStreamKbps is the fallback estimate for an active remote Live
	// TV stream whose bitrate has not been probed yet.
	DefaultLiveStreamKbps = 4000

	// StepDownMarginPct avoids reacting to negligible (<8%) budget shifts on
	// an already-running stream when total usage is within target.
	StepDownMarginPct = 92

	// StepUpMarginPct requires >= 15% headroom above the next quality step
	// before stepping an adaptive stream back up (hysteresis).
	StepUpMarginPct = 115

	// Cooldowns prevent flapping when viewers join, seek, pause or leave.
	StepDownCooldown = 8 * time.Second
	StepUpCooldown   = 20 * time.Second
)

// ActiveStream summarizes one running Ether stream for budget accounting.
type ActiveStream struct {
	ID              string
	Remote          bool
	Live            bool
	DirectPlay      bool
	Adaptive        bool
	BitrateKbps     int   // current effective stream bitrate
	BaseBitrateKbps int   // uncapped natural/base bitrate demand (0 = unknown/high)
	CurrentCapKbps  int   // currently applied upload-budget cap (0 = uncapped)
	RungKbps        []int // optional descending ladder rung bitrates (base -> lowest)
}

// Adjustment asks a running adaptive remote stream to adopt a new upload
// budget cap (0 clears the budget cap so the stream steps back up to base).
type Adjustment struct {
	SessionID string
	CapKbps   int
	StepDown  bool
}

// UsableBudgetKbps returns ~85% of total upload capacity (0 when no budget).
func UsableBudgetKbps(capacityKbps int) int {
	if capacityKbps <= 0 {
		return 0
	}
	u := capacityKbps * TargetUtilizationPct / 100
	if u < MinStreamBitrateKbps {
		u = MinStreamBitrateKbps
	}
	return u
}

func effectiveStreamBitrate(s ActiveStream) int {
	if s.BitrateKbps > 0 {
		return s.BitrateKbps
	}
	if s.BaseBitrateKbps > 0 {
		return s.BaseBitrateKbps
	}
	if s.Live {
		return DefaultLiveStreamKbps
	}
	return 0
}

type elasticDemand struct {
	id     string
	demand int // 0 means unbounded (wants as much as fair share allows)
}

// waterFill divides pool across streams using max-min fairness so streams that
// naturally need less than an equal share leave the surplus for higher-bitrate
// streams.
func waterFill(pool int, streams []elasticDemand) map[string]int {
	out := make(map[string]int, len(streams))
	if len(streams) == 0 {
		return out
	}
	if pool <= 0 {
		for _, s := range streams {
			out[s.id] = MinStreamBitrateKbps
		}
		return out
	}

	// Finite demands sorted ascending come first; unbounded (0) demands last.
	ordered := make([]elasticDemand, len(streams))
	copy(ordered, streams)
	sort.SliceStable(ordered, func(i, j int) bool {
		di, dj := ordered[i].demand, ordered[j].demand
		if di == 0 {
			return false
		}
		if dj == 0 {
			return true
		}
		return di < dj
	})

	rem := pool
	for i, s := range ordered {
		left := len(ordered) - i
		share := rem / left
		if share < MinStreamBitrateKbps {
			share = MinStreamBitrateKbps
		}
		if s.demand > 0 && s.demand <= share {
			out[s.id] = s.demand
			rem -= s.demand
			if rem < 0 {
				rem = 0
			}
			continue
		}
		for _, rest := range ordered[i:] {
			out[rest.id] = share
		}
		break
	}
	return out
}

// AllocateForNewStream computes the dynamic bitrate cap (kbps) for a newly
// starting remote stream so that all active remote streams (Ether VOD + Live
// TV + external sibling PMS WAN streams) stay within usableKbps (~85% of total
// budget). Returns 0 when no upload budget is active.
//
// Invariants:
//   - LAN streams (!s.Remote) are never counted.
//   - Direct-play / non-adaptive remote streams are counted at their bitrate
//     without being forced to transcode.
//   - Live TV remote streams are counted.
func AllocateForNewStream(usableKbps, externalKbps int, active []ActiveStream, excludeID string, newDemandKbps int) int {
	if usableKbps <= 0 {
		return 0
	}
	if externalKbps < 0 {
		externalKbps = 0
	}

	fixedKbps := externalKbps
	const newKey = "\x00new"
	elastics := make([]elasticDemand, 0, len(active)+1)

	for _, s := range active {
		if !s.Remote {
			continue // LAN streams are never counted or affected.
		}
		if excludeID != "" && s.ID == excludeID {
			continue
		}
		if s.DirectPlay || !s.Adaptive {
			fixedKbps += effectiveStreamBitrate(s)
			continue
		}
		demand := s.BaseBitrateKbps
		if demand <= 0 {
			demand = effectiveStreamBitrate(s)
		}
		elastics = append(elastics, elasticDemand{id: s.ID, demand: demand})
	}
	elastics = append(elastics, elasticDemand{id: newKey, demand: 0})

	alloc := waterFill(usableKbps-fixedKbps, elastics)
	capKbps := alloc[newKey]
	if capKbps < MinStreamBitrateKbps {
		capKbps = MinStreamBitrateKbps
	}
	return capKbps
}

// HysteresisController tracks per-session step-down / step-up timing so
// running adaptive HLS remote streams step down promptly when capacity tightens
// and step back up smoothly without flapping once headroom stabilizes.
type HysteresisController struct {
	mu              sync.Mutex
	sessions        map[string]*hysteresisEntry
	lastRemoteCount int
	lastExternal    int
}

type hysteresisEntry struct {
	appliedCap      int       // last cap handed to the session (0 = uncapped)
	lastDownAt      time.Time // most recent step-down timestamp
	lastChangeAt    time.Time // most recent adjustment in either direction
	upEligibleSince time.Time // when headroom for a step-up first appeared
}

// NewHysteresisController creates a stateful hysteresis controller for running
// adaptive remote streams.
func NewHysteresisController() *HysteresisController {
	return &HysteresisController{
		sessions: map[string]*hysteresisEntry{},
	}
}

// Reconcile computes budget cap adjustments for currently running adaptive
// remote streams. LAN streams and direct-play/non-adaptive streams are never
// adjusted.
func (h *HysteresisController) Reconcile(now time.Time, usableKbps, externalKbps int, active []ActiveStream) []Adjustment {
	if h == nil {
		return nil
	}
	if externalKbps < 0 {
		externalKbps = 0
	}

	// Filter to active remote streams and compute aggregate demand.
	fixedKbps := externalKbps
	totalCurrentKbps := externalKbps
	remoteCount := 0
	var elastics []elasticDemand
	adaptiveByID := make(map[string]ActiveStream, len(active))
	liveIDs := make(map[string]struct{}, len(active))

	for _, s := range active {
		if !s.Remote {
			continue // LAN streams are completely exempt.
		}
		remoteCount++
		liveIDs[s.ID] = struct{}{}
		cur := effectiveStreamBitrate(s)
		totalCurrentKbps += cur
		if s.DirectPlay || !s.Adaptive {
			fixedKbps += cur
			continue
		}
		demand := s.BaseBitrateKbps
		if demand <= 0 {
			demand = cur
		}
		elastics = append(elastics, elasticDemand{id: s.ID, demand: demand})
		adaptiveByID[s.ID] = s
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Prune ended sessions so memory stays O(active streams).
	for id := range h.sessions {
		if _, ok := liveIDs[id]; !ok {
			delete(h.sessions, id)
		}
	}

	loadIncreased := remoteCount > h.lastRemoteCount || externalKbps > h.lastExternal+500
	h.lastRemoteCount = remoteCount
	h.lastExternal = externalKbps

	if len(elastics) == 0 {
		return nil
	}

	var alloc map[string]int
	if usableKbps > 0 {
		alloc = waterFill(usableKbps-fixedKbps, elastics)
	}

	var out []Adjustment
	overBudget := usableKbps > 0 && totalCurrentKbps > usableKbps

	for _, ed := range elastics {
		s := adaptiveByID[ed.id]
		st := h.sessions[s.ID]
		if st == nil {
			st = &hysteresisEntry{appliedCap: s.CurrentCapKbps}
			h.sessions[s.ID] = st
		}
		currentCap := st.appliedCap
		if currentCap == 0 && s.CurrentCapKbps > 0 {
			currentCap = s.CurrentCapKbps
			st.appliedCap = currentCap
		}

		// Desired cap: 0 means uncapped (base bitrate fits comfortably).
		desiredCap := 0
		if usableKbps > 0 {
			share := alloc[s.ID]
			base := s.BaseBitrateKbps
			if base <= 0 {
				base = effectiveStreamBitrate(s)
			}
			if base == 0 || share < base {
				desiredCap = max(share, MinStreamBitrateKbps)
			}
		}

		// Determine if this is a step-down, step-up, or steady state.
		isStepDown := false
		isStepUp := false
		switch {
		case desiredCap > 0 && currentCap == 0:
			isStepDown = true
		case desiredCap > 0 && currentCap > 0 && desiredCap < currentCap:
			isStepDown = true
		case desiredCap == 0 && currentCap > 0:
			isStepUp = true
		case desiredCap > 0 && currentCap > 0 && desiredCap > currentCap:
			isStepUp = true
		}

		if isStepDown {
			st.upEligibleSince = time.Time{}
			// Ignore tiny noise unless total usage actually exceeds 85% budget.
			baseline := currentCap
			if baseline == 0 {
				baseline = s.BaseBitrateKbps
				if baseline == 0 {
					baseline = effectiveStreamBitrate(s)
				}
			}
			meaningful := overBudget || baseline == 0 || desiredCap*100 <= baseline*StepDownMarginPct
			if !meaningful {
				continue
			}
			// Step down immediately when a new stream pushed total load up;
			// otherwise respect StepDownCooldown.
			if !loadIncreased && !st.lastChangeAt.IsZero() && now.Sub(st.lastChangeAt) < StepDownCooldown {
				continue
			}
			st.appliedCap = desiredCap
			st.lastDownAt = now
			st.lastChangeAt = now
			out = append(out, Adjustment{SessionID: s.ID, CapKbps: desiredCap, StepDown: true})
			continue
		}

		if isStepUp {
			// Require clear headroom margin before stepping back up so small
			// oscillations never flap between rungs.
			neededForStepUp := stepUpThresholdKbps(s, currentCap)
			hasHeadroom := desiredCap == 0 || desiredCap >= neededForStepUp
			if !hasHeadroom {
				st.upEligibleSince = time.Time{}
				continue
			}
			if st.upEligibleSince.IsZero() {
				st.upEligibleSince = now
			}
			// Must hold headroom continuously for StepUpCooldown AND wait at
			// least StepUpCooldown since the last step-down/change.
			if now.Sub(st.upEligibleSince) < StepUpCooldown {
				continue
			}
			if !st.lastDownAt.IsZero() && now.Sub(st.lastDownAt) < StepUpCooldown {
				continue
			}
			if !st.lastChangeAt.IsZero() && now.Sub(st.lastChangeAt) < StepUpCooldown {
				continue
			}
			st.appliedCap = desiredCap
			st.lastChangeAt = now
			st.upEligibleSince = time.Time{}
			out = append(out, Adjustment{SessionID: s.ID, CapKbps: desiredCap, StepDown: false})
			continue
		}

		// Steady state.
		st.upEligibleSince = time.Time{}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

// stepUpThresholdKbps computes the minimum cap required to step up cleanly
// with StepUpMarginPct hysteresis. When the session publishes discrete ladder
// rungs, it checks headroom above the next rung higher than currentCap;
// otherwise it checks StepUpMarginPct above currentCap.
func stepUpThresholdKbps(s ActiveStream, currentCap int) int {
	if currentCap <= 0 {
		return 0
	}
	nextHigher := 0
	for _, r := range s.RungKbps {
		if r > currentCap && (nextHigher == 0 || r < nextHigher) {
			nextHigher = r
		}
	}
	if nextHigher > 0 {
		return nextHigher * StepUpMarginPct / 100
	}
	return currentCap * StepUpMarginPct / 100
}
