package screens

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/favorites"
	"github.com/braedonsaunders/ether/internal/plugins"
)

const liveEventsCapability = plugins.CapLiveEvents

// RegistryOptions configures optional policy/publishing hooks on Registry.
type RegistryOptions struct {
	// IsPublished optionally overrides whether a plugin screen is published to
	// frontendID (reusing the plugin's existing frontend publishing state).
	// Nil defaults to published when the plugin is enabled, active, and lists
	// frontendID (or omits frontends, which defaults to Vela).
	IsPublished func(ctx context.Context, pluginID, screenID, frontendID string) bool
	// IsViewerAllowed optionally adds viewer-level restrictions for a plugin
	// screen or entity (for example granted_viewers checks).
	IsViewerAllowed func(ctx context.Context, viewer *core.User, pluginID, screenID string) bool
}

type pluginEntry struct {
	pluginID       string
	enabled        bool
	shadow         bool
	liveEventsOff  bool
	grantedViewers bool
	defaultFeed    string
	entityKinds    map[string]string // feed -> default entity kind
	feeds          []string
	screens        []storedScreen
	fingerprint    string
}

type storedScreen struct {
	meta        plugins.ScreenMeta
	title       string
	defaultFeed string
	entityKind  string
	sections    []plugins.ScreenSection
}

// Registry maintains an in-memory index of installed plugin screens and feeds,
// updated lazily via plugin lifecycle hooks (OnSync / OnRemove). Reads are O(N)
// in the number of plugins declaring screens and touch neither the database
// nor the media catalogue.
type Registry struct {
	mu   sync.RWMutex
	opts RegistryOptions
	byID map[string]pluginEntry
	gen  atomic.Uint64
}

// NewRegistry returns an empty screen registry.
func NewRegistry(opts RegistryOptions) *Registry {
	return &Registry{
		opts: opts,
		byID: make(map[string]pluginEntry),
	}
}

// Generation advances whenever screen declarations, enabled state, or roles
// change so frontend bootstrap caches invalidate promptly.
func (r *Registry) Generation() uint64 {
	if r == nil {
		return 0
	}
	return r.gen.Load()
}

// Sync updates the registry entry for one plugin record. resolveText resolves
// "$t:<key>" strings for the plugin and may be nil.
func (r *Registry) Sync(rec plugins.Record, resolveText func(string) string) {
	if r == nil || rec.ID == "" {
		return
	}
	if rec.Manifest.Ether == nil {
		r.Remove(rec.ID)
		return
	}

	feeds, entityKinds := extractManifestFeeds(rec.Manifest.Ether.Contributes.LiveFeeds, rec.ManifestRaw)
	defaultFeed := ""
	if len(feeds) == 1 {
		defaultFeed = feeds[0]
	}

	declared := rec.Manifest.Ether.Contributes.Screens
	if len(declared) == 0 && len(feeds) == 0 {
		r.Remove(rec.ID)
		return
	}

	resolve := func(s string) string {
		if resolveText != nil && strings.HasPrefix(s, "$t:") {
			if out := strings.TrimSpace(resolveText(strings.TrimPrefix(s, "$t:"))); out != "" {
				return out
			}
		}
		return s
	}

	stored := make([]storedScreen, 0, len(declared))
	feedSet := map[string]bool{}
	for _, f := range feeds {
		feedSet[f] = true
	}
	for _, sc := range declared {
		scFeed := sc.Feed
		if scFeed == "" {
			scFeed = defaultFeed
		}
		sections := make([]plugins.ScreenSection, len(sc.Sections))
		for i, sec := range sc.Sections {
			cp := sec
			cp.Title = resolve(sec.Title)
			if cp.Query != nil {
				qcp := *cp.Query
				qcp.State = slices.Clone(cp.Query.State)
				cp.Query = &qcp
			}
			cp.Leagues = slices.Clone(sec.Leagues)
			sections[i] = cp
			if f := sectionFeedHint(cp, scFeed); f != "" && !feedSet[f] {
				feedSet[f] = true
				feeds = append(feeds, f)
			}
			if cp.Feed != "" && cp.EntityKind != "" && entityKinds[cp.Feed] == "" {
				entityKinds[cp.Feed] = cp.EntityKind
			}
		}
		if scFeed == "" && len(feeds) == 1 {
			scFeed = feeds[0]
		}
		entKind := entityKinds[scFeed]
		if entKind == "" {
			entKind = "team"
		}
		stored = append(stored, storedScreen{
			meta:        sc,
			title:       resolve(sc.Title),
			defaultFeed: scFeed,
			entityKind:  entKind,
			sections:    sections,
		})
	}
	sort.Strings(feeds)

	liveOff := false
	for _, role := range rec.RolesOff {
		if role == liveEventsCapability {
			liveOff = true
			break
		}
	}

	entry := pluginEntry{
		pluginID:       rec.ID,
		enabled:        rec.Enabled,
		shadow:         rec.Shadow,
		liveEventsOff:  liveOff,
		grantedViewers: rec.Manifest.Ether.Permissions.Viewers.Identity == "granted_viewers",
		defaultFeed:    defaultFeed,
		entityKinds:    entityKinds,
		feeds:          feeds,
		screens:        stored,
	}
	entry.fingerprint = entryFingerprint(entry)

	r.mu.Lock()
	prev, existed := r.byID[rec.ID]
	changed := !existed || prev.fingerprint != entry.fingerprint
	r.byID[rec.ID] = entry
	r.mu.Unlock()
	if changed {
		r.gen.Add(1)
	}
}

