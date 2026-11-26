package app

import (
	"context"
	"net/url"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// pluginImages authorizes images a plugin names by URL (team crests, league
// logos). Clients only ever receive sealed grants for them; every read checks
// again that the plugin is enabled, that the viewer may use it, and that the
// URL's host is one the plugin declared in permissions.network.
type pluginImages struct{ app *App }

var _ frontend.PluginImageAuthorizer = pluginImages{}

func (p pluginImages) AuthorizePluginImage(ctx context.Context, viewer *core.Viewer, pluginID string, path string) (string, error) {
	if p.app == nil || p.app.pluginsRT == nil || p.app.pluginsRT.mgr == nil || viewer == nil {
		return "", core.ErrNotAvailable
	}
	raw, ok := strings.CutPrefix(path, frontend.PluginImagePrefix)
	if !ok {
		return "", core.ErrNotFound
	}
	img, ok := frontend.PluginImage(pluginID, raw)
	if !ok || img.Path != path {
		return "", core.ErrNotFound
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", core.ErrNotFound
	}
	mgr := p.app.pluginsRT.mgr
	err = frontend.PluginImageMemoFrom(ctx).Decide(viewer.ID, pluginID, u.Hostname(), func() error {
		if !p.app.screenPluginViewerAllowed(ctx, mgr, viewer, pluginID) || !mgr.AllowNetworkHost(ctx, pluginID, u.Hostname()) {
			return core.ErrUnauthorized
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}
