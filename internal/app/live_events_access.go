package app

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/liveevents"
)

// Live events belong to the plugin whose feed produced them. A plugin that
// limits itself to granted viewers (permissions.viewers.identity:
// "granted_viewers") must be invisible to everyone else on every live-event
// surface, not only on its screens: snapshots, detail, reveal, the stream,
// the ticker, entity favourites and alerts all ask the same question here.

// liveEventPluginAllowed reports whether viewer may see live events from
// pluginID: the plugin is enabled, not shadowed, and the viewer is granted.
func (a *App) liveEventPluginAllowed(ctx context.Context, viewer *core.User, pluginID string) bool {
	if a == nil || a.pluginsRT == nil || a.pluginsRT.mgr == nil || viewer == nil || strings.TrimSpace(pluginID) == "" {
		return false
	}
	return a.screenPluginViewerAllowed(ctx, a.pluginsRT.mgr, viewer, pluginID)
}

// liveEventIDPlugin returns the plugin of a namespaced event id
// ("pluginID:feedID:eventID").
func liveEventIDPlugin(id string) string {
	pluginID, _, _ := liveevents.ParseEventID(strings.TrimSpace(id), "")
	return pluginID
}

// grantedFavoriteEntities drops entity favourites whose plugin the viewer may
// no longer see, so alerts (in-app, push and Flows) and the alert scheduler
// never act on them. Viewers and plugin decisions are looked up once per call.
type grantedFavoriteEntities struct {
	app   *App
	inner liveevents.FavoriteEntitiesReader
}

func (g grantedFavoriteEntities) ListEntityFavorites(ctx context.Context) ([]liveevents.EntityFavorite, error) {
	favs, err := g.inner.ListEntityFavorites(ctx)
	if err != nil || len(favs) == 0 || g.app == nil || g.app.users == nil {
		return favs, err
	}
	type decisionKey struct {
		viewer int64
		plugin string
	}
	viewers := map[int64]*core.User{}
	decided := map[decisionKey]bool{}
	out := favs[:0]
	for _, fav := range favs {
		parsed, ok := liveevents.ParseEntityKey(fav.Key)
		if !ok {
			continue
		}
		key := decisionKey{viewer: fav.ViewerID, plugin: parsed.PluginID}
		allowed, seen := decided[key]
		if !seen {
			viewer, cached := viewers[fav.ViewerID]
			if !cached {
				viewer, _ = g.app.users.ActiveUser(ctx, fav.ViewerID)
				viewers[fav.ViewerID] = viewer
			}
			allowed = viewer != nil && g.app.liveEventPluginAllowed(ctx, viewer, parsed.PluginID)
			decided[key] = allowed
		}
		if allowed {
			out = append(out, fav)
		}
	}
	return out, nil
}
