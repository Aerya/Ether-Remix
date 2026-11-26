package screens

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/screendoc"
)

// Document screens and surfaces (docs/plugin-screens.md): a plugin ships
// documents, Ether compiles them once per plugin version and serves them to
// the viewers and apps allowed to see them. DocRegistry is the in-memory
// index; it touches no database on reads.

// TextFunc localises one of a plugin's string keys for a locale.
type TextFunc func(locale, key string) (string, bool)

// DocRegistryOptions are the policy hooks the app supplies.
type DocRegistryOptions struct {
	// IsPublished reports the owner's per-app switch for one screen or
	// surface ("screen"/"surface", its plugin-local id). Nil uses the
	// manifest's default.
	IsPublished func(ctx context.Context, pluginID, kind, id, frontendID string, def bool) bool
	// IsViewerAllowed reports whether viewer may use pluginID at all
	// (enabled, not shadowed, granted_viewers).
	IsViewerAllowed func(ctx context.Context, viewer *core.User, pluginID string) bool
	// RatingAllowed reports whether a declared content rating is within the
	// viewer's limit. Nil allows every rating.
	RatingAllowed func(viewer *core.User, rating string) bool
	// OverlayOrder returns the owner's order of player overlays by wire id
	// for one app; unlisted overlays follow in install order.
	OverlayOrder func(ctx context.Context, frontendID string) []string
}

type docPlugin struct {
	pluginID    string
	version     string
	enabled     bool
	shadow      bool
	screensOff  bool
	liveOff     bool
	installedAt int64
	feeds       []string
	contrib     plugins.Contributes
	docs        plugins.ScreenDocuments
	text        TextFunc
	fingerprint string
	// draft marks a Studio preview: never cached.
	draft bool
}

// DocRegistry indexes every installed plugin's document screens and surfaces.
type DocRegistry struct {
	opts DocRegistryOptions
	mu   sync.RWMutex
	byID map[string]*docPlugin
	gen  atomic.Uint64

	servedMu sync.Mutex
	served   map[string][]byte

	previews previews
}

// NewDocRegistry returns an empty registry.
func NewDocRegistry(opts DocRegistryOptions) *DocRegistry {
	return &DocRegistry{opts: opts, byID: map[string]*docPlugin{}, served: map[string][]byte{}}
}

// Generation changes whenever a plugin's screens, surfaces or state change.
func (r *DocRegistry) Generation() uint64 {
	if r == nil {
		return 0
	}
	return r.gen.Load()
}

// Bump invalidates navigation and bootstrap caches after an owner switch or
// overlay order change.
func (r *DocRegistry) Bump() {
	if r != nil {
		r.gen.Add(1)
	}
}

// Sync compiles rec's documents from its package files and indexes them. A
// plugin with no document screens or surfaces is removed.
func (r *DocRegistry) Sync(rec plugins.Record, files map[string][]byte, text TextFunc) error {
	if r == nil || rec.ID == "" {
		return nil
	}
	if !plugins.ScreensUseDocuments(rec.Manifest) {
		r.Remove(rec.ID)
		return nil
	}
	var english func(string) (string, bool)
	if text != nil {
		english = func(k string) (string, bool) { return text("en", k) }
	}
	docs, err := plugins.CompileScreenDocuments(rec.Manifest, files, english)
	if err != nil {
		r.Remove(rec.ID)
		return err
	}
	e := &docPlugin{
		pluginID: rec.ID,
		version:  rec.Version,
		enabled:  rec.Enabled,
		shadow:   rec.Shadow,
		contrib:  rec.Manifest.Ether.Contributes,
		docs:     docs,
		text:     text,
	}
	if !rec.InstalledAt.IsZero() {
		e.installedAt = rec.InstalledAt.UnixNano()
	}
	for _, role := range rec.RolesOff {
		switch role {
		case plugins.CapScreens:
			e.screensOff = true
		case plugins.CapLiveEvents:
			e.liveOff = true
		}
	}
	for _, f := range e.contrib.LiveFeeds {
		e.feeds = append(e.feeds, f.ID)
	}
	e.fingerprint = docFingerprint(e)
	r.mu.Lock()
	prev := r.byID[rec.ID]
	r.byID[rec.ID] = e
	r.mu.Unlock()
	if prev == nil || prev.fingerprint != e.fingerprint {
		r.forgetServed(rec.ID)
		r.gen.Add(1)
	}
	return nil
}

// Remove drops a plugin.
func (r *DocRegistry) Remove(pluginID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	_, ok := r.byID[pluginID]
	delete(r.byID, pluginID)
	r.mu.Unlock()
	if ok {
		r.forgetServed(pluginID)
		r.gen.Add(1)
	}
}

