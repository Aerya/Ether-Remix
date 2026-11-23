package safefetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestTrustedHostsAllowLANAndBlockLoopbackRebindAndRedirects(t *testing.T) {
	lanSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-ungranted" {
			http.Redirect(w, r, "http://other-lan:8901/secret", http.StatusFound)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer lanSrv.Close()

	_, srvPort, err := net.SplitHostPort(lanSrv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	guard := &Guard{
		LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			switch host {
			case "tuliprox", "other-lan":
				return []netip.Addr{netip.MustParseAddr("192.168.1.20")}, nil
			case "rebind-loop":
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			default:
				return nil, fmt.Errorf("unexpected lookup %q", host)
			}
		},
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, lanSrv.Listener.Addr().String())
		},
	}
	client := guard.Client(&http.Client{})

	// Granted LAN hostname resolves to RFC1918 and succeeds.
	ctx := WithTrustedHosts(context.Background(), []string{"tuliprox:" + srvPort, "rebind-loop"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://tuliprox:"+srvPort+"/playlist.m3u", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("granted LAN host failed: %v", err)
	}
	resp.Body.Close()

	// Wrong port on host:port grant stays blocked.
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://tuliprox:9999/playlist.m3u", nil)
	if _, err := client.Do(req); !errors.Is(err, ErrBlocked) {
		t.Fatalf("ungranted port err = %v, want ErrBlocked", err)
	}

	// Redirect from granted host to ungranted private host is blocked.
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://tuliprox:"+srvPort+"/redirect-ungranted", nil)
	if _, err := client.Do(req); !errors.Is(err, ErrBlocked) {
		t.Fatalf("redirect to ungranted host err = %v, want ErrBlocked", err)
	}

	// Non-loopback hostname resolving to 127.0.0.1 stays blocked even when granted.
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://rebind-loop:"+srvPort+"/", nil)
	if _, err := client.Do(req); !errors.Is(err, ErrBlocked) {
		t.Fatalf("loopback rebind err = %v, want ErrBlocked", err)
	}

	// Explicit 127.0.0.1 grant allows loopback.
	loopCtx := WithTrustedHosts(context.Background(), []string{lanSrv.Listener.Addr().String()})
	req, _ = http.NewRequestWithContext(loopCtx, http.MethodGet, lanSrv.URL+"/", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("explicit 127.0.0.1 grant failed: %v", err)
	}
	resp.Body.Close()
}
