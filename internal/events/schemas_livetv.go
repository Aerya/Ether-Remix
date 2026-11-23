package events

import "time"

// Canonical Live TV event type names.
const (
	EventLiveTVChannelTuned         = "livetv.channel.tuned"
	EventLiveTVChannelStopped       = "livetv.channel.stopped"
	EventLiveTVInputStalled         = "livetv.input.stalled"
	EventLiveTVInputRecovered       = "livetv.input.recovered"
	EventLiveTVInputLost            = "livetv.input.lost"
	EventLiveTVSourceFailover       = "livetv.source.failover"
	EventLiveTVSourceDegraded       = "livetv.source.degraded"
	EventLiveTVSourceCooldown       = "livetv.source.cooldown"
	EventLiveTVProviderAuthFailed   = "livetv.provider.auth_failed"
	EventLiveTVProviderLimitReached = "livetv.provider.limit_reached"
	EventLiveTVGuideRefreshed       = "livetv.guide.refreshed"
	EventLiveTVGuideFailed          = "livetv.guide.failed"
)

// LiveTVChannelTunedPayload is emitted when a viewer tunes a Live TV channel.
type LiveTVChannelTunedPayload struct {
	ChannelID     string `json:"channelId" event:"label=Channel ID;example=ch_espn"`
	ChannelName   string `json:"channelName" event:"label=Channel name;example=ESPN HD"`
	ChannelNumber string `json:"channelNumber,omitempty" event:"label=Channel number;example=206"`
	SourceID      string `json:"sourceId,omitempty" event:"label=Stream source ID;example=src_primary"`
	ProviderID    string `json:"providerId,omitempty" event:"label=Provider ID;example=prov_iptv_1"`
	Mode          string `json:"mode,omitempty" event:"label=Playout mode;example=broadcast"`
	ViewerID      int64  `json:"viewerId,omitempty" event:"label=Viewer ID;example=42"`
	Username      string `json:"username,omitempty" event:"label=Viewer username;pii;example=alex"`
	Client        string `json:"client,omitempty" event:"label=Client;example=Vela tvOS"`
}

// LiveTVChannelStoppedPayload is emitted when a Live TV channel session ends.
type LiveTVChannelStoppedPayload struct {
	ChannelID   string `json:"channelId" event:"label=Channel ID;example=ch_espn"`
	ChannelName string `json:"channelName" event:"label=Channel name;example=ESPN HD"`
	SourceID    string `json:"sourceId,omitempty" event:"label=Stream source ID;example=src_primary"`
	ViewerID    int64  `json:"viewerId,omitempty" event:"label=Viewer ID;example=42"`
	WatchedMS   int64  `json:"watchedMs" event:"label=Watched duration;duration_ms;unit=ms;example=1800000"`
	BytesServed int64  `json:"bytesServed,omitempty" event:"label=Bytes served;unit=bytes;example=640000000"`
	Mode        string `json:"mode,omitempty" event:"label=Playout mode;example=broadcast"`
}

// LiveTVInputStalledPayload is emitted when an upstream live input stalls.
type LiveTVInputStalledPayload struct {
	ChannelID      string `json:"channelId" event:"label=Channel ID;example=ch_espn"`
	ChannelName    string `json:"channelName,omitempty" event:"label=Channel name;example=ESPN HD"`
	SourceID       string `json:"sourceId,omitempty" event:"label=Stream source ID;example=src_primary"`
	Transport      string `json:"transport,omitempty" event:"label=Upstream transport;example=hls"`
	DataAgeMS      int64  `json:"dataAgeMs" event:"label=Data age;duration_ms;unit=ms;example=8500"`
	ReserveDepthMS int64  `json:"reserveDepthMs" event:"label=Reserve depth;duration_ms;unit=ms;example=2000"`
	StallAfterMS   int64  `json:"stallAfterMs" event:"label=Stall threshold;duration_ms;unit=ms;example=8000"`
	Reconnects     int    `json:"reconnects" event:"label=Reconnect attempts;example=1"`
}

// LiveTVInputRecoveredPayload is emitted when a stalled live input resumes delivering media.
type LiveTVInputRecoveredPayload struct {
	ChannelID   string `json:"channelId" event:"label=Channel ID;example=ch_espn"`
	ChannelName string `json:"channelName,omitempty" event:"label=Channel name;example=ESPN HD"`
	SourceID    string `json:"sourceId,omitempty" event:"label=Stream source ID;example=src_primary"`
	Transport   string `json:"transport,omitempty" event:"label=Upstream transport;example=hls"`
	DowntimeMS  int64  `json:"downtimeMs" event:"label=Downtime;duration_ms;unit=ms;example=4200"`
	Reconnects  int    `json:"reconnects" event:"label=Reconnect attempts;example=1"`
}

