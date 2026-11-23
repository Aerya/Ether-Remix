package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type stubOwnerSource struct {
	owner *core.User
}

func (s stubOwnerSource) Owner(context.Context) (*core.User, error) {
	if s.owner == nil {
		return nil, core.ErrUnauthorized
	}
	u := *s.owner
	return &u, nil
}

type failAuth struct{ called bool }

func (f *failAuth) Authenticate(context.Context, string) (*core.User, error) {
	f.called = true
	return nil, core.ErrUnauthorized
}

func TestAPIKeysHashOnlySurfaceScopedAndImmediateRevoke(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	owner := &core.User{ID: 1, Username: "owner", IsOwner: true}
	keys, err := NewAPIKeys(db, stubOwnerSource{owner: owner}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	info1, secret1, err := keys.Create(ctx, "jellyfin", "Wizarr")
	if err != nil {
		t.Fatal(err)
	}
	if !IsAPIKeyCredential(secret1) || info1.Handle == "" || strings.Contains(info1.Handle, secret1) {
		t.Fatalf("unexpected key/handle: %+v %q", info1, secret1)
	}
	now = now.Add(time.Second)
	info2, secret2, err := keys.Create(ctx, "jellyfin", "Jellyseerr")
	if err != nil {
		t.Fatal(err)
	}
	if secret1 == secret2 || info1.Handle == info2.Handle {
		t.Fatal("duplicate secrets or handles")
	}

	// Database must never store plaintext secrets.
	var storedHash, storedHandle string
	if err := db.QueryRow(`SELECT hash, handle FROM identity_api_keys WHERE id=?`, info1.ID).Scan(&storedHash, &storedHandle); err != nil {
		t.Fatal(err)
	}
	if storedHash == secret1 || strings.Contains(storedHash, "ek1_") || len(storedHash) != 64 {
		t.Fatalf("expected SHA-256 hash only, got %q", storedHash)
	}

	// List preserves ascending creation order so newest is last (Jellyseerr contract).
	listed, err := keys.List(ctx, "jellyfin")
	if err != nil || len(listed) != 2 || listed[0].AppName != "Wizarr" || listed[1].AppName != "Jellyseerr" {
		t.Fatalf("list: %+v err=%v", listed, err)
	}

	// Surface isolation: Jellyfin key resolves on jellyfin, fails on emby.
	viewer, resolved, err := keys.Resolve(ctx, "jellyfin", secret1)
	if err != nil || viewer == nil || !viewer.IsOwner || viewer.Delegated || resolved.AppName != "Wizarr" {
		t.Fatalf("resolve jellyfin: viewer=%+v info=%+v err=%v", viewer, resolved, err)
	}
	if resolved.LastUsed.IsZero() {
		t.Fatal("expected last_used updated on resolve")
	}
	if _, _, err := keys.Resolve(ctx, "emby", secret1); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("cross-surface resolve should fail: %v", err)
	}

	// API keys must never reach external validators (e.g. Plex) or device grants.
	grants, err := NewGrants(db, activeViewerFunc(func(context.Context, int64) (*core.User, error) { return owner, nil }), time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	ext := &failAuth{}
	resolver, err := NewResolver(map[string]Authenticator{"plex": ext}, grants)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(ctx, "plex", secret1); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("plex resolver should reject api key: %v", err)
	}
	if ext.called {
		t.Fatal("external plex validator must never be called for an API key")
	}
	if _, err := grants.Resolve(ctx, "jellyfin", secret1); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("grants resolver should reject api key: %v", err)
	}

	// Revoking by plaintext secret takes effect immediately.
	genBefore := keys.Generation()
	revokedID, err := keys.Revoke(ctx, "jellyfin", secret1)
	if err != nil || revokedID != info1.ID || keys.Generation() <= genBefore {
		t.Fatalf("revoke by secret: id=%d err=%v gen=%d->%d", revokedID, err, genBefore, keys.Generation())
	}
	if _, _, err := keys.Resolve(ctx, "jellyfin", secret1); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("revoked key must immediately fail: %v", err)
	}

	// Revoking by non-secret handle (Jellyfin Web after reload) also works immediately.
	revokedID, err = keys.Revoke(ctx, "jellyfin", info2.Handle)
	if err != nil || revokedID != info2.ID {
		t.Fatalf("revoke by handle: id=%d err=%v", revokedID, err)
	}
	if _, _, err := keys.Resolve(ctx, "jellyfin", secret2); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("handle-revoked key must immediately fail: %v", err)
	}

	// RevokeByID works for console revocation and returns ErrNotFound when already revoked.
	info3, secret3, err := keys.Create(ctx, "emby", "Wizarr")
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.RevokeByID(ctx, "emby", info3.ID); err != nil {
		t.Fatal(err)
	}
	if err := keys.RevokeByID(ctx, "emby", info3.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second RevokeByID: got %v, want ErrNotFound", err)
	}
	if _, _, err := keys.Resolve(ctx, "emby", secret3); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("RevokeByID key must immediately fail: %v", err)
	}
}

type activeViewerFunc func(context.Context, int64) (*core.User, error)

func (f activeViewerFunc) ActiveUser(ctx context.Context, id int64) (*core.User, error) {
	return f(ctx, id)
}
