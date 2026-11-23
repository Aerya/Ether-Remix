package usenet

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/usenet/nntptest"
)

func TestStatTrustUnitThresholdsAndRecovery(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	st := newStatTrust(func() time.Time { return now })

	if st.isUntrusted() || st.healthWeight() != 1.0 {
		t.Fatalf("fresh statTrust: untrusted=%v weight=%v", st.isUntrusted(), st.healthWeight())
	}

	// Fewer than 3 distinct correlated articles must not mark the provider untrusted,
	// and retries of the same message-id within TTL count at most once.
	for range 4 {
		st.noteStatPresent("a1@x")
		st.noteBodyOutcome("a1@x", false)
	}
	st.noteStatPresent("a2@x")
	st.noteBodyOutcome("a2@x", false)
	if st.isUntrusted() {
		t.Fatal("marked untrusted before minimum distinct sample count")
	}

	// Third distinct false positive (both orderings: BODY before STAT too) trips untrusted.
	st.noteBodyOutcome("<a3@x>", false)
	st.noteStatPresent("a3@x")
	if !st.isUntrusted() {
		t.Fatal("expected statTrust to become untrusted at 3/3 false positives")
	}
	if w := st.healthWeight(); w >= 0.25 {
		t.Fatalf("expected lowered untrusted weight, got %v", w)
	}

	// Caller cancellation must never count as a STAT false positive.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	st2 := newStatTrust(func() time.Time { return now })
	for _, id := range []string{"c1@x", "c2@x", "c3@x", "c4@x"} {
		st2.noteStatPresent(id)
		st2.noteBodyResult(id, context.Canceled, true, cctx)
	}
	if v, fp, u, _ := st2.snapshot(); v != 0 || fp != 0 || u {
		t.Fatalf("cancelled fetches polluted statTrust: verified=%d fp=%d untrusted=%v", v, fp, u)
	}

	// Sustained clean downloads bring the failure rate to <= 10% (3/30) and recover trust.
	for i := range 27 {
		id := "ok-" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + "@x"
		st.noteStatPresent(id)
		st.noteBodyOutcome(id, true)
	}
	if st.isUntrusted() {
		_, _, _, rate := st.snapshot()
		t.Fatalf("expected recovery at <=10%% failure rate, still untrusted at rate=%v", rate)
	}
}

func TestStatTrustDetectsLyingProviderAndVerifiesStat(t *testing.T) {
	lying := nntptest.Start(t, nntptest.Options{})
	for _, id := range []string{"trip1@x", "trip2@x", "trip3@x", "dead1@x", "dead2@x", "dead3@x", "good@x"} {
		addSeg(t, lying, id, []byte("payload-"+id))
	}
	lying.SetFault(func(cmd, arg string) nntptest.Fault {
		if cmd == "BODY" && !strings.Contains(arg, "good@x") {
			return nntptest.Fault{Code: 451, Text: "0 " + arg}
		}
		return nntptest.Fault{}
	})

	f := newFetcher(t, []config.UsenetProvider{lying.Provider("lying", 2)}, Options{Retries: 0})
	ctx := context.Background()

	if f.StatUntrusted() {
		t.Fatal("fresh fetcher should trust STAT initially")
	}

	for _, id := range []string{"trip1@x", "trip2@x", "trip3@x"} {
		if ok, err := f.Stat(ctx, id); err != nil || !ok {
			t.Fatalf("initial STAT(%s) = %v, %v", id, ok, err)
		}
		if _, err := f.Body(ctx, id, nil, core.PriorityLive); err == nil {
			t.Fatalf("expected BODY(%s) takedown failure", id)
		}
	}

	st := statsFor(f, "lying")
	if !st.StatUntrusted || st.StatVerified != 3 || st.StatFalsePositives != 3 || !f.StatUntrusted() {
		t.Fatalf("expected provider STAT untrusted after 3/3 BODY failures: %+v", st)
	}

	// Once untrusted, Stat and StatStream must verify 223 claims with BODY instead of trusting STAT.
	if ok, err := f.Stat(ctx, "dead1@x"); err != nil || ok {
		t.Fatalf("untrusted Stat(dead1@x) = %v, %v; want false, nil", ok, err)
	}
	if ok, err := f.Stat(ctx, "good@x"); err != nil || !ok {
		t.Fatalf("untrusted Stat(good@x) = %v, %v; want true, nil", ok, err)
	}

	got := map[string]bool{}
	if err := f.StatStream(ctx, []string{"dead2@x", "good@x", "dead3@x"}, func(id string, present bool) bool {
		got[id] = present
		return true
	}); err != nil {
		t.Fatalf("StatStream: %v", err)
	}
	if got["dead2@x"] || got["dead3@x"] || !got["good@x"] {
		t.Fatalf("StatStream results = %v, want dead=false good=true", got)
	}
}

