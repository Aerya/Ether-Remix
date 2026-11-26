// Package adapters bridges runtime plugin contributions (WS9a list sources
// and WS9c Live TV channel sources) into Ether's collection, personalization
// and Live TV registries.
package adapters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/personalize"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/stremio"
)

// MaxPluginListItems bounds the maximum items an explicit source config may request.
const MaxPluginListItems = plugins.MaxListItems

// DefaultPluginListItems is what a list that declares no size reads per
// refresh (shown in the install review, owner-adjustable).
const DefaultPluginListItems = plugins.DefaultListItems

// Bridge tracks the source kinds registered for each plugin so enable,
// disable, update and uninstall cleanly replace or remove them.
type Bridge struct {
	mgr  *plugins.Manager
	cols *collections.Service
	pers *personalize.Engine
	tv   *livetv.Service

	mu          sync.Mutex
	listIDs     map[string][]string
	livetvID    map[string]string
	pausedUntil map[string]time.Time
}

// Attach wires plugin list sources (collections + Home rows) and Live TV
// channel sources to the plugin manager's lifecycle.
func Attach(mgr *plugins.Manager, cols *collections.Service, pers *personalize.Engine, tv *livetv.Service) *Bridge {
	b := &Bridge{
		mgr:         mgr,
		cols:        cols,
		pers:        pers,
		tv:          tv,
		listIDs:     map[string][]string{},
		livetvID:    map[string]string{},
		pausedUntil: map[string]time.Time{},
	}
	if cols != nil && pers != nil {
		cols.SetOnBuilt(pers.InvalidateOwnerRows)
	}
	if tv != nil && mgr != nil {
		tv.SetStreamOpener(b.openPluginStream)
		tv.SetStreamExpander(b.expandPluginStream)
	}
	if mgr != nil {
		mgr.OnSync(b.Sync)
	}
	return b
}

// Sync updates all adapter registrations for one plugin record.
func (b *Bridge) Sync(_ context.Context, r plugins.Record) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, id := range b.listIDs[r.ID] {
		if b.cols != nil {
			b.cols.UnregisterSourceKind(id)
		}
		if b.pers != nil {
			b.pers.UnregisterRowSourceKind(id)
		}
	}
	delete(b.listIDs, r.ID)

	if id, ok := b.livetvID[r.ID]; ok {
		if b.tv != nil {
			b.tv.UnregisterSourceKind(id)
		}
		delete(b.livetvID, r.ID)
	}

	if !r.Enabled {
		return
	}

	c := r.Manifest.EffectiveEther().Contributes
	for _, ls := range c.ListSources {
		if !r.Manifest.HasCapability(plugins.CapListSource) || ls.Key == "" {
			continue
		}
		kind := &listSourceKind{
			bridge:     b,
			mgr:        b.mgr,
			pluginID:   r.ID,
			pluginName: r.Name,
			meta:       ls,
			fields:     plugins.ParseListParamFields(ls.Params),
		}
		kind.catalog, kind.declared = plugins.ListCatalog(r.Manifest.Stremio(), ls)
		if b.cols != nil {
			_ = b.cols.RegisterSourceKind(kind)
		}
		if b.pers != nil && kind.rowable() {
			_ = b.pers.RegisterRowSourceKind(&rowSourceKind{list: kind})
		}
		b.listIDs[r.ID] = append(b.listIDs[r.ID], kind.ID())
	}

	if r.Manifest.HasCapability(plugins.CapLiveTV) && c.Channels != nil {
		chKind := &channelSourceKind{
			mgr:        b.mgr,
			pluginID:   r.ID,
			pluginName: r.Name,
			meta:       *c.Channels,
			mode:       modeNative,
		}
		if r.Manifest.SpeaksStremio() {
			chKind.mode = modeAddon
			if r.Manifest.ServesLiveEvents() {
				chKind.onDemandInterval = eventAddonRefreshInterval
			}
		}
		if b.tv != nil {
			_ = b.tv.RegisterSourceKind(chKind)
		}
		b.livetvID[r.ID] = chKind.ID()
	}
}

