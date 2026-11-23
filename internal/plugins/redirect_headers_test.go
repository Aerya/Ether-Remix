package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestRedirectHeaderPolicyAcrossPluginHTTPHostFunctions(t *testing.T) {
	ctx := context.Background()

	type capturedReq struct {
		url    string
		header http.Header
	}
	var mu sync.Mutex
	var captured []capturedReq
	resetCaptured := func() {
		mu.Lock()
		captured = nil
		mu.Unlock()
	}
	lastCaptured := func() capturedReq {
		mu.Lock()
		defer mu.Unlock()
		if len(captured) == 0 {
			return capturedReq{}
		}
		return captured[len(captured)-1]
	}

	// Route virtual HTTPS/HTTP URLs through a single in-memory RoundTripper so
	// real public-domain hostnames and default ports are exercised through
	// Go's real net/http.Client redirect machinery.
	routes := map[string]func(r *http.Request) *http.Response{}
	redirectTo := func(target string) func(*http.Request) *http.Response {
		return func(r *http.Request) *http.Response {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{target}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}
		}
	}
	okBody := func(body string) func(*http.Request) *http.Response {
		return func(r *http.Request) *http.Response {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"application/octet-stream"}},
				Body:          io.NopCloser(strings.NewReader(body)),
				ContentLength: int64(len(body)),
				Request:       r,
			}
		}
	}

	routes["https://example.org/download/item/file"] = redirectTo("https://node123.storage.example.org/items/item/file")
	routes["https://node123.storage.example.org/items/item/file"] = okBody("subdomain-payload")
	routes["https://api.example.org/download/item"] = redirectTo("https://node123.storage.example.org/items/item/file")
	routes["https://example.org/to-partner"] = redirectTo("https://cdn.partner.net/file")
	routes["https://cdn.partner.net/file"] = okBody("partner-payload")
	routes["https://example.org/downgrade"] = redirectTo("http://node123.storage.example.org/items/item/file")
	routes["http://node123.storage.example.org/items/item/file"] = okBody("downgraded-payload")
	routes["https://example.org/bounce"] = redirectTo("https://cdn.partner.net/back")
	routes["https://cdn.partner.net/back"] = redirectTo("https://example.org/final")
	routes["https://example.org/final"] = okBody("bounced-payload")

	rt := dataRoundTripper(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		captured = append(captured, capturedReq{url: r.URL.String(), header: r.Header.Clone()})
		mu.Unlock()
		fn, ok := routes[r.URL.String()]
		if !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("not found")), Request: r}, nil
		}
		return fn(r), nil
	})

	env := newTestEnv(t)
	rec := installContent(t, env)
	rec.Manifest.Ether.Permissions.Network = []string{"example.org", "*.example.org", "cdn.partner.net"}
	rec.Manifest.Ether.Permissions.Secrets = []string{"sessionCookie", "apiToken"}
	var marshalErr error
	rec.ManifestRaw, marshalErr = json.Marshal(rec.Manifest)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := env.m.store.upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	env.m.client = &http.Client{Transport: rt}
	libKey := core.PluginLibraryKey(rec.ID, "recording")
	env.m.SetKindBackends(KindBackends{CachePut: func(_ context.Context, _, _, _ string, r io.Reader) (string, error) {
		_, err := io.ReadAll(r)
		return "blob:redirect-test", err
	}})
	h := env.m.HostFor(rec.ID).(*host)
	if err := h.SecretSet(ctx, "sessionCookie", "sid=topsecret", 3600); err != nil {
		t.Fatal(err)
	}
	if err := h.SecretSet(ctx, "apiToken", "tok-999", 3600); err != nil {
		t.Fatal(err)
	}

	reqHeaders := map[string]string{
		"Authorization": "Bearer tok-999",
		"Cookie":        "sid=topsecret",
		"X-Api-Key":     "custom-key",
		"Accept":        "application/octet-stream",
	}

	t.Run("same-site strict subdomain keeps credentials on fetch, fetchToCache, hash, and preview", func(t *testing.T) {
		// 1. http.fetch via hostBridge (hosted JS wire path)
		resetCaptured()
		bridge := hostBridge{h: h}
		argJSON, _ := json.Marshal([]any{map[string]any{
			"url":     "https://example.org/download/item/file",
			"headers": reqHeaders,
		}})
		if _, err := bridge.CallHost(ctx, "http.fetch", argJSON); err != nil {
			t.Fatalf("http.fetch failed: %v", err)
		}
		got := lastCaptured()
		if got.url != "https://node123.storage.example.org/items/item/file" {
			t.Fatalf("fetch landed on %q", got.url)
		}
		if got.header.Get("Cookie") != "sid=topsecret" || got.header.Get("Authorization") != "Bearer tok-999" || got.header.Get("X-Api-Key") != "custom-key" {
			t.Fatalf("http.fetch dropped credentials on strict subdomain redirect: %v", got.header)
		}

		// 2. http.fetchToCache via CallDataHost
		resetCaptured()
		cacheArgs, _ := json.Marshal([]any{map[string]any{
			"url":        "https://example.org/download/item/file",
			"headers":    reqHeaders,
			"libraryKey": libKey,
			"name":       "item.bin",
		}})
		if _, err := CallDataHost(ctx, h, "http.fetchToCache", cacheArgs); err != nil {
			t.Fatalf("http.fetchToCache failed: %v", err)
		}
		got = lastCaptured()
		if got.header.Get("Cookie") != "sid=topsecret" || got.header.Get("Authorization") != "Bearer tok-999" {
			t.Fatalf("http.fetchToCache dropped credentials on strict subdomain redirect: %v", got.header)
		}

		// 3. http.hash with {secret:...} expansion
		resetCaptured()
		res, err := h.HTTPHash(ctx, HashRequest{
			URL:  "https://example.org/download/item/file",
			Algo: "sha256",
			Headers: map[string]string{
				"Authorization": "Bearer {secret:apiToken}",
				"Cookie":        "{secret:sessionCookie}",
				"X-Api-Key":     "custom-key",
			},
		})
		if err != nil {
			t.Fatalf("http.hash start: %v", err)
		}
		if res.Pending {
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				res, err = h.HTTPHash(ctx, HashRequest{URL: "https://example.org/download/item/file", Algo: "sha256"})
				if err != nil {
					t.Fatalf("http.hash poll: %v", err)
				}
				if !res.Pending {
					break
				}
				time.Sleep(15 * time.Millisecond)
			}
		}
		if res.Pending || res.SHA256 == "" {
			t.Fatalf("http.hash did not complete: %+v", res)
		}
		got = lastCaptured()
		if got.header.Get("Cookie") != "sid=topsecret" || got.header.Get("Authorization") != "Bearer tok-999" || got.header.Get("X-Api-Key") != "custom-key" {
			t.Fatalf("http.hash dropped credentials on strict subdomain redirect: %v", got.header)
		}

		// 4. PreviewFetch
		resetCaptured()
		if _, err := env.m.PreviewFetch(ctx, []string{"example.org", "*.example.org"}, FetchRequest{
			URL:     "https://example.org/download/item/file",
			Headers: reqHeaders,
		}); err != nil {
			t.Fatalf("PreviewFetch failed: %v", err)
		}
		got = lastCaptured()
		if got.header.Get("Cookie") != "sid=topsecret" || got.header.Get("Authorization") != "Bearer tok-999" {
			t.Fatalf("PreviewFetch dropped credentials on strict subdomain redirect: %v", got.header)
		}
	})

	t.Run("sibling subdomain and separate CDN require explicit keepHeadersOnRedirect", func(t *testing.T) {
		// Sibling subdomain WITHOUT opt-in: credentials stripped.
		resetCaptured()
		if _, err := h.HTTPFetch(ctx, FetchRequest{
			URL:     "https://api.example.org/download/item",
			Headers: reqHeaders,
		}); err != nil {
			t.Fatalf("sibling default fetch: %v", err)
		}
		got := lastCaptured()
		if got.header.Get("Authorization") != "" || got.header.Get("Cookie") != "" || got.header.Get("X-Api-Key") != "" {
			t.Fatalf("sibling subdomain without opt-in leaked credentials: %v", got.header)
		}

		// Sibling subdomain WITH keepHeadersOnRedirect via hostBridge: Go-dropped credentials restored!
		resetCaptured()
		bridge := hostBridge{h: h}
		argJSON, _ := json.Marshal([]any{map[string]any{
			"url":                   "https://api.example.org/download/item",
			"headers":               reqHeaders,
			"keepHeadersOnRedirect": []string{"*.storage.example.org"},
		}})
		if _, err := bridge.CallHost(ctx, "http.fetch", argJSON); err != nil {
			t.Fatalf("sibling opt-in fetch: %v", err)
		}
		got = lastCaptured()
		if got.header.Get("Authorization") != "Bearer tok-999" || got.header.Get("Cookie") != "sid=topsecret" || got.header.Get("X-Api-Key") != "custom-key" {
			t.Fatalf("sibling subdomain with keepHeadersOnRedirect missing credentials: %v", got.header)
		}

		// Separate granted CDN: stripped by default, restored with keepHeadersOnRedirect on fetchToCache.
		resetCaptured()
		if _, err := h.HTTPFetch(ctx, FetchRequest{
			URL:     "https://example.org/to-partner",
			Headers: reqHeaders,
		}); err != nil {
			t.Fatalf("partner default fetch: %v", err)
		}
		got = lastCaptured()
		if got.header.Get("Authorization") != "" || got.header.Get("Cookie") != "" || got.header.Get("X-Api-Key") != "" {
			t.Fatalf("third-party partner without opt-in leaked credentials: %v", got.header)
		}
		if got.header.Get("Accept") != "application/octet-stream" {
			t.Fatalf("safe Accept header unexpectedly stripped: %v", got.header)
		}

		resetCaptured()
		cacheArgs, _ := json.Marshal([]any{map[string]any{
			"url":                   "https://example.org/to-partner",
			"headers":               reqHeaders,
			"keepHeadersOnRedirect": []string{"cdn.partner.net"},
			"libraryKey":            libKey,
			"name":                  "partner.bin",
		}})
		if _, err := CallDataHost(ctx, h, "http.fetchToCache", cacheArgs); err != nil {
			t.Fatalf("partner opt-in fetchToCache: %v", err)
		}
		got = lastCaptured()
		if got.header.Get("Authorization") != "Bearer tok-999" || got.header.Get("Cookie") != "sid=topsecret" || got.header.Get("X-Api-Key") != "custom-key" {
			t.Fatalf("partner opt-in fetchToCache missing credentials: %v", got.header)
		}
	})

	t.Run("https downgrade and sticky bounce never carry credentials", func(t *testing.T) {
		resetCaptured()
		if _, err := h.HTTPFetch(ctx, FetchRequest{
			URL:                   "https://example.org/downgrade",
			Headers:               reqHeaders,
			KeepHeadersOnRedirect: []string{"*.storage.example.org"},
		}); err != nil {
			t.Fatalf("downgrade fetch: %v", err)
		}
		got := lastCaptured()
		if got.header.Get("Authorization") != "" || got.header.Get("Cookie") != "" || got.header.Get("X-Api-Key") != "" {
			t.Fatalf("HTTPS->HTTP downgrade leaked credentials: %v", got.header)
		}

		resetCaptured()
		if _, err := h.HTTPFetch(ctx, FetchRequest{
			URL:     "https://example.org/bounce",
			Headers: reqHeaders,
		}); err != nil {
			t.Fatalf("bounce fetch: %v", err)
		}
		got = lastCaptured()
		if got.url != "https://example.org/final" {
			t.Fatalf("bounce landed on %q", got.url)
		}
		if got.header.Get("Authorization") != "" || got.header.Get("Cookie") != "" || got.header.Get("X-Api-Key") != "" {
			t.Fatalf("sticky bounce restored credentials: %v", got.header)
		}
	})

	t.Run("keepHeadersOnRedirect validates syntax and network permissions", func(t *testing.T) {
		var validErr *ValidationError
		_, err := h.HTTPFetch(ctx, FetchRequest{
			URL:                   "https://example.org/download/item/file",
			KeepHeadersOnRedirect: []string{"*.github.io"},
		})
		if !errors.As(err, &validErr) {
			t.Fatalf("public suffix wildcard err = %v, want *ValidationError", err)
		}

		_, err = h.HTTPFetch(ctx, FetchRequest{
			URL:                   "https://example.org/download/item/file",
			KeepHeadersOnRedirect: []string{"evil.other.net"},
		})
		if !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("undeclared host err = %v, want ErrPermissionDenied", err)
		}

		_, err = h.HTTPHash(ctx, HashRequest{
			URL:                   "https://example.org/download/item/file",
			Algo:                  "sha256",
			KeepHeadersOnRedirect: []string{"evil.other.net"},
		})
		if !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("http.hash undeclared keepHeadersOnRedirect err = %v, want ErrPermissionDenied", err)
		}

		_, err = env.m.PreviewFetch(ctx, []string{"example.org"}, FetchRequest{
			URL:                   "https://example.org/download/item/file",
			KeepHeadersOnRedirect: []string{"*.example.org"},
		})
		if !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("PreviewFetch broader wildcard err = %v, want ErrPermissionDenied", err)
		}
	})
}
