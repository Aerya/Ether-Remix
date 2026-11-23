package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/stremio"
)

func init() {
	store.Register("collections_addon_imports", 1, `
CREATE TABLE IF NOT EXISTS collections_addon_imports (
  addon_id TEXT PRIMARY KEY,
  folder TEXT NOT NULL DEFAULT '',
  name_mode TEXT NOT NULL DEFAULT 'autoType',
  name_affix TEXT NOT NULL DEFAULT '',
  follow_manifest INTEGER NOT NULL DEFAULT 1,
  refresh TEXT NOT NULL DEFAULT 'daily',
  max_items INTEGER NOT NULL DEFAULT 500,
  enabled INTEGER NOT NULL DEFAULT 1,
  targets_json TEXT NOT NULL DEFAULT '{}',
  known_keys_json TEXT NOT NULL DEFAULT '[]',
  last_synced_at INTEGER NOT NULL DEFAULT 0,
  last_summary_json TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);`)
}

const (
	// deferBulkCreateThreshold controls when an import switches from immediate
	// bounded background builds to scheduler-managed deferred builds.
	deferBulkCreateThreshold = 5
	removedWarningPrefix     = "Removed from add-on:"
)

// CatalogImporter manages bulk Stremio catalogue imports and manifest-following
// synchronization between installed Stremio add-ons and Ether collections.
type CatalogImporter struct {
	db   *sql.DB
	mgr  *plugins.Manager
	cols *collections.Service
	log  *slog.Logger
	now  func() time.Time

	lockMu     sync.Mutex
	addonLocks map[string]*sync.Mutex
}

// AttachCatalogImporter constructs a CatalogImporter and hooks it into the
// plugin manager's manifest sync and uninstall lifecycle. Call after Attach
// so listSourceKinds are already registered before sync creates collections.
func AttachCatalogImporter(db *sql.DB, mgr *plugins.Manager, cols *collections.Service, log *slog.Logger) *CatalogImporter {
	if log == nil {
		log = slog.Default()
	}
	ci := &CatalogImporter{
		db:         db,
		mgr:        mgr,
		cols:       cols,
		log:        log,
		now:        time.Now,
		addonLocks: map[string]*sync.Mutex{},
	}
	if mgr != nil {
		mgr.OnSync(ci.onPluginSync)
		mgr.OnRemove(ci.onPluginRemove)
	}
	return ci
}

func (ci *CatalogImporter) lockAddon(addonID string) func() {
	ci.lockMu.Lock()
	m := ci.addonLocks[addonID]
	if m == nil {
		m = &sync.Mutex{}
		ci.addonLocks[addonID] = m
	}
	ci.lockMu.Unlock()
	m.Lock()
	return m.Unlock
}

// storedSubscription is one row of collections_addon_imports.
type storedSubscription struct {
	AddonID        string
	Folder         string
	NameMode       string
	NameAffix      string
	FollowManifest bool
	Refresh        string
	MaxItems       int
	Enabled        bool
	Targets        core.CollectionTargets
	KnownKeys      []string
	LastSyncedAt   time.Time
	LastSummary    *core.AddonImportSummary
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type importValidationError struct {
	fields map[string]string
}

func (e *importValidationError) Error() string {
	for k, v := range e.fields {
		return k + ": " + v
	}
	return "invalid add-on import"
}

func (e *importValidationError) InvalidFields() map[string]string { return e.fields }

type importUpstreamError struct{ msg string }

func (e *importUpstreamError) Error() string  { return e.msg }
func (e *importUpstreamError) Upstream() bool { return true }

func wrapAddonErr(err error) error {
	if err == nil {
		return nil
	}
	var pve *plugins.ValidationError
	if errors.As(err, &pve) {
		fields := map[string]string{}
		for _, f := range pve.Fields {
			key := f.Field
			if key == "" {
				key = "url"
			}
			fields[key] = f.Message
		}
		return &importValidationError{fields: fields}
	}
	if errors.Is(err, plugins.ErrNotFound) || errors.Is(err, core.ErrNotFound) {
		return core.ErrNotFound
	}
	return &importUpstreamError{msg: "Ether couldn't read the add-on manifest. Check the link and make sure the add-on is reachable."}
}

// List returns every active add-on import subscription plus installed Stremio
// catalog add-ons that are ready to import.
func (ci *CatalogImporter) List(ctx context.Context) (*core.AddonCatalogImportList, error) {
	subs, err := ci.listSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	specs, err := ci.cols.ListSpecsLight(ctx)
	if err != nil {
		return nil, err
	}
	managedByAddon := map[string]int{}
	removedByAddon := map[string]int{}
	for _, sp := range specs {
		if sp.AddonSync != nil && sp.AddonSync.AddonID != "" {
			managedByAddon[sp.AddonSync.AddonID]++
			if sp.AddonSync.Removed {
				removedByAddon[sp.AddonSync.AddonID]++
			}
		}
	}
	recs, err := ci.mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]plugins.Record, len(recs))
	for _, r := range recs {
		byID[r.ID] = r
	}
	subIDs := make(map[string]bool, len(subs))
	out := &core.AddonCatalogImportList{
		Imports:         make([]core.AddonCatalogSubscription, 0, len(subs)),
		AvailableAddons: []core.AddonCatalogAvailable{},
	}
	for _, s := range subs {
		subIDs[s.AddonID] = true
		rec, ok := byID[s.AddonID]
		var recPtr *plugins.Record
		if ok {
			recCopy := rec
			recPtr = &recCopy
		}
		out.Imports = append(out.Imports, ci.toSubscriptionView(s, recPtr, managedByAddon[s.AddonID], removedByAddon[s.AddonID]))
	}
	for _, r := range recs {
		if !r.Manifest.IsStremioOnly() {
			continue
		}
		cats := listableManifestCatalogs(r.Manifest)
		if len(cats) == 0 && !subIDs[r.ID] {
			continue
		}
		out.AvailableAddons = append(out.AvailableAddons, core.AddonCatalogAvailable{
			AddonID:      r.ID,
			Name:         firstNonEmpty(r.Name, r.Manifest.Name, r.ID),
			Logo:         r.Manifest.Logo,
			Host:         plugins.AddonHost(r),
			Enabled:      r.Enabled,
			CatalogCount: len(cats),
			HasImport:    subIDs[r.ID],
		})
	}
	return out, nil
}

