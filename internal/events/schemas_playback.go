package events

// Canonical playback event type names.
const (
	EventPlaybackSessionStarted       = "playback.session.started"
	EventPlaybackSessionPaused        = "playback.session.paused"
	EventPlaybackSessionResumed       = "playback.session.resumed"
	EventPlaybackSessionStopped       = "playback.session.stopped"
	EventPlaybackSessionCompleted     = "playback.session.completed"
	EventPlaybackSessionFailed        = "playback.session.failed"
	EventPlaybackSessionTerminated    = "playback.session.terminated"
	EventPlaybackSourceFailover       = "playback.source.failover"
	EventPlaybackStreamStalled        = "playback.stream.stalled"
	EventPlaybackStreamDecision       = "playback.stream.decision"
	EventPlaybackStreamQualityChanged = "playback.stream.quality_changed"
	EventPlaybackStreamSeeked         = "playback.stream.seeked"
	EventPlaybackMarkerEntered        = "playback.marker.entered"
	EventPlaybackMarkerExited         = "playback.marker.exited"
	EventPlaybackConcurrencyExceeded  = "playback.concurrency.exceeded"
	EventPlaybackAdmissionDenied      = "playback.admission.denied"
	EventPlaybackTranscodeFallback    = "playback.transcode.fallback"
)

// PlaybackSourceContext describes the underlying source media being played.
type PlaybackSourceContext struct {
	Container         string `json:"container,omitempty" event:"label=Source container;desc=Container format of the source file;example=mkv"`
	VideoCodec        string `json:"videoCodec,omitempty" event:"label=Source video codec;desc=Codec of the source video track;example=hevc"`
	VideoResolution   string `json:"videoResolution,omitempty" event:"label=Source resolution;desc=Normalized resolution tier of the source video;enum=4k|1080p|720p|480p;example=4k"`
	VideoWidth        int    `json:"videoWidth,omitempty" event:"label=Source video width;unit=px;example=3840"`
	VideoHeight       int    `json:"videoHeight,omitempty" event:"label=Source video height;unit=px;example=2160"`
	VideoDynamicRange string `json:"videoDynamicRange,omitempty" event:"label=Source dynamic range;desc=HDR or SDR dynamic range of the source video;enum=sdr|hdr10|dv|hlg;example=hdr10"`
	AudioCodec        string `json:"audioCodec,omitempty" event:"label=Source audio codec;example=truehd"`
	AudioChannels     int    `json:"audioChannels,omitempty" event:"label=Source audio channels;example=8"`
	BitrateKbps       int    `json:"bitrateKbps,omitempty" event:"label=Source bitrate;unit=kbps;example=48500"`
	FileSizeBytes     int64  `json:"fileSizeBytes,omitempty" event:"label=Source file size;unit=bytes;example=38400000000"`
}

// PlaybackStreamContext describes the active delivery pipeline for a playback session.
type PlaybackStreamContext struct {
	Delivery          string  `json:"delivery,omitempty" event:"label=Delivery mode;desc=Overall stream delivery mode;enum=direct_play|remux|transcode;example=transcode"`
	VideoDecision     string  `json:"videoDecision,omitempty" event:"label=Video decision;enum=direct_play|copy|transcode;example=transcode"`
	AudioDecision     string  `json:"audioDecision,omitempty" event:"label=Audio decision;enum=direct_play|copy|transcode;example=copy"`
	SubtitleDecision  string  `json:"subtitleDecision,omitempty" event:"label=Subtitle decision;enum=none|direct_play|copy|sidecar|burn|transcode;example=none"`
	Container         string  `json:"container,omitempty" event:"label=Stream container;example=hls"`
	VideoCodec        string  `json:"videoCodec,omitempty" event:"label=Stream video codec;example=h264"`
	VideoResolution   string  `json:"videoResolution,omitempty" event:"label=Stream resolution;enum=4k|1080p|720p|480p;example=1080p"`
	VideoHeight       int     `json:"videoHeight,omitempty" event:"label=Stream video height;unit=px;example=1080"`
	VideoDynamicRange string  `json:"videoDynamicRange,omitempty" event:"label=Stream dynamic range;enum=sdr|hdr10|dv|hlg;example=sdr"`
	AudioCodec        string  `json:"audioCodec,omitempty" event:"label=Stream audio codec;example=aac"`
	AudioChannels     int     `json:"audioChannels,omitempty" event:"label=Stream audio channels;example=6"`
	BitrateKbps       int     `json:"bitrateKbps,omitempty" event:"label=Stream bitrate;unit=kbps;example=8000"`
	BandwidthKbps     int     `json:"bandwidthKbps,omitempty" event:"label=Estimated bandwidth;unit=kbps;example=9200"`
	HWDecode          bool    `json:"hwDecode,omitempty" event:"label=Hardware decode;example=true"`
	HWEncode          bool    `json:"hwEncode,omitempty" event:"label=Hardware encode;example=true"`
	TranscodeSpeed    float64 `json:"transcodeSpeed,omitempty" event:"label=Transcode speed;unit=x;example=2.4"`
	Throttled         bool    `json:"throttled,omitempty" event:"label=Transcode throttled;example=false"`
}