// LiveTVInputLostPayload is emitted when a live channel input fails terminally.
type LiveTVInputLostPayload struct {
	ChannelID   string `json:"channelId" event:"label=Channel ID;example=ch_espn"`
	ChannelName string `json:"channelName,omitempty" event:"label=Channel name;example=ESPN HD"`
	SourceID    string `json:"sourceId,omitempty" event:"label=Stream source ID;example=src_primary"`
	Transport   string `json:"transport,omitempty" event:"label=Upstream transport;example=hls"`
	Reconnects  int    `json:"reconnects" event:"label=Reconnect attempts;example=3"`
	Error       string `json:"error" event:"label=Error message;example=upstream EOF after reconnect limit"`
	HasBackup   bool   `json:"hasBackup" event:"label=Backup source available;example=true"`
}

// LiveTVSourceFailoverPayload is emitted when a live channel switches to another source.
type LiveTVSourceFailoverPayload struct {
	ChannelID    string `json:"channelId" event:"label=Channel ID;example=ch_espn"`
	ChannelName  string `json:"channelName,omitempty" event:"label=Channel name;example=ESPN HD"`
	FromSourceID string `json:"fromSourceId,omitempty" event:"label=Previous source ID;example=src_primary"`
	ToSourceID   string `json:"toSourceId,omitempty" event:"label=Next source ID;example=src_backup"`
	Reason       string `json:"reason,omitempty" event:"label=Failover reason;example=input stalled"`
	Mode         string `json:"mode,omitempty" event:"label=Playout mode;example=broadcast"`
}

// LiveTVSourceDegradedPayload is emitted when a channel source shows sustained health degradation.
type LiveTVSourceDegradedPayload struct {
	ChannelID        string  `json:"channelId" event:"label=Channel ID;example=ch_espn"`
	ChannelName      string  `json:"channelName,omitempty" event:"label=Channel name;example=ESPN HD"`
	SourceID         string  `json:"sourceId" event:"label=Stream source ID;example=src_primary"`
	DamagePerHour    float64 `json:"damagePerHour" event:"label=Damage per hour;example=6.5"`
	StallsPerHour    float64 `json:"stallsPerHour" event:"label=Stalls per hour;example=4.2"`
	MaxKeyframeGapMS int64   `json:"maxKeyframeGapMs" event:"label=Max keyframe gap;duration_ms;unit=ms;example=9200"`
	MediaSec         float64 `json:"mediaSec" event:"label=Observed media duration;unit=s;example=1800"`
}

// LiveTVSourceCooldownPayload is emitted when a stream source enters backoff cooldown.
type LiveTVSourceCooldownPayload struct {
	ChannelID  string     `json:"channelId" event:"label=Channel ID;example=ch_espn"`
	SourceID   string     `json:"sourceId" event:"label=Stream source ID;example=src_primary"`
	Failures   int        `json:"failures" event:"label=Consecutive failures;example=2"`
	ErrorClass string     `json:"errorClass,omitempty" event:"label=Error class;example=timeout"`
	RetryAfter *time.Time `json:"retryAfter,omitempty" event:"label=Retry after;example=2026-11-19T12:05:00Z"`
}

// LiveTVProviderAuthFailedPayload is emitted when a Live TV provider rejects credentials.
type LiveTVProviderAuthFailedPayload struct {
	ProviderID   string `json:"providerId" event:"label=Provider ID;example=prov_iptv_1"`
	ProviderName string `json:"providerName,omitempty" event:"label=Provider name;example=Primary IPTV"`
	AccountID    string `json:"accountId,omitempty" event:"label=Account ID;example=acct_1"`
	StatusCode   int    `json:"statusCode,omitempty" event:"label=HTTP status code;example=401"`
	Error        string `json:"error" event:"label=Error message;example=provider rejected credentials"`
}

// LiveTVProviderLimitReachedPayload is emitted when a provider's concurrent tuner limit is saturated.
type LiveTVProviderLimitReachedPayload struct {
	ProviderID        string `json:"providerId" event:"label=Provider ID;example=prov_iptv_1"`
	ProviderName      string `json:"providerName,omitempty" event:"label=Provider name;example=Primary IPTV"`
	ActiveConnections int    `json:"activeConnections" event:"label=Active connections;example=2"`
	MaxConnections    int    `json:"maxConnections" event:"label=Max connections;example=2"`
	ChannelID         string `json:"channelId,omitempty" event:"label=Requested channel ID;example=ch_espn"`
}

