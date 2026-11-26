package frontend

import (
	"context"
	"sync/atomic"
)

// Liaison Nuvio (internal/pse/nuvio). Le frontend Stremio ne peut pas
// importer internal/pse : l'application branche ici la fonction qui marque
// une écriture venue de Nuvio, pour qu'elle ne lui soit pas renvoyée.
// Voir docs/pse/FORK.fr.md.

var pseNuvioIncoming atomic.Pointer[func(user int64, key RatingKey)]

// SetPSENuvioIncoming branche (ou, avec nil, retire) le marqueur.
func SetPSENuvioIncoming(fn func(user int64, key RatingKey)) {
	if fn == nil {
		pseNuvioIncoming.Store(nil)
		return
	}
	pseNuvioIncoming.Store(&fn)
}

// PSENuvioIncoming signale qu'une écriture sur key vient de Nuvio.
func PSENuvioIncoming(user int64, key RatingKey) {
	if fn := pseNuvioIncoming.Load(); fn != nil {
		(*fn)(user, key)
	}
}

// PSENuvioBackend écrit l'historique du spectateur d'un lien Nuvio sans test de
// visibilité : le contrôle parental évolue dans le temps et ne s'applique
// qu'à la lecture, l'historique d'un compte est conservé entièrement.
type PSENuvioBackend interface {
	// Resolve : kind « movie » ou « episode » (saison et épisode alors donnés).
	Resolve(ctx context.Context, imdb, kind string, season, episode int) (RatingKey, bool)
	Progress(ctx context.Context, user int64, key RatingKey, p OfflineProgress) error
	Played(ctx context.Context, user int64, key RatingKey, played bool) error
}

var pseNuvioBackend atomic.Pointer[PSENuvioBackend]

// SetPSENuvioBackend branche (ou, avec nil, retire) l'écriture directe.
func SetPSENuvioBackend(b PSENuvioBackend) {
	if b == nil {
		pseNuvioBackend.Store(nil)
		return
	}
	pseNuvioBackend.Store(&b)
}

// PSENuvio renvoie l'écriture directe branchée, ou nil.
func PSENuvio() PSENuvioBackend {
	if b := pseNuvioBackend.Load(); b != nil {
		return *b
	}
	return nil
}
