package frontend

import (
	"context"

	"github.com/braedonsaunders/ether/internal/artwork"
)

// Ajouts Remix autour des affiches — voir docs/pse/FORK.fr.md.

// PosterOrigin : voir artwork.PosterOrigin.
type PosterOrigin = artwork.PosterOrigin

// WithPosterOrigin attache à ctx un témoin « affiche venue du fournisseur ».
func WithPosterOrigin(ctx context.Context) (context.Context, *PosterOrigin) {
	return artwork.WithPosterOrigin(ctx)
}

// ResizePosterCached : ResizePoster sans recalcul pour une image déjà vue.
func ResizePosterCached(data []byte, width int) ([]byte, error) {
	return artwork.ResizePosterCached(data, width)
}
