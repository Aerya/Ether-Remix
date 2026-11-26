package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
)

const libraryTabsScopeNote = "Vela shows these as tabs. Plex, Jellyfin, Emby and Silo show the first rows tab as the library's Recommended page."

var catalogueTabSortOptions = []string{
	"popularity",
	"added",
	"released",
	"year",
	"rating",
	"duration",
	"title",
	"lastViewed",
	"server",
}

type tabFacetOptionDTO struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Category string `json:"category,omitempty"`
}

type tabFacetsCatalogDTO struct {
	Collections []tabFacetOptionDTO `json:"collections"`
	People      []tabFacetOptionDTO `json:"people"`
}

type libraryTabsDTO struct {
	LibraryID       int64               `json:"libraryId"`
	Customized      bool                `json:"customized"`
	Tabs            []core.LibraryTab   `json:"tabs"`
	EffectiveTabs   []core.LibraryTab   `json:"effectiveTabs"`
	Defaults        []core.LibraryTab   `json:"defaults"`
	ScopeNote       string              `json:"scopeNote"`
	AvailableFacets tabFacetsCatalogDTO `json:"availableFacets"`
	AvailableSorts  []string            `json:"availableSorts"`
}

type tabPreviewCollectionDTO struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	ItemCount  int         `json:"itemCount"`
	NewestItem *core.Title `json:"newestItem,omitempty"`
}

type tabPreviewPersonDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	ItemCount int    `json:"itemCount"`
}

func (c *librariesConsole) pluginKindDecl(ctx context.Context, lib core.Library) (plugins.MediaKindMeta, bool) {
	if lib.Source != core.LibraryPlugin {
		return plugins.MediaKindMeta{}, false
	}
	items, _, err := c.services()
	if err != nil || items == nil {
		return plugins.MediaKindMeta{}, false
	}
	rec, err := items.GetKindByLibrary(ctx, lib.ID)
	if err != nil {
		return plugins.MediaKindMeta{}, false
	}
	return rec.Decl, true
}

func isGroupablePluginCollectionField(fd plugins.KindField) bool {
	switch fd.Type {
	case "string", "strings", "enum", "int", "float", "date", "bool", "duration":
		return true
	default:
		return false
	}
}

func (c *librariesConsole) pluginTabSchema(ctx context.Context, lib core.Library) *libraries.PluginTabSchema {
	if lib.Source != core.LibraryPlugin {
		return nil
	}
	decl, ok := c.pluginKindDecl(ctx, lib)
	if !ok {
		return nil
	}
	schema := &libraries.PluginTabSchema{
		Levels: make(map[string]bool, len(decl.Levels)),
		CollectionFacets: map[string]bool{
			"year":     true,
			"decade":   true,
			"released": true,
			"rating":   true,
		},
		PeopleFacets: make(map[string]bool),
		Sorts:        make(map[string]bool),
	}
	for _, lv := range decl.Levels {
		schema.Levels[lv.Key] = true
	}
	for _, fd := range decl.Fields {
		if fd.Type == "people" {
			schema.PeopleFacets[fd.Key] = true
		} else if isGroupablePluginCollectionField(fd) {
			schema.CollectionFacets[fd.Key] = true
			if fd.Type == "date" {
				schema.CollectionFacets[fd.Key+"-decade"] = true
			}
		}
	}
	for _, s := range decl.Browse.Sorts {
		schema.Sorts[s] = true
	}
	if len(schema.Sorts) == 0 {
		schema.Sorts["server"] = true
	}
	schema.Sorts[string(core.SortLastViewed)] = true
	return schema
}

