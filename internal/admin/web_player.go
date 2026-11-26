package admin

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
)

func (h *Handler) webPlayer(w http.ResponseWriter, r *http.Request) {
	if h.deps.IdentityGrants == nil || h.deps.Frontends == nil {
		writeError(w, 503, "unavailable", "Web player unavailable")
		return
	}
	var viewer *core.Viewer
	for _, surface := range []string{"owner", "viewer-portal"} {
		v, err := h.deps.IdentityGrants.Resolve(r.Context(), surface, tokenFromRequest(r))
		if err == nil {
			viewer = v
			break
		}
	}
	// An owner signed in through Plex (or with an owner session) holds a
	// credential identity grants do not resolve; requireAuth admits it via
	// isOwner, so the web player must too, as the owner's own viewer.
	if viewer == nil && h.isOwner(r, tokenFromRequest(r)) && h.deps.Users != nil {
		if users, err := h.deps.Users.Users(r.Context()); err == nil {
			for i := range users {
				if users[i].IsOwner {
					u := users[i]
					viewer = &u
					break
				}
			}
		}
	}
	if viewer == nil {
		writeError(w, 401, "unauthorized", "Sign in required")
		return
	}
	target := r.URL.Query().Get("app")
	if target == "" {
		target = "jellyfin"
	}
	if target != "jellyfin" && target != "vela" {
		writeError(w, 400, "invalid_app", "Unknown web player")
		return
	}
	infos, err := h.deps.Frontends.List(r.Context())
	origins := map[string]string{}
	if err == nil {
		for _, info := range infos {
			origin := ""
			if info.Descriptor.ID == "vela" {
				if info.Enabled && info.Status.Mounted && info.Status.WebPlayerReady {
					if path, ok := h.sameOriginAppPath(r, "vela", "/vela"); ok {
						origin = path + "/"
					} else if u, e := h.resolveAppAddress(r.Context(), r, "vela", "/vela", false); e == nil {
						origin = strings.TrimRight(u.String(), "/") + "/"
					} else {
						var cfg config.Config
						if h.deps.Config != nil {
							cfg = h.deps.Config.Get()
						}
						if sharedPrefixActive(cfg, "vela", "/vela") {
							if reqOrigin, valid := requestOriginInfo(r, h.proxyPrefixes()); valid {
								host := mainPortHost(reqOrigin.Host, cfg)
								if host == reqOrigin.Host {
									origin = reqOrigin.BaseURL("/vela") + "/"
								} else if !viaProxy(r) {
									origin = originURL(reqOrigin.Scheme, host, "/vela") + "/"
								}
							}
						}
						if origin == "" {
							info = h.presentConnectURLs(r, info)
							for _, raw := range info.Status.ConnectURLs {
								if u, e := url.Parse(raw); e == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
									origin = strings.TrimRight(raw, "/") + "/"
									break
								}
							}
						}
					}
				}
				if origin != "" {
					origins["vela"] = origin
				}
				continue
			}

			// The web client downloads on first use: /web answers a
			// self-refreshing "Preparing" page meanwhile, so the button must
			// not wait for an install nobody has triggered yet.
			if info.Descriptor.ID != "jellyfin" || !info.Enabled || !info.Status.Mounted {
				continue
			}
			info = h.presentConnectURLs(r, info)
			reqOrigin, valid := requestOriginInfo(r, h.proxyPrefixes())
			// Use the shared app address resolver first (honouring published
			// addresses and reverse-proxy layouts). For a direct local HTTP
			// console with no published address, prefer the native listener
			// port on the browser's current host.
			hasPublished := h.deps.Config != nil && len(frontendPublishedURLs(h.deps.Config.Get(), "jellyfin")) > 0
			if path, ok := h.sameOriginAppPath(r, "jellyfin", "/jellyfin"); ok {
				origin = path
			}
			if origin == "" && !hasPublished && valid && reqOrigin.Scheme == "http" && !viaProxy(r) {
				hostname := reqOrigin.Hostname()
				for _, address := range info.Status.Addresses {
					if _, port, e := net.SplitHostPort(address); e == nil {
						origin = (&url.URL{Scheme: "http", Host: net.JoinHostPort(hostname, port)}).String()
						break
					}
				}
			}
			if origin == "" {
				if u, e := h.resolveAppAddress(r.Context(), r, "jellyfin", "/jellyfin", false); e == nil {
					origin = strings.TrimRight(u.String(), "/")
				}
			}
			if origin == "" && valid {
				origin = reqOrigin.BaseURL("/jellyfin")
			}

			if origin == "" {
				for _, raw := range info.Status.ConnectURLs {
					u, e := url.Parse(raw)
					if e == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
						origin = strings.TrimRight(raw, "/")
						break
					}
				}
			}
			if origin != "" {
				origins["jellyfin"] = origin + "/web/ether-signin"
			}
		}
	}
	if r.URL.Query().Get("app") == "" && origins[target] == "" {
		target = "vela"
	}
	if origins[target] == "" {
		writeError(w, 503, "unavailable", "Web player unavailable")
		return
	}
	type player struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	players := func(code string) []player {
		out := make([]player, 0, 2)
		for _, id := range []string{"vela", "jellyfin"} {
			if origin := origins[id]; origin != "" {
				if code != "" {
					origin += "#code=" + url.QueryEscape(code)
				}
				out = append(out, player{ID: id, URL: origin})
			}
		}
		return out
	}
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"available": true, "players": players("")})
		return
	}
	if !csrfOK(r) {
		writeError(w, 403, "csrf", "Request rejected")
		return
	}
	code, err := h.deps.IdentityGrants.IssueWebCode(r.Context(), viewer.ID)
	if err != nil {
		writeError(w, 503, "unavailable", "Sign-in unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"url": origins[target] + "#code=" + url.QueryEscape(code), "players": players(code)})
}

// sameOriginAppPath returns the root-relative path of an app Ether serves on
// the port the console was loaded from, for the console to resolve against its
// own origin. A browser that reached the console behind a reverse proxy or a
// tunnel can always reach this same Ether there, while the host Ether sees
// (a loopback tunnel end, a proxy's upstream name) may mean nothing to it.
// It applies only when the owner published no address for the app (a
// published address wins), the app serves its shared prefix on the main port,
// and the request is proxied or arrived on a loopback host. A direct LAN
// console keeps the resolved absolute address.
func (h *Handler) sameOriginAppPath(r *http.Request, id, prefix string) (string, bool) {
	if h.deps.Config == nil {
		return "", false
	}
	cfg := h.deps.Config.Get()
	if len(frontendPublishedURLs(cfg, id)) > 0 || !sharedPrefixActive(cfg, id, prefix) {
		return "", false
	}
	if !viaProxy(r) && !loopbackHost(r.Host) {
		return "", false
	}
	// The shared prefixes live on the main port, not a dedicated console port.
	if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && cfg.Server.ConsolePort > 0 && cfg.Server.ConsolePort != cfg.Server.Port {
		if _, port, err := net.SplitHostPort(local.String()); err == nil && port == strconv.Itoa(cfg.Server.ConsolePort) {
			return "", false
		}
	}
	forwarded := ""
	if origin, ok := requestOriginInfo(r, h.proxyPrefixes()); ok {
		forwarded = origin.Prefix
	}
	return clientnet.JoinPrefix(forwarded, prefix), true
}

func loopbackHost(host string) bool {
	h := strings.ToLower(hostOnly(host))
	if h == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && ip.Unmap().IsLoopback()
}
