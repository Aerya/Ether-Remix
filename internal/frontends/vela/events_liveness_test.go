package vela

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

// Exercise the actual stream's connect and heartbeat path with no media or
// observation requests. The manager's idle/stop semantics have a fake-clock test.
func TestVelaEventsLiveness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	calls := 0
	s := &playSession{scope: "fake-family", viewer: 1, device: "device", signal: make(chan struct{}), plan: object{"sessionId": "session"}}
	sessions := frontendtest.Sessions{KeepAliveFunc: func(c context.Context, a frontend.SessionAccess, id string) error {
		if c.Err() != nil || a.Viewer.ID != 1 || a.Device.ID != "device" || a.Protocol != "vela" || id != "session" {
			t.Fatal("lost authorization", a, id)
		}
		calls++
		if calls == 2 {
			s.mu.Lock()
			s.ended = time.Now()
			s.publish("ended")
			s.mu.Unlock()
		}
		return nil
	}}
	i := &instance{
		ctx:             ctx,
		eventsHeartbeat: 20 * time.Millisecond,
		disconnectGrace: 80 * time.Millisecond,
		host:            frontend.Host{Identity: identityFake{}, Sessions: sessions},
		sessions:        map[string]*playSession{"session": s},
	}
	defer i.Close(ctx)
	r := httptest.NewRequest("GET", "/vela/v0/sessions/session/events", nil).WithContext(ctx)
	r.SetPathValue("id", "session")
	r.Header.Set("Authorization", "Bearer token")
	r.Header.Set("X-Vela-Device-Id", "device")
	w := httptest.NewRecorder()
	i.events(w, r)
	if calls != 2 {
		t.Fatalf("connect + heartbeat calls = %d, body %s", calls, w.Body.String())
	}
}

type deadPeerWriter struct {
	header      http.Header
	writes      int
	deadline    time.Time
	failOnFlush int
}

func (w *deadPeerWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *deadPeerWriter) WriteHeader(int) {}
func (w *deadPeerWriter) Write(b []byte) (int, error) {
	w.writes++
	return len(b), nil
}
func (w *deadPeerWriter) Flush() {}
func (w *deadPeerWriter) SetWriteDeadline(d time.Time) error {
	w.deadline = d
	return nil
}
func (w *deadPeerWriter) FlushError() error {
	if w.deadline.IsZero() {
		return errors.New("missing write deadline")
	}
	if w.failOnFlush > 0 && w.writes >= w.failOnFlush {
		return context.DeadlineExceeded
	}
	return nil
}

