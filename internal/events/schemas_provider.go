package events

// Canonical provider (Usenet & debrid) event type names.
const (
	EventProviderServiceOutage        = "provider.service.outage"
	EventProviderServiceRecovered     = "provider.service.recovered"
	EventProviderAuthRejected         = "provider.auth.rejected"
	EventProviderQuotaWarning         = "provider.quota.warning"
	EventProviderQuotaExhausted       = "provider.quota.exhausted"
	EventProviderDebridCached         = "provider.debrid.cached"
	EventProviderDebridFailed         = "provider.debrid.failed"
	EventProviderConnectionsSaturated = "provider.connections.saturated"
)

// ProviderServiceOutagePayload is emitted when a Usenet or debrid provider enters outage backoff.
type ProviderServiceOutagePayload struct {
	ProviderID   string `json:"providerId" event:"label=Provider ID;example=usenet_primary"`
	ProviderName string `json:"providerName" event:"label=Provider name;example=Newshosting"`
	Kind         string `json:"kind" event:"label=Provider kind;example=usenet"`
	Error        string `json:"error" event:"label=Error message;example=connection refused"`
	BackoffSec   int    `json:"backoffSec" event:"label=Backoff duration;unit=s;example=60"`
}

// ProviderServiceRecoveredPayload is emitted when a provider recovers from outage or auth failure.
type ProviderServiceRecoveredPayload struct {
	ProviderID       string `json:"providerId" event:"label=Provider ID;example=usenet_primary"`
	ProviderName     string `json:"providerName" event:"label=Provider name;example=Newshosting"`
	Kind             string `json:"kind" event:"label=Provider kind;example=usenet"`
	OutageDurationMS int64  `json:"outageDurationMs" event:"label=Outage duration;duration_ms;unit=ms;example=180000"`
}

// ProviderAuthRejectedPayload is emitted when a provider rejects credentials or an API key.
type ProviderAuthRejectedPayload struct {
	ProviderID   string `json:"providerId" event:"label=Provider ID;example=debrid_rd"`
	ProviderName string `json:"providerName" event:"label=Provider name;example=Real-Debrid"`
	Kind         string `json:"kind" event:"label=Provider kind;example=realdebrid"`
	StatusCode   int    `json:"statusCode,omitempty" event:"label=Status code;example=401"`
	Error        string `json:"error" event:"label=Error message;example=invalid API token"`
}

// ProviderQuotaWarningPayload is emitted when a provider quota approaches exhaustion.
type ProviderQuotaWarningPayload struct {
	ProviderID   string  `json:"providerId" event:"label=Provider ID;example=debrid_rd"`
	ProviderName string  `json:"providerName" event:"label=Provider name;example=Real-Debrid"`
	Kind         string  `json:"kind" event:"label=Provider kind;example=realdebrid"`
	Metric       string  `json:"metric" event:"label=Quota metric;example=active_downloads"`
	Used         int64   `json:"used" event:"label=Used quota;example=8"`
	Limit        int64   `json:"limit" event:"label=Quota limit;example=10"`
	PercentUsed  float64 `json:"percentUsed" event:"label=Percent used;unit=%;example=80"`
}

// ProviderQuotaExhaustedPayload is emitted when a provider quota or active download limit is exhausted.
type ProviderQuotaExhaustedPayload struct {
	ProviderID   string `json:"providerId" event:"label=Provider ID;example=debrid_rd"`
	ProviderName string `json:"providerName" event:"label=Provider name;example=Real-Debrid"`
	Kind         string `json:"kind" event:"label=Provider kind;example=realdebrid"`
	Metric       string `json:"metric" event:"label=Quota metric;example=active_downloads"`
	Used         int64  `json:"used" event:"label=Used quota;example=10"`
	Limit        int64  `json:"limit" event:"label=Quota limit;example=10"`
}

// ProviderDebridCachedPayload is emitted when a debrid torrent becomes cached and ready.
type ProviderDebridCachedPayload struct {
	ProviderID   string `json:"providerId" event:"label=Provider ID;example=debrid_rd"`
	ProviderName string `json:"providerName" event:"label=Provider name;example=Real-Debrid"`
	InfoHash     string `json:"infoHash" event:"label=Torrent info hash;example=0123456789abcdef0123456789abcdef01234567"`
	TitleKey     string `json:"titleKey,omitempty" event:"label=Title key;example=movie:603"`
	TitleName    string `json:"titleName,omitempty" event:"label=Title name;example=The Matrix"`
	SizeBytes    int64  `json:"sizeBytes,omitempty" event:"label=File size;unit=bytes;example=18400000000"`
	WaitMS       int64  `json:"waitMs" event:"label=Wait duration;duration_ms;unit=ms;example=1450"`
}

