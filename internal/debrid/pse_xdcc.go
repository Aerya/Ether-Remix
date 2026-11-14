package debrid

// Ether-Remix : sources locales en option (internal/pse/xdcc, internal/pse/ftp).
// Leurs releases sont des flux HTTP à la demande servis par Remix lui-même
// sur 127.0.0.1 ; avant la sonde, la source prépare la release en mémoire :
// elle vérifie le fichier, lance ou rejoint un téléchargement, renvoie un
// message précis (file d'attente, progression, échec) et donne un lien
// local à jeton valable pour ce processus.

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/core"
)

// PSEDirectPrepare prépare une release HTTP ; handled est faux pour une
// release qui n'appartient pas à la source.
type PSEDirectPrepare func(ctx context.Context, rel core.Release) (out core.Release, handled bool, err error)

var (
	pseDirectMu      sync.Mutex
	pseDirectByName  = map[string]PSEDirectPrepare{}
	pseDirectPrepare atomic.Pointer[[]PSEDirectPrepare]
)

// SetPSEDirectPrepare installe (ou retire, avec nil) la préparation XDCC.
func SetPSEDirectPrepare(f PSEDirectPrepare) { SetPSEDirectPrepareFor("xdcc", f) }

// SetPSEDirectPrepareFor installe (ou retire, avec nil) la préparation
// d'une source nommée ; chaque source reconnaît ses propres releases.
func SetPSEDirectPrepareFor(name string, f PSEDirectPrepare) {
	pseDirectMu.Lock()
	defer pseDirectMu.Unlock()
	if f == nil {
		delete(pseDirectByName, name)
	} else {
		pseDirectByName[name] = f
	}
	names := make([]string, 0, len(pseDirectByName))
	for n := range pseDirectByName {
		names = append(names, n)
	}
	sort.Strings(names)
	list := make([]PSEDirectPrepare, 0, len(names))
	for _, n := range names {
		list = append(list, pseDirectByName[n])
	}
	pseDirectPrepare.Store(&list)
}

func psePrepareDirect(ctx context.Context, rel core.Release) (core.Release, error) {
	list := pseDirectPrepare.Load()
	if list == nil {
		return rel, nil
	}
	for _, f := range *list {
		out, handled, err := f(ctx, rel)
		if handled {
			return out, err
		}
	}
	return rel, nil
}
