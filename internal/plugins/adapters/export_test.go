package adapters

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// FirstOpenWait returns the current first-open facet/person expansion wait.
func FirstOpenWait() time.Duration {
	return firstOpenWait
}

// SetFirstOpenWait overrides how long a first open waits for a fill and
// returns a function that restores the previous value.
func SetFirstOpenWait(d time.Duration) (restore func()) {
	prev := firstOpenWait
	firstOpenWait = d
	return func() { firstOpenWait = prev }
}

// SetExpansionErrorCooldown overrides the failed-expansion cooldown for tests
// and returns a function that restores the previous value.
func SetExpansionErrorCooldown(d time.Duration) (restore func()) {
	prev := expansionErrorCooldown
	expansionErrorCooldown = d
	return func() { expansionErrorCooldown = prev }
}

// OpenReleaseSource opens a release-pattern source as a kind's byte
// pipeline does.
func (r *KindResolver) OpenReleaseSource(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
	return r.openReleaseForSourceWithCategory(ctx, src, "", 0, nil)
}
