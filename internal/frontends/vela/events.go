package vela

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/frontend"
	"time"
)

// Subscribe only after a viewer opens playback. The broker owns stop and remote
// control events; disabling the frontend cancels every subscription.
func (i *instance) watchSession(viewer *frontend.Viewer, device frontend.Device, id, scope string, s *playSession) {
	ctx, cancel := context.WithCancel(i.ctx)
	events, err := i.host.Sessions.Events(ctx, frontend.EventScope{Viewer: viewer, Protocol: "vela", SessionID: id, Device: device})
	if err != nil {
		events = nil
	}
	go func() {
		defer cancel()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		var liveEventC <-chan time.Time
		if i.host.LiveEventLink != nil {
			s.mu.Lock()
			isLiveChannel := s.channelID != ""
			s.mu.Unlock()
			if isLiveChannel {
				lt := time.NewTicker(i.liveEventPollInterval())
				defer lt.Stop()
				liveEventC = lt.C
			}
		}

		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		output := t.C
		for {
			select {

			case <-liveEventC:
				i.refreshSessionLiveEvent(ctx, viewer, s)

			case <-output:
				s.mu.Lock()
				if !s.ended.IsZero() {
					s.mu.Unlock()
					return
				}
				if s.commandActive {
					s.mu.Unlock()
					continue
				}
				copy, revision := s.outputSnapshot(), s.revision
				s.mu.Unlock()
				changed := i.refreshOutput(ctx, id, copy)
				s.mu.Lock()
				if changed && !s.commandActive && s.revision == revision && s.source == copy.source && s.seek == copy.seek && s.ended.IsZero() {
					s.applyOutput(copy)
					s.publish("plan")
				}
				s.mu.Unlock()
			case <-ctx.Done():
				return
			case <-ticker.C:
				resolver, ok := i.host.Identity.(frontend.GrantScopes)
				if !ok {
					return
				}
				_, err := resolver.GrantScopeCurrent(ctx, scope, device)
				if err != nil && !errors.Is(err, frontend.ErrUnauthorized) && !errors.Is(err, frontend.ErrNotFound) {
					continue
				}
				if err != nil {
					_ = i.host.Sessions.Stop(ctx, frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "vela"}, id)
				}
				s.mu.Lock()
				ended := !s.ended.IsZero()
				if err != nil && !ended {
					s.ended = time.Now()
					s.clearDisconnectTimerLocked()
					s.plan["state"] = "ended"
					s.publish("ended")
				}
				s.mu.Unlock()
				if err != nil || ended {
					return
				}
			case e, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if e.SessionID != id {
					continue
				}
				s.mu.Lock()
				if !s.ended.IsZero() {
					s.mu.Unlock()
					return
				}
				switch e.Type {
				case "SessionStopped":
					s.plan["state"] = "ended"
					s.ended = time.Now()
					s.clearDisconnectTimerLocked()
					s.publish("ended")
					s.mu.Unlock()
					return
				case "SessionCommand":
					if e.Command != nil {
						switch e.Command.Kind {
						case frontend.CommandPause:
							s.plan["state"] = "paused"
							s.publish("plan")
						case frontend.CommandUnpause:
							s.plan["state"] = "playing"
							s.publish("plan")
						}
					}
				}
				s.mu.Unlock()
			}
		}
	}()
}
