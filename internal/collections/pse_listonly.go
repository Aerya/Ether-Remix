package collections

// Ajout Remix : voir docs/pse/FORK.fr.md (listes légères).

import (
	"context"

	"github.com/braedonsaunders/ether/internal/pse/listonly"
)

// pseFullBuild : une construction de collection est enregistrée et servie à
// tous ; elle ne doit jamais hériter d'une liste légère (titres manquants,
// distribution réduite).
func pseFullBuild(ctx context.Context) context.Context { return listonly.Without(ctx) }
