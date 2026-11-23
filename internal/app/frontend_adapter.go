package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/frontendadapter"
)

// adapterCatalog implements frontendadapter.Catalog from the plugin store:
// it reports one declaration per frontendAdapters[] entry of every enabled
// plugin, resolving each declared media-kind key to the library that kind
// created. A kind whose library does not exist yet is skipped, so an
// adapter never advertises a library the viewer could not open.
type adapterCatalog struct {
	app *App
}

func (c adapterCatalog) EnabledAdapters(ctx context.Context) ([]frontendadapter.Declaration, error) {
	if c.app.pluginsRT == nil || c.app.pluginsRT.mgr == nil {
		return nil, nil
	}
	recs, err := c.app.pluginsRT.mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []frontendadapter.Declaration
	for _, rec := range recs {
		if !rec.Enabled || rec.Manifest.Ether == nil {
			continue
		}
		man := rec.Manifest
		man.Ether = man.EffectiveEther()
		if man.Ether == nil || !man.HasCapability(plugins.CapFrontendAdapter) {
			continue
		}
		for _, meta := range man.Ether.Contributes.FrontendAdapters {
			decl := frontendadapter.Declaration{
				PluginID:       rec.ID,
				Meta:           meta,
				Name:           strings.TrimSpace(meta.Label),
				Icon:           rec.Manifest.Logo,
				Version:        rec.Version,
				DocsURL:        rec.Manifest.Ether.Repo,
				SettingsSchema: rec.Manifest.Ether.Settings,
			}
			decl.Libraries = c.resolveLibraries(ctx, rec.ID, meta.Libraries)
			out = append(out, decl)
		}
	}
	return out, nil
}

// resolveLibraries maps an adapter's declared media-kind keys to the
// library ids those kinds created. A key with no library yet contributes
// nothing; the adapter then sees an empty allowed set for it.
func (c adapterCatalog) resolveLibraries(ctx context.Context, pluginID string, kindKeys []string) []core.LibraryID {
	if len(kindKeys) == 0 || c.app.libraries == nil {
		return nil
	}
	out := make([]core.LibraryID, 0, len(kindKeys))
	for _, key := range kindKeys {
		lib, err := c.app.libraries.ByKey(ctx, core.PluginLibraryKey(pluginID, key))
		if err != nil {
			continue
		}
		out = append(out, lib.ID)
	}
	return out
}

// buildAdapterController constructs the FrontendAdapter lifecycle controller
// and keeps it in step with plugin install/enable/disable/upgrade/remove.
func (a *App) buildAdapterController(registry *frontend.Registry) (*frontendadapter.Controller, error) {
	if a.pluginsRT == nil || a.pluginsRT.mgr == nil {
		return nil, nil
	}
	ctrl, err := frontendadapter.NewController(adapterRegistry{Registry: registry, app: a}, adapterManager{Manager: a.pluginsRT.mgr, app: a}, adapterCatalog{app: a})
	if err != nil {
		return nil, err
	}
	a.pluginsRT.adapterCtrl = ctrl
	// OnSync fires for every already-installed plugin immediately, so the
	// initial registration is the same call path as later changes. Skip the
	// frontend lifecycle lock and reconcile when the synced plugin neither
	// declares a frontend adapter nor affects any currently live adapter.
	a.pluginsRT.mgr.OnSync(func(ctx context.Context, r plugins.Record) {
		if r.Manifest.HasCapability(plugins.CapFrontendAdapter) || len(ctrl.Live()) > 0 {
			a.syncAdapters(ctx)
		}
	})
	a.pluginsRT.mgr.OnRemove(func(ctx context.Context, _ string) {
		if len(ctrl.Live()) > 0 {
			a.syncAdapters(ctx)
		}
	})
	return ctrl, nil
}

