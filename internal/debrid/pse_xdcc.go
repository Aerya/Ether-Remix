package debrid

// Ether-Remix : source XDCC en option (internal/pse/xdcc). Ses releases sont
// des flux HTTP à la demande servis par Remix lui-même sur 127.0.0.1 ; avant
// la sonde, la source prépare la release en mémoire : elle lance ou rejoint
// le téléchargement, attend le tampon, renvoie un message précis (file
// d'attente d'un bot, progression, échec) et donne un lien local à jeton
// valable pour ce processus.

import (
	"context"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/core"
)

// PSEDirectPrepare prépare une release HTTP ; handled est faux pour une
// release qui n'appartient pas à la source.
type PSEDirectPrepare func(ctx context.Context, rel core.Release) (out core.Release, handled bool, err error)

var pseDirectPrepare atomic.Pointer[PSEDirectPrepare]

// SetPSEDirectPrepare installe (ou retire, avec nil) la préparation XDCC.
func SetPSEDirectPrepare(f PSEDirectPrepare) {
	if f == nil {
		pseDirectPrepare.Store(nil)
		return
	}
	pseDirectPrepare.Store(&f)
}

func psePrepareDirect(ctx context.Context, rel core.Release) (core.Release, error) {
	f := pseDirectPrepare.Load()
	if f == nil {
		return rel, nil
	}
	out, handled, err := (*f)(ctx, rel)
	if !handled {
		return rel, nil
	}
	return out, err
}
