package remote

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
)

// Secure-connection modes, matching config.Plex.SecureConnections and PMS's
// "Secure connections" preference.
const (
	SecureRequired  = "required"
	SecurePreferred = "preferred"
	SecureDisabled  = "disabled"
)

// Connection is one way to reach the server, in the shape plex.tv's
// resources API returns it.
type Connection struct {
	Protocol string `json:"protocol"` // "http" or "https"
	Address  string `json:"address"`  // IP or host
	Port     int    `json:"port"`
	URI      string `json:"uri"`
	Local    bool   `json:"local"`
	Relay    bool   `json:"relay,omitempty"`
	IPv6     bool   `json:"IPv6,omitempty"`
	Custom   bool   `json:"custom,omitempty"` // owner-supplied custom URL
}

// PlexDirectHost returns the plex.direct host for ip under a certificate
// hash: 192.168.1.5 -> "192-168-1-5.<hash>.plex.direct". IPv6 addresses use
// their expanded colon groups joined by dashes, as plex.tv does.
func PlexDirectHost(ip net.IP, hash string) string {
	return PlexDirectLabel(ip) + "." + strings.ToLower(hash) + ".plex.direct"
}

// PlexDirectLabel is the IP-encoding first label of a plex.direct host.
func PlexDirectLabel(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return strings.ReplaceAll(v4.String(), ".", "-")
	}
	ip16 := ip.To16()
	if ip16 == nil {
		return ""
	}
	parts := make([]string, 8)
	for i := 0; i < 8; i++ {
		parts[i] = strconv.FormatUint(uint64(ip16[2*i])<<8|uint64(ip16[2*i+1]), 16)
	}
	return strings.Join(parts, "-")
}

// ParsePlexDirectHost reverses PlexDirectHost. ok is false for hosts that
// are not "<ip-label>.<hash>.plex.direct".
func ParsePlexDirectHost(host string) (ip net.IP, hash string, ok bool) {
	return clientnet.ParsePlexDirectHost(host)
}

// BuildOptions describes the server's addresses for BuildConnections.
type BuildOptions struct {
	LAN        []net.IP
	WAN        net.IP // nil when unknown or remote access is off
	LocalPort  int    // port the server listens on (32400)
	PublicPort int    // external port on the WAN; 0 = LocalPort
	// CertHash is the plex.direct hash of a valid plex.direct certificate.
	// Without it no https plex.direct URIs are produced.
	CertHash string
	// Secure is SecureRequired, SecurePreferred (default) or SecureDisabled.
	Secure string
	// CustomURLs are owner-supplied URLs (reverse proxies, DDNS names).
	CustomURLs []string
	// HTTPSOnPlainIP adds https://<ip>:port URIs (self-signed setups). Off
	// by default because clients cannot validate a certificate for a bare IP.
	HTTPSOnPlainIP bool
}

