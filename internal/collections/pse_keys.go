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
	// Sans le filtre de découverte : l'indexeur et les exceptions de langue
	// partent de ces titres, y compris ceux qui ne sont pas encore lisibles.
	keys, err := s.serveMembers(ctx, row, user, false)
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

// WithPSEDiscovery branche le filtre français et lisible de Remix sur les
// pages et les rangées de collections.
func WithPSEDiscovery(fn func(context.Context, []core.RatingKey) ([]core.RatingKey, error)) Option {
	return func(s *Service) { s.pseDiscovery = fn }
}

// serve renvoie les titres affichés d'une collection. Une collection de
// sorties à venir montre par nature des titres pas encore disponibles :
// elle échappe au filtre.
func (s *Service) serve(ctx context.Context, row *specRow, user *core.User, wait bool) ([]core.RatingKey, error) {
	keys, err := s.serveMembers(ctx, row, user, wait)
	if err != nil || s.pseDiscovery == nil || row.spec.ReleaseMode == "upcoming" || row.spec.ReleaseMode == "mixed" {
		return keys, err
	}
	return s.pseDiscovery(ctx, keys)
}

// pseFilterTitles applique le même filtre aux films d'une franchise.
func (s *Service) pseFilterTitles(ctx context.Context, items []core.Title) ([]core.Title, error) {
	if s.pseDiscovery == nil || len(items) == 0 {
		return items, nil
	}
	keys := make([]core.RatingKey, len(items))
	for i := range items {
		keys[i] = items[i].Key
	}
	kept, err := s.pseDiscovery(ctx, keys)
	if err != nil {
		return nil, err
	}
	allowed := make(map[core.RatingKey]bool, len(kept))
	for _, k := range kept {
		allowed[k] = true
	}
	out := make([]core.Title, 0, len(kept))
	for _, t := range items {
		if allowed[t.Key] {
			out = append(out, t)
		}
	}
	return out, nil
}
