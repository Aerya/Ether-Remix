package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

func TestObservedAddressesRecordDedupBoundAndTTL(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	tracker := newObservedAddresses()
	tracker.now = func() time.Time { return now }
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}

	// Trusted proxy with subdomain and path prefix.
	reqSub := httptest.NewRequest(http.MethodGet, "http://10.0.0.2:8096/System/Info/Public", nil)
	reqSub.RemoteAddr = "10.0.0.1:40000"
	reqSub.Header.Set("X-Forwarded-Proto", "https")
	reqSub.Header.Set("X-Forwarded-Host", "Jellyfin.Example.com:443")
	tracker.recordRequest("jellyfin", reqSub, "", http.StatusOK, trusted)

	// Path-based reverse proxy (strips /jellyfin and sends X-Forwarded-Prefix).
	now = now.Add(2 * time.Minute)
	reqPath := httptest.NewRequest(http.MethodGet, "http://10.0.0.2:8096/System/Info/Public", nil)
	reqPath.RemoteAddr = "10.0.0.1:40001"
	reqPath.Header.Set("X-Forwarded-Proto", "https")
	reqPath.Header.Set("X-Forwarded-Host", "media.example.com")
	reqPath.Header.Set("X-Forwarded-Prefix", "/jellyfin")
	tracker.recordRequest("jellyfin", reqPath, "", http.StatusOK, trusted)

	// Untrusted proxy headers must be ignored (and LAN host recorded instead).
	now = now.Add(2 * time.Minute)
	reqUntrusted := httptest.NewRequest(http.MethodGet, "http://192.168.1.5:8096/System/Info/Public", nil)
	reqUntrusted.RemoteAddr = "192.168.1.99:50000"
	reqUntrusted.Header.Set("X-Forwarded-Proto", "https")
	reqUntrusted.Header.Set("X-Forwarded-Host", "evil.example.com")
	reqUntrusted.Header.Set("X-Forwarded-Prefix", "/spoof")
	tracker.recordRequest("jellyfin", reqUntrusted, "", http.StatusOK, trusted)

	// Error status (401/500) must not be recorded.
	reqFail := httptest.NewRequest(http.MethodGet, "http://10.0.0.2:8096/Users/Me", nil)
	reqFail.RemoteAddr = "10.0.0.1:40002"
	reqFail.Header.Set("X-Forwarded-Proto", "https")
	reqFail.Header.Set("X-Forwarded-Host", "failed.example.com")
	tracker.recordRequest("jellyfin", reqFail, "", http.StatusUnauthorized, trusted)

	// Loopback and Docker bridge hosts must not be recorded.
	for _, host := range []string{"127.0.0.1:8096", "localhost:8096", "172.17.0.2:8096"} {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+"/System/Info/Public", nil)
		r.RemoteAddr = "127.0.0.1:51000"
		tracker.recordRequest("jellyfin", r, "", http.StatusOK, trusted)
	}

	got := tracker.list("jellyfin")
	want := []string{
		"http://192.168.1.5:8096",
		"https://media.example.com/jellyfin",
		"https://jellyfin.example.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("observed = %#v, want %#v", got, want)
	}

	// Re-seeing an older address moves it back to the front without duplicating.
	now = now.Add(2 * time.Minute)
	tracker.recordRequest("jellyfin", reqSub, "", http.StatusOK, trusted)
	got = tracker.list("jellyfin")
	want = []string{
		"https://jellyfin.example.com",
		"http://192.168.1.5:8096",
		"https://media.example.com/jellyfin",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("re-seen observed = %#v, want %#v", got, want)
	}

	// Silo /api/v2 and /api/v1 aliases normalize to root, and Vela /sessions/media is skipped.
	siloReq := httptest.NewRequest(http.MethodGet, "http://192.168.1.5:32400/api/v2/system/info", nil)
	siloReq.RemoteAddr = "192.168.1.20:51000"
	tracker.recordRequest("silo", siloReq, "/api/v2", http.StatusOK, nil)
	if gotSilo := tracker.list("silo"); !reflect.DeepEqual(gotSilo, []string{"http://192.168.1.5:32400"}) {
		t.Fatalf("silo observed = %#v", gotSilo)
	}
	velaMedia := httptest.NewRequest(http.MethodGet, "http://192.168.1.5:32400/sessions/media/abc", nil)
	velaMedia.RemoteAddr = "192.168.1.20:51000"
	tracker.recordRequest("vela", velaMedia, "/sessions/media", http.StatusOK, nil)
	if gotVela := tracker.list("vela"); len(gotVela) != 0 {
		t.Fatalf("vela /sessions/media should not be recorded: %#v", gotVela)
	}

	// Bounded to maxObservedAddresses.
	for i := 0; i < maxObservedAddresses+5; i++ {
		now = now.Add(2 * time.Minute)
		r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://192.168.1.%d:8097/System/Info/Public", 10+i), nil)
		r.RemoteAddr = "192.168.1.20:52000"
		tracker.recordRequest("emby", r, "", http.StatusOK, nil)
	}
	if gotEmby := tracker.list("emby"); len(gotEmby) != maxObservedAddresses {
		t.Fatalf("emby count = %d, want %d", len(gotEmby), maxObservedAddresses)
	}

	// Expired entries drop out after observedAddressTTL.
	now = now.Add(observedAddressTTL + time.Second)
	if gotExpired := tracker.list("jellyfin"); len(gotExpired) != 0 {
		t.Fatalf("expired observed = %#v, want empty", gotExpired)
	}
}
