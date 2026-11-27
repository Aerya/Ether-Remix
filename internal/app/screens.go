package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/favorites"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/liveevents"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/screens"
)

func (a *App) buildScreensService(mgr *plugins.Manager) *screens.Service {
	svc := screens.NewService(screens.ServiceOptions{
		Hub:       pluginLiveFeedBridge{mgr: mgr},
		Favorites: appFavoriteEntities{app: a},
		Now:       time.Now,
		Documents: a.buildDocRegistry(mgr),
	})
	svc.SetPluginCaller(screenPluginCaller{mgr: mgr})
	svc.SetItemFinder(screenItemFinder{app: a})
	mgr.SetScreenPush(screenPushes{svc: svc})
	return svc
}

func (a *App) screenFrontendPublished(frontendID string) bool {
	if frontendID == "" {
		return true
	}
	if a == nil || a.cfg == nil {
		return frontendID == "vela"
	}
	return frontendDesired(a.cfg.Get(), frontend.Protocol(frontendID))
}

func (a *App) screenPublishStatus(ctx context.Context, mgr *plugins.Manager, rec plugins.Record, frontendID string) (bool, string) {
	if !rec.Enabled {
		return false, "plugin_disabled"
	}
	if rec.Shadow {
		return false, "shadow"
	}
	for _, role := range rec.RolesOff {
		if role == plugins.CapLiveEvents {
			return false, "capability_off"
		}
	}
	if mgr != nil {
		if grants, err := mgr.GrantsOf(ctx, rec.ID); err == nil {
			for _, r := range grants.Revoked {
				if r == string(plugins.CapLiveEvents) {
					return false, "capability_off"
				}
			}
		}
	}
	if !a.screenFrontendPublished(frontendID) {
		return false, "frontend_disabled"
	}
	return true, "published"
}

func (a *App) screenPluginViewerAllowed(ctx context.Context, mgr *plugins.Manager, viewer *core.User, pluginID string) bool {
	if viewer == nil || mgr == nil {
		return false
	}
	rec, err := mgr.Get(ctx, pluginID)
	if err != nil || !rec.Enabled || rec.Shadow {
		return false
	}
	if rec.Manifest.Ether != nil && rec.Manifest.Ether.Permissions.Viewers.Identity == "granted_viewers" && !viewer.IsOwner {
		grants, gerr := mgr.GrantsOf(ctx, pluginID)
		if gerr != nil {
			return false
		}
		for _, id := range grants.Viewers {
			if id == viewer.ID {
				return true
			}
		}
		return false
	}
	return true
}

type appFavoriteEntities struct{ app *App }

func (f appFavoriteEntities) ListEntityKeys(ctx context.Context, viewer *core.User) ([]string, error) {
	if f.app == nil || f.app.favorites == nil || viewer == nil {
		return nil, nil
	}
	entries, err := f.app.favorites.List(ctx, viewer, favorites.KindEntity, 0)
	if errors.Is(err, favorites.ErrForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, entry.Subject.ID)
	}
	return keys, nil
}

func (f appFavoriteEntities) ListChannelIDs(ctx context.Context, viewer *core.User) ([]string, error) {
	if f.app == nil || f.app.favorites == nil || viewer == nil {
		return nil, nil
	}
	entries, err := f.app.favorites.List(ctx, viewer, favorites.KindChannel, 0)
	if errors.Is(err, favorites.ErrForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Subject.ID != "" {
			ids = append(ids, entry.Subject.ID)
		}
	}
	return ids, nil
}

// pluginLiveFeedBridge is a zero-background-work fallback LiveFeedHub that calls
// plugin live-feed routes on demand via plugins.Manager.Call until W255's
// liveevents.Hub attaches via screens.Service.SetHub.
type pluginLiveFeedBridge struct {
	mgr *plugins.Manager
}

func (b pluginLiveFeedBridge) Events(ctx context.Context, _ *core.User, q screens.EventQuery) ([]core.LiveEvent, error) {
	if b.mgr == nil || q.PluginID == "" || q.Feed == "" {
		return nil, nil
	}
	vals := url.Values{}
	if !q.From.IsZero() {
		vals.Set("from", q.From.UTC().Format(time.RFC3339))
	}
	if !q.To.IsZero() {
		vals.Set("to", q.To.UTC().Format(time.RFC3339))
	}
	if q.League != "" {
		vals.Set("league", q.League)
	}
	if len(q.IDs) > 0 {
		vals.Set("ids", strings.Join(q.IDs, ","))
	}
	path := "/ether/v1/live/" + url.PathEscape(q.Feed) + "/events"
	if encoded := vals.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := b.mgr.Call(ctx, q.PluginID, plugins.Request{
		PluginID: q.PluginID,
		Method:   http.MethodGet,
		Path:     path,
	})
	if err != nil {
		return nil, err
	}
	if resp.Status == http.StatusNotFound {
		return nil, core.ErrNotFound
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, core.ErrNotAvailable
	}
	var payload struct {
		Events []core.LiveEvent `json:"events"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return nil, err
	}
	prefix := q.PluginID + ":"
	for i := range payload.Events {
		if payload.Events[i].Feed == "" {
			payload.Events[i].Feed = q.Feed
		}
		if payload.Events[i].ID != "" && !strings.HasPrefix(payload.Events[i].ID, prefix) {
			payload.Events[i].ID = q.PluginID + ":" + q.Feed + ":" + payload.Events[i].ID
		}
	}
	return payload.Events, nil
}

func (b pluginLiveFeedBridge) Standings(ctx context.Context, _ *core.User, pluginID, feed, league string) ([]core.LiveEventTable, error) {
	if b.mgr == nil || pluginID == "" || feed == "" {
		return nil, nil
	}
	path := "/ether/v1/live/" + url.PathEscape(feed) + "/standings"
	if league != "" {
		path += "?league=" + url.QueryEscape(league)
	}
	resp, err := b.mgr.Call(ctx, pluginID, plugins.Request{
		PluginID: pluginID,
		Method:   http.MethodGet,
		Path:     path,
	})
	if err != nil {
		return nil, err
	}
	if resp.Status == http.StatusNotFound {
		return nil, core.ErrNotFound
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, core.ErrNotAvailable
	}
	var payload struct {
		Tables []core.LiveEventTable `json:"tables"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return nil, err
	}
	return payload.Tables, nil
}