// syncAdapters reconciles registered adapter frontends with the installed
// plugin set, then asks the frontend runtime to mount any adapter the owner
// already enabled. Errors are logged, not fatal: one bad adapter must not
// stop the others.
func (a *App) syncAdapters(ctx context.Context) {
	if a.pluginsRT == nil || a.pluginsRT.adapterCtrl == nil {
		return
	}
	// Serialize descriptor replacement with listener lifecycle operations.
	if a.frontends != nil {
		a.frontends.lifecycleMu.Lock()
	}
	err := a.pluginsRT.adapterCtrl.Sync(ctx)
	if a.frontends != nil {
		a.frontends.lifecycleMu.Unlock()
	}
	if err != nil {
		a.log.Warn("frontend adapter sync", "err", err)
	}
	// a.frontends is nil while the runtime is still being constructed; the
	// runtime's own start/reconcile mounts enabled adapters then.
	if a.frontends != nil {
		cfg := a.cfg.Get()
		a.frontends.reconcile(cfg)
		a.frontends.mu.Lock()
		running := a.frontends.runCtx != nil && !a.frontends.closed
		a.frontends.mu.Unlock()
		if running {
			for _, id := range a.pluginsRT.adapterCtrl.Live() {
				if !cfg.Frontends[string(id)].Enabled {
					continue
				}
				status := a.frontends.control.listeners.status(ctx, id)
				if status.Mounted {
					a.log.Info("frontend adapter mounted", "adapter", id)
				} else {
					a.log.Warn("frontend adapter mount failed", "adapter", id, "err", status.Error)
				}
			}
		}
	}
}

// adapterStreamURL builds the host backend behind Host.StreamURL (spec
// §6.5): it mints a scoped stream ticket for one of a plugin's own kind
// items for one viewer, after re-checking item ownership and library
// visibility. The item must belong to the calling plugin and its library
// must be visible to the viewer; anything else is refused.
func (a *App) adapterStreamURL(issuer *frontendadapter.TicketIssuer) func(ctx context.Context, pluginID string, viewerID int64, itemRef string) (string, error) {
	optsFn := a.adapterStreamURLOptions(issuer)
	return func(ctx context.Context, pluginID string, viewerID int64, itemRef string) (string, error) {
		return optsFn(ctx, pluginID, viewerID, itemRef, plugins.StreamURLOptions{})
	}
}

func (a *App) adapterStreamPartURL(issuer *frontendadapter.TicketIssuer) func(ctx context.Context, pluginID string, viewerID int64, itemRef string, partIndex *int) (string, error) {
	optsFn := a.adapterStreamURLOptions(issuer)
	return func(ctx context.Context, pluginID string, viewerID int64, itemRef string, partIndex *int) (string, error) {
		return optsFn(ctx, pluginID, viewerID, itemRef, plugins.StreamURLOptions{PartIndex: partIndex})
	}
}

func (a *App) adapterStreamURLOptions(issuer *frontendadapter.TicketIssuer) func(ctx context.Context, pluginID string, viewerID int64, itemRef string, opts plugins.StreamURLOptions) (string, error) {
	return func(ctx context.Context, pluginID string, viewerID int64, itemRef string, opts plugins.StreamURLOptions) (string, error) {
		if issuer == nil || a.pluginsRT == nil || a.pluginsRT.pluginitems == nil || a.libraries == nil {
			return "", plugins.ErrUnavailable
		}
		if opts.PartIndex != nil && *opts.PartIndex < 0 {
			return "", core.ErrNotFound
		}
		ref, err := core.ParseItemRef(itemRef)
		if err != nil {
			return "", core.ErrNotFound
		}
		if ref.Plugin() != pluginID {
			return "", plugins.ErrPermissionDenied
		}
		if viewerID <= 0 || a.users == nil {
			return "", plugins.ErrPermissionDenied
		}
		viewer, err := a.users.PolicyUser(ctx, viewerID)
		if err != nil || viewer == nil {
			return "", plugins.ErrPermissionDenied
		}
		item, libID, err := a.pluginsRT.pluginitems.GetByRef(ctx, ref)
		if err != nil {
			return "", core.ErrNotFound
		}
		lib, err := a.libraries.Get(ctx, libID)
		if err != nil || lib.Hidden || !viewer.AllowsLibrary(lib.ID) {
			return "", core.ErrNotFound
		}
		if opts.PartIndex != nil {
			rec, kerr := a.pluginsRT.pluginitems.GetKindByLibrary(ctx, libID)
			if kerr != nil {
				return "", core.ErrNotFound
			}
			lv, _, ok := rec.Decl.Level(item.Level)
			if !ok {
				return "", core.ErrNotFound
			}
			if lv.Playable && item.Playable {
				parts := core.ExtractKindItemPartsFromItem(item)
				if len(parts) == 0 || *opts.PartIndex >= len(parts) {
					return "", core.ErrNotFound
				}
			}
		}
		return issuer.StreamURL(frontendadapter.StreamGrant{
			ViewerID:  viewerID,
			ItemID:    ref.String(),
			Adapter:   pluginID,
			PartIndex: opts.PartIndex,
			Source:    opts.Source,
		})
	}
}

