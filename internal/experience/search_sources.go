package experience

import (
	"cmp"
	"context"
	"slices"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// SearchSources lives in internal/frontend (host_sessions_experience.go).
type SearchSources = frontend.SearchSources

// PeopleTitlesFunc resolves titles credited to people matching query for a
// viewer. Multiple providers (such as the TMDB catalogue and plugin libraries)
// join into the shared search flow with CombinePeopleTitles.
type PeopleTitlesFunc = func(ctx context.Context, viewer *core.User, query string, limit int) ([]core.Title, error)

// CombinePeopleTitles merges one or more PeopleTitlesFunc providers into a
// single hook for SearchSources.PeopleTitles, deduplicating by RatingKey
// (keeping the highest SearchScore) and ordering by SearchScore DESC,
// Popularity DESC.
func CombinePeopleTitles(sources ...PeopleTitlesFunc) PeopleTitlesFunc {
	var active []PeopleTitlesFunc
	for _, fn := range sources {
		if fn != nil {
			active = append(active, fn)
		}
	}
	switch len(active) {
	case 0:
		return nil
	case 1:
		return active[0]
	}
	return func(ctx context.Context, viewer *core.User, query string, limit int) ([]core.Title, error) {
		var combined []core.Title
		seen := make(map[core.RatingKey]int)
		var firstErr error
		for _, fn := range active {
			items, err := fn(ctx, viewer, query, limit)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			for _, t := range items {
				if idx, ok := seen[t.Key]; ok {
					if t.SearchScore > combined[idx].SearchScore {
						combined[idx] = t
					}
					continue
				}
				seen[t.Key] = len(combined)
				combined = append(combined, t)
			}
		}
		if len(combined) == 0 && firstErr != nil {
			return nil, firstErr
		}
		slices.SortStableFunc(combined, func(a, b core.Title) int {
			if c := cmp.Compare(b.SearchScore, a.SearchScore); c != 0 {
				return c
			}
			return cmp.Compare(b.Popularity, a.Popularity)
		})
		if limit > 0 && len(combined) > limit {
			combined = combined[:limit]
		}
		return combined, nil
	}
}

// SearchFor is the shared search seen through one frontend's visibility.
// unrestricted is the trusted host policy capability (nil fails closed).
func SearchFor(src SearchSources, vis Visibility, unrestricted func(*core.User) bool) Search {
	return Search{
		Catalog: src.Catalog, PeopleTitles: src.PeopleTitles, Natural: src.Natural, Familiar: src.Familiar, Details: src.Details,
		Visibility: vis, Unrestricted: unrestricted,
		Vibe: VibeSearch{Source: src.Vibe, Visibility: vis, Unrestricted: unrestricted},
		gate: newDescriptiveGate(),
	}
}
