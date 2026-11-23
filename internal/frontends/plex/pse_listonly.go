package plex

import "github.com/braedonsaunders/ether/internal/frontend"

// Listes légères — voir docs/pse/FORK.fr.md.

// pseEnrichCached est enrich sans aucune récupération : une liste complète une
// ligne avec le détail déjà en mémoire, sans en programmer la lecture.
func (s *Server) pseEnrichCached(t *frontend.Title) *frontend.Title {
	titles := s.titleDetails()
	if titles == nil || !lacksDetail(t) {
		return t
	}
	switch t.Key.Kind() {
	case frontend.KindMovie, frontend.KindShow:
	default:
		return t
	}
	if t.Type != frontend.TypeMovie && t.Type != frontend.TypeShow {
		return t
	}
	d := titles.peek(t.Key)
	if d == nil {
		return t
	}
	cp := *t
	mergeDetail(&cp, d)
	return &cp
}
