package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/safefetch"
)

// OperatorNetworkEntry is the manifest token in ether.permissions.network that
// declares the plugin connects to server addresses chosen and approved by the
// server owner, without allowing a wildcard.
const OperatorNetworkEntry = "operator"

const maxBlockedOperatorHosts = 10

// BlockedOperatorHostError reports that a plugin declaring "operator" in
// permissions.network targeted an address the owner has not yet approved in
// Grants.OperatorHosts.
type BlockedOperatorHostError struct {
	Host string
}

// PluginMessage returns the message exposed to the plugin runtime.
func (e *BlockedOperatorHostError) PluginMessage() string {
	return fmt.Sprintf("%s needs owner approval; the owner can approve it in this plugin's settings", e.Host)
}

func (e *BlockedOperatorHostError) Error() string {
	return fmt.Sprintf("%s: %s", ErrPermissionDenied, e.PluginMessage())
}

func (e *BlockedOperatorHostError) Unwrap() []error {
	return []error{ErrPermissionDenied, ErrFetchFailed}
}

// DeclaresOperatorHosts reports whether man declares "operator" in
// ether.permissions.network for an executable plugin.
func DeclaresOperatorHosts(man Manifest) bool {
	if man.Ether == nil || man.Ether.Runtime.Kind == "data" {
		return false
	}
	for _, entry := range man.Ether.Permissions.Network {
		if strings.EqualFold(strings.TrimSpace(entry), OperatorNetworkEntry) {
			return true
		}
	}
	return false
}

// NormalizeOperatorHost validates a single owner-approved host grant ("host"
// or "host:port", or a single IP literal with optional port, without schemes,
// wildcards, or CIDRs) and returns its canonical lowercase form. Wildcards
// (including bare "*") and the manifest token "operator" are rejected.
func NormalizeOperatorHost(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.Contains(trimmed, "*") {
		return "", fmt.Errorf("wildcards are not allowed; enter a specific host name or IP address")
	}
	if strings.EqualFold(trimmed, OperatorNetworkEntry) {
		return "", fmt.Errorf("must be a specific host name or IP address")
	}
	norm, ok := safefetch.NormalizeTrustedHost(trimmed)
	if !ok {
		return "", fmt.Errorf("must be a host name or single IP address (with optional :port), without wildcards or CIDRs")
	}
	return norm, nil
}

