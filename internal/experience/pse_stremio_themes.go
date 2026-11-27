package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// pseStremioRows liste les rangées des bibliothèques Remix que le spectateur
// peut ouvrir, avec la même règle de droits que la vue Jellyfin.
func (s *Service) pseStremioRows(ctx context.Context, v *core.User, themes frontend.PSEThemes, only string) []pseStremioRow {
	var out []pseStremioRow
	explicit := frontend.ThemeExplicit(v)
	for _, part := range themes.Themes() {
		if !frontend.IsThemeSection(part.Section) || len(part.Rows) == 0 ||
			!frontend.ThemeAllowed(v, part.Section) || (part.VisibleTo != nil && !part.VisibleTo(v)) {
			continue
		}
		if !explicit && !s.pseJellyfinBaseVisible(ctx, v, part.Type) {
			continue
		}
		name := part.LibraryTitle
		if name == "" {
			name = part.Title
		}
		typ := "movie"
		if part.Type == core.TypeShow {
			typ = "series"
		}
		for _, row := range part.Rows {
			if only != "" && row.ID != only {
				continue
			}
			out = append(out, pseStremioRow{part: part, row: row, typ: typ, name: name + " · " + row.Title})
		}
	}
	return out
}

type pseStremioRow struct {
	part frontend.PSETheme
	row  frontend.PSEThemeRow
	typ  string
	name string
}

// PSEThemeCatalogs renvoie un catalogue par rangée visible (au moins un titre
// permis : contrôle parental et langue compris) des bibliothèques Remix.
func (s *Service) PSEThemeCatalogs(ctx context.Context, viewer *core.Viewer) ([]frontend.PSEThemeCatalog, error) {
	themes := frontend.CurrentPSEThemes()
	if themes == nil {
		return nil, nil
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, err
	}
	ctx = core.WithCatalogViewer(ctx, v)
	var out []frontend.PSEThemeCatalog
	for _, r := range s.pseStremioRows(ctx, v, themes, "") {
		if s.pseJellyfinRowHasAllowed(ctx, themes, v, r.part, r.row) {
			out = append(out, frontend.PSEThemeCatalog{ID: r.row.ID, Name: r.name, Type: r.typ})
		}
	}
	return out, nil
}

// PSEThemeCatalogTitles renvoie les titres permis de la rangée rowID pour le
// type Stremio typ, dans l'ordre de la collection ; ErrNotFound si le
// spectateur n'a pas accès à cette rangée.
func (s *Service) PSEThemeCatalogTitles(ctx context.Context, viewer *core.Viewer, typ, rowID string) ([]core.Title, error) {
	themes := frontend.CurrentPSEThemes()
	if themes == nil {
		return nil, core.ErrNotFound
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, err
	}
	ctx = core.WithCatalogViewer(ctx, v)
	for _, r := range s.pseStremioRows(ctx, v, themes, rowID) {
		if r.typ != typ {
			continue
		}
		membership := pseJellyfinMembership{keys: map[core.RatingKey]bool{}, langFree: map[core.RatingKey]bool{}}
		for _, key := range pseJellyfinRowKeys(ctx, themes, v, r.row.ID) {
			if !pseJellyfinThemePartKey(r.part.Type, key) || membership.keys[key] {
				continue
			}
			membership.ordered = append(membership.ordered, key)
			membership.keys[key] = true
			if pseJellyfinRowLangFree(r.part, r.row) {
				membership.langFree[key] = true
			}
		}
		var out []core.Title
		for i := 0; i < len(membership.ordered); i += 400 {
			titles, err := s.pseJellyfinThemeTitles(ctx, v, membership.ordered[i:min(i+400, len(membership.ordered))], membership)
			if err != nil {
				return nil, err
			}
			out = append(out, titles...)
		}
		return out, nil
	}
	return nil, core.ErrNotFound
}
