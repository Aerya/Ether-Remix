package debrid

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Playback progress must survive both queued priority promotion and overlapping
// reads joining an existing range. Neither needs a new dial or a seek epoch.
func TestPlaybackProgress(t *testing.T) {
	t.Run("promoted sleeping dial", func(t *testing.T) {
		c := newConnCap(1)
		const addr = "cdn:443"
		c.dialGap = 50 * time.Millisecond
		c.nextDial[addr] = time.Now().Add(100 * time.Millisecond)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		var promoted atomic.Bool
		done := make(chan string, 2)
		enqueue := func(name string, urgent func() bool, count int) {
			request := context.WithValue(ctx, readPriorityKey{}, urgent)
			go func() {
				if err := c.acquire(request, addr); err == nil {
					done <- name
				} else {
					done <- err.Error()
				}
			}()
			for {
				c.mu.Lock()
				n := len(c.waiters[addr])
				c.mu.Unlock()
				if n == count {
					return
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
		}
		enqueue("background", func() bool { return false }, 1)
		enqueue("playback", promoted.Load, 2)
		promoted.Store(true) // no release or other signal: only dial spacing expires
		select {
		case got := <-done:
			if got != "playback" {
				t.Fatalf("first grant = %s", got)
			}
		case <-ctx.Done():
			t.Fatal("priority promotion stranded a free dial slot")
		}
		c.release(addr)
		select {
		case got := <-done:
			if got != "background" {
				t.Fatalf("second grant = %s", got)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		c.release(addr)
	})
	t.Run("forward overlap retains flights", func(t *testing.T) {
		r := newEngineRig(t, 64*testBlock, -1)
		r.media.pace = 5 * time.Millisecond
		src, err := r.eng.Open(t.Context(), r.rel.ID, r.pl)
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		s := src.(*httpSource)
		epoch := s.raCtx
		s.last = 8 // a later HTTP request arrived first
		fl, _ := s.f.startRun(epoch, 4, 8, fetchReadAhead, false)
		buf := make([]byte, 4096)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		n, err := s.ReadAtConcurrentContext(ctx, buf, 6*testBlock)
		if err != nil || n != len(buf) || !bytes.Equal(buf, r.media.data[6*testBlock:6*testBlock+len(buf)]) {
			t.Fatalf("overlapping read: n=%d err=%v", n, err)
		}
		if epoch.Err() != nil || s.raCtx != epoch {
			t.Fatal("overlap retired forward read-ahead")
		}
		<-fl.done
		// Joining a later block must preserve the run's tail, not steal flights.
		for _, rg := range mediaRanges(r.media) {
			if strings.HasPrefix(rg, "bytes=393216-") {
				t.Fatal("reader duplicated its in-flight block")
			}
		}
	})
}
