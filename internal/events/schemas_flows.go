package events

import "time"

const (
	EventFlowsRunFailed        = "flows.run.failed"
	EventFlowsFlowAutoDisabled = "flows.flow.auto_disabled"
	EventFlowsBusDropped       = "flows.bus.dropped"
	EventFlowsDigestScheduled  = "flows.digest.scheduled"
)

type FlowsRunFailedPayload struct {
	FlowID              string `json:"flowId" event:"label=Flow ID;desc=Identifier of the flow whose run failed;example=flow_notify_discord"`
	FlowName            string `json:"flowName" event:"label=Flow Name;desc=Display name of the flow;example=Notify Discord on New Releases"`
	RunID               string `json:"runId" event:"label=Run ID;desc=Identifier of the failed flow execution;example=run_01JABC"`
	NodeID              string `json:"nodeId,omitempty" event:"label=Node ID;desc=Graph node that failed;example=node_webhook_1"`
	Error               string `json:"error" event:"label=Error;desc=Error message reported by the failed node;example=webhook returned HTTP 500"`
	ConsecutiveFailures int    `json:"consecutiveFailures" event:"label=Consecutive Failures;desc=Count of consecutive failed runs for this flow;example=3"`
}

type FlowsFlowAutoDisabledPayload struct {
	FlowID              string `json:"flowId" event:"label=Flow ID;desc=Identifier of the disabled flow;example=flow_notify_discord"`
	FlowName            string `json:"flowName" event:"label=Flow Name;desc=Display name of the disabled flow;example=Notify Discord on New Releases"`
	ConsecutiveFailures int    `json:"consecutiveFailures" event:"label=Consecutive Failures;desc=Failure count that tripped auto-disable;example=20"`
	LastError           string `json:"lastError" event:"label=Last Error;desc=Final error before the flow was auto-disabled;example=webhook returned HTTP 500"`
}

type FlowsBusDroppedPayload struct {
	Subscriber   string `json:"subscriber" event:"label=Subscriber;desc=Name of the slow event bus subscriber;example=flows-engine"`
	EventType    string `json:"eventType" event:"label=Event Type;desc=Type of the event that was dropped;example=library.title.added"`
	DroppedTotal uint64 `json:"droppedTotal" event:"label=Dropped Total;desc=Cumulative count of dropped events for this subscriber;example=1"`
	Capacity     int    `json:"capacity" event:"label=Capacity;desc=Buffer capacity of the subscriber channel;example=256"`
}

type FlowsDigestScheduledPayload struct {
	Period        string    `json:"period" event:"label=Period;desc=Digest schedule cadence;example=weekly"`
	WindowStart   time.Time `json:"windowStart,omitzero" event:"label=Window Start;desc=Start of the summarized activity window;example=2026-11-12T00:00:00Z"`
	WindowEnd     time.Time `json:"windowEnd,omitzero" event:"label=Window End;desc=End of the summarized activity window;example=2026-11-19T00:00:00Z"`
	TotalPlays    int       `json:"totalPlays" event:"label=Total Plays;desc=Total completed plays during the window;example=42"`
	UniqueViewers int       `json:"uniqueViewers" event:"label=Unique Viewers;desc=Count of distinct viewers active during the window;example=4"`
}

func init() {
	registerBuiltin(RegisterFlowsSchemas)
}

// RegisterFlowsSchemas registers all flows meta-domain event schemas on r.
func RegisterFlowsSchemas(r *Registry) {
	windowStart := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)

	mustRegisterBuiltin[FlowsRunFailedPayload](r, EventSchema{
		Type:        EventFlowsRunFailed,
		Label:       "Flow Run Failed",
		Description: "A flow execution failed on one of its steps.",
		Sample: FlowsRunFailedPayload{
			FlowID:              "flow_notify_discord",
			FlowName:            "Notify Discord on New Releases",
			RunID:               "run_01JABC",
			NodeID:              "node_webhook_1",
			Error:               "webhook returned HTTP 500",
			ConsecutiveFailures: 3,
		},
	})

	mustRegisterBuiltin[FlowsFlowAutoDisabledPayload](r, EventSchema{
		Type:        EventFlowsFlowAutoDisabled,
		Label:       "Flow Auto-Disabled",
		Description: "A flow exceeded its consecutive failure threshold and was automatically paused.",
		Critical:    true,
		Sample: FlowsFlowAutoDisabledPayload{
			FlowID:              "flow_notify_discord",
			FlowName:            "Notify Discord on New Releases",
			ConsecutiveFailures: 20,
			LastError:           "webhook returned HTTP 500",
		},
	})

	mustRegisterBuiltin[FlowsBusDroppedPayload](r, EventSchema{
		Type:        EventFlowsBusDropped,
		Label:       "Event Bus Dropped Event",
		Description: "A non-blocking event bus subscriber overflowed its buffer and dropped an event.",
		Sample: FlowsBusDroppedPayload{
			Subscriber:   "flows-engine",
			EventType:    EventLibraryTitleAdded,
			DroppedTotal: 1,
			Capacity:     256,
		},
	})

	mustRegisterBuiltin[FlowsDigestScheduledPayload](r, EventSchema{
		Type:        EventFlowsDigestScheduled,
		Label:       "Digest Scheduled",
		Description: "A periodic household activity digest window completed.",
		LegacyNames: []string{"newsletter.weekly"},
		Sample: FlowsDigestScheduledPayload{
			Period:        "weekly",
			WindowStart:   windowStart,
			WindowEnd:     windowEnd,
			TotalPlays:    42,
			UniqueViewers: 4,
		},
	})
}
