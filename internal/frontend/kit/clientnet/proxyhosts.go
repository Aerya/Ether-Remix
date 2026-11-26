package clientnet

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// A trusted proxy may be named instead of numbered, for example a Docker
// container ("nginx-proxy-manager") whose address changes when it is
// recreated. Names are resolved through the system resolver, which inside a
// Docker network is Docker's own DNS, and re-resolved in the background so a
// request never waits on DNS after the first lookup.
const (
	proxyHostTTL      = 30 * time.Second
	proxyHostRetry    = 5 * time.Second
	proxyLookupBudget = 2 * time.Second
)

// lookupProxyHost resolves a proxy name. Tests replace it.
var lookupProxyHost = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

type proxyHostEntry struct {
	prefixes     []netip.Prefix
	expires      time.Time
	staleUntil   time.Time
	initialUntil time.Time
	refreshing   bool
	ready        chan struct{}
}

var proxyHosts struct {
	mu      sync.Mutex
	entries map[string]*proxyHostEntry
}

var errProxyEntry = errors.New("enter an IP address, a CIDR range or a host or container name (for example 192.168.1.5, 10.0.0.0/8 or nginx-proxy-manager)")

// proxyHostName reports whether raw is a host or container name rather than
// an address: letters, digits, '.', '-' and '_', starting with a letter or
// digit and containing at least one letter.
func proxyHostName(raw string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(raw))
	s := strings.TrimSuffix(name, ".")
	if s == "" || len(s) > 253 || strings.Contains(name, "..") {
		return "", false
	}
	letter := false
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z':
			letter = true
		case c >= '0' && c <= '9':
		case (c == '.' || c == '-' || c == '_') && i > 0:
		default:
			return "", false
		}
	}
	if !letter {
		return "", false
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return "", false
	}
	return name, true
}

// ValidateProxyEntry accepts what ProxyPrefixes can trust: an IP address, a
// CIDR range or a host or container name.
func ValidateProxyEntry(raw string) error {
	if _, err := ParseProxyEntry(raw); err == nil {
		return nil
	} else if strings.Contains(raw, "/") {
		return err
	}
	if _, ok := proxyHostName(raw); ok {
		return nil
	}
	return errProxyEntry
}

// HasProxyHostNames reports whether any entry is a name, so callers that
// cache ProxyPrefixes know the result can change without a config change.
func HasProxyHostNames(entries []string) bool {
	for _, raw := range entries {
		if _, err := ParseProxyEntry(raw); err == nil {
			continue
		}
		if _, ok := proxyHostName(raw); ok {
			return true
		}
	}
	return false
}

// startProxyHost starts a lookup on first use or refreshes an expired entry.
// Only callers sharing the initial lookup may wait for it.
func startProxyHost(name string) (*proxyHostEntry, <-chan struct{}) {
	proxyHosts.mu.Lock()
	defer proxyHosts.mu.Unlock()
	if proxyHosts.entries == nil {
		proxyHosts.entries = make(map[string]*proxyHostEntry)
	}
	e := proxyHosts.entries[name]
	now := time.Now()
	if e == nil {
		e = &proxyHostEntry{initialUntil: now.Add(proxyLookupBudget)}
		proxyHosts.entries[name] = e
	}
	if !e.refreshing && !now.Before(e.expires) {
		e.refreshing = true
		e.ready = make(chan struct{})
		go resolveProxyHost(name, e, lookupProxyHost)
	}
	if e.expires.IsZero() && now.Before(e.initialUntil) {
		return e, e.ready
	}
	return e, nil
}

// localProxyIP is deliberately narrower than LAN: directly attached public
// subnets and CGNAT do not qualify for trust based on a DNS answer.
func localProxyIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

func resolveProxyHost(name string, e *proxyHostEntry, lookup func(context.Context, string) ([]netip.Addr, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), proxyLookupBudget)
	defer cancel()
	addrs, err := lookup(ctx, name)
	var prefixes []netip.Prefix
	if err == nil && ctx.Err() == nil {
		seen := make(map[netip.Addr]struct{})
		for _, a := range addrs {
			a = a.Unmap().WithZone("")
			if !localProxyIP(a) {
				continue
			}
			if _, dup := seen[a]; dup {
				continue
			}
			seen[a] = struct{}{}
			prefixes = append(prefixes, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	var dnsErr *net.DNSError
	notFound := errors.As(err, &dnsErr) && dnsErr.IsNotFound
	timedOut := !notFound && (errors.Is(err, context.DeadlineExceeded) ||
		(dnsErr != nil && dnsErr.IsTimeout))

	proxyHosts.mu.Lock()
	defer proxyHosts.mu.Unlock()
	now := time.Now()
	e.expires = now.Add(proxyHostRetry)
	switch {
	case len(prefixes) > 0:
		e.prefixes = prefixes
		e.expires = now.Add(proxyHostTTL)
		// Permit one retry window and the time for that lookup. Further
		// failures cannot move this deadline past the last good answer.
		e.staleUntil = e.expires.Add(proxyHostRetry + proxyLookupBudget)
	case !timedOut || !now.Before(e.staleUntil):
		e.prefixes = nil
		e.staleUntil = time.Time{}
	}
	e.refreshing = false
	close(e.ready)
}

// resetProxyHosts forgets every resolved name. Intended for tests.
func resetProxyHosts() {
	proxyHosts.mu.Lock()
	proxyHosts.entries = nil
	proxyHosts.mu.Unlock()
}
