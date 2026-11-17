package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/stremio"
)

type xperienceBackend struct{ app *App }

func (b xperienceBackend) Preview(ctx context.Context, link string) (stremio.Manifest, error) {
	mgr := b.app.pluginManager()
	if mgr == nil {
		return stremio.Manifest{}, fmt.Errorf("catalogues indisponibles")
	}
	p, err := mgr.PreviewInstall(ctx, plugins.Source{Kind: "url", URL: link})
	if err != nil {
		return stremio.Manifest{}, fmt.Errorf("manifeste inaccessible")
	}
	if !p.StremioOnly {
		return stremio.Manifest{}, fmt.Errorf("un manifeste Stremio est requis")
	}
	return p.Manifest.Stremio(), nil
}
func (b xperienceBackend) Install(ctx context.Context, link string, manifestRefreshHours int) (string, stremio.Manifest, error) {
	mgr := b.app.pluginManager()
	if mgr == nil {
		return "", stremio.Manifest{}, fmt.Errorf("catalogues indisponibles")
	}
	// Install exactly the validated plain Stremio preview, never a native package.
	p, err := mgr.PreviewInstall(ctx, plugins.Source{Kind: "url", URL: link})
	if err != nil || !p.StremioOnly {
		return "", stremio.Manifest{}, fmt.Errorf("manifeste Stremio inaccessible")
	}
	rec, err := mgr.Get(ctx, p.Manifest.ID)
	created := err != nil
	if created {
		rec, err = mgr.Install(ctx, p, plugins.InstallOptions{})
		if err != nil {
			return "", stremio.Manifest{}, fmt.Errorf("source inaccessible")
		}
	} else if !rec.Enabled {
		return "", stremio.Manifest{}, fmt.Errorf("source désactivée")
	}
	// Preserve roles on an existing installation; new imports only supply lists.
	var off []plugins.Capability
	if created {
		for _, role := range rec.Manifest.AddonRoles() {
			if role != plugins.CapListSource {
				off = append(off, role)
			}
		}
	} else {
		for _, role := range rec.RolesOff {
			if role != plugins.CapListSource && slices.Contains(rec.Manifest.AddonRoles(), role) {
				off = append(off, role)
			}
		}
	}
	rec, err = mgr.SetRoles(ctx, rec.ID, off)
	if err != nil {
		return "", stremio.Manifest{}, fmt.Errorf("source inaccessible")
	}
	if err := mgr.SetManifestRefresh(ctx, rec.ID, manifestRefreshHours); err != nil {
		return "", stremio.Manifest{}, err
	}
	return rec.ID, rec.Manifest.Stremio(), nil
}
