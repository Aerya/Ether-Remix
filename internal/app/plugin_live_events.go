package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/screens"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/httpjson"
	"github.com/braedonsaunders/ether/internal/liveevents"
	"github.com/braedonsaunders/ether/internal/plugins"
)

const (
	defaultConsoleLiveEventsLimit = 12
	maxConsoleLiveEventsLimit     = 50
)

var (
	consoleLiveEventsStreamMaxAge    = 5 * time.Minute
	consoleLiveEventsStreamHeartbeat = 25 * time.Second
)

type pluginScreenFrontendOverview struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Published bool   `json:"published"`
	Reason    string `json:"reason"`
	// Enabled is the owner's switch for a document screen or surface in this
	// app (absent for 0.3.19 section screens, which have none).
	Enabled *bool `json:"enabled,omitempty"`
}

type pluginScreenOverview struct {
	ID        string                         `json:"id"`
	Title     string                         `json:"title"`
	Icon      string                         `json:"icon,omitempty"`
	Feed      string                         `json:"feed,omitempty"`
	Frontends []pluginScreenFrontendOverview `json:"frontends"`
	// Kind is "screen" or "surface"; Presentation names a surface's slot.
	Kind         string `json:"kind,omitempty"`
	Presentation string `json:"presentation,omitempty"`
}

type pluginLiveEventSummary struct {
	ID          string                     `json:"id"`
	Feed        string                     `json:"feed"`
	League      core.LiveEventLeague       `json:"league"`
	Title       string                     `json:"title,omitempty"`
	Start       string                     `json:"start"`
	Status      core.LiveEventStatus       `json:"status"`
	Competitors []core.LiveEventCompetitor `json:"competitors"`
	Ranking     []core.LiveEventRankRow    `json:"ranking,omitempty"`
	Venue       string                     `json:"venue,omitempty"`
	WatchCount  int                        `json:"watchCount"`
}

type pluginLiveEventsOverview struct {
	PluginID      string                    `json:"pluginId"`
	Feeds         []liveevents.FeedOverview `json:"feeds"`
	Screens       []pluginScreenOverview    `json:"screens"`
	Events        []pluginLiveEventSummary  `json:"events"`
	TotalEvents   int                       `json:"totalEvents"`
	LiveCount     int                       `json:"liveCount"`
	UpcomingCount int                       `json:"upcomingCount"`
}

// wrapPluginLiveEventsHandler intercepts GET /{id}/live-events under /api/plugins
// so the plugin drawer Overview can read hub-cached live events, feed health,
// and screen publish state (and optionally hold a brief hub subscription while
// the drawer is open without forcing extra plugin polling).
func (a *App) wrapPluginLiveEventsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) == 2 && parts[1] == "live-events" && parts[0] != "" {
			a.handlePluginLiveEvents(w, r, parts[0])
			return
		}
		if len(parts) == 2 && parts[0] == "screens" && parts[1] == "overlay-order" {
			a.handleOverlayOrder(w, r)
			return
		}
		if len(parts) == 2 && parts[1] == "screen-publish" && parts[0] != "" {
			a.handleScreenPublish(w, r, parts[0])
			return
		}
		if len(parts) == 4 && parts[0] == "studio" && parts[1] == "projects" && parts[3] == "screen-preview" && parts[2] != "" {
			a.handleScreenPreview(w, r, parts[2])
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) handlePluginLiveEvents(w http.ResponseWriter, r *http.Request, pluginID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		httpjson.Write(w, http.StatusMethodNotAllowed, map[string]any{"code": "method_not_allowed", "message": "use GET"})
		return
	}
	if a == nil || a.pluginsRT == nil || a.pluginsRT.mgr == nil {
		httpjson.Write(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "plugin not found"})
		return
	}
	limit := defaultConsoleLiveEventsLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			httpjson.Write(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "limit must be a positive integer"})
			return
		}
		if n > maxConsoleLiveEventsLimit {
			n = maxConsoleLiveEventsLimit
		}
		limit = n
	}

	rec, err := a.pluginsRT.mgr.Get(r.Context(), pluginID)
	if err != nil {
		if errors.Is(err, plugins.ErrNotFound) {
			httpjson.Write(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "plugin not found"})
			return
		}
		httpjson.Write(w, http.StatusInternalServerError, map[string]any{"code": "internal", "message": err.Error()})
		return
	}

	streamParam := strings.TrimSpace(r.URL.Query().Get("stream"))
	wantsStream := streamParam == "1" || strings.EqualFold(streamParam, "true") ||
		strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
	if wantsStream {
		a.servePluginLiveEventsStream(w, r, rec, limit)
		return
	}

	overview := a.buildPluginLiveEventsOverview(r.Context(), rec, limit)
	httpjson.Write(w, http.StatusOK, overview)
}

