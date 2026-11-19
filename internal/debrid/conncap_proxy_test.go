package debrid

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/egress"
)

// connectProxy is a minimal HTTP CONNECT proxy.
func connectProxy(t *testing.T) *httptest.Server {
	t.Helper()
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "connect only", http.StatusMethodNotAllowed)
			return
		}
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj := w.(http.Hijacker)
		c, _, err := hj.Hijack()
		if err != nil {
			up.Close()
			return
		}
		c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() { io.Copy(up, c); up.Close() }()
		go func() { io.Copy(c, up); c.Close() }()
	}))
	t.Cleanup(p.Close)
	return p
}

type proxyRig struct {
	limit   *connCap
	client  *http.Client
	urls    [2]string
	started chan string // host of each request that reached its handler
	release chan struct{}
	proxy   string
}

func newProxyRig(t *testing.T) *proxyRig {
	t.Helper()
	r := &proxyRig{limit: newConnCap(4), started: make(chan string, 32), release: make(chan struct{})}
	for i := range r.urls {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.started <- req.Host
			<-r.release
			w.Write([]byte("ok"))
		}))
		t.Cleanup(s.Close)
		r.urls[i] = s.URL
	}
	p := connectProxy(t)
	pu, _ := url.Parse(p.URL)
	r.proxy = pu.Host
	tr := streamingTransport(r.limit, egress.CapDebrid)
	tr.Proxy = http.ProxyURL(pu)
	tr.ForceAttemptHTTP2 = false
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	r.client = &http.Client{Transport: tr}
	t.Cleanup(tr.CloseIdleConnections)
	// Handlers must be released before the servers close.
	t.Cleanup(func() {
		select {
		case <-r.release:
		default:
			close(r.release)
		}
	})
	return r
}

// get issues a request capped by the real host, as Engine.send does.
func (r *proxyRig) get(ctx context.Context, u string) error {
	req, err := http.NewRequestWithContext(withCapKey(ctx, dialAddr(u)), http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

func (r *proxyRig) hold(t *testing.T, u string, n int) chan error {
	t.Helper()
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errs <- r.get(context.Background(), u) }()
	}
	return errs
}

func (r *proxyRig) waitStarted(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-r.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d requests reached the server", i, n)
		}
	}
}

func (r *proxyRig) expectBlocked(t *testing.T, u string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := r.get(ctx, u); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("extra connection to %s: err = %v, want it to wait for the cap", u, err)
	}
}

func (r *proxyRig) finish(t *testing.T, errs ...chan error) {
	t.Helper()
	close(r.release)
	for _, c := range errs {
		for i := cap(c); i > 0; i-- {
			if err := <-c; err != nil {
				t.Errorf("held request: %v", err)
			}
		}
	}
}

func TestConnCapPerRealHostBehindProxy(t *testing.T) {
	r := newProxyRig(t)
	a := r.hold(t, r.urls[0], 4)
	b := r.hold(t, r.urls[1], 4)
	r.waitStarted(t, 8) // 4 per host at once: the proxy address is not the key
	r.limit.mu.Lock()
	proxyOpen := r.limit.open[r.proxy]
	r.limit.mu.Unlock()
	if proxyOpen != 0 {
		t.Fatalf("connections counted against the proxy: %d", proxyOpen)
	}
	r.finish(t, a, b)
}

func TestConnCapFifthConnectionToHostWaits(t *testing.T) {
	r := newProxyRig(t)
	a := r.hold(t, r.urls[0], 4)
	r.waitStarted(t, 4)
	r.expectBlocked(t, r.urls[0])
	r.finish(t, a)
}

func TestConnCapReduceRealHostBehindProxy(t *testing.T) {
	r := newProxyRig(t)
	r.limit.reduce(dialAddr(r.urls[0]))
	a := r.hold(t, r.urls[0], 1)
	r.waitStarted(t, 1)
	r.expectBlocked(t, r.urls[0])
	b := r.hold(t, r.urls[1], 4) // the other host keeps its full cap
	r.waitStarted(t, 4)
	r.finish(t, a, b)
}

func TestHedgeAllowedUsesRealHostBehindProxy(t *testing.T) {
	r := newProxyRig(t)
	a := r.hold(t, r.urls[0], 3)
	r.waitStarted(t, 3)
	if r.limit.hedgeAllowed(dialAddr(r.urls[0])) {
		t.Fatal("hedge allowed with the real host nearly full")
	}
	if !r.limit.hedgeAllowed(dialAddr(r.urls[1])) {
		t.Fatal("hedge refused for an idle host")
	}
	r.finish(t, a)
}

func TestConnCapKeyReachesDialContext(t *testing.T) {
	limit := newConnCap(4)
	var gotAddr string
	next := func(ctx context.Context, network, addr string) (net.Conn, error) {
		gotAddr = addr
		c, _ := net.Pipe()
		return c, nil
	}
	conn, err := limit.wrap(next)(withCapKey(context.Background(), "media.example:443"), "tcp", "proxy.example:3128")
	if err != nil {
		t.Fatal(err)
	}
	if gotAddr != "proxy.example:3128" {
		t.Fatalf("dialed %q, want the proxy", gotAddr)
	}
	if limit.open["media.example:443"] != 1 || limit.open["proxy.example:3128"] != 0 {
		t.Fatalf("open = %v", limit.open)
	}
	conn.Close()
	if len(limit.open) != 0 {
		t.Fatalf("open after close = %v", limit.open)
	}
	// Without a key the dial address is the key.
	conn, err = limit.wrap(next)(context.Background(), "tcp", "direct.example:443")
	if err != nil {
		t.Fatal(err)
	}
	if limit.open["direct.example:443"] != 1 {
		t.Fatalf("open = %v", limit.open)
	}
	conn.Close()
}
