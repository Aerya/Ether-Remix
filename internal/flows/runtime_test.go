package flows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
)

type recordedCall struct {
	Actx   ActionContext
	Params map[string]any
}

type fakeAction struct {
	spec  ActionSpec
	mu    sync.Mutex
	calls []recordedCall
	fn    func(ctx context.Context, actx ActionContext, params map[string]any) (ActionResult, error)
}

func newFakeAction(actionType string, idempotent bool, fn func(ctx context.Context, actx ActionContext, params map[string]any) (ActionResult, error)) *fakeAction {
	return &fakeAction{
		spec: ActionSpec{
			Type:       actionType,
			Owner:      "builtin",
			Group:      "test",
			Label:      actionType,
			Idempotent: idempotent,
			Params: []ParamSchema{
				{Key: "message", Label: "Message", Type: events.FieldString, Default: "default-msg"},
				{Key: "apiKey", Label: "API Key", Type: events.FieldString, Secret: true},
			},
		},
		fn: fn,
	}
}

func (a *fakeAction) Spec() ActionSpec { return a.spec }

func (a *fakeAction) Execute(ctx context.Context, actx ActionContext, params map[string]any) (ActionResult, error) {
	cp := make(map[string]any, len(params))
	for k, v := range params {
		cp[k] = v
	}
	a.mu.Lock()
	a.calls = append(a.calls, recordedCall{Actx: actx, Params: cp})
	a.mu.Unlock()
	if a.fn != nil {
		return a.fn(ctx, actx, cp)
	}
	return ActionResult{Output: map[string]any{"ok": true, "message": cp["message"]}}, nil
}

func (a *fakeAction) Calls() []recordedCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]recordedCall, len(a.calls))
	copy(out, a.calls)
	return out
}

func newTestEvent(id, eventType string, corr events.Correlation, payload any) events.Envelope {
	b, _ := json.Marshal(payload)
	return events.Envelope{
		ID:          id,
		Type:        eventType,
		Version:     1,
		OccurredAt:  time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		Node:        "node-1",
		Source:      events.SourceBuiltin,
		Correlation: corr,
		Payload:     b,
	}
}

func newConditionFlow(t *testing.T, name, eventType, actionType string, cond ConditionGroup) Flow {
	t.Helper()
	return Flow{
		Name:    name,
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{
					ID:     "trig",
					Kind:   NodeTriggerEvent,
					Label:  "Trigger",
					Config: mustRawJSON(t, EventTriggerConfig{Events: []string{eventType}}),
				},
				{
					ID:     "cond",
					Kind:   NodeCondition,
					Label:  "Check",
					Config: mustRawJSON(t, ConditionNodeConfig{Condition: cond}),
				},
				{
					ID:    "act",
					Kind:  NodeAction,
					Label: "Run Action",
					Config: mustRawJSON(t, ActionNodeConfig{
						ActionType: actionType,
						Params: map[string]any{
							"message": "Hello {{ payload.user }} ({{ payload.quality }})",
							"apiKey":  "super-secret-value",
						},
					}),
				},
			},
			Edges: []Edge{
				{ID: "e1", Source: "trig", SourceHandle: HandleNext, Target: "cond"},
				{ID: "e2", Source: "cond", SourceHandle: HandleTrue, Target: "act"},
			},
		},
	}
}

