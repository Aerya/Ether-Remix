package flows

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
)

// ValidationIssue is one structured error or warning produced by ValidateGraph/ValidateFlow.
type ValidationIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	NodeID  string `json:"nodeId,omitempty"`
	EdgeID  string `json:"edgeId,omitempty"`
	Message string `json:"message"`
}

// ValidationResult aggregates every error and non-fatal warning for a graph.
type ValidationResult struct {
	Valid    bool              `json:"valid"`
	Errors   []ValidationIssue `json:"errors,omitempty"`
	Warnings []ValidationIssue `json:"warnings,omitempty"`
}

// Error summarizes the first validation error, or empty when Valid.
func (r ValidationResult) Error() string {
	if len(r.Errors) == 0 {
		return ""
	}
	e := r.Errors[0]
	if e.Path != "" {
		return fmt.Sprintf("flows: invalid (%s at %s): %s", e.Code, e.Path, e.Message)
	}
	return fmt.Sprintf("flows: invalid (%s): %s", e.Code, e.Message)
}

// ValidateOptions injects live registries and optional overrides into validation.
type ValidateOptions struct {
	Events   *events.Registry
	Actions  ActionRegistry
	Triggers TriggerRegistry
	MaxNodes int
	MaxEdges int
	MaxBytes int
}

var nodeIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

// ValidateFlow validates a complete Flow definition including metadata, limits, and graph.
func ValidateFlow(f Flow, opts ValidateOptions) ValidationResult {
	res := ValidateGraph(f.Graph, opts)
	if strings.TrimSpace(f.Name) == "" {
		res.Errors = append(res.Errors, ValidationIssue{
			Code:    "missing_name",
			Path:    "name",
			Message: "flow name is required",
		})
	}
	if len(f.Name) > 120 {
		res.Errors = append(res.Errors, ValidationIssue{
			Code:    "name_too_long",
			Path:    "name",
			Message: "flow name must be at most 120 characters",
		})
	}
	if f.Limits.RateLimitPerMin < 0 || f.Limits.RateLimitPerMin > HardRateLimitPerMin {
		res.Errors = append(res.Errors, ValidationIssue{
			Code:    "invalid_limit",
			Path:    "limits.rateLimitPerMin",
			Message: fmt.Sprintf("rateLimitPerMin must be between 0 and %d", HardRateLimitPerMin),
		})
	}
	if f.Limits.MaxSteps < 0 || f.Limits.MaxSteps > HardMaxSteps {
		res.Errors = append(res.Errors, ValidationIssue{
			Code:    "invalid_limit",
			Path:    "limits.maxSteps",
			Message: fmt.Sprintf("maxSteps must be between 0 and %d", HardMaxSteps),
		})
	}
	if f.Limits.Concurrency < 0 || f.Limits.Concurrency > HardConcurrency {
		res.Errors = append(res.Errors, ValidationIssue{
			Code:    "invalid_limit",
			Path:    "limits.concurrency",
			Message: fmt.Sprintf("concurrency must be between 0 and %d", HardConcurrency),
		})
	}
	res.Valid = len(res.Errors) == 0
	return res
}

