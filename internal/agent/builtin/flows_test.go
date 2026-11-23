package builtin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/builtin"
	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
)

type fakeFlowsService struct {
	flowsMap  map[string]flows.Flow
	runs      []flows.Run
	steps     map[string][]flows.RunStep
	templates []flows.Template
	writes    int
	testReqs  []flows.TestRunRequest
}

func newFakeFlowsService() *fakeFlowsService {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	return &fakeFlowsService{
		flowsMap: map[string]flows.Flow{
			"flow_1": {
				ID:          "flow_1",
				Name:        "Backup alerts",
				Description: "Notify on backup failure",
				Enabled:     true,
				Version:     2,
				Tags:        []string{"ops", "alerts"},
				Graph: flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						{ID: "t1", Kind: flows.NodeTriggerEvent, Config: flows.MustMarshalConfig(flows.EventTriggerConfig{Events: []string{"system.backup.failed"}})},
					},
				},
				LastRunAt:  now,
				LastStatus: flows.RunCompleted,
				UpdatedAt:  now,
			},
			"flow_2": {
				ID:      "flow_2",
				Name:    "Paused flow",
				Enabled: false,
				Version: 1,
				Tags:    []string{"digest"},
			},
		},
		runs: []flows.Run{
			{
				ID:          "run_1",
				FlowID:      "flow_1",
				FlowName:    "Backup alerts",
				FlowVersion: 2,
				TriggerKind: flows.NodeTriggerEvent,
				EventType:   "system.backup.failed",
				Status:      flows.RunCompleted,
				StepCount:   2,
				StartedAt:   now,
				FinishedAt:  now.Add(150 * time.Millisecond),
			},
		},
		steps: map[string][]flows.RunStep{
			"run_1": {
				{Seq: 1, NodeID: "t1", NodeKind: flows.NodeTriggerEvent, Status: flows.StepCompleted, DurationMS: 2},
				{Seq: 2, NodeID: "a1", NodeKind: flows.NodeAction, Status: flows.StepCompleted, DurationMS: 120},
			},
		},
		templates: []flows.Template{
			{
				ID:          "notify-critical-alerts",
				Name:        "Critical server alerts",
				Description: "Alert on critical events",
				Category:    "notifications",
				Params: []flows.ParamSchema{
					{Key: "channelIds", Type: events.FieldStringList, Label: "Channels", Required: true},
				},
			},
		},
	}
}

func (f *fakeFlowsService) ListFlows(context.Context) ([]flows.Flow, error) {
	out := make([]flows.Flow, 0, len(f.flowsMap))
	for _, fl := range f.flowsMap {
		out = append(out, fl)
	}
	return out, nil
}

func (f *fakeFlowsService) GetFlow(_ context.Context, id string) (*flows.Flow, error) {
	fl, ok := f.flowsMap[id]
	if !ok {
		return nil, flows.ErrNotFound
	}
	cp := fl
	return &cp, nil
}

func (f *fakeFlowsService) ListTemplates() []flows.Template { return f.templates }

func (f *fakeFlowsService) PreviewTemplate(_ context.Context, templateID, name, description string, enabled *bool, params map[string]any) (*flows.Flow, error) {
	if templateID != "notify-critical-alerts" {
		return nil, errors.New("unknown template")
	}
	if params == nil || params["channelIds"] == nil {
		return nil, errors.New("params.channelIds is required")
	}
	if name == "" {
		name = "Critical server alerts"
	}
	en := true
	if enabled != nil {
		en = *enabled
	}
	return &flows.Flow{
		Name:        name,
		Description: description,
		Enabled:     en,
		TemplateID:  templateID,
		Graph: flows.Graph{
			SchemaVersion: flows.SchemaVersion,
			Nodes:         []flows.Node{{ID: "t1", Kind: flows.NodeTriggerEvent}},
		},
	}, nil
}

func (f *fakeFlowsService) CreateFromTemplate(ctx context.Context, templateID, name, description string, enabled *bool, params map[string]any) (*flows.Flow, error) {
	draft, err := f.PreviewTemplate(ctx, templateID, name, description, enabled, params)
	if err != nil {
		return nil, err
	}
	f.writes++
	draft.ID = "flow_created"
	draft.Version = 1
	f.flowsMap[draft.ID] = *draft
	return draft, nil
}