// PlaybackViewerContext describes the viewer and their current concurrency state.
type PlaybackViewerContext struct {
	ID                int64    `json:"id,omitempty" event:"label=Viewer ID;example=42"`
	Username          string   `json:"username,omitempty" event:"label=Viewer username;pii;example=alex"`
	Role              string   `json:"role,omitempty" event:"label=Viewer role;enum=owner|viewer;example=viewer"`
	Tags              []string `json:"tags,omitempty" event:"label=Viewer tags;example=household,4k"`
	ActiveStreams     int      `json:"activeStreams,omitempty" event:"label=Viewer active streams;example=1"`
	ActiveTranscodes  int      `json:"activeTranscodes,omitempty" event:"label=Viewer active transcodes;example=1"`
	DistinctActiveIPs int      `json:"distinctActiveIPs,omitempty" event:"label=Viewer distinct IPs;example=1"`
}

// PlaybackClientContext describes the playback client device and protocol.
type PlaybackClientContext struct {
	ID       string `json:"id,omitempty" event:"label=Client ID;example=appletv-living-room"`
	Name     string `json:"name,omitempty" event:"label=Client name;example=Living Room Apple TV"`
	Platform string `json:"platform,omitempty" event:"label=Client platform;example=tvOS"`
	Product  string `json:"product,omitempty" event:"label=Client product;example=Vela"`
	Version  string `json:"version,omitempty" event:"label=Client version;example=1.8.0"`
	Protocol string `json:"protocol,omitempty" event:"label=Client protocol;enum=vela|plex|jellyfin|emby|dlna|webdav|silo;example=vela"`
}

// PlaybackNetworkContext describes network locality for a playback session.
type PlaybackNetworkContext struct {
	RemoteIP string `json:"remoteIP,omitempty" event:"label=Remote IP;pii;example=203.0.113.42"`
	Location string `json:"location,omitempty" event:"label=Network location;enum=lan|wan;example=wan"`
	Country  string `json:"country,omitempty" event:"label=Country;example=US"`
	City     string `json:"city,omitempty" event:"label=City;example=Austin"`
	ASN      string `json:"asn,omitempty" event:"label=ASN;example=AS7922"`
	IsRelay  bool   `json:"isRelay,omitempty" event:"label=Relayed connection;example=false"`
}

// PlaybackServerContext carries live aggregate counts across the server.
type PlaybackServerContext struct {
	ActiveStreams      int `json:"activeStreams,omitempty" event:"label=Server active streams;example=3"`
	ActiveTranscodes   int `json:"activeTranscodes,omitempty" event:"label=Server active transcodes;example=1"`
	ActiveDirectPlays  int `json:"activeDirectPlays,omitempty" event:"label=Server direct plays;example=2"`
	TotalBandwidthKbps int `json:"totalBandwidthKbps,omitempty" event:"label=Server total bandwidth;unit=kbps;example=42000"`
}