func docFingerprint(e *docPlugin) string {
	h := sha256.New()
	write := func(s string) { h.Write([]byte(s)); h.Write([]byte{0}) }
	write(e.version)
	for _, b := range []bool{e.enabled, e.shadow, e.screensOff, e.liveOff} {
		if b {
			write("1")
		} else {
			write("0")
		}
	}
	ids := make([]string, 0)
	for id, pages := range e.docs.Screens {
		for page, vs := range pages {
			for _, d := range vs {
				ids = append(ids, "s:"+id+"/"+page+"/"+d.Hash)
			}
		}
	}
	for id, vs := range e.docs.Surfaces {
		for _, d := range vs {
			ids = append(ids, "f:"+id+"/"+d.Hash)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		write(id)
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

func (e *docPlugin) active() bool {
	return e.enabled && !e.shadow && !e.screensOff
}

func (r *DocRegistry) snapshot(ctx context.Context) []*docPlugin {
	draft := r.previewFor(ctx)
	r.mu.RLock()
	out := make([]*docPlugin, 0, len(r.byID)+1)
	for id, e := range r.byID {
		if draft != nil && id == draft.pluginID {
			continue
		}
		out = append(out, e)
	}
	r.mu.RUnlock()
	if draft != nil {
		out = append(out, draft)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].installedAt != out[j].installedAt {
			return out[i].installedAt < out[j].installedAt
		}
		return out[i].pluginID < out[j].pluginID
	})
	return out
}

// pickVariant returns the highest-renderer variant the client draws.
func pickVariant(docs []*screendoc.Compiled, renderer int) *screendoc.Compiled {
	for _, d := range docs {
		if d.Renderer <= renderer {
			return d
		}
	}
	return nil
}

// ScreenTarget is one document screen a viewer may open.
type ScreenTarget struct {
	WireID   string
	PluginID string
	Meta     plugins.ScreenMeta
	plugin   *docPlugin
}

// SurfaceTarget is one surface a viewer's app may show.
type SurfaceTarget struct {
	WireID   string
	PluginID string
	Meta     plugins.SurfaceMeta
	plugin   *docPlugin
}

func (r *DocRegistry) viewerAllows(ctx context.Context, viewer *core.User, e *docPlugin, rating string) bool {
	if viewer == nil || !e.active() {
		return false
	}
	if r.opts.IsViewerAllowed != nil && !r.opts.IsViewerAllowed(ctx, viewer, e.pluginID) {
		return false
	}
	if rating != "" && r.opts.RatingAllowed != nil && !r.opts.RatingAllowed(viewer, rating) {
		return false
	}
	return true
}

func (r *DocRegistry) published(ctx context.Context, e *docPlugin, kind, id string, frontends []string, def *bool, frontendID string) bool {
	if len(frontends) == 0 {
		if frontendID != "" && frontendID != "vela" {
			return false
		}
	} else if frontendID != "" && !containsString(frontends, frontendID) {
		return false
	}
	d := def == nil || *def
	if r.opts.IsPublished != nil {
		return r.opts.IsPublished(ctx, e.pluginID, kind, id, frontendID, d)
	}
	return d
}

// usesLive reports whether any document of the screen reads a live feed, so
// switching off the plugin's live_events role hides it.
func usesLive(pages map[string][]*screendoc.Compiled) bool {
	for _, vs := range pages {
		for _, d := range vs {
			for _, s := range d.Sources {
				if strings.HasPrefix(s.From, "live") || s.From == screendoc.FromWatch {
					return true
				}
			}
		}
	}
	return false
}

// Screens lists the document screens viewer may open in frontendID.
func (r *DocRegistry) Screens(ctx context.Context, viewer *core.User, frontendID string) []ScreenTarget {
	if r == nil {
		return nil
	}
	var out []ScreenTarget
	for _, e := range r.snapshot(ctx) {
		for _, sc := range e.contrib.Screens {
			pages, ok := e.docs.Screens[sc.ID]
			if !ok || len(pages) == 0 {
				continue
			}
			if e.liveOff && usesLive(pages) {
				continue
			}
			if !r.published(ctx, e, "screen", sc.ID, sc.Frontends, sc.Default, frontendID) || !r.viewerAllows(ctx, viewer, e, sc.ContentRating) {
				continue
			}
			out = append(out, ScreenTarget{WireID: sc.ID, PluginID: e.pluginID, Meta: sc, plugin: e})
		}
	}
	seen := map[string]bool{}
	for i := range out {
		if seen[out[i].WireID] {
			out[i].WireID = out[i].PluginID + ":" + out[i].Meta.ID
		}
		seen[out[i].WireID] = true
	}
	return out
}

// FindScreen resolves a wire id (bare or plugin-qualified).
func (r *DocRegistry) FindScreen(ctx context.Context, viewer *core.User, frontendID, id string) (ScreenTarget, bool) {
	for _, t := range r.Screens(ctx, viewer, frontendID) {
		if t.WireID == id || t.PluginID+":"+t.Meta.ID == id {
			return t, true
		}
	}
	return ScreenTarget{}, false
}

// pluginShort is the last segment of a plugin id ("org.ether.sports" ->
// "sports"), the prefix of surface wire ids.
func pluginShort(pluginID string) string {
	if i := strings.LastIndex(pluginID, "."); i >= 0 && i < len(pluginID)-1 {
		return pluginID[i+1:]
	}
	return pluginID
}

// MaxOverlaysPerAnchor bounds the player overlays sharing one anchor across
// every plugin.
const MaxOverlaysPerAnchor = 2

// Surfaces lists the surfaces viewer's app may show, capped and ordered: at
// most two player overlays per anchor, in the owner's order.
func (r *DocRegistry) Surfaces(ctx context.Context, viewer *core.User, frontendID string) []SurfaceTarget {
	if r == nil {
		return nil
	}
	var all []SurfaceTarget
	for _, e := range r.snapshot(ctx) {
		for _, sf := range e.contrib.Surfaces {
			docs := e.docs.Surfaces[sf.ID]
			if len(docs) == 0 {
				continue
			}
			if e.liveOff && usesLive(map[string][]*screendoc.Compiled{"": docs}) {
				continue
			}
			if !r.published(ctx, e, "surface", sf.ID, sf.Frontends, sf.Default, frontendID) || !r.viewerAllows(ctx, viewer, e, sf.ContentRating) {
				continue
			}
			all = append(all, SurfaceTarget{WireID: pluginShort(e.pluginID) + "." + sf.ID, PluginID: e.pluginID, Meta: sf, plugin: e})
		}
	}
	seen := map[string]bool{}
	for i := range all {
		if seen[all[i].WireID] {
			all[i].WireID = all[i].PluginID + "." + all[i].Meta.ID
		}
		seen[all[i].WireID] = true
	}
	if r.opts.OverlayOrder != nil {
		rank := map[string]int{}
		for i, id := range r.opts.OverlayOrder(ctx, frontendID) {
			rank[id] = i + 1
		}
		sort.SliceStable(all, func(i, j int) bool {
			ri, rj := rank[all[i].WireID], rank[all[j].WireID]
			if ri == 0 || rj == 0 {
				return ri != 0 && rj == 0
			}
			return ri < rj
		})
	}
	perAnchor := map[string]int{}
	out := all[:0]
	for _, t := range all {
		if t.Meta.Presentation == screendoc.PresentPlayerOverlay {
			if perAnchor[t.Meta.Anchor] >= MaxOverlaysPerAnchor {
				continue
			}
			perAnchor[t.Meta.Anchor]++
		}
		out = append(out, t)
	}
	return out
}

// HomeRowSurfaces lists the homeRow surfaces published for Vela, whatever
// the viewer: the sources Recommendations pages can place as rows. Apps gate
// each viewer and client when they serve the row.
func (r *DocRegistry) HomeRowSurfaces(ctx context.Context) []SurfaceTarget {
	if r == nil {
		return nil
	}
	var out []SurfaceTarget
	seen := map[string]bool{}
	for _, e := range r.snapshot(ctx) {
		if e.draft || !e.active() {
			continue
		}
		for _, sf := range e.contrib.Surfaces {
			if sf.Presentation != screendoc.PresentHomeRow || len(e.docs.Surfaces[sf.ID]) == 0 {
				continue
			}
			if !r.published(ctx, e, "surface", sf.ID, sf.Frontends, sf.Default, "vela") {
				continue
			}
			sf.Title = e.localise("", sf.Title)
			t := SurfaceTarget{WireID: pluginShort(e.pluginID) + "." + sf.ID, PluginID: e.pluginID, Meta: sf, plugin: e}
			if seen[t.WireID] {
				t.WireID = t.PluginID + "." + sf.ID
			}
			seen[t.WireID] = true
			out = append(out, t)
		}
	}
	return out
}

// FindSurface resolves a surface wire id.
func (r *DocRegistry) FindSurface(ctx context.Context, viewer *core.User, frontendID, id string) (SurfaceTarget, bool) {
	for _, t := range r.Surfaces(ctx, viewer, frontendID) {
		if t.WireID == id {
			return t, true
		}
	}
	return SurfaceTarget{}, false
}

// ErrNoVariant reports that no variant of a document fits the client.
var ErrNoVariant = errors.New("screens: no document variant fits this app version")

func (r *DocRegistry) forgetServed(pluginID string) {
	r.servedMu.Lock()
	for k := range r.served {
		if strings.HasPrefix(k, pluginID+"\x00") {
			delete(r.served, k)
		}
	}
	r.servedMu.Unlock()
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
