package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/llm"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Safe per-plugin bounds for Host.AIComplete (#669, #702). No owner settings are
// exposed for these bounds; they protect the owner's configured language model
// and memory budget with predictable defaults.
const (
	// DefaultAICompletePerMin caps completions per plugin in a sliding minute.
	DefaultAICompletePerMin = 20
	// DefaultAISearchCompletePerMin caps uncached interactive search completions
	// per (plugin, viewer) in a sliding minute, separate from the plugin's
	// background completion budget (#702).
	DefaultAISearchCompletePerMin = 10
	// MaxAIMessages caps the chat message count in a single completion request.
	MaxAIMessages = 20
	// MaxAIInputBytes caps total UTF-8 message content bytes per request.
	MaxAIInputBytes = 32 * 1024
	// MaxAISchemaBytes caps the optional JSON Schema document size.
	MaxAISchemaBytes = 32 * 1024
	// DefaultAIMaxTokens is the reply token budget when maxTokens is omitted.
	DefaultAIMaxTokens = 1024
	// MaxAIMaxTokens is the hard ceiling on maxTokens per completion.
	MaxAIMaxTokens = 4096
	// DefaultAITimeout bounds one background/action completion when timeoutMs is omitted.
	DefaultAITimeout = 30 * time.Second
	// MaxAITimeout is the hard ceiling on timeoutMs for background/action completions.
	MaxAITimeout = 30 * time.Second
	// DefaultAISearchTimeout bounds one completion on an interactive search route
	// when timeoutMs is omitted (#702). Matches Ether's own descriptive search
	// wait bound (experience.DescriptiveSearchWaitBound).
	DefaultAISearchTimeout = 20 * time.Second
	// MaxAISearchTimeout is the hard per-request ceiling on timeoutMs for
	// interactive search completions (#702), never exceeding Ether's 20 s
	// descriptive search wait bound.
	MaxAISearchTimeout = 20 * time.Second
	// AISearchRouteDeadline gives an opted-in catalog/kind search route room
	// for MaxAISearchTimeout plus fallback serialization after a timeout.
	AISearchRouteDeadline = MaxAISearchTimeout + 2*time.Second
	// MaxAISearchQueryBytes bounds the search query string used for interactive
	// search completions and per-query caching (matches Ether's descriptive search).
	MaxAISearchQueryBytes = 1000
	// DefaultAISearchCacheTTL bounds how long a search completion stays cached
	// per normalized query before re-asking the model (matches Ether's search cache).
	DefaultAISearchCacheTTL = 10 * time.Minute
	// MaxAISearchCacheEntries caps cached search completions per plugin.
	MaxAISearchCacheEntries = 256
	// MaxAdapterSearchPaths caps declared searchPaths entries per frontend adapter.
	MaxAdapterSearchPaths = 16
	// MaxAIResponseBytes caps the model reply text / validated JSON returned
	// to the plugin.
	MaxAIResponseBytes = 64 * 1024
)

type callPathKey struct{}
type callSearchQueryKey struct{}

// withCallPath records the active plugin route path on ctx so host calls can
// enforce route-class policies (for example blocking ai.complete on client
// request paths).
func withCallPath(ctx context.Context, path string) context.Context {
	if path == "" {
		return ctx
	}
	return context.WithValue(ctx, callPathKey{}, path)
}

func withCallSearchQuery(ctx context.Context, query string) context.Context {
	q := strings.TrimSpace(query)
	if q == "" {
		return ctx
	}
	return context.WithValue(ctx, callSearchQueryKey{}, q)
}

// WithCallContext attaches the active plugin route path and any host-extracted
// search query to ctx (used by remote host callbacks authenticated with a
// host-minted X-Ether-Viewer-Token).
func WithCallContext(ctx context.Context, path, searchQuery string) context.Context {
	ctx = withCallPath(ctx, path)
	return withCallSearchQuery(ctx, searchQuery)
}

func callPathFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if p, _ := ctx.Value(callPathKey{}).(string); p != "" {
		return p
	}
	return wasm.CallPath(ctx)
}

func callSearchQueryFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	q, _ := ctx.Value(callSearchQueryKey{}).(string)
	return strings.TrimSpace(q)
}

// AIPermissionAllowsComplete reports whether an ether.permissions.ai level
// grants base ai.complete access ("complete" or "complete+search").
func AIPermissionAllowsComplete(level string) bool {
	l := normLevel(strings.TrimSpace(level))
	return l == "complete" || l == "complete+search"
}

// AIPermissionAllowsSearch reports whether an ether.permissions.ai level
// opts in to bounded, cached ai.complete on interactive search routes (#702).
func AIPermissionAllowsSearch(level string) bool {
	return normLevel(strings.TrimSpace(level)) == "complete+search"
}

