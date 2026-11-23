package debrid

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/lru"
)

// A foreground refill can use every freed slot without paying a staircase
// of dial gaps. Speculation and hosts recovering from failures stay paced.
func TestConnCapForegroundRefill(t *testing.T) {
	for _, event := range []string{"open", "seek", "early close"} {
		t.Run(event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const addr = "cdn:443"
				const gap = 250 * time.Millisecond
				c := newConnCap(defaultHostConnections)
				c.dialGap = gap
				background := context.WithValue(t.Context(), fetchModeKey{}, fetchReadAhead)
				// Leave spacing debt even on open; subsequent lifecycle events
				// release a full set of old connections at the same instant.
				n := 1
				if event != "open" {
					n = defaultHostConnections
				}
				for i := 0; i < n; i++ {
					if err := c.acquire(t.Context(), addr); err != nil {
						t.Fatal(err)
					}
				}
				for i := 0; i < n; i++ {
					c.release(addr)
				}
				began := time.Now()
				// Background queues before foreground and must not hold up
				// the burst, even though it owns the oldest spacing waiter.
				bgDone := make(chan error, 1)
				go func() { bgDone <- c.acquire(background, addr) }()
				synctest.Wait()
				for i := 0; i < defaultHostConnections; i++ {
					if err := c.acquire(t.Context(), addr); err != nil {
						t.Fatal(err)
					}
				}
				if elapsed := time.Since(began); elapsed != 0 {
					t.Fatalf("foreground refill spent %v in spacing", elapsed)
				}
				if got := c.inUse(addr); got != defaultHostConnections {
					t.Fatalf("refill connections = %d, want %d", got, defaultHostConnections)
				}
				// A fifth foreground dial still waits for the physical cap.
				ctx, cancel := context.WithCancel(t.Context())
				extra := make(chan error, 1)
				go func() { extra <- c.acquire(ctx, addr) }()
				synctest.Wait()
				select {
				case err := <-extra:
					t.Fatalf("extra dial escaped cap: %v", err)
				default:
				}
				cancel()
				if err := <-extra; err == nil {
					t.Fatal("cancelled extra dial admitted")
				}
				for i := 0; i < defaultHostConnections; i++ {
					c.release(addr)
				}
				if err := <-bgDone; err != nil {
					t.Fatal(err)
				}
				if elapsed := time.Since(began); elapsed != gap {
					t.Fatalf("background after burst waited %v, want %v", elapsed, gap)
				}
				c.release(addr)
				for i := 0; i < defaultHostConnections; i++ {
					before := time.Now()
					if err := c.acquire(background, addr); err != nil {
						t.Fatal(err)
					}
					if elapsed := time.Since(before); elapsed != gap {
						t.Fatalf("background dial spacing = %v, want %v", elapsed, gap)
					}
					c.release(addr)
				}
				// Hedge duplicates retain spacing despite foreground priority.
				before := time.Now()
				if err := c.acquire(context.WithValue(t.Context(), hedgeDialKey{}, true), addr); err != nil {
					t.Fatal(err)
				}
				if time.Since(before) != gap {
					t.Fatal("hedge skipped spacing")
				}
				c.release(addr)
				c.reduce(addr)
				before = time.Now()
				if err := c.acquire(t.Context(), addr); err != nil {
					t.Fatal(err)
				}
				if time.Since(before) != gap {
					t.Fatal("foreground skipped spacing on reduced host")
				}
				if limit, _ := c.current(addr); limit != 1 {
					t.Fatalf("reduced cap = %d, want 1", limit)
				}
				c.release(addr)
			})
		})
	}
}

// Read-ahead is the producer for long streaming reads: empty reserves must
// refill as a burst, healthy reserves stay paced, and joining a sleeping run
// must wake admission immediately rather than wait for its spacing timer.
func TestConnCapReadAheadLowWater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const addr = "cdn:443"
		const gap = 250 * time.Millisecond
		const mib = 1 << 20
		c := newConnCap(defaultHostConnections)
		c.dialGap = gap
		f := &remoteFile{
			e: &Engine{block: mib, limit: c}, size: 64 * mib, nblocks: 64,
			mem:   lru.New(lru.Options[int64, []byte]{MaxEntries: memBlocksPerFile}),
			edges: map[int64][]byte{},
		}
		f.bitrateFloor.Store(2 * mib)
		epoch := t.Context()
		s := &httpSource{f: f, raCtx: epoch}
		background := context.WithValue(epoch, fetchModeKey{}, fetchReadAhead)
		ctx := context.WithValue(background, readAheadDialKey{}, func() bool { return s.readAheadDialUrgent(epoch) })
		burst := func() {
			t.Helper()
			c.nextDial[addr] = time.Now().Add(gap)
			before := time.Now()
			for i := 0; i < defaultHostConnections; i++ {
				if err := c.acquire(ctx, addr); err != nil {
					t.Fatal(err)
				}
			}
			if elapsed := time.Since(before); elapsed != 0 {
				t.Fatalf("read-ahead refill paid spacing: %v", elapsed)
			}
			extraCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- c.acquire(extraCtx, addr) }()
			synctest.Wait()
			select {
			case err := <-done:
				t.Fatalf("read-ahead exceeded cap: %v", err)
			default:
			}
			cancel()
			if err := <-done; err == nil {
				t.Fatal("cancelled dial admitted")
			}
			for i := 0; i < defaultHostConnections; i++ {
				c.release(addr)
			}
		}
		burst() // open, before the first measurement
		s.sustain.measured, s.sustain.rate = true, mib
		burst() // measured but empty: bitrate floor exceeds observed delivery
		// Three MiB exceeds 2.5 seconds of measured delivery, but falls
		// short of the five MiB required by the unchanged bitrate floor.
		for i := int64(0); i < 3; i++ {
			f.edges[i] = []byte{1}
		}
		burst()
		for i := int64(3); i < 6; i++ {
			f.edges[i] = []byte{1}
		}
		for i := 0; i < defaultHostConnections; i++ {
			before := time.Now()
			if err := c.acquire(ctx, addr); err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(before); elapsed != gap {
				t.Fatalf("healthy reserve spacing = %v, want %v", elapsed, gap)
			}
			c.release(addr)
		}
		// A new seek epoch is urgent even with a retained estimate/cache.
		s.sustain.measured = false
		burst()
		s.sustain.measured = true
		fl := &blockFlight{demand: new(atomic.Bool)}
		joined := context.WithValue(ctx, playheadDemandKey{}, fl.demand)
		done := make(chan error, 1)
		before := time.Now()
		go func() { done <- c.acquire(joined, addr) }()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("healthy run skipped spacing: %v", err)
		default:
		}
		f.promoteDemand(fl)
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("promoted run was not woken")
		}
		if time.Since(before) != 0 {
			t.Fatal("promoted run paid spacing")
		}
		c.release(addr)
	})
}