func pluginText(pluginID, raw string) extend.Text {
	if strings.HasPrefix(raw, "$t:") {
		return extend.PluginText(pluginID, strings.TrimPrefix(raw, "$t:"))
	}
	return extend.Text{Resolved: raw}
}

func resolvePlainText(mgr *plugins.Manager, pluginID, raw, fallback string) string {
	if raw == "" {
		return fallback
	}
	if strings.HasPrefix(raw, "$t:") {
		key := strings.TrimPrefix(raw, "$t:")
		if mgr != nil {
			if s, ok := mgr.ResolveText(context.Background(), pluginID, key, "en"); ok && s != "" {
				return s
			}
		}
		return fallback
	}
	return raw
}

type listSourceKind struct {
	bridge     *Bridge
	mgr        *plugins.Manager
	pluginID   string
	pluginName string
	meta       plugins.ListSourceMeta
	fields     []plugins.ListParamField
	// catalog is the catalog the list reads. Its Type is empty for a
	// plugin list that serves whichever of movies and series a collection
	// asks for. declared marks a catalog the manifest declares (every
	// Stremio add-on catalog): its extras are checked against the
	// declaration and it pages only when it declares skip.
	catalog  stremio.Catalog
	declared bool
}

func (k *listSourceKind) MinRefresh() time.Duration {
	d, err := time.ParseDuration(k.meta.Refresh)
	if err != nil || d < time.Hour {
		return time.Hour
	}
	return d
}

func (k *listSourceKind) ID() string {
	return extend.PluginID(k.pluginID, k.meta.Key)
}

func (k *listSourceKind) label() string {
	return resolvePlainText(k.mgr, k.pluginID, k.meta.Label, firstNonEmpty(k.pluginName, k.meta.Key))
}

func (k *listSourceKind) mediaTypes() []string {
	switch k.catalog.Type {
	case stremio.TypeMovie:
		return []string{"movie"}
	case stremio.TypeSeries:
		return []string{"show"}
	default:
		return []string{"movie", "show"}
	}
}

func (k *listSourceKind) Describe() collections.KindDescribe {
	params, defaults := plugins.ParamFieldsFromList(k.pluginID, k.fields)
	if defaults != "{}" {
		defaults = `{"params":` + defaults + `}`
	}
	return collections.KindDescribe{
		Label:      pluginText(k.pluginID, firstNonEmpty(k.meta.Label, k.meta.Key)),
		Hint:       pluginText(k.pluginID, k.meta.Hint),
		Icon:       "list-video",
		Order:      500,
		Params:     params,
		Defaults:   defaults,
		MediaTypes: k.mediaTypes(),
	}
}

func (k *listSourceKind) Validate(ve *collections.ValidationError, path string, src core.CollectionSource) {
	if src.MaxItems < 0 || src.MaxItems > MaxPluginListItems {
		ve.Add(path+".maxItems", fmt.Sprintf("must be between 1 and %d", MaxPluginListItems))
	}
	if k.declared {
		if err := k.catalog.ValidateExtras(catalogExtras(src.Params)); err != nil {
			name, _, _ := strings.Cut(err.Error(), " ")
			ve.Add(path+".params."+name, err.Error())
		}
		return
	}
	for _, f := range k.fields {
		key := firstNonEmpty(f.Key, f.Name)
		if f.Required && key != "" && strings.TrimSpace(src.Params[key]) == "" {
			ve.Add(path+".params."+key, key+" is required")
		}
	}
}

// catalogExtras are a source's params as catalog extras: set values only,
// and never skip, which the reader adds while paging.
func catalogExtras(params map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range params {
		if v = strings.TrimSpace(v); k != "" && k != "skip" && v != "" {
			out[k] = v
		}
	}
	return out
}