func normalizeOperatorHosts(in []string) ([]string, error) {
	if in == nil {
		return nil, nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		norm, err := NormalizeOperatorHost(raw)
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

func (g Grants) allowsOperatorURL(u *url.URL) bool {
	if u == nil || strings.EqualFold(u.Hostname(), OperatorNetworkEntry) {
		return false
	}
	return safefetch.TrustedHostMatchesURL(g.OperatorHosts, u)
}

func (g Grants) allowsOperatorHostName(hostName string) bool {
	if strings.EqualFold(strings.TrimSpace(hostName), OperatorNetworkEntry) {
		return false
	}
	return localListCoversHost(g.OperatorHosts, hostName)
}

func isExplicitLoopbackEntry(raw string) bool {
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
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

// trustedFetchHosts returns the exact destinations allowed to resolve to
// non-public addresses during safefetch: Grants.LocalHosts plus, for plugins
// declaring "operator", any explicitly approved LAN or loopback literal in
// Grants.OperatorHosts. Public DNS names in OperatorHosts stay on the public
// pool so DNS-rebinding SSRF protection remains enforced.
func trustedFetchHosts(man Manifest, g Grants) []string {
	if !DeclaresOperatorHosts(man) || len(g.OperatorHosts) == 0 {
		return g.LocalHosts
	}
	var extra []string
	for _, entry := range g.OperatorHosts {
		if IsLocalNetworkHost(entry) || isExplicitLoopbackEntry(entry) {
			extra = append(extra, entry)
		}
	}
	if len(extra) == 0 {
		return g.LocalHosts
	}
	out := make([]string, 0, len(g.LocalHosts)+len(extra))
	out = append(out, g.LocalHosts...)
	out = append(out, extra...)
	return out
}

func isOperatorHostField(key string, field map[string]any) bool {
	if field != nil {
		for _, prop := range []string{"kind", "type", "format"} {
			if s, _ := field[prop].(string); s == "url" || s == "uri" {
				return true
			}
		}
	}
	lower := strings.ToLower(key)
	for _, token := range []string{"url", "host", "endpoint", "server", "origin", "address"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func extractSettingHost(raw string, hostField bool) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return "", false
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "stremio", "ws", "wss":
		default:
			return "", false
		}
		norm, err := NormalizeOperatorHost(urlHostPortDisplay(u))
		if err != nil {
			return "", false
		}
		return norm, true
	}
	if !hostField || strings.ContainsAny(s, " /\\?#@%") {
		return "", false
	}
	norm, err := NormalizeOperatorHost(s)
	if err != nil {
		return "", false
	}
	return norm, true
}

// SettingsCandidateHosts extracts normalized host or host:port candidates from
// a plugin's non-secret settings (including schema defaults when settings omit
// a field). Secret fields are never inspected.
func SettingsCandidateHosts(settings map[string]any, schema json.RawMessage, declaredSecrets []string, excludeLoopback bool) []string {
	public := NonSecretSettings(settings, schema, declaredSecrets)
	if len(public) == 0 {
		return nil
	}
	hostFields := map[string]bool{}
	var doc map[string]any
	if len(schema) > 0 && json.Unmarshal(schema, &doc) == nil {
		visitSettingsFields(doc, func(key string, field map[string]any) {
			if isOperatorHostField(key, field) {
				hostFields[key] = true
			}
		})
	}
	keys := make([]string, 0, len(public))
	for key := range public {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	var out []string
	seen := map[string]bool{}
	add := func(raw string, hostField bool) {
		norm, ok := extractSettingHost(raw, hostField)
		if !ok || seen[norm] {
			return
		}
		if excludeLoopback && isExplicitLoopbackEntry(norm) {
			return
		}
		seen[norm] = true
		out = append(out, norm)
	}
	for _, key := range keys {
		val := public[key]
		hf := hostFields[key] || isOperatorHostField(key, nil)
		switch v := val.(type) {
		case string:
			add(v, hf)
		case []string:
			for _, item := range v {
				add(item, hf)
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					add(s, hf)
				}
			}
		}
	}
	return out
}

func operatorCandidateCovered(man Manifest, g Grants, cand string) bool {
	norm, err := NormalizeOperatorHost(cand)
	if err != nil {
		return true
	}
	hostSpec := norm
	if ip, ipErr := netip.ParseAddr(norm); ipErr == nil && ip.Is6() {
		hostSpec = "[" + ip.String() + "]"
	}
	u, parseErr := url.Parse("https://" + hostSpec)
	if parseErr != nil || u.Hostname() == "" {
		return true
	}
	var declared []string
	if man.Ether != nil {
		for _, entry := range man.Ether.Permissions.Network {
			if !strings.EqualFold(strings.TrimSpace(entry), OperatorNetworkEntry) {
				declared = append(declared, entry)
			}
		}
	}
	if u.Port() != "" {
		return networkAllowedURL(declared, u) || g.allowsLocalURL(u) || g.allowsOperatorURL(u)
	}
	return hostAllowed(declared, u.Hostname()) ||
		localGrantCoversBlockedEntry(declared, norm) ||
		localGrantCoversBlockedEntry(g.LocalHosts, norm) ||
		localGrantCoversBlockedEntry(g.OperatorHosts, norm)
}

// PendingOperatorHosts returns distinct unapproved host or host:port entries
// referenced by a plugin's non-secret settings or recently blocked at runtime
// for a plugin declaring "operator" in permissions.network.
func PendingOperatorHosts(man Manifest, g Grants, settings map[string]any, schema json.RawMessage, blocked []string, excludeLoopback bool) []string {
	if !DeclaresOperatorHosts(man) {
		return nil
	}
	var secrets []string
	if man.Ether != nil {
		secrets = man.Ether.Permissions.Secrets
	}
	candidates := SettingsCandidateHosts(settings, schema, secrets, excludeLoopback)
	out := make([]string, 0, len(candidates)+len(blocked))
	seen := map[string]bool{}
	for _, cand := range append(candidates, blocked...) {
		norm, err := NormalizeOperatorHost(cand)
		if err != nil || seen[norm] {
			continue
		}
		if excludeLoopback && isExplicitLoopbackEntry(norm) {
			continue
		}
		if operatorCandidateCovered(man, g, norm) {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func operatorGrantCoversBlockedEntry(granted []string, blocked string) bool {
	hostSpec := blocked
	if ip, err := netip.ParseAddr(blocked); err == nil && ip.Is6() {
		hostSpec = "[" + ip.String() + "]"
	}
	u, err := url.Parse("https://" + hostSpec)
	if err != nil || u.Hostname() == "" {
		return false
	}
	if u.Port() != "" {
		return safefetch.TrustedHostMatchesURL(granted, u)
	}
	for _, g := range granted {
		norm, err := NormalizeOperatorHost(g)
		if err != nil {
			continue
		}
		if norm == blocked {
			return true
		}
		if h, _, splitErr := net.SplitHostPort(norm); splitErr == nil && strings.Trim(h, "[]") == blocked {
			return true
		}
	}
	return false
}

func (m *Manager) noteBlockedOperatorHost(ctx context.Context, pluginID, blocked string) {
	norm, err := NormalizeOperatorHost(blocked)
	if err != nil || pluginID == "" {
		return
	}
	if g, gErr := m.store.grants(ctx, pluginID); gErr == nil {
		if operatorGrantCoversBlockedEntry(g.OperatorHosts, norm) || localGrantCoversBlockedEntry(g.LocalHosts, norm) {
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.blockedOperator == nil {
		m.blockedOperator = map[string][]string{}
	}
	cur := m.blockedOperator[pluginID]
	next := make([]string, 0, min(len(cur)+1, maxBlockedOperatorHosts))
	for _, existing := range cur {
		if existing != norm {
			next = append(next, existing)
		}
	}
	next = append(next, norm)
	if len(next) > maxBlockedOperatorHosts {
		next = next[len(next)-maxBlockedOperatorHosts:]
	}
	m.blockedOperator[pluginID] = next
}

// BlockedOperatorHosts returns recently blocked operator-configurable hosts
// (up to 10 distinct entries) for one plugin, oldest to newest.
func (m *Manager) BlockedOperatorHosts(pluginID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.blockedOperator[pluginID]
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

func (m *Manager) pruneBlockedOperatorHosts(pluginID string, granted []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.blockedOperator[pluginID]
	if len(cur) == 0 {
		return
	}
	kept := cur[:0]
	for _, b := range cur {
		if !operatorGrantCoversBlockedEntry(granted, b) {
			kept = append(kept, b)
		}
	}
	if len(kept) == 0 {
		delete(m.blockedOperator, pluginID)
		return
	}
	m.blockedOperator[pluginID] = kept
}

func (m *Manager) clearBlockedOperatorHosts(pluginID string) {
	m.mu.Lock()
	delete(m.blockedOperator, pluginID)
	m.mu.Unlock()
}
