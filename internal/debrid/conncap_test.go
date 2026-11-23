package debrid

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestConnCapNeverExceedsHostLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// Count the connections this process holds open, as the CDN does.
	var peak, open atomic.Int32
	d := &net.Dialer{}
	counting := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if n := open.Add(1); n > peak.Load() {
			peak.Store(n)
		}
		return &cappedConn{Conn: c, release: func() { open.Add(-1) }}, nil
	}
	limit := newConnCap(3)
	a, b := newStreamingTransport(limit), newStreamingTransport(limit)
	a.DialContext, b.DialContext = limit.wrap(counting), limit.wrap(counting)
	limit.closeIdle = func() { a.CloseIdleConnections(); b.CloseIdleConnections() }
	clients := []*http.Client{{Transport: a}, {Transport: b}}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
			resp, err := clients[i%2].Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
		}(i)
	}
	wg.Wait()
	if p := peak.Load(); p > 3 {
		t.Fatalf("peak connections to one host = %d, cap 3", p)
	}
	// Idle connections held by one pool are handed back to the other.
	a.CloseIdleConnections()
	b.CloseIdleConnections()
	deadline := time.Now().Add(2 * time.Second)
	for limit.inUse(srv.Listener.Addr().String()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := limit.inUse(srv.Listener.Addr().String()); n != 0 {
		t.Fatalf("connections still counted after close: %d", n)
	}
}

func TestConnCapDialHonorsContext(t *testing.T) {
	limit := newConnCap(1)
	if err := limit.acquire(context.Background(), "h:443"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := limit.acquire(ctx, "h:443"); err == nil {
		t.Fatal("second connection admitted past the cap")
	}
	limit.release("h:443")
	if err := limit.acquire(context.Background(), "h:443"); err != nil {
		t.Fatal(err)
	}
}

// A playback read queued behind read-ahead gets the next free host slot,
// and the gate never admits more requests than the host cap.
func TestHostGateUrgentFirst(t *testing.T) {
	g := newHostGate(newConnCap(2))
	ctx := context.Background()
	no := func() bool { return false }
	yes := func() bool { return true }
	r1, _ := g.acquire(ctx, "cdn:443", no)
	r2, _ := g.acquire(ctx, "cdn:443", no)
	if _, ok := g.tryAcquire("cdn:443"); ok {
		t.Fatal("hedge admitted over the cap")
	}
	order := make(chan string, 2)
	go func() {
		rel, _ := g.acquire(ctx, "cdn:443", no)
		order <- "readahead"
		rel()
	}()
	time.Sleep(20 * time.Millisecond) // the read-ahead queues first
	go func() {
		rel, _ := g.acquire(ctx, "cdn:443", yes)
		order <- "urgent"
		time.Sleep(10 * time.Millisecond)
		rel()
	}()
	time.Sleep(20 * time.Millisecond)
	r1()
	if got := <-order; got != "urgent" {
		t.Fatalf("first grant went to %s", got)
	}
	r2()
	if got := <-order; got != "readahead" {
		t.Fatalf("second grant went to %s", got)
	}
	// A cancelled waiter leaves no slot behind.
	cctx, cancel := context.WithCancel(ctx)
	hold1, _ := g.acquire(ctx, "cdn:443", no)
	hold2, _ := g.acquire(ctx, "cdn:443", no)
	done := make(chan error, 1)
	go func() { _, err := g.acquire(cctx, "cdn:443", yes); done <- err }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled waiter was admitted")
	}
	hold1()
	hold2()
	g.mu.Lock()
	busy, waiting := g.busy["cdn:443"], len(g.waiters["cdn:443"])
	g.mu.Unlock()
	if busy != 0 || waiting != 0 {
		t.Fatalf("leaked busy=%d waiting=%d", busy, waiting)
	}
}

// Foreground dials bypass queued background work, including when a detached
// warm-up is promoted while waiting. Background work retains FIFO order and
// proceeds once the foreground queue drains.
func TestConnCapPriorityOrdering(t *testing.T) {
	c := newConnCap(1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	const addr = "cdn:443"
	if err := c.acquire(ctx, addr); err != nil {
		t.Fatal(err)
	}
	type grant struct {
		name string
		err  error
	}
	grants := make(chan grant, 4)
	enqueue := func(name string, request context.Context, count int) {
		t.Helper()
		go func() { grants <- grant{name, c.acquire(request, addr)} }()
		for {
			c.mu.Lock()
			n := len(c.waiters[addr])
			c.mu.Unlock()
			if n == count {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("dial did not queue")
			case <-time.After(time.Millisecond):
			}
		}
	}
	background := context.WithValue(ctx, fetchModeKey{}, fetchPrefetch)
	enqueue("background-1", background, 1)
	enqueue("background-2", background, 2)
	warm, promote := core.WithSpeculative(ctx)
	// This is how block workers retain admission without inheriting a
	// caller's cancellation. Keep this test bounded by ctx independently.
	warm = inheritReadPriority(ctx, warm)
	warm = context.WithValue(warm, fetchModeKey{}, fetchDurable)
	if gateUrgent(warm) {
		t.Fatal("warm-up lost background priority when detached")
	}
	enqueue("promoted", warm, 3)
	demand := new(atomic.Bool)
	joined := context.WithValue(background, playheadDemandKey{}, demand)
	enqueue("foreground", joined, 4)
	// A viewer now needs the queued read-ahead at the playhead.
	demand.Store(true)
	promote()
	for _, want := range []string{"promoted", "foreground", "background-1", "background-2"} {
		c.release(addr)
		select {
		case got := <-grants:
			if got.err != nil || got.name != want {
				t.Fatalf("grant = %s, %v; want %s", got.name, got.err, want)
			}
		case <-ctx.Done():
			t.Fatal("queued dial did not proceed")
		}
	}
	c.release(addr)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.waiters) != 0 || len(c.open) != 0 {
		t.Fatalf("admission leaked: waiters=%v open=%v", c.waiters, c.open)
	}
}
