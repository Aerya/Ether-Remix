package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// VibeSearch resolves an optional natural-language collection source.
type VibeSearch struct {
	Source     VibeSource
	Visibility Visibility
	// Unrestricted is a trusted host policy capability; nil fails closed.
	Unrestricted func(*core.User) bool
}

func (s VibeSearch) Find(ctx context.Context, viewer *core.User, query string) (*core.Collection, []core.Title, bool, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if s.Source == nil {
		return nil, nil, false, nil
	}
	collection, items, ok, err := s.Source.VibeCollection(ctx, viewer, query)
	if s.Unrestricted != nil && s.Unrestricted(viewer) {
		if err != nil || !ok {
			return collection, items, ok, err
		}
		return collection, s.filterItems(ctx, viewer, items), true, nil
	}
	if err != nil || !ok {
		return nil, nil, ok, err
	}
	if collection != nil && !s.Visibility.CollectionAllowed(viewer, collection) {
		return nil, nil, true, nil
	}
	return collection, s.filterItems(ctx, viewer, items), true, nil
}

func (s VibeSearch) filterItems(ctx context.Context, viewer *core.User, items []core.Title) []core.Title {
	filtered := make([]core.Title, 0, len(items))
	if s.Unrestricted != nil && s.Unrestricted(viewer) {
		for i := range items {
			if s.Visibility.TitleAllowed(viewer, &items[i]) {
				filtered = append(filtered, items[i])
			}
		}
		return filtered
	}
	for i := range items {
		if s.Visibility.Visible(ctx, viewer, &items[i]) {
			filtered = append(filtered, items[i])
		}
	}
	return filtered
}

type VibeSource = frontend.VibeSource
