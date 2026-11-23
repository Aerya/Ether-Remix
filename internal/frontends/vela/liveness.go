package vela

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

const (
	defaultEventsHeartbeat    = 5 * time.Second
	defaultEventsWriteTimeout = 5 * time.Second
	defaultDisconnectGrace    = 15 * time.Second
	defaultDeadPeerTimeout    = 8 * time.Second
)

func (i *instance) heartbeatInterval() time.Duration {
	if i.eventsHeartbeat > 0 {
		return i.eventsHeartbeat
	}
	return defaultEventsHeartbeat
}

func (i *instance) streamWriteTimeout() time.Duration {
	if i.eventsWriteTimeout > 0 {
		return i.eventsWriteTimeout
	}
	return defaultEventsWriteTimeout
}

func (i *instance) streamDisconnectGrace() time.Duration {
	if i.disconnectGrace > 0 {
		return i.disconnectGrace
	}
	return defaultDisconnectGrace
}

func (i *instance) deadPeerTimeout() time.Duration {
	if i.eventsHeartbeat > 0 || i.eventsWriteTimeout > 0 {
		return max(2*i.heartbeatInterval(), i.streamWriteTimeout())
	}
	return defaultDeadPeerTimeout
}

func (i *instance) done() <-chan struct{} {
	if i.ctx == nil {
		return nil
	}
	return i.ctx.Done()
}

func (s *playSession) noteActivity() {
	s.lastActivityNano.Store(time.Now().UnixNano())
}

func (s *playSession) beginMediaRead() func() {
	s.mediaReads.Add(1)
	s.noteActivity()
	return func() {
		s.mediaReads.Add(-1)
		s.noteActivity()
	}
}

func (s *playSession) remainingGrace(grace time.Duration) time.Duration {
	ns := s.lastActivityNano.Load()
	if ns == 0 {
		return 0
	}
	elapsed := time.Since(time.Unix(0, ns))
	if elapsed >= grace {
		return 0
	}
	return grace - elapsed
}

func (s *playSession) clearDisconnectTimerLocked() {
	if s.disconnectTimer != nil {
		s.disconnectTimer.Stop()
		s.disconnectTimer = nil
	}
}

func (s *playSession) armDisconnectTimerLocked(delay time.Duration, fn func()) {
	s.clearDisconnectTimerLocked()
	s.disconnectTimer = time.AfterFunc(delay, fn)
}

func (i *instance) checkDisconnectedSession(id string, s *playSession) {
	if i.ctx != nil && i.ctx.Err() != nil {
		return
	}
	grace := i.streamDisconnectGrace()
	retry := func() { i.checkDisconnectedSession(id, s) }

	s.mu.Lock()
	if !s.ended.IsZero() || s.stopping || s.streamOpen {
		s.disconnectTimer = nil
		s.mu.Unlock()
		return
	}
	if s.commandActive || s.mediaReads.Load() > 0 {
		s.armDisconnectTimerLocked(grace, retry)
		s.mu.Unlock()
		return
	}
	if rem := s.remainingGrace(grace); rem > 0 {
		s.armDisconnectTimerLocked(rem, retry)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	s.commandMu.Lock()
	defer s.commandMu.Unlock()

	s.mu.Lock()
	if !s.ended.IsZero() || s.stopping || s.streamOpen {
		s.disconnectTimer = nil
		s.mu.Unlock()
		return
	}
	if s.commandActive || s.mediaReads.Load() > 0 {
		s.armDisconnectTimerLocked(grace, retry)
		s.mu.Unlock()
		return
	}
	if rem := s.remainingGrace(grace); rem > 0 {
		s.armDisconnectTimerLocked(rem, retry)
		s.mu.Unlock()
		return
	}
	s.disconnectTimer = nil
	s.stopping = true
	viewer := s.accessViewer
	if viewer == nil {
		viewer = &frontend.Viewer{ID: s.viewer}
	}
	dev := s.accessDevice
	if dev.ID == "" {
		dev = frontend.Device{ID: s.device}
	}
	s.mu.Unlock()

	baseCtx := i.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	stopCtx, cancel := context.WithTimeout(baseCtx, 5*time.Second)
	err := i.host.Sessions.Stop(stopCtx, frontend.SessionAccess{Viewer: viewer, Device: dev, Protocol: "vela"}, id)
	cancel()

	s.mu.Lock()
	s.stopping = false
	if s.ended.IsZero() && (err == nil || errors.Is(err, frontend.ErrNotFound) || errors.Is(err, frontend.ErrNotAvailable)) {
		s.ended = time.Now()
		if s.plan != nil {
			s.plan["state"] = "ended"
		}
		if s.signal != nil {
			s.publish("ended")
		}
	}
	s.mu.Unlock()
}

func unwrapResponseWriter(w http.ResponseWriter) http.ResponseWriter {
	for w != nil {
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return w
		}
		next := u.Unwrap()
		if next == nil || next == w {
			return w
		}
		w = next
	}
	return nil
}

func flushSSE(rc *http.ResponseController, flush http.Flusher, timeout time.Duration) error {
	_ = rc.SetWriteDeadline(time.Now().Add(timeout))
	err := rc.Flush()
	if errors.Is(err, http.ErrNotSupported) {
		flush.Flush()
		return nil
	}
	return err
}
