package transcode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// DASH clients fetch the chosen representation's init before requesting
// media. An idle rung must start on that request, or neither side proceeds.
func TestDASHLazyRepresentationStartsOnInit(t *testing.T) {
	fr := newFakeRunner()
	m := newTestManager(t, fr, config.Transcode{ABRLadder: true}, WithSegmentTimeout(500*time.Millisecond), withRungDwell(10*time.Second))
	// Encode primary. This manager config leaves abrLadderUnderCopy false, so a copy primary would not advertise soft rungs.
	s := startDASHSession(t, m, ladderEncodeDASHRequest(t, "init-first", 10*time.Minute))
	if fr.startCount() != 0 {
		t.Fatal("DASH metadata started an unrequested encoder")
	}
	rec := httptest.NewRecorder()
	s.ServeInit(rec, httptest.NewRequest(http.MethodGet, "/2/header", nil), 2)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("lazy init: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if fr.startCount() != 1 {
		t.Fatalf("init did not start selected rung: %d starts", fr.startCount())
	}
	rec = httptest.NewRecorder()
	s.ServeInit(rec, httptest.NewRequest(http.MethodGet, "/1/header", nil), 1)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 || fr.startCount() != 1 {
		t.Fatalf("audio init switched the video rung: HTTP %d, starts %d", rec.Code, fr.startCount())
	}
}

func TestDASHManifestMultiRungDoesNotStartEncoder(t *testing.T) {
	fr := newFakeRunner()
	m := newTestManager(t, fr, config.Transcode{ABRLadder: true, ABRLadderUnderCopy: true}, WithManifestWait(2*time.Second))
	s := startDASHSession(t, m, dashRequest(t, "multi-rung-mpd", 10*time.Minute))
	if len(s.rungs) < 2 {
		t.Fatalf("expected multi-rung ladder, got %d rungs", len(s.rungs))
	}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.ServeManifest(rec, httptest.NewRequest(http.MethodGet, "/start.mpd", nil))
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ServeManifest blocked on multi-rung DASH session")
	}
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("ServeManifest HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if fr.startCount() != 0 {
		t.Fatalf("ServeManifest started %d encoders, want 0", fr.startCount())
	}
	s.mu.Lock()
	cached := s.dash.mpd != nil
	s.mu.Unlock()
	if cached {
		t.Fatal("ServeManifest cached estimated MPD before exact codecs arrived")
	}
}

func TestDASHResumeHintStartsAtResumeSegment(t *testing.T) {
	fr := newFakeRunner()
	m := newTestManager(t, fr, config.Transcode{})
	req := dashRequest(t, "dash-resume", 10*time.Minute)
	req.FirstSegmentAt = 2 * time.Minute
	s := startDASHSession(t, m, req)
	at := int(req.FirstSegmentAt / s.segDur)
	wantSeg := max(0, at-1)
	if at <= 0 || s.lastReq != wantSeg {
		t.Fatalf("lastReq = %d, want %d (at=%d)", s.lastReq, wantSeg, at)
	}
	// Both at-1 (Plex Web resume fetch) and at must hit run=1 without restarting.
	for _, seg := range []int{wantSeg, at} {
		rec := httptest.NewRecorder()
		s.ServeMedia(rec, httptest.NewRequest(http.MethodGet, "/seg", nil), 0, seg)
		if rec.Code != http.StatusOK {
			t.Fatalf("segment %d: HTTP %d %s", seg, rec.Code, rec.Body.String())
		}
	}
	if got := fr.startCount(); got != 1 {
		t.Fatalf("encoder starts = %d, want 1 (no wasted restart on at-1)", got)
	}
}

// When ServeManifest defers run=1, native DASH clients issue ranged GETs for
// video (0/header) and audio (1/header) concurrently while the first start is
// still pending (s.cur == nil, s.pendingStart == true). Matching tracks must
// wait for publication instead of failing with 503, while probes of an inactive
// video rung still fail with 503.
func TestDASHRangedInitWaitsForPendingStart(t *testing.T) {
	for _, tc := range []struct {
		name     string
		track    int
		wantWait bool
	}{
		{"pending-video", 0, true},
		{"audio", 1, true},
		{"inactive-rung", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{
				m:         &Manager{opts: options{segmentTimeout: time.Second}},
				dir:       t.TempDir(),
				proto:     core.ProtocolDASH,
				container: ContainerFMP4,
				sessionEncoding: sessionEncoding{
					runSeq:       1,
					pendingStart: true,
					pendingTrack: 0,
				},
				sessionDelivery: sessionDelivery{
					done:    []map[int]bool{{}, {}, {}},
					changed: make(chan struct{}),
					dash: &dashState{
						inits: make([]string, 3),
						reps:  []dashRep{{Kind: "video"}, {Kind: "audio"}, {Kind: "video"}},
					},
				},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waitCtx := &initWaitContext{Context: ctx, entered: make(chan struct{})}
			req := httptest.NewRequest(http.MethodGet, "/header", nil).WithContext(waitCtx)
			req.Header.Set("Range", "bytes=0-")
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); s.serveInit(rec, req, tc.track) }()
			defer func() { cancel(); <-done }()

			if !tc.wantWait {
				<-done
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("inactive rung ranged probe: status=%d, want 503", rec.Code)
				}
				return
			}
			select {
			case <-done:
				t.Fatalf("ranged init returned before pending start published: HTTP %d %q", rec.Code, rec.Body.String())
			case <-waitCtx.entered:
			case <-time.After(time.Second):
				t.Fatal("ranged init did not wait")
			}
			body := []byte("pending init bytes")
			if err := os.WriteFile(filepath.Join(s.dir, "ready.init"), body, 0o600); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			s.dash.inits[tc.track] = "ready.init"
			s.pendingStart = false
			s.broadcastLocked()
			s.mu.Unlock()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("ranged init waiter did not complete")
			}
			if rec.Code != http.StatusPartialContent || rec.Body.String() != string(body) {
				t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
			}
		})
	}
}
