package core

import (
	"context"
	"sync/atomic"
)

type searchPendingKey struct{}

// SearchPending collects, for one search request, whether a source answered
// from what it already holds while a slower lookup (a plugin's remote
// catalogue) is still running in the background. The response then tells
// the client that asking again shortly returns more.
type SearchPending struct{ pending atomic.Bool }

// WithSearchPending makes the search in ctx answer without waiting on remote
// lookups: sources start them in the background, return local results, and
// mark the returned SearchPending. A ctx that already carries one is kept.
func WithSearchPending(ctx context.Context) (context.Context, *SearchPending) {
	if p, ok := ctx.Value(searchPendingKey{}).(*SearchPending); ok {
		return ctx, p
	}
	p := &SearchPending{}
	return context.WithValue(ctx, searchPendingKey{}, p), p
}

// SearchPendingFrom returns the request's collector, or nil when the search
// may wait on remote lookups (the default).
func SearchPendingFrom(ctx context.Context) *SearchPending {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(searchPendingKey{}).(*SearchPending)
	return p
}

// Mark records that a lookup is still running.
func (p *SearchPending) Mark() {
	if p != nil {
		p.pending.Store(true)
	}
}

// Pending reports whether any lookup was still running when the search
// answered.
func (p *SearchPending) Pending() bool { return p != nil && p.pending.Load() }
