package core

import (
	"context"
	"time"
)

// WarmDecision is what a title's warm-up says about its sources.
type WarmDecision int

const (
	// WarmUnknown: no verdict within the budget, or none recorded.
	WarmUnknown WarmDecision = iota
	// WarmHasSource: the warm-up found a release to play.
	WarmHasSource
	// WarmNoSource: the resolver answered that no release exists.
	WarmNoSource
)

type noQueueKey struct{}

// WithoutDownloadQueue marks ctx as a resolve that only looks for a release:
// it must not hand an uncached torrent to a debrid service.
func WithoutDownloadQueue(ctx context.Context) context.Context {
	return context.WithValue(ctx, noQueueKey{}, true)
}

// NoDownloadQueue reports whether ctx came from WithoutDownloadQueue.
func NoDownloadQueue(ctx context.Context) bool {
	v, _ := ctx.Value(noQueueKey{}).(bool)
	return v
}

// WarmDecider is optionally implemented by playback managers that remember
// the verdict of a warm-up. WaitWarmDecision waits at most budget for the
// warm-up of this user, client and title; it never starts or cancels one.
type WarmDecider interface {
	WaitWarmDecision(ctx context.Context, user *User, key RatingKey, caps ClientCaps, budget time.Duration) WarmDecision
}
