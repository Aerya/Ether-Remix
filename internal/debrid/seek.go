package debrid

import (
	"context"
	"sync"
)

type seekRunsKey struct{}

// seekRuns closes registration before cancellation, so a seek can hand over
// all of its old epoch's slots before submitting the new playhead request.
// Concurrent foreground ranges use their request context, not this epoch.
type seekRuns struct {
	mu      sync.Mutex
	retired bool
	wg      sync.WaitGroup
}

func newSeekEpoch(parent context.Context) (context.Context, context.CancelFunc) {
	runs := new(seekRuns)
	ctx, cancel := context.WithCancel(context.WithValue(parent, seekRunsKey{}, runs))
	return ctx, func() {
		runs.mu.Lock()
		runs.retired = true
		cancel()
		runs.mu.Unlock()
	}
}

func (r *seekRuns) register() bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.retired {
		return false
	}
	r.wg.Add(1)
	return true
}

func (r *seekRuns) finish() {
	if r != nil {
		r.wg.Done()
	}
}

func (r *seekRuns) wait() {
	if r != nil {
		r.wg.Wait()
	}
}