// TestSmokeEventConditionActionOnceDespiteReplay is the required F2 smoke test:
// an event triggers a flow, its condition passes, and the action runs once despite replay.
func TestSmokeEventConditionActionOnceDespiteReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)
	bus := events.NewBus(events.BusOptions{})
	actions := NewActionRegistry()

	actionDone := make(chan recordedCall, 4)
	act := newFakeAction("test.notify", false, func(_ context.Context, actx ActionContext, params map[string]any) (ActionResult, error) {
		actionDone <- recordedCall{Actx: actx, Params: params}
		return ActionResult{Output: map[string]any{"sent": true}}, nil
	})
	if err := actions.Register(act); err != nil {
		t.Fatalf("Register action: %v", err)
	}

	flowDef := newConditionFlow(t, "4K Transcode Alert", "playback.session.started", "test.notify", ConditionGroup{
		Op: "and",
		Rules: []ConditionRule{
			{Field: "payload.quality", Op: "eq", Value: "4k"},
		},
	})
	saved, err := st.SaveFlow(ctx, flowDef)
	if err != nil {
		t.Fatalf("SaveFlow: %v", err)
	}

	leaderReady := make(chan struct{})
	runDoneCh := make(chan Run, 8)
	eng := NewEngine(EngineOptions{
		Store:           st,
		Bus:             bus,
		Actions:         actions,
		Now:             clk.Now,
		TickInterval:    time.Hour, // rely on event wakeups, never wall-clock ticks
		OnLeaderStarted: func() { close(leaderReady) },
		OnRunUpdated: func(r Run) {
			if r.Status.Terminal() {
				runDoneCh <- r
			}
		},
	})

	startErr := make(chan error, 1)
	go func() { startErr <- eng.Start(ctx) }()
	<-leaderReady

	ev := newTestEvent("evt_smoke_replay_1", "playback.session.started", events.Correlation{SessionID: "sess-1"}, map[string]any{
		"user":    "Alice",
		"quality": "4k",
	})

	// Publish event once, then immediately replay the identical envelope on the bus.
	bus.Publish(ev)
	select {
	case call := <-actionDone:
		if call.Params["message"] != "Hello Alice (4k)" {
			t.Fatalf("unexpected rendered param: %v", call.Params["message"])
		}
		if call.Actx.IdempotencyKey == "" {
			t.Fatalf("expected non-empty IdempotencyKey")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for action execution")
	}

	select {
	case finished := <-runDoneCh:
		if finished.Status != RunCompleted {
			t.Fatalf("expected completed run, got %s (err=%s)", finished.Status, finished.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for run completion")
	}

	// Replay the same event via local bus AND via events_outbox directly.
	bus.Publish(ev)
	outbox := NewOutboxStore(st.DB(), clk.Now)
	if _, err := outbox.Enqueue(ctx, ev); err != nil {
		t.Fatalf("outbox.Enqueue replay: %v", err)
	}
	if err := eng.ProcessPending(ctx); err != nil {
		t.Fatalf("ProcessPending after replay: %v", err)
	}

	// Also publish a non-matching event where the condition fails (false branch).
	nonMatch := newTestEvent("evt_smoke_nomatch", "playback.session.started", events.Correlation{SessionID: "sess-2"}, map[string]any{
		"user":    "Bob",
		"quality": "1080p",
	})
	bus.Publish(nonMatch)
	select {
	case finished := <-runDoneCh:
		if finished.Status != RunCompleted {
			t.Fatalf("expected completed run for non-matching condition, got %s", finished.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for non-matching run")
	}

	// Verify the action ran strictly once across initial delivery + replays + non-matching event.
	calls := act.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected action to run once despite replay, got %d calls", len(calls))
	}

	// Verify secret parameter was redacted in persisted step input.
	runs, _, err := st.ListRuns(ctx, RunFilter{FlowID: saved.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs (1 matched + 1 non-matched, replays deduplicated), got %d", len(runs))
	}
	matchedRunID := EventRunID(saved.ID, "trig", ev.ID)
	steps, err := st.ListRunSteps(ctx, matchedRunID)
	if err != nil {
		t.Fatalf("ListRunSteps: %v", err)
	}
	var actStep *RunStep
	for i := range steps {
		if steps[i].NodeID == "act" {
			actStep = &steps[i]
		}
	}
	if actStep == nil {
		t.Fatalf("expected persisted action step, got %+v", steps)
	}
	var inp map[string]any
	_ = json.Unmarshal(actStep.Input, &inp)
	if inp["apiKey"] != events.RedactedValue {
		t.Fatalf("expected secret param redacted as %q, got %v", events.RedactedValue, inp["apiKey"])
	}

	cancel()
	if err := <-startErr; err != nil {
		t.Fatalf("Engine.Start returned error: %v", err)
	}
}

func TestRuntimeTriggerPreFilterAndDAGFeatures(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)
	actions := NewActionRegistry()

	var executed []string
	var execMu sync.Mutex
	recordExec := func(name string, out map[string]any, setVarKey, setVarVal string, retErr error) *fakeAction {
		return newFakeAction(name, true, func(_ context.Context, actx ActionContext, params map[string]any) (ActionResult, error) {
			execMu.Lock()
			executed = append(executed, fmt.Sprintf("%s:%v", actx.NodeID, params["message"]))
			execMu.Unlock()
			if setVarKey != "" {
				actx.SetVar(setVarKey, setVarVal)
			}
			if retErr != nil {
				return ActionResult{}, retErr
			}
			return ActionResult{Output: out}, nil
		})
	}

	_ = actions.Register(recordExec("act.ok", map[string]any{"tag": "v1"}, "greeting", "hi-from-var", nil))
	_ = actions.Register(recordExec("act.fail", nil, "", "", errors.New("boom")))
	_ = actions.Register(newFakeAction("act.panic", true, func(_ context.Context, _ ActionContext, _ map[string]any) (ActionResult, error) {
		panic("unexpected crash")
	}))

	// Flow with trigger pre-filter (only severity=="high"), switch (matchAll), diamond merge,
	// disabled node skip, SetVar propagation, and error-handle recovery.
	flowDef := Flow{
		Name:    "Rich DAG",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{
					ID:   "trig",
					Kind: NodeTriggerEvent,
					Config: mustRawJSON(t, EventTriggerConfig{
						Events: []string{"system.*"},
						Filter: &ConditionGroup{
							Op:    "and",
							Rules: []ConditionRule{{Field: "payload.severity", Op: "eq", Value: "high"}},
						},
					}),
				},
				{
					ID:   "sw",
					Kind: NodeLogicSwitch,
					Config: mustRawJSON(t, SwitchNodeConfig{
						MatchAll: true,
						Cases: []SwitchCase{
							{Key: "c1", Label: "Case 1", Condition: ConditionGroup{Op: "and", Rules: []ConditionRule{{Field: "payload.code", Op: "gte", Value: 500}}}},
							{Key: "c2", Label: "Case 2", Condition: ConditionGroup{Op: "and", Rules: []ConditionRule{{Field: "payload.notify", Op: "is_true"}}}},
						},
					}),
				},
				{
					ID:     "a1",
					Kind:   NodeAction,
					Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.ok", Params: map[string]any{"message": "branch1"}}),
				},
				{
					ID:       "disabled_mid",
					Kind:     NodeAction,
					Disabled: true,
					Config:   mustRawJSON(t, ActionNodeConfig{ActionType: "act.fail"}),
				},
				{
					ID:     "join",
					Kind:   NodeAction,
					Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.ok", Params: map[string]any{"message": "joined:{{ vars.greeting }}:{{ steps.a1.output.tag }}"}}),
				},
				{
					ID:     "failing",
					Kind:   NodeAction,
					Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.panic"}),
				},
				{
					ID:     "recovered",
					Kind:   NodeAction,
					Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.ok", Params: map[string]any{"message": "recovered"}}),
				},
			},
			Edges: []Edge{
				{ID: "e1", Source: "trig", SourceHandle: HandleNext, Target: "sw"},
				{ID: "e2", Source: "sw", SourceHandle: "c1", Target: "a1"},
				{ID: "e3", Source: "sw", SourceHandle: "c2", Target: "disabled_mid"},
				{ID: "e4", Source: "a1", SourceHandle: HandleNext, Target: "join"},
				{ID: "e5", Source: "disabled_mid", SourceHandle: HandleNext, Target: "join"},
				{ID: "e6", Source: "join", SourceHandle: HandleNext, Target: "failing"},
				{ID: "e7", Source: "failing", SourceHandle: HandleError, Target: "recovered"},
			},
		},
	}

	saved, err := st.SaveFlow(ctx, flowDef)
	if err != nil {
		t.Fatalf("SaveFlow: %v", err)
	}
	eng := NewEngine(EngineOptions{Store: st, Actions: actions, Now: clk.Now})
	if err := eng.ReloadFlows(ctx); err != nil {
		t.Fatalf("ReloadFlows: %v", err)
	}

	// Low severity event must be pre-filtered before creating any run!
	lowEv := newTestEvent("evt_low", "system.disk.low", events.Correlation{}, map[string]any{"severity": "low"})
	if created, _, err := eng.ingestEvent(ctx, lowEv, 0); err != nil || created != 0 {
		t.Fatalf("expected 0 runs for filtered-out event, got created=%d err=%v", created, err)
	}
	runs, _, _ := st.ListRuns(ctx, RunFilter{FlowID: saved.ID, Limit: 10})
	if len(runs) != 0 {
		t.Fatalf("expected 0 persisted runs for filtered-out event, got %d", len(runs))
	}

	// High severity event runs full diamond DAG + panic error-handle recovery.
	highEv := newTestEvent("evt_high", "system.disk.low", events.Correlation{}, map[string]any{"severity": "high", "code": 503, "notify": true})
	if created, _, err := eng.ingestEvent(ctx, highEv, 0); err != nil || created != 1 {
		t.Fatalf("ingestEvent high: created=%d err=%v", created, err)
	}
	if err := eng.ProcessPending(ctx); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}

	runID := EventRunID(saved.ID, "trig", highEv.ID)
	run, err := st.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != RunCompleted {
		t.Fatalf("expected completed run after error-handle recovery, got %s (err=%s)", run.Status, run.Error)
	}

	// Verify diamond join ran once (not twice) and saw var + step output, and recovered ran after panic.
	wantExec := []string{"a1:branch1", "join:joined:hi-from-var:v1", "recovered:recovered"}
	if fmt.Sprint(executed) != fmt.Sprint(wantExec) {
		t.Fatalf("executed actions mismatch:\n got: %v\nwant: %v", executed, wantExec)
	}
}

