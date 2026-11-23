package app

import (
	"context"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/playback"
)

// Owner status presents one row per viewer, including passthrough IPTV.
// Programme sources stay private to the playout service.
type ownerPlayback struct {
	core.Playback
	app *App
}

func (p ownerPlayback) Sessions() []*core.PlaySession {
	out := p.localSessions()
	// Other cluster nodes' streams (nil on a standalone server).
	if p.app != nil && p.app.playback != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		for _, s := range p.app.playback.RemoteSessions(ctx) {
			if core.SessionVisible(s) && !liveProgrammeSource(s) {
				out = append(out, s)
			}
		}
		cancel()
	}
	return out
}

// activeSessions returns only actively playing sessions under the shared
// liveness rule (excluding paused, starting/abandoned, idle, and stopped).
func (p ownerPlayback) activeSessions() []*core.PlaySession {
	all := p.Sessions()
	out := make([]*core.PlaySession, 0, len(all))
	for _, s := range all {
		if core.SessionActiveStream(s) {
			out = append(out, s)
		}
	}
	return out
}

// activeLocalSessions returns this node's actively playing sessions only.
func (p ownerPlayback) activeLocalSessions() []*core.PlaySession {
	all := p.localSessions()
	out := make([]*core.PlaySession, 0, len(all))
	for _, s := range all {
		if core.SessionActiveStream(s) {
			out = append(out, s)
		}
	}
	return out
}

// localSessions is this node's share of Sessions: in-memory snapshots only,
// no cluster calls, so the console stream can watch it cheaply. Both Overview
// and Activity read this same shared liveness filter (playing + explicitly paused).
func (p ownerPlayback) localSessions() []*core.PlaySession {
	var live []*core.PlaySession
	if p.app != nil && p.app.livetv != nil {
		live = p.app.livetv.ViewingSessions()
	}
	groups := map[string]bool{}
	for _, s := range live {
		groups[s.ID] = true
	}
	out := make([]*core.PlaySession, 0, len(live))
	backend := p.Playback
	if p.app != nil {
		if p.app.brokerPlayback != nil {
			backend = p.app.brokerPlayback
		} else if p.app.spikeLibrary != nil {
			backend = p.app.spikeLibrary.Playback()
		}
	}
	if manager, ok := backend.(*playback.Manager); ok && manager == nil {
		backend = nil
	}
	var local []*core.PlaySession
	if backend != nil {
		local = backend.Sessions()
	}
	for _, s := range local {
		if !core.SessionVisible(s) || (s.PlaybackGroup != "" && groups[s.PlaybackGroup]) || liveProgrammeSource(s) {
			continue
		}
		out = append(out, s)
	}
	for _, s := range live {
		if core.SessionVisible(s) {
			out = append(out, s)
		}
	}
	return out
}

// liveProgrammeSource recognises the source a Live TV channel opens to feed its
// broadcast (the programme it is airing). It belongs to the channel's viewing
// row, not to a viewer of its own, even when the backend could not group it.
func liveProgrammeSource(s *core.PlaySession) bool {
	return strings.HasPrefix(s.ClientID, "channel-input/") || strings.HasPrefix(s.Client.ClientID, "channel-input/")
}

func (p ownerPlayback) Session(id string) (*core.PlaySession, bool) {
	for _, s := range p.Sessions() {
		if s.ID == id {
			return s, true
		}
	}
	return nil, false
}

const ownerStopMessage = "The server owner has ended this stream."

func (a *App) stopOwnerSession(id string) error {
	return a.terminateSession(context.Background(), id, "owner", "owner_stopped", ownerStopMessage)
}

// stopOwnerSessionWithReason stops a session with a custom client-visible
// message (Flows' playback.stop_session action, Plex terminate). It shares
// terminateSession's single stop path, so playback.session.terminated fires
// exactly once.
func (a *App) stopOwnerSessionWithReason(ctx context.Context, id, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = ownerStopMessage
	}
	return a.terminateSession(ctx, id, "owner", "owner_stopped", reason)
}

func (a *App) terminateSession(ctx context.Context, id, actor, reason, message string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var viewerID int64
	if ps, ok := (ownerPlayback{Playback: a.playback, app: a}).Session(id); ok && ps != nil {
		viewerID = ps.UserID
	}
	if a.frontends != nil {
		a.frontends.noteOwnerStop(id, message)
		if a.frontends.broker != nil {
			a.frontends.broker.StopPlayback(ctx, id)
		}
	}
	var stopErr error
	stoppedLive := false
	if a.livetv != nil {
		for _, s := range a.livetv.ViewingSessions() {
			if s.ID == id {
				stopErr = a.livetv.Stop(ctx, id, &core.User{IsOwner: true})
				stoppedLive = true
				break
			}
		}
	}
	if !stoppedLive {
		// Broker retirement can leave the source on the owner list. The named
		// session still has to stop.
		if a.brokerPlayback != nil {
			a.brokerPlayback.Stop(id)
		} else if a.spikeLibrary != nil {
			a.spikeLibrary.Playback().Stop(id)
		} else if a.playback != nil {
			a.playback.Stop(id)
		}
	}
	a.publishEvent(
		events.EventPlaybackSessionTerminated,
		events.PlaybackSessionTerminatedPayload{
			SessionID: id,
			PlayID:    id,
			ViewerID:  viewerID,
			Actor:     actor,
			Reason:    reason,
			Message:   message,
		},
		events.WithCorrelation(events.Correlation{
			SessionID: id,
			PlayID:    id,
			ViewerID:  viewerID,
		}),
	)
	return stopErr
}
