package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/screens"
)

// Plugin Home-row surfaces as Recommendations sources (plugin screens §3b).
// The engine places them as page rows; Vela gates each viewer and client
// when it serves the row.

// recommendationSurfaces lists the published homeRow surfaces for
// personalize.Options.Surfaces: one registry read, whatever the viewer.
func (a *App) recommendationSurfaces(ctx context.Context) []core.SurfaceInfo {
	docs := (surfaceScreens{app: a}).docs()
	if docs == nil {
		return nil
	}
	targets := docs.HomeRowSurfaces(ctx)
	out := make([]core.SurfaceInfo, 0, len(targets))
	names := map[string]string{}
	for _, t := range targets {
		name, ok := names[t.PluginID]
		if !ok {
			name = t.PluginID
			if a.pluginsRT != nil && a.pluginsRT.mgr != nil {
				if rec, err := a.pluginsRT.mgr.Get(ctx, t.PluginID); err == nil && rec.Manifest.Name != "" {
					name = rec.Manifest.Name
				}
			}
			names[t.PluginID] = name
		}
		out = append(out, core.SurfaceInfo{Plugin: t.PluginID, ID: t.Meta.ID, WireID: t.WireID, Title: t.Meta.Title, PluginName: name, ContentRating: t.Meta.ContentRating})
	}
	return out
}

// offerHomeRowSurfaces adds pluginID's published homeRow surfaces to Home
// the first time each is published (install, or the owner's Show in apps
// switch): pinned at #2, or shown when #2 is taken. An owner's later Hide or
// removal sticks.
func (a *App) offerHomeRowSurfaces(ctx context.Context, docs *screens.DocRegistry, pluginID string) {
	if docs == nil || a.personalize == nil {
		return
	}
	for _, t := range docs.HomeRowSurfaces(ctx) {
		if t.PluginID != pluginID {
			continue
		}
		ref := t.PluginID + ":" + t.Meta.ID
		row := core.Row{
			Source:    core.RowSource{Kind: "surface", Params: map[string]string{"surface": ref}},
			Placement: core.RowPlacement{Mode: core.PlacementPinned, Position: 2},
		}
		if _, err := a.personalize.OfferOnce(ctx, core.PageHome, "surface:"+ref, row); err != nil {
			a.log.Warn("recommendations: offering a plugin Home row failed", "plugin", pluginID, "surface", t.Meta.ID, "err", err)
		}
	}
}
