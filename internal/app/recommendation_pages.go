package app

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/personalize"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Library descriptions for Recommendations pages (personalize.Options
// PageLibraries and LibraryRowsTab). Everything here reads the cached
// library list, the cached plugin kind declarations and the installed
// plugin records: the cost follows the number of libraries, never their
// size.

// pluginsReady reports whether plugin kind declarations can be read
// without opening services on the request path.
func (a *App) pluginsReady() bool {
	return a.pluginsRT != nil && a.pluginsRT.pluginitems != nil && a.pluginsRT.blobcache != nil
}

// libraryRowsTab is the key of a library's first rows tab: the page Plex,
// Jellyfin and Emby show as its Recommended page.
func (a *App) libraryRowsTab(ctx context.Context, id core.LibraryID) string {
	if a.libraries == nil {
		return ""
	}
	lib, err := a.libraries.Get(ctx, id)
	if err != nil || lib.Source == core.LibraryPlugin && !a.pluginsReady() {
		return ""
	}
	for _, t := range (&librariesConsole{app: a}).effectiveTabs(ctx, lib) {
		if t.Kind == core.TabRecommended {
			return t.Key
		}
	}
	return ""
}

// pageLibraries describes every library, built-in and plugin, for the
// Recommendations designer and engine.
func (a *App) pageLibraries(ctx context.Context) []core.PageLibrary {
	out := []core.PageLibrary{}
	if a.libraries == nil {
		return out
	}
	libs, err := a.libraries.List(ctx)
	if err != nil {
		return out
	}
	c := &librariesConsole{app: a}
	var pluginNames map[string]string
	for _, lib := range libs {
		pl := core.PageLibrary{ID: lib.ID, Name: lib.Name, Experience: core.LibraryExperienceWatch}
		if lib.Source == core.LibraryPlugin {
			if !a.pluginsReady() {
				continue
			}
			decl, ok := c.pluginKindDecl(ctx, lib)
			if !ok {
				continue
			}
			if pluginNames == nil {
				pluginNames = a.pluginDisplayNames(ctx)
			}
			pl.Plugin = pluginNames[lib.PluginID]
			if pl.Plugin == "" {
				pl.Plugin = lib.PluginID
			}
			pl.Experience = core.EffectiveLibraryExperience(decl.Library.Experience, core.MediaClass(decl.Media))
			pl.Card = lib.EffectiveCardShape(decl.Library.Card)
			pl.Caps = pluginLibraryCaps(pl.Experience, decl)
			pl.Shelves = pluginLibraryShelves(decl)
		} else {
			pl.MediaType = lib.MediaType
			pl.Card = lib.EffectiveCardShape("")
			pl.Caps = catalogueLibraryCaps(lib.MediaType)
		}
		for _, t := range c.effectiveTabs(ctx, lib) {
			if t.Kind == core.TabRecommended {
				pl.RowsTabs = append(pl.RowsTabs, core.PageTab{Key: t.Key, Label: t.Label})
			}
		}
		out = append(out, pl)
	}
	return out
}

func (a *App) pluginDisplayNames(ctx context.Context) map[string]string {
	out := map[string]string{}
	if a.pluginsRT == nil || a.pluginsRT.mgr == nil {
		return out
	}
	recs, err := a.pluginsRT.mgr.List(ctx)
	if err != nil {
		return out
	}
	for _, r := range recs {
		out[r.ID] = r.Name
	}
	return out
}

// catalogueLibraryCaps are a catalogue (TMDB) library's capabilities
// (design §4.1); "" media type holds both films and series.
func catalogueLibraryCaps(t core.MediaType) []string {
	caps := []string{core.CapCharts, core.CapTaste, core.CapProgress, core.CapAdded, core.CapReleased,
		core.CapPopularity, core.CapCatalogue, "facet:genre", "facet:decade", "facet:language", "facet:studio"}
	if t != core.TypeShow {
		caps = append(caps, core.CapMovies)
	}
	if t != core.TypeMovie {
		caps = append(caps, core.CapEpisodes, "facet:network")
	}
	return caps
}

