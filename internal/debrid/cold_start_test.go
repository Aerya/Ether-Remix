package debrid

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Cold-open ordering is a resource handover, not a latency threshold. Hold
// bodies at channel barriers and freeze the policy clock to prove cleanup,
// foreground ownership and startup headroom independently of machine speed.
func TestColdOpenHandover(t *testing.T) {
	for _, hostCap := range []int{4, 1} {
		t.Run(fmt.Sprintf("host cap %d", hostCap), func(t *testing.T) {
			r := newEngineRig(t, 16<<20, -1)
			now := time.Unix(1000, 0)
			r.eng.now = func() time.Time { return now }
			r.eng.limit = newConnCap(hostCap)
			src, err := r.eng.Open(t.Context(), r.rel.ID, r.pl)
			if err != nil {
				t.Fatal(err)
			}
			s := src.(*httpSource)
			defer s.Close()
			f := s.f
			metadataClosing, metadataClose := make(chan struct{}), make(chan struct{})
			allow := make(chan struct{})
			oldReady := make(chan struct{}, 4)
			newReady := make(chan struct{}, 4)
			var oldBodies atomic.Int32
			r.eng.hc = &http.Client{Transport: deliveryTransport(func(req *http.Request) (*http.Response, error) {
				var start, end int64
				if _, err := fmt.Sscanf(req.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
					return nil, err
				}
				body := &seekHeldBody{ctx: req.Context(), left: end - start + 1, allow: allow}
				switch {
				case start == f.size-testBlock: // native probe's final tail read
					ready := make(chan struct{})
					close(ready)
					body.allow, body.ready = ready, func() {}
					body.close = func() { close(metadataClosing); <-metadataClose }
				case start >= 128*testBlock: // preparation beyond the startup horizon, left in the old epoch
					oldBodies.Add(1)
					body.ready = func() { oldReady <- struct{}{} }
					var once sync.Once
					body.close = func() { once.Do(func() { oldBodies.Add(-1) }) }
				default:
					body.ready = func() { newReady <- struct{}{} }
					body.close = func() {}
					if old := oldBodies.Load(); old != 0 {
						t.Errorf("startup admitted with %d obsolete bodies", old)
					}
					if start == 0 && !gateUrgent(req.Context()) {
						t.Error("first head range entered admission without foreground priority")
					}
				}
				return &http.Response{StatusCode: http.StatusPartialContent,
					Header:        http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", start, end, f.size)}},
					ContentLength: end - start + 1, Request: req, Body: body}, nil
			})}
			metadataDone := make(chan error, 1)
			go func() {
				_, err := s.ReadMetadataAtContext(t.Context(), make([]byte, 1), f.size-testBlock)
				metadataDone <- err
			}()
			<-metadataClosing
			select {
			case err := <-metadataDone:
				t.Fatalf("probe returned before releasing its response: %v", err)
			default:
			}
			close(metadataClose)
			if err := <-metadataDone; err != nil {
				t.Fatal(err)
			}

			epoch := s.raCtx
			for i := int64(1); i <= 4; i++ {
				f.startRun(epoch, 128+(i-1)*32, 32, fetchUrgent, false)
			}
			for i := 0; i < 4; i++ {
				<-oldReady
			}
			done := make(chan error, 3)
			request, cancel := newSeekEpoch(t.Context())
			defer cancel()
			read := func(off int64) {
				_, err := s.ReadAtConcurrentContext(request, make([]byte, 1), off)
				done <- err
			}
			go read(0)
			parallel := hostCap
			for i := 0; i < parallel; i++ {
				<-newReady
			}
			if epoch.Err() == nil {
				t.Fatal("cold head read did not retire preparation epoch")
			}
			f.bmu.Lock()
			fl, runs := f.flights[0], f.runCount
			f.bmu.Unlock()
			if fl == nil || fl.mode != fetchUrgent || !fl.demand.Load() || runs != parallel {
				t.Fatalf("startup foreground=%+v runs=%d, want urgent demand and %d runs", fl, runs, parallel)
			}
			go read(testBlock)
			go read(2 * testBlock)
			close(allow)
			for i := 0; i < 3; i++ {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			cancel()
			s.raCancel()
			runsInEpoch, _ := s.raCtx.Value(seekRunsKey{}).(*seekRuns)
			runsInEpoch.wait()
			// Concurrent foreground workers belong to the request's epoch.
			requestRuns, _ := request.Value(seekRunsKey{}).(*seekRuns)
			requestRuns.wait()
		})
	}
}