// PlaybackSessionContext is embedded in playback session, stream, marker, and
// concurrency events so flows can filter on title, source, stream, viewer,
// client, network, and live server counts without extra lookups.
type PlaybackSessionContext struct {
	TitleKey      string                 `json:"titleKey,omitempty" event:"label=Title key;example=movie:603"`
	TitleName     string                 `json:"titleName,omitempty" event:"label=Title name;example=The Matrix"`
	ShowTitle     string                 `json:"showTitle,omitempty" event:"label=Show title;example=Severance"`
	Season        int                    `json:"season,omitempty" event:"label=Season number;example=1"`
	Episode       int                    `json:"episode,omitempty" event:"label=Episode number;example=1"`
	MediaType     string                 `json:"mediaType,omitempty" event:"label=Media type;enum=movie|episode|live|trailer;example=movie"`
	Year          int                    `json:"year,omitempty" event:"label=Release year;example=1999"`
	LibraryID     string                 `json:"libraryId,omitempty" event:"label=Library ID;example=movies"`
	LibraryName   string                 `json:"libraryName,omitempty" event:"label=Library name;example=Movies"`
	ContentRating string                 `json:"contentRating,omitempty" event:"label=Content rating;example=R"`
	Genres        []string               `json:"genres,omitempty" event:"label=Genres;example=Action,Science Fiction"`
	Labels        []string               `json:"labels,omitempty" event:"label=Labels;example=4k-collection"`
	Actors        []string               `json:"actors,omitempty" event:"label=Actors;example=Keanu Reeves,Carrie-Anne Moss"`
	Directors     []string               `json:"directors,omitempty" event:"label=Directors;example=Lana Wachowski,Lilly Wachowski"`
	Studio        string                 `json:"studio,omitempty" event:"label=Studio;example=Warner Bros."`
	DurationMS    int64                  `json:"durationMs,omitempty" event:"label=Duration;duration_ms;unit=ms;example=8160000"`
	PosterURL     string                 `json:"posterUrl,omitempty" event:"label=Poster URL;example=/img/poster/movie:603"`
	Source        PlaybackSourceContext  `json:"source"`
	Stream        PlaybackStreamContext  `json:"stream"`
	Viewer        PlaybackViewerContext  `json:"viewer"`
	Client        PlaybackClientContext  `json:"client"`
	Network       PlaybackNetworkContext `json:"network"`
	Server        PlaybackServerContext  `json:"server"`
}

// PlaybackSessionStartedPayload is emitted when a playback session starts.
type PlaybackSessionStartedPayload struct {
	SessionID string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID    string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	PlaybackSessionContext
}

// PlaybackSessionPausedPayload is emitted when playback transitions to paused.
type PlaybackSessionPausedPayload struct {
	SessionID  string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID     string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	PositionMS int64  `json:"positionMs" event:"label=Position;duration_ms;unit=ms;example=1245000"`
	PlaybackSessionContext
}

// PlaybackSessionResumedPayload is emitted when playback resumes from pause.
type PlaybackSessionResumedPayload struct {
	SessionID   string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID      string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	PositionMS  int64  `json:"positionMs" event:"label=Position;duration_ms;unit=ms;example=1245000"`
	PausedForMS int64  `json:"pausedForMs" event:"label=Paused duration;duration_ms;unit=ms;example=185000"`
	PlaybackSessionContext
}

// PlaybackSessionStoppedPayload is emitted when a playback session stops.
type PlaybackSessionStoppedPayload struct {
	SessionID    string  `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID       string  `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	PositionMS   int64   `json:"positionMs" event:"label=Position;duration_ms;unit=ms;example=3600000"`
	BytesServed  int64   `json:"bytesServed" event:"label=Bytes served;unit=bytes;example=1450000000"`
	WatchedRatio float64 `json:"watchedRatio" event:"label=Watched ratio;example=0.44"`
	StopReason   string  `json:"stopReason,omitempty" event:"label=Stop reason;example=client_stopped"`
	PlaybackSessionContext
}

// PlaybackSessionCompletedPayload is emitted when playback crosses the watched threshold.
type PlaybackSessionCompletedPayload struct {
	SessionID        string  `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID           string  `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	WatchedMS        int64   `json:"watchedMs" event:"label=Watched duration;duration_ms;unit=ms;example=7500000"`
	WatchedRatio     float64 `json:"watchedRatio" event:"label=Watched ratio;example=0.92"`
	ThresholdPercent int     `json:"thresholdPercent" event:"label=Watched threshold;unit=%;example=90"`
	PlaybackSessionContext
}

// PlaybackSessionFailedPayload is emitted when playback fails to start or continue.
type PlaybackSessionFailedPayload struct {
	SessionID string `json:"sessionId,omitempty" event:"label=Session ID;example=sess_01"`
	PlayID    string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	Stage     string `json:"stage,omitempty" event:"label=Failure stage;example=open"`
	Error     string `json:"error" event:"label=Error message;example=all releases failed verification"`
	ReleaseID string `json:"releaseId,omitempty" event:"label=Release ID;example=rel_01"`
	Indexer   string `json:"indexer,omitempty" event:"label=Indexer;example=NZBGeek"`
	PlaybackSessionContext
}