// NormalizeAISearchQuery normalizes a viewer search query for per-query
// caching (lowercased, runs of whitespace collapsed to a single space).
func NormalizeAISearchQuery(q string) string {
	return strings.ToLower(strings.Join(strings.Fields(q), " "))
}

func normalizeAdapterSearchPath(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || !strings.HasPrefix(s, "/") {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x20 || c == 0x7f || c == '?' || c == '#' || c == '\\' {
			return "", false
		}
	}
	if strings.Contains(s, "..") || strings.Contains(s, "//") {
		return "", false
	}
	if len(s) > 1 {
		s = strings.TrimRight(s, "/")
		if s == "" {
			s = "/"
		}
	}
	return s, true
}

// isClientRequestPath reports whether p is an interactive viewer, client
// protocol adapter, or console UI/lifecycle path that must never wait on a
// language model call (except opted-in interactive search routes under
// permissions.ai: "complete+search").
func isClientRequestPath(p string) bool {
	p, _, _ = strings.Cut(strings.TrimSpace(p), "?")
	return wasm.ViewerRequestPath(p) ||
		strings.HasPrefix(p, "/ether/stream/") ||
		strings.HasPrefix(p, "/ether/v1/frontend/") ||
		strings.HasPrefix(p, "/ether/view/") || p == "/ether/view" ||
		strings.HasPrefix(p, "/ether/options/") || p == "/ether/options" ||
		p == "/ether/health" ||
		p == "/ether/configure" ||
		strings.HasPrefix(p, "/ether/file/") ||
		strings.HasPrefix(p, "/ether/directory/")
}

func isStremioCatalogRoute(path string) bool {
	p, _, _ := strings.Cut(strings.TrimSpace(path), "?")
	if !strings.HasPrefix(p, "/catalog/") || !strings.HasSuffix(p, ".json") {
		return false
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(p, "/catalog/"), ".json")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 && len(parts) != 3 {
		return false
	}
	for _, seg := range parts {
		if seg == "" {
			return false
		}
	}
	return true
}

// stremioCatalogSearchQuery extracts a non-empty Stremio catalog search query
// from /catalog/{type}/{id}/{extra}.json (extra containing search=<q>) or
// /catalog/{type}/{id}.json?search=<q>.
func stremioCatalogSearchQuery(path string) (string, bool) {
	p, rawQuery, _ := strings.Cut(strings.TrimSpace(path), "?")
	if !strings.HasPrefix(p, "/catalog/") || !strings.HasSuffix(p, ".json") {
		return "", false
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(p, "/catalog/"), ".json")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != "" {
		if vals, err := url.ParseQuery(parts[2]); err == nil {
			if q := strings.TrimSpace(vals.Get("search")); q != "" {
				return q, true
			}
		}
	}
	if (len(parts) == 2 || len(parts) == 3) && parts[0] != "" && parts[1] != "" && rawQuery != "" {
		if vals, err := url.ParseQuery(rawQuery); err == nil {
			if q := strings.TrimSpace(vals.Get("search")); q != "" {
				return q, true
			}
		}
	}
	return "", false
}

func isKindSearchPath(path string) bool {
	p, _, _ := strings.Cut(strings.TrimSpace(path), "?")
	if !strings.HasPrefix(p, "/ether/v1/kinds/") {
		return false
	}
	rest := strings.TrimPrefix(p, "/ether/v1/kinds/")
	kindKey, action, ok := strings.Cut(rest, "/")
	return ok && kindKey != "" && action == "search"
}

func isConventionalAdapterSearchSubpath(subpath string) bool {
	if subpath == "" || subpath == "/" {
		return false
	}
	trimmed := strings.Trim(subpath, "/")
	if trimmed == "" {
		return false
	}
	last := trimmed
	if idx := strings.LastIndexByte(trimmed, '/'); idx >= 0 {
		last = trimmed[idx+1:]
	}
	last = strings.ToLower(last)
	for _, ext := range []string{".json", ".xml", ".view"} {
		last = strings.TrimSuffix(last, ext)
	}
	return last == "search" || last == "search2" || last == "search3"
}

