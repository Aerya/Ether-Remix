// Package flows defines Ether's automation graph model, static validator,
// typed condition evaluator, safe template renderer, action/trigger registry
// contracts, durable runtime interfaces, and database migrations.
package flows

import (
	"encoding/json"
	"fmt"
	"time"
)

// SchemaVersion is the current wire format version for Graph JSON.
const SchemaVersion = 1

// Graph and runtime safety bounds.
const (
	DefaultMaxNodes                = 100
	DefaultMaxEdges                = 250
	DefaultMaxGraphBytes           = 256 << 10
	DefaultMaxIDLen                = 64
	DefaultMaxSteps                = 100
	HardMaxSteps                   = 250
	DefaultRateLimitPerMin         = 60
	HardRateLimitPerMin            = 600
	DefaultConcurrency             = 4
	HardConcurrency                = 32
	MaxFlowDepth                   = 3
	DefaultConsecutiveFailureLimit = 10
	DefaultMaxRunDuration          = 7 * 24 * time.Hour
	HardMaxRunDuration             = 30 * 24 * time.Hour
	MaxActionTimeout               = 5 * time.Minute
	MaxDelayDuration               = 30 * 24 * time.Hour
	MaxWindowDuration              = 7 * 24 * time.Hour
	MaxRegexLen                    = 512
)

// NodeKind identifies the behaviour of one node in a flow graph.
type NodeKind string

const (
	NodeTriggerEvent    NodeKind = "trigger.event"
	NodeTriggerSchedule NodeKind = "trigger.schedule"
	NodeTriggerWebhook  NodeKind = "trigger.webhook"
	NodeTriggerManual   NodeKind = "trigger.manual"

	NodeCondition NodeKind = "condition"

	NodeLogicSwitch    NodeKind = "logic.switch"
	NodeLogicDelay     NodeKind = "logic.delay"
	NodeLogicWaitEvent NodeKind = "logic.wait_event"
	NodeLogicWindow    NodeKind = "logic.window"
	NodeLogicDedupe    NodeKind = "logic.dedupe"

	NodeAction NodeKind = "action"
)

// Standard output handle names.
const (
	HandleNext       = "next"
	HandleError      = "error"
	HandleTrue       = "true"
	HandleFalse      = "false"
	HandleDefault    = "default"
	HandleMatched    = "matched"
	HandleTimeout    = "timeout"
	HandleReached    = "reached"
	HandleBelow      = "below"
	HandlePass       = "pass"
	HandleSuppressed = "suppressed"
)

// IsTrigger reports whether k is an entry trigger node kind.
func (k NodeKind) IsTrigger() bool {
	switch k {
	case NodeTriggerEvent, NodeTriggerSchedule, NodeTriggerWebhook, NodeTriggerManual:
		return true
	default:
		return false
	}
}

// IsValid reports whether k is a recognized built-in NodeKind.
func (k NodeKind) IsValid() bool {
	switch k {
	case NodeTriggerEvent, NodeTriggerSchedule, NodeTriggerWebhook, NodeTriggerManual,
		NodeCondition,
		NodeLogicSwitch, NodeLogicDelay, NodeLogicWaitEvent, NodeLogicWindow, NodeLogicDedupe,
		NodeAction:
		return true
	default:
		return false
	}
}

// Category groups node kinds for console palettes and diagnostics.
func (k NodeKind) Category() string {
	switch {
	case k.IsTrigger():
		return "trigger"
	case k == NodeCondition:
		return "condition"
	case k == NodeAction:
		return "action"
	case k.IsValid():
		return "logic"
	default:
		return "unknown"
	}
}

// Flow is a saved automation definition plus operational summary metadata.
type Flow struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	Description         string     `json:"description,omitempty"`
	Enabled             bool       `json:"enabled"`
	DisabledReason      string     `json:"disabledReason,omitempty"`
	Version             int        `json:"version"`
	Graph               Graph      `json:"graph"`
	Limits              FlowLimits `json:"limits,omitempty"`
	ConsecutiveFailures int        `json:"consecutiveFailures,omitempty"`
	TemplateID          string     `json:"templateId,omitempty"`
	Tags                []string   `json:"tags,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	LastRunAt           time.Time  `json:"lastRunAt,omitzero"`
	LastStatus          RunStatus  `json:"lastStatus,omitempty"`
}

// FlowLimits configures per-flow safety and scheduling guardrails.
// Zero values take package defaults; negative AutoDisableAfter disables the circuit breaker.
type FlowLimits struct {
	RateLimitPerMin   int  `json:"rateLimitPerMin,omitempty"`
	MaxSteps          int  `json:"maxSteps,omitempty"`
	Concurrency       int  `json:"concurrency,omitempty"`
	OrderedByKey      bool `json:"orderedByKey,omitempty"`
	AutoDisableAfter  int  `json:"autoDisableAfter,omitempty"`
	MaxRunDurationSec int  `json:"maxRunDurationSec,omitempty"`
}

// Effective returns l with defaults applied and values clamped to hard ceilings.
func (l FlowLimits) Effective() FlowLimits {
	out := l
	if out.RateLimitPerMin <= 0 {
		out.RateLimitPerMin = DefaultRateLimitPerMin
	}
	if out.RateLimitPerMin > HardRateLimitPerMin {
		out.RateLimitPerMin = HardRateLimitPerMin
	}
	if out.MaxSteps <= 0 {
		out.MaxSteps = DefaultMaxSteps
	}
	if out.MaxSteps > HardMaxSteps {
		out.MaxSteps = HardMaxSteps
	}
	if out.Concurrency <= 0 {
		out.Concurrency = DefaultConcurrency
	}
	if out.Concurrency > HardConcurrency {
		out.Concurrency = HardConcurrency
	}
	if out.AutoDisableAfter == 0 {
		out.AutoDisableAfter = DefaultConsecutiveFailureLimit
	}
	if out.MaxRunDurationSec <= 0 {
		out.MaxRunDurationSec = int(DefaultMaxRunDuration / time.Second)
	}
	if maxSec := int(HardMaxRunDuration / time.Second); out.MaxRunDurationSec > maxSec {
		out.MaxRunDurationSec = maxSec
	}
	return out
}

// Graph is the versioned DAG executed by the runtime and edited in the console.
type Graph struct {
	SchemaVersion int    `json:"schemaVersion"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}

