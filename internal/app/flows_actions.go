package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/flows/actions"
	"github.com/braedonsaunders/ether/internal/notify"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/resolver"
)

// flowActionDeps wires Ether's live subsystems to the narrow interfaces
// consumed by built-in Flow actions.
func (a *App) flowActionDeps() actions.Deps {
	var rstore *actions.RestrictionStore
	if a.db != nil {
		rstore = actions.NewRestrictionStore(a.db, nil)
	}
	var exportSink actions.ExportSink
	if a.opts.DataDir != "" {
		exportSink = &actions.FileExportSink{
			Dir:             filepath.Join(a.opts.DataDir, "flows-exports"),
			BaseDownloadURL: "/api/flows/exports",
			MaxFiles:        50,
		}
	}
	var notifier actions.Notifier
	if a.notifications != nil {
		notifier = a.notifications
	}
	return actions.Deps{
		Notify:       notifier,
		Playback:     appFlowPlayback{app: a},
		Viewers:      appFlowViewers{app: a},
		Restrictions: rstore,
		Libraries:    appFlowLibraries{app: a},
		Exports:      exportSink,
		Digests:      appFlowDigests{app: a},
		Sources:      appFlowSources{app: a},
		Releases:     appFlowReleases{app: a},
		Log:          a.log,
	}
}

// registerBuiltinFlowActions registers all 13 built-in Flow actions backed by
// this App's subsystems.
func (a *App) registerBuiltinFlowActions(reg flows.ActionRegistry) error {
	return actions.RegisterAll(reg, a.flowActionDeps())
}

// startNotifyEventsBridge wires the non-blocking events.Bus -> notify.Service
// bridge so legacy notification channel rules keep working unchanged.
func (a *App) startNotifyEventsBridge(ctx context.Context, bus *events.Bus, opts notify.BridgeOptions) (*notify.BusBridge, error) {
	if a.notifications == nil || bus == nil {
		return nil, nil
	}
	if opts.Logger == nil {
		opts.Logger = a.log
	}
	return notify.StartBusBridge(ctx, a.notifications, bus, opts)
}

// --- playback.stop_session adapter ---

type appFlowPlayback struct {
	app *App
}

func (p appFlowPlayback) ListSessions(ctx context.Context) ([]actions.ActiveSession, error) {
	if p.app == nil {
		return nil, nil
	}
	raw := ownerPlayback{Playback: p.app.playback, app: p.app}.Sessions()
	out := make([]actions.ActiveSession, 0, len(raw))
	for _, s := range raw {
		if s == nil {
			continue
		}
		var viewerRole string
		if p.app.users != nil && s.UserID > 0 {
			if u, err := p.app.users.User(ctx, s.UserID); err == nil && u != nil && u.IsOwner {
				viewerRole = "owner"
			}
		}
		var delivery string
		var videoHeight, bitrateKbps int
		if s.DeliveryDecision != nil {
			delivery = strings.ToLower(strings.TrimSpace(string(s.DeliveryDecision.Kind)))
			videoHeight = s.DeliveryDecision.MaxHeight
			bitrateKbps = s.DeliveryDecision.BitrateKbps
		}
		if delivery == "" {
			delivery = strings.ToLower(strings.TrimSpace(string(s.Decision)))
		}
		if videoHeight == 0 {
			videoHeight = s.Client.MaxHeight
		}
		if bitrateKbps == 0 {
			bitrateKbps = s.Client.MaxBitrateKbps
		}
		hasVideo := s.Info.Video() != nil
		isTranscode := delivery == "transcode" || (hasVideo && !s.DecisionVideoCopy && delivery != "" && delivery != "direct" && delivery != "direct_play" && delivery != "directplay" && delivery != "remux" && delivery != "copy")
		videoDecision := "direct_play"
		if !hasVideo {
			videoDecision = "none"
		} else if isTranscode {
			videoDecision = "transcode"
		} else if s.DecisionVideoCopy {
			videoDecision = "copy"
		}
		state := strings.ToLower(strings.TrimSpace(s.ViewerState))
		if state == "" {
			state = "playing"
		}
		var pausedSince time.Time
		if state == "paused" {
			pausedSince = s.LastActive
		}
		clientName := strings.TrimSpace(s.Client.DeviceName)
		if clientName == "" {
			clientName = strings.TrimSpace(s.Client.Product)
		}
		vid := ""
		if s.UserID > 0 {
			vid = strconv.FormatInt(s.UserID, 10)
		}
		out = append(out, actions.ActiveSession{
			ID:            s.ID,
			ClientSession: s.Client.ClientID,
			ViewerID:      vid,
			Username:      s.Username,
			ViewerRole:    viewerRole,
			ClientID:      s.ClientID,
			ClientName:    clientName,
			Platform:      s.Client.Platform,
			TitleKey:      s.Key.String(),
			TitleName:     s.TitleName,
			State:         state,
			IsTranscode:   isTranscode,
			VideoDecision: videoDecision,
			VideoHeight:   videoHeight,
			BitrateKbps:   bitrateKbps,
			IsRemote:      s.Client.Remote,
			StartedAt:     s.StartedAt,
			PausedSince:   pausedSince,
			LastActive:    s.LastActive,
			Node:          s.Node,
		})
	}
	return out, nil
}

