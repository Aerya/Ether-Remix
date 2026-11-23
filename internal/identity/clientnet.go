package identity

import (
	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
	"net/http"
	"net/netip"
)

// Locality describes a computed local-vs-remote source classification.
type Locality = clientnet.Locality

// LAN reports whether an address belongs to a LAN, overlay, or host interface subnet.
func LAN(ip netip.Addr) bool { return clientnet.LAN(ip) }

// Classify returns the local-vs-remote classification and reason for ip.
func Classify(ip netip.Addr) Locality { return clientnet.Classify(ip) }

// LocalityReason formats the computed local-vs-remote reason for display.
func LocalityReason(ipStr string, remote bool) string {
	return clientnet.LocalityReason(ipStr, remote)
}

// ClientNet applies the shared frontend peer/proxy trust grammar.
func ClientNet(r *http.Request, trusted ...netip.Prefix) (netip.Addr, bool) {
	return clientnet.ClientNet(r, trusted...)
}

// TrustedPeer reports whether r's connecting peer is loopback or a trusted proxy.
func TrustedPeer(r *http.Request, trusted ...netip.Prefix) bool {
	return clientnet.TrustedPeer(r, trusted...)
}

// ParseProxyEntry parses one trusted proxy IP address or CIDR prefix.
func ParseProxyEntry(raw string) (netip.Prefix, error) { return clientnet.ParseProxyEntry(raw) }

// ProxyPrefixes parses configured peer IPs and networks; invalid entries grant no trust.
func ProxyPrefixes(cidrs []string) []netip.Prefix { return clientnet.ProxyPrefixes(cidrs) }
