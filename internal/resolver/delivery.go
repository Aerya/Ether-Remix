package resolver

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
)

type deliverySample struct {
	rate      float64
	at        time.Time
	count     int
	stallRate float64
	stallAt   time.Time
}
type deliveryState struct {
	mu      sync.Mutex
	samples map[string]deliverySample
}

// Measured delivery is a short-lived routing hint, never a permanent rejection.
// Usenet listings share the configured NNTP pool; torrent evidence is scoped
// to the release because different debrid links may use different CDNs.
func deliveryKey(r core.Release) string {
	if r.Protocol == core.ProtocolTorrent || r.Protocol == core.ProtocolHTTP {
		return r.Protocol + ":" + r.ID
	}
	return "usenet"
}

func (s *Service) ObserveSourceDelivery(r core.Release, bytesPerSecond float64) {
	if bytesPerSecond <= 0 || math.IsNaN(bytesPerSecond) || math.IsInf(bytesPerSecond, 0) {
		return
	}
	s.delivery.mu.Lock()
	defer s.delivery.mu.Unlock()
	if s.delivery.samples == nil {
		s.delivery.samples = map[string]deliverySample{}
	}
	now := s.now()
	key := deliveryKey(r)
	old := s.delivery.samples[key]
	if !old.at.IsZero() && now.Sub(old.at) < 2*time.Second {
		return
	}
	if !old.at.IsZero() && now.Sub(old.at) > 2*time.Minute {
		old.rate, old.at, old.count = 0, time.Time{}, 0
	}
	if !old.stallAt.IsZero() && now.Sub(old.stallAt) > 2*time.Minute {
		old.stallRate, old.stallAt = 0, time.Time{}
	}
	rate := bytesPerSecond
	if old.count > 0 {
		rate = old.rate*.8 + rate*.2
	}
	if _, exists := s.delivery.samples[key]; !exists && len(s.delivery.samples) >= 256 {
		clear(s.delivery.samples)
		old = deliverySample{}
		rate = bytesPerSecond
	}
	old.rate = rate
	old.at = now
	old.count++
	s.delivery.samples[key] = old
}

// ObserveSourceStall records source throughput at which playback actually
// starved or rebuffered. Only stall-backed evidence justifies demoting a
// preferred release below a lower-scored fallback.
func (s *Service) ObserveSourceStall(r core.Release, bytesPerSecond float64) {
	if bytesPerSecond <= 0 || math.IsNaN(bytesPerSecond) || math.IsInf(bytesPerSecond, 0) {
		return
	}
	s.delivery.mu.Lock()
	defer s.delivery.mu.Unlock()
	if s.delivery.samples == nil {
		s.delivery.samples = map[string]deliverySample{}
	}
	now := s.now()
	key := deliveryKey(r)
	old := s.delivery.samples[key]
	if !old.at.IsZero() && now.Sub(old.at) > 2*time.Minute {
		old.rate, old.at, old.count = 0, time.Time{}, 0
	}
	rate := bytesPerSecond
	if old.stallRate > 0 && !old.stallAt.IsZero() && now.Sub(old.stallAt) <= 2*time.Minute {
		rate = old.stallRate*.8 + rate*.2
	}
	if _, exists := s.delivery.samples[key]; !exists && len(s.delivery.samples) >= 256 {
		clear(s.delivery.samples)
		old = deliverySample{}
		rate = bytesPerSecond
	}
	old.stallRate = rate
	old.stallAt = now
	s.delivery.samples[key] = old
}

func (s *Service) deliveryFit(r core.Release, caps core.ClientCaps, title *core.Title) int {
	fit, _, _ := s.deliveryFitReason(r, caps, title)
	return fit
}

// hasRecentStall reports whether r's delivery key recorded a playback stall
// within the 2-minute delivery window. Used as a fallback when a plugin source
// lacks size or runtime metadata so deliveryFitReason cannot compute bitrate.
func (s *Service) hasRecentStall(r core.Release) bool {
	now := s.now()
	s.delivery.mu.Lock()
	e := s.delivery.samples[deliveryKey(r)]
	s.delivery.mu.Unlock()
	return e.stallRate > 0 && !e.stallAt.IsZero() && now.Sub(e.stallAt) <= 2*time.Minute
}

