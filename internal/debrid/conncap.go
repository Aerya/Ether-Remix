package debrid

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Keep a conservative shared ceiling across primary and hedge pools. TorBox
// recommends at most four connections, while other providers may impose
// host-specific limits. No operator tuning is needed.
const defaultHostConnections = 4

const resetCooldown = 3 * time.Minute

// connCap counts open connections per dial address and makes a dial wait,
// after asking the transports to drop idle connections, rather than exceed
// the limit.
type connCap struct {
	limit     int
	closeIdle func()
	log       *slog.Logger

	now func() time.Time

	mu       sync.Mutex
	open     map[string]int
	nextDial map[string]time.Time
	dialGap  time.Duration
	reduced  map[string]time.Time // addr -> end of its reduced-cap cooldown
	freed    chan struct{}        // closed and replaced whenever admission changes
	waiters  map[string][]*dialWaiter
}

func newConnCap(limit int) *connCap {
	return &connCap{limit: limit, now: time.Now, open: map[string]int{}, nextDial: map[string]time.Time{}, reduced: map[string]time.Time{}, freed: make(chan struct{}), waiters: map[string][]*dialWaiter{}}
}

// limitLocked is addr's effective cap and, while it is reduced, when the
// reduction ends.
func (c *connCap) limitLocked(addr string) (int, time.Time) {
	until, ok := c.reduced[addr]
	if !ok {
		return c.limit, time.Time{}
	}
	if !c.now().Before(until) {
		delete(c.reduced, addr)
		if c.log != nil {
			c.log.Info("media host connection cap restored", "host", addr, "limit", c.limit)
		}
		return c.limit, time.Time{}
	}
	return 1, until
}

// reduce lowers addr's cap for resetCooldown; further resets extend it.
// Open connections are left alone; new dials wait under the lower cap.
func (c *connCap) reduce(addr string) {
	if addr == "" {
		return
	}
	c.mu.Lock()
	_, already := c.reduced[addr]
	c.reduced[addr] = c.now().Add(resetCooldown)
	lim, _ := c.limitLocked(addr)
	c.mu.Unlock()
	if !already && c.log != nil {
		c.log.Warn("media host connection cap reduced", "host", addr, "limit", lim, "cooldown", resetCooldown)
	}
}

type capKeyCtx struct{}

// withCapKey makes dials made for this request count against key instead of
// the dial address. Behind a proxy the dial address is the proxy, which every
// media host shares; the cap must follow the real host.
func withCapKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, capKeyCtx{}, key)
}

func capKey(ctx context.Context) (string, bool) {
	k, ok := ctx.Value(capKeyCtx{}).(string)
	return k, ok && k != ""
}

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func (c *connCap) wrap(next dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		key := addr
		if k, ok := capKey(ctx); ok {
			key = k
		}
		if err := c.acquire(ctx, key); err != nil {
			return nil, err
		}
		conn, err := next(ctx, network, addr)
		if err != nil {
			c.release(key)
			return nil, err
		}
		return &cappedConn{Conn: conn, release: func() { c.release(key) }}, nil
	}
}

// hedgeDialKey keeps speculative duplicate dials on the paced path even when
// they inherit a foreground request's priority.
type hedgeDialKey struct{}

// readAheadDialKey carries a live, bounded buffer check only to dial admission.
// It does not promote speculative requests to hedges or change host caps.
type readAheadDialKey struct{}

func dialUrgent(ctx context.Context) bool {
	if gateUrgent(ctx) {
		return true
	}
	refill, _ := ctx.Value(readAheadDialKey{}).(func() bool)
	return refill != nil && refill()
}

type dialWaiter struct {
	urgent  func() bool
	hedge   bool
	granted bool
}

