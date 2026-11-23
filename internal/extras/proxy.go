package extras

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/egress"
)

type runnerProxyKey struct{}

type runnerProxyConfig struct {
	proxy         *url.URL
	policyEnabled bool
	bypass        []string
}

func withRunnerProxy(ctx context.Context, cfg runnerProxyConfig) context.Context {
	return context.WithValue(ctx, runnerProxyKey{}, cfg)
}

func runnerProxyFrom(ctx context.Context) (runnerProxyConfig, bool) {
	v, ok := ctx.Value(runnerProxyKey{}).(runnerProxyConfig)
	return v, ok
}

// extrasProxyFor resolves the outbound proxy policy for CapExtras against
// targetURL. When the policy routes CapExtras and has no usable proxy, it
// returns egress.ErrNotConfigured so callers fail closed.
func extrasProxyFor(targetURL string) (*url.URL, bool, []string, error) {
	p := egress.Current()
	proxy, err := p.ProxyURL(egress.CapExtras, targetURL)
	return proxy, p.Enabled, p.Bypass, err
}

// subprocessEnv constructs a child process environment honoring Ether's
// outbound proxy policy. When policyEnabled is false, it returns nil so the
// child inherits the process environment unchanged. When policyEnabled is true,
// existing proxy env vars are stripped and replaced with proxy (when set).
func subprocessEnv(base []string, proxy *url.URL, policyEnabled bool, bypass []string, includeNoProxy bool) []string {
	if !policyEnabled {
		return nil
	}
	if base == nil {
		base = os.Environ()
	}
	out := make([]string, 0, len(base)+8)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		out = append(out, kv)
	}
	if proxy != nil {
		raw := proxy.String()
		out = append(out,
			"HTTP_PROXY="+raw,
			"http_proxy="+raw,
			"HTTPS_PROXY="+raw,
			"https_proxy="+raw,
			"ALL_PROXY="+raw,
			"all_proxy="+raw,
		)
		if includeNoProxy {
			noProxy := buildNoProxy(bypass)
			out = append(out, "NO_PROXY="+noProxy, "no_proxy="+noProxy)
		}
	}
	return out
}

func buildNoProxy(bypass []string) string {
	entries := []string{"localhost", "127.0.0.1", "::1"}
	for _, b := range bypass {
		b = strings.TrimSpace(b)
		if b != "" {
			entries = append(entries, b)
		}
	}
	return strings.Join(entries, ",")
}

func scrubProxyText(s string, proxy *url.URL) string {
	return egress.ScrubProxy(s, proxy)
}

// scrubProxyErr removes proxy credentials from err while preserving sentinel
// errors such as egress.ErrNotConfigured and context cancellation.
func scrubProxyErr(err error, proxies ...*url.URL) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, egress.ErrNotConfigured) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	msg := err.Error()
	scrubbed := msg
	for _, p := range proxies {
		if p != nil {
			scrubbed = egress.ScrubProxy(scrubbed, p)
		}
	}
	if scrubbed == msg {
		return err
	}
	return errors.New(scrubbed)
}

func (s *Service) httpClient(timeout time.Duration) *http.Client {
	if s.cacheHTTP != nil {
		return s.cacheHTTP
	}
	return egress.Client(egress.CapExtras, timeout)
}

// muxInputArgs builds the ffmpeg input arguments for split video+audio URLs.
// When an input has an http:// proxy, httpproxy is added to the protocol
// whitelist and -http_proxy is placed before -i.
func muxInputArgs(inputs []string, headers map[string]string, proxies []*url.URL) ([]string, error) {
	var headerArg string
	if len(headers) > 0 {
		var b strings.Builder
		names := make([]string, 0, len(headers))
		for name := range headers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			value := headers[name]
			if strings.ContainsAny(name+value, "\r\n") {
				return nil, errors.New("invalid extra input header")
			}
			fmt.Fprintf(&b, "%s: %s\r\n", name, value)
		}
		headerArg = b.String()
	}
	args := make([]string, 0, len(inputs)*8)
	for i, input := range inputs {
		var proxy *url.URL
		if i < len(proxies) {
			proxy = proxies[i]
		}
		if proxy != nil {
			args = append(args, "-protocol_whitelist", "http,https,httpproxy,tcp,tls,crypto", "-http_proxy", proxy.String(), "-rw_timeout", "15000000")
		} else {
			args = append(args, "-protocol_whitelist", "http,https,tcp,tls,crypto", "-rw_timeout", "15000000")
		}
		if headerArg != "" {
			args = append(args, "-headers", headerArg)
		}
		args = append(args, "-i", input)
	}
	return args, nil
}
