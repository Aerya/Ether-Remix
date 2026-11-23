package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
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

// startPSEContinue démarre la tâche qui tient à jour la table « à suivre » de
// « Continuer à regarder » (remplissage initial, passe incrémentale toutes les
// 30 s, nouveaux épisodes toutes les 30 min). Tant que la table n'est pas
// remplie pour un spectateur, la rangée garde le calcul d'Ether.
func (a *App) startPSEContinue() {
	if a.db == nil {
		return
	}
	s := &continuewatch.Store{DB: a.db, Pause: 200 * time.Millisecond}
	if a.catalog != nil {
		// Charge la saison suivante des séries encore diffusées, en arrière-plan.
		s.Refresh = func(ctx context.Context, keys []core.RatingKey) { _, _ = a.catalog.ContinueEpisodes(ctx, keys) }
	}
	s.Busy = func() bool { return a.playback != nil && len(a.playback.Sessions()) > 0 }
	continuewatch.SetDefault(s)
	ctx, cancel := context.WithCancel(context.Background())
	a.closers = append(a.closers, func() { cancel(); continuewatch.SetDefault(nil) })
	go s.Run(ctx, func(what string, err error) {
		a.log.Warn("continuer à regarder : "+what, "err", err)
	})
}
