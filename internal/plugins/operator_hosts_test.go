package plugins

import (
	"bytes"
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

func TestManifestOperatorNetworkValidationAndPermissions(t *testing.T) {
	for _, bad := range []string{"*", "https://api.example.com", "Operator", "api.*.com"} {
		man := baseManifest()
		man.Ether.Permissions.Network = []string{bad}
		if err := man.Validate(); err == nil {
			t.Fatalf("expected manifest network entry %q to be rejected", bad)
		}
	}

	man := baseManifest()
	man.Ether.Permissions.Network = []string{"api.example.com", OperatorNetworkEntry}
	if err := man.Validate(); err != nil {
		t.Fatalf("expected \"operator\" manifest network entry to be valid, got %v", err)
	}
	if !DeclaresOperatorHosts(man) {
		t.Fatal("expected DeclaresOperatorHosts to be true")
	}
	if got := DeclaredLocalHosts(man); len(got) != 0 {
		t.Fatalf("expected \"operator\" not to be treated as a local host, got %v", got)
	}

	lines, _, _ := PermissionSheet(man)
	if !slices.Contains(lines, "Connects to api.example.com") || !slices.Contains(lines, "Connects to addresses you approve") {
		t.Fatalf("unexpected PermissionSheet lines: %v", lines)
	}
	if slices.Contains(lines, "Connects to operator") {
		t.Fatalf("PermissionSheet leaked raw operator token: %v", lines)
	}

	prev := baseManifest()
	prev.Ether.Permissions.Network = []string{"api.example.com"}
	diff := PermissionDiff(prev, man)
	if !slices.Equal(diff, []string{"Connects to addresses you approve"}) {
		t.Fatalf("unexpected PermissionDiff: %v", diff)
	}
}

func TestOperatorHostGrantsLifecycleAndIsolation(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	unapprovedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unapproved server must not be reached")
		w.WriteHeader(http.StatusOK)
	}))
	defer unapprovedSrv.Close()
	_, unapprovedPort, err := net.SplitHostPort(unapprovedSrv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	approvedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-other" {
			http.Redirect(w, r, "http://unapproved.example.com:"+unapprovedPort+"/items", http.StatusFound)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer approvedSrv.Close()
	_, approvedPort, err := net.SplitHostPort(approvedSrv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	guard := &safefetch.Guard{
		LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			switch host {
			case "books.example.com", "unapproved.example.com":
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			case "rebind.example.com":
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			case "audiobookshelf.local":
				return []netip.Addr{netip.MustParseAddr("192.168.1.50")}, nil
			default:
				return nil, fmt.Errorf("unexpected lookup %q", host)
			}
		},
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, _ := net.SplitHostPort(addr)
			var d net.Dialer
			if port == unapprovedPort {
				return d.DialContext(ctx, network, unapprovedSrv.Listener.Addr().String())
			}
			return d.DialContext(ctx, network, approvedSrv.Listener.Addr().String())
		},
	}
	env.m.client = guard.Client(&http.Client{})

	schema := `{
		"fields": [
			{"key": "serverUrl", "kind": "url", "label": "Server URL", "default": "https://default.example.com/api"},
			{"key": "apiToken", "kind": "secret", "label": "API token", "default": "https://secret.example.com"}
		]
	}`
	man := baseManifest()
	man.ID = "community.test.operatora"
	man.Ether.Permissions.Network = []string{OperatorNetworkEntry}
	man.Ether.Permissions.Secrets = []string{"apiToken"}
	man.Ether.Settings = json.RawMessage(`{"$ref":"settings.schema.json"}`)
	rawMan, _ := json.Marshal(man)

	preview, err := env.m.PreviewInstall(ctx, Source{
		Kind:     "studio",
		Manifest: rawMan,
		Files: map[string][]byte{
			"dist/plugin.js":       []byte("// x"),
			"settings.schema.json": []byte(schema),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.NeedsOperatorHosts {
		t.Fatal("expected preview.NeedsOperatorHosts = true")
	}
	if !slices.Equal(preview.PendingOperatorHosts, []string{"default.example.com"}) {
		t.Fatalf("preview.PendingOperatorHosts = %v, want [default.example.com]", preview.PendingOperatorHosts)
	}

	recA, err := env.m.Install(ctx, preview, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Second plugin with "operator" to prove per-plugin grant isolation.
	manB := baseManifest()
	manB.ID = "community.test.operatorb"
	manB.Ether.Permissions.Network = []string{OperatorNetworkEntry}
	rawManB, _ := json.Marshal(manB)
	prevB, err := env.m.PreviewInstall(ctx, Source{
		Kind:     "studio",
		Manifest: rawManB,
		Files:    map[string][]byte{"dist/plugin.js": []byte("// y")},
	})
	if err != nil {
		t.Fatal(err)
	}
	recB, err := env.m.Install(ctx, prevB, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}

	targetHost := "books.example.com:" + approvedPort
	targetURL := "http://" + targetHost + "/items"

	// Saving settings with an unapproved server URL surfaces it in Detail.PendingOperatorHosts.
	handler := env.m.Handler()

	putSettingsBody, _ := json.Marshal(map[string]any{
		"settings": map[string]any{
			"serverUrl": "http://" + targetHost + "/library",
			"apiToken":  "https://secret-never-shown.example.com",
		},
	})
	putReq := httptest.NewRequest(http.MethodPut, "/"+recA.ID+"/settings", bytes.NewReader(putSettingsBody))
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT settings status %d: %s", putRec.Code, putRec.Body.String())
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/"+recA.ID, nil)
	detailRec := httptest.NewRecorder()
	handler.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("GET detail status %d: %s", detailRec.Code, detailRec.Body.String())
	}
	var detail struct {
		Permissions struct {
			NeedsOperatorHosts bool `json:"needsOperatorHosts"`
		} `json:"permissions"`
		PendingOperatorHosts []string `json:"pendingOperatorHosts"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if !detail.Permissions.NeedsOperatorHosts {
		t.Fatal("expected detail.permissions.needsOperatorHosts = true")
	}
	if !slices.Equal(detail.PendingOperatorHosts, []string{targetHost}) {
		t.Fatalf("detail.PendingOperatorHosts = %v, want [%s]", detail.PendingOperatorHosts, targetHost)
	}

	// Before approval: AllowNetworkHost and HTTPFetch are blocked and record the host.
	if env.m.AllowNetworkHost(ctx, recA.ID, "unapproved.example.com") {
		t.Fatal("expected AllowNetworkHost to deny unapproved operator host")
	}
	if !slices.Contains(env.m.BlockedOperatorHosts(recA.ID), "unapproved.example.com") {
		t.Fatalf("expected unapproved host recorded in BlockedOperatorHosts, got %v", env.m.BlockedOperatorHosts(recA.ID))
	}

	hostA := env.m.HostFor(recA.ID)
	_, err = hostA.HTTPFetch(ctx, FetchRequest{URL: "http://unapproved.example.com:" + unapprovedPort + "/items"})
	if err == nil {
		t.Fatal("expected HTTPFetch to unapproved operator host to be blocked")
	}
	if !errors.Is(err, ErrPermissionDenied) || !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("error %v must wrap ErrPermissionDenied and ErrFetchFailed", err)
	}
	if !strings.Contains(err.Error(), "needs owner approval; the owner can approve it in this plugin's settings") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// Invalid operator host grants (wildcards, CIDRs, "operator") are rejected.
	for _, bad := range []string{"*", "*.example.com", "operator", "10.0.0.0/8", "https://books.example.com"} {
		if err := env.m.SetGrants(ctx, recA.ID, Grants{OperatorHosts: []string{bad}}); err == nil {
			t.Fatalf("expected SetGrants to reject %q", bad)
		}
	}

	// Approve targetHost and audiobookshelf.local + rebind.example.com on plugin A.
	lanTarget := "audiobookshelf.local:" + approvedPort
	rebindTarget := "rebind.example.com:" + unapprovedPort
	if err := env.m.SetGrants(ctx, recA.ID, Grants{
		OperatorHosts: []string{targetHost, lanTarget, rebindTarget},
	}); err != nil {
		t.Fatalf("SetGrants valid OperatorHosts: %v", err)
	}

	if !env.m.AllowNetworkHost(ctx, recA.ID, "books.example.com") {
		t.Fatal("expected AllowNetworkHost to admit approved operator host")
	}
	resp, err := hostA.HTTPFetch(ctx, FetchRequest{URL: targetURL})
	if err != nil {
		t.Fatalf("HTTPFetch to approved operator host failed: %v", err)
	}
	if string(resp.Body) != `{"ok":true}` {
		t.Fatalf("unexpected body %q", string(resp.Body))
	}

	// Explicitly approved local hostname in OperatorHosts works on LAN.
	if _, err := hostA.HTTPFetch(ctx, FetchRequest{URL: "http://" + lanTarget + "/items"}); err != nil {
		t.Fatalf("HTTPFetch to approved LAN operator host failed: %v", err)
	}

	// Public DNS name in OperatorHosts that resolves to loopback is still blocked by SSRF guard.
	if _, err := hostA.HTTPFetch(ctx, FetchRequest{URL: "http://" + rebindTarget + "/items"}); err == nil {
		t.Fatal("expected public DNS operator host resolving to loopback to remain blocked")
	}

	// Redirect from approved host to unapproved host is blocked and recorded.
	if _, err := hostA.HTTPFetch(ctx, FetchRequest{URL: "http://" + targetHost + "/redirect-other"}); err == nil {
		t.Fatal("expected redirect to unapproved host to be blocked")
	}

	// Plugin B remains isolated and cannot use plugin A's approved host.
	if env.m.AllowNetworkHost(ctx, recB.ID, "books.example.com") {
		t.Fatal("expected plugin B not to inherit plugin A's operatorHosts")
	}
	if _, err := env.m.HostFor(recB.ID).HTTPFetch(ctx, FetchRequest{URL: "http://unapproved.example.com:" + unapprovedPort + "/items"}); err == nil {
		t.Fatal("expected plugin B HTTPFetch to unapproved host to fail")
	}

	// Revoking OperatorHosts takes effect immediately.
	if err := env.m.SetGrants(ctx, recA.ID, Grants{OperatorHosts: []string{}}); err != nil {
		t.Fatalf("revoke OperatorHosts: %v", err)
	}
	if env.m.AllowNetworkHost(ctx, recA.ID, "books.example.com") {
		t.Fatal("expected AllowNetworkHost to deny revoked operator host")
	}
}