func (p appFlowPlayback) StopSession(ctx context.Context, sessionID, message string) error {
	if p.app == nil {
		return errors.New("app unavailable")
	}
	return p.app.stopOwnerSessionWithReason(ctx, sessionID, message)
}

// --- viewer.restrict / viewer.unrestrict adapter ---

type appFlowViewers struct {
	app *App
}

func (v appFlowViewers) parseViewerID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid viewerId %q", raw)
	}
	return id, nil
}

func (v appFlowViewers) GetViewerPolicy(ctx context.Context, viewerID string) (actions.ViewerPolicySnapshot, error) {
	if v.app == nil || v.app.users == nil {
		return actions.ViewerPolicySnapshot{}, errors.New("viewer service unavailable")
	}
	uid, err := v.parseViewerID(viewerID)
	if err != nil {
		return actions.ViewerPolicySnapshot{}, err
	}
	u, err := v.app.users.User(ctx, uid)
	if err != nil {
		return actions.ViewerPolicySnapshot{}, err
	}
	snap := actions.ViewerPolicySnapshot{
		ViewerID:       viewerID,
		StreamLimit:    u.StreamLimit,
		AccountEnabled: true,
	}
	if v.app.safety != nil {
		pol := v.app.safety.Get(uid)
		snap.RemoteDisabled = pol.RemoteDisabled
		snap.MaxBitrateKbps = pol.MaxBitrateKbps
	}
	return snap, nil
}