// deliveryFitReason returns the hard bandwidth gate (bandwidth), the soft
// tie-break signal for unverified throughput estimates (delivery), and the
// honest source-history reason when a hard limit demotes the release: the
// client's declared bitrate, the debrid account's measured speed, or stall-backed
// source read speed.
func (s *Service) deliveryFitReason(r core.Release, caps core.ClientCaps, title *core.Title) (int, int, string) {
	fit := sourceBandwidthFit(r, caps, title)
	reason := ""
	if fit < 0 {
		reason = fmt.Sprintf("larger than the client's connection carries: needs %s, above the %s the app allows (its quality setting)", mbps(releaseBitsPerSecond(r, title)), mbps(float64(caps.MaxBitrateKbps)*1000))
	}
	if r.Size <= 0 || title == nil || title.Runtime <= 0 {
		return fit, 0, reason
	}
	need := float64(r.Size) / title.Runtime.Seconds()
	if source, ok := s.indexer.(interface{ RecentSourceThroughput(core.Release) float64 }); ok {
		if rate := source.RecentSourceThroughput(r); rate > 0 {
			if need > rate*.75 {
				return -1, 0, fmt.Sprintf("needs %s, more than the debrid account recently delivered (%s)", mbps(need*8), mbps(rate*8))
			}
			if fit >= 0 {
				return 1, 0, ""
			}
		}
	}
	now := s.now()
	s.delivery.mu.Lock()
	e := s.delivery.samples[deliveryKey(r)]
	s.delivery.mu.Unlock()
	if e.stallRate > 0 && !e.stallAt.IsZero() && now.Sub(e.stallAt) <= 2*time.Minute {
		if need > e.stallRate*.75 {
			return -1, -1, sourceDeliveryReason(r, need, e.stallRate)
		}
		return fit, 0, reason
	}
	// Without stall evidence, measured source reads are a soft tie-break among
	// otherwise equal candidates, never a veto over quality, language or score.
	if e.count >= 3 && !e.at.IsZero() && now.Sub(e.at) <= 2*time.Minute && need > e.rate*.75 {
		return fit, -1, reason
	}
	return fit, 0, reason
}

func sourceDeliveryReason(r core.Release, need, rate float64) string {
	if deliveryKey(r) == "usenet" {
		return fmt.Sprintf("needs %s; recent reads from your Usenet provider ran at %s", mbps(need*8), mbps(rate*8))
	}
	return fmt.Sprintf("needs %s; recent reads from this source ran at %s", mbps(need*8), mbps(rate*8))
}

func releaseBitsPerSecond(r core.Release, title *core.Title) float64 {
	if r.Size <= 0 || title == nil || title.Runtime <= 0 {
		return 0
	}
	return float64(r.Size) * 8 / title.Runtime.Seconds()
}

func mbps(bitsPerSecond float64) string {
	return fmt.Sprintf("%.1f Mbps", bitsPerSecond/1e6)
}

// ChannelSourceAllowed applies the hard channel source envelope first.
// Measurements may reject a slow encode, never admit a heavier source.
func (s *Service) ChannelSourceAllowed(r core.Release, caps core.ClientCaps, title *core.Title) bool {
	if caps.BroadcastBitrateKbps <= 0 {
		return true
	}
	if title == nil || title.Runtime <= 0 || !releasepolicy.ChannelSourceAllowed(r, title.Runtime, caps.BroadcastBitrateKbps) {
		return false
	}
	// Pack and direct-link listings may not have an individual file size.
	// Playback rechecks this gate with the selected file before admission.
	if r.Size <= 0 {
		return true
	}
	required := float64(r.Size) / title.Runtime.Seconds()
	rate := float64(0)
	if source, ok := s.indexer.(interface{ RecentSourceThroughput(core.Release) float64 }); ok {
		rate = source.RecentSourceThroughput(r)
	}
	now := s.now()
	s.delivery.mu.Lock()
	e := s.delivery.samples[deliveryKey(r)]
	s.delivery.mu.Unlock()
	if e.stallRate > 0 && !e.stallAt.IsZero() && now.Sub(e.stallAt) <= 2*time.Minute {
		if rate <= 0 || e.stallRate < rate {
			rate = e.stallRate
		}
	}
	if e.count >= 3 && !e.at.IsZero() && now.Sub(e.at) <= 2*time.Minute {
		if rate <= 0 || e.rate < rate {
			rate = e.rate
		}
	}
	// Known slow delivery rejects even a small encode.
	if rate > 0 {
		return required <= rate*.75
	}
	return true
}