func (a *App) viewerHasPluginContentFilter(viewer *core.User) bool {
	if viewer == nil || a.safety == nil {
		return false
	}
	if viewer.MaxContentRating != "" {
		return true
	}
	p := a.safety.ForUser(viewer)
	return len(p.BlockedGenres) > 0 || len(p.BlockedKeywords) > 0
}

func (a *App) adapterKindLibrary(ctx context.Context, pluginID string, viewerID int64, kindKey string) (*core.User, core.Library, bool, error) {
	if a.pluginsRT == nil || a.pluginsRT.pluginitems == nil || a.libraries == nil || a.users == nil {
		return nil, core.Library{}, false, plugins.ErrUnavailable
	}
	if viewerID <= 0 {
		return nil, core.Library{}, false, plugins.ErrPermissionDenied
	}
	viewer, err := a.users.PolicyUser(ctx, viewerID)
	if err != nil || viewer == nil {
		return nil, core.Library{}, false, plugins.ErrPermissionDenied
	}
	lib, err := a.libraries.ByKey(ctx, core.PluginLibraryKey(pluginID, kindKey))
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return viewer, core.Library{}, false, nil
		}
		return nil, core.Library{}, false, err
	}
	if lib.Hidden || !viewer.AllowsLibrary(lib.ID) {
		return viewer, lib, false, nil
	}
	return viewer, lib, true, nil
}

// adapterKindVisible builds the host backend behind Host.KindVisible (#615):
// it evaluates the plugin library's LibraryPolicy (same libraryfilter rules as
// the console and protocol projectors) plus viewer library/safety access and
// returns the allowed subset of external IDs.
func (a *App) adapterKindVisible() func(ctx context.Context, pluginID string, viewerID int64, kindKey string, ids []string) ([]string, error) {
	return func(ctx context.Context, pluginID string, viewerID int64, kindKey string, ids []string) ([]string, error) {
		viewer, lib, ok, err := a.adapterKindLibrary(ctx, pluginID, viewerID, kindKey)
		if err != nil || !ok {
			return []string{}, err
		}
		items := a.pluginsRT.pluginitems
		visible, err := items.FilterVisibleIDs(ctx, pluginID, kindKey, lib, ids)
		if err != nil || len(visible) == 0 {
			return visible, err
		}
		if !a.viewerHasPluginContentFilter(viewer) {
			return visible, nil
		}
		rec, err := items.GetKindByLibrary(ctx, lib.ID)
		if err != nil {
			return nil, err
		}
		loaded, err := items.KindItemsByID(ctx, pluginID, kindKey, visible)
		if err != nil {
			return nil, err
		}
		allowed := make(map[string]bool, len(loaded))
		for _, it := range loaded {
			t := pluginitems.Project(it, rec.Decl, lib.Present)
			if a.safety.PolicyAllowed(viewer, &t) {
				allowed[it.ID] = true
			}
		}
		out := make([]string, 0, len(allowed))
		for _, id := range visible {
			if allowed[id] {
				out = append(out, id)
			}
		}
		return out, nil
	}
}