func (c *librariesConsole) defaultTabsForLibrary(ctx context.Context, lib core.Library) []core.LibraryTab {
	if lib.Source == core.LibraryPlugin {
		if decl, ok := c.pluginKindDecl(ctx, lib); ok {
			rawTabs := decl.Library.EffectiveTabs()
			out := make([]core.LibraryTab, 0, len(rawTabs))
			for _, t := range rawTabs {
				search := t.Kind != core.TabRecommended
				if t.Kind == core.TabCollections || t.Kind == core.TabPeople || t.Kind == core.TabView || t.Kind == core.TabLive {
					search = t.EffectiveSearch()
				}
				label := c.catalogueLabel(ctx, lib.PluginID, t.Label, t.Key)
				var placeholder string
				if search && strings.TrimSpace(t.SearchPlaceholder) != "" {
					placeholder = c.catalogueLabel(ctx, lib.PluginID, t.SearchPlaceholder, "")
				}
				sortID := t.Sort
				if sortID != "" && core.IsLastViewedSort(sortID) {
					sortID = string(core.SortLastViewed)
				}
				tab := core.LibraryTab{
					Key:               t.Key,
					Label:             label,
					Kind:              t.Kind,
					Level:             t.Level,
					Facet:             t.Facet,
					Order:             t.Order,
					Sort:              sortID,
					Direction:         t.Direction,
					Card:              t.EffectiveCard(),
					Layout:            t.EffectiveLayout(),
					Search:            &search,
					SearchPlaceholder: placeholder,
					Filters:           t.Filters.Clone(),
				}
				if tab.Kind == core.TabCollections && tab.Facet != "" && tab.Order == "" {
					tab.Order = "count"
				} else if tab.Kind == core.TabPeople && tab.Facet != "" && tab.Order == "" {
					tab.Order = "name"
				}
				out = append(out, tab)
			}
			return out
		}
	}
	return core.DefaultCatalogueTabs()
}

func (c *librariesConsole) effectiveTabs(ctx context.Context, lib core.Library) []core.LibraryTab {
	if lib.TabsCustomized {
		out := make([]core.LibraryTab, len(lib.Tabs))
		for i, t := range lib.Tabs {
			cp := t.Clone()
			if lib.Source == core.LibraryPlugin {
				cp.Label = c.catalogueLabel(ctx, lib.PluginID, cp.Label, cp.Key)
				if strings.TrimSpace(cp.SearchPlaceholder) != "" {
					cp.SearchPlaceholder = c.catalogueLabel(ctx, lib.PluginID, cp.SearchPlaceholder, "")
				}
			}
			eff := cp.EffectiveSearch()
			cp.Search = &eff
			out[i] = cp
		}
		return out
	}
	return c.defaultTabsForLibrary(ctx, lib)
}