func TestRuntimeDelayAndWaitEventResumeAndTimeout(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)
	actions := NewActionRegistry()

	matchedAct := newFakeAction("act.matched", true, nil)
	timeoutAct := newFakeAction("act.timeout", true, nil)
	afterDelayAct := newFakeAction("act.after_delay", true, nil)
	_ = actions.Register(matchedAct)
	_ = actions.Register(timeoutAct)
	_ = actions.Register(afterDelayAct)

	// Flow: trigger -> delay 5m -> after_delay -> wait_event (resumed|stopped, matchCorrelation=true, timeout=10m)
	//       -> matched -> act.matched
	//       -> timeout -> act.timeout
	flowDef := Flow{
		Name:    "Idle Pause Terminator",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerEvent, Config: mustRawJSON(t, EventTriggerConfig{Events: []string{"playback.session.paused"}})},
				{ID: "d", Kind: NodeLogicDelay, Config: mustRawJSON(t, DelayNodeConfig{Duration: "5m"})},
				{ID: "ad", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.after_delay"})},
				{
					ID:   "w",
					Kind: NodeLogicWaitEvent,
					Config: mustRawJSON(t, WaitEventNodeConfig{
						EventTypes:       []string{"playback.session.resumed", "playback.session.stopped"},
						MatchCorrelation: true,
						Timeout:          "10m",
					}),
				},
				{ID: "am", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.matched", Params: map[string]any{"message": "{{ steps.w.event.type }}"}})},
				{ID: "at", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.timeout"})},
			},
			Edges: []Edge{
				{ID: "e1", Source: "t", SourceHandle: HandleNext, Target: "d"},
				{ID: "e2", Source: "d", SourceHandle: HandleNext, Target: "ad"},
				{ID: "e3", Source: "ad", SourceHandle: HandleNext, Target: "w"},
				{ID: "e4", Source: "w", SourceHandle: HandleMatched, Target: "am"},
				{ID: "e5", Source: "w", SourceHandle: HandleTimeout, Target: "at"},
			},
		},
	}

	saved, err := st.SaveFlow(ctx, flowDef)
	if err != nil {
		t.Fatalf("SaveFlow: %v", err)
	}
	eng := NewEngine(EngineOptions{Store: st, Actions: actions, Now: clk.Now})
	_ = eng.ReloadFlows(ctx)

	// Trigger two sessions: sess-match (will resume) and sess-timeout (will time out).
	ev1 := newTestEvent("evt_p1", "playback.session.paused", events.Correlation{SessionID: "sess-match"}, map[string]any{})
	ev2 := newTestEvent("evt_p2", "playback.session.paused", events.Correlation{SessionID: "sess-timeout"}, map[string]any{})
	_, _, _ = eng.ingestEvent(ctx, ev1, 0)
	_, _, _ = eng.ingestEvent(ctx, ev2, 0)
	_ = eng.ProcessPending(ctx)

	// Both runs should be suspended in RunWaiting at the 5m delay.
	r1ID := EventRunID(saved.ID, "t", ev1.ID)
	r2ID := EventRunID(saved.ID, "t", ev2.ID)
	r1, _ := st.GetRun(ctx, r1ID)
	if r1.Status != RunWaiting || r1.WakeAt.IsZero() {
		t.Fatalf("expected run1 waiting on delay, got status=%s wakeAt=%v", r1.Status, r1.WakeAt)
	}
	if len(afterDelayAct.Calls()) != 0 {
		t.Fatalf("after_delay should not run before delay expires")
	}

	// Advance clock by 5m -> both resume past delay, execute after_delay, and suspend on wait_event.
	clk.Advance(5 * time.Minute)
	_ = eng.ProcessPending(ctx)

	if len(afterDelayAct.Calls()) != 2 {
		t.Fatalf("expected after_delay to run twice after 5m delay, got %d", len(afterDelayAct.Calls()))
	}
	r1, _ = st.GetRun(ctx, r1ID)
	r2, _ := st.GetRun(ctx, r2ID)
	if r1.Status != RunWaiting || r2.Status != RunWaiting {
		t.Fatalf("expected both runs waiting on wait_event, got r1=%s r2=%s", r1.Status, r2.Status)
	}

	// SubscribedEvents should include waiter event patterns.
	subs, err := eng.SubscribedEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribedEvents: %v", err)
	}
	if len(subs) < 3 {
		t.Fatalf("expected trigger + waiter event patterns, got %v", subs)
	}

	// Send matching resume event for sess-match only.
	resumedEv := newTestEvent("evt_res1", "playback.session.resumed", events.Correlation{SessionID: "sess-match"}, map[string]any{})
	_, matchedWaiters, err := eng.ingestEvent(ctx, resumedEv, 0)
	if err != nil || matchedWaiters != 1 {
		t.Fatalf("expected 1 waiter matched, got %d err=%v", matchedWaiters, err)
	}
	_ = eng.ProcessPending(ctx)

	if len(matchedAct.Calls()) != 1 || matchedAct.Calls()[0].Params["message"] != "playback.session.resumed" {
		t.Fatalf("expected act.matched with matched event type, got %+v", matchedAct.Calls())
	}
	if len(timeoutAct.Calls()) != 0 {
		t.Fatalf("act.timeout should not have fired yet")
	}

	// Advance clock 10m -> sess-timeout waiter expires and routes to timeout branch.
	clk.Advance(10 * time.Minute)
	_ = eng.ProcessPending(ctx)

	if len(timeoutAct.Calls()) != 1 {
		t.Fatalf("expected act.timeout to fire once after timeout, got %d", len(timeoutAct.Calls()))
	}
	r1, _ = st.GetRun(ctx, r1ID)
	r2, _ = st.GetRun(ctx, r2ID)
	if r1.Status != RunCompleted || r2.Status != RunCompleted {
		t.Fatalf("expected both runs completed, got r1=%s r2=%s", r1.Status, r2.Status)
	}
}