// adapterKindBrowse builds the host backend behind Host.KindBrowse (#615):
// it executes the same pluginitems Browse/Search + LibraryPolicy path the
// console uses, scoped to the requesting frontend-adapter viewer.
func (a *App) adapterKindBrowse() func(ctx context.Context, pluginID string, viewerID int64, kindKey string, q core.KindBrowseRequest) (core.KindPage, error) {
	return func(ctx context.Context, pluginID string, viewerID int64, kindKey string, q core.KindBrowseRequest) (core.KindPage, error) {
		viewer, lib, ok, err := a.adapterKindLibrary(ctx, pluginID, viewerID, kindKey)
		if err != nil {
			return core.KindPage{}, err
		}
		if !ok {
			if q.Total == "none" {
				return core.KindPage{Items: []core.KindItem{}}, nil
			}
			zero := 0
			return core.KindPage{Items: []core.KindItem{}, Total: &zero}, nil
		}
		noTotal, cheapTotal := true, true
		switch q.Total {
		case "none":
			noTotal, cheapTotal = true, false
		case "exact":
			noTotal, cheapTotal = false, false
		}
		items := a.pluginsRT.pluginitems
		policy, err := items.LibraryPolicy(ctx, lib)
		if err != nil {
			return core.KindPage{}, err
		}
		filterSafety := a.viewerHasPluginContentFilter(viewer)
		var kindRec pluginitems.KindRecord
		if filterSafety {
			kindRec, err = items.GetKindByLibrary(ctx, lib.ID)
			if err != nil {
				return core.KindPage{}, err
			}
		}
		limit := q.Limit
		if limit <= 0 {
			limit = pluginitems.DefaultBrowseLimit
		}
		if limit > pluginitems.MaxBrowseLimit {
			limit = pluginitems.MaxBrowseLimit
		}
		if queryStr := strings.TrimSpace(q.Query); queryStr != "" {
			searchPolicy := policy.ForSearch()
			page, err := items.Search(ctx, pluginitems.SearchQuery{
				NoTotal:       noTotal,
				CheapTotal:    cheapTotal,
				Policy:        searchPolicy,
				LibraryID:     lib.ID,
				Query:         queryStr,
				Level:         q.Level,
				Facets:        q.Facets,
				AllFacets:     q.AllFacets,
				ExcludeFacets: q.ExcludeFacets,
				Limit:         limit,
			})
			if err != nil {
				return core.KindPage{}, err
			}
			outItems := page.Items
			var totalPtr *int
			if filterSafety {
				filtered := make([]core.KindItem, 0, len(outItems))
				for _, it := range outItems {
					t := pluginitems.Project(it, kindRec.Decl, lib.Present)
					if a.safety.PolicyAllowed(viewer, &t) {
						filtered = append(filtered, it)
					}
				}
				outItems = filtered
				if q.Total != "none" && page.TotalExact {
					total := len(filtered)
					totalPtr = &total
				}
			} else if q.Total != "none" && page.TotalExact {
				total := page.Total
				totalPtr = &total
			}
			if outItems == nil {
				outItems = []core.KindItem{}
			}
			return core.KindPage{Items: outItems, NextCursor: page.NextCursor, Total: totalPtr}, nil
		}
		var parentRef core.ItemRef
		if q.ParentID != "" {
			parentRef = core.NewItemRef(pluginID, kindKey, q.ParentID)
		}
		mapBrowseErr := func(err error) error {
			if err != nil && strings.Contains(err.Error(), "invalid cursor") {
				return &plugins.ValidationError{Fields: []plugins.FieldError{{Field: "cursor", Message: "invalid cursor"}}}
			}
			return err
		}
		if !filterSafety {
			page, err := items.Browse(ctx, pluginitems.BrowseQuery{
				NoTotal:       noTotal,
				CheapTotal:    cheapTotal,
				Policy:        policy,
				LibraryID:     lib.ID,
				ParentRef:     parentRef,
				Level:         q.Level,
				Sort:          q.Sort,
				Desc:          q.Desc,
				Facets:        q.Facets,
				AllFacets:     q.AllFacets,
				ExcludeFacets: q.ExcludeFacets,
				Cursor:        q.Cursor,
				Limit:         limit,
			})
			if err != nil {
				return core.KindPage{}, mapBrowseErr(err)
			}
			outItems := page.Items
			if outItems == nil {
				outItems = []core.KindItem{}
			}
			var totalPtr *int
			if q.Total != "none" && page.TotalExact {
				total := page.Total
				totalPtr = &total
			}
			return core.KindPage{Items: outItems, NextCursor: page.NextCursor, Total: totalPtr}, nil
		}
		const maxSafetyWalkPages = 20
		kept := make([]core.KindItem, 0, limit)
		cursor := q.Cursor
		nextCursor := ""
		for walk := 0; walk < maxSafetyWalkPages && len(kept) < limit; walk++ {
			remaining := limit - len(kept)
			page, err := items.Browse(ctx, pluginitems.BrowseQuery{
				NoTotal:       true,
				Policy:        policy,
				LibraryID:     lib.ID,
				ParentRef:     parentRef,
				Level:         q.Level,
				Sort:          q.Sort,
				Desc:          q.Desc,
				Facets:        q.Facets,
				AllFacets:     q.AllFacets,
				ExcludeFacets: q.ExcludeFacets,
				Cursor:        cursor,
				Limit:         remaining,
			})
			if err != nil {
				return core.KindPage{}, mapBrowseErr(err)
			}
			for _, it := range page.Items {
				t := pluginitems.Project(it, kindRec.Decl, lib.Present)
				if a.safety.PolicyAllowed(viewer, &t) {
					kept = append(kept, it)
				}
			}
			nextCursor = page.NextCursor
			if nextCursor == "" || len(page.Items) == 0 {
				break
			}
			cursor = nextCursor
		}
		return core.KindPage{Items: kept, NextCursor: nextCursor}, nil
	}
}

