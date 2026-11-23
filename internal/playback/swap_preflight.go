package playback

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

func (m *Manager) videoTranscodingOff() bool {
	if m.config().Transcode.VideoTranscodingOff() {
		return true
	}
	if v, ok := m.deps.Transcoder.(interface{ VideoTranscodingOff() bool }); ok && v.VideoTranscodingOff() {
		return true
	}
	return false
}

func (m *Manager) videoOff(caps core.ClientCaps) bool {
	return caps.NoVideoTranscode || m.videoTranscodingOff()
}

func (m *Manager) sessionVideoOff(s *session) bool {
	return s.noVideoTranscodeActive() || m.videoTranscodingOff()
}

func (m *Manager) probedNeedsVideoTranscode(info core.MediaInfo, caps core.ClientCaps) (core.Decision, bool) {
	if info.Estimated || info.Video() == nil {
		return core.Decision{}, false
	}
	if len(caps.VideoCodecs) == 0 && len(caps.Containers) == 0 && caps.MaxHeight == 0 &&
		caps.MaxBitrateKbps == 0 && len(caps.VideoMaxHeights) == 0 &&
		caps.Product == "" && caps.Platform == "" && caps.Protocol == "" && !caps.ExplicitNoHDR {
		return core.Decision{}, false
	}
	if m.videoOff(caps) {
		caps.NoVideoTranscode = true
	}
	opts := core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}
	var dec core.Decision
	if m.deps.Transcoder != nil {
		dec = m.deps.Transcoder.Decide(info, caps, opts)
	} else {
		dec = transcode.Decide(info, caps, opts, transcode.Policy{RemoteMaxBitrateKbps: m.config().Transcode.RemoteMaxBitrateKbps})
	}
	if dec.Kind == "" || dec.Kind == core.DecisionDirectPlay {
		return dec, false
	}
	if dec.VideoCopy && dec.Kind != core.DecisionTranscode {
		return dec, false
	}
	return dec, true
}

func (m *Manager) sessionNeedsVideoTranscode(s *session, g *generation, caps core.ClientCaps) bool {
	if !m.videoOff(caps) || g == nil {
		return false
	}
	info := g.infoSnapshot()
	if _, needs := m.probedNeedsVideoTranscode(info, caps); needs {
		return true
	}
	if info.Estimated || info.BitrateKbps <= 0 {
		capKbps := caps.MaxBitrateKbps
		if capKbps <= 0 && caps.Remote {
			capKbps = m.config().Transcode.RemoteMaxBitrateKbps
		}
		if capKbps > 0 && g.res != nil {
			dur := info.Duration
			if dur <= 0 && s != nil && s.title != nil {
				dur = s.title.Runtime
			}
			if !sourceFitsBitrate(g.res.Release.Size, dur, info.BitrateKbps, capKbps) {
				return true
			}
		}
		if info.Estimated && g.res != nil && !rememberedCodecCurrent(g.res.Release, caps) {
			return true
		}
	}
	return false
}

// swapChecker is implemented by HLS sessions whose delivered format is
// fixed for their lifetime (a segmented-Matroska header the player never
// refetches). It is optional: other sessions accept any replacement.
type swapChecker interface {
	CheckSwap(info core.MediaInfo) error
}

// preflightSwap reports whether the session's running HLS stream can take
// g as its replacement input. It runs before a failover publishes g, so an
// incompatible candidate is released and the next one tried. The mismatch
// is specific to this session: the release is not marked bad.
func (m *Manager) preflightSwap(ctx context.Context, s *session, g *generation) error {
	if m.sessionVideoOff(s) {
		caps := s.caps
		caps.NoVideoTranscode = true
		if _, needs := m.probedNeedsVideoTranscode(g.infoSnapshot(), caps); needs {
			return core.ErrVideoTranscodingDisabled
		}
	}
	if m.deps.HLSLookup == nil {
		return nil
	}
	hls, ok := m.deps.HLSLookup(s.id)
	if !ok || hls == nil {
		return nil
	}
	c, ok := hls.(swapChecker)
	if !ok {
		return nil
	}
	if err := c.CheckSwap(g.infoSnapshot()); err != nil {
		if errors.Is(err, core.ErrVideoTranscodingDisabled) {
			return err
		}
		return fmt.Errorf("%w: %v", core.ErrNotAvailable, err)
	}
	if preparer, ok := hls.(interface {
		PrepareSwap(context.Context, string, core.MediaInfo, time.Duration) error
	}); ok {
		at := time.Duration(0)
		if position, ok := hls.(core.PlaybackPosition); ok {
			at = position.RequestedPosition()
		}
		if source, ok := hls.(core.RecoveryBufferSource); ok {
			b := source.RecoveryBuffer()
			if b.ReadyUntil > at && b.ReadyUntil < g.infoSnapshot().Duration {
				at = b.ReadyUntil
			}
		}
		if err := preparer.PrepareSwap(ctx, m.urlFor(g), g.infoSnapshot(), at); err != nil {
			return fmt.Errorf("%w: replacement picture preparation failed", core.ErrNotAvailable)
		}
	}
	return nil
}