func TestStatTrustCatchesMidBodyCorruptionInStatStream(t *testing.T) {
	s := nntptest.Start(t, nntptest.Options{})
	for _, id := range []string{"t1@x", "t2@x", "t3@x"} {
		addSeg(t, s, id, []byte("gone"))
	}
	// Six articles > checkBodyDrainMax (200 KiB); header probe alone stops after
	// =ybegin, while the sampled full-body fetch decodes the whole article and
	// catches the bad CRC on badmid@x (index 6/3 = 2).
	big := bytes.Repeat([]byte("z"), 200<<10)
	ids := []string{"s0@x", "s1@x", "badmid@x", "s3@x", "s4@x", "s5@x"}
	for _, id := range ids {
		if id == "badmid@x" {
			s.AddArticle(id, []byte("=ybegin line=128 size=204800 name=f.bin\r\ncorruptdata\r\n=yend size=204800 crc32=deadbeef\r\n"))
			continue
		}
		addSeg(t, s, id, big)
	}
	s.SetFault(func(cmd, arg string) nntptest.Fault {
		if cmd == "BODY" && strings.HasPrefix(strings.Trim(arg, "<>"), "t") {
			return nntptest.Fault{Code: 451, Text: "taken down"}
		}
		return nntptest.Fault{}
	})

	f := newFetcher(t, []config.UsenetProvider{s.Provider("p", 2)}, Options{Retries: 0})
	ctx := context.Background()
	for _, id := range []string{"t1@x", "t2@x", "t3@x"} {
		_, _ = f.Stat(ctx, id)
		_ = f.CheckBody(ctx, id, nil, core.PriorityLive)
	}
	if !f.StatUntrusted() {
		t.Fatal("expected provider STAT to be untrusted")
	}

	got := map[string]bool{}
	if err := f.StatStream(ctx, ids, func(id string, present bool) bool {
		got[id] = present
		return true
	}); err != nil {
		t.Fatalf("StatStream: %v", err)
	}
	if got["badmid@x"] {
		t.Fatalf("corrupt mid-body segment accepted as present: %v", got)
	}
	if !got["s0@x"] || !got["s5@x"] {
		t.Fatalf("valid segments rejected: %v", got)
	}
}

