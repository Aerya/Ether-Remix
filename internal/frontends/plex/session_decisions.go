package plex

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func decisionOwner(pr *Request) frontend.DecisionOwner {
	if pr.User == nil {
		return frontend.DecisionOwner{}
	}
	return frontend.DecisionOwner{ID: trackID(pr.User.ID, pr.ClientID, pr.SessionID), UserID: pr.User.ID, ClientID: pr.ClientID, SessionID: pr.SessionID}
}
func (s *Server) armDecision(pr *Request, key frontend.RatingKey) {
	s.decisionArms.Arm(decisionOwner(pr), key)
}
func (s *Server) decisionArmed(pr *Request, key frontend.RatingKey) bool {
	return s.decisionArms.Armed(decisionOwner(pr), key)
}
func (s *Server) disarmDecision(pr *Request, key frontend.RatingKey) {
	s.decisionArms.Disarm(decisionOwner(pr), key)
}
func (s *Server) refreshDecisionArms(now time.Time) { s.decisionArms.Refresh(now) }
func (s *Server) clearOwnDecisionArms()             { s.decisionArms.ClearOwn() }

// admitStart makes the decision a start request implies when the player sent
// none, as PMS does: Plex clients may open start.m3u8/start.mpd/start
// directly. It is handleDecision's admission with the start's own
// parameters: arm this player and title for the start's delivery generation,
// then wait for the source outside the owner gate. The start itself then
// prepares through the same policy path a decision uses, and keeps its own
// superseded (410) and session ownership (403) checks.
//
// The arm still scopes a decision to one player and title. What it newly
// refuses is a start for a delivery the player has already stopped: that is
// a late request from a finished playback, not a new one, and must not
// reopen it. A new playback uses a new session= value, or decides first.
//
// It reports false once it has answered the request.
func (s *Server) admitStart(w http.ResponseWriter, r *http.Request) bool {
	ctx := r.Context()
	pr := FromContext(ctx)
	if pr == nil || pr.User == nil {
		return true // lockPlaybackRequest answers 401
	}
	p, err := s.parsePlayRequest(r)
	if err != nil {
		return true // the start's own parse answers 400
	}
	if s.decisionArmed(pr, p.key) {
		return true
	}
	owner := decisionOwner(pr)
	owner.Generation = p.sessionID
	if s.decisionArms.Stopped(owner, p.key) {
		writePlaybackStatus(w, http.StatusBadRequest)
		return false
	}
	if tt := chi.URLParam(r, "ttype"); tt != "video" && tt != "music" && tt != "audio" {
		return true // nothing to decide; the start refuses it unarmed
	}
	s.decisionArms.Arm(owner, p.key)
	if isPlayableKey(p.key) {
		s.recentClients.recordTitle(r, pr, p.key, s.now())
	}
	if _, err := s.awaitSource(ctx, r, pr); err != nil {
		if ctx.Err() != nil {
			return false
		}
		s.log.Info("start playback failed", "key", p.key, "reason", playbackMessage(err), "err", err, "decision", "implicit")
		s.writeStartErr(w, r, pr, err)
		return false
	}
	return true
}
