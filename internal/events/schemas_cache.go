package events

// Canonical cache event type names.
const (
	EventCacheBudgetFull        = "cache.budget.full"
	EventCacheBudgetRecovered   = "cache.budget.recovered"
	EventCacheEvictionSpike     = "cache.eviction.spike"
	EventCachePrefetchCompleted = "cache.prefetch.completed"
	EventCachePrefetchFailed    = "cache.prefetch.failed"
)

// CacheBudgetFullPayload is emitted when cache usage reaches or exceeds its configured budget.
type CacheBudgetFullPayload struct {
	CacheKind   string  `json:"cacheKind" event:"label=Cache kind;example=segment"`
	UsedBytes   int64   `json:"usedBytes" event:"label=Used bytes;unit=bytes;example=53687091200"`
	BudgetBytes int64   `json:"budgetBytes" event:"label=Budget bytes;unit=bytes;example=53687091200"`
	PercentUsed float64 `json:"percentUsed" event:"label=Percent used;unit=%;example=100"`
}

// CacheBudgetRecoveredPayload is emitted when cache usage falls back below the full threshold.
type CacheBudgetRecoveredPayload struct {
	CacheKind   string  `json:"cacheKind" event:"label=Cache kind;example=segment"`
	UsedBytes   int64   `json:"usedBytes" event:"label=Used bytes;unit=bytes;example=45097156608"`
	BudgetBytes int64   `json:"budgetBytes" event:"label=Budget bytes;unit=bytes;example=53687091200"`
	PercentUsed float64 `json:"percentUsed" event:"label=Percent used;unit=%;example=84"`
}

// CacheEvictionSpikePayload is emitted when cache eviction volume spikes over a short window.
type CacheEvictionSpikePayload struct {
	CacheKind      string `json:"cacheKind" event:"label=Cache kind;example=segment"`
	EvictedBytes   int64  `json:"evictedBytes" event:"label=Evicted bytes;unit=bytes;example=2147483648"`
	EvictedEntries int    `json:"evictedEntries" event:"label=Evicted entries;example=512"`
	WindowSec      int    `json:"windowSec" event:"label=Window duration;unit=s;example=60"`
}

// CachePrefetchCompletedPayload is emitted when a prefetch job finishes warming data.
type CachePrefetchCompletedPayload struct {
	TitleKey    string `json:"titleKey,omitempty" event:"label=Title key;example=episode:12345"`
	TitleName   string `json:"titleName,omitempty" event:"label=Title name;example=Severance S01E02"`
	Kind        string `json:"kind" event:"label=Prefetch kind;example=next_episode"`
	BytesWarmed int64  `json:"bytesWarmed,omitempty" event:"label=Bytes warmed;unit=bytes;example=67108864"`
	DurationMS  int64  `json:"durationMs" event:"label=Prefetch duration;duration_ms;unit=ms;example=2350"`
}

// CachePrefetchFailedPayload is emitted when a prefetch job fails.
type CachePrefetchFailedPayload struct {
	TitleKey  string `json:"titleKey,omitempty" event:"label=Title key;example=episode:12345"`
	TitleName string `json:"titleName,omitempty" event:"label=Title name;example=Severance S01E02"`
	Kind      string `json:"kind" event:"label=Prefetch kind;example=next_episode"`
	Error     string `json:"error" event:"label=Error message;example=indexer budget exhausted"`
}

// RegisterCacheSchemas registers all built-in cache event schemas.
func RegisterCacheSchemas(r *Registry) {
	registerMediaSchema[CacheBudgetFullPayload](r, EventSchema{
		Type:        EventCacheBudgetFull,
		Version:     1,
		Domain:      "cache",
		Label:       "Cache budget full",
		Description: "Emitted when cache usage reaches or exceeds its configured budget.",
		Owner:       OwnerBuiltin,
		Critical:    true,
		LegacyNames: []string{"cache.full"},
	})
	registerMediaSchema[CacheBudgetRecoveredPayload](r, EventSchema{
		Type:        EventCacheBudgetRecovered,
		Version:     1,
		Domain:      "cache",
		Label:       "Cache budget recovered",
		Description: "Emitted when cache usage drops back below the full threshold.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"cache.recovered"},
	})
	registerMediaSchema[CacheEvictionSpikePayload](r, EventSchema{
		Type:        EventCacheEvictionSpike,
		Version:     1,
		Domain:      "cache",
		Label:       "Cache eviction spike",
		Description: "Emitted when cache eviction volume spikes within a short window.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[CachePrefetchCompletedPayload](r, EventSchema{
		Type:        EventCachePrefetchCompleted,
		Version:     1,
		Domain:      "cache",
		Label:       "Cache prefetch completed",
		Description: "Emitted when a background cache prefetch job completes.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[CachePrefetchFailedPayload](r, EventSchema{
		Type:        EventCachePrefetchFailed,
		Version:     1,
		Domain:      "cache",
		Label:       "Cache prefetch failed",
		Description: "Emitted when a background cache prefetch job fails.",
		Owner:       OwnerBuiltin,
	})
}

func init() {
	registerBuiltin(RegisterCacheSchemas)
}
