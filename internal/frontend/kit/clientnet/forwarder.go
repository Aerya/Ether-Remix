package clientnet

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// forwarderWindow is how long an untrusted forwarding peer stays reported
// after its last request.
const forwarderWindow = 24 * time.Hour

// maxForwarders bounds how many untrusted forwarding peers are remembered.
const maxForwarders = 16

// UntrustedForwarderSighting is a home-network peer that is not a trusted
// proxy but keeps sending forwarding headers that name a public address:
// almost always a reverse proxy the owner has not trusted yet, which makes
// every viewer behind it look like they are at home.
type UntrustedForwarderSighting struct {
	Peer      netip.Addr
	Requests  int64
	FirstSeen time.Time
	LastSeen  time.Time
}

var forwarders struct {
	mu   sync.Mutex
	seen map[netip.Addr]*UntrustedForwarderSighting
}

// noteUntrustedForwarder records peer when r names a public client address in
// a forwarding header. The header is not believed; it only suggests a proxy.
func noteUntrustedForwarder(r *http.Request, peer netip.Addr) {
	if peer.IsLoopback() || !localProxyIP(peer) || !forwardsPublicClient(r) {
		return
	}
	now := time.Now()
	forwarders.mu.Lock()
	defer forwarders.mu.Unlock()
	if forwarders.seen == nil {
		forwarders.seen = make(map[netip.Addr]*UntrustedForwarderSighting)
	}
	s := forwarders.seen[peer]
	if s == nil {
		// Bound memory: a LAN host spraying headers from many addresses
		// cannot grow this past maxForwarders entries.
		if len(forwarders.seen) >= maxForwarders {
			var oldest netip.Addr
			for addr, old := range forwarders.seen {
				if !oldest.IsValid() || old.LastSeen.Before(forwarders.seen[oldest].LastSeen) {
					oldest = addr
				}
			}
			delete(forwarders.seen, oldest)
		}
		s = &UntrustedForwarderSighting{Peer: peer, FirstSeen: now}
		forwarders.seen[peer] = s
	}
	s.Requests++
	s.LastSeen = now
}

func forwardsPublicClient(r *http.Request) bool {
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	hops = append(hops, r.Header.Values("X-Real-IP")...)
	for _, v := range r.Header.Values("Forwarded") {
		for _, element := range strings.Split(v, ",") {
			for _, param := range strings.Split(element, ";") {
				if k, val, ok := strings.Cut(strings.TrimSpace(param), "="); ok && strings.EqualFold(k, "for") {
					val = strings.Trim(strings.TrimSpace(val), "\"")
					if h, _, err := net.SplitHostPort(val); err == nil {
						val = h
					}
					hops = append(hops, strings.Trim(val, "[]"))
				}
			}
		}
	}
	for _, hop := range hops {
		ip, err := netip.ParseAddr(strings.TrimSpace(hop))
		if err == nil && ip.Unmap().IsGlobalUnicast() && !LAN(ip.Unmap()) {
			return true
		}
	}
	return false
}

// UntrustedForwarders lists the untrusted forwarding peers seen within the
// last day that are still not covered by trusted, busiest first.
func UntrustedForwarders(trusted ...netip.Prefix) []UntrustedForwarderSighting {
	now := time.Now()
	forwarders.mu.Lock()
	var out []UntrustedForwarderSighting
	for addr, s := range forwarders.seen {
		if now.Sub(s.LastSeen) > forwarderWindow {
			delete(forwarders.seen, addr)
			continue
		}
		if isTrustedAddr(addr, trusted) {
			continue
		}
		out = append(out, *s)
	}
	forwarders.mu.Unlock()
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Requests > out[j-1].Requests; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// resetForwarders forgets every sighting. Intended for tests.
func resetForwarders() {
	forwarders.mu.Lock()
	forwarders.seen = nil
	forwarders.mu.Unlock()
}
