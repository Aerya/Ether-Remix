package core

import (
	"context"
	"sync"
	"time"
)

// MediaCapWaitSource identifies sources for which admission wait is measurable.
// HTTP/add-on and Usenet sources do not claim debrid measurements.
type MediaCapWaitSource interface{ MediaCapWaitKnown() bool }

type mediaCapWaitKey struct{}

// MediaCapWait is request-scoped. It measures only the initial source read,
// because response headers precede the body; later reads cannot amend them.
type MediaCapWait struct {
	mu                       sync.Mutex
	known, reading, upstream bool
	wait                     time.Duration
}

func WithMediaCapWait(ctx context.Context, m *MediaCapWait) context.Context {
	return context.WithValue(ctx, mediaCapWaitKey{}, m)
}
func MediaCapWaitFrom(ctx context.Context) *MediaCapWait {
	m, _ := ctx.Value(mediaCapWaitKey{}).(*MediaCapWait)
	return m
}
func (m *MediaCapWait) MarkKnown() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.known = true
	m.mu.Unlock()
}
func (m *MediaCapWait) BeginRead() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reading {
		return false
	}
	m.known, m.reading = true, true
	return true
}
func (m *MediaCapWait) FirstUpstream(wait time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.upstream {
		m.upstream, m.wait = true, max(0, wait)
	}
}
func (m *MediaCapWait) Milliseconds() (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wait.Milliseconds(), m.known
}
