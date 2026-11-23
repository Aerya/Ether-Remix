package flows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
)

// Common sentinel errors returned by Store and Runtime implementations.
var (
	ErrNotFound         = errors.New("flows: not found")
	ErrFlowDisabled     = errors.New("flows: flow is disabled")
	ErrRateLimited      = errors.New("flows: rate limit exceeded")
	ErrInvalidWebhook   = errors.New("flows: invalid webhook token")
	ErrNoManualTrigger  = errors.New("flows: flow has no manual trigger")
	ErrRunNotActive     = errors.New("flows: run is not active")
	ErrLoopDepthLimit   = errors.New("flows: event recursion depth limit reached")
	ErrValidationFailed = errors.New("flows: validation failed")
)

// RunStatus tracks the durable lifecycle state of a single flow run.
type RunStatus string

const (
	RunPending   RunStatus = "pending"
	RunRunning   RunStatus = "running"
	RunWaiting   RunStatus = "waiting"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
	RunThrottled RunStatus = "throttled"
)

// Terminal reports whether s is a finished run state.
func (s RunStatus) Terminal() bool {
	switch s {
	case RunCompleted, RunFailed, RunCancelled, RunThrottled:
		return true
	default:
		return false
	}
}

// StepStatus tracks the outcome of one node execution inside a run.
type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepCompleted StepStatus = "completed"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
	// StepUncertain marks a non-idempotent side-effecting action whose node
	// crashed mid-execution so recovery never blindly double-executes it.
	StepUncertain StepStatus = "uncertain"
)

// Run is one durable execution of a Flow.
type Run struct {
	ID             string          `json:"id"`
	FlowID         string          `json:"flowId"`
	FlowName       string          `json:"flowName,omitempty"`
	FlowVersion    int             `json:"flowVersion"`
	TriggerKind    NodeKind        `json:"triggerKind"`
	EventID        string          `json:"eventId,omitempty"`
	EventType      string          `json:"eventType,omitempty"`
	CorrelationKey string          `json:"correlationKey,omitempty"`
	Status         RunStatus       `json:"status"`
	Attempt        int             `json:"attempt"`
	DryRun         bool            `json:"dryRun,omitempty"`
	Trigger        json.RawMessage `json:"trigger,omitempty"`
	Context        json.RawMessage `json:"context,omitempty"`
	Error          string          `json:"error,omitempty"`
	StepCount      int             `json:"stepCount"`
	FlowDepth      int             `json:"flowDepth,omitempty"`
	StartedAt      time.Time       `json:"startedAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	FinishedAt     time.Time       `json:"finishedAt,omitzero"`
	WakeAt         time.Time       `json:"wakeAt,omitzero"`
	LeaseOwner     string          `json:"-"`
	LeaseUntil     time.Time       `json:"-"`
}

// RunStep is one recorded node execution inside a Run.
type RunStep struct {
	ID             int64           `json:"id"`
	RunID          string          `json:"runId"`
	Seq            int             `json:"seq"`
	NodeID         string          `json:"nodeId"`
	NodeKind       NodeKind        `json:"nodeKind"`
	Status         StepStatus      `json:"status"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	Branch         string          `json:"branch,omitempty"`
	Input          json.RawMessage `json:"input,omitempty"`
	Output         json.RawMessage `json:"output,omitempty"`
	Error          string          `json:"error,omitempty"`
	StartedAt      time.Time       `json:"startedAt"`
	FinishedAt     time.Time       `json:"finishedAt,omitzero"`
	DurationMS     int64           `json:"durationMs"`
}

// Waiter is a durable suspension created by a logic.wait_event node.
type Waiter struct {
	ID             string          `json:"id"`
	RunID          string          `json:"runId"`
	FlowID         string          `json:"flowId"`
	NodeID         string          `json:"nodeId"`
	EventType      string          `json:"eventType"`
	CorrelationKey string          `json:"correlationKey,omitempty"`
	Filter         json.RawMessage `json:"filter,omitempty"`
	TimeoutAt      time.Time       `json:"timeoutAt"`
	CreatedAt      time.Time       `json:"createdAt"`
	Status         string          `json:"status"` // waiting | matched | timed_out | cancelled
}

