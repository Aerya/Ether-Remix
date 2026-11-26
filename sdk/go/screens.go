package etherplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Plugin screens and surfaces (docs/plugin-screens.md). A document's
// "plugin" source and the screen's declared actions call these handlers.
// Data reaches viewers only through envelopes: wrap images, links, records,
// events and entities with Image, Asset, Link, Record, Event and Entity.
// Ether rewrites each one for the viewer; a bare URL string is dropped.

// ScreenSourceRequest is what Ether sends a screen source.
type ScreenSourceRequest struct {
	Screen string            `json:"-"`
	Route  string            `json:"-"`
	Page   string            `json:"page"`
	Args   map[string]string `json:"args"`
	Locale string            `json:"locale"`
	// TZ is the viewer's IANA time zone.
	TZ string `json:"tz"`
}

// ScreenRecord is one of the plugin's records ({type, id, value}).
type ScreenRecord struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Value any    `json:"value"`
}

// ScreenSourceAnswer is a source's answer.
type ScreenSourceAnswer struct {
	Value   any            `json:"value"`
	Records []ScreenRecord `json:"records,omitempty"`
	// MaxAgeSec is how long Ether may reuse the answer (5..3600, default 60;
	// viewer-scoped answers at most 60).
	MaxAgeSec int `json:"maxAgeSec,omitempty"`
	// NextRefreshSec asks Ether to read again this soon while a viewer has
	// the page open (at least 10).
	NextRefreshSec int `json:"nextRefreshSec,omitempty"`
}

// ScreenActionRequest is one call of a declared action.
type ScreenActionRequest struct {
	Screen string                     `json:"-"`
	Action string                     `json:"-"`
	Page   string                     `json:"page"`
	Args   map[string]json.RawMessage `json:"args"`
	Params map[string]string          `json:"params"`
	Locale string                     `json:"locale"`
	TZ     string                     `json:"tz"`
}

// ScreenActionAnswer patches the client at once.
type ScreenActionAnswer struct {
	Records []ScreenRecord `json:"records,omitempty"`
	Sources map[string]any `json:"sources,omitempty"`
	// Refresh names sources Ether reads again before answering.
	Refresh []string `json:"refresh,omitempty"`
	// Toast is plain text, at most 200 characters.
	Toast string `json:"toast,omitempty"`
	// Navigate is a Vela navigate action: {"action": "navigate", "args": {...}}.
	Navigate map[string]any `json:"navigate,omitempty"`
}

// ScreenSourceHandler answers a "plugin" source.
type ScreenSourceHandler func(ctx *Context, req ScreenSourceRequest) (ScreenSourceAnswer, error)

// ScreenActionHandler runs a declared screen action.
type ScreenActionHandler func(ctx *Context, req ScreenActionRequest) (ScreenActionAnswer, error)

// OnScreenSource registers a source handler; screen is the screen or surface
// id, or "*" for any.
func (p *Plugin) OnScreenSource(screen, route string, h ScreenSourceHandler) {
	k := p.kinds()
	if k.screenSources == nil {
		k.screenSources = map[string]ScreenSourceHandler{}
	}
	k.screenSources[screen+"/"+route] = h
}

// OnScreenAction registers an action handler; screen may be "*".
func (p *Plugin) OnScreenAction(screen, action string, h ScreenActionHandler) {
	k := p.kinds()
	if k.screenActions == nil {
		k.screenActions = map[string]ScreenActionHandler{}
	}
	k.screenActions[screen+"/"+action] = h
}

