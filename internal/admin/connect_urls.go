package admin

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
	"github.com/braedonsaunders/ether/internal/remote"
)

// dockerBridge is the range Docker uses for bridge networks (172.16.0.0/12).
var dockerBridge = netip.MustParsePrefix("172.16.0.0/12")

// sharedPrefix is the path a protocol also serves on Ether's own port.
func sharedPrefix(id string) string {
	return consoleFrontendPolicy(id).SharedPrefix
}

// sharedPrefixActive reports whether frontend id currently serves its shared
// path prefix on Ether's main port.
func sharedPrefixActive(cfg config.Config, id, prefix string) bool {
	if prefix == "" {
		return false
	}
	if id == "vela" && cfg.FrontendPorts["vela"] > 0 && !vela.ServeOnMainPort(cfg.Frontends["vela"].Settings) {
		return false
	}
	return true
}

// mainPortHost maps a request host on a dedicated console port back to Ether's
// main port when synthesizing LAN main-listener or shared-path URLs.
func mainPortHost(host string, cfg config.Config) string {
	if cfg.Server.ConsolePort > 0 && cfg.Server.Port > 0 && originPort(host) == strconv.Itoa(cfg.Server.ConsolePort) {
		return net.JoinHostPort(hostOnly(host), strconv.Itoa(cfg.Server.Port))
	}
	return host
}

// consoleConnectURLs returns the addresses the owner console presents for an
// app: explicit published addresses for this app (if any), followed by its
// listener / LAN addresses. It never invents domain.com/<app> from Plex's
// custom URLs or from a public console hostname, and never offers a container
// bridge address.
func consoleConnectURLs(r *http.Request, id string, urls []string, cfg config.Config, trusted []netip.Prefix) []string {
	origin, ok := requestOriginInfo(r, trusted)
	scheme, host := origin.Scheme, origin.Host
	localConsole := ok && localConsoleHost(origin.Hostname())
	seen := map[string]bool{}
	var out []string
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			return
		}
		seen[raw] = true
		out = append(out, raw)
	}
	prefix := sharedPrefix(id)
	published := frontendPublishedURLs(cfg, id)
	for _, pub := range published {
		add(pub)
	}
	mainHost := mainPortHost(host, cfg)
	// Plex is the process the owner already opened. A bridge container's
	// own address is not "this computer only". Plex apps at home connect
	// straight to the server, so a reverse proxy or the public host name is
	// never the at-home address, nor is the http:// guess for it (#381).
	if consoleFrontendPolicy(id).MainListener && ok && !viaProxy(r) && !publicRouteHost(cfg, hostOnly(mainHost)) {
		add(originURL(scheme, mainHost, ""))
	}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		if containerHost(u.Hostname()) && !sameHost(u.Host, host) {
			path := u.EscapedPath()
			if localConsole && path != "" && path != "/" {
				add(originURL(scheme, mainHost, path))
			}
			continue
		}
		add(raw)
	}
	if localConsole && sharedPrefixActive(cfg, id, prefix) {
		add(originURL(scheme, mainHost, prefix))
	}
	ranked := rankConnectURLs(out, mainHost, prefix)
	if len(published) > 0 {
		// Keep owner-configured published addresses first, in configured order.
		pubSet := make(map[string]bool, len(published))
		for _, pub := range published {
			pubSet[pub] = true
		}
		rest := make([]string, 0, len(ranked))
		for _, candidate := range ranked {
			if !pubSet[candidate] {
				rest = append(rest, candidate)
			}
		}
		ranked = append(append([]string(nil), published...), rest...)
	}
	return ranked
}

