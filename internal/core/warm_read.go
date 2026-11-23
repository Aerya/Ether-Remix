package core

import (
	"context"
	"sync"
)

// ReadOrigin attributes source I/O without logging URLs or credentials.
type ReadOrigin struct{ Component, Reason string }
type readOriginKey struct{}

func WithReadOrigin(ctx context.Context, component, reason string) context.Context {
	return context.WithValue(ctx, readOriginKey{}, ReadOrigin{component, reason})
}
func SourceReadOrigin(ctx context.Context) ReadOrigin {
	origin, _ := ctx.Value(readOriginKey{}).(ReadOrigin)
	return origin
}

// WarmReadWindow retires startup speculation behind the actual media
// Range. It belongs to one source generation, never to a shared release.
type WarmReadWindow struct {
	mu       sync.Mutex
	observed bool
	floor    int64
	runs     map[*warmReadRun]struct{}
}
type warmReadRun struct {
	end    int64
	cancel context.CancelFunc
}
type warmReadKey struct{}

func WithWarmReadWindow(ctx context.Context, w *WarmReadWindow) context.Context {
	return context.WithValue(ctx, warmReadKey{}, w)
}

// Observe records a media Range, excluding header/tail probes at the caller.
// A later backward seek lowers the floor so its new warming can proceed.
func (w *WarmReadWindow) Observe(off int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.observed, w.floor = true, off
	for run := range w.runs {
		if run.end <= off {
			run.cancel()
		}
	}
}

// WarmReadContext gives each background GET its own cancellation, leaving
// ranges at or beyond the playhead running. Future stale runs are refused too.
func WarmReadContext(ctx context.Context, end int64) (context.Context, context.CancelFunc) {
	w, _ := ctx.Value(warmReadKey{}).(*WarmReadWindow)
	if w == nil {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	run := &warmReadRun{end: end, cancel: cancel}
	w.mu.Lock()
	if w.observed && end <= w.floor {
		cancel()
	} else {
		if w.runs == nil {
			w.runs = make(map[*warmReadRun]struct{})
		}
		w.runs[run] = struct{}{}
	}
	w.mu.Unlock()
	return ctx, func() { cancel(); w.mu.Lock(); delete(w.runs, run); w.mu.Unlock() }
}

func HasWarmReadWindow(ctx context.Context) bool {
	w, _ := ctx.Value(warmReadKey{}).(*WarmReadWindow)
	return w != nil
}
