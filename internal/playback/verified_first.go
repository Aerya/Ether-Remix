package playback

import (
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// verifiedFilesFirst reports resolver.verifiedFiles "first" (the default):
// a verified AllDebrid file that passed the hard gates leads every other
// source. The resolver already ranks that way; what it cannot see is a choice
// remembered earlier (a warm-up or the resume record), which this package
// replays without ranking again.
func (m *Manager) verifiedFilesFirst() bool {
	switch strings.ToLower(strings.TrimSpace(m.config().Resolver.VerifiedFiles)) {
	case "off", "tie":
		return false
	}
	return true
}

// verifiedFileOutranks reports a freshly ranked release that must replace a
// remembered one: with verifiedFiles "first", a verified file beats a
// remembered release that is not one (typically an add-on stream chosen
// before the file could be offered).
func (m *Manager) verifiedFileOutranks(fresh, remembered core.Release) bool {
	return m.verifiedFilesFirst() && core.IsVerifiedFile(fresh) && !core.IsVerifiedFile(remembered)
}

// isAddonStream reports a Stremio add-on stream link that is not a verified
// AllDebrid file: the kind of remembered choice a verified file replaces.
func isAddonStream(r core.Release) bool {
	return r.Protocol == core.ProtocolHTTP && r.StreamURL != "" && !core.IsVerifiedFile(r)
}
