package debrid

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestWarmHostSlotYieldsToDemand(t *testing.T) {
	gate := newHostGate(newConnCap(1))
	warm, cancel := context.WithCancel(t.Context())
	defer cancel()
	release, err := gate.acquire(warm, "cdn:443", func() bool { return false }, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	got := make(chan func(), 1)
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	go func() {
		r, err := gate.acquire(ctx, "cdn:443", func() bool { return true })
		if err == nil {
			got <- r
		}
	}()
	select {
	case <-warm.Done():
	case <-ctx.Done():
		t.Fatal("warm body retained the only host slot")
	}
	release() // response body closes after cancellation
	select {
	case r := <-got:
		r()
	case <-ctx.Done():
		t.Fatal("demand was not admitted")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if len(gate.active) != 0 || len(gate.busy) != 0 {
		t.Fatal("host lease leaked")
	}
}

// An admitted background run can become the producer a viewer is waiting on.
// Preemption must consult the live priority rather than its admission class.
func TestWarmHostSlotProtectsPromotedDemand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := newHostGate(newConnCap(1))
		warm, cancel := context.WithCancel(t.Context())
		defer cancel()
		var demand atomic.Bool
		release, err := gate.acquire(warm, "cdn:443", demand.Load, cancel)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		demand.Store(true)
		done := make(chan error, 1)
		go func() {
			r, err := gate.acquire(t.Context(), "cdn:443", func() bool { return true })
			if err == nil {
				r()
			}
			done <- err
		}()
		synctest.Wait()
		if warm.Err() != nil {
			t.Fatal("joined viewer's body was preempted")
		}
		release()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

// Admission uses identity of the source seek epoch. Context annotations must
// not replace that identity when the callback checks the live buffer floor.
func TestWarmReadAttributionPreservesLowWaterDial(t *testing.T) {
	r := newEngineRig(t, 16<<20, -1)
	src, err := r.eng.Open(t.Context(), r.rel.ID, r.pl)
	if err != nil {
		t.Fatal(err)
	}
	s := src.(*httpSource)
	defer s.Close()
	seen := make(chan bool, 8)
	r.eng.hc = &http.Client{Transport: deliveryTransport(func(req *http.Request) (*http.Response, error) {
		var start, end int64
		if _, err := fmt.Sscanf(req.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			return nil, err
		}
		select {
		case seen <- dialUrgent(req.Context()):
		default:
		}
		return &http.Response{StatusCode: http.StatusPartialContent,
			Header:        http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", start, end, s.f.size)}},
			ContentLength: end - start + 1, Request: req,
			Body: io.NopCloser(bytes.NewReader(make([]byte, end-start+1)))}, nil
	})}
	s.readAhead(0)
	select {
	case urgent := <-seen:
		if !urgent {
			t.Fatal("attribution disabled the active low-water epoch")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no read-ahead request")
	}
}