// PlaybackSessionTerminatedPayload is emitted when an operator or flow terminates a session.
type PlaybackSessionTerminatedPayload struct {
	SessionID string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID    string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	ViewerID  int64  `json:"viewerId,omitempty" event:"label=Viewer ID;example=42"`
	Actor     string `json:"actor,omitempty" event:"label=Actor;example=owner"`
	Reason    string `json:"reason,omitempty" event:"label=Reason;example=transcode_policy"`
	Message   string `json:"message,omitempty" event:"label=Message shown to viewer;example=Please choose a direct-play client for 4K playback."`
}

// PlaybackSourceFailoverPayload is emitted when a session switches releases mid-playback.
type PlaybackSourceFailoverPayload struct {
	SessionID     string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID        string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	TitleKey      string `json:"titleKey,omitempty" event:"label=Title key;example=movie:603"`
	TitleName     string `json:"titleName,omitempty" event:"label=Title name;example=The Matrix"`
	FromReleaseID string `json:"fromReleaseId,omitempty" event:"label=Previous release ID;example=rel_01"`
	ToReleaseID   string `json:"toReleaseId,omitempty" event:"label=Next release ID;example=rel_02"`
	FromIndexer   string `json:"fromIndexer,omitempty" event:"label=Previous indexer;example=NZBGeek"`
	ToIndexer     string `json:"toIndexer,omitempty" event:"label=Next indexer;example=DrunkenSlug"`
	Reason        string `json:"reason,omitempty" event:"label=Failover reason;example=missing articles"`
	Attempt       int    `json:"attempt" event:"label=Failover attempt;example=1"`
}

// PlaybackStreamStalledPayload is emitted when playback stalls/rebuffers.
type PlaybackStreamStalledPayload struct {
	SessionID  string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID     string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	StallCount int    `json:"stallCount" event:"label=Stall count;example=1"`
	PositionMS int64  `json:"positionMs" event:"label=Position;duration_ms;unit=ms;example=915000"`
	BufferMS   int64  `json:"bufferMs" event:"label=Buffer depth;duration_ms;unit=ms;example=0"`
	PlaybackSessionContext
}

// PlaybackStreamDecisionPayload is emitted when the delivery or transcode decision is set or changed.
type PlaybackStreamDecisionPayload struct {
	SessionID        string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID           string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	PreviousDelivery string `json:"previousDelivery,omitempty" event:"label=Previous delivery;enum=direct_play|remux|transcode;example=direct_play"`
	Delivery         string `json:"delivery" event:"label=Delivery mode;enum=direct_play|remux|transcode;example=transcode"`
	VideoDecision    string `json:"videoDecision,omitempty" event:"label=Video decision;enum=direct_play|copy|transcode;example=transcode"`
	AudioDecision    string `json:"audioDecision,omitempty" event:"label=Audio decision;enum=direct_play|copy|transcode;example=copy"`
	SubtitleDecision string `json:"subtitleDecision,omitempty" event:"label=Subtitle decision;enum=none|direct_play|copy|sidecar|burn|transcode;example=none"`
	MaxHeight        int    `json:"maxHeight,omitempty" event:"label=Target max height;unit=px;example=1080"`
	BitrateKbps      int    `json:"bitrateKbps,omitempty" event:"label=Target bitrate;unit=kbps;example=8000"`
	HWDecode         bool   `json:"hwDecode,omitempty" event:"label=Hardware decode;example=true"`
	HWEncode         bool   `json:"hwEncode,omitempty" event:"label=Hardware encode;example=true"`
	Reason           string `json:"reason,omitempty" event:"label=Decision reason;example=Client bitrate limit exceeded"`
	PlaybackSessionContext
}

// PlaybackStreamQualityChangedPayload is emitted when target video height or bitrate changes.
type PlaybackStreamQualityChangedPayload struct {
	SessionID       string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID          string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	FromHeight      int    `json:"fromHeight,omitempty" event:"label=Previous height;unit=px;example=2160"`
	ToHeight        int    `json:"toHeight,omitempty" event:"label=New height;unit=px;example=1080"`
	FromBitrateKbps int    `json:"fromBitrateKbps,omitempty" event:"label=Previous bitrate;unit=kbps;example=20000"`
	ToBitrateKbps   int    `json:"toBitrateKbps,omitempty" event:"label=New bitrate;unit=kbps;example=8000"`
	Reason          string `json:"reason,omitempty" event:"label=Change reason;example=client_selected_quality"`
}

