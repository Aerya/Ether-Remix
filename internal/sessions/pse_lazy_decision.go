package sessions

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// WarmDecision implements frontend.LazyStreamDecider: it rebuilds the viewer
// and caps Warm used and asks playback what the warm-up concluded.
func (s *lazyStreams) WarmDecision(ctx context.Context, credential string, req frontend.PlanRequest, budget time.Duration) frontend.WarmDecision {
	if req.Protocol != s.protocol || req.Device.ID == "" || req.Viewer == nil || req.Item == 0 || req.Channel != "" || req.Intent.Hints != nil {
		return core.WarmUnknown
	}
	decider, ok := s.broker.deps.Playback.(core.WarmDecider)
	if !ok {
		return core.WarmUnknown
	}
	_, viewer, err := s.grants.GrantReference(ctx, string(s.protocol), credential)
	if err != nil || viewer == nil || viewer.ID != req.Viewer.ID {
		return core.WarmUnknown
	}
	if viewer, err = s.broker.confirm(ctx, viewer, req.Caps); err != nil {
		return core.WarmUnknown
	}
	return decider.WaitWarmDecision(ctx, viewer, req.Item, s.broker.playCaps(viewer, req.Protocol, req.Device, req.Caps), budget)
}