// ValidateGraph statically checks structure, cycles, node configs, handles,
// templates, condition operators, event schemas, and action schemas.
func ValidateGraph(g Graph, opts ValidateOptions) ValidationResult {
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = DefaultMaxNodes
	}
	if opts.MaxEdges <= 0 {
		opts.MaxEdges = DefaultMaxEdges
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxGraphBytes
	}

	var res ValidationResult
	addErr := func(code, path, nodeID, edgeID, msg string) {
		res.Errors = append(res.Errors, ValidationIssue{
			Code: code, Path: path, NodeID: nodeID, EdgeID: edgeID, Message: msg,
		})
	}
	addWarn := func(code, path, nodeID, edgeID, msg string) {
		res.Warnings = append(res.Warnings, ValidationIssue{
			Code: code, Path: path, NodeID: nodeID, EdgeID: edgeID, Message: msg,
		})
	}

	if g.SchemaVersion != SchemaVersion {
		addErr("unsupported_schema_version", "schemaVersion", "", "",
			fmt.Sprintf("unsupported graph schemaVersion %d (expected %d)", g.SchemaVersion, SchemaVersion))
	}
	if raw, err := json.Marshal(g); err != nil {
		addErr("unserializable_graph", "", "", "", "graph cannot be serialized to JSON")
	} else if len(raw) > opts.MaxBytes {
		addErr("graph_too_large", "", "", "", fmt.Sprintf("graph exceeds %d byte limit", opts.MaxBytes))
	}
	if len(g.Nodes) > opts.MaxNodes {
		addErr("too_many_nodes", "nodes", "", "", fmt.Sprintf("graph has %d nodes (max %d)", len(g.Nodes), opts.MaxNodes))
	}
	if len(g.Edges) > opts.MaxEdges {
		addErr("too_many_edges", "edges", "", "", fmt.Sprintf("graph has %d edges (max %d)", len(g.Edges), opts.MaxEdges))
	}

	nodesByID := make(map[string]Node, len(g.Nodes))
	switchCasesByNode := make(map[string]map[string]bool)
	triggerEventPatterns := make(map[string][]string) // trigger node ID -> patterns
	var triggerIDs []string

	for i, n := range g.Nodes {
		path := fmt.Sprintf("nodes[%d]", i)
		if !nodeIDRe.MatchString(n.ID) {
			addErr("invalid_node_id", path+".id", n.ID, "", fmt.Sprintf("invalid node id %q", n.ID))
			continue
		}
		if _, dup := nodesByID[n.ID]; dup {
			addErr("duplicate_node_id", path+".id", n.ID, "", fmt.Sprintf("duplicate node id %q", n.ID))
			continue
		}
		nodesByID[n.ID] = n

		if math.IsNaN(n.Position.X) || math.IsInf(n.Position.X, 0) || math.IsNaN(n.Position.Y) || math.IsInf(n.Position.Y, 0) {
			addErr("invalid_position", path+".position", n.ID, "", "node position coordinates must be finite numbers")
		}
		if !n.Kind.IsValid() {
			addErr("unknown_node_kind", path+".kind", n.ID, "", fmt.Sprintf("unknown node kind %q", n.Kind))
			continue
		}
		if n.Kind.IsTrigger() {
			if opts.Triggers != nil {
				if _, ok := opts.Triggers.Get(n.Kind); !ok {
					addErr("unknown_trigger_kind", path+".kind", n.ID, "", fmt.Sprintf("trigger kind %q is not registered", n.Kind))
				}
			}
			triggerIDs = append(triggerIDs, n.ID)
		}
	}

	if len(triggerIDs) == 0 {
		addErr("missing_trigger", "nodes", "", "", "flow must contain at least one trigger node")
	}

	// Validate node configs (first pass for triggers/switches so edges & schema propagation know them).
	for i, n := range g.Nodes {
		if _, ok := nodesByID[n.ID]; !ok || !n.Kind.IsValid() {
			continue
		}
		path := fmt.Sprintf("nodes[%d].config", i)
		for _, key := range unresolvedTemplateParams(n.Config) {
			addErr("unresolved_template_param", path, n.ID, "",
				fmt.Sprintf("%q is a template setting that was never filled in; choose a value for it", key))
		}
		switch n.Kind {
		case NodeTriggerEvent:
			cfg, err := DecodeConfig[EventTriggerConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			if len(cfg.Events) == 0 {
				addErr("missing_events", path+".events", n.ID, "", "event trigger requires at least one event type or pattern")
				continue
			}
			triggerEventPatterns[n.ID] = cfg.Events
			schemas := validateEventPatterns(cfg.Events, opts.Events, path+".events", n.ID, addErr, addWarn)
			if cfg.Filter != nil {
				validateConditionGroup(*cfg.Filter, schemas, path+".filter", n.ID, addErr, addWarn)
			}

		case NodeTriggerSchedule:
			cfg, err := DecodeConfig[ScheduleTriggerConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			hasCron := strings.TrimSpace(cfg.Cron) != ""
			hasEvery := strings.TrimSpace(cfg.Every) != ""
			if hasCron == hasEvery {
				addErr("invalid_schedule", path, n.ID, "", "schedule trigger requires exactly one of cron or every")
			} else if hasCron {
				if err := validateCron(cfg.Cron); err != nil {
					addErr("invalid_cron", path+".cron", n.ID, "", err.Error())
				}
			} else if hasEvery {
				if d, err := ParseDuration(cfg.Every); err != nil {
					addErr("invalid_interval", path+".every", n.ID, "", durationIssue(cfg.Every, "schedule interval", time.Minute, time.Duration(1<<63-1)))
				} else if d < time.Minute {
					addErr("invalid_interval", path+".every", n.ID, "", "schedule interval must be at least 1m")
				}
			}
			if cfg.Timezone != "" {
				if _, err := time.LoadLocation(cfg.Timezone); err != nil {
					addErr("invalid_timezone", path+".timezone", n.ID, "", fmt.Sprintf("unknown timezone %q", cfg.Timezone))
				}
			}

		case NodeTriggerWebhook:
			cfg, err := DecodeConfig[WebhookTriggerConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			for _, m := range cfg.AllowedMethods {
				switch strings.ToUpper(m) {
				case "POST", "PUT", "GET":
				default:
					addErr("invalid_method", path+".allowedMethods", n.ID, "", fmt.Sprintf("unsupported webhook HTTP method %q", m))
				}
			}

		case NodeTriggerManual:
			cfg, err := DecodeConfig[ManualTriggerConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			seenKeys := map[string]bool{}
			for j, p := range cfg.Inputs {
				pPath := fmt.Sprintf("%s.inputs[%d]", path, j)
				if strings.TrimSpace(p.Key) == "" || seenKeys[p.Key] {
					addErr("invalid_param", pPath, n.ID, "", fmt.Sprintf("invalid or duplicate manual input key %q", p.Key))
				}
				seenKeys[p.Key] = true
				if !p.Type.Valid() {
					addErr("invalid_param_type", pPath, n.ID, "", fmt.Sprintf("invalid input type %q", p.Type))
				}
			}
			if cfg.SampleEventType != "" && opts.Events != nil {
				if _, ok := opts.Events.Get(cfg.SampleEventType); !ok {
					addErr("unknown_event", path+".sampleEventType", n.ID, "", fmt.Sprintf("unknown sample event type %q", cfg.SampleEventType))
				}
			}

		case NodeLogicSwitch:
			cfg, err := DecodeConfig[SwitchNodeConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			if len(cfg.Cases) == 0 {
				addErr("empty_switch", path+".cases", n.ID, "", "switch node requires at least one case")
				continue
			}
			cases := make(map[string]bool, len(cfg.Cases))
			for j, c := range cfg.Cases {
				cPath := fmt.Sprintf("%s.cases[%d]", path, j)
				if !nodeIDRe.MatchString(c.Key) || c.Key == HandleDefault {
					addErr("invalid_switch_case", cPath+".key", n.ID, "", fmt.Sprintf("invalid switch case key %q", c.Key))
					continue
				}
				if cases[c.Key] {
					addErr("duplicate_switch_case", cPath+".key", n.ID, "", fmt.Sprintf("duplicate switch case key %q", c.Key))
					continue
				}
				cases[c.Key] = true
			}
			switchCasesByNode[n.ID] = cases

		case NodeLogicDelay:
			cfg, err := DecodeConfig[DelayNodeConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			count := 0
			if cfg.Duration != "" {
				count++
			}
			if cfg.DurationField != "" {
				count++
			}
			if cfg.Until != "" {
				count++
			}
			if count != 1 {
				addErr("invalid_delay", path, n.ID, "", "delay node requires exactly one of duration, durationField, or until")
				continue
			}
			if cfg.Duration != "" {
				if msg := durationIssue(cfg.Duration, "delay", time.Millisecond, MaxDelayDuration); msg != "" {
					addErr("invalid_duration", path+".duration", n.ID, "", msg)
				}
			}
			if cfg.Until != "" {
				if _, err := time.Parse(time.RFC3339, cfg.Until); err != nil {
					addErr("invalid_timestamp", path+".until", n.ID, "", "until must be an RFC3339 timestamp")
				}
			}

		case NodeLogicWaitEvent:
			cfg, err := DecodeConfig[WaitEventNodeConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			if len(cfg.EventTypes) == 0 {
				addErr("missing_events", path+".eventTypes", n.ID, "", "wait_event node requires at least one event type")
			}
			schemas := validateEventPatterns(cfg.EventTypes, opts.Events, path+".eventTypes", n.ID, addErr, addWarn)
			if msg := durationIssue(cfg.Timeout, "wait timeout", time.Millisecond, MaxDelayDuration); msg != "" {
				addErr("invalid_timeout", path+".timeout", n.ID, "", msg)
			}
			if cfg.Filter != nil {
				validateConditionGroup(*cfg.Filter, schemas, path+".filter", n.ID, addErr, addWarn)
			}

		case NodeLogicWindow:
			cfg, err := DecodeConfig[WindowNodeConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			if cfg.Threshold < 1 {
				addErr("invalid_threshold", path+".threshold", n.ID, "", "window threshold must be >= 1")
			}
			if msg := durationIssue(cfg.Window, "window", time.Second, MaxWindowDuration); msg != "" {
				addErr("invalid_window", path+".window", n.ID, "", msg)
			}
			if cfg.KeyTemplate != "" {
				if err := ValidateTemplate(cfg.KeyTemplate); err != nil {
					addErr("invalid_template", path+".keyTemplate", n.ID, "", err.Error())
				}
			}

		case NodeLogicDedupe:
			cfg, err := DecodeConfig[DedupeNodeConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			if msg := durationIssue(cfg.Cooldown, "cooldown", time.Second, MaxDelayDuration); msg != "" {
				addErr("invalid_cooldown", path+".cooldown", n.ID, "", msg)
			}
			if cfg.MaxPerWindow < 0 {
				addErr("invalid_max_per_window", path+".maxPerWindow", n.ID, "", "maxPerWindow must be >= 0")
			}
			if cfg.KeyTemplate != "" {
				if err := ValidateTemplate(cfg.KeyTemplate); err != nil {
					addErr("invalid_template", path+".keyTemplate", n.ID, "", err.Error())
				}
			}

		case NodeAction:
			cfg, err := DecodeConfig[ActionNodeConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			validateActionConfig(cfg, opts.Actions, path, n.ID, addErr, addWarn)
		}
	}

	// Validate edges, handles, adjacency, and cycles.
	edgeIDs := make(map[string]bool, len(g.Edges))
	seenTriples := make(map[string]bool, len(g.Edges))
	adj := make(map[string][]string, len(g.Nodes))

	for i, e := range g.Edges {
		ePath := fmt.Sprintf("edges[%d]", i)
		if !nodeIDRe.MatchString(e.ID) {
			addErr("invalid_edge_id", ePath+".id", "", e.ID, fmt.Sprintf("invalid edge id %q", e.ID))
		} else if edgeIDs[e.ID] {
			addErr("duplicate_edge_id", ePath+".id", "", e.ID, fmt.Sprintf("duplicate edge id %q", e.ID))
		}
		edgeIDs[e.ID] = true

		src, okSrc := nodesByID[e.Source]
		dst, okDst := nodesByID[e.Target]
		if !okSrc {
			addErr("unknown_node", ePath+".source", "", e.ID, fmt.Sprintf("edge references unknown source node %q", e.Source))
		}
		if !okDst {
			addErr("unknown_node", ePath+".target", "", e.ID, fmt.Sprintf("edge references unknown target node %q", e.Target))
		}
		if !okSrc || !okDst {
			continue
		}
		if e.Source == e.Target {
			addErr("self_loop", ePath, e.Source, e.ID, "edge cannot connect a node to itself")
			continue
		}
		if dst.Kind.IsTrigger() {
			addErr("trigger_target", ePath+".target", dst.ID, e.ID, "trigger nodes cannot have incoming edges")
		}
		if !isValidSourceHandle(src.Kind, e.SourceHandle, switchCasesByNode[src.ID]) {
			addErr("invalid_source_handle", ePath+".sourceHandle", src.ID, e.ID,
				fmt.Sprintf("invalid output handle %q for %s node %q", e.SourceHandle, src.Kind, src.ID))
		}
		triple := e.Source + "\x00" + e.SourceHandle + "\x00" + e.Target
		if seenTriples[triple] {
			addErr("duplicate_edge", ePath, src.ID, e.ID, "duplicate edge between the same handle and target")
			continue
		}
		seenTriples[triple] = true
		adj[e.Source] = append(adj[e.Source], e.Target)
	}

	// Cycle detection (3-color DFS).
	if cycle := findCycle(g.Nodes, adj); len(cycle) > 0 {
		addErr("cycle", "edges", cycle[0], "", fmt.Sprintf("graph contains a cycle: %s", strings.Join(cycle, " -> ")))
	}

	// Reachability & upstream event schema inference from triggers.
	reachable := make(map[string]bool, len(g.Nodes))
	nodeTriggerEvents := make(map[string][]string) // nodeID -> union of upstream trigger event patterns
	nodeHasNonEventTrigger := make(map[string]bool)

	for _, tid := range triggerIDs {
		tNode := nodesByID[tid]
		patterns := triggerEventPatterns[tid]
		isEventTrig := tNode.Kind == NodeTriggerEvent

		visited := map[string]bool{tid: true}
		queue := []string{tid}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			reachable[cur] = true
			if isEventTrig {
				nodeTriggerEvents[cur] = appendUnique(nodeTriggerEvents[cur], patterns...)
			} else {
				nodeHasNonEventTrigger[cur] = true
			}
			for _, next := range adj[cur] {
				if !visited[next] {
					visited[next] = true
					queue = append(queue, next)
				}
			}
		}
	}

	for i, n := range g.Nodes {
		if _, ok := nodesByID[n.ID]; !ok {
			continue
		}
		if !reachable[n.ID] {
			addWarn("unreachable_node", fmt.Sprintf("nodes[%d]", i), n.ID, "", fmt.Sprintf("node %q is not reachable from any trigger", n.ID))
		}
	}

	// Second pass: validate condition & switch expressions against upstream trigger event schemas.
	for i, n := range g.Nodes {
		if _, ok := nodesByID[n.ID]; !ok {
			continue
		}
		path := fmt.Sprintf("nodes[%d].config", i)
		var upstreamSchemas []events.EventSchema
		if opts.Events != nil && !nodeHasNonEventTrigger[n.ID] && len(nodeTriggerEvents[n.ID]) > 0 {
			upstreamSchemas = resolveSchemasForPatterns(nodeTriggerEvents[n.ID], opts.Events)
		}
		switch n.Kind {
		case NodeCondition:
			cfg, err := DecodeConfig[ConditionNodeConfig](n)
			if err != nil {
				addErr("invalid_config", path, n.ID, "", err.Error())
				continue
			}
			validateConditionGroup(cfg.Condition, upstreamSchemas, path+".condition", n.ID, addErr, addWarn)
		case NodeLogicSwitch:
			cfg, err := DecodeConfig[SwitchNodeConfig](n)
			if err != nil {
				continue
			}
			for j, c := range cfg.Cases {
				cPath := fmt.Sprintf("%s.cases[%d].condition", path, j)
				validateConditionGroup(c.Condition, upstreamSchemas, cPath, n.ID, addErr, addWarn)
			}
		}
	}

	res.Valid = len(res.Errors) == 0
	return res
}

func isValidSourceHandle(kind NodeKind, handle string, switchCases map[string]bool) bool {
	switch kind {
	case NodeTriggerEvent, NodeTriggerSchedule, NodeTriggerWebhook, NodeTriggerManual, NodeLogicDelay:
		return handle == "" || handle == HandleNext
	case NodeCondition:
		return handle == HandleTrue || handle == HandleFalse
	case NodeLogicSwitch:
		return handle == HandleDefault || switchCases[handle]
	case NodeLogicWaitEvent:
		return handle == HandleMatched || handle == HandleTimeout
	case NodeLogicWindow:
		return handle == HandleReached || handle == HandleBelow
	case NodeLogicDedupe:
		return handle == HandlePass || handle == HandleSuppressed
	case NodeAction:
		return handle == "" || handle == HandleNext || handle == HandleError
	default:
		return false
	}
}

func findCycle(nodes []Node, adj map[string][]string) []string {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(nodes))
	var stack []string

	var dfs func(id string) []string
	dfs = func(id string) []string {
		state[id] = visiting
		stack = append(stack, id)
		for _, next := range adj[id] {
			switch state[next] {
			case visiting:
				idx := 0
				for idx < len(stack) && stack[idx] != next {
					idx++
				}
				return append(append([]string(nil), stack[idx:]...), next)
			case unvisited:
				if cyc := dfs(next); len(cyc) > 0 {
					return cyc
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = visited
		return nil
	}

	for _, n := range nodes {
		if state[n.ID] == unvisited {
			if cyc := dfs(n.ID); len(cyc) > 0 {
				return cyc
			}
		}
	}
	return nil
}

func validateEventPatterns(
	patterns []string,
	reg *events.Registry,
	path, nodeID string,
	addErr, addWarn func(code, path, nodeID, edgeID, msg string),
) []events.EventSchema {
	var matched []events.EventSchema
	seen := map[string]bool{}
	allSchemas := []events.EventSchema(nil)
	if reg != nil {
		allSchemas = reg.List()
	}

	for i, p := range patterns {
		pPath := fmt.Sprintf("%s[%d]", path, i)
		p = strings.TrimSpace(p)
		if p == "" {
			addErr("empty_event_pattern", pPath, nodeID, "", "event pattern cannot be empty")
			continue
		}
		isWildcard := p == "*" || strings.HasSuffix(p, ".*") || strings.HasSuffix(p, "/*") || strings.HasSuffix(p, ".") || strings.HasSuffix(p, "/")
		if !isWildcard {
			if err := events.ValidateType(p); err != nil {
				addErr("invalid_event_type", pPath, nodeID, "", err.Error())
				continue
			}
		}
		if reg == nil {
			continue
		}
		if !isWildcard {
			s, ok := reg.Get(p)
			if !ok {
				addErr("unknown_event", pPath, nodeID, "", fmt.Sprintf("unknown event type %q", p))
				continue
			}
			if s.Deprecated {
				msg := fmt.Sprintf("event type %q is deprecated", p)
				if s.ReplacedBy != "" {
					msg += fmt.Sprintf(" (use %q)", s.ReplacedBy)
				}
				addWarn("deprecated_event", pPath, nodeID, "", msg)
			}
			if !seen[s.Type] {
				seen[s.Type] = true
				matched = append(matched, s)
			}
			continue
		}
		// Wildcard pattern: check at least one registered event matches.
		hits := 0
		for _, s := range allSchemas {
			if events.MatchPattern(p, s.Type) {
				hits++
				if !seen[s.Type] {
					seen[s.Type] = true
					matched = append(matched, s)
				}
			}
		}
		if hits == 0 {
			addWarn("unmatched_event_pattern", pPath, nodeID, "", fmt.Sprintf("event pattern %q matches no registered events", p))
		}
	}
	return matched
}

func resolveSchemasForPatterns(patterns []string, reg *events.Registry) []events.EventSchema {
	if reg == nil || len(patterns) == 0 {
		return nil
	}
	all := reg.List()
	seen := map[string]bool{}
	var out []events.EventSchema
	for _, p := range patterns {
		for _, s := range all {
			if events.MatchPattern(p, s.Type) && !seen[s.Type] {
				seen[s.Type] = true
				out = append(out, s)
			}
		}
	}
	return out
}

func validateConditionGroup(
	g ConditionGroup,
	schemas []events.EventSchema,
	path, nodeID string,
	addErr, addWarn func(code, path, nodeID, edgeID, msg string),
) {
	if g.Op != "" && g.Op != LogicAnd && g.Op != LogicOr {
		addErr("invalid_logic_op", path+".op", nodeID, "", fmt.Sprintf("logic op must be %q or %q, got %q", LogicAnd, LogicOr, g.Op))
	}
	for i, r := range g.Rules {
		rPath := fmt.Sprintf("%s.rules[%d]", path, i)
		validateConditionRule(r, schemas, rPath, nodeID, addErr, addWarn)
	}
	for i, sub := range g.Groups {
		sPath := fmt.Sprintf("%s.groups[%d]", path, i)
		validateConditionGroup(sub, schemas, sPath, nodeID, addErr, addWarn)
	}
}

func validateConditionRule(
	r ConditionRule,
	schemas []events.EventSchema,
	path, nodeID string,
	addErr, addWarn func(code, path, nodeID, edgeID, msg string),
) {
	field := strings.TrimSpace(r.Field)
	if field == "" {
		addErr("missing_field", path+".field", nodeID, "", "condition rule requires a field path")
		return
	}
	if !r.Op.Valid() {
		addErr("invalid_operator", path+".op", nodeID, "", fmt.Sprintf("unknown condition operator %q", r.Op))
		return
	}
	if r.Op.RequiresValue() && r.Value == nil {
		addErr("missing_value", path+".value", nodeID, "", fmt.Sprintf("operator %q requires a comparison value", r.Op))
		return
	}
	if r.Op == OpMatches {
		pat, ok := asString(r.Value)
		if !ok || pat == "" {
			addErr("invalid_regex", path+".value", nodeID, "", "matches operator requires a non-empty string pattern")
			return
		}
		if len(pat) > MaxRegexLen {
			addErr("invalid_regex", path+".value", nodeID, "", fmt.Sprintf("regex exceeds %d bytes", MaxRegexLen))
			return
		}
		if _, err := regexp.Compile(pat); err != nil {
			addErr("invalid_regex", path+".value", nodeID, "", fmt.Sprintf("invalid regex: %v", err))
			return
		}
	}

	// Schema-check payload fields when upstream event schemas are known.
	payloadField, isPayload := extractPayloadFieldPath(field)
	if !isPayload || len(schemas) == 0 {
		return
	}
	var foundFields []events.FieldSchema
	for _, s := range schemas {
		if f, ok := s.Field(payloadField); ok {
			foundFields = append(foundFields, f)
		}
	}
	if len(foundFields) == 0 {
		addErr("unknown_payload_field", path+".field", nodeID, "",
			fmt.Sprintf("field %q does not exist on subscribed event schema(s)", payloadField))
		return
	}
	if len(foundFields) < len(schemas) {
		addWarn("partial_payload_field", path+".field", nodeID, "",
			fmt.Sprintf("field %q only exists on %d of %d subscribed event types", payloadField, len(foundFields), len(schemas)))
	}
	// Check operator & value compatibility against matched field schema(s).
	f := foundFields[0]
	if !OperatorAllowedForField(f.Type, r.Op) {
		addErr("incompatible_operator", path+".op", nodeID, "",
			fmt.Sprintf("operator %q is not valid for %s field %q", r.Op, f.Type, payloadField))
		return
	}
	if r.Op.RequiresValue() {
		validateRuleValueAgainstField(f, r.Op, r.Value, path+".value", nodeID, addErr)
	}
}

func extractPayloadFieldPath(field string) (string, bool) {
	if after, ok := strings.CutPrefix(field, "payload."); ok {
		return after, after != ""
	}
	if after, ok := strings.CutPrefix(field, "event.payload."); ok {
		return after, after != ""
	}
	head, _, _ := strings.Cut(field, ".")
	switch head {
	case "event", "correlation", "vars", "steps", "trigger":
		return "", false
	default:
		return field, true
	}
}

func validateRuleValueAgainstField(
	f events.FieldSchema,
	op Operator,
	val any,
	path, nodeID string,
	addErr func(code, path, nodeID, edgeID, msg string),
) {
	if op == OpIn || op == OpNotIn {
		items, ok := asSlice(val)
		if !ok {
			items = []any{val}
		}
		for _, item := range items {
			validateScalarAgainstField(f, item, path, nodeID, addErr)
		}
		return
	}
	if op == OpContainsAny || op == OpContainsAll {
		items, ok := asSlice(val)
		if !ok {
			addErr("invalid_value_type", path, nodeID, "", fmt.Sprintf("operator %q expects a list value", op))
			return
		}
		for _, item := range items {
			if _, ok := asString(item); !ok {
				addErr("invalid_value_type", path, nodeID, "", "list elements must be strings")
				return
			}
		}
		return
	}
	if f.Type == events.FieldStringList && (op == OpContains || op == OpNotContains) {
		if _, ok := asString(val); !ok {
			addErr("invalid_value_type", path, nodeID, "", fmt.Sprintf("field %q expects a string value", f.Name))
		}
		return
	}
	validateScalarAgainstField(f, val, path, nodeID, addErr)
}

func validateScalarAgainstField(
	f events.FieldSchema,
	val any,
	path, nodeID string,
	addErr func(code, path, nodeID, edgeID, msg string),
) {
	switch f.Type {
	case events.FieldInt, events.FieldFloat, events.FieldDurationMS:
		if _, ok := asFloat(val); !ok {
			addErr("invalid_value_type", path, nodeID, "", fmt.Sprintf("field %q (%s) requires a numeric value", f.Name, f.Type))
		}
	case events.FieldBool:
		if _, ok := asBool(val); !ok {
			addErr("invalid_value_type", path, nodeID, "", fmt.Sprintf("field %q requires a boolean value", f.Name))
		}
	case events.FieldEnum:
		s, ok := asString(val)
		if !ok || !f.HasEnumValue(s) {
			addErr("invalid_enum_value", path, nodeID, "", fmt.Sprintf("value %v is not a valid option for enum field %q", val, f.Name))
		}
	case events.FieldTimestamp:
		if _, ok := asTime(val); !ok {
			if _, okNum := asFloat(val); !okNum {
				addErr("invalid_value_type", path, nodeID, "", fmt.Sprintf("field %q requires an RFC3339 timestamp", f.Name))
			}
		}
	}
}

func validateActionConfig(
	cfg ActionNodeConfig,
	reg ActionRegistry,
	path, nodeID string,
	addErr, addWarn func(code, path, nodeID, edgeID, msg string),
) {
	if strings.TrimSpace(cfg.ActionType) == "" {
		addErr("missing_action_type", path+".actionType", nodeID, "", "action node requires actionType")
		return
	}
	if cfg.Timeout != "" {
		if msg := durationIssue(cfg.Timeout, "action timeout", time.Millisecond, MaxActionTimeout); msg != "" {
			addErr("invalid_timeout", path+".timeout", nodeID, "", msg)
		}
	}
	if cfg.IdempotencyKey != "" {
		if err := ValidateTemplate(cfg.IdempotencyKey); err != nil {
			addErr("invalid_template", path+".idempotencyKey", nodeID, "", err.Error())
		}
	}
	if reg == nil {
		// Still validate templates inside params.
		validateTemplatesInValue(cfg.Params, path+".params", nodeID, addErr)
		return
	}
	spec, ok := reg.Spec(cfg.ActionType)
	if !ok {
		addErr("unknown_action", path+".actionType", nodeID, "", fmt.Sprintf("unknown action %q", cfg.ActionType))
		return
	}
	for _, p := range spec.Params {
		val, present := cfg.Params[p.Key]
		if !present || isEmptyParam(val) {
			if p.Required && p.Default == nil {
				addErr("missing_required_param", path+".params."+p.Key, nodeID, "",
					fmt.Sprintf("action %q requires parameter %q", cfg.ActionType, p.Key))
			}
			continue
		}
		validateActionParamValue(p, val, path+".params."+p.Key, nodeID, addErr, addWarn)
	}
	for k := range cfg.Params {
		if _, known := spec.Param(k); !known {
			addErr("unknown_param", path+".params."+k, nodeID, "",
				fmt.Sprintf("action %q does not accept parameter %q", cfg.ActionType, k))
		}
	}
}

func validateActionParamValue(
	p ParamSchema,
	val any,
	path, nodeID string,
	addErr, addWarn func(code, path, nodeID, edgeID, msg string),
) {
	if s, ok := val.(string); ok && HasTemplateSyntax(s) {
		if err := ValidateTemplate(s); err != nil {
			addErr("invalid_template", path, nodeID, "", err.Error())
			return
		}
		if !p.Template {
			addWarn("unexpected_template", path, nodeID, "", fmt.Sprintf("parameter %q does not advertise template support", p.Key))
		}
		return
	}
	switch p.Type {
	case events.FieldString:
		if _, ok := val.(string); !ok {
			addErr("invalid_param_type", path, nodeID, "", fmt.Sprintf("parameter %q expects a string", p.Key))
		}
	case events.FieldEnum:
		s, ok := val.(string)
		if !ok || !p.HasEnumValue(s) {
			addErr("invalid_enum_value", path, nodeID, "", fmt.Sprintf("parameter %q value %v is not an allowed option", p.Key, val))
		}
	case events.FieldInt, events.FieldDurationMS:
		f, ok := asFloat(val)
		if !ok || f != float64(int64(f)) {
			addErr("invalid_param_type", path, nodeID, "", fmt.Sprintf("parameter %q expects an integer", p.Key))
		}
	case events.FieldFloat:
		if _, ok := asFloat(val); !ok {
			addErr("invalid_param_type", path, nodeID, "", fmt.Sprintf("parameter %q expects a number", p.Key))
		}
	case events.FieldBool:
		if _, ok := asBool(val); !ok {
			addErr("invalid_param_type", path, nodeID, "", fmt.Sprintf("parameter %q expects a boolean", p.Key))
		}
	case events.FieldStringList:
		items, ok := asSlice(val)
		if !ok {
			addErr("invalid_param_type", path, nodeID, "", fmt.Sprintf("parameter %q expects a list of strings", p.Key))
			return
		}
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				addErr("invalid_param_type", path, nodeID, "", fmt.Sprintf("parameter %q elements must be strings", p.Key))
				return
			}
			if HasTemplateSyntax(s) {
				if err := ValidateTemplate(s); err != nil {
					addErr("invalid_template", path, nodeID, "", err.Error())
					return
				}
			}
		}
	case events.FieldObject:
		if _, ok := val.(map[string]any); !ok {
			addErr("invalid_param_type", path, nodeID, "", fmt.Sprintf("parameter %q expects an object", p.Key))
			return
		}
		validateTemplatesInValue(val, path, nodeID, addErr)
	}
}

func validateTemplatesInValue(v any, path, nodeID string, addErr func(code, path, nodeID, edgeID, msg string)) {
	switch x := v.(type) {
	case string:
		if HasTemplateSyntax(x) {
			if err := ValidateTemplate(x); err != nil {
				addErr("invalid_template", path, nodeID, "", err.Error())
			}
		}
	case map[string]any:
		for k, item := range x {
			validateTemplatesInValue(item, path+"."+k, nodeID, addErr)
		}
	case []any:
		for i, item := range x {
			validateTemplatesInValue(item, fmt.Sprintf("%s[%d]", path, i), nodeID, addErr)
		}
	}
}

func isEmptyParam(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

func appendUnique(dst []string, items ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, s := range dst {
		seen[s] = true
	}
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			dst = append(dst, item)
		}
	}
	return dst
}

// validateCron checks standard 5-field cron ("min hour dom month dow") and
// standard descriptor aliases (@hourly, @daily, @midnight, @weekly, @monthly).
func validateCron(expr string) error {
	expr = strings.TrimSpace(expr)
	switch expr {
	case "@hourly", "@daily", "@midnight", "@weekly", "@monthly":
		return nil
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("cron expression must have 5 fields (min hour dom month dow)")
	}
	bounds := [5][2]int{
		{0, 59}, // minute
		{0, 23}, // hour
		{1, 31}, // day of month
		{1, 12}, // month
		{0, 7},  // day of week (0 and 7 both Sunday)
	}
	for i, field := range fields {
		if err := validateCronField(field, bounds[i][0], bounds[i][1]); err != nil {
			return fmt.Errorf("cron field %d (%q): %w", i+1, field, err)
		}
	}
	return nil
}

func validateCronField(field string, minVal, maxVal int) error {
	for _, seg := range strings.Split(field, ",") {
		if seg == "" {
			return fmt.Errorf("empty list segment")
		}
		base, stepStr, hasStep := strings.Cut(seg, "/")
		if hasStep {
			step, err := strconv.Atoi(stepStr)
			if err != nil || step <= 0 || step > maxVal {
				return fmt.Errorf("invalid step %q", stepStr)
			}
		}
		if base == "*" {
			continue
		}
		if loStr, hiStr, isRange := strings.Cut(base, "-"); isRange {
			lo, err1 := strconv.Atoi(loStr)
			hi, err2 := strconv.Atoi(hiStr)
			if err1 != nil || err2 != nil || lo < minVal || hi > maxVal || lo > hi {
				return fmt.Errorf("invalid range %q", base)
			}
			continue
		}
		n, err := strconv.Atoi(base)
		if err != nil || n < minVal || n > maxVal {
			return fmt.Errorf("value %q out of range [%d..%d]", base, minVal, maxVal)
		}
	}
	return nil
}

// templateParamRe matches a template-gallery placeholder ({{ param.key }}).
// Those are substituted when a template is instantiated; one left in a
// saved flow renders empty at run time.
var templateParamRe = regexp.MustCompile(`\{\{\s*param\.([A-Za-z0-9_.-]+)\s*\}\}`)

// unresolvedTemplateParams lists the template parameter keys still
// referenced by a node config, sorted and de-duplicated.
func unresolvedTemplateParams(raw json.RawMessage) []string {
	if len(raw) == 0 || !bytes.Contains(raw, []byte("param.")) {
		return nil
	}
	var keys []string
	for _, m := range templateParamRe.FindAllSubmatch(raw, -1) {
		keys = appendUnique(keys, string(m[1]))
	}
	sort.Strings(keys)
	return keys
}
