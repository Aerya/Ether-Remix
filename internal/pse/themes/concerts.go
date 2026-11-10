package themes

import (
	"context"
	"maps"

	"github.com/braedonsaunders/ether/internal/core"
)

const legacyConcertKeywords = "156205|6029|304615|318730"

// UpgradeConcertCollections resserre uniquement les requêtes originales du
// modèle. Les collections personnelles et les requêtes modifiées restent
// intactes ; noms, identifiants et réglages sont conservés. UpdateSpec lance
// la reconstruction des membres. Une seconde exécution ne change rien.
func (s *Service) UpgradeConcertCollections(ctx context.Context, ed Editor) error {
	if s.cols == nil || ed == nil {
		return nil
	}
	specs, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return err
	}
	for _, sp := range specs {
		if sp.OwnerUserID != 0 || sp.Description != presetDescription || sp.MediaType != TypeMovie || len(sp.Sources) != 1 {
			continue
		}
		src := sp.Sources[0]
		if src.Kind != "tmdbDiscover" || (src.MediaType != "" && src.MediaType != TypeMovie) {
			continue
		}
		for _, next := range PresetSpecs("concerts") {
			old := maps.Clone(next.Sources[0].Params)
			old["with_keywords"] = legacyConcertKeywords
			delete(old, "with_genres")
			delete(old, "include_video")
			if !maps.Equal(src.Params, old) {
				continue
			}
			sp.Sources = append([]core.CollectionSource(nil), sp.Sources...)
			sp.Sources[0].Params = maps.Clone(next.Sources[0].Params)
			if _, err := ed.UpdateSpec(ctx, sp.ID, sp); err != nil {
				return err
			}
			break
		}
	}
	return nil
}