// pluginLibraryCaps are a plugin library's capabilities from its
// experience and declaration (design §4.1, §4.2).
// pluginLibraryShelves are a plugin library's declared shelves (library
// and rows tabs), the choices of its pages' Plugin Shelf source.
func pluginLibraryShelves(decl plugins.MediaKindMeta) []core.ParamOption {
	var out []core.ParamOption
	seen := map[string]bool{}
	add := func(key, label string) {
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		if label == "" || strings.HasPrefix(label, "$t:") {
			label = key
		}
		out = append(out, core.ParamOption{Value: key, Label: label})
	}
	for _, sh := range decl.Library.Shelves {
		add(sh.Key, sh.Label)
	}
	for _, t := range decl.Library.Tabs {
		for _, sh := range t.Shelves {
			add(sh.Key, sh.Label)
		}
	}
	return out
}

func pluginLibraryCaps(experience string, decl plugins.MediaKindMeta) []string {
	caps := []string{}
	switch experience {
	case core.LibraryExperienceLive:
		caps = append(caps, core.CapLive)
	case core.LibraryExperienceGallery:
		caps = append(caps, core.CapAdded)
	default:
		caps = append(caps, core.CapProgress, core.CapAdded, core.CapReleased, core.CapPopularity)
	}
	shelves := len(decl.Library.Shelves) > 0
	for _, t := range decl.Library.Tabs {
		shelves = shelves || len(t.Shelves) > 0
	}
	if shelves {
		caps = append(caps, core.CapShelves)
	}
	if experience != core.LibraryExperienceLive {
		for _, f := range decl.Browse.Facets {
			caps = append(caps, "facet:"+f)
		}
		for _, lv := range decl.Levels {
			if lv.Role != "" && !slices.Contains(caps, "role:"+lv.Role) {
				caps = append(caps, "role:"+lv.Role)
			}
		}
	}
	return caps
}

// The engine is the admin API's Recommendations backend.
var _ admin.Personalization = (*personalize.Engine)(nil)

// pluginPageCandidates returns a plugin library's candidate shelves (before
// the page arranges them) for Recommendations previews.
func (a *App) pluginPageCandidates(ctx context.Context, viewer *core.User, id core.LibraryID, tab string) []core.HomeRow {
	// Vela presents every plugin library (Silo only those given a Silo
	// type), so its experience reads the page's shelves.
	var svc *surfaceExperience
	if a.frontends != nil && a.frontends.velaExperience != nil {
		svc = a.frontends.velaExperience
	} else if a.pluginsRT != nil && a.pluginsRT.kindCatalog != nil {
		if raw, err := a.newSurfaceExperience("vela"); err == nil {
			svc, _ = raw.(*surfaceExperience)
		}
	}
	if svc == nil || svc.Service == nil {
		return nil
	}
	v := viewer
	if v == nil || v.ID <= 0 {
		if owner, err := a.users.Owner(ctx); err == nil && owner != nil {
			v = owner
		} else {
			v = &core.User{ID: 1, IsOwner: true}
		}
	}
	cands, err := svc.Service.PluginLibraryCandidateRows(ctx, v, strconv.FormatInt(int64(id), 10), tab)
	if err != nil {
		return nil
	}
	rows := make([]core.HomeRow, 0, len(cands))
	for _, r := range cands {
		rows = append(rows, core.HomeRow{HubID: r.ID, Title: r.Title, Kind: r.Kind, Source: core.RowSourcePrecomposed, Items: r.Items, Present: core.RowPresentation{Personal: r.Layout.Present.Personal},
			Hub: core.Hub{ID: r.ID, Title: r.Title, Items: r.Items, ItemCount: len(r.Items)}})
	}
	return rows
}
