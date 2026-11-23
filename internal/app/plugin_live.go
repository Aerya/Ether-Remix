package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
)

const (
	pluginLiveCacheTTL     = 45 * time.Second
	pluginLiveCacheMax     = 256
	pluginLiveThumbTTL     = 6 * time.Hour
	pluginLiveThumbMax     = 8192
	pluginLiveDefaultLimit = 50
	pluginLiveMaxLimit     = 100
)

type pluginLiveCaller interface {
	Call(ctx context.Context, pluginID string, req plugins.Request) (plugins.Response, error)
}

type pluginLiveCacheKey struct {
	libID    core.LibraryID
	pluginID string
	kindKey  string
	tab      string
	query    string
	filter   string
	cursor   string
	limit    int
}

type pluginLiveCacheEntry struct {
	page      frontend.LibraryLivePage
	rawThumbs map[string]string // thumb hash -> raw upstream URL
	expires   time.Time
}

type pluginLiveThumbKey struct {
	libID core.LibraryID
	hash  string
}

type pluginLiveThumbEntry struct {
	url     string
	expires time.Time
}

// PluginLiveService mediates plugin library live-tab listings, uncached stream
// resolution, viewer/library access checks, and opaque thumbnail registration.
type PluginLiveService struct {
	app    *App
	caller pluginLiveCaller
	now    func() time.Time

	cacheMu sync.Mutex
	cache   map[pluginLiveCacheKey]pluginLiveCacheEntry
	order   []pluginLiveCacheKey

	thumbMu    sync.RWMutex
	thumbs     map[pluginLiveThumbKey]pluginLiveThumbEntry
	thumbOrder []pluginLiveThumbKey
}

// NewPluginLiveService builds a bounded live-tab coordinator bound to app.
func NewPluginLiveService(app *App) *PluginLiveService {
	return &PluginLiveService{
		app:    app,
		now:    time.Now,
		cache:  make(map[pluginLiveCacheKey]pluginLiveCacheEntry),
		thumbs: make(map[pluginLiveThumbKey]pluginLiveThumbEntry),
	}
}

func (a *App) pluginLiveService() *PluginLiveService {
	if a == nil {
		return nil
	}
	if a.pluginLive != nil {
		return a.pluginLive
	}
	a.pluginLiveOnce.Do(func() {
		if a.pluginLive == nil {
			a.pluginLive = NewPluginLiveService(a)
		}
	})
	return a.pluginLive
}