func (b pluginLiveFeedBridge) Entities(ctx context.Context, _ *core.User, q screens.EntityQuery) ([]core.LiveEventEntity, error) {
	if b.mgr == nil || q.PluginID == "" || q.Feed == "" {
		return nil, nil
	}
	vals := url.Values{}
	if q.Kind != "" {
		vals.Set("kind", q.Kind)
	}
	if q.League != "" {
		vals.Set("league", q.League)
	}
	if q.Query != "" {
		vals.Set("q", q.Query)
	}
	if len(q.IDs) > 0 {
		vals.Set("ids", strings.Join(q.IDs, ","))
	}
	path := "/ether/v1/live/" + url.PathEscape(q.Feed) + "/entities"
	if encoded := vals.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := b.mgr.Call(ctx, q.PluginID, plugins.Request{
		PluginID: q.PluginID,
		Method:   http.MethodGet,
		Path:     path,
	})
	if err != nil {
		return nil, err
	}
	if resp.Status == http.StatusNotFound {
		return nil, core.ErrNotFound
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, core.ErrNotAvailable
	}
	var payload struct {
		Entities []core.LiveEventEntity `json:"entities"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return nil, err
	}
	return payload.Entities, nil
}

// liveEventsScreenHub adapts W255's *liveevents.Hub to screens.LiveFeedHub so
// screens share the hub's single-flight cache, rate limits, and viewer spoiler
// filtering without duplicating plugin requests.
type liveEventsScreenHub struct {
	hub *liveevents.Hub
}

func (h liveEventsScreenHub) Events(ctx context.Context, viewer *core.User, q screens.EventQuery) ([]core.LiveEvent, error) {
	if h.hub == nil || q.PluginID == "" || q.Feed == "" {
		return nil, nil
	}
	lq := liveevents.EventQuery{
		League: q.League,
		IDs:    q.IDs,
	}
	if !q.From.IsZero() {
		lq.From = q.From.UTC().Format(time.RFC3339)
	}
	if !q.To.IsZero() {
		lq.To = q.To.UTC().Format(time.RFC3339)
	}
	resp, err := h.hub.FetchEvents(ctx, q.PluginID, q.Feed, lq)
	if err != nil {
		return nil, mapScreenHubError(err)
	}
	return h.hub.ApplySpoilers(ctx, viewer, resp.Events), nil
}

func (h liveEventsScreenHub) Standings(ctx context.Context, _ *core.User, pluginID, feed, league string) ([]core.LiveEventTable, error) {
	if h.hub == nil || pluginID == "" || feed == "" {
		return nil, nil
	}
	resp, err := h.hub.GetStandings(ctx, pluginID, feed, league)
	if err != nil {
		return nil, mapScreenHubError(err)
	}
	return resp.Tables, nil
}

func (h liveEventsScreenHub) Entities(ctx context.Context, _ *core.User, q screens.EntityQuery) ([]core.LiveEventEntity, error) {
	if h.hub == nil || q.PluginID == "" || q.Feed == "" {
		return nil, nil
	}
	if len(q.IDs) > 0 {
		resp, err := h.hub.GetEntitiesByIDs(ctx, q.PluginID, q.Feed, q.Kind, q.IDs)
		if err != nil {
			return nil, mapScreenHubError(err)
		}
		return resp.Entities, nil
	}
	resp, err := h.hub.GetEntities(ctx, q.PluginID, q.Feed, q.Kind, q.League, q.Query)
	if err != nil {
		return nil, mapScreenHubError(err)
	}
	return resp.Entities, nil
}

func mapScreenHubError(err error) error {
	switch {
	case errors.Is(err, liveevents.ErrNotFound):
		return core.ErrNotFound
	case errors.Is(err, liveevents.ErrUnavailable):
		return core.ErrNotAvailable
	default:
		return err
	}
}

type surfaceScreens struct {
	app     *App
	surface string
}

func (s surfaceScreens) Generation() uint64 {
	if s.app == nil || s.app.pluginsRT == nil || s.app.pluginsRT.screens == nil {
		return 0
	}
	return s.app.pluginsRT.screens.Documents().Generation()
}

// SetFavoriteEntity sets or clears one entity favourite (ether.favorite).
func (s surfaceScreens) SetFavoriteEntity(ctx context.Context, viewer *frontend.Viewer, key string, on bool) error {
	if s.app == nil || s.app.favorites == nil || viewer == nil {
		return frontend.ErrNotFound
	}
	if _, ok := favorites.ParseEntityKey(key); !ok {
		return frontend.ErrScreenParams
	}
	_, err := s.app.favorites.Set(ctx, viewer, favorites.Subject{Kind: favorites.KindEntity, ID: key}, on)
	if errors.Is(err, favorites.ErrForbidden) {
		return frontend.ErrForbidden
	}
	return err
}
