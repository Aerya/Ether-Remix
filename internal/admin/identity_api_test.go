package admin_test

import (
	"bufio"
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLocalOwnerAndPortalIsolation(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	users, e := auth.New(ctx, db, auth.Options{})
	if e != nil {
		t.Fatal(e)
	}
	local, _ := identity.NewLocal(db)
	grants, _ := identity.NewGrants(db, users, time.Hour, nil)
	limiter, _ := identity.NewLoginLimiter(db, time.Minute, 100, 20, nil)
	invites, _ := identity.NewInvite(db, time.Hour, time.Hour, nil)
	cfg := newStore(t, nil)
	h := admin.NewHandler(admin.Deps{Config: cfg, LocalIdentity: local, IdentityGrants: grants, IdentityLimiter: limiter, Invites: invites, RefreshViewers: users.RefreshViewers, LocalOwnerConfigured: func(ctx context.Context) bool { x, err := users.Owner(ctx); return err == nil && x.PlexID == 0 }})
	setup := serve(h, req("POST", "/ether/api/setup/local", `{"username":"alice","password":"secret-pass1"}`))
	if setup.Code != 200 {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body)
	}
	var v struct {
		Token string `json:"token"`
	}
	decode(t, setup, &v)
	ownerGrant := v.Token
	// A local owner closes anonymous setup before the wizard is complete.
	// Bootstrap must send browsers to sign-in rather than into protected pages.
	for _, token := range []string{"", "invalid-owner-token", ownerGrant} {
		r := req("GET", "/ether/api/bootstrap", "")
		r.Header.Set("Authorization", "Bearer "+token)
		res := serve(h, r)
		var boot struct {
			AuthRequired  bool     `json:"authRequired"`
			Authenticated bool     `json:"authenticated"`
			SetupComplete bool     `json:"setupComplete"`
			SignInMethods []string `json:"signInMethods"`
		}
		decode(t, res, &boot)
		if res.Code != 200 || !boot.AuthRequired || boot.SetupComplete || boot.Authenticated != (token == ownerGrant) {
			t.Fatalf("local-owner bootstrap disagrees with access policy: status=%d boot=%+v", res.Code, boot)
		}
		if len(boot.SignInMethods) != 1 || boot.SignInMethods[0] != "local" {
			t.Fatalf("unclaimed local server offered unavailable sign-in: %v", boot.SignInMethods)
		}
	}
	owner, err := users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	events, err := grants.ViewerTimes().Get(ctx, owner.ID, owner.ID)
	if err != nil || events.LastLogin.IsZero() || events.LastActivity.IsZero() {
		t.Fatalf("owner setup did not record sign-in: %+v %v", events, err)
	}
	// Setup must close to unauthenticated LAN clients immediately after local creation.
	r0 := req("GET", "/ether/api/config", "")
	if res := serve(h, r0); res.Code != 401 || strings.Contains(res.Body.String(), "setup_local_host") {
		t.Fatalf("setup left open after owner: %d", res.Code)
	}
	r0 = req("GET", "/ether/api/setup/status", "")
	r0.Header.Set("Authorization", "Bearer "+ownerGrant)
	if res := serve(h, r0); res.Code != 200 {
		t.Fatalf("owner cannot continue unfinished setup: %d", res.Code)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Frontends = map[string]config.Frontend{"jellyfin": {Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r0 = req("POST", "/ether/api/setup/complete", `{"force":true}`)
	r0.Header.Set("Authorization", "Bearer "+ownerGrant)
	if res := serve(h, r0); res.Code != 200 {
		t.Fatalf("Plex-free setup complete: %d %s", res.Code, res.Body)
	}
	if e = cfg.Update(func(c *config.Config) error { c.SetupComplete = true; return nil }); e != nil {
		t.Fatal(e)
	}
	r := req("GET", "/ether/api/config", "")
	r.Header.Set("X-Plex-Token", v.Token)
	if res := serve(h, r); res.Code != 200 {
		t.Fatalf("owner rejected: %d", res.Code)
	}
	r = req("GET", "/ether/api/me", "")
	r.Header.Set("Authorization", "Bearer "+v.Token)
	if res := serve(h, r); res.Code != http.StatusUnauthorized {
		t.Fatalf("owner grant on portal: %d", res.Code)
	}
	login := serve(h, req("POST", "/ether/api/me/login", `{"username":"alice","password":"secret-pass1"}`))
	if login.Code != 200 {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	decode(t, login, &v)
	r = req("GET", "/ether/api/config", "")
	r.Header.Set("Authorization", "Bearer "+v.Token)
	if res := serve(h, r); res.Code != http.StatusUnauthorized {
		t.Fatalf("portal grant on owner: %d", res.Code)
	}
	r = req("GET", "/ether/api/me", "")
	r.Header.Set("Authorization", "Bearer "+v.Token)
	if res := serve(h, r); res.Code != 200 {
		t.Fatalf("portal rejected: %d", res.Code)
	}
	r = req("GET", "/ether/api/me/devices", "")
	r.Header.Set("Authorization", "Bearer "+v.Token)
	if res := serve(h, r); res.Code != 200 {
		t.Fatalf("devices: %d", res.Code)
	}
	r = req("GET", "/ether/api/me/identities", "")
	r.Header.Set("Authorization", "Bearer "+v.Token)
	if res := serve(h, r); res.Code != 200 {
		t.Fatalf("identities: %d", res.Code)
	}

	r = req("POST", "/ether/api/me/invite", `{}`)
	r.Header.Set("Authorization", "Bearer "+v.Token)
	if res := serve(h, r); res.Code == 200 {
		t.Fatal("owner invite accepted")
	}
	_, e = db.Exec(`INSERT INTO auth_users(id,plex_id,username,created_at,updated_at) VALUES(22,NULL,'viewer',0,0)`)
	if e != nil {
		t.Fatal(e)
	}
	if e = users.RefreshViewers(ctx); e != nil {
		t.Fatal(e)
	}
	r = req("POST", "/ether/api/users/22/invite", `{}`)
	r.Header.Set("Authorization", "Bearer "+ownerGrant)
	res := serve(h, r)
	if res.Code != 200 {
		t.Fatalf("invite creation: %d %s", res.Code, res.Body)
	}
	var invite struct {
		URL string `json:"url"`
	}
	decode(t, res, &invite)
	secret := strings.TrimPrefix(invite.URL, "/ether/me#invite=")
	if secret == invite.URL {
		t.Fatal("missing fragment")
	}
	r = req("POST", "/ether/api/me/invite/redeem", `{"secret":"`+secret+`"}`)
	res = serve(h, r)
	if res.Code != 200 {
		t.Fatalf("invite redeem: %d %s", res.Code, res.Body)
	}
	decode(t, res, &v)
	r = req("GET", "/ether/api/config", "")
	r.Header.Set("Authorization", "Bearer "+v.Token)
	if res = serve(h, r); res.Code != 401 {
		t.Fatalf("invite reached owner: %d", res.Code)
	}
	r = req("POST", "/ether/api/me/invite/redeem", `{"secret":"`+secret+`"}`)
	if res = serve(h, r); res.Code != 401 {
		t.Fatalf("invite reuse: %d", res.Code)
	}
	if _, err := users.Authenticate(ctx, v.Token); err != core.ErrUnauthorized {
		t.Fatalf("Plex accepted portal: %v", err)
	}
}

func TestBootstrapPlexSignInRequiresConfiguredClaim(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		cfg := newStore(t, func(c *config.Config) { c.Plex.Claimed = claimed })
		h := admin.NewHandler(admin.Deps{Config: cfg, SignIn: &fakeSignIn{}})
		var boot struct {
			SignInMethods []string `json:"signInMethods"`
		}
		decode(t, serve(h, req("GET", "/ether/api/bootstrap", "")), &boot)
		if claimed {
			if len(boot.SignInMethods) != 1 || boot.SignInMethods[0] != "plex" {
				t.Fatalf("claimed server lost Plex sign-in: %v", boot.SignInMethods)
			}
		} else if len(boot.SignInMethods) != 0 {
			t.Fatalf("unclaimed server advertises Plex sign-in: %v", boot.SignInMethods)
		}
	}
}

func TestPortalPlexLinkCannotAttachAnotherViewer(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	u, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(db)
	id, err := local.CreateOwner(ctx, "alice", "secret-pass1")
	if err != nil {
		t.Fatal(err)
	}
	if err = u.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	grants, _ := identity.NewGrants(db, u, time.Hour, nil)
	limiter, _ := identity.NewLoginLimiter(db, time.Minute, 100, 100, nil)
	portal, err := grants.Issue(ctx, id, nil, "viewer-portal", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	si := &fakeSignIn{tokens: map[string]string{}}
	cfg := newStore(t, func(c *config.Config) { c.SetupComplete = true })
	pollLimiter, _ := identity.NewPollLimiter(db, time.Minute, 120, 60, nil)
	h := admin.NewHandler(admin.Deps{Config: cfg, LocalIdentity: local, IdentityGrants: grants, IdentityLimiter: limiter, PollLimiter: pollLimiter, SignIn: si, LinkPlex: func(context.Context, int64, string) error { return core.ErrUnauthorized }})
	r := req("POST", "/ether/api/me/link/plex", `{}`)
	r.Header.Set("Authorization", "Bearer "+portal)
	res := serve(h, r)
	if res.Code != 200 {
		t.Fatalf("start %d %s", res.Code, res.Body)
	}
	var p struct {
		Handle string `json:"handle"`
	}
	decode(t, res, &p)
	si.approve("4242", "other-token")
	r = req("GET", "/ether/api/me/link/plex/"+p.Handle, "")
	r.Header.Set("Authorization", "Bearer "+portal)
	res = serve(h, r)
	if res.Code != 403 {
		t.Fatalf("linked another viewer: %d %s", res.Code, res.Body)
	}
	links, err := local.Links(ctx, id)
	if err != nil || len(links) != 1 {
		t.Fatalf("links %+v: %v", links, err)
	}
}

func TestOwnerPausesAndResumesLocalViewer(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(db)
	owner, err := local.CreateOwner(ctx, "owner", "secret-pass1")
	if err != nil {
		t.Fatal(err)
	}
	if err = users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	grants, _ := identity.NewGrants(db, users, time.Hour, nil)
	limiter, _ := identity.NewLoginLimiter(db, time.Minute, 100, 100, nil)
	ownerToken, err := grants.Issue(ctx, owner, nil, "owner", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := newStore(t, func(c *config.Config) { c.SetupComplete = true })
	h := admin.NewHandler(admin.Deps{Config: cfg, LocalIdentity: local, IdentityGrants: grants, IdentityLimiter: limiter, RefreshViewers: users.RefreshViewers, Lifecycle: users, ViewerDirectory: lifecycleDirectory(users)})
	r := req("POST", "/ether/api/users/local", `{"username":"bob","password":"secret-pass1"}`)
	r.Header.Set("Authorization", "Bearer "+ownerToken)
	res := serve(h, r)
	if res.Code != 201 {
		t.Fatalf("create %d %s", res.Code, res.Body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	decode(t, res, &created)
	token, err := grants.Issue(ctx, created.ID, nil, "jellyfin", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	r = req("POST", fmt.Sprintf("/ether/api/users/%d/pause", created.ID), `{}`)
	r.Header.Set("Authorization", "Bearer "+ownerToken)
	if res = serve(h, r); res.Code != 200 {
		t.Fatalf("pause: %d %s", res.Code, res.Body)
	}
	if _, err = grants.Resolve(ctx, "jellyfin", token); err == nil {
		t.Fatal("paused viewer's grant accepted")
	}
	r = req("POST", fmt.Sprintf("/ether/api/users/%d/resume", created.ID), `{}`)
	r.Header.Set("Authorization", "Bearer "+ownerToken)
	if res = serve(h, r); res.Code != 200 {
		t.Fatalf("resume: %d %s", res.Code, res.Body)
	}
	// Pausing signs the viewer out everywhere, so the old grant stays
	// revoked after a resume; a fresh sign-in works again.
	if _, err = grants.Resolve(ctx, "jellyfin", token); err == nil {
		t.Fatal("grant revoked by pause accepted after resume")
	}
	fresh, err := grants.Issue(ctx, created.ID, nil, "jellyfin", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = grants.Resolve(ctx, "jellyfin", fresh); err != nil {
		t.Fatal(err)
	}
	r = req("POST", fmt.Sprintf("/ether/api/users/%d/pause", owner), `{}`)
	r.Header.Set("Authorization", "Bearer "+ownerToken)
	if res = serve(h, r); res.Code != 403 {
		t.Fatalf("owner paused: %d", res.Code)
	}
}

func TestPlexFreeSetupRequiresOwnerEvenForced(t *testing.T) {
	cfg := newStore(t, func(c *config.Config) { c.Frontends = map[string]config.Frontend{"jellyfin": {Enabled: true}} })
	h := admin.NewHandler(admin.Deps{Config: cfg})
	rec := serve(h, req("POST", "/ether/api/setup/complete", `{"force":true}`))
	if rec.Code != 409 {
		t.Fatalf("forced setup without owner: %d %s", rec.Code, rec.Body)
	}
	if cfg.Get().SetupComplete {
		t.Fatal("setup completed without owner")
	}
}
func TestGrantNeverFallsBackToPlexOwner(t *testing.T) {
	cfg := newStore(t, func(c *config.Config) { c.SetupComplete = true })
	h := admin.NewHandler(admin.Deps{Config: cfg, IsOwnerToken: func(context.Context, string) bool { return true }})
	r := req("GET", "/ether/api/config", "")
	r.Header.Set("Authorization", "Bearer eg1_invalid")
	if res := serve(h, r); res.Code != 401 {
		t.Fatalf("grant accepted by permissive Plex validator: %d", res.Code)
	}
}
func TestLocalOwnerConsoleSessionSignOut(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(db)
	grants, _ := identity.NewGrants(db, users, time.Hour, nil)
	limiter, _ := identity.NewLoginLimiter(db, time.Minute, 100, 20, nil)
	cfg := newStore(t, nil)
	h := admin.NewHandler(admin.Deps{Config: cfg, LocalIdentity: local, IdentityGrants: grants, IdentityLimiter: limiter, RefreshViewers: users.RefreshViewers, Sessions: users})
	res := serve(h, req("POST", "/ether/api/setup/local", `{"username":"alice","password":"secret-pass1"}`))
	if res.Code != 200 {
		t.Fatalf("setup: %d %s", res.Code, res.Body)
	}
	var v struct {
		Token string `json:"token"`
	}
	decode(t, res, &v)
	ownerToken := v.Token
	if err := cfg.Update(func(c *config.Config) error { c.SetupComplete = true; return nil }); err != nil {
		t.Fatal(err)
	}
	r := req("GET", "/ether/api/config", "")
	r.Header.Set("X-Ether-Admin-Token", ownerToken)
	if res := serve(h, r); res.Code != 200 {
		t.Fatalf("owner header: %d", res.Code)
	}
	res = serve(h, req("POST", "/ether/api/me/login", `{"username":"alice","password":"secret-pass1"}`))
	if res.Code != 200 {
		t.Fatalf("viewer login: %d", res.Code)
	}
	decode(t, res, &v)
	for range 2 {
		r = req("DELETE", "/ether/api/auth/session", "")
		r.Header.Set("X-Ether-Admin-Token", ownerToken)
		if res := serve(h, r); res.Code != 200 {
			t.Fatalf("sign out: %d %s", res.Code, res.Body)
		}
	}
	r = req("GET", "/ether/api/config", "")
	r.Header.Set("X-Ether-Admin-Token", ownerToken)
	if res := serve(h, r); res.Code != 401 {
		t.Fatalf("signed-out owner still accepted: %d", res.Code)
	}
	r = req("GET", "/ether/api/me", "")
	r.Header.Set("Authorization", "Bearer "+v.Token)
	if res := serve(h, r); res.Code != 200 {
		t.Fatalf("sign-out revoked independent viewer: %d", res.Code)
	}
	// A database failure must not falsely report successful revocation.
	res = serve(h, req("POST", "/ether/api/auth/local", `{"username":"alice","password":"secret-pass1"}`))
	if res.Code != 200 {
		t.Fatalf("owner login: %d", res.Code)
	}
	decode(t, res, &v)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	r = req("DELETE", "/ether/api/auth/session", "")
	r.Header.Set("X-Ether-Admin-Token", v.Token)
	if res := serve(h, r); res.Code != 503 {
		t.Fatalf("revocation unavailable: %d", res.Code)
	}

}

func TestPortalApproveDistinguishesUsedCodes(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, err := identity.NewLocal(db)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, users, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := identity.NewLoginLimiter(db, time.Minute, 100, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	quick, err := identity.NewQuickConnect(db, time.Minute, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	h := admin.NewHandler(admin.Deps{
		Config: newStore(t, nil), LocalIdentity: local, IdentityGrants: grants, IdentityLimiter: limiter, QuickConnect: quick,
		RefreshViewers: users.RefreshViewers,
		LocalOwnerConfigured: func(ctx context.Context) bool {
			owner, err := users.Owner(ctx)
			return err == nil && owner.PlexID == 0
		},
	})
	if setup := serve(h, req("POST", "/ether/api/setup/local", `{"username":"alice","password":"secret-pass1"}`)); setup.Code != 200 {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body)
	}
	login := serve(h, req("POST", "/ether/api/me/login", `{"username":"alice","password":"secret-pass1"}`))
	if login.Code != 200 {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	var session struct {
		Token string `json:"token"`
	}
	decode(t, login, &session)
	approve := func(code string) *httptest.ResponseRecorder {
		r := req("POST", "/ether/api/me/quickconnect", `{"code":"`+code+`"}`)
		r.Header.Set("Authorization", "Bearer "+session.Token)
		return serve(h, r)
	}
	code, secret, err := quick.Initiate(ctx, "jellyfin", core.Device{ID: "tv", DeviceName: "Living Room"})
	if err != nil {
		t.Fatal(err)
	}
	if res := approve(code); res.Code != 200 {
		t.Fatalf("approve: %d %s", res.Code, res.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	used := approve(code)
	decode(t, used, &body)
	if used.Code != 400 || body.Error.Code != "used" {
		t.Fatalf("approved again: %d %s", used.Code, used.Body)
	}
	if _, err = quick.Poll(ctx, secret); err != nil {
		t.Fatal(err)
	}
	used = approve(code)
	decode(t, used, &body)
	if used.Code != 400 || body.Error.Code != "used" {
		t.Fatalf("consumed: %d %s", used.Code, used.Body)
	}
	expiredCode, _, err := quick.Initiate(ctx, "jellyfin", core.Device{ID: "tv2"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	expired := approve(expiredCode)
	decode(t, expired, &body)
	if expired.Code != 400 || body.Error.Code != "expired" {
		t.Fatalf("expired: %d %s", expired.Code, expired.Body)
	}
	unknown := approve("NOPE")
	decode(t, unknown, &body)
	if unknown.Code != 400 || body.Error.Code != "invalid" {
		t.Fatalf("unknown: %d %s", unknown.Code, unknown.Body)
	}
}

// The owner's recovery sign-in is refreshed after setup, every owner local
// sign-in and every local password change; a failed sign-in does nothing.
func TestOwnerCredentialChangesAreReported(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(db)
	grants, _ := identity.NewGrants(db, users, time.Hour, nil)
	limiter, _ := identity.NewLoginLimiter(db, time.Minute, 100, 20, nil)
	calls := 0
	cfg := newStore(t, nil)
	h := admin.NewHandler(admin.Deps{Config: cfg, LocalIdentity: local, IdentityGrants: grants, IdentityLimiter: limiter, RefreshViewers: users.RefreshViewers, Sessions: users,
		OwnerCredentialChanged: func(context.Context) { calls++ }})
	if res := serve(h, req("POST", "/ether/api/setup/local", `{"username":"alice","password":"secret-pass1"}`)); res.Code != 200 {
		t.Fatalf("setup: %d %s", res.Code, res.Body)
	}
	if calls != 1 {
		t.Fatalf("after setup: %d calls", calls)
	}
	if res := serve(h, req("POST", "/ether/api/auth/local", `{"username":"alice","password":"wrong"}`)); res.Code != 401 {
		t.Fatalf("wrong password: %d", res.Code)
	}
	if res := serve(h, req("POST", "/ether/api/auth/local", `{"username":"alice","password":"secret-pass1"}`)); res.Code != 200 {
		t.Fatalf("owner login: %d", res.Code)
	}
	if calls != 2 {
		t.Fatalf("after a failed and a good sign-in: %d calls", calls)
	}
}

func TestLocalPasswordMinimumLength(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(db)
	grants, _ := identity.NewGrants(db, users, time.Hour, nil)
	limiter, _ := identity.NewLoginLimiter(db, time.Minute, 100, 20, nil)
	cfg := newStore(t, nil)
	h := admin.NewHandler(admin.Deps{
		Config: cfg, LocalIdentity: local, IdentityGrants: grants, IdentityLimiter: limiter,
		RefreshViewers: users.RefreshViewers,
		LocalOwnerConfigured: func(ctx context.Context) bool {
			x, err := users.Owner(ctx)
			return err == nil && x.PlexID == 0
		},
	})
	shortSetup := serve(h, req("POST", "/ether/api/setup/local", `{"username":"alice","password":"short"}`))
	if shortSetup.Code != 400 || !strings.Contains(shortSetup.Body.String(), "at least 12 characters") {
		t.Fatalf("short setup: %d %s", shortSetup.Code, shortSetup.Body)
	}
	okSetup := serve(h, req("POST", "/ether/api/setup/local", `{"username":"alice","password":"secret-pass1"}`))
	if okSetup.Code != 200 {
		t.Fatalf("ok setup: %d %s", okSetup.Code, okSetup.Body)
	}
	var v struct {
		Token string `json:"token"`
	}
	decode(t, okSetup, &v)
	ownerToken := v.Token
	if err := cfg.Update(func(c *config.Config) error { c.SetupComplete = true; return nil }); err != nil {
		t.Fatal(err)
	}
	r := req("POST", "/ether/api/users/local", `{"username":"bob","password":"short"}`)
	r.Header.Set("Authorization", "Bearer "+ownerToken)
	if res := serve(h, r); res.Code != 400 || !strings.Contains(res.Body.String(), "at least 12 characters") {
		t.Fatalf("short create viewer: %d %s", res.Code, res.Body)
	}
	r = req("POST", "/ether/api/users/1/password", `{"username":"alice","password":"short"}`)
	r.Header.Set("Authorization", "Bearer "+ownerToken)
	if res := serve(h, r); res.Code != 400 || !strings.Contains(res.Body.String(), "at least 12 characters") {
		t.Fatalf("short owner set password: %d %s", res.Code, res.Body)
	}
	login := serve(h, req("POST", "/ether/api/me/login", `{"username":"alice","password":"secret-pass1"}`))
	if login.Code != 200 {
		t.Fatalf("me login: %d %s", login.Code, login.Body)
	}
	decode(t, login, &v)
	r = req("POST", "/ether/api/me/password", `{"username":"alice","password":"short"}`)
	r.Header.Set("Authorization", "Bearer "+v.Token)
	if res := serve(h, r); res.Code != 400 || !strings.Contains(res.Body.String(), "at least 12 characters") {
		t.Fatalf("short me password: %d %s", res.Code, res.Body)
	}
}

// The real HTTP server must stop reading an unfinished login body. The client
// deadline is only a deadlock guard: a passing response comes from the server.
func TestLoginStalledBodyDeadline(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(db)
	grants, _ := identity.NewGrants(db, users, time.Hour, nil)
	limiter, _ := identity.NewLoginLimiter(db, time.Minute, 100, 20, nil)
	h := admin.NewHandler(admin.Deps{Config: newStore(t, nil), LocalIdentity: local, IdentityGrants: grants, IdentityLimiter: limiter})
	server := httptest.NewServer(h)
	defer server.Close()
	for _, prefix := range []string{`{"username":`, `{"username":"alice","password":"secret-pass1"}}`} {
		t.Run(prefix, func(t *testing.T) {
			conn, err := net.Dial("tcp", server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(12 * time.Second)); err != nil {
				t.Fatal(err)
			}
			_, err = fmt.Fprintf(conn, "POST /ether/api/auth/local HTTP/1.1\r\nHost: localhost\r\nX-Ether-Request: 1\r\nContent-Type: application/json\r\nContent-Length: 80\r\n\r\n%s", prefix)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("stalled request did not receive server response: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d", response.StatusCode)
			}
		})
	}
}

func TestLockoutsAdminAPIAndLocalLoginReset(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(db)
	grants, _ := identity.NewGrants(db, users, time.Hour, nil)
	now := time.Unix(6000, 0)
	limiter, _ := identity.NewLoginLimiter(db, 15*time.Minute, 5, 2, func() time.Time { return now })
	pollLimiter, _ := identity.NewPollLimiter(db, time.Minute, 10, 2, func() time.Time { return now })
	cfg := newStore(t, nil)
	h := admin.NewHandler(admin.Deps{
		Config:          cfg,
		LocalIdentity:   local,
		IdentityGrants:  grants,
		IdentityLimiter: limiter,
		PollLimiter:     pollLimiter,
		RefreshViewers:  users.RefreshViewers,
		LocalOwnerConfigured: func(ctx context.Context) bool {
			x, err := users.Owner(ctx)
			return err == nil && x.PlexID == 0
		},
	})

	// Before owner setup, tokenless LAN access must still be denied on owner-only lockouts routes.
	if res := serve(h, req("GET", "/ether/api/auth/lockouts", "")); res.Code != http.StatusForbidden {
		t.Fatalf("tokenless setup GET lockouts: got %d, want 403", res.Code)
	}

	setup := serve(h, req("POST", "/ether/api/setup/local", `{"username":"owner","password":"owner-secret-12"}`))
	if setup.Code != 200 {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body)
	}
	var tok struct {
		Token string `json:"token"`
	}
	decode(t, setup, &tok)
	ownerToken := tok.Token

	// Local portal login: fail once, succeed once -> identity bucket resets (can fail twice more before locking out).
	loginReq := func(user, pass string) *httptest.ResponseRecorder {
		r := req("POST", "/ether/api/me/login", fmt.Sprintf(`{"username":%q,"password":%q}`, user, pass))
		r.RemoteAddr = "198.51.100.7:1234"
		return serve(h, r)
	}
	if res := loginReq("owner", "wrong-secret-12"); res.Code != http.StatusUnauthorized {
		t.Fatalf("first fail: %d", res.Code)
	}
	okRes := loginReq("owner", "owner-secret-12")
	if okRes.Code != http.StatusOK {
		t.Fatalf("valid portal login: %d %s", okRes.Code, okRes.Body)
	}
	decode(t, okRes, &tok)
	portalToken := tok.Token

	// Non-owner portal token must be rejected on lockouts API.
	r := req("GET", "/ether/api/auth/lockouts", "")
	r.Header.Set("Authorization", "Bearer "+portalToken)
	if res := serve(h, r); res.Code != http.StatusUnauthorized {
		t.Fatalf("portal token on lockouts: got %d, want 401", res.Code)
	}

	// Because the valid login reset owner+IP, two more failures are allowed (IP=4/5, owner=2/2) before 429.
	for i := 0; i < 2; i++ {
		if res := loginReq("owner", "wrong-secret-12"); res.Code != http.StatusUnauthorized {
			t.Fatalf("post-reset fail %d: got %d, want 401", i, res.Code)
		}
	}
	if res := loginReq("owner", "wrong-secret-12"); res.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 3rd failure after reset, got %d", res.Code)
	}
	// Also lock a poll bucket with an opaque secret to verify both namespaces appear and secret stays hidden.
	pollIP, _ := identity.ClientNet(req("GET", "/", ""))
	for i := 0; i < 3; i++ {
		_, _ = pollLimiter.Allow(identity.WithRateLimitApp(ctx, "Jellyfin"), "opaque-poll-secret", pollIP)
	}

	ownerReq := func(method, path string) *httptest.ResponseRecorder {
		r := req(method, path, "")
		r.Header.Set("Authorization", "Bearer "+ownerToken)
		return serve(h, r)
	}
	listRes := ownerReq("GET", "/ether/api/auth/lockouts")
	if listRes.Code != http.StatusOK {
		t.Fatalf("list lockouts: %d %s", listRes.Code, listRes.Body)
	}
	var listed struct {
		Lockouts []identity.RateLockout `json:"lockouts"`
	}
	decode(t, listRes, &listed)
	if len(listed.Lockouts) < 2 {
		t.Fatalf("expected at least 2 lockouts (login + poll), got %+v", listed.Lockouts)
	}
	var ownerKey string
	for _, lo := range listed.Lockouts {
		if lo.Namespace == "poll" && lo.Identity != "" {
			t.Fatalf("poll lockout exposed secret: %+v", lo)
		}
		if lo.Namespace == "login" && lo.Identity == "owner" {
			ownerKey = lo.Key
		}
	}
	if ownerKey == "" {
		t.Fatalf("missing owner login lockout in %+v", listed.Lockouts)
	}

	// Clear just the owner identity lockout, then clear all remaining lockouts.
	if res := ownerReq("DELETE", "/ether/api/auth/lockouts/"+ownerKey); res.Code != http.StatusOK {
		t.Fatalf("clear one: %d %s", res.Code, res.Body)
	}
	if res := ownerReq("DELETE", "/ether/api/auth/lockouts"); res.Code != http.StatusOK {
		t.Fatalf("clear all: %d %s", res.Code, res.Body)
	}
	after := ownerReq("GET", "/ether/api/auth/lockouts")
	decode(t, after, &listed)
	if len(listed.Lockouts) != 0 {
		t.Fatalf("expected empty lockouts after clear all, got %+v", listed.Lockouts)
	}
	// Owner can sign in again immediately from the same IP.
	if res := loginReq("owner", "owner-secret-12"); res.Code != http.StatusOK {
		t.Fatalf("login after clear all: %d %s", res.Code, res.Body)
	}
}
