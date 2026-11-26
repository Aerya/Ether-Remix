package service

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func (a Source) collectionCardEntries(ctx context.Context, req frontend.ArtworkRequest) ([]frontendArtworkEntry, bool, error) {
	c, bound := frontend.ArtworkCollection(ctx, req.Item)
	if !bound && a.CollectionCards != nil {
		viewer := req.Viewer
		if policy, ok := a.Visibility.Policy.(core.UserPolicySource); ok {
			viewer = policy.UserPolicy(viewer)
		}
		cards, err := a.CollectionCards(ctx, viewer, []core.RatingKey{req.Item})
		if err != nil {
			return nil, false, err
		}
		c = cards[req.Item]
	}
	if c == nil || !a.Visibility.CollectionAllowed(req.Viewer, c) {
		return nil, false, core.ErrNotFound
	}
	var entries []frontendArtworkEntry
	for _, image := range []struct{ kind, path string }{
		{frontend.ImagePoster, c.PosterPath}, {frontend.ImageThumb, c.PosterPath}, {frontend.ImageBackdrop, c.BackdropPath},
	} {
		if image.path == "" {
			continue
		}
		ref := artworkReference(req, image.kind, frontend.CollectionCardArtworkSubject+"\x00"+image.path+"\x00"+collectionStamp(c))
		entries = append(entries, frontendArtworkEntry{
			image:      frontend.Image{Kind: image.kind, Path: ref, Tag: ref, Subject: frontend.CollectionCardArtworkSubject},
			remotePath: image.path,
		})
	}
	return entries, true, nil
}
