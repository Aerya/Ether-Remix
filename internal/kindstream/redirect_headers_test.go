package kindstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestKindstreamRedirectHostAndCredentialPolicy(t *testing.T) {
	ctx := context.Background()
	payload := strings.Repeat("PDF-DATA-", 128) // 1152 bytes

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

	serveBytes := func(r *http.Request) *http.Response {
		if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
			var start, end int
			if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err == nil && start >= 0 && start < len(payload) {
				if end >= len(payload) {
					end = len(payload) - 1
				}
				slice := payload[start : end+1]
				return &http.Response{
					StatusCode:    http.StatusPartialContent,
					Header:        http.Header{"Content-Range": []string{fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload))}},
					Body:          io.NopCloser(strings.NewReader(slice)),
					ContentLength: int64(len(slice)),
					Request:       r,
				}
			}
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": []string{"application/octet-stream"}},
			Body:          io.NopCloser(strings.NewReader(payload)),
			ContentLength: int64(len(payload)),
			Request:       r,
		}
	}
	redir := func(target string) func(*http.Request) *http.Response {
		return func(r *http.Request) *http.Response {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{target}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}
		}
	}

	routes := map[string]func(*http.Request) *http.Response{
		"https://example.org/file.bin":                 redir("https://node123.storage.example.org/file.bin"),
		"https://node123.storage.example.org/file.bin": serveBytes,
		"https://api.example.org/file.bin":             redir("https://node123.storage.example.org/file.bin"),
		"https://example.org/to-thirdparty":            redir("https://cdn.partner.net/file.bin"),
		"https://cdn.partner.net/file.bin":             serveBytes,
		"https://example.org/to-unpermitted":           redir("https://evil.unpermitted.net/file.bin"),
		"https://evil.unpermitted.net/file.bin":        serveBytes,
		"https://example.org/downgrade":                redir("http://node123.storage.example.org/file.bin"),
		"http://node123.storage.example.org/file.bin":  serveBytes,
	}

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		captured = append(captured, capturedReq{url: r.URL.String(), header: r.Header.Clone()})
		mu.Unlock()
		fn, ok := routes[r.URL.String()]
		if !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return fn(r), nil
	})}

	allowHost := func(host string) bool {
		host = strings.ToLower(host)
		return host == "example.org" || strings.HasSuffix(host, ".example.org") || host == "cdn.partner.net"
	}
	secrets := func(_ context.Context, name string) (string, error) {
		switch name {
		case "tok":
			return "secret-token", nil
		case "uaSecret":
			return "ua-secret-123", nil
		default:
			return "", errors.New("missing secret")
		}
	}
	deps := Deps{
		HTTP:      client,
		AllowHost: allowHost,
		Secret:    secrets,
	}

	headers := map[string]string{
		"Authorization": "Bearer {secret:tok}",
		"Cookie":        "sid=abc",
		"X-Custom":      "custom-val",
		"User-Agent":    "plugin/{secret:uaSecret}",
		"Accept":        "*/*",
	}

	for _, pattern := range []string{PatternRange, PatternDownload} {
		t.Run(pattern+": strict subdomain keeps credentials", func(t *testing.T) {
			resetCaptured()
			bs, err := Open(ctx, core.PlayableSource{
				ID:      "sub",
				Pattern: pattern,
				MIME:    "application/octet-stream",
				URL:     "https://example.org/file.bin",
				Headers: headers,
			}, deps)
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			_ = bs.Close()
			got := lastCaptured()
			if got.header.Get("Authorization") != "Bearer secret-token" || got.header.Get("Cookie") != "sid=abc" || got.header.Get("User-Agent") != "plugin/ua-secret-123" {
				t.Fatalf("subdomain redirect dropped headers: %v", got.header)
			}
		})

		t.Run(pattern+": third-party strips credentials, custom headers, and secret User-Agent", func(t *testing.T) {
			resetCaptured()
			bs, err := Open(ctx, core.PlayableSource{
				ID:      "thirdparty",
				Pattern: pattern,
				MIME:    "application/octet-stream",
				URL:     "https://example.org/to-thirdparty",
				Headers: headers,
			}, deps)
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			_ = bs.Close()
			got := lastCaptured()
			for _, k := range []string{"Authorization", "Cookie", "X-Custom", "User-Agent"} {
				if v := got.header.Get(k); v != "" && !strings.HasPrefix(v, "Go-http-client") {
					t.Fatalf("third-party redirect leaked %s=%q", k, v)
				}
			}
			if got.header.Get("Accept") != "*/*" {
				t.Fatalf("safe Accept header = %q, want kept", got.header.Get("Accept"))
			}
			if pattern == PatternRange && got.header.Get("Range") == "" {
				t.Fatalf("Range header missing on range source redirect")
			}
		})

		t.Run(pattern+": sibling subdomain keeps credentials when keepHeadersOnRedirect opts in", func(t *testing.T) {
			resetCaptured()
			bs, err := Open(ctx, core.PlayableSource{
				ID:                    "sibling",
				Pattern:               pattern,
				MIME:                  "application/octet-stream",
				URL:                   "https://api.example.org/file.bin",
				Headers:               headers,
				KeepHeadersOnRedirect: []string{"*.storage.example.org"},
			}, deps)
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			_ = bs.Close()
			got := lastCaptured()
			if got.header.Get("Authorization") != "Bearer secret-token" || got.header.Get("Cookie") != "sid=abc" {
				t.Fatalf("keepHeadersOnRedirect missing restored credentials: %v", got.header)
			}
		})

		t.Run(pattern+": redirect to unpermitted host rejected with ErrHostNotAllowed", func(t *testing.T) {
			resetCaptured()
			_, err := Open(ctx, core.PlayableSource{
				ID:      "blocked",
				Pattern: pattern,
				MIME:    "application/octet-stream",
				URL:     "https://example.org/to-unpermitted",
				Headers: headers,
			}, deps)
			if !errors.Is(err, ErrHostNotAllowed) {
				t.Fatalf("unpermitted redirect err = %v, want ErrHostNotAllowed", err)
			}
		})

		t.Run(pattern+": HTTPS to HTTP downgrade strips credentials even with keepHeadersOnRedirect", func(t *testing.T) {
			resetCaptured()
			bs, err := Open(ctx, core.PlayableSource{
				ID:                    "downgrade",
				Pattern:               pattern,
				MIME:                  "application/octet-stream",
				URL:                   "https://example.org/downgrade",
				Headers:               headers,
				KeepHeadersOnRedirect: []string{"*.storage.example.org"},
			}, deps)
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			_ = bs.Close()
			got := lastCaptured()
			if got.header.Get("Authorization") != "" || got.header.Get("Cookie") != "" || got.header.Get("X-Custom") != "" {
				t.Fatalf("HTTPS->HTTP downgrade leaked headers: %v", got.header)
			}
		})

		t.Run(pattern+": invalid or unpermitted keepHeadersOnRedirect fails up front", func(t *testing.T) {
			_, err := Open(ctx, core.PlayableSource{
				ID:                    "bad-pat",
				Pattern:               pattern,
				MIME:                  "application/octet-stream",
				URL:                   "https://example.org/file.bin",
				KeepHeadersOnRedirect: []string{"*.github.io"},
			}, deps)
			if !errors.Is(err, ErrInvalidTemplate) {
				t.Fatalf("invalid pattern err = %v, want ErrInvalidTemplate", err)
			}

			_, err = Open(ctx, core.PlayableSource{
				ID:                    "unpermitted-pat",
				Pattern:               pattern,
				MIME:                  "application/octet-stream",
				URL:                   "https://example.org/file.bin",
				KeepHeadersOnRedirect: []string{"evil.unpermitted.net"},
			}, deps)
			if !errors.Is(err, ErrHostNotAllowed) {
				t.Fatalf("unpermitted pattern err = %v, want ErrHostNotAllowed", err)
			}
		})
	}
}