func TestRuntimeCrashRecoveryUncertainVsIdempotent(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)
	actions := NewActionRegistry()

	nonIdemAct := newFakeAction("act.non_idem", false, nil)
	idemAct := newFakeAction("act.idem", true, nil)
	errHandlerAct := newFakeAction("act.err_handler", true, nil)
	_ = actions.Register(nonIdemAct)
	_ = actions.Register(idemAct)
	_ = actions.Register(errHandlerAct)

	// 1. Non-idempotent flow WITHOUT error edge -> crash recovery must mark StepUncertain & fail run without re-executing.
	f1, _ := st.SaveFlow(ctx, Flow{
		Name:    "Crash Non-Idempotent Fail",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerManual, Config: mustRawJSON(t, ManualTriggerConfig{})},
				{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.non_idem"})},
			},
			Edges: []Edge{{ID: "e1", Source: "t", Target: "a"}},
		},
	})
	// 2. Non-idempotent flow WITH error edge -> crash recovery marks StepUncertain and routes to error edge!
	f2, _ := st.SaveFlow(ctx, Flow{
		Name:    "Crash Non-Idempotent Error Route",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerManual, Config: mustRawJSON(t, ManualTriggerConfig{})},
				{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.non_idem"})},
				{ID: "eh", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.err_handler"})},
			},
			Edges: []Edge{
				{ID: "e1", Source: "t", Target: "a"},
				{ID: "e2", Source: "a", SourceHandle: HandleError, Target: "eh"},
			},
		},
	})
	// 3. Idempotent flow -> crash recovery retries with the EXACT SAME IdempotencyKey!
	f3, _ := st.SaveFlow(ctx, Flow{
		Name:    "Crash Idempotent Retry",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerManual, Config: mustRawJSON(t, ManualTriggerConfig{})},
				{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.idem"})},
			},
			Edges: []Edge{{ID: "e1", Source: "t", Target: "a"}},
		},
	})

	// Simulate crashed leader leaving all three runs in RunRunning with expired lease and action step in StepRunning.
	seedCrashedRun := func(runID string, f *Flow, origKey string) {
		ctxRaw, _ := encodeRunState(runState{
			Queue:   []string{"a"},
			Visited: map[string]bool{"t": true},
			NextSeq: 3,
		})
		_ = st.CreateRun(ctx, Run{
			ID:          runID,
			FlowID:      f.ID,
			FlowVersion: 1,
			TriggerKind: NodeTriggerManual,
			Status:      RunRunning,
			Attempt:     1,
			Context:     ctxRaw,
			StepCount:   1,
			StartedAt:   clk.Now().Add(-2 * time.Minute),
			UpdatedAt:   clk.Now().Add(-2 * time.Minute),
			LeaseOwner:  "dead-leader",
			LeaseUntil:  clk.Now().Add(-time.Minute), // expired!
		})
		_, _ = st.RecordStep(ctx, RunStep{
			RunID:      runID,
			Seq:        1,
			NodeID:     "t",
			NodeKind:   NodeTriggerManual,
			Status:     StepCompleted,
			StartedAt:  clk.Now().Add(-2 * time.Minute),
			FinishedAt: clk.Now().Add(-2 * time.Minute),
		})
		_, _ = st.RecordStep(ctx, RunStep{
			RunID:          runID,
			Seq:            2,
			NodeID:         "a",
			NodeKind:       NodeAction,
			Status:         StepRunning,
			IdempotencyKey: origKey,
			StartedAt:      clk.Now().Add(-2 * time.Minute),
		})
	}

	origIdemKey := IdempotencyKey("run_crash_3", "a", 1)
	seedCrashedRun("run_crash_1", f1, IdempotencyKey("run_crash_1", "a", 1))
	seedCrashedRun("run_crash_2", f2, IdempotencyKey("run_crash_2", "a", 1))
	seedCrashedRun("run_crash_3", f3, origIdemKey)

	eng := NewEngine(EngineOptions{Store: st, Actions: actions, NodeID: "new-leader", Now: clk.Now})
	if err := eng.ProcessPending(ctx); err != nil {
		t.Fatalf("ProcessPending crash recovery: %v", err)
	}

	// Non-idempotent action must NEVER have been re-executed!
	if len(nonIdemAct.Calls()) != 0 {
		t.Fatalf("non-idempotent action was re-executed after ambiguous crash: %d calls", len(nonIdemAct.Calls()))
	}

	// Flow 1: failed with StepUncertain.
	r1, _ := st.GetRun(ctx, "run_crash_1")
	if r1.Status != RunFailed || r1.Attempt != 2 {
		t.Fatalf("expected run_crash_1 failed with attempt=2, got status=%s attempt=%d", r1.Status, r1.Attempt)
	}
	steps1, _ := st.ListRunSteps(ctx, "run_crash_1")
	if steps1[1].Status != StepUncertain {
		t.Fatalf("expected step status uncertain, got %s", steps1[1].Status)
	}

	// Flow 2: StepUncertain routed to error handler and completed.
	r2, _ := st.GetRun(ctx, "run_crash_2")
	if r2.Status != RunCompleted {
		t.Fatalf("expected run_crash_2 completed via error edge, got %s (err=%s)", r2.Status, r2.Error)
	}
	if len(errHandlerAct.Calls()) != 1 {
		t.Fatalf("expected error handler called once, got %d", len(errHandlerAct.Calls()))
	}
	steps2, _ := st.ListRunSteps(ctx, "run_crash_2")
	if steps2[1].Status != StepUncertain || steps2[1].Branch != HandleError {
		t.Fatalf("expected step2 uncertain on error branch, got status=%s branch=%s", steps2[1].Status, steps2[1].Branch)
	}

	// Flow 3: Idempotent action retried ONCE with the exact original IdempotencyKey!
	r3, _ := st.GetRun(ctx, "run_crash_3")
	if r3.Status != RunCompleted || r3.Attempt != 2 {
		t.Fatalf("expected run_crash_3 completed with attempt=2, got status=%s attempt=%d", r3.Status, r3.Attempt)
	}
	calls3 := idemAct.Calls()
	if len(calls3) != 1 || calls3[0].Actx.IdempotencyKey != origIdemKey {
		t.Fatalf("expected idempotent retry with original key %q, got %+v", origIdemKey, calls3)
	}
}

