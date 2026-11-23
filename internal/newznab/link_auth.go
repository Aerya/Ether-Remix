package newznab

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/safefetch"
)

const (
	linkSecretPrefix = "~ether."
	linkSecretSuffix = "~"
	// minPathSecret is the shortest secret value eligible for exact path-segment
	// replacement, avoiding collisions with common route segments like "/api" or
	// "/nzb" when a test or indexer uses a very short key.
	minPathSecret = 8
)

type pluginLinkSecret struct {
	token       string
	raw         string
	allowPath   bool
	allowSubstr bool
}

// sameAuthOrigin reports whether targetURL targets the exact same scheme,
// host, and port as baseURL so credentials are never sent off-origin or
// downgraded from HTTPS to HTTP on a custom port.
func sameAuthOrigin(baseURL, targetURL string) bool {
	if !safefetch.SameOrigin(baseURL, targetURL) {
		return false
	}
	ua, errA := url.Parse(strings.TrimSpace(baseURL))
	ub, errB := url.Parse(strings.TrimSpace(targetURL))
	if errA != nil || errB != nil {
		return false
	}
	return ua.Scheme != "" && strings.EqualFold(ua.Scheme, ub.Scheme)
}

func isSameOriginLink(cfg config.Indexer, u *url.URL, rawURL string) bool {
	if strings.TrimSpace(cfg.URL) == "" {
		return false
	}
	// Relative URLs (no scheme, no host — excludes protocol-relative "//host")
	// resolve onto cfg.URL at fetch time.
	if u.Scheme == "" && u.Host == "" {
		return true
	}
	return sameAuthOrigin(cfg.URL, rawURL)
}

func (c *Client) pluginLinkSecrets() []pluginLinkSecret {
	cfg := c.Config()
	var out []pluginLinkSecret
	seen := map[string]bool{}
	add := func(tokenName, raw string, allowPath, allowSubstr bool) {
		if raw == "" || strings.ContainsRune(raw, 0) || seen[raw] {
			return
		}
		seen[raw] = true
		out = append(out, pluginLinkSecret{
			token:       linkSecretPrefix + tokenName + linkSecretSuffix,
			raw:         raw,
			allowPath:   allowPath,
			allowSubstr: allowSubstr,
		})
	}
	if cfg.APIKey != "" {
		add("apikey", cfg.APIKey, len(cfg.APIKey) >= minPathSecret, len(cfg.APIKey) >= minAddonSecret)
	}
	if cfg.IsStremio() || cfg.IsAddon() {
		if u, err := url.Parse(strings.TrimSpace(cfg.URL)); err == nil {
			n := 0
			for _, seg := range strings.Split(u.EscapedPath(), "/") {
				raw, err := url.PathUnescape(seg)
				if err != nil || raw == "manifest.json" {
					continue
				}
				parts := append([]string{raw}, strings.FieldsFunc(raw, func(r rune) bool { return strings.ContainsRune("|=,;&", r) })...)
				for _, part := range parts {
					if len(part) >= minAddonSecret {
						add(fmt.Sprintf("addon.%d", n), part, true, true)
						n++
					}
				}
			}
		}
	}
	keys := make([]string, 0, len(cfg.SecretSettings))
	for k := range cfg.SecretSettings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := cfg.SecretSettings[k]
		add("secret."+hex.EncodeToString([]byte(k)), v, len(v) >= minPathSecret, len(v) >= minAddonSecret)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].raw) > len(out[j].raw) })
	return out
}

func splitRawURL(rawURL string) (prefix, rawPath, rawQuery, frag string, hasQuery, hasFrag bool) {
	beforeFrag := rawURL
	if b, f, ok := strings.Cut(rawURL, "#"); ok {
		beforeFrag, frag, hasFrag = b, f, true
	}
	beforeQuery := beforeFrag
	if b, q, ok := strings.Cut(beforeFrag, "?"); ok {
		beforeQuery, rawQuery, hasQuery = b, q, true
	}
	if idx := strings.Index(beforeQuery, "://"); idx >= 0 {
		if slash := strings.IndexByte(beforeQuery[idx+3:], '/'); slash >= 0 {
			cut := idx + 3 + slash
			return beforeQuery[:cut], beforeQuery[cut:], rawQuery, frag, hasQuery, hasFrag
		}
		return beforeQuery, "", rawQuery, frag, hasQuery, hasFrag
	}
	return "", beforeQuery, rawQuery, frag, hasQuery, hasFrag
}

func joinRawURL(prefix, rawPath, rawQuery, frag string, hasQuery, hasFrag bool) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(rawPath)
	if hasQuery && rawQuery != "" {
		b.WriteByte('?')
		b.WriteString(rawQuery)
	}
	if hasFrag {
		b.WriteByte('#')
		b.WriteString(frag)
	}
	return b.String()
}

func replaceSecretsInComponent(unescaped string, secrets []pluginLinkSecret, isPath bool) (string, bool) {
	cur := unescaped
	changed := false
	for _, s := range secrets {
		if isPath && !s.allowPath {
			continue
		}
		if cur == s.raw {
			return s.token, true
		}
		if s.allowSubstr && strings.Contains(cur, s.raw) {
			cur = strings.ReplaceAll(cur, s.raw, s.token)
			changed = true
		}
	}
	return cur, changed
}