// frontendPublishedURLs returns the app's configured published addresses
// (supporting both subdomain roots and path prefixes). Plex never uses this
// list: Plex apps keep using Plex.CustomURLs.
func frontendPublishedURLs(cfg config.Config, id string) []string {
	if !consoleFrontendPolicy(id).PublicURL {
		return nil
	}
	rawList := cfg.Frontends[id].EffectivePublishedAddresses()
	if len(rawList) == 0 {
		return nil
	}
	out := make([]string, 0, len(rawList))
	seen := make(map[string]bool, len(rawList))
	for _, raw := range rawList {
		p, err := remote.ParsePublishedAddress(raw)
		if err != nil || containerHost(p.Host) {
			continue
		}
		if !seen[p.URI] {
			seen[p.URI] = true
			out = append(out, p.URI)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// publishedMainOrigin returns the first external published main-server origin
// configured in Plex.CustomURLs or Plex.PublicHost, used for out-of-band links
// such as viewer invites when no app-scoped request is in hand.
func publishedMainOrigin(cfg config.Config) (string, bool) {
	for _, raw := range cfg.Plex.CustomURLs {
		c, err := remote.ParseCustomURL(strings.TrimSpace(raw))
		if err != nil || c.Local || containerHost(c.Address) {
			continue
		}
		return strings.TrimRight(c.URI, "/"), true
	}
	if host := strings.TrimSpace(cfg.Plex.PublicHost); host != "" {
		candidate := host
		if !strings.HasPrefix(candidate, "http://") && !strings.HasPrefix(candidate, "https://") {
			scheme := "https"
			if cfg.Plex.PublicPort > 0 && cfg.Plex.PublicPort != 443 && cfg.Plex.SecureConnections == remote.SecureDisabled {
				scheme = "http"
			}
			if _, _, err := net.SplitHostPort(candidate); err != nil && cfg.Plex.PublicPort > 0 && !((scheme == "https" && cfg.Plex.PublicPort == 443) || (scheme == "http" && cfg.Plex.PublicPort == 80)) {
				candidate = net.JoinHostPort(strings.Trim(candidate, "[]"), strconv.Itoa(cfg.Plex.PublicPort))
			}
			candidate = scheme + "://" + candidate
		}
		if p, err := remote.ParsePublishedAddress(candidate); err == nil && !p.Local && !containerHost(p.Host) {
			return p.URI, true
		}
	}
	return "", false
}

// publishedPortalURL returns the viewer portal URL (/ether/me) on the
// server's external published main origin, using the same source and
// dedicated-console-port rules as inviteURL. It returns "" when no published
// main origin serves the viewer portal.
func (h *Handler) publishedPortalURL() string {
	if h.deps.Config == nil {
		return ""
	}
	cfg := h.deps.Config.Get()
	if cfg.Server.ConsolePort > 0 && cfg.Server.DisableConsoleOnMainPort {
		return ""
	}
	if base, ok := publishedMainOrigin(cfg); ok {
		return strings.TrimRight(base, "/") + "/ether/me"
	}
	return ""
}

func (h *Handler) inviteURL(secret string) string {
	if portal := h.publishedPortalURL(); portal != "" {
		return portal + "#invite=" + secret
	}
	return "/ether/me#invite=" + secret
}

func localConsoleHost(host string) bool {
	h := strings.TrimSpace(strings.Trim(host, "[]"))
	if h == "" {
		return false
	}
	lower := strings.ToLower(h)
	if lower == "localhost" || strings.HasSuffix(lower, ".local") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if containerHost(ip.String()) {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || tailscaleNet.Contains(ip)
}

// rankConnectURLs puts the address the owner can hand to a device first.
// For protocols with a dedicated listener port, that listener beats the
// shared-port fallback path on Ether's main port. Home LAN (192.168.0.0/16)
// beats other private ranges, which are often VPNs. Loopback stays available
// but is not the default when a better address exists.
func rankConnectURLs(urls []string, originHost, prefix string) []string {
	type scored struct {
		url   string
		score int
		index int
	}
	rows := make([]scored, len(urls))
	for i, raw := range urls {
		rows[i] = scored{url: raw, score: connectScore(raw, originHost, prefix), index: i}
	}
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		j := i
		for j > 0 && (rows[j-1].score < row.score || (rows[j-1].score == row.score && rows[j-1].index > row.index)) {
			rows[j] = rows[j-1]
			j--
		}
		rows[j] = row
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.url
	}
	return out
}

var tailscaleNet = netip.MustParsePrefix("100.64.0.0/10")

func connectScore(raw, originHost, prefix string) int {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return -1
	}
	host := hostOnly(u.Hostname())
	score := 15
	ip, ipErr := netip.ParseAddr(host)
	if ipErr != nil {
		if strings.EqualFold(host, "localhost") {
			score = 0
		} else {
			score = 70
		}
	} else {
		ip = ip.Unmap()
		switch {
		case ip.IsLoopback():
			score = 0
		case ip.Is4() && homeLAN(ip):
			score = 80
		case tailscaleNet.Contains(ip):
			score = 20
		case ip.IsPrivate():
			score = 40
		case ip.Is6():
			score = 10
		}
	}
	if originHost != "" && sameHost(host, originHost) && score >= 40 {
		score += 25
	}
	// A dedicated listener (root path on its own port) always beats the
	// shared-port fallback path (/jellyfin, /emby, /silo) on the same tier.
	if prefix != "" && (u.Path == "" || u.Path == "/") && score > 0 {
		score += 15
	} else if originPort(originHost) != "" && originPort(originHost) == u.Port() && score >= 40 {
		// For main-listener and shared-only apps, prefer the port the console is open on.
		score += 5
	}
	return score
}

func originPort(hostport string) string {
	_, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return ""
	}
	return port
}

func homeLAN(ip netip.Addr) bool {
	b := ip.As4()
	return b[0] == 192 && b[1] == 168
}

// viaProxy reports whether the request carries reverse-proxy headers, so
// the browser did not reach Ether's own listener directly.
func viaProxy(r *http.Request) bool {
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Prefix", "X-Real-Ip"} {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// publicRouteHost reports whether host is the owner's public host name or
// the host of an outside custom or published URL: addresses for away from home.
func publicRouteHost(cfg config.Config, host string) bool {
	if host == "" {
		return false
	}
	public := strings.TrimSpace(cfg.Plex.PublicHost)
	if _, rest, ok := strings.Cut(public, "://"); ok {
		public = rest
	}
	public = strings.TrimSuffix(public, "/")
	if public != "" && sameHost(public, host) {
		return true
	}
	for _, raw := range cfg.Plex.CustomURLs {
		if c, err := remote.ParseCustomURL(raw); err == nil && !c.Local && strings.EqualFold(c.Address, host) {
			return true
		}
	}
	for _, fe := range cfg.Frontends {
		for _, raw := range fe.EffectivePublishedAddresses() {
			if p, err := remote.ParsePublishedAddress(raw); err == nil && !p.Local && strings.EqualFold(p.Host, host) {
				return true
			}
		}
	}
	return false
}

func requestOriginInfo(r *http.Request, trusted []netip.Prefix) (clientnet.RequestOrigin, bool) {
	origin, ok := clientnet.Origin(r, trusted...)
	if !ok || containerHost(origin.Hostname()) {
		return clientnet.RequestOrigin{}, false
	}
	return origin, true
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(hostport, "[]")
}

func containerHost(host string) bool {
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	return dockerBridge.Contains(ip.Unmap())
}

func sameHost(a, b string) bool {
	return strings.EqualFold(hostOnly(a), hostOnly(b))
}

func originURL(scheme, host, path string) string {
	u := &url.URL{Scheme: scheme, Host: host}
	if path != "" {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		u.Path = path
	}
	return u.String()
}
