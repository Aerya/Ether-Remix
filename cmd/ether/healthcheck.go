package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/service"
)

const defaultHealthcheckTimeout = 3 * time.Second

// runHealthcheck probes the local Ether server's public /ether/api/health
// endpoint and returns 0 when healthy (with no output), 1 otherwise.
func runHealthcheck(args []string, defaultDataDir string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	fset := flag.NewFlagSet("ether healthcheck", flag.ContinueOnError)
	fset.SetOutput(stderr)
	dataDir := fset.String("data-dir", defaultDataDir, "data directory (config, database, cache)")
	timeout := fset.Duration("timeout", defaultHealthcheckTimeout, "probe timeout")
	if err := fset.Parse(args); err != nil {
		return 1
	}
	if fset.NArg() > 0 {
		fmt.Fprintf(stderr, "ether healthcheck: unexpected argument %q\n", fset.Arg(0))
		return 1
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "ether healthcheck: timeout must be positive")
		return 1
	}
	baseURLs, err := healthcheckBaseURLs(*dataDir, os.Getenv)
	if err != nil {
		fmt.Fprintln(stderr, "ether healthcheck:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var lastErr error
	for _, baseURL := range baseURLs {
		if err := probeLocalHealth(ctx, baseURL+"/ether/api/health", *timeout); err == nil {
			return 0
		} else {
			lastErr = err
		}
	}
	fmt.Fprintln(stderr, "ether healthcheck:", lastErr)
	return 1
}

func healthcheckBaseURL(dataDir string, getenv func(string) string) (string, error) {
	urls, err := healthcheckBaseURLs(dataDir, getenv)
	if err != nil {
		return "", err
	}
	return urls[0], nil
}

func healthcheckBaseURLs(dataDir string, getenv func(string) string) ([]string, error) {
	if dataDir == "" {
		dataDir = config.DefaultDataDir()
	}
	port := config.DefaultPort
	consolePort := 0
	bindAddress := ""

	raw, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	switch {
	case err == nil:
		var doc struct {
			Server struct {
				Port        int    `json:"port"`
				ConsolePort int    `json:"consolePort"`
				BindAddress string `json:"bindAddress"`
			} `json:"server"`
		}
		if jerr := json.Unmarshal(raw, &doc); jerr != nil {
			return nil, fmt.Errorf("read config.json: %w", jerr)
		}
		if doc.Server.Port >= 1 && doc.Server.Port <= 65535 {
			port = doc.Server.Port
		}
		if doc.Server.ConsolePort >= 1 && doc.Server.ConsolePort <= 65535 {
			consolePort = doc.Server.ConsolePort
		}
		bindAddress = doc.Server.BindAddress
	case errors.Is(err, fs.ErrNotExist):
		// Fresh install before config.json is written: default port applies.
	default:
		if getenv == nil || (strings.TrimSpace(getenv("ETHER_PORT")) == "" && strings.TrimSpace(getenv("ETHER_CONSOLE_PORT")) == "") {
			if localURL, lerr := service.ReadLocalURL(dataDir); lerr == nil && localURL != "" {
				return []string{strings.TrimRight(localURL, "/")}, nil
			}
		}
	}

	if getenv != nil {
		if v := strings.TrimSpace(getenv("ETHER_PORT")); v != "" {
			p, perr := strconv.Atoi(v)
			if perr != nil || p < 1 || p > 65535 {
				return nil, fmt.Errorf("ETHER_PORT=%q: want a port between 1 and 65535", v)
			}
			port = p
		}
		if v := strings.TrimSpace(getenv("ETHER_CONSOLE_PORT")); v != "" {
			p, perr := strconv.Atoi(v)
			if perr != nil || p < 1 || p > 65535 {
				return nil, fmt.Errorf("ETHER_CONSOLE_PORT=%q: want a port between 1 and 65535", v)
			}
			consolePort = p
		}
	}

	host := strings.Trim(strings.TrimSpace(bindAddress), "[]")
	if ip := net.ParseIP(host); host == "" || strings.EqualFold(host, "localhost") || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	mainURL := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	if consolePort > 0 && consolePort != port {
		consoleURL := "http://" + net.JoinHostPort(host, strconv.Itoa(consolePort))
		return []string{consoleURL, mainURL}, nil
	}
	return []string{mainURL}, nil
}

func probeLocalHealth(ctx context.Context, endpoint string, timeout time.Duration) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !body.OK {
		return errors.New("server reported ok=false")
	}
	return nil
}