// maxItems is how many items one refresh reads: the list's granted size
// (what it declares, or what the owner set in the plugin's limits). A
// collection that sets its own size reads that many, within what the
// list declares and the owner's limits; the default size for a list that
// declares none does not cut it short. granted reports whether the grant,
// not the collection, is the bound.
func (k *listSourceKind) maxItems(src core.CollectionSource) (n int, granted bool) {
	meta := k.meta
	if src.MaxItems > 0 && meta.MaxItems <= 0 {
		meta.MaxItems = MaxPluginListItems
	}
	n = meta.RequestedItems()
	if k.mgr != nil {
		n = k.mgr.GrantedListItems(k.pluginID, meta)
	}
	if src.MaxItems > 0 && src.MaxItems < n {
		return src.MaxItems, false
	}
	return n, true
}

// catalogs are the catalogs one gather reads: the list's own, or for a
// list without a type, one per media type the collection asks for.
func (k *listSourceKind) catalogs(kinds []string) []stremio.Catalog {
	if k.catalog.Type != "" {
		return []stremio.Catalog{k.catalog}
	}
	var out []stremio.Catalog
	for _, typ := range []string{stremio.TypeMovie, stremio.TypeSeries} {
		for _, kind := range kinds {
			if (kind == "tv") == (typ == stremio.TypeSeries) {
				c := k.catalog
				c.Type = typ
				out = append(out, c)
				break
			}
		}
	}
	if len(out) == 0 {
		c := k.catalog
		c.Type = stremio.TypeMovie
		out = append(out, c)
	}
	return out
}

// Gather reads the list through the add-on protocol and matches its items
// to the catalogue by id. It runs only in background builds and owner
// previews (collections never gathers a plugin list on a viewer request);
// a preview reads the first page only. Items without an id Ether can
// match are counted with the unmatched ones, never guessed by title.
func (k *listSourceKind) Gather(g *collections.Gather) {
	if k.mgr == nil {
		if g.Strict {
			g.Fail(fmt.Errorf("%s can't be read because plugins are not running on this server", k.label()))
			return
		}
		g.Warn("%s is unavailable", k.label())
		return
	}
	rec, err := k.mgr.Get(g.Ctx, k.pluginID)
	if err != nil {
		if g.Strict {
			g.Fail(err)
			return
		}
		g.Warn("%s: %v", k.label(), err)
		return
	}
	preview := collections.IsPreview(g.Ctx)
	if !preview && k.bridge != nil {
		if until, paused := k.bridge.rateLimitUntil(k.pluginID); paused {
			g.Fail(fmt.Errorf("add-on rate-limited until %s", until.UTC().Format(time.RFC3339)))
			return
		}
	}
	limit, granted := k.maxItems(g.Src)
	if preview && limit > collections.PreviewItems {
		limit = collections.PreviewItems
	}
	extra := catalogExtras(g.Src.Params)
	metadata := rec.Manifest.HasCapability(plugins.CapMetadata)
	stremioManifest := rec.Manifest.Stremio()
	foreignStremio := rec.Manifest.SpeaksStremio()
	var refs []collections.ExternalRef
	read, total, unidentified := false, 0, 0
	for _, cat := range k.catalogs(g.Kinds) {
		if total >= limit {
			break
		}
		fetcher := &healthFetcher{inner: k.mgr.Fetcher(rec.ID)}
		addon := k.mgr.Addon(rec)
		addon.Fetcher = fetcher
		var metas []stremio.MetaPreview
		if preview {
			metas, _, err = addon.CatalogPage(g.Ctx, cat.Type, cat.ID, extra)
		} else {
			metas, err = addon.Catalog(g.Ctx, cat, extra, limit-total)
		}
		if err != nil {
			var he *stremio.HTTPError
			rateLimited := errors.As(err, &he) && he.RateLimited()
			if rateLimited && k.bridge != nil {
				k.bridge.recordRateLimit(rec.ID, he.RetryAfter)
			}
			if preview && errors.Is(err, context.DeadlineExceeded) {
				g.Fail(fmt.Errorf("plugin list preview timed out: %w", err))
				return
			}
			if !fetcher.recorded {
				k.mgr.RecordFailure(g.Ctx, rec.ID, fmt.Sprintf("catalog %s/%s: %v", cat.Type, cat.ID, err))
			}
			// If earlier pages already yielded items, keep them and warn instead
			// of discarding the whole list when a deep skip page fails.
			// On a rate limit during a background build, fail instead of wiping
			// the collection's existing members.
			if (g.Strict || (!preview && rateLimited)) && len(metas) == 0 && !read {
				g.Fail(err)
				return
			}
			g.Warn("%s: %v", k.label(), err)
		}
		if err == nil || len(metas) > 0 {
			read = true
		}
		for _, m := range metas {
			if total >= limit {
				break
			}
			total++
			canMeta := metadata
			if canMeta && foreignStremio && m.ID != "" {
				metaType := strings.ToLower(m.Type)
				if metaType != stremio.TypeMovie && metaType != stremio.TypeSeries {
					metaType = cat.Type
				}
				canMeta = stremioManifest.TakesID(stremio.ResourceMeta, metaType, m.ID)
			}
			if ref, ok := catalogRef(m, cat.Type, canMeta, rec.ID); ok {
				refs = append(refs, ref)
			} else {
				unidentified++
			}
		}
	}
	if !read {
		return
	}
	if !preview {
		g.Paged(total, limit)
		if granted && total >= limit {
			g.Warn("%s: synced the first %d titles; raise Titles per list in the plugin's limits to sync more", k.label(), limit)
		}
	}
	if total == 0 {
		g.Warn("%s is empty", k.label())
		return
	}
	g.ResolveExternal(k.label(), refs, unidentified)
}