// LiveTVGuideRefreshedPayload is emitted when a Live TV guide/source refresh completes.
type LiveTVGuideRefreshedPayload struct {
	SourceID   string `json:"sourceId" event:"label=Source ID;example=prov_iptv_1"`
	SourceName string `json:"sourceName,omitempty" event:"label=Source name;example=Primary IPTV"`
	Channels   int    `json:"channels" event:"label=Channels;example=240"`
	Programs   int    `json:"programs" event:"label=Programs;example=14800"`
	DurationMS int64  `json:"durationMs" event:"label=Refresh duration;duration_ms;unit=ms;example=3250"`
}

// LiveTVGuideFailedPayload is emitted when a Live TV guide/source refresh fails.
type LiveTVGuideFailedPayload struct {
	SourceID   string  `json:"sourceId" event:"label=Source ID;example=prov_iptv_1"`
	SourceName string  `json:"sourceName,omitempty" event:"label=Source name;example=Primary IPTV"`
	Error      string  `json:"error" event:"label=Error message;example=HTTP 502 fetching XMLTV guide"`
	StaleHours float64 `json:"staleHours,omitempty" event:"label=Guide staleness;unit=h;example=14.5"`
}

// RegisterLiveTVSchemas registers all built-in Live TV event schemas.
func RegisterLiveTVSchemas(r *Registry) {
	registerMediaSchema[LiveTVChannelTunedPayload](r, EventSchema{
		Type:        EventLiveTVChannelTuned,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV channel tuned",
		Description: "Emitted when a viewer tunes a Live TV channel.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[LiveTVChannelStoppedPayload](r, EventSchema{
		Type:        EventLiveTVChannelStopped,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV channel stopped",
		Description: "Emitted when a Live TV viewing session stops.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[LiveTVInputStalledPayload](r, EventSchema{
		Type:        EventLiveTVInputStalled,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV input stalled",
		Description: "Emitted when an upstream live channel feed stops advancing.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[LiveTVInputRecoveredPayload](r, EventSchema{
		Type:        EventLiveTVInputRecovered,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV input recovered",
		Description: "Emitted when a stalled upstream live feed resumes delivering data.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[LiveTVInputLostPayload](r, EventSchema{
		Type:        EventLiveTVInputLost,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV input lost",
		Description: "Emitted when an upstream live feed fails and cannot recover.",
		Owner:       OwnerBuiltin,
		Critical:    true,
	})
	registerMediaSchema[LiveTVSourceFailoverPayload](r, EventSchema{
		Type:        EventLiveTVSourceFailover,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV source failover",
		Description: "Emitted when a live channel fails over to a backup stream source.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[LiveTVSourceDegradedPayload](r, EventSchema{
		Type:        EventLiveTVSourceDegraded,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV source degraded",
		Description: "Emitted when a live stream source accumulates sustained stalls or discontinuity damage.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[LiveTVSourceCooldownPayload](r, EventSchema{
		Type:        EventLiveTVSourceCooldown,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV source cooldown",
		Description: "Emitted when a live stream source enters failure cooldown.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[LiveTVProviderAuthFailedPayload](r, EventSchema{
		Type:        EventLiveTVProviderAuthFailed,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV provider authentication failed",
		Description: "Emitted when a Live TV provider rejects authentication credentials.",
		Owner:       OwnerBuiltin,
		Critical:    true,
	})
	registerMediaSchema[LiveTVProviderLimitReachedPayload](r, EventSchema{
		Type:        EventLiveTVProviderLimitReached,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV provider connection limit reached",
		Description: "Emitted when a Live TV provider has no free concurrent stream slots.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[LiveTVGuideRefreshedPayload](r, EventSchema{
		Type:        EventLiveTVGuideRefreshed,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV guide refreshed",
		Description: "Emitted when a Live TV source finishes refreshing its lineup or guide.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[LiveTVGuideFailedPayload](r, EventSchema{
		Type:        EventLiveTVGuideFailed,
		Version:     1,
		Domain:      "livetv",
		Label:       "Live TV guide refresh failed",
		Description: "Emitted when a Live TV guide or lineup refresh fails.",
		Owner:       OwnerBuiltin,
	})
}

func init() {
	registerBuiltin(RegisterLiveTVSchemas)
}
