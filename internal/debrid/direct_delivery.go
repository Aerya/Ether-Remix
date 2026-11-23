package debrid

// Direct delivery: handing a client the provider's own link for a file instead
// of streaming its bytes through Ether (core.DirectURLSource).
//
// The first version vouches for one shape only: a provider-issued TorBox HTTPS
// link, on the route the file was minted onto. A direct HTTP stream is read
// through a safefetch guard with the addon's own request headers, and a plugin
// provider mints its URL for Ether's client — neither promises a player can
// fetch it unaided. A replay edge is excluded deliberately: Ether reads that
// route with redirects refused (the replayRead branch in engine.go), so handing
// it to a player would send it to a host Ether itself refuses to follow, and
// answer a Range in coordinates nobody mapped.

import (
	"net/url"

	"github.com/braedonsaunders/ether/internal/core"
)

// DirectURL implements core.DirectURLSource. The link is the file's current
// route and must still be fresh by the engine's own margin: an expired
// signature is worse for a client than a relayed read, because a player has no
// way to ask Ether for a new one.
func (s *httpSource) DirectURL() (core.DirectSourceLink, bool) {
	f := s.f
	f.mu.Lock()
	p, closed := f.pl, f.closed
	f.mu.Unlock()
	if closed || p.Kind != "torbox" || p.Provider == "" || p.OnReplayEdge || p.Zip != nil || !f.e.svc.fresh(p) {
		return core.DirectSourceLink{}, false
	}
	dest, err := url.Parse(p.URL)
	if err != nil || dest.Scheme != "https" || dest.Host == "" || dest.User != nil {
		return core.DirectSourceLink{}, false
	}
	// The URL travels as the provider wrote it: re-encoding a signed link risks
	// something and buys nothing. Edge is for the log, which must not carry the
	// signature.
	return core.DirectSourceLink{URL: p.URL, Provider: s.Origin(), Edge: dest.Host, Expires: p.Expires}, true
}
