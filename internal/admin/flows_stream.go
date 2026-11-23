package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
)

// FlowChangedEvent is the JSON payload carried by admin.StreamFlowsChanged
// ("flows.changed") console SSE events.
type FlowChangedEvent struct {
	Action        string    `json:"action"` // created | updated | deleted | enabled | disabled | token_rotated | auto_disabled
	FlowID        string    `json:"flowId"`
	Name          string    `json:"name,omitempty"`
	Version       int       `json:"version,omitempty"`
	Enabled       *bool     `json:"enabled,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	CancelledRuns int       `json:"cancelledRuns,omitempty"`
	At            time.Time `json:"at,omitzero"`
}

// FlowRunEvent is the JSON payload carried by admin.StreamFlowRun
// ("flows.run") console SSE events.
type FlowRunEvent struct {
	RunID      string          `json:"runId"`
	FlowID     string          `json:"flowId"`
	FlowName   string          `json:"flowName,omitempty"`
	EventType  string          `json:"eventType,omitempty"`
	Status     flows.RunStatus `json:"status"`
	DryRun     bool            `json:"dryRun,omitempty"`
	StepCount  int             `json:"stepCount,omitempty"`
	Step       *flows.RunStep  `json:"step,omitempty"`
	Error      string          `json:"error,omitempty"`
	StartedAt  time.Time       `json:"startedAt,omitzero"`
	UpdatedAt  time.Time       `json:"updatedAt,omitzero"`
	FinishedAt time.Time       `json:"finishedAt,omitzero"`
}

// PublishFlowChanged pushes a flows.changed SSE event to hub without blocking.
func PublishFlowChanged(hub *StreamHub, ev FlowChangedEvent) error {
	if hub == nil {
		return nil
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = hub.Publish(StreamFlowsChanged, raw)
	return err
}

// PublishFlowRun pushes a flows.run SSE event to hub without blocking.
func PublishFlowRun(hub *StreamHub, ev FlowRunEvent) error {
	if hub == nil {
		return nil
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = hub.Publish(StreamFlowRun, raw)
	return err
}

// BridgeFlowEventsToStream subscribes asynchronously to flows.* events on bus
// and forwards them to the console SSE publisher (e.g. consoleStreamRuntime.Publish
// or a StreamHub wrapper). Publishers are never blocked.
func BridgeFlowEventsToStream(ctx context.Context, bus *events.Bus, publish func(context.Context, string, any) error) *events.Subscription {
	if bus == nil || publish == nil {
		return nil
	}
	return bus.SubscribeFunc(ctx, events.SubscriptionOptions{
		Name:     "flows-console-stream",
		Patterns: []string{"flows.*"},
	}, func(subCtx context.Context, env events.Envelope) {
		var payload map[string]any
		if len(env.Payload) > 0 {
			_ = json.Unmarshal(env.Payload, &payload)
		}
		str := func(key string) string {
			if payload == nil {
				return ""
			}
			v, _ := payload[key].(string)
			return v
		}
		switch env.Type {
		case "flows.flow.auto_disabled":
			enabled := false
			_ = publish(subCtx, StreamFlowsChanged, FlowChangedEvent{
				Action:  "auto_disabled",
				FlowID:  str("flowId"),
				Name:    str("flowName"),
				Enabled: &enabled,
				Reason:  str("reason"),
				At:      env.OccurredAt,
			})
		case "flows.run.started", "flows.run.completed", "flows.run.failed", "flows.run.cancelled", "flows.run.throttled", "flows.run.waiting":
			status := flows.RunStatus(strings.TrimPrefix(env.Type, "flows.run."))
			if status == "started" {
				status = flows.RunRunning
			}
			if rawStatus := str("status"); rawStatus != "" {
				status = flows.RunStatus(rawStatus)
			}
			_ = publish(subCtx, StreamFlowRun, FlowRunEvent{
				RunID:     str("runId"),
				FlowID:    str("flowId"),
				FlowName:  str("flowName"),
				EventType: str("eventType"),
				Status:    status,
				Error:     str("error"),
				UpdatedAt: env.OccurredAt,
			})
		}
	})
}

// flowStreamMeta is the minimal subset extracted from a StreamEvent payload to
// apply optional flowId / runId filters without re-allocating the full struct.
type flowStreamMeta struct {
	FlowID string `json:"flowId"`
	RunID  string `json:"runId"`
}

func matchesFlowStreamFilter(ev StreamEvent, flowID, runID string) bool {
	if ev.Type != StreamFlowsChanged && ev.Type != StreamFlowRun {
		return false
	}
	if runID != "" && ev.Type != StreamFlowRun {
		return false
	}
	if flowID == "" && runID == "" {
		return true
	}
	var meta flowStreamMeta
	if err := json.Unmarshal(ev.Data, &meta); err != nil {
		return false
	}
	if flowID != "" && meta.FlowID != flowID {
		return false
	}
	if runID != "" && meta.RunID != runID {
		return false
	}
	return true
}

// FlowsStreamHandler serves filtered Server-Sent Events for flow definition
// changes (flows.changed) and run progress (flows.run) using the shared admin
// StreamHub patterns: Last-Event-ID resume, retry directive, bounded queue,
// slow-consumer eviction, and periodic heartbeat comments.
func FlowsStreamHandler(hub *StreamHub, defaultFlowID, defaultRunID string) http.Handler {
	return flowsStreamHandlerWithHeartbeat(hub, defaultFlowID, defaultRunID, streamHeartbeat)
}

func flowsStreamHandlerWithHeartbeat(hub *StreamHub, defaultFlowID, defaultRunID string, heartbeat time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return
		}
		if hub == nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "flow live stream is not configured")
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming_unsupported", "this server cannot stream events")
			return
		}
		var lastID uint64
		if raw := strings.TrimSpace(r.Header.Get("Last-Event-ID")); raw != "" {
			lastID, _ = strconv.ParseUint(raw, 10, 64)
		} else if raw := strings.TrimSpace(r.URL.Query().Get("lastEventId")); raw != "" {
			lastID, _ = strconv.ParseUint(raw, 10, 64)
		}
		flowID := strings.TrimSpace(defaultFlowID)
		if flowID == "" {
			flowID = strings.TrimSpace(r.URL.Query().Get("flowId"))
		}
		runID := strings.TrimSpace(defaultRunID)
		if runID == "" {
			runID = strings.TrimSpace(r.URL.Query().Get("runId"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "retry: 10000\n\n")
		fl.Flush()

		serveFilteredStreamEvents(w, r, hub, lastID, heartbeat, fl, func(ev StreamEvent) bool {
			return matchesFlowStreamFilter(ev, flowID, runID)
		})
	})
}

func serveFilteredStreamEvents(w http.ResponseWriter, r *http.Request, hub *StreamHub, lastID uint64, heartbeat time.Duration, fl http.Flusher, accept func(StreamEvent) bool) {
	ch, cancel := hub.Subscribe(lastID)
	defer cancel()
	if heartbeat <= 0 {
		heartbeat = streamHeartbeat
	}
	beat := time.NewTicker(heartbeat)
	defer beat.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			_, _ = fmt.Fprintf(w, ": heartbeat %d\n\n", time.Now().Unix())
			fl.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if accept != nil && !accept(ev) {
				continue
			}
			writeStreamEvent(w, ev)
			fl.Flush()
		}
	}
}