func (c *librariesConsole) buildLibraryTabsDTO(ctx context.Context, lib core.Library) libraryTabsDTO {
	defaults := c.defaultTabsForLibrary(ctx, lib)
	effective := c.effectiveTabs(ctx, lib)
	stored := []core.LibraryTab{}
	if lib.TabsCustomized && len(lib.Tabs) > 0 {
		stored = make([]core.LibraryTab, len(lib.Tabs))
		for i, t := range lib.Tabs {
			stored[i] = t.Clone()
		}
	}

	facets := tabFacetsCatalogDTO{
		Collections: []tabFacetOptionDTO{},
		People:      []tabFacetOptionDTO{},
	}
	var sorts []string

	if lib.Source == core.LibraryPlugin {
		if decl, ok := c.pluginKindDecl(ctx, lib); ok {
			fieldsByKey := make(map[string]plugins.KindField, len(decl.Fields))
			for _, fd := range decl.Fields {
				fieldsByKey[fd.Key] = fd
			}
			seenCol := make(map[string]bool)
			seenPeople := make(map[string]bool)
			addCol := func(id, label, category string) {
				if seenCol[id] {
					return
				}
				seenCol[id] = true
				facets.Collections = append(facets.Collections, tabFacetOptionDTO{
					ID:       id,
					Label:    label,
					Category: category,
				})
			}
			addPerson := func(id, label string) {
				if seenPeople[id] {
					return
				}
				seenPeople[id] = true
				facets.People = append(facets.People, tabFacetOptionDTO{
					ID:       id,
					Label:    label,
					Category: "people",
				})
			}
			addDeclaredField := func(fd plugins.KindField) {
				label := c.catalogueLabel(ctx, lib.PluginID, fd.Label, fd.Key)
				if fd.Type == "people" {
					addPerson(fd.Key, label)
					return
				}
				if !isGroupablePluginCollectionField(fd) {
					return
				}
				addCol(fd.Key, label, "plugin")
				if fd.Type == "date" {
					addCol(fd.Key+"-decade", label+" (decade)", "plugin")
				}
			}
			for _, fk := range decl.Browse.Facets {
				switch fk {
				case "year":
					addCol("year", "Year", "essentials")
				case "decade":
					addCol("decade", "Decade", "essentials")
				case "released":
					addCol("released", "Released", "essentials")
				case "rating":
					addCol("rating", "Rating", "essentials")
				default:
					if fd, exists := fieldsByKey[fk]; exists {
						addDeclaredField(fd)
					}
				}
			}
			for _, fd := range decl.Fields {
				addDeclaredField(fd)
			}
			addCol("year", "Year", "essentials")
			addCol("decade", "Decade", "essentials")
			addCol("released", "Released", "essentials")
			addCol("rating", "Rating", "essentials")
			sorts = append(sorts, decl.Browse.Sorts...)
		}
		if len(sorts) == 0 {
			sorts = []string{"server"}
		}
		if !slices.Contains(sorts, string(core.SortLastViewed)) {
			sorts = append(sorts, string(core.SortLastViewed))
		}
	} else {
		hasStudios := c.app != nil && c.app.catalog != nil && c.app.catalog.HasStudioData(ctx, libraryScopeFilter(lib, core.Filter{}))
		facets.Collections = []tabFacetOptionDTO{
			{ID: "", Label: "Your collections", Category: "essentials"},
			{ID: "genre", Label: "Genres", Category: "essentials"},
		}
		switch lib.MediaType {
		case core.TypeShow:
			facets.Collections = append(facets.Collections, tabFacetOptionDTO{ID: "network", Label: "Networks", Category: "essentials"})
			if hasStudios {
				facets.Collections = append(facets.Collections, tabFacetOptionDTO{ID: "studio", Label: "Studios", Category: "essentials"})
			}
		case core.TypeMovie:
			facets.Collections = append(facets.Collections, tabFacetOptionDTO{ID: "studio", Label: "Studios", Category: "essentials"})
			if hasStudios {
				facets.Collections = append(facets.Collections, tabFacetOptionDTO{ID: "network", Label: "Networks", Category: "essentials"})
			}
		default:
			facets.Collections = append(facets.Collections,
				tabFacetOptionDTO{ID: "studio", Label: "Studios", Category: "essentials"},
				tabFacetOptionDTO{ID: "network", Label: "Networks", Category: "essentials"},
			)
		}
		facets.Collections = append(facets.Collections,
			tabFacetOptionDTO{ID: "country", Label: "Countries", Category: "essentials"},
			tabFacetOptionDTO{ID: "language", Label: "Languages", Category: "essentials"},
			tabFacetOptionDTO{ID: "contentRating", Label: "Content ratings", Category: "essentials"},
			tabFacetOptionDTO{ID: "year", Label: "Years", Category: "essentials"},
			tabFacetOptionDTO{ID: "decade", Label: "Decades", Category: "essentials"},
		)
		if lib.MediaType != core.TypeShow {
			facets.Collections = append(facets.Collections, tabFacetOptionDTO{ID: "franchise", Label: "Franchises", Category: "essentials"})
		}
		facets.Collections = append(facets.Collections,
			tabFacetOptionDTO{ID: "tag", Label: "Tags", Category: "essentials"},
			tabFacetOptionDTO{ID: "keyword", Label: "Keywords", Category: "essentials"},
			tabFacetOptionDTO{ID: "rating", Label: "Rating bands", Category: "essentials"},
			tabFacetOptionDTO{ID: "runtime", Label: "Runtime bands", Category: "essentials"},
			tabFacetOptionDTO{ID: "addedMonth", Label: "Added month", Category: "essentials"},
			tabFacetOptionDTO{ID: "director", Label: "Directors", Category: "people"},
			tabFacetOptionDTO{ID: "writer", Label: "Writers", Category: "people"},
			tabFacetOptionDTO{ID: "resolution", Label: "Resolutions", Category: "technical"},
			tabFacetOptionDTO{ID: "hdr", Label: "HDR", Category: "technical"},
			tabFacetOptionDTO{ID: "audioLanguage", Label: "Audio languages", Category: "technical"},
		)
		facets.People = []tabFacetOptionDTO{
			{ID: "cast", Label: "Actors", Category: "people"},
			{ID: "director", Label: "Directors", Category: "people"},
			{ID: "writer", Label: "Writers", Category: "people"},
		}
		sorts = append([]string(nil), catalogueTabSortOptions...)
	}

	return libraryTabsDTO{
		LibraryID:       int64(lib.ID),
		Customized:      lib.TabsCustomized,
		Tabs:            stored,
		EffectiveTabs:   effective,
		Defaults:        defaults,
		ScopeNote:       libraryTabsScopeNote,
		AvailableFacets: facets,
		AvailableSorts:  sorts,
	}
}

