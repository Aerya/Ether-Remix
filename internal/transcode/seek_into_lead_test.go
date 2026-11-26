package transcode

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

// A copy runs far ahead of the player and the throttle pauses it at
// playhead+aheadLimit. A seek that lands inside that lead is served from
// disk and is the encoder head, so the playhead stays where the player
// left. When the player then reaches the first segment the paused run has
// not written, nothing older is open and the request is blocked on that
// segment: it is the playhead. Before, the playhead stayed behind, the
// throttle kept the run paused for it, and the segment never came (Swiftfin
// AVPlayer froze after a fast-forward and retried forever).
func TestSeekIntoTheLeadResumesThePausedRun(t *testing.T) {
	fr := newFakeRunner()
	fr.segDelay = 5 * time.Millisecond
	m := newTestManager(t, fr, config.Transcode{}, WithBuffering(24*time.Second, 2*time.Minute, 3*time.Minute), WithSegmentPadLimit(3*time.Second))
	s := startSession(t, m, remuxRequest(t, "seek-into-lead", time.Hour))
	if r := get(t, s, 10); r.Code != 200 {
		t.Fatalf("segment 10: %d %s", r.Code, r.Body.String())
	}
	waitFor(t, "encoder throttled ahead of the player", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.cur != nil && s.cur.paused && s.cur.maxDone > 10+s.aheadLimit()
	})
	s.mu.Lock()
	head := s.cur.maxDone
	s.mu.Unlock()

	// The seek lands on media the run already wrote: served at once, and
	// it does not move the playhead (the encoder-head rule still holds).
	if r := get(t, s, head-1); r.Code != 200 {
		t.Fatalf("segment %d: %d %s", head-1, r.Code, r.Body.String())
	}

	// A segment a little past the paused run (inside restartLimit, so the
	// run is waited for, not restarted) is the only open fetch.
	next := head + 3
	type result struct {
		code     int
		body     int
		panicked any
	}
	done := make(chan result, 1)
	go func() {
		rec := httptest.NewRecorder()
		defer func() {
			if p := recover(); p != nil {
				done <- result{panicked: p}
			}
		}()
		req := httptest.NewRequest(http.MethodGet, "/seg/next", nil)
		s.ServeSegment(rec, mediaRequest(s, req, next), next)
		done <- result{code: rec.Code, body: rec.Body.Len()}
	}()
	select {
	case r := <-done:
		if r.panicked != nil || r.code != 200 || r.body == 0 {
			t.Fatalf("segment %d after seeking into the lead: code %d, %d bytes, aborted %v", next, r.code, r.body, r.panicked)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("segment %d never arrived: the paused run was not resumed", next)
	}
	s.mu.Lock()
	playhead := s.lastReq
	s.mu.Unlock()
	if playhead < next {
		t.Fatalf("playhead = %d, want the blocked fetch %d", playhead, next)
	}
}

// AVPlayer bursts forward after a long pause: the playhead stays behind
// because every fetch lands on published media past its restart window, and
// the fetch of the paused run's newest segment stays open while the player
// reads only part of its body. The next segment is then not the oldest open
// fetch, but nothing older waits on the encoder: an open fetch of published
// media is delivery, not a wait. The request for the unwritten segment must
// become the playhead and resume the run (#778: the run stayed paused and
// the segment never came).
func TestHeldOpenHeadFetchDoesNotKeepThePausedRunPaused(t *testing.T) {
	fr := newFakeRunner()
	fr.segDelay = 5 * time.Millisecond
	m := newTestManager(t, fr, config.Transcode{}, WithBuffering(24*time.Second, 2*time.Minute, 3*time.Minute), WithSegmentPadLimit(3*time.Second))
	s := startSession(t, m, remuxRequest(t, "held-open-head", time.Hour))
	for n := 0; n <= 13; n++ {
		if r := get(t, s, n); r.Code != 200 {
			t.Fatalf("segment %d: %d %s", n, r.Code, r.Body.String())
		}
	}
	waitFor(t, "encoder throttled ahead of the player", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.cur != nil && s.cur.paused && s.cur.maxDone > s.lastReq+s.aheadLimit()
	})
	// A pause can land while a segment is being written: wait until the
	// suspended run has published its last one and the session has seen it.
	p := fr.lastProc()
	var head int
	waitFor(t, "the suspended run's last segment", func() bool {
		parked, last := p.isParked()
		if !parked {
			return false
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		head = last
		return s.cur != nil && s.cur.maxDone == last
	})
	next := head + 1
	s.mu.Lock()
	playhead := s.lastReq
	s.mu.Unlock()
	for n := playhead + s.restartLimit() + 2; n < head; n++ {
		if r := get(t, s, n); r.Code != 200 {
			t.Fatalf("segment %d: %d %s", n, r.Code, r.Body.String())
		}
	}

	hold := &holdResponse{got: make(chan struct{}), rel: make(chan struct{})}
	heldDone := make(chan struct{})
	go func() {
		defer close(heldDone)
		req := httptest.NewRequest(http.MethodGet, "/seg/head", nil)
		s.ServeSegment(hold, mediaRequest(s, req, head), head)
	}()
	defer func() {
		close(hold.rel)
		<-heldDone
	}()
	select {
	case <-hold.got:
	case <-time.After(30 * time.Second):
		t.Fatalf("segment %d was not served", head)
	}

	type result struct {
		code, body int
		aborted    any
	}
	done := make(chan result, 1)
	go func() {
		rec := httptest.NewRecorder()
		defer func() {
			if p := recover(); p != nil {
				done <- result{aborted: p}
			}
		}()
		req := httptest.NewRequest(http.MethodGet, "/seg/next", nil)
		s.ServeSegment(rec, mediaRequest(s, req, next), next)
		done <- result{code: rec.Code, body: rec.Body.Len()}
	}()
	select {
	case r := <-done:
		if r.aborted != nil || r.code != 200 || r.body == 0 {
			s.mu.Lock()
			defer s.mu.Unlock()
			t.Fatalf("segment %d while %d is held open: code %d, %d bytes, aborted %v (playhead %d, oldest %d, paused %v)", next, head, r.code, r.body, r.aborted, s.lastReq, s.oldestFetchLocked(), s.cur != nil && s.cur.paused)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("segment %d never arrived while %d was held open", next, head)
	}
}
