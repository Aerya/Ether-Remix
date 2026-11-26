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

// WarmDecider is optionally implemented by playback managers that remember
// the verdict of a warm-up. WaitWarmDecision waits at most budget for the
// warm-up of this user, client and title; it never starts or cancels one.
type WarmDecider interface {
	WaitWarmDecision(ctx context.Context, user *User, key RatingKey, caps ClientCaps, budget time.Duration) WarmDecision
}
