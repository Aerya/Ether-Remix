package plugins

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"

	"github.com/braedonsaunders/ether/internal/safefetch"
)

const maxBlockedLocalHosts = 10

// BlockedLocalHostError reports that a plugin fetch targeted a non-public
// network address that the owner has not granted in Grants.LocalHosts.
type BlockedLocalHostError struct {
	Host string
}

// PluginMessage returns the message exposed to the plugin runtime.
func (e *BlockedLocalHostError) PluginMessage() string {
	return fmt.Sprintf("%s is on your network; the owner can allow it in this plugin's settings", e.Host)
}

func (e *BlockedLocalHostError) Error() string {
	return fmt.Sprintf("%s: %s", ErrFetchFailed, e.PluginMessage())
}

func (e *BlockedLocalHostError) Unwrap() []error {
	return []error{ErrFetchFailed, safefetch.ErrBlocked}
}

// NormalizeLocalHost validates a single local-network host grant ("host" or
// "host:port", or a single IP literal with optional port, without wildcards
// or CIDRs) and returns its canonical lowercase form.
func NormalizeLocalHost(raw string) (string, error) {
	norm, ok := safefetch.NormalizeTrustedHost(raw)
	if !ok {
		return "", fmt.Errorf("must be a host name or single IP address (with optional :port), without wildcards or CIDRs")
	}
	return norm, nil
}

func normalizeLocalHosts(in []string) ([]string, error) {
	if in == nil {
		return nil, nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		norm, err := NormalizeLocalHost(raw)
		if err != nil {
			return nil, err
		}
		if !seen[norm] {
			seen[norm] = true
			out = append(out, norm)
		}
	}
	return out, nil
}

func (g Grants) allowsLocalURL(u *url.URL) bool {
	return safefetch.TrustedHostMatchesURL(g.LocalHosts, u)
}

func (g Grants) allowsLocalHostName(hostName string) bool {
	return localListCoversHost(g.LocalHosts, hostName)
}

func localListCoversHost(entries []string, hostName string) bool {
	normHost, ok := safefetch.NormalizeTrustedHost(hostName)
	if !ok {
		return false
	}
	for _, raw := range entries {
		if strings.EqualFold(strings.TrimSpace(raw), OperatorNetworkEntry) {
			continue
		}
		norm, ok := safefetch.NormalizeTrustedHost(raw)
		if !ok {
			continue
		}
		if norm == normHost {
			return true
		}
		if h, _, err := net.SplitHostPort(norm); err == nil && h == normHost {
			return true
		}
	}
	return false
}

func localGrantCoversBlockedEntry(granted []string, blocked string) bool {
	u, err := url.Parse("http://" + blocked)
	if err != nil || u.Hostname() == "" {
		return false
	}
	if u.Port() != "" {
		return safefetch.TrustedHostMatchesURL(granted, u)
	}
	for _, g := range granted {
		norm, ok := safefetch.NormalizeTrustedHost(g)
		if !ok {
			continue
		}
		if norm == blocked {
			return true
		}
	}
	return false
}

// IsLocalNetworkHost reports whether raw is an exact non-public LAN host
// (dotless hostname, .local, .lan, .home.arpa, or a private/CGNAT IP literal).
// Loopback (localhost, 127.0.0.1, ::1), "operator", and wildcards are excluded
// so a manifest never pre-grants loopback or operator-placeholder access.
func IsLocalNetworkHost(raw string) bool {
	if strings.Contains(raw, "*") || strings.EqualFold(strings.TrimSpace(raw), OperatorNetworkEntry) {
		return false
	}
	norm, ok := safefetch.NormalizeTrustedHost(raw)
	if !ok {
		return false
	}
	host := norm
	if h, _, err := net.SplitHostPort(norm); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return safefetch.IsPrivateLANIP(ip)
	}
	return !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".lan") ||
		strings.HasSuffix(host, ".home.arpa")
}