func (a *App) servePluginLiveEventsStream(w http.ResponseWriter, r *http.Request, rec plugins.Record, limit int) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpjson.Write(w, http.StatusInternalServerError, map[string]any{"code": "streaming_unsupported", "message": "streaming not supported"})
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeOverview := func(ctx context.Context) bool {
		latest, err := a.pluginsRT.mgr.Get(ctx, rec.ID)
		if err != nil {
			latest = rec
		}
		ov := a.buildPluginLiveEventsOverview(ctx, latest, limit)
		b, err := json.Marshal(ov)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: overview\ndata: %s\n\n", b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}

	var activeRefs []string
	if a.liveEvents != nil && rec.Enabled && !rec.Shadow && rec.Manifest.Ether != nil {
		for _, lf := range rec.Manifest.Ether.Contributes.LiveFeeds {
			if lf.ID != "" {
				activeRefs = append(activeRefs, rec.ID+":"+lf.ID)
			}
		}
	}

	// No hub or no active feeds: emit the snapshot once and close cleanly.
	if a.liveEvents == nil || len(activeRefs) == 0 {
		_ = writeOverview(r.Context())
		return
	}

	pollCh := make(chan struct{}, 1)
	unsubPoll := a.liveEvents.OnFeedPoll(func(pid, _ string) {
		if pid != rec.ID {
			return
		}
		select {
		case pollCh <- struct{}{}:
		default:
		}
	})
	defer unsubPoll()

	// Subscribe through the hub's normal subscriber rules; closing immediately
	// stops polling when the drawer closes if no other subscribers remain.
	sub, err := a.liveEvents.Subscribe(r.Context(), liveevents.SubscribeRequest{
		Feeds:          activeRefs,
		FilterSpoilers: false,
	})
	if err != nil {
		_ = writeOverview(r.Context())
		return
	}
	defer sub.Close()

	if !writeOverview(r.Context()) {
		return
	}

	maxTimer := time.NewTimer(consoleLiveEventsStreamMaxAge)
	defer maxTimer.Stop()
	beat := time.NewTicker(consoleLiveEventsStreamHeartbeat)
	defer beat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-maxTimer.C:
			return
		case <-beat.C:
			if _, err := fmt.Fprintf(w, ": heartbeat %d\n\n", time.Now().Unix()); err != nil {
				return
			}
			fl.Flush()
		case _, ok := <-sub.Updates():
			if !ok {
				return
			}
			// Drain any coalesced poll signal before writing the updated snapshot.
			select {
			case <-pollCh:
			default:
			}
			if !writeOverview(ctx) {
				return
			}
		case <-pollCh:
			if !writeOverview(ctx) {
				return
			}
		}
	}
}

