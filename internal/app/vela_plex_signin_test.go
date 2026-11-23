package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
)

type velaPlexTVRoundTripper struct {
	mu        sync.Mutex
	machineID string
	down      bool
	accounts  map[string]plextv.Account
	shared    map[string]bool
}

func (f *velaPlexTVRoundTripper) setDown(v bool) {
	f.mu.Lock()
	f.down = v
	f.mu.Unlock()
}

func (f *velaPlexTVRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	down := f.down
	machineID := f.machineID
	tok := r.Header.Get("X-Plex-Token")
	acct, known := f.accounts[tok]
	hasServer := f.shared[tok]
	f.mu.Unlock()

	if down {
		return nil, errors.New("plex.tv connection refused")
	}
	reply := func(status int, contentType, body string) *http.Response {
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{contentType}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}
	}
	if !known {
		return reply(http.StatusUnauthorized, "application/json", `{"errors":[{"code":1001,"message":"User could not be authenticated"}]}`), nil
	}
	switch r.URL.Path {
	case "/api/v2/user":
		payload, _ := json.Marshal(map[string]any{
			"id":         acct.ID,
			"uuid":       acct.UUID,
			"username":   acct.Username,
			"title":      acct.DisplayName(),
			"email":      acct.Email,
			"home":       acct.Home,
			"restricted": acct.Restricted,
		})
		return reply(http.StatusOK, "application/json", string(payload)), nil
	case "/api/v2/resources":
		if hasServer {
			payload, _ := json.Marshal([]map[string]any{{
				"name":             "Ether",
				"provides":         "server",
				"clientIdentifier": machineID,
				"owned":            acct.ID == 42,
			}})
			return reply(http.StatusOK, "application/json", string(payload)), nil
		}
		return reply(http.StatusOK, "application/json", `[]`), nil
	default:
		return reply(http.StatusNotFound, "application/json", `{}`), nil
	}
}

