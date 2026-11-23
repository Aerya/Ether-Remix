package usenet

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/usenet/nntptest"
)

// TestSimultaneousIPBackoffSkipsPrimaryAndReportsServingState verifies that:
//  1. NNTP 481 simultaneous-IP refusals immediately place a primary provider into
//     StateBackoff and climb to DefaultBackoffMax (2m) across repeated refusals.
//  2. Fetcher.route (used by both Body/FetchEach and Stat/StatMany) skips a
//     backing-off higher-priority primary in favor of a healthy secondary.
//  3. HasServingProvider ignores metered Backfill block accounts when primary
//     accounts exist, returning false once all primaries are backing off so
//     resolver ranking can prefer debrid/non-Usenet candidates during the 2m
//     backoff window, and returning true again once the backoff window expires.
func TestSimultaneousIPBackoffSkipsPrimaryAndReportsServingState(t *testing.T) {
	primarySrv := nntptest.Start(t, nntptest.Options{Username: "u", Password: "p"})
	secondarySrv := nntptest.Start(t, nntptest.Options{Username: "u", Password: "p"})
	blockSrv := nntptest.Start(t, nntptest.Options{Username: "u", Password: "p"})

	wantA := []byte("article-alpha")
	wantB := []byte("article-bravo")
	for _, srv := range []*nntptest.Server{primarySrv, secondarySrv, blockSrv} {
		addSeg(t, srv, "a@x", wantA)
		addSeg(t, srv, "b@x", wantB)
	}

	var primaryIPLocked, secondaryIPLocked atomic.Bool
	primaryIPLocked.Store(true)
	primarySrv.SetFault(func(cmd, _ string) nntptest.Fault {
		if cmd == "AUTHINFO" && primaryIPLocked.Load() {
			return nntptest.Fault{Code: 481, Text: "max number of simultaneous IP addresses reached"}
		}
		return nntptest.Fault{}
	})
	secondarySrv.SetFault(func(cmd, _ string) nntptest.Fault {
		if cmd == "AUTHINFO" && secondaryIPLocked.Load() {
			return nntptest.Fault{Code: 481, Text: "max number of simultaneous IP addresses reached"}
		}
		return nntptest.Fault{}
	})

	pCfg := primarySrv.Provider("primary", 2)
	pCfg.Priority = 0
	sCfg := secondarySrv.Provider("secondary", 2)
	sCfg.Priority = 1
	bCfg := blockSrv.Provider("block", 2)
	bCfg.Priority = 2
	bCfg.Backfill = true

	f := newFetcher(t, []config.UsenetProvider{pCfg, sCfg, bCfg}, Options{
		BackoffMin:    DefaultBackoffMin,
		BackoffMax:    DefaultBackoffMax,
		FailThreshold: DefaultFailThreshold,
		Retries:       0,
	})

	var clockMu sync.Mutex
	curNow := time.Unix(1_800_000_000, 0)
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return curNow
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		curNow = curNow.Add(d)
		clockMu.Unlock()
	}
	primaryPool := poolFor(t, f, "primary")
	secondaryPool := poolFor(t, f, "secondary")
	blockPool := poolFor(t, f, "block")
	for _, p := range []*pool{primaryPool, secondaryPool, blockPool} {
		p.brk.mu.Lock()
		p.brk.now = nowFn
		p.brk.mu.Unlock()
	}

	ctx := context.Background()

	// First request hits 481 simultaneous-IP on primary, trips primary into
	// immediate StateBackoff, and serves cleanly from secondary.
	got, err := f.Body(ctx, "a@x", nil, core.PriorityLive)
	if err != nil || !bytes.Equal(got, wantA) {
		t.Fatalf("Body with primary IP-locked = (%q, %v), want %q", got, err, wantA)
	}
	if st := statsFor(f, "primary"); st.State != StateBackoff {
		t.Fatalf("primary state after 481 = %q, want %q", st.State, StateBackoff)
	}
	if !f.HasServingProvider() {
		t.Fatal("HasServingProvider = false while secondary primary is healthy")
	}

	// Step the primary connect backoff ladder up to DefaultBackoffMax (2m).
	var lastWait time.Duration
	for i := 0; i < 8; i++ {
		lastWait = primaryPool.brk.connFailure(errors.New("481 max number of simultaneous IP addresses"))
	}
	if lastWait != DefaultBackoffMax || DefaultBackoffMax != 2*time.Minute {
		t.Fatalf("backoff cap = %v (DefaultBackoffMax=%v), want 2m", lastWait, DefaultBackoffMax)
	}

	// While primary is in its 2-minute backoff window, both Body and Stat/StatMany
	// skip primary completely without dialing it or touching backfill.
	dialsBefore := statsFor(f, "primary").Dials
	gotB, err := f.Body(ctx, "b@x", nil, core.PriorityLive)
	if err != nil || !bytes.Equal(gotB, wantB) {
		t.Fatalf("Body(b@x) during primary 2m backoff = (%q, %v), want %q", gotB, err, wantB)
	}
	if ok, err := f.Stat(ctx, "a@x"); err != nil || !ok {
		t.Fatalf("Stat(a@x) during primary 2m backoff = (%v, %v), want (true, nil)", ok, err)
	}
	if res, err := f.StatMany(ctx, []string{"a@x", "b@x"}); err != nil || !res["a@x"] || !res["b@x"] {
		t.Fatalf("StatMany during primary 2m backoff = (%v, %v)", res, err)
	}
	if gotDials := statsFor(f, "primary").Dials; gotDials != dialsBefore {
		t.Fatalf("primary dialed during 2m backoff: %d -> %d", dialsBefore, gotDials)
	}
	if blockSrv.BodyRequests("a@x")+blockSrv.BodyRequests("b@x") != 0 {
		t.Fatal("backfill block account served while secondary primary was healthy")
	}

	// Drop secondary's idle socket and lock secondary with 481 simultaneous IP too.
	secondaryPool.discardIdle()
	secondaryIPLocked.Store(true)
	_, _ = f.Stat(ctx, "probe-secondary@x")
	if st := statsFor(f, "secondary"); st.State != StateBackoff {
		t.Fatalf("secondary state = %q, want %q", st.State, StateBackoff)
	}

	// Even though a metered Backfill block account is configured and idle,
	// HasServingProvider must report false so resolver ranking sees the primary
	// Usenet backoff.
	if f.HasServingProvider() {
		t.Fatal("HasServingProvider = true when all primary accounts are in 481 backoff and only backfill remains")
	}

	// Extend secondary to 2m too and verify 90s into the 2m window is still backing off.
	for i := 0; i < 8; i++ {
		secondaryPool.brk.connFailure(errors.New("481 max number of simultaneous IP addresses"))
	}
	advance(90 * time.Second)
	if f.HasServingProvider() {
		t.Fatal("HasServingProvider = true 90s into 2-minute backoff window")
	}

	// Once the 2-minute window elapses and primary's IP lock clears, primary
	// serves again and HasServingProvider returns true.
	advance(31 * time.Second)
	primaryIPLocked.Store(false)
	if !f.HasServingProvider() {
		t.Fatal("HasServingProvider = false after 2-minute backoff window elapsed")
	}
	gotAfter, err := f.Body(ctx, "a@x", nil, core.PriorityLive)
	if err != nil || !bytes.Equal(gotAfter, wantA) {
		t.Fatalf("Body after 2m backoff expiry = (%q, %v), want %q", gotAfter, err, wantA)
	}
	if st := statsFor(f, "primary"); st.State != StateOK {
		t.Fatalf("primary state after recovery = %q, want %q", st.State, StateOK)
	}
}
