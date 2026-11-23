package liveevents

import (
	"context"
	"sync"
)

// singleflight deduplicates concurrent calls with the same key while allowing
// each waiting caller to cancel independently on its own context.
type singleflight[T any] struct {
	mu    sync.Mutex
	calls map[string]*sfCall[T]
}

type sfCall[T any] struct {
	done chan struct{}
	val  T
	err  error
}

// Do executes fn at most once concurrently for key. fn receives runCtx (not the
// individual waiter's ctx) so one cancelled waiter cannot abort another waiter's
// shared fetch.
func (g *singleflight[T]) Do(waitCtx context.Context, runCtx context.Context, key string, fn func(context.Context) (T, error)) (T, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*sfCall[T])
	}
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		select {
		case <-c.done:
			return c.val, c.err
		case <-waitCtx.Done():
			var zero T
			return zero, waitCtx.Err()
		}
	}
	c := &sfCall[T]{done: make(chan struct{})}
	g.calls[key] = c
	g.mu.Unlock()

	go func() {
		defer func() {
			g.mu.Lock()
			delete(g.calls, key)
			g.mu.Unlock()
			close(c.done)
		}()
		c.val, c.err = fn(runCtx)
	}()

	select {
	case <-c.done:
		return c.val, c.err
	case <-waitCtx.Done():
		var zero T
		return zero, waitCtx.Err()
	}
}
