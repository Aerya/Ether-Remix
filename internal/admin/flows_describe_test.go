package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/llm"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakeFlowLLM struct {
	mu      sync.Mutex
	enabled bool
	calls   [][]llm.Message
	fn      func(ctx context.Context, attempt int, msgs []llm.Message, jsonMode bool) (string, error)
}

func (f *fakeFlowLLM) Enabled() bool { return f.enabled }

func (f *fakeFlowLLM) Chat(ctx context.Context, msgs []llm.Message, jsonMode bool) (string, error) {
	f.mu.Lock()
	attempt := len(f.calls)
	cp := append([]llm.Message(nil), msgs...)
	f.calls = append(f.calls, cp)
	f.mu.Unlock()
	return f.fn(ctx, attempt, cp, jsonMode)
}

func (f *fakeFlowLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeFlowLLM) call(i int) []llm.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

func describeTestRegistries(t *testing.T) (*events.Registry, flows.ActionRegistry) {
	t.Helper()
	evReg := events.NewBuiltinRegistry()
	actReg := flows.NewActionRegistry()
	actReg.MustRegister(flows.ActionFunc{
		ActionSpec: flows.ActionSpec{
			Type:        "notify.send",
			Group:       "notifications",
			Label:       "Send notification",
			Description: "Deliver a notification to one or more channels.",
			Params: []flows.ParamSchema{
				{Key: "channelIds", Type: events.FieldStringList, Label: "Channels", Required: true, Template: true},
				{Key: "title", Type: events.FieldString, Label: "Title", Required: true, Template: true},
				{Key: "message", Type: events.FieldString, Label: "Message", Required: true, Template: true},
				{Key: "bypassDigest", Type: events.FieldBool, Label: "Bypass digest", Default: false},
			},
			Outputs: []events.FieldSchema{
				{Name: "sent", Type: events.FieldInt, Label: "Sent"},
			},
		},
		Run: func(_ context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
			return flows.ActionResult{Output: map[string]any{"sent": 1, "dryRun": actx.DryRun, "message": params["message"]}}, nil
		},
	})
	actReg.MustRegister(flows.ActionFunc{
		ActionSpec: flows.ActionSpec{
			Type:        "flow.set_variable",
			Group:       "flow",
			Label:       "Set flow variable",
			Description: "Store a value in vars.<key>.",
			Params: []flows.ParamSchema{
				{Key: "key", Type: events.FieldString, Label: "Key", Required: true},
				{Key: "value", Type: events.FieldString, Label: "Value", Required: true, Template: true},
			},
		},
		Run: func(_ context.Context, _ flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
			return flows.ActionResult{Output: map[string]any{"key": params["key"], "value": params["value"]}}, nil
		},
	})
	return evReg, actReg
}