// Remove deletes a plugin from the screen registry.
func (r *Registry) Remove(pluginID string) {
	if r == nil || pluginID == "" {
		return
	}
	r.mu.Lock()
	_, existed := r.byID[pluginID]
	delete(r.byID, pluginID)
	r.mu.Unlock()
	if existed {
		r.gen.Add(1)
	}
}

// ListScreens returns all screens published to frontendID that viewer may use,
// in deterministic order with collision-safe wire IDs.
func (r *Registry) ListScreens(ctx context.Context, viewer *core.User, frontendID string) ([]DeclaredScreen, error) {
	if r == nil || viewer == nil {
		return nil, nil
	}
	entries := r.snapshot()
	var visible []DeclaredScreen
	counts := map[string]int{}
	for _, entry := range entries {
		if !entry.active() {
			continue
		}
		for _, sc := range entry.screens {
			if !r.screenPublished(ctx, entry, sc.meta, frontendID) {
				continue
			}
			if !r.viewerAllowed(ctx, viewer, entry, sc.meta.ID) {
				continue
			}
			counts[sc.meta.ID]++
			visible = append(visible, DeclaredScreen{
				ID:          sc.meta.ID,
				ScreenID:    sc.meta.ID,
				PluginID:    entry.pluginID,
				Title:       sc.title,
				Icon:        sc.meta.Icon,
				DefaultFeed: sc.defaultFeed,
				EntityKind:  sc.entityKind,
				Sections:    cloneSections(sc.sections),
			})
		}
	}
	// If two plugins publish the same screen id, keep the first bare id and
	// qualify subsequent collisions as "pluginID:screenID" so neither disappears.
	seen := map[string]bool{}
	for i := range visible {
		id := visible[i].ScreenID
		if seen[id] {
			visible[i].ID = visible[i].PluginID + ":" + id
		}
		seen[id] = true
	}
	return visible, nil
}

