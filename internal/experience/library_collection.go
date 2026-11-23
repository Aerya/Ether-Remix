package experience

import (
	"context"
	"strconv"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

var _ frontend.LibraryScopeReader = (*Service)(nil)

// libraryBrowseFilter composes the source pool and visible rules in indexed SQL.
func (s *Service) libraryBrowseFilter(ctx context.Context, v *core.User, lib core.Library, req core.Filter) core.Filter {
	f := req
	f.CatalogueSources = lib.Sources
	for _, source := range f.CatalogueSources {
		if source == "tmdb-movies" || source == "tmdb-tv" {
			f.CatalogueSources = nil // The union includes the full catalogue.
			break
		}
	}
	// Unrestricted built-ins impose their type at each browse boundary.
	// Keeping it out of the request filter preserves recursive indexed paging.
	if (!core.IsBuiltinLibrary(lib.ID) || len(f.CatalogueSources) > 0) && req.Type != core.TypeSeason && req.Type != core.TypeEpisode {
		f.Type = filterMediaType(lib)
	}
	if lib.Source == core.LibraryCollection {
		f.CollectionMembers = lib.CollectionID
		f.CatalogueSources = nil
	}
	if core.IsBuiltinLibrary(lib.ID) {
		if extra := core.BuiltinLibraryExtraRules(lib.Rules); len(extra) > 0 {
			f.ExtraRules = extra
		}
	} else {
		f.ExtraRules = lib.Rules
		f.ExtraFallback = lib.VisibilityDefault
	}
	return f
}

// LibraryBrowseFilter attaches the library's browse-only scope (such as a
// built-in library's "library"-scoped hide rules, or a custom library's rules,
// sources or collection) to req. scoped reports whether the library narrows
// browse results beyond the canonical catalogue policy.
func (s *Service) LibraryBrowseFilter(ctx context.Context, viewer *core.Viewer, viewID string, req core.Filter) (core.Filter, bool, error) {
	if s.d.PluginLibraries == nil {
		return req, false, nil
	}
	id, err := strconv.Atoi(viewID)
	if err != nil || id <= 0 {
		return req, false, nil
	}
	stored, err := s.d.PluginLibraries.Library(ctx, core.LibraryID(id))
	if err != nil {
		return req, false, nil
	}
	if stored.Source != core.LibraryBuiltin && stored.Source != core.LibraryCatalog && stored.Source != core.LibraryCollection {
		return req, false, nil
	}
	var v *core.User
	if viewer != nil {
		v, err = s.viewer(ctx, viewer)
		if err != nil {
			return req, false, err
		}
	}
	out := s.libraryBrowseFilter(ctx, v, stored, req)
	return out, filterHasLibraryScope(out), nil
}

// LibraryMembers checks which of keys belong to libraryID's browse scope in one
// indexed query. scoped is false when libraryID has no per-library browse scope
// (e.g. an unrestricted built-in library or a plugin library).
func (s *Service) LibraryMembers(ctx context.Context, libraryID core.LibraryID, keys []core.RatingKey) (map[core.RatingKey]bool, bool, error) {
	if s.d.LibraryMembers != nil {
		members, _, scoped, err := s.d.LibraryMembers(ctx, libraryID, keys)
		return members, scoped, err
	}
	return nil, false, nil
}

// collectionLibraryScan is the in-process fallback for a collection library
// page the indexed path could not answer (a sort or request shape SQL cannot
// page). It walks the collection's ids in their stored order in bounded
// batches, applies the viewer policy and the request, and pages the kept
// titles. Each batch is bounded; the walk is the scan path every filter
// library already uses when its rules cannot be pushed into SQL.
func (s *Service) collectionLibraryScan(ctx context.Context, v *core.User, library *Library, q frontend.ItemQuery, collectionID int64) (core.Page, error) {
	filter := q.Filter
	filter.CollectionMembers = collectionID
	filter.CatalogueSources = nil
	filter.Type = library.Type
	if v.MaxContentRating != "" && (filter.MaxContentRating == "" || int(contentrating.EnforceLevelOf(v.MaxContentRating)) < int(contentrating.EnforceLevelOf(filter.MaxContentRating))) {
		filter.MaxContentRating = v.MaxContentRating
	}
	var kept []core.Title
	total := 0
	for offset := 0; ; {
		bq := core.BrowseQuery{Filter: filter, Sort: q.Sort, Desc: q.Desc, Offset: offset, Limit: 500}
		if q.Sort.IsLastViewed() {
			bq.Sort = core.SortTitle
			bq.SortLastViewed = true
			bq.SortViewerID = v.ID
		}
		page, err := s.d.Browse(ctx, bq)
		if err != nil {
			return core.Page{}, err
		}
		for i := range page.Items {
			title := &page.Items[i]
			if !s.visible(ctx, v, title) {
				continue
			}
			ok, e := s.matchesUserQuery(ctx, v, *title, q)
			if e != nil {
				return core.Page{}, e
			}
			if !ok {
				continue
			}
			kept = append(kept, *title)
			total++
		}
		if page.Total == 0 || len(page.Items) == 0 {
			break
		}
		offset += len(page.Items)
		if offset >= page.Total {
			break
		}
		if err := ctx.Err(); err != nil {
			return core.Page{}, err
		}
	}
	if q.AdjacentTo != 0 {
		return adjacentTitles(kept, q)
	}
	if q.StartAt != 0 {
		return startingTitles(kept, q)
	}
	start := min(max(q.Offset, 0), total)
	end := total
	if q.Limit > 0 {
		end = min(start+q.Limit, total)
	}
	return core.Page{Items: kept[start:end], Total: total, TotalExact: true}, nil
}
