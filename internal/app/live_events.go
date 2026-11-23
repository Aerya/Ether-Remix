package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/liveevents"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// LiveEventsHub returns Ether's live events hub (nil if plugins are not wired).
func (a *App) LiveEventsHub() *liveevents.Hub {
	if a == nil {
		return nil
	}
	return a.liveEvents
}

// buildLiveEvents constructs the live events hub, A5/A6 watch matcher and
// ticker, and A7 favourite-entity alert engine/scheduler bound to the plugin
// host and (when clustered) the cluster runtime. Safe to call multiple times.
func (a *App) buildLiveEvents() {
	if a == nil || a.liveEvents != nil || a.pluginsRT == nil || a.pluginsRT.mgr == nil {
		return
	}
	var cl cluster.Cluster
	var cbus clusterbus.Bus
	if a.clustered() {
		cl = a.cluster
		cbus = a.clusterNode()
	}
	var log *slog.Logger
	if a.log != nil {
		log = a.log.With("component", "liveevents")
	}
	hub := liveevents.NewHub(liveevents.Options{
		Source:     liveevents.NewManagerSource(a.pluginsRT.mgr),
		DB:         a.db,
		Cluster:    cl,
		ClusterBus: cbus,
		Log:        log,
	})
	a.liveEvents = hub

	watchMatcher := liveevents.NewWatchMatcher(liveevents.WatchMatcherOptions{
		Guide:            appLiveTVGuideSource{app: a},
		ActiveEvents:     hubActiveEventsSource{hub: hub},
		FavoriteEntities: appFavoriteEntities{app: a}.ListEntityKeys,
		FavoriteChannels: appFavoriteEntities{app: a}.ListChannelIDs,
	})
	a.liveWatchMatcher = watchMatcher

	// Wire A5 watch matcher into W255 hub (events/stream + detail, playable targets only).
	hub.SetWatchEnricher(func(ctx context.Context, viewer *core.Viewer, events []core.LiveEvent) {
		if viewer == nil || len(events) == 0 {
			return
		}
		byID, err := watchMatcher.WatchTargetsBatch(ctx, events, viewer)
		if err != nil {
			return
		}
		for i := range events {
			if targets := byID[events[i].ID]; len(targets) > 0 {
				events[i].Watch = targets
			}
		}
	})
	hub.SetDetailWatchEnricher(func(ctx context.Context, viewer *core.Viewer, detail *core.LiveEventDetail) {
		if viewer == nil || detail == nil {
			return
		}
		targets, err := watchMatcher.WatchTargets(ctx, detail.LiveEvent, viewer)
		if err == nil && len(targets) > 0 {
			detail.Watch = targets
		}
	})

	// Wire W256 screens to W255's hub and W257's watch matcher.
	if a.pluginsRT.screens != nil {
		a.pluginsRT.screens.SetHub(liveEventsScreenHub{hub: hub})
		a.pluginsRT.screens.SetWatchTargets(watchTargetAdapter{matcher: watchMatcher})
		hub.SetScreenFeedResolver(func(ctx context.Context, viewer *core.Viewer, screenID string) ([]liveevents.FeedRef, error) {
			return a.liveScreenFeeds(ctx, viewer, screenID)
		})
	}

	// Wire A6 compact ticker service.
	a.liveTicker = liveevents.NewTickerService(liveevents.TickerServiceOptions{
		Source: appTickerSource{app: a, hub: hub},
		Policy: hubAlertPolicy{hub: hub},
	})

	// Wire A7 alert engine and bounded-window scheduler into W255's unified
	// AlertHub (/vela/v0/live/stream?alerts=1) + Flows bus + ViewerPush.
	if a.db != nil {
		favReader := liveevents.NewSQLFavoriteEntitiesReader(a.db)
		alertEngine := liveevents.NewAlertEngine(liveevents.AlertEngineOptions{
			Favorites: favReader,
			Policy:    hubAlertPolicy{hub: hub},
			Bus:       a.EventBus(),
			Push:      appViewerPushDeliverer{app: a},
			InApp:     hub.AlertDeliverer(),
		})
		a.liveAlertEngine = alertEngine
		a.liveAlertScheduler = liveevents.NewAlertScheduler(liveevents.AlertSchedulerOptions{
			Favorites: favReader,
			Poller:    hubAlertPoller{hub: hub},
			Engine:    alertEngine,
		})
		hub.OnEventChange(func(_ *core.LiveEvent, next core.LiveEvent) {
			if !hub.IsLeading() {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = alertEngine.ProcessEvents(ctx, []core.LiveEvent{next})
		})
	}

	a.pluginsRT.mgr.OnSync(func(ctx context.Context, _ plugins.Record) {
		hub.SyncFeeds(ctx)
		if a.liveAlertScheduler != nil {
			a.liveAlertScheduler.NotifyFavoritesChanged()
		}
	})
	a.pluginsRT.mgr.OnRemove(func(ctx context.Context, _ string) {
		hub.SyncFeeds(ctx)
		if a.liveAlertScheduler != nil {
			a.liveAlertScheduler.NotifyFavoritesChanged()
		}
	})
	a.closers = append(a.closers, hub.Close)
}

func (a *App) liveScreenFeeds(ctx context.Context, viewer *core.Viewer, screenID string) ([]liveevents.FeedRef, error) {
	if a == nil || a.pluginsRT == nil || a.pluginsRT.screens == nil || viewer == nil {
		return nil, nil
	}
	reg := a.pluginsRT.screens.Registry()
	if reg == nil {
		return nil, nil
	}
	sc, ok, err := reg.FindScreen(ctx, viewer, "vela", screenID)
	if err != nil || !ok {
		return nil, err
	}
	seen := map[string]bool{}
	var refs []liveevents.FeedRef
	addFeed := func(feed string) {
		if feed == "" || seen[feed] {
			return
		}
		seen[feed] = true
		refs = append(refs, liveevents.FeedRef{PluginID: sc.PluginID, FeedID: feed})
	}
	addFeed(sc.DefaultFeed)
	for _, sec := range sc.Sections {
		if sec.Feed != "" {
			addFeed(sec.Feed)
		} else if sec.Query != nil && sec.Query.Feed != "" {
			addFeed(sec.Query.Feed)
		}
	}
	return refs, nil
}

// runLiveEvents runs the hub's cluster leadership/bus lifecycle and the A7
// favourite-entity alert scheduler until ctx is canceled. Idle cost is ~0
// when no clients are subscribed and no viewer has favourite entities.
func (a *App) runLiveEvents(ctx context.Context) {
	if a == nil || a.liveEvents == nil {
		return
	}
	var wg sync.WaitGroup
	if a.liveAlertScheduler != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.clustered() {
				a.cluster.RunAsLeader(ctx, "live event alerts", a.liveAlertScheduler.Run)
			} else {
				a.liveAlertScheduler.Run(ctx)
			}
		}()
	}
	a.liveEvents.Start(ctx)
	wg.Wait()
}

