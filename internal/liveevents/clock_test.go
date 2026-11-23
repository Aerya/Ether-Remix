package liveevents

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
	armed  chan struct{}
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{
		now:   start,
		armed: make(chan struct{}, 64),
	}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	t := &fakeTimer{
		clock:    c,
		ch:       make(chan time.Time, 1),
		deadline: c.now.Add(d),
		active:   true,
	}
	if d <= 0 {
		t.active = false
		t.ch <- c.now
	} else {
		c.timers = append(c.timers, t)
	}
	c.mu.Unlock()
	c.notifyArmed()
	return t
}

func (c *fakeClock) notifyArmed() {
	select {
	case c.armed <- struct{}{}:
	default:
	}
}

func (c *fakeClock) ActiveTimerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if t.active {
			n++
		}
	}
	return n
}

func (c *fakeClock) WaitForActiveTimers(t *testing.T, want int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if c.ActiveTimerCount() >= want {
			return
		}
		select {
		case <-c.armed:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %d active timer(s); have %d", want, c.ActiveTimerCount())
		}
	}
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var fire []*fakeTimer
	for _, t := range c.timers {
		if t.active && !t.deadline.After(now) {
			t.active = false
			fire = append(fire, t)
		}
	}
	c.mu.Unlock()
	for _, t := range fire {
		select {
		case t.ch <- now:
		default:
		}
	}
}

type fakeTimer struct {
	clock    *fakeClock
	ch       chan time.Time
	deadline time.Time
	active   bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := t.active
	t.active = false
	return was
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	was := t.active
	// Drain any unread tick before re-arming.
	select {
	case <-t.ch:
	default:
	}
	if d <= 0 {
		t.active = false
		t.ch <- t.clock.now
	} else {
		t.deadline = t.clock.now.Add(d)
		t.active = true
	}
	t.clock.mu.Unlock()
	t.clock.notifyArmed()
	return was
}
