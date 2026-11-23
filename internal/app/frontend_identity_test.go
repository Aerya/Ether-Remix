package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
)

func TestFrontendClientIPAcceptsPublicPeersAndRejectsSpoofedHeaders(t *testing.T) {
	a := setupTestApp(t, true)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.4:1234"
	req.Header.Set("X-Forwarded-For", "192.168.1.5")
	ip, ok := a.frontendClientIP(req)
	if !ok || ip.String() != "198.51.100.4" {
		t.Fatal(ip, ok)
	}
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	ip, ok = a.frontendClientIP(req)
	if !ok || ip.String() != "198.51.100.9" {
		t.Fatal(ip, ok)
	}
	req.Header.Set("X-Forwarded-For", "invalid")
	if ip, ok = a.frontendClientIP(req); ok || ip.IsValid() {
		t.Fatal(ip, ok)
	}
}

func TestSurfaceIdentityLoginQuickConnectAndRevoke(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "owner-token"); err != nil {
		t.Fatal(err)
	}
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	local, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Create(ctx, owner.ID, "local-owner", "correct-password"); err != nil {
		t.Fatal(err)
	}
	grants, _ := identity.NewGrants(a.db, a.users, time.Hour, nil)
	resolver, _ := identity.NewResolver(map[string]identity.Authenticator{"plex": a.users}, grants)
	login, _ := identity.NewLoginLimiter(a.db, time.Minute, 100, 10, nil)
	poll, _ := identity.NewPollLimiter(a.db, time.Minute, 100, 20, nil)
	quick, _ := identity.NewQuickConnect(a.db, time.Minute, time.Hour, nil)
	jf := surfaceIdentity{surface: "jellyfin", resolver: resolver, local: local, grants: grants, quick: quick, login: login, poll: poll}
	emby := jf
	emby.surface = "emby"
	ctx = frontend.WithClientIP(ctx, netip.MustParseAddr("192.0.2.8"))
	if !jf.SetupComplete() {
		t.Fatal("protocol setup incomplete")
	}
	if _, err := jf.SignIn(context.Background(), "local-owner", "correct-password", core.Device{}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("missing peer: %v", err)
	}
	if _, err := jf.SignIn(ctx, "local-owner", "wrong", core.Device{}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("bad password: %v", err)
	}
	token, err := jf.SignIn(ctx, "local-owner", "correct-password", core.Device{ID: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := jf.Resolve(ctx, token)
	if err != nil || viewer.ID != owner.ID {
		t.Fatal(viewer, err)
	}
	if _, err := emby.Resolve(ctx, token); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("cross-surface grant", err)
	}
	if err := emby.Revoke(ctx, token); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("cross-surface revoke", err)
	}
	code, secret, err := jf.QuickConnectInitiate(ctx, core.Device{ID: "tablet", DeviceName: "Family tablet", Client: "Jellyfin", Product: "Jellyfin TV", Version: "1.2"})
	if err != nil {
		t.Fatal(err)
	}
	details, err := jf.QuickConnectDetails(ctx, secret)
	if err != nil || details.Approved || details.Code != code || details.Secret != secret || details.Device.DeviceName != "Family tablet" || details.Device.Client != "Jellyfin" || details.CreatedAt.IsZero() || !details.ExpiresAt.Equal(details.CreatedAt.Add(time.Minute)) {
		t.Fatal(details, err)
	}
	if _, err := emby.QuickConnectDetails(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("cross-surface details", err)
	}
	if approved, err := jf.QuickConnectStatus(ctx, secret); err != nil || approved {
		t.Fatal("pending status", approved, err)
	}
	if _, err := emby.QuickConnectStatus(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("cross-surface status", err)
	}
	if _, err := jf.QuickConnectPoll(ctx, secret); !errors.Is(err, frontend.ErrQuickConnectPending) {
		t.Fatal(err)
	}
	if err := emby.QuickConnectApprove(ctx, viewer, code); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("cross-surface approval", err)
	}
	if err := jf.QuickConnectApprove(ctx, viewer, code); err != nil {
		t.Fatal(err)
	}
	// Re-approving an already approved code maps to frontend.ErrQuickConnectUsed.
	if err := jf.QuickConnectApprove(ctx, viewer, code); !errors.Is(err, frontend.ErrQuickConnectUsed) {
		t.Fatalf("expected ErrQuickConnectUsed on duplicate approve, got %v", err)
	}
	// Delegated viewers cannot approve quick-connect codes.
	delegated := *viewer
	delegated.Delegated = true
	if err := jf.QuickConnectApprove(ctx, &delegated, code); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for delegated viewer, got %v", err)
	}
	if details, err := jf.QuickConnectDetails(ctx, secret); err != nil || !details.Approved || details.Code != code {
		t.Fatal("approved details", details, err)
	}
	for i := 0; i < 2; i++ {
		if approved, err := jf.QuickConnectStatus(ctx, secret); err != nil || !approved {
			t.Fatal("approved status", approved, err)
		}
	}
	if _, err := emby.QuickConnectPoll(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("cross-surface poll", err)
	}
	quickGrant, err := jf.QuickConnectPoll(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jf.Resolve(ctx, quickGrant); err != nil {
		t.Fatal(err)
	}
	if _, err := jf.QuickConnectPoll(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("reused secret", err)
	}
	if _, err := jf.QuickConnectStatus(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("consumed status", err)
	}
	if err := jf.Revoke(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := jf.Resolve(ctx, token); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("revoked grant resolved", err)
	}
	plex := jf
	plex.surface = "plex"
	plex.policy = frontendHostPolicy(plex.surface)
	if _, err := plex.SignIn(ctx, "local-owner", "correct-password", core.Device{}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("Plex accepted local grant", err)
	}
}