type watchTargetAdapter struct {
	matcher *liveevents.WatchMatcher
}

func (w watchTargetAdapter) WatchTargets(ctx context.Context, viewer *core.User, event core.LiveEvent) ([]core.LiveEventWatchTarget, error) {
	if w.matcher == nil {
		return nil, nil
	}
	return w.matcher.WatchTargets(ctx, event, viewer)
}

func (w watchTargetAdapter) WatchTargetsBatch(ctx context.Context, viewer *core.User, events []core.LiveEvent) (map[string][]core.LiveEventWatchTarget, error) {
	if w.matcher == nil {
		return nil, nil
	}
	return w.matcher.WatchTargetsBatch(ctx, events, viewer)
}

type appLiveTVGuideSource struct {
	app *App
}

func (g appLiveTVGuideSource) source() liveevents.GuideSource {
	if g.app == nil || g.app.livetv == nil {
		return nil
	}
	return liveevents.NewLiveTVGuideSource(g.app.livetv)
}

func (g appLiveTVGuideSource) GuideRevision(ctx context.Context, viewer *core.User) (string, error) {
	src := g.source()
	if src == nil {
		return "none", nil
	}
	return src.GuideRevision(ctx, viewer)
}

func (g appLiveTVGuideSource) WatchCandidates(ctx context.Context, viewer *core.User, q liveevents.GuideWatchQuery) ([]liveevents.GuideCandidate, string, error) {
	src := g.source()
	if src == nil {
		return nil, "none", nil
	}
	return src.WatchCandidates(ctx, viewer, q)
}

func (g appLiveTVGuideSource) CurrentProgramme(ctx context.Context, viewer *core.User, channelID string, at time.Time) (*liveevents.GuideCandidate, string, error) {
	src := g.source()
	if src == nil {
		return nil, "", nil
	}
	return src.CurrentProgramme(ctx, viewer, channelID, at)
}

type hubActiveEventsSource struct {
	hub *liveevents.Hub
}