// Equal-priority dials with the same spacing policy keep FIFO order. Background
// dials resume after the foreground queue drains; open connections stay put.
func (c *connCap) acquire(ctx context.Context, addr string) error {
	w := &dialWaiter{urgent: func() bool { return dialUrgent(ctx) }, hedge: ctx.Value(hedgeDialKey{}) != nil}
	c.mu.Lock()
	c.waiters[addr] = append(c.waiters[addr], w)
	c.signalLocked()
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		q := c.waiters[addr]
		for i, other := range q {
			if other == w {
				q = append(q[:i], q[i+1:]...)
				break
			}
		}
		if len(q) == 0 {
			delete(c.waiters, addr)
		} else {
			c.waiters[addr] = q
		}
		c.signalLocked()
		c.mu.Unlock()
	}()
	asked := false
	began := time.Now()
	waited := false
	defer func() {
		if waited {
			recordCapWait(ctx, began)
		}
	}()
	lim := c.limit
	defer func() {
		if wait := time.Since(began); wait > 250*time.Millisecond && c.log != nil {
			c.log.Info("media connection waited for the host cap", "host", addr, "wait", wait, "limit", lim)
		}
	}()
	for {
		c.mu.Lock()
		if w.granted {
			c.mu.Unlock()
			return nil
		}
		if err := ctx.Err(); err != nil {
			c.mu.Unlock()
			return err
		}
		var until time.Time
		lim, until = c.limitLocked(addr)
		// Any awake waiter can grant the selected dial. Priority may change
		// while that dial sleeps; progress cannot depend on it waking itself.
		q := c.waiters[addr]
		if c.open[addr] < lim {
			pick := 0
			for i, candidate := range q {
				if candidate.urgent() {
					pick = i
					break
				}
			}
			// A paced hedge must not hold an exempt playhead dial behind it.
			if until.IsZero() && c.now().Before(c.nextDial[addr]) && q[pick].hedge {
				for i, candidate := range q {
					if !candidate.hedge && candidate.urgent() {
						pick = i
						break
					}
				}
			}
			chosen := q[pick]
			// Spacing was introduced with the conservative CDN cap and reset/
			// refusal recovery. Keep it for speculation and troubled hosts, but
			// let playhead reads refill immediately after open, seek or early
			// close: serial handshakes can otherwise exhaust the audio reserve.
			// Every dial still owns a slot under the effective host cap, and
			// even an exempt dial pushes the next background dial back one gap.
			foreground := chosen.urgent() && !chosen.hedge && until.IsZero()
			if foreground || !c.now().Before(c.nextDial[addr]) {
				c.waiters[addr] = append(q[:pick:pick], q[pick+1:]...)
				chosen.granted = true
				c.open[addr]++
				if c.dialGap > 0 {
					c.nextDial[addr] = c.now().Add(c.dialGap)
				}
				c.signalLocked()
				if chosen == w {
					c.mu.Unlock()
					return nil
				}
			}
		}
		// A spare slot gets a timer even when another waiter is currently
		// selected. When it expires we can grant a newly promoted sleeper.
		if c.open[addr] < lim {
			until = c.nextDial[addr]
		}
		waited = true
		freed := c.freed
		c.mu.Unlock()
		if !asked && c.closeIdle != nil {
			// Idle keep-alives are the cheapest connections to give back.
			asked = true
			go c.closeIdle()
		}
		var restored <-chan time.Time
		var t *time.Timer
		if !until.IsZero() {
			// Wake for dial spacing or cap restoration, even if nothing closes.
			t = time.NewTimer(until.Sub(c.now()))
			restored = t.C
		}
		select {
		case <-freed:
		case <-restored:
		case <-ctx.Done():
			if t != nil {
				t.Stop()
			}
			continue // recheck a grant racing with cancellation under c.mu
		}
		if t != nil {
			t.Stop()
		}
	}
}

func (c *connCap) release(addr string) {
	c.mu.Lock()
	if c.open[addr] <= 1 {
		delete(c.open, addr)
	} else {
		c.open[addr]--
	}
	c.signalLocked()
	c.mu.Unlock()
}

// wake re-evaluates queued dials after playhead progress or demand promotion.
func (c *connCap) wake() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.signalLocked()
	c.mu.Unlock()
}

func (c *connCap) signalLocked() {
	close(c.freed)
	c.freed = make(chan struct{})
}