func isFrontendAdapterSearchPath(man Manifest, path string) bool {
	p, _, _ := strings.Cut(strings.TrimSpace(path), "?")
	if !strings.HasPrefix(p, "/ether/v1/frontend/") {
		return false
	}
	if !man.HasCapability(CapFrontendAdapter) {
		return false
	}
	e := man.EffectiveEther()
	if e == nil {
		return false
	}
	rest := strings.TrimPrefix(p, "/ether/v1/frontend/")
	if rest == "" {
		return false
	}
	adapterKey, tail, hasSlash := strings.Cut(rest, "/")
	if adapterKey == "" {
		return false
	}
	subpath := "/"
	if hasSlash {
		subpath = "/" + tail
	}
	normSub, ok := normalizeAdapterSearchPath(subpath)
	if !ok {
		return false
	}
	for _, a := range e.Contributes.FrontendAdapters {
		if a.Key != adapterKey {
			continue
		}
		if len(a.SearchPaths) > 0 {
			for _, declared := range a.SearchPaths {
				if normDecl, ok := normalizeAdapterSearchPath(declared); ok && normDecl == normSub {
					return true
				}
			}
			return false
		}
		return isConventionalAdapterSearchSubpath(normSub)
	}
	return false
}

// AICompleteSearchPath reports whether path is an interactive viewer search
// route eligible for ai.complete when permissions.ai is "complete+search":
//   - a Stremio-style catalog search with a non-empty query (/catalog/{type}/{id}/search=….json),
//   - a media-kind live search (/ether/v1/kinds/{kind}/search), or
//   - a declared frontend adapter's search route (/ether/v1/frontend/{key}/…).
func AICompleteSearchPath(man Manifest, path string) bool {
	if _, ok := stremioCatalogSearchQuery(path); ok {
		return true
	}
	if isKindSearchPath(path) {
		return true
	}
	return isFrontendAdapterSearchPath(man, path)
}

func isActiveSearchRoute(man Manifest, path, callQuery string) bool {
	if AICompleteSearchPath(man, path) {
		return true
	}
	return isStremioCatalogRoute(path) && strings.TrimSpace(callQuery) != ""
}

// extractCallSearchQuery pulls a viewer search query from an inbound plugin
// request when present (Stremio search extra, kinds search JSON body, or
// frontend-adapter query string) for host enforcement and per-query caching.
func extractCallSearchQuery(req Request) string {
	if q, ok := stremioCatalogSearchQuery(req.Path); ok {
		return q
	}
	if isStremioCatalogRoute(req.Path) && req.Query != nil {
		if q := strings.TrimSpace(req.Query["search"]); q != "" {
			return q
		}
	}
	if isKindSearchPath(req.Path) {
		if len(req.Body) > 0 {
			var body struct {
				Query string `json:"query"`
			}
			if json.Unmarshal(req.Body, &body) == nil {
				if q := strings.TrimSpace(body.Query); q != "" {
					return q
				}
			}
		}
		if q := queryParamFromRequest(req, "query", "q", "search"); q != "" {
			return q
		}
		return ""
	}
	if isFrontendAdapterRoute(req.Path) {
		return queryParamFromRequest(req, "q", "query", "search")
	}
	return ""
}

func queryParamFromRequest(req Request, keys ...string) string {
	if _, rawQuery, ok := strings.Cut(req.Path, "?"); ok && rawQuery != "" {
		if vals, err := url.ParseQuery(rawQuery); err == nil {
			for _, k := range keys {
				if q := strings.TrimSpace(vals.Get(k)); q != "" {
					return q
				}
			}
		}
	}
	for _, k := range keys {
		if q := strings.TrimSpace(req.Query[k]); q != "" {
			return q
		}
	}
	return ""
}

// AICompleteAllowedPath reports whether a plugin route may call ai.complete.
// Interactive viewer/client request paths are refused except opted-in search
// routes (Stremio catalog search with a query, kinds search, and frontend
// adapter search routes) when the manifest declares permissions.ai:
// "complete+search" (#702). Actions, jobs, Flow actions, tools, event
// deliveries, setup/test, background sync, and declared scheduled routes are
// allowed under both "complete" and "complete+search".
func AICompleteAllowedPath(man Manifest, path string, background bool) bool {
	p, _, _ := strings.Cut(strings.TrimSpace(path), "?")
	if p == "" {
		return false
	}
	if isClientRequestPath(p) {
		e := man.EffectiveEther()
		return e != nil && AIPermissionAllowsSearch(e.Permissions.AI) && AICompleteSearchPath(man, path)
	}
	if background {
		return true
	}
	switch {
	case strings.HasPrefix(p, "/ether/action/"),
		strings.HasPrefix(p, "/ether/job/"),
		strings.HasPrefix(p, "/ether/v1/flow-actions/"),
		p == "/ether/event",
		strings.HasPrefix(p, "/ether/tool/"),
		strings.HasPrefix(p, "/ether/v1/setup/"),
		p == "/ether/test",
		strings.HasPrefix(p, "/ether/feed/"),
		wasm.KindSyncPath(p),
		wasm.LiveTVSyncPath(p):
		return true
	}
	man.Ether = man.EffectiveEther()
	if man.Ether != nil {
		for _, s := range man.Ether.Schedule {
			callPath, _, _ := strings.Cut(strings.TrimSpace(s.Call), "?")
			if callPath == p {
				return true
			}
		}
		for _, fa := range man.Ether.Contributes.FlowActions {
			route, _, _ := strings.Cut(strings.TrimSpace(fa.Route), "?")
			if route == p {
				return true
			}
		}
	}
	return false
}