// mountAdapterMedia registers the public kind-media endpoint. It is not
// LAN-only: a remote client follows the short-lived ticket URL from a
// plugin adapter response, and the ticket itself is the credential.
func (a *App) mountAdapterMedia(mux *http.ServeMux) {
	h := a.adapterMediaHandler()
	if h == nil {
		return
	}
	mux.Handle(frontendadapter.KindMediaPath, h)
}

// adapterMediaHandler builds the /ether/v1/kind-media/{ticket} endpoint. It
// returns nil when the kind resolver is unavailable, so the route stays
// unmounted.
func (a *App) adapterMediaHandler() *frontendadapter.MediaHandler {
	if a.pluginsRT == nil || a.pluginsRT.kindResolver == nil || a.pluginsRT.pluginitems == nil {
		return nil
	}
	issuer := a.pluginsRT.adapterTickets
	if issuer == nil {
		return nil
	}
	items := a.pluginsRT.pluginitems
	resolver := a.pluginsRT.kindResolver
	return frontendadapter.NewMediaHandler(frontendadapter.MediaDeps{
		Tickets: issuer,
		Log:     a.log.With("component", "kind-media"),
		Viewer: func(ctx context.Context, id int64) (*core.Viewer, error) {
			if a.users == nil {
				return nil, core.ErrNotFound
			}
			return a.users.PolicyUser(ctx, id)
		},
		Open: func(ctx context.Context, viewer *core.Viewer, itemRef string, accept []string) (*frontend.KindMedia, error) {
			ref, err := core.ParseItemRef(itemRef)
			if err != nil {
				return nil, err
			}
			item, _, err := items.GetByRef(ctx, ref)
			if err != nil {
				return nil, err
			}
			return resolver.OpenKind(ctx, viewer, item.Key, accept)
		},
		OpenPart: func(ctx context.Context, viewer *core.Viewer, itemRef string, partIndex int, accept []string) (*frontend.KindMedia, error) {
			ref, err := core.ParseItemRef(itemRef)
			if err != nil {
				return nil, err
			}
			item, _, err := items.GetByRef(ctx, ref)
			if err != nil {
				return nil, err
			}
			return resolver.OpenKindPart(ctx, viewer, item.Key, partIndex, accept)
		},
		OpenSource: func(ctx context.Context, viewer *core.Viewer, itemRef string, partIndex *int, source string, accept []string) (*frontend.KindMedia, error) {
			ref, err := core.ParseItemRef(itemRef)
			if err != nil {
				return nil, err
			}
			item, _, err := items.GetByRef(ctx, ref)
			if err != nil {
				return nil, err
			}
			return resolver.OpenKindPartSource(ctx, viewer, item.Key, partIndex, source, accept)
		},
	})
}