func TestRuntimeScheduleCoalescingWindowAndDedupe(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)
	actions := NewActionRegistry()

	schedAct := newFakeAction("act.sched", true, nil)
	winAct := newFakeAction("act.win_reached", true, nil)
	dedAct := newFakeAction("act.ded_pass", true, nil)
	_ = actions.Register(schedAct)
	_ = actions.Register(winAct)
	_ = actions.Register(dedAct)

	// Scheduled flow every 10m.
	_, err := st.SaveFlow(ctx, Flow{
		Name:    "Scheduled Job",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "s", Kind: NodeTriggerSchedule, Config: mustRawJSON(t, ScheduleTriggerConfig{Every: "10m"})},
				{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.sched"})},
			},
			Edges: []Edge{{ID: "e1", Source: "s", Target: "a"}},
		},
	})
	if err != nil {
		t.Fatalf("SaveFlow schedule: %v", err)
	}

	// Window + Dedupe flow.
	_, err = st.SaveFlow(ctx, Flow{
		Name:    "Window and Dedupe",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerEvent, Config: mustRawJSON(t, EventTriggerConfig{Events: []string{"livetv.input.stalled"}})},
				{ID: "win", Kind: NodeLogicWindow, Config: mustRawJSON(t, WindowNodeConfig{KeyTemplate: "{{ payload.channel }}", Threshold: 3, Window: "5m", ResetOnReach: true})},
				{ID: "ded", Kind: NodeLogicDedupe, Config: mustRawJSON(t, DedupeNodeConfig{KeyTemplate: "{{ payload.channel }}", Cooldown: "10m", MaxPerWindow: 1})},
				{ID: "a_win", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.win_reached"})},
				{ID: "a_ded", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.ded_pass"})},
			},
			Edges: []Edge{
				{ID: "e1", Source: "t", Target: "win"},
				{ID: "e2", Source: "win", SourceHandle: HandleReached, Target: "a_win"},
				{ID: "e3", Source: "a_win", Target: "ded"},
				{ID: "e4", Source: "ded", SourceHandle: HandlePass, Target: "a_ded"},
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveFlow window/dedupe: %v", err)
	}

	eng := NewEngine(EngineOptions{Store: st, Actions: actions, Now: clk.Now})
	_ = eng.ReloadFlows(ctx)

	// Jump clock ahead 2 hours (12 missed 10m intervals) -> wall-clock coalescing fires ONCE, not 12 times.
	clk.Advance(2 * time.Hour)
	_ = eng.ProcessPending(ctx)
	if len(schedAct.Calls()) != 1 {
		t.Fatalf("expected 1 coalesced schedule run after 2h jump, got %d", len(schedAct.Calls()))
	}
	// Running ProcessPending again at the same timestamp must not fire again.
	_ = eng.ProcessPending(ctx)
	if len(schedAct.Calls()) != 1 {
		t.Fatalf("expected schedule not to double-fire, got %d", len(schedAct.Calls()))
	}

	// Send 6 stall events in rapid succession:
	// - events 1,2 -> below; event 3 -> reached (a_win #1) -> dedupe pass (a_ded #1)
	// - events 4,5 -> below; event 6 -> reached (a_win #2) -> dedupe suppressed (a_ded still 1!)
	for i := range 6 {
		ev := newTestEvent(fmt.Sprintf("stall_%d", i), "livetv.input.stalled", events.Correlation{}, map[string]any{"channel": "ch1"})
		_, _, _ = eng.ingestEvent(ctx, ev, 0)
		_ = eng.ProcessPending(ctx)
	}
	if len(winAct.Calls()) != 2 {
		t.Fatalf("expected window reached twice across 6 events (threshold 3 + reset), got %d", len(winAct.Calls()))
	}
	if len(dedAct.Calls()) != 1 {
		t.Fatalf("expected dedupe to suppress second reached alert within 10m cooldown, got %d", len(dedAct.Calls()))
	}
}

func TestRuntimeRateLimitLoopDepthBackoffAndAutoDisable(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)
	bus := events.NewBus(events.BusOptions{})
	rec := events.NewRecorder(bus, "flows.*")
	defer rec.Close()

	actions := NewActionRegistry()
	failAct := newFakeAction("act.always_fail", false, func(_ context.Context, _ ActionContext, _ map[string]any) (ActionResult, error) {
		return ActionResult{}, errors.New("downstream unreachable")
	})
	ownerAlertAct := newFakeAction("act.owner_alert", true, nil)
	_ = actions.Register(failAct)
	_ = actions.Register(ownerAlertAct)

	// Flow A: fails every time, RateLimitPerMin=20, AutoDisableAfter=7 (so we see backoff at 5 & 6, then auto-disable at 7).
	flaky, err := st.SaveFlow(ctx, Flow{
		Name:    "Flaky Flow",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerEvent, Config: mustRawJSON(t, EventTriggerConfig{Events: []string{"test.flaky"}})},
				{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.always_fail"})},
			},
			Edges: []Edge{{ID: "e1", Source: "t", Target: "a"}},
		},
		Limits: FlowLimits{RateLimitPerMin: 20, AutoDisableAfter: 7},
	})
	if err != nil {
		t.Fatalf("SaveFlow flaky: %v", err)
	}

	// Flow B: owner notification flow triggered by flows.flow.auto_disabled!
	_, err = st.SaveFlow(ctx, Flow{
		Name:    "Owner Auto-Disable Notifier",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerEvent, Config: mustRawJSON(t, EventTriggerConfig{Events: []string{"flows.flow.auto_disabled"}})},
				{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.owner_alert", Params: map[string]any{"message": "Disabled: {{ payload.flowName }}"}})},
			},
			Edges: []Edge{{ID: "e1", Source: "t", Target: "a"}},
		},
	})
	if err != nil {
		t.Fatalf("SaveFlow owner alert: %v", err)
	}

	// Flow C: low rate limit (2/min) + recursion depth test.
	rateAct := newFakeAction("act.rate", true, nil)
	_ = actions.Register(rateAct)
	rateFlow, _ := st.SaveFlow(ctx, Flow{
		Name:    "Rate Limited Flow",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerEvent, Config: mustRawJSON(t, EventTriggerConfig{Events: []string{"test.rate"}})},
				{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.rate"})},
			},
			Edges: []Edge{{ID: "e1", Source: "t", Target: "a"}},
		},
		Limits: FlowLimits{RateLimitPerMin: 2},
	})

	var autoDisabledCount atomic.Int32
	eng := NewEngine(EngineOptions{
		Store:            st,
		Bus:              bus,
		Actions:          actions,
		Now:              clk.Now,
		BackoffThreshold: 5,
		BackoffBase:      5 * time.Second,
		BackoffMax:       15 * time.Minute,
		OnAutoDisable: func(f Flow, r Run) {
			autoDisabledCount.Add(1)
		},
	})
	_ = eng.ReloadFlows(ctx)

	// 1. Loop / depth cap: event with FlowDepth == MaxFlowDepth (3) must NOT trigger any run.
	deepEv := newTestEvent("evt_deep", "test.rate", events.Correlation{}, map[string]any{})
	deepEv.Meta.FlowDepth = MaxFlowDepth
	if created, _, err := eng.ingestEvent(ctx, deepEv, 0); err != nil || created != 0 {
		t.Fatalf("expected depth-capped event to create 0 runs, got %d err=%v", created, err)
	}

	// 2. Per-flow rate limit (2/min): 3rd event within 1m is throttled, 4th after +61s succeeds.
	for i := range 3 {
		ev := newTestEvent(fmt.Sprintf("rate_%d", i), "test.rate", events.Correlation{}, map[string]any{})
		_, _, _ = eng.ingestEvent(ctx, ev, 0)
	}
	_ = eng.ProcessPending(ctx)
	if len(rateAct.Calls()) != 2 {
		t.Fatalf("expected 2 rate-limited runs executed, got %d", len(rateAct.Calls()))
	}
	throttledRuns, _, _ := st.ListRuns(ctx, RunFilter{FlowID: rateFlow.ID, Status: RunThrottled, Limit: 10})
	if len(throttledRuns) == 0 {
		t.Fatalf("expected at least 1 sampled RunThrottled record")
	}
	clk.Advance(61 * time.Second)
	_, _, _ = eng.ingestEvent(ctx, newTestEvent("rate_after", "test.rate", events.Correlation{}, map[string]any{}), 0)
	_ = eng.ProcessPending(ctx)
	if len(rateAct.Calls()) != 3 {
		t.Fatalf("expected 3rd run after rate window reset, got %d", len(rateAct.Calls()))
	}

	// 3. Failure backoff (starts at 5th failure -> 5s, 6th -> 10s) and auto-disable at 7th failure.
	for i := 1; i <= 5; i++ {
		_, _, _ = eng.ingestEvent(ctx, newTestEvent(fmt.Sprintf("flaky_%d", i), "test.flaky", events.Correlation{}, map[string]any{}), 0)
		_ = eng.ProcessPending(ctx)
	}
	if len(failAct.Calls()) != 5 {
		t.Fatalf("expected 5 failure attempts, got %d", len(failAct.Calls()))
	}

	// 2 seconds later: still within 5s backoff -> must NOT run!
	clk.Advance(2 * time.Second)
	_, _, _ = eng.ingestEvent(ctx, newTestEvent("flaky_backoff_blocked", "test.flaky", events.Correlation{}, map[string]any{}), 0)
	_ = eng.ProcessPending(ctx)
	if len(failAct.Calls()) != 5 {
		t.Fatalf("expected backoff to suppress 6th failure at +2s, got %d calls", len(failAct.Calls()))
	}

	// Advance past 5s backoff -> 6th failure executes (next backoff = 10s).
	clk.Advance(4 * time.Second)
	_, _, _ = eng.ingestEvent(ctx, newTestEvent("flaky_6", "test.flaky", events.Correlation{}, map[string]any{}), 0)
	_ = eng.ProcessPending(ctx)
	if len(failAct.Calls()) != 6 {
		t.Fatalf("expected 6th failure after backoff expired, got %d", len(failAct.Calls()))
	}

	// 5 seconds later: still inside 10s backoff -> blocked!
	clk.Advance(5 * time.Second)
	_, _, _ = eng.ingestEvent(ctx, newTestEvent("flaky_backoff2_blocked", "test.flaky", events.Correlation{}, map[string]any{}), 0)
	_ = eng.ProcessPending(ctx)
	if len(failAct.Calls()) != 6 {
		t.Fatalf("expected 10s backoff to suppress 7th failure at +5s, got %d", len(failAct.Calls()))
	}

	// Advance past 10s backoff -> 7th failure trips AutoDisableAfter!
	clk.Advance(6 * time.Second)
	_, _, _ = eng.ingestEvent(ctx, newTestEvent("flaky_7", "test.flaky", events.Correlation{}, map[string]any{}), 0)
	_ = eng.ProcessPending(ctx)

	if autoDisabledCount.Load() != 1 {
		t.Fatalf("expected OnAutoDisable called once, got %d", autoDisabledCount.Load())
	}
	reloaded, err := st.GetFlow(ctx, flaky.ID)
	if err != nil {
		t.Fatalf("GetFlow flaky: %v", err)
	}
	if reloaded.Enabled || reloaded.DisabledReason == "" || reloaded.ConsecutiveFailures != 7 {
		t.Fatalf("expected flow auto-disabled after 7 failures, got enabled=%v reason=%q streak=%d",
			reloaded.Enabled, reloaded.DisabledReason, reloaded.ConsecutiveFailures)
	}

	// Verify durable flows.flow.auto_disabled event was published onto the bus!
	autoEv := rec.WaitFor(t, "flows.flow.auto_disabled", 2*time.Second)
	if !autoEv.Meta.Durable {
		t.Fatalf("expected flows.flow.auto_disabled to have Meta.Durable=true")
	}

	// Ingesting that owner notification event triggers Flow B (Owner Auto-Disable Notifier).
	_, _, _ = eng.ingestEvent(ctx, autoEv, 0)
	_ = eng.ProcessPending(ctx)
	if len(ownerAlertAct.Calls()) != 1 || ownerAlertAct.Calls()[0].Params["message"] != "Disabled: Flaky Flow" {
		t.Fatalf("expected owner notification flow to execute for auto-disabled flow, got %+v", ownerAlertAct.Calls())
	}
}