func TestFlowsTestDraftEndpoint(t *testing.T) {
	evReg, actReg := describeTestRegistries(t)
	env := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.Events = evReg
		d.Actions = actReg
	})

	// Method enforcement.
	rec := env.do(t, http.MethodGet, "/test", nil)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Fatalf("GET /test = %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}

	// Missing graph -> 400.
	rec = env.do(t, http.MethodPost, "/test", map[string]any{"dryRun": true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /test without graph = %d %s", rec.Code, rec.Body.String())
	}

	// Invalid graph -> 400 with validation payload.
	rec = env.do(t, http.MethodPost, "/test", map[string]any{
		"graph": flows.Graph{
			SchemaVersion: flows.SchemaVersion,
			Nodes: []flows.Node{{
				ID:     "a1",
				Kind:   flows.NodeAction,
				Config: flows.MustMarshalConfig(flows.ActionNodeConfig{ActionType: "notify.send"}),
			}},
		},
	})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "validation_failed") {
		t.Fatalf("POST /test invalid graph = %d %s", rec.Code, rec.Body.String())
	}

	// Valid draft graph with default dryRun=true and event envelope.
	draft := manualGraph()
	draft.SchemaVersion = 0 // defaulted to flows.SchemaVersion
	rec = env.do(t, http.MethodPost, "/test", map[string]any{
		"graph": draft,
		"event": map[string]any{"type": events.EventSystemBackupFailed},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /test valid draft = %d %s", rec.Code, rec.Body.String())
	}
	if env.rt.lastTestReq.FlowID != "" {
		t.Fatalf("expected empty FlowID for draft test, got %q", env.rt.lastTestReq.FlowID)
	}
	if !env.rt.lastTestReq.DryRun {
		t.Fatal("expected POST /test to default dryRun=true")
	}
	if env.rt.lastTestReq.Graph == nil || env.rt.lastTestReq.Graph.SchemaVersion != flows.SchemaVersion {
		t.Fatalf("expected normalized draft graph passed to runtime, got %+v", env.rt.lastTestReq.Graph)
	}
	if env.rt.lastTestReq.Event.Type != events.EventSystemBackupFailed {
		t.Fatalf("expected event type forwarded, got %+v", env.rt.lastTestReq.Event)
	}
	var out flows.TestRunResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode test run result: %v", err)
	}
	if out.Run.ID == "" || len(out.Steps) == 0 {
		t.Fatalf("unexpected draft test result: %+v", out)
	}

	// Explicit dryRun=false is forwarded.
	rec = env.do(t, http.MethodPost, "/test", map[string]any{
		"graph":  manualGraph(),
		"dryRun": false,
	})
	if rec.Code != http.StatusOK || env.rt.lastTestReq.DryRun {
		t.Fatalf("explicit dryRun=false: code=%d dryRun=%v", rec.Code, env.rt.lastTestReq.DryRun)
	}

	// Nil runtime -> 503.
	noRT := newFlowsTestEnv(t, func(d *FlowsDeps) { d.Runtime = nil })
	rec = noRT.do(t, http.MethodPost, "/test", map[string]any{"graph": manualGraph()})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil runtime: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFlowsTestDraftWithRealEngine(t *testing.T) {
	evReg, actReg := describeTestRegistries(t)
	db, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlStore := flows.NewSQLStore(db)
	eng := flows.NewEngine(flows.EngineOptions{
		Store:   sqlStore,
		Events:  evReg,
		Actions: actReg,
	})
	api := NewFlowsAPI(FlowsDeps{
		Store:   sqlStore,
		Runtime: eng,
		Events:  evReg,
		Actions: actReg,
	})

	draft := flows.Graph{
		SchemaVersion: flows.SchemaVersion,
		Nodes: []flows.Node{
			{
				ID:     "t1",
				Kind:   flows.NodeTriggerEvent,
				Label:  "Backup failed",
				Config: flows.MustMarshalConfig(flows.EventTriggerConfig{Events: []string{events.EventSystemBackupFailed}}),
			},
			{
				ID:    "c1",
				Kind:  flows.NodeCondition,
				Label: "Scheduled backup",
				Config: flows.MustMarshalConfig(flows.ConditionNodeConfig{
					Condition: flows.ConditionGroup{
						Op: flows.LogicAnd,
						Rules: []flows.ConditionRule{
							{Field: "payload.kind", Op: flows.OpEq, Value: "scheduled"},
						},
					},
				}),
			},
			{
				ID:    "a1",
				Kind:  flows.NodeAction,
				Label: "Notify",
				Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
					ActionType: "notify.send",
					Params: map[string]any{
						"channelIds": []string{"*"},
						"title":      "Backup failed",
						"message":    "Error: {{ payload.error }}",
					},
				}),
			},
		},
		Edges: []flows.Edge{
			{ID: "e1", Source: "t1", SourceHandle: flows.HandleNext, Target: "c1"},
			{ID: "e2", Source: "c1", SourceHandle: flows.HandleTrue, Target: "a1"},
		},
	}

	env := &flowsTestEnv{api: api}
	// Send event with type only (no payload), matching TestRunDrawer.tsx.
	rec := env.do(t, http.MethodPost, "/test", map[string]any{
		"graph":  draft,
		"dryRun": true,
		"event":  map[string]any{"type": events.EventSystemBackupFailed},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("real engine POST /test = %d %s", rec.Code, rec.Body.String())
	}
	var res flows.TestRunResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Run.Status != flows.RunCompleted || !res.Run.DryRun {
		t.Fatalf("unexpected run: %+v", res.Run)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("expected 3 steps (trigger -> condition true -> action), got %d: %+v", len(res.Steps), res.Steps)
	}
	if res.Steps[1].Branch != flows.HandleTrue {
		t.Fatalf("expected condition branch true using sample payload, got %q", res.Steps[1].Branch)
	}
	if !strings.Contains(string(res.Steps[2].Output), "no space left on device") {
		t.Fatalf("expected sample payload error rendered in action output, got %s", string(res.Steps[2].Output))
	}
	// Draft dry-run must never persist runs.
	runs, _, err := sqlStore.ListRuns(context.Background(), flows.RunFilter{Limit: 10})
	if err != nil || len(runs) != 0 {
		t.Fatalf("expected 0 persisted runs after draft test, got err=%v len=%d", err, len(runs))
	}
}

