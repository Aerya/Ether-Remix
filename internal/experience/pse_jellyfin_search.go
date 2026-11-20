package experience

import (
	"context"
	"slices"

	"github.com/braedonsaunders/ether/internal/core"
)

func (s *Service) pseJellyfinSearchTypes(ctx context.Context, v *core.User, types []core.MediaType) []core.MediaType {
	if s.d.Protocol != "jellyfin" {
		return types
	}
	for _, theme := range s.pseJellyfinThemes(ctx, v) {
		for _, part := range theme.parts {
			if !slices.Contains(types, part.Type) {
				types = append(types, part.Type)
			}
		}
	}
	return types
}