// denyExternalSchemaLoader refuses every external $ref load so plugin-supplied
// JSON Schemas can never trigger outbound network calls or local file reads.
type denyExternalSchemaLoader struct{}

func (denyExternalSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema $ref is not allowed (%s)", url)
}

type compiledAIRequest struct {
	messages        []AIMessage
	maxTokens       int
	temperature     *float64
	timeout         time.Duration
	schema          *jsonschema.Schema
	canonicalSchema []byte
	jsonObjectMode  bool
	searchQuery     string
}

// ValidateAICompleteRequest validates messages, size/token/temperature/timeout
// bounds, and compiles any optional JSON Schema. Exported for Studio and the
// conformance kit so offline checks enforce the same contract as the host.
func ValidateAICompleteRequest(req AICompleteRequest) (*compiledAIRequest, error) {
	var errs []FieldError
	if len(req.Messages) == 0 {
		errs = append(errs, FieldError{Field: "messages", Message: "at least one message is required"})
	} else if len(req.Messages) > MaxAIMessages {
		errs = append(errs, FieldError{Field: "messages", Message: fmt.Sprintf("at most %d messages are allowed", MaxAIMessages)})
	}

	var totalBytes int
	hasUser := false
	msgs := make([]AIMessage, 0, len(req.Messages))
	for i, m := range req.Messages {
		role := strings.TrimSpace(m.Role)
		switch role {
		case "system", "user", "assistant":
			if role == "user" {
				hasUser = true
			}
		default:
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("messages[%d].role", i),
				Message: "must be one of system, user, assistant",
			})
		}
		if strings.TrimSpace(m.Content) == "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("messages[%d].content", i),
				Message: "must not be empty",
			})
		}
		totalBytes += len(m.Content)
		msgs = append(msgs, AIMessage{Role: role, Content: m.Content})
	}
	if len(req.Messages) > 0 && !hasUser {
		errs = append(errs, FieldError{Field: "messages", Message: "at least one user message is required"})
	}
	if totalBytes > MaxAIInputBytes {
		errs = append(errs, FieldError{Field: "messages", Message: fmt.Sprintf("total message content exceeds %d bytes", MaxAIInputBytes)})
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultAIMaxTokens
	} else if maxTokens < 0 || maxTokens > MaxAIMaxTokens {
		errs = append(errs, FieldError{Field: "maxTokens", Message: fmt.Sprintf("must be between 1 and %d", MaxAIMaxTokens)})
	}

	var temp *float64
	if req.Temperature != nil {
		t := *req.Temperature
		if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 || t > 2 {
			errs = append(errs, FieldError{Field: "temperature", Message: "must be between 0 and 2"})
		} else {
			temp = &t
		}
	}

	timeout := DefaultAITimeout
	if req.TimeoutMS < 0 {
		errs = append(errs, FieldError{Field: "timeoutMs", Message: "must not be negative"})
	} else if req.TimeoutMS > 0 {
		timeout = min(time.Duration(req.TimeoutMS)*time.Millisecond, MaxAITimeout)
	}

	searchQuery := strings.TrimSpace(req.SearchQuery)
	if len(searchQuery) > MaxAISearchQueryBytes {
		errs = append(errs, FieldError{Field: "searchQuery", Message: fmt.Sprintf("exceeds %d bytes", MaxAISearchQueryBytes)})
	}

	var compiled *jsonschema.Schema
	var canonicalSchema []byte
	var jsonObjectMode bool
	trimmedSchema := bytes.TrimSpace(req.Schema)
	if len(trimmedSchema) > 0 && !bytes.Equal(trimmedSchema, []byte("null")) {
		if len(trimmedSchema) > MaxAISchemaBytes {
			errs = append(errs, FieldError{Field: "schema", Message: fmt.Sprintf("exceeds %d bytes", MaxAISchemaBytes)})
		} else {
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(trimmedSchema))
			if err != nil {
				errs = append(errs, FieldError{Field: "schema", Message: "must be valid JSON"})
			} else if obj, ok := doc.(map[string]any); !ok {
				errs = append(errs, FieldError{Field: "schema", Message: "must be a JSON Schema object"})
			} else {
				compiler := jsonschema.NewCompiler()
				compiler.UseLoader(denyExternalSchemaLoader{})
				const schemaURI = "urn:ether:plugin:ai:schema.json"
				if err := compiler.AddResource(schemaURI, doc); err != nil {
					errs = append(errs, FieldError{Field: "schema", Message: "invalid JSON Schema: " + err.Error()})
				} else if s, err := compiler.Compile(schemaURI); err != nil {
					errs = append(errs, FieldError{Field: "schema", Message: "invalid JSON Schema: " + err.Error()})
				} else {
					compiled = s
					canonicalSchema, _ = json.Marshal(doc)
					jsonObjectMode = schemaExpectsJSONObject(obj)
				}
			}
		}
	}

	if len(errs) > 0 {
		return nil, &ValidationError{Fields: errs}
	}
	return &compiledAIRequest{
		messages:        msgs,
		maxTokens:       maxTokens,
		temperature:     temp,
		timeout:         timeout,
		schema:          compiled,
		canonicalSchema: canonicalSchema,
		jsonObjectMode:  jsonObjectMode,
		searchQuery:     searchQuery,
	}, nil
}

