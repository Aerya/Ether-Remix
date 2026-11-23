package safefetch

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// MaxRedirectHeaderPatterns bounds per-request keepHeadersOnRedirect entries.
const MaxRedirectHeaderPatterns = 16

// RedirectHeaderPattern is one validated entry in a request's
// keepHeadersOnRedirect allowlist: either an exact host / host:port or a
// non-public-suffix wildcard (*.example.org).
type RedirectHeaderPattern struct {
	Raw      string
	Host     string
	Port     string
	Wildcard bool
}

// SyntheticURL returns an https URL for an exact host or host:port pattern so
// callers can check it against URL-based permission helpers. It returns nil
// for wildcard patterns.
func (p RedirectHeaderPattern) SyntheticURL() *url.URL {
	if p.Wildcard || p.Host == "" {
		return nil
	}
	host := p.Host
	if p.Port != "" {
		host = net.JoinHostPort(p.Host, p.Port)
	} else if ip, err := netip.ParseAddr(p.Host); err == nil && ip.Is6() {
		host = "[" + p.Host + "]"
	}
	return &url.URL{Scheme: "https", Host: host}
}

// ParseRedirectHeaderPatterns validates, canonicalizes, and deduplicates a
// request's keepHeadersOnRedirect list.
func ParseRedirectHeaderPatterns(raw []string) ([]RedirectHeaderPattern, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > MaxRedirectHeaderPatterns {
		return nil, fmt.Errorf("at most %d patterns are allowed", MaxRedirectHeaderPatterns)
	}
	out := make([]RedirectHeaderPattern, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		p, err := ParseRedirectHeaderPattern(entry)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[p.Raw]; dup {
			continue
		}
		seen[p.Raw] = struct{}{}
		out = append(out, p)
	}
	return out, nil
}

// ParseRedirectHeaderPattern validates one keepHeadersOnRedirect entry.
// Accepted forms:
//   - exact host or host:port (DNS name, IPv4, or [IPv6])
//   - wildcard subdomain "*.<domain>" where <domain> is a non-local DNS name
//     containing a registrable domain (never a bare TLD or public suffix such
//     as "*.com", "*.co.uk", or "*.github.io")
//
// Bare "*", "operator", schemes, paths, and userinfo are rejected.
func ParseRedirectHeaderPattern(raw string) (RedirectHeaderPattern, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 255 {
		return RedirectHeaderPattern{}, fmt.Errorf("invalid redirect header pattern %q", raw)
	}
	if s == "*" || strings.EqualFold(s, "operator") || strings.Contains(s, "://") || strings.ContainsAny(s, "/\\?#@% \t\r\n") {
		return RedirectHeaderPattern{}, fmt.Errorf("invalid redirect header pattern %q", raw)
	}
	if strings.HasPrefix(s, "*.") {
		rest := s[2:]
		if strings.ContainsAny(rest, "*:") {
			return RedirectHeaderPattern{}, fmt.Errorf("invalid wildcard redirect header pattern %q", raw)
		}
		suffix := strings.ToLower(rest)
		if !isPublicRegistrableHost(suffix) {
			return RedirectHeaderPattern{}, fmt.Errorf("wildcard redirect header pattern %q must name a public DNS domain", raw)
		}
		if _, err := publicsuffix.EffectiveTLDPlusOne(suffix); err != nil {
			return RedirectHeaderPattern{}, fmt.Errorf("wildcard redirect header pattern %q cannot target a public suffix", raw)
		}
		return RedirectHeaderPattern{
			Raw:      "*." + suffix,
			Host:     suffix,
			Wildcard: true,
		}, nil
	}
	if strings.Contains(s, "*") {
		return RedirectHeaderPattern{}, fmt.Errorf("invalid redirect header pattern %q", raw)
	}
	th, ok := parseTrustedHost(s)
	if !ok {
		return RedirectHeaderPattern{}, fmt.Errorf("invalid redirect header pattern %q", raw)
	}
	norm, ok := NormalizeTrustedHost(s)
	if !ok {
		return RedirectHeaderPattern{}, fmt.Errorf("invalid redirect header pattern %q", raw)
	}
	return RedirectHeaderPattern{
		Raw:  norm,
		Host: th.host,
		Port: th.port,
	}, nil
}