// catalogRef is a catalog item's identity: its IMDb, TMDB or TVDB id, or,
// when the add-on also provides title details, the add-on's own id.
func catalogRef(m stremio.MetaPreview, catType string, metadata bool, pluginID string) (collections.ExternalRef, bool) {
	typ := strings.ToLower(m.Type)
	if typ != stremio.TypeMovie && typ != stremio.TypeSeries {
		typ = catType
	}
	ref := collections.ExternalRef{Type: "movie"}
	if typ == stremio.TypeSeries {
		ref.Type = "show"
	}
	ref.IMDB, ref.TMDB, ref.TVDB = m.Ref()
	if metadata && m.ID != "" {
		ref.MetadataProvider = extend.PluginID(pluginID, "metadata")
		ref.ProviderID = m.ID
	}
	return ref, ref.MetadataProvider != "" || ref.IMDB != "" || ref.TMDB > 0 || ref.TVDB > 0
}

// healthFetcher reads through the plugin runtime, which records transport
// failures in the plugin's health itself; recorded tells the caller not
// to record that failure again.
type healthFetcher struct {
	inner    stremio.Fetcher
	recorded bool
}

func (f *healthFetcher) Fetch(ctx context.Context, route string, limit int64) ([]byte, error) {
	body, err := f.inner.Fetch(ctx, route, limit)
	var status *stremio.HTTPError
	if err != nil && !errors.As(err, &status) {
		f.recorded = true
	}
	return body, err
}

func (b *Bridge) rateLimitUntil(pluginID string) (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	until := b.pausedUntil[pluginID]
	if until.IsZero() || !time.Now().Before(until) {
		delete(b.pausedUntil, pluginID)
		return time.Time{}, false
	}
	return until, true
}

func (b *Bridge) recordRateLimit(pluginID, retryAfter string) {
	wait := parseRetryAfterDuration(retryAfter)
	until := time.Now().Add(wait)
	b.mu.Lock()
	if until.After(b.pausedUntil[pluginID]) {
		b.pausedUntil[pluginID] = until
	}
	b.mu.Unlock()
}

