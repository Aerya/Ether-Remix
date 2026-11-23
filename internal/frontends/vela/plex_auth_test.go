package vela

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type plexAuthTestIdentity struct {
	frontendtest.Identity
	scope string
}

func (f plexAuthTestIdentity) GrantScope(_ context.Context, _ string, d frontend.Device) (string, error) {
	return f.scope + "-" + d.ID, nil
}

func (f plexAuthTestIdentity) GrantScopeCurrent(_ context.Context, _ string, _ frontend.Device) (*frontend.Viewer, error) {
	return &frontend.Viewer{ID: 1, Username: "owner"}, nil
}

func TestVelaPlexAuthCapabilitiesAndContract(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE vela_replays(key TEXT PRIMARY KEY,digest TEXT NOT NULL,response BLOB,expires INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	plexEnabled := false
	signInCalls := 0
	var id plexAuthTestIdentity
	id.scope = "fam"
	id.AuthenticationFeaturesFunc = func() frontend.AuthenticationFeatures {
		return frontend.AuthenticationFeatures{Password: true, PlexAccountSignIn: plexEnabled}
	}
	id.ResolveDeviceFunc = func(_ context.Context, token string, d frontend.Device) (*frontend.Viewer, time.Time, error) {
		if token == "eg1_owner_grant" && d.ID == "iphone-1" {
			return &frontend.Viewer{ID: 1, Username: "owner"}, time.Unix(1_900_000_000, 0).UTC(), nil
		}
		return nil, time.Time{}, frontend.ErrUnauthorized
	}
	id.IssueRefreshFunc = func(_ context.Context, access string, d frontend.Device) (string, error) {
		return "er1_refresh_" + d.ID + "_" + access, nil
	}
	id.SignInWithPlexFunc = func(_ context.Context, plexToken string, _ frontend.Device) (string, error) {
		signInCalls++
		switch plexToken {
		case "secret-owner-plex-token":
			return "eg1_owner_grant", nil
		case "secret-unshared-plex-token":
			return "", frontend.ErrPlexNotShared
		case "secret-rate-limited-token":
			return "", &frontend.IdentityRateLimitError{RetryAfter: 42 * time.Second}
		case "secret-outage-token":
			return "", frontend.ErrPlexUnreachable
		default:
			return "", frontend.ErrUnauthorized
		}
	}

	rawInst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(t.Context(), frontend.Host{
		Identity:       id,
		Experience:     frontendtest.Experience{},
		Sessions:       frontendtest.Sessions{},
		ServerIdentity: frontend.ServerIdentity{MachineID: "mach-1", FriendlyName: "Ether", Version: "1.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rawInst.Close(t.Context())
	h := rawInst.Handler()

	getCaps := func() map[string]any {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/vela/v0/capabilities", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("capabilities status %d: %s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// Capability flag omitted when Plex identity is not configured.
	if _, present := getCaps()["plexAccountSignIn"]; present {
		t.Fatalf("plexAccountSignIn advertised when disabled")
	}
	plexEnabled = true
	if got := getCaps()["plexAccountSignIn"]; got != true {
		t.Fatalf("plexAccountSignIn = %v, want true", got)
	}

	postPlex := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/vela/v0/auth/plex", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Success + idempotent replay + conflict + no raw token persistence.
	reqBody := `{"plexToken":"secret-owner-plex-token","idempotencyKey":"idem-owner-1","device":{"id":"iphone-1","name":"iPhone","platform":"iOS","appVersion":"0.2.0"}}`
	first := postPlex(reqBody)
	second := postPlex(reqBody)
	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body.String() != second.Body.String() || signInCalls != 1 {
		t.Fatalf("exchange/replay: first=%d second=%d calls=%d body=%s", first.Code, second.Code, signInCalls, first.Body.String())
	}
	if strings.Contains(first.Body.String(), "secret-owner-plex-token") {
		t.Fatalf("response echoed plexToken")
	}
	var tokResp struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    string `json:"expiresAt"`
		Viewer       struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			PolicyRevision string `json:"policyRevision"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &tokResp); err != nil {
		t.Fatal(err)
	}
	if tokResp.AccessToken != "eg1_owner_grant" || !strings.HasPrefix(tokResp.RefreshToken, "er1_") || tokResp.ExpiresAt == "" || tokResp.Viewer.Name != "owner" || tokResp.Viewer.ID == "" || tokResp.Viewer.PolicyRevision == "" {
		t.Fatalf("unexpected token payload: %+v", tokResp)
	}

	conflict := postPlex(`{"plexToken":"different-token","idempotencyKey":"idem-owner-1","device":{"id":"iphone-1","name":"iPhone","platform":"iOS","appVersion":"0.2.0"}}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict status = %d, want 409", conflict.Code)
	}

	rows, err := db.Query(`SELECT key, digest, response FROM vela_replays`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, d string
		var resp []byte
		if err := rows.Scan(&k, &d, &resp); err != nil {
			t.Fatal(err)
		}
		joined := k + "|" + d + "|" + string(resp)
		if strings.Contains(joined, "secret-owner-plex-token") || strings.Contains(joined, "eg1_owner_grant") {
			t.Fatalf("vela_replays leaked plaintext secret")
		}
	}

	// Error contracts: 401, 403, 429, 503 — never echoing the token.
	for _, tc := range []struct {
		key, token  string
		wantStatus  int
		wantClass   string
		wantCode    string
		wantRetry   bool
		retryHeader string
	}{
		{"idem-401", "secret-bad-plex-token", http.StatusUnauthorized, "auth", "invalid_credentials", false, ""},
		{"idem-403", "secret-unshared-plex-token", http.StatusForbidden, "policy", "not_shared", false, ""},
		{"idem-429", "secret-rate-limited-token", http.StatusTooManyRequests, "capacity", "rate_limited", true, "42"},
		{"idem-503", "secret-outage-token", http.StatusServiceUnavailable, "transient", "plex_unreachable", false, ""},
	} {
		w := postPlex(`{"plexToken":"` + tc.token + `","idempotencyKey":"` + tc.key + `","device":{"id":"iphone-1","name":"iPhone","platform":"iOS","appVersion":"0.2.0"}}`)
		if w.Code != tc.wantStatus {
			t.Fatalf("%s: status=%d want=%d body=%s", tc.wantCode, w.Code, tc.wantStatus, w.Body.String())
		}
		if strings.Contains(w.Body.String(), tc.token) {
			t.Fatalf("%s echoed token in error body: %s", tc.wantCode, w.Body.String())
		}
		if got := w.Header().Get("Retry-After"); got != tc.retryHeader {
			t.Fatalf("%s Retry-After=%q want=%q", tc.wantCode, got, tc.retryHeader)
		}
		var env struct {
			Error struct {
				Class   string `json:"class"`
				Code    string `json:"code"`
				Message string `json:"message"`
				Retry   struct {
					Allowed      bool `json:"allowed"`
					AfterSeconds int  `json:"afterSeconds"`
					MaxAttempts  int  `json:"maxAttempts"`
				} `json:"retry"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Error.Class != tc.wantClass || env.Error.Code != tc.wantCode || env.Error.Message == "" || env.Error.Retry.Allowed != tc.wantRetry {
			t.Fatalf("%s wire mismatch: %+v", tc.wantCode, env.Error)
		}
		if tc.wantCode == "not_shared" && !strings.Contains(strings.ToLower(env.Error.Message), "owner") {
			t.Fatalf("not_shared message should advise asking owner to share: %q", env.Error.Message)
		}
	}
}