// ProviderDebridFailedPayload is emitted when a debrid acquisition or uncached download fails.
type ProviderDebridFailedPayload struct {
	ProviderID   string `json:"providerId,omitempty" event:"label=Provider ID;example=debrid_rd"`
	ProviderName string `json:"providerName,omitempty" event:"label=Provider name;example=Real-Debrid"`
	InfoHash     string `json:"infoHash" event:"label=Torrent info hash;example=0123456789abcdef0123456789abcdef01234567"`
	TitleKey     string `json:"titleKey,omitempty" event:"label=Title key;example=movie:603"`
	Error        string `json:"error" event:"label=Error message;example=provider refused torrent"`
}

// ProviderConnectionsSaturatedPayload is emitted when all provider connection slots are occupied with waiting readers.
type ProviderConnectionsSaturatedPayload struct {
	ProviderID        string `json:"providerId" event:"label=Provider ID;example=usenet_primary"`
	ProviderName      string `json:"providerName" event:"label=Provider name;example=Newshosting"`
	ActiveConnections int    `json:"activeConnections" event:"label=Active connections;example=20"`
	MaxConnections    int    `json:"maxConnections" event:"label=Max connections;example=20"`
	WaitingReaders    int    `json:"waitingReaders" event:"label=Waiting readers;example=4"`
}

// RegisterProviderSchemas registers all built-in provider event schemas.
func RegisterProviderSchemas(r *Registry) {
	registerMediaSchema[ProviderServiceOutagePayload](r, EventSchema{
		Type:        EventProviderServiceOutage,
		Version:     1,
		Domain:      "provider",
		Label:       "Provider outage",
		Description: "Emitted when a Usenet or debrid provider enters outage backoff.",
		Owner:       OwnerBuiltin,
		Critical:    true,
		LegacyNames: []string{"provider.outage"},
	})
	registerMediaSchema[ProviderServiceRecoveredPayload](r, EventSchema{
		Type:        EventProviderServiceRecovered,
		Version:     1,
		Domain:      "provider",
		Label:       "Provider recovered",
		Description: "Emitted when a Usenet or debrid provider recovers from an outage.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"provider.recovered"},
	})
	registerMediaSchema[ProviderAuthRejectedPayload](r, EventSchema{
		Type:        EventProviderAuthRejected,
		Version:     1,
		Domain:      "provider",
		Label:       "Provider authentication rejected",
		Description: "Emitted when a Usenet or debrid provider rejects authentication credentials.",
		Owner:       OwnerBuiltin,
		Critical:    true,
	})
	registerMediaSchema[ProviderQuotaWarningPayload](r, EventSchema{
		Type:        EventProviderQuotaWarning,
		Version:     1,
		Domain:      "provider",
		Label:       "Provider quota warning",
		Description: "Emitted when a provider quota approaches its configured limit.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[ProviderQuotaExhaustedPayload](r, EventSchema{
		Type:        EventProviderQuotaExhausted,
		Version:     1,
		Domain:      "provider",
		Label:       "Provider quota exhausted",
		Description: "Emitted when a provider exhausts its quota or active download slots.",
		Owner:       OwnerBuiltin,
		Critical:    true,
	})
	registerMediaSchema[ProviderDebridCachedPayload](r, EventSchema{
		Type:        EventProviderDebridCached,
		Version:     1,
		Domain:      "provider",
		Label:       "Debrid release cached",
		Description: "Emitted when a torrent release is cached and ready on a debrid provider.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[ProviderDebridFailedPayload](r, EventSchema{
		Type:        EventProviderDebridFailed,
		Version:     1,
		Domain:      "provider",
		Label:       "Debrid acquisition failed",
		Description: "Emitted when a debrid torrent acquisition or download fails.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[ProviderConnectionsSaturatedPayload](r, EventSchema{
		Type:        EventProviderConnectionsSaturated,
		Version:     1,
		Domain:      "provider",
		Label:       "Provider connections saturated",
		Description: "Emitted when all provider connections are in use and readers are queued.",
		Owner:       OwnerBuiltin,
	})
}

func init() {
	registerBuiltin(RegisterProviderSchemas)
}
