package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/remote"
)

type lanCheck struct {
	state, reason, fingerprint string
	checked                    time.Time
}
type lanTarget struct {
	uri    string
	source net.IP
}

// Local addresses are selected only from this machine's interfaces. Binding the
// source prevents a proxy or another route from turning a local probe into WAN evidence.
func lanTargets(urls []string, interfaces []net.Addr) []lanTarget {
	ips := map[string]net.IP{}
	for _, a := range interfaces {
		ip, _, err := net.ParseCIDR(a.String())
		if err == nil && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() {
			ips[ip.String()] = ip
		}
	}
	var out []lanTarget
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			continue
		}
		ip := net.ParseIP(u.Hostname())
		if ip == nil {
			ip, _, _ = remote.ParsePlexDirectHost(u.Hostname())
		}
		if ip == nil {
			continue
		}
		src := ips[ip.String()]
		if src == nil || seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, lanTarget{raw, src})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].uri < out[j].uri })
	return out
}
func lanFingerprint(targets []lanTarget, id string) string {
	var s strings.Builder
	s.WriteString(id)
	for _, t := range targets {
		s.WriteByte('|')
		s.WriteString(t.uri)
	}
	return s.String()
}

func probeLAN(ctx context.Context, targets []lanTarget, machine string) lanCheck {
	result := lanCheck{state: "unknown", reason: "no_addresses", checked: time.Now(), fingerprint: lanFingerprint(targets, machine)}
	if machine == "" {
		result.reason = "no_identity"
		return result
	}
	if len(targets) == 0 {
		return result
	}
	if len(targets) > 32 {
		result.reason = "too_many"
		return result
	}
	type outcome struct {
		ok     bool
		reason string
	}
	ch := make(chan outcome, len(targets))
	for _, target := range targets {
		go func(t lanTarget) {
			// In a container the advertised LAN address is the host's, not on
			// this machine's interfaces: probe it unbound, as the host sees it.
			dialer := &net.Dialer{Timeout: 3 * time.Second}
			if t.source != nil {
				dialer.LocalAddr = &net.TCPAddr{IP: t.source}
			}
			transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				// A plex.direct name spells out its IP: read it rather than ask
				// DNS, which routers with rebinding protection refuse (#279).
				var want net.IP
				if ip, _, ok := remote.ParsePlexDirectHost(host); ok && ip != nil {
					want = ip
				} else if ip := net.ParseIP(host); ip != nil {
					want = ip
				} else {
					resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
					if err != nil || len(resolved) == 0 {
						return nil, fmt.Errorf("address does not resolve")
					}
					want = resolved[0].IP
				}
				if t.source != nil && !want.Equal(t.source) {
					return nil, fmt.Errorf("address does not resolve to its local interface")
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(want.String(), port))
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			check := remote.ReachabilityChecker{Client: client, Timeout: 4 * time.Second}
			res := check.Check(ctx, []string{t.uri}, machine)
			reason := "probe_failed"
			if len(res.Results) > 0 && strings.Contains(res.Results[0].Error, "different server") {
				reason = "wrong_identity"
			} else if !res.Reachable && t.source == nil {
				// An unbound probe (the address is the container host's) that
				// fails proves nothing: many container networks cannot reach
				// their own host's LAN address.
				reason = "container"
			}
			ch <- outcome{res.Reachable, reason}
		}(target)
	}
	verified := 0
	unverified := false
	failReason := ""
	for range targets {
		o := <-ch
		switch {
		case o.ok:
			verified++
		case o.reason == "wrong_identity":
			failReason = "wrong_identity"
		case o.reason == "container":
			unverified = true
		default:
			if failReason == "" {
				failReason = o.reason
			}
		}
	}
	switch {
	case failReason == "wrong_identity":
		result.state, result.reason = "unreachable", "wrong_identity"
	case verified > 0:
		result.state, result.reason = "reachable", "verified"
	case unverified && failReason == "":
		result.state, result.reason = "unknown", "container"
	default:
		if failReason == "" {
			failReason = "probe_failed"
		}
		result.state, result.reason = "unreachable", failReason
	}
	return result
}

// lanInterfaceAddrs returns real LAN interface CIDRs, excluding virtual bridges,
// point-to-point VPN tunnels, loopback, link-local, and CGNAT interfaces.
func lanInterfaceAddrs() []net.Addr {
	addrs, err := remote.LANAddresses(remote.LANOptions{IncludeIPv6: true})
	if err != nil {
		raw, _ := net.InterfaceAddrs()
		return raw
	}
	out := make([]net.Addr, 0, len(addrs))
	for _, a := range addrs {
		if a.Net != nil {
			out = append(out, a.Net)
		}
	}
	return out
}

func (r *frontendRuntime) lanInputs() ([]lanTarget, string) {
	r.mu.Lock()
	addr := r.mainAddr
	r.mu.Unlock()
	var urls []string
	addresses := lanInterfaceAddrs()
	v4Only := false
	if addr != nil {
		if host, _, err := net.SplitHostPort(addr.String()); err == nil && strings.TrimSpace(host) == "0.0.0.0" {
			v4Only = true
		}
		urls = append(urls, connectionURLs(addr.String(), "", addresses)...)
	}
	var advertised []string
	if r.app.registrar != nil {
		for _, c := range r.app.registrar.Status().Connections {
			if c.Local && !c.Custom {
				if ip := net.ParseIP(c.Address); ip != nil && remote.IsCGNAT(ip) {
					continue
				}
				if v4Only && c.IPv6 {
					continue
				}
				advertised = append(advertised, c.URI)
				urls = append(urls, c.URI)
			}
		}
	}
	targets := lanTargets(urls, addresses)
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		seen[target.uri] = true
	}
	for _, uri := range advertised {
		if !seen[uri] {
			targets = append(targets, lanTarget{uri: uri})
			seen[uri] = true
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].uri < targets[j].uri })
	return targets, r.app.cfg.Get().Server.MachineIdentifier
}
func (r *frontendRuntime) refreshLAN() {
	r.mu.Lock()
	if r.lanChecking || r.closed {
		r.mu.Unlock()
		return
	}
	r.lanChecking = true
	generation := r.lanGeneration
	r.mu.Unlock()
	go func() {
		targets, id := r.lanInputs()
		result := probeLAN(r.ctx, targets, id)
		r.mu.Lock()
		if generation == r.lanGeneration && !r.closed {
			r.lan = result
		}
		r.lanChecking = false
		r.mu.Unlock()
	}()
}