// DeclaredLocalHosts returns any local-network hosts declared in a manifest's
// permissions.network list, normalized and deduplicated.
func DeclaredLocalHosts(man Manifest) []string {
	if man.Ether == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, h := range man.Ether.Permissions.Network {
		if !IsLocalNetworkHost(h) {
			continue
		}
		norm, ok := safefetch.NormalizeTrustedHost(h)
		if !ok || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	return out
}

func isValidManifestNetworkEntry(raw string) bool {
	if raw == OperatorNetworkEntry {
		return true
	}
	if strings.EqualFold(raw, OperatorNetworkEntry) || strings.Contains(raw, "://") {
		return false
	}
	lower := strings.ToLower(raw)
	if hostRe.MatchString(lower) {
		return true
	}
	if strings.Contains(raw, "*") {
		return false
	}
	_, ok := safefetch.NormalizeTrustedHost(raw)
	return ok
}

func networkAllowedURL(patterns []string, u *url.URL) bool {
	if u == nil || strings.EqualFold(u.Hostname(), OperatorNetworkEntry) {
		return false
	}
	if hostAllowed(patterns, u.Hostname()) {
		return true
	}
	return safefetch.TrustedHostMatchesURL(patterns, u)
}

func networkHostDeclaredOrGranted(man Manifest, g Grants, host string) bool {
	if man.Ether != nil {
		if hostAllowed(man.Ether.Permissions.Network, host) || localListCoversHost(man.Ether.Permissions.Network, host) {
			return true
		}
		if DeclaresOperatorHosts(man) && g.allowsOperatorHostName(host) {
			return true
		}
	}
	return localListCoversHost(g.LocalHosts, host)
}

func urlHostPortDisplay(u *url.URL) string {
	if u == nil || u.Hostname() == "" {
		return ""
	}
	if port := u.Port(); port != "" {
		if norm, ok := safefetch.NormalizeTrustedHost(net.JoinHostPort(u.Hostname(), port)); ok {
			return norm
		}
	}
	if norm, ok := safefetch.NormalizeTrustedHost(u.Hostname()); ok {
		return norm
	}
	return strings.ToLower(u.Hostname())
}

func isNonPublicBlockError(err error) bool {
	if !errors.Is(err, safefetch.ErrBlocked) {
		return false
	}
	msg := err.Error()
	return !strings.Contains(msg, "does not resolve") &&
		!strings.Contains(msg, "redirect to scheme") &&
		!strings.Contains(msg, "not an http(s) URL")
}

func (h *host) allowNetworkURL(ctx context.Context, u *url.URL) error {
	r, g, err := h.record(ctx)
	if err != nil {
		return err
	}
	if r.Manifest.Ether == nil {
		return ErrPermissionDenied
	}
	if g.revoked("network") {
		h.noteDenied("network")
		return fmt.Errorf("%w: network", ErrPermissionDenied)
	}
	if networkAllowedURL(r.Manifest.Ether.Permissions.Network, u) ||
		g.allowsLocalURL(u) ||
		(DeclaresOperatorHosts(r.Manifest) && g.allowsOperatorURL(u)) {
		return nil
	}
	if DeclaresOperatorHosts(r.Manifest) {
		blocked := urlHostPortDisplay(u)
		h.m.noteBlockedOperatorHost(ctx, h.plugin, blocked)
		return &BlockedOperatorHostError{Host: blocked}
	}
	if IsLocalNetworkHost(u.Hostname()) {
		blocked := urlHostPortDisplay(u)
		h.m.noteBlockedLocalHost(ctx, h.plugin, blocked)
		return &BlockedLocalHostError{Host: blocked}
	}
	h.noteDenied("network")
	return fmt.Errorf("%w: network", ErrPermissionDenied)
}

func (m *Manager) noteBlockedLocalHost(ctx context.Context, pluginID, blocked string) {
	norm, ok := safefetch.NormalizeTrustedHost(blocked)
	if !ok || pluginID == "" {
		return
	}
	if g, err := m.store.grants(ctx, pluginID); err == nil {
		if localGrantCoversBlockedEntry(g.LocalHosts, norm) {
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.blockedLocal == nil {
		m.blockedLocal = map[string][]string{}
	}
	cur := m.blockedLocal[pluginID]
	next := make([]string, 0, min(len(cur)+1, maxBlockedLocalHosts))
	for _, existing := range cur {
		if existing != norm {
			next = append(next, existing)
		}
	}
	next = append(next, norm)
	if len(next) > maxBlockedLocalHosts {
		next = next[len(next)-maxBlockedLocalHosts:]
	}
	m.blockedLocal[pluginID] = next
}

// BlockedLocalHosts returns recently blocked private-network hosts (up to 10
// distinct entries) for one plugin, oldest to newest.
func (m *Manager) BlockedLocalHosts(pluginID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.blockedLocal[pluginID]
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

func (m *Manager) pruneBlockedLocalHosts(pluginID string, granted []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.blockedLocal[pluginID]
	if len(cur) == 0 {
		return
	}
	kept := cur[:0]
	for _, b := range cur {
		if !localGrantCoversBlockedEntry(granted, b) {
			kept = append(kept, b)
		}
	}
	if len(kept) == 0 {
		delete(m.blockedLocal, pluginID)
		return
	}
	m.blockedLocal[pluginID] = kept
}

func (m *Manager) clearBlockedLocalHosts(pluginID string) {
	m.mu.Lock()
	delete(m.blockedLocal, pluginID)
	m.mu.Unlock()
}
