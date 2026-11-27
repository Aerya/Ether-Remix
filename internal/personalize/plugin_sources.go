package personalize

import (
	"context"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
)

// Plugin and listen row sources on Recommendations pages: the choices of
// the shelf and surface sources, and the listen-only looks.

// pluginSourceRefs fills the choices of a ref source that comes from
// plugins: a shelf's choices are the declared shelves of the page's
// libraries, a surface's the published Home-row surfaces. A source with
// nothing to choose from isn't available.
func (e *Engine) pluginSourceRefs(ctx context.Context, ps *core.PageSource, pageLibs []core.PageLibrary) {
	switch ps.Kind {
	case SourceShelf:
		seen := map[string]bool{}
		for _, l := range pageLibs {
			for _, sh := range l.Shelves {
				if seen[sh.Value] {
					continue
				}
				seen[sh.Value] = true
				opt := sh
				if len(pageLibs) > 1 {
					opt.Label = l.Name + " › " + sh.Label
				}
				ps.Refs = append(ps.Refs, opt)
			}
		}
	case SourceSurface:
		if e.opts.Surfaces != nil {
			for _, sf := range e.opts.Surfaces(ctx) {
				label := sf.Title
				if label == "" {
					label = sf.ID
				}
				if sf.PluginName != "" {
					label = sf.PluginName + " · " + label
				}
				ps.Refs = append(ps.Refs, core.ParamOption{Value: sf.Plugin + ":" + sf.ID, Label: label})
			}
		}
		if len(ps.Refs) == 0 {
			ps.Available, ps.Unavailable = false, "No plugin has a row to place here"
		}
	}
}

// listenLook reports a row whose look only listening libraries can draw
// (Tiles, Track list) on a page that isn't one.
func (e *Engine) listenLook(ctx context.Context, key core.PageKey, r core.Row) error {
	if r.Look.Style != core.RowStyleTiles && r.Look.Style != core.RowStyleTracks {
		return nil
	}
	libs := map[core.LibraryID]core.PageLibrary{}
	for _, l := range e.pageLibraries(ctx) {
		libs[l.ID] = l
	}
	want := r.Scope.Libraries
	if id, _, ok := key.Library(); ok {
		want = []core.LibraryID{id}
	}
	if len(want) == 0 && !r.Scope.PerLibrary {
		return fmt.Errorf("tiles and track list need listening libraries: pick them under Libraries")
	}
	for _, id := range want {
		if l, ok := libs[id]; !ok || l.Experience != "listen" {
			return fmt.Errorf("tiles and track list are for listening libraries")
		}
	}
	return nil
}