// PlaybackStreamSeekedPayload is emitted when the viewer seeks across a playback timeline.
type PlaybackStreamSeekedPayload struct {
	SessionID string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID    string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	FromMS    int64  `json:"fromMs" event:"label=From position;duration_ms;unit=ms;example=60000"`
	ToMS      int64  `json:"toMs" event:"label=To position;duration_ms;unit=ms;example=180000"`
	DeltaMS   int64  `json:"deltaMs" event:"label=Seek delta;duration_ms;unit=ms;example=120000"`
}

// PlaybackMarkerEnteredPayload is emitted when playback enters a known marker span.
type PlaybackMarkerEnteredPayload struct {
	SessionID  string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID     string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	MarkerKind string `json:"markerKind" event:"label=Marker kind;enum=intro|credits|commercial|recap;example=intro"`
	StartMS    int64  `json:"startMs" event:"label=Marker start;duration_ms;unit=ms;example=45000"`
	EndMS      int64  `json:"endMs" event:"label=Marker end;duration_ms;unit=ms;example=125000"`
	PositionMS int64  `json:"positionMs" event:"label=Current position;duration_ms;unit=ms;example=46000"`
	PlaybackSessionContext
}

// PlaybackMarkerExitedPayload is emitted when playback leaves a marker span.
type PlaybackMarkerExitedPayload struct {
	SessionID  string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID     string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	MarkerKind string `json:"markerKind" event:"label=Marker kind;enum=intro|credits|commercial|recap;example=intro"`
	PositionMS int64  `json:"positionMs" event:"label=Current position;duration_ms;unit=ms;example=126000"`
	PlaybackSessionContext
}

// PlaybackConcurrencyExceededPayload is emitted when a viewer hits their concurrent stream limit.
type PlaybackConcurrencyExceededPayload struct {
	SessionID         string `json:"sessionId,omitempty" event:"label=Session ID;example=sess_02"`
	PlayID            string `json:"playId,omitempty" event:"label=Play ID;example=play_02"`
	Limit             int    `json:"limit" event:"label=Stream limit;example=2"`
	ActiveStreams     int    `json:"activeStreams" event:"label=Active streams;example=2"`
	ActiveTranscodes  int    `json:"activeTranscodes,omitempty" event:"label=Active transcodes;example=1"`
	DistinctActiveIPs int    `json:"distinctActiveIPs,omitempty" event:"label=Distinct active IPs;example=2"`
	PlaybackSessionContext
}

// PlaybackAdmissionDeniedPayload is emitted when playback admission is denied by policy.
type PlaybackAdmissionDeniedPayload struct {
	TitleKey  string `json:"titleKey,omitempty" event:"label=Title key;example=movie:603"`
	TitleName string `json:"titleName,omitempty" event:"label=Title name;example=The Matrix"`
	ViewerID  int64  `json:"viewerId,omitempty" event:"label=Viewer ID;example=42"`
	Username  string `json:"username,omitempty" event:"label=Viewer username;pii;example=alex"`
	Client    string `json:"client,omitempty" event:"label=Client;example=Vela iOS"`
	Remote    bool   `json:"remote,omitempty" event:"label=Remote access;example=true"`
	Policy    string `json:"policy,omitempty" event:"label=Policy;example=bedtime"`
	Reason    string `json:"reason" event:"label=Denial reason;example=bedtime quiet hours active"`
}

// PlaybackTranscodeFallbackPayload is emitted when hardware transcoding falls back to software.
type PlaybackTranscodeFallbackPayload struct {
	SessionID        string `json:"sessionId" event:"label=Session ID;example=sess_01"`
	PlayID           string `json:"playId,omitempty" event:"label=Play ID;example=play_01"`
	FromHWAccel      string `json:"fromHWAccel" event:"label=Previous hardware accelerator;example=videotoolbox"`
	ToEncoder        string `json:"toEncoder" event:"label=Fallback encoder;example=libx264"`
	Reason           string `json:"reason" event:"label=Fallback reason;example=hardware encoder session init failed"`
	ActiveTranscodes int    `json:"activeTranscodes,omitempty" event:"label=Active transcodes;example=2"`
}

// registerMediaSchema registers a media/delivery schema via the shared
// idempotent builtin registrar, so RegisterBuiltins stays safe to repeat.
func registerMediaSchema[T any](r *Registry, meta EventSchema) {
	mustRegisterBuiltin[T](r, meta)
}

