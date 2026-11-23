package experience

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

const maxProviderIDLookups = 200

func emptyProviderIDPage(q frontend.ItemQuery) (core.Page, error) {
	if q.StartAt != 0 || q.AdjacentTo != 0 {
		return core.Page{}, core.ErrNotFound
	}
	return core.Page{Items: []core.Title{}}, nil
}

// providerIDLibraryItems answers /Items and /Users/{id}/Items requests carrying
// AnyProviderIdEquals via Ether's external-id indexes instead of scanning any
// library. Visibility, library scoping, IncludeItemTypes, ExcludeItemTypes,
// Recursive, IsPlayed/Filters, Sort, and Limit all apply to the resolved set.
func (s *Service) providerIDLibraryItems(ctx context.Context, v *core.User, viewID string, q frontend.ItemQuery) (core.Page, error) {
	var (
		stored          *core.Library
		libraryType     core.MediaType
		needMemberCheck bool
	)
	if viewID != "" {
		id, err := strconv.Atoi(viewID)
		if err != nil {
			return core.Page{}, core.ErrNotFound
		}
		if s.d.PluginLibraries != nil {
			lib, err := s.d.PluginLibraries.Library(ctx, core.LibraryID(id))
			if err != nil {
				return core.Page{}, core.ErrNotFound
			}
			mt := filterMediaType(lib)
			if lib.Source != core.LibraryCollection {
				var ok bool
				mt, _, ok = s.publishedLibrary(ctx, lib)
				if !ok {
					return core.Page{}, core.ErrNotFound
				}
			} else if lib.Hidden {
				return core.Page{}, core.ErrNotFound
			}
			if !v.AllowsLibrary(lib.ID) {
				return core.Page{}, core.ErrNotFound
			}
			if lib.Source == core.LibraryPlugin {
				// Plugin items currently do not carry TMDB/IMDb/TVDB provider ids.
				return emptyProviderIDPage(q)
			}
			stored = &lib
			libraryType = mt
			needMemberCheck = libraryNeedsMemberCheck(lib)
		} else {
			var found *Library
			libs := s.libraries()
			for i := range libs {
				if libs[i].ID == id {
					found = &libs[i]
					break
				}
			}
			if found == nil || !SectionAllowed(v, id) {
				return core.Page{}, core.ErrNotFound
			}
			libraryType = found.Type
		}
	}

	var baseTypes []core.MediaType
	switch {
	case viewID == "" && q.Recursive:
		baseTypes = []core.MediaType{core.TypeMovie, core.TypeShow, core.TypeSeason, core.TypeEpisode}
	case viewID == "":
		baseTypes = []core.MediaType{core.TypeMovie, core.TypeShow}
	case libraryType == core.TypeMovie:
		baseTypes = []core.MediaType{core.TypeMovie}
	case libraryType == core.TypeShow && q.Recursive:
		baseTypes = []core.MediaType{core.TypeShow, core.TypeSeason, core.TypeEpisode}
	case libraryType == core.TypeShow:
		baseTypes = []core.MediaType{core.TypeShow}
	}
	allowedTypes := intersectRequestedTypes(baseTypes, q)
	if len(allowedTypes) == 0 || len(q.ProviderIDs) == 0 {
		return emptyProviderIDPage(q)
	}

	keys, err := s.resolveProviderIDKeys(ctx, q.ProviderIDs, allowedTypes)
	if err != nil {
		return core.Page{}, err
	}
	if len(keys) == 0 {
		return emptyProviderIDPage(q)
	}

	var colMembers map[core.RatingKey]bool
	if q.CollectionKey != 0 {
		items, err := s.collectionItems(ctx, v, q.CollectionKey)
		if err != nil {
			if errors.Is(err, core.ErrNotFound) {
				return emptyProviderIDPage(q)
			}
			return core.Page{}, err
		}
		colMembers = make(map[core.RatingKey]bool, len(items))
		for _, it := range items {
			colMembers[it.Key] = true
		}
	}

	loadCtx := s.NoProviderWait(ctx)
	check := q
	check.FilterByProviderIDs = false
	check.ProviderIDs = nil
	check.Filter.Type = ""

	out := make([]core.Title, 0, len(keys))
	for _, key := range keys {
		if colMembers != nil && !colMembers[key] {
			continue
		}
		t, err := s.item(loadCtx, v, key)
		if err != nil {
			if errors.Is(err, core.ErrNotFound) {
				continue
			}
			return core.Page{}, err
		}
		if !containsMediaType(allowedTypes, t.Type) {
			continue
		}
		if needMemberCheck && stored != nil {
			targetKey := t.Key
			if (t.Type == core.TypeSeason || t.Type == core.TypeEpisode) && t.ShowKey != 0 {
				targetKey = t.ShowKey
			}
			var member bool
			if core.IsBuiltinLibrary(stored.ID) && core.HasLibraryScopedRules(stored.Rules) && s.d.LibraryMembers != nil {
				m, _, ok, err := s.d.LibraryMembers(ctx, stored.ID, []core.RatingKey{targetKey})
				if err != nil || (ok && !m[targetKey]) {
					continue
				}
				member = true
			} else {
				var err error
				member, err = s.d.PluginLibraries.MemberOf(ctx, *stored, targetKey)
				if err != nil || !member {
					continue
				}
			}
			if !member {
				continue
			}
		}
		ok, err := s.matchesUserQuery(ctx, v, *t, check)
		if err != nil {
			return core.Page{}, err
		}
		if ok {
			out = append(out, *t)
		}
	}

	if viewID == "" && q.Sort == "" {
		q.Sort = core.SortPopularity
		q.Desc = true
	}
	return s.finishProviderIDPage(ctx, out, q)
}

