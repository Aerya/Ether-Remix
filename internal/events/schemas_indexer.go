package events

import "time"

// Canonical indexer event type names.
const (
	EventIndexerRequestFailed    = "indexer.request.failed"
	EventIndexerServiceOutage    = "indexer.service.outage"
	EventIndexerServiceRecovered = "indexer.service.recovered"
	EventIndexerBudgetWarning    = "indexer.budget.warning"
	EventIndexerBudgetExhausted  = "indexer.budget.exhausted"
	EventIndexerRateLimited      = "indexer.rate.limited"
)

// IndexerRequestFailedPayload is emitted when an indexer search or fetch request fails.
type IndexerRequestFailedPayload struct {
	IndexerID   string `json:"indexerId" event:"label=Indexer ID;example=idx_nzbgeek"`
	IndexerName string `json:"indexerName" event:"label=Indexer name;example=NZBGeek"`
	Kind        string `json:"kind" event:"label=Request kind;example=search"`
	StatusCode  int    `json:"statusCode,omitempty" event:"label=HTTP status code;example=503"`
	Error       string `json:"error" event:"label=Error message;example=indexer returned HTTP 503"`
	LatencyMS   int64  `json:"latencyMs,omitempty" event:"label=Latency;duration_ms;unit=ms;example=1240"`
}

// IndexerServiceOutagePayload is emitted when an indexer trips its circuit breaker.
type IndexerServiceOutagePayload struct {
	IndexerID           string `json:"indexerId" event:"label=Indexer ID;example=idx_nzbgeek"`
	IndexerName         string `json:"indexerName" event:"label=Indexer name;example=NZBGeek"`
	Kind                string `json:"kind,omitempty" event:"label=Indexer kind;example=newznab"`
	ConsecutiveFailures int    `json:"consecutiveFailures" event:"label=Consecutive failures;example=3"`
	Error               string `json:"error" event:"label=Error message;example=connection timed out"`
}

// IndexerServiceRecoveredPayload is emitted when an indexer recovers from an outage.
type IndexerServiceRecoveredPayload struct {
	IndexerID        string `json:"indexerId" event:"label=Indexer ID;example=idx_nzbgeek"`
	IndexerName      string `json:"indexerName" event:"label=Indexer name;example=NZBGeek"`
	Kind             string `json:"kind,omitempty" event:"label=Indexer kind;example=newznab"`
	OutageDurationMS int64  `json:"outageDurationMs" event:"label=Outage duration;duration_ms;unit=ms;example=305000"`
}

// IndexerBudgetWarningPayload is emitted when an indexer's daily hit or grab budget crosses the warning threshold.
type IndexerBudgetWarningPayload struct {
	IndexerID   string  `json:"indexerId" event:"label=Indexer ID;example=idx_nzbgeek"`
	IndexerName string  `json:"indexerName" event:"label=Indexer name;example=NZBGeek"`
	Metric      string  `json:"metric" event:"label=Budget metric;enum=hits|grabs;example=hits"`
	Used        int     `json:"used" event:"label=Used count;example=800"`
	Limit       int     `json:"limit" event:"label=Daily limit;example=1000"`
	PercentUsed float64 `json:"percentUsed" event:"label=Percent used;unit=%;example=80"`
}

// IndexerBudgetExhaustedPayload is emitted when an indexer's daily budget is exhausted.
type IndexerBudgetExhaustedPayload struct {
	IndexerID   string     `json:"indexerId" event:"label=Indexer ID;example=idx_nzbgeek"`
	IndexerName string     `json:"indexerName" event:"label=Indexer name;example=NZBGeek"`
	Metric      string     `json:"metric" event:"label=Budget metric;enum=hits|grabs;example=hits"`
	Used        int        `json:"used" event:"label=Used count;example=1000"`
	Limit       int        `json:"limit" event:"label=Daily limit;example=1000"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty" event:"label=Budget resets at;example=2026-11-20T00:00:00Z"`
}

// IndexerRateLimitedPayload is emitted when an indexer returns a rate-limit response.
type IndexerRateLimitedPayload struct {
	IndexerID    string `json:"indexerId" event:"label=Indexer ID;example=idx_nzbgeek"`
	IndexerName  string `json:"indexerName" event:"label=Indexer name;example=NZBGeek"`
	RetryAfterMS int64  `json:"retryAfterMs" event:"label=Retry after;duration_ms;unit=ms;example=60000"`
	StatusCode   int    `json:"statusCode,omitempty" event:"label=HTTP status code;example=429"`
}

// RegisterIndexerSchemas registers all built-in indexer event schemas.
func RegisterIndexerSchemas(r *Registry) {
	registerMediaSchema[IndexerRequestFailedPayload](r, EventSchema{
		Type:        EventIndexerRequestFailed,
		Version:     1,
		Domain:      "indexer",
		Label:       "Indexer request failed",
		Description: "Emitted when an individual indexer API or fetch request fails.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[IndexerServiceOutagePayload](r, EventSchema{
		Type:        EventIndexerServiceOutage,
		Version:     1,
		Domain:      "indexer",
		Label:       "Indexer outage",
		Description: "Emitted when an indexer enters outage backoff after repeated failures.",
		Owner:       OwnerBuiltin,
		Critical:    true,
		LegacyNames: []string{"indexer.outage"},
	})
	registerMediaSchema[IndexerServiceRecoveredPayload](r, EventSchema{
		Type:        EventIndexerServiceRecovered,
		Version:     1,
		Domain:      "indexer",
		Label:       "Indexer recovered",
		Description: "Emitted when an indexer recovers after an outage.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"indexer.recovered"},
	})
	registerMediaSchema[IndexerBudgetWarningPayload](r, EventSchema{
		Type:        EventIndexerBudgetWarning,
		Version:     1,
		Domain:      "indexer",
		Label:       "Indexer budget warning",
		Description: "Emitted when an indexer approaches its daily API or grab budget.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[IndexerBudgetExhaustedPayload](r, EventSchema{
		Type:        EventIndexerBudgetExhausted,
		Version:     1,
		Domain:      "indexer",
		Label:       "Indexer budget exhausted",
		Description: "Emitted when an indexer exhausts its daily API or grab budget.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"indexer.budget"},
	})
	registerMediaSchema[IndexerRateLimitedPayload](r, EventSchema{
		Type:        EventIndexerRateLimited,
		Version:     1,
		Domain:      "indexer",
		Label:       "Indexer rate limited",
		Description: "Emitted when an indexer responds with a rate-limit pause.",
		Owner:       OwnerBuiltin,
	})
}

func init() {
	registerBuiltin(RegisterIndexerSchemas)
}
