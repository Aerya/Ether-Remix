package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/llm"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

func aiManifestJSON(aiPerm string, schedule []map[string]any) []byte {
	etherBlock := map[string]any{
		"protocol":     1,
		"kind":         "extension",
		"summary":      "Tags titles using the owner language model",
		"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
		"capabilities": []string{},
	}
	if aiPerm != "" {
		etherBlock["permissions"] = map[string]any{"ai": aiPerm}
	}
	if len(schedule) > 0 {
		etherBlock["schedule"] = schedule
	}
	raw, _ := json.Marshal(map[string]any{
		"id":      "community.example.ai",
		"version": "1.0.0",
		"name":    "AI Tagger",
		"ether":   etherBlock,
	})
	return raw
}

func TestAIManifestPermissionSheetAndDiff(t *testing.T) {
	bad, err := ParseManifest(aiManifestJSON("write", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "ether.permissions.ai") {
		t.Fatalf("expected validation error for permissions.ai=write, got %v", err)
	}

	noneMan, err := ParseManifest(aiManifestJSON("none", nil))
	if err != nil || noneMan.Validate() != nil {
		t.Fatalf("permissions.ai=none failed: %v %v", err, noneMan.Validate())
	}
	noneLines, _, _ := PermissionSheet(noneMan)
	if slices.Contains(noneLines, "Uses your configured language model") {
		t.Fatalf("permissions.ai=none should not appear on PermissionSheet: %v", noneLines)
	}

	aiMan, err := ParseManifest(aiManifestJSON("complete", nil))
	if err != nil || aiMan.Validate() != nil {
		t.Fatalf("permissions.ai=complete failed: %v %v", err, aiMan.Validate())
	}
	aiLines, _, _ := PermissionSheet(aiMan)
	if !slices.Contains(aiLines, "Uses your configured language model") {
		t.Fatalf("PermissionSheet missing AI line: %v", aiLines)
	}
	added := PermissionDiff(noneMan, aiMan)
	if !slices.Contains(added, "Uses your configured language model") {
		t.Fatalf("PermissionDiff missing AI addition: %v", added)
	}

	searchMan, err := ParseManifest(aiManifestJSON("complete+search", nil))
	if err != nil || searchMan.Validate() != nil {
		t.Fatalf("permissions.ai=complete+search failed: %v %v", err, searchMan.Validate())
	}
	searchLines, _, _ := PermissionSheet(searchMan)
	if !slices.Contains(searchLines, "Uses your configured language model") ||
		!slices.Contains(searchLines, "Uses your language model while a viewer searches") {
		t.Fatalf("PermissionSheet missing complete+search lines: %v", searchLines)
	}
	upgradeAdded := PermissionDiff(aiMan, searchMan)
	if len(upgradeAdded) != 1 || upgradeAdded[0] != "Uses your language model while a viewer searches" {
		t.Fatalf("PermissionDiff(complete -> complete+search) = %v, want only search line", upgradeAdded)
	}
	fromNoneAdded := PermissionDiff(noneMan, searchMan)
	if !slices.Contains(fromNoneAdded, "Uses your configured language model") ||
		!slices.Contains(fromNoneAdded, "Uses your language model while a viewer searches") {
		t.Fatalf("PermissionDiff(none -> complete+search) = %v, want both lines", fromNoneAdded)
	}
}

func aiSearchAdapterManifestJSON(aiPerm string, searchPaths []string) []byte {
	adapter := map[string]any{
		"key":          "stash",
		"label":        "Stash GraphQL",
		"protocolHint": "custom",
		"pathPrefix":   "/stash",
	}
	if len(searchPaths) > 0 {
		adapter["searchPaths"] = searchPaths
	}
	opds := map[string]any{
		"key":          "opds",
		"label":        "OPDS Catalog",
		"protocolHint": "opds",
		"pathPrefix":   "/opds",
	}
	raw, _ := json.Marshal(map[string]any{
		"id":      "community.example.aisearch",
		"version": "1.0.0",
		"name":    "AI Search Adapter",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "extension",
			"summary":      "Frontend adapter with opt-in AI search",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"frontend_adapter"},
			"permissions":  map[string]any{"ai": aiPerm},
			"contributes": map[string]any{
				"frontendAdapters": []map[string]any{adapter, opds},
			},
		},
	})
	return raw
}

