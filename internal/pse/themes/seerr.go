package themes

import (
	"context"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// Titres SeerrCatalog : SeerrCatalog trouve la VF quand elle existe, et à
// défaut une version de secours (VOSTFR, VO + sous-titres). Ether-Remix tient
// deux familles de collections (films, séries) :
//   - « SeerrCatalog » : tous les titres disponibles ;
//   - « SeerrCatalog · VOSTFR » : ceux trouvés seulement en version de
//     secours, rangés dans la bibliothèque « VOSTFR ».
// Les deux sont des exceptions de langue : un titre sans VF reste visible
// quand on les ouvre, mais n'apparaît jamais dans Films, Séries ou l'accueil.

const (
	SeerrName       = "SeerrCatalog"
	SeerrVostfrName = "SeerrCatalog · VOSTFR"
	// maxSeerr suit la limite des collections de titres d'Ether.
	maxSeerr = 5000
)

func seerrSpec(specs []core.CollectionSpec, name, typ string) *core.CollectionSpec {
	for i := range specs {
		sp := &specs[i]
		if sp.OwnerUserID == 0 && sp.MediaType == typ && strings.EqualFold(sp.Name, name) &&
			len(sp.Sources) == 1 && sp.Sources[0].Kind == "titles" {
			return sp
		}
	}
	return nil
}

func seerrNewSpec(name, typ string, keys []string) core.CollectionSpec {
	sp := specFor(Entry{Name: name}, typ)
	sp.Description = "Titres disponibles d'après SeerrCatalog, gérés par Ether-Remix."
	if name == SeerrVostfrName {
		sp.Description = "Titres que SeerrCatalog n'a trouvés qu'en VOSTFR (ou VO sous-titrée), gérés par Ether-Remix."
	}
	sp.Sources = []core.CollectionSource{{Kind: "titles", Keys: keys}}
	sp.Refresh = "hourly"
	return sp
}

func seerrKeys(keys []core.RatingKey, kind core.KeyKind) []string {
	out := []string{}
	for _, k := range keys {
		if k.Kind() == kind && len(out) < maxSeerr {
			out = append(out, k.String())
		}
	}
	return out
}

// SyncSeerr met les collections SeerrCatalog à jour : vf liste les titres
// trouvés dans la langue principale, fallback ceux trouvés seulement en
// version de secours. Une collection vide est désactivée, pas supprimée.
func (s *Service) SyncSeerr(ctx context.Context, ed Editor, vf, fallback []core.RatingKey) error {
	if ed == nil || s.cols == nil {
		return nil
	}
	specs, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return err
	}
	all := append(append([]core.RatingKey{}, fallback...), vf...)
	var free, vostfr []string
	for _, typ := range []struct {
		name string
		kind core.KeyKind
	}{{TypeMovie, core.KindMovie}, {TypeShow, core.KindShow}} {
		id, err := s.seerrSet(ctx, ed, specs, SeerrName, typ.name, seerrKeys(all, typ.kind))
		if err != nil {
			return err
		}
		if id != "" {
			free = append(free, id)
		}
		id, err = s.seerrSet(ctx, ed, specs, SeerrVostfrName, typ.name, seerrKeys(fallback, typ.kind))
		if err != nil {
			return err
		}
		if id != "" {
			free = append(free, id)
			vostfr = append(vostfr, id)
		}
	}
	if err := s.Reload(ctx); err != nil {
		return err
	}
	if err := s.addExceptions(ctx, free); err != nil {
		return err
	}
	for _, id := range vostfr {
		if err := s.vostfrWire(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// seerrSet crée ou met à jour une collection et renvoie son identifiant ;
// vide quand elle n'existe pas et qu'il n'y a rien à y mettre.
func (s *Service) seerrSet(ctx context.Context, ed Editor, specs []core.CollectionSpec, name, typ string, keys []string) (string, error) {
	sp := seerrSpec(specs, name, typ)
	if len(keys) == 0 {
		if sp == nil {
			return "", nil
		}
		if sp.Enabled {
			next := *sp
			next.Enabled = false
			if _, err := ed.UpdateSpec(ctx, sp.ID, next); err != nil {
				return "", err
			}
		}
		return sp.ID, nil
	}
	if sp == nil {
		created, err := ed.CreateSpec(ctx, seerrNewSpec(name, typ, keys))
		if err != nil {
			return "", err
		}
		return created.ID, nil
	}
	if sp.Enabled && slices.Equal(sp.Sources[0].Keys, keys) {
		return sp.ID, nil
	}
	next := *sp
	next.Enabled = true
	next.Sources = []core.CollectionSource{{Kind: "titles", Keys: keys}}
	if _, err := ed.UpdateSpec(ctx, sp.ID, next); err != nil {
		return "", err
	}
	return sp.ID, nil
}

// addExceptions déclare des collections en exception de langue.
func (s *Service) addExceptions(ctx context.Context, ids []string) error {
	free := s.Exceptions()
	n := len(free)
	for _, id := range ids {
		if !slices.Contains(free, id) {
			free = append(free, id)
		}
	}
	if len(free) == n {
		return nil
	}
	return s.SetExceptions(ctx, free)
}
