package identity_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestQuickConnect(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	_, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,99,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	q, err := identity.NewQuickConnect(db, time.Minute, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	code, secret, err := q.Initiate(ctx, "jellyfin", core.Device{ID: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 {
		t.Fatal(code)
	}
	details, err := q.DetailsSurface(ctx, "jellyfin", secret)
	if err != nil || !details.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("expiry: %+v %v", details, err)
	}
	if _, err = q.Poll(ctx, secret); !errors.Is(err, identity.ErrQuickConnectPending) {
		t.Fatalf("pending: %v", err)
	}
	if err = q.Approve(ctx, code, 1); err != nil {
		t.Fatal(err)
	}
	if err = q.Approve(ctx, code, 1); !errors.Is(err, identity.ErrQuickConnectUsed) {
		t.Fatalf("duplicate approval: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, e := q.Poll(ctx, secret)
			if e == nil {
				results <- token
			}
		}()
	}
	wg.Wait()
	close(results)
	var tokens []string
	for token := range results {
		tokens = append(tokens, token)
	}
	if len(tokens) != 1 {
		t.Fatalf("successful redemptions: %d", len(tokens))
	}
	if err = q.Approve(ctx, code, 1); !errors.Is(err, identity.ErrQuickConnectUsed) {
		t.Fatalf("consumed approval: %v", err)
	}
	if err = q.Approve(ctx, "ZZZZZZ", 1); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("unknown code: %v", err)
	}
	grants, err := identity.NewGrants(db, members{db}, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = grants.Resolve(ctx, "jellyfin", tokens[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = grants.Resolve(ctx, "plex", tokens[0]); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("cross surface: %v", err)
	}
	code, secret, err = q.Initiate(ctx, "emby", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err = q.Approve(ctx, code, 1); !errors.Is(err, identity.ErrQuickConnectExpired) {
		t.Fatalf("expired approval: %v", err)
	}
	if _, err = q.Poll(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expired poll: %v", err)
	}
	code, secret, err = q.Initiate(ctx, "emby", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Approve(ctx, code, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE auth_users SET active=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Poll(ctx, secret); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("inactive: %v", err)
	}
}

func TestQuickConnectReplayIsDeviceBound(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,99,0,0)`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2000, 0)
	q, err := identity.NewQuickConnect(db, time.Minute, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	phone := core.Device{ID: "apple-tv", Client: "Swiftfin"}
	code, secret, err := q.Initiate(ctx, "jellyfin", phone)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Approve(ctx, code, 1); err != nil {
		t.Fatal(err)
	}
	first, err := q.PollSurfaceDevice(ctx, "jellyfin", secret, phone)
	if err != nil {
		t.Fatal(err)
	}
	again, err := q.PollSurfaceDevice(ctx, "jellyfin", secret, phone)
	if err != nil || again != first {
		t.Fatalf("replay %q %v", again, err)
	}
	if _, err = q.PollSurfaceDevice(ctx, "jellyfin", secret, core.Device{ID: "other", Client: "Swiftfin"}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("other device: %v", err)
	}
	if _, err = q.PollSurfaceDevice(ctx, "jellyfin", secret, core.Device{ID: "apple-tv", Client: "Infuse"}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("other client: %v", err)
	}
	grants, err := identity.NewGrants(db, members{db}, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(first))
	if err = grants.RevokeGrant(ctx, 1, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if _, err = q.PollSurfaceDevice(ctx, "jellyfin", secret, phone); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("revoked replay: %v", err)
	}
	code, secret, err = q.Initiate(ctx, "jellyfin", phone)
	if err != nil || q.Approve(ctx, code, 1) != nil {
		t.Fatal(err)
	}
	if _, err = q.PollSurfaceDevice(ctx, "jellyfin", secret, phone); err != nil {
		t.Fatal(err)
	}
	now = now.Add(61 * time.Second)
	if _, err = q.PollSurfaceDevice(ctx, "jellyfin", secret, phone); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expired replay: %v", err)
	}
}

func TestQuickConnectApprovalNamesTheDevice(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,99,0,0)`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2000, 0)
	q, err := identity.NewQuickConnect(db, time.Minute, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := q.Initiate(ctx, "jellyfin", core.Device{DeviceName: "Living Room", Product: "Jellyfin", Client: "Apple TV"})
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Approve(ctx, " "+strings.ToLower(code[:3])+"-"+code[3:]+" ", 1); err != nil {
		t.Fatal(err)
	}
	target, err := q.ApprovedTarget(ctx, code, 1)
	if err != nil || target.DeviceName != "Living Room" || target.Surface != "jellyfin" || target.Product != "Jellyfin" {
		t.Fatalf("target: %+v %v", target, err)
	}
}

func TestQuickConnectJellyfinCodesAreSixDigits(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Unix(3000, 0)
	q, err := identity.NewQuickConnect(db, time.Minute, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		code, _, err := q.Initiate(ctx, "jellyfin", core.Device{ID: "firetv"})
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 6 || code[0] < '1' || code[0] > '9' {
			t.Fatalf("jellyfin code %q outside [100000, 999999]", code)
		}
		for _, ch := range code {
			if ch < '0' || ch > '9' {
				t.Fatalf("jellyfin code %q contains non-digit %q", code, ch)
			}
		}
	}
	code, _, err := q.Initiate(ctx, "emby", core.Device{ID: "appletv"})
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 {
		t.Fatalf("emby code length: %q", code)
	}
}