func (f *fakeFlowsService) SetFlowEnabled(_ context.Context, id string, enabled bool, _ string) error {
	fl, ok := f.flowsMap[id]
	if !ok {
		return flows.ErrNotFound
	}
	f.writes++
	fl.Enabled = enabled
	f.flowsMap[id] = fl
	return nil
}

func (f *fakeFlowsService) TestRun(_ context.Context, req flows.TestRunRequest) (*flows.TestRunResult, error) {
	f.testReqs = append(f.testReqs, req)
	if !req.DryRun {
		f.writes++
	}
	now := time.Now().UTC()
	return &flows.TestRunResult{
		Run: flows.Run{
			ID:          "run_test",
			FlowID:      req.FlowID,
			TriggerKind: flows.NodeTriggerEvent,
			EventType:   req.Event.Type,
			Status:      flows.RunCompleted,
			DryRun:      req.DryRun,
			StepCount:   1,
			StartedAt:   now,
			FinishedAt:  now,
		},
		Steps: []flows.RunStep{
			{Seq: 1, NodeID: "t1", NodeKind: flows.NodeTriggerEvent, Status: flows.StepCompleted, DurationMS: 3},
		},
	}, nil
}

func (f *fakeFlowsService) ListRuns(_ context.Context, filter flows.RunFilter) ([]flows.Run, string, error) {
	var out []flows.Run
	for _, r := range f.runs {
		if filter.FlowID != "" && r.FlowID != filter.FlowID {
			continue
		}
		if filter.Status != "" && r.Status != filter.Status {
			continue
		}
		out = append(out, r)
	}
	return out, "", nil
}

func (f *fakeFlowsService) GetRun(_ context.Context, runID string) (*flows.Run, []flows.RunStep, error) {
	for _, r := range f.runs {
		if r.ID == runID {
			cp := r
			return &cp, f.steps[runID], nil
		}
	}
	return nil, nil, flows.ErrNotFound
}

