package egress

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"net/http"
	"net/url"
	"sync/atomic"
)

type sourceProxyKey struct{}

// WithSourceProxy makes an explicit source route take precedence over global
// proxy and MediaFlow settings. Disabled means inherit, not direct access.
func WithSourceProxy(ctx context.Context, p config.SourceProxy) context.Context {
	if !p.Enabled {
		return ctx
	}
	return context.WithValue(ctx, sourceProxyKey{}, p)
}
func sourceProxy(ctx context.Context) (config.SourceProxy, bool) {
	p, ok := ctx.Value(sourceProxyKey{}).(config.SourceProxy)
	return p, ok && p.Enabled
}
func selectedSourceProxy(req *http.Request) (*url.URL, bool, error) {
	if req == nil {
		return nil, false, nil
	}
	p, ok := sourceProxy(req.Context())
	if !ok {
		return nil, false, nil
	}
	policy := PolicyFrom(p.Outbound())
	if policy.Proxy == nil {
		return nil, true, ErrNotConfigured
	}
	return policy.Proxy, true, nil
}

type sourceTransport struct {
	base  http.RoundTripper
	proxy config.SourceProxy
}

func (t *sourceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(r.Clone(WithSourceProxy(r.Context(), t.proxy)))
}
func (t *sourceTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// SourceClient copies the client; shared transports are never mutated.
func SourceClient(c *http.Client, p config.SourceProxy) *http.Client {
	if !p.Enabled {
		return c
	}
	copy := *c
	base := c.Transport
	if base == nil {
		base = Transport(CapStremio)
	}
	copy.Transport = &sourceTransport{base: base, proxy: p}
	return &copy
}

type streamRoutes struct {
	addons   map[string]config.SourceProxy
	accounts map[string]config.SourceProxy
	origins  map[string]config.SourceProxy
}

var streamRouting atomic.Pointer[streamRoutes]

// UseSources publishes immutable routes, so resumed payloads never store proxy secrets.
func UseSources(c config.Config) {
	r := &streamRoutes{addons: map[string]config.SourceProxy{}, accounts: map[string]config.SourceProxy{}, origins: map[string]config.SourceProxy{}}
	for _, ix := range c.Indexers {
		if ix.IsStremio() {
			r.addons[ix.ID] = ix.StreamProxy
			if u, err := url.Parse(ix.URL); err == nil && u.Host != "" {
				if u.Scheme == "stremio" {
					u.Scheme = "https"
				}
				origin := u.Scheme + "://" + u.Host
				route := ix.StreamProxy
				if !route.Enabled {
					route = config.SourceProxy{}
				}
				if old, exists := r.origins[origin]; exists && old != route {
					// Legacy payloads lack a source ID. Ambiguous routes must not
					// silently inherit a direct route or pick an arbitrary account.
					r.origins[origin] = config.SourceProxy{Enabled: true}
				} else {
					r.origins[origin] = route
				}
			}
		}
	}
	for i, p := range c.Debrid.Providers {
		if p.ID == "" {
			p.ID = fmt.Sprintf("%s-%d", p.Kind, i)
		}
		r.accounts[p.ID] = p.Proxy
	}
	streamRouting.Store(r)
}
func WithStreamSource(ctx context.Context, account, addon string) context.Context {
	r := streamRouting.Load()
	if r == nil {
		return ctx
	}
	if account != "" {
		return WithSourceProxy(ctx, r.accounts[account])
	}
	if addon != "" {
		return WithSourceProxy(ctx, r.addons[addon])
	}
	return ctx
}

// WithAddonStream also handles payloads saved before addonId was introduced.
func WithAddonStream(ctx context.Context, id, origin string) context.Context {
	r := streamRouting.Load()
	if r == nil {
		return ctx
	}
	if id != "" {
		if p, ok := r.addons[id]; ok {
			return WithSourceProxy(ctx, p)
		}
		return ctx // A different kind of source must not borrow an addon route.
	}
	if p, ok := r.origins[origin]; ok {
		return WithSourceProxy(ctx, p)
	}
	return ctx
}

// SourceProxySelector adds explicit source routing while preserving a caller's
// existing default policy when no source override is attached.
func SourceProxySelector(inherit func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(r *http.Request) (*url.URL, error) {
		if p, selected, err := selectedSourceProxy(r); selected {
			return p, err
		}
		if inherit != nil {
			return inherit(r)
		}
		return nil, nil
	}
}
