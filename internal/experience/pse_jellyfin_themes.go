package experience

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"slices"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/paging"
	"github.com/braedonsaunders/ether/internal/reqmemo"
)

type pseJellyfinTheme struct {
	view  frontend.View
	parts []frontend.PSETheme
}

func (s *Service) pseJellyfinThemes(ctx context.Context, v *core.User) []pseJellyfinTheme {
	return s.pseJellyfinThemesFrom(ctx, v, frontend.CurrentPSEThemes())
}

func (s *Service) pseJellyfinThemesFrom(ctx context.Context, v *core.User, themes frontend.PSEThemes) []pseJellyfinTheme {
	if s.d.Protocol != "jellyfin" || themes == nil {
		return nil
	}
	var out []pseJellyfinTheme
	positions := make(map[int]int)
	explicit := frontend.ThemeExplicit(v)
	for _, part := range themes.Themes() {
		if !frontend.IsThemeSection(part.Section) || len(part.Rows) == 0 ||
			!frontend.ThemeAllowed(v, part.Section) || (part.VisibleTo != nil && !part.VisibleTo(v)) {
			continue
		}
		if !explicit && !s.pseJellyfinBaseVisible(ctx, v, part.Type) {
			continue
		}
		id := part.Section
		if id > frontend.PSEThemeSectionBase+frontend.PSEThemeShowOffset {
			id -= frontend.PSEThemeShowOffset
		}
		if at, ok := positions[id]; ok {
			theme := &out[at]
			theme.parts = append(theme.parts, part)
			if theme.view.Type != part.Type {
				theme.view.Type = "mixed"
			}
			continue
		}
		name := part.LibraryTitle
		if name == "" {
			name = part.Title
		}
		positions[id] = len(out)
		out = append(out, pseJellyfinTheme{
			view:  frontend.View{ID: strconv.Itoa(id), LibraryID: core.LibraryID(id), Name: name, Type: part.Type, Virtual: true},
			parts: []frontend.PSETheme{part},
		})
	}
	return out
}

func (s *Service) pseJellyfinBaseVisible(ctx context.Context, v *core.User, mt core.MediaType) bool {
	base, ok := LibrarySection(s.libraries(), mt)
	if !ok || !SectionAllowed(v, base) {
		return false
	}
	if s.d.PluginLibraries == nil {
		return true
	}
	lib, err := s.d.PluginLibraries.Library(ctx, core.LibraryID(base))
	if err != nil {
		return false
	}
	_, _, ok = s.publishedLibrary(ctx, lib)
	return ok
}

func (s *Service) pseJellyfinAppendThemeViews(ctx context.Context, v *core.User, views []frontend.View) []frontend.View {
	for _, theme := range s.pseJellyfinThemes(ctx, v) {
		views = append(views, theme.view)
	}
	return views
}

func (s *Service) pseJellyfinThemeViewID(viewID string) bool {
	if s.d.Protocol != "jellyfin" {
		return false
	}
	id, err := strconv.ParseInt(viewID, 10, 64)
	if err != nil {
		return false
	}
	return frontend.IsPSEThemeJellyfinView(id)
}

func pseJellyfinThemeRowID(themeID, rowID string) string {
	sum := sha256.Sum256([]byte(themeID + "\x00" + rowID))
	id := int64(binary.BigEndian.Uint64(sum[:8])&uint64(frontend.PSEThemeJellyfinRowBase-1)) | frontend.PSEThemeJellyfinRowBase
	return strconv.FormatInt(id, 10)
}

func pseJellyfinRowKeys(ctx context.Context, themes frontend.PSEThemes, v *core.User, row string) []core.RatingKey {
	keys, _ := reqmemo.Do(ctx, "experience:theme-row:"+strconv.FormatInt(v.ID, 10)+":"+row, func() ([]core.RatingKey, error) {
		return themes.RowKeys(ctx, row, frontend.ThemeKeyViewer(v)), nil
	})
	return keys
}

func pseJellyfinThemePartKey(typ core.MediaType, key core.RatingKey) bool {
	kind := key.Kind()
	if typ == core.TypeMovie {
		// A local video without a catalogue match is stored as a movie.
		return kind == core.KindMovie || kind == core.KindLocal && !key.IsPluginKey()
	}
	return typ == core.TypeShow && kind == core.KindShow
}

