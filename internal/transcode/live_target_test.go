package transcode

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestLiveTargetStableAcrossWindowAndGOPs(t *testing.T) {
	for _, initial := range []time.Duration{4 * time.Second, 12 * time.Second} {
		t.Run(initial.String(), func(t *testing.T) {
			m := newTestManager(t, newFakeRunner(), config.Transcode{})
			req := remuxRequest(t, "target", 0)
			req.LiveInput = true
			req.LazyStart = true
			s := startSession(t, m, req)
			s.mu.Lock()
			defer s.mu.Unlock()
			if b := s.renderPlaylistLocked(0); len(b) != 0 {
				t.Fatal("committed target before startup observation")
			}
			target := max(8*time.Second, initial)
			for n := 0; n < 25; n++ {
				d := 4 * time.Second
				if n == 0 {
					d = initial
				}
				if n == 5 {
					d = 7 * time.Second
				}
				if err := s.validateLiveDurationLocked(n, d); err != nil {
					t.Fatal(err)
				}
				s.noteLiveDurationLocked(0, n, d)
				s.done[0][n] = true
				if n == 5 {
					s.liveDiscontinuities = map[int]bool{5: true}
				}
				b := string(s.renderPlaylistLocked(0))
				if !strings.Contains(b, fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", int(target.Seconds()))) {
					t.Fatal(b)
				}
				if !strings.Contains(b, fmt.Sprintf("#EXTINF:%.6f,", d.Seconds())) {
					t.Fatal("duration changed", b)
				}
			}
			if err := s.validateLiveDurationLocked(25, target+499*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if err := s.validateLiveDurationLocked(25, target+500*time.Millisecond); err == nil {
				t.Fatal("accepted oversized unsplittable GOP")
			}
			if s.liveTarget != target {
				t.Fatal("rejection changed target")
			}
			if b := string(s.renderPlaylistLocked(0)); !strings.Contains(b, "#EXT-X-DISCONTINUITY-SEQUENCE:1\n") {
				t.Fatal(b)
			}
		})
	}
}

func TestLiveTargetStartupLazyAndCancellation(t *testing.T) {
	for _, rung := range []bool{false, true} {
		t.Run(fmt.Sprint(rung), func(t *testing.T) {
			fr := newFakeRunner()
			m := newTestManager(t, fr, config.Transcode{ABRLadder: rung})
			req := remuxRequest(t, "lazy-target", 0)
			if rung {
				req.Info = ladderInfo(1080, 10000)
				req.Info.Duration = 0
				req.Decision = core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", AudioCodec: "aac", AudioStream: 1, SubtitleStream: -1, Container: ContainerMPEGTS, BitrateKbps: 8000}
			}
			req.LiveInput = true
			req.LazyStart = true
			s := startSession(t, m, req)
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/", nil)
			if rung {
				if len(s.rungs) < 2 {
					t.Fatal("fixture needs lower rendition")
				}
				s.ServeRungPlaylist(w, r, s.rungs[1].id)
				s.mu.Lock()
				track := s.cur.track
				s.mu.Unlock()
				if track != 1 {
					t.Fatal("activated wrong rendition", track)
				}
			} else {
				s.ServePlaylist(w, r)
			}
			if w.Code != 200 || !strings.Contains(w.Body.String(), "#EXT-X-TARGETDURATION:8\n") || fr.startCount() != 1 {
				t.Fatalf("lazy startup: %d %s starts=%d", w.Code, w.Body, fr.startCount())
			}
		})
	}
	fr := newFakeRunner()
	fr.segDelay = time.Hour
	m := newTestManager(t, fr, config.Transcode{})
	req := remuxRequest(t, "cancel-target", 0)
	req.LiveInput = true
	req.LazyStart = true
	s := startSession(t, m, req)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.awaitLiveTarget(ctx, 0) }()
	deadline := time.Now().Add(time.Second)
	for fr.startCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("startup ignored cancellation")
	}
	s.mu.Lock()
	target := s.liveTarget
	s.mu.Unlock()
	if target != 0 {
		t.Fatal("cancellation committed target")
	}
}

func TestLiveTargetRejectsBeforePublicationAndNotifiesOnce(t *testing.T) {
	fr := newFakeRunner()
	fr.segDelay = time.Hour
	failures := make(chan error, 2)
	m := newTestManager(t, fr, config.Transcode{}, WithInputFailureHandler(func(_, _ string, _ time.Duration, err error) { failures <- err }))
	req := remuxRequest(t, "bad-gop", 0)
	req.LiveInput = true
	s := startSession(t, m, req)
	s.mu.Lock()
	r := s.cur
	s.establishLiveTargetLocked(4 * time.Second)
	s.mu.Unlock()
	path := s.trackSegPath(0, 0)
	if err := os.WriteFile(path, []byte("unsplittable media"), 0600); err != nil {
		t.Fatal(err)
	}
	playlist := filepath.Join(r.spec.Dir, r.spec.Playlist)
	if err := os.WriteFile(playlist, []byte("#EXTM3U\n#EXTINF:12.000000,\n"+filepath.Base(path)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.poll(r, playlist, false)
	select {
	case err := <-failures:
		var targetErr *liveTargetError
		if !errors.As(err, &targetErr) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no failure callback")
	}
	s.mu.Lock()
	if s.done[0][0] || s.liveTarget != 8*time.Second || !s.inputFailed {
		t.Fatal("invalid output published or failure lost")
	}
	s.mu.Unlock()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid media retained", err)
	}
	if err := s.SwapInput(context.Background(), req.InputURL, req.Info, 0); err == nil {
		t.Fatal("restarted rejected input")
	}
	if fr.startCount() != 1 {
		t.Fatal("same source retried")
	}
	if err := s.SwapInput(context.Background(), "http://127.0.0.1/replacement", req.Info, 0); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	target := s.liveTarget
	s.mu.Unlock()
	if fr.startCount() != 2 || target != 8*time.Second {
		t.Fatal("replacement changed target or failed to start")
	}
	select {
	case <-failures:
		t.Fatal("duplicate failure callback")
	default:
	}
}
