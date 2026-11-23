package admin

import (
	"net/http"
	"os"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
)

func TestConsoleConnectURLsReplaceContainerAddress(t *testing.T) {
	r := httptestRequest("192.168.68.66:38034")
	got := consoleConnectURLs(r, "jellyfin", []string{
		"http://172.22.0.2:8096",
		"http://172.22.0.2:38034/jellyfin",
	}, config.Config{}, nil)
	if len(got) != 1 || got[0] != "http://192.168.68.66:38034/jellyfin" {
		t.Fatalf("connect URLs = %#v", got)
	}
}

func TestConsoleConnectURLsPreferDedicatedListenerOverSharedPort(t *testing.T) {
	for _, tc := range []struct {
		id         string
		console    string
		urls       []string
		wantFirst  string
		wantSecond string
	}{
		{
			id:         "jellyfin",
			console:    "192.168.68.65:32550",
			urls:       []string{"http://192.168.68.65:32550/jellyfin", "http://192.168.68.65:8096"},
			wantFirst:  "http://192.168.68.65:8096",
			wantSecond: "http://192.168.68.65:32550/jellyfin",
		},
		{
			id:         "emby",
			console:    "192.168.68.65:32550",
			urls:       []string{"http://192.168.68.65:32550/emby", "http://192.168.68.65:8097"},
			wantFirst:  "http://192.168.68.65:8097",
			wantSecond: "http://192.168.68.65:32550/emby",
		},
		{
			id:         "silo",
			console:    "10.0.0.43:32450",
			urls:       []string{"http://10.0.0.43:8090", "http://10.0.0.43:32450/silo"},
			wantFirst:  "http://10.0.0.43:8090",
			wantSecond: "http://10.0.0.43:32450/silo",
		},
	} {
		r := httptestRequest(tc.console)
		got := consoleConnectURLs(r, tc.id, tc.urls, config.Config{}, nil)
		if len(got) != 2 || got[0] != tc.wantFirst || got[1] != tc.wantSecond {
			t.Fatalf("%s connect URLs = %#v, want [%q, %q]", tc.id, got, tc.wantFirst, tc.wantSecond)
		}
	}
}

func TestConsoleConnectURLsNeverInventsPublicDomainPaths(t *testing.T) {
	// Plex CustomURLs must never be grafted as domain.com/<app> onto another app.
	r := httptestRequest("127.0.0.1:32400")
	cfg := config.Config{Plex: config.Plex{CustomURLs: []string{"https://ether.example:443"}}}
	got := consoleConnectURLs(r, "emby", []string{"http://192.168.1.5:8097"}, cfg, nil)
	for _, candidate := range got {
		if candidate == "https://ether.example:443/emby" || candidate == "https://ether.example/emby" {
			t.Fatalf("must not invent Plex host path for Emby: %#v", got)
		}
	}

	// A reverse-proxied public console hostname must never invent console.example.com/<app>.
	proxied := httptestRequest("127.0.0.1:32400")
	proxied.RemoteAddr = "127.0.0.1:9"
	proxied.Header.Set("X-Forwarded-Host", "console.example.com")
	proxied.Header.Set("X-Forwarded-Proto", "https")
	got = consoleConnectURLs(proxied, "jellyfin", []string{"http://192.168.1.5:8096", "http://172.20.0.2:32400/jellyfin"}, cfg, nil)
	if len(got) != 1 || got[0] != "http://192.168.1.5:8096" {
		t.Fatalf("proxied connect URLs = %#v, want only LAN listener", got)
	}
}

func TestConsoleConnectURLsIgnoreUntrustedForwardedHost(t *testing.T) {
	r := httptestRequest("192.168.1.20:32400")
	r.Header.Set("X-Forwarded-Host", "attacker.example")
	r.Header.Set("X-Forwarded-Proto", "https")
	got := consoleConnectURLs(r, "silo", nil, config.Config{}, nil)
	if len(got) != 1 || got[0] != "http://192.168.1.20:32400/silo" {
		t.Fatalf("connect URLs = %#v", got)
	}
}

func TestConsoleConnectURLsPlexUsesTheBrowserHost(t *testing.T) {
	r := httptestRequest("192.168.68.66:32803")
	got := consoleConnectURLs(r, "plex", []string{"http://172.20.0.2:32400"}, config.Config{}, nil)
	if len(got) != 1 || got[0] != "http://192.168.68.66:32803" {
		t.Fatalf("connect URLs = %#v", got)
	}
}

