package vela

import (
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func (i *instance) commandLive(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, s *playSession, kind string, target *float64) {
	id := r.PathValue("id")
	tuneID := id
	if s.archive != nil {
		tuneID = s.archive.tuneID
	}
	if !s.portSnapshot(w, func(copy *playSession) { i.refreshLiveWindow(r.Context(), tuneID, copy) }) {
		return
	}
	alignUnobservedLivePosition(s)
	start, edge, hasWindow := liveWindowBounds(s.plan["live"])
	paused := s.plan["state"] == "paused"
	if hasWindow && paused && s.position < durationFromSeconds(start) && (kind == "resume" || kind == "seek") {
		// Give the client a recovery point for its next resume or seek.
		s.position = durationFromSeconds(start)
		s.observedSource = s.source
		s.publish("plan")
		timeshiftConflict(w, "timeshift_position_evicted", "The paused position is no longer in the live window.", start, edge)
		return
	}

	switch kind {
	case "seek", "goLive":
		if kind == "seek" && target == nil {
			problem(w, 400, "request", "invalid_position")
			return
		}
		if !hasWindow {
			i.unavailable(w, r)
			return
		}
		at := edge
		if kind == "seek" {
			at = *target
			if at < start || at > edge {
				timeshiftConflict(w, "timeshift_outside_window", "Position is outside the live window.", start, edge)
				return
			}
		}
		s.position = durationFromSeconds(at)
		s.observedSource = s.source
		s.seek++
	case "pause", "resume":
		state := "paused"
		if kind == "resume" {
			state = "playing"
		}
		position, archive := s.position, s.archive != nil
		access := i.access(r, v)
		var err error
		if !s.port(w, func() {
			if archive {
				err = i.playbackKeepAlive(r.Context(), access, id, s)
				return
			}
			err = i.host.Sessions.Report(r.Context(), access, id, frontend.Timeline{Position: position, State: state})
		}) {
			return
		}
		if err != nil {
			i.failFor(w, v, err, r)
			return
		}
		s.plan["state"] = state
	}
	s.publish("plan")
	send(w, http.StatusOK, s.plan)
}

func timeshiftConflict(w http.ResponseWriter, code, message string, start, edge float64) {
	send(w, http.StatusConflict, object{"error": object{
		"class": "conflict", "code": code, "message": message, "retry": object{"allowed": false},
		"windowStartSeconds": start, "liveEdgeSeconds": edge,
	}})
}
