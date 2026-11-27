package vela

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

// A refresh whose caller gave up mid-flight must leave its key replayable or
// re-executable. Vela derives the key from the refresh token, so a key stuck
// at 409 mutation_outcome_unknown strands the device forever.
func TestAbandonedRefreshSameKeyNeverStuck(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE vela_replays(key TEXT PRIMARY KEY,digest TEXT NOT NULL,response BLOB,expires INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	entered, proceed := make(chan struct{}), make(chan struct{})
	var id plexAuthTestIdentity
	id.ResolveDeviceFunc = func(_ context.Context, token string, d frontend.Device) (*frontend.Viewer, time.Time, error) {
		if strings.HasPrefix(token, "eg1_rotated_") && d.ID == "tv-1" {
			return &frontend.Viewer{ID: 1, Username: "owner"}, time.Unix(1_900_000_000, 0).UTC(), nil
		}
		return nil, time.Time{}, frontend.ErrUnauthorized
	}
	id.RefreshFunc = func(ctx context.Context, token string, _ frontend.Device) (string, string, error) {
		n := calls.Add(1)
		if n == 1 {
			close(entered)
			<-proceed // the caller has already gone away
			if ctx.Err() != nil {
				return "", "", ctx.Err()
			}
		}
		return fmt.Sprintf("eg1_rotated_%d", n), fmt.Sprintf("er1_rotated_%d", n), nil
	}
	inst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(t.Context(), frontend.Host{Identity: id, Experience: frontendtest.Experience{}, Sessions: frontendtest.Sessions{}})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(t.Context())
	h := inst.Handler()
	const body = `{"refreshToken":"er1_old","idempotencyKey":"refresh-er1_old","device":{"id":"tv-1","name":"TV","platform":"tvOS","appVersion":"1"}}`
	post := func(ctx context.Context) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/vela/v0/auth/refresh", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	refreshToken := func(w *httptest.ResponseRecorder) string {
		var out struct{ RefreshToken string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.RefreshToken
	}

	// 1. The client aborts while the rotation runs. The rotation still
	// commits, and the same key replays its tokens.
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { post(ctx); close(done) }()
	<-entered
	cancel()
	close(proceed)
	<-done
	retry := post(t.Context())
	if retry.Code != http.StatusOK || refreshToken(retry) != "er1_rotated_1" || calls.Load() != 1 {
		t.Fatalf("abandoned refresh retry: %d calls=%d %s", retry.Code, calls.Load(), retry.Body.String())
	}

	// 2. A claim whose owner died before recording anything answers a
	// retryable 409 while its lease runs, and executes once it lapses.
	key := hash("tv-1\x00POST\x00/vela/v0/auth/refresh\x00refresh-er1_old")
	if _, err := db.Exec(`UPDATE vela_replays SET response=NULL,expires=? WHERE key=?`, time.Now().Add(replayLease).UnixMilli(), key); err != nil {
		t.Fatal(err)
	}
	pending := post(t.Context())
	var problem struct {
		Error struct {
			Code  string
			Retry struct{ Allowed bool }
		}
	}
	_ = json.Unmarshal(pending.Body.Bytes(), &problem)
	if pending.Code != http.StatusConflict || problem.Error.Code != "mutation_outcome_unknown" || !problem.Error.Retry.Allowed || pending.Header().Get("Retry-After") == "" {
		t.Fatalf("pending claim: %d %s", pending.Code, pending.Body.String())
	}
	if _, err := db.Exec(`UPDATE vela_replays SET expires=0 WHERE key=?`, key); err != nil {
		t.Fatal(err)
	}
	if after := post(t.Context()); after.Code != http.StatusOK || refreshToken(after) != "er1_rotated_2" {
		t.Fatalf("lapsed claim: %d %s", after.Code, after.Body.String())
	}
}
