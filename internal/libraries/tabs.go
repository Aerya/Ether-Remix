package libraries

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("libraries", 11, `ALTER TABLE libraries ADD COLUMN tabs TEXT NOT NULL DEFAULT '';`)
	store.RegisterDialect("libraries", 11, store.Postgres, `ALTER TABLE libraries ADD COLUMN tabs TEXT NOT NULL DEFAULT '';`)
}

// MaxLibraryTabs bounds how many tabs an owner may configure on a single library.
const MaxLibraryTabs = 50

// PluginTabSchema describes the declared levels, facets and sorts of a plugin
// library kind when validating owner-defined tabs. Nil skips manifest-specific
// checks when a standalone libraries.Service runs without the plugin registry.
type PluginTabSchema struct {
	Levels           map[string]bool
	CollectionFacets map[string]bool
	PeopleFacets     map[string]bool
	Sorts            map[string]bool
}

var allowedCatalogueTabSorts = map[string]string{
	"popularity":  "popularity",
	"addedat":     "added",
	"added_at":    "added",
	"added":       "added",
	"released":    "released",
	"year":        "year",
	"rating":      "rating",
	"duration":    "duration",
	"runtime":     "duration",
	"title":       "title",
	"server":      "server",
	"lastviewed":  "lastViewed",
	"last_viewed": "lastViewed",
	"played":      "lastViewed",
}

func validTabKey(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || (i > 0 && (c == '-' || c == '_'))
		if !ok {
			return false
		}
	}
	return true
}

