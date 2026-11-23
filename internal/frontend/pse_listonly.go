package frontend

import (
	"context"
	"net/http"

	"github.com/braedonsaunders/ether/internal/pse/listonly"
)

// Listes légères — voir docs/pse/FORK.fr.md.

// WithPSEListOnly marque ctx comme celui d'une liste : voir listonly.With.
func WithPSEListOnly(ctx context.Context) context.Context { return listonly.With(ctx) }

// PSEListOnly indique si ctx appartient à une liste légère.
func PSEListOnly(ctx context.Context) bool { return listonly.On(ctx) }

// PSEListRequest renvoie r dont le contexte est marqué comme celui d'une liste.
func PSEListRequest(r *http.Request) *http.Request {
	if ctx := listonly.With(r.Context()); ctx != r.Context() {
		return r.WithContext(ctx)
	}
	return r
}
