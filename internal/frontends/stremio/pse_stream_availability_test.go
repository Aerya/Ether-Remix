package stremio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type decidingLazy struct {
	lazyFixture
	decision frontend.WarmDecision
	wait     time.Duration
}

func (d *decidingLazy) WarmDecision(ctx context.Context, _ string, _ frontend.PlanRequest, _ time.Duration) frontend.WarmDecision {
	if d.wait > 0 {
		select {
		case <-time.After(d.wait):
		case <-ctx.Done():
			return frontend.WarmUnknown
		}
	}
	return d.decision
}

func streamWith(t *testing.T, lazy frontend.LazyStreams) *httptest.ResponseRecorder {
	t.Helper()
	host := frontend.Host{
		Identity:   frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return &frontend.Viewer{ID: 7}, nil }},
		Experience: frontendtest.Experience{}, Sessions: frontendtest.Sessions{}, LazyStreams: lazy,
		Capabilities: frontendtest.Capabilities{}, ClientIP: func(*http.Request) (netip.Addr, bool) { return netip.MustParseAddr("192.168.1.7"), true },
		SecurePortalURL: func(context.Context, *http.Request) (string, error) { return "https://ether.example/ether/me", nil },
	}
	inst, err := (Frontend{}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stremio/grant/stream/movie/tmdb:42.json", nil))
	return w
}

func TestStreamListIsEmptyWhenNoSourceExists(t *testing.T) {
	w := streamWith(t, &decidingLazy{decision: frontend.WarmNoSource})
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"streams":[]}` || !strings.Contains(w.Header().Get("Cache-Control"), "max-age=120") {
		t.Fatalf("response: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
}

func TestStreamListKeepsStreamWithSourceOrDoubt(t *testing.T) {
	for _, d := range []frontend.WarmDecision{frontend.WarmHasSource, frontend.WarmUnknown} {
		w := streamWith(t, &decidingLazy{decision: d})
		if w.Code != 200 || !strings.Contains(w.Body.String(), "/stremio/sessions/lazy/scoped/file") {
			t.Fatalf("decision %v: %d %s", d, w.Code, w.Body.String())
		}
	}
}

func TestStreamListWaitIsBounded(t *testing.T) {
	start := time.Now()
	w := streamWith(t, &decidingLazy{decision: frontend.WarmNoSource, wait: time.Minute})
	if elapsed := time.Since(start); elapsed > streamDecisionBudget+2*time.Second {
		t.Fatalf("waited %s", elapsed)
	}
	if !strings.Contains(w.Body.String(), "/stremio/sessions/lazy/scoped/file") {
		t.Fatalf("a late verdict must keep the stream: %s", w.Body.String())
	}
}