// adapterRegistry keeps listener ownership and registry ownership in step during
// replacement. Startup can register adapters before the runtime exists.
type adapterRegistry struct {
	*frontend.Registry
	app *App
}

func (r adapterRegistry) Unmount(ctx context.Context, id frontend.Protocol) error {
	if rt := r.app.frontends; rt != nil {
		if err := rt.control.listeners.stop(ctx, id); err != nil {
			return err
		}
	}
	if _, mounted := r.Registry.Instance(id); mounted {
		return r.Registry.Unmount(ctx, id)
	}
	r.app.log.Info("frontend adapter unmounted", "adapter", id)
	return nil
}

// adapterManager adds host-owned binary delivery to the normal plugin calls.
type adapterManager struct {
	*plugins.Manager
	app *App
}

func (m adapterManager) OpenFrontendBlob(ctx context.Context, pluginID string, viewerID int64, allowed []core.LibraryID, spec plugins.FrontendBlob) (io.ReadCloser, int64, error) {
	a := m.app
	if a.users == nil || a.libraries == nil || a.pluginsRT == nil || a.pluginsRT.blobcache == nil {
		return nil, 0, core.ErrNotFound
	}
	rec, err := m.Get(ctx, pluginID)
	if err != nil || !rec.Enabled || !rec.Manifest.HasCapability(plugins.CapFrontendAdapter) {
		return nil, 0, core.ErrNotFound
	}
	man := rec.Manifest.EffectiveEther()
	if man == nil {
		return nil, 0, core.ErrNotFound
	}
	owned := false
	for _, kind := range man.Contributes.MediaKinds {
		if core.PluginLibraryKey(pluginID, kind.Key) == spec.LibraryKey {
			owned = true
			break
		}
	}
	if !owned {
		return nil, 0, core.ErrNotFound
	}
	viewer, err := a.users.PolicyUser(ctx, viewerID)
	if err != nil || viewer == nil {
		return nil, 0, core.ErrNotFound
	}
	lib, err := a.libraries.ByKey(ctx, spec.LibraryKey)
	if err != nil || lib.Hidden || !viewer.AllowsLibrary(lib.ID) {
		return nil, 0, core.ErrNotFound
	}
	visible := false
	for _, id := range allowed {
		if id == lib.ID {
			visible = true
			break
		}
	}
	if !visible {
		return nil, 0, core.ErrNotFound
	}
	_, err = blobcache.ParseRef(spec.Ref)
	if err != nil {
		return nil, 0, core.ErrNotFound
	}
	limits := m.HostFor(pluginID).(interface {
		DataLimits(context.Context) plugins.RuntimeLimits
	}).DataLimits(ctx)
	deadline, err := time.ParseDuration(limits.Deadline)
	if err != nil || deadline <= 0 {
		return nil, 0, core.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	source, err := a.pluginsRT.blobcache.Namespace(spec.LibraryKey, blobcache.Quota{}).Open(blobcache.Ref(spec.Ref))
	if err != nil {
		cancel()
		return nil, 0, core.ErrNotFound
	}
	reader := &adapterBlobReader{Reader: source, source: source, cancel: cancel}
	size, err := source.Seek(0, io.SeekEnd)
	if err == nil {
		_, err = source.Seek(0, io.SeekStart)
	}
	if err == nil && size >= 0 && size <= limits.FetchBytes && spec.MemberIndex != nil {
		member, n, e := plugins.OpenFrontendArchiveMember(ctx, source, *spec.MemberIndex, limits)
		reader.member, reader.Reader, size, err = member, member, n, e
	}
	if err != nil || size < 0 || size > limits.FetchBytes {
		reader.Close()
		return nil, 0, core.ErrNotFound
	}
	reader.ctx = ctx
	return reader, size, nil
}

type adapterBlobReader struct {
	io.Reader
	ctx    context.Context
	source io.Closer
	member io.Closer
	cancel context.CancelFunc
}

func (r *adapterBlobReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
func (r *adapterBlobReader) Close() error {
	if r.member != nil {
		_ = r.member.Close()
	}
	err := r.source.Close()
	r.cancel()
	return err
}