func (a *App) buildPluginLiveEventsOverview(ctx context.Context, rec plugins.Record, limit int) pluginLiveEventsOverview {
	resolve := func(raw, fallback string) string {
		if key, ok := strings.CutPrefix(raw, "$t:"); ok && a.pluginsRT != nil && a.pluginsRT.mgr != nil {
			if text, found := a.pluginsRT.mgr.ResolveText(ctx, rec.ID, key, "en"); found && strings.TrimSpace(text) != "" {
				return text
			}
			if fallback != "" {
				return fallback
			}
		}
		if strings.TrimSpace(raw) != "" {
			return raw
		}
		return fallback
	}

	var hubFeeds []liveevents.FeedOverview
	var cachedEvents []core.LiveEvent
	if a.liveEvents != nil {
		hubFeeds, cachedEvents = a.liveEvents.PluginCachedSnapshot(ctx, rec.ID)
	}
	hubFeedByID := make(map[string]liveevents.FeedOverview, len(hubFeeds))
	for _, f := range hubFeeds {
		hubFeedByID[f.FeedID] = f
	}

	var declaredFeeds []plugins.LiveFeedMeta
	var declaredScreens []plugins.ScreenMeta
	if rec.Manifest.Ether != nil {
		declaredFeeds = rec.Manifest.Ether.Contributes.LiveFeeds
		declaredScreens = rec.Manifest.Ether.Contributes.Screens
	}

	// Static league chips came from 0.3.19 sections; documents carry none,
	// so a feed shows leagues once the hub has cached events.
	staticLeaguesByFeed := map[string][]liveevents.FeedLeagueSummary{}
	defaultFeed := ""
	if len(declaredFeeds) == 1 {
		defaultFeed = declaredFeeds[0].ID
	}
	feeds := make([]liveevents.FeedOverview, 0, max(len(declaredFeeds), len(hubFeeds)))
	seenFeed := make(map[string]bool, len(declaredFeeds))
	for _, decl := range declaredFeeds {
		seenFeed[decl.ID] = true
		if hf, ok := hubFeedByID[decl.ID]; ok {
			if hf.Title == "" || strings.HasPrefix(hf.Title, "$t:") {
				hf.Title = resolve(decl.Title, decl.ID)
			}
			if len(hf.Leagues) == 0 && len(staticLeaguesByFeed[decl.ID]) > 0 {
				hf.Leagues = staticLeaguesByFeed[decl.ID]
			}
			feeds = append(feeds, hf)
			continue
		}
		interval := decl.MinIntervalSec
		if interval < plugins.MinLiveFeedIntervalSec {
			interval = plugins.MinLiveFeedIntervalSec
		}
		var kinds []string
		if len(decl.EntityKinds) > 0 {
			kinds = append([]string(nil), decl.EntityKinds...)
		}
		feeds = append(feeds, liveevents.FeedOverview{
			FeedID:         decl.ID,
			Title:          resolve(decl.Title, decl.ID),
			MinIntervalSec: interval,
			EntityKinds:    kinds,
			Active:         false,
			Leagues:        staticLeaguesByFeed[decl.ID],
		})
	}
	for _, hf := range hubFeeds {
		if !seenFeed[hf.FeedID] {
			feeds = append(feeds, hf)
		}
	}

	screensOut := make([]pluginScreenOverview, 0, len(declaredScreens))
	switchState := func(kind, id, fe string, def *bool) *bool {
		on := def == nil || *def
		if a.screenPublish != nil {
			on = a.screenPublish.Published(ctx, rec.ID, kind, id, fe, on)
		}
		return &on
	}
	for _, sc := range declaredScreens {
		frontends := sc.Frontends
		if len(frontends) == 0 {
			frontends = []string{"vela"}
		}
		feOut := make([]pluginScreenFrontendOverview, 0, len(frontends))
		for _, fe := range frontends {
			pub, reason := a.screenPublishStatus(ctx, a.pluginsRT.mgr, rec, fe)
			entry := pluginScreenFrontendOverview{
				ID:        fe,
				Name:      consoleFrontendLabel(fe),
				Published: pub,
				Reason:    reason,
			}
			if len(sc.Pages) > 0 {
				entry.Enabled = switchState("screen", sc.ID, fe, sc.Default)
				if pub && !*entry.Enabled {
					entry.Published, entry.Reason = false, "owner_off"
				}
			}
			feOut = append(feOut, entry)
		}
		scFeed := defaultFeed
		screensOut = append(screensOut, pluginScreenOverview{
			ID:        sc.ID,
			Title:     resolve(sc.Title, sc.ID),
			Icon:      sc.Icon,
			Feed:      scFeed,
			Frontends: feOut,
		})
	}

	if rec.Manifest.Ether != nil {
		for _, sf := range rec.Manifest.Ether.Contributes.Surfaces {
			frontends := sf.Frontends
			if len(frontends) == 0 {
				frontends = []string{"vela"}
			}
			feOut := make([]pluginScreenFrontendOverview, 0, len(frontends))
			for _, fe := range frontends {
				pub, reason := a.screenPublishStatus(ctx, a.pluginsRT.mgr, rec, fe)
				entry := pluginScreenFrontendOverview{ID: fe, Name: consoleFrontendLabel(fe), Published: pub, Reason: reason, Enabled: switchState("surface", sf.ID, fe, sf.Default)}
				if pub && !*entry.Enabled {
					entry.Published, entry.Reason = false, "owner_off"
				}
				feOut = append(feOut, entry)
			}
			screensOut = append(screensOut, pluginScreenOverview{ID: sf.ID, Title: resolve(sf.Title, sf.ID), Frontends: feOut, Kind: "surface", Presentation: sf.Presentation})
		}
	}

	var liveUpcoming []core.LiveEvent
	var liveCount, upcomingCount int
	for _, ev := range cachedEvents {
		switch ev.Status.State {
		case core.LiveEventStateIn, core.LiveEventStateDelayed:
			liveCount++
			liveUpcoming = append(liveUpcoming, ev)
		case core.LiveEventStatePre:
			upcomingCount++
			liveUpcoming = append(liveUpcoming, ev)
		}
	}
	sort.Slice(liveUpcoming, func(i, j int) bool {
		aEv, bEv := liveUpcoming[i], liveUpcoming[j]
		ra, rb := consoleLiveEventRank(aEv), consoleLiveEventRank(bEv)
		if ra != rb {
			return ra < rb
		}
		if aEv.Start != bEv.Start {
			return aEv.Start < bEv.Start
		}
		return aEv.ID < bEv.ID
	})

	shown := liveUpcoming
	if len(shown) > limit {
		shown = shown[:limit]
	}

	var watchByID map[string][]core.WatchTarget
	if a.liveWatchMatcher != nil && len(shown) > 0 {
		viewer := a.liveEventsConsoleViewer(ctx)
		if matched, err := a.liveWatchMatcher.WatchTargetsBatch(ctx, shown, viewer); err == nil {
			watchByID = matched
		}
	}

	eventsOut := make([]pluginLiveEventSummary, 0, len(shown))
	for _, ev := range shown {
		comps := ev.Competitors
		if comps == nil {
			comps = []core.LiveEventCompetitor{}
		}
		wc := len(ev.Watch)
		if matched := len(watchByID[ev.ID]); matched > wc {
			wc = matched
		}
		eventsOut = append(eventsOut, pluginLiveEventSummary{
			ID:          ev.ID,
			Feed:        ev.Feed,
			League:      ev.League,
			Title:       ev.Title,
			Start:       ev.Start,
			Status:      ev.Status,
			Competitors: comps,
			Ranking:     ev.Ranking,
			Venue:       ev.Venue,
			WatchCount:  wc,
		})
	}

	return pluginLiveEventsOverview{
		PluginID:      rec.ID,
		Feeds:         feeds,
		Screens:       screensOut,
		Events:        eventsOut,
		TotalEvents:   len(cachedEvents),
		LiveCount:     liveCount,
		UpcomingCount: upcomingCount,
	}
}