func TestHostAICompleteSearchOptInCacheBoundsAndIsolation(t *testing.T) {
	// Validate FrontendAdapterMeta.SearchPaths rejects unsafe or duplicate paths.
	for _, bad := range [][]string{{"search"}, {"/a/../b"}, {"/search?q=1"}, {"/graphql", "/graphql/"}} {
		man, err := ParseManifest(aiSearchAdapterManifestJSON("complete+search", bad))
		if err != nil {
			t.Fatal(err)
		}
		if err := man.Validate(); err == nil || !strings.Contains(err.Error(), "searchPaths") {
			t.Fatalf("searchPaths=%v: expected validation error, got %v", bad, err)
		}
	}

	env := newTestEnv(t)
	ctx := context.Background()

	var backendCalls int
	var lastDeadline time.Duration
	env.m.deps.Backends.AIComplete = func(callCtx context.Context, req AICompleteBackendRequest) (string, error) {
		backendCalls++
		if dl, ok := callCtx.Deadline(); ok {
			lastDeadline = time.Until(dl)
		}
		return `{"tags":["noir"]}`, nil
	}

	// 1. Plugin with permissions.ai: "complete" (no +search) is refused on all search routes.
	prevBase, err := env.m.PreviewInstall(ctx, Source{
		Kind:     "studio",
		Manifest: aiSearchAdapterManifestJSON("complete", []string{"/graphql"}),
		Files:    map[string][]byte{"dist/plugin.js": []byte(`globalThis.handle = function(){ return { status: 200, body: "{}" }; };`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseRec, err := env.m.Install(ctx, prevBase, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	baseHost := env.m.HostFor(baseRec.ID)
	for _, tc := range []struct {
		path  string
		query string
	}{
		{"/catalog/movie/top/search=cozy+noir.json", ""},
		{"/ether/v1/kinds/books/search", "cozy noir"},
		{"/ether/v1/frontend/opds/search?q=cozy+noir", ""},
		{"/ether/v1/frontend/stash/graphql", "cozy noir"},
	} {
		c := withCallPath(ctx, tc.path)
		if _, err := baseHost.AIComplete(c, AICompleteRequest{
			Messages:    []AIMessage{{Role: "user", Content: "cozy noir"}},
			SearchQuery: tc.query,
		}); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("permissions.ai=complete on %s: got %v, want ErrPermissionDenied", tc.path, err)
		}
	}

	// 2. Plugin with permissions.ai: "complete+search" opens ONLY search routes with a query.
	if err := env.m.Remove(ctx, baseRec.ID, false); err != nil {
		t.Fatal(err)
	}
	prev, err := env.m.PreviewInstall(ctx, Source{
		Kind:     "studio",
		Manifest: aiSearchAdapterManifestJSON("complete+search", []string{"/graphql"}),
		Files:    map[string][]byte{"dist/plugin.js": []byte(`globalThis.handle = function(){ return { status: 200, body: "{}" }; };`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	h := env.m.HostFor(rec.ID)

	// Every non-search viewer/client path and query-less search route stays refused.
	for _, tc := range []struct {
		path  string
		query string
	}{
		{"/catalog/movie/top.json", "cozy noir"},
		{"/catalog/movie/top/skip=20.json", "cozy noir"},
		{"/catalog/movie/top/search=.json", ""},
		{"/stream/movie/tt0133093.json", "cozy noir"},
		{"/meta/movie/tt0133093.json", "cozy noir"},
		{"/ether/v1/kinds/books/resolve", "cozy noir"},
		{"/ether/v1/kinds/books/match", "cozy noir"},
		{"/ether/v1/kinds/books/items/123", "cozy noir"},
		{"/ether/v1/kinds/books/live", "cozy noir"},
		{"/ether/v1/kinds/books/search", ""},            // empty query refused
		{"/ether/v1/frontend/stash/graphql", ""},        // GraphQL browse without searchQuery refused
		{"/ether/v1/frontend/stash/cover", "noir"},      // non-search adapter subpath refused
		{"/ether/v1/frontend/opds/getCoverArt", "noir"}, // non-search adapter subpath refused
		{"/ether/v1/frontend/undeclared/search?q=noir", "noir"},
		{"/ether/view/home", "cozy noir"},
	} {
		c := withCallPath(ctx, tc.path)
		if _, err := h.AIComplete(c, AICompleteRequest{
			Messages:    []AIMessage{{Role: "user", Content: "hi"}},
			SearchQuery: tc.query,
		}); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("blocked route %s (query=%q): got %v, want ErrPermissionDenied", tc.path, tc.query, err)
		}
	}

	// Hard per-request timeout bound: timeoutMs=30000 is clamped to <= MaxAISearchTimeout (20 s).
	schema := json.RawMessage(`{"type":"object","required":["tags"],"properties":{"tags":{"type":"array","items":{"type":"string"}}}}`)
	resp, err := h.AIComplete(WithViewer(withCallPath(ctx, "/ether/v1/frontend/stash/graphql"), 7), AICompleteRequest{
		Messages:    []AIMessage{{Role: "user", Content: "Cozy Noir"}},
		Schema:      schema,
		TimeoutMS:   30_000,
		SearchQuery: "Cozy Noir",
	})
	if err != nil {
		t.Fatalf("declared adapter searchPath failed: %v", err)
	}
	if !strings.Contains(resp.Text, `"noir"`) {
		t.Fatalf("unexpected search response: %+v", resp)
	}
	if lastDeadline > MaxAISearchTimeout+100*time.Millisecond {
		t.Fatalf("search backend deadline %v exceeded MaxAISearchTimeout (%v)", lastDeadline, MaxAISearchTimeout)
	}
	if backendCalls != 1 {
		t.Fatalf("backendCalls = %d, want 1", backendCalls)
	}

	// Per-query caching: repeating same query (different case/spacing) across search routes does NOT re-ask.
	for _, c := range []context.Context{
		WithViewer(withCallPath(ctx, "/catalog/movie/top/search=cozy++noir.json"), 7),
		WithViewer(withCallSearchQuery(withCallPath(ctx, "/ether/v1/kinds/books/search"), "  COZY noir "), 8),
		WithViewer(withCallPath(ctx, "/ether/v1/frontend/opds/search?q=cozy+noir"), 9),
	} {
		cachedResp, err := h.AIComplete(c, AICompleteRequest{
			Messages: []AIMessage{{Role: "user", Content: "cozy noir"}},
			Schema:   schema,
		})
		if err != nil {
			t.Fatalf("cached search call failed: %v", err)
		}
		if cachedResp.Text != resp.Text {
			t.Fatalf("cached resp %q != %q", cachedResp.Text, resp.Text)
		}
	}
	if backendCalls != 1 {
		t.Fatalf("repeated normalized searches re-asked backend: calls=%d, want 1", backendCalls)
	}

	// Per-viewer search rate limit is isolated per viewer AND separate from background budget.
	env.m.mu.Lock()
	env.m.aiSearchLimit = 1
	env.m.aiSearchTimes = map[string][]time.Time{}
	env.m.mu.Unlock()
	v1Ctx := WithViewer(withCallPath(ctx, "/ether/v1/frontend/stash/graphql"), 101)
	v2Ctx := WithViewer(withCallPath(ctx, "/ether/v1/frontend/stash/graphql"), 202)
	if _, err := h.AIComplete(v1Ctx, AICompleteRequest{
		Messages:    []AIMessage{{Role: "user", Content: "query-alpha"}},
		SearchQuery: "query-alpha",
	}); err != nil {
		t.Fatalf("viewer 1 first search: %v", err)
	}
	if _, err := h.AIComplete(v1Ctx, AICompleteRequest{
		Messages:    []AIMessage{{Role: "user", Content: "query-beta"}},
		SearchQuery: "query-beta",
	}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("viewer 1 second uncached search: got %v, want ErrRateLimited", err)
	}
	// Cached repeat for viewer 1 still succeeds even while rate-limited.
	if _, err := h.AIComplete(v1Ctx, AICompleteRequest{
		Messages:    []AIMessage{{Role: "user", Content: "query-alpha"}},
		SearchQuery: "query-alpha",
	}); err != nil {
		t.Fatalf("viewer 1 cached repeat while rate-limited: %v", err)
	}
	// Viewer 2 has its own search budget.
	if _, err := h.AIComplete(v2Ctx, AICompleteRequest{
		Messages:    []AIMessage{{Role: "user", Content: "query-gamma"}},
		SearchQuery: "query-gamma",
	}); err != nil {
		t.Fatalf("viewer 2 search blocked by viewer 1: %v", err)
	}
	// Background action budget is separate from search budget.
	if _, err := h.AIComplete(withCallPath(ctx, "/ether/action/tag"), AICompleteRequest{
		Messages: []AIMessage{{Role: "user", Content: "bg"}},
	}); err != nil {
		t.Fatalf("background action blocked by search rate limit: %v", err)
	}

	// Independent live revocation: revoking "ai.search" blocks search routes but keeps background actions working.
	if err := env.m.SetGrants(ctx, rec.ID, Grants{Revoked: []string{"ai.search"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AIComplete(v2Ctx, AICompleteRequest{
		Messages:    []AIMessage{{Role: "user", Content: "after revoke"}},
		SearchQuery: "after revoke",
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("revoked ai.search on search route: got %v, want ErrPermissionDenied", err)
	}
	if _, err := h.AIComplete(withCallPath(ctx, "/ether/action/tag"), AICompleteRequest{
		Messages: []AIMessage{{Role: "user", Content: "bg after search revoke"}},
	}); err != nil {
		t.Fatalf("background action should still work when only ai.search is revoked: %v", err)
	}
	if err := env.m.SetGrants(ctx, rec.ID, Grants{Revoked: []string{"ai"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AIComplete(withCallPath(ctx, "/ether/action/tag"), AICompleteRequest{
		Messages: []AIMessage{{Role: "user", Content: "bg after ai revoke"}},
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("revoked ai on action route: got %v, want ErrPermissionDenied", err)
	}
}

func TestHostAICompleteValidationSchemaBoundsAndSecrets(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	var backendReply string
	var backendErr error
	var backendCalls int
	var lastReq AICompleteBackendRequest
	env.m.deps.Backends.AIComplete = func(_ context.Context, req AICompleteBackendRequest) (string, error) {
		backendCalls++
		lastReq = req
		return backendReply, backendErr
	}

	// Install plugin with permissions.ai: "complete".
	prev, err := env.m.PreviewInstall(ctx, Source{
		Kind:     "studio",
		Manifest: aiManifestJSON("complete", []map[string]any{{"every": "1h", "call": "/ether/refresh"}}),
		Files:    map[string][]byte{"dist/plugin.js": []byte(`globalThis.handle = function(){ return { status: 200, body: "{}" }; };`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	h := env.m.HostFor(rec.ID)

	// 1. Structured JSON Schema validation with fenced reply + canonical output.
	backendReply = "```json\n{\"genres\":[\"Sci-Fi\"],\"year\":1999}\n```"
	schema := json.RawMessage(`{
		"type":"object",
		"required":["genres"],
		"properties":{
			"genres":{"type":"array","items":{"type":"string"}},
			"year":{"type":"integer"}
		},
		"additionalProperties":false
	}`)
	actionCtx := withCallPath(ctx, "/ether/action/classify")
	resp, err := h.AIComplete(actionCtx, AICompleteRequest{
		Messages:  []AIMessage{{Role: "user", Content: "The Matrix"}},
		Schema:    schema,
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatalf("AIComplete structured: %v", err)
	}
	if !lastReq.JSONMode || lastReq.MaxTokens != 256 {
		t.Fatalf("unexpected backend req: %+v", lastReq)
	}
	if string(resp.JSON) != resp.Text || !strings.Contains(resp.Text, `"Sci-Fi"`) {
		t.Fatalf("unexpected structured resp: %+v", resp)
	}

	// 2. Schema mismatch yields ErrInvalidOutput.
	backendReply = `{"genres":"not-an-array"}`
	if _, err := h.AIComplete(actionCtx, AICompleteRequest{
		Messages: []AIMessage{{Role: "user", Content: "bad shape"}},
		Schema:   schema,
	}); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("schema mismatch: got %v, want ErrInvalidOutput", err)
	}

	// 3. External $ref in schema is refused before hitting backend.
	callsBefore := backendCalls
	if _, err := h.AIComplete(actionCtx, AICompleteRequest{
		Messages: []AIMessage{{Role: "user", Content: "ssrf"}},
		Schema:   json.RawMessage(`{"$ref":"https://evil.example/schema.json"}`),
	}); err == nil {
		t.Fatal("expected validation error for external $ref schema")
	}
	if backendCalls != callsBefore {
		t.Fatalf("external $ref should not reach backend (calls %d -> %d)", callsBefore, backendCalls)
	}

	// 4. Provider errors NEVER leak credentials, internal URLs, or upstream response bodies.
	backendReply = ""
	backendErr = errors.New("POST http://10.0.0.5:8000/v1/chat failed with Authorization Bearer sk-secret-owner-key-999")
	_, err = h.AIComplete(actionCtx, AICompleteRequest{
		Messages: []AIMessage{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider error: got %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), "sk-secret") || strings.Contains(err.Error(), "10.0.0.5") {
		t.Fatalf("provider error leaked secret/URL to plugin: %v", err)
	}

	// 5. ErrNotConfigured returns clearly and does NOT burn rate-limit quota.
	env.m.mu.Lock()
	env.m.aiCompleteLimit = 2
	env.m.aiCompleteTimes[rec.ID] = nil
	env.m.mu.Unlock()
	backendErr = llm.ErrNotConfigured
	for range 5 {
		if _, err := h.AIComplete(actionCtx, AICompleteRequest{
			Messages: []AIMessage{{Role: "user", Content: "probe"}},
		}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("unconfigured backend: got %v, want ErrNotConfigured", err)
		}
	}
	// Rate limit still has full budget of 2 after repeated not_configured calls.
	backendErr = nil
	backendReply = "ok"
	for i := range 2 {
		if _, err := h.AIComplete(actionCtx, AICompleteRequest{
			Messages: []AIMessage{{Role: "user", Content: "ok"}},
		}); err != nil {
			t.Fatalf("call %d after not_configured failed: %v", i, err)
		}
	}
	if _, err := h.AIComplete(actionCtx, AICompleteRequest{
		Messages: []AIMessage{{Role: "user", Content: "third"}},
	}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("3rd call in minute: got %v, want ErrRateLimited", err)
	}

	// 6. Client/viewer request paths are refused even with valid grant; scheduled route allowed.
	env.m.mu.Lock()
	env.m.aiCompleteLimit = 20
	env.m.aiCompleteTimes[rec.ID] = nil
	env.m.mu.Unlock()
	for _, blocked := range []string{
		"/catalog/movie/all.json",
		"/stream/movie/tt0133093.json",
		"/meta/movie/tt0133093.json",
		"/ether/stream/open",
		"/ether/v1/frontend/plex/library",
		"/ether/view/home",
		"/ether/v1/kinds/games/resolve",
	} {
		if _, err := h.AIComplete(withCallPath(ctx, blocked), AICompleteRequest{
			Messages: []AIMessage{{Role: "user", Content: "hi"}},
		}); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("path %s: got %v, want ErrPermissionDenied", blocked, err)
		}
	}
	for _, allowed := range []string{
		"/ether/action/tag",
		"/ether/job/backfill",
		"/ether/v1/flow-actions/enrich",
		"/ether/v1/kinds/games/browse",
		"/ether/refresh", // declared in manifest ether.schedule
	} {
		if _, err := h.AIComplete(withCallPath(ctx, allowed), AICompleteRequest{
			Messages: []AIMessage{{Role: "user", Content: "hi"}},
		}); err != nil {
			t.Fatalf("allowed path %s failed: %v", allowed, err)
		}
	}

	// 7. Live grant revocation blocks immediately.
	if err := env.m.SetGrants(ctx, rec.ID, Grants{Revoked: []string{"ai"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AIComplete(actionCtx, AICompleteRequest{
		Messages: []AIMessage{{Role: "user", Content: "revoked"}},
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("revoked ai grant: got %v, want ErrPermissionDenied", err)
	}
}

func TestHostedWasmAICompleteRoutePolicyAndNotConfigured(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })
	env.m.RegisterRuntime("hosted", HostedRuntimeFactory(eng))

	configured := true
	env.m.deps.Backends.AIComplete = func(_ context.Context, req AICompleteBackendRequest) (string, error) {
		if !configured {
			return "", llm.ErrNotConfigured
		}
		return `{"mood":"noir"}`, nil
	}

	script := `
globalThis.handle = function(req) {
  try {
    var res = ether.ai.complete({
      messages: [{ role: "user", content: "Blade Runner" }],
      schema: {
        type: "object",
        required: ["mood"],
        properties: { mood: { type: "string" } }
      }
    });
    return { status: 200, body: JSON.stringify({ ok: true, mood: res.json.mood, text: res.text }) };
  } catch (e) {
    return { status: 200, body: JSON.stringify({ ok: false, code: e.code || "", message: String(e.message || e) }) };
  }
};
`
	prev, err := env.m.PreviewInstall(ctx, Source{
		Kind:     "studio",
		Manifest: aiManifestJSON("complete", nil),
		Files:    map[string][]byte{"dist/plugin.js": []byte(script)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}

	callJSON := func(path string) map[string]any {
		t.Helper()
		resp, err := env.m.Call(ctx, rec.ID, Request{Method: "POST", Path: path})
		if err != nil || resp.Status != 200 {
			t.Fatalf("%s: resp=%+v err=%v", path, resp, err)
		}
		var bodyStr string
		if err := json.Unmarshal(resp.Body, &bodyStr); err != nil {
			bodyStr = string(resp.Body)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(bodyStr), &out); err != nil {
			t.Fatalf("%s invalid body %q: %v", path, bodyStr, err)
		}
		return out
	}

	// Allowed action route returns validated JSON object.
	got := callJSON("/ether/action/classify")
	if got["ok"] != true || got["mood"] != "noir" {
		t.Fatalf("/ether/action/classify = %+v", got)
	}

	// Viewer/client route is refused with forbidden inside the hosted sandbox.
	got = callJSON("/catalog/movie/all.json")
	if got["ok"] != false || got["code"] != "forbidden" {
		t.Fatalf("/catalog/movie/all.json = %+v, want forbidden", got)
	}

	// Unconfigured language model surfaces clear not_configured code.
	configured = false
	got = callJSON("/ether/action/classify")
	if got["ok"] != false || got["code"] != "not_configured" {
		t.Fatalf("not configured = %+v, want not_configured", got)
	}
}

func TestHostedWasmAICompleteSearchFallbackAndCache(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })
	env.m.RegisterRuntime("hosted", HostedRuntimeFactory(eng))

	var backendCalls int
	var failTimeout bool
	env.m.deps.Backends.AIComplete = func(callCtx context.Context, _ AICompleteBackendRequest) (string, error) {
		backendCalls++
		if failTimeout {
			return "", context.DeadlineExceeded
		}
		return `{"mood":"noir"}`, nil
	}

	script := `
globalThis.handle = function(req) {
  try {
    var res = ether.ai.complete({
      messages: [{ role: "user", content: req.path }],
      searchQuery: (req.body && req.body.query) || "",
      schema: {
        type: "object",
        required: ["mood"],
        properties: { mood: { type: "string" } }
      }
    });
    return { status: 200, body: JSON.stringify({ ai: true, mood: res.json.mood }) };
  } catch (e) {
    return { status: 200, body: JSON.stringify({ ai: false, code: e.code || "", fallback: true }) };
  }
};
`
	prev, err := env.m.PreviewInstall(ctx, Source{
		Kind:     "studio",
		Manifest: aiSearchAdapterManifestJSON("complete+search", []string{"/graphql"}),
		Files:    map[string][]byte{"dist/plugin.js": []byte(script)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}

	callJSON := func(req Request) map[string]any {
		t.Helper()
		resp, err := env.m.Call(ctx, rec.ID, req)
		if err != nil || resp.Status != 200 {
			t.Fatalf("%s: resp=%+v err=%v", req.Path, resp, err)
		}
		var bodyStr string
		if err := json.Unmarshal(resp.Body, &bodyStr); err != nil {
			bodyStr = string(resp.Body)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(bodyStr), &out); err != nil {
			t.Fatalf("%s invalid body %q: %v", req.Path, bodyStr, err)
		}
		return out
	}

	// Non-search catalog browse stays refused.
	got := callJSON(Request{Method: "GET", Path: "/catalog/movie/top.json"})
	if got["ai"] != false || got["code"] != "forbidden" {
		t.Fatalf("non-search catalog = %+v, want forbidden", got)
	}

	// Stremio catalog search succeeds and caches repeated calls.
	got = callJSON(Request{Method: "GET", Path: "/catalog/movie/top/search=cozy+noir.json"})
	if got["ai"] != true || got["mood"] != "noir" {
		t.Fatalf("catalog search = %+v, want ai=true mood=noir", got)
	}
	got = callJSON(Request{Method: "GET", Path: "/catalog/movie/top/search=cozy+noir.json"})
	if got["ai"] != true || backendCalls != 1 {
		t.Fatalf("cached catalog search: got=%+v calls=%d, want calls=1", got, backendCalls)
	}

	// Kinds search with query succeeds.
	got = callJSON(Request{Method: "POST", Path: "/ether/v1/kinds/books/search", Body: json.RawMessage(`{"query":"space opera"}`)})
	if got["ai"] != true || got["mood"] != "noir" {
		t.Fatalf("kinds search = %+v, want ai=true", got)
	}

	// Timeout returns "timeout" so the adapter answers with its non-AI fallback.
	failTimeout = true
	got = callJSON(Request{Method: "POST", Path: "/ether/v1/frontend/stash/graphql", Body: json.RawMessage(`{"query":"slow query"}`)})
	if got["ai"] != false || got["code"] != "timeout" || got["fallback"] != true {
		t.Fatalf("timeout fallback = %+v, want code=timeout fallback=true", got)
	}
}