func (c *librariesConsole) handleTabsGet(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	libJSON(w, http.StatusOK, c.buildLibraryTabsDTO(r.Context(), lib))
}

func decodeTabsPayload(w http.ResponseWriter, r *http.Request) ([]core.LibraryTab, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		libError(w, http.StatusBadRequest, "Invalid request body")
		return nil, false
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		libError(w, http.StatusBadRequest, "Invalid request body")
		return nil, false
	}
	if trimmed[0] == '[' {
		var list []core.LibraryTab
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&list); err != nil {
			libError(w, http.StatusBadRequest, "Invalid tabs payload")
			return nil, false
		}
		return list, true
	}
	var wrapper struct {
		Tabs []core.LibraryTab `json:"tabs"`
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wrapper); err != nil || wrapper.Tabs == nil {
		libError(w, http.StatusBadRequest, "Invalid tabs payload")
		return nil, false
	}
	return wrapper.Tabs, true
}

func (c *librariesConsole) handleTabsPut(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	tabs, ok := decodeTabsPayload(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	schema := c.pluginTabSchema(ctx, lib)
	if _, err := c.app.libraries.SetTabs(ctx, lib.ID, tabs, schema); err != nil {
		libError(w, libErrStatus(err), err.Error())
		return
	}
	fresh, err := c.app.libraries.Get(ctx, lib.ID)
	if err != nil {
		libError(w, libErrStatus(err), "Library not found")
		return
	}
	libJSON(w, http.StatusOK, c.buildLibraryTabsDTO(ctx, fresh))
}

func (c *librariesConsole) handleTabsReset(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := c.app.libraries.ResetTabs(ctx, lib.ID); err != nil {
		libError(w, libErrStatus(err), "Could not reset library tabs")
		return
	}
	fresh, err := c.app.libraries.Get(ctx, lib.ID)
	if err != nil {
		libError(w, libErrStatus(err), "Library not found")
		return
	}
	libJSON(w, http.StatusOK, c.buildLibraryTabsDTO(ctx, fresh))
}

type tabPreviewRequest struct {
	Tab   *core.LibraryTab `json:"tab"`
	Query string           `json:"query"`
	Limit int              `json:"limit"`
}

func decodeTabPreviewRequest(w http.ResponseWriter, r *http.Request) (core.LibraryTab, string, int, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		libError(w, http.StatusBadRequest, "Invalid request body")
		return core.LibraryTab{}, "", 0, false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		libError(w, http.StatusBadRequest, "Invalid request body")
		return core.LibraryTab{}, "", 0, false
	}
	if _, hasTab := probe["tab"]; hasTab {
		var req tabPreviewRequest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil || req.Tab == nil {
			libError(w, http.StatusBadRequest, "Invalid tab preview body")
			return core.LibraryTab{}, "", 0, false
		}
		return *req.Tab, strings.TrimSpace(req.Query), req.Limit, true
	}
	var flat core.LibraryTab
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&flat); err != nil {
		libError(w, http.StatusBadRequest, "Invalid tab preview body")
		return core.LibraryTab{}, "", 0, false
	}
	return flat, "", 0, true
}

func effectivePreviewSortDir(tab core.LibraryTab, defaultSort string) (string, string) {
	s := cmp.Or(tab.Sort, defaultSort)
	d := tab.Direction
	if d == "" {
		switch s {
		case "server", "title", "index":
			d = "asc"
		default:
			d = "desc"
		}
	}
	return s, d
}

