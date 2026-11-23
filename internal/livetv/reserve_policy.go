package livetv

import (
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

// The stability preset is the owner's one Live TV reliability choice: how
// much provider stream Ether keeps in reserve, behind the live edge, so a
// provider blip plays out of the reserve while Ether reconnects or fails
// over. It is server-wide (config.LiveTV.Stability) with a per-channel
// override (Channel.Stability).
//
// Ether never delays a tune to build a reserve. An HLS backlog or a faster-
// than-real-time burst accrues immediately; when Balanced or Rock solid is
// still below its target (at tune-in on a 1x MPEG-TS stream, or after a
// stall drains the reserve), Steady plays out imperceptibly slower (~3.5%,
// with pitch-preserved WSOLA audio) until the target is reached, then
// returns to 1.0x. Fastest always plays at 1.0x.
const (
	stabilityFastest   = config.LiveTVStabilityFastest
	stabilityBalanced  = config.LiveTVStabilityBalanced
	stabilityRockSolid = config.LiveTVStabilityRockSolid
)

// stabilityReserves are the preset targets. Balanced holds enough for the
// common provider blip (a dropped connection and a reconnect, typically a
// few seconds) plus a same-source retry ladder; Rock solid covers a failover
// to a backup source that has to be resolved and probed.
var stabilityReserves = map[string]time.Duration{
	stabilityFastest:   0,
	stabilityBalanced:  12 * time.Second,
	stabilityRockSolid: 30 * time.Second,
}

// stabilityPreset resolves the channel's preset: its own override, else the
// server default, else balanced.
func (s *Service) stabilityPreset(c Channel) string {
	if v := strings.ToLower(strings.TrimSpace(c.Stability)); v != "" && config.ValidLiveTVStability(v) {
		return v
	}
	if s != nil && s.o.Stability != nil {
		if v := strings.ToLower(strings.TrimSpace(s.o.Stability())); v != "" && config.ValidLiveTVStability(v) {
			return v
		}
	}
	return stabilityBalanced
}

// stabilityReserve is the reserve the channel's preset aims for.
func (s *Service) stabilityReserve(c Channel) time.Duration {
	reserve := stabilityReserves[s.stabilityPreset(c)]
	switch s.latencyPolicy().LatencyProfile {
	case "low":
		return 0
	case "stable":
		return max(reserve, 30*time.Second)
	default:
		return reserve
	}
}