// PSEThemeRowViews lists collections with members of the authorized theme.
// This omits the name of a private collection whose members were all filtered.
func (s *Service) PSEThemeRowViews(ctx context.Context, viewer *core.Viewer, viewID string) ([]frontend.View, error) {
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, err
	}
	ctx = core.WithCatalogViewer(ctx, v)
	themes := frontend.CurrentPSEThemes()
	if themes == nil {
		return nil, core.ErrNotFound
	}
	for _, theme := range s.pseJellyfinThemesFrom(ctx, v, themes) {
		if theme.view.ID == viewID {
			return theme.rowViews(ctx, themes, v, ""), nil
		}
	}
	return nil, core.ErrNotFound
}

func (theme pseJellyfinTheme) rowViews(ctx context.Context, themes frontend.PSEThemes, v *core.User, viewID string) []frontend.View {
	var out []frontend.View
	positions := make(map[string]int)
	for _, part := range theme.parts {
		for _, row := range part.Rows {
			id := pseJellyfinThemeRowID(theme.view.ID, row.ID)
			if viewID != "" && viewID != id {
				continue
			}
			keys := pseJellyfinRowKeys(ctx, themes, v, row.ID)
			if !slices.ContainsFunc(keys, func(key core.RatingKey) bool { return pseJellyfinThemePartKey(part.Type, key) }) {
				continue
			}
			if pos, ok := positions[row.ID]; ok {
				if out[pos].Type != part.Type {
					out[pos].Type = "mixed"
				}
				continue
			}
			positions[row.ID] = len(out)
			out = append(out, frontend.View{
				ID: id, LibraryID: theme.view.LibraryID,
				Name: row.Title, Type: part.Type, Virtual: true,
			})
		}
	}
	return out
}

// PSEThemeRowView resolves a synthetic folder using only current theme rights.
func (s *Service) PSEThemeRowView(ctx context.Context, viewer *core.Viewer, viewID string) (frontend.View, error) {
	if id, err := strconv.ParseInt(viewID, 10, 64); err != nil || !frontend.IsPSEThemeJellyfinRow(id) {
		return frontend.View{}, core.ErrNotFound
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.View{}, err
	}
	ctx = core.WithCatalogViewer(ctx, v)
	themes := frontend.CurrentPSEThemes()
	for _, theme := range s.pseJellyfinThemesFrom(ctx, v, themes) {
		if rows := theme.rowViews(ctx, themes, v, viewID); len(rows) != 0 {
			return rows[0], nil
		}
	}
	return frontend.View{}, core.ErrNotFound
}

type pseJellyfinMembership struct {
	ordered  []core.RatingKey
	keys     map[core.RatingKey]bool
	langFree map[core.RatingKey]bool
}

func (m pseJellyfinMembership) contains(title *core.Title) bool {
	if title == nil || title.Key.Kind() == core.KindLocal && title.Type != core.TypeMovie {
		return false
	}
	return m.keys[title.Key] || title.ShowKey != 0 && m.keys[title.ShowKey]
}

// pseJellyfinThemeMembership returns the authorized scope behind a theme view,
// a row view, or the union of accessible themes when viewID is empty. It does
// not authorize reading an item: callers must still apply title visibility.
func (s *Service) pseJellyfinThemeMembership(ctx context.Context, v *core.User, viewID string) (pseJellyfinMembership, bool, error) {
	if viewID != "" && !s.pseJellyfinThemeViewID(viewID) {
		return pseJellyfinMembership{}, false, nil
	}
	themes := frontend.CurrentPSEThemes()
	if themes == nil {
		if viewID != "" {
			return pseJellyfinMembership{}, true, core.ErrNotFound
		}
		return pseJellyfinMembership{}, false, nil
	}
	membership := pseJellyfinMembership{keys: map[core.RatingKey]bool{}, langFree: map[core.RatingKey]bool{}}
	found := false
	for _, theme := range s.pseJellyfinThemesFrom(ctx, v, themes) {
		allRows := viewID == "" || viewID == theme.view.ID
		found = found || allRows
		for _, part := range theme.parts {
			for _, row := range part.Rows {
				if !allRows && viewID != pseJellyfinThemeRowID(theme.view.ID, row.ID) {
					continue
				}
				keys := pseJellyfinRowKeys(ctx, themes, v, row.ID)
				for _, key := range keys {
					if !pseJellyfinThemePartKey(part.Type, key) {
						continue
					}
					found = true
					if !membership.keys[key] {
						membership.ordered = append(membership.ordered, key)
						membership.keys[key] = true
					}
					if part.AllLanguages || row.LangFree {
						membership.langFree[key] = true
					}
				}
			}
		}
	}
	if !found && viewID != "" {
		return pseJellyfinMembership{}, true, core.ErrNotFound
	}
	return membership, found, nil
}

