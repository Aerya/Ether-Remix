package config

import "time"

// LiveLatencyPolicy is an owner preference, not a hard playback deadline.
// Clients may adapt to network conditions within this preference. Zero/unknown
// profiles resolve to Automatic so older configuration documents remain valid.
type LiveLatencyPolicy struct {
	LatencyProfile       string  `json:"latencyProfile"`
	TargetLatencySeconds float64 `json:"targetLatencySeconds"`
}

func ValidLiveLatencyProfile(v string) bool {
	return v == "" || v == "auto" || v == "low" || v == "stable"
}

func (c LiveTV) LatencyPolicy() LiveLatencyPolicy {
	switch c.LatencyProfile {
	case "low":
		return LiveLatencyPolicy{"low", 3}
	case "stable":
		return LiveLatencyPolicy{"stable", 12}
	default:
		return LiveLatencyPolicy{"auto", 6}
	}
}

func (p LiveLatencyPolicy) TargetLatency() time.Duration {
	return time.Duration(p.TargetLatencySeconds * float64(time.Second))
}
func (p LiveLatencyPolicy) SegmentDuration() time.Duration { return p.TargetLatency() / 3 }
func (p LiveLatencyPolicy) PlaylistWindow() time.Duration  { return 4 * p.TargetLatency() }
