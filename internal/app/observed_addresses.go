package app

import (
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
	"github.com/braedonsaunders/ether/internal/remote"
)

const (
	maxObservedAddresses = 8
	observedAddressTTL   = 7 * 24 * time.Hour
	observedRefreshGrace = time.Minute
)

type observedAddressEntry struct {
	url      string
	lastSeen time.Time
}

// observedAddresses keeps a cheap, bounded, deduplicated in-memory list of
// recent base URLs (scheme://host[+prefix]) on which each frontend answered a
// successful request. It never writes to the database on the request hot path.
type observedAddresses struct {
	mu         sync.RWMutex
	now        func() time.Time
	byFrontend map[frontend.Protocol][]observedAddressEntry
}

func newObservedAddresses() *observedAddresses {
	return &observedAddresses{
		now:        time.Now,
		byFrontend: make(map[frontend.Protocol][]observedAddressEntry),
	}
}

func (o *observedAddresses) currentTime() time.Time {
	if o != nil && o.now != nil {
		return o.now()
	}
	return time.Now()
}

// recordRequest records the request's client-visible base URL when status is
// 2xx/3xx and the address is usable by another device.
func (o *observedAddresses) recordRequest(id frontend.Protocol, r *http.Request, mountPrefix string, status int, trusted []netip.Prefix) {
	if o == nil || r == nil || status < 200 || status >= 400 {
		return
	}
	obsPrefix, keep := observedPrefixFor(id, mountPrefix)
	if !keep {
		return
	}
	origin, ok := clientnet.Origin(r, trusted...)
	if !ok || !observableOrigin(origin) {
		return
	}
	base := origin.CanonicalBase(obsPrefix)
	if base == "" {
		return
	}
	normalized, err := remote.ParsePublishedURL(base)
	if err != nil {
		return
	}
	o.record(id, normalized)
}

func (o *observedAddresses) record(id frontend.Protocol, baseURL string) {
	now := o.currentTime()
	// Fast path: most requests hit the same current address; avoid a write lock.
	o.mu.RLock()
	entries := o.byFrontend[id]
	if len(entries) > 0 && entries[0].url == baseURL && now.Sub(entries[0].lastSeen) < observedRefreshGrace {
		o.mu.RUnlock()
		return
	}
	o.mu.RUnlock()

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.byFrontend == nil {
		o.byFrontend = make(map[frontend.Protocol][]observedAddressEntry)
	}
	current := o.byFrontend[id]
	next := make([]observedAddressEntry, 0, min(len(current)+1, maxObservedAddresses))
	next = append(next, observedAddressEntry{url: baseURL, lastSeen: now})
	for _, entry := range current {
		if entry.url == baseURL || now.Sub(entry.lastSeen) > observedAddressTTL {
			continue
		}
		if len(next) >= maxObservedAddresses {
			break
		}
		next = append(next, entry)
	}
	o.byFrontend[id] = next
}

// list returns recent observed addresses for id, most recently seen first.
func (o *observedAddresses) list(id frontend.Protocol) []string {
	if o == nil {
		return nil
	}
	now := o.currentTime()
	o.mu.RLock()
	entries := o.byFrontend[id]
	if len(entries) == 0 {
		o.mu.RUnlock()
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if now.Sub(entry.lastSeen) <= observedAddressTTL {
			out = append(out, entry.url)
		}
	}
	o.mu.RUnlock()
	if len(out) == 0 {
		return nil
	}
	return out
}

func observedPrefixFor(id frontend.Protocol, mountPrefix string) (string, bool) {
	if id == "vela" && mountPrefix == "/sessions/media" {
		return "", false
	}
	if id == "silo" && (mountPrefix == "/api/v1" || mountPrefix == "/api/v2") {
		return "", true
	}
	return mountPrefix, true
}

func observableOrigin(origin clientnet.RequestOrigin) bool {
	host := strings.TrimSpace(origin.Hostname())
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return false
	}
	if !origin.Proxied && strings.EqualFold(host, "example.com") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || portalDockerBridge.Contains(ip) {
			return false
		}
	}
	return true
}