func parseRetryAfterDuration(raw string) time.Duration {
	const (
		def = time.Minute
		min = 15 * time.Second
		max = 30 * time.Minute
	)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		d := time.Duration(secs) * time.Second
		if d < min {
			return min
		}
		if d > max {
			return max
		}
		return d
	}
	if at, err := http.ParseTime(raw); err == nil {
		d := time.Until(at)
		if d < min {
			return min
		}
		if d > max {
			return max
		}
		return d
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// rowSourceKind adapts a plugin list source to a Home Designer row kind.
type rowSourceKind struct {
	list *listSourceKind
}

// Info describes the list as a Recommendations row source under "From
// plugins › <plugin>": catalogue titles of the list's type, and a Genre
// choice when the catalogue takes one. Its rows are served by
// collections.LayoutRow.
func (r *rowSourceKind) Info() core.RowSourceInfo {
	k := r.list
	info := core.RowSourceInfo{
		Kind:        k.ID(),
		Title:       k.label(),
		Description: resolvePlainText(k.mgr, k.pluginID, k.meta.Hint, ""),
		Group:       "plugins",
		Plugin:      firstNonEmpty(k.pluginName, k.pluginID),
		Needs:       []string{core.CapCatalogue},
		Split:       true,
		Params:      k.rowParams(),
	}
	switch k.catalog.Type {
	case stremio.TypeMovie:
		info.Needs = append(info.Needs, core.CapMovies)
	case stremio.TypeSeries:
		info.Needs = append(info.Needs, core.CapEpisodes)
	}
	return info
}

// rowable reports whether the list can fill a row by itself: every value
// it needs is one the owner picks from a list. Catalogues that need ids
// only a client has (calendarVideosIds, lastVideosIds) or a free-text
// value (search) are not offered.
func (k *listSourceKind) rowable() bool {
	if k.declared {
		for _, ex := range k.catalog.Extras() {
			switch ex.Name {
			case "calendarVideosIds", "lastVideosIds":
				return false
			}
			if ex.IsRequired && ex.Name != "skip" && len(ex.Options) == 0 {
				return false
			}
		}
		return true
	}
	for _, f := range k.fields {
		if f.Required && len(f.Options) == 0 {
			return false
		}
	}
	return true
}

// rowParams are the list's options as row parameters: a declared
// catalogue's extras with options (Genre), or the plugin's own fields
// with options. Free-text values are not row options.
func (k *listSourceKind) rowParams() []core.RowParam {
	var out []core.RowParam
	add := func(key, label string, required bool, def string, opts []core.ParamOption) {
		if key == "" || len(opts) == 0 {
			return
		}
		p := core.RowParam{Key: key, Label: label, Type: core.ParamChoice, Required: required, Default: def}
		if !required {
			p.Choices = append(p.Choices, core.ParamOption{Value: "", Label: "Any"})
		}
		p.Choices = append(p.Choices, opts...)
		out = append(out, p)
	}
	if k.declared {
		for _, ex := range k.catalog.Extras() {
			if ex.Name == "skip" || ex.Name == "search" || len(ex.Options) == 0 {
				continue
			}
			opts := make([]core.ParamOption, 0, len(ex.Options))
			for _, o := range ex.Options {
				if o = strings.TrimSpace(o); o != "" {
					opts = append(opts, core.ParamOption{Value: o, Label: o})
				}
			}
			label := strings.ToUpper(ex.Name[:1]) + ex.Name[1:]
			def := ""
			if ex.IsRequired && len(opts) > 0 {
				def = opts[0].Value
			}
			add(ex.Name, label, ex.IsRequired, def, opts)
		}
		return out
	}
	for _, f := range k.fields {
		key := firstNonEmpty(f.Key, f.Name)
		opts := make([]core.ParamOption, 0, len(f.Options))
		for _, o := range f.Options {
			if strings.TrimSpace(o.Value) != "" {
				opts = append(opts, core.ParamOption{Value: o.Value, Label: resolvePlainText(k.mgr, k.pluginID, o.Label, o.Value)})
			}
		}
		def, _ := f.Default.(string)
		label := resolvePlainText(k.mgr, k.pluginID, f.Label, key)
		add(key, label, f.Required, def, opts)
	}
	return out
}