func TestStatTrustLowersWeightAndPrefersHonestProviders(t *testing.T) {
	lying := nntptest.Start(t, nntptest.Options{})
	honest := nntptest.Start(t, nntptest.Options{})
	block := nntptest.Start(t, nntptest.Options{})

	var lyingStatCalls atomic.Int64
	for _, id := range []string{"t1@x", "t2@x", "t3@x", "shared@x", "backfillonly@x"} {
		addSeg(t, lying, id, []byte("data-"+id))
	}
	addSeg(t, honest, "shared@x", []byte("data-shared@x"))
	addSeg(t, block, "backfillonly@x", []byte("data-backfillonly@x"))

	lying.SetFault(func(cmd, arg string) nntptest.Fault {
		if cmd == "STAT" {
			lyingStatCalls.Add(1)
		}
		if cmd == "BODY" {
			return nntptest.Fault{Code: 451, Text: "taken down"}
		}
		return nntptest.Fault{}
	})

	lp := lying.Provider("lying", 2)
	lp.Priority = 0
	hp := honest.Provider("honest", 2)
	hp.Priority = 1 // lower-priority primary tier, but honest
	bp := block.Provider("block", 2)
	bp.Backfill = true

	f := newFetcher(t, []config.UsenetProvider{lp, hp, bp}, Options{Retries: 0})
	ctx := context.Background()

	// Trip STAT distrust on the tier-0 primary.
	for _, id := range []string{"t1@x", "t2@x", "t3@x"} {
		_, _ = f.Stat(ctx, id)
		_ = f.CheckBody(ctx, id, nil, core.PriorityLive)
	}
	if !statsFor(f, "lying").StatUntrusted {
		t.Fatal("expected lying provider to be untrusted")
	}

	// Health routing demotes untrusted primary behind honest primary (even across primary tiers),
	// so shared@x is answered by honest without touching lying's STAT at all.
	beforeLyingStats := lyingStatCalls.Load()
	if ok, err := f.Stat(ctx, "shared@x"); err != nil || !ok {
		t.Fatalf("Stat(shared@x) = %v, %v", ok, err)
	}
	if lyingStatCalls.Load() != beforeLyingStats {
		t.Fatalf("untrusted primary was queried before honest primary (stat calls %d -> %d)", beforeLyingStats, lyingStatCalls.Load())
	}

	// An article missing on honest, lying-223+451 on lying, and present on backfill
	// must fail over past lying's 223 lie and succeed on backfill.
	if ok, err := f.Stat(ctx, "backfillonly@x"); err != nil || !ok {
		t.Fatalf("Stat(backfillonly@x) = %v, %v; want true via backfill", ok, err)
	}

	// Within the same priority tier, order() also lowers the untrusted provider's weight.
	hpSame := hp
	hpSame.Priority = 0
	fSame := newFetcher(t, []config.UsenetProvider{lp, hpSame}, Options{Retries: 0})
	poolFor(t, fSame, "lying").statTrust.noteStatPresent("x1@x")
	poolFor(t, fSame, "lying").statTrust.noteBodyOutcome("x1@x", false)
	poolFor(t, fSame, "lying").statTrust.noteStatPresent("x2@x")
	poolFor(t, fSame, "lying").statTrust.noteBodyOutcome("x2@x", false)
	poolFor(t, fSame, "lying").statTrust.noteStatPresent("x3@x")
	poolFor(t, fSame, "lying").statTrust.noteBodyOutcome("x3@x", false)
	for range 6 {
		ord := fSame.order()
		if ord[0].cfg.ID != "honest" {
			t.Fatalf("same-tier order picked %s ahead of honest provider", ord[0].cfg.ID)
		}
	}
}

func TestVerifyArticles(t *testing.T) {
	lying := nntptest.Start(t, nntptest.Options{})
	ids := []string{"v1@x", "v2@x", "v3@x", "v4@x"}
	for _, id := range ids {
		addSeg(t, lying, id, []byte("video-segment-"+id))
	}
	takedown := atomic.Bool{}
	takedown.Store(true)
	lying.SetFault(func(cmd, _ string) nntptest.Fault {
		if cmd == "BODY" && takedown.Load() {
			return nntptest.Fault{Code: 451, Text: "taken down"}
		}
		return nntptest.Fault{}
	})

	f := newFetcher(t, []config.UsenetProvider{lying.Provider("p", 2)}, Options{Retries: 0})
	ctx := context.Background()

	if ok, err := f.VerifyArticles(ctx, nil, nil); err != nil || !ok {
		t.Fatalf("VerifyArticles(empty) = %v, %v", ok, err)
	}
	// Even on a cold fetcher where STAT has not yet been marked untrusted,
	// VerifyArticles samples BODY and rejects STAT-223 + BODY-451 takedowns.
	if ok, err := f.VerifyArticles(ctx, ids, nil); err != nil || ok {
		t.Fatalf("VerifyArticles(takedown) = %v, %v; want false, nil", ok, err)
	}

	// With a backfill account that actually serves BODY, VerifyArticles succeeds.
	block := nntptest.Start(t, nntptest.Options{})
	for _, id := range ids {
		addSeg(t, block, id, []byte("video-segment-"+id))
	}
	bp := block.Provider("block", 2)
	bp.Backfill = true
	fWithBackfill := newFetcher(t, []config.UsenetProvider{lying.Provider("p", 2), bp}, Options{Retries: 0})
	if ok, err := fWithBackfill.VerifyArticles(ctx, ids, nil); err != nil || !ok {
		t.Fatalf("VerifyArticles(with backfill) = %v, %v; want true, nil", ok, err)
	}
}