// RedirectHeaderOptions configures per-request redirect credential/header
// confinement.
type RedirectHeaderOptions struct {
	// Patterns lists explicit per-request keepHeadersOnRedirect hosts/wildcards
	// (already parsed and verified against the plugin's network permissions).
	Patterns []RedirectHeaderPattern
	// InitialHeaders is the snapshot of headers set on the initial request
	// before Client.Do runs. When a redirect is allowed by Patterns (for
	// example across sibling subdomains or to an explicit CDN host), Go's
	// net/http drops Authorization/Cookie headers before calling CheckRedirect;
	// InitialHeaders allows restoring them on authorized hops only.
	InitialHeaders http.Header
	// StripAllCallerHeaders, when true (used by http.hash), strips only
	// sensitive headers plus StripHeaders on untrusted redirects, keeping
	// host-set headers like Accept-Encoding: identity. When false, untrusted
	// redirects strip sensitive headers, StripHeaders, and every non-safe
	// caller header from InitialHeaders.
	StripAllCallerHeaders bool
	// StripHeaders lists extra header names to strip on untrusted redirects
	// (for example all caller headers in http.hash, or headers whose values
	// expanded a {secret:<name>} placeholder in kindstream).
	StripHeaders []string
}

// RedirectHeaderState tracks one redirect chain so confinement is sticky
// across hops (e.g. A -> untrusted B -> A never restores credentials) and
// HTTPS -> HTTP downgrades are rejected even after an initial HTTP -> HTTPS
// upgrade.
type RedirectHeaderState struct {
	initial        *url.URL
	seenHTTPS      bool
	leftTrusted    bool
	opts           RedirectHeaderOptions
	initialHeaders http.Header
	stripCanonical []string
}

// NewRedirectHeaderState initializes redirect header tracking for one outbound
// request starting at initial.
func NewRedirectHeaderState(initial *url.URL, opts RedirectHeaderOptions) *RedirectHeaderState {
	var initClone http.Header
	if opts.InitialHeaders != nil {
		initClone = opts.InitialHeaders.Clone()
	}
	strip := make([]string, 0, len(opts.StripHeaders))
	for _, k := range opts.StripHeaders {
		if k != "" {
			strip = append(strip, http.CanonicalHeaderKey(k))
		}
	}
	return &RedirectHeaderState{
		initial:        initial,
		seenHTTPS:      initial != nil && strings.EqualFold(initial.Scheme, "https"),
		opts:           opts,
		initialHeaders: initClone,
		stripCanonical: strip,
	}
}

var sensitiveRedirectHeaders = []string{
	"Authorization",
	"Cookie",
	"Cookie2",
	"Proxy-Authorization",
	"Proxy-Authenticate",
	"Www-Authenticate",
	"Referer",
}

var restorableSensitiveHeaders = []string{
	"Authorization",
	"Cookie",
	"Cookie2",
	"Proxy-Authorization",
	"Proxy-Authenticate",
	"Www-Authenticate",
}

var safeCrossOriginHeaders = map[string]bool{
	"Accept":          true,
	"Accept-Encoding": true,
	"Accept-Language": true,
	"Range":           true,
	"User-Agent":      true,
}

// Apply updates next.Header according to the redirect credential policy.
// It must be called from http.Client.CheckRedirect on every redirect hop.
func (s *RedirectHeaderState) Apply(next *http.Request) {
	if s == nil || next == nil {
		return
	}
	allowed := !s.leftTrusted && s.allowURL(next.URL)
	if next.URL != nil && strings.EqualFold(next.URL.Scheme, "https") {
		s.seenHTTPS = true
	}
	if !allowed {
		s.leftTrusted = true
		for _, k := range sensitiveRedirectHeaders {
			next.Header.Del(k)
		}
		for _, k := range s.stripCanonical {
			next.Header.Del(k)
		}
		if !s.opts.StripAllCallerHeaders {
			for k := range s.initialHeaders {
				if !safeCrossOriginHeaders[http.CanonicalHeaderKey(k)] {
					next.Header.Del(k)
				}
			}
		}
		return
	}

	// Redirect is trusted. Restore sensitive headers Go's net/http may have
	// dropped before invoking CheckRedirect (e.g. sibling subdomain or
	// explicit keepHeadersOnRedirect host).
	for _, k := range restorableSensitiveHeaders {
		if vals := s.initialHeaders.Values(k); len(vals) > 0 && len(next.Header.Values(k)) == 0 {
			next.Header[k] = append([]string(nil), vals...)
		}
	}
	// On a cross-host trusted redirect, preserve an explicit caller Referer
	// but strip Go's auto-generated Referer so query tokens in the initial URL
	// are not leaked to another host.
	if s.initial != nil && next.URL != nil && canonicalRedirectHost(s.initial.Hostname()) != canonicalRedirectHost(next.URL.Hostname()) {
		if vals := s.initialHeaders.Values("Referer"); len(vals) > 0 {
			next.Header["Referer"] = append([]string(nil), vals...)
		} else {
			next.Header.Del("Referer")
		}
	}
}

