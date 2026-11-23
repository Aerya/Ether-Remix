package debrid

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type seekHeldBody struct {
	ctx   context.Context
	ready func()
	allow <-chan struct{}
	left  int64
	once  sync.Once
	close func()
}

func (b *seekHeldBody) Read(p []byte) (int, error) {
	b.once.Do(b.ready)
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.allow:
	}
	if b.left == 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), b.left))
	clear(p[:n])
	b.left -= int64(n)
	return n, nil
}
func (b *seekHeldBody) Close() error { b.close(); return nil }

// An epoch's response bodies and slots must all retire before a cold seek
// submits its playhead. The entire demand-sized burst must then start before
// any new body delivers bytes. A fixed injected clock and channel barriers
// make these ordering assertions independent of machine speed.
func TestSeekSlotHandover(t *testing.T) {
	r := newEngineRig(t, 64<<20, -1)
	now := time.Now()
	r.eng.now = func() time.Time { return now }
	src, err := r.eng.Open(t.Context(), r.rel.ID, r.pl)
	if err != nil {
		t.Fatal(err)
	}
	s := src.(*httpSource)
	defer s.Close()
	f := s.f
	const seekBlock = 512
	oldReady := make(chan struct{}, 4)
	newReady := make(chan int32, 4)
	allow := make(chan struct{})
	var oldBodies atomic.Int32
	r.eng.hc = &http.Client{Transport: deliveryTransport(func(req *http.Request) (*http.Response, error) {
		var start, end int64
		if _, err := fmt.Sscanf(req.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			return nil, err
		}
		var ready func()
		var closeBody func()
		if start < seekBlock*testBlock {
			oldBodies.Add(1)
			ready = func() { oldReady <- struct{}{} }
			var once sync.Once
			closeBody = func() { once.Do(func() { oldBodies.Add(-1) }) }
		} else {
			ready = func() { newReady <- oldBodies.Load() }
			closeBody = func() {}
		}
		return &http.Response{StatusCode: http.StatusPartialContent,
			Header:        http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", start, end, f.size)}},
			ContentLength: end - start + 1, Request: req,
			Body: &seekHeldBody{ctx: req.Context(), ready: ready, allow: allow, left: end - start + 1, close: closeBody}}, nil
	})}
	// Fill all four host slots, including an old ordinary foreground tail.
	epoch := s.raCtx
	for i := int64(0); i < 4; i++ {
		mode := fetchReadAhead
		if i == 3 {
			mode = fetchUrgent
		}
		f.startRun(epoch, i*32, 32, mode, false)
	}
	for i := 0; i < 4; i++ {
		<-oldReady
	}
	s.mu.Lock()
	s.last = 0
	s.sustain.rate, s.sustain.rateAt, s.sustain.lastRead = 8<<20, now, now
	s.mu.Unlock()
	f.bmu.Lock()
	f.tput = 2 << 20
	f.bmu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := s.ReadAtContext(t.Context(), make([]byte, 1), seekBlock*testBlock)
		done <- err
	}()
	for i := 0; i < 4; i++ {
		if old := <-newReady; old != 0 {
			t.Fatalf("new seek admitted with %d obsolete bodies", old)
		}
	}
	if epoch.Err() == nil {
		t.Fatal("old epoch survived seek")
	}
	select {
	case err := <-done:
		t.Fatalf("playhead returned before its first byte: %v", err)
	default:
	}
	close(allow)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Await cleanup explicitly: no range worker is left behind by this test.
	s.raCancel()
	runs, _ := s.raCtx.Value(seekRunsKey{}).(*seekRuns)
	runs.wait()
}
