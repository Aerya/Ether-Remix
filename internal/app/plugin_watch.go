package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/state"
)

func (a *App) pluginCatalogTitle(ctx context.Context, id string) (*core.Title, error) {
	var title *core.Title
	var err error
	switch provider, externalID, _ := strings.Cut(id, ":"); provider {
	case "imdb", "tvdb":
		title, err = a.catalog.ByExternalID(ctx, provider, externalID)
	case "tmdb":
		n, parseErr := strconv.ParseInt(externalID, 10, 64)
		if parseErr != nil || n <= 0 {
			return nil, errors.New("invalid TMDB id")
		}
		title, err = a.catalog.Get(ctx, core.MovieKey(n))
		if errors.Is(err, core.ErrNotFound) {
			title, err = a.catalog.Get(ctx, core.ShowKey(n))
		}
	default:
		key, parseErr := core.ParseRatingKey(id)
		if parseErr != nil {
			return nil, parseErr
		}
		title, err = a.catalog.Get(ctx, key)
	}
	if err == nil && title == nil {
		err = core.ErrNotFound
	}
	return title, err
}

func (a *App) setPluginWatchState(ctx context.Context, pluginID string, viewerID int64, titleID string, watched bool) (bool, error) {
	viewer, err := a.users.PolicyUser(ctx, viewerID)
	if err != nil || viewer == nil {
		return false, plugins.ErrPermissionDenied
	}
	ctx = core.WithCatalogViewer(ctx, viewer)
	var key core.RatingKey
	if strings.HasPrefix(titleID, "pk:") {
		ref := core.ItemRef(titleID)
		owner, kind, id, ok := ref.Parse()
		if !ok || owner != pluginID {
			return false, plugins.ErrNotFound
		}
		visible, err := a.adapterKindVisible()(ctx, pluginID, viewerID, kind, []string{id})
		if err != nil {
			return false, err
		}
		if len(visible) == 0 {
			return false, plugins.ErrNotFound
		}
		item, _, err := a.pluginsRT.pluginitems.GetByRef(ctx, ref)
		if err != nil || !item.Playable || item.Unavailable != nil {
			return false, plugins.ErrNotFound
		}
		key = item.Key
	} else {
		title, err := a.pluginCatalogTitle(ctx, titleID)
		if err != nil || (title.Type != core.TypeMovie && title.Type != core.TypeEpisode) || title.Key.IsPluginKey() {
			return false, plugins.ErrNotFound
		}
		visibility := experienceVisibility(a.safety, a.catalog.CachedGet, nil, nil, a.configuredLibraries())
		if _, err := a.admissionSessionTitle(ctx, viewer, title.Key, visibility, true); err != nil {
			return false, plugins.ErrNotFound
		}
		key = title.Key
	}

	current, err := a.state.Get(ctx, viewerID, key)
	if err != nil {
		return false, err
	}
	previous := state.IsWatched(current)
	if previous == watched && !state.IsInProgress(current) {
		return previous, nil
	}
	ctx = (adapterViewerState{app: a}).originCtx(ctx, pluginID)
	if watched {
		return previous, a.state.Scrobble(ctx, viewerID, key)
	}
	return previous, a.state.Unscrobble(ctx, viewerID, key)
}

func (a *App) publishViewerWatchEvent(ev state.Event) {
	imported := ev.Imported || ev.Account
	// Reviewed imports also emit synthetic historical scrobbles. Only the
	// current-state update (EventProgress) should trigger watch sync.
	if ev.Imported && !ev.Account && ev.Kind != state.EventProgress {
		return
	}
	// Report resume checkpoints rather than the periodic playing timeline.
	if ev.Kind == state.EventProgress && !imported && (!ev.ResumeSet || ev.Completed || ev.State == "playing") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload := events.ViewerWatchScrobbledPayload{
		ViewerID:  ev.UserID,
		Username:  a.lookupUsername(ctx, ev.UserID),
		TitleKey:  ev.Key.String(),
		TitleName: ev.Key.String(),
		MediaType: ratingKeyMediaType(ev.Key),
		Surface:   string(ev.Origin.Surface),
		Source:    "manual",
	}
	eventType := events.EventViewerWatchWatched
	switch ev.Kind {
	case state.EventUnscrobble:
		eventType = events.EventViewerWatchUnwatched
	case state.EventProgress:
		position := ev.Resume.Milliseconds()
		if ev.Imported && !ev.Account {
			if a.state == nil {
				return
			}
			status, err := a.state.Get(ctx, ev.UserID, ev.Key)
			if err != nil {
				return
			}
			position = status.ViewOffset.Milliseconds()
			if position == 0 {
				if !state.IsWatched(status) {
					eventType = events.EventViewerWatchUnwatched
				}
				break
			}
		}
		eventType = events.EventViewerWatchProgress
		payload.PositionMS = &position
	}
	switch eventType {
	case events.EventViewerWatchWatched:
		payload.State, payload.Watched = "watched", true
	case events.EventViewerWatchUnwatched:
		payload.State = "unwatched"
	default:
		payload.State = "progress"
	}
	busSource := "state"
	switch {
	case imported:
		payload.Source = "import"
	case strings.HasPrefix(payload.Surface, events.PluginPrefix):
		payload.Source, busSource = "plugin", payload.Surface
	case ev.Kind == state.EventCompleted || ev.Kind == state.EventProgress:
		payload.Source = "playback"
	}
	a.enrichViewerWatchEvent(ctx, ev.Key, &payload)
	correlation := events.WithCorrelation(events.Correlation{ViewerID: ev.UserID, TitleID: payload.TitleKey})
	a.publishEvent(eventType, payload, events.WithSource(busSource), correlation)
	if !imported && ev.Kind != state.EventProgress {
		a.publishEvent(events.EventViewerWatchScrobbled, payload, events.WithSource(busSource), correlation)
	}
}

func (a *App) enrichViewerWatchEvent(ctx context.Context, key core.RatingKey, payload *events.ViewerWatchScrobbledPayload) {
	if key.IsPluginKey() {
		if key.IsPluginItemKey() && a.pluginsRT != nil && a.pluginsRT.pluginitems != nil {
			if item, _, err := a.pluginsRT.pluginitems.GetByKey(ctx, key); err == nil {
				payload.ItemRef, payload.TitleName = string(item.Ref), item.Title
			}
		}
		return
	}

	switch key.Kind() {
	case core.KindMovie, core.KindShow, core.KindSeason, core.KindEpisode:
		payload.TMDBID = key.TMDB()
	default:
		return
	}
	if key.Kind() == core.KindEpisode || key.Kind() == core.KindSeason {
		season := key.Season()
		payload.Season = &season
	}
	if key.Kind() == core.KindEpisode {
		episode := key.Episode()
		payload.Episode = &episode
	}
	if a.catalog == nil {
		return
	}
	title, err := a.catalog.CachedGet(ctx, key)
	if err != nil {
		return
	}
	payload.TitleName = title.Title
	ids := title.IDs
	if title.ShowKey != 0 {
		ids = title.ShowIDs
		if (ids.TVDB == 0 || ids.IMDB == "") && title.ShowKey != key {
			if show, err := a.catalog.CachedGet(ctx, title.ShowKey); err == nil {
				if ids.TVDB == 0 {
					ids.TVDB = show.IDs.TVDB
				}
				if ids.IMDB == "" {
					ids.IMDB = show.IDs.IMDB
				}
			}
		}
	}
	payload.TVDBID, payload.IMDBID = ids.TVDB, ids.IMDB
}