// The root fallback is only for an explicit profile without any regular
// library. Other viewers retain the existing root semantics and can browse
// themes separately by view ID.
func (s *Service) pseJellyfinThemeOnly(ctx context.Context, v *core.User) (bool, error) {
	if s.d.Protocol != "jellyfin" || !frontend.ThemeExplicit(v) {
		return false, nil
	}
	if s.d.PluginLibraries != nil {
		views, err := s.storedViews(ctx, v)
		return len(views) == 0, err
	}
	for _, lib := range s.libraries() {
		if v.AllowsLibrary(core.LibraryID(lib.ID)) {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) pseJellyfinLibraryItems(ctx context.Context, v *core.User, device core.Device, viewID string, q frontend.ItemQuery) (core.Page, bool, error) {
	if viewID == "" {
		if only, err := s.pseJellyfinThemeOnly(ctx, v); !only || err != nil {
			return core.Page{}, false, err
		}
	}
	ctx = core.WithCatalogViewer(ctx, v)
	membership, handled, err := s.pseJellyfinThemeMembership(ctx, v, viewID)
	if !handled || err != nil {
		return core.Page{}, handled, err
	}
	if len(q.UnservedTypes) > 0 && len(q.Types) == 0 {
		return core.Page{}, true, nil
	}
	var include map[core.RatingKey]bool
	if q.Filter.IncludeKeys != nil {
		if len(q.Filter.IncludeKeys) == 0 {
			return core.Page{}, true, nil
		}
		include = make(map[core.RatingKey]bool, len(q.Filter.IncludeKeys))
		for _, key := range q.Filter.IncludeKeys {
			include[key] = true
		}
	}
	keys := membership.ordered
	if include != nil && !q.Recursive {
		keys = make([]core.RatingKey, 0, min(len(keys), len(include)))
		for _, key := range membership.ordered {
			if include[key] {
				keys = append(keys, key)
			}
		}
	}
	var items []core.Title
	for batch := range slices.Chunk(keys, 400) {
		titles, err := s.pseJellyfinThemeTitles(ctx, v, batch, membership)
		if err != nil {
			return core.Page{}, true, err
		}
		for _, title := range titles {
			if include == nil || include[title.Key] {
				match, err := s.matchesUserQuery(ctx, v, title, q)
				if err != nil {
					return core.Page{}, true, err
				}
				if match {
					items = append(items, title)
				}
			}
			if q.Recursive && title.Type == core.TypeShow {
				children, err := s.pseJellyfinThemeChildren(ctx, v, device, title, q, membership, include)
				if err != nil {
					return core.Page{}, true, err
				}
				items = append(items, children...)
			}
		}
	}
	if err = s.sortTitles(ctx, items, q); err != nil {
		return core.Page{}, true, err
	}
	if q.AdjacentTo != 0 {
		page, err := adjacentTitles(items, q)
		return page, true, err
	}
	if q.StartAt != 0 {
		page, err := startingTitles(items, q)
		return page, true, err
	}
	limit := q.Limit
	if limit == 0 {
		limit = -1
	}
	return core.Page{Items: paging.Slice(items, q.Offset, limit), Total: len(items)}, true, nil
}

func (s *Service) pseJellyfinThemeTitles(ctx context.Context, v *core.User, keys []core.RatingKey, membership pseJellyfinMembership) ([]core.Title, error) {
	keys, err := s.pseJellyfinDiscoveryKeys(ctx, keys, membership)
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	var titles map[core.RatingKey]core.Title
	if s.d.CachedGetMany != nil {
		titles, err = s.d.CachedGetMany(ctx, keys)
		if err != nil {
			return nil, err
		}
	} else {
		titles = make(map[core.RatingKey]core.Title, len(keys))
		for _, key := range keys {
			var title *core.Title
			var err error
			if s.d.CachedGet != nil {
				title, err = s.d.CachedGet(ctx, key)
			} else {
				title, err = s.d.Get(ctx, v, key)
			}
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return nil, err
			}
			if err == nil && title != nil {
				titles[key] = *title
			}
		}
	}
	var normal, allLanguages []core.Title
	for _, key := range keys {
		title, ok := titles[key]
		if !ok || title.Key != key || !membership.contains(&title) {
			continue
		}
		if membership.langFree[key] {
			allLanguages = append(allLanguages, title)
		} else {
			normal = append(normal, title)
		}
	}
	allowed, err := s.allowTitles(ctx, v, normal)
	if err != nil {
		return nil, err
	}
	if len(allLanguages) > 0 {
		more, err := s.allowTitles(core.WithPSELangFree(ctx), v, allLanguages)
		if err != nil {
			return nil, err
		}
		allowed = append(allowed, more...)
	}
	permitted := make(map[core.RatingKey]core.Title, len(allowed))
	for _, title := range allowed {
		permitted[title.Key] = title
	}
	out := make([]core.Title, 0, len(allowed))
	for _, key := range keys {
		if title, ok := permitted[key]; ok {
			out = append(out, title)
		}
	}
	return out, nil
}

func (s *Service) pseJellyfinDiscoveryKeys(ctx context.Context, keys []core.RatingKey, membership pseJellyfinMembership) ([]core.RatingKey, error) {
	if s.d.PSEThemeDiscoveryKeys == nil {
		return keys, nil
	}
	var normal, allLanguages []core.RatingKey
	for _, key := range keys {
		if membership.langFree[key] {
			allLanguages = append(allLanguages, key)
		} else {
			normal = append(normal, key)
		}
	}
	allowed := make(map[core.RatingKey]bool, len(keys))
	for _, batch := range []struct {
		ctx  context.Context
		keys []core.RatingKey
	}{{ctx, normal}, {core.WithPSELangFree(ctx), allLanguages}} {
		if len(batch.keys) == 0 {
			continue
		}
		selected, err := s.d.PSEThemeDiscoveryKeys(batch.ctx, batch.keys)
		if err != nil {
			return nil, err
		}
		for _, key := range selected {
			allowed[key] = true
		}
	}
	out := make([]core.RatingKey, 0, len(allowed))
	for _, key := range keys {
		if allowed[key] {
			out = append(out, key)
		}
	}
	return out, nil
}

func (s *Service) pseJellyfinThemeChildren(ctx context.Context, v *core.User, device core.Device, show core.Title, q frontend.ItemQuery, membership pseJellyfinMembership, include map[core.RatingKey]bool) ([]core.Title, error) {
	if membership.langFree[show.Key] {
		ctx = core.WithPSELangFree(ctx)
	}
	var out []core.Title
	for _, typ := range []core.MediaType{core.TypeSeason, core.TypeEpisode} {
		if (len(q.Types) > 0 && !slices.Contains(q.Types, typ)) || slices.Contains(q.ExcludedTypes, typ) {
			continue
		}
		childQuery := q
		childQuery.Types, childQuery.Recursive = []core.MediaType{typ}, typ == core.TypeEpisode
		childQuery.Offset, childQuery.Limit, childQuery.AdjacentTo, childQuery.StartAt = 0, 0, 0, 0
		childQuery.Sort = ""
		page, err := s.Children(ctx, v, device, show.Key, childQuery)
		if errors.Is(err, core.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for i := range page.Items {
			title := &page.Items[i]
			if title.Type == typ && title.ShowKey == show.Key && (include == nil || include[title.Key]) {
				out = append(out, *title)
			}
		}
	}
	return out, nil
}
