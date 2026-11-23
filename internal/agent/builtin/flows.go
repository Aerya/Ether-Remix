package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
)

const flowsUnavailable = "Flows are not available on this server."

// FlowsService is the narrow automation surface exposed to built-in MCP and
// Ask Ether tools.
type FlowsService interface {
	ListFlows(ctx context.Context) ([]flows.Flow, error)
	GetFlow(ctx context.Context, id string) (*flows.Flow, error)
	ListTemplates() []flows.Template
	PreviewTemplate(ctx context.Context, templateID, name, description string, enabled *bool, params map[string]any) (*flows.Flow, error)
	CreateFromTemplate(ctx context.Context, templateID, name, description string, enabled *bool, params map[string]any) (*flows.Flow, error)
	SetFlowEnabled(ctx context.Context, id string, enabled bool, reason string) error
	TestRun(ctx context.Context, req flows.TestRunRequest) (*flows.TestRunResult, error)
	ListRuns(ctx context.Context, filter flows.RunFilter) ([]flows.Run, string, error)
	GetRun(ctx context.Context, runID string) (*flows.Run, []flows.RunStep, error)
}

func flowSummary(f flows.Flow) map[string]any {
	m := map[string]any{
		"id":        f.ID,
		"name":      truncate(f.Name, 200),
		"enabled":   f.Enabled,
		"version":   f.Version,
		"nodeCount": len(f.Graph.Nodes),
		"edgeCount": len(f.Graph.Edges),
	}
	if f.Description != "" {
		m["description"] = truncate(f.Description, 300)
	}
	if f.TemplateID != "" {
		m["templateId"] = f.TemplateID
	}
	if len(f.Tags) > 0 {
		m["tags"] = f.Tags
	}
	if !f.LastRunAt.IsZero() {
		m["lastRunAt"] = f.LastRunAt.UTC().Format(time.RFC3339)
	}
	if f.LastStatus != "" {
		m["lastStatus"] = string(f.LastStatus)
	}
	if f.ConsecutiveFailures > 0 {
		m["consecutiveFailures"] = f.ConsecutiveFailures
	}
	if !f.UpdatedAt.IsZero() {
		m["updatedAt"] = f.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return m
}

func runSummary(r flows.Run) map[string]any {
	m := map[string]any{
		"id":          r.ID,
		"flowId":      r.FlowID,
		"flowVersion": r.FlowVersion,
		"triggerKind": string(r.TriggerKind),
		"status":      string(r.Status),
		"stepCount":   r.StepCount,
		"startedAt":   r.StartedAt.UTC().Format(time.RFC3339),
	}
	if r.FlowName != "" {
		m["flowName"] = truncate(r.FlowName, 200)
	}
	if r.EventType != "" {
		m["eventType"] = r.EventType
	}
	if r.DryRun {
		m["dryRun"] = true
	}
	if r.Error != "" {
		m["error"] = truncate(r.Error, 300)
	}
	if !r.FinishedAt.IsZero() {
		m["finishedAt"] = r.FinishedAt.UTC().Format(time.RFC3339)
	}
	return m
}

func stepSummary(s flows.RunStep) map[string]any {
	m := map[string]any{
		"seq":        s.Seq,
		"nodeId":     s.NodeID,
		"nodeKind":   string(s.NodeKind),
		"status":     string(s.Status),
		"durationMs": s.DurationMS,
	}
	if s.Branch != "" {
		m["branch"] = s.Branch
	}
	if s.Error != "" {
		m["error"] = truncate(s.Error, 300)
	}
	if len(s.Output) > 0 && string(s.Output) != "null" {
		var out any
		if json.Unmarshal(s.Output, &out) == nil {
			m["output"] = out
		}
	}
	return m
}

func templateSummary(t flows.Template) map[string]any {
	params := make([]map[string]any, 0, len(t.Params))
	for _, p := range t.Params {
		pm := map[string]any{
			"key":      p.Key,
			"type":     string(p.Type),
			"label":    p.Label,
			"required": p.Required,
		}
		if p.Description != "" {
			pm["description"] = truncate(p.Description, 200)
		}
		if p.Default != nil {
			pm["default"] = p.Default
		}
		params = append(params, pm)
	}
	m := map[string]any{
		"id":          t.ID,
		"name":        t.Name,
		"description": truncate(t.Description, 300),
		"category":    t.Category,
		"params":      params,
	}
	if t.NotifyPreset != "" {
		m["notifyPreset"] = t.NotifyPreset
	}
	return m
}

func flowsTools(d Deps) []tools.Tool {
	return []tools.Tool{
		tool(tools.Tool{
			Name: "flows_list", Title: "Flows", ReadOnly: true,
			Description: "List saved automation flows with status and recent execution summary. Optionally filter by enabled state or tag, or set includeTemplates=true to see starter templates that can be passed to flow_create_from_template.",
			InputSchema: schema(merge(pPage(), map[string]any{
				"enabled":          pBool("Filter to only enabled (true) or disabled (false) flows"),
				"tag":              pString("Filter flows containing this tag", 64),
				"includeTemplates": pBool("Include curated starter templates in the response"),
			})),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				if d.Flows == nil {
					return tools.Result{}, tools.Userf(flowsUnavailable)
				}
				page, pageSize, err := pageArgs(in)
				if err != nil {
					return tools.Result{}, err
				}
				tag, err := optStr(in, "tag", 64)
				if err != nil {
					return tools.Result{}, err
				}
				var hasEnabled bool
				var wantEnabled bool
				if _, ok := in["enabled"]; ok && in["enabled"] != nil {
					b, err := boolArg(in, "enabled", false)
					if err != nil {
						return tools.Result{}, err
					}
					hasEnabled = true
					wantEnabled = b
				}
				includeTemplates, err := boolArg(in, "includeTemplates", false)
				if err != nil {
					return tools.Result{}, err
				}
				all, err := d.Flows.ListFlows(ctx)
				if err != nil {
					return tools.Result{}, err
				}
				filtered := make([]flows.Flow, 0, len(all))
				for _, f := range all {
					if hasEnabled && f.Enabled != wantEnabled {
						continue
					}
					if tag != "" {
						matched := false
						for _, t := range f.Tags {
							if strings.EqualFold(t, tag) {
								matched = true
								break
							}
						}
						if !matched {
							continue
						}
					}
					filtered = append(filtered, f)
				}
				slice, total := paged(filtered, page, pageSize)
				items := make([]map[string]any, 0, len(slice))
				for _, f := range slice {
					items = append(items, flowSummary(f))
				}
				data := merge(pageMeta(page, pageSize, total), map[string]any{"flows": items})
				if includeTemplates {
					tmpls := d.Flows.ListTemplates()
					tlist := make([]map[string]any, 0, len(tmpls))
					for _, t := range tmpls {
						tlist = append(tlist, templateSummary(t))
					}
					data["templates"] = tlist
				}
				return tools.Result{
					Data:  data,
					Links: []tools.Link{{Label: "Flows", Route: "/flows/list"}},
				}, nil
			},
		}),
		tool(tools.Tool{
			Name: "flow_get", Title: "Flow details", ReadOnly: true,
			Description: "Inspect one saved automation flow by ID, including its trigger, condition, and action nodes, edges, limits, and status.",
			InputSchema: schema(map[string]any{
				"flowId": pString("Flow ID from flows_list", 128),
			}, "flowId"),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				if d.Flows == nil {
					return tools.Result{}, tools.Userf(flowsUnavailable)
				}
				id, err := reqStr(in, "flowId", 128)
				if err != nil {
					return tools.Result{}, err
				}
				f, err := d.Flows.GetFlow(ctx, id)
				if err != nil {
					if errors.Is(err, flows.ErrNotFound) {
						return tools.Result{}, tools.Userf("no flow found with id %q", id)
					}
					return tools.Result{}, err
				}
				raw, _ := json.Marshal(f)
				var data map[string]any
				_ = json.Unmarshal(raw, &data)
				return tools.Result{
					Data:  map[string]any{"flow": data},
					Links: []tools.Link{{Label: f.Name, Route: "/flows/list/" + url.PathEscape(f.ID)}},
				}, nil
			},
		}),
		tool(tools.Tool{
			Name: "flow_create_from_template", Title: "Create a flow from a template",
			Description: "Draft a new automation flow from a built-in template and propose creating it. Validates the template and parameters before asking the owner for approval. Call flows_list with includeTemplates=true to discover template IDs and parameters.",
			InputSchema: schema(map[string]any{
				"templateId":  pString("Template ID from flows_list(includeTemplates=true)", 128),
				"name":        pString("Optional display name overriding the template name", 120),
				"description": pString("Optional description overriding the template description", 400),
				"enabled":     pBool("Whether the new flow should start enabled (default true)"),
				"params": map[string]any{
					"type":                 "object",
					"maxProperties":        50,
					"additionalProperties": true,
					"description":          "Template parameter values keyed by parameter name",
				},
			}, "templateId"),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				if d.Flows == nil {
					return tools.Result{}, tools.Userf(flowsUnavailable)
				}
				templateID, err := reqStr(in, "templateId", 128)
				if err != nil {
					return tools.Result{}, err
				}
				name, err := optStr(in, "name", 120)
				if err != nil {
					return tools.Result{}, err
				}
				desc, err := optStr(in, "description", 400)
				if err != nil {
					return tools.Result{}, err
				}
				var enabled *bool
				if _, ok := in["enabled"]; ok && in["enabled"] != nil {
					b, err := boolArg(in, "enabled", true)
					if err != nil {
						return tools.Result{}, err
					}
					enabled = &b
				}
				var params map[string]any
				if rawParams, ok := in["params"]; ok && rawParams != nil {
					m, ok := rawParams.(map[string]any)
					if !ok {
						return tools.Result{}, tools.Userf("params must be an object")
					}
					if len(m) > 50 {
						return tools.Result{}, tools.Userf("too many template params")
					}
					params = m
				}
				draft, err := d.Flows.PreviewTemplate(ctx, templateID, name, desc, enabled, params)
				if err != nil {
					return tools.Result{}, tools.Userf("%v", err)
				}
				stateLabel := "enabled"
				if !draft.Enabled {
					stateLabel = "disabled"
				}
				details := []tools.ProposalDetail{
					{Label: "Template", Value: templateID},
					{Label: "Name", Value: draft.Name},
					{Label: "Initial state", Value: stateLabel},
					{Label: "Nodes", Value: fmt.Sprintf("%d", len(draft.Graph.Nodes))},
				}
				return propose(
					"Create a flow",
					fmt.Sprintf("Create automation flow %q from template %q (%s).", draft.Name, templateID, stateLabel),
					details,
					in,
					func(applyCtx context.Context) (tools.Result, error) {
						saved, err := d.Flows.CreateFromTemplate(applyCtx, templateID, name, desc, enabled, params)
						if err != nil {
							return tools.Result{}, err
						}
						return tools.Result{
							Data:  map[string]any{"created": true, "flow": flowSummary(*saved)},
							Links: []tools.Link{{Label: saved.Name, Route: "/flows/list/" + url.PathEscape(saved.ID)}},
						}, nil
					},
				), nil
			},
		}),
		tool(tools.Tool{
			Name: "flow_enable", Title: "Turn a flow on or off",
			Description: "Propose enabling or disabling a saved automation flow.",
			InputSchema: schema(map[string]any{
				"flowId":  pString("Flow ID from flows_list", 128),
				"enabled": pBool("True to enable the flow, false to pause/disable it"),
				"reason":  pString("Optional short note explaining why the flow was toggled", 200),
			}, "flowId", "enabled"),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				if d.Flows == nil {
					return tools.Result{}, tools.Userf(flowsUnavailable)
				}
				id, err := reqStr(in, "flowId", 128)
				if err != nil {
					return tools.Result{}, err
				}
				enabled, err := boolArg(in, "enabled", true)
				if err != nil {
					return tools.Result{}, err
				}
				reason, err := optStr(in, "reason", 200)
				if err != nil {
					return tools.Result{}, err
				}
				existing, err := d.Flows.GetFlow(ctx, id)
				if err != nil {
					if errors.Is(err, flows.ErrNotFound) {
						return tools.Result{}, tools.Userf("no flow found with id %q", id)
					}
					return tools.Result{}, err
				}
				actionWord := "Disable"
				if enabled {
					actionWord = "Enable"
				}
				details := []tools.ProposalDetail{
					{Label: "Flow", Value: existing.Name},
					{Label: "Flow ID", Value: existing.ID},
					{Label: "Action", Value: actionWord},
				}
				if reason != "" {
					details = append(details, tools.ProposalDetail{Label: "Reason", Value: reason})
				}
				return propose(
					actionWord+" a flow",
					fmt.Sprintf("%s automation flow %q (%s).", actionWord, existing.Name, existing.ID),
					details,
					map[string]any{"flowId": id, "enabled": enabled, "reason": reason},
					func(applyCtx context.Context) (tools.Result, error) {
						if err := d.Flows.SetFlowEnabled(applyCtx, id, enabled, reason); err != nil {
							return tools.Result{}, err
						}
						return tools.Result{
							Data:  map[string]any{"changed": true, "flowId": id, "enabled": enabled},
							Links: []tools.Link{{Label: existing.Name, Route: "/flows/list/" + url.PathEscape(existing.ID)}},
						}, nil
					},
				), nil
			},
		}),
		tool(tools.Tool{
			Name: "flow_test_run", Title: "Test-run a flow",
			Description: "Execute a test run of a saved flow and return its step-by-step trace. Defaults to dryRun=true (executes immediately without external side effects). Passing dryRun=false proposes a live execution requiring owner approval.",
			InputSchema: schema(map[string]any{
				"flowId":    pString("Flow ID from flows_list", 128),
				"dryRun":    pBool("Simulate side-effecting actions without sending notifications or modifying state (default true)"),
				"eventType": pString("Optional event type to simulate (e.g. system.backup.failed)", 128),
				"payload": map[string]any{
					"type":                 "object",
					"maxProperties":        50,
					"additionalProperties": true,
					"description":          "Optional sample event payload object",
				},
				"inputs": map[string]any{
					"type":                 "object",
					"maxProperties":        50,
					"additionalProperties": true,
					"description":          "Optional manual trigger inputs object",
				},
			}, "flowId"),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				if d.Flows == nil {
					return tools.Result{}, tools.Userf(flowsUnavailable)
				}
				id, err := reqStr(in, "flowId", 128)
				if err != nil {
					return tools.Result{}, err
				}
				dryRun, err := boolArg(in, "dryRun", true)
				if err != nil {
					return tools.Result{}, err
				}
				eventType, err := optStr(in, "eventType", 128)
				if err != nil {
					return tools.Result{}, err
				}
				var payloadRaw json.RawMessage
				if p, ok := in["payload"]; ok && p != nil {
					m, ok := p.(map[string]any)
					if !ok {
						return tools.Result{}, tools.Userf("payload must be an object")
					}
					if len(m) > 50 {
						return tools.Result{}, tools.Userf("payload has too many properties")
					}
					b, err := json.Marshal(m)
					if err != nil {
						return tools.Result{}, tools.Userf("invalid payload: %v", err)
					}
					payloadRaw = b
				}
				var inputs map[string]any
				if rawIn, ok := in["inputs"]; ok && rawIn != nil {
					m, ok := rawIn.(map[string]any)
					if !ok {
						return tools.Result{}, tools.Userf("inputs must be an object")
					}
					if len(m) > 50 {
						return tools.Result{}, tools.Userf("inputs has too many properties")
					}
					inputs = m
				}
				existing, err := d.Flows.GetFlow(ctx, id)
				if err != nil {
					if errors.Is(err, flows.ErrNotFound) {
						return tools.Result{}, tools.Userf("no flow found with id %q", id)
					}
					return tools.Result{}, err
				}
				req := flows.TestRunRequest{
					FlowID: id,
					DryRun: dryRun,
					Inputs: inputs,
				}
				if eventType != "" {
					req.Event = events.Envelope{
						Type:    eventType,
						Payload: payloadRaw,
					}
				}
				execRun := func(runCtx context.Context) (tools.Result, error) {
					res, err := d.Flows.TestRun(runCtx, req)
					if err != nil {
						return tools.Result{}, err
					}
					steps := make([]map[string]any, 0, len(res.Steps))
					for _, st := range res.Steps {
						steps = append(steps, stepSummary(st))
					}
					return tools.Result{
						Data: map[string]any{
							"run":   runSummary(res.Run),
							"steps": steps,
						},
						Links: []tools.Link{{Label: existing.Name, Route: "/flows/list/" + url.PathEscape(existing.ID)}},
					}, nil
				}
				if dryRun {
					return execRun(ctx)
				}
				details := []tools.ProposalDetail{
					{Label: "Flow", Value: existing.Name},
					{Label: "Flow ID", Value: existing.ID},
					{Label: "Mode", Value: "Live (side effects enabled)"},
				}
				if eventType != "" {
					details = append(details, tools.ProposalDetail{Label: "Simulated event", Value: eventType})
				}
				return propose(
					"Run a flow live",
					fmt.Sprintf("Run automation flow %q (%s) live with real side effects.", existing.Name, existing.ID),
					details,
					in,
					execRun,
				), nil
			},
		}),
		tool(tools.Tool{
			Name: "flow_runs", Title: "Flow runs", ReadOnly: true,
			Description: "List recent automation flow runs (filterable by flowId, status, or eventType), or pass runId to inspect one run with its step-by-step execution trace.",
			InputSchema: schema(map[string]any{
				"runId":     pString("Specific run ID to inspect with all recorded steps", 128),
				"flowId":    pString("Filter runs to one flow ID", 128),
				"status":    pEnum("Filter runs by status", "pending", "running", "waiting", "completed", "failed", "cancelled", "throttled"),
				"eventType": pString("Filter runs by triggering event type", 128),
				"limit":     pInt("Maximum runs to return, at most 50", 1, 50),
				"cursor":    pString("Pagination cursor from a previous flow_runs response", 256),
			}),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				if d.Flows == nil {
					return tools.Result{}, tools.Userf(flowsUnavailable)
				}
				runID, err := optStr(in, "runId", 128)
				if err != nil {
					return tools.Result{}, err
				}
				if runID != "" {
					run, steps, err := d.Flows.GetRun(ctx, runID)
					if err != nil {
						if errors.Is(err, flows.ErrNotFound) {
							return tools.Result{}, tools.Userf("no flow run found with id %q", runID)
						}
						return tools.Result{}, err
					}
					stepItems := make([]map[string]any, 0, len(steps))
					for _, st := range steps {
						stepItems = append(stepItems, stepSummary(st))
					}
					return tools.Result{
						Data: map[string]any{
							"run":   runSummary(*run),
							"steps": stepItems,
						},
						Links: []tools.Link{{Label: "Run trace", Route: "/flows/runs?run=" + url.QueryEscape(run.ID)}},
					}, nil
				}
				flowID, err := optStr(in, "flowId", 128)
				if err != nil {
					return tools.Result{}, err
				}
				status, err := optStr(in, "status", 32)
				if err != nil {
					return tools.Result{}, err
				}
				eventType, err := optStr(in, "eventType", 128)
				if err != nil {
					return tools.Result{}, err
				}
				limit, err := intArg(in, "limit", 20, 1, 50)
				if err != nil {
					return tools.Result{}, err
				}
				cursor, err := optStr(in, "cursor", 256)
				if err != nil {
					return tools.Result{}, err
				}
				runs, nextCursor, err := d.Flows.ListRuns(ctx, flows.RunFilter{
					FlowID:    flowID,
					Status:    flows.RunStatus(status),
					EventType: eventType,
					Limit:     limit,
					Cursor:    cursor,
				})
				if err != nil {
					return tools.Result{}, err
				}
				items := make([]map[string]any, 0, len(runs))
				for _, r := range runs {
					items = append(items, runSummary(r))
				}
				data := map[string]any{"runs": items, "count": len(items)}
				if nextCursor != "" {
					data["nextCursor"] = nextCursor
				}
				return tools.Result{
					Data:  data,
					Links: []tools.Link{{Label: "Flow runs", Route: "/flows/runs"}},
				}, nil
			},
		}),
	}
}
