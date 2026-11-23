package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/stats"
)

func TestActivityPlayTimelineRouteAndInstalledHandler(t *testing.T) {
	ctx := context.Background()
	a := setupTestApp(t, false)

	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://localhost/ether/api"+path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1000"
		req.Header.Set("X-Ether-Request", "1")
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Ether-Admin-Token", token)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		return w
	}

	setup := request(http.MethodPost, "/setup/local", `{"username":"console-owner","password":"console-owner-test-password"}`, "")
	if setup.Code != http.StatusOK {
		t.Fatalf("owner setup %d: %s", setup.Code, setup.Body.String())
	}
	var auth struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(setup.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}

	started := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Second)
	ps := &core.PlaySession{
		ID:          "app-play-1",
		UserID:      1,
		ClientID:    "tv-1",
		Key:         core.MovieKey(603),
		Username:    "Owner",
		TitleName:   "The Matrix (1999)",
		Client:      core.ClientCaps{ClientID: "tv-1", DeviceName: "Living Room TV", Platform: "tvOS"},
		StartedAt:   started,
		BytesServed: 0,
		Experience: &core.PlayExperience{
			Requested:    true,
			RequestedAt:  started,
			StartTime:    -1,
			StartFailure: "every release failed (2 tried): open The.Matrix.1999: missing article",
		},
	}

	// Emit lifecycle events + session-scoped logs through App's installed slog handler.
	a.observePlayTimeline(playback.TimelineEvent{
		SessionID: ps.ID,
		At:        started,
		Kind:      "started",
		Level:     "info",
		Message:   "Playback requested",
		Detail:    "Living Room TV · tvOS",
	})
	a.log.Debug("opening candidate header", "session", ps.ID, "apikey", "topsecret-api-key")
	a.log.Warn("candidate failed", "session", ps.ID, "release", "The.Matrix.1999.2160p", "err", "430 missing article")
	a.observePlayTimeline(playback.TimelineEvent{
		SessionID: ps.ID,
		At:        started.Add(2400 * time.Millisecond),
		Kind:      "error",
		Level:     "error",
		Failing:   true,
		Message:   "Playback failed",
		Detail:    "every release failed (2 tried): open The.Matrix.1999: missing article",
	})
	if err := a.stats.Record(ctx, ps, true, "every release failed (2 tried): open The.Matrix.1999: missing article"); err != nil {
		t.Fatal(err)
	}
	if err := a.stats.FlushTimeline(ctx); err != nil {
		t.Fatal(err)
	}

	// Unauthenticated and non-owner requests must be rejected with 401.
	if unauth := request(http.MethodGet, "/activity/plays/app-play-1/timeline", "", ""); unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated timeline status = %d, want 401", unauth.Code)
	}
	if bogus := request(http.MethodGet, "/activity/plays/app-play-1/timeline", "", "not-an-owner-token"); bogus.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token timeline status = %d, want 401", bogus.Code)
	}

	// Owner-authenticated request returns 200 with redacted timeline + buffered debug log.
	rec := request(http.MethodGet, "/activity/plays/app-play-1/timeline", "", auth.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated timeline status = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "topsecret-api-key") {
		t.Fatalf("timeline response leaked secret: %s", rec.Body.String())
	}
	var got stats.PlayTimeline
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Failed || !strings.Contains(got.Error, "every release failed (2 tried)") {
		t.Fatalf("unexpected failed timeline metadata: %+v", got)
	}
	if len(got.Entries) < 4 {
		t.Fatalf("expected >=4 timeline entries (started, debug log, warn log, error), got %+v", got.Entries)
	}
}