func TestFlowsDescribeUnavailableAndValidationErrors(t *testing.T) {
	evReg, actReg := describeTestRegistries(t)

	// Method enforcement.
	envNoLLM := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.Events = evReg
		d.Actions = actReg
	})
	rec := envNoLLM.do(t, http.MethodGet, "/describe", nil)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Fatalf("GET /describe = %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}

	// Empty prompt -> 400.
	rec = envNoLLM.do(t, http.MethodPost, "/describe", map[string]any{"prompt": "   "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty prompt = %d %s", rec.Code, rec.Body.String())
	}

	// Oversized prompt -> 400.
	rec = envNoLLM.do(t, http.MethodPost, "/describe", map[string]any{"prompt": strings.Repeat("a", maxDescribePromptRunes+1)})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized prompt = %d %s", rec.Code, rec.Body.String())
	}

	// Nil LLM -> 409 ai_unavailable.
	rec = envNoLLM.do(t, http.MethodPost, "/describe", map[string]any{"prompt": "Alert me when backups fail"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("nil LLM = %d %s", rec.Code, rec.Body.String())
	}
	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &errBody)
	if errBody.Error.Code != "ai_unavailable" || !strings.Contains(errBody.Error.Message, "Integrations") {
		t.Fatalf("unexpected 409 payload: %+v", errBody)
	}

	// Disabled LLM -> 409 ai_unavailable.
	disabledLLM := &fakeFlowLLM{enabled: false}
	envDisabled := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.Events = evReg
		d.Actions = actReg
		d.LLM = disabledLLM
	})
	rec = envDisabled.do(t, http.MethodPost, "/describe", map[string]any{"prompt": "Alert me when backups fail"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"ai_unavailable"`) {
		t.Fatalf("disabled LLM = %d %s", rec.Code, rec.Body.String())
	}
	if disabledLLM.callCount() != 0 {
		t.Fatal("disabled LLM must not be called")
	}
}

func TestFlowsDescribeSuccessAndContextPrompt(t *testing.T) {
	evReg, actReg := describeTestRegistries(t)
	validReply := "<think>choosing backup failed event</think>\n```json\n" + `{
		"name": "Backup failure alert",
		"description": "Sends an alert whenever a scheduled backup fails.",
		"explanation": "Listens for system.backup.failed events, checks that the backup was scheduled, and sends a notification.",
		"graph": {
			"nodes": [
				{
					"id": "trigger_1",
					"kind": "trigger.event",
					"label": "Backup failed",
					"config": {"events": ["system.backup.failed"]}
				},
				{
					"id": "cond_1",
					"kind": "condition",
					"label": "Scheduled only",
					"config": {
						"condition": {
							"op": "and",
							"rules": [{"field": "payload.kind", "op": "eq", "value": "scheduled"}]
						}
					}
				},
				{
					"id": "notify_1",
					"kind": "action",
					"label": "Notify owner",
					"config": {
						"actionType": "notify.send",
						"params": {
							"channelIds": ["*"],
							"title": "Scheduled backup failed",
							"message": "Backup failed: {{ payload.error }}"
						}
					}
				}
			],
			"edges": [
				{"id": "e1", "source": "trigger_1", "sourceHandle": "next", "target": "cond_1"},
				{"id": "e2", "source": "cond_1", "sourceHandle": "true", "target": "notify_1"}
			]
		}
	}` + "\n```"

	mockLLM := &fakeFlowLLM{
		enabled: true,
		fn: func(ctx context.Context, attempt int, msgs []llm.Message, jsonMode bool) (string, error) {
			if _, hasDeadline := ctx.Deadline(); !hasDeadline {
				t.Error("expected bounded deadline on LLM context")
			}
			if !jsonMode {
				t.Error("expected jsonMode=true")
			}
			return validReply, nil
		},
	}

	env := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.Events = evReg
		d.Actions = actReg
		d.LLM = mockLLM
	})

	rec := env.do(t, http.MethodPost, "/describe", map[string]any{"prompt": "Notify me when a scheduled backup fails"})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /describe = %d %s", rec.Code, rec.Body.String())
	}
	var got describeFlowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "Backup failure alert" || got.Description == "" || got.Explanation == "" {
		t.Fatalf("unexpected response metadata: %+v", got)
	}
	if got.Graph.SchemaVersion != flows.SchemaVersion || len(got.Graph.Nodes) != 3 || len(got.Graph.Edges) != 2 {
		t.Fatalf("unexpected graph: %+v", got.Graph)
	}
	// Auto-layout should have assigned distinct X coordinates across the chain.
	if !(got.Graph.Nodes[0].Position.X < got.Graph.Nodes[1].Position.X && got.Graph.Nodes[1].Position.X < got.Graph.Nodes[2].Position.X) {
		t.Fatalf("expected auto-layout left-to-right positions, got %+v, %+v, %+v",
			got.Graph.Nodes[0].Position, got.Graph.Nodes[1].Position, got.Graph.Nodes[2].Position)
	}

	// Verify the system prompt gave the model events (types, fields, enums),
	// operators, actions, and starter templates.
	if mockLLM.callCount() != 1 {
		t.Fatalf("expected 1 LLM call, got %d", mockLLM.callCount())
	}
	sys := mockLLM.call(0)[0].Content
	for _, want := range []string{
		events.EventSystemBackupFailed,
		"consecutiveFailures:int",
		"kind:enum[scheduled|manual]",
		"contains_any",
		"notify.send",
		"channelIds:string_list (required)",
		"Reference templates",
		"block-4k-remote-transcode",
		"notify-critical-alerts",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}

