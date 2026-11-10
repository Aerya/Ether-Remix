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

// PSEQualityKeys uses the materialized membership before visibility/quality
// filtering. Otherwise rejecting a title could remove it from the scope that
// rejected it, making it reappear on the next refresh. Only shared collections
// participate in a server-wide release policy.
func (s *Service) PSEQualityKeys(ctx context.Context, id string) []core.RatingKey {
	row, err := s.rowByID(ctx, id)
	if err != nil || !row.enabled || row.owner != 0 {
		return nil
	}
	members, err := s.membersFor(ctx, row, false)
	if err != nil {
		return nil
	}
	return append([]core.RatingKey(nil), members.keys...)
}