func (p *Plugin) dispatchScreen(pctx *Context, method, screen, kind, name string, req RawRequest) RawResponse {
	if method != http.MethodPost {
		return errorJSON(http.StatusMethodNotAllowed, "method_not_allowed", "Screen routes use POST.")
	}
	k := p.kindHandlers
	if kind == "sources" {
		var h ScreenSourceHandler
		if k != nil {
			if h = k.screenSources[screen+"/"+name]; h == nil {
				h = k.screenSources["*/"+name]
			}
		}
		if h == nil {
			return errorJSON(http.StatusNotFound, "not_found", fmt.Sprintf("No screen source %q is registered for %q.", name, screen))
		}
		var in ScreenSourceRequest
		if len(req.Body) > 0 {
			if err := json.Unmarshal(req.Body, &in); err != nil {
				return errorJSON(http.StatusBadRequest, "invalid_json", "Request body must be valid JSON.")
			}
		}
		in.Screen, in.Route = screen, name
		out, err := h(pctx, in)
		if err != nil {
			return toRouteErrorResponse(err)
		}
		return okJSON(out, http.StatusOK)
	}
	var h ScreenActionHandler
	if k != nil {
		if h = k.screenActions[screen+"/"+name]; h == nil {
			h = k.screenActions["*/"+name]
		}
	}
	if h == nil {
		return errorJSON(http.StatusNotFound, "not_found", fmt.Sprintf("No screen action %q is registered for %q.", name, screen))
	}
	var in ScreenActionRequest
	if len(req.Body) > 0 {
		if err := json.Unmarshal(req.Body, &in); err != nil {
			return errorJSON(http.StatusBadRequest, "invalid_json", "Request body must be valid JSON.")
		}
	}
	in.Screen, in.Action = screen, name
	out, err := h(pctx, in)
	if err != nil {
		return toRouteErrorResponse(err)
	}
	return okJSON(out, http.StatusOK)
}

// Image is a picture on one of the plugin's permissions.network hosts,
// served to viewers through Ether's signed image proxy.
func Image(url string) map[string]string { return map[string]string{"$image": url} }

// Asset is a file in the plugin package.
func Asset(path string) map[string]string {
	return map[string]string{"$image": "asset:" + strings.TrimLeft(path, "/")}
}

// Link is an https page a viewer may open on a phone or the web.
func Link(url string) map[string]string { return map[string]string{"$link": url} }

// Record references one of the plugin's records (send its value in Records).
func Record(typ, id string) map[string]map[string]string {
	return map[string]map[string]string{"$record": {"type": typ, "id": id}}
}

// Event references one of the plugin's live events.
func Event(feed, id string) map[string]map[string]string {
	return map[string]map[string]string{"$event": {"feed": feed, "id": id}}
}

// Entity references one of the plugin's live entities.
func Entity(feed, kind, id string) map[string]map[string]string {
	return map[string]map[string]string{"$entity": {"feed": feed, "kind": kind, "id": id}}
}

// ScreensHost pushes screen updates (screens capability). RemoteHostClient
// implements it; Context.InvalidateScreens and Context.PutScreenRecords use it.
type ScreensHost interface {
	ScreensInvalidate(ctx context.Context) (int, error)
	ScreensPutRecords(ctx context.Context, records []ScreenRecord) (int, error)
}

// ScreensInvalidate makes open screens and surfaces read their sources again
// through POST /ether/plugin-host/screens/invalidate.
func (c *RemoteHostClient) ScreensInvalidate(ctx context.Context) (int, error) {
	var out struct {
		Streams int `json:"streams"`
	}
	_, err := c.callJSON(ctx, http.MethodPost, "/ether/plugin-host/screens/invalidate", map[string]any{}, &out, false)
	return out.Streams, err
}

// ScreensPutRecords sends new values for the plugin's records to open screens
// showing them through POST /ether/plugin-host/screens/records.
func (c *RemoteHostClient) ScreensPutRecords(ctx context.Context, records []ScreenRecord) (int, error) {
	var out struct {
		Streams int `json:"streams"`
	}
	_, err := c.callJSON(ctx, http.MethodPost, "/ether/plugin-host/screens/records", map[string]any{"records": records}, &out, false)
	return out.Streams, err
}

// InvalidateScreens makes this plugin's open screens read their sources
// again now. It returns how many open streams heard it.
func (c *Context) InvalidateScreens() (int, error) {
	if sh, ok := c.Ether.(ScreensHost); ok {
		return sh.ScreensInvalidate(c.Context)
	}
	return 0, fmt.Errorf("screens.invalidate is not supported by this host")
}

// PutScreenRecords sends new values for records open screens are showing.
func (c *Context) PutScreenRecords(records ...ScreenRecord) (int, error) {
	if sh, ok := c.Ether.(ScreensHost); ok {
		return sh.ScreensPutRecords(c.Context, records)
	}
	return 0, fmt.Errorf("screens.putRecords is not supported by this host")
}
