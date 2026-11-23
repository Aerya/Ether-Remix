package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/safefetch"
)

func TestLocalHostGrantsFetchRedirectAndDiscovery(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	ungrantedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("ungranted private server must not be reached")
		w.WriteHeader(http.StatusOK)
	}))
	defer ungrantedSrv.Close()

	_, ungrantedPort, err := net.SplitHostPort(ungrantedSrv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	grantedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-ungranted" {
			http.Redirect(w, r, "http://other-lan:"+ungrantedPort+"/secret", http.StatusFound)
			return
		}
		fmt.Fprint(w, "#EXTM3U\n")
	}))
	defer grantedSrv.Close()

	_, grantedPort, err := net.SplitHostPort(grantedSrv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	guard := &safefetch.Guard{
		LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			switch host {
			case "tuliprox", "other-lan", "ungranted.lan":
				return []netip.Addr{netip.MustParseAddr("192.168.1.20")}, nil
			case "loopback-rebind":
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			default:
				return nil, fmt.Errorf("unexpected lookup %q", host)
			}
		},
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, _ := net.SplitHostPort(addr)
			var d net.Dialer
			if port == ungrantedPort {
				return d.DialContext(ctx, network, ungrantedSrv.Listener.Addr().String())
			}
			return d.DialContext(ctx, network, grantedSrv.Listener.Addr().String())
		},
	}
	env.m.client = guard.Client(&http.Client{})

	man := baseManifest()
	man.Ether.Permissions.Network = []string{"tuliprox", "loopback-rebind"}
	raw, _ := json.Marshal(man)
	preview, err := env.m.PreviewInstall(ctx, Source{
		Kind:     "studio",
		Manifest: raw,
		Files:    map[string][]byte{"dist/plugin.js": []byte("// x")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(preview.PermissionLines, "May reach on your network: tuliprox, loopback-rebind") {
		t.Fatalf("permission lines missing local network hint: %v", preview.PermissionLines)
	}

	rec, err := env.m.Install(ctx, preview, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	grants, err := env.m.GrantsOf(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(grants.LocalHosts, "tuliprox") {
		t.Fatalf("expected manifest local host pre-granted on install, got %v", grants.LocalHosts)
	}

	h := env.m.HostFor(rec.ID)

	// 1. Granted host reaches private server.
	resp, err := h.HTTPFetch(ctx, FetchRequest{URL: "http://tuliprox:" + grantedPort + "/playlist.m3u"})
	if err != nil {
		t.Fatalf("granted private host fetch failed: %v", err)
	}
	if string(resp.Body) != "#EXTM3U\n" {
		t.Fatalf("unexpected body %q", string(resp.Body))
	}

	// 2. Ungranted private host is blocked with the new message and recorded.
	ungrantedHost := "ungranted.lan:" + ungrantedPort
	_, err = h.HTTPFetch(ctx, FetchRequest{URL: "http://" + ungrantedHost + "/playlist.m3u"})
	if err == nil {
		t.Fatal("expected ungranted private host to be blocked")
	}
	wantMsg := ungrantedHost + " is on your network; the owner can allow it in this plugin's settings"
	if !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("got error %q, want substring %q", err.Error(), wantMsg)
	}
	if !errors.Is(err, ErrFetchFailed) || !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("error %v must wrap ErrFetchFailed and safefetch.ErrBlocked", err)
	}
	if !slices.Contains(env.m.BlockedLocalHosts(rec.ID), ungrantedHost) {
		t.Fatalf("expected %q in blocked local hosts, got %v", ungrantedHost, env.m.BlockedLocalHosts(rec.ID))
	}

	// 3. Redirect from granted host to ungranted private host is blocked and recorded.
	redirectTarget := "other-lan:" + ungrantedPort
	_, err = h.HTTPFetch(ctx, FetchRequest{URL: "http://tuliprox:" + grantedPort + "/redirect-ungranted"})
	if err == nil {
		t.Fatal("expected redirect to ungranted private host to be blocked")
	}
	if !strings.Contains(err.Error(), redirectTarget+" is on your network; the owner can allow it in this plugin's settings") {
		t.Fatalf("unexpected redirect block message: %v", err)
	}
	if !slices.Contains(env.m.BlockedLocalHosts(rec.ID), redirectTarget) {
		t.Fatalf("expected %q in blocked local hosts, got %v", redirectTarget, env.m.BlockedLocalHosts(rec.ID))
	}

	// 4. Loopback remains blocked unless localhost or 127.0.0.1 is explicitly granted.
	if _, err := h.HTTPFetch(ctx, FetchRequest{URL: "http://loopback-rebind:" + grantedPort + "/"}); err == nil {
		t.Fatal("expected loopback rebind on non-loopback hostname to stay blocked")
	}

	// Wildcards and CIDRs are rejected in SetGrants.
	for _, bad := range []string{"*.lan", "192.168.1.0/24", "*"} {
		if err := env.m.SetGrants(ctx, rec.ID, Grants{LocalHosts: []string{bad}}); err == nil {
			t.Fatalf("expected invalid local host %q to be rejected", bad)
		}
	}

	// Granting the blocked host clears it from BlockedLocalHosts.
	if err := env.m.SetGrants(ctx, rec.ID, Grants{LocalHosts: []string{"tuliprox", ungrantedHost}}); err != nil {
		t.Fatalf("SetGrants valid localHosts: %v", err)
	}
	if slices.Contains(env.m.BlockedLocalHosts(rec.ID), ungrantedHost) {
		t.Fatalf("expected granted host %q to be cleared from blocked list: %v", ungrantedHost, env.m.BlockedLocalHosts(rec.ID))
	}
}