// BuildConnections returns the connection list for the server, best first:
// LAN before WAN, https plex.direct before plain http (unless secure
// connections are disabled), then custom URLs.
func BuildConnections(o BuildOptions) []Connection {
	if o.LocalPort == 0 {
		o.LocalPort = 32400
	}
	if o.PublicPort == 0 {
		o.PublicPort = o.LocalPort
	}
	secure := o.Secure
	if secure == "" {
		secure = SecurePreferred
	}
	var parsedCustom []Connection
	hasCustomHTTPS := false
	for _, raw := range o.CustomURLs {
		c, err := ParseCustomURL(raw)
		if err != nil {
			continue
		}
		if secure == SecureDisabled && c.Protocol == "https" {
			continue
		}
		if c.Protocol == "https" {
			hasCustomHTTPS = true
		}
		parsedCustom = append(parsedCustom, c)
	}
	wantHTTPS := secure != SecureDisabled && o.CertHash != ""
	wantHTTP := secure != SecureRequired || (o.CertHash == "" && !o.HTTPSOnPlainIP && !hasCustomHTTPS)

	var out []Connection
	seen := map[string]bool{}
	add := func(c Connection) {
		if c.URI == "" || seen[c.URI] {
			return
		}
		seen[c.URI] = true
		out = append(out, c)
	}
	addIP := func(ip net.IP, port int, local bool) {
		if ip == nil {
			return
		}
		local = local || IsCGNAT(ip)
		v6 := ip.To4() == nil
		addr := ip.String()
		if wantHTTPS {
			host := PlexDirectHost(ip, o.CertHash)
			add(Connection{Protocol: "https", Address: addr, Port: port, Local: local, IPv6: v6,
				URI: "https://" + net.JoinHostPort(host, strconv.Itoa(port))})
		}
		if o.HTTPSOnPlainIP && secure != SecureDisabled && !wantHTTPS {
			add(Connection{Protocol: "https", Address: addr, Port: port, Local: local, IPv6: v6,
				URI: "https://" + net.JoinHostPort(addr, strconv.Itoa(port))})
		}
		if wantHTTP {
			add(Connection{Protocol: "http", Address: addr, Port: port, Local: local, IPv6: v6,
				URI: "http://" + net.JoinHostPort(addr, strconv.Itoa(port))})
		}
	}
	for _, ip := range o.LAN {
		addIP(ip, o.LocalPort, true)
	}
	if o.WAN != nil {
		addIP(o.WAN, o.PublicPort, false)
	}
	for _, c := range parsedCustom {
		// A container on a bridge network only sees its own address, so the
		// owner names the host's LAN IP here. Publish its plex.direct form too:
		// a browser client cannot use the plain-IP URL securely.
		if ip := net.ParseIP(c.Address); ip != nil && c.Local && wantHTTPS {
			if v4 := ip.To4(); v4 != nil {
				ip = v4
			}
			add(Connection{Protocol: "https", Address: ip.String(), Port: c.Port, Local: true, IPv6: c.IPv6, Custom: true,
				URI: "https://" + net.JoinHostPort(PlexDirectHost(ip, o.CertHash), strconv.Itoa(c.Port))})
		}
		if secure == SecureRequired && c.Protocol == "http" && (wantHTTPS || o.HTTPSOnPlainIP || hasCustomHTTPS) {
			continue
		}
		add(c)
	}
	return out
}

// PublishedAddress is a validated owner-supplied URL for reaching an app.
// Unlike Plex's CustomURLs, it may include a path prefix (for example
// https://domain.com/jellyfin as well as https://jellyfin.domain.com).
type PublishedAddress struct {
	Scheme string // "http" or "https"
	Host   string // hostname or IP literal without port or brackets
	Port   int
	Path   string // "" or "/jellyfin" (never trailing slash)
	URI    string // normalized URL without trailing slash
	Local  bool
	IPv6   bool
}

// ParsePublishedAddress validates an optional per-app published address,
// supporting both subdomain roots and path prefixes.
func ParsePublishedAddress(raw string) (PublishedAddress, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return PublishedAddress{}, fmt.Errorf("published address %q: empty", raw)
	}
	if len(s) > 512 || strings.HasPrefix(s, "[") || strings.ContainsAny(s, " \t\r\n()<>\"'\\|?#") {
		return PublishedAddress{}, fmt.Errorf("published address %q: must be an http(s) URL", raw)
	}
	u, err := url.Parse(s)
	if err != nil {
		return PublishedAddress{}, err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return PublishedAddress{}, fmt.Errorf("published address %q: scheme must be http or https", raw)
	}
	if u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return PublishedAddress{}, fmt.Errorf("published address %q: must not include credentials, query or fragment", raw)
	}
	host := u.Hostname()
	if host == "" {
		return PublishedAddress{}, fmt.Errorf("published address %q: missing host", raw)
	}
	if strings.HasSuffix(u.Host, ":") {
		return PublishedAddress{}, fmt.Errorf("published address %q: bad port", raw)
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() || (ip.To4() != nil && strings.ContainsAny(u.Host, "[]")) {
			return PublishedAddress{}, fmt.Errorf("published address %q: invalid host", raw)
		}
	} else if strings.EqualFold(host, "localhost") || strings.ContainsAny(u.Host, "[]") || !isValidDNSName(host) {
		return PublishedAddress{}, fmt.Errorf("published address %q: invalid host", raw)
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	rawPort := u.Port()
	if rawPort != "" {
		n, err := strconv.Atoi(rawPort)
		if err != nil || n <= 0 || n > 65535 {
			return PublishedAddress{}, fmt.Errorf("published address %q: bad port", raw)
		}
		port = n
	}
	cleanPath := ""
	if u.Path != "" && u.Path != "/" {
		trimmed := strings.TrimRight(u.Path, "/")
		if !strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "//") {
			return PublishedAddress{}, fmt.Errorf("published address %q: invalid path", raw)
		}
		for _, seg := range strings.Split(trimmed[1:], "/") {
			if seg == "" || seg == "." || seg == ".." {
				return PublishedAddress{}, fmt.Errorf("published address %q: invalid path", raw)
			}
		}
		cleanPath = trimmed
	}
	lowerHost := strings.ToLower(host)
	u.Host = lowerHost
	if rawPort != "" {
		u.Host = net.JoinHostPort(lowerHost, rawPort)
	} else if ip != nil && ip.To4() == nil {
		u.Host = "[" + lowerHost + "]"
	}
	u.Path = cleanPath
	u.RawPath = ""
	u.RawQuery, u.Fragment = "", ""
	resolvedIP := ip
	if resolvedIP == nil {
		if pdIP, _, ok := ParsePlexDirectHost(lowerHost); ok {
			resolvedIP = pdIP
		}
	}
	return PublishedAddress{
		Scheme: u.Scheme,
		Host:   lowerHost,
		Port:   port,
		Path:   cleanPath,
		URI:    strings.TrimRight(u.String(), "/"),
		Local:  resolvedIP != nil && (resolvedIP.IsPrivate() || resolvedIP.IsLoopback() || IsCGNAT(resolvedIP)),
		IPv6:   resolvedIP != nil && resolvedIP.To4() == nil,
	}, nil
}