// A console opened through a reverse proxy or the public host name must not
// make that route (or an http:// guess for it) the Plex at-home address.
func TestConsoleConnectURLsPlexNeverUsesThePublicRouteAtHome(t *testing.T) {
	lan := []string{"http://192.168.1.5:32400"}
	proxied := httptestRequest("ether.example.com")
	proxied.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := consoleConnectURLs(proxied, "plex", lan, config.Config{}, nil); len(got) != 1 || got[0] != lan[0] {
		t.Fatalf("proxied connect URLs = %#v", got)
	}
	cfg := config.Config{Plex: config.Plex{PublicHost: "https://ether.example.com"}}
	direct := httptestRequest("ether.example.com:32400")
	if got := consoleConnectURLs(direct, "plex", lan, cfg, nil); len(got) != 1 || got[0] != lan[0] {
		t.Fatalf("public host connect URLs = %#v", got)
	}
}

func TestConsoleConnectURLsTrustForwardedHostFromLoopback(t *testing.T) {
	r := httptestRequest("127.0.0.1:32400")
	r.RemoteAddr = "127.0.0.1:9"
	r.Header.Set("X-Forwarded-Host", "192.168.68.66:32802")
	r.Header.Set("X-Forwarded-Proto", "http")
	got := consoleConnectURLs(r, "jellyfin", []string{"http://172.20.0.2:8096"}, config.Config{}, nil)
	if len(got) != 1 || got[0] != "http://192.168.68.66:32802/jellyfin" {
		t.Fatalf("connect URLs = %#v", got)
	}
}

func TestConsoleConnectURLsPreferLANOverLoopback(t *testing.T) {
	r := httptestRequest("127.0.0.1:32400")
	got := consoleConnectURLs(r, "jellyfin", []string{
		"http://127.0.0.1:8096",
		"http://[::1]:8096",
		"http://10.255.0.1:8096",
		"http://100.64.0.2:8096",
		"http://192.168.68.66:8096",
		"http://192.168.68.66:32400/jellyfin",
	}, config.Config{}, nil)
	if len(got) < 2 || got[0] != "http://192.168.68.66:8096" || got[1] != "http://192.168.68.66:32400/jellyfin" {
		t.Fatalf("connect URLs = %#v", got)
	}
	if got[len(got)-1] == "http://192.168.68.66:8096" {
		t.Fatal("loopback should not outrank the LAN address")
	}
}

func TestValidateCustomURLsRejectsJunkAndKeepsCGNAT(t *testing.T) {
	base := config.Defaults(os.TempDir())
	base.Plex.CustomURLs = []string{
		"http://100.100.1.2:32400",
		"https://plex.example.com:8443",
	}
	if errs := ValidateConfig(base); len(errs) != 0 {
		t.Fatalf("expected valid custom URLs (including Tailscale CGNAT), got %+v", errs)
	}

	for _, junk := range []string{
		"[http://192.168.1.50:32450](http://192.168.1.50:32450/)",
		"plex.example.com:32400",
		"http://plex.example.com:32400/web",
	} {
		cfg := base
		cfg.Plex.CustomURLs = []string{junk}
		errs := ValidateConfig(cfg)
		var found *FieldError
		for i := range errs {
			if errs[i].Field == "plex.customUrls" {
				found = &errs[i]
				break
			}
		}
		if found == nil || found.Message != "Enter full http:// or https:// server URLs, separated by commas." {
			t.Fatalf("ValidateConfig(%q) = %+v, want plex.customUrls field error", junk, errs)
		}
	}
}

// An app with its own published addresses shows them first (supporting both
// subdomain roots and path prefixes), never inventing a Plex custom URL path
// for other apps; Plex itself keeps using its custom URLs, and invalid
// published addresses are rejected.
func TestConsoleConnectURLsPerAppPublishedAddresses(t *testing.T) {
	r := httptestRequest("192.168.1.5:32400")
	cfg := config.Config{
		Plex: config.Plex{CustomURLs: []string{"https://plex.example.com"}},
		Frontends: map[string]config.Frontend{
			"jellyfin": {
				Enabled:            true,
				PublishedAddresses: []string{"https://jelly.example.com/", "https://media.example.com/jellyfin/"},
			},
		},
	}
	got := consoleConnectURLs(r, "jellyfin", []string{"http://192.168.1.5:8096"}, cfg, nil)
	if len(got) < 2 || got[0] != "https://jelly.example.com" || got[1] != "https://media.example.com/jellyfin" {
		t.Fatalf("connect URLs = %#v, want Jellyfin published addresses first", got)
	}
	for _, candidate := range got {
		if candidate == "https://plex.example.com/jellyfin" {
			t.Fatalf("connect URLs = %#v, must not show the Plex host for Jellyfin", got)
		}
	}
	// Emby has no published address of its own and must NOT inherit Plex's custom URL.
	got = consoleConnectURLs(r, "emby", []string{"http://192.168.1.5:8097"}, cfg, nil)
	for _, candidate := range got {
		if candidate == "https://plex.example.com/emby" {
			t.Fatalf("emby connect URLs = %#v, must not invent Plex host path", got)
		}
	}
	// PublishedAddresses on Plex itself are ignored.
	cfg.Frontends["plex"] = config.Frontend{Enabled: true, PublishedAddresses: []string{"https://wrong.example.com"}}
	if got := frontendPublishedURLs(cfg, "plex"); len(got) != 0 {
		t.Fatalf("Plex must not use per-frontend published addresses, got %#v", got)
	}

	base := config.Defaults(os.TempDir())
	base.Frontends = map[string]config.Frontend{"jellyfin": {Enabled: true, PublishedAddresses: []string{"jelly.example.com"}}}
	errs := ValidateConfig(base)
	found := false
	for _, e := range errs {
		if e.Field == "frontends.jellyfin.publishedAddresses" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ValidateConfig = %+v, want frontends.jellyfin.publishedAddresses error", errs)
	}
	base.Frontends["jellyfin"] = config.Frontend{Enabled: true, PublishedAddresses: []string{"https://jelly.example.com", "https://media.example.com/jellyfin"}}
	for _, e := range ValidateConfig(base) {
		if e.Field == "frontends.jellyfin.publishedAddresses" {
			t.Fatalf("ValidateConfig rejected valid published addresses: %+v", e)
		}
	}
}