func (c *librariesConsole) handleTabsPreview(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	draft, query, limit, ok := decodeTabPreviewRequest(w, r)
	if !ok {
		return
	}
	if len(query) > 256 {
		libError(w, http.StatusBadRequest, "Search query is too long")
		return
	}
	if limit <= 0 {
		limit = 12
	} else if limit > 48 {
		limit = 48
	}
	if strings.TrimSpace(draft.Label) == "" {
		draft.Label = "Preview"
	}
	if strings.TrimSpace(draft.Key) == "" {
		draft.Key = "preview"
	}
	draft.Hidden = false

	ctx := r.Context()
	schema := c.pluginTabSchema(ctx, lib)
	validatedList, err := c.app.libraries.ValidateTabs(ctx, lib, []core.LibraryTab{draft}, schema)
	if err != nil {
		libError(w, http.StatusBadRequest, err.Error())
		return
	}
	tab := validatedList[0]

	switch tab.Kind {
	case core.TabRecommended:
		libJSON(w, http.StatusOK, map[string]any{
			"kind":      core.TabRecommended,
			"scopeNote": libraryTabsScopeNote,
		})

	case core.TabLive:
		libJSON(w, http.StatusOK, map[string]any{
			"kind":      core.TabLive,
			"card":      tab.EffectiveCard(),
			"scopeNote": libraryTabsScopeNote,
		})

	case core.TabLibrary, core.TabView:
		owner := c.consoleOwner(r)
		ownerID := int64(0)
		if owner != nil {
			ownerID = owner.ID
			ctx = core.WithCatalogViewer(ctx, owner)
		}
		if lib.Source == core.LibraryPlugin {
			items, _, err := c.services()
			if err != nil || items == nil {
				libError(w, http.StatusServiceUnavailable, "Plugin items are unavailable")
				return
			}
			decl, _ := c.pluginKindDecl(ctx, lib)
			defaultSort := "server"
			if len(decl.Browse.Sorts) > 0 {
				defaultSort = decl.Browse.Sorts[0]
			}
			effSort, effDir := effectivePreviewSortDir(tab, defaultSort)
			rootLevel := ""
			if len(decl.Levels) > 0 {
				rootLevel = decl.Levels[0].Key
			}
			targetLevel := cmp.Or(tab.Level, rootLevel)
			policy, err := items.LibraryPolicy(ctx, lib)
			if err != nil {
				libError(w, http.StatusBadRequest, "Invalid library filter")
				return
			}
			var tabPolicy *libraryfilter.Filter
			if tab.Filters != nil {
				rules, fallback := tab.Filters.ToPolicy("")
				if len(rules) > 0 || fallback != "" {
					tabPolicy, err = items.CompilePolicy(ctx, lib.ID, rules, fallback)
					if err != nil {
						libError(w, http.StatusBadRequest, err.Error())
						return
					}
				}
			}
			if query != "" {
				sq := pluginitems.SearchQuery{
					Policy:    policy.ForSearch(),
					TabPolicy: tabPolicy.ForSearch(),
					LibraryID: lib.ID,
					ViewerID:  ownerID,
					Query:     query,
					Level:     targetLevel,
					Limit:     limit,
				}
				page, err := items.Search(ctx, sq)
				if err != nil {
					libError(w, http.StatusInternalServerError, "Preview search failed")
					return
				}
				libJSON(w, http.StatusOK, map[string]any{
					"kind":      tab.Kind,
					"sort":      effSort,
					"direction": effDir,
					"total":     page.Total,
					"items":     kindDTOs(page.Items),
				})
				return
			}
			browseSort := ""
			if effSort != "server" && effSort != "" {
				browseSort = effSort + ":" + effDir
			}
			page, err := items.Browse(ctx, pluginitems.BrowseQuery{
				Policy:    policy,
				TabPolicy: tabPolicy,
				LibraryID: lib.ID,
				ViewerID:  ownerID,
				Level:     targetLevel,
				Sort:      browseSort,
				Limit:     limit,
			})
			if err != nil {
				libError(w, http.StatusBadRequest, "Could not preview tab")
				return
			}
			libJSON(w, http.StatusOK, map[string]any{
				"kind":      tab.Kind,
				"sort":      effSort,
				"direction": effDir,
				"total":     page.Total,
				"items":     kindDTOs(page.Items),
			})
			return
		}

		if c.app.collections == nil {
			libError(w, http.StatusServiceUnavailable, "Catalogue browse is unavailable")
			return
		}
		effSort, effDir := effectivePreviewSortDir(tab, "popularity")
		var reqFilter core.Filter
		reqFilter.Query = query
		reqFilter.CatalogViewerID = ownerID
		if tab.Filters != nil {
			reqFilter.TabRules, reqFilter.TabFallback = tab.Filters.ToPolicy(lib.MediaType)
		}
		var browseSort core.SortOrder
		if effSort != "server" {
			browseSort = core.SortOrder(effSort)
		} else {
			browseSort = core.SortTitle
		}
		bq := core.BrowseQuery{
			Sort:  browseSort,
			Desc:  effDir == "desc",
			Limit: limit,
		}
		if browseSort.IsLastViewed() {
			bq.Sort = core.SortTitle
			bq.SortLastViewed = true
			bq.SortViewerID = ownerID
		}
		page, err := c.browseTitles(ctx, lib, reqFilter, bq)
		if err != nil {
			libError(w, http.StatusInternalServerError, "Could not preview tab")
			return
		}
		if page.Items == nil {
			page.Items = []core.Title{}
		}
		libJSON(w, http.StatusOK, map[string]any{
			"kind":      tab.Kind,
			"sort":      effSort,
			"direction": effDir,
			"total":     page.Total,
			"items":     page.Items,
		})

	case core.TabCollections:
		effOrder := cmp.Or(tab.Order, "count")
		if lib.Source == core.LibraryPlugin {
			items, _, err := c.services()
			if err != nil || items == nil {
				libError(w, http.StatusServiceUnavailable, "Plugin items are unavailable")
				return
			}
			decl, _ := c.pluginKindDecl(ctx, lib)
			rootLevel := ""
			if len(decl.Levels) > 0 {
				rootLevel = decl.Levels[0].Key
			}
			policy, err := items.LibraryPolicy(ctx, lib)
			if err != nil {
				libError(w, http.StatusBadRequest, "Invalid library filter")
				return
			}
			page, err := items.FacetCollections(ctx, pluginitems.FacetCollectionsQuery{
				LibraryID: lib.ID,
				Level:     cmp.Or(tab.Level, rootLevel),
				Policy:    policy,
				FieldKey:  tab.Facet,
				Order:     effOrder,
				Query:     query,
				Limit:     limit,
			})
			if err != nil {
				libError(w, http.StatusInternalServerError, "Could not preview collections tab")
				return
			}
			cols := make([]map[string]any, len(page.Items))
			for i, col := range page.Items {
				entry := map[string]any{
					"id":        "facet:" + tab.Facet + ":" + col.ID,
					"bucketId":  col.ID,
					"name":      col.Label,
					"itemCount": col.Count,
				}
				if col.NewestItem != nil {
					it := *col.NewestItem
					entry["newestItem"] = kindItemDTO{KindItem: it, Ref: string(it.Ref), Key: int64(it.Key)}
				}
				cols[i] = entry
			}
			libJSON(w, http.StatusOK, map[string]any{
				"kind":        core.TabCollections,
				"facet":       tab.Facet,
				"order":       effOrder,
				"collections": cols,
				"more":        page.More,
			})
			return
		}

		if tab.Facet == "" {
			cols := []tabPreviewCollectionDTO{}
			more := false
			if c.app.collections != nil {
				specs, err := c.app.collections.ListSpecs(ctx)
				if err != nil {
					libError(w, libErrStatus(err), "Could not list collections")
					return
				}
				needle := strings.ToLower(query)
				for _, sp := range specs {
					if lib.MediaType != "" && sp.MediaType != "" && sp.MediaType != "mixed" && sp.MediaType != string(lib.MediaType) {
						continue
					}
					if needle != "" && !strings.Contains(strings.ToLower(sp.Name), needle) {
						continue
					}
					if len(cols) >= limit {
						more = true
						break
					}
					cols = append(cols, tabPreviewCollectionDTO{
						ID:        sp.ID,
						Name:      sp.Name,
						ItemCount: sp.ItemCount,
					})
				}
			}
			libJSON(w, http.StatusOK, map[string]any{
				"kind":        core.TabCollections,
				"facet":       "",
				"order":       "server",
				"collections": cols,
				"more":        more,
			})
			return
		}

		if c.app.catalog == nil {
			libError(w, http.StatusServiceUnavailable, "Catalogue is unavailable")
			return
		}
		page, err := c.app.catalog.LibraryFacetCollections(ctx, core.CatalogFacetCollectionsQuery{
			Filter: libraryScopeFilter(lib, core.Filter{}),
			Facet:  tab.Facet,
			Order:  effOrder,
			Query:  query,
			Limit:  limit,
		})
		if err != nil {
			libError(w, http.StatusInternalServerError, "Could not preview collections tab")
			return
		}
		cols := make([]tabPreviewCollectionDTO, len(page.Items))
		for i, fc := range page.Items {
			cols[i] = tabPreviewCollectionDTO{
				ID:         "facet:" + tab.Facet + ":" + fc.ID,
				Name:       fc.Name,
				ItemCount:  fc.ItemCount,
				NewestItem: fc.NewestItem,
			}
		}
		libJSON(w, http.StatusOK, map[string]any{
			"kind":        core.TabCollections,
			"facet":       tab.Facet,
			"order":       effOrder,
			"collections": cols,
			"more":        page.More,
		})

	case core.TabPeople:
		effOrder := cmp.Or(tab.Order, "name")
		if lib.Source == core.LibraryPlugin {
			items, _, err := c.services()
			if err != nil || items == nil {
				libError(w, http.StatusServiceUnavailable, "Plugin items are unavailable")
				return
			}
			decl, _ := c.pluginKindDecl(ctx, lib)
			rootLevel := ""
			if len(decl.Levels) > 0 {
				rootLevel = decl.Levels[0].Key
			}
			policy, err := items.LibraryPolicy(ctx, lib)
			if err != nil {
				libError(w, http.StatusBadRequest, "Invalid library filter")
				return
			}
			page, err := items.FacetPeople(ctx, pluginitems.FacetPeopleQuery{
				LibraryID: lib.ID,
				Level:     cmp.Or(tab.Level, rootLevel),
				Policy:    policy,
				FieldKey:  tab.Facet,
				Order:     effOrder,
				Query:     query,
				Limit:     limit,
			})
			if err != nil {
				libError(w, http.StatusInternalServerError, "Could not preview people tab")
				return
			}
			people := make([]map[string]any, len(page.Items))
			for i, p := range page.Items {
				entry := map[string]any{
					"id":        p.PersonID,
					"name":      p.Name,
					"itemCount": p.Count,
				}
				if p.Thumb != "" {
					entry["thumb"] = p.Thumb
				}
				people[i] = entry
			}
			libJSON(w, http.StatusOK, map[string]any{
				"kind":   core.TabPeople,
				"facet":  tab.Facet,
				"order":  effOrder,
				"people": people,
				"more":   page.More,
			})
			return
		}

		if c.app.catalog == nil {
			libError(w, http.StatusServiceUnavailable, "Catalogue is unavailable")
			return
		}
		page, err := c.app.catalog.LibraryFacetPeople(ctx, core.CatalogFacetPeopleQuery{
			Filter: libraryScopeFilter(lib, core.Filter{}),
			Facet:  tab.Facet,
			Order:  effOrder,
			Query:  query,
			Limit:  limit,
		})
		if err != nil {
			libError(w, http.StatusInternalServerError, "Could not preview people tab")
			return
		}
		people := make([]tabPreviewPersonDTO, len(page.Items))
		for i, p := range page.Items {
			people[i] = tabPreviewPersonDTO{
				ID:        p.PersonTMDB,
				Name:      p.Name,
				ItemCount: p.ItemCount,
			}
		}
		libJSON(w, http.StatusOK, map[string]any{
			"kind":   core.TabPeople,
			"facet":  tab.Facet,
			"order":  effOrder,
			"people": people,
			"more":   page.More,
		})
	}
}

func isValidPluginTabSort(decl plugins.MediaKindMeta, sort string) bool {
	if core.IsLastViewedSort(sort) {
		return true
	}
	if len(decl.Browse.Sorts) == 0 {
		return sort == "server"
	}
	return slices.Contains(decl.Browse.Sorts, sort)
}
