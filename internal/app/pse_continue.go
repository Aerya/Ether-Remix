package app

import (
	"fmt"
	"log/slog"

	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/pse/continuewatch"
)

// Ajout Remix — voir docs/pse/FORK.fr.md. Branche les réglages de « Continuer
// à regarder » (internal/pse/continuewatch) sur la rangée des surfaces
// construites par le service d'expérience. Les réglages sont relus à chaque
// demande : pas de redémarrage.

// pseContinueSettings lit les réglages à chaque demande.
var pseContinueSettings = continuewatch.Load

// psePrepareContinue renseigne les réglages d'une rangée construite par le service.
func psePrepareContinue(c frontend.Continue) frontend.Continue {
	if ec, ok := c.(experience.Continue); ok {
		ec.PSE = pseContinueSettings
		return ec
	}
	if c != nil {
		slog.Warn("continue watching settings not applied: unexpected selector type", "type", fmt.Sprintf("%T", c))
	}
	return c
}