func schemaExpectsJSONObject(doc map[string]any) bool {
	if rawType, ok := doc["type"]; ok {
		s, _ := rawType.(string)
		return s == "object"
	}
	_, hasProps := doc["properties"]
	return hasProps
}

func (m *Manager) aiCompletePerMin() int {
	m.mu.Lock()
	limit := m.aiCompleteLimit
	m.mu.Unlock()
	if limit <= 0 {
		return DefaultAICompletePerMin
	}
	return limit
}

func (m *Manager) aiSearchPerMin() int {
	m.mu.Lock()
	limit := m.aiSearchLimit
	m.mu.Unlock()
	if limit <= 0 {
		return DefaultAISearchCompletePerMin
	}
	return limit
}

func (m *Manager) aiSearchCacheTTL() time.Duration {
	m.aiSearchCacheMu.Lock()
	ttl := m.aiSearchTTL
	m.aiSearchCacheMu.Unlock()
	if ttl <= 0 {
		return DefaultAISearchCacheTTL
	}
	return ttl
}

// AISearchGranted reports whether pluginID is enabled, declares
// permissions.ai: "complete+search", and has neither "ai" nor "ai.search"
// revoked in its live grants.
func (m *Manager) AISearchGranted(ctx context.Context, pluginID string) bool {
	if m == nil || m.store == nil || pluginID == "" {
		return false
	}
	r, err := m.store.record(ctx, pluginID)
	if err != nil || !r.Enabled {
		return false
	}
	return m.aiSearchGrantedRecord(ctx, r)
}

func (m *Manager) aiSearchGrantedRecord(ctx context.Context, r Record) bool {
	e := r.Manifest.EffectiveEther()
	if e == nil || !AIPermissionAllowsSearch(e.Permissions.AI) {
		return false
	}
	g, err := m.store.grants(ctx, r.ID)
	if err != nil {
		return false
	}
	return !g.revoked("ai") && !g.revoked("ai.search")
}

type aiSearchCacheEntry struct {
	resp AICompleteResponse
	at   time.Time
}

type aiSearchFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	resp    AICompleteResponse
	err     error
}

func cloneAICompleteResponse(resp AICompleteResponse) AICompleteResponse {
	if len(resp.JSON) == 0 {
		return resp
	}
	cp := make([]byte, len(resp.JSON))
	copy(cp, resp.JSON)
	resp.JSON = cp
	return resp
}

