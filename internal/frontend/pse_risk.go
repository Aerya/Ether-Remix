package frontend

import (
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/core"
)

// Liaison entre les clients (Plex, Jellyfin, affiches) et les titres
// risqués de PSE (internal/pse/releasedb/risk.go), qu'ils n'importent pas
// directement — voir docs/pse/FORK.fr.md. L'application publie
// l'implémentation avec SetPSERisk.

// PSERisk signale les titres affichés avec moins de releases que demandé.
type PSERisk interface {
	// MarkSummary renvoie le résumé, précédé d'un avertissement si besoin.
	MarkSummary(key core.RatingKey, summary string) string
	// PosterRisk renvoie le badge d'affiche ("" sans risque).
	PosterRisk(key core.RatingKey) string
}

type pseRiskBox struct{ r PSERisk }

var pseRisk atomic.Pointer[pseRiskBox]

// SetPSERisk publie l'implémentation active (nil la retire).
func SetPSERisk(r PSERisk) { pseRisk.Store(&pseRiskBox{r: r}) }

// PSEMarkSummary applique l'avertissement des titres risqués au résumé.
func PSEMarkSummary(key core.RatingKey, summary string) string {
	if b := pseRisk.Load(); b != nil && b.r != nil {
		return b.r.MarkSummary(key, summary)
	}
	return summary
}

// PSEPosterRisk renvoie le badge d'affiche d'un titre risqué.
func PSEPosterRisk(key core.RatingKey) string {
	if b := pseRisk.Load(); b != nil && b.r != nil {
		return b.r.PosterRisk(key)
	}
	return ""
}
