package app

import (
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Ajout Remix — voir docs/pse/FORK.fr.md. Branche les réglages de « Continuer
// à regarder » (internal/pse/continuewatch) sur la rangée des surfaces
// construites par le service d'expérience. Les réglages sont relus à chaque
// demande : pas de redémarrage.

// psePrepareContinue renseigne les réglages d'une rangée construite par le service.
func psePrepareContinue(c frontend.Continue) frontend.Continue {
	if ec, ok := c.(experience.Continue); ok {
		ec.PSE = frontend.PSEContinueSettingsNow
		return ec
	}
	return c
}