func aiSearchCacheKey(rawQuery string, compiled *compiledAIRequest) string {
	normQ := NormalizeAISearchQuery(rawQuery)
	trimmedRaw := strings.TrimSpace(rawQuery)
	h := sha256.New()
	h.Write([]byte("q:"))
	h.Write([]byte(normQ))
	h.Write([]byte{0})
	for _, msg := range compiled.messages {
		content := msg.Content
		if trimmedRaw != "" && trimmedRaw != normQ {
			content = strings.ReplaceAll(content, trimmedRaw, normQ)
		}
		h.Write([]byte(msg.Role))
		h.Write([]byte{0})
		h.Write([]byte(content))
		h.Write([]byte{0})
	}
	h.Write([]byte("s:"))
	h.Write(compiled.canonicalSchema)
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(compiled.maxTokens)))
	h.Write([]byte{0})
	if compiled.temperature != nil {
		h.Write([]byte(strconv.FormatFloat(*compiled.temperature, 'f', 6, 64)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (m *Manager) lookupAISearchCacheLocked(pluginID, key string, now time.Time, ttl time.Duration) (AICompleteResponse, bool) {
	byPlugin := m.aiSearchCache[pluginID]
	if byPlugin == nil {
		return AICompleteResponse{}, false
	}
	entry, ok := byPlugin[key]
	if !ok {
		return AICompleteResponse{}, false
	}
	if now.Sub(entry.at) >= ttl {
		delete(byPlugin, key)
		return AICompleteResponse{}, false
	}
	return cloneAICompleteResponse(entry.resp), true
}

func (m *Manager) storeAISearchCache(pluginID, key string, resp AICompleteResponse) {
	now := m.now()
	ttl := m.aiSearchCacheTTL()
	m.aiSearchCacheMu.Lock()
	defer m.aiSearchCacheMu.Unlock()
	if m.aiSearchCache == nil {
		m.aiSearchCache = map[string]map[string]aiSearchCacheEntry{}
	}
	byPlugin := m.aiSearchCache[pluginID]
	if byPlugin == nil {
		byPlugin = map[string]aiSearchCacheEntry{}
		m.aiSearchCache[pluginID] = byPlugin
	}
	if len(byPlugin) >= MaxAISearchCacheEntries {
		for k, v := range byPlugin {
			if now.Sub(v.at) >= ttl {
				delete(byPlugin, k)
			}
		}
		if len(byPlugin) >= MaxAISearchCacheEntries {
			var oldestKey string
			var oldestAt time.Time
			for k, v := range byPlugin {
				if oldestKey == "" || v.at.Before(oldestAt) {
					oldestKey = k
					oldestAt = v.at
				}
			}
			if oldestKey != "" {
				delete(byPlugin, oldestKey)
			}
		}
	}
	byPlugin[key] = aiSearchCacheEntry{
		resp: cloneAICompleteResponse(resp),
		at:   now,
	}
}

func (m *Manager) clearAISearchCache(pluginID string) {
	m.aiSearchCacheMu.Lock()
	delete(m.aiSearchCache, pluginID)
	m.aiSearchCacheMu.Unlock()
}

func (m *Manager) executeAISearchCached(
	ctx context.Context,
	pluginID string,
	viewerID int64,
	rawQuery string,
	compiled *compiledAIRequest,
	backend func(context.Context, AICompleteBackendRequest) (string, error),
) (AICompleteResponse, error) {
	cacheKey := aiSearchCacheKey(rawQuery, compiled)
	flightKey := pluginID + "\x00" + cacheKey
	ttl := m.aiSearchCacheTTL()
	now := m.now()

	waitForFlight := func(f *aiSearchFlight) (AICompleteResponse, error) {
		timer := time.NewTimer(compiled.timeout)
		defer timer.Stop()
		select {
		case <-f.done:
			m.aiSearchCacheMu.Lock()
			f.waiters--
			resp, err := cloneAICompleteResponse(f.resp), f.err
			m.aiSearchCacheMu.Unlock()
			return resp, err
		case <-timer.C:
			m.aiSearchCacheMu.Lock()
			f.waiters--
			if f.waiters <= 0 && f.cancel != nil {
				f.cancel()
			}
			m.aiSearchCacheMu.Unlock()
			return AICompleteResponse{}, context.DeadlineExceeded
		case <-ctx.Done():
			m.aiSearchCacheMu.Lock()
			f.waiters--
			if f.waiters <= 0 && f.cancel != nil {
				f.cancel()
			}
			m.aiSearchCacheMu.Unlock()
			return AICompleteResponse{}, ctx.Err()
		}
	}

	m.aiSearchCacheMu.Lock()
	if resp, ok := m.lookupAISearchCacheLocked(pluginID, cacheKey, now, ttl); ok {
		m.aiSearchCacheMu.Unlock()
		return resp, nil
	}
	if existing := m.aiSearchInflight[flightKey]; existing != nil {
		existing.waiters++
		m.aiSearchCacheMu.Unlock()
		return waitForFlight(existing)
	}
	m.aiSearchCacheMu.Unlock()

	rateKey := fmt.Sprintf("%s\x00%d", pluginID, viewerID)
	limit := m.aiSearchPerMin()
	if !m.rateOK(m.aiSearchTimes, rateKey, limit, time.Minute) {
		return AICompleteResponse{}, fmt.Errorf("%w: ai.complete search is limited to %d calls per minute per viewer", ErrRateLimited, limit)
	}

	m.aiSearchCacheMu.Lock()
	if resp, ok := m.lookupAISearchCacheLocked(pluginID, cacheKey, m.now(), ttl); ok {
		m.aiSearchCacheMu.Unlock()
		m.rateRefund(m.aiSearchTimes, rateKey)
		return resp, nil
	}
	if existing := m.aiSearchInflight[flightKey]; existing != nil {
		existing.waiters++
		m.aiSearchCacheMu.Unlock()
		m.rateRefund(m.aiSearchTimes, rateKey)
		return waitForFlight(existing)
	}
	if m.aiSearchInflight == nil {
		m.aiSearchInflight = map[string]*aiSearchFlight{}
	}
	flightCtx, flightCancel := context.WithTimeout(context.Background(), compiled.timeout)
	f := &aiSearchFlight{
		done:    make(chan struct{}),
		cancel:  flightCancel,
		waiters: 1,
	}
	m.aiSearchInflight[flightKey] = f
	m.aiSearchCacheMu.Unlock()

	go func() {
		defer flightCancel()
		flightCompiled := *compiled
		resp, err := executeAIComplete(flightCtx, m.log, pluginID, backend, &flightCompiled)
		if err == nil {
			m.storeAISearchCache(pluginID, cacheKey, resp)
		} else if errors.Is(err, ErrNotConfigured) {
			m.rateRefund(m.aiSearchTimes, rateKey)
		}
		m.aiSearchCacheMu.Lock()
		f.resp = resp
		f.err = err
		delete(m.aiSearchInflight, flightKey)
		close(f.done)
		m.aiSearchCacheMu.Unlock()
	}()

	return waitForFlight(f)
}

// executeAIComplete runs a validated completion against backend, keeping
// provider credentials, endpoints, and raw upstream errors out of plugin-visible
// results, and validating structured output against the compiled JSON Schema.
func executeAIComplete(
	ctx context.Context,
	log *slog.Logger,
	pluginID string,
	backend func(context.Context, AICompleteBackendRequest) (string, error),
	compiled *compiledAIRequest,
) (AICompleteResponse, error) {
	if backend == nil {
		return AICompleteResponse{}, ErrNotConfigured
	}

	msgs := make([]AIMessage, 0, len(compiled.messages)+1)
	if compiled.schema != nil {
		msgs = append(msgs, AIMessage{
			Role: "system",
			Content: "Respond with valid JSON only, with no markdown fences or extra text. " +
				"The JSON value must conform to this JSON Schema:\n" + string(compiled.canonicalSchema),
		})
	}
	msgs = append(msgs, compiled.messages...)

	callCtx, cancel := context.WithTimeout(ctx, compiled.timeout)
	defer cancel()

	rawText, err := backend(callCtx, AICompleteBackendRequest{
		Messages:    msgs,
		JSONMode:    compiled.jsonObjectMode,
		MaxTokens:   compiled.maxTokens,
		Temperature: compiled.temperature,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotConfigured) || errors.Is(err, llm.ErrNotConfigured):
			return AICompleteResponse{}, ErrNotConfigured
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded):
			return AICompleteResponse{}, context.DeadlineExceeded
		case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
			return AICompleteResponse{}, context.Canceled
		default:
			if log != nil {
				log.Warn("plugin ai.complete failed", "plugin", pluginID, "err", err)
			}
			// Never return raw provider errors, URLs, or upstream response bodies
			// to the plugin: they may contain owner endpoint info or key hints.
			return AICompleteResponse{}, fmt.Errorf("%w: language model request failed", ErrUnavailable)
		}
	}

	text := llm.StripReasoning(rawText)
	if len(text) > MaxAIResponseBytes {
		return AICompleteResponse{}, fmt.Errorf("%w: language model response exceeded %d bytes", ErrInvalidOutput, MaxAIResponseBytes)
	}

	if compiled.schema == nil {
		return AICompleteResponse{Text: text}, nil
	}

	candidate := strings.TrimSpace(text)
	if candidate == "" || !json.Valid([]byte(candidate)) {
		candidate = strings.TrimSpace(stripCodeFence(candidate))
	}
	if candidate == "" || !json.Valid([]byte(candidate)) {
		candidate = strings.TrimSpace(llm.ExtractJSON(text))
	}
	if candidate == "" || !json.Valid([]byte(candidate)) {
		return AICompleteResponse{}, fmt.Errorf("%w: language model did not return valid JSON", ErrInvalidOutput)
	}

	val, err := jsonschema.UnmarshalJSON(strings.NewReader(candidate))
	if err != nil {
		return AICompleteResponse{}, fmt.Errorf("%w: language model did not return valid JSON", ErrInvalidOutput)
	}
	if err := compiled.schema.Validate(val); err != nil {
		return AICompleteResponse{}, fmt.Errorf("%w: language model output did not match schema", ErrInvalidOutput)
	}
	canonical, err := json.Marshal(val)
	if err != nil || len(canonical) > MaxAIResponseBytes {
		return AICompleteResponse{}, fmt.Errorf("%w: language model response exceeded %d bytes", ErrInvalidOutput, MaxAIResponseBytes)
	}
	return AICompleteResponse{
		Text: string(canonical),
		JSON: json.RawMessage(canonical),
	}, nil
}

func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	rest := s[3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	}
	if end := strings.LastIndex(rest, "```"); end >= 0 {
		return rest[:end]
	}
	return s
}