func TestVelaEventsDisconnectEndsSessionUnlessReconnectedOrActive(t *testing.T) {
	newReq := func(ctx context.Context, path string) *http.Request {
		r := httptest.NewRequest("GET", path, nil).WithContext(ctx)
		r.SetPathValue("id", "session")
		r.Header.Set("Authorization", "Bearer token")
		r.Header.Set("X-Vela-Device-Id", "device")
		return r
	}

	t.Run("reconnect inside grace keeps session", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var stops atomic.Int32
		stopped := make(chan struct{}, 1)
		s := &playSession{scope: "fake-family", viewer: 1, device: "device", signal: make(chan struct{}), plan: object{"sessionId": "session", "state": "playing"}}
		sessions := frontendtest.Sessions{
			StopFunc: func(_ context.Context, a frontend.SessionAccess, id string) error {
				if a.Viewer.ID != 1 || a.Device.ID != "device" || a.Protocol != "vela" || id != "session" {
					t.Errorf("unexpected stop access: %+v id=%s", a, id)
				}
				stops.Add(1)
				select {
				case stopped <- struct{}{}:
				default:
				}
				return nil
			},
		}
		i := &instance{
			ctx:                ctx,
			cancel:             cancel,
			eventsHeartbeat:    20 * time.Millisecond,
			eventsWriteTimeout: 20 * time.Millisecond,
			disconnectGrace:    80 * time.Millisecond,
			host:               frontend.Host{Identity: identityFake{}, Sessions: sessions},
			sessions:           map[string]*playSession{"session": s},
		}
		defer i.Close(ctx)

		// First stream connects and immediately drops (network blip / background).
		s1Ctx, s1Cancel := context.WithCancel(ctx)
		s1Done := make(chan struct{})
		go func() {
			defer close(s1Done)
			i.events(httptest.NewRecorder(), newReq(s1Ctx, "/vela/v0/sessions/session/events"))
		}()
		time.Sleep(15 * time.Millisecond)
		s1Cancel()
		<-s1Done

		// Reconnect within the 80ms grace window and hold stream open past it.
		time.Sleep(25 * time.Millisecond)
		s2Ctx, s2Cancel := context.WithCancel(ctx)
		s2Done := make(chan struct{})
		go func() {
			defer close(s2Done)
			i.events(httptest.NewRecorder(), newReq(s2Ctx, "/vela/v0/sessions/session/events"))
		}()
		time.Sleep(130 * time.Millisecond)
		if got := stops.Load(); got != 0 {
			t.Fatalf("session stopped despite reconnect inside grace (stops=%d)", got)
		}

		// Now close the second stream and stay silent: session must end shortly after grace.
		s2Cancel()
		<-s2Done
		select {
		case <-stopped:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("disconnected session did not stop after grace")
		}
		if got := stops.Load(); got != 1 {
			t.Fatalf("stops = %d, want 1", got)
		}
	})

	t.Run("media read and session request extend window; dead peer surfaces via write deadline", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var stops atomic.Int32
		stopped := make(chan struct{}, 1)
		s := &playSession{scope: "fake-family", viewer: 1, device: "device", signal: make(chan struct{}), plan: object{"sessionId": "session", "state": "playing"}}
		sessions := frontendtest.Sessions{
			ServeMediaFunc: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("segment-bytes"))
			},
			StopFunc: func(_ context.Context, _ frontend.SessionAccess, _ string) error {
				stops.Add(1)
				select {
				case stopped <- struct{}{}:
				default:
				}
				return nil
			},
		}
		i := &instance{
			ctx:                ctx,
			cancel:             cancel,
			eventsHeartbeat:    20 * time.Millisecond,
			eventsWriteTimeout: 25 * time.Millisecond,
			disconnectGrace:    90 * time.Millisecond,
			host:               frontend.Host{Identity: identityFake{}, Sessions: sessions},
			sessions:           map[string]*playSession{"session": s},
		}
		defer i.Close(ctx)

		// Stream writes initial snapshot, then fails flush on first heartbeat (dead peer).
		streamDone := make(chan struct{})
		dw := &deadPeerWriter{failOnFlush: 2}
		go func() {
			defer close(streamDone)
			i.events(dw, newReq(ctx, "/vela/v0/sessions/session/events"))
		}()
		select {
		case <-streamDone:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("dead peer heartbeat flush failure did not terminate events stream promptly")
		}

		// Keep session alive across >2x disconnectGrace via media reads and snapshot requests.
		for n := range 3 {
			time.Sleep(45 * time.Millisecond)
			if n%2 == 0 {
				i.serveMedia(httptest.NewRecorder(), httptest.NewRequest("GET", "/sessions/media/session/grant/seg.ts", nil))
			} else {
				i.snapshot(httptest.NewRecorder(), newReq(ctx, "/vela/v0/sessions/session"))
			}
			if got := stops.Load(); got != 0 {
				t.Fatalf("session stopped while client still active (step=%d, stops=%d)", n, got)
			}
		}

		// Once media reads and session requests cease, session ends after grace.
		select {
		case <-stopped:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("session did not stop after activity ceased")
		}
		// The stop callback fires before the session records its end.
		for range 200 {
			s.mu.Lock()
			ended := !s.ended.IsZero() && s.plan["state"] == "ended"
			s.mu.Unlock()
			if ended {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("playSession was not marked ended")
	})
}
