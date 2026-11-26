package frontend

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// WarmDecision is what the warm-up of a stream says about its sources.
type WarmDecision = core.WarmDecision

const (
	WarmUnknown   = core.WarmUnknown
	WarmHasSource = core.WarmHasSource
	WarmNoSource  = core.WarmNoSource
)

// LazyStreamDecider is optionally implemented by LazyStreams. After Warm, it
// waits at most budget for the verdict of the resolver for the same request.
// Anything but WarmNoSource means "keep offering the stream".
type LazyStreamDecider interface {
	WarmDecision(ctx context.Context, credential string, req PlanRequest, budget time.Duration) WarmDecision
}