// providerIDChildren answers Children(show/season) when AnyProviderIdEquals is
// set, resolving candidate keys via external-id indexes and checking ancestry.
func (s *Service) providerIDChildren(ctx context.Context, v *core.User, parentKey core.RatingKey, q frontend.ItemQuery) (core.Page, error) {
	var baseTypes []core.MediaType
	switch parentKey.Kind() {
	case core.KindShow:
		if q.Recursive {
			baseTypes = []core.MediaType{core.TypeSeason, core.TypeEpisode}
		} else {
			baseTypes = []core.MediaType{core.TypeSeason}
		}
	case core.KindSeason:
		baseTypes = []core.MediaType{core.TypeEpisode}
	default:
		return emptyProviderIDPage(q)
	}
	allowedTypes := intersectRequestedTypes(baseTypes, q)
	if len(allowedTypes) == 0 || len(q.ProviderIDs) == 0 {
		return emptyProviderIDPage(q)
	}
	keys, err := s.resolveProviderIDKeys(ctx, q.ProviderIDs, allowedTypes)
	if err != nil {
		return core.Page{}, err
	}
	if len(keys) == 0 {
		return emptyProviderIDPage(q)
	}

	loadCtx := s.NoProviderWait(ctx)
	check := q
	check.FilterByProviderIDs = false
	check.ProviderIDs = nil
	check.Filter.Type = ""

	out := make([]core.Title, 0, len(keys))
	for _, key := range keys {
		t, err := s.item(loadCtx, v, key)
		if err != nil {
			if errors.Is(err, core.ErrNotFound) {
				continue
			}
			return core.Page{}, err
		}
		if !containsMediaType(allowedTypes, t.Type) {
			continue
		}
		switch parentKey.Kind() {
		case core.KindShow:
			if t.ShowKey != parentKey {
				continue
			}
		case core.KindSeason:
			if t.ShowKey != core.ShowKey(parentKey.TMDB()) || t.Season != parentKey.Season() {
				continue
			}
		}
		ok, err := s.matchesUserQuery(ctx, v, *t, check)
		if err != nil {
			return core.Page{}, err
		}
		if ok {
			out = append(out, *t)
		}
	}
	return s.finishProviderIDPage(ctx, out, q)
}

