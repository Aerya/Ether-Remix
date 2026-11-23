package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/llm"
	"github.com/braedonsaunders/ether/internal/textclip"
)

const (
	defaultDescribeTimeout = 45 * time.Second
	maxDescribePromptRunes = 2000
	maxDescribeRepairChars = 6000
)

type describeFlowRequest struct {
	Prompt string `json:"prompt"`
}

type describeFlowResponse struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Graph       flows.Graph `json:"graph"`
	Explanation string      `json:"explanation"`
}

func (a *FlowsAPI) handleDescribeFlow(w http.ResponseWriter, r *http.Request) {
	var req describeFlowRequest
	if err := decodeJSONLenient(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" || utf8.RuneCountInString(prompt) > maxDescribePromptRunes {
		writeError(w, http.StatusBadRequest, "bad_request", "describe the flow you want in at most 2,000 characters")
		return
	}
	if a.deps.LLM == nil || !a.deps.LLM.Enabled() {
		writeError(w, http.StatusConflict, "ai_unavailable", "Configure AI in Integrations to describe flows in plain language.")
		return
	}

	timeout := a.deps.DescribeTimeout
	if timeout <= 0 {
		timeout = defaultDescribeTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	msgs := []llm.Message{
		{Role: "system", Content: a.buildDescribeSystemPrompt()},
		{Role: "user", Content: "Request: " + prompt},
	}
	opts := a.validateOpts()

	var (
		lastErr        error
		lastValidation *flows.ValidationResult
	)
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := a.deps.LLM.Chat(ctx, msgs, true)
		if err != nil {
			a.writeDescribeLLMError(w, r.Context(), ctx, err)
			return
		}
		resp, valRes, perr := parseAndValidateDescribedFlow(raw, opts)
		if perr == nil {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		lastErr = perr
		lastValidation = valRes

		if attempt == 0 {
			msgs = append(msgs,
				llm.Message{Role: "assistant", Content: textclip.Bytes(raw, maxDescribeRepairChars)},
				llm.Message{Role: "user", Content: buildDescribeRepairPrompt(valRes, perr)},
			)
		}
	}

	writeDescribeValidationFailure(w, lastValidation, lastErr)
}

func (a *FlowsAPI) writeDescribeLLMError(w http.ResponseWriter, reqCtx, callCtx context.Context, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, "timeout", "describing this flow took too long; try again")
		return
	}
	if errors.Is(reqCtx.Err(), context.Canceled) {
		return
	}
	a.log.Warn("flows: describe LLM call failed", "err", err)
	writeError(w, http.StatusBadGateway, "upstream", "the language model could not be reached: "+err.Error())
}

func parseAndValidateDescribedFlow(raw string, opts flows.ValidateOptions) (describeFlowResponse, *flows.ValidationResult, error) {
	extracted := strings.TrimSpace(llm.ExtractJSON(raw))
	if extracted == "" {
		return describeFlowResponse{}, nil, errors.New("the model reply did not contain a JSON object")
	}

	var resp describeFlowResponse
	if err := json.Unmarshal([]byte(extracted), &resp); err != nil {
		return describeFlowResponse{}, nil, fmt.Errorf("invalid JSON in model reply: %w", err)
	}

	// Tolerate a model returning a bare Graph without the outer envelope.
	if len(resp.Graph.Nodes) == 0 {
		var bare flows.Graph
		if err := json.Unmarshal([]byte(extracted), &bare); err == nil && len(bare.Nodes) > 0 {
			resp.Graph = bare
		}
	}

	resp.Name = strings.TrimSpace(resp.Name)
	resp.Description = strings.TrimSpace(resp.Description)
	resp.Explanation = strings.TrimSpace(resp.Explanation)
	if resp.Name == "" {
		resp.Name = "Draft flow"
	}
	if resp.Explanation == "" {
		resp.Explanation = resp.Description
	}
	if resp.Description == "" {
		resp.Description = resp.Explanation
	}

	if resp.Graph.SchemaVersion == 0 {
		resp.Graph.SchemaVersion = flows.SchemaVersion
	}
	if resp.Graph.Nodes == nil {
		resp.Graph.Nodes = []flows.Node{}
	}
	if resp.Graph.Edges == nil {
		resp.Graph.Edges = []flows.Edge{}
	}
	res := flows.ValidateGraph(resp.Graph, opts)
	if !res.Valid {
		return describeFlowResponse{}, &res, errors.New(formatValidationIssues(res.Errors))
	}
	ensureDescribedLayout(&resp.Graph)
	return resp, &res, nil
}

