package extras

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
)

func TestExtrasOutboundProxy(t *testing.T) {
	prev := egress.Current()
	t.Cleanup(func() { egress.Set(prev) })

	const secretPass = "s3cr3tProxyPass"
	var proxyHits atomic.Int32
	mp4Body := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, make([]byte, 64)...)
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(mp4Body)
	}))
	defer proxySrv.Close()

	proxyURL, err := url.Parse(proxySrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL.User = url.UserPassword("vpnuser", secretPass)
	rawProxy := proxyURL.String()

	clip := Extra{
		Key:       core.ExtraKey(42),
		ParentKey: core.MovieKey(603),
		Title:     "Official Trailer",
		Subtype:   "trailer",
		ExtraType: 1,
		Site:      "YouTube",
		SiteKey:   "dQw4w9WgXcQ",
		Source:    SourceCommand,
	}

	// 1. PolicyFrom maps Outbound.Extras and defaults to off when omitted.
	defaultPolicy := egress.PolicyFrom(config.Outbound{Enabled: true, URL: proxySrv.URL, Indexers: true})
	if defaultPolicy.Extras {
		t.Fatal("Outbound.Extras should default off")
	}
	routedPolicy := egress.PolicyFrom(config.Outbound{
		Enabled: true, URL: proxySrv.URL, Username: "vpnuser", Password: secretPass, Extras: true,
	})
	if !routedPolicy.Extras || routedPolicy.Proxy == nil {
		t.Fatalf("PolicyFrom did not wire Extras proxy: %+v", routedPolicy)
	}

	// 2. When Extras route switch is off, resolver gets no --proxy and muxInputArgs gets no -http_proxy.
	egress.Set(defaultPolicy)
	var seenArgs []string
	svcDirect := New(Options{
		CacheDir: t.TempDir(),
		Config:   func() config.Extras { return config.Extras{Enabled: true, ResolverCommand: "yt-dlp"} },
		LookPath: lookOK,
		Runner: func(_ context.Context, _ string, args []string) ([]byte, error) {
			seenArgs = append([]string(nil), args...)
			return []byte("https://cdn.example/video.mp4\n"), nil
		},
	})
	t.Cleanup(svcDirect.Close)
	if _, err := svcDirect.Resolve(context.Background(), clip); err != nil {
		t.Fatalf("Resolve with Extras off: %v", err)
	}
	if slices.Contains(seenArgs, "--proxy") {
		t.Fatalf("resolver received --proxy when Extras switch is off: %v", seenArgs)
	}

	// 3. Fail-closed: Extras route on with unusable proxy fails Resolve, muxExtra,
	// CacheExtra, and fetchTheme with egress.ErrNotConfigured without invoking runner.
	egress.Set(egress.Policy{Enabled: true, Extras: true})
	var runnerCalled atomic.Bool
	svcFailClosed := New(Options{
		CacheDir:    t.TempDir(),
		ThemeFFmpeg: func() string { return "/bin/false" },
		Config:      func() config.Extras { return config.Extras{Enabled: true, ResolverCommand: "yt-dlp"} },
		LookPath:    lookOK,
		Runner: func(context.Context, string, []string) ([]byte, error) {
			runnerCalled.Store(true)
			return nil, errors.New("should not run when proxy is unconfigured")
		},
	})
	t.Cleanup(svcFailClosed.Close)
	if _, err := svcFailClosed.Resolve(context.Background(), clip); !errors.Is(err, egress.ErrNotConfigured) {
		t.Fatalf("Resolve fail-closed err = %v, want ErrNotConfigured", err)
	}
	tmpOut, err := os.CreateTemp(t.TempDir(), "mux-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer tmpOut.Close()
	if err := svcFailClosed.muxExtra(context.Background(), Resolved{URL: "https://cdn.example/v.mp4", AudioURL: "https://cdn.example/a.m4a"}, tmpOut, 8<<20); !errors.Is(err, egress.ErrNotConfigured) {
		t.Fatalf("muxExtra fail-closed err = %v, want ErrNotConfigured", err)
	}
	if err := svcFailClosed.fetchTheme(context.Background(), core.Title{Key: core.ShowKey(1), Title: "Show"}, "Show"); !errors.Is(err, egress.ErrNotConfigured) {
		t.Fatalf("fetchTheme fail-closed err = %v, want ErrNotConfigured", err)
	}
	if runnerCalled.Load() {
		t.Fatal("runner was invoked despite unconfigured proxy")
	}

	// 4. Routed with credentials:
	// - resolver gets --proxy <url> and child env proxy; credentials are scrubbed from errors;
	// - muxInputArgs sets httpproxy in -protocol_whitelist and -http_proxy before every -i;
	// - single-file MP4 cache download routes through CapExtras HTTP proxy.
	egress.Set(routedPolicy)
	var gotProxyEnv string
	svcRouted := New(Options{
		CacheDir: t.TempDir(),
		Config:   func() config.Extras { return config.Extras{Enabled: true, ResolverCommand: "yt-dlp"} },
		LookPath: lookOK,
		Runner: func(ctx context.Context, _ string, args []string) ([]byte, error) {
			seenArgs = append([]string(nil), args...)
			rc, _ := runnerProxyFrom(ctx)
			if rc.proxy != nil {
				gotProxyEnv = rc.proxy.String()
			}
			return []byte("http://media.example/trailer.mp4\n"), nil
		},
	})
	t.Cleanup(svcRouted.Close)

	cached, err := svcRouted.CacheExtra(context.Background(), clip)
	if err != nil {
		t.Fatalf("CacheExtra through proxy: %v", err)
	}
	if _, err := os.Stat(cached.Path); err != nil {
		t.Fatalf("cached file missing: %v", err)
	}
	if idx := slices.Index(seenArgs, "--proxy"); idx < 0 || idx+1 >= len(seenArgs) || seenArgs[idx+1] != rawProxy {
		t.Fatalf("resolver args missing --proxy: %v", seenArgs)
	}
	if gotProxyEnv != rawProxy {
		t.Fatal("runner context did not carry CapExtras proxy")
	}
	if proxyHits.Load() == 0 {
		t.Fatal("CacheExtra HTTP download did not traverse CapExtras proxy")
	}

	// Credentials must never appear in errors returned by resolver.
	svcErr := New(Options{
		CacheDir: t.TempDir(),
		Config:   func() config.Extras { return config.Extras{Enabled: true, ResolverCommand: "yt-dlp"} },
		LookPath: lookOK,
		Runner: func(context.Context, string, []string) ([]byte, error) {
			return nil, fmt.Errorf("yt-dlp failed connecting via %s", rawProxy)
		},
	})
	t.Cleanup(svcErr.Close)
	_, err = svcErr.Resolve(context.Background(), clip)
	if err == nil {
		t.Fatal("expected resolver error")
	}
	if strings.Contains(err.Error(), secretPass) || strings.Contains(err.Error(), rawProxy) {
		t.Fatalf("proxy credentials leaked in error: %q", err.Error())
	}

	// muxInputArgs passes httpproxy whitelist + -http_proxy before each -i.
	p1, _, _, err := extrasProxyFor("https://cdn.example/v.mp4")
	if err != nil || p1 == nil {
		t.Fatalf("extrasProxyFor video: proxy=%v err=%v", p1, err)
	}
	p2, _, _, err := extrasProxyFor("https://cdn.example/a.m4a")
	if err != nil || p2 == nil {
		t.Fatalf("extrasProxyFor audio: proxy=%v err=%v", p2, err)
	}
	muxArgs, err := muxInputArgs([]string{"https://cdn.example/v.mp4", "https://cdn.example/a.m4a"}, nil, []*url.URL{p1, p2})
	if err != nil {
		t.Fatalf("muxInputArgs: %v", err)
	}
	proxyFlagCount := 0
	for i := 0; i < len(muxArgs)-1; i++ {
		if muxArgs[i] == "-http_proxy" && muxArgs[i+1] == rawProxy {
			proxyFlagCount++
		}
		if muxArgs[i] == "-protocol_whitelist" && !strings.Contains(muxArgs[i+1], "httpproxy") {
			t.Fatalf("mux protocol_whitelist missing httpproxy: %q", muxArgs[i+1])
		}
	}
	if proxyFlagCount != 2 {
		t.Fatalf("expected -http_proxy before both -i inputs, got %d in %v", proxyFlagCount, muxArgs)
	}

	// Custom resolverArgs should not have --proxy injected on argv, and env still carries proxy.
	var customArgs []string
	svcCustom := New(Options{
		CacheDir: t.TempDir(),
		Config: func() config.Extras {
			return config.Extras{Enabled: true, ResolverCommand: "my-wrapper", ResolverArgs: []string{"{url}"}}
		},
		LookPath: lookOK,
		Runner: func(ctx context.Context, _ string, args []string) ([]byte, error) {
			customArgs = append([]string(nil), args...)
			rc, ok := runnerProxyFrom(ctx)
			env := subprocessEnv([]string{"PATH=/usr/bin"}, rc.proxy, rc.policyEnabled, rc.bypass, true)
			if !ok || !slices.Contains(env, "HTTPS_PROXY="+rawProxy) {
				return nil, errors.New("missing HTTPS_PROXY in custom resolver env")
			}
			return []byte("http://media.example/custom.mp4\n"), nil
		},
	})
	t.Cleanup(svcCustom.Close)
	if _, err := svcCustom.Resolve(context.Background(), clip); err != nil {
		t.Fatalf("custom resolver with proxy: %v", err)
	}
	if slices.Contains(customArgs, "--proxy") {
		t.Fatalf("custom ResolverArgs should use env, not inject --proxy: %v", customArgs)
	}

	// Bypass/loopback URLs stay direct even when Extras is enabled.
	if lp, _, _, err := extrasProxyFor("http://127.0.0.1:9999/local.mp4"); err != nil || lp != nil {
		t.Fatalf("loopback URL should bypass proxy: proxy=%v err=%v", lp, err)
	}
}