// FindScreen locates one published, viewer-permitted screen by wire ID, bare
// screen ID, or qualified "pluginID:screenID".
func (r *Registry) FindScreen(ctx context.Context, viewer *core.User, frontendID, id string) (DeclaredScreen, bool, error) {
	if r == nil || viewer == nil || id == "" {
		return DeclaredScreen{}, false, nil
	}
	all, err := r.ListScreens(ctx, viewer, frontendID)
	if err != nil {
		return DeclaredScreen{}, false, err
	}
	for _, sc := range all {
		if sc.ID == id || sc.PluginID+":"+sc.ScreenID == id {
			return sc, true, nil
		}
	}
	return DeclaredScreen{}, false, nil
}

// ResolveFeed maps a client feed parameter ("sports" or "pluginID:sports") to
// an active, viewer-allowed plugin and feed ID.
func (r *Registry) ResolveFeed(ctx context.Context, viewer *core.User, frontendID, feedRef string) (pluginID, feedID, defaultEntityKind string, ok bool) {
	if r == nil || viewer == nil {
		return "", "", "", false
	}
	feedRef = strings.TrimSpace(feedRef)
	if feedRef == "" {
		return "", "", "", false
	}
	wantPlugin, wantFeed, qualified := strings.Cut(feedRef, ":")
	if !qualified {
		wantPlugin, wantFeed = "", feedRef
	}
	for _, entry := range r.snapshot() {
		if !entry.active() {
			continue
		}
		if wantPlugin != "" && entry.pluginID != wantPlugin {
			continue
		}
		if !slices.Contains(entry.feeds, wantFeed) {
			continue
		}
		if !r.feedPublished(ctx, entry, wantFeed, frontendID) {
			continue
		}
		if !r.viewerAllowed(ctx, viewer, entry, "") {
			continue
		}
		kind := entry.entityKinds[wantFeed]
		if kind == "" {
			kind = "team"
		}
		return entry.pluginID, wantFeed, kind, true
	}
	return "", "", "", false
}

// EntityAllowed checks in memory whether viewer may favorite or read the given
// canonical entity key ("pluginID:feed:entityKind:id").
func (r *Registry) EntityAllowed(ctx context.Context, viewer *core.User, rawKey string) bool {
	if r == nil || viewer == nil {
		return false
	}
	key, ok := favorites.ParseEntityKey(rawKey)
	if !ok {
		return false
	}
	r.mu.RLock()
	entry, exists := r.byID[key.PluginID]
	r.mu.RUnlock()
	if !exists || !entry.active() {
		return false
	}
	if len(entry.feeds) > 0 && !slices.Contains(entry.feeds, key.Feed) {
		return false
	}
	return r.viewerAllowed(ctx, viewer, entry, "")
}

func (e pluginEntry) active() bool {
	return e.enabled && !e.shadow && !e.liveEventsOff
}

func (r *Registry) snapshot() []pluginEntry {
	r.mu.RLock()
	out := make([]pluginEntry, 0, len(r.byID))
	for _, e := range r.byID {
		out = append(out, e)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].pluginID < out[j].pluginID })
	return out
}

func (r *Registry) screenPublished(ctx context.Context, entry pluginEntry, sc plugins.ScreenMeta, frontendID string) bool {
	if len(sc.Frontends) == 0 {
		if frontendID != "" && frontendID != "vela" {
			return false
		}
	} else if frontendID != "" && !slices.Contains(sc.Frontends, frontendID) {
		return false
	}
	if r.opts.IsPublished != nil {
		return r.opts.IsPublished(ctx, entry.pluginID, sc.ID, frontendID)
	}
	return true
}

func (r *Registry) feedPublished(ctx context.Context, entry pluginEntry, feedID, frontendID string) bool {
	if len(entry.screens) == 0 {
		if r.opts.IsPublished != nil {
			return r.opts.IsPublished(ctx, entry.pluginID, "", frontendID)
		}
		return frontendID == "" || frontendID == "vela"
	}
	for _, sc := range entry.screens {
		if !r.screenPublished(ctx, entry, sc.meta, frontendID) {
			continue
		}
		if sc.defaultFeed == feedID || sc.meta.Feed == feedID {
			return true
		}
		for _, sec := range sc.sections {
			if sectionFeedHint(sec, sc.defaultFeed) == feedID {
				return true
			}
		}
	}
	return false
}