// Preview inspects either an installed add-on (by AddonID) or a manifest URL
// and returns every movie/series catalogue in manifest order along with
// required extras, duplicate-name hints, and already-imported collection links.
func (ci *CatalogImporter) Preview(ctx context.Context, req core.AddonCatalogPreviewRequest) (*core.AddonCatalogPreview, error) {
	addonID := strings.TrimSpace(req.AddonID)
	rawURL := strings.TrimSpace(req.URL)
	if addonID == "" && rawURL == "" {
		return nil, &importValidationError{fields: map[string]string{"url": "Provide an add-on install link or choose an installed add-on."}}
	}

	var (
		host      string
		installed *plugins.Record
		man       plugins.Manifest
		err       error
	)
	if rawURL != "" {
		addonID, host, installed, man, err = ci.mgr.PreviewAddonURL(ctx, rawURL)
		if err != nil {
			return nil, wrapAddonErr(err)
		}
	} else {
		rec, gerr := ci.mgr.Get(ctx, addonID)
		if gerr != nil {
			return nil, wrapAddonErr(gerr)
		}
		if !rec.Manifest.IsStremioOnly() {
			return nil, &importValidationError{fields: map[string]string{"addonId": "That plugin is not a Stremio catalogue add-on."}}
		}
		recCopy := rec
		installed = &recCopy
		man = rec.Manifest
		host = plugins.AddonHost(rec)
	}

	specs, err := ci.cols.ListSpecsLight(ctx)
	if err != nil {
		return nil, err
	}
	existingByKey, removed := indexAddonCollections(specs, addonID)
	cats := listableManifestCatalogs(man)
	dupNames := duplicateCatalogNames(cats)

	items := make([]core.AddonCatalogPreviewItem, 0, len(cats))
	seenKeys := make(map[string]bool, len(cats))
	for i, c := range cats {
		key := plugins.StremioCatalogKey(c)
		seenKeys[key] = true
		item := buildCatalogPreviewItem(c, key, i+1, dupNames)
		if sp, ok := existingByKey[key]; ok {
			item.Imported = true
			item.ExistingCollectionID = sp.ID
			item.ExistingCollectionName = sp.Name
			if sp.AddonSync != nil {
				item.ExistingRemoved = sp.AddonSync.Removed
			}
			for _, src := range sp.Sources {
				if src.Kind == extend.PluginID(addonID, key) {
					if len(src.Params) > 0 {
						item.ExistingParams = maps.Clone(src.Params)
					}
					item.ExistingMaxItems = src.MaxItems
					break
				}
			}
		}
		items = append(items, item)
	}

	var removedItems []core.AddonCatalogRemovedItem
	for _, sp := range removed {
		if sp.AddonSync == nil || seenKeys[sp.AddonSync.CatalogKey] {
			continue
		}
		removedItems = append(removedItems, core.AddonCatalogRemovedItem{
			ID:          sp.ID,
			Name:        sp.Name,
			CatalogType: sp.AddonSync.CatalogType,
			CatalogID:   sp.AddonSync.CatalogID,
			CatalogKey:  sp.AddonSync.CatalogKey,
		})
	}

	var subView *core.AddonCatalogSubscription
	if sub, ok, err := ci.getSubscription(ctx, addonID); err == nil && ok {
		managedCount, removedCount := countAddonSpecs(specs, addonID)
		v := ci.toSubscriptionView(sub, installed, managedCount, removedCount)
		subView = &v
	}

	addonName := firstNonEmpty(man.Name, addonID)
	if installed != nil && strings.TrimSpace(installed.Name) != "" {
		addonName = installed.Name
	}
	return &core.AddonCatalogPreview{
		AddonID:            addonID,
		Installed:          installed != nil,
		AddonName:          addonName,
		AddonVersion:       man.Version,
		AddonDescription:   man.Description,
		AddonLogo:          man.Logo,
		Host:               host,
		ConfigureFirst:     man.ConfigurationRequired(),
		Subscription:       subView,
		Catalogs:           items,
		RemovedCollections: removedItems,
	}, nil
}