func componentHasSecret(unescaped string, s pluginLinkSecret, isPath bool) bool {
	if isPath && !s.allowPath {
		return false
	}
	if unescaped == s.raw {
		return true
	}
	return s.allowSubstr && strings.Contains(unescaped, s.raw)
}

func hydrateTokensInComponent(unescaped string, byToken map[string]string) (string, bool, error) {
	if !strings.Contains(unescaped, linkSecretPrefix) {
		return unescaped, false, nil
	}
	var b strings.Builder
	changed := false
	for i := 0; i < len(unescaped); {
		idx := strings.Index(unescaped[i:], linkSecretPrefix)
		if idx < 0 {
			b.WriteString(unescaped[i:])
			break
		}
		b.WriteString(unescaped[i : i+idx])
		start := i + idx
		endRel := strings.Index(unescaped[start+len(linkSecretPrefix):], linkSecretSuffix)
		if endRel < 0 {
			return "", false, fmt.Errorf("newznab: link references malformed indexer credential placeholder: %w", core.ErrReleaseRejected)
		}
		end := start + len(linkSecretPrefix) + endRel + len(linkSecretSuffix)
		tok := unescaped[start:end]
		val, ok := byToken[tok]
		if !ok || val == "" {
			return "", false, fmt.Errorf("newznab: link references unavailable indexer credential: %w", core.ErrReleaseRejected)
		}
		b.WriteString(val)
		changed = true
		i = end
	}
	return b.String(), changed, nil
}

// StripLinkAuth removes this indexer's credentials from a same-origin (or
// relative) download link so it can be handed to plugins and cached without
// exposing secrets. Standard Newznab/Torznab "apikey" query parameters are
// removed outright; non-standard credential placements (such as "?r=<key>",
// path-embedded keys, Stremio addon path secrets, or built-in site passkeys)
// are replaced with URL-safe origin-confined tokens hydrated only on
// same-origin fetches through this indexer.
func (c *Client) StripLinkAuth(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	cfg := c.Config()
	u, err := url.Parse(rawURL)
	if err != nil || !isSameOriginLink(cfg, u, rawURL) {
		return rawURL
	}
	// Step 1: remove standard "apikey" query parameters on non-backend
	// indexers (Newznab/Torznab/Prowlarr) so standard links stay clean and
	// completely key-less.
	if !cfg.UsesBackend() && cfg.APIKey != "" && u.RawQuery != "" {
		if q, qerr := url.ParseQuery(u.RawQuery); qerr == nil {
			removed := false
			for k := range q {
				if strings.EqualFold(k, "apikey") {
					delete(q, k)
					removed = true
				}
			}
			if removed {
				u.RawQuery = q.Encode()
				rawURL = u.String()
			}
		}
	}
	// Step 2: replace any remaining indexer secret in path segments or other
	// query parameters (e.g. "?r=<key>", path keys, addon path secrets,
	// SecretSettings passkeys) with URL-safe tokens.
	secrets := c.pluginLinkSecrets()
	if len(secrets) == 0 {
		return rawURL
	}
	prefix, rawPath, rawQuery, frag, hasQuery, hasFrag := splitRawURL(rawURL)
	modified := false
	if rawPath != "" {
		segs := strings.Split(rawPath, "/")
		for i, seg := range segs {
			if seg == "" {
				continue
			}
			unesc, uerr := url.PathUnescape(seg)
			if uerr != nil {
				continue
			}
			if replaced, ok := replaceSecretsInComponent(unesc, secrets, true); ok {
				segs[i] = url.PathEscape(replaced)
				modified = true
			}
		}
		if modified {
			rawPath = strings.Join(segs, "/")
		}
	}
	if hasQuery && rawQuery != "" {
		parts := strings.Split(rawQuery, "&")
		queryModified := false
		for i, part := range parts {
			k, v, hasEq := strings.Cut(part, "=")
			if !hasEq || v == "" {
				continue
			}
			unesc, uerr := url.QueryUnescape(v)
			if uerr != nil {
				continue
			}
			if replaced, ok := replaceSecretsInComponent(unesc, secrets, false); ok {
				parts[i] = k + "=" + url.QueryEscape(replaced)
				queryModified = true
			}
		}
		if queryModified {
			rawQuery = strings.Join(parts, "&")
			modified = true
		}
	}
	if !modified {
		return rawURL
	}
	return joinRawURL(prefix, rawPath, rawQuery, frag, hasQuery, hasFrag)
}

// resolveLink resolves a relative link against the indexer's configured URL.
func (c *Client) resolveLink(rawURL string) (string, error) {
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		return rawURL, nil
	}
	base, err := url.Parse(strings.TrimRight(c.Config().URL, "/") + "/")
	if err != nil {
		return "", redactURLError(err)
	}
	ref, err := url.Parse(rawURL)
	if err != nil {
		return "", redactURLError(err)
	}
	return base.ResolveReference(ref).String(), nil
}

