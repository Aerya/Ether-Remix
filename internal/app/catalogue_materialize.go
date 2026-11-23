package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"strings"
	"time"
)

type forceCatalogueSyncKey struct{}

func withForceCatalogueSync(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceCatalogueSyncKey{}, true)
}

func isForceCatalogueSync(ctx context.Context) bool {
	v, _ := ctx.Value(forceCatalogueSyncKey{}).(bool)
	return v
}

func (a *App) catalogueSourceType(ctx context.Context, id string) (string, error) {
	if id == "tmdb-movies" {
		return "movie", nil
	}
	if id == "tmdb-tv" {
		return "show", nil
	}
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 || a.pluginsRT == nil || a.pluginsRT.mgr == nil {
		return "", fmt.Errorf("unknown catalogue source")
	}
	rec, err := a.pluginsRT.mgr.Get(ctx, parts[1])
	if err != nil || !rec.Enabled {
		return "", fmt.Errorf("catalogue source is unavailable")
	}
	if parts[0] == "plugin" {
		if e := rec.Manifest.EffectiveEther(); e != nil {
			for _, k := range e.Contributes.MediaKinds {
				if k.Key == parts[2] {
					return "plugin:" + id, nil
				}
			}
		}
	}
	if parts[0] == "stremio" && rec.Manifest.HasCapability(plugins.CapListSource) {
		for _, cat := range rec.Manifest.Stremio().Catalogs {
			if cat.Standalone() && plugins.StremioCatalogKey(cat) == parts[2] {
				if cat.Type == "movie" {
					return "movie", nil
				}
				if cat.Type == "series" {
					return "show", nil
				}
			}
		}
	}
	return "", fmt.Errorf("unknown catalogue source")
}
func (a *App) validateCatalogueSources(ctx context.Context, lib core.Library, ids []string) error {
	if len(ids) == 0 || len(ids) > 32 {
		return fmt.Errorf("choose between one and 32 catalogue sources")
	}
	if lib.Source == core.LibraryCollection {
		return fmt.Errorf("change the collection source from library settings")
	}
	if lib.Source == core.LibraryBuiltin {
		return fmt.Errorf("built-in catalogue sources cannot be changed")
	}
	typ := lib.MediaType
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("duplicate catalogue source")
		}
		seen[id] = true
		sourceType, err := a.catalogueSourceType(ctx, id)
		if err != nil {
			return err
		}
		if lib.Source == core.LibraryPlugin {
			if len(ids) != 1 || id != "plugin:"+lib.PluginID+":"+lib.KindKey {
				return fmt.Errorf("plugin libraries use their own content kind")
			}
		} else if sourceType != string(typ) {
			return fmt.Errorf("choose sources with the same media type")
		}
	}
	return nil
}

