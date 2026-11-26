package frontend

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

const MaxCollectionCards = core.MaxCollectionCards

func ValidRowID(id string) bool { return core.ValidRowID(id) }

// CollectionCardArtworkSubject distinguishes an authorized card image from a
// full collection poster. It is included in the signed image claim.
const CollectionCardArtworkSubject = "collection-card"

type collectionCardArtworkKey struct{}

func WithCollectionCardArtwork(ctx context.Context) context.Context {
	return context.WithValue(ctx, collectionCardArtworkKey{}, true)
}

func CollectionCardArtwork(ctx context.Context) bool {
	card, _ := ctx.Value(collectionCardArtworkKey{}).(bool)
	return card
}
