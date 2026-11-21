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

// pseJellyfinThemeOnlySearch searches a profile limited to Remix themes. Movies
// and series come from the members of its authorized themes, since the
// catalogue's candidate window covers every library and filtering it
// afterwards would drop titles of the themes. Seasons and episodes come from
// the catalogue's bounded title index, as in Service.Search, and are kept when
// their series belongs to those themes: no series is ever walked.
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
	end := min(offset+limit, 1000)
	asked := func(types ...core.MediaType) []core.MediaType {
		var out []core.MediaType
		for _, typ := range types {
			if (len(q.Types) == 0 || slices.Contains(q.Types, typ)) && !slices.Contains(q.ExcludedTypes, typ) {
				out = append(out, typ)
			}
		}
		return out
	}
	// Without a type Service.Search also finds seasons and episodes.
	own, children := asked(core.TypeMovie, core.TypeShow), asked(core.TypeSeason, core.TypeEpisode)
	if len(q.Types) == 0 && len(q.UnservedTypes) > 0 {
		// Only unserved types were asked: Service.Search answers empty.
		return frontend.SearchPage{TotalExact: true, Continuation: frontend.ContinuationExhausted}, true, nil
	}
	var items []core.Title
	if len(own) > 0 {
		members := q
		members.Types, members.Recursive, members.Offset, members.Limit = own, false, 0, 0
		members.Filter.Query = query
		page, err := s.LibraryItems(ctx, viewer, device, "", members)
		if err != nil {
			return frontend.SearchPage{}, true, err
		}
		items = page.Items
	}
	if len(children) > 0 {
		found, err := s.pseJellyfinThemeChildSearch(ctx, v, query, q, children, min(max(2*end, searchWindowMin), 1000))
		if err != nil {
			return frontend.SearchPage{}, true, err
		}
		items = append(items, found...)
	}
	if q.Sort == "" {
		pseJellyfinRankSearch(items, query)
	}
	start, stop := min(offset, len(items)), min(end, len(items))
	// Exact only while the totals come from the members of the themes.
	page := frontend.SearchPage{Page: core.Page{Items: items[start:stop], Total: len(items)}, TotalExact: len(children) == 0, Continuation: frontend.ContinuationUnknown}
	switch {
	case len(items) > end:
		page.Continuation = frontend.ContinuationMore
	case len(children) == 0:
		page.Continuation = frontend.ContinuationExhausted
	}
	return page, true, nil
}

// pseJellyfinThemeChildSearch finds seasons and episodes through the
// catalogue's title index and keeps those of a series of the viewer's
// authorized themes, judged with the language exception of the row that holds
// the series.
func (s *Service) pseJellyfinThemeChildSearch(ctx context.Context, v *core.User, query string, q frontend.ItemQuery, types []core.MediaType, window int) ([]core.Title, error) {
	if s.d.Search.Catalog == nil {
		return nil, nil
	}
	membership, _, err := s.pseJellyfinThemeMembership(ctx, v, "")
	if err != nil || len(membership.keys) == 0 {
		return nil, err
	}
	candidates, err := s.d.Search.Find(ctx, v, query, types, window)
	if err != nil {
		return nil, err
	}
	vis := withoutSectionCheck(s.d.Visibility)
	match := q
	match.Filter.Query = ""
	var out []core.Title
	for _, title := range candidates {
		show := title.ShowKey
		if show == 0 {
			show = core.ShowKey(title.Key.TMDB())
		}
		if !membership.keys[show] {
			continue
		}
		listCtx := ctx
		if membership.langFree[show] {
			listCtx = core.WithPSELangFree(ctx)
		}
		if !vis.Playable(listCtx, v, &title) || len(vis.Available(listCtx, v, []core.Title{title})) == 0 {
			continue
		}
		ok, err := s.matchesUserQuery(ctx, v, title, match)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, title)
		}
	}
	return out, nil
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
