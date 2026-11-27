package frontend

import (
	"context"
	"encoding/json"
	"errors"
)

// Plugin screens and surfaces drawn from documents (docs/plugin-screens.md).
// The app implements PluginScreens; frontends only route and authenticate.

var (
	// ErrScreenParams reports a page parameter, context value, source name or
	// action argument that fails its declaration.
	ErrScreenParams = errors.New("frontend: invalid screen parameters")
	// ErrScreenNoVariant reports that no document variant fits the client.
	ErrScreenNoVariant = errors.New("frontend: no document for this app version")
	// ErrScreenTopic reports a stream topic that is forged, expired or not the
	// viewer's.
	ErrScreenTopic = errors.New("frontend: invalid stream topic")
	// ErrScreenTooManyTopics reports more stream topics than one stream may hold.
	ErrScreenTooManyTopics = errors.New("frontend: too many stream topics")
	// ErrScreenRateLimited reports a viewer calling an action too often.
	ErrScreenRateLimited = errors.New("frontend: action rate limited")
)

// MaxScreenStreamTopics bounds the topics one stream carries.
const MaxScreenStreamTopics = 4

// ScreenClient is what a request says about the client.
type ScreenClient struct {
	// Renderer is the client's RENDERER_VERSION (X-Vela-Screen-Renderer);
	// 0 means it draws no documents.
	Renderer int
	Locale   string
}

// ScreenImages turns image references into URLs the client may load: a
// plugin-named URL and a channel's logo. An empty answer drops the image.
type ScreenImages interface {
	Plugin(pluginID, url string) string
	Channel(channelID string) string
}

// ScreenDataRequest is a data read of one page or surface.
type ScreenDataRequest struct {
	Images  ScreenImages
	Kind    string // screen|surface
	ID      string
	Page    string
	Sources []string
	Params  map[string]string
	TZ      string
	Client  ScreenClient
}

// ScreenActionRequest is one action call.
type ScreenActionRequest struct {
	Kind           string // screen|surface
	ID             string
	Action         string
	Page           string
	Params         map[string]string
	Args           map[string]json.RawMessage
	Context        map[string]string
	IdempotencyKey string
	TZ             string
	Client         ScreenClient
	Images         ScreenImages
}

// ScreenFrame is one stream frame: event name and JSON data.
type ScreenFrame struct {
	Event string
	Data  json.RawMessage
}

// ScreenStream delivers frames for a set of topics until closed.
type ScreenStream interface {
	Frames() <-chan ScreenFrame
	Close()
}

// PluginScreens serves document screens and surfaces.
type PluginScreens interface {
	// ScreenNavigation returns the document screens for bootstrap
	// navigation (a JSON array), empty for clients that draw no documents.
	ScreenNavigation(ctx context.Context, viewer *Viewer, c ScreenClient) (json.RawMessage, error)
	// ScreenSurfaces returns bootstrap surfaces (a JSON array).
	ScreenSurfaces(ctx context.Context, viewer *Viewer, c ScreenClient) (json.RawMessage, error)
	// ScreenDocument returns a served document and its version tag.
	ScreenDocument(ctx context.Context, viewer *Viewer, kind, id, page string, c ScreenClient) ([]byte, string, error)
	// ScreenData resolves a data read.
	ScreenData(ctx context.Context, viewer *Viewer, req ScreenDataRequest) (json.RawMessage, error)
	// ScreenAction runs an action and returns its answer.
	ScreenAction(ctx context.Context, viewer *Viewer, req ScreenActionRequest) (json.RawMessage, error)
	// SetSurfaceSetting remembers a viewer's player-menu switch.
	SetSurfaceSetting(ctx context.Context, viewer *Viewer, id string, enabled bool) error
	// ScreenAsset returns a package file a document names, with its type.
	ScreenAsset(ctx context.Context, pluginID, version, path string) ([]byte, string, error)
	// ScreenStream opens the live updates of topics, replaying everything
	// newer than since.
	ScreenStream(ctx context.Context, viewer *Viewer, topics []string, since int64, images ScreenImages) (ScreenStream, error)
	// ToastSurface returns the wire id of pluginID's published toast surface
	// for viewer, or "".
	ToastSurface(ctx context.Context, viewer *Viewer, c ScreenClient, pluginID string) string
	// ScreenPreviewValid reports whether ctx's Studio preview token is live.
	ScreenPreviewValid(ctx context.Context) bool
}

type screenPreviewKey struct{}

// WithScreenPreview carries a Studio preview token (X-Vela-Screen-Preview):
// the request sees that draft's documents in place of the installed ones.
func WithScreenPreview(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, screenPreviewKey{}, token)
}

// ScreenPreviewFrom returns the request's preview token, if any.
func ScreenPreviewFrom(ctx context.Context) string {
	t, _ := ctx.Value(screenPreviewKey{}).(string)
	return t
}