// Position holds visual canvas coordinates for the console editor.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Node is one trigger, condition, logic step, or action in a Graph.
type Node struct {
	ID       string          `json:"id"`
	Kind     NodeKind        `json:"kind"`
	Label    string          `json:"label,omitempty"`
	Position Position        `json:"position"`
	Config   json.RawMessage `json:"config,omitempty"`
	Disabled bool            `json:"disabled,omitempty"`
}

// Edge connects one output handle of Source to Target.
type Edge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceHandle string `json:"sourceHandle,omitempty"`
}

// DecodeConfig unmarshals n.Config into T.
func DecodeConfig[T any](n Node) (T, error) {
	var cfg T
	if len(n.Config) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(n.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("flows: decode %s config for node %q: %w", n.Kind, n.ID, err)
	}
	return cfg, nil
}

// MustMarshalConfig encodes v as json.RawMessage for tests and template builders.
func MustMarshalConfig(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("flows: marshal node config: %v", err))
	}
	return b
}

// EventTriggerConfig configures a trigger.event node.
type EventTriggerConfig struct {
	Events []string        `json:"events"`
	Filter *ConditionGroup `json:"filter,omitempty"`
}

// ScheduleTriggerConfig configures a trigger.schedule node.
// Exactly one of Cron or Every must be provided.
type ScheduleTriggerConfig struct {
	Cron     string `json:"cron,omitempty"`
	Every    string `json:"every,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// WebhookTriggerConfig configures a trigger.webhook node.
type WebhookTriggerConfig struct {
	SecretHeader   string   `json:"secretHeader,omitempty"`
	RequireAuth    bool     `json:"requireAuth,omitempty"`
	AllowedMethods []string `json:"allowedMethods,omitempty"`
}

// ManualTriggerConfig configures a trigger.manual node.
type ManualTriggerConfig struct {
	Inputs          []ParamSchema `json:"inputs,omitempty"`
	SampleEventType string        `json:"sampleEventType,omitempty"`
}

// ConditionNodeConfig configures a condition node.
type ConditionNodeConfig struct {
	Condition ConditionGroup `json:"condition"`
}

// SwitchCase is one named branch in a logic.switch node.
type SwitchCase struct {
	Key       string         `json:"key"`
	Label     string         `json:"label,omitempty"`
	Condition ConditionGroup `json:"condition"`
}

// SwitchNodeConfig configures a logic.switch node.
type SwitchNodeConfig struct {
	Cases    []SwitchCase `json:"cases"`
	MatchAll bool         `json:"matchAll,omitempty"`
}

// DelayNodeConfig configures a logic.delay node.
// Exactly one of Duration, DurationField, or Until must be set.
type DelayNodeConfig struct {
	Duration      string `json:"duration,omitempty"`
	DurationField string `json:"durationField,omitempty"`
	Until         string `json:"until,omitempty"`
}

// WaitEventNodeConfig configures a logic.wait_event node ("wait until event or timeout").
type WaitEventNodeConfig struct {
	EventTypes       []string        `json:"eventTypes"`
	MatchCorrelation bool            `json:"matchCorrelation,omitempty"`
	Filter           *ConditionGroup `json:"filter,omitempty"`
	Timeout          string          `json:"timeout"`
}

// WindowNodeConfig configures a logic.window node ("N times in T", per key).
type WindowNodeConfig struct {
	KeyTemplate  string `json:"keyTemplate,omitempty"`
	Threshold    int    `json:"threshold"`
	Window       string `json:"window"`
	ResetOnReach bool   `json:"resetOnReach,omitempty"`
}

// DedupeNodeConfig configures a logic.dedupe node (cooldown / throttle per key).
type DedupeNodeConfig struct {
	KeyTemplate  string `json:"keyTemplate,omitempty"`
	Cooldown     string `json:"cooldown"`
	MaxPerWindow int    `json:"maxPerWindow,omitempty"` // 0 treated as 1
}

// ActionNodeConfig configures an action node.
type ActionNodeConfig struct {
	ActionType      string         `json:"actionType"`
	Params          map[string]any `json:"params,omitempty"`
	ContinueOnError bool           `json:"continueOnError,omitempty"`
	IdempotencyKey  string         `json:"idempotencyKey,omitempty"`
	Timeout         string         `json:"timeout,omitempty"`
}

// SubscribedEventPatterns returns the distinct event patterns referenced by
// trigger.event and logic.wait_event nodes in g.
func (g Graph) SubscribedEventPatterns() []string {
	seen := make(map[string]bool)
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, n := range g.Nodes {
		if n.Disabled {
			continue
		}
		switch n.Kind {
		case NodeTriggerEvent:
			if cfg, err := DecodeConfig[EventTriggerConfig](n); err == nil {
				for _, ev := range cfg.Events {
					add(ev)
				}
			}
		case NodeLogicWaitEvent:
			if cfg, err := DecodeConfig[WaitEventNodeConfig](n); err == nil {
				for _, ev := range cfg.EventTypes {
					add(ev)
				}
			}
		}
	}
	return out
}