func (s *PluginLiveService) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *PluginLiveService) pluginCaller() pluginLiveCaller {
	if s == nil {
		return nil
	}
	if s.caller != nil {
		return s.caller
	}
	if s.app != nil && s.app.pluginsRT != nil && s.app.pluginsRT.mgr != nil {
		return s.app.pluginsRT.mgr
	}
	return nil
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func sanitizeStreamHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		if k == "" || strings.ContainsAny(k, "\r\n:") || strings.ContainsAny(v, "\r\n") {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RegisterThumb maps a plugin-provided live thumbnail URL to an opaque,
// library-scoped image reference ("library-live/<32-hex>") so raw upstream URLs
// never cross into Vela or console responses.
func (s *PluginLiveService) RegisterThumb(libID core.LibraryID, rawURL string) (ref string, hash string) {
	rawURL = strings.TrimSpace(rawURL)
	if s == nil || libID <= 0 || !validHTTPURL(rawURL) {
		return "", ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", libID, rawURL)))
	hash = hex.EncodeToString(sum[:16])
	key := pluginLiveThumbKey{libID: libID, hash: hash}
	now := s.clock()

	s.thumbMu.Lock()
	defer s.thumbMu.Unlock()
	if s.thumbs == nil {
		s.thumbs = make(map[pluginLiveThumbKey]pluginLiveThumbEntry)
	}
	if _, exists := s.thumbs[key]; !exists {
		if len(s.thumbs) >= pluginLiveThumbMax {
			s.evictThumbsLocked(now)
		}
		s.thumbOrder = append(s.thumbOrder, key)
	}
	s.thumbs[key] = pluginLiveThumbEntry{url: rawURL, expires: now.Add(pluginLiveThumbTTL)}
	return frontend.LibraryLiveThumbPrefix + hash, hash
}

func (s *PluginLiveService) evictThumbsLocked(now time.Time) {
	kept := s.thumbOrder[:0]
	for _, k := range s.thumbOrder {
		ent, ok := s.thumbs[k]
		if !ok || !now.Before(ent.expires) {
			delete(s.thumbs, k)
			continue
		}
		kept = append(kept, k)
	}
	s.thumbOrder = kept
	for len(s.thumbs) >= pluginLiveThumbMax && len(s.thumbOrder) > 0 {
		oldest := s.thumbOrder[0]
		s.thumbOrder = s.thumbOrder[1:]
		delete(s.thumbs, oldest)
	}
}

// ResolveThumb resolves a library-scoped thumb hash or "library-live/<hash>"
// path back to its registered upstream URL.
func (s *PluginLiveService) ResolveThumb(libID core.LibraryID, hashOrRef string) (string, error) {
	if s == nil || libID <= 0 {
		return "", core.ErrNotFound
	}
	hash := strings.TrimPrefix(strings.TrimSpace(hashOrRef), frontend.LibraryLiveThumbPrefix)
	if len(hash) != 32 {
		return "", core.ErrNotFound
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", core.ErrNotFound
	}
	now := s.clock()
	s.thumbMu.RLock()
	ent, ok := s.thumbs[pluginLiveThumbKey{libID: libID, hash: hash}]
	s.thumbMu.RUnlock()
	if !ok || !now.Before(ent.expires) {
		return "", core.ErrNotFound
	}
	return ent.url, nil
}

func (s *PluginLiveService) kindDecl(ctx context.Context, lib core.Library) (plugins.MediaKindMeta, bool) {
	if s == nil || s.app == nil || lib.Source != core.LibraryPlugin {
		return plugins.MediaKindMeta{}, false
	}
	if s.app.pluginsRT != nil && s.app.pluginsRT.pluginitems != nil {
		rec, err := s.app.pluginsRT.pluginitems.GetKindByLibrary(ctx, lib.ID)
		if err == nil {
			return rec.Decl, true
		}
	}
	return plugins.MediaKindMeta{}, false
}

// HasVisibleLiveTab reports whether lib declares a visible "live" tab (matching
// tabKey when non-empty) and returns the resolved tab key.
func (s *PluginLiveService) HasVisibleLiveTab(ctx context.Context, lib core.Library, tabKey string) (string, bool) {
	if lib.Source != core.LibraryPlugin {
		return "", false
	}
	tabKey = strings.TrimSpace(tabKey)
	if lib.TabsCustomized {
		for _, t := range lib.Tabs {
			if t.Hidden || t.Kind != core.TabLive {
				continue
			}
			if tabKey == "" || t.Key == tabKey {
				return t.Key, true
			}
		}
		return "", false
	}
	decl, ok := s.kindDecl(ctx, lib)
	if !ok {
		return "", false
	}
	for _, t := range decl.Library.EffectiveTabs() {
		if t.Kind != core.TabLive {
			continue
		}
		if tabKey == "" || t.Key == tabKey {
			return t.Key, true
		}
	}
	return "", false
}

// AllowViewer checks current viewer state, library visibility, active plugin
// state, protocol publication (from core.LiveApp(ctx)), and visible live tab.
func (s *PluginLiveService) AllowViewer(ctx context.Context, viewer *core.Viewer, libID core.LibraryID, tab string) (core.Library, string, error) {
	if s == nil || s.app == nil || s.app.libraries == nil {
		return core.Library{}, "", core.ErrNotAvailable
	}
	if viewer == nil {
		return core.Library{}, "", core.ErrUnauthorized
	}
	if s.app.users != nil && viewer.ID > 0 {
		fresh, err := s.app.users.ActiveUser(ctx, viewer.ID)
		if err != nil || fresh == nil {
			return core.Library{}, "", core.ErrUnauthorized
		}
		viewer = fresh
	}
	lib, err := s.app.libraries.Get(ctx, libID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return core.Library{}, "", core.ErrNotFound
		}
		return core.Library{}, "", err
	}
	if lib.Hidden || lib.Source != core.LibraryPlugin || !viewer.AllowsLibrary(lib.ID) {
		return core.Library{}, "", core.ErrNotFound
	}
	rootMT := core.TypeMovie
	if s.app.pluginsRT != nil && s.app.pluginsRT.kindCatalog != nil {
		if !s.app.pluginsRT.kindCatalog.IsPluginLibraryActive(ctx, lib) {
			return core.Library{}, "", core.ErrNotFound
		}
		rootMT = s.app.pluginsRT.kindCatalog.RootMediaType(ctx, lib)
	}
	if surface := core.LiveApp(ctx); surface != "" {
		if _, _, ok := presentLibraryForProtocol(lib.Present, frontend.Protocol(surface), rootMT); !ok {
			return core.Library{}, "", core.ErrNotFound
		}
	}
	resolvedTab, ok := s.HasVisibleLiveTab(ctx, lib, tab)
	if !ok {
		return core.Library{}, "", core.ErrNotFound
	}
	return lib, resolvedTab, nil
}

func cloneLivePage(in frontend.LibraryLivePage) frontend.LibraryLivePage {
	out := frontend.LibraryLivePage{
		Items:      make([]frontend.LibraryLiveItem, len(in.Items)),
		Filters:    make([]frontend.LibraryLiveFilter, len(in.Filters)),
		NextCursor: in.NextCursor,
	}
	for i, it := range in.Items {
		cp := it
		if it.Viewers != nil {
			v := *it.Viewers
			cp.Viewers = &v
		}
		if len(it.Tags) > 0 {
			cp.Tags = append([]string(nil), it.Tags...)
		}
		out.Items[i] = cp
	}
	copy(out.Filters, in.Filters)
	return out
}

// List queries a plugin library's live endpoint with a brief bounded TTL cache.
func (s *PluginLiveService) List(ctx context.Context, lib core.Library, req frontend.LibraryLiveRequest) (frontend.LibraryLivePage, error) {
	caller := s.pluginCaller()
	if caller == nil || lib.Source != core.LibraryPlugin || lib.PluginID == "" || lib.KindKey == "" {
		return frontend.LibraryLivePage{}, core.ErrNotAvailable
	}
	limit := req.Limit
	if limit <= 0 {
		limit = pluginLiveDefaultLimit
	}
	if limit > pluginLiveMaxLimit {
		limit = pluginLiveMaxLimit
	}
	tab := strings.TrimSpace(req.Tab)
	query := strings.TrimSpace(req.Query)
	filter := strings.TrimSpace(req.Filter)
	cursor := strings.TrimSpace(req.Cursor)

	cacheKey := pluginLiveCacheKey{
		libID:    lib.ID,
		pluginID: lib.PluginID,
		kindKey:  lib.KindKey,
		tab:      tab,
		query:    query,
		filter:   filter,
		cursor:   cursor,
		limit:    limit,
	}
	now := s.clock()
	s.cacheMu.Lock()
	if ent, ok := s.cache[cacheKey]; ok && now.Before(ent.expires) {
		page := cloneLivePage(ent.page)
		rawThumbs := ent.rawThumbs
		s.cacheMu.Unlock()
		for _, rawURL := range rawThumbs {
			s.RegisterThumb(lib.ID, rawURL)
		}
		return page, nil
	}
	s.cacheMu.Unlock()

	body, err := json.Marshal(plugins.KindLiveRequest{
		Tab:    tab,
		Q:      query,
		Filter: filter,
		Cursor: cursor,
		Limit:  limit,
	})
	if err != nil {
		return frontend.LibraryLivePage{}, err
	}
	resp, err := caller.Call(ctx, lib.PluginID, plugins.Request{
		PluginID: lib.PluginID,
		Method:   http.MethodPost,
		Path:     "/ether/v1/kinds/" + url.PathEscape(lib.KindKey) + "/live",
		Body:     body,
	})
	if err != nil {
		return frontend.LibraryLivePage{}, core.ErrNotAvailable
	}
	switch {
	case resp.Status == http.StatusNotFound:
		return frontend.LibraryLivePage{}, core.ErrNotFound
	case resp.Status < 200 || resp.Status >= 300:
		return frontend.LibraryLivePage{}, core.ErrNotAvailable
	}
	var wire plugins.KindLivePage
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &wire); err != nil {
			return frontend.LibraryLivePage{}, core.ErrNotAvailable
		}
	}
	page := frontend.LibraryLivePage{
		Items:      make([]frontend.LibraryLiveItem, 0, len(wire.Items)),
		Filters:    make([]frontend.LibraryLiveFilter, 0, len(wire.Filters)),
		NextCursor: strings.TrimSpace(wire.NextCursor),
	}
	rawThumbs := make(map[string]string, len(wire.Items))
	for _, it := range wire.Items {
		id := strings.TrimSpace(it.ID)
		title := strings.TrimSpace(it.Title)
		if id == "" || title == "" {
			continue
		}
		ref, h := s.RegisterThumb(lib.ID, it.Thumb)
		if h != "" {
			rawThumbs[h] = strings.TrimSpace(it.Thumb)
		}
		var viewers *int
		if it.Viewers != nil && *it.Viewers >= 0 {
			v := *it.Viewers
			viewers = &v
		}
		var tags []string
		for _, tag := range it.Tags {
			if trimmed := strings.TrimSpace(tag); trimmed != "" {
				tags = append(tags, trimmed)
			}
		}
		page.Items = append(page.Items, frontend.LibraryLiveItem{
			ID:       id,
			Title:    title,
			Subtitle: strings.TrimSpace(it.Subtitle),
			Badge:    strings.TrimSpace(it.Badge),
			Viewers:  viewers,
			Tags:     tags,
			ThumbRef: ref,
			Live:     it.IsLive(),
		})
	}
	for _, f := range wire.Filters {
		k := strings.TrimSpace(f.Key)
		lbl := strings.TrimSpace(f.Label)
		if k == "" || lbl == "" {
			continue
		}
		page.Filters = append(page.Filters, frontend.LibraryLiveFilter{Key: k, Label: lbl})
	}

	s.cacheMu.Lock()
	if s.cache == nil {
		s.cache = make(map[pluginLiveCacheKey]pluginLiveCacheEntry)
	}
	if _, exists := s.cache[cacheKey]; !exists {
		if len(s.cache) >= pluginLiveCacheMax {
			kept := s.order[:0]
			for _, k := range s.order {
				ent, ok := s.cache[k]
				if !ok || !now.Before(ent.expires) {
					delete(s.cache, k)
					continue
				}
				kept = append(kept, k)
			}
			s.order = kept
			for len(s.cache) >= pluginLiveCacheMax && len(s.order) > 0 {
				oldest := s.order[0]
				s.order = s.order[1:]
				delete(s.cache, oldest)
			}
		}
		s.order = append(s.order, cacheKey)
	}
	s.cache[cacheKey] = pluginLiveCacheEntry{
		page:      cloneLivePage(page),
		rawThumbs: rawThumbs,
		expires:   now.Add(pluginLiveCacheTTL),
	}
	s.cacheMu.Unlock()

	return page, nil
}