func (v appFlowViewers) ApplyViewerPolicy(ctx context.Context, viewerID string, patch actions.ViewerRestrictionPatch) error {
	if v.app == nil || v.app.users == nil {
		return errors.New("viewer service unavailable")
	}
	uid, err := v.parseViewerID(viewerID)
	if err != nil {
		return err
	}
	if (patch.RemoteDisabled != nil || patch.MaxBitrateKbps != nil) && v.app.safety != nil {
		pol := v.app.safety.Get(uid)
		if patch.RemoteDisabled != nil {
			pol.RemoteDisabled = *patch.RemoteDisabled
		}
		if patch.MaxBitrateKbps != nil {
			pol.MaxBitrateKbps = *patch.MaxBitrateKbps
		}
		if err := v.app.safety.Put(ctx, uid, pol); err != nil {
			return err
		}
	}
	if patch.StreamLimit != nil {
		u, err := v.app.users.User(ctx, uid)
		if err != nil {
			return err
		}
		updated := *u
		updated.StreamLimit = *patch.StreamLimit
		if err := v.app.users.UpdateUser(ctx, updated); err != nil {
			return err
		}
	}
	if patch.AccountEnabled != nil {
		if *patch.AccountEnabled {
			if err := v.app.users.ResumeViewer(ctx, uid); err != nil {
				return err
			}
		} else {
			if err := v.app.users.PauseViewer(ctx, uid); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- library.refresh & library.export adapter ---

type appFlowLibraries struct {
	app *App
}

func (l appFlowLibraries) RefreshLibrary(ctx context.Context, libraryID string, fullSync bool) (actions.RefreshResult, error) {
	if l.app == nil {
		return actions.RefreshResult{}, errors.New("app unavailable")
	}
	if libraryID == "" {
		if l.app.catalog == nil {
			return actions.RefreshResult{}, errors.New("catalog unavailable")
		}
		if err := l.app.catalog.Sync(ctx); err != nil {
			return actions.RefreshResult{}, err
		}
		return actions.RefreshResult{Target: "catalog", Kind: "catalog"}, nil
	}

	if l.app.libraries == nil {
		return actions.RefreshResult{}, errors.New("libraries service unavailable")
	}
	lib, err := l.resolveLibrary(ctx, libraryID)
	if err != nil {
		return actions.RefreshResult{}, err
	}
	switch lib.Source {
	case core.LibraryPlugin:
		if l.app.pluginsRT == nil || l.app.pluginsRT.pluginitems == nil {
			return actions.RefreshResult{}, errors.New("plugin items service unavailable")
		}
		res, err := l.app.pluginsRT.pluginitems.SyncKind(ctx, lib.PluginID, lib.KindKey, pluginitems.SyncOptions{
			Force:    fullSync,
			FullWalk: fullSync,
		})
		if err != nil {
			return actions.RefreshResult{}, err
		}
		return actions.RefreshResult{
			Target: strconv.FormatInt(int64(lib.ID), 10),
			Kind:   "plugin_library",
			Items:  res.Upserted + res.Skipped,
		}, nil

	case core.LibraryCollection:
		if l.app.collections == nil {
			return actions.RefreshResult{}, errors.New("collections service unavailable")
		}
		collID := strconv.FormatInt(lib.CollectionID, 10)
		if _, err := l.app.collections.Rebuild(ctx, collID); err != nil {
			return actions.RefreshResult{}, err
		}
		return actions.RefreshResult{
			Target: collID,
			Kind:   "collection_library",
		}, nil

	default:
		if len(lib.Sources) > 0 && l.app.catalog != nil {
			if err := l.app.catalog.BrowseSourceLibrary(ctx, lib.Sources); err != nil {
				return actions.RefreshResult{}, err
			}
			return actions.RefreshResult{
				Target: strconv.FormatInt(int64(lib.ID), 10),
				Kind:   "source_library",
			}, nil
		}
		if l.app.catalog == nil {
			return actions.RefreshResult{}, errors.New("catalog unavailable")
		}
		if err := l.app.catalog.Sync(ctx); err != nil {
			return actions.RefreshResult{}, err
		}
		return actions.RefreshResult{
			Target: strconv.FormatInt(int64(lib.ID), 10),
			Kind:   "builtin_library",
		}, nil
	}
}

func (l appFlowLibraries) RefreshCollection(ctx context.Context, collectionID string) (actions.RefreshResult, error) {
	if l.app == nil || l.app.collections == nil {
		return actions.RefreshResult{}, errors.New("collections service unavailable")
	}
	if _, err := l.app.collections.Rebuild(ctx, collectionID); err != nil {
		return actions.RefreshResult{}, err
	}
	return actions.RefreshResult{Target: collectionID, Kind: "collection"}, nil
}

func (l appFlowLibraries) ListExportItems(ctx context.Context, q actions.ExportQuery) ([]actions.ExportItem, error) {
	if l.app == nil || l.app.catalog == nil {
		return nil, errors.New("catalog unavailable")
	}
	limit := q.MaxItems
	if limit <= 0 || limit > 50000 {
		limit = 5000
	}

	switch q.Scope {
	case "item":
		rk, err := core.ParseRatingKey(strings.TrimSpace(q.TargetID))
		if err != nil {
			return nil, fmt.Errorf("invalid item ratingKey %q: %w", q.TargetID, err)
		}
		t, err := l.app.catalog.Get(ctx, rk)
		if err != nil {
			return nil, err
		}
		return []actions.ExportItem{titleToExportItem(*t, "", "")}, nil

	case "collection":
		if l.app.collections == nil {
			return nil, errors.New("collections service unavailable")
		}
		key, err := l.resolveCollectionKey(ctx, q.TargetID)
		if err != nil {
			return nil, err
		}
		page, err := l.app.collections.SnapshotItems(ctx, key, 0, limit)
		if err != nil {
			return nil, err
		}
		return titlesToExportItems(page.Items, q.TargetID, ""), nil

	case "library":
		lib, err := l.resolveLibrary(ctx, q.TargetID)
		if err != nil {
			return nil, err
		}
		return l.browseLibraryExport(ctx, lib, limit)

	case "server", "playlist":
		var all []actions.ExportItem
		for _, mt := range []core.MediaType{core.TypeMovie, core.TypeShow} {
			if len(all) >= limit {
				break
			}
			page, err := l.app.catalog.Browse(ctx, core.BrowseQuery{
				Filter:  core.Filter{Type: mt},
				Limit:   limit - len(all),
				NoTotal: true,
			})
			if err != nil {
				return nil, err
			}
			all = append(all, titlesToExportItems(page.Items, "", string(mt))...)
		}
		return all, nil

	default:
		return nil, fmt.Errorf("unsupported export scope %q", q.Scope)
	}
}

func (l appFlowLibraries) resolveCollectionKey(ctx context.Context, rawID string) (core.RatingKey, error) {
	rawID = strings.TrimSpace(rawID)
	if rk, err := core.ParseRatingKey(rawID); err == nil && (rk.Kind() == core.KindCollection || rk.Kind() == core.KindSmart) {
		return rk, nil
	}
	if spec, err := l.app.collections.GetSpec(ctx, rawID); err == nil && spec != nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(spec.ID), 10, 64); err == nil && n > 0 {
			return core.SmartKey(n), nil
		}
	}
	if n, err := strconv.ParseInt(rawID, 10, 64); err == nil && n > 0 {
		return core.SmartKey(n), nil
	}
	return 0, fmt.Errorf("collection %q not found", rawID)
}

func (l appFlowLibraries) resolveLibrary(ctx context.Context, rawID string) (core.Library, error) {
	if idNum, err := strconv.ParseInt(strings.TrimSpace(rawID), 10, 64); err == nil {
		if lib, err := l.app.libraries.Get(ctx, core.LibraryID(idNum)); err == nil {
			return lib, nil
		}
	}
	libs, err := l.app.libraries.List(ctx)
	if err != nil {
		return core.Library{}, err
	}
	for _, lib := range libs {
		if strings.EqualFold(lib.Key, rawID) || strings.EqualFold(lib.Name, rawID) {
			return lib, nil
		}
	}
	return core.Library{}, fmt.Errorf("library %q not found", rawID)
}

func (l appFlowLibraries) browseLibraryExport(ctx context.Context, lib core.Library, limit int) ([]actions.ExportItem, error) {
	libIDStr := strconv.FormatInt(int64(lib.ID), 10)
	if lib.Source == core.LibraryCollection && l.app.collections != nil {
		page, err := l.app.collections.SnapshotItems(ctx, core.SmartKey(lib.CollectionID), 0, limit)
		if err != nil {
			return nil, err
		}
		return titlesToExportItems(page.Items, libIDStr, lib.Name), nil
	}
	page, err := l.app.catalog.Browse(ctx, core.BrowseQuery{
		Filter:  core.Filter{Type: lib.MediaType},
		Limit:   limit,
		NoTotal: true,
	})
	if err != nil {
		return nil, err
	}
	return titlesToExportItems(page.Items, libIDStr, lib.Name), nil
}

func titlesToExportItems(titles []core.Title, libID, libName string) []actions.ExportItem {
	out := make([]actions.ExportItem, 0, len(titles))
	for _, t := range titles {
		out = append(out, titleToExportItem(t, libID, libName))
	}
	return out
}

func titleToExportItem(t core.Title, libID, libName string) actions.ExportItem {
	var addedAt string
	if !t.AddedAt.IsZero() {
		addedAt = t.AddedAt.UTC().Format(time.RFC3339)
	}
	var extIDs []string
	if t.IDs.TMDB > 0 {
		extIDs = append(extIDs, "tmdb:"+strconv.FormatInt(t.IDs.TMDB, 10))
	}
	if t.IDs.IMDB != "" {
		extIDs = append(extIDs, "imdb:"+t.IDs.IMDB)
	}
	if t.IDs.TVDB > 0 {
		extIDs = append(extIDs, "tvdb:"+strconv.FormatInt(t.IDs.TVDB, 10))
	}
	var studio string
	if len(t.Studios) > 0 {
		studio = t.Studios[0]
	}
	directors := make([]string, 0, len(t.Directors))
	for _, p := range t.Directors {
		if strings.TrimSpace(p.Name) != "" {
			directors = append(directors, p.Name)
		}
	}
	poster := t.PosterPath
	if poster == "" {
		poster = t.StillPath
	}
	return actions.ExportItem{
		Key:           t.Key.String(),
		Title:         t.Title,
		ShowTitle:     t.ShowTitle,
		Season:        t.Season,
		Episode:       t.Episode,
		MediaType:     string(t.Type),
		Year:          t.Year,
		LibraryID:     libID,
		LibraryName:   libName,
		Summary:       t.Summary,
		ContentRating: t.ContentRating,
		Studio:        studio,
		Rating:        t.Rating,
		AddedAt:       addedAt,
		Genres:        append([]string(nil), t.Genres...),
		Directors:     directors,
		ExternalIDs:   extIDs,
		DurationMS:    t.Runtime.Milliseconds(),
		PosterURL:     poster,
		BackdropURL:   t.BackdropPath,
	}
}

// --- digest.generate adapter ---

type appFlowDigests struct {
	app *App
}

func (d appFlowDigests) RecentlyAdded(ctx context.Context, q actions.DigestQuery) ([]actions.DigestItem, error) {
	if d.app == nil || d.app.catalog == nil {
		return nil, errors.New("catalog unavailable")
	}
	wantType := map[string]bool{}
	for _, mt := range q.MediaTypes {
		wantType[strings.ToLower(strings.TrimSpace(mt))] = true
	}
	allow := func(mt string) bool {
		return len(wantType) == 0 || wantType[mt]
	}

	var out []actions.DigestItem
	if allow("movie") {
		if movies, err := d.app.catalog.RecentMovies(ctx, q.Since); err == nil {
			for _, m := range movies {
				out = append(out, actions.DigestItem{
					TitleKey:  m.Key.String(),
					Title:     m.Title,
					MediaType: "movie",
					Year:      m.Year,
					Summary:   m.Summary,
					Genres:    m.Genres,
					Rating:    m.Rating,
					PosterURL: m.PosterPath,
					AddedAt:   m.AddedAt,
				})
			}
		}
	}
	if allow("episode") {
		if eps, err := d.app.catalog.RecentEvidenceEpisodes(ctx, q.Since); err == nil {
			for _, ep := range eps {
				poster := ep.PosterPath
				if poster == "" {
					poster = ep.StillPath
				}
				out = append(out, actions.DigestItem{
					TitleKey:  ep.Key.String(),
					Title:     ep.Title,
					ShowKey:   ep.ShowKey.String(),
					ShowTitle: ep.ShowTitle,
					Season:    ep.Season,
					Episode:   ep.Episode,
					MediaType: "episode",
					Year:      ep.Year,
					Summary:   ep.Summary,
					Genres:    ep.Genres,
					PosterURL: poster,
					AddedAt:   ep.AddedAt,
				})
			}
		}
	}
	if allow("show") {
		if shows, err := d.app.catalog.RecentShows(ctx, q.Since); err == nil {
			for _, sh := range shows {
				out = append(out, actions.DigestItem{
					TitleKey:  sh.Key.String(),
					Title:     sh.Title,
					ShowKey:   sh.Key.String(),
					ShowTitle: sh.Title,
					MediaType: "show",
					Year:      sh.Year,
					Summary:   sh.Summary,
					Genres:    sh.Genres,
					Rating:    sh.Rating,
					PosterURL: sh.PosterPath,
					AddedAt:   sh.AddedAt,
				})
			}
		}
	}
	if q.MaxItems > 0 && len(out) > q.MaxItems {
		out = out[:q.MaxItems]
	}
	return out, nil
}

func (d appFlowDigests) ActivitySummary(ctx context.Context, since, until time.Time) (actions.DigestActivitySummary, error) {
	if d.app == nil || d.app.stats == nil {
		return actions.DigestActivitySummary{}, nil
	}
	sum, err := d.app.stats.Summary(ctx, since, until, 0)
	if err != nil {
		return actions.DigestActivitySummary{}, err
	}
	tops := make([]actions.DigestTopTitle, 0, min(len(sum.Titles), 5))
	for i, b := range sum.Titles {
		if i >= 5 {
			break
		}
		tops = append(tops, actions.DigestTopTitle{Title: b.Label, Plays: int(b.Plays)})
	}
	return actions.DigestActivitySummary{
		TotalPlays:    int(sum.Plays),
		UniqueViewers: len(sum.Users),
		HoursWatched:  float64(sum.WatchedMS) / float64(time.Hour/time.Millisecond),
		TopTitles:     tops,
	}, nil
}

// --- source.refresh adapter ---

type appFlowSources struct {
	app *App
}

func (s appFlowSources) RefreshSource(ctx context.Context, sourceKind, sourceID string) (actions.RefreshResult, error) {
	if s.app == nil {
		return actions.RefreshResult{}, errors.New("app unavailable")
	}
	switch sourceKind {
	case "livetv_guide":
		if s.app.livetv == nil {
			return actions.RefreshResult{}, errors.New("Live TV service unavailable")
		}
		if err := s.app.livetv.RefreshGuide(ctx); err != nil {
			return actions.RefreshResult{}, err
		}
		return actions.RefreshResult{Target: "guide", Kind: sourceKind}, nil

	case "livetv_source":
		if s.app.livetv == nil {
			return actions.RefreshResult{}, errors.New("Live TV service unavailable")
		}
		src, err := s.app.livetv.RefreshSource(ctx, sourceID)
		if err != nil {
			return actions.RefreshResult{}, err
		}
		return actions.RefreshResult{Target: src.ID, Kind: sourceKind, Items: src.ChannelCount}, nil

	case "livetv_vod":
		if s.app.livetv == nil {
			return actions.RefreshResult{}, errors.New("Live TV service unavailable")
		}
		if err := s.app.livetv.RefreshVOD(ctx, sourceID); err != nil {
			return actions.RefreshResult{}, err
		}
		return actions.RefreshResult{Target: sourceID, Kind: sourceKind}, nil

	case "watchlist":
		if s.app.watchlist == nil {
			return actions.RefreshResult{}, errors.New("watchlist service unavailable")
		}
		if sourceID != "" && s.app.users != nil {
			uid, err := strconv.ParseInt(sourceID, 10, 64)
			if err != nil || uid <= 0 {
				return actions.RefreshResult{}, fmt.Errorf("invalid viewerId %q", sourceID)
			}
			u, err := s.app.users.User(ctx, uid)
			if err != nil {
				return actions.RefreshResult{}, err
			}
			if err := s.app.watchlist.Sync(ctx, u); err != nil {
				return actions.RefreshResult{}, err
			}
			return actions.RefreshResult{Target: sourceID, Kind: sourceKind}, nil
		}
		if err := s.app.watchlist.SyncRSS(ctx); err != nil {
			return actions.RefreshResult{}, err
		}
		return actions.RefreshResult{Target: "rss", Kind: sourceKind}, nil

	case "watchlist_rss":
		if s.app.watchlist == nil {
			return actions.RefreshResult{}, errors.New("watchlist service unavailable")
		}
		if err := s.app.watchlist.SyncRSS(ctx); err != nil {
			return actions.RefreshResult{}, err
		}
		return actions.RefreshResult{Target: "rss", Kind: sourceKind}, nil

	case "plugin":
		if s.app.pluginsRT == nil || s.app.pluginsRT.pluginitems == nil {
			return actions.RefreshResult{}, errors.New("plugin items service unavailable")
		}
		pluginID, kindKey, hasKind := strings.Cut(sourceID, "/")
		if hasKind && pluginID != "" && kindKey != "" {
			res, err := s.app.pluginsRT.pluginitems.SyncKind(ctx, pluginID, kindKey, pluginitems.SyncOptions{})
			if err != nil {
				return actions.RefreshResult{}, err
			}
			return actions.RefreshResult{Target: sourceID, Kind: sourceKind, Items: res.Upserted + res.Skipped}, nil
		}
		if s.app.libraries == nil {
			return actions.RefreshResult{}, errors.New("libraries service unavailable")
		}
		libs, err := s.app.libraries.List(ctx)
		if err != nil {
			return actions.RefreshResult{}, err
		}
		total := 0
		matched := false
		for _, lib := range libs {
			if lib.Source == core.LibraryPlugin && lib.PluginID == sourceID {
				matched = true
				res, err := s.app.pluginsRT.pluginitems.SyncKind(ctx, lib.PluginID, lib.KindKey, pluginitems.SyncOptions{})
				if err != nil {
					return actions.RefreshResult{}, err
				}
				total += res.Upserted + res.Skipped
			}
		}
		if !matched {
			return actions.RefreshResult{}, fmt.Errorf("no plugin library found for plugin %q", sourceID)
		}
		return actions.RefreshResult{Target: sourceID, Kind: sourceKind, Items: total}, nil

	default:
		return actions.RefreshResult{}, fmt.Errorf("unsupported sourceKind %q", sourceKind)
	}
}

// --- release.pin & release.unpin adapter ---

type appFlowReleases struct {
	app *App
}

func (r appFlowReleases) PinRelease(ctx context.Context, titleKey, releaseID, note string) (actions.PinResult, error) {
	if r.app == nil || r.app.resolver == nil {
		return actions.PinResult{}, errors.New("resolver service unavailable")
	}
	rk, err := core.ParseRatingKey(strings.TrimSpace(titleKey))
	if err != nil {
		return actions.PinResult{}, fmt.Errorf("invalid titleKey %q: %w", titleKey, err)
	}
	rel, ok := r.app.resolver.LookupRelease(releaseID)
	if !ok {
		return actions.PinResult{}, resolver.ErrUnknownRelease
	}
	var titleName string
	if r.app.catalog != nil {
		if t, err := r.app.catalog.Get(ctx, rk); err == nil && t != nil {
			titleName = t.Title
		}
	}
	if _, err := r.app.resolver.Pin(ctx, rk, titleName, rel, note); err != nil {
		return actions.PinResult{}, err
	}
	if r.app.playback != nil {
		r.app.playback.InvalidateTitleEverywhere(rk)
	}
	return actions.PinResult{
		TitleKey:    rk.String(),
		TitleName:   titleName,
		ReleaseID:   rel.ID,
		ReleaseName: rel.Name,
	}, nil
}

func (r appFlowReleases) UnpinRelease(ctx context.Context, titleKey string) error {
	if r.app == nil || r.app.resolver == nil {
		return errors.New("resolver service unavailable")
	}
	rk, err := core.ParseRatingKey(strings.TrimSpace(titleKey))
	if err != nil {
		return fmt.Errorf("invalid titleKey %q: %w", titleKey, err)
	}
	if _, err := r.app.resolver.Unpin(ctx, rk); err != nil {
		return err
	}
	if r.app.playback != nil {
		r.app.playback.InvalidateTitleEverywhere(rk)
	}
	return nil
}
