package clientnet

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"testing"
)

func TestLANAndClassifyCoverLocalRangesAndHostPrefixes(t *testing.T) {
	restore := OverrideHostPrefixes([]netip.Prefix{
		netip.MustParsePrefix("198.18.10.0/24"),
		netip.MustParsePrefix("2001:db8:abcd:1::/64"),
	})
	t.Cleanup(restore)

	cases := []struct {
		ip         string
		wantLocal  bool
		wantReason string
	}{
		{"127.0.0.1", true, "Home network (source 127.0.0.1)"},
		{"::1", true, "Home network (source ::1)"},
		{"192.168.3.10", true, "Home network (source 192.168.3.10)"},
		{"10.0.0.5", true, "Home network (source 10.0.0.5)"},
		{"172.16.4.20", true, "Home network (source 172.16.4.20)"},
		{"::ffff:192.168.3.10", true, "Home network (source 192.168.3.10)"},
		{"fd12:3456:789a::42", true, "Home network (source fd12:3456:789a::42)"},
		{"fe80::1", true, "Home network (source fe80::1)"},
		{"169.254.10.20", true, "Home network (source 169.254.10.20)"},
		{"100.64.1.5", true, "Home network (source 100.64.1.5)"},
		{"198.18.10.42", true, "Home network (source 198.18.10.42, local subnet 198.18.10.0/24)"},
		{"198.18.11.42", false, "Remote network (source 198.18.11.42)"},
		{"2001:db8:abcd:1::42", true, "Home network (source 2001:db8:abcd:1::42, local subnet 2001:db8:abcd:1::/64)"},
		{"2001:db8:abcd:2::42", false, "Remote network (source 2001:db8:abcd:2::42)"},
		{"203.0.113.10", false, "Remote network (source 203.0.113.10)"},
		{"0.0.0.0", false, "Remote network (source 0.0.0.0)"},
	}
	for _, tc := range cases {
		ip := netip.MustParseAddr(tc.ip)
		got := Classify(ip)
		if got.Local != tc.wantLocal || LAN(ip) != tc.wantLocal {
			t.Errorf("Classify(%s).Local = %v, want %v", tc.ip, got.Local, tc.wantLocal)
		}
		if got.Reason != tc.wantReason {
			t.Errorf("Classify(%s).Reason = %q, want %q", tc.ip, got.Reason, tc.wantReason)
		}
		if reason := LocalityReason(tc.ip, !tc.wantLocal); reason != tc.wantReason {
			t.Errorf("LocalityReason(%s, %v) = %q, want %q", tc.ip, !tc.wantLocal, reason, tc.wantReason)
		}
	}
	if got := LocalityReason("", false); got != "Home network" {
		t.Errorf("LocalityReason empty local = %q", got)
	}
	if got := LocalityReason("", true); got != "Remote network" {
		t.Errorf("LocalityReason empty remote = %q", got)
	}
}

