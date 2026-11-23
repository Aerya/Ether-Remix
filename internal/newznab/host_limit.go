package newznab

import (
	"context"
	"net"
	"net/url"
	"strings"
	"sync"
)

// DefaultMaxPerHost bounds concurrent requests to any single indexer origin
// (scheme://host:port), so a shared proxy such as Prowlarr, Jackett or
// FlareSolverr with dozens of configured feeds is not hit with all of them at
// once. Eight keeps a 20-feed Prowlarr search to three short waves, so
// first-play search time barely moves while Prowlarr is never flooded.
const DefaultMaxPerHost = 8

// HostLimiter bounds in-flight HTTP requests per origin (scheme://host:port).
// Idle host entries are dropped once their last holder or waiter leaves.
type HostLimiter struct {
	limit int
	mu    sync.Mutex
	hosts map[string]*hostGate
}

type hostGate struct {
	sem  chan struct{}
	refs int
}

// NewHostLimiter returns a per-host concurrency limiter. limit == 0 uses
// DefaultMaxPerHost; limit < 0 disables limiting.
func NewHostLimiter(limit int) *HostLimiter {
	if limit == 0 {
		limit = DefaultMaxPerHost
	}
	return &HostLimiter{limit: limit}
}

// Acquire claims one in-flight slot for rawURL's origin, waiting until a slot
// is free or ctx ends. The returned release func is safe to call more than once.
func (l *HostLimiter) Acquire(ctx context.Context, rawURL string) (func(), error) {
	if l == nil || l.limit < 0 {
		return func() {}, nil
	}
	key := hostKey(rawURL)
	if key == "" {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.hosts == nil {
		l.hosts = make(map[string]*hostGate)
	}
	g, ok := l.hosts[key]
	if !ok {
		g = &hostGate{sem: make(chan struct{}, l.limit)}
		l.hosts[key] = g
	}
	g.refs++
	l.mu.Unlock()

	deref := func() {
		l.mu.Lock()
		g.refs--
		if g.refs == 0 && l.hosts[key] == g {
			delete(l.hosts, key)
		}
		l.mu.Unlock()
	}

	select {
	case g.sem <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-g.sem
				deref()
			})
		}, nil
	case <-ctx.Done():
		deref()
		return nil, ctx.Err()
	}
}

func hostKey(rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return ""
	}
	if len(raw) >= len("stremio://") && strings.EqualFold(raw[:len("stremio://")], "stremio://") {
		raw = "https://" + raw[len("stremio://"):]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

type heldHostKey struct{}

// acquireHost claims one slot on c.hosts for rawURL unless ctx already holds a
// slot for the same normalized origin.
func (c *Client) acquireHost(ctx context.Context, rawURL string) (context.Context, func(), error) {
	key := hostKey(rawURL)
	if key == "" || c.hosts == nil {
		return ctx, func() {}, nil
	}
	if held, _ := ctx.Value(heldHostKey{}).(string); held == key {
		return ctx, func() {}, nil
	}
	release, err := c.hosts.Acquire(ctx, rawURL)
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, heldHostKey{}, key), release, nil
}