func TestSurfaceIdentityRateLimits(t *testing.T) {
	a := setupTestApp(t, true)
	local, _ := identity.NewLocal(a.db)
	login, _ := identity.NewLoginLimiter(a.db, time.Minute, 20, 1, nil)
	poll, _ := identity.NewPollLimiter(a.db, time.Minute, 20, 1, nil)
	quick, _ := identity.NewQuickConnect(a.db, time.Minute, time.Hour, nil)
	s := surfaceIdentity{surface: "jellyfin", local: local, login: login, poll: poll, quick: quick}
	ctx := frontend.WithClientIP(context.Background(), netip.MustParseAddr("192.0.2.11"))
	if _, err := s.SignIn(ctx, "absent", "bad", core.Device{}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal(err)
	}
	if _, err := s.SignIn(ctx, "absent", "bad", core.Device{}); err != nil {
		var limited *frontend.IdentityRateLimitError
		if !errors.Is(err, frontend.ErrRateLimited) || !errors.As(err, &limited) || limited.RetryAfter <= 0 || limited.RetryAfter > time.Minute {
			t.Fatal("identity retry delay missing", err)
		}
	} else {
		t.Fatal("identity limit bypassed")
	}
	_, secret, err := s.QuickConnectInitiate(ctx, core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.QuickConnectPoll(ctx, secret); !errors.Is(err, frontend.ErrQuickConnectPending) {
		t.Fatal(err)
	}
	if _, err := s.QuickConnectPoll(ctx, secret); !errors.Is(err, frontend.ErrRateLimited) {
		t.Fatal(err)
	}
}

func TestPublicViewerPickerRequiresOptInAndOnlyActiveLocalCredentials(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	if _, err := a.db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,created_at,updated_at,active) VALUES(11,111,0,0,1),(12,112,0,0,0),(13,113,0,0,1),(14,114,0,0,1)`); err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(a.db)
	for _, item := range []struct {
		id   int64
		name string
	}{{11, "plex-linked"}, {12, "inactive"}, {14, "local-viewer"}} {
		if err := local.Create(ctx, item.id, item.name, "password-1234"); err != nil {
			t.Fatal(err)
		}
	}
	limiter, _ := identity.NewLoginLimiter(a.db, time.Minute, 30, 8, nil)
	enabled := false
	s := surfaceIdentity{surface: "jellyfin", local: local, login: limiter, settings: func() frontend.Settings { return frontend.Settings{"publicViewerPicker": enabled} }}
	if list, err := s.PublicViewers(ctx); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	enabled = true
	if _, err := s.PublicViewers(ctx); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("missing IP", err)
	}
	ctx = frontend.WithClientIP(ctx, netip.MustParseAddr("192.0.2.42"))
	viewers, err := s.PublicViewers(ctx)
	if err != nil || len(viewers) != 2 {
		t.Fatal(viewers, err)
	}
	if viewers[0].Name != "local-viewer" || viewers[1].Name != "plex-linked" {
		t.Fatal(viewers)
	}
	for _, viewer := range viewers {
		if viewer.HasConfiguredPassword == nil || !*viewer.HasConfiguredPassword {
			t.Fatalf("missing credential capability: %+v", viewer)
		}
	}
}

func TestViewerTimesRequiresSurfaceCredentialAndActiveOwner(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "owner-token"); err != nil {
		t.Fatal(err)
	}
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	local, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	viewerID, err := local.CreateViewer(ctx, "member-times", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(a.db, a.users, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := identity.NewResolver(map[string]identity.Authenticator{"plex": a.users}, grants)
	if err != nil {
		t.Fatal(err)
	}
	times, err := identity.NewViewerTimes(a.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := surfaceIdentity{surface: "jellyfin", resolver: resolver, grants: grants, times: times}
	ownerGrant, err := grants.Issue(ctx, owner.ID, nil, "jellyfin", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	memberGrant, err := grants.Issue(ctx, viewerID, nil, "jellyfin", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ViewerTimes(ctx, memberGrant, owner.ID); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("member read owner: %v", err)
	}
	result, err := s.ViewerTimes(ctx, memberGrant, viewerID)
	if err != nil || !result.LastLogin.IsZero() || result.LastActivity.IsZero() {
		t.Fatalf("self: %+v %v", result, err)
	}
	if _, err = s.ViewerTimes(ctx, ownerGrant, viewerID); err != nil {
		t.Fatalf("owner: %v", err)
	}
	wrong := s
	wrong.surface = "emby"
	if _, err = wrong.ViewerTimes(ctx, ownerGrant, viewerID); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("cross surface: %v", err)
	}
	if _, err = a.db.ExecContext(ctx, `UPDATE auth_users SET is_owner=0 WHERE id=?`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ViewerTimes(ctx, ownerGrant, viewerID); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("stale owner: %v", err)
	}
	if err = s.Revoke(ctx, memberGrant); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ViewerTimes(ctx, memberGrant, viewerID); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("revoked credential: %v", err)
	}
}

func TestSurfaceRefreshObservesActivityWithoutNewLogin(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	local, _ := identity.NewLocal(a.db)
	viewerID, err := local.CreateViewer(ctx, "refresh-times", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	clock := func() time.Time { return now }
	grants, err := identity.NewGrants(a.db, a.users, time.Hour, clock)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := identity.NewResolver(map[string]identity.Authenticator{"plex": a.users}, grants)
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := identity.NewPollLimiter(a.db, time.Minute, 100, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := surfaceIdentity{surface: "silo", resolver: resolver, grants: grants, times: grants.ViewerTimes(), poll: limiter}
	device := core.Device{ID: "tv"}
	access, err := grants.IssueLogin(ctx, viewerID, nil, "silo", device)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := s.IssueRefresh(ctx, access, device)
	if err != nil {
		t.Fatal(err)
	}
	login := now
	now = now.Add(6 * time.Minute)
	ctx = frontend.WithClientIP(ctx, netip.MustParseAddr("192.0.2.3"))
	access, _, err = s.Refresh(ctx, refresh, device)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ViewerTimes(ctx, access, viewerID)
	if err != nil || !got.LastLogin.Equal(login) || !got.LastActivity.Equal(now) {
		t.Fatalf("timestamps=%+v err=%v", got, err)
	}
}

func TestDeviceIdentityBindsSurfaceAndRejectsPlex(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	local, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	viewerID, err := local.CreateViewer(ctx, "device-check", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10000, 0)
	grants, err := identity.NewGrants(a.db, a.users, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	device := core.Device{ID: "apple-tv"}
	token, err := grants.Issue(ctx, viewerID, nil, "silo", device)
	if err != nil {
		t.Fatal(err)
	}
	surface := surfaceIdentity{surface: "silo", grants: grants}
	viewer, expires, err := surface.ResolveDevice(ctx, token, device)
	if err != nil || viewer == nil || viewer.ID != viewerID || !expires.Equal(now.Add(time.Hour)) {
		t.Fatalf("viewer=%+v expires=%s err=%v", viewer, expires, err)
	}
	bound, actual, boundExpiry, err := surface.ResolveGrantDevice(ctx, token, "")
	if err != nil || bound == nil || bound.ID != viewerID || actual.ID != device.ID || !boundExpiry.Equal(expires) {
		t.Fatalf("stored binding=%+v device=%+v expiry=%s err=%v", bound, actual, boundExpiry, err)
	}
	if _, _, _, err = surface.ResolveGrantDevice(ctx, token, "wrong"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("wrong device claim: %v", err)
	}
	surface.surface = "emby"
	if _, _, err = surface.ResolveDevice(ctx, token, device); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("cross surface: %v", err)
	}
	if _, _, _, err = surface.ResolveGrantDevice(ctx, token, ""); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("cross-surface stored binding: %v", err)
	}
	surface.surface = "plex"
	surface.policy = frontendHostPolicy(surface.surface)
	if _, _, err = surface.ResolveDevice(ctx, token, device); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("Plex grant: %v", err)
	}
	if _, _, _, err = surface.ResolveGrantDevice(ctx, token, ""); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("Plex stored binding: %v", err)
	}
}

func TestSurfaceIdentityAppPasswordSignInOnJellyfinEmbySilo(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := frontend.WithClientIP(context.Background(), netip.MustParseAddr("192.0.2.55"))
	if _, err := a.db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,username,title,created_at,updated_at,active) VALUES(88,888,'plex-only','Plex Only',0,0,1)`); err != nil {
		t.Fatal(err)
	}
	if err := a.users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	local, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(a.db, a.users, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := identity.NewResolver(map[string]identity.Authenticator{"plex": a.users}, grants)
	if err != nil {
		t.Fatal(err)
	}
	login, err := identity.NewLoginLimiter(a.db, time.Minute, 100, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	poll, err := identity.NewPollLimiter(a.db, time.Minute, 100, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, proto := range []frontend.Protocol{"jellyfin", "emby", "silo"} {
		surface := string(proto)
		s := surfaceIdentity{surface: proto, resolver: resolver, local: local, grants: grants, login: login, poll: poll}
		appPassword, err := grants.Issue(ctx, 88, nil, surface, core.Device{ID: surface + "-minted", Client: surface, Product: surface})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SignIn(ctx, "wrong-user", appPassword, core.Device{ID: "infuse-1"}); !errors.Is(err, core.ErrUnauthorized) {
			t.Fatalf("%s accepted wrong username: %v", surface, err)
		}
		dev := core.Device{ID: "infuse-" + surface, Client: "Infuse", Product: "Apple TV", Version: "8.1"}
		tok, err := s.SignIn(ctx, "plex-only", appPassword, dev)
		if err != nil {
			t.Fatalf("%s sign-in failed: %v", surface, err)
		}
		viewer, bound, _, err := s.ResolveGrantDevice(ctx, tok, dev.ID)
		if err != nil || viewer.ID != 88 || bound.ID != dev.ID || bound.Client != "Infuse" {
			t.Fatalf("%s resolve device: viewer=%+v bound=%+v err=%v", surface, viewer, bound, err)
		}
		if proto == "silo" {
			if _, err := s.IssueRefresh(ctx, tok, dev); err != nil {
				t.Fatalf("silo refresh issue: %v", err)
			}
		}
	}
}

func TestXtreamAppPasswordRateLimitAndRevocation(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := frontend.WithClientIP(context.Background(), netip.MustParseAddr("192.0.2.10"))
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "owner-token"); err != nil {
		t.Fatal(err)
	}
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	grants, _ := identity.NewGrants(a.db, a.users, time.Hour, nil, sealer)
	resolver, _ := identity.NewResolver(map[string]identity.Authenticator{"plex": a.users}, grants)
	limiter, _ := identity.NewLoginLimiter(a.db, time.Minute, 3, 3, nil)
	surface := surfaceIdentity{surface: "xtream", resolver: resolver, grants: grants, login: limiter}
	password, err := grants.Issue(ctx, owner.ID, nil, "xtream", core.Device{ID: "xtream-test"})
	if err != nil {
		t.Fatal(err)
	}
	username := frontend.XtreamUsername(password)
	for n := 0; n < 20; n++ {
		viewer, err := surface.AuthenticateAppPassword(ctx, username, password)
		if err != nil || viewer.ID != owner.ID {
			t.Fatal("normal startup locked out", err)
		}
	}
	shortUser, shortPass, credential, err := grants.IssueXtream(ctx, owner.ID, core.Device{ID: "xtream-short"})
	if err != nil {
		t.Fatal(err)
	}
	if len(shortUser) > 12 || len(shortPass) != 12 {
		t.Fatal("invalid short pair shape")
	}
	var sealed string
	if err := a.db.QueryRowContext(ctx, `SELECT xtream_password FROM identity_grants WHERE xtream_username=?`, shortUser).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if !config.IsSealed(sealed) || strings.Contains(sealed, shortPass) {
		t.Fatal("password not encrypted")
	}
	hash := sha256.Sum256([]byte(credential))
	revealedUser, revealedPass, available, err := grants.RevealXtream(ctx, owner.ID, hex.EncodeToString(hash[:]))
	if err != nil || !available || revealedUser != shortUser || revealedPass != shortPass {
		t.Fatal("encrypted reveal failed", err)
	}
	oldHash := sha256.Sum256([]byte(password))
	if _, _, available, err := grants.RevealXtream(ctx, owner.ID, hex.EncodeToString(oldHash[:])); err != nil || available {
		t.Fatal("old login unexpectedly revealable", err)
	}
	otherUser, _, _, err := grants.IssueXtream(ctx, owner.ID, core.Device{ID: "xtream-other"})
	if err != nil || otherUser == shortUser {
		t.Fatal("username not unique", err)
	}
	for _, spelling := range []string{shortPass, strings.ReplaceAll(shortPass, "-", ""), strings.ToUpper(shortPass)} {
		got, ok := frontend.XtreamGrantCredential(shortUser, spelling)
		if !ok || got != credential {
			t.Fatal("spellings do not share one grant")
		}
		for n := 0; n < 20; n++ {
			v, err := surface.AuthenticateAppPassword(ctx, shortUser, spelling)
			if err != nil || v == nil || v.ID != owner.ID {
				t.Fatal("short login failed", err)
			}
		}
	}
	var stored string
	if err := a.db.QueryRowContext(ctx, `SELECT hash FROM identity_grants WHERE xtream_username=?`, shortUser).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(credential))
	if stored != hex.EncodeToString(sum[:]) {
		t.Fatal("short login must store only the grant hash")
	}
	for n := 0; n < 3; n++ {
		if _, err := surface.AuthenticateAppPassword(ctx, shortUser, "zzzz-zzzz-zz"); !errors.Is(err, core.ErrUnauthorized) {
			t.Fatal(err)
		}
	}
	if _, err := surface.AuthenticateAppPassword(ctx, shortUser, "zzzz-zzzz-zz"); !errors.Is(err, frontend.ErrRateLimited) {
		t.Fatal("brute force not limited", err)
	}
	// Failed attempts cannot interrupt valid API/Range requests for either form.
	if _, err := surface.AuthenticateAppPassword(ctx, username, password); err != nil {
		t.Fatal("old login stopped working", err)
	}
	if _, err := surface.AuthenticateAppPassword(ctx, shortUser, shortPass); err != nil {
		t.Fatal("valid short login locked out", err)
	}
	if err := surface.Revoke(ctx, credential); err != nil {
		t.Fatal(err)
	}
	if _, err := surface.AuthenticateAppPassword(ctx, shortUser, shortPass); err == nil {
		t.Fatal("revoked short login accepted")
	}
	if err := surface.Revoke(ctx, password); err != nil {
		t.Fatal(err)
	}
	if _, err := surface.AuthenticateAppPassword(ctx, username, password); err == nil {
		t.Fatal("revoked app password accepted")
	}
	wrong := surface
	wrong.surface = "jellyfin"
	if _, err := wrong.AuthenticateAppPassword(ctx, username, password); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("cross-surface grant accepted")
	}
}

