package sessions

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/transcode"
)

func (b *Broker) ProactiveFallbackAvailable() bool {
	provider, ok := b.deps.Transcoder.(interface{ ProactiveFallbackAvailable() bool })
	return ok && provider.ProactiveFallbackAvailable()
}

func (b *Broker) qualityPlan(ctx context.Context, s *brokerSession, viewer *core.Viewer) ([]core.QualityVariant, []core.QualityLimitation, error) {
	deny := func(reason string) ([]core.QualityVariant, []core.QualityLimitation, error) {
		return nil, []core.QualityLimitation{{Reason: reason}}, nil
	}
	if s.public.Protocol != "vela" || s.live || s.restored {
		return nil, nil, nil
	}
	if !b.ProactiveFallbackAvailable() {
		return deny("quality_service_unavailable")
	}
	if !b.currentMediaPolicy(ctx, viewer, s) {
		return deny("owner_policy")
	}
	if s.allowTranscoding != nil && !*s.allowTranscoding {
		return deny("transcoding_disabled")
	}
	ps, ok := b.deps.Playback.Session(s.playID)
	if !ok || ps == nil {
		return nil, nil, core.ErrNotAvailable
	}
	if s.sourcePinned && sourceRevision(ps) != s.sourceETag {
		return nil, nil, frontend.ErrSourceGone
	}
	dec := s.decision
	if dec.Reason == "original_runtime" {
		options := s.opts
		options.DirectPlay = false
		var err error
		dec, err = b.decideProfile(ps, frontend.PlanRequest{Viewer: viewer, Device: s.public.Device, Protocol: s.public.Protocol, Caps: s.caps, Intent: frontend.PlayIntent{Options: options, AllowTranscoding: s.allowTranscoding}})
		if err != nil {
			return deny("output_profile_unavailable")
		}
	}
	dec, allowed := b.hlsOutputDecision(ps, s.caps, s.opts, s.allowTranscoding, dec)
	if !allowed || dec.Container == "mkv" {
		return deny("output_profile_unavailable")
	}
	width, height, kbps := s.opts.MaxWidth, s.opts.MaxHeight, s.opts.MaxBitrateKbps
	if s.caps.MaxHeight > 0 && (height == 0 || s.caps.MaxHeight < height) {
		height = s.caps.MaxHeight
	}
	if s.caps.MaxBitrateKbps > 0 && (kbps == 0 || s.caps.MaxBitrateKbps < kbps) {
		kbps = s.caps.MaxBitrateKbps
	}
	if b.deps.BitrateLimit != nil {
		if limit := b.deps.BitrateLimit(viewer); limit > 0 && (kbps == 0 || limit < kbps) {
			kbps = limit
		}
	}
	if s.caps.Remote && b.deps.RemoteBitrateLimit != nil {
		if limit := b.deps.RemoteBitrateLimit(); limit > 0 && (kbps == 0 || limit < kbps) {
			kbps = limit
		}
	}
	support := transcode.QualitySupport{}
	if provider, ok := b.deps.Transcoder.(interface {
		QualitySupport() transcode.QualitySupport
	}); ok {
		support = provider.QualitySupport()
	}
	variants, limits := transcode.VelaQualityVariants(ps.Info, dec, s.caps, width, height, kbps, support)
	out := variants[:0]
	for _, variant := range variants {
		d, ok := constrainedH264(variant.Decision, ps.Info, s.caps, b.h264EncoderVerified())
		if !ok || !profileDeliveryAllowed(s.caps.Profiles, d, ps.Info, "hls") {
			limits = append(limits, core.QualityLimitation{Height: variant.Rung.Height, Reason: "decoder_profile_limit"})
			continue
		}
		variant.Decision = d
		out = append(out, variant)
	}
	return out, limits, nil
}

func (b *Broker) qualityRungs(ctx context.Context, s *brokerSession, viewer *core.Viewer) ([]core.HLSRung, error) {
	variants, _, err := b.qualityPlan(ctx, s, viewer)
	out := make([]core.HLSRung, 0, len(variants))
	for _, variant := range variants {
		out = append(out, variant.Rung)
	}
	return out, err
}

// QualityLimitations reports stable reason codes without starting media work.
func (b *Broker) QualityLimitations(ctx context.Context, a frontend.SessionAccess, id string) ([]core.QualityLimitation, error) {
	s, v, err := b.access(ctx, a, id)
	if err != nil {
		return nil, err
	}
	_, limits, err := b.qualityPlan(ctx, s, v)
	return limits, err
}

func (b *Broker) QualityRungs(ctx context.Context, a frontend.SessionAccess, id string) ([]core.HLSRung, error) {
	s, v, err := b.access(ctx, a, id)
	if err != nil {
		return nil, err
	}
	return b.qualityRungs(ctx, s, v)
}

func (b *Broker) PrepareQuality(ctx context.Context, a frontend.SessionAccess, id, rung string, position time.Duration) (string, time.Duration, error) {
	rungs, err := b.QualityRungs(ctx, a, id)
	if err != nil {
		return "", 0, err
	}
	allowed := false
	for _, r := range rungs {
		if r.ID == rung {
			allowed = true
		}
	}
	if !allowed {
		return "", 0, core.ErrNotAvailable
	}
	s, _, err := b.access(ctx, a, id)
	if err != nil {
		return "", 0, err
	}
	origin := position
	if hs, ok := b.transcodeSession(id); ok && hs != nil {
		origin = s.streamPosition
	}
	url, err := b.StartStream(ctx, a, id, frontend.StreamRequest{Format: frontend.StreamHLS, Position: origin, LazyStart: true, Continuous: true, PrepareOnly: true})
	if err != nil {
		return "", 0, err
	}
	hs, ok := b.transcodeSession(id)
	if !ok {
		return "", 0, core.ErrNotAvailable
	}
	preparer, ok := hs.(interface {
		PrepareRung(context.Context, string, time.Duration) error
	})
	if !ok {
		return "", 0, core.ErrNotAvailable
	}
	if err = preparer.PrepareRung(ctx, rung, position); err != nil {
		return "", 0, err
	}
	if clock, ok := hs.(interface{ TimelineOrigin() time.Duration }); ok {
		origin = clock.TimelineOrigin()
	}
	return url, origin, nil
}