func (m *Manager) rateRefund(bucket map[string][]time.Time, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if times := bucket[key]; len(times) > 0 {
		bucket[key] = times[:len(times)-1]
	}
}

// AIComplete runs one bounded completion on the owner's configured language
// model. Requires permissions.ai: "complete" (or "complete+search" for opted-in
// interactive search routes; #702), checked live against grants. Every
// non-search viewer/client request path is refused. Interactive search calls are
// hard-bounded at MaxAISearchTimeout (20 s), cached per normalized query, and
// rate-limited per viewer separately from the plugin's background budget.
func (h *host) AIComplete(ctx context.Context, req AICompleteRequest) (AICompleteResponse, error) {
	r, g, err := h.record(ctx)
	if err != nil {
		return AICompleteResponse{}, err
	}
	level := normLevel(r.Manifest.Ether.Permissions.AI)
	if err := h.deny(r, g, "ai", level, AIPermissionAllowsComplete); err != nil {
		return AICompleteResponse{}, err
	}

	path := callPathFromContext(ctx)
	callQuery := callSearchQueryFromContext(ctx)
	isSearchPath := false
	if path != "" {
		if isClientRequestPath(path) {
			if !isActiveSearchRoute(r.Manifest, path, callQuery) {
				h.noteDenied("ai_client_path")
				return AICompleteResponse{}, fmt.Errorf("%w: ai.complete cannot run on viewer or client request path %s", ErrPermissionDenied, path)
			}
			if err := h.deny(r, g, "ai.search", level, AIPermissionAllowsSearch); err != nil {
				return AICompleteResponse{}, err
			}
			isSearchPath = true
		} else if !AICompleteAllowedPath(r.Manifest, path, isBackgroundCall(ctx) || wasm.IsBackgroundCall(ctx)) {
			h.noteDenied("ai_client_path")
			return AICompleteResponse{}, fmt.Errorf("%w: ai.complete cannot run on viewer or client request path %s", ErrPermissionDenied, path)
		}
	}

	compiled, err := ValidateAICompleteRequest(req)
	if err != nil {
		return AICompleteResponse{}, err
	}

	var searchQuery string
	if isSearchPath {
		searchQuery = compiled.searchQuery
		if searchQuery == "" {
			searchQuery = callQuery
		}
		if searchQuery == "" {
			if q, ok := stremioCatalogSearchQuery(path); ok {
				searchQuery = q
			} else if isFrontendAdapterRoute(path) {
				searchQuery = queryParamFromRequest(Request{Path: path}, "q", "query", "search")
			}
		}
		if searchQuery == "" {
			return AICompleteResponse{}, fmt.Errorf("%w: ai.complete on a search route requires a non-empty searchQuery", ErrPermissionDenied)
		}
		if len(searchQuery) > MaxAISearchQueryBytes {
			return AICompleteResponse{}, &ValidationError{Fields: []FieldError{{
				Field:   "searchQuery",
				Message: fmt.Sprintf("exceeds %d bytes", MaxAISearchQueryBytes),
			}}}
		}
		if req.TimeoutMS == 0 || compiled.timeout > MaxAISearchTimeout {
			compiled.timeout = MaxAISearchTimeout
		}
	} else if path == "" && compiled.searchQuery != "" && AIPermissionAllowsSearch(level) && !g.revoked("ai.search") {
		// Remote plugin calling /ether/plugin-host/ai/complete for a search
		// without a frontend-adapter viewer token (e.g. Stremio catalog search
		// or kinds search) still gets the hard search bound and per-query cache.
		isSearchPath = true
		searchQuery = compiled.searchQuery
		if req.TimeoutMS == 0 || compiled.timeout > MaxAISearchTimeout {
			compiled.timeout = MaxAISearchTimeout
		}
	}

	backend := h.m.deps.Backends.AIComplete
	if backend == nil {
		return AICompleteResponse{}, ErrNotConfigured
	}

	if isSearchPath {
		viewerID, _ := ViewerFromContext(ctx)
		return h.m.executeAISearchCached(ctx, h.plugin, viewerID, searchQuery, compiled, backend)
	}

	limit := h.m.aiCompletePerMin()
	if !h.m.rateOK(h.m.aiCompleteTimes, h.plugin, limit, time.Minute) {
		return AICompleteResponse{}, fmt.Errorf("%w: ai.complete is limited to %d calls per minute", ErrRateLimited, limit)
	}

	resp, err := executeAIComplete(ctx, h.m.log, h.plugin, backend, compiled)
	if errors.Is(err, ErrNotConfigured) {
		h.m.rateRefund(h.m.aiCompleteTimes, h.plugin)
	}
	return resp, err
}