func TestSignInSuccessResetsIdentityBucketAndLockoutLogsOnceAtWarn(t *testing.T) {
	a := setupTestApp(t, true)
	var logBuf bytes.Buffer
	a.log = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx := context.Background()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 77, Username: "owner"}, "owner-token"); err != nil {
		t.Fatal(err)
	}
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	local, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Create(ctx, owner.ID, "alice", "correct-password-12"); err != nil {
		t.Fatal(err)
	}
	grants, _ := identity.NewGrants(a.db, a.users, time.Hour, nil)
	now := time.Unix(5000, 0)
	login, err := identity.NewLoginLimiter(a.db, 15*time.Minute, 5, 2, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	a.wireIdentityLimiter(login, "login")
	jf := surfaceIdentity{app: a, surface: "jellyfin", local: local, grants: grants, login: login}
	ctx = frontend.WithClientIP(ctx, netip.MustParseAddr("198.51.100.44"))
	dev := core.Device{ID: "firetv-1", Product: "Jellyfin Android TV"}

	// One failure then one success resets (alice, IP) while leaving the IP bucket at 2/5.
	if _, err := jf.SignIn(ctx, "alice", "wrong-password-12", dev); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
	if _, err := jf.SignIn(ctx, "alice", "correct-password-12", dev); err != nil {
		t.Fatalf("valid sign-in failed: %v", err)
	}
	// Two more failures for alice succeed admission (IP=4/5, alice=2/2) because alice's bucket was reset.
	for i := 0; i < 2; i++ {
		if _, err := jf.SignIn(ctx, "alice", "wrong-password-12", dev); !errors.Is(err, core.ErrUnauthorized) {
			t.Fatalf("failure %d after reset: got %v", i, err)
		}
	}
	// Next attempt hits alice+IP limit (IP=5/5, alice=3/2) and logs WARN once.
	if _, err := jf.SignIn(ctx, "alice", "wrong-password-12", dev); !errors.Is(err, frontend.ErrRateLimited) {
		t.Fatalf("expected rate limited, got %v", err)
	}
	// Another failed attempt for bob from the same IP hits the IP-wide limit (IP=6/5),
	// proving alice's earlier success did not reset the IP-wide bucket.
	if _, err := jf.SignIn(ctx, "bob", "wrong-password-12", dev); !errors.Is(err, frontend.ErrRateLimited) {
		t.Fatalf("expected IP-wide rate limit for bob, got %v", err)
	}
	// Repeated retries during lockout must not emit duplicate WARN lines.
	for i := 0; i < 3; i++ {
		if _, err := jf.SignIn(ctx, "alice", "wrong-password-12", dev); !errors.Is(err, frontend.ErrRateLimited) {
			t.Fatalf("repeat %d: got %v", i, err)
		}
	}

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	// Exactly two WARN lines: one when alice+IP locked out, one when the IP-wide bucket locked out.
	if len(lines) != 2 {
		t.Fatalf("got %d WARN lines, want 2:\n%s", len(lines), logBuf.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, "level=WARN") || !strings.Contains(line, `app="Jellyfin Android TV"`) || !strings.Contains(line, "remaining=15m0s") {
			t.Fatalf("unexpected WARN log line: %s", line)
		}
	}
}