func TestVelaPlexAccountSignInAndCapabilities(t *testing.T) {
	fake := &velaPlexTVRoundTripper{
		accounts: map[string]plextv.Account{
			"plex-owner-iphone":    {ID: 42, UUID: "u-owner", Username: "owner", Title: "Owner"},
			"plex-alice-iphone":    {ID: 100, UUID: "u-alice", Username: "alice", Title: "Alice"},
			"plex-home-kid-iphone": {ID: 300, UUID: "u-kid", Username: "kid", Title: "Kid", Home: true, Restricted: true},
			"plex-stranger-iphone": {ID: 999, UUID: "u-stranger", Username: "stranger", Title: "Stranger"},
		},
		shared: map[string]bool{
			"plex-owner-iphone":    true,
			"plex-alice-iphone":    true,
			"plex-home-kid-iphone": true,
			"plex-stranger-iphone": false,
		},
	}

	a := setupTestAppConfigured(t, true, Options{
		plextvHTTP: &http.Client{Transport: fake},
	}, func(c *config.Config) {
		c.Plex.Claimed = false
		c.Plex.OwnerPlexID = 0
	})
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Plex.Token = ""
		c.Plex.Claimed = false
		c.Plex.OwnerPlexID = 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.machineID = a.cfg.Read().Server.MachineIdentifier
	fake.mu.Unlock()

	ctx := context.Background()
	if err := a.frontends.control.listeners.start(ctx, "vela"); err != nil {
		t.Fatal(err)
	}
	velaInst, ok := a.frontends.registry.Instance("vela")
	if !ok {
		t.Fatal("vela frontend not mounted")
	}
	handler := velaInst.Handler()

	getCapabilities := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/vela/v0/capabilities", nil)
		req.RemoteAddr = "198.51.100.10:54321"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /vela/v0/capabilities = %d: %s", rec.Code, rec.Body.String())
		}
		var caps map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &caps); err != nil {
			t.Fatal(err)
		}
		return caps
	}

	postPlexAuth := func(ip, idemKey, deviceID, plexToken string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"plexToken":%q,"idempotencyKey":%q,"device":{"id":%q,"name":"iPhone","platform":"iOS","appVersion":"0.2.0"}}`, plexToken, idemKey, deviceID)
		req := httptest.NewRequest(http.MethodPost, "/vela/v0/auth/plex", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = ip + ":54321"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// 5a. Before Plex identity is configured, capabilities must NOT advertise plexAccountSignIn,
	// and exchange attempts are rejected with 401 invalid_credentials.
	if _, ok := getCapabilities()["plexAccountSignIn"]; ok {
		t.Fatalf("plexAccountSignIn advertised on unconfigured server")
	}
	unconf := postPlexAuth("198.51.100.11", "unconf-1", "iphone-owner", "plex-owner-iphone")
	if unconf.Code != http.StatusUnauthorized {
		t.Fatalf("unconfigured exchange status = %d, want 401 (%s)", unconf.Code, unconf.Body.String())
	}

	// Configure Plex owner & shared friend Alice (restricted to Movies library 1 and PG-13).
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Plex.Claimed = true
		c.Plex.OwnerPlexID = 42
		c.Plex.OwnerUsername = "owner"
		c.Plex.Token = "server-secret-token"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.users.BootstrapOwner(ctx, a.cfg.Read().Plex); err != nil {
		t.Fatal(err)
	}
	if err := a.users.SyncFriends(ctx, []plextv.Friend{
		{Owned: true, PlexID: 42, Token: "owner-synced-token"},
		{PlexID: 100, UUID: "u-alice", Username: "alice", Title: "Alice", Token: "alice-synced-token", Sections: []string{"1"}},
	}); err != nil {
		t.Fatal(err)
	}
	aliceUser, err := a.users.Authenticate(ctx, "alice-synced-token")
	if err != nil {
		t.Fatal(err)
	}
	aliceUser.MaxContentRating = "PG-13"
	if err := a.users.UpdateUser(ctx, *aliceUser); err != nil {
		t.Fatal(err)
	}

	// 5b. Capabilities now advertises plexAccountSignIn: true, while ExternalIdentity surfaces never do.
	if got := getCapabilities()["plexAccountSignIn"]; got != true {
		t.Fatalf("plexAccountSignIn = %v, want true", got)
	}
	grants, err := identity.NewGrants(a.db, a.users, 24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	loginLim, err := identity.NewLoginLimiter(a.db, time.Minute, 20, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	extSurface := surfaceIdentity{
		policy:     core.FrontendHostPolicy{ExternalIdentity: true},
		hostConfig: a.cfg.Read,
		surface:    "plex",
		users:      a.users,
		grants:     grants,
		login:      loginLim,
	}
	if extSurface.AuthenticationFeatures().PlexAccountSignIn {
		t.Fatal("ExternalIdentity surface advertised PlexAccountSignIn")
	}
	extCtx := frontend.WithClientIP(ctx, netip.MustParseAddr("198.51.100.12"))
	if _, err := extSurface.SignInWithPlex(extCtx, "plex-owner-iphone", core.Device{ID: "d1"}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("ExternalIdentity SignInWithPlex = %v, want ErrUnauthorized", err)
	}

	// 1. Owner's Plex account token exchanges for Vela access + refresh tokens and rotates cleanly.
	ownerRes := postPlexAuth("198.51.100.20", "owner-exchange-1", "iphone-owner", "plex-owner-iphone")
	if ownerRes.Code != http.StatusOK {
		t.Fatalf("owner exchange = %d: %s", ownerRes.Code, ownerRes.Body.String())
	}
	var ownerTokens struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    string `json:"expiresAt"`
		Viewer       struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			PolicyRevision string `json:"policyRevision"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(ownerRes.Body.Bytes(), &ownerTokens); err != nil {
		t.Fatal(err)
	}
	if !core.IsGrantCredential(ownerTokens.AccessToken) || !strings.HasPrefix(ownerTokens.RefreshToken, "er1_") || ownerTokens.Viewer.Name != "owner" {
		t.Fatalf("unexpected owner tokens: %+v", ownerTokens)
	}

	// Verify 90-day idle / 1-year absolute refresh family and working refresh rotation.
	var absExp, idleExp int64
	if err := a.db.QueryRow(`SELECT absolute_expiry, idle_expiry FROM identity_refresh_families WHERE surface='vela' AND device_id='iphone-owner'`).Scan(&absExp, &idleExp); err != nil {
		t.Fatal(err)
	}
	span := time.Duration(absExp-idleExp) * time.Millisecond
	if span != (365-90)*24*time.Hour {
		t.Fatalf("refresh family span = %v, want 275d (1y absolute - 90d idle)", span)
	}
	refreshReq := httptest.NewRequest(http.MethodPost, "/vela/v0/auth/refresh", strings.NewReader(fmt.Sprintf(`{"refreshToken":%q,"idempotencyKey":"owner-refresh-1","device":{"id":"iphone-owner","name":"iPhone","platform":"iOS","appVersion":"0.2.0"}}`, ownerTokens.RefreshToken)))
	refreshReq.Header.Set("Content-Type", "application/json")
	refreshReq.RemoteAddr = "198.51.100.20:54321"
	refreshRec := httptest.NewRecorder()
	handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh = %d: %s", refreshRec.Code, refreshRec.Body.String())
	}

	// 2. Shared user's Plex token works and enforces Alice's library restrictions.
	aliceRes := postPlexAuth("198.51.100.21", "alice-exchange-1", "iphone-alice", "plex-alice-iphone")
	if aliceRes.Code != http.StatusOK {
		t.Fatalf("alice exchange = %d: %s", aliceRes.Code, aliceRes.Body.String())
	}
	var aliceTokens struct {
		AccessToken string `json:"accessToken"`
		Viewer      struct {
			Name           string `json:"name"`
			PolicyRevision string `json:"policyRevision"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(aliceRes.Body.Bytes(), &aliceTokens); err != nil {
		t.Fatal(err)
	}
	if aliceTokens.Viewer.Name != "alice" {
		t.Fatalf("alice viewer = %+v", aliceTokens.Viewer)
	}
	libReq := httptest.NewRequest(http.MethodGet, "/vela/v0/libraries", nil)
	libReq.Header.Set("Authorization", "Bearer "+aliceTokens.AccessToken)
	libReq.Header.Set("X-Vela-Device-Id", "iphone-alice")
	libReq.RemoteAddr = "198.51.100.21:54321"
	libRec := httptest.NewRecorder()
	handler.ServeHTTP(libRec, libReq)
	if libRec.Code != http.StatusOK {
		t.Fatalf("alice libraries = %d: %s", libRec.Code, libRec.Body.String())
	}
	var libDoc struct {
		Libraries []struct {
			Kind string `json:"kind"`
		} `json:"libraries"`
	}
	if err := json.Unmarshal(libRec.Body.Bytes(), &libDoc); err != nil {
		t.Fatal(err)
	}
	for _, lib := range libDoc.Libraries {
		if lib.Kind == "show" {
			t.Fatalf("alice saw unshared Shows library: %+v", libDoc.Libraries)
		}
	}

	// Home user token also works.
	kidRes := postPlexAuth("198.51.100.22", "kid-exchange-1", "ipad-kid", "plex-home-kid-iphone")
	if kidRes.Code != http.StatusOK {
		t.Fatalf("home kid exchange = %d: %s", kidRes.Code, kidRes.Body.String())
	}

	usersBeforeBad, _ := a.users.AllUsers(ctx)

	// 3. Unknown account, server token, delegation token and Ether grant -> 401 invalid_credentials.
	ownerObj, _ := a.users.Owner(ctx)
	delToken, err := a.users.IssueDelegation(ctx, ownerObj.ID)
	if err != nil {
		t.Fatal(err)
	}
	for idx, tok := range []string{"unknown-plex-token-xyz", "server-secret-token", delToken, ownerTokens.AccessToken} {
		rec := postPlexAuth("198.51.100.23", fmt.Sprintf("bad-%d", idx), "iphone-bad", tok)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("bad token %d status = %d, want 401 (%s)", idx, rec.Code, rec.Body.String())
		}
		var errDoc struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &errDoc)
		if errDoc.Error.Code != "invalid_credentials" || strings.Contains(rec.Body.String(), tok) {
			t.Fatalf("bad token %d body = %s", idx, rec.Body.String())
		}
	}

	// 4. Valid but unshared Plex account -> 403 not_shared, no viewer created.
	strangerRes := postPlexAuth("198.51.100.24", "stranger-1", "iphone-stranger", "plex-stranger-iphone")
	if strangerRes.Code != http.StatusForbidden {
		t.Fatalf("stranger status = %d, want 403 (%s)", strangerRes.Code, strangerRes.Body.String())
	}
	var strangerErr struct {
		Error struct {
			Class   string `json:"class"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(strangerRes.Body.Bytes(), &strangerErr); err != nil {
		t.Fatal(err)
	}
	if strangerErr.Error.Class != "policy" || strangerErr.Error.Code != "not_shared" || strings.Contains(strangerRes.Body.String(), "plex-stranger-iphone") {
		t.Fatalf("stranger error = %+v", strangerErr.Error)
	}
	usersAfterBad, _ := a.users.AllUsers(ctx)
	if len(usersAfterBad) != len(usersBeforeBad) {
		t.Fatalf("rejected Plex sign-in created viewer: got %d want %d", len(usersAfterBad), len(usersBeforeBad))
	}

	// 503 plex_unreachable when plex.tv cannot be reached for an uncached token.
	fake.setDown(true)
	outageRes := postPlexAuth("198.51.100.25", "outage-1", "iphone-outage", "plex-uncached-outage-token")
	fake.setDown(false)
	if outageRes.Code != http.StatusServiceUnavailable {
		t.Fatalf("outage status = %d, want 503 (%s)", outageRes.Code, outageRes.Body.String())
	}
	var outageErr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(outageRes.Body.Bytes(), &outageErr)
	if outageErr.Error.Code != "plex_unreachable" {
		t.Fatalf("outage error = %s", outageRes.Body.String())
	}

	// Rate limit (429 with Retry-After) on repeated attempts from one IP/account.
	got429 := false
	for attempt := 0; attempt < 15; attempt++ {
		rec := postPlexAuth("198.51.100.26", fmt.Sprintf("rl-%d", attempt), "iphone-rl", "plex-stranger-iphone")
		if rec.Code == http.StatusTooManyRequests {
			if rec.Header().Get("Retry-After") == "" {
				t.Fatalf("429 missing Retry-After header: %s", rec.Body.String())
			}
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("expected 429 rate_limited after repeated attempts")
	}

	// Ensure no plaintext Plex token was persisted in auth_tokens or identity_grants.
	var leaked int
	if err := a.db.QueryRow(`SELECT count(*) FROM auth_tokens WHERE token_hash IN ('plex-owner-iphone','plex-alice-iphone','plex-stranger-iphone')`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("plaintext token in auth_tokens: count=%d err=%v", leaked, err)
	}
}
