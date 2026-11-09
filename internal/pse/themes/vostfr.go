package themes

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// Titres en VOSTFR : le spectateur choisit des films et séries précis, sans
// passer par l'éditeur de collections. Ether-Remix tient pour lui deux
// collections « VOSTFR » (films, séries) à source « titres », les déclare en
// exception de langue (elles apparaissent alors dans Films et Séries même
// sans VF) et les range dans une bibliothèque à thème « VOSTFR ».

// VostfrName nomme les collections et la bibliothèque gérées.
const VostfrName = "VOSTFR"

// MaxVostfr borne le nombre de titres choisis.
const MaxVostfr = 500

// Editor crée et modifie les collections d'Ether.
type Editor interface {
	Creator
	UpdateSpec(ctx context.Context, id string, sp core.CollectionSpec) (*core.CollectionSpec, error)
}

// vostfrSpec retrouve la collection gérée d'un type (movie ou show).
func vostfrSpec(specs []core.CollectionSpec, typ string) *core.CollectionSpec {
	for i := range specs {
		sp := &specs[i]
		if sp.OwnerUserID == 0 && sp.MediaType == typ && strings.EqualFold(sp.Name, VostfrName) &&
			len(sp.Sources) == 1 && sp.Sources[0].Kind == "titles" {
			return sp
		}
	}
	return nil
}

// Vostfr renvoie les titres choisis, films puis séries.
func (s *Service) Vostfr(ctx context.Context) ([]string, error) {
	if s.cols == nil {
		return []string{}, nil
	}
	specs, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, typ := range []string{TypeMovie, TypeShow} {
		if sp := vostfrSpec(specs, typ); sp != nil {
			out = append(out, sp.Sources[0].Keys...)
		}
	}
	return out, nil
}

func vostfrNewSpec(typ string, keys []string) core.CollectionSpec {
	sp := specFor(Entry{Name: VostfrName}, typ)
	sp.Description = "Titres choisis en VOSTFR, gérés par Ether-Remix (Ether-Remix › VOSTFR à la demande)."
	sp.Sources = []core.CollectionSource{{Kind: "titles", Keys: keys}}
	sp.Refresh = "hourly"
	return sp
}

// SetVostfr ajoute (ou retire) un titre de la sélection VOSTFR. Pour un
// retrait, le type peut être vide : le titre est cherché dans les deux.
func (s *Service) SetVostfr(ctx context.Context, ed Editor, key, typ string, add bool) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return invalid("titre manquant")
	}
	if !add && typ == "" {
		for _, t := range []string{TypeMovie, TypeShow} {
			if err := s.SetVostfr(ctx, ed, key, t, false); err != nil {
				return err
			}
		}
		return nil
	}
	if typ != TypeMovie && typ != TypeShow {
		return invalid("type %q : film ou série attendu", typ)
	}
	if ed == nil || s.cols == nil {
		return invalid("collections indisponibles")
	}
	specs, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return err
	}
	sp := vostfrSpec(specs, typ)
	var keys []string
	if sp != nil {
		keys = append(keys, sp.Sources[0].Keys...)
	}
	idx := -1
	for i, k := range keys {
		if k == key {
			idx = i
		}
	}
	switch {
	case add && idx >= 0, !add && idx < 0:
		return nil // déjà dans l'état voulu
	case add:
		if total, _ := s.Vostfr(ctx); len(total) >= MaxVostfr {
			return invalid("%d titres en VOSTFR au plus", MaxVostfr)
		}
		keys = append(keys, key)
	default:
		keys = append(keys[:idx], keys[idx+1:]...)
	}
	if keys == nil {
		keys = []string{}
	}
	var id string
	if sp == nil {
		created, err := ed.CreateSpec(ctx, vostfrNewSpec(typ, keys))
		if err != nil {
			return err
		}
		id = created.ID
	} else {
		next := *sp
		next.Sources = []core.CollectionSource{{Kind: "titles", Keys: keys}}
		if _, err := ed.UpdateSpec(ctx, sp.ID, next); err != nil {
			return err
		}
		id = sp.ID
	}
	if err := s.Reload(ctx); err != nil {
		return err
	}
	return s.vostfrWire(ctx, id)
}

// vostfrWire déclare la collection en exception de langue et la range dans
// la bibliothèque « VOSTFR » (créée au besoin).
func (s *Service) vostfrWire(ctx context.Context, col string) error {
	free := s.Exceptions()
	has := false
	for _, c := range free {
		has = has || c == col
	}
	if !has {
		if err := s.SetExceptions(ctx, append(free, col)); err != nil {
			return err
		}
	}
	for _, l := range s.Libraries() {
		if !strings.EqualFold(l.Name, VostfrName) {
			continue
		}
		for _, c := range l.Collections {
			if c == col {
				return nil
			}
		}
		l.Collections = append(l.Collections, col)
		l.Type = TypeMixed
		_, err := s.Update(ctx, l)
		return err
	}
	_, err := s.Create(ctx, Library{Name: VostfrName, Type: TypeMixed, Collections: []string{col}, AllLanguages: true})
	return err
}