// RegisterPlaybackSchemas registers all built-in playback event schemas.
func RegisterPlaybackSchemas(r *Registry) {
	registerMediaSchema[PlaybackSessionStartedPayload](r, EventSchema{
		Type:        EventPlaybackSessionStarted,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback started",
		Description: "Emitted when a viewer begins a playback session.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"playback.started", "playback.start"},
	})
	registerMediaSchema[PlaybackSessionPausedPayload](r, EventSchema{
		Type:        EventPlaybackSessionPaused,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback paused",
		Description: "Emitted when an active playback session transitions to paused.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"playback.paused"},
	})
	registerMediaSchema[PlaybackSessionResumedPayload](r, EventSchema{
		Type:        EventPlaybackSessionResumed,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback resumed",
		Description: "Emitted when a paused playback session resumes playing.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"playback.resumed"},
	})
	registerMediaSchema[PlaybackSessionStoppedPayload](r, EventSchema{
		Type:        EventPlaybackSessionStopped,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback stopped",
		Description: "Emitted when a playback session ends.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"playback.stopped", "playback.stop"},
	})
	registerMediaSchema[PlaybackSessionCompletedPayload](r, EventSchema{
		Type:        EventPlaybackSessionCompleted,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback completed",
		Description: "Emitted when playback crosses the watched completion threshold.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"playback.completed"},
	})
	registerMediaSchema[PlaybackSessionFailedPayload](r, EventSchema{
		Type:        EventPlaybackSessionFailed,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback failed",
		Description: "Emitted when a playback session fails during preparation or delivery.",
		Owner:       OwnerBuiltin,
		Critical:    true,
		LegacyNames: []string{"playback.error"},
	})
	registerMediaSchema[PlaybackSessionTerminatedPayload](r, EventSchema{
		Type:        EventPlaybackSessionTerminated,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback terminated",
		Description: "Emitted when an owner or flow terminates an active playback session.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[PlaybackSourceFailoverPayload](r, EventSchema{
		Type:        EventPlaybackSourceFailover,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback source failover",
		Description: "Emitted when playback fails over from one release to another.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"playback.failover"},
	})
	registerMediaSchema[PlaybackStreamStalledPayload](r, EventSchema{
		Type:        EventPlaybackStreamStalled,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback stream stalled",
		Description: "Emitted when a playback stream stalls or rebuffers.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[PlaybackStreamDecisionPayload](r, EventSchema{
		Type:        EventPlaybackStreamDecision,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback stream decision",
		Description: "Emitted when the direct-play, remux, or transcode decision is established or updated.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[PlaybackStreamQualityChangedPayload](r, EventSchema{
		Type:        EventPlaybackStreamQualityChanged,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback stream quality changed",
		Description: "Emitted when target playback resolution or bitrate changes.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[PlaybackStreamSeekedPayload](r, EventSchema{
		Type:        EventPlaybackStreamSeeked,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback stream seeked",
		Description: "Emitted when playback jumps to a new timeline position.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[PlaybackMarkerEnteredPayload](r, EventSchema{
		Type:        EventPlaybackMarkerEntered,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback marker entered",
		Description: "Emitted when the playhead enters an intro, credits, commercial, or recap marker.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[PlaybackMarkerExitedPayload](r, EventSchema{
		Type:        EventPlaybackMarkerExited,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback marker exited",
		Description: "Emitted when the playhead leaves a marker segment.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[PlaybackConcurrencyExceededPayload](r, EventSchema{
		Type:        EventPlaybackConcurrencyExceeded,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback concurrency exceeded",
		Description: "Emitted when a viewer exceeds their concurrent stream limit.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[PlaybackAdmissionDeniedPayload](r, EventSchema{
		Type:        EventPlaybackAdmissionDenied,
		Version:     1,
		Domain:      "playback",
		Label:       "Playback admission denied",
		Description: "Emitted when playback is blocked by safety, permission, or access policy.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[PlaybackTranscodeFallbackPayload](r, EventSchema{
		Type:        EventPlaybackTranscodeFallback,
		Version:     1,
		Domain:      "playback",
		Label:       "Transcode hardware fallback",
		Description: "Emitted when hardware-accelerated transcoding falls back to software encoding or decoding.",
		Owner:       OwnerBuiltin,
	})
}

func init() {
	registerBuiltin(RegisterPlaybackSchemas)
}
