package frontend

import "sync/atomic"

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