// authenticateLink attaches this indexer's credentials (or hydrates
// StripLinkAuth placeholders) right before fetching a download URL, strictly
// confined to the indexer's own origin. Off-origin links never receive
// credentials, and off-origin credential placeholders fail closed.
func (c *Client) authenticateLink(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", nil
	}
	cfg := c.Config()
	hasToken := strings.Contains(rawURL, linkSecretPrefix)
	sameOrigin := sameAuthOrigin(cfg.URL, rawURL)
	if hasToken && !sameOrigin {
		return "", fmt.Errorf("newznab: refusing to send indexer credentials off-origin: %w", safefetch.ErrBlocked)
	}
	if !sameOrigin {
		return rawURL, nil
	}
	if hasToken {
		secrets := c.pluginLinkSecrets()
		byToken := make(map[string]string, len(secrets))
		for _, s := range secrets {
			byToken[s.token] = s.raw
		}
		prefix, rawPath, rawQuery, frag, hasQuery, hasFrag := splitRawURL(rawURL)
		if strings.Contains(prefix, linkSecretPrefix) || strings.Contains(frag, linkSecretPrefix) {
			return "", fmt.Errorf("newznab: invalid credential placeholder position: %w", core.ErrReleaseRejected)
		}
		if rawPath != "" && strings.Contains(rawPath, linkSecretPrefix) {
			segs := strings.Split(rawPath, "/")
			for i, seg := range segs {
				if !strings.Contains(seg, linkSecretPrefix) {
					continue
				}
				unesc, uerr := url.PathUnescape(seg)
				if uerr != nil {
					return "", fmt.Errorf("newznab: invalid escaped path in credentialed link: %w", core.ErrReleaseRejected)
				}
				hydrated, changed, err := hydrateTokensInComponent(unesc, byToken)
				if err != nil {
					return "", err
				}
				if changed {
					segs[i] = url.PathEscape(hydrated)
				}
			}
			rawPath = strings.Join(segs, "/")
		}
		if hasQuery && strings.Contains(rawQuery, linkSecretPrefix) {
			parts := strings.Split(rawQuery, "&")
			for i, part := range parts {
				if !strings.Contains(part, linkSecretPrefix) {
					continue
				}
				k, v, hasEq := strings.Cut(part, "=")
				if strings.Contains(k, linkSecretPrefix) || !hasEq {
					return "", fmt.Errorf("newznab: invalid query credential placeholder: %w", core.ErrReleaseRejected)
				}
				unesc, uerr := url.QueryUnescape(v)
				if uerr != nil {
					return "", fmt.Errorf("newznab: invalid escaped query in credentialed link: %w", core.ErrReleaseRejected)
				}
				hydrated, changed, err := hydrateTokensInComponent(unesc, byToken)
				if err != nil {
					return "", err
				}
				if changed {
					parts[i] = k + "=" + url.QueryEscape(hydrated)
				}
			}
			rawQuery = strings.Join(parts, "&")
		}
		out := joinRawURL(prefix, rawPath, rawQuery, frag, hasQuery, hasFrag)
		if strings.Contains(out, linkSecretPrefix) {
			return "", fmt.Errorf("newznab: link references unavailable indexer credential: %w", core.ErrReleaseRejected)
		}
		return out, nil
	}
	if cfg.UsesBackend() || cfg.APIKey == "" {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, nil
	}
	q := u.Query()
	for k, vals := range q {
		if strings.EqualFold(k, "apikey") {
			for _, v := range vals {
				if v != "" {
					return rawURL, nil
				}
			}
		}
	}
	// Do not append apikey when the link already embeds the API key in another
	// parameter or path segment (for example an unstripped "?r=<key>" link).
	apiSecret := pluginLinkSecret{
		raw:         cfg.APIKey,
		allowPath:   len(cfg.APIKey) >= minPathSecret,
		allowSubstr: len(cfg.APIKey) >= minAddonSecret,
	}
	for _, vals := range q {
		for _, v := range vals {
			if componentHasSecret(v, apiSecret, false) {
				return rawURL, nil
			}
		}
	}
	if apiSecret.allowPath {
		for _, seg := range strings.Split(u.EscapedPath(), "/") {
			if seg == "" {
				continue
			}
			if unesc, uerr := url.PathUnescape(seg); uerr == nil && componentHasSecret(unesc, apiSecret, true) {
				return rawURL, nil
			}
		}
	}
	q.Set("apikey", cfg.APIKey)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// StripLinkAuth strips credentials from r.NZBURL when r identifies a unique
// configured indexer client. Unknown or ambiguous releases are returned
// unchanged.
func (m *Manager) StripLinkAuth(r core.Release) core.Release {
	if m == nil || strings.TrimSpace(r.NZBURL) == "" {
		return r
	}
	var found *Client
	for _, c := range m.Clients() {
		matches := c.indexerID() == r.IndexerID
		if r.IndexerID == "" {
			matches = c.Name() == r.Indexer
		}
		if !matches {
			continue
		}
		if found != nil {
			return r
		}
		found = c
	}
	if found == nil {
		return r
	}
	r.NZBURL = found.StripLinkAuth(r.NZBURL)
	return r
}