// Apply installs (or reuses) the Stremio add-on, saves its manifest-following
// subscription, and creates or updates one collection per selected catalogue.
func (ci *CatalogImporter) Apply(ctx context.Context, req core.AddonCatalogImportRequest) (*core.AddonCatalogImportResult, error) {
	rawURL := strings.TrimSpace(req.URL)
	addonID := strings.TrimSpace(req.AddonID)
	if rawURL == "" && addonID == "" {
		return nil, &importValidationError{fields: map[string]string{"url": "Provide an add-on install link or choose an installed add-on."}}
	}
	if len(req.Items) == 0 {
		return nil, &importValidationError{fields: map[string]string{"items": "Select at least one catalogue to import."}}
	}
	cfg, err := normalizeImportConfig(req)
	if err != nil {
		return nil, err
	}

	var rec plugins.Record
	if rawURL != "" {
		rec, err = ci.mgr.InstallAddon(ctx, plugins.AddonInstall{
			Link:      rawURL,
			Roles:     []plugins.Capability{plugins.CapListSource},
			Exclusive: true,
		})
		if err != nil {
			return nil, wrapAddonErr(err)
		}
	} else {
		rec, err = ci.mgr.Get(ctx, addonID)
		if err != nil {
			return nil, wrapAddonErr(err)
		}
		if !rec.Enabled {
			return nil, &importValidationError{fields: map[string]string{"addonId": "Enable this add-on in Plugins before importing its catalogues."}}
		}
		if !rec.Manifest.HasCapability(plugins.CapListSource) {
			return nil, &importValidationError{fields: map[string]string{"addonId": "This add-on does not offer movie or series catalogues."}}
		}
	}
	if rec.Manifest.ConfigurationRequired() {
		return nil, &importValidationError{fields: map[string]string{"url": stremio.ErrConfigurationRequired.Error()}}
	}

	unlock := ci.lockAddon(rec.ID)
	defer unlock()

	cats := listableManifestCatalogs(rec.Manifest)
	if len(cats) == 0 {
		return nil, &importValidationError{fields: map[string]string{"url": "This add-on does not declare any movie or series catalogues."}}
	}
	catByKey := make(map[string]manifestCatalogEntry, len(cats))
	allKeys := make([]string, 0, len(cats))
	for i, c := range cats {
		key := plugins.StremioCatalogKey(c)
		catByKey[key] = manifestCatalogEntry{cat: c, key: key, order: i + 1}
		allKeys = append(allKeys, key)
	}
	dupNames := duplicateCatalogNames(cats)

	prevSub, hasPrev, err := ci.getSubscription(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	addonName := firstNonEmpty(rec.Name, rec.Manifest.Name, rec.ID)
	if cfg.Folder == "" {
		if hasPrev && prevSub.Folder != "" {
			cfg.Folder = prevSub.Folder
		} else {
			cfg.Folder = clampRunes(addonName, 80)
		}
	}

	specs, err := ci.cols.ListSpecsLight(ctx)
	if err != nil {
		return nil, err
	}
	existingByKey, allManaged := indexAddonCollections(specs, rec.ID)

	// Deduplicate requested items and keep manifest order.
	selected := make([]core.AddonCatalogImportItem, 0, len(req.Items))
	seenReq := map[string]bool{}
	for _, it := range req.Items {
		key := strings.TrimSpace(it.Key)
		if key == "" || seenReq[key] {
			continue
		}
		seenReq[key] = true
		selected = append(selected, it)
	}
	slices.SortFunc(selected, func(a, b core.AddonCatalogImportItem) int {
		oa, ob := catByKey[a.Key].order, catByKey[b.Key].order
		if oa == ob {
			return strings.Compare(a.Key, b.Key)
		}
		if oa == 0 {
			return 1
		}
		if ob == 0 {
			return -1
		}
		return oa - ob
	})

	// Count how many brand-new collections will be created so we can defer
	// builds smoothly when importing a large manifest.
	newToCreate := 0
	for _, it := range selected {
		if _, ok := catByKey[it.Key]; !ok {
			continue
		}
		if _, exists := existingByKey[it.Key]; !exists {
			newToCreate++
		}
	}
	deferBuilds := newToCreate > deferBulkCreateThreshold

	res := &core.AddonCatalogImportResult{
		AddonID:   rec.ID,
		AddonName: addonName,
	}

	for _, it := range selected {
		entry, ok := catByKey[it.Key]
		if !ok {
			res.Skipped = append(res.Skipped, core.AddonImportSkippedItem{
				Key:    it.Key,
				Name:   firstNonEmpty(it.Name, it.Key),
				Reason: "Catalogue is not present in the add-on manifest",
			})
			continue
		}
		params := catalogExtras(it.Params)
		if err := entry.cat.ValidateExtras(params); err != nil {
			res.Skipped = append(res.Skipped, core.AddonImportSkippedItem{
				Key:    entry.key,
				Name:   firstNonEmpty(entry.cat.Name, entry.cat.ID),
				Reason: formatExtraReason(entry.cat, err),
			})
			continue
		}
		mediaType := collections.MediaMovie
		if entry.cat.Type == stremio.TypeSeries {
			mediaType = collections.MediaShow
		}
		autoName := formatImportedCatalogName(entry.cat, mediaType, cfg.NameMode, cfg.NameAffix, dupNames)
		chosenName := strings.TrimSpace(it.Name)
		if chosenName == "" {
			chosenName = autoName
		}
		chosenName = clampRunes(chosenName, 120)

		maxItems := cfg.MaxItems
		if it.MaxItems > 0 {
			maxItems = clampMaxItems(it.MaxItems)
		}
		kindID := extend.PluginID(rec.ID, entry.key)

		if existing, exists := existingByKey[entry.key]; exists {
			wasRemoved := existing.AddonSync != nil && existing.AddonSync.Removed
			updatedSpec, err := ci.cols.UpdateSpecFromAddonSync(ctx, existing.ID, func(sp *core.CollectionSpec, prevWarnings []string) ([]string, bool) {
				if sp.AddonSync == nil {
					// Adopt manually created collection without clobbering its custom name or targets.
					sp.AddonSync = &core.CollectionAddonSync{
						AddonID:     rec.ID,
						AddonName:   addonName,
						CatalogType: entry.cat.Type,
						CatalogID:   entry.cat.ID,
						CatalogKey:  entry.key,
						ManagedName: autoName,
					}
				} else {
					if sp.Name == sp.AddonSync.ManagedName || sp.AddonSync.ManagedName == "" {
						sp.Name = chosenName
					}
					sp.AddonSync.AddonName = addonName
					sp.AddonSync.CatalogType = entry.cat.Type
					sp.AddonSync.CatalogID = entry.cat.ID
					sp.AddonSync.ManagedName = autoName
					sp.AddonSync.Removed = false
				}
				sp.Folder = cfg.Folder
				sp.SortOrder = entry.order
				// Update params/maxItems only on the matching plugin source, preserving
				// targets (including targets.users), rules, artwork, rotate, limit and enabled.
				for i := range sp.Sources {
					if sp.Sources[i].Kind == kindID {
						if it.Params != nil {
							sp.Sources[i].Params = params
						}
						if it.MaxItems > 0 || req.MaxItems > 0 {
							sp.Sources[i].MaxItems = maxItems
						}
						break
					}
				}
				if wasRemoved {
					return stripRemovedWarnings(prevWarnings), true
				}
				return nil, false
			})
			if err != nil {
				res.Skipped = append(res.Skipped, core.AddonImportSkippedItem{
					Key:    entry.key,
					Name:   firstNonEmpty(entry.cat.Name, entry.cat.ID),
					Reason: err.Error(),
				})
				continue
			}
			if wasRemoved {
				res.Restored++
			} else {
				res.Updated++
			}
			res.CollectionIDs = append(res.CollectionIDs, updatedSpec.ID)
			continue
		}

		sp := core.CollectionSpec{
			Name:      chosenName,
			MediaType: mediaType,
			Folder:    cfg.Folder,
			SortOrder: entry.order,
			Sources: []core.CollectionSource{{
				Kind:     kindID,
				Params:   params,
				MaxItems: maxItems,
			}},
			Match:   collections.MatchAll,
			Sort:    "source",
			Refresh: cfg.Refresh,
			Artwork: core.CollectionArtwork{Mode: "auto"},
			Targets: cfg.Targets,
			Enabled: cfg.Enabled,
			AddonSync: &core.CollectionAddonSync{
				AddonID:     rec.ID,
				AddonName:   addonName,
				CatalogType: entry.cat.Type,
				CatalogID:   entry.cat.ID,
				CatalogKey:  entry.key,
				ManagedName: autoName,
			},
		}
		var created *core.CollectionSpec
		if deferBuilds {
			created, err = ci.cols.CreateSpecDeferred(ctx, sp)
		} else {
			created, err = ci.cols.CreateSpec(ctx, sp)
		}
		if err != nil {
			res.Skipped = append(res.Skipped, core.AddonImportSkippedItem{
				Key:    entry.key,
				Name:   firstNonEmpty(entry.cat.Name, entry.cat.ID),
				Reason: err.Error(),
			})
			continue
		}
		res.Created++
		res.CollectionIDs = append(res.CollectionIDs, created.ID)
	}

	// Mark any previously managed collections whose catalogue vanished from the manifest.
	removedCount, err := ci.markMissingRemoved(ctx, rec.ID, addonName, catByKey, allManaged)
	if err != nil {
		ci.log.Warn("addon import: could not flag removed catalogues", "addon", rec.ID, "err", err)
	}
	res.Removed = removedCount

	now := ci.now().UTC()
	knownKeys := mergeKnownKeys(prevSub.KnownKeys, allKeys)
	summary := &core.AddonImportSummary{
		Created:  res.Created,
		Updated:  res.Updated,
		Removed:  res.Removed,
		Restored: res.Restored,
		Skipped:  res.Skipped,
	}
	sub := storedSubscription{
		AddonID:        rec.ID,
		Folder:         cfg.Folder,
		NameMode:       cfg.NameMode,
		NameAffix:      cfg.NameAffix,
		FollowManifest: cfg.FollowManifest,
		Refresh:        cfg.Refresh,
		MaxItems:       cfg.MaxItems,
		Enabled:        cfg.Enabled,
		Targets:        cfg.Targets,
		KnownKeys:      knownKeys,
		LastSyncedAt:   now,
		LastSummary:    summary,
		CreatedAt:      prevSub.CreatedAt,
		UpdatedAt:      now,
	}
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = now
	}
	if err := ci.saveSubscription(ctx, sub); err != nil {
		return nil, err
	}

	finalSpecs, _ := ci.cols.ListSpecsLight(ctx)
	managedCount, totalRemoved := countAddonSpecs(finalSpecs, rec.ID)
	recCopy := rec
	res.Subscription = ci.toSubscriptionView(sub, &recCopy, managedCount, totalRemoved)
	return res, nil
}