// ParsePublishedURL validates raw and returns its normalized URL string.
func ParsePublishedURL(raw string) (string, error) {
	a, err := ParsePublishedAddress(raw)
	if err != nil {
		return "", err
	}
	return a.URI, nil
}

// ParseCustomURL validates an owner-supplied connection URL.
func ParseCustomURL(raw string) (Connection, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Connection{}, fmt.Errorf("custom url %q: empty", raw)
	}
	if len(s) > 512 || strings.HasPrefix(s, "[") || strings.ContainsAny(s, " \t\r\n()<>\"'\\|?#") {
		return Connection{}, fmt.Errorf("custom url %q: must be an http(s) server URL", raw)
	}
	u, err := url.Parse(s)
	if err != nil {
		return Connection{}, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Connection{}, fmt.Errorf("custom url %q: scheme must be http or https", raw)
	}
	if u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return Connection{}, fmt.Errorf("custom url %q: must not include credentials, query or fragment", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return Connection{}, fmt.Errorf("custom url %q: must not include a path", raw)
	}
	host := u.Hostname()
	if host == "" {
		return Connection{}, fmt.Errorf("custom url %q: missing host", raw)
	}
	if strings.HasSuffix(u.Host, ":") {
		return Connection{}, fmt.Errorf("custom url %q: bad port", raw)
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.To4() != nil && strings.ContainsAny(u.Host, "[]") {
			return Connection{}, fmt.Errorf("custom url %q: invalid host", raw)
		}
	} else if strings.ContainsAny(u.Host, "[]") || !isValidDNSName(host) {
		return Connection{}, fmt.Errorf("custom url %q: invalid host", raw)
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return Connection{}, fmt.Errorf("custom url %q: bad port", raw)
		}
		port = n
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery, u.Fragment = "", ""
	resolvedIP := ip
	if resolvedIP == nil {
		if pdIP, _, ok := ParsePlexDirectHost(host); ok {
			resolvedIP = pdIP
		}
	}
	return Connection{
		Protocol: u.Scheme,
		Address:  host,
		Port:     port,
		URI:      u.String(),
		Local:    resolvedIP != nil && (resolvedIP.IsPrivate() || resolvedIP.IsLoopback() || IsCGNAT(resolvedIP)),
		IPv6:     resolvedIP != nil && resolvedIP.To4() == nil,
		Custom:   true,
	}, nil
}

// IsPublishableURI reports whether raw is a valid connection URI that may be
// published to plex.tv. CGNAT (100.64.0.0/10, including Tailscale) addresses
// remain usable locally but are never sent to plex.tv.
func IsPublishableURI(raw string) bool {
	c, err := ParseCustomURL(raw)
	if err != nil {
		return false
	}
	if ip := net.ParseIP(c.Address); ip != nil {
		return !IsCGNAT(ip)
	}
	if ip, _, ok := ParsePlexDirectHost(c.Address); ok {
		return !IsCGNAT(ip)
	}
	return true
}

func isValidDNSName(host string) bool {
	if host == "" || len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	allDigits := true
	for i := 0; i < len(last); i++ {
		if last[i] < '0' || last[i] > '9' {
			allDigits = false
			break
		}
	}
	return !allDigits
}
