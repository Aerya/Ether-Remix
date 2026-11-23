package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func TestMediaBrowserPreparingPlaybackAndStreamReturn503RetryAfter(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	host := frontend.Host{
		Identity:     frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
		Capabilities: frontendtest.Capabilities{},
		Sessions: frontendtest.Sessions{
			PlanFunc: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
				return frontend.PlayPlan{ID: "plan", Decision: frontend.Decision{Kind: "directplay"}}, nil
			},
			OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
				return frontend.Session{ID: "prep-session"}, nil
			},
			StartStreamFunc: func(context.Context, frontend.SessionAccess, string, frontend.StreamRequest) (string, error) {
				return "", &frontend.PreparingError{RetryAfter: 7 * time.Second}
			},
			SessionsFunc: func(context.Context, frontend.SessionAccess) ([]frontend.Session, error) {
				return []frontend.Session{{ID: "prep-session", Item: 5, Protocol: "jellyfin", Device: frontend.Device{ID: "tv"}}}, nil
			},
		},
	}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	id := ids.RatingKeyGUID(5)
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/Items/" + id + "/PlaybackInfo", `{"IsPlayback":true}`},
		{http.MethodGet, "/Videos/" + id + "/stream?PlaySessionId=prep-session", ""},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("X-Emby-Token", "account-secret")
		r.Header.Set("X-Emby-Device-Id", "tv")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status = %d, want 503; body = %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if got := w.Header().Get("Retry-After"); got != "7" {
			t.Fatalf("%s %s: Retry-After = %q, want 7", tc.method, tc.path, got)
		}
		if got := w.Header().Get("X-Ether-Media-Error"); got != "preparing" {
			t.Fatalf("%s %s: X-Ether-Media-Error = %q, want preparing", tc.method, tc.path, got)
		}
		var wire struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil || wire.Error.Code != "preparing" {
			t.Fatalf("%s %s: body = %s, err = %v", tc.method, tc.path, w.Body.String(), err)
		}
	}
}