// SyncAddon refreshes an add-on's manifest via the plugin manager and
// reconciles all managed collections (adding newly appeared standalone
// catalogues when FollowManifest is enabled, updating unedited names and
// manifest order, restoring returned catalogues, and flagging removed ones).
func (ci *CatalogImporter) SyncAddon(ctx context.Context, addonID string) (*core.AddonCatalogImportResult, error) {
	addonID = strings.TrimSpace(addonID)
	if addonID == "" {
		return nil, core.ErrNotFound
	}
	sub, ok, err := ci.getSubscription(ctx, addonID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, core.ErrNotFound
	}
	_, changed, checkErr := ci.mgr.CheckUpdate(ctx, addonID)
	if checkErr != nil && !errors.Is(checkErr, plugins.ErrNotFound) {
		return nil, wrapAddonErr(checkErr)
	}
	rec, err := ci.mgr.Get(ctx, addonID)
	if err != nil {
		return nil, wrapAddonErr(err)
	}
	res, err := ci.reconcileAddon(ctx, rec, sub)
	if err != nil {
		return nil, err
	}
	res.ManifestChanged = changed
	return res, nil
}

// DeleteSubscription unlinks an add-on import subscription. When
// deleteCollections is true, it also deletes managed collections belonging to
// that add-on.
func (ci *CatalogImporter) DeleteSubscription(ctx context.Context, addonID string, deleteCollections bool) error {
	addonID = strings.TrimSpace(addonID)
	if addonID == "" {
		return core.ErrNotFound
	}
	unlock := ci.lockAddon(addonID)
	defer unlock()

	res, err := ci.db.ExecContext(ctx, `DELETE FROM collections_addon_imports WHERE addon_id=?`, addonID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 && !deleteCollections {
		return core.ErrNotFound
	}
	if deleteCollections {
		specs, err := ci.cols.ListSpecsLight(ctx)
		if err != nil {
			return err
		}
		for _, sp := range specs {
			if sp.AddonSync != nil && sp.AddonSync.AddonID == addonID {
				_ = ci.cols.DeleteSpec(ctx, sp.ID)
			}
		}
	}
	return nil
}

// onPluginSync is called by plugins.Manager whenever an add-on is installed,
// enabled, disabled, reconfigured, or updated via its hourly manifest check.
func (ci *CatalogImporter) onPluginSync(ctx context.Context, rec plugins.Record) {
	if !rec.Enabled || !rec.Manifest.IsStremioOnly() {
		return
	}
	sub, ok, err := ci.getSubscription(ctx, rec.ID)
	if err != nil || !ok {
		return
	}
	if _, err := ci.reconcileAddon(ctx, rec, sub); err != nil {
		ci.log.Warn("addon catalog sync failed", "addon", rec.ID, "err", err)
	}
}

// onPluginRemove flags managed collections when their backing add-on is
// uninstalled, without deleting the collections or wiping their last members.
func (ci *CatalogImporter) onPluginRemove(ctx context.Context, addonID string) {
	unlock := ci.lockAddon(addonID)
	defer unlock()

	_, _ = ci.db.ExecContext(ctx, `DELETE FROM collections_addon_imports WHERE addon_id=?`, addonID)
	specs, err := ci.cols.ListSpecsLight(ctx)
	if err != nil {
		return
	}
	for _, sp := range specs {
		if sp.AddonSync == nil || sp.AddonSync.AddonID != addonID {
			continue
		}
		addonName := firstNonEmpty(sp.AddonSync.AddonName, addonID)
		_, _ = ci.cols.UpdateSpecFromAddonSync(ctx, sp.ID, func(cur *core.CollectionSpec, prevWarnings []string) ([]string, bool) {
			if cur.AddonSync != nil {
				cur.AddonSync.Removed = true
			}
			return withRemovedWarning(prevWarnings, fmt.Sprintf("%s %s was removed.", removedWarningPrefix, addonName)), true
		})
	}
}

// reconcileAddon brings managed collections in line with rec.Manifest under
// the addon's lock.
func (ci *CatalogImporter) reconcileAddon(ctx context.Context, rec plugins.Record, sub storedSubscription) (*core.AddonCatalogImportResult, error) {
	unlock := ci.lockAddon(rec.ID)
	defer unlock()

	// Re-read subscription under lock so concurrent Apply/Sync see latest known keys.
	if latest, ok, err := ci.getSubscription(ctx, rec.ID); err == nil && ok {
		sub = latest
	}
	addonName := firstNonEmpty(rec.Name, rec.Manifest.Name, rec.ID)
	cats := listableManifestCatalogs(rec.Manifest)
	catByKey := make(map[string]manifestCatalogEntry, len(cats))
	allKeys := make([]string, 0, len(cats))
	for i, c := range cats {
		key := plugins.StremioCatalogKey(c)
		catByKey[key] = manifestCatalogEntry{cat: c, key: key, order: i + 1}
		allKeys = append(allKeys, key)
	}
	dupNames := duplicateCatalogNames(cats)

	specs, err := ci.cols.ListSpecsLight(ctx)
	if err != nil {
		return nil, err
	}
	existingByKey, allManaged := indexAddonCollections(specs, rec.ID)
	known := make(map[string]bool, len(sub.KnownKeys))
	for _, k := range sub.KnownKeys {
		known[k] = true
	}

	res := &core.AddonCatalogImportResult{
		AddonID:   rec.ID,
		AddonName: addonName,
	}

	// 1. Update existing managed collections: renames (when owner did not rename),
	//    manifest order, addon name, and restore any catalogue that returned.
	for key, entry := range catByKey {
		existing, ok := existingByKey[key]
		if !ok || existing.AddonSync == nil || existing.AddonSync.AddonID != rec.ID {
			continue
		}
		mediaType := collections.MediaMovie
		if entry.cat.Type == stremio.TypeSeries {
			mediaType = collections.MediaShow
		}
		autoName := formatImportedCatalogName(entry.cat, mediaType, sub.NameMode, sub.NameAffix, dupNames)
		wasRemoved := existing.AddonSync.Removed
		wantName := existing.Name
		if existing.Name == existing.AddonSync.ManagedName || existing.AddonSync.ManagedName == "" {
			wantName = autoName
		}
		needsUpdate := wasRemoved ||
			existing.SortOrder != entry.order ||
			existing.Name != wantName ||
			existing.AddonSync.ManagedName != autoName ||
			existing.AddonSync.AddonName != addonName
		if !needsUpdate {
			continue
		}
		updated, err := ci.cols.UpdateSpecFromAddonSync(ctx, existing.ID, func(sp *core.CollectionSpec, prevWarnings []string) ([]string, bool) {
			if sp.AddonSync == nil {
				return nil, false
			}
			if sp.Name == sp.AddonSync.ManagedName || sp.AddonSync.ManagedName == "" {
				sp.Name = autoName
			}
			sp.AddonSync.AddonName = addonName
			sp.AddonSync.CatalogType = entry.cat.Type
			sp.AddonSync.CatalogID = entry.cat.ID
			sp.AddonSync.ManagedName = autoName
			sp.AddonSync.Removed = false
			sp.SortOrder = entry.order
			if wasRemoved {
				return stripRemovedWarnings(prevWarnings), true
			}
			return nil, false
		})
		if err != nil {
			ci.log.Warn("addon sync: could not update collection", "id", existing.ID, "err", err)
			continue
		}
		if wasRemoved {
			res.Restored++
		} else {
			res.Updated++
		}
		res.CollectionIDs = append(res.CollectionIDs, updated.ID)
	}

	// 2. When followManifest is on, add newly appeared standalone catalogues
	//    whose key was not present in previous manifests.
	if sub.FollowManifest && rec.Manifest.HasCapability(plugins.CapListSource) {
		for _, c := range cats {
			key := plugins.StremioCatalogKey(c)
			if known[key] {
				continue
			}
			if _, exists := existingByKey[key]; exists {
				continue
			}
			if !c.Standalone() {
				res.Skipped = append(res.Skipped, core.AddonImportSkippedItem{
					Key:    key,
					Name:   firstNonEmpty(c.Name, c.ID),
					Reason: formatExtraReason(c, nil),
				})
				continue
			}
			entry := catByKey[key]
			mediaType := collections.MediaMovie
			if c.Type == stremio.TypeSeries {
				mediaType = collections.MediaShow
			}
			autoName := formatImportedCatalogName(c, mediaType, sub.NameMode, sub.NameAffix, dupNames)
			sp := core.CollectionSpec{
				Name:      autoName,
				MediaType: mediaType,
				Folder:    sub.Folder,
				SortOrder: entry.order,
				Sources: []core.CollectionSource{{
					Kind:     extend.PluginID(rec.ID, key),
					MaxItems: sub.MaxItems,
				}},
				Match:   collections.MatchAll,
				Sort:    "source",
				Refresh: sub.Refresh,
				Artwork: core.CollectionArtwork{Mode: "auto"},
				Targets: sub.Targets,
				Enabled: sub.Enabled,
				AddonSync: &core.CollectionAddonSync{
					AddonID:     rec.ID,
					AddonName:   addonName,
					CatalogType: c.Type,
					CatalogID:   c.ID,
					CatalogKey:  key,
					ManagedName: autoName,
				},
			}
			created, err := ci.cols.CreateSpecDeferred(ctx, sp)
			if err != nil {
				res.Skipped = append(res.Skipped, core.AddonImportSkippedItem{
					Key:    key,
					Name:   firstNonEmpty(c.Name, c.ID),
					Reason: err.Error(),
				})
				continue
			}
			res.Created++
			res.CollectionIDs = append(res.CollectionIDs, created.ID)
		}
	}

	// 3. Flag any managed collection whose catalogue disappeared from the manifest.
	removedCount, err := ci.markMissingRemoved(ctx, rec.ID, addonName, catByKey, allManaged)
	if err != nil {
		ci.log.Warn("addon sync: could not flag removed catalogues", "addon", rec.ID, "err", err)
	}
	res.Removed = removedCount

	// Persist updated known keys & sync summary only when anything changed or
	// this is a first/explicit sync.
	nextKnown := mergeKnownKeys(sub.KnownKeys, allKeys)
	knownChanged := !slices.Equal(nextKnown, sub.KnownKeys)
	if knownChanged || res.Created > 0 || res.Updated > 0 || res.Removed > 0 || res.Restored > 0 || len(res.Skipped) > 0 || sub.LastSyncedAt.IsZero() {
		now := ci.now().UTC()
		sub.KnownKeys = nextKnown
		sub.LastSyncedAt = now
		sub.UpdatedAt = now
		sub.LastSummary = &core.AddonImportSummary{
			Created:  res.Created,
			Updated:  res.Updated,
			Removed:  res.Removed,
			Restored: res.Restored,
			Skipped:  res.Skipped,
		}
		if err := ci.saveSubscription(ctx, sub); err != nil {
			return nil, err
		}
	}

	finalSpecs, _ := ci.cols.ListSpecsLight(ctx)
	managedCount, totalRemoved := countAddonSpecs(finalSpecs, rec.ID)
	recCopy := rec
	res.Subscription = ci.toSubscriptionView(sub, &recCopy, managedCount, totalRemoved)
	return res, nil
}

func (ci *CatalogImporter) markMissingRemoved(ctx context.Context, addonID, addonName string, present map[string]manifestCatalogEntry, managed []core.CollectionSpec) (int, error) {
	flagged := 0
	for _, sp := range managed {
		if sp.AddonSync == nil || sp.AddonSync.AddonID != addonID {
			continue
		}
		if _, ok := present[sp.AddonSync.CatalogKey]; ok {
			continue
		}
		if sp.AddonSync.Removed {
			continue
		}
		warnMsg := fmt.Sprintf("%s %q is no longer listed by %s. Edit or delete this collection, or restore it in the add-on.", removedWarningPrefix, firstNonEmpty(sp.AddonSync.CatalogID, sp.Name), addonName)
		if _, err := ci.cols.UpdateSpecFromAddonSync(ctx, sp.ID, func(cur *core.CollectionSpec, prevWarnings []string) ([]string, bool) {
			if cur.AddonSync != nil {
				cur.AddonSync.AddonName = addonName
				cur.AddonSync.Removed = true
			}
			return withRemovedWarning(prevWarnings, warnMsg), true
		}); err != nil {
			return flagged, err
		}
		flagged++
	}
	return flagged, nil
}

// ---------------------------------------------------------------------------
// Storage

func (ci *CatalogImporter) getSubscription(ctx context.Context, addonID string) (storedSubscription, bool, error) {
	row := ci.db.QueryRowContext(ctx, `SELECT addon_id,folder,name_mode,name_affix,follow_manifest,refresh,max_items,enabled,targets_json,known_keys_json,last_synced_at,last_summary_json,created_at,updated_at FROM collections_addon_imports WHERE addon_id=?`, addonID)
	s, err := scanSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return storedSubscription{}, false, nil
	}
	if err != nil {
		return storedSubscription{}, false, err
	}
	return s, true, nil
}

