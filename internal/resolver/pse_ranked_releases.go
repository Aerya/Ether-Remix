package resolver

import (
	"context"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
)

// RankedReleases lists every release the resolver accepts for this viewer and
// client, in the order Auto would try them (profil, règles, langues). Elle ne
// lit que le cache de recherche : sans recherche récente, la liste est vide.
// Chaque entrée porte l'identifiant de sa release, utilisable comme version
// exacte. Voir docs/pse/FORK.fr.md.
func (s *Service) RankedReleases(ctx context.Context, req core.ResolveRequest) ([]core.ReleaseVariant, error) {
	if req.Title == nil {
		return nil, fmt.Errorf("resolver: nil title")
	}
	q := s.qualityCfg()
	dec := decideProfile(q, req.Client, req.User, titleTarget(req.Title, s.membershipFor(ctx, req.Title)))
	req.Client = dec.caps
	sreq := s.cachedSearchRequest(req.Title)
	results, ok := s.peekSearchCache(searchCacheKey(sreq))
	if !ok || len(results) == 0 {
		return nil, nil
	}
	ranked, _, err := s.rankCandidatesLocal(ctx, req, sreq, dec.profile, results, nil, "", false)
	if err != nil {
		return nil, err
	}
	ranked = acceptableVariantAudio(clearIdentityVariants(ranked))
	out := make([]core.ReleaseVariant, 0, len(ranked))
	for _, c := range ranked {
		v := core.ReleaseVariant{ID: c.rel.ID, Label: c.rel.Name, Release: browserRelease(c.rel)}
		if info, ok := s.probedMediaFor(c.rel); ok {
			v.Info = info
		}
		out = append(out, v)
	}
	return out, nil
}
