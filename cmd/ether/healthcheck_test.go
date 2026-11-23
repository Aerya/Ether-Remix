package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

func TestHealthcheckSubcommand(t *testing.T) {
	var modeMu sync.Mutex
	var modeValue string
	setMode := func(m string) { modeMu.Lock(); modeValue = m; modeMu.Unlock() }
	getMode := func() string { modeMu.Lock(); defer modeMu.Unlock(); return modeValue }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ether/api/health" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Ether-Admin-Token") != "" {
			t.Errorf("unexpected auth header on health probe: %v", r.Header)
		}
		switch getMode() {
		case "ok":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"version":"test","authRequired":true}`))
		case "starting":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"starting","phase":"migrating"}}`))
		case "slow":
			time.Sleep(150 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()

	_, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(`{"server":{"port":%d,"bindAddress":"0.0.0.0"}}`, port)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ETHER_DATA_DIR", dir)
	t.Setenv("ETHER_PORT", "")

	// Healthy server: exit 0 and zero output on stdout/stderr, without auth.
	setMode("ok")
	var stdout, stderr bytes.Buffer
	if code := runHealthcheck(nil, config.DefaultDataDir(), &stdout, &stderr); code != 0 {
		t.Fatalf("healthy exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected silent success, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	// Starting / 503: exit 1.
	setMode("starting")
	stdout.Reset()
	stderr.Reset()
	if code := runHealthcheck(nil, config.DefaultDataDir(), &stdout, &stderr); code != 1 {
		t.Fatalf("starting exit = %d, want 1", code)
	}

	// Exceeding timeout: exit 1.
	setMode("slow")
	stdout.Reset()
	stderr.Reset()
	if code := runHealthcheck([]string{"--timeout", "40ms"}, config.DefaultDataDir(), &stdout, &stderr); code != 1 {
		t.Fatalf("slow exit = %d, want 1", code)
	}

	// ETHER_PORT overrides config.json port, and missing data dir is not created.
	setMode("ok")
	missingDir := filepath.Join(dir, "does-not-exist")
	t.Setenv("ETHER_DATA_DIR", missingDir)
	t.Setenv("ETHER_PORT", portStr)
	stdout.Reset()
	stderr.Reset()
	if code := runHealthcheck(nil, config.DefaultDataDir(), &stdout, &stderr); code != 0 {
		t.Fatalf("ETHER_PORT override exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if _, err := os.Stat(missingDir); !os.IsNotExist(err) {
		t.Fatalf("healthcheck created missing data dir %s", missingDir)
	}
}

func TestHealthcheckConsolePortAndFallback(t *testing.T) {
	mainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // /ether disabled on main port
	}))
	defer mainSrv.Close()
	consoleSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ether/api/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer consoleSrv.Close()

	_, mainPortStr, _ := net.SplitHostPort(mainSrv.Listener.Addr().String())
	_, consolePortStr, _ := net.SplitHostPort(consoleSrv.Listener.Addr().String())
	mainPort, _ := strconv.Atoi(mainPortStr)
	consolePort, _ := strconv.Atoi(consolePortStr)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(`{"server":{"port":%d,"consolePort":%d,"disableConsoleOnMainPort":true}}`, mainPort, consolePort)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ETHER_DATA_DIR", dir)
	t.Setenv("ETHER_PORT", "")
	t.Setenv("ETHER_CONSOLE_PORT", "")

	var stdout, stderr bytes.Buffer
	if code := runHealthcheck(nil, config.DefaultDataDir(), &stdout, &stderr); code != 0 {
		t.Fatalf("dedicated consolePort healthcheck = %d, want 0 (stderr=%q)", code, stderr.String())
	}

	// Fallback: when consolePort fails to bind (closed), healthcheck falls back to main port.
	fallbackMain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ether/api/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer fallbackMain.Close()
	_, fbPortStr, _ := net.SplitHostPort(fallbackMain.Listener.Addr().String())
	fbPort, _ := strconv.Atoi(fbPortStr)
	consoleSrv.Close() // dedicated port now closed -> simulates fallback to main port
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(`{"server":{"port":%d,"consolePort":%d}}`, fbPort, consolePort)), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runHealthcheck(nil, config.DefaultDataDir(), &stdout, &stderr); code != 0 {
		t.Fatalf("fallback healthcheck = %d, want 0 (stderr=%q)", code, stderr.String())
	}
}
