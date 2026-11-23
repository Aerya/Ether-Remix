package liveevents

import "time"

// Clock abstracts time so hub polling, caching, backoff, and cluster interest
// leases can be tested deterministically without wall-clock sleeps.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer is the minimal subset of *time.Timer needed by the hub.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

type realClock struct{}

func (realClock) Now() time.Time                 { return time.Now() }
func (realClock) NewTimer(d time.Duration) Timer { return &realTimer{t: time.NewTimer(d)} }

type realTimer struct {
	t *time.Timer
}

func (r *realTimer) C() <-chan time.Time        { return r.t.C }
func (r *realTimer) Stop() bool                 { return r.t.Stop() }
func (r *realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }
