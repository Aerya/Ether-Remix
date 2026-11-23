package plex

// Direct delivery (plex.delivery = "redirect"): an authorised direct-play part
// read can hand the client a temporary TorBox file URL. The player then
// fetches the bytes directly from the provider.
//
// Ether keeps every decision above the bytes. Both hops below run at the end of
// handlePart, after authorisation, visibility, the viewer's stream limit, broker
// admission and the selection of the exact file that version plays, so a request
// that has not earned the read never learns the link — and the link itself,
// which is signed, never reaches the log.
//
// A URL-authenticated read takes two hops, because a redirected request names the
// URL that redirected it (delivery_grant.go); a read with its Plex credential in
// a header relays, because only the client knows which of its headers survive the
// trip to a foreign host.

import (
	"net/http"
	"net/url"
	"regexp"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// partDirectDelivery answers r with a redirect when direct delivery is configured
// and this session's file has a live provider URL. It reports whether it answered
// the request; false means the caller serves the bytes.
func (s *Server) partDirectDelivery(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	if !s.cfg().Plex.Redirects() {
		return false
	}
	pr := FromContext(r.Context())
	if pr == nil {
		return false // handlePart has already answered without a request context
	}
	// Before any hop that leaves for another host: a header credential may
	// travel with the client, and the provider's edge is not Ether's host.
	if requestHeaderCredential(r) || !providerRangeRequest(r) {
		return false
	}
	if pr.deliveryGrantHop {
		return s.redirectToProvider(w, r, sessionID)
	}
	if !requestQueryCredential(r.URL.Query()) {
		// No URL credential of ours to hide: a read authenticated by a stream
		// grant or a read capability keeps relaying in this version.
		return false
	}
	if _, ok := s.directDelivery(sessionID); !ok {
		return false
	}
	location := s.deliveryGrants.mint(pr.User.ID, r)
	if location == "" {
		return false
	}
	// A grant is spent the moment it is used, so no cache downstream may answer
	// this redirect a second time.
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, location, http.StatusTemporaryRedirect)
	s.log.Info("part redirected to delivery grant", "session", sessionID)
	return true
}

// redirectToProvider answers the granted second hop with the provider's own URL.
func (s *Server) redirectToProvider(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	link, ok := s.directDelivery(sessionID)
	if !ok {
		return false
	}
	// A signed link expires, so nothing downstream should hold this answer and
	// hand the client a dead signature later. And no referrer: the URL the player
	// is about to name to the provider carries a grant that is already spent, and
	// the provider's edge has no need to read even that.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, link.URL, http.StatusTemporaryRedirect)
	// The log records which edge the player was sent to, never the signed link.
	s.log.Info("part redirected to provider", "session", sessionID, "provider", link.Provider, "edge", link.Edge)
	return true
}

// directDelivery asks the playback which provider URL this session's file may be
// fetched from. A playback that cannot answer — a remote cluster session, a
// Usenet source, a session that moved to another generation or needs a remux —
// keeps relaying, and answers with its own status (409 included).
func (s *Server) directDelivery(sessionID string) (frontend.DirectSourceLink, bool) {
	deliverer, ok := s.playback().(frontend.DirectDeliverer)
	if !ok {
		return frontend.DirectSourceLink{}, false
	}
	link, ok := deliverer.DirectDelivery(sessionID)
	if !ok || link.URL == "" {
		return frontend.DirectSourceLink{}, false
	}
	if dest, err := url.Parse(link.URL); err != nil || dest.Host == "" {
		return frontend.DirectSourceLink{}, false
	}
	return link, true
}

// Plex ignores conditional validators and has its own response for multi-range
// and nonstandard Range requests. A CDN can instead send a complete movie,
// a multipart body, or an error. Keep those requests on Plex's range handler.
var providerByteRange = regexp.MustCompile(`^bytes=([0-9]+-[0-9]*|-[1-9][0-9]*)$`)

func providerRangeRequest(r *http.Request) bool {
	for _, field := range []string{"If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		if len(r.Header.Values(field)) > 0 {
			return false
		}
	}
	ranges := r.Header.Values("Range")
	return len(ranges) == 0 || (len(ranges) == 1 && providerByteRange.MatchString(ranges[0]))
}
