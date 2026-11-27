package app

import (
	"context"
	"fmt"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/personalize"
)

// migrateRowsPages runs the one-time rows-pages-v1 migration: the old Home
// layout, the collections' publication targets and the Ether-managed
// discovery collections become Recommendations pages; collections keep
// only "Browse in apps"; add-on imports and the Jellyfin/Emby Home settings
// drop their row placement. It runs once per database (a failed run is
// retried at the next start) and is deleted when upgrades from 0.3.20 end.
func (a *App) migrateRowsPages(ctx context.Context) error {
	if a.personalize == nil {
		return nil
	}
	state, err := a.personalize.MigrationState(ctx, personalize.RowsPagesMigration)
	if err != nil || state == "done" {
		return err
	}
	var placements []personalize.LegacyCollectionPlacement
	if a.collections != nil {
		legacy, err := a.collections.TakeLegacyPlacements(ctx)
		if err != nil {
			return fmt.Errorf("collections: read old placements: %w", err)
		}
		for _, p := range legacy {
			placements = append(placements, personalize.LegacyCollectionPlacement{ID: p.ID, MediaType: p.MediaType, Enabled: p.Enabled,
				HomeRow: p.HomeRow, SectionRow: p.SectionRow, Viewers: p.Viewers, When: p.When, Devices: p.Devices})
		}
	}
	n, err := a.personalize.MigrateRowsPages(ctx, placements)
	if err != nil {
		return err
	}
	// The Ether-managed discovery collections are built-in sources now.
	if a.collections != nil {
		if err := a.collections.DeleteRetired(ctx, personalize.RetiredDiscoveryIDs()); err != nil {
			return fmt.Errorf("collections: delete discovery collections: %w", err)
		}
	}
	// Jellyfin and Emby rows come from Home now: their own row lists go.
	if err := a.cfg.Update(func(c *config.Config) error {
		for _, id := range []string{"jellyfin", "emby"} {
			fe, ok := c.Frontends[id]
			if !ok || fe.Settings == nil {
				continue
			}
			delete(fe.Settings, "homeRows")
			delete(fe.Settings, "homeRowKinds")
			c.Frontends[id] = fe
		}
		return nil
	}); err != nil {
		return fmt.Errorf("config: drop Jellyfin/Emby home row settings: %w", err)
	}
	if err := a.personalize.SetMigrationState(ctx, personalize.RowsPagesMigration, "done"); err != nil {
		return err
	}
	a.log.Info("recommendations: moved Home layout and collection rows onto Recommendations pages", "pages", n, "collections", len(placements))
	return nil
}
