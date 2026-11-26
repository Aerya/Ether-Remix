package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

var _ frontend.FavoriteTitles = (*Service)(nil)

// FavoriteTitles lists one page of the viewer's liked titles of one role,
// hydrated from stored metadata (no plugin or provider call) and filtered to
// what the viewer may see.
func (s *Service) FavoriteTitles(ctx context.Context, viewer *core.Viewer, q frontend.FavoriteTitlesQuery) (frontend.FavoriteTitlesPage, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.FavoriteTitlesPage{}, err
	}
	if s.d.FavoritePage == nil {
		return frontend.FavoriteTitlesPage{}, core.ErrNotAvailable
	}
	keys, likedAt, next, total, err := s.d.FavoritePage(ctx, v, q.Role, q.Cursor, q.Limit)
	if err != nil {
		return frontend.FavoriteTitlesPage{}, err
	}
	visible, err := s.loadVisiblePlaylistTitles(ctx, v, keys)
	if err != nil {
		return frontend.FavoriteTitlesPage{}, err
	}
	out := frontend.FavoriteTitlesPage{Items: make([]frontend.LikedTitle, 0, len(keys)), Next: next, Total: total}
	for i, key := range keys {
		if title, ok := visible[key]; ok {
			out.Items = append(out.Items, frontend.LikedTitle{Title: title, LikedAt: likedAt[i]})
		}
	}
	return out, nil
}