// Reserve headroom for real reads, including connections in both pools.
func (c *connCap) hedgeAllowed(addr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	limit, _ := c.limitLocked(addr)
	return limit > 1 && c.open[addr] < limit-1
}

type cappedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *cappedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// current is addr's effective cap and, while it is reduced, when the
// reduction ends.
func (c *connCap) current(addr string) (int, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limitLocked(addr)
}

// hostGate admits media requests to a host up to its connection cap, held
// until the response body is closed. The dial cap alone cannot order
// waiters: the transport hands a connection that turns idle to whichever
// request asked first, so a queued read-ahead could take it ahead of the
// playback read that is stalling a viewer. The gate decides instead: a
// waiting urgent read goes before every queued read-ahead and prefetch,
// and requests of equal priority keep their order.
type hostGate struct {
	limit func(addr string) (int, time.Time)
	now   func() time.Time
	log   *slog.Logger

	mu         sync.Mutex
	busy       map[string]int
	waiters    map[string][]*gateWaiter
	foreground map[string]int
	drains     map[string]map[*drainLease]struct{}
	active     map[string]map[*gateWaiter]struct{}
}

type gateWaiter struct {
	urgent  func() bool // evaluated at grant time: a warm-up can turn into Play
	ready   chan struct{}
	granted bool
	preempt context.CancelFunc
}

func newHostGate(c *connCap) *hostGate {
	return &hostGate{limit: c.current, now: func() time.Time { return c.now() }, busy: map[string]int{}, waiters: map[string][]*gateWaiter{}}
}

// A drain owns its connection normally, but may keep it only while no
// foreground request is entering admission. Cancellation aborts the HTTP
// body; neither the request gate nor the physical connection cap is bypassed.
type drainLease struct{ cancel context.CancelFunc }

func (g *hostGate) beginForeground(addr string) func() {
	if g == nil {
		return func() {}
	}
	g.mu.Lock()
	if g.foreground == nil {
		g.foreground = make(map[string]int)
	}
	g.foreground[addr]++
	var cancel []context.CancelFunc
	for d := range g.drains[addr] {
		cancel = append(cancel, d.cancel)
	}
	g.mu.Unlock()
	for _, stop := range cancel {
		stop()
	}
	return func() {
		g.mu.Lock()
		g.foreground[addr]--
		if g.foreground[addr] == 0 {
			delete(g.foreground, addr)
		}
		g.mu.Unlock()
	}
}

