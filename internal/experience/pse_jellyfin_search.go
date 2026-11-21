package experience

import (
	"context"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
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

// pseJellyfinThemeOnlySearch searches a profile limited to Remix themes inside
// the union of its authorized themes. The catalogue's candidate window covers
// every library, so filtering it afterwards would drop titles of the themes.
func (s *Service) pseJellyfinThemeOnlySearch(ctx context.Context, viewer *core.Viewer, device core.Device, v *core.User, query string, q frontend.ItemQuery) (frontend.SearchPage, bool, error) {
	if only, err := s.pseJellyfinThemeOnly(ctx, v); !only || err != nil {
		return frontend.SearchPage{}, false, err
	}
	offset, limit := max(q.Offset, 0), q.Limit
	if limit <= 0 {
		limit = 50
	}
	if offset >= 1000 {
		return frontend.SearchPage{Continuation: frontend.ContinuationUnknown}, true, nil
	}
	q.Filter.Query = query
	// As Service.Search, a request without types also finds seasons and
	// episodes of the series.
	q.Recursive = q.Recursive || len(q.Types) == 0 || slices.Contains(q.Types, core.TypeSeason) || slices.Contains(q.Types, core.TypeEpisode)
	// The whole membership is read: it is ranked before it is paged.
	q.Offset, q.Limit = 0, 0
	page, err := s.LibraryItems(ctx, viewer, device, "", q)
	if err != nil {
		return frontend.SearchPage{}, true, err
	}
	items := page.Items
	if q.Sort == "" {
		pseJellyfinRankSearch(items, query)
	}
	start := min(offset, len(items))
	stop := min(offset+limit, len(items))
	// The catalogue's relevance score does not exist for members read from a
	// theme: exact matches come first, then prefixes, then the list's order.
	continuation := frontend.ContinuationExhausted
	if stop < len(items) {
		continuation = frontend.ContinuationMore
	}
	return frontend.SearchPage{Page: core.Page{Items: items[start:stop], Total: len(items)}, TotalExact: true, Continuation: continuation}, true, nil
}

// pseJellyfinRankSearch orders exact title matches, then titles starting with
// the query, then the others, keeping the existing order inside each class.
func pseJellyfinRankSearch(items []core.Title, query string) {
	query = strings.ToLower(strings.TrimSpace(query))
	rank := func(title core.Title) int {
		name := strings.ToLower(strings.TrimSpace(title.Title))
		switch {
		case name == query:
			return 0
		case strings.HasPrefix(name, query):
			return 1
		}
		return 2
	}
	slices.SortStableFunc(items, func(a, b core.Title) int { return rank(a) - rank(b) })
}
