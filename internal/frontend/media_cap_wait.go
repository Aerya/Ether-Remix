package frontend

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// MediaCapWait records initial upstream admission for a media response.
type MediaCapWait = core.MediaCapWait

func WithMediaCapWait(ctx context.Context, measurement *MediaCapWait) context.Context {
	return core.WithMediaCapWait(ctx, measurement)
}
