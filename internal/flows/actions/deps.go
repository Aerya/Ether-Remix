// Package actions implements and registers all built-in Flow actions defined
// in docs/design/flows.md §4.4. Every action declares a typed flows.ActionSpec,
// supports DryRun mode cleanly without external side effects, enforces strict
// bounds, and communicates with Ether subsystems through narrow interfaces.
package actions

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/notify"
)

// Notifier delivers Flow notifications through configured notification channels
// and publishes MQTT messages via configured MQTT broker channels.
type Notifier interface {
	DeliverToChannels(ctx context.Context, channelIDs []string, ev notify.Event, opts notify.DeliverOptions) ([]notify.DeliverResult, error)
	PublishMQTT(ctx context.Context, brokerID, topic string, payload []byte, qos byte, retain bool) error
}

// ActiveSession is the normalized view of one active playback session used by
// playback.stop_session selectors.
type ActiveSession struct {
	ID            string
	ClientSession string // optional frontend session ID (e.g. Plex sessionId)
	ViewerID      string
	Username      string
	ViewerRole    string
	ViewerTags    []string
	ClientID      string
	ClientName    string
	Platform      string
	TitleKey      string
	TitleName     string
	State         string // "playing" | "paused" | "buffering" | "stopped"
	IsTranscode   bool
	VideoDecision string // "direct_play" | "copy" | "transcode"
	VideoHeight   int
	BitrateKbps   int
	IsRemote      bool
	StartedAt     time.Time
	PausedSince   time.Time
	LastActive    time.Time
	Node          string
}

// PlaybackController lists active sessions and stops a session with a message
// delivered to the viewer's client (matching Plex /status/sessions/terminate).
type PlaybackController interface {
	ListSessions(ctx context.Context) ([]ActiveSession, error)
	StopSession(ctx context.Context, sessionID, message string) error
}

// ViewerPolicySnapshot captures the reversible viewer restriction fields.
type ViewerPolicySnapshot struct {
	ViewerID       string `json:"viewerId"`
	RemoteDisabled bool   `json:"remoteDisabled"`
	MaxBitrateKbps int    `json:"maxBitrateKbps"`
	StreamLimit    int    `json:"streamLimit"`
	AccountEnabled bool   `json:"accountEnabled"`
}

// ViewerRestrictionPatch specifies which fields to change on a viewer.
type ViewerRestrictionPatch struct {
	RemoteDisabled *bool `json:"remoteDisabled,omitempty"`
	MaxBitrateKbps *int  `json:"maxBitrateKbps,omitempty"`
	StreamLimit    *int  `json:"streamLimit,omitempty"`
	AccountEnabled *bool `json:"accountEnabled,omitempty"`
}

// ViewerController reads and applies viewer safety & account restrictions.
type ViewerController interface {
	GetViewerPolicy(ctx context.Context, viewerID string) (ViewerPolicySnapshot, error)
	ApplyViewerPolicy(ctx context.Context, viewerID string, patch ViewerRestrictionPatch) error
}

// RefreshResult summarizes a completed library, collection, or source refresh.
type RefreshResult struct {
	Target     string `json:"target"`
	Kind       string `json:"kind"`
	Items      int    `json:"items"`
	DurationMS int64  `json:"durationMs"`
}

// ExportItem is one normalized media item serialized by library.export.
type ExportItem struct {
	Key           string   `json:"key" xml:"key,attr"`
	Title         string   `json:"title" xml:"Title"`
	ShowTitle     string   `json:"showTitle,omitempty" xml:"ShowTitle,omitempty"`
	Season        int      `json:"season,omitempty" xml:"Season,omitempty"`
	Episode       int      `json:"episode,omitempty" xml:"Episode,omitempty"`
	MediaType     string   `json:"mediaType" xml:"MediaType"`
	Year          int      `json:"year,omitempty" xml:"Year,omitempty"`
	LibraryID     string   `json:"libraryId,omitempty" xml:"LibraryId,omitempty"`
	LibraryName   string   `json:"libraryName,omitempty" xml:"LibraryName,omitempty"`
	Summary       string   `json:"summary,omitempty" xml:"Summary,omitempty"`
	ContentRating string   `json:"contentRating,omitempty" xml:"ContentRating,omitempty"`
	Studio        string   `json:"studio,omitempty" xml:"Studio,omitempty"`
	Rating        float64  `json:"rating,omitempty" xml:"Rating,omitempty"`
	AddedAt       string   `json:"addedAt,omitempty" xml:"AddedAt,omitempty"`
	Genres        []string `json:"genres,omitempty" xml:"Genres>Genre,omitempty"`
	Actors        []string `json:"actors,omitempty" xml:"Actors>Actor,omitempty"`
	Directors     []string `json:"directors,omitempty" xml:"Directors>Director,omitempty"`
	Labels        []string `json:"labels,omitempty" xml:"Labels>Label,omitempty"`
	ExternalIDs   []string `json:"externalIds,omitempty" xml:"ExternalIds>Id,omitempty"`
	DurationMS    int64    `json:"durationMs,omitempty" xml:"DurationMs,omitempty"`
	Container     string   `json:"container,omitempty" xml:"Container,omitempty"`
	VideoCodec    string   `json:"videoCodec,omitempty" xml:"VideoCodec,omitempty"`
	VideoWidth    int      `json:"videoWidth,omitempty" xml:"VideoWidth,omitempty"`
	VideoHeight   int      `json:"videoHeight,omitempty" xml:"VideoHeight,omitempty"`
	DynamicRange  string   `json:"dynamicRange,omitempty" xml:"DynamicRange,omitempty"`
	AudioCodec    string   `json:"audioCodec,omitempty" xml:"AudioCodec,omitempty"`
	AudioChannels int      `json:"audioChannels,omitempty" xml:"AudioChannels,omitempty"`
	BitrateKbps   int      `json:"bitrateKbps,omitempty" xml:"BitrateKbps,omitempty"`
	FileSizeBytes int64    `json:"fileSizeBytes,omitempty" xml:"FileSizeBytes,omitempty"`
	PosterURL     string   `json:"posterUrl,omitempty" xml:"PosterUrl,omitempty"`
	BackdropURL   string   `json:"backdropUrl,omitempty" xml:"BackdropUrl,omitempty"`
	PlayURL       string   `json:"playUrl,omitempty" xml:"PlayUrl,omitempty"`
}