func (s *RedirectHeaderState) allowURL(next *url.URL) bool {
	if s.initial == nil || next == nil {
		return false
	}
	nextScheme := strings.ToLower(next.Scheme)
	if nextScheme != "http" && nextScheme != "https" {
		return false
	}
	// Never carry credentials across an HTTPS -> HTTP downgrade.
	if s.seenHTTPS && nextScheme != "https" {
		return false
	}
	initHost := canonicalRedirectHost(s.initial.Hostname())
	nextHost := canonicalRedirectHost(next.Hostname())
	if initHost == "" || nextHost == "" {
		return false
	}

	// 1. Exact same host with compatible port (same port, or http:80 -> https:443).
	if initHost == nextHost && redirectPortsCompatible(s.initial, next) {
		return true
	}

	// 2. Strict subdomain of the initial host sharing the same registrable
	// domain (eTLD+1), with compatible port and no scheme downgrade.
	if redirectPortsCompatible(s.initial, next) && isSafeStrictSubdomain(initHost, nextHost) {
		return true
	}

	// 3. Explicit per-request keepHeadersOnRedirect pattern.
	for _, p := range s.opts.Patterns {
		if p.matches(s.initial, next, nextHost) {
			return true
		}
	}
	return false
}

func (p RedirectHeaderPattern) matches(initial, next *url.URL, nextHost string) bool {
	if p.Wildcard {
		if !strings.HasSuffix(nextHost, "."+p.Host) {
			return false
		}
		return redirectPortsCompatible(initial, next) || isDefaultSchemePort(next)
	}
	if nextHost != p.Host {
		return false
	}
	if p.Port != "" {
		return urlEffectivePort(next) == p.Port
	}
	return redirectPortsCompatible(initial, next) || isDefaultSchemePort(next)
}

func canonicalRedirectHost(host string) string {
	trimmed := strings.Trim(strings.TrimSpace(host), "[]")
	if trimmed == "" {
		return ""
	}
	if ip, err := netip.ParseAddr(trimmed); err == nil {
		return ip.Unmap().String()
	}
	return strings.TrimSuffix(strings.ToLower(trimmed), ".")
}

func urlEffectivePort(u *url.URL) string {
	if u == nil {
		return ""
	}
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

func isDefaultSchemePort(u *url.URL) bool {
	if u == nil {
		return false
	}
	port := urlEffectivePort(u)
	if strings.EqualFold(u.Scheme, "https") {
		return port == "443"
	}
	return strings.EqualFold(u.Scheme, "http") && port == "80"
}

func redirectPortsCompatible(initial, next *url.URL) bool {
	if initial == nil || next == nil {
		return false
	}
	initPort := urlEffectivePort(initial)
	nextPort := urlEffectivePort(next)
	if initPort == nextPort {
		return true
	}
	// Allow http (default 80) -> https (default 443) upgrade.
	return strings.EqualFold(initial.Scheme, "http") &&
		strings.EqualFold(next.Scheme, "https") &&
		initPort == "80" && nextPort == "443"
}

func isPublicRegistrableHost(host string) bool {
	if host == "" || !strings.Contains(host, ".") {
		return false
	}
	if !validDNSName(host) {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	if host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".lan") ||
		strings.HasSuffix(host, ".home.arpa") ||
		strings.HasSuffix(host, ".internal") {
		return false
	}
	return true
}

func isSafeStrictSubdomain(initHost, nextHost string) bool {
	if !isPublicRegistrableHost(initHost) || !isPublicRegistrableHost(nextHost) {
		return false
	}
	if !strings.HasSuffix(nextHost, "."+initHost) {
		return false
	}
	initSite, err1 := publicsuffix.EffectiveTLDPlusOne(initHost)
	if err1 != nil {
		return false
	}
	nextSite, err2 := publicsuffix.EffectiveTLDPlusOne(nextHost)
	if err2 != nil {
		return false
	}
	return initSite == nextSite
}