// One loader per App; lock only remote refreshes. The persisted timestamp
// avoids network work after the first bounded load for an opened catalogue.
// Each list imports independently under its own timeout so one failing or huge
// list never blocks sibling lists in the same library.
func (a *App) wireCatalogueSources() {
	if a.catalog == nil {
		return
	}
	a.catalogueSync.ctx, a.catalogueSync.cancel = context.WithCancel(context.Background())
	a.closers = append(a.closers, a.catalogueSync.cancel)
	lock := make(chan struct{}, 1)
	type sourceFailure struct {
		at  time.Time
		err error
	}
	failures := map[string]sourceFailure{}
	recordError := func(parent context.Context, id string, err error) {
		if err == nil {
			return
		}
		errCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		_, _ = a.db.ExecContext(errCtx, `INSERT INTO libraries_catalogue_state(source_id,refreshed,item_count,last_error) VALUES(?,0,0,?) ON CONFLICT(source_id) DO UPDATE SET refreshed=0,last_error=excluded.last_error`, id, err.Error())
	}
	a.catalog.SetCatalogueSourceLoader(func(ctx context.Context, ids []string) error {
		force := isForceCatalogueSync(ctx)
		succeeded := 0
		var firstErr error
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !strings.HasPrefix(id, "stremio:") {
				succeeded++
				continue
			}
			itemCtx, cancel := context.WithTimeout(ctx, catalogueSyncTimeout())
			typ, err := a.catalogueSourceType(itemCtx, id)
			if err != nil {
				cancel()
				recordError(ctx, id, err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			var refreshed int64
			_ = a.db.QueryRowContext(itemCtx, `SELECT refreshed FROM libraries_catalogue_state WHERE source_id=?`, id).Scan(&refreshed)
			if !force && time.Since(time.Unix(refreshed, 0)) < time.Hour {
				cancel()
				succeeded++
				continue
			}
			err = func() error {
				select {
				case lock <- struct{}{}:
				case <-itemCtx.Done():
					return itemCtx.Err()
				}
				defer func() { <-lock }()
				if !force {
					if failed, ok := failures[id]; ok && time.Since(failed.at) < time.Minute {
						return failed.err
					}
					_ = a.db.QueryRowContext(itemCtx, `SELECT refreshed FROM libraries_catalogue_state WHERE source_id=?`, id).Scan(&refreshed)
					if time.Since(time.Unix(refreshed, 0)) < time.Hour {
						return nil
					}
				}
				if a.collections == nil {
					return fmt.Errorf("catalogue sources unavailable")
				}
				parts := strings.SplitN(id, ":", 3)
				started := time.Now()
				titles, err := a.collections.GatherCatalogueSource(itemCtx, extend.PluginID(parts[1], parts[2]), typ)
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) {
						err = fmt.Errorf("catalogue synchronization timed out after %s: %w", catalogueSyncTimeout(), err)
					}
					failures[id] = sourceFailure{at: time.Now(), err: err}
					return err
				}
				fetched := time.Since(started)
				delete(failures, id)
				unique := make([]core.Title, 0, len(titles))
				seen := map[core.RatingKey]bool{}
				for _, title := range titles {
					if title.Type == core.MediaType(typ) && title.IDs.TMDB > 0 && !seen[title.Key] {
						seen[title.Key] = true
						unique = append(unique, title)
					}
				}
				titles = unique
				writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer writeCancel()
				if err := a.catalog.UpsertCollectionTitles(writeCtx, titles); err != nil {
					return err
				}
				tx, err := a.db.BeginTx(writeCtx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err = tx.ExecContext(writeCtx, `DELETE FROM libraries_catalogue_members WHERE source_id=?`, id); err != nil {
					return err
				}
				stmt, err := tx.PrepareContext(writeCtx, `INSERT INTO libraries_catalogue_members(source_id,key) VALUES(?,?) ON CONFLICT DO NOTHING`)
				if err != nil {
					return err
				}
				defer stmt.Close()
				for _, title := range titles {
					if _, err = stmt.ExecContext(writeCtx, id, int64(title.Key)); err != nil {
						return err
					}
				}
				if _, err = tx.ExecContext(writeCtx, `INSERT INTO libraries_catalogue_state(source_id,refreshed,item_count,last_error) VALUES(?,?,?,'') ON CONFLICT(source_id) DO UPDATE SET refreshed=excluded.refreshed,item_count=excluded.item_count,last_error=''`, id, time.Now().Unix(), len(titles)); err != nil {
					return err
				}
				if err = tx.Commit(); err == nil {
					a.catalog.CatalogueSourcesChanged()
					if a.log != nil {
						a.log.Info("catalogue source synchronized", "source", id, "items", len(titles), "fetch_duration", fetched, "persist_duration", time.Since(started)-fetched)
					}
				}
				return err
			}()
			cancel()
			if err != nil {
				recordError(ctx, id, err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			succeeded++
		}
		if firstErr != nil && succeeded == 0 {
			return firstErr
		}
		return nil
	})
}