func TestConsoleConnectURLsVelaDedicatedAndConsolePort(t *testing.T) {
	// Vela on dedicated port 8098 with serveOnMainPort=false: must NOT synthesize :32400/vela.
	cfg := config.Config{
		Server:        config.Server{Port: 32400, ConsolePort: 8099, DisableConsoleOnMainPort: true},
		FrontendPorts: map[string]int{"vela": 8098},
		Frontends: map[string]config.Frontend{
			"vela": {
				Enabled:            true,
				PublishedAddresses: []string{"https://vela.example.com"},
				Settings:           map[string]any{"serveOnMainPort": false},
			},
		},
	}
	// Owner opens the console on dedicated console port 8099.
	r := httptestRequest("192.168.1.5:8099")
	gotVela := consoleConnectURLs(r, "vela", []string{"http://192.168.1.5:8098"}, cfg, nil)
	if len(gotVela) != 2 || gotVela[0] != "https://vela.example.com" || gotVela[1] != "http://192.168.1.5:8098" {
		t.Fatalf("vela connect URLs = %#v, want [https://vela.example.com http://192.168.1.5:8098]", gotVela)
	}
	// Plex and shared Jellyfin URLs built while browsing console port 8099 must target main port 32400, not 8099.
	gotPlex := consoleConnectURLs(r, "plex", nil, cfg, nil)
	if len(gotPlex) != 1 || gotPlex[0] != "http://192.168.1.5:32400" {
		t.Fatalf("plex connect URLs from console port = %#v, want [http://192.168.1.5:32400]", gotPlex)
	}
	gotJF := consoleConnectURLs(r, "jellyfin", []string{"http://192.168.1.5:8096"}, cfg, nil)
	for _, u := range gotJF {
		if u == "http://192.168.1.5:8099/jellyfin" {
			t.Fatalf("jellyfin connect URLs leaked dedicated console port: %#v", gotJF)
		}
	}

	// ValidateConfig checks for server.consolePort & server.disableConsoleOnMainPort.
	base := config.Defaults(os.TempDir())
	base.Server.Port = 32400
	base.Server.ConsolePort = 32400
	errs := ValidateConfig(base)
	foundConsolePortErr := false
	for _, e := range errs {
		if e.Field == "server.consolePort" {
			foundConsolePortErr = true
		}
	}
	if !foundConsolePortErr {
		t.Fatalf("expected server.consolePort error when matching server.port, got %+v", errs)
	}

	base.Server.ConsolePort = 0
	base.Server.DisableConsoleOnMainPort = true
	errs = ValidateConfig(base)
	foundDisableErr := false
	for _, e := range errs {
		if e.Field == "server.disableConsoleOnMainPort" {
			foundDisableErr = true
		}
	}
	if !foundDisableErr {
		t.Fatalf("expected server.disableConsoleOnMainPort error without consolePort, got %+v", errs)
	}

	base.Server.ConsolePort = 8099
	base.Server.DisableConsoleOnMainPort = true
	for _, e := range ValidateConfig(base) {
		if e.Field == "server.consolePort" || e.Field == "server.disableConsoleOnMainPort" {
			t.Fatalf("ValidateConfig rejected valid console port settings: %+v", e)
		}
	}
}

func httptestRequest(host string) *http.Request {
	r, err := http.NewRequest(http.MethodGet, "/ether/api/frontends/jellyfin/status", nil)
	if err != nil {
		panic(err)
	}
	r.Host = host
	r.RemoteAddr = "192.168.1.20:9"
	return r
}
