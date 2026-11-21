package frontend

import "github.com/braedonsaunders/ether/internal/pse/continuewatch"

// Ajout Remix — voir docs/pse/FORK.fr.md. Réglages de « Continuer à
// regarder » (séries examinées, périodes, quantités), lus à chaque demande.

// PSEContinueSettings sont les réglages de la rangée « Continuer à regarder ».
type PSEContinueSettings = continuewatch.Settings

// PSEContinueSettingsNow lit les réglages effectifs.
func PSEContinueSettingsNow() PSEContinueSettings { return continuewatch.Load() }
