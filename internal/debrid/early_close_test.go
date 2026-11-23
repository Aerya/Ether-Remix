package debrid

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Freeze time while an unread remainder holds the last physical connection,
// request slot and file slot. An early foreground close must release all
// three without spending the drain budget. Background drains may keep their
// slots only until foreground admission arrives, including under a reduced cap.
func TestEarlyRangeSlotHandover(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		for _, mode := range []fetchMode{fetchUrgent, fetchReadAhead} {
			t.Run(fmt.Sprintf("reduced=%t/mode=%d", reduced, mode), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					const addr = "media.example:443"
					c := newConnCap(defaultHostConnections)
					if reduced {
						c.reduce(addr)
					}
					g := newHostGate(c)
					limit, _ := c.current(addr)
					for i := 0; i < limit; i++ {
						if err := c.acquire(t.Context(), addr); err != nil {
							t.Fatal(err)
						}
					}
					defer func() {
						for i := 1; i < limit; i++ {
							c.release(addr)
						}
					}()
					host, err := g.acquire(t.Context(), addr, func() bool { return true })
					if err != nil {
						t.Fatal(err)
					}
					fileSlots := make(chan struct{}, 1)
					fileSlots <- struct{}{}
					request, abandon := context.WithCancel(t.Context())
					httpCtx, abort := context.WithCancel(t.Context())
					defer abort()
					finished := make(chan struct{})
					body := newStallBody(&seekHeldBody{ctx: httpCtx, ready: func() {},
						allow: make(chan struct{}), left: 1, close: func() { c.release(addr) }},
						0, abort, func() { host(); <-fileSlots })
					go func() {
						_, _ = io.Copy(io.Discard, body)
						body.Close()
						close(finished)
					}()
					f := &remoteFile{e: &Engine{gate: g}, ctx: t.Context(),
						pl: Payload{URL: "https://media.example/file"}, yieldSig: make(chan struct{})}
					var remaining atomic.Int64
					remaining.Store(1)
					watchDone := make(chan struct{})
					go func() {
						f.keepWarmOnYield(request, finished, &remaining, mode, abort)
						close(watchDone)
					}()
					began := time.Now() // synctest's fake clock
					abandon()
					synctest.Wait()
					if mode == fetchReadAhead {
						if httpCtx.Err() != nil {
							t.Fatal("uncontended background drain was aborted")
						}
						end := g.beginForeground(addr)
						defer end()
						if release, ok := g.tryDrain(addr, func() {}); ok {
							release()
							t.Fatal("drain admitted ahead of foreground")
						}
						synctest.Wait()
					}
					if !time.Now().Equal(began) {
						t.Fatal("handover spent fake time on draining")
					}
					select {
					case <-finished:
					default:
						t.Fatal("abandoned body still owns its slots")
					}
					<-watchDone
					if len(fileSlots) != 0 || g.busy[addr] != 0 || c.open[addr] != limit-1 {
						t.Fatalf("slots retained: file=%d host=%d connections=%d", len(fileSlots), g.busy[addr], c.open[addr])
					}
					if err := c.acquire(t.Context(), addr); err != nil {
						t.Fatal(err)
					}
					c.release(addr)
				})
			})
		}
	}
}