func (s *Service) finishProviderIDPage(ctx context.Context, out []core.Title, q frontend.ItemQuery) (core.Page, error) {
	if err := s.sortTitles(ctx, out, q); err != nil {
		return core.Page{}, err
	}
	if q.AdjacentTo != 0 {
		return adjacentTitles(out, q)
	}
	if q.StartAt != 0 {
		return startingTitles(out, q)
	}
	total := len(out)
	start := min(max(q.Offset, 0), total)
	end := total
	if q.Limit > 0 {
		end = min(start+q.Limit, total)
	}
	return core.Page{Items: out[start:end], Total: total}, nil
}

func libraryNeedsMemberCheck(lib core.Library) bool {
	if lib.Source == core.LibraryCollection || lib.Source == core.LibraryCatalog || core.HasLibraryScopedRules(lib.Rules) {
		return true
	}
	if len(lib.Sources) == 0 {
		return false
	}
	for _, src := range lib.Sources {
		if src == "tmdb-movies" || src == "tmdb-tv" {
			return false
		}
	}
	return true
}

func intersectRequestedTypes(base []core.MediaType, q frontend.ItemQuery) []core.MediaType {
	if len(base) == 0 {
		return nil
	}
	// If the caller explicitly passed IncludeItemTypes naming only unserved
	// kinds (e.g. Person, Audio), no catalogue type can match.
	if len(q.Types) == 0 && len(q.UnservedTypes) > 0 {
		return nil
	}
	out := make([]core.MediaType, 0, len(base))
	for _, mt := range base {
		if len(q.Types) > 0 && !containsMediaType(q.Types, mt) {
			continue
		}
		if containsMediaType(q.ExcludedTypes, mt) {
			continue
		}
		if !slices.Contains(out, mt) {
			out = append(out, mt)
		}
	}
	return out
}

func (s *Service) resolveProviderIDKeys(ctx context.Context, ids []frontend.ProviderID, allowedTypes []core.MediaType) ([]core.RatingKey, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > maxProviderIDLookups {
		ids = ids[:maxProviderIDLookups]
	}
	var keys []core.RatingKey
	seen := make(map[core.RatingKey]bool, len(ids))
	add := func(k core.RatingKey) {
		if k != 0 && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, p := range ids {
		provider := strings.ToLower(strings.TrimSpace(p.Provider))
		id := strings.TrimSpace(p.ID)
		if provider == "" || id == "" {
			continue
		}
		if s.d.KeysByProviderID != nil {
			matched, err := s.d.KeysByProviderID(ctx, provider, id, allowedTypes)
			if err != nil {
				return nil, err
			}
			for _, k := range matched {
				add(k)
			}
			continue
		}
		// Fallback when KeysByProviderID is not wired (unit tests with Get/ByExternalID).
		switch provider {
		case "tmdb":
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil || n <= 0 || n >= 1_000_000_000_000_000 || strconv.FormatInt(n, 10) != id {
				continue
			}
			if containsMediaType(allowedTypes, core.TypeMovie) {
				add(core.MovieKey(n))
			}
			if containsMediaType(allowedTypes, core.TypeShow) {
				add(core.ShowKey(n))
			}
		case "imdb", "tvdb":
			if s.d.ByExternalID == nil {
				continue
			}
			if provider == "imdb" {
				id = strings.ToLower(id)
			}
			t, err := s.d.ByExternalID(ctx, provider, id)
			if err != nil {
				if errors.Is(err, core.ErrNotFound) {
					continue
				}
				return nil, err
			}
			if t != nil {
				add(t.Key)
			}
		}
	}
	return keys, nil
}

func titleMatchesProviderIDs(t core.Title, ids []frontend.ProviderID) bool {
	for _, p := range ids {
		id := strings.TrimSpace(p.ID)
		if id == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(p.Provider)) {
		case "tmdb":
			if t.IDs.TMDB > 0 && strconv.FormatInt(t.IDs.TMDB, 10) == id {
				return true
			}
		case "imdb":
			if t.IDs.IMDB != "" && strings.EqualFold(t.IDs.IMDB, id) {
				return true
			}
		case "tvdb":
			if t.IDs.TVDB > 0 && strconv.FormatInt(t.IDs.TVDB, 10) == id {
				return true
			}
		}
	}
	return false
}
