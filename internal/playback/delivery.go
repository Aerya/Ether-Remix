package playback

// Direct delivery (plex.delivery = "redirect"): a frontend hands the client
// the source's own provider URL instead of the bytes. Whether that is allowed is
// a playback question, not a routing one — only this Manager knows which file
// the client's byte coordinates belong to — so it answers here and the frontend
// only turns the answer into a redirect.

import (
	"github.com/braedonsaunders/ether/internal/core"
)

// DirectDelivery implements core.DirectDeliverer. It reports the provider URL a
// direct play of this session can be redirected to: the file the client was
// given byte coordinates for, with a link the engine still considers fresh.
//
// The two marks are part of the answer, not a side effect. A redirected read
// really is a direct play of a session the client is watching; without them a
// session whose bytes never pass through Ether would be recorded as never having
// played, and would age out like one nobody touched.
func (m *Manager) DirectDelivery(sessionID string) (core.DirectSourceLink, bool) {
	if sessionID == "" {
		return core.DirectSourceLink{}, false
	}
	s := m.lookup(sessionID)
	if s == nil {
		return core.DirectSourceLink{}, false
	}
	g, ok := s.directDeliveryGeneration()
	if !ok {
		return core.DirectSourceLink{}, false
	}
	src, ok := g.src.(core.DirectURLSource)
	if !ok {
		return core.DirectSourceLink{}, false
	}
	link, ok := src.DirectURL()
	if !ok || link.URL == "" {
		return core.DirectSourceLink{}, false
	}
	now := m.now()
	if s.markDirectPlay() {
		m.emitStreamDecision(s, "", core.Decision{
			Kind:      core.DecisionDirectPlay,
			VideoCopy: true,
			AudioCopy: true,
			Reason:    "Direct URL delivery",
		})
	}
	s.touchMedia(now)
	return link, true
}

// directDeliveryGeneration reports the generation a redirect may name: the one
// the session was adopted with, whose bytes are the coordinates the client holds.
// The relay can keep serving a later generation at those coordinates because it
// maps them (toSource); a redirect hands the client's Range to the provider's
// file as it stands, so only the original file answers correctly. That is
// deliberately stricter than directRangeGeneration, which stays satisfied
// through a spliceable TS release swap precisely because the relay can remap it.
// A remux handoff is excluded for the same reason as the relay: it owes the
// client a 409 and a new decision, not somebody else's bytes.
func (s *session) directDeliveryGeneration() (*generation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur, s.cur != nil && !s.requiresRemux && s.cur == s.virtualGen
}
