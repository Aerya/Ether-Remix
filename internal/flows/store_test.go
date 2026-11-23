package flows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t.UTC()}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t.UTC()
}

func mustRawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return b
}

func openTestSQLStore(t *testing.T, clk *fakeClock) *SQLStore {
	t.Helper()
	db := store.OpenTest(t)
	var opts []StoreOption
	if clk != nil {
		opts = append(opts, WithStoreNow(clk.Now))
	}
	return NewSQLStore(db, opts...)
}

func TestSQLStoreFlowCRUDWebhookAndSchedules(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)

	var changed []string
	st.AddOnChange(func(id string) { changed = append(changed, id) })

	f := Flow{
		Name:        "Nightly Cleanup",
		Description: "Runs on schedule",
		Enabled:     true,
		TemplateID:  "tpl_nightly",
		Tags:        []string{"maintenance", "ops"},
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{
					ID:     "sched1",
					Kind:   NodeTriggerSchedule,
					Label:  "Every 10m",
					Config: mustRawJSON(t, ScheduleTriggerConfig{Every: "10m"}),
				},
			},
		},
		Limits: FlowLimits{Concurrency: 2, RateLimitPerMin: 30},
	}

	saved, err := st.SaveFlow(ctx, f)
	if err != nil {
		t.Fatalf("SaveFlow create: %v", err)
	}
	if saved.ID == "" || saved.Version != 1 {
		t.Fatalf("unexpected saved flow metadata: id=%q version=%d", saved.ID, saved.Version)
	}

	got, err := st.GetFlow(ctx, saved.ID)
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if got.Name != f.Name || got.TemplateID != "tpl_nightly" || !slices.Equal(got.Tags, f.Tags) || !got.Enabled {
		t.Fatalf("GetFlow mismatch: %+v", got)
	}

	// Schedule row should have been seeded for +10m.
	clk.Advance(10 * time.Minute)
	due, err := st.ListDueSchedules(ctx, clk.Now(), 10)
	if err != nil {
		t.Fatalf("ListDueSchedules: %v", err)
	}
	if len(due) != 1 || due[0].FlowID != saved.ID || due[0].NodeID != "sched1" {
		t.Fatalf("expected 1 due schedule, got %+v", due)
	}

	// Update bumps version.
	got.Description = "Updated"
	updated, err := st.SaveFlow(ctx, *got)
	if err != nil {
		t.Fatalf("SaveFlow update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("expected version 2, got %d", updated.Version)
	}

	// Webhook token hash lookup.
	hash := HashWebhookToken("secret-tok-123")
	if err := st.SetWebhookTokenHash(ctx, saved.ID, hash); err != nil {
		t.Fatalf("SetWebhookTokenHash: %v", err)
	}
	byHook, err := st.FindFlowByWebhookHash(ctx, hash)
	if err != nil || byHook.ID != saved.ID {
		t.Fatalf("FindFlowByWebhookHash: %v, %+v", err, byHook)
	}
	if _, err := st.FindFlowByWebhookHash(ctx, HashWebhookToken("wrong")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for wrong webhook hash, got %v", err)
	}

	// Disabling removes schedules and stores disabled reason; re-enabling clears it and re-seeds schedule.
	if err := st.SetFlowEnabled(ctx, saved.ID, false, "paused for maintenance"); err != nil {
		t.Fatalf("SetFlowEnabled(false): %v", err)
	}
	disabled, _ := st.GetFlow(ctx, saved.ID)
	if disabled.Enabled || disabled.DisabledReason != "paused for maintenance" {
		t.Fatalf("expected disabled flow with reason, got %+v", disabled)
	}
	dueAfterDisable, _ := st.ListDueSchedules(ctx, clk.Now().Add(time.Hour), 10)
	if len(dueAfterDisable) != 0 {
		t.Fatalf("expected no due schedules while disabled, got %+v", dueAfterDisable)
	}

	if err := st.SetFlowEnabled(ctx, saved.ID, true, ""); err != nil {
		t.Fatalf("SetFlowEnabled(true): %v", err)
	}
	reenabled, _ := st.GetFlow(ctx, saved.ID)
	if !reenabled.Enabled || reenabled.DisabledReason != "" {
		t.Fatalf("expected re-enabled flow with cleared reason, got %+v", reenabled)
	}

	// ListFlows & DeleteFlow.
	all, err := st.ListFlows(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListFlows: len=%d err=%v", len(all), err)
	}
	if err := st.DeleteFlow(ctx, saved.ID); err != nil {
		t.Fatalf("DeleteFlow: %v", err)
	}
	if _, err := st.GetFlow(ctx, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if len(changed) == 0 {
		t.Fatalf("expected onChange callbacks to be invoked")
	}
}

func TestSQLStoreRunsClaimingConcurrencyOrderingAndCursorPagination(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)

	f, err := st.SaveFlow(ctx, Flow{
		Name:    "Ordered Concurrency Flow",
		Enabled: true,
		Graph:   Graph{SchemaVersion: SchemaVersion},
		Limits: FlowLimits{
			Concurrency:  2,
			OrderedByKey: true,
		},
	})
	if err != nil {
		t.Fatalf("SaveFlow: %v", err)
	}

	// Insert 3 runs: two on correlation key "user:1" (ordered), one on "user:2", one on "user:3".
	mkRun := func(id, corr string, started time.Time) Run {
		return Run{
			ID:             id,
			FlowID:         f.ID,
			FlowVersion:    1,
			TriggerKind:    NodeTriggerEvent,
			EventType:      "playback.session.started",
			CorrelationKey: corr,
			Status:         RunPending,
			Attempt:        1,
			StartedAt:      started,
			UpdatedAt:      started,
		}
	}

	t0 := clk.Now()
	for i, r := range []Run{
		mkRun("run_a1", "user:1", t0.Add(1*time.Second)),
		mkRun("run_a2", "user:1", t0.Add(2*time.Second)),
		mkRun("run_b1", "user:2", t0.Add(3*time.Second)),
		mkRun("run_c1", "user:3", t0.Add(4*time.Second)),
	} {
		created, err := st.CreateRunIfAbsent(ctx, r)
		if err != nil || !created {
			t.Fatalf("CreateRunIfAbsent[%d]: created=%v err=%v", i, created, err)
		}
	}

	// Duplicate CreateRunIfAbsent must be ignored cleanly.
	dup, err := st.CreateRunIfAbsent(ctx, mkRun("run_a1", "user:1", t0.Add(1*time.Second)))
	if err != nil || dup {
		t.Fatalf("expected duplicate CreateRunIfAbsent to return false, got dup=%v err=%v", dup, err)
	}

	// Claim with concurrency=2 and orderedByKey=true:
	// Should claim run_a1 ("user:1") and run_b1 ("user:2"), skipping run_a2 (blocked by run_a1)
	// and run_c1 (blocked by concurrency=2).
	claimed, err := st.ClaimDueRuns(ctx, "leader-1", clk.Now(), 30*time.Second, 10)
	if err != nil {
		t.Fatalf("ClaimDueRuns: %v", err)
	}
	if len(claimed) != 2 || claimed[0].ID != "run_a1" || claimed[1].ID != "run_b1" {
		t.Fatalf("unexpected claimed runs: %+v", claimed)
	}
	if claimed[0].Attempt != 1 || claimed[0].LeaseOwner != "leader-1" {
		t.Fatalf("unexpected lease metadata: %+v", claimed[0])
	}

	// Renew lease works for current owner and fails for another owner.
	ok, err := st.RenewRunLease(ctx, "run_a1", "leader-1", clk.Now().Add(60*time.Second))
	if err != nil || !ok {
		t.Fatalf("RenewRunLease owner: ok=%v err=%v", ok, err)
	}
	ok, err = st.RenewRunLease(ctx, "run_a1", "other-node", clk.Now().Add(60*time.Second))
	if err != nil || ok {
		t.Fatalf("RenewRunLease wrong owner should be false: ok=%v err=%v", ok, err)
	}

	// Complete run_a1 -> now run_a2 ("user:1") becomes eligible, and total running is 1 so 1 slot opens.
	rA1 := claimed[0]
	rA1.Status = RunCompleted
	rA1.FinishedAt = clk.Now()
	rA1.LeaseOwner = ""
	rA1.LeaseUntil = time.Time{}
	if err := st.UpdateRun(ctx, rA1); err != nil {
		t.Fatalf("UpdateRun complete run_a1: %v", err)
	}

	claimed2, err := st.ClaimDueRuns(ctx, "leader-1", clk.Now(), 30*time.Second, 10)
	if err != nil {
		t.Fatalf("ClaimDueRuns 2: %v", err)
	}
	if len(claimed2) != 1 || claimed2[0].ID != "run_a2" {
		t.Fatalf("expected run_a2 claimed next, got %+v", claimed2)
	}

	// Advance clock past lease expiry (31s): run_b1 and run_a2 expire and are reclaimed with Attempt=2.
	clk.Advance(31 * time.Second)
	reclaimed, err := st.ClaimDueRuns(ctx, "leader-2", clk.Now(), 30*time.Second, 10)
	if err != nil {
		t.Fatalf("ClaimDueRuns after lease expiry: %v", err)
	}
	if len(reclaimed) != 2 {
		t.Fatalf("expected 2 reclaimed runs, got %d", len(reclaimed))
	}
	for _, r := range reclaimed {
		if r.Attempt != 2 || r.LeaseOwner != "leader-2" {
			t.Fatalf("expected attempt=2 owner=leader-2 on reclaimed run %s, got attempt=%d owner=%q", r.ID, r.Attempt, r.LeaseOwner)
		}
	}

	// CancelRun transitions active run to cancelled and rejects terminal run.
	if err := st.CancelRun(ctx, "run_c1", clk.Now()); err != nil {
		t.Fatalf("CancelRun run_c1: %v", err)
	}
	if err := st.CancelRun(ctx, "run_c1", clk.Now()); !errors.Is(err, ErrRunNotActive) {
		t.Fatalf("expected ErrRunNotActive cancelling terminal run, got %v", err)
	}

	// Cursor pagination across all 4 runs (page size 2 -> 2 pages, no duplicates).
	page1, cur1, err := st.ListRuns(ctx, RunFilter{FlowID: f.ID, Limit: 2})
	if err != nil || len(page1) != 2 || cur1 == "" {
		t.Fatalf("page1: len=%d cur=%q err=%v", len(page1), cur1, err)
	}
	page2, cur2, err := st.ListRuns(ctx, RunFilter{FlowID: f.ID, Limit: 2, Cursor: cur1})
	if err != nil || len(page2) != 2 || cur2 != "" {
		t.Fatalf("page2: len=%d cur=%q err=%v", len(page2), cur2, err)
	}
	seen := map[string]bool{}
	for _, r := range append(page1, page2...) {
		if seen[r.ID] {
			t.Fatalf("duplicate run ID %s across cursor pages", r.ID)
		}
		seen[r.ID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 unique runs across pages, got %d", len(seen))
	}
}

func TestSQLStoreWaitersWindowDedupeAndRetentionPruning(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)

	f, err := st.SaveFlow(ctx, Flow{
		Name:    "Stateful Primitives",
		Enabled: true,
		Graph:   Graph{SchemaVersion: SchemaVersion},
	})
	if err != nil {
		t.Fatalf("SaveFlow: %v", err)
	}

	// Create a waiting run with two sibling waiters (one exact, one wildcard).
	run := Run{
		ID:          "run_wait_1",
		FlowID:      f.ID,
		FlowVersion: 1,
		TriggerKind: NodeTriggerEvent,
		Status:      RunWaiting,
		StartedAt:   clk.Now(),
		UpdatedAt:   clk.Now(),
	}
	if err := st.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	w1 := Waiter{
		ID:             "wait_1",
		RunID:          run.ID,
		FlowID:         f.ID,
		NodeID:         "w_node",
		EventType:      "playback.session.resumed",
		CorrelationKey: "session:42",
		TimeoutAt:      clk.Now().Add(5 * time.Minute),
		CreatedAt:      clk.Now(),
	}
	w2 := Waiter{
		ID:             "wait_2",
		RunID:          run.ID,
		FlowID:         f.ID,
		NodeID:         "w_node",
		EventType:      "playback.session.*",
		CorrelationKey: "session:42",
		TimeoutAt:      clk.Now().Add(5 * time.Minute),
		CreatedAt:      clk.Now(),
	}
	if err := st.CreateWaiter(ctx, w1); err != nil {
		t.Fatalf("CreateWaiter w1: %v", err)
	}
	if err := st.CreateWaiter(ctx, w2); err != nil {
		t.Fatalf("CreateWaiter w2: %v", err)
	}

	// Pattern listing includes both active patterns.
	pats, err := st.ActiveWaiterEventPatterns(ctx)
	if err != nil || len(pats) != 2 {
		t.Fatalf("ActiveWaiterEventPatterns: %v, %v", pats, err)
	}

	// Wrong correlation key matches nothing.
	if ms, err := st.MatchWaiters(ctx, "playback.session.resumed", "session:99", clk.Now()); err != nil || len(ms) != 0 {
		t.Fatalf("expected 0 matches for wrong correlation key, got %v err=%v", ms, err)
	}
	// Matching event matches both exact and wildcard waiters.
	ms, err := st.MatchWaiters(ctx, "playback.session.resumed", "session:42", clk.Now())
	if err != nil || len(ms) != 2 {
		t.Fatalf("expected 2 matching waiters, got %v err=%v", ms, err)
	}

	// Resolving w1 wakes run and cancels sibling w2.
	matched, err := st.ResolveWaiterAndWakeRun(ctx, WaiterMatchCommit{
		WaiterID: w1.ID,
		RunID:    run.ID,
		NodeID:   "w_node",
	}, clk.Now())
	if err != nil || !matched {
		t.Fatalf("ResolveWaiterAndWakeRun: matched=%v err=%v", matched, err)
	}
	wokenRun, _ := st.GetRun(ctx, run.ID)
	if wokenRun.Status != RunPending {
		t.Fatalf("expected woken run status pending, got %s", wokenRun.Status)
	}
	if msAfter, _ := st.MatchWaiters(ctx, "playback.session.stopped", "session:42", clk.Now()); len(msAfter) != 0 {
		t.Fatalf("expected sibling waiter cancelled, still matched: %+v", msAfter)
	}

	// Waiter timeout expiration.
	run2 := Run{ID: "run_wait_2", FlowID: f.ID, FlowVersion: 1, TriggerKind: NodeTriggerEvent, Status: RunWaiting, StartedAt: clk.Now(), UpdatedAt: clk.Now()}
	_ = st.CreateRun(ctx, run2)
	_ = st.CreateWaiter(ctx, Waiter{ID: "wait_exp_a", RunID: run2.ID, FlowID: f.ID, NodeID: "w2", EventType: "a.b", TimeoutAt: clk.Now().Add(time.Minute)})
	_ = st.CreateWaiter(ctx, Waiter{ID: "wait_exp_b", RunID: run2.ID, FlowID: f.ID, NodeID: "w2", EventType: "a.c", TimeoutAt: clk.Now().Add(time.Minute)})
	expired, err := st.ExpireWaiters(ctx, clk.Now().Add(2*time.Minute), 10)
	if err != nil || len(expired) != 1 {
		t.Fatalf("expected 1 deduplicated expired waiter for (run,node), got %+v err=%v", expired, err)
	}

	// Sliding window counter with resetAt=3.
	t0 := clk.Now()
	c1, r1, err := st.RecordWindowEvent(ctx, f.ID, "win1", "k", t0, time.Minute, 3)
	if err != nil || c1 != 1 || r1 {
		t.Fatalf("window 1: c=%d r=%v err=%v", c1, r1, err)
	}
	c2, r2, err := st.RecordWindowEvent(ctx, f.ID, "win1", "k", t0.Add(10*time.Second), time.Minute, 3)
	if err != nil || c2 != 2 || r2 {
		t.Fatalf("window 2: c=%d r=%v err=%v", c2, r2, err)
	}
	c3, r3, err := st.RecordWindowEvent(ctx, f.ID, "win1", "k", t0.Add(20*time.Second), time.Minute, 3)
	if err != nil || c3 != 3 || !r3 {
		t.Fatalf("window 3: c=%d r=%v err=%v", c3, r3, err)
	}
	// After reset, count restarts at 1.
	c4, r4, err := st.RecordWindowEvent(ctx, f.ID, "win1", "k", t0.Add(25*time.Second), time.Minute, 3)
	if err != nil || c4 != 1 || r4 {
		t.Fatalf("window 4 after reset: c=%d r=%v err=%v", c4, r4, err)
	}

	// Dedupe / throttle with maxPerWindow=2 over 5m cooldown.
	a1, h1, err := st.CheckDedupe(ctx, f.ID, "ded1", "alert:1", t0, 5*time.Minute, 2)
	if err != nil || !a1 || h1 != 1 {
		t.Fatalf("dedupe 1: allowed=%v hits=%d err=%v", a1, h1, err)
	}
	a2, h2, err := st.CheckDedupe(ctx, f.ID, "ded1", "alert:1", t0.Add(time.Minute), 5*time.Minute, 2)
	if err != nil || !a2 || h2 != 2 {
		t.Fatalf("dedupe 2: allowed=%v hits=%d err=%v", a2, h2, err)
	}
	a3, h3, err := st.CheckDedupe(ctx, f.ID, "ded1", "alert:1", t0.Add(2*time.Minute), 5*time.Minute, 2)
	if err != nil || a3 || h3 != 3 {
		t.Fatalf("dedupe 3 should be suppressed: allowed=%v hits=%d err=%v", a3, h3, err)
	}
	// After cooldown expires, allowed again with hits=1.
	a4, h4, err := st.CheckDedupe(ctx, f.ID, "ded1", "alert:1", t0.Add(6*time.Minute), 5*time.Minute, 2)
	if err != nil || !a4 || h4 != 1 {
		t.Fatalf("dedupe 4 after cooldown: allowed=%v hits=%d err=%v", a4, h4, err)
	}

	// Retention pruning: create 5 old completed runs and 3 recent completed runs, plus 1 active run.
	for i := range 5 {
		oldAt := clk.Now().Add(-40 * 24 * time.Hour).Add(time.Duration(i) * time.Minute)
		_ = st.CreateRun(ctx, Run{
			ID:          fmt.Sprintf("run_old_%d", i),
			FlowID:      f.ID,
			TriggerKind: NodeTriggerManual,
			Status:      RunCompleted,
			StartedAt:   oldAt,
			UpdatedAt:   oldAt,
			FinishedAt:  oldAt,
		})
	}
	for i := range 3 {
		recentAt := clk.Now().Add(-time.Hour).Add(time.Duration(i) * time.Minute)
		_ = st.CreateRun(ctx, Run{
			ID:          fmt.Sprintf("run_new_%d", i),
			FlowID:      f.ID,
			TriggerKind: NodeTriggerManual,
			Status:      RunCompleted,
			StartedAt:   recentAt,
			UpdatedAt:   recentAt,
			FinishedAt:  recentAt,
		})
	}

	// Prune runs older than 30d and keep at most 2 terminal runs.
	pruned, err := st.PruneHistory(ctx, clk.Now().Add(-30*24*time.Hour), 2)
	if err != nil {
		t.Fatalf("PruneHistory: %v", err)
	}
	if pruned < 6 { // 5 old + 1 excess recent
		t.Fatalf("expected at least 6 pruned runs, got %d", pruned)
	}
	remainingCompleted, _, err := st.ListRuns(ctx, RunFilter{FlowID: f.ID, Status: RunCompleted, Limit: 50})
	if err != nil {
		t.Fatalf("ListRuns after prune: %v", err)
	}
	if len(remainingCompleted) != 2 {
		t.Fatalf("expected 2 remaining completed runs, got %d", len(remainingCompleted))
	}
	// Active pending run must NOT be pruned.
	if _, err := st.GetRun(ctx, run.ID); err != nil {
		t.Fatalf("expected active run %s to survive pruning: %v", run.ID, err)
	}
}
