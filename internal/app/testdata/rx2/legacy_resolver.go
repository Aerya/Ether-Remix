package resolver

import (
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// RX2LegacyDecision exposes the released, library-aware internal decision
// without reimplementing its profile or limit selection in the audit.
func RX2LegacyDecision(q config.Quality, caps core.ClientCaps, user *core.User, title *core.Title, libraryProfile string) (core.QualityProfile, core.ClientCaps) {
	decision := decideProfile(q, caps, user, titleMedia(title), libraryProfile)
	return decision.profile, decision.caps
}
