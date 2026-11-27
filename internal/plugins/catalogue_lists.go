package plugins

import (
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/stremio"
)

// CatalogueList is one movie or series catalog a plugin's list source
// reads without parameters: the unit a library attaches as a catalogue
// source. The catalogue source id is "stremio:<plugin>:<Key>" and the sync
// gathers it through the list source List, so every catalogue source a
// plugin offers is backed by a list-source kind the plugin registers.
type CatalogueList struct {
	// Key is the catalog's key (StremioCatalogKey).
	Key string
	// List is the list-source key that reads the catalog.
	List string
	// Label is the list source's label (plain text or "$t:key").
	Label string
	// Catalog is the catalog read; its Type is always movie or series.
	Catalog StremioCat
}

// ListCatalog is the catalog a list source reads ("movie/top", or an id
// alone) and whether the manifest declares it. A plugin's own list the
// manifest does not declare pages with skip, as the plugin contract says.
// A catalog without a type serves whichever of movies and series a
// collection asks for.
func ListCatalog(m stremio.Manifest, ls ListSourceMeta) (stremio.Catalog, bool) {
	spec := strings.TrimSuffix(strings.TrimPrefix(strings.Trim(strings.TrimSpace(ls.Catalog), "/"), "catalog/"), ".json")
	typ, id, ok := strings.Cut(spec, "/")
	if !ok {
		typ, id = "", spec
	}
	typ, id = strings.ToLower(strings.TrimSpace(typ)), strings.TrimSpace(id)
	if id == "" {
		id = ls.Key
	}
	switch typ {
	case "show", "tv":
		typ = stremio.TypeSeries
	}
	if c, ok := m.CatalogNamed(typ, id); ok {
		return c, true
	}
	return stremio.Catalog{Type: typ, ID: id, Extra: []stremio.Extra{{Name: "skip"}}}, false
}

// CatalogueLists are the catalogue sources the plugin offers, one per
// movie or series catalog its parameter-free list sources read. A list
// without a type offers each type the manifest declares that catalog for,
// or, when it declares neither, each type the add-on serves.
func (m Manifest) CatalogueLists() []CatalogueList {
	if !m.HasCapability(CapListSource) {
		return nil
	}
	sm := m.Stremio()
	types := []string{stremio.TypeMovie, stremio.TypeSeries}
	seen := map[string]bool{}
	var out []CatalogueList
	for _, ls := range m.EffectiveEther().Contributes.ListSources {
		if ls.Key == "" || slices.ContainsFunc(ParseListParamFields(ls.Params), func(f ListParamField) bool { return f.Required }) {
			continue
		}
		cat, _ := ListCatalog(sm, ls)
		var cats []stremio.Catalog
		if cat.Type != "" {
			cats = append(cats, cat)
		} else {
			for _, typ := range types {
				if c, ok := sm.CatalogNamed(typ, cat.ID); ok {
					cats = append(cats, c)
				}
			}
			if len(cats) == 0 {
				for _, typ := range types {
					if len(sm.Types) == 0 || slices.Contains(sm.Types, typ) {
						c := cat
						c.Type = typ
						cats = append(cats, c)
					}
				}
			}
		}
		for _, c := range cats {
			if (c.Type != stremio.TypeMovie && c.Type != stremio.TypeSeries) || !c.Standalone() {
				continue
			}
			key := StremioCatalogKey(c)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, CatalogueList{Key: key, List: ls.Key, Label: ls.Label, Catalog: c})
		}
	}
	return out
}

// CatalogueList finds the catalogue source with the given catalog key.
func (m Manifest) CatalogueList(key string) (CatalogueList, bool) {
	for _, c := range m.CatalogueLists() {
		if c.Key == key {
			return c, true
		}
	}
	return CatalogueList{}, false
}