func validIdent(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// ValidateTabs normalizes and validates a library's owner-defined tabs,
// compiling any saved view or library tab filters against the library's media
// type or plugin schema.
func (s *Service) ValidateTabs(ctx context.Context, lib core.Library, tabs []core.LibraryTab, pluginSchema *PluginTabSchema) ([]core.LibraryTab, error) {
	if len(tabs) == 0 {
		return nil, fmt.Errorf("%w: library must have at least one tab", ErrInvalid)
	}
	if len(tabs) > MaxLibraryTabs {
		return nil, fmt.Errorf("%w: library may have at most %d tabs", ErrInvalid, MaxLibraryTabs)
	}

	pinnedMedia := core.MediaType("mixed")
	if lib.MediaType == core.TypeMovie || lib.MediaType == core.TypeShow {
		pinnedMedia = lib.MediaType
	}

	compileFilter := s.compileCatalogueRules
	if lib.Source == core.LibraryPlugin {
		compileFilter = func(specs []core.LibraryExclusion, fallback string) (*libraryfilter.Filter, error) {
			if s.compilePluginPolicy != nil {
				return s.compilePluginPolicy(ctx, lib.ID, specs, fallback)
			}
			return libraryfilter.CompilePluginPolicy(specs, fallback, nil)
		}
	}

	out := make([]core.LibraryTab, len(tabs))
	usedKeys := make(map[string]bool, len(tabs))
	visibleCount := 0

	for i, raw := range tabs {
		t := raw.Clone()
		t.Label = strings.TrimSpace(t.Label)
		if t.Label == "" || utf8.RuneCountInString(t.Label) > 80 {
			return nil, fmt.Errorf("%w: tab %d needs a label (1-80 characters)", ErrInvalid, i+1)
		}

		t.Key = strings.ToLower(strings.TrimSpace(t.Key))
		if t.Key == "" {
			base := slugify(t.Label)
			if base == "" {
				base = "tab"
			}
			if len(base) > 56 {
				base = base[:56]
			}
			key := base
			for n := 2; usedKeys[key]; n++ {
				key = fmt.Sprintf("%s-%d", base, n)
			}
			t.Key = key
		} else {
			if !validTabKey(t.Key) {
				return nil, fmt.Errorf("%w: invalid tab key %q", ErrInvalid, t.Key)
			}
			if usedKeys[t.Key] {
				return nil, fmt.Errorf("%w: duplicate tab key %q", ErrInvalid, t.Key)
			}
		}
		usedKeys[t.Key] = true

		t.Kind = strings.ToLower(strings.TrimSpace(t.Kind))
		switch t.Kind {
		case core.TabRecommended, core.TabLibrary, core.TabCollections, core.TabPeople, core.TabView:
		case core.TabLive:
			if lib.Source != core.LibraryPlugin {
				return nil, fmt.Errorf("%w: live tab %q is only supported on plugin libraries", ErrInvalid, t.Key)
			}
		default:
			return nil, fmt.Errorf("%w: tab %q has unknown kind %q", ErrInvalid, t.Key, t.Kind)
		}

		// Card validation (live and recommended tabs).
		t.Card = strings.ToLower(strings.TrimSpace(t.Card))
		switch t.Kind {
		case core.TabLive:
			if !core.ValidCardShape(t.Card) {
				return nil, fmt.Errorf("%w: live tab %q card must be poster, landscape or square", ErrInvalid, t.Key)
			}
		case core.TabRecommended:
			if t.Card != "" && !core.ValidCardShape(t.Card) {
				return nil, fmt.Errorf("%w: recommended tab %q card must be poster, landscape or square", ErrInvalid, t.Key)
			}
		default:
			if t.Card != "" {
				return nil, fmt.Errorf("%w: tab %q of kind %s cannot set card", ErrInvalid, t.Key, t.Kind)
			}
		}

		// Layout validation (view tabs only).
		t.Layout = strings.ToLower(strings.TrimSpace(t.Layout))
		if t.Kind == core.TabView {
			if !core.ValidTabLayout(t.Layout) {
				return nil, fmt.Errorf("%w: view tab %q layout must be grid, list or timeline", ErrInvalid, t.Key)
			}
		} else if t.Layout != "" {
			return nil, fmt.Errorf("%w: tab %q of kind %s cannot set layout", ErrInvalid, t.Key, t.Kind)
		}

		// Facet validation.
		t.Facet = strings.TrimSpace(t.Facet)
		switch t.Kind {
		case core.TabCollections:
			if lib.Source == core.LibraryPlugin {
				if t.Facet == "" || !validIdent(t.Facet) {
					return nil, fmt.Errorf("%w: plugin collections tab %q requires a declared facet", ErrInvalid, t.Key)
				}
				if pluginSchema != nil && !pluginSchema.CollectionFacets[t.Facet] {
					return nil, fmt.Errorf("%w: tab %q uses undeclared collections facet %q", ErrInvalid, t.Key, t.Facet)
				}
			} else {
				norm, ok := core.NormalizeCatalogueTabFacet(core.TabCollections, t.Facet)
				if !ok {
					return nil, fmt.Errorf("%w: tab %q has unsupported collections facet %q", ErrInvalid, t.Key, t.Facet)
				}
				t.Facet = norm
			}
		case core.TabPeople:
			if lib.Source == core.LibraryPlugin {
				if t.Facet == "" || !validIdent(t.Facet) {
					return nil, fmt.Errorf("%w: plugin people tab %q requires a declared people facet", ErrInvalid, t.Key)
				}
				if pluginSchema != nil && !pluginSchema.PeopleFacets[t.Facet] {
					return nil, fmt.Errorf("%w: tab %q uses undeclared people facet %q", ErrInvalid, t.Key, t.Facet)
				}
			} else {
				norm, ok := core.NormalizeCatalogueTabFacet(core.TabPeople, t.Facet)
				if !ok {
					return nil, fmt.Errorf("%w: tab %q has unsupported people facet %q", ErrInvalid, t.Key, t.Facet)
				}
				t.Facet = norm
			}
		default:
			if t.Facet != "" {
				return nil, fmt.Errorf("%w: tab %q of kind %s cannot set facet", ErrInvalid, t.Key, t.Kind)
			}
		}

		// Level validation (plugin library, view, collections, and people tabs).
		t.Level = strings.TrimSpace(t.Level)
		if t.Level != "" {
			if lib.Source != core.LibraryPlugin {
				return nil, fmt.Errorf("%w: tab %q cannot set level on a non-plugin library", ErrInvalid, t.Key)
			}
			switch t.Kind {
			case core.TabLibrary, core.TabView, core.TabCollections, core.TabPeople:
				if !validIdent(t.Level) {
					return nil, fmt.Errorf("%w: tab %q has invalid level %q", ErrInvalid, t.Key, t.Level)
				}
				if pluginSchema != nil && len(pluginSchema.Levels) > 0 && !pluginSchema.Levels[t.Level] {
					return nil, fmt.Errorf("%w: tab %q uses undeclared level %q", ErrInvalid, t.Key, t.Level)
				}
			default:
				return nil, fmt.Errorf("%w: tab %q of kind %s cannot set level", ErrInvalid, t.Key, t.Kind)
			}
		}

		// Order validation (count|name on facet collections and people).
		t.Order = strings.ToLower(strings.TrimSpace(t.Order))
		if t.Order != "" {
			allowsOrder := t.Kind == core.TabPeople || (t.Kind == core.TabCollections && t.Facet != "")
			if !allowsOrder {
				return nil, fmt.Errorf("%w: tab %q cannot set order", ErrInvalid, t.Key)
			}
			if t.Order != "count" && t.Order != "name" {
				return nil, fmt.Errorf("%w: tab %q order must be count or name", ErrInvalid, t.Key)
			}
		}

		// Sort & direction validation (library, view, or facet collections).
		t.Sort = strings.TrimSpace(t.Sort)
		t.Direction = strings.ToLower(strings.TrimSpace(t.Direction))
		allowsSort := t.Kind == core.TabLibrary || t.Kind == core.TabView || (t.Kind == core.TabCollections && t.Facet != "")
		if !allowsSort {
			if t.Sort != "" || t.Direction != "" {
				return nil, fmt.Errorf("%w: tab %q cannot set sort or direction", ErrInvalid, t.Key)
			}
		} else {
			if t.Sort != "" {
				if lib.Source == core.LibraryPlugin {
					if core.IsLastViewedSort(t.Sort) {
						t.Sort = string(core.SortLastViewed)
					} else if !validIdent(t.Sort) || (pluginSchema != nil && !pluginSchema.Sorts[t.Sort]) {
						return nil, fmt.Errorf("%w: tab %q has unsupported sort %q", ErrInvalid, t.Key, t.Sort)
					}
				} else {
					canon, ok := allowedCatalogueTabSorts[strings.ToLower(t.Sort)]
					if !ok {
						return nil, fmt.Errorf("%w: tab %q has unsupported sort %q", ErrInvalid, t.Key, t.Sort)
					}
					t.Sort = canon
				}
			}
			if t.Direction != "" && t.Direction != "asc" && t.Direction != "desc" {
				return nil, fmt.Errorf("%w: tab %q direction must be asc or desc", ErrInvalid, t.Key)
			}
		}

		// Search & SearchPlaceholder validation.
		t.SearchPlaceholder = strings.TrimSpace(t.SearchPlaceholder)
		if utf8.RuneCountInString(t.SearchPlaceholder) > 120 {
			return nil, fmt.Errorf("%w: tab %q search placeholder is too long", ErrInvalid, t.Key)
		}
		if t.Kind == core.TabRecommended {
			if t.Search != nil && *t.Search {
				return nil, fmt.Errorf("%w: recommended tab %q cannot enable in-tab search", ErrInvalid, t.Key)
			}
			t.Search = nil
			t.SearchPlaceholder = ""
		}

		// Filters validation (allowed on view and library).
		if t.Filters != nil && t.Filters.IsEmpty() {
			t.Filters = nil
		}
		if t.Filters != nil {
			if t.Kind != core.TabView && t.Kind != core.TabLibrary {
				return nil, fmt.Errorf("%w: tab %q of kind %s cannot set filters", ErrInvalid, t.Key, t.Kind)
			}
			specs, fallback := t.Filters.ToPolicy(pinnedMedia)
			if _, err := compileFilter(specs, fallback); err != nil {
				return nil, fmt.Errorf("%w: tab %q filters: %v", ErrInvalid, t.Key, err)
			}
		}

		if !t.Hidden {
			visibleCount++
		}
		out[i] = t
	}

	if visibleCount == 0 {
		return nil, fmt.Errorf("%w: at least one tab must be visible", ErrInvalid)
	}
	return out, nil
}

// SetTabs validates and saves owner-defined tabs for library id.
func (s *Service) SetTabs(ctx context.Context, id core.LibraryID, tabs []core.LibraryTab, pluginSchema *PluginTabSchema) ([]core.LibraryTab, error) {
	lib, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	validated, err := s.ValidateTabs(ctx, lib, tabs, pluginSchema)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(validated)
	if err != nil {
		return nil, err
	}
	if err := s.setColumn(ctx, id, "tabs", string(raw)); err != nil {
		return nil, err
	}
	return validated, nil
}

// ResetTabs clears any owner-defined tabs on library id so it falls back to
// plugin-declared tabs or DefaultCatalogueTabs.
func (s *Service) ResetTabs(ctx context.Context, id core.LibraryID) error {
	return s.setColumn(ctx, id, "tabs", "")
}
