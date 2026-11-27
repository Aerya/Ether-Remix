package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// RankedReleases implements frontend.RankedReleases for a surface.
func (s surfaceEditions) RankedReleases(ctx context.Context, req core.ResolveRequest) ([]core.ReleaseVariant, error) {
	if s.app == nil || s.app.resolver == nil || req.Title == nil || req.Title.Key.IsPluginKey() {
		return nil, nil
	}
	req.User = s.app.withEffectiveAudioLanguages(ctx, req.User)
	return s.app.resolver.RankedReleases(ctx, req)
}

// pseRankedOffers adds, for Stremio, the ranked releases to the offers a
// version handle may name: the stream list gives one flux per release, not
// only the configured version slots.
func pseRankedOffers(ctx context.Context, a *App, v *core.Viewer, t *core.Title, caps core.ClientCaps, offered []core.ReleaseVariant, err error) ([]core.ReleaseVariant, error) {
	if err != nil || caps.Protocol != "stremio" || a.resolver == nil || t == nil || t.Key.IsPluginKey() {
		return offered, err
	}
	ranked, rerr := a.resolver.RankedReleases(ctx, core.ResolveRequest{Title: t, User: a.withEffectiveAudioLanguages(ctx, v), Client: caps})
	if rerr != nil {
		return offered, nil
	}
	seen := make(map[string]bool, len(offered))
	for _, o := range offered {
		seen[o.ID] = true
	}
	for _, r := range ranked {
		if !seen[r.ID] {
			offered = append(offered, r)
		}
	}
	return offered, nil
}