func TestFlowsDescribeRepairsOnceOnValidationError(t *testing.T) {
	evReg, actReg := describeTestRegistries(t)

	// Attempt 0 has two validation bugs: unknown payload field + missing required action param.
	badFirst := `{
		"name": "Backup alert",
		"description": "Alert on backup failure.",
		"explanation": "Alerts when backups fail.",
		"graph": {
			"schemaVersion": 1,
			"nodes": [
				{"id": "t1", "kind": "trigger.event", "config": {"events": ["system.backup.failed"]}},
				{"id": "c1", "kind": "condition", "config": {"condition": {"op": "and", "rules": [{"field": "payload.nonexistent", "op": "eq", "value": "x"}]}}},
				{"id": "a1", "kind": "action", "config": {"actionType": "notify.send", "params": {"title": "Failed"}}}
			],
			"edges": [
				{"id": "e1", "source": "t1", "sourceHandle": "next", "target": "c1"},
				{"id": "e2", "source": "c1", "sourceHandle": "true", "target": "a1"}
			]
		}
	}`
	repairedSecond := `{
		"name": "Backup alert",
		"description": "Alert on backup failure.",
		"explanation": "Alerts when backups fail.",
		"graph": {
			"schemaVersion": 1,
			"nodes": [
				{"id": "t1", "kind": "trigger.event", "config": {"events": ["system.backup.failed"]}},
				{"id": "a1", "kind": "action", "config": {"actionType": "notify.send", "params": {"channelIds": ["*"], "title": "Backup failed", "message": "{{ payload.error }}"}}}
			],
			"edges": [
				{"id": "e1", "source": "t1", "sourceHandle": "next", "target": "a1"}
			]
		}
	}`

	mockLLM := &fakeFlowLLM{
		enabled: true,
		fn: func(_ context.Context, attempt int, msgs []llm.Message, _ bool) (string, error) {
			if attempt == 0 {
				return badFirst, nil
			}
			// Repair pass must include the validation errors from attempt 0.
			last := msgs[len(msgs)-1]
			if last.Role != "user" || !strings.Contains(last.Content, "nonexistent") || !strings.Contains(last.Content, "channelIds") {
				t.Errorf("expected repair prompt to feed validation errors back, got: %s", last.Content)
			}
			return repairedSecond, nil
		},
	}

	env := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.Events = evReg
		d.Actions = actReg
		d.LLM = mockLLM
	})

	rec := env.do(t, http.MethodPost, "/describe", map[string]any{"prompt": "Notify me on backup failure"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected repaired 200, got %d %s", rec.Code, rec.Body.String())
	}
	if mockLLM.callCount() != 2 {
		t.Fatalf("expected exactly 2 LLM attempts (initial + 1 repair), got %d", mockLLM.callCount())
	}
}