// Template is a curated starter flow exposed in the console template gallery.
type Template struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Category     string        `json:"category"`
	Icon         string        `json:"icon,omitempty"`
	NotifyPreset string        `json:"notifyPreset,omitempty"`
	Params       []ParamSchema `json:"params,omitempty"`
	Graph        Graph         `json:"graph"`
	Limits       FlowLimits    `json:"limits,omitempty"`
}

// RunFilter scopes Run listing queries for the console API.
type RunFilter struct {
	FlowID    string
	Status    RunStatus
	EventType string
	Limit     int
	Cursor    string
}

// TestRunRequest configures POST /api/flows/{id}/test.
type TestRunRequest struct {
	FlowID  string          `json:"flowId,omitempty"`
	Graph   *Graph          `json:"graph,omitempty"` // optional unsaved draft override
	DryRun  bool            `json:"dryRun"`
	Event   events.Envelope `json:"event,omitempty"`
	Inputs  map[string]any  `json:"inputs,omitempty"`
	Trigger NodeKind        `json:"trigger,omitempty"`
}

// TestRunResult returns the complete trace of a synchronous test run.
type TestRunResult struct {
	Run   Run       `json:"run"`
	Steps []RunStep `json:"steps"`
}

// Store is the durable persistence contract shared by the Flow runtime and
// console HTTP API (implemented over SQLite and PostgreSQL using flows/1).
type Store interface {
	ListFlows(ctx context.Context) ([]Flow, error)
	GetFlow(ctx context.Context, id string) (*Flow, error)
	SaveFlow(ctx context.Context, f Flow) (*Flow, error)
	DeleteFlow(ctx context.Context, id string) error
	SetFlowEnabled(ctx context.Context, id string, enabled bool, reason string) error
	SetWebhookTokenHash(ctx context.Context, flowID, tokenHash string) error
	FindFlowByWebhookHash(ctx context.Context, tokenHash string) (*Flow, error)

	CreateRun(ctx context.Context, run Run) error
	UpdateRun(ctx context.Context, run Run) error
	GetRun(ctx context.Context, runID string) (*Run, error)
	ListRuns(ctx context.Context, filter RunFilter) ([]Run, string, error)
	RecordStep(ctx context.Context, step RunStep) (int64, error)
	UpdateStep(ctx context.Context, step RunStep) error
	ListRunSteps(ctx context.Context, runID string) ([]RunStep, error)

	ClaimDueRuns(ctx context.Context, owner string, now time.Time, lease time.Duration, limit int) ([]Run, error)
	CreateWaiter(ctx context.Context, w Waiter) error
	MatchWaiters(ctx context.Context, eventType, correlationKey string, now time.Time) ([]Waiter, error)
	ExpireWaiters(ctx context.Context, now time.Time, limit int) ([]Waiter, error)

	// RecordWindowEvent records one timestamp for logic.window and returns the
	// count of events inside [now-window, now].
	RecordWindowEvent(ctx context.Context, flowID, nodeID, groupKey string, at time.Time, window time.Duration, resetAt int) (count int, reached bool, err error)
	// CheckDedupe records/checks logic.dedupe state and reports whether this key passes.
	CheckDedupe(ctx context.Context, flowID, nodeID, dedupeKey string, at time.Time, cooldown time.Duration, maxPerWindow int) (allowed bool, hits int, err error)

	PruneHistory(ctx context.Context, olderThan time.Time, maxRuns int) (int64, error)
}

// Runtime drives event ingestion, schedules, durable step execution, restart
// recovery, and test runs. In clustered mode, Start runs inside cluster.RunAsLeader.
type Runtime interface {
	Start(ctx context.Context) error
	TriggerManual(ctx context.Context, flowID string, inputs map[string]any) (*Run, error)
	TriggerWebhook(ctx context.Context, token string, payload map[string]any, headers map[string]string) (*Run, error)
	TestRun(ctx context.Context, req TestRunRequest) (*TestRunResult, error)
	CancelRun(ctx context.Context, runID string) error
	SubscribedEvents(ctx context.Context) ([]string, error)
}

// IdempotencyKey derives a deterministic, replay-safe action idempotency key
// for one step attempt inside a run.
func IdempotencyKey(runID, nodeID string, attempt int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("ether-flow:%s:%s:%d", runID, nodeID, attempt)))
	return "idem_" + hex.EncodeToString(sum[:16])
}
