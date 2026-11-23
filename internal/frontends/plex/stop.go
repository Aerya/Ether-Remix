package plex

// NoteOwnerStop tells the Plex client why a stream is ending using the same
// termination delivery path as /status/sessions/terminate. The shared broker
// stops the source.
func (s *Server) NoteOwnerStop(id, reason string) {
	if id == "" {
		return
	}
	if reason == "" {
		reason = "The server owner has ended this stream."
	}
	if ltv := s.svc().liveTV; ltv != nil {
		for _, v := range ltv.ViewingSessions() {
			if v.ID == id {
				return
			}
		}
	}
	if pb := s.playback(); pb != nil {
		if ps, ok := pb.Session(id); ok && ps != nil {
			unlock, err := s.lockPlaybackOwner(s.ctx, ps.UserID, ps.ClientID)
			if err != nil {
				return
			}
			defer unlock()
			for _, e := range s.sessions.list() {
				if found, ok := pb.SessionFor(e.user.ID, e.client.ClientID, e.key); ok && found.ID == id {
					s.setOwnerTermination(e.id, reason)
				}
			}
			return
		}
	}
	// Fall back to direct Plex sessionId matching (local, then cluster remote).
	if s.terminateLocal(id, reason) {
		return
	}
	if s.cl != nil {
		for _, e := range s.cl.remoteEntries(s.ctx) {
			if matchesSession(e, id) {
				s.cl.publish(topicTerminate, terminateEvent{Scope: "plex", ID: id, Reason: reason, Node: e.node})
				return
			}
		}
	}
}

func (s *Server) liveTVAPIVersion() int {
	if s.liveTVEnabled() {
		return 4
	}
	return 0
}
