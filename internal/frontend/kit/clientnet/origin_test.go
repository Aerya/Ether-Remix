package clientnet

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestOriginTrustedProxyHonoursProtoHostPrefix(t *testing.T) {
	proxy := netip.MustParsePrefix("10.0.0.5/32")

	req := httptest.NewRequest(http.MethodGet, "http://10.0.0.2:8096/System/Info", nil)
	req.RemoteAddr = "10.0.0.5:43210"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "media.example.com")
	req.Header.Set("X-Forwarded-Prefix", "/jellyfin/")

	origin, ok := Origin(req, proxy)
	if !ok || !origin.Proxied {
		t.Fatalf("Origin = (%+v, %v), want proxied ok", origin, ok)
	}
	if origin.Scheme != "https" || origin.Host != "media.example.com" || origin.Prefix != "/jellyfin" {
		t.Fatalf("unexpected origin: %+v", origin)
	}
	if got := origin.BaseURL(""); got != "https://media.example.com/jellyfin" {
		t.Fatalf("BaseURL(\"\") = %q", got)
	}
	// Duplicate trailing mount prefix is not doubled.
	if got := origin.BaseURL("/jellyfin"); got != "https://media.example.com/jellyfin" {
		t.Fatalf("BaseURL(\"/jellyfin\") = %q", got)
	}
	// Outer proxy prefix combines cleanly with mount prefix.
	req.Header.Set("X-Forwarded-Prefix", "/ether-edge")
	origin, _ = Origin(req, proxy)
	if got := origin.BaseURL("/jellyfin"); got != "https://media.example.com/ether-edge/jellyfin" {
		t.Fatalf("outer prefix BaseURL = %q", got)
	}
}

func TestOriginIgnoresUntrustedProxyHeaders(t *testing.T) {
	proxy := netip.MustParsePrefix("10.0.0.5/32")

	req := httptest.NewRequest(http.MethodGet, "http://192.168.1.20:8096/System/Info", nil)
	req.RemoteAddr = "192.168.1.99:55000"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "evil.example.com")
	req.Header.Set("X-Forwarded-Prefix", "/spoof")

	origin, ok := Origin(req, proxy)
	if !ok || origin.Proxied {
		t.Fatalf("Origin = (%+v, %v), want direct non-proxied", origin, ok)
	}
	if origin.Scheme != "http" || origin.Host != "192.168.1.20:8096" || origin.Prefix != "" {
		t.Fatalf("untrusted headers leaked into origin: %+v", origin)
	}
}

func TestOriginCanonicalBaseStripsDefaultPorts(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://Jellyfin.Example.com:443/", nil)
	req.TLS = &tls.ConnectionState{}
	origin, ok := Origin(req)
	if !ok {
		t.Fatal("Origin failed")
	}
	if got := origin.CanonicalBase(""); got != "https://jellyfin.example.com" {
		t.Fatalf("CanonicalBase = %q", got)
	}
}

func TestSanitizePrefixAndRedirectPathRejectsOpenRedirects(t *testing.T) {
	for _, bad := range []string{"//evil.com", "https://evil.com", "/../etc", "/a/./b", "/foo?bar=1", "/foo#frag", "/foo\\bar"} {
		if got := SanitizePrefix(bad); got != "" {
			t.Errorf("SanitizePrefix(%q) = %q, want empty", bad, got)
		}
	}
	if got := RedirectPath("/media", "/jellyfin", "/web/"); got != "/media/jellyfin/web/" {
		t.Fatalf("RedirectPath = %q", got)
	}
	if got := RedirectPath("/jellyfin", "/jellyfin", "/web/"); got != "/jellyfin/web/" {
		t.Fatalf("RedirectPath duplicate = %q", got)
	}
	if got := RedirectPath("//evil.com", "", "/web/"); got != "/web/" {
		t.Fatalf("RedirectPath open redirect = %q", got)
	}
}