func (r *Registry) viewerAllowed(ctx context.Context, viewer *core.User, entry pluginEntry, screenID string) bool {
	if viewer == nil {
		return false
	}
	if r.opts.IsViewerAllowed != nil {
		return r.opts.IsViewerAllowed(ctx, viewer, entry.pluginID, screenID)
	}
	return true
}

func sectionFeedHint(sec plugins.ScreenSection, fallback string) string {
	if sec.Feed != "" {
		return sec.Feed
	}
	if sec.Query != nil && sec.Query.Feed != "" {
		return sec.Query.Feed
	}
	return fallback
}

// extractManifestFeeds reads contributes.liveFeeds from the parsed manifest,
// falling back to raw manifest JSON when a caller only populated ManifestRaw.
func extractManifestFeeds(parsed []plugins.LiveFeedMeta, raw json.RawMessage) ([]string, map[string]string) {
	entityKinds := map[string]string{}
	if len(parsed) > 0 {
		var feeds []string
		seen := map[string]bool{}
		for _, lf := range parsed {
			id := strings.TrimSpace(lf.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			feeds = append(feeds, id)
			if len(lf.EntityKinds) > 0 && strings.TrimSpace(lf.EntityKinds[0]) != "" {
				entityKinds[id] = strings.TrimSpace(lf.EntityKinds[0])
			}
		}
		return feeds, entityKinds
	}
	if len(raw) == 0 {
		return nil, entityKinds
	}
	var doc struct {
		Ether *struct {
			Contributes struct {
				LiveFeeds []struct {
					ID          string   `json:"id"`
					EntityKinds []string `json:"entityKinds"`
				} `json:"liveFeeds"`
			} `json:"contributes"`
		} `json:"ether"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Ether == nil {
		return nil, entityKinds
	}
	var feeds []string
	seen := map[string]bool{}
	for _, lf := range doc.Ether.Contributes.LiveFeeds {
		id := strings.TrimSpace(lf.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		feeds = append(feeds, id)
		if len(lf.EntityKinds) > 0 && strings.TrimSpace(lf.EntityKinds[0]) != "" {
			entityKinds[id] = strings.TrimSpace(lf.EntityKinds[0])
		}
	}
	return feeds, entityKinds
}

func cloneSections(in []plugins.ScreenSection) []plugins.ScreenSection {
	out := make([]plugins.ScreenSection, len(in))
	for i, s := range in {
		cp := s
		if s.Query != nil {
			q := *s.Query
			q.State = slices.Clone(s.Query.State)
			cp.Query = &q
		}
		cp.Leagues = slices.Clone(s.Leagues)
		out[i] = cp
	}
	return out
}

func entryFingerprint(e pluginEntry) string {
	b, _ := json.Marshal(struct {
		PluginID       string
		Enabled        bool
		Shadow         bool
		LiveEventsOff  bool
		GrantedViewers bool
		Feeds          []string
		EntityKinds    map[string]string
		Screens        []plugins.ScreenMeta
		Titles         []string
	}{
		PluginID:       e.pluginID,
		Enabled:        e.enabled,
		Shadow:         e.shadow,
		LiveEventsOff:  e.liveEventsOff,
		GrantedViewers: e.grantedViewers,
		Feeds:          e.feeds,
		EntityKinds:    e.entityKinds,
		Screens: func() []plugins.ScreenMeta {
			out := make([]plugins.ScreenMeta, len(e.screens))
			for i, s := range e.screens {
				out[i] = s.meta
			}
			return out
		}(),
		Titles: func() []string {
			out := make([]string, len(e.screens))
			for i, s := range e.screens {
				out[i] = s.title
			}
			return out
		}(),
	})
	return string(b)
}