func TestFlowsBuiltinTools(t *testing.T) {
	ctx := context.Background()
	_, d := buildDeps()
	ff := newFakeFlowsService()
	d.Flows = ff

	cat := tools.NewCatalogue()
	if err := cat.Register(builtin.All(d)...); err != nil {
		t.Fatal(err)
	}
	owner := tools.Principal{ViewerID: 1, IsOwner: true, Surface: "mcp", Scopes: []string{"full"}}

	// 1. flows_list (filtered + templates).
	res, err := callTool(t, cat, ctx, owner, "flows_list", map[string]any{"enabled": true, "includeTemplates": true})
	if err != nil {
		t.Fatalf("flows_list: %v", err)
	}
	if res.Proposal != nil {
		t.Fatal("flows_list must not propose")
	}
	list := res.Data["flows"].([]map[string]any)
	if len(list) != 1 || list[0]["id"] != "flow_1" {
		t.Fatalf("unexpected flows_list result: %+v", list)
	}
	tmpls := res.Data["templates"].([]map[string]any)
	if len(tmpls) != 1 || tmpls[0]["id"] != "notify-critical-alerts" {
		t.Fatalf("unexpected templates result: %+v", tmpls)
	}

	// 2. flow_get (found & not found).
	res, err = callTool(t, cat, ctx, owner, "flow_get", map[string]any{"flowId": "flow_1"})
	if err != nil {
		t.Fatalf("flow_get: %v", err)
	}
	if res.Data["flow"].(map[string]any)["name"] != "Backup alerts" {
		t.Fatalf("unexpected flow_get result: %+v", res.Data)
	}
	if len(res.Links) != 1 || res.Links[0].Route != "/flows/list/flow_1" {
		t.Fatalf("unexpected flow_get links: %+v", res.Links)
	}
	if _, err := callTool(t, cat, ctx, owner, "flow_get", map[string]any{"flowId": "missing"}); err == nil {
		t.Fatal("expected error for missing flow_get")
	}

	// 3. flow_create_from_template (validates before proposing; does not write until Apply).
	if _, err := callTool(t, cat, ctx, owner, "flow_create_from_template", map[string]any{"templateId": "notify-critical-alerts"}); err == nil {
		t.Fatal("expected missing param error before proposal")
	}
	res, err = callTool(t, cat, ctx, owner, "flow_create_from_template", map[string]any{
		"templateId": "notify-critical-alerts",
		"name":       "My Alerts",
		"params":     map[string]any{"channelIds": []string{"discord-1"}},
	})
	if err != nil {
		t.Fatalf("flow_create_from_template: %v", err)
	}
	if res.Proposal == nil {
		t.Fatal("expected proposal from flow_create_from_template")
	}
	if ff.writes != 0 {
		t.Fatalf("expected 0 writes before approval, got %d", ff.writes)
	}
	applied, err := res.Proposal.Apply(ctx)
	if err != nil {
		t.Fatalf("apply flow_create_from_template: %v", err)
	}
	if ff.writes != 1 || applied.Data["created"] != true {
		t.Fatalf("unexpected apply result: writes=%d data=%+v", ff.writes, applied.Data)
	}
	if len(applied.Links) != 1 || applied.Links[0].Route != "/flows/list/flow_created" {
		t.Fatalf("unexpected flow_create_from_template links: %+v", applied.Links)
	}

	// 4. flow_enable (proposes before mutating).
	res, err = callTool(t, cat, ctx, owner, "flow_enable", map[string]any{
		"flowId":  "flow_1",
		"enabled": false,
		"reason":  "maintenance",
	})
	if err != nil {
		t.Fatalf("flow_enable: %v", err)
	}
	if res.Proposal == nil {
		t.Fatal("expected proposal from flow_enable")
	}
	if !ff.flowsMap["flow_1"].Enabled {
		t.Fatal("flow_enable mutated before approval")
	}
	enableApplied, err := res.Proposal.Apply(ctx)
	if err != nil {
		t.Fatalf("apply flow_enable: %v", err)
	}
	if len(enableApplied.Links) != 1 || enableApplied.Links[0].Route != "/flows/list/flow_1" {
		t.Fatalf("unexpected flow_enable links: %+v", enableApplied.Links)
	}
	if ff.flowsMap["flow_1"].Enabled {
		t.Fatal("expected flow_1 to be disabled after approval")
	}

	// 5. flow_test_run: dryRun=true executes immediately without proposal; dryRun=false proposes.
	res, err = callTool(t, cat, ctx, owner, "flow_test_run", map[string]any{
		"flowId":    "flow_1",
		"eventType": "system.backup.failed",
		"payload":   map[string]any{"error": "disk full"},
	})
	if err != nil {
		t.Fatalf("flow_test_run dry: %v", err)
	}
	if res.Proposal != nil {
		t.Fatal("dry-run flow_test_run must execute immediately without proposal")
	}
	if len(ff.testReqs) != 1 || !ff.testReqs[0].DryRun {
		t.Fatalf("unexpected dry-run call: %+v", ff.testReqs)
	}
	if len(res.Links) != 1 || res.Links[0].Route != "/flows/list/flow_1" {
		t.Fatalf("unexpected flow_test_run links: %+v", res.Links)
	}
	liveRes, err := callTool(t, cat, ctx, owner, "flow_test_run", map[string]any{
		"flowId": "flow_1",
		"dryRun": false,
	})
	if err != nil {
		t.Fatalf("flow_test_run live: %v", err)
	}
	if liveRes.Proposal == nil {
		t.Fatal("live flow_test_run must require approval")
	}
	if len(ff.testReqs) != 1 {
		t.Fatal("live flow_test_run executed before approval")
	}
	if _, err := liveRes.Proposal.Apply(ctx); err != nil {
		t.Fatalf("apply live flow_test_run: %v", err)
	}
	if len(ff.testReqs) != 2 || ff.testReqs[1].DryRun {
		t.Fatalf("expected live TestRun after approval, got %+v", ff.testReqs)
	}

	// 6. flow_runs (list + inspect single run with steps).
	res, err = callTool(t, cat, ctx, owner, "flow_runs", map[string]any{"flowId": "flow_1"})
	if err != nil {
		t.Fatalf("flow_runs list: %v", err)
	}
	if res.Data["count"].(int) != 1 {
		t.Fatalf("unexpected flow_runs list: %+v", res.Data)
	}
	res, err = callTool(t, cat, ctx, owner, "flow_runs", map[string]any{"runId": "run_1"})
	if err != nil {
		t.Fatalf("flow_runs detail: %v", err)
	}
	steps := res.Data["steps"].([]map[string]any)
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps for run_1, got %d", len(steps))
	}
}
