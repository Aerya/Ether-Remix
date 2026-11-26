package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/stremio"
)

const tvdbCatalogueLimit = 100

func materializedCatalogueSource(id string) bool {
	return id == "tvdb" || strings.HasPrefix(id, "stremio:")
}

type forceCatalogueSyncKey struct{}

func withForceCatalogueSync(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceCatalogueSyncKey{}, true)
}

func isForceCatalogueSync(ctx context.Context) bool {
	v, _ := ctx.Value(forceCatalogueSyncKey{}).(bool)
	return v
}

func (a *App) catalogueSourceType(ctx context.Context, id string) (string, error) {
	ref, err := a.resolveCatalogueSource(ctx, id)
	return ref.typ, err
}

// catalogueSourceRef is what a catalogue source id resolves to: its media
// type and, for a plugin list, the list-source kind it syncs through and
// the name the owner knows it by.
type catalogueSourceRef struct {
	typ   string
	kind  string
	label string
	// limit is how many titles one sync reads: the list's grant in the
	// plugin's limits.
	limit int
}

// resolveCatalogueSource resolves a catalogue source id. A plugin list
// ("stremio:<plugin>:<catalog key>") resolves through the plugin's
// CatalogueLists, the same list the console offers, so a source the owner
// can attach always syncs through a list-source kind the plugin registers.
// Errors name the source and say why it can't be used.
func (a *App) resolveCatalogueSource(ctx context.Context, id string) (catalogueSourceRef, error) {
	if id == "tmdb-movies" {
		return catalogueSourceRef{typ: "movie"}, nil
	}
	if id == "tmdb-tv" {
		return catalogueSourceRef{typ: "show"}, nil
	}
	if id == "tvdb" {
		return catalogueSourceRef{typ: "show", limit: tvdbCatalogueLimit}, nil
	}
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 || (parts[0] != "plugin" && parts[0] != "stremio") {
		return catalogueSourceRef{}, fmt.Errorf("unknown catalogue source %q", id)
	}
	if a.pluginsRT == nil || a.pluginsRT.mgr == nil {
		return catalogueSourceRef{}, fmt.Errorf("catalogue source %q can't be used because plugins are not running on this server", id)
	}
	rec, err := a.pluginsRT.mgr.Get(ctx, parts[1])
	if errors.Is(err, plugins.ErrNotFound) {
		return catalogueSourceRef{}, fmt.Errorf("the plugin %q that provided this catalogue source is not installed", parts[1])
	}
	if err != nil {
		return catalogueSourceRef{}, fmt.Errorf("couldn't read the plugin %q: %w", parts[1], err)
	}
	if !rec.Enabled {
		return catalogueSourceRef{}, fmt.Errorf("%s is turned off; turn it on in Plugins to use its catalogue sources", rec.Name)
	}
	if parts[0] == "plugin" {
		if e := rec.Manifest.EffectiveEther(); e != nil {
			for _, k := range e.Contributes.MediaKinds {
				if k.Key == parts[2] {
					return catalogueSourceRef{typ: "plugin:" + id}, nil
				}
			}
		}
		return catalogueSourceRef{}, fmt.Errorf("%s no longer offers the content kind %q; it may have changed in an update", rec.Name, parts[2])
	}
	cl, ok := rec.Manifest.CatalogueList(parts[2])
	if !ok {
		return catalogueSourceRef{}, fmt.Errorf("%s no longer offers this list; it may have changed in an update", rec.Name)
	}
	typ := "movie"
	if cl.Catalog.Type == stremio.TypeSeries {
		typ = "show"
	}
	name := cl.Catalog.Name
	if name == "" {
		name = cl.Label
		if key, ok := strings.CutPrefix(name, "$t:"); ok {
			name, _ = a.pluginsRT.mgr.ResolveText(ctx, rec.ID, key, "en")
		}
	}
	label := rec.Name
	if name = strings.TrimSpace(name); name != "" {
		label = fmt.Sprintf("%s (%s)", name, rec.Name)
	}
	ref := catalogueSourceRef{typ: typ, kind: extend.PluginID(rec.ID, cl.List), label: label}
	if ls, ok := rec.Manifest.ListSource(cl.List); ok {
		ref.limit = a.pluginsRT.mgr.GrantedListItems(rec.ID, ls)
	}
	return ref, nil
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

// catalogueSyncSlots is how many catalogue lists sync at once, so one big
// list never holds up its siblings.
const catalogueSyncSlots = 2

// catalogueSyncTimeout bounds one list sync: 30 seconds per 500 titles
// it may read, so a list granted 2,000 titles gets the time to page
// through them and a small list still fails fast.
func catalogueSourceSyncTimeout(limit int) time.Duration {
	n := (limit + plugins.DefaultListItems - 1) / plugins.DefaultListItems
	return max(catalogueSyncTimeout(), time.Duration(max(n, 1))*30*time.Second)
}

// One loader per App. A list never synced (or whose last sync failed)
// loads on first use; a list synced before serves its stored titles and
// refreshes in the background once stale (an hour old, or its granted
// size changed), so no viewer request waits on a remote list. Lists sync
// independently, at most catalogueSyncSlots at a time, each under a
// timeout that grows with its granted size, so one failing or huge list
// never blocks sibling lists in the same library. How many titles a list
// reads is its grant in the plugin's limits.
func (a *App) wireCatalogueSources() {
	if a.catalog == nil {
		return
	}
	a.catalogueSync.ctx, a.catalogueSync.cancel = context.WithCancel(context.Background())
	a.closers = append(a.closers, a.catalogueSync.cancel)
	slots := make(chan struct{}, catalogueSyncSlots)
	type sourceFailure struct {
		at  time.Time
		err error
	}
	var mu sync.Mutex
	failures := map[string]sourceFailure{}
	inflight := map[string]chan struct{}{}
	recentFailure := func(id string) error {
		mu.Lock()
		defer mu.Unlock()
		if f, ok := failures[id]; ok && time.Since(f.at) < time.Minute {
			return f.err
		}
		return nil
	}
	// recordError keeps the error for the owner. A first load clears
	// refreshed so the next use retries; a background refresh leaves the
	// stored titles serving and retries in the background.
	recordError := func(parent context.Context, id string, err error, firstLoad bool) {
		errCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		if firstLoad {
			_, _ = a.db.ExecContext(errCtx, `INSERT INTO libraries_catalogue_state(source_id,refreshed,item_count,last_error) VALUES(?,0,0,?) ON CONFLICT(source_id) DO UPDATE SET refreshed=0,last_error=excluded.last_error`, id, err.Error())
			return
		}
		_, _ = a.db.ExecContext(errCtx, `UPDATE libraries_catalogue_state SET last_error=? WHERE source_id=?`, err.Error(), id)
	}
	readState := func(ctx context.Context, id string) (refreshed int64, limit int) {
		_ = a.db.QueryRowContext(ctx, `SELECT refreshed,item_limit FROM libraries_catalogue_state WHERE source_id=?`, id).Scan(&refreshed, &limit)
		return refreshed, limit
	}
	fresh := func(refreshed int64, limit int, ref catalogueSourceRef) bool {
		return refreshed > 0 && time.Since(time.Unix(refreshed, 0)) < time.Hour && (ref.limit == 0 || limit == ref.limit)
	}
	write := func(ctx context.Context, id string, ref catalogueSourceRef, res collections.CatalogueSync) error {
		unique := make([]core.Title, 0, len(res.Titles))
		seen := map[core.RatingKey]bool{}
		for _, title := range res.Titles {
			if title.Type == core.MediaType(ref.typ) && title.Key != 0 && (id == string(core.MetadataTVDB) || title.IDs.TMDB > 0) && !seen[title.Key] {
				seen[title.Key] = true
				unique = append(unique, title)
			}
		}
		writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer writeCancel()
		if err := a.catalog.UpsertCollectionTitles(writeCtx, unique); err != nil {
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
		for _, title := range unique {
			if _, err = stmt.ExecContext(writeCtx, id, int64(title.Key)); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(writeCtx, `INSERT INTO libraries_catalogue_state(source_id,refreshed,item_count,last_error,listed,item_limit) VALUES(?,?,?,'',?,?) ON CONFLICT(source_id) DO UPDATE SET refreshed=excluded.refreshed,item_count=excluded.item_count,last_error='',listed=excluded.listed,item_limit=excluded.item_limit`, id, time.Now().Unix(), len(unique), res.Listed, ref.limit); err != nil {
			return err
		}
		if err = tx.Commit(); err == nil {
			a.catalog.CatalogueSourcesChanged()
		}
		return err
	}
	// sync runs one list, joining a sync of the same list already running.
	syncSource := func(ctx context.Context, id string, ref catalogueSourceRef, force, firstLoad bool) error {
		var done chan struct{}
		for {
			mu.Lock()
			running, busy := inflight[id]
			if !busy {
				done = make(chan struct{})
				inflight[id] = done
				mu.Unlock()
				break
			}
			mu.Unlock()
			select {
			case <-running:
			case <-ctx.Done():
				return ctx.Err()
			}
			if !force {
				return recentFailure(id)
			}
		}
		defer func() {
			mu.Lock()
			delete(inflight, id)
			close(done)
			mu.Unlock()
		}()
		if !force {
			if err := recentFailure(id); err != nil {
				return err
			}
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-slots }()
		if !force {
			if refreshed, limit := readState(ctx, id); fresh(refreshed, limit, ref) {
				return nil
			}
		}
		itemCtx, cancel := context.WithTimeout(ctx, catalogueSourceSyncTimeout(ref.limit))
		defer cancel()
		var res collections.CatalogueSync
		var err error
		if id == string(core.MetadataTVDB) {
			res.Titles, res.Listed, err = a.catalog.TVDBSourceTitles(itemCtx, ref.limit)
		} else if a.collections != nil {
			res, err = a.collections.GatherCatalogueSource(itemCtx, ref.kind, ref.label, ref.typ)
		} else {
			err = fmt.Errorf("catalogue sources unavailable")
		}
		if err == nil {
			err = write(ctx, id, ref, res)
		}
		mu.Lock()
		if err != nil {
			failures[id] = sourceFailure{at: time.Now(), err: err}
		} else {
			delete(failures, id)
		}
		mu.Unlock()
		if err != nil {
			recordError(ctx, id, err, firstLoad)
		}
		return err
	}
	a.catalog.SetCatalogueSourceLoader(func(ctx context.Context, ids []string) error {
		force := isForceCatalogueSync(ctx)
		var (
			wg        sync.WaitGroup
			resMu     sync.Mutex
			succeeded int
			firstErr  error
		)
		result := func(err error) {
			resMu.Lock()
			defer resMu.Unlock()
			if err == nil {
				succeeded++
			} else if firstErr == nil {
				firstErr = err
			}
		}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !materializedCatalogueSource(id) {
				result(nil)
				continue
			}
			resolveCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			ref, err := a.resolveCatalogueSource(resolveCtx, id)
			var refreshed int64
			var limit int
			if err == nil {
				refreshed, limit = readState(resolveCtx, id)
			}
			cancel()
			if err != nil {
				recordError(ctx, id, err, true)
				result(err)
				continue
			}
			if !force && fresh(refreshed, limit, ref) {
				result(nil)
				continue
			}
			if !force && refreshed > 0 && id != string(core.MetadataTVDB) {
				// Stale but synced before: serve what is stored and refresh
				// in the background.
				bg, bgCancel := context.WithTimeout(context.Background(), 10*time.Minute)
				go func() {
					defer bgCancel()
					_ = syncSource(bg, id, ref, false, false)
				}()
				result(nil)
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				// TheTVDB is refreshed in this open library's context only.
				// Keep the recorded membership if a refresh fails.
				if id == string(core.MetadataTVDB) && !force && refreshed > 0 {
					_ = syncSource(ctx, id, ref, false, false)
					result(nil)
					return
				}
				result(syncSource(ctx, id, ref, force, true))
			}()
		}
		wg.Wait()
		if firstErr != nil && succeeded == 0 {
			return firstErr
		}
		return nil
	})
}
