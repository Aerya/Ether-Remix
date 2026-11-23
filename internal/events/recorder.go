package events

import (
	"slices"
	"sync"
	"time"
)

// TestTB is the subset of testing.TB used by test helpers so importing
// internal/events in production binaries never pulls in testing's init flags.
type TestTB interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// Recorder captures events published to a Bus for assertions in tests.
type Recorder struct {
	sub    *Subscription
	mu     sync.Mutex
	events []Envelope
	notify chan struct{}
	done   chan struct{}
}

// NewTestBus constructs a Bus wired to a Recorder subscribed to all events
// (including dry-run events) and automatically cleans up when t finishes.
func NewTestBus(t TestTB, opts ...BusOptions) (*Bus, *Recorder) {
	t.Helper()
	var o BusOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.NodeID == "" {
		o.NodeID = "test-node"
	}
	if o.DefaultBuffer <= 0 {
		o.DefaultBuffer = 1024
	}
	bus := NewBus(o)
	rec := NewRecorder(bus, "*")
	t.Cleanup(func() {
		rec.Close()
		bus.Close()
	})
	return bus, rec
}

// NewRecorder attaches a capturing subscriber to bus for patterns (default "*").
func NewRecorder(bus *Bus, patterns ...string) *Recorder {
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	sub := bus.Subscribe(SubscriptionOptions{
		Name:          "test-recorder",
		Buffer:        1024,
		Patterns:      patterns,
		IncludeDryRun: true,
	})
	r := &Recorder{
		sub:    sub,
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(r.done)
		for ev := range sub.C() {
			r.mu.Lock()
			r.events = append(r.events, ev)
			r.mu.Unlock()
			select {
			case r.notify <- struct{}{}:
			default:
			}
		}
	}()
	return r
}

// Close unsubscribes and waits for the recorder goroutine to exit.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.sub.Unsubscribe()
	<-r.done
}

// Reset clears previously recorded events.
func (r *Recorder) Reset() {
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
}

// Events returns a copy of all recorded envelopes in arrival order.
func (r *Recorder) Events() []Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// OfType returns all recorded envelopes matching pattern (exact or prefix).
func (r *Recorder) OfType(pattern string) []Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Envelope
	for _, ev := range r.events {
		if MatchPattern(pattern, ev.Type) {
			out = append(out, ev)
		}
	}
	return out
}

// WaitFor blocks until an event matching pattern is recorded or timeout expires.
func (r *Recorder) WaitFor(t TestTB, pattern string, timeout time.Duration) Envelope {
	t.Helper()
	return r.WaitForFunc(t, pattern, func(ev Envelope) bool {
		return MatchPattern(pattern, ev.Type)
	}, timeout)
}

// WaitForFunc blocks until pred returns true for a recorded event or timeout expires.
func (r *Recorder) WaitForFunc(t TestTB, desc string, pred func(Envelope) bool, timeout time.Duration) Envelope {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		r.mu.Lock()
		for _, ev := range r.events {
			if pred(ev) {
				r.mu.Unlock()
				return ev
			}
		}
		r.mu.Unlock()
		select {
		case <-r.notify:
		case <-deadline.C:
			t.Fatalf("events: timed out after %s waiting for %s (recorded %d events)", timeout, desc, len(r.Events()))
			return Envelope{}
		}
	}
}

// AssertNone fails t if any event matching pattern is recorded within wait.
func (r *Recorder) AssertNone(t TestTB, pattern string, wait time.Duration) {
	t.Helper()
	if evs := r.OfType(pattern); len(evs) > 0 {
		t.Fatalf("events: expected no %q events, already recorded %d", pattern, len(evs))
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-r.notify:
			if evs := r.OfType(pattern); len(evs) > 0 {
				t.Fatalf("events: expected no %q events, got %+v", pattern, evs[0])
			}
		case <-timer.C:
			return
		}
	}
}
