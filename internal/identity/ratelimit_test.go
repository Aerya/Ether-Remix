package identity

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestLoginLimiterBuckets(t *testing.T) {
	db := store.OpenTest(t)
	now := time.Unix(1000, 0)
	l, err := NewLoginLimiter(db, time.Minute, 3, 2, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ip := netip.MustParseAddr("192.0.2.1")
	check := func(id string, want bool) {
		t.Helper()
		got, err := l.Allow(ctx, id, ip)
		if err != nil || got != want {
			t.Fatalf("%s: got %v, %v; want %v", id, got, err, want)
		}
	}
	check("alice", true)
	check("alice", true)
	check("alice", false)
	// Even if either prior login succeeded, there is no success reset; attempts
	// for another username still encounter the exhausted IP bucket.
	check("bob", false)
	now = now.Add(time.Minute)
	check("bob", true)
	if ok, err := l.Allow(ctx, "alice", netip.MustParseAddr("192.0.2.2")); err != nil || !ok {
		t.Fatalf("other IP: %v %v", ok, err)
	}
	// Different bucket start times require waiting for both windows. Denied
	// attempts increase the count but must not slide either expiry forward.
	ip = netip.MustParseAddr("192.0.2.3")
	check("alice", true)
	check("alice", true)
	now = now.Add(10 * time.Second)
	check("bob", true)
	check("bob", false)
	allowed, retry, err := l.AllowWithRetry(ctx, "bob", ip)
	if err != nil || allowed || retry != time.Minute {
		t.Fatalf("later identity window: allowed=%v retry=%v err=%v", allowed, retry, err)
	}
	now = now.Add(50 * time.Second)
	allowed, retry, err = l.AllowWithRetry(ctx, "bob", ip)
	if err != nil || allowed || retry != 10*time.Second {
		t.Fatalf("remaining identity window: allowed=%v retry=%v err=%v", allowed, retry, err)
	}
	now = now.Add(10 * time.Second)
	allowed, retry, err = l.AllowWithRetry(ctx, "bob", ip)
	if err != nil || !allowed || retry != 0 {
		t.Fatalf("natural reset: allowed=%v retry=%v err=%v", allowed, retry, err)
	}
}

func TestLoginLimiterCleanupIsBounded(t *testing.T) {
	db := store.OpenTest(t)
	now := time.Unix(1000, 0)
	l, err := NewLoginLimiter(db, time.Minute, 100, 100, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, key := range []string{"a", "b", "c"} {
		if _, err = db.ExecContext(ctx, `INSERT INTO identity_rate_buckets(bucket_key,window_start,attempts) VALUES(?,?,?)`, key, now.Add(-time.Hour).UnixMilli(), 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO identity_rate_buckets(bucket_key,window_start,attempts) VALUES(?,?,?)`, "active", now.UnixMilli(), 1); err != nil {
		t.Fatal(err)
	}
	n, err := l.Cleanup(ctx, 2)
	if err != nil || n != 2 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM identity_rate_buckets`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("remaining: %d %v", count, err)
	}
}

func TestPollingLimitsAreIndependentOfLogin(t *testing.T) {
	db := store.OpenTest(t)
	now := time.Unix(1000, 0)
	ctx := context.Background()
	ip := netip.MustParseAddr("192.0.2.1")
	login, _ := NewLoginLimiter(db, 15*time.Minute, 30, 8, func() time.Time { return now })
	poll, _ := NewPollLimiter(db, time.Minute, 120, 60, func() time.Time { return now })
	// A complete ten-minute PIN flow, polling every two seconds, is admitted.
	for n := 0; n < 300; n++ {
		ok, err := poll.Allow(ctx, "opaque-handle", ip)
		if err != nil || !ok {
			t.Fatalf("poll %d: %v %v", n, ok, err)
		}
		now = now.Add(2 * time.Second)
	}
	for n := 0; n < 8; n++ {
		ok, err := login.Allow(ctx, "alice", ip)
		if err != nil || !ok {
			t.Fatalf("login %d: %v %v", n, ok, err)
		}
	}
	// A poll cleanup must not delete a still-valid longer login window.
	now = now.Add(2 * time.Minute)
	if _, err := poll.Cleanup(ctx, 100); err != nil {
		t.Fatal(err)
	}
	ok, err := login.Allow(ctx, "alice", ip)
	if err != nil || ok {
		t.Fatalf("login budget reset by polling: %v %v", ok, err)
	}
	// An abusive burst of the same polling handle is bounded.
	for n := 0; n < 60; n++ {
		ok, err = poll.Allow(ctx, "new-handle", ip)
		if err != nil || !ok {
			t.Fatalf("burst %d: %v %v", n, ok, err)
		}
	}
	ok, err = poll.Allow(ctx, "new-handle", ip)
	if err != nil || ok {
		t.Fatalf("poll abuse admitted: %v %v", ok, err)
	}
}

func TestLoginLimiterResetListClearAndLogOnce(t *testing.T) {
	db := store.OpenTest(t)
	now := time.Unix(2000, 0)
	ctx := WithRateLimitApp(context.Background(), "Jellyfin Android TV")
	ip := netip.MustParseAddr("198.51.100.10")
	login, err := NewLoginLimiter(db, 15*time.Minute, 4, 2, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	poll, err := NewPollLimiter(db, time.Minute, 10, 2, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	var events []RateLimitEvent
	login.SetOnLimited(func(ev RateLimitEvent) { events = append(events, ev) })

	// Two attempts for alice, then ResetIdentity clears alice+IP but preserves the IP count (2/4).
	for i := 0; i < 2; i++ {
		if ok, err := login.Allow(ctx, "alice", ip); err != nil || !ok {
			t.Fatalf("alice attempt %d: %v %v", i, ok, err)
		}
	}
	if err := login.ResetIdentity(ctx, "alice", ip); err != nil {
		t.Fatal(err)
	}
	// Alice can attempt again (IP count becomes 3/4, alice+IP becomes 1/2).
	if ok, err := login.Allow(ctx, "alice", ip); err != nil || !ok {
		t.Fatalf("alice after reset: %v %v", ok, err)
	}
	// Bob gets only 1 attempt before the IP bucket reaches 4/4 (proof IP count was not reset).
	if ok, err := login.Allow(ctx, "bob", ip); err != nil || !ok {
		t.Fatalf("bob attempt 1: %v %v", ok, err)
	}
	if ok, err := login.Allow(ctx, "bob", ip); err != nil || ok {
		t.Fatalf("bob attempt 2 should hit IP limit: %v %v", ok, err)
	}
	// Repeated rejections in the same window fire FirstInWin only once.
	for i := 0; i < 3; i++ {
		if ok, err := login.Allow(ctx, "bob", ip); err != nil || ok {
			t.Fatalf("repeat %d: %v %v", i, ok, err)
		}
	}
	firstCount := 0
	for _, ev := range events {
		if ev.FirstInWin {
			firstCount++
			if ev.App != "Jellyfin Android TV" || ev.Remaining != 15*time.Minute || ev.RemoteIP != ip.String() {
				t.Fatalf("unexpected first lockout event: %+v", ev)
			}
		}
	}
	if firstCount != 1 {
		t.Fatalf("FirstInWin count = %d, want 1 (events=%+v)", firstCount, events)
	}

	// Poll lockout with an opaque secret must not persist or return the secret.
	pollSecret := "super-secret-quickconnect-poll-token"
	for i := 0; i < 3; i++ {
		_, _ = poll.Allow(ctx, pollSecret, ip)
	}
	pollLockouts, err := poll.ListLockouts(ctx)
	if err != nil || len(pollLockouts) != 1 {
		t.Fatalf("poll lockouts: %+v %v", pollLockouts, err)
	}
	if pollLockouts[0].Identity != "" {
		t.Fatalf("poll lockout leaked secret identity: %q", pollLockouts[0].Identity)
	}

	// ListLockouts shows both the IP-wide and bob+IP locked buckets; clearing
	// only the IP bucket unblocks carol while bob remains locked until cleared.
	loginLockouts, err := login.ListLockouts(ctx)
	if err != nil || len(loginLockouts) != 2 {
		t.Fatalf("login lockouts: %+v %v", loginLockouts, err)
	}
	var ipKey string
	for _, lo := range loginLockouts {
		if lo.App != "Jellyfin Android TV" || lo.RemoteIP != ip.String() {
			t.Fatalf("unexpected lockout row: %+v", lo)
		}
		if lo.Scope == "ip" {
			ipKey = lo.Key
		}
	}
	if ipKey == "" {
		t.Fatalf("missing IP-wide lockout row in %+v", loginLockouts)
	}
	cleared, err := login.ClearLockout(ctx, ipKey)
	if err != nil || !cleared {
		t.Fatalf("ClearLockout: %v %v", cleared, err)
	}
	if ok, err := login.Allow(ctx, "carol", ip); err != nil || !ok {
		t.Fatalf("carol after ClearLockout(ip): %v %v", ok, err)
	}
	if ok, err := login.Allow(ctx, "bob", ip); err != nil || ok {
		t.Fatalf("bob should still be locked until bob+ip bucket is cleared: %v %v", ok, err)
	}
	if n, err := poll.ClearAll(ctx); err != nil || n == 0 {
		t.Fatalf("ClearAll poll: %d %v", n, err)
	}
	if left, err := poll.ListLockouts(ctx); err != nil || len(left) != 0 {
		t.Fatalf("poll lockouts after ClearAll: %+v %v", left, err)
	}
}
