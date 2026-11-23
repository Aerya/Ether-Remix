package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

// An in-flight host report owns neither byte serving nor the newer UI clock.
func TestVelaObservationOrdering(t *testing.T) {
	for _, seek := range []bool{false, true} {
		name := "sequence"
		if seek {
			name = "seek"
		}
		t.Run(name, func(t *testing.T) {
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			sessions := &frontendtest.Sessions{ReportFunc: func(_ context.Context, _ frontend.SessionAccess, _ string, report frontend.Timeline) error {
				if report.Sequence == 1 {
					close(entered)
					<-release
				}
				return nil
			}, ServeMediaFunc: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("media")) }}
			s := &playSession{scope: "fake-family", viewer: 1, device: "device", source: 2, seek: 3, signal: make(chan struct{}), plan: object{"sessionId": "session", "selectedPathId": "original", "paths": []object{{"id": "original", "transport": "file"}}}}
			i := &instance{host: frontend.Host{Identity: identityFake{}, Sessions: sessions}, sessions: map[string]*playSession{"session": s}}
			request := func(payload string) *http.Request {
				r := httptest.NewRequest("POST", "/sessions/session/observations", nil)
				r.SetPathValue("id", "session")
				r.Header.Set("Authorization", "Bearer token")
				r.Header.Set("X-Vela-Device-Id", "device")
				return r.WithContext(context.WithValue(r.Context(), bodyKey{}, json.RawMessage(payload)))
			}
			old := httptest.NewRecorder()
			go func() {
				defer close(done)
				i.observe(old, request(`{"sourceGeneration":2,"seekGeneration":3,"sequence":1,"positionSeconds":10,"state":"playing","selectedPathId":"original"}`))
			}()
			defer func() { close(release); <-done }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("report did not start")
			}
			mediaDone := make(chan struct{})
			go func() {
				defer close(mediaDone)
				i.serveObservedMedia(httptest.NewRecorder(), httptest.NewRequest("GET", "/sessions/media/session/grant/file", nil))
			}()
			select {
			case <-mediaDone:
			case <-time.After(time.Second):
				t.Fatal("media waited for report")
			}
			w := httptest.NewRecorder()
			if seek {
				i.command(w, request(`{"kind":"seek","sourceGeneration":2,"seekGeneration":3,"positionSeconds":90}`))
			} else {
				i.observe(w, request(`{"sourceGeneration":2,"seekGeneration":3,"sequence":2,"positionSeconds":90,"state":"paused","selectedPathId":"original"}`))
			}
			if w.Code != 200 {
				t.Fatalf("newer operation: %d %s", w.Code, w.Body.String())
			}
			// Release and join before inspecting the final state.
			release <- struct{}{}
			<-done
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.position != 90*time.Second || (!seek && s.sequence != 2) || (seek && s.seek != 4) {
				t.Fatalf("older report replaced newer clock: position=%s sequence=%d seek=%d", s.position, s.sequence, s.seek)
			}
			if seek && old.Code != 409 || !seek && old.Code != 200 {
				t.Fatalf("old response: %d", old.Code)
			}
		})
	}
}