func (s hubActiveEventsSource) ActiveEvents(ctx context.Context, _ *core.User, _ time.Time) ([]core.LiveEvent, string, error) {
	if s.hub == nil {
		return nil, "empty", nil
	}
	feeds := s.hub.Feeds(ctx)
	if len(feeds) == 0 {
		return nil, "empty", nil
	}
	byID := make(map[string]core.LiveEvent)
	for _, f := range feeds {
		resp, err := s.hub.FetchEvents(ctx, f.PluginID, f.FeedID, liveevents.EventQuery{})
		if err != nil {
			continue
		}
		for _, ev := range resp.Events {
			byID[ev.ID] = ev
		}
	}
	if len(byID) == 0 {
		return nil, "empty", nil
	}
	out := make([]core.LiveEvent, 0, len(byID))
	for _, ev := range byID {
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	h := sha256.New()
	for _, ev := range out {
		_, _ = fmt.Fprintf(h, "%s|%s|%s|", ev.ID, ev.Status.State, ev.UpdatedAt)
	}
	return out, hex.EncodeToString(h.Sum(nil)[:8]), nil
}

type appTickerSource struct {
	app *App
	hub *liveevents.Hub
}

func (s appTickerSource) TickerEvents(ctx context.Context, viewer *core.User, screen string, _ time.Time) ([]core.LiveEvent, error) {
	if s.hub == nil {
		return nil, nil
	}
	var refs []liveevents.FeedRef
	if screen != "" && s.app != nil {
		resolved, err := s.app.liveScreenFeeds(ctx, viewer, screen)
		if err != nil {
			return nil, err
		}
		refs = resolved
	} else {
		for _, f := range s.hub.Feeds(ctx) {
			refs = append(refs, liveevents.FeedRef{PluginID: f.PluginID, FeedID: f.FeedID})
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	byID := make(map[string]core.LiveEvent)
	for _, ref := range refs {
		resp, err := s.hub.FetchEvents(ctx, ref.PluginID, ref.FeedID, liveevents.EventQuery{})
		if err != nil {
			continue
		}
		for _, ev := range resp.Events {
			byID[ev.ID] = ev
		}
	}
	out := make([]core.LiveEvent, 0, len(byID))
	for _, ev := range byID {
		out = append(out, ev)
	}
	return out, nil
}

type hubAlertPolicy struct {
	hub *liveevents.Hub
}

func (p hubAlertPolicy) SpoilerFree(ctx context.Context, viewerID int64) (bool, error) {
	if p.hub == nil {
		return false, nil
	}
	return p.hub.SpoilerFree(ctx, viewerID)
}

func (p hubAlertPolicy) EventRevealed(ctx context.Context, viewerID int64, eventID string) (bool, error) {
	if p.hub == nil {
		return false, nil
	}
	return p.hub.IsEventRevealed(ctx, viewerID, eventID)
}

type appViewerPushDeliverer struct {
	app *App
}

func (d appViewerPushDeliverer) DeliverLiveEventAlert(ctx context.Context, viewerID int64, alert core.LiveEventAlert) error {
	if d.app == nil {
		return nil
	}
	push := d.app.viewerPush()
	if push == nil {
		return nil
	}
	return push.DeliverLiveEventAlert(ctx, viewerID, alert)
}

type hubAlertPoller struct {
	hub *liveevents.Hub
}

func (p hubAlertPoller) FetchSchedule(ctx context.Context, pluginID, feedID string, from, to time.Time) (liveevents.FeedPollResult, error) {
	if p.hub == nil {
		return liveevents.FeedPollResult{}, nil
	}
	resp, err := p.hub.FetchEvents(ctx, pluginID, feedID, liveevents.EventQuery{
		From: from.UTC().Format(time.RFC3339),
		To:   to.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return liveevents.FeedPollResult{}, err
	}
	return liveevents.FeedPollResult{Events: resp.Events, NextPollSec: resp.NextPollSec}, nil
}

func (p hubAlertPoller) PollEvents(ctx context.Context, pluginID, feedID string, eventIDs []string) (liveevents.FeedPollResult, error) {
	if p.hub == nil || len(eventIDs) == 0 {
		return liveevents.FeedPollResult{}, nil
	}
	localIDs := make([]string, 0, len(eventIDs))
	for _, raw := range eventIDs {
		if _, _, localID, ok := core.ParseLiveEventID(raw); ok {
			localIDs = append(localIDs, localID)
		} else if raw != "" {
			localIDs = append(localIDs, raw)
		}
	}
	resp, err := p.hub.FetchEvents(ctx, pluginID, feedID, liveevents.EventQuery{IDs: localIDs})
	if err != nil {
		return liveevents.FeedPollResult{}, err
	}
	return liveevents.FeedPollResult{Events: resp.Events, NextPollSec: resp.NextPollSec}, nil
}

type surfaceLiveEventLink struct {
	app *App
}

func (s surfaceLiveEventLink) LiveEventForChannel(ctx context.Context, viewer *frontend.Viewer, channelID string) (string, *frontend.LiveEventRef, error) {
	if s.app == nil || s.app.liveWatchMatcher == nil {
		return "", nil, nil
	}
	return s.app.liveWatchMatcher.LiveEventForChannel(ctx, viewer, channelID)
}

type surfaceLiveEventTicker struct {
	app *App
}

func (s surfaceLiveEventTicker) LiveTicker(ctx context.Context, viewer *frontend.Viewer, screen string) ([]frontend.LiveTickerItem, error) {
	if s.app == nil || s.app.liveTicker == nil {
		return nil, frontend.ErrNotAvailable
	}
	return s.app.liveTicker.LiveTicker(ctx, viewer, screen)
}

type surfaceLiveEvents struct {
	app *App
}

func (s surfaceLiveEvents) hub() *liveevents.Hub {
	if s.app == nil {
		return nil
	}
	return s.app.liveEvents
}

func (s surfaceLiveEvents) Available(ctx context.Context) bool {
	hub := s.hub()
	if hub == nil {
		return false
	}
	return hub.HasLiveFeeds(ctx)
}

func (s surfaceLiveEvents) Events(ctx context.Context, viewer *frontend.Viewer, ids []string) ([]frontend.LiveEvent, error) {
	hub := s.hub()
	if hub == nil {
		return nil, frontend.ErrNotAvailable
	}
	events, err := hub.GetEvents(ctx, viewer, ids)
	if err != nil {
		return nil, mapLiveEventError(err)
	}
	return events, nil
}

func (s surfaceLiveEvents) Detail(ctx context.Context, viewer *frontend.Viewer, id string) (*frontend.LiveEventDetail, error) {
	hub := s.hub()
	if hub == nil {
		return nil, frontend.ErrNotAvailable
	}
	detail, err := hub.GetDetail(ctx, viewer, id)
	if err != nil {
		return nil, mapLiveEventError(err)
	}
	return detail, nil
}

func (s surfaceLiveEvents) Subscribe(ctx context.Context, viewer *frontend.Viewer, req frontend.LiveEventStreamRequest) (frontend.LiveEventStream, error) {
	hub := s.hub()
	if hub == nil {
		return nil, frontend.ErrNotAvailable
	}
	sub, err := hub.Subscribe(ctx, liveevents.SubscribeRequest{
		EventIDs:       req.EventIDs,
		Screens:        req.Screens,
		Viewer:         viewer,
		FilterSpoilers: true,
		Alerts:         req.Alerts,
	})
	if err != nil {
		return nil, mapLiveEventError(err)
	}
	initial, err := hub.SubscriptionSnapshot(ctx, sub)
	if err != nil {
		sub.Close()
		return nil, mapLiveEventError(err)
	}
	return &liveEventStreamAdapter{sub: sub, initial: initial}, nil
}

func (s surfaceLiveEvents) Reveal(ctx context.Context, viewer *frontend.Viewer, id string) error {
	hub := s.hub()
	if hub == nil {
		return frontend.ErrNotAvailable
	}
	_, err := hub.RevealEvent(ctx, viewer, id)
	return mapLiveEventError(err)
}

func (s surfaceLiveEvents) SpoilerFree(ctx context.Context, viewer *frontend.Viewer) (bool, error) {
	hub := s.hub()
	if hub == nil {
		return false, frontend.ErrNotAvailable
	}
	if viewer == nil || viewer.ID <= 0 {
		return false, frontend.ErrUnauthorized
	}
	sf, err := hub.SpoilerFree(ctx, viewer.ID)
	if err != nil {
		return false, mapLiveEventError(err)
	}
	return sf, nil
}

func (s surfaceLiveEvents) SetSpoilerFree(ctx context.Context, viewer *frontend.Viewer, enabled bool) error {
	hub := s.hub()
	if hub == nil {
		return frontend.ErrNotAvailable
	}
	if viewer == nil || viewer.ID <= 0 {
		return frontend.ErrUnauthorized
	}
	return mapLiveEventError(hub.SetSpoilerFree(ctx, viewer.ID, enabled))
}

type liveEventStreamAdapter struct {
	sub     *liveevents.Subscription
	initial []frontend.LiveEvent
}

func (a *liveEventStreamAdapter) Initial() []frontend.LiveEvent          { return a.initial }
func (a *liveEventStreamAdapter) Updates() <-chan []frontend.LiveEvent   { return a.sub.Updates() }
func (a *liveEventStreamAdapter) Alerts() <-chan frontend.LiveEventAlert { return a.sub.Alerts() }
func (a *liveEventStreamAdapter) Close()                                 { a.sub.Close() }

func mapLiveEventError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, liveevents.ErrNotFound):
		return frontend.ErrNotFound
	case errors.Is(err, liveevents.ErrInvalidRequest):
		return frontend.ErrInvalidPreferences
	case errors.Is(err, liveevents.ErrRateLimited):
		return frontend.ErrRateLimited
	case errors.Is(err, liveevents.ErrTooManySubscribers):
		return frontend.ErrReceiptCapacity
	case errors.Is(err, liveevents.ErrUnavailable):
		return frontend.ErrSourceUnavailable
	case errors.Is(err, core.ErrUnauthorized):
		return frontend.ErrUnauthorized
	default:
		return err
	}
}
