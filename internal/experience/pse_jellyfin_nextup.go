package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type pseThemeNextUpKey struct{}

// pseJellyfinNextUpContext marque une requête « À suivre » de Jellyfin : les
// titres accordés par un thème Remix y sont acceptés (voir pseSectionAllowed).
func (s *Service) pseJellyfinNextUpContext(ctx context.Context) context.Context {
	if s.d.Protocol != "jellyfin" {
		return ctx
	}
	return context.WithValue(ctx, pseThemeNextUpKey{}, true)
}

// pseSectionAllowed ajoute à la règle des sections de base celle des thèmes
// Remix (PSEThemeGrant) pour un profil en mode explicite. Hors requête Jellyfin
// marquée, le résultat est celui de SectionAllowed.
func (c Continue) pseSectionAllowed(ctx context.Context, viewer *core.User, title *core.Title) bool {
	if c.SectionAllowed(viewer, title.Type) {
		return true
	}
	themed, _ := ctx.Value(pseThemeNextUpKey{}).(bool)
	return themed && frontend.PSEThemeGrant(viewer, title)
}