// ExportQuery scopes what library.export retrieves from LibraryController.
type ExportQuery struct {
	Scope          string // "server" | "library" | "collection" | "playlist" | "item"
	TargetID       string
	MetadataLevel  int
	MediaInfoLevel int
	ArtworkLevel   int
	MaxItems       int
}

// LibraryController handles library/collection refresh and item enumeration for export.
type LibraryController interface {
	RefreshLibrary(ctx context.Context, libraryID string, fullSync bool) (RefreshResult, error)
	RefreshCollection(ctx context.Context, collectionID string) (RefreshResult, error)
	ListExportItems(ctx context.Context, q ExportQuery) ([]ExportItem, error)
}

// ExportArtifact describes a persisted export or shareable digest file.
type ExportArtifact struct {
	Filename    string `json:"filename"`
	ExportPath  string `json:"exportPath"`
	DownloadURL string `json:"downloadUrl"`
	SizeBytes   int64  `json:"sizeBytes"`
}

// ExportSink persists generated exports or shareable digest HTML files safely.
type ExportSink interface {
	Save(ctx context.Context, filename string, data []byte) (ExportArtifact, error)
}

// DigestQuery specifies the time window and filters for digest.generate.
type DigestQuery struct {
	Since      time.Time
	Until      time.Time
	LibraryIDs []string
	MediaTypes []string
	MaxItems   int
}

// DigestItem is one recently added movie, show, or episode entry.
type DigestItem struct {
	TitleKey    string
	Title       string
	ShowKey     string
	ShowTitle   string
	Season      int
	Episode     int
	MediaType   string // "movie" | "show" | "episode"
	Year        int
	Summary     string
	Genres      []string
	Rating      float64
	PosterURL   string
	LinkURL     string
	LibraryName string
	AddedAt     time.Time
}

// DigestTopTitle is a top-watched title included in activity digests.
type DigestTopTitle struct {
	Title string
	Plays int
}

// DigestActivitySummary summarizes playback activity over a digest window.
type DigestActivitySummary struct {
	TotalPlays    int
	UniqueViewers int
	HoursWatched  float64
	TopTitles     []DigestTopTitle
}

// DigestSource retrieves recently added media and playback stats for digest.generate.
type DigestSource interface {
	RecentlyAdded(ctx context.Context, q DigestQuery) ([]DigestItem, error)
	ActivitySummary(ctx context.Context, since, until time.Time) (DigestActivitySummary, error)
}

// SourceRefresher triggers Live TV, watchlist, or plugin source refreshes.
type SourceRefresher interface {
	RefreshSource(ctx context.Context, sourceKind, sourceID string) (RefreshResult, error)
}

// PinResult reports the outcome of release.pin.
type PinResult struct {
	TitleKey    string
	TitleName   string
	ReleaseID   string
	ReleaseName string
}

// ReleasePinner pins and unpins releases for titles.
type ReleasePinner interface {
	PinRelease(ctx context.Context, titleKey, releaseID, note string) (PinResult, error)
	UnpinRelease(ctx context.Context, titleKey string) error
}