func (g *hostGate) tryDrain(addr string, cancel context.CancelFunc) (func(), bool) {
	if g == nil {
		return nil, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.foreground[addr] > 0 {
		return nil, false
	}
	if g.drains == nil {
		g.drains = make(map[string]map[*drainLease]struct{})
	}
	if g.drains[addr] == nil {
		g.drains[addr] = make(map[*drainLease]struct{})
	}
	d := &drainLease{cancel: cancel}
	g.drains[addr][d] = struct{}{}
	return func() {
		g.mu.Lock()
		delete(g.drains[addr], d)
		if len(g.drains[addr]) == 0 {
			delete(g.drains, addr)
		}
		g.mu.Unlock()
	}, true
}

// acquire waits for a request slot to addr. urgent reports whether a viewer
// is waiting on this request; it is consulted again whenever a slot frees.
func (g *hostGate) acquire(ctx context.Context, addr string, urgent func() bool, preempt ...context.CancelFunc) (func(), error) {
	if g == nil || addr == "" {
		return func() {}, nil
	}
	began := time.Now()
	w := &gateWaiter{urgent: urgent, ready: make(chan struct{})}
	if len(preempt) > 0 {
		w.preempt = preempt[0]
	}
	g.mu.Lock()
	lim, until := g.limit(addr)
	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		return nil, err
	}
	if g.busy[addr] < lim && len(g.waiters[addr]) == 0 {
		g.busy[addr]++
		g.trackLocked(addr, w)
		g.mu.Unlock()
		return g.trackedReleaser(addr, w), nil
	}
	g.waiters[addr] = append(g.waiters[addr], w)
	g.dispatchLocked(addr)
	if !w.granted {
		defer recordCapWait(ctx, began)
	}
	// An admitted warm body must yield too; queue priority alone cannot free
	// a host whose slots are all occupied by background work.
	if !w.granted && urgent() {
		for active := range g.active[addr] {
			if active.preempt != nil && !active.urgent() {
				active.preempt()
				active.preempt = nil
				break
			}
		}
	}
	g.mu.Unlock()
	defer func() {
		if wait := time.Since(began); wait > 250*time.Millisecond && g.log != nil {
			g.log.Info("media request waited for the host cap", "host", addr, "wait", wait, "limit", lim, "urgent", urgent())
		}
	}()
	for {
		var restored <-chan time.Time
		var t *time.Timer
		if !until.IsZero() {
			// A reduced cap rises when its cooldown ends, even if nothing
			// releases a slot.
			t = time.NewTimer(max(0, until.Sub(g.now())))
			restored = t.C
		}
		select {
		case <-w.ready:
			if t != nil {
				t.Stop()
			}
			return g.trackedReleaser(addr, w), nil
		case <-restored:
			g.mu.Lock()
			g.dispatchLocked(addr)
			lim, until = g.limit(addr)
			g.mu.Unlock()
		case <-ctx.Done():
			if t != nil {
				t.Stop()
			}
			g.mu.Lock()
			if w.granted {
				delete(g.active[addr], w)
				if len(g.active[addr]) == 0 {
					delete(g.active, addr)
				}
				g.mu.Unlock()
				g.release(addr)
			} else {
				q := g.waiters[addr]
				for i, o := range q {
					if o == w {
						g.waiters[addr] = append(q[:i:i], q[i+1:]...)
						break
					}
				}
				if len(g.waiters[addr]) == 0 {
					delete(g.waiters, addr)
				}
				g.mu.Unlock()
			}
			return nil, ctx.Err()
		}
	}
}

// tryAcquire takes a spare slot without queueing (speculative hedges).
func (g *hostGate) tryAcquire(addr string) (func(), bool) {
	if g == nil || addr == "" {
		return func() {}, true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	lim, _ := g.limit(addr)
	if g.busy[addr] >= lim || len(g.waiters[addr]) > 0 {
		return nil, false
	}
	g.busy[addr]++
	return g.releaser(addr), true
}

func (g *hostGate) releaser(addr string) func() {
	var once sync.Once
	return func() { once.Do(func() { g.release(addr) }) }
}

func (g *hostGate) release(addr string) {
	g.mu.Lock()
	if g.busy[addr] <= 1 {
		delete(g.busy, addr)
	} else {
		g.busy[addr]--
	}
	g.dispatchLocked(addr)
	g.mu.Unlock()
}

// dispatchLocked grants free slots: the oldest urgent waiter first, else
// the oldest waiter.
func (g *hostGate) dispatchLocked(addr string) {
	lim, _ := g.limit(addr)
	for g.busy[addr] < lim {
		q := g.waiters[addr]
		if len(q) == 0 {
			break
		}
		pick := 0
		for i, w := range q {
			if w.urgent() {
				pick = i
				break
			}
		}
		w := q[pick]
		q = append(q[:pick:pick], q[pick+1:]...)
		if len(q) == 0 {
			delete(g.waiters, addr)
		} else {
			g.waiters[addr] = q
		}
		g.busy[addr]++
		g.trackLocked(addr, w)
		w.granted = true
		close(w.ready)
	}
}

func (g *hostGate) trackLocked(addr string, w *gateWaiter) {
	if w.preempt == nil {
		return
	}
	if g.active == nil {
		g.active = make(map[string]map[*gateWaiter]struct{})
	}
	if g.active[addr] == nil {
		g.active[addr] = make(map[*gateWaiter]struct{})
	}
	g.active[addr][w] = struct{}{}
}
func (g *hostGate) trackedReleaser(addr string, w *gateWaiter) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.active[addr], w)
			if len(g.active[addr]) == 0 {
				delete(g.active, addr)
			}
			g.mu.Unlock()
			g.release(addr)
		})
	}
}