// Resolve resolves one live item to its fresh upstream stream URL. Never cached.
func (s *PluginLiveService) Resolve(ctx context.Context, lib core.Library, itemID string) (frontend.LibraryLiveStream, error) {
	caller := s.pluginCaller()
	itemID = strings.TrimSpace(itemID)
	if caller == nil || lib.Source != core.LibraryPlugin || lib.PluginID == "" || lib.KindKey == "" || itemID == "" {
		return frontend.LibraryLiveStream{}, core.ErrNotAvailable
	}
	body, err := json.Marshal(plugins.KindLiveResolveRequest{ID: itemID})
	if err != nil {
		return frontend.LibraryLiveStream{}, err
	}
	resp, err := caller.Call(ctx, lib.PluginID, plugins.Request{
		PluginID: lib.PluginID,
		Method:   http.MethodPost,
		Path:     "/ether/v1/kinds/" + url.PathEscape(lib.KindKey) + "/live/resolve",
		Body:     body,
	})
	if err != nil {
		return frontend.LibraryLiveStream{}, core.ErrNotAvailable
	}
	switch {
	case resp.Status == http.StatusNotFound:
		return frontend.LibraryLiveStream{}, core.ErrNotFound
	case resp.Status < 200 || resp.Status >= 300:
		return frontend.LibraryLiveStream{}, core.ErrNotAvailable
	}
	var wire plugins.KindLiveResolveResponse
	if err := json.Unmarshal(resp.Body, &wire); err != nil {
		return frontend.LibraryLiveStream{}, core.ErrNotAvailable
	}
	rawURL := strings.TrimSpace(wire.URL)
	if !validHTTPURL(rawURL) {
		return frontend.LibraryLiveStream{}, core.ErrNotAvailable
	}
	return frontend.LibraryLiveStream{
		URL:     rawURL,
		Headers: sanitizeStreamHeaders(wire.Headers),
		MIME:    strings.TrimSpace(wire.MIME),
		Title:   strings.TrimSpace(wire.Title),
	}, nil
}

