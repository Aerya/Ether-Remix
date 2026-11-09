package collections

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// PSEKeys renvoie les titres d'une collection visibles par user, pour les
// bibliothèques à thème de PSE — voir docs/pse/FORK.fr.md. La collection
// n'est jamais construite sur le chemin de la requête : tant qu'elle ne
// l'est pas, la construction part en arrière-plan et rien n'est renvoyé.
// Les cibles de publication (onglet Collections, rangées) sont ignorées :
// une collection peut alimenter une bibliothèque sans être affichée
// ailleurs.
func (s *Service) PSEKeys(ctx context.Context, id string, user *core.User) []core.RatingKey {
	row, err := s.rowByID(ctx, id)
	if err != nil || !row.enabled {
		return nil
	}
	if row.owner != 0 && (user == nil || user.ID != row.owner) {
		return nil
	}
	if !row.spec.Targets.Users.Includes(user) {
		return nil
	}
	keys, err := s.serve(ctx, row, user, false)
	if err != nil {
		return nil
	}
	return keys
}