func TestPrefixesFromInterfacesDetectsIPv4SubnetsAndIPv6Slash64(t *testing.T) {
	mustIPNet := func(cidr string) *net.IPNet {
		ip, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		ipnet.IP = ip
		return ipnet
	}
	ifaces := []net.Interface{
		{Name: "en0", Flags: net.FlagUp | net.FlagBroadcast},
		{Name: "en1", Flags: net.FlagUp | net.FlagBroadcast},
		{Name: "lo0", Flags: net.FlagUp | net.FlagLoopback},
		{Name: "utun0", Flags: net.FlagUp | net.FlagPointToPoint},
		{Name: "en2", Flags: net.FlagBroadcast}, // down
	}
	addrsByName := map[string][]net.Addr{
		"en0": {
			mustIPNet("198.18.20.5/24"),
			mustIPNet("2001:db8:1111:2222::10/64"),
			mustIPNet("2001:db8:1111:3333::10/128"), // DHCPv6 /128 normalized to /64
			mustIPNet("2001:db8:4444:5555::10/48"),  // clamped to /64, never widened
			mustIPNet("fe80::1/64"),                 // link-local skipped
			mustIPNet("169.254.1.5/16"),             // link-local skipped
		},
		"en1": {
			mustIPNet("198.18.20.6/24"), // duplicate prefix deduplicated
			mustIPNet("0.0.0.0/0"),      // wildcard skipped
		},
		"lo0":   {mustIPNet("127.0.0.1/8"), mustIPNet("::1/128")},
		"utun0": {mustIPNet("2001:db8:9999:1::1/128")}, // point-to-point VPN skipped
		"en2":   {mustIPNet("198.18.99.5/24")},         // down interface skipped
	}
	got := prefixesFromInterfaces(ifaces, func(iface net.Interface) ([]net.Addr, error) {
		return addrsByName[iface.Name], nil
	})
	want := []netip.Prefix{
		netip.MustParsePrefix("198.18.20.0/24"),
		netip.MustParsePrefix("2001:db8:1111:2222::/64"),
		netip.MustParsePrefix("2001:db8:1111:3333::/64"),
		netip.MustParsePrefix("2001:db8:4444:5555::/64"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("prefixesFromInterfaces = %v, want %v", got, want)
	}
}

func TestClientNetTrustedProxyWithLocalIPv4AndGlobalIPv6HostPrefix(t *testing.T) {
	restore := OverrideHostPrefixes([]netip.Prefix{
		netip.MustParsePrefix("2001:db8:abcd:1::/64"),
	})
	t.Cleanup(restore)

	trusted := ProxyPrefixes([]string{"172.16.50.5"})

	// Private IPv4 behind trusted reverse proxy -> local.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.16.50.5:41000"
	req.Header.Set("X-Forwarded-For", "192.168.3.10")
	if ip, local := ClientNet(req, trusted...); !local || ip.String() != "192.168.3.10" {
		t.Fatalf("private behind trusted proxy = (%s, local=%v), want (192.168.3.10, true)", ip, local)
	}

	// Global IPv6 in host /64 behind trusted reverse proxy -> local.
	req6 := httptest.NewRequest(http.MethodGet, "/", nil)
	req6.RemoteAddr = "172.16.50.5:41001"
	req6.Header.Set("X-Forwarded-For", "2001:db8:abcd:1::99")
	if ip, local := ClientNet(req6, trusted...); !local || ip.String() != "2001:db8:abcd:1::99" {
		t.Fatalf("host /64 IPv6 behind trusted proxy = (%s, local=%v), want (2001:db8:abcd:1::99, true)", ip, local)
	}

	// Global IPv6 outside host /64 behind trusted reverse proxy -> remote.
	req6Out := httptest.NewRequest(http.MethodGet, "/", nil)
	req6Out.RemoteAddr = "172.16.50.5:41002"
	req6Out.Header.Set("X-Forwarded-For", "2001:db8:ffff:1::99")
	if ip, local := ClientNet(req6Out, trusted...); local || ip.String() != "2001:db8:ffff:1::99" {
		t.Fatalf("public IPv6 behind trusted proxy = (%s, local=%v), want (2001:db8:ffff:1::99, false)", ip, local)
	}

	// Public client spoofing host /64 prefix ahead of its own IP -> stays remote.
	reqSpoof := httptest.NewRequest(http.MethodGet, "/", nil)
	reqSpoof.RemoteAddr = "172.16.50.5:41003"
	reqSpoof.Header.Set("X-Forwarded-For", "2001:db8:abcd:1::99, 203.0.113.7")
	if ip, local := ClientNet(reqSpoof, trusted...); local || ip.String() != "203.0.113.7" {
		t.Fatalf("spoofed IPv6 prefix = (%s, local=%v), want (203.0.113.7, false)", ip, local)
	}

	// Untrusted public peer spoofing private X-Forwarded-For -> stays remote.
	reqUntrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	reqUntrusted.RemoteAddr = "203.0.113.9:51000"
	reqUntrusted.Header.Set("X-Forwarded-For", "192.168.3.10")
	if ip, local := ClientNet(reqUntrusted, trusted...); local || ip.String() != "203.0.113.9" {
		t.Fatalf("untrusted spoof = (%s, local=%v), want (203.0.113.9, false)", ip, local)
	}
}