func TestRuntimeTestRunManualWebhookAndCancel(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)
	evReg := events.NewRegistry()
	if err := evReg.Register(events.EventSchema{
		Type:    "library.item.added",
		Domain:  "library",
		Label:   "Item added",
		Version: 1,
		Sample:  map[string]any{"title": "Sample Movie"},
	}); err != nil {
		t.Fatalf("evReg.Register: %v", err)
	}

	actions := NewActionRegistry()
	actStarted := make(chan struct{}, 1)
	actCancelled := make(chan struct{}, 1)
	testAct := newFakeAction("act.echo", true, nil)
	blockingAct := newFakeAction("act.block", false, func(actCtx context.Context, _ ActionContext, _ map[string]any) (ActionResult, error) {
		actStarted <- struct{}{}
		<-actCtx.Done()
		actCancelled <- struct{}{}
		return ActionResult{}, actCtx.Err()
	})
	_ = actions.Register(testAct)
	_ = actions.Register(blockingAct)

	eng := NewEngine(EngineOptions{
		Store:   st,
		Events:  evReg,
		Actions: actions,
		Now:     clk.Now,
	})

	// 1. TestRun (dry) on unsaved draft graph with delay + auto-generated sample event -> zero DB writes, DryRun=true.
	draftGraph := Graph{
		SchemaVersion: SchemaVersion,
		Nodes: []Node{
			{ID: "t", Kind: NodeTriggerEvent, Config: mustRawJSON(t, EventTriggerConfig{Events: []string{"library.item.added"}})},
			{ID: "d", Kind: NodeLogicDelay, Config: mustRawJSON(t, DelayNodeConfig{Duration: "1h"})},
			{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.echo", Params: map[string]any{"message": "Added {{ payload.title }}"}})},
		},
		Edges: []Edge{
			{ID: "e1", Source: "t", Target: "d"},
			{ID: "e2", Source: "d", Target: "a"},
		},
	}
	dryRes, err := eng.TestRun(ctx, TestRunRequest{Graph: &draftGraph, DryRun: true})
	if err != nil {
		t.Fatalf("TestRun dry draft: %v", err)
	}
	if dryRes.Run.Status != RunCompleted || !dryRes.Run.DryRun || len(dryRes.Steps) != 3 {
		t.Fatalf("unexpected dry TestRun result: status=%s dry=%v steps=%d", dryRes.Run.Status, dryRes.Run.DryRun, len(dryRes.Steps))
	}
	if len(testAct.Calls()) != 1 || !testAct.Calls()[0].Actx.DryRun || testAct.Calls()[0].Params["message"] != "Added Sample Movie" {
		t.Fatalf("unexpected dry action call: %+v", testAct.Calls())
	}
	if allRuns, _, _ := st.ListRuns(ctx, RunFilter{Limit: 10}); len(allRuns) != 0 {
		t.Fatalf("dry TestRun must not persist runs, found %d", len(allRuns))
	}

	// 1b. TestRun with event type only (empty payload) fills sample payload from schema; explicit {} stays empty.
	typeOnlyRes, err := eng.TestRun(ctx, TestRunRequest{
		Graph:  &draftGraph,
		DryRun: true,
		Event:  events.Envelope{Type: "library.item.added"},
	})
	if err != nil {
		t.Fatalf("TestRun type-only event: %v", err)
	}
	if typeOnlyRes.Run.Status != RunCompleted || len(testAct.Calls()) != 2 || testAct.Calls()[1].Params["message"] != "Added Sample Movie" {
		t.Fatalf("expected schema sample payload for type-only test event, got %+v", testAct.Calls())
	}
	emptyPayloadRes, err := eng.TestRun(ctx, TestRunRequest{
		Graph:  &draftGraph,
		DryRun: true,
		Event:  events.Envelope{Type: "library.item.added", Payload: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatalf("TestRun explicit empty payload: %v", err)
	}
	if emptyPayloadRes.Run.Status != RunCompleted || len(testAct.Calls()) != 3 || testAct.Calls()[2].Params["message"] != "Added " {
		t.Fatalf("expected explicit empty payload preserved, got %+v", testAct.Calls())
	}

	// 2. TestRun (live) on saved flow -> DryRun=false, persists run & steps, simulates delay synchronously.
	savedFlow, err := st.SaveFlow(ctx, Flow{Name: "Saved Testable", Enabled: true, Graph: draftGraph})
	if err != nil {
		t.Fatalf("SaveFlow: %v", err)
	}
	liveRes, err := eng.TestRun(ctx, TestRunRequest{FlowID: savedFlow.ID, DryRun: false})
	if err != nil {
		t.Fatalf("TestRun live: %v", err)
	}
	if liveRes.Run.Status != RunCompleted || liveRes.Run.DryRun {
		t.Fatalf("unexpected live TestRun: status=%s dry=%v", liveRes.Run.Status, liveRes.Run.DryRun)
	}
	if len(testAct.Calls()) != 4 || testAct.Calls()[3].Actx.DryRun {
		t.Fatalf("expected live TestRun action with DryRun=false, got %+v", testAct.Calls())
	}
	if _, err := st.GetRun(ctx, liveRes.Run.ID); err != nil {
		t.Fatalf("expected live TestRun persisted in DB: %v", err)
	}

	// 3. TriggerManual (inputs + defaults) + TriggerWebhook (token verification).
	manualAndHookFlow, _ := st.SaveFlow(ctx, Flow{
		Name:    "Manual and Webhook",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "tm", Kind: NodeTriggerManual, Config: mustRawJSON(t, ManualTriggerConfig{
					Inputs: []ParamSchema{
						{Key: "target", Type: events.FieldString, Default: "all"},
						{Key: "note", Type: events.FieldString, Default: "none"},
					},
				})},
				{ID: "tw", Kind: NodeTriggerWebhook, Config: mustRawJSON(t, WebhookTriggerConfig{})},
				{ID: "am", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.echo", Params: map[string]any{"message": "manual:{{ trigger.inputs.target }}:{{ trigger.inputs.note }}"}})},
				{ID: "aw", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.echo", Params: map[string]any{"message": "hook:{{ trigger.payload.action }}"}})},
			},
			Edges: []Edge{
				{ID: "e1", Source: "tm", Target: "am"},
				{ID: "e2", Source: "tw", Target: "aw"},
			},
		},
	})
	if err := st.SetWebhookTokenHash(ctx, manualAndHookFlow.ID, HashWebhookToken("tok-abc")); err != nil {
		t.Fatalf("SetWebhookTokenHash: %v", err)
	}

	mRun, err := eng.TriggerManual(ctx, manualAndHookFlow.ID, map[string]any{"note": "deploy"})
	if err != nil {
		t.Fatalf("TriggerManual: %v", err)
	}
	if _, err := eng.TriggerWebhook(ctx, "wrong-token", nil, nil); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("expected ErrInvalidWebhook, got %v", err)
	}
	wRun, err := eng.TriggerWebhook(ctx, "tok-abc", map[string]any{"action": "refresh"}, map[string]string{"X-Test": "1"})
	if err != nil {
		t.Fatalf("TriggerWebhook: %v", err)
	}
	_ = eng.ProcessPending(ctx)

	mGot, _ := st.GetRun(ctx, mRun.ID)
	wGot, _ := st.GetRun(ctx, wRun.ID)
	if mGot.Status != RunCompleted || wGot.Status != RunCompleted {
		t.Fatalf("expected manual & webhook runs completed, got manual=%s webhook=%s", mGot.Status, wGot.Status)
	}
	calls := testAct.Calls()
	msgByRun := map[string]any{}
	for _, c := range calls {
		msgByRun[c.Actx.RunID] = c.Params["message"]
	}
	if msgByRun[mRun.ID] != "manual:all:deploy" || msgByRun[wRun.ID] != "hook:refresh" {
		t.Fatalf("unexpected manual/webhook messages: manual=%v webhook=%v", msgByRun[mRun.ID], msgByRun[wRun.ID])
	}

	// 4. CancelRun interrupts an active in-flight action cleanly.
	blockFlow, _ := st.SaveFlow(ctx, Flow{
		Name:    "Cancellable",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "tm", Kind: NodeTriggerManual, Config: mustRawJSON(t, ManualTriggerConfig{})},
				{ID: "ab", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.block"})},
			},
			Edges: []Edge{{ID: "e1", Source: "tm", Target: "ab"}},
		},
	})
	bRun, err := eng.TriggerManual(ctx, blockFlow.ID, nil)
	if err != nil {
		t.Fatalf("TriggerManual blockFlow: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.ProcessPending(ctx)
	}()

	select {
	case <-actStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for blocking action to start")
	}
	if err := eng.CancelRun(ctx, bRun.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	select {
	case <-actCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for blocking action context cancellation")
	}
	<-done
	bGot, _ := st.GetRun(ctx, bRun.ID)
	if bGot.Status != RunCancelled {
		t.Fatalf("expected cancelled status, got %s", bGot.Status)
	}
}

func TestRuntimeGuardrailsWaitFilterDelayModesAndNonBlockingPublish(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)
	actions := NewActionRegistry()

	okAct := newFakeAction("act.ok_g", true, nil)
	failAct := newFakeAction("act.fail_g", true, func(_ context.Context, _ ActionContext, _ map[string]any) (ActionResult, error) {
		return ActionResult{}, errors.New("non-fatal failure")
	})
	_ = actions.Register(okAct)
	_ = actions.Register(failAct)

	// 1. Wait-event with Filter + Delay via durationField + ContinueOnError.
	f1, err := st.SaveFlow(ctx, Flow{
		Name:    "Wait Filter and ContinueOnError",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerEvent, Config: mustRawJSON(t, EventTriggerConfig{Events: []string{"job.started"}})},
				{ID: "d", Kind: NodeLogicDelay, Config: mustRawJSON(t, DelayNodeConfig{DurationField: "payload.wait"})},
				{
					ID:   "w",
					Kind: NodeLogicWaitEvent,
					Config: mustRawJSON(t, WaitEventNodeConfig{
						EventTypes:       []string{"job.progress"},
						MatchCorrelation: true,
						Filter: &ConditionGroup{
							Op:    LogicAnd,
							Rules: []ConditionRule{{Field: "payload.percent", Op: OpGte, Value: 100}},
						},
						Timeout: "1h",
					}),
				},
				{ID: "af", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.fail_g", ContinueOnError: true})},
				{ID: "ao", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.ok_g", Params: map[string]any{"message": "after-continue"}})},
			},
			Edges: []Edge{
				{ID: "e1", Source: "t", Target: "d"},
				{ID: "e2", Source: "d", Target: "w"},
				{ID: "e3", Source: "w", SourceHandle: HandleMatched, Target: "af"},
				{ID: "e4", Source: "af", SourceHandle: HandleNext, Target: "ao"},
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveFlow f1: %v", err)
	}

	// 2. MaxSteps guardrail test.
	fMaxSteps, err := st.SaveFlow(ctx, Flow{
		Name:    "MaxSteps Guard",
		Enabled: true,
		Limits:  FlowLimits{MaxSteps: 2},
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerManual, Config: mustRawJSON(t, ManualTriggerConfig{})},
				{ID: "a1", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.ok_g"})},
				{ID: "a2", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.ok_g"})},
			},
			Edges: []Edge{
				{ID: "e1", Source: "t", Target: "a1"},
				{ID: "e2", Source: "a1", Target: "a2"},
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveFlow fMaxSteps: %v", err)
	}

	eng := NewEngine(EngineOptions{Store: st, Actions: actions, Now: clk.Now})
	_ = eng.ReloadFlows(ctx)

	startEv := newTestEvent("evt_job_1", "job.started", events.Correlation{Key: "job:7"}, map[string]any{"wait": "2m"})
	_, _, _ = eng.ingestEvent(ctx, startEv, 0)
	_ = eng.ProcessPending(ctx)

	rID := EventRunID(f1.ID, "t", startEv.ID)
	r, _ := st.GetRun(ctx, rID)
	if r.Status != RunWaiting || !r.WakeAt.Equal(clk.Now().Add(2*time.Minute)) {
		t.Fatalf("expected delay via durationField of 2m, got status=%s wakeAt=%v", r.Status, r.WakeAt)
	}

	clk.Advance(2 * time.Minute)
	_ = eng.ProcessPending(ctx)

	// Progress=50 fails the waiter filter -> must NOT wake the run!
	p50 := newTestEvent("evt_prog_50", "job.progress", events.Correlation{Key: "job:7"}, map[string]any{"percent": 50})
	if _, matched, _ := eng.ingestEvent(ctx, p50, 0); matched != 0 {
		t.Fatalf("expected waiter filter to reject percent=50, got matched=%d", matched)
	}
	// Progress=100 passes the waiter filter -> wakes run, failAct fails with ContinueOnError, okAct executes!
	p100 := newTestEvent("evt_prog_100", "job.progress", events.Correlation{Key: "job:7"}, map[string]any{"percent": 100})
	if _, matched, _ := eng.ingestEvent(ctx, p100, 0); matched != 1 {
		t.Fatalf("expected waiter filter to accept percent=100, got matched=%d", matched)
	}
	_ = eng.ProcessPending(ctx)

	r, _ = st.GetRun(ctx, rID)
	if r.Status != RunCompleted {
		t.Fatalf("expected run completed after ContinueOnError, got %s (err=%s)", r.Status, r.Error)
	}
	if len(okAct.Calls()) != 1 || okAct.Calls()[0].Params["message"] != "after-continue" {
		t.Fatalf("expected okAct after ContinueOnError, got %+v", okAct.Calls())
	}

	// MaxSteps=2: trigger (step 1) + a1 (step 2) -> a2 exceeds limit and fails run cleanly.
	msRun, err := eng.TriggerManual(ctx, fMaxSteps.ID, nil)
	if err != nil {
		t.Fatalf("TriggerManual fMaxSteps: %v", err)
	}
	_ = eng.ProcessPending(ctx)
	msGot, _ := st.GetRun(ctx, msRun.ID)
	if msGot.Status != RunFailed || msGot.Error == "" {
		t.Fatalf("expected MaxSteps run to fail cleanly, got status=%s err=%q", msGot.Status, msGot.Error)
	}

	// Non-blocking publisher check: even with a full 1-element subscriber buffer and nobody reading,
	// bus.Publish never blocks the play path.
	bus := events.NewBus(events.BusOptions{})
	sub := bus.Subscribe(events.SubscriptionOptions{Name: "stalled-flows", Buffer: 1, Patterns: []string{"*"}})
	defer sub.Unsubscribe()
	for i := range 20 {
		bus.Publish(newTestEvent(fmt.Sprintf("nb_%d", i), "playback.session.progress", events.Correlation{}, map[string]any{}))
	}
	if sub.Stats().Dropped == 0 {
		t.Fatalf("expected stalled subscriber to drop excess events rather than blocking publisher")
	}
}
