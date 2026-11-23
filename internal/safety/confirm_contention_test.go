package safety

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/viewerpolicy"
)

func TestConfirmAdmissionRestrictedViewerSurvivesEpochContention(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	users, err := auth.New(ctx, db, auth.Options{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := users.BootstrapOwner(ctx, config.Plex{OwnerPlexID: 7, OwnerUsername: "owner", Token: "server-token"}); err != nil {
		t.Fatal(err)
	}
	if err := users.SyncFriends(ctx, []plextv.Friend{{
		PlexID:   9,
		Username: "friend",
		Token:    "friend-token",
		Sections: []string{"1", "2"},
	}}); err != nil {
		t.Fatal(err)
	}
	friend, err := users.Authenticate(ctx, "friend-token")
	if err != nil {
		t.Fatal(err)
	}

	policy, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Restricted(friend) {
		t.Fatal("shared viewer with section restrictions must be classified as restricted")
	}

	var stop atomic.Bool
	var bumps atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if err := viewerpolicy.Bump(ctx, db); err == nil {
				bumps.Add(1)
			}
		}
	}()

	// Wait until the concurrent writer is actively bumping viewer_policy_epoch.
	deadline := time.Now().Add(2 * time.Second)
	for bumps.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	for i := 0; i < 24; i++ {
		fresh, err := policy.ConfirmAdmission(ctx, friend, false, time.Now())
		if err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("ConfirmAdmission iteration %d failed under epoch contention (bumps=%d): %v", i, bumps.Load(), err)
		}
		if fresh == nil || fresh.ID != friend.ID || len(fresh.AllowedLibraries) != 2 {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("ConfirmAdmission iteration %d returned unexpected viewer: %#v", i, fresh)
		}
	}

	stop.Store(true)
	wg.Wait()
}

func TestConfirmAdmissionRestrictedViewerPreservesContextError(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := users.BootstrapOwner(ctx, config.Plex{OwnerPlexID: 7, OwnerUsername: "owner", Token: "server-token"}); err != nil {
		t.Fatal(err)
	}
	if err := users.SyncFriends(ctx, []plextv.Friend{{
		PlexID:   9,
		Username: "friend",
		Token:    "friend-token",
		Sections: []string{"2"},
	}}); err != nil {
		t.Fatal(err)
	}
	friend, err := users.Authenticate(ctx, "friend-token")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	expiredCtx, cancelExpired := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancelExpired()
	if _, err := policy.ConfirmAdmission(expiredCtx, friend, false, time.Now()); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expired context error = %v, want context.DeadlineExceeded (not ErrUnauthorized)", err)
	}

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := policy.ConfirmAdmission(canceledCtx, friend, false, time.Now()); !errors.Is(err, context.Canceled) || errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("canceled context error = %v, want context.Canceled (not ErrUnauthorized)", err)
	}
}