type surfaceLibraryLive struct {
	app     *App
	surface string
}

var (
	_ frontend.LibraryLive                = surfaceLibraryLive{}
	_ frontend.LibraryLiveThumbAuthorizer = surfaceLibraryLive{}
)

func parseLibraryViewID(viewID string) (core.LibraryID, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(viewID), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return core.LibraryID(id), true
}

func (s surfaceLibraryLive) ListLive(ctx context.Context, viewer *core.Viewer, viewID string, req frontend.LibraryLiveRequest) (frontend.LibraryLivePage, error) {
	svc := s.app.pluginLiveService()
	if svc == nil {
		return frontend.LibraryLivePage{}, core.ErrNotAvailable
	}
	libID, ok := parseLibraryViewID(viewID)
	if !ok {
		return frontend.LibraryLivePage{}, core.ErrNotFound
	}
	lib, resolvedTab, err := svc.AllowViewer(core.WithLiveApp(ctx, s.surface), viewer, libID, req.Tab)
	if err != nil {
		return frontend.LibraryLivePage{}, err
	}
	req.Tab = resolvedTab
	return svc.List(ctx, lib, req)
}

func (s surfaceLibraryLive) ResolveLive(ctx context.Context, viewer *core.Viewer, viewID string, tab string, itemID string) (frontend.LibraryLiveStream, error) {
	svc := s.app.pluginLiveService()
	if svc == nil {
		return frontend.LibraryLiveStream{}, core.ErrNotAvailable
	}
	libID, ok := parseLibraryViewID(viewID)
	if !ok {
		return frontend.LibraryLiveStream{}, core.ErrNotFound
	}
	lib, _, err := svc.AllowViewer(core.WithLiveApp(ctx, s.surface), viewer, libID, tab)
	if err != nil {
		return frontend.LibraryLiveStream{}, err
	}
	return svc.Resolve(ctx, lib, itemID)
}

func (s surfaceLibraryLive) AuthorizeLibraryLiveThumb(ctx context.Context, viewer *core.Viewer, viewID string, path string) (string, error) {
	svc := s.app.pluginLiveService()
	if svc == nil {
		return "", core.ErrNotAvailable
	}
	libID, ok := parseLibraryViewID(viewID)
	if !ok {
		return "", core.ErrNotFound
	}
	surface := core.LiveApp(ctx)
	if surface == "" {
		surface = s.surface
	}
	if _, _, err := svc.AllowViewer(core.WithLiveApp(ctx, surface), viewer, libID, ""); err != nil {
		return "", err
	}
	return svc.ResolveThumb(libID, path)
}