func TestFlowsDescribeReturns422WhenRepairStillFails(t *testing.T) {
	evReg, actReg := describeTestRegistries(t)

	// Cyclic + invalid action graph on both attempts (also verifies cyclic graphs don't hang layout).
	invalidGraph := `{
		"name": "Broken flow",
		"description": "Broken",
		"explanation": "Broken",
		"graph": {
			"schemaVersion": 1,
			"nodes": [
				{"id": "t1", "kind": "trigger.event", "config": {"events": ["system.backup.failed"]}},
				{"id": "a1", "kind": "action", "config": {"actionType": "no.such.action", "params": {}}}
			],
			"edges": [
				{"id": "e1", "source": "t1", "sourceHandle": "next", "target": "a1"},
				{"id": "e2", "source": "a1", "sourceHandle": "next", "target": "t1"}
			]
		}
	}`

	mockLLM := &fakeFlowLLM{
		enabled: true,
		fn: func(_ context.Context, _ int, _ []llm.Message, _ bool) (string, error) {
			return invalidGraph, nil
		},
	}

	env := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.Events = evReg
		d.Actions = actReg
		d.LLM = mockLLM
	})

	rec := env.do(t, http.MethodPost, "/describe", map[string]any{"prompt": "Do something impossible"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 after failed repair, got %d %s", rec.Code, rec.Body.String())
	}
	if mockLLM.callCount() != 2 {
		t.Fatalf("expected exactly 2 LLM attempts, got %d", mockLLM.callCount())
	}
	var errResp struct {
		Error struct {
			Code    string       `json:"code"`
			Message string       `json:"message"`
			Fields  []FieldError `json:"fields"`
		} `json:"error"`
		Validation *flows.ValidationResult `json:"validation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode 422 response: %v", err)
	}
	if errResp.Error.Code != "validation_failed" || !strings.Contains(errResp.Error.Message, "no.such.action") {
		t.Fatalf("expected readable error in 422 response, got %+v", errResp.Error)
	}
	if len(errResp.Error.Fields) == 0 || errResp.Validation == nil || errResp.Validation.Valid {
		t.Fatalf("expected validation details in 422 response, got %+v", errResp)
	}
}

func TestFlowsDescribeReturns422OnNonJSON(t *testing.T) {
	evReg, actReg := describeTestRegistries(t)
	mockLLM := &fakeFlowLLM{
		enabled: true,
		fn: func(_ context.Context, _ int, _ []llm.Message, _ bool) (string, error) {
			return "Sorry, I cannot help with that.", nil
		},
	}
	env := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.Events = evReg
		d.Actions = actReg
		d.LLM = mockLLM
	})
	rec := env.do(t, http.MethodPost, "/describe", map[string]any{"prompt": "Alert me"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on non-JSON output, got %d %s", rec.Code, rec.Body.String())
	}
	if mockLLM.callCount() != 2 {
		t.Fatalf("expected 2 attempts on non-JSON output, got %d", mockLLM.callCount())
	}
}

func TestFlowsDescribeBoundedTimeoutAndUpstreamError(t *testing.T) {
	evReg, actReg := describeTestRegistries(t)

	// 1. Timeout is bounded by DescribeTimeout.
	slowLLM := &fakeFlowLLM{
		enabled: true,
		fn: func(ctx context.Context, _ int, _ []llm.Message, _ bool) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	envSlow := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.Events = evReg
		d.Actions = actReg
		d.LLM = slowLLM
		d.DescribeTimeout = 40 * time.Millisecond
	})
	start := time.Now()
	rec := envSlow.do(t, http.MethodPost, "/describe", map[string]any{"prompt": "Alert me"})
	elapsed := time.Since(start)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 on timeout, got %d %s", rec.Code, rec.Body.String())
	}
	if elapsed > 2*time.Second {
		t.Fatalf("describe exceeded bounded timeout: %s", elapsed)
	}

	// 2. Upstream LLM failure -> 502.
	failLLM := &fakeFlowLLM{
		enabled: true,
		fn: func(_ context.Context, _ int, _ []llm.Message, _ bool) (string, error) {
			return "", errors.New("connection refused")
		},
	}
	envFail := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.Events = evReg
		d.Actions = actReg
		d.LLM = failLLM
	})
	rec = envFail.do(t, http.MethodPost, "/describe", map[string]any{"prompt": "Alert me"})
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "connection refused") {
		t.Fatalf("expected 502 on upstream failure, got %d %s", rec.Code, rec.Body.String())
	}
}
