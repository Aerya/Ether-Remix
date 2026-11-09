package seerrcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbe(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"empty feed", 200, `{"schema":"seerrcatalog.availability.v1","revision":0,"items":[]}`, "ok"},
		{"bad token", 401, `secret response`, "unauthorized"},
		{"disabled export", 403, `secret response`, "unauthorized"},
		{"wrong path", 404, ``, "not_found"},
		{"server error", 500, ``, "unavailable"},
		{"login page", 200, `<html>login</html>`, "invalid_feed"},
		{"missing items", 200, `{"schema":"seerrcatalog.availability.v1","revision":0}`, "invalid_feed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
					t.Error("missing auth")
				}
				if r.URL.Path != "/api/integrations/remix/v1/availability" || r.URL.Query().Get("limit") != "1" {
					t.Error("unexpected request")
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			got := New(nil, server.Client()).Probe(context.Background(), Settings{URL: server.URL, Token: strings.Repeat("x", 32)})
			if got != tc.want || calls != 1 {
				t.Fatalf("got %s calls %d", got, calls)
			}
		})
	}
	s := New(nil, nil)
	if s.Probe(context.Background(), Settings{}) != "configuration" {
		t.Fatal("invalid config accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.Probe(ctx, Settings{URL: "http://127.0.0.1:1", Token: strings.Repeat("x", 32)}) != "unreachable" {
		t.Fatal("cancel not reported")
	}
}

func TestProbeRefusesRedirect(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	got := New(nil, source.Client()).Probe(context.Background(), Settings{URL: source.URL, Token: strings.Repeat("x", 32)})
	if got != "unreachable" || targetCalls != 0 {
		t.Fatalf("redirect leaked: %s, %d calls", got, targetCalls)
	}
}