// Deps supplies subsystem adapters for built-in Flow actions. Nil dependencies
// cause only the actions relying on them to return a clear unavailable error
// when executed (specs remain inspectable and validatable even with empty Deps).
type Deps struct {
	Notify       Notifier
	Playback     PlaybackController
	HTTPClient   *http.Client
	TrustedHosts []string
	Viewers      ViewerController
	Restrictions *RestrictionStore
	Libraries    LibraryController
	Exports      ExportSink
	Digests      DigestSource
	Sources      SourceRefresher
	Releases     ReleasePinner
	Now          func() time.Time
	Log          *slog.Logger
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// All returns all 13 built-in Flow action handlers bound to deps.
func All(deps Deps) []flows.ActionHandler {
	if deps.Restrictions == nil {
		deps.Restrictions = NewMemoryRestrictionStore(deps.Now)
	}
	return []flows.ActionHandler{
		NewNotifySend(deps),
		NewPlaybackStopSession(deps),
		NewHTTPWebhook(deps),
		NewMQTTPublish(deps),
		NewViewerRestrict(deps),
		NewViewerUnrestrict(deps),
		NewLibraryRefresh(deps),
		NewLibraryExport(deps),
		NewDigestGenerate(deps),
		NewSourceRefresh(deps),
		NewReleasePin(deps),
		NewReleaseUnpin(deps),
		NewFlowSetVariable(deps),
	}
}

// Specs returns the ActionSpec metadata for all 13 built-in actions without
// requiring live subsystem dependencies.
func Specs() []flows.ActionSpec {
	handlers := All(Deps{})
	out := make([]flows.ActionSpec, 0, len(handlers))
	for _, h := range handlers {
		out = append(out, h.Spec())
	}
	return out
}

// RegisterAll registers every built-in Flow action into reg.
func RegisterAll(reg flows.ActionRegistry, deps Deps) error {
	for _, h := range All(deps) {
		if err := reg.Register(h); err != nil {
			return fmt.Errorf("flows/actions: register %s: %w", h.Spec().Type, err)
		}
	}
	return nil
}

// NewRegistry builds a fresh InMemoryActionRegistry pre-populated with all
// built-in Flow actions.
func NewRegistry(deps Deps) (*flows.InMemoryActionRegistry, error) {
	reg := flows.NewActionRegistry()
	if err := RegisterAll(reg, deps); err != nil {
		return nil, err
	}
	return reg, nil
}

// --- shared parameter extraction helpers ---

func correlationViewerID(actx flows.ActionContext) string {
	if actx.Event.Correlation.ViewerID <= 0 {
		return ""
	}
	return strconv.FormatInt(actx.Event.Correlation.ViewerID, 10)
}

func paramString(params map[string]any, key, def string) string {
	v, ok := params[key]
	if !ok || v == nil {
		return def
	}
	switch x := v.(type) {
	case string:
		if strings.TrimSpace(x) == "" {
			return def
		}
		return x
	case fmt.Stringer:
		s := x.String()
		if strings.TrimSpace(s) == "" {
			return def
		}
		return s
	default:
		s := fmt.Sprint(x)
		if strings.TrimSpace(s) == "" {
			return def
		}
		return s
	}
}

func paramStringList(params map[string]any, key string) []string {
	v, ok := params[key]
	if !ok || v == nil {
		return nil
	}
	switch x := v.(type) {
	case []string:
		out := make([]string, 0, len(x))
		for _, s := range x {
			if t := strings.TrimSpace(s); t != "" {
				out = append(out, t)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" && item != nil {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		for _, part := range strings.Split(x, ",") {
			if t := strings.TrimSpace(part); t != "" {
				out = append(out, t)
			}
		}
		return out
	default:
		return nil
	}
}

func paramInt(params map[string]any, key string, def int) (int, bool, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return def, false, nil
	}
	switch x := v.(type) {
	case int:
		return x, true, nil
	case int64:
		return int(x), true, nil
	case float64:
		return int(x), true, nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return def, false, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return def, true, fmt.Errorf("parameter %q must be an integer", key)
		}
		return n, true, nil
	default:
		return def, true, fmt.Errorf("parameter %q must be an integer", key)
	}
}

func paramInt64(params map[string]any, key string, def int64) (int64, bool, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return def, false, nil
	}
	switch x := v.(type) {
	case int:
		return int64(x), true, nil
	case int64:
		return x, true, nil
	case float64:
		return int64(x), true, nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return def, false, nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return def, true, fmt.Errorf("parameter %q must be an integer", key)
		}
		return n, true, nil
	default:
		return def, true, fmt.Errorf("parameter %q must be an integer", key)
	}
}

func paramBool(params map[string]any, key string, def bool) (bool, bool, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return def, false, nil
	}
	switch x := v.(type) {
	case bool:
		return x, true, nil
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		if s == "" {
			return def, false, nil
		}
		b, err := strconv.ParseBool(s)
		if err != nil {
			return def, true, fmt.Errorf("parameter %q must be a boolean", key)
		}
		return b, true, nil
	default:
		return def, true, fmt.Errorf("parameter %q must be a boolean", key)
	}
}

func paramMap(params map[string]any, key string) map[string]any {
	v, ok := params[key]
	if !ok || v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// parseHumanDuration parses Go duration strings ("30m", "2h") as well as
// day ("7d") and week ("2w") shorthands.
func parseHumanDuration(raw string) (time.Duration, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(s, "d") && !strings.ContainsAny(s[:len(s)-1], "hmsw") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid day duration %q", raw)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "w") && !strings.ContainsAny(s[:len(s)-1], "hmsd") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "w"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid week duration %q", raw)
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid duration %q", raw)
	}
	return d, nil
}
