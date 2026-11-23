package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/startup"
)

func reserveEphemeralPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func httpGetNoRedirect(t *testing.T, rawURL string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestFrontDoorDedicatedConsoleListenerAndMainDisable(t *testing.T) {
	mainLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mainPort := mainLn.Addr().(*net.TCPAddr).Port
	consolePort := reserveEphemeralPort(t)
	if consolePort == mainPort {
		consolePort = reserveEphemeralPort(t)
	}

	var allowRemote atomic.Bool
	trustedProxies := func() []string { return nil }
	remoteFn := func() bool { return allowRemote.Load() }

	tracker := startup.NewTracker("test", time.Now)
	tracker.Fail(startup.Failure{}, 1, time.Second)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := newFrontDoor(tracker, log)
	f.setProxies(trustedProxies)
	f.setRemote(remoteFn)
	var recH http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("recovery-ok"))
	})
	f.recovery.Store(&recH)
	f.serve(mainLn)
	defer f.close()

	// Bind dedicated console listener with /ether disabled on the main port.
	f.reconcileConsole(config.Server{
		Port:                     mainPort,
		BindAddress:              "127.0.0.1",
		ConsolePort:              consolePort,
		DisableConsoleOnMainPort: true,
	})
	st := f.consoleStatus()
	if st.Fallback || st.ActivePort != consolePort || st.OnMainPort {
		t.Fatalf("consoleStatus = %+v, want active=%d onMain=false fallback=false", st, consolePort)
	}

	mainBase := fmt.Sprintf("http://127.0.0.1:%d", mainPort)
	consoleBase := fmt.Sprintf("http://127.0.0.1:%d", consolePort)

	// While starting (gate closed): dedicated port serves startup status, recovery API, and "/" redirect;
	// main port must NOT serve /ether/ or /ether/api/startup-status when disabled on main port.
	if resp, _ := httpGetNoRedirect(t, consoleBase+startup.StatusPath, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("console startup-status = %d, want 200", resp.StatusCode)
	}
	if resp, body := httpGetNoRedirect(t, consoleBase+startup.RecoveryPrefix+"info", nil); resp.StatusCode != http.StatusOK || body != "recovery-ok" {
		t.Fatalf("console recovery = %d %q, want 200 recovery-ok", resp.StatusCode, body)
	}
	if resp, _ := httpGetNoRedirect(t, consoleBase+"/", nil); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ether/" {
		t.Fatalf("console starting root = %d loc=%q, want 302 /ether/", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := httpGetNoRedirect(t, mainBase+startup.StatusPath, nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("main startup-status while disabled = %d, want 503 starting", resp.StatusCode)
	}
	if resp, _ := httpGetNoRedirect(t, mainBase+startup.RecoveryPrefix+"info", nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("main recovery while disabled = %d, want 503 starting", resp.StatusCode)
	}

	// Open both gates with realistic consoleMux and main mux gated on f.consoleOnMain.
	consoleMux := http.NewServeMux()
	consoleAdmin := lanOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("console-ok"))
	}), trustedProxies, remoteFn)
	consoleMux.Handle("/ether/", consoleAdmin)
	consoleRedirect := lanOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ether/", http.StatusFound)
	}), trustedProxies, remoteFn)
	consoleMux.Handle("/ether", consoleRedirect)
	consoleMux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			consoleRedirect.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	}))

	mainMux := http.NewServeMux()
	gateConsole := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !f.consoleOnMain.Load() {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	mainMux.Handle("/ether/", gateConsole(consoleMux))
	mainMux.Handle("/ether", gateConsole(consoleMux))
	mainMux.Handle("/", browserRootRedirectWhen(f.consoleOnMain.Load, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("plex-ok"))
	})))

	f.open(mainMux, consoleMux)

	// Main port: /ether/ and /ether return 404; browser GET / reaches Plex instead of redirecting to /ether/.
	if resp, _ := httpGetNoRedirect(t, mainBase+"/ether/", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("main /ether/ with disable=true = %d, want 404", resp.StatusCode)
	}
	if resp, _ := httpGetNoRedirect(t, mainBase+"/ether", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("main /ether with disable=true = %d, want 404", resp.StatusCode)
	}
	if resp, body := httpGetNoRedirect(t, mainBase+"/", map[string]string{"Accept": "text/html"}); resp.StatusCode != http.StatusOK || body != "plex-ok" {
		t.Fatalf("main browser GET / with disable=true = %d %q, want 200 plex-ok", resp.StatusCode, body)
	}

	// Dedicated console port: serves /ether/, redirects / -> /ether/, returns 404 for Plex paths,
	// and enforces lanOnly + allowRemoteConsole.
	if resp, body := httpGetNoRedirect(t, consoleBase+"/ether/", nil); resp.StatusCode != http.StatusOK || body != "console-ok" {
		t.Fatalf("console /ether/ = %d %q, want 200 console-ok", resp.StatusCode, body)
	}
	if resp, _ := httpGetNoRedirect(t, consoleBase+"/", nil); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ether/" {
		t.Fatalf("console / = %d loc=%q, want 302 /ether/", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := httpGetNoRedirect(t, consoleBase+"/library/sections", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("console /library/sections = %d, want 404", resp.StatusCode)
	}
	// Public client via loopback reverse proxy: blocked when allowRemoteConsole=false, allowed when true.
	if resp, _ := httpGetNoRedirect(t, consoleBase+"/ether/", map[string]string{"X-Forwarded-For": "198.51.100.7"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("public console request with allowRemote=false = %d, want 403", resp.StatusCode)
	}
	allowRemote.Store(true)
	if resp, body := httpGetNoRedirect(t, consoleBase+"/ether/", map[string]string{"X-Forwarded-For": "198.51.100.7"}); resp.StatusCode != http.StatusOK || body != "console-ok" {
		t.Fatalf("public console request with allowRemote=true = %d %q, want 200 console-ok", resp.StatusCode, body)
	}

	// Live toggle: re-enable /ether on main port without restarting.
	f.reconcileConsole(config.Server{
		Port:                     mainPort,
		BindAddress:              "127.0.0.1",
		ConsolePort:              consolePort,
		DisableConsoleOnMainPort: false,
	})
	if resp, body := httpGetNoRedirect(t, mainBase+"/ether/", nil); resp.StatusCode != http.StatusOK || body != "console-ok" {
		t.Fatalf("main /ether/ after re-enabling = %d %q, want 200 console-ok", resp.StatusCode, body)
	}
	if resp, body := httpGetNoRedirect(t, consoleBase+"/ether/", nil); resp.StatusCode != http.StatusOK || body != "console-ok" {
		t.Fatalf("console /ether/ still works = %d %q, want 200 console-ok", resp.StatusCode, body)
	}

	// Verify lanOnly directly on a non-loopback public RemoteAddr as well.
	rec := httptest.NewRecorder()
	allowRemote.Store(false)
	pubReq := httptest.NewRequest(http.MethodGet, "/ether/", nil)
	pubReq.RemoteAddr = "203.0.113.9:54321"
	consoleMux.ServeHTTP(rec, pubReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("direct public peer on consoleMux = %d, want 403", rec.Code)
	}
}

func TestFrontDoorConsoleBindFailureFallbackAndRecovery(t *testing.T) {
	mainLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mainPort := mainLn.Addr().(*net.TCPAddr).Port

	// Occupy a port to force dedicated console bind failure.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	busyPort := blocker.Addr().(*net.TCPAddr).Port
	defer blocker.Close()

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	tracker := startup.NewTracker("test", time.Now)
	tracker.Fail(startup.Failure{}, 1, time.Second)
	f := newFrontDoor(tracker, log)
	var recH http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("recovery-reachable"))
	})
	f.recovery.Store(&recH)
	f.serve(mainLn)
	defer f.close()

	// Even though DisableConsoleOnMainPort is true, bind failure MUST fall back
	// to the main port and log a warning so the owner is never locked out.
	f.reconcileConsole(config.Server{
		Port:                     mainPort,
		BindAddress:              "127.0.0.1",
		ConsolePort:              busyPort,
		DisableConsoleOnMainPort: true,
	})

	st := f.consoleStatus()
	if !st.Fallback || !st.OnMainPort || st.ActivePort != 0 || st.ConfiguredPort != busyPort || st.Error == "" {
		t.Fatalf("consoleStatus on bind failure = %+v, want fallback=true onMainPort=true activePort=0", st)
	}
	if !strings.Contains(logs.String(), "dedicated console port failed to bind") {
		t.Fatalf("expected bind failure warning log, got %q", logs.String())
	}

	mainBase := fmt.Sprintf("http://127.0.0.1:%d", mainPort)
	// Startup status AND recovery API stay reachable on the main port.
	if resp, _ := httpGetNoRedirect(t, mainBase+startup.StatusPath, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("fallback main startup-status = %d, want 200", resp.StatusCode)
	}
	if resp, body := httpGetNoRedirect(t, mainBase+startup.RecoveryPrefix+"info", nil); resp.StatusCode != http.StatusOK || body != "recovery-reachable" {
		t.Fatalf("fallback main recovery = %d %q, want 200 recovery-reachable", resp.StatusCode, body)
	}

	// Open the gate and verify /ether/ is reachable on the main port.
	mainMux := http.NewServeMux()
	consoleHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("console-fallback-ok"))
	})
	mainMux.Handle("/ether/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !f.consoleOnMain.Load() {
			http.NotFound(w, r)
			return
		}
		consoleHandler.ServeHTTP(w, r)
	}))
	f.open(mainMux, consoleHandler)

	if resp, body := httpGetNoRedirect(t, mainBase+"/ether/", nil); resp.StatusCode != http.StatusOK || body != "console-fallback-ok" {
		t.Fatalf("fallback main /ether/ = %d %q, want 200 console-fallback-ok", resp.StatusCode, body)
	}

	// Also verify custom listen error injection triggers fallback cleanly.
	f.listenConsole = func(string, string) (net.Listener, error) {
		return nil, errors.New("permission denied")
	}
	f.reconcileConsole(config.Server{
		Port:                     mainPort,
		BindAddress:              "127.0.0.1",
		ConsolePort:              busyPort + 1,
		DisableConsoleOnMainPort: true,
	})
	if st := f.consoleStatus(); !st.Fallback || !st.OnMainPort || !strings.Contains(st.Error, "permission denied") {
		t.Fatalf("injected error status = %+v", st)
	}

	// Once the blocker closes and normal listening is restored, reconciling binds the port and clears fallback.
	f.listenConsole = nil
	_ = blocker.Close()
	f.reconcileConsole(config.Server{
		Port:                     mainPort,
		BindAddress:              "127.0.0.1",
		ConsolePort:              busyPort,
		DisableConsoleOnMainPort: true,
	})
	if st := f.consoleStatus(); st.Fallback || st.OnMainPort || st.ActivePort != busyPort {
		t.Fatalf("recovered consoleStatus = %+v, want active=%d fallback=false onMain=false", st, busyPort)
	}
	if resp, _ := httpGetNoRedirect(t, mainBase+"/ether/", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("main /ether/ after dedicated port recovered = %d, want 404", resp.StatusCode)
	}
	if resp, body := httpGetNoRedirect(t, fmt.Sprintf("http://127.0.0.1:%d/ether/", busyPort), nil); resp.StatusCode != http.StatusOK || body != "console-fallback-ok" {
		t.Fatalf("dedicated /ether/ after recovery = %d %q, want 200", resp.StatusCode, body)
	}
}