func (a *App) liveEventsConsoleViewer(ctx context.Context) *core.User {
	if a != nil && a.users != nil {
		if owner, err := a.users.Owner(ctx); err == nil && owner != nil {
			return owner
		}
	}
	return &core.User{ID: 1, IsOwner: true}
}

func consoleLiveEventRank(ev core.LiveEvent) int {
	switch ev.Status.State {
	case core.LiveEventStateIn:
		if ev.Status.Close {
			return 0
		}
		return 1
	case core.LiveEventStateDelayed:
		return 2
	case core.LiveEventStatePre:
		return 3
	default:
		return 4
	}
}

func consoleFrontendLabel(id string) string {
	switch id {
	case "vela":
		return "Vela"
	case "plex":
		return "Plex"
	case "jellyfin":
		return "Jellyfin"
	case "emby":
		return "Emby"
	case "silo":
		return "Silo"
	case "stremio":
		return "Stremio"
	case "xtream":
		return "Xtream"
	default:
		if id == "" {
			return ""
		}
		return strings.ToUpper(id[:1]) + id[1:]
	}
}

// handleScreenPublish sets the owner's per-app switch for one of a plugin's
// document screens or surfaces: PUT /api/plugins/{id}/screen-publish
// {kind: "screen"|"surface", id, frontend, enabled}.
func (a *App) handleScreenPublish(w http.ResponseWriter, r *http.Request, pluginID string) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", http.MethodPut)
		httpjson.Write(w, http.StatusMethodNotAllowed, map[string]any{"code": "method_not_allowed", "message": "use PUT"})
		return
	}
	if a == nil || a.pluginsRT == nil || a.pluginsRT.mgr == nil || a.screenPublish == nil {
		httpjson.Write(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "plugin not found"})
		return
	}
	var in struct {
		Kind     string `json:"kind"`
		ID       string `json:"id"`
		Frontend string `json:"frontend"`
		Enabled  *bool  `json:"enabled"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.Enabled == nil || (in.Kind != "screen" && in.Kind != "surface") || in.Frontend != "vela" {
		httpjson.Write(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "expected {kind: screen|surface, id, frontend: vela, enabled}"})
		return
	}
	rec, err := a.pluginsRT.mgr.Get(r.Context(), pluginID)
	if err != nil || rec.Manifest.Ether == nil {
		httpjson.Write(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "plugin not found"})
		return
	}
	found := false
	if in.Kind == "screen" {
		for _, sc := range rec.Manifest.Ether.Contributes.Screens {
			found = found || (sc.ID == in.ID && len(sc.Pages) > 0)
		}
	} else {
		for _, sf := range rec.Manifest.Ether.Contributes.Surfaces {
			found = found || sf.ID == in.ID
		}
	}
	if !found {
		httpjson.Write(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "no such screen or surface"})
		return
	}
	if err := a.screenPublish.Set(r.Context(), pluginID, in.Kind, in.ID, in.Frontend, *in.Enabled); err != nil {
		httpjson.Write(w, http.StatusInternalServerError, map[string]any{"code": "internal", "message": "could not save"})
		return
	}
	if docs := (surfaceScreens{app: a}).docs(); docs != nil {
		docs.Bump()
		if in.Kind == "surface" && *in.Enabled {
			a.offerHomeRowSurfaces(r.Context(), docs, pluginID)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleScreenPreview compiles a Studio project's screens and surfaces and
// mints a two-hour preview token: POST /api/plugins/studio/projects/{id}/
// screen-preview. A Vela client sending X-Vela-Screen-Preview: <token> sees
// the draft in place of the installed documents. Compile problems come back
// as {diagnostics: [{field, message}]} with 422.
func (a *App) handleScreenPreview(w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		httpjson.Write(w, http.StatusMethodNotAllowed, map[string]any{"code": "method_not_allowed", "message": "use POST"})
		return
	}
	docs := (surfaceScreens{app: a}).docs()
	if a == nil || a.pluginsRT == nil || a.pluginsRT.studio == nil || docs == nil {
		httpjson.Write(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "studio unavailable"})
		return
	}
	proj, err := a.pluginsRT.studio.GetProject(r.Context(), projectID)
	if err != nil || proj == nil {
		httpjson.Write(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "project not found"})
		return
	}
	man, err := plugins.ParseManifest([]byte(proj.Files["manifest.json"]))
	if err != nil || man.Ether == nil {
		httpjson.Write(w, http.StatusUnprocessableEntity, map[string]any{"code": "invalid", "diagnostics": []plugins.FieldError{{Field: "manifest.json", Message: "the manifest does not parse"}}})
		return
	}
	files := make(map[string][]byte, len(proj.Files))
	for k, v := range proj.Files {
		files[k] = []byte(v)
	}
	// Draft strings come from the working copy's strings files.
	text := draftText(man, files)
	var in struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in)
	token, expires, err := docs.UpdatePreview(strings.TrimSpace(in.Token), man, files, text, time.Now())
	if err != nil {
		var ve *plugins.ValidationError
		if errors.As(err, &ve) {
			httpjson.Write(w, http.StatusUnprocessableEntity, map[string]any{"code": "invalid", "diagnostics": ve.Fields})
			return
		}
		httpjson.Write(w, http.StatusUnprocessableEntity, map[string]any{"code": "invalid", "diagnostics": []plugins.FieldError{{Field: "screens", Message: err.Error()}}})
		return
	}
	type pageList struct {
		ID    string   `json:"id"`
		Title string   `json:"title"`
		Pages []string `json:"pages"`
	}
	type surfaceItem struct {
		ID           string `json:"id"`
		Presentation string `json:"presentation"`
	}
	var screensOut []pageList
	for _, sc := range man.Ether.Contributes.Screens {
		if len(sc.Pages) == 0 {
			continue
		}
		pl := pageList{ID: sc.ID, Title: sc.Title}
		for p := range sc.Pages {
			pl.Pages = append(pl.Pages, p)
		}
		sort.Strings(pl.Pages)
		screensOut = append(screensOut, pl)
	}
	var surfacesOut []surfaceItem
	for _, sf := range man.Ether.Contributes.Surfaces {
		surfacesOut = append(surfacesOut, surfaceItem{ID: sf.ID, Presentation: sf.Presentation})
	}
	httpjson.Write(w, http.StatusOK, map[string]any{"token": token, "expires": expires.UTC().Format(time.RFC3339), "screens": screensOut, "surfaces": surfacesOut})
}

// draftText looks up a working copy's own strings files.
func draftText(man plugins.Manifest, files map[string][]byte) screens.TextFunc {
	byLocale := map[string]map[string]string{}
	for loc, raw := range man.Ether.Strings {
		var path string
		var doc map[string]string
		if json.Unmarshal(raw, &path) == nil {
			_ = json.Unmarshal(files[path], &doc)
		} else {
			_ = json.Unmarshal(raw, &doc)
		}
		byLocale[loc] = doc
	}
	return func(locale, key string) (string, bool) {
		for _, loc := range []string{locale, strings.SplitN(locale, "-", 2)[0], "en"} {
			if t, ok := byLocale[loc][key]; ok {
				return t, true
			}
		}
		return "", false
	}
}

// handleOverlayOrder reads and sets the owner's order of player overlays:
// GET/PUT /api/plugins/screens/overlay-order {frontend: "vela", ids: [...]}.
// When more than two overlays share an anchor, the first two in this order
// show.
func (a *App) handleOverlayOrder(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.screenPublish == nil {
		httpjson.Write(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "screens unavailable"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		httpjson.Write(w, http.StatusOK, map[string]any{"frontend": "vela", "ids": a.screenPublish.OverlayOrder(r.Context(), "vela")})
	case http.MethodPut:
		var in struct {
			Frontend string   `json:"frontend"`
			IDs      []string `json:"ids"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil || in.Frontend != "vela" || len(in.IDs) > 64 {
			httpjson.Write(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "expected {frontend: vela, ids: [surface ids]}"})
			return
		}
		if err := a.screenPublish.SetOverlayOrder(r.Context(), in.Frontend, in.IDs); err != nil {
			httpjson.Write(w, http.StatusInternalServerError, map[string]any{"code": "internal", "message": "could not save"})
			return
		}
		if docs := (surfaceScreens{app: a}).docs(); docs != nil {
			docs.Bump()
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT")
		httpjson.Write(w, http.StatusMethodNotAllowed, map[string]any{"code": "method_not_allowed", "message": "use GET or PUT"})
	}
}
