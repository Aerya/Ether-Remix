package admin

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestProxyNameOffersTheContainerNameOnlyWhenItResolvesBack(t *testing.T) {
	prev := proxyHintLookups
	t.Cleanup(func() { proxyHintLookups = prev })
	ptr := map[string][]string{
		"172.18.0.2": {"nginx-proxy-manager.jellyfin_network."},
		"172.18.0.3": {"stale-name.jellyfin_network."},
	}
	forward := map[string][]netip.Addr{
		"nginx-proxy-manager": {netip.MustParseAddr("172.18.0.2")},
		"stale-name":          {netip.MustParseAddr("172.18.0.77")},
	}
	proxyHintLookups.addr = func(_ context.Context, addr string) ([]string, error) {
		if names, ok := ptr[addr]; ok {
			return names, nil
		}
		return nil, errors.New("no PTR")
	}
	proxyHintLookups.host = func(_ context.Context, host string) ([]netip.Addr, error) {
		if addrs, ok := forward[host]; ok {
			return addrs, nil
		}
		return nil, errors.New("no such host")
	}
	cases := map[string]string{
		"172.18.0.2": "nginx-proxy-manager",
		"172.18.0.3": "", // the name points somewhere else now
		"172.18.0.4": "", // no reverse name at all
	}
	for addr, want := range cases {
		if got := proxyName(context.Background(), netip.MustParseAddr(addr)); got != want {
			t.Errorf("proxyName(%s) = %q, want %q", addr, got, want)
		}
	}
}
