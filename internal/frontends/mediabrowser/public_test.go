package mediabrowser

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/conform"
	"github.com/braedonsaunders/ether/internal/conform/openapi/spectest"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

func TestPublicRoutes(t *testing.T) {
	r := &route.Router{}
	if err := PublicRoutes(r, PublicInfo{ID: "123", ServerName: "Ether", Version: "12.4.0", ProductName: "Jellyfin Server"}); err != nil {
		t.Fatal(err)
	}
	h := StripPrefix("/jellyfin", r)
	for _, path := range []string{"/System/Info/Public", "/Jellyfin/System/Info/Public"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		var got map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got["Id"] != "123" || got["StartupWizardCompleted"] != true {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if path == "/Jellyfin/System/Info/Public" && got["LocalAddress"] != "http://example.com/jellyfin" {
			t.Fatal(got)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/Users/Public", nil))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/jellyfinish/Users/Public", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestJellyfinPublicSpec(t *testing.T) {
	doc := spectest.Document(t, "jellyfin")
	if doc == nil {
		return
	}
	r := &route.Router{}
	if err := PublicRoutes(r, PublicInfo{ID: "00000000000000000000000000000001", ServerName: "Ether", Version: "12.4.0", ProductName: "Jellyfin Server"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/System/Info/Public", "/Users/Public", "/System/Ping", "/Branding/Configuration"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if err := doc.Validate("GET", path, w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestPublicRecordedCorpus(t *testing.T) {
	fixtures := map[string]string{"/System/Info/Public": "findroid_jsonconnect_system info_GET__System_Info_Public.json", "/Users/Public": "findroid_jsonconnect_public users_GET__Users_Public.json"}
	r := &route.Router{}
	if err := PublicRoutes(r, PublicInfo{ID: "123", ServerName: "Ether", Version: "12.4.0", ProductName: "Jellyfin Server"}); err != nil {
		t.Fatal(err)
	}
	for path, file := range fixtures {
		reference, err := conform.LoadFixture("../../../testdata/conform/jellyfin/findroid/" + file)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		report := conform.Compare(reference, conform.Response{Status: w.Code, Headers: w.Header(), Body: w.Body.Bytes()})
		if report.Blocking() != 0 {
			t.Errorf("%s: %+v", path, report.Differences)
		}
	}
}

func TestEmbyUnservedWebAssetsReturnNotFound(t *testing.T) {
	r := &route.Router{}
	if err := PublicRoutes(r, PublicInfo{ProductName: "Emby Server", Version: "4.10.2.0"}); err != nil {
		t.Fatal(err)
	}
	h := StripPrefix("/emby", r)
	for _, path := range []string{"/emby/web/manifest.json", "/emby/web/index.html", "/emby/web/strings/en-US.json", "/web/other.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/emby/web", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Connect to Ether") {
		t.Fatalf("connection page: %d %q", w.Code, w.Body.String())
	}
}

func TestConnectionPageLocaleCoverage(t *testing.T) {
	for _, lang := range []string{"en", "fr", "es", "cs", "da", "de", "fi", "it", "ja", "ko", "nb", "nl", "pl", "pt-BR", "ru", "sv", "tr", "uk", "zh-CN", "zh-TW"} {
		t.Run(lang, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/web", nil)
			r.Header.Set("Accept-Language", lang)
			w := httptest.NewRecorder()
			webPage(w, r, nil)
			if !strings.Contains(w.Body.String(), `lang="`+lang+`"`) {
				t.Fatalf("wrong locale: %s", w.Body.String())
			}
			if lang != "en" && strings.Contains(w.Body.String(), "Open a native") {
				t.Fatal("English placeholder")
			}
		})
	}
}

func TestSystemEndpointReportsCallerNetwork(t *testing.T) {
	restore := clientnet.OverrideHostPrefixes([]netip.Prefix{
		netip.MustParsePrefix("198.18.10.0/24"),
		netip.MustParsePrefix("2001:db8:abcd:1::/64"),
	})
	t.Cleanup(restore)

	r := &route.Router{}
	if err := PublicRoutes(r, PublicInfo{ID: "123", ServerName: "Ether", ProductName: "Emby Server"}); err != nil {
		t.Fatal(err)
	}
	h := StripPrefix("/emby", r)
	for _, tc := range []struct {
		path, remote string
		local, lan   bool
	}{
		{"/System/Endpoint", "192.168.68.70:51000", false, true},
		{"/emby/System/Endpoint", "127.0.0.1:51000", true, true},
		{"/emby/System/Endpoint", "[fe80::1]:51000", false, true},
		{"/System/Endpoint", "100.64.1.10:51000", false, true},
		{"/System/Endpoint", "198.18.10.25:51000", false, true},
		{"/System/Endpoint", "[2001:db8:abcd:1::42]:51000", false, true},
		{"/System/Endpoint", "[2001:db8:ffff:1::42]:51000", false, false},
		{"/System/Endpoint", "203.0.113.9:51000", false, false},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.RemoteAddr = tc.remote
		req.Header.Set("X-Forwarded-For", "10.0.0.5")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var got map[string]bool
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got["IsLocal"] != tc.local || got["IsInNetwork"] != tc.lan {
			t.Fatalf("%s from %s: %d %s", tc.path, tc.remote, w.Code, w.Body.String())
		}
	}

	// away() used by pre-rolls shares the same clientnet.LAN classification.
	for _, tc := range []struct {
		ip       string
		wantAway bool
	}{
		{"192.168.3.10", false},
		{"100.64.1.10", false},
		{"198.18.10.25", false},
		{"2001:db8:abcd:1::42", false},
		{"2001:db8:ffff:1::42", true},
		{"203.0.113.9", true},
	} {
		inst := &instance{host: frontend.Host{ClientIP: func(*http.Request) (netip.Addr, bool) {
			return netip.MustParseAddr(tc.ip), true
		}}}
		if got := inst.away(httptest.NewRequest("GET", "/", nil)); got != tc.wantAway {
			t.Errorf("away(%s) = %v, want %v", tc.ip, got, tc.wantAway)
		}
	}
}

func TestSystemInfoPublicHonoursTrustedProxyHeaders(t *testing.T) {
	r := &route.Router{}
	if err := PublicRoutes(r, PublicInfo{ID: "123", ServerName: "Ether", Version: "10.10.0", ProductName: "Jellyfin Server"}); err != nil {
		t.Fatal(err)
	}

	// Trusted loopback proxy with subdomain + path prefix.
	req := httptest.NewRequest("GET", "http://127.0.0.1:8096/System/Info/Public", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "media.example.com")
	req.Header.Set("X-Forwarded-Prefix", "/jellyfin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body struct {
		LocalAddress string `json:"LocalAddress"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.LocalAddress != "https://media.example.com/jellyfin" {
		t.Fatalf("trusted proxy LocalAddress = %q (code %d)", body.LocalAddress, w.Code)
	}

	// Untrusted remote peer spoofing X-Forwarded-* headers must be ignored.
	spoof := httptest.NewRequest("GET", "http://192.168.1.5:8096/System/Info/Public", nil)
	spoof.RemoteAddr = "203.0.113.9:50000"
	spoof.Header.Set("X-Forwarded-Proto", "https")
	spoof.Header.Set("X-Forwarded-Host", "evil.example.com")
	spoof.Header.Set("X-Forwarded-Prefix", "/evil")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, spoof)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.LocalAddress != "http://192.168.1.5:8096" {
		t.Fatalf("untrusted proxy LocalAddress = %q (code %d)", body.LocalAddress, w.Code)
	}
}