func (ci *CatalogImporter) listSubscriptions(ctx context.Context) ([]storedSubscription, error) {
	rows, err := ci.db.QueryContext(ctx, `SELECT addon_id,folder,name_mode,name_affix,follow_manifest,refresh,max_items,enabled,targets_json,known_keys_json,last_synced_at,last_summary_json,created_at,updated_at FROM collections_addon_imports ORDER BY created_at ASC, addon_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedSubscription
	for rows.Next() {
		s, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanSubscription(sc interface{ Scan(...any) error }) (storedSubscription, error) {
	var s storedSubscription
	var follow, enabled int
	var targetsJSON, knownJSON, summaryJSON string
	var syncedAt, createdAt, updatedAt int64
	if err := sc.Scan(&s.AddonID, &s.Folder, &s.NameMode, &s.NameAffix, &follow, &s.Refresh, &s.MaxItems, &enabled, &targetsJSON, &knownJSON, &syncedAt, &summaryJSON, &createdAt, &updatedAt); err != nil {
		return storedSubscription{}, err
	}
	s.FollowManifest = follow != 0
	s.Enabled = enabled != 0
	if targetsJSON != "" {
		_ = json.Unmarshal([]byte(targetsJSON), &s.Targets)
	}
	if !s.Targets.Users.All && s.Targets.Users.IDs == nil {
		s.Targets.Users = core.AllUsers()
	}
	if knownJSON != "" {
		_ = json.Unmarshal([]byte(knownJSON), &s.KnownKeys)
	}
	if summaryJSON != "" && summaryJSON != "{}" && summaryJSON != "null" {
		var sum core.AddonImportSummary
		if json.Unmarshal([]byte(summaryJSON), &sum) == nil {
			s.LastSummary = &sum
		}
	}
	if syncedAt > 0 {
		s.LastSyncedAt = time.Unix(syncedAt, 0).UTC()
	}
	if createdAt > 0 {
		s.CreatedAt = time.Unix(createdAt, 0).UTC()
	}
	if updatedAt > 0 {
		s.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	}
	return s, nil
}

func (ci *CatalogImporter) saveSubscription(ctx context.Context, s storedSubscription) error {
	targetsB, err := json.Marshal(s.Targets)
	if err != nil {
		return err
	}
	known := s.KnownKeys
	if known == nil {
		known = []string{}
	}
	knownB, err := json.Marshal(known)
	if err != nil {
		return err
	}
	summaryB := []byte("{}")
	if s.LastSummary != nil {
		summaryB, err = json.Marshal(s.LastSummary)
		if err != nil {
			return err
		}
	}
	syncedAt := int64(0)
	if !s.LastSyncedAt.IsZero() {
		syncedAt = s.LastSyncedAt.Unix()
	}
	createdAt := s.CreatedAt.Unix()
	if s.CreatedAt.IsZero() {
		createdAt = ci.now().UTC().Unix()
	}
	updatedAt := s.UpdatedAt.Unix()
	if s.UpdatedAt.IsZero() {
		updatedAt = ci.now().UTC().Unix()
	}
	_, err = ci.db.ExecContext(ctx, `
INSERT INTO collections_addon_imports(
  addon_id,folder,name_mode,name_affix,follow_manifest,refresh,max_items,enabled,
  targets_json,known_keys_json,last_synced_at,last_summary_json,created_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(addon_id) DO UPDATE SET
  folder=excluded.folder,
  name_mode=excluded.name_mode,
  name_affix=excluded.name_affix,
  follow_manifest=excluded.follow_manifest,
  refresh=excluded.refresh,
  max_items=excluded.max_items,
  enabled=excluded.enabled,
  targets_json=excluded.targets_json,
  known_keys_json=excluded.known_keys_json,
  last_synced_at=excluded.last_synced_at,
  last_summary_json=excluded.last_summary_json,
  updated_at=excluded.updated_at`,
		s.AddonID, s.Folder, s.NameMode, s.NameAffix, store.BoolInt(s.FollowManifest), s.Refresh, s.MaxItems, store.BoolInt(s.Enabled),
		string(targetsB), string(knownB), syncedAt, string(summaryB), createdAt, updatedAt,
	)
	return err
}

func (ci *CatalogImporter) toSubscriptionView(s storedSubscription, rec *plugins.Record, managedCount, removedCount int) core.AddonCatalogSubscription {
	v := core.AddonCatalogSubscription{
		AddonID:        s.AddonID,
		AddonName:      s.AddonID,
		Folder:         s.Folder,
		NameMode:       s.NameMode,
		NameAffix:      s.NameAffix,
		FollowManifest: s.FollowManifest,
		Refresh:        s.Refresh,
		MaxItems:       s.MaxItems,
		Enabled:        s.Enabled,
		Targets:        s.Targets,
		ManagedCount:   managedCount,
		RemovedCount:   removedCount,
		LastSummary:    s.LastSummary,
	}
	if !s.LastSyncedAt.IsZero() {
		t := s.LastSyncedAt.UTC()
		v.LastSyncedAt = &t
	}
	if rec != nil {
		v.AddonName = firstNonEmpty(rec.Name, rec.Manifest.Name, rec.ID)
		v.AddonLogo = rec.Manifest.Logo
		v.Host = plugins.AddonHost(*rec)
		v.AddonEnabled = rec.Enabled
		v.CatalogCount = len(listableManifestCatalogs(rec.Manifest))
	}
	return v
}

// ---------------------------------------------------------------------------
// Helpers

type manifestCatalogEntry struct {
	cat   stremio.Catalog
	key   string
	order int
}

type normalizedImportConfig struct {
	Folder         string
	NameMode       string
	NameAffix      string
	FollowManifest bool
	Refresh        string
	MaxItems       int
	Enabled        bool
	Targets        core.CollectionTargets
}

func normalizeImportConfig(req core.AddonCatalogImportRequest) (normalizedImportConfig, error) {
	fields := map[string]string{}
	folder := strings.TrimSpace(req.Folder)
	if len(folder) > 80 {
		fields["folder"] = "Keep the folder name under 80 characters."
	}
	nameMode := strings.TrimSpace(req.NameMode)
	if nameMode == "" {
		nameMode = core.AddonImportNameAutoType
	}
	switch nameMode {
	case core.AddonImportNameAutoType, core.AddonImportNamePlain, core.AddonImportNameAppendType, core.AddonImportNamePrefix, core.AddonImportNameSuffix:
	default:
		fields["nameMode"] = "Unknown naming mode."
	}
	affix := strings.TrimSpace(req.NameAffix)
	if len(affix) > 40 {
		fields["nameAffix"] = "Keep the prefix or suffix under 40 characters."
	}
	refresh := strings.TrimSpace(req.Refresh)
	if refresh == "" {
		refresh = collections.RefreshDaily
	}
	switch refresh {
	case collections.RefreshHourly, collections.RefreshDaily, collections.RefreshWeekly:
	default:
		fields["refresh"] = "Choose hourly, daily, or weekly refresh for add-on catalogues."
	}
	maxItems := req.MaxItems
	if maxItems == 0 {
		maxItems = DefaultPluginListItems
	} else if maxItems < 1 || maxItems > MaxPluginListItems {
		fields["maxItems"] = fmt.Sprintf("Must be between 1 and %d.", MaxPluginListItems)
	}
	targets := req.Targets
	if !targets.CollectionsTab && !targets.HomeRow && !targets.SectionRow {
		targets.CollectionsTab = true
	}
	if !targets.Users.All && targets.Users.IDs == nil {
		targets.Users = core.AllUsers()
	}
	targets.Schedule = collections.NormalizeSchedule(targets.Schedule)
	if err := collections.ValidateSchedule(targets.Schedule); err != nil {
		fields["targets.schedule"] = err.Error()
	}
	if len(fields) > 0 {
		return normalizedImportConfig{}, &importValidationError{fields: fields}
	}
	return normalizedImportConfig{
		Folder:         folder,
		NameMode:       nameMode,
		NameAffix:      affix,
		FollowManifest: req.FollowManifest,
		Refresh:        refresh,
		MaxItems:       maxItems,
		Enabled:        req.Enabled,
		Targets:        targets,
	}, nil
}

func listableManifestCatalogs(man plugins.Manifest) []stremio.Catalog {
	if man.ConfigurationRequired() || !man.HasResource(stremio.ResourceCatalog) {
		return nil
	}
	sm := man.Stremio()
	seen := map[string]bool{}
	out := make([]stremio.Catalog, 0, len(sm.Catalogs))
	for _, c := range sm.Catalogs {
		if c.Type != stremio.TypeMovie && c.Type != stremio.TypeSeries {
			continue
		}
		key := plugins.StremioCatalogKey(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

func duplicateCatalogNames(cats []stremio.Catalog) map[string]bool {
	counts := make(map[string]int, len(cats))
	for _, c := range cats {
		name := strings.ToLower(strings.TrimSpace(firstNonEmpty(c.Name, c.ID)))
		counts[name]++
	}
	dups := map[string]bool{}
	for name, n := range counts {
		if n > 1 {
			dups[name] = true
		}
	}
	return dups
}

func buildCatalogPreviewItem(c stremio.Catalog, key string, order int, dupNames map[string]bool) core.AddonCatalogPreviewItem {
	name := firstNonEmpty(c.Name, c.ID)
	mediaType := collections.MediaMovie
	if c.Type == stremio.TypeSeries {
		mediaType = collections.MediaShow
	}
	var extras []core.AddonCatalogExtra
	var reqExtras []string
	for _, x := range c.Extras() {
		if x.Name == "skip" {
			continue
		}
		extras = append(extras, core.AddonCatalogExtra{
			Name:     x.Name,
			Label:    extraDisplayLabel(x.Name),
			Required: x.IsRequired,
			Options:  append([]string(nil), x.Options...),
		})
		if x.IsRequired {
			reqExtras = append(reqExtras, x.Name)
		}
	}
	return core.AddonCatalogPreviewItem{
		Key:              key,
		Type:             c.Type,
		MediaType:        mediaType,
		ID:               c.ID,
		Name:             name,
		Order:            order,
		SupportsSkip:     c.SupportsSkip(),
		Standalone:       len(reqExtras) == 0,
		Extras:           extras,
		RequiredExtras:   reqExtras,
		HasDuplicateName: dupNames[strings.ToLower(strings.TrimSpace(name))],
	}
}

func extraDisplayLabel(name string) string {
	switch name {
	case "genre":
		return "Genre"
	case "search":
		return "Search"
	default:
		if name == "" {
			return ""
		}
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

func formatImportedCatalogName(c stremio.Catalog, mediaType, mode, affix string, dupNames map[string]bool) string {
	base := strings.TrimSpace(firstNonEmpty(c.Name, c.ID))
	typeLabel := "Movies"
	if mediaType == collections.MediaShow {
		typeLabel = "Series"
	}
	var out string
	switch mode {
	case core.AddonImportNamePlain:
		out = base
	case core.AddonImportNameAppendType:
		out = fmt.Sprintf("%s (%s)", base, typeLabel)
	case core.AddonImportNamePrefix:
		if affix != "" {
			out = affix + " " + base
		} else {
			out = base
		}
	case core.AddonImportNameSuffix:
		if affix != "" {
			out = base + " " + affix
		} else {
			out = base
		}
	default: // autoType
		if dupNames[strings.ToLower(base)] && !alreadyNamesMediaType(base) {
			out = fmt.Sprintf("%s (%s)", base, typeLabel)
		} else {
			out = base
		}
	}
	return clampRunes(strings.TrimSpace(out), 120)
}

func alreadyNamesMediaType(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, word := range []string{"movie", "movies", "film", "films", "series", "show", "shows", "tv"} {
		if strings.HasSuffix(lower, " "+word) || strings.HasSuffix(lower, "("+word+")") || lower == word {
			return true
		}
	}
	return false
}

func indexAddonCollections(specs []core.CollectionSpec, addonID string) (byKey map[string]core.CollectionSpec, managed []core.CollectionSpec) {
	byKey = map[string]core.CollectionSpec{}
	prefix := "plugin:" + addonID + "/"
	// Prefer explicit AddonSync matches first.
	for _, sp := range specs {
		if sp.AddonSync != nil && sp.AddonSync.AddonID == addonID {
			managed = append(managed, sp)
			if _, exists := byKey[sp.AddonSync.CatalogKey]; !exists {
				byKey[sp.AddonSync.CatalogKey] = sp
			}
		}
	}
	// Also recognize single-source collections manually built from this add-on.
	for _, sp := range specs {
		if sp.AddonSync != nil || len(sp.Sources) != 1 {
			continue
		}
		if key, ok := strings.CutPrefix(sp.Sources[0].Kind, prefix); ok && key != "" {
			if _, exists := byKey[key]; !exists {
				byKey[key] = sp
			}
		}
	}
	return byKey, managed
}

func countAddonSpecs(specs []core.CollectionSpec, addonID string) (managed, removed int) {
	for _, sp := range specs {
		if sp.AddonSync != nil && sp.AddonSync.AddonID == addonID {
			managed++
			if sp.AddonSync.Removed {
				removed++
			}
		}
	}
	return managed, removed
}

func mergeKnownKeys(prev, current []string) []string {
	seen := make(map[string]bool, len(prev)+len(current))
	out := make([]string, 0, len(prev)+len(current))
	for _, k := range prev {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, k := range current {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func formatExtraReason(c stremio.Catalog, err error) string {
	var req []string
	for _, x := range c.Extras() {
		if x.IsRequired && x.Name != "skip" {
			req = append(req, x.Name)
		}
	}
	if err != nil {
		return err.Error()
	}
	if len(req) > 0 {
		return "Requires extra: " + strings.Join(req, ", ")
	}
	return "Requires filter parameters"
}

func withRemovedWarning(prev []string, msg string) []string {
	out := stripRemovedWarnings(prev)
	return append(out, msg)
}

func stripRemovedWarnings(prev []string) []string {
	out := make([]string, 0, len(prev))
	for _, w := range prev {
		if strings.HasPrefix(w, removedWarningPrefix) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func clampMaxItems(n int) int {
	if n < 1 {
		return 1
	}
	if n > MaxPluginListItems {
		return MaxPluginListItems
	}
	return n
}

func clampRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max]))
}