func ensureDescribedLayout(g *flows.Graph) {
	if g == nil || len(g.Nodes) == 0 {
		return
	}
	for _, n := range g.Nodes {
		if n.Position.X != 0 || n.Position.Y != 0 {
			return
		}
	}
	adj := make(map[string][]string, len(g.Nodes))
	for _, e := range g.Edges {
		adj[e.Source] = append(adj[e.Source], e.Target)
	}
	depth := make(map[string]int, len(g.Nodes))
	var queue []string
	for _, n := range g.Nodes {
		if n.Kind.IsTrigger() {
			depth[n.ID] = 0
			queue = append(queue, n.ID)
		}
	}
	maxDepth := len(g.Nodes)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		nextDepth := depth[cur] + 1
		if nextDepth > maxDepth {
			continue
		}
		for _, target := range adj[cur] {
			if prev, seen := depth[target]; !seen || nextDepth > prev {
				depth[target] = nextDepth
				queue = append(queue, target)
			}
		}
	}
	rowByCol := make(map[int]int)
	fallbackCol := 0
	for _, d := range depth {
		if d >= fallbackCol {
			fallbackCol = d + 1
		}
	}
	for i := range g.Nodes {
		col, ok := depth[g.Nodes[i].ID]
		if !ok {
			col = fallbackCol
		}
		row := rowByCol[col]
		rowByCol[col] = row + 1
		g.Nodes[i].Position = flows.Position{
			X: 80 + float64(col)*300,
			Y: 120 + float64(row)*150,
		}
	}
}

func buildDescribeRepairPrompt(res *flows.ValidationResult, err error) string {
	var b strings.Builder
	b.WriteString("Your previous flow JSON failed validation:\n")
	if res != nil && len(res.Errors) > 0 {
		for _, issue := range res.Errors {
			b.WriteString("- ")
			b.WriteString(formatValidationIssue(issue))
			b.WriteByte('\n')
		}
	} else if err != nil {
		b.WriteString("- ")
		b.WriteString(err.Error())
		b.WriteByte('\n')
	}
	b.WriteString("Fix every error above and reply with ONLY the corrected JSON object matching the schema.")
	return b.String()
}

func writeDescribeValidationFailure(w http.ResponseWriter, res *flows.ValidationResult, err error) {
	if res != nil && len(res.Errors) > 0 {
		fields := make([]FieldError, 0, len(res.Errors))
		msgs := make([]string, 0, len(res.Errors))
		for _, issue := range res.Errors {
			loc := issueLocation(issue)
			fields = append(fields, FieldError{Field: loc, Message: issue.Message})
			msgs = append(msgs, fmt.Sprintf("%s: %s", loc, issue.Message))
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{
				"code":    "validation_failed",
				"message": "Could not produce a valid flow: " + strings.Join(msgs, "; "),
				"fields":  fields,
			},
			"validation": res,
		})
		return
	}
	msg := "Could not produce a valid flow from that description"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		msg += ": " + err.Error()
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error": map[string]any{
			"code":    "invalid_flow",
			"message": msg,
		},
	})
}

func issueLocation(issue flows.ValidationIssue) string {
	switch {
	case issue.NodeID != "" && issue.Path != "":
		return issue.NodeID + " (" + issue.Path + ")"
	case issue.NodeID != "":
		return issue.NodeID
	case issue.EdgeID != "":
		return issue.EdgeID
	case issue.Path != "":
		return issue.Path
	default:
		return "graph"
	}
}

func formatValidationIssue(issue flows.ValidationIssue) string {
	return fmt.Sprintf("[%s] %s: %s", issue.Code, issueLocation(issue), issue.Message)
}

func formatValidationIssues(issues []flows.ValidationIssue) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, formatValidationIssue(issue))
	}
	return strings.Join(parts, "; ")
}

func (a *FlowsAPI) buildDescribeSystemPrompt() string {
	var b strings.Builder
	b.WriteString(`You design Ether automation flows from plain-language requests.
Reply with ONE valid JSON object only, no markdown prose outside JSON:
{
  "name": "<short friendly flow name>",
  "description": "<one-sentence summary>",
  "explanation": "<plain-English explanation of the trigger, checks, and actions>",
  "graph": {
    "schemaVersion": 1,
    "nodes": [
      {"id": "trigger_1", "kind": "...", "label": "...", "position": {"x": 80, "y": 120}, "config": { ... }}
    ],
    "edges": [
      {"id": "e1", "source": "trigger_1", "sourceHandle": "next", "target": "..."}
    ]
  }
}

Graph rules (enforced strictly by the validator):
- schemaVersion must be 1. Every flow needs at least one trigger node and no cycles. Triggers cannot have incoming edges.
- Node and edge IDs must be unique and match ^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$.
- Node kinds & config shapes:
  - trigger.event: {"events": ["<exact event type from catalogue>"], "filter"?: <ConditionGroup>}  (handle: "next")
  - trigger.schedule: {"cron": "<5-field min hour dom month dow>", "timezone"?: "UTC"} OR {"every": "<Go duration >= 1m>"} — provide EXACTLY ONE of cron or every  (handle: "next")
  - trigger.webhook: {"allowedMethods"?: ["POST"]}  (handle: "next")
  - trigger.manual: {"inputs"?: [...], "sampleEventType"?: "<event type>"}  (handle: "next")
  - condition: {"condition": <ConditionGroup>}  (handles: "true", "false")
  - logic.switch: {"cases": [{"key": "<case_id>", "label": "...", "condition": <ConditionGroup>}], "matchAll"?: false}  (handles: each case key, or "default")
  - logic.delay: {"duration": "<Go duration>"}  (handle: "next")
  - logic.wait_event: {"eventTypes": ["<event type>"], "matchCorrelation": true, "filter"?: <ConditionGroup>, "timeout": "<Go duration>"}  (handles: "matched", "timeout")
  - logic.window: {"keyTemplate": "{{ payload.<field> }}", "threshold": <int >= 1>, "window": "<Go duration >= 1s>", "resetOnReach": true}  (handles: "reached", "below")
  - logic.dedupe: {"keyTemplate": "{{ payload.<field> }}", "cooldown": "<Go duration >= 1s>", "maxPerWindow": 1}  (handles: "pass", "suppressed")
  - action: {"actionType": "<exact action type from catalogue>", "params": { ... }, "continueOnError"?: false}  (handles: "next", "error")
- ConditionGroup: {"op": "and"|"or", "negate"?: false, "rules": [{"field": "payload.<fieldName>", "op": "<operator>", "value": ...}], "groups"?: [...]}.
  - Only reference fields that exist on the upstream event schema(s). Prefix event payload fields with "payload.".
  - Operators is_true, is_false, exists, not_exists take NO value field.
  - For enum fields, value MUST be one of the listed enum options.
- Go durations (for trigger.schedule.every, logic.delay.duration, logic.wait_event.timeout, logic.window.window, logic.dedupe.cooldown) only allow Go units: ms, s, m, h (e.g. "30s", "15m", "1h", "24h" — NEVER "d" or "w").
- Action params: ONLY use parameters defined in the action's schema, with matching JSON types, and include every required parameter. For notify.send, use "channelIds": ["*"] when the user does not name a specific channel.
- Templates in string parameters/keyTemplate use {{ payload.<field> }}, {{ event.type }}, {{ event.node }}, {{ steps.<nodeId>.output.<field> }}, {{ vars.<key> }}, {{ trigger.inputs.<key> }}.
`)

	b.WriteString("\nCondition operators by field type:\n")
	fieldTypes := []events.FieldType{
		events.FieldString,
		events.FieldEnum,
		events.FieldInt,
		events.FieldFloat,
		events.FieldBool,
		events.FieldDurationMS,
		events.FieldTimestamp,
		events.FieldStringList,
		events.FieldObject,
	}
	for _, ft := range fieldTypes {
		ops := flows.OperatorsForFieldType(ft)
		opStrs := make([]string, len(ops))
		for i, op := range ops {
			opStrs[i] = string(op)
		}
		fmt.Fprintf(&b, "- %s: %s\n", ft, strings.Join(opStrs, ", "))
	}

	if a.deps.Events != nil {
		schemas := a.deps.Events.List()
		if len(schemas) > 0 {
			b.WriteString("\nEvent catalogue (type — label: fields):\n")
			for _, s := range schemas {
				if s.Deprecated {
					continue
				}
				fmt.Fprintf(&b, "- %s (%s)", s.Type, s.Label)
				if s.Description != "" {
					fmt.Fprintf(&b, ": %s", s.Description)
				}
				if len(s.Fields) > 0 {
					fields := make([]string, 0, len(s.Fields))
					for _, f := range s.Fields {
						desc := f.Name + ":" + string(f.Type)
						if len(f.Enum) > 0 {
							vals := make([]string, len(f.Enum))
							for i, opt := range f.Enum {
								vals[i] = opt.Value
							}
							desc += "[" + strings.Join(vals, "|") + "]"
						}
						fields = append(fields, desc)
					}
					fmt.Fprintf(&b, " | fields: %s", strings.Join(fields, ", "))
				}
				b.WriteByte('\n')
			}
		}
	}

	if a.deps.Actions != nil {
		specs := a.deps.Actions.List()
		if len(specs) > 0 {
			b.WriteString("\nAction catalogue:\n")
			for _, spec := range specs {
				fmt.Fprintf(&b, "- %s (%s): %s\n", spec.Type, spec.Label, spec.Description)
				if len(spec.Params) > 0 {
					params := make([]string, 0, len(spec.Params))
					for _, p := range spec.Params {
						entry := p.Key + ":" + string(p.Type)
						if p.Required && p.Default == nil {
							entry += " (required)"
						}
						if len(p.Enum) > 0 {
							vals := make([]string, len(p.Enum))
							for i, opt := range p.Enum {
								vals[i] = opt.Value
							}
							entry += "[" + strings.Join(vals, "|") + "]"
						}
						if p.Default != nil {
							entry += fmt.Sprintf(" default=%v", p.Default)
						}
						if p.Template {
							entry += " (template)"
						}
						params = append(params, entry)
					}
					fmt.Fprintf(&b, "  params: %s\n", strings.Join(params, ", "))
				}
				if len(spec.Outputs) > 0 {
					outs := make([]string, 0, len(spec.Outputs))
					for _, o := range spec.Outputs {
						outs = append(outs, o.Name+":"+string(o.Type))
					}
					fmt.Fprintf(&b, "  outputs: %s\n", strings.Join(outs, ", "))
				}
			}
		}
	}

	templates := a.templates()
	if len(templates) > 0 {
		b.WriteString("\nReference templates (adapt these proven graph patterns when relevant):\n")
		for _, tmpl := range templates {
			gBytes, err := json.Marshal(tmpl.Graph)
			if err != nil {
				continue
			}
			fmt.Fprintf(&b, "- %s (%s): %s\n  graph: %s\n", tmpl.ID, tmpl.Name, tmpl.Description, string(gBytes))
		}
	}

	return b.String()
}
