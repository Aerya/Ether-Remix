package vela

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

type libraryLiveCursor struct {
	Scope        string `json:"scope"`
	Query        string `json:"query"`
	PluginCursor string `json:"pluginCursor"`
	Expires      int64  `json:"expires"`
}

func (i *instance) readLibraryLiveCursor(r *http.Request, scope, query string) (libraryLiveCursor, error) {
	c := libraryLiveCursor{Scope: scope, Query: query, Expires: cursorExpiry(time.Now())}
	token := r.URL.Query().Get("cursor")
	if token == "" {
		return c, nil
	}
	if len(token) > 4096 {
		return c, frontend.ErrLiveGuideStale
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) < i.seal.NonceSize() {
		return c, frontend.ErrLiveGuideStale
	}
	b, err = i.seal.Open(nil, b[:i.seal.NonceSize()], b[i.seal.NonceSize():], []byte("vela-lib-live"))
	if err != nil || json.Unmarshal(b, &c) != nil || c.Scope != scope || c.Query != query || c.Expires <= time.Now().Unix() || c.PluginCursor == "" {
		return c, frontend.ErrLiveGuideStale
	}
	return c, nil
}

func (i *instance) sealLibraryLiveCursor(c libraryLiveCursor, nextPluginCursor string) (string, error) {
	c.PluginCursor = nextPluginCursor
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	nonce, err := i.cursorNonce("vela-lib-live", b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(i.seal.Seal(nonce, nonce, b, []byte("vela-lib-live"))), nil
}

func findLiveTab(view frontend.View, requested string) (frontend.ViewTab, bool) {
	requested = strings.TrimSpace(requested)
	for _, t := range view.Tabs {
		if t.Kind != "live" {
			continue
		}
		if requested == "" || t.ID == requested {
			return t, true
		}
	}
	return frontend.ViewTab{}, false
}

func (i *instance) libraryLive(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	if i.host.LibraryLive == nil {
		i.unavailable(w, r)
		return
	}
	for param, entries := range r.URL.Query() {
		switch param {
		case "tab", "query", "filter", "cursor", "limit":
			if len(entries) != 1 {
				problem(w, 400, "request", "unsupported_library_query")
				return
			}
		default:
			problem(w, 400, "request", "unsupported_library_query")
			return
		}
	}
	views, err := i.host.Experience.Views(r.Context(), v)
	if err != nil {
		fail(w, err, r)
		return
	}
	id := r.PathValue("id")
	var view frontend.View
	found := false
	for _, candidate := range views {
		if candidate.ID == id {
			view = candidate
			found = true
			break
		}
	}
	if !found {
		problem(w, 404, "not-found", "not_found")
		return
	}
	q := r.URL.Query()
	tab, ok := findLiveTab(view, q.Get("tab"))
	if !ok {
		problem(w, 404, "not-found", "not_found")
		return
	}
	scope, err := i.livePageScope(r, v, token)
	if err != nil {
		fail(w, err, r)
		return
	}
	queryText := strings.TrimSpace(q.Get("query"))
	filterKey := strings.TrimSpace(q.Get("filter"))
	pageLimit := limit(r)
	cursorQuery := hashJSON([]any{"library-live", id, tab.ID, queryText, filterKey, pageLimit})
	cursor, err := i.readLibraryLiveCursor(r, scope, cursorQuery)
	if err != nil {
		fail(w, err, r)
		return
	}
	page, err := i.host.LibraryLive.ListLive(r.Context(), v, id, frontend.LibraryLiveRequest{
		Tab:    tab.ID,
		Query:  queryText,
		Filter: filterKey,
		Cursor: cursor.PluginCursor,
		Limit:  pageLimit,
	})
	if err != nil {
		fail(w, err, r)
		return
	}
	dev := requestDevice(r)
	items := make([]object, 0, len(page.Items))
	for _, it := range page.Items {
		art := []object{}
		if it.ThumbRef != "" && i.host.ImageGrants != nil {
			img := frontend.Image{
				Kind:    frontend.ImageThumb,
				Path:    it.ThumbRef,
				Tag:     it.ThumbRef,
				Subject: id,
			}
			secret, issueErr := i.host.ImageGrants.Issue(r.Context(), frontend.ImageGrantRequest{
				Credential: token,
				Device:     dev,
				Image:      img,
			})
			if issueErr == nil && secret != "" {
				art = append(art, object{
					"id":    hash(img.Path),
					"kind":  img.Kind,
					"tag":   img.Tag,
					"sizes": []object{imageSize(img, secret)},
				})
			}
		}
		item := object{
			"id":      it.ID,
			"title":   it.Title,
			"live":    it.Live,
			"artwork": art,
		}
		if it.Subtitle != "" {
			item["subtitle"] = it.Subtitle
		}
		if it.Badge != "" {
			item["badge"] = it.Badge
		}
		if it.Viewers != nil {
			item["viewers"] = *it.Viewers
		}
		if len(it.Tags) > 0 {
			item["tags"] = it.Tags
		}
		items = append(items, item)
	}
	filters := make([]object, 0, len(page.Filters))
	for _, f := range page.Filters {
		filters = append(filters, object{"key": f.Key, "label": f.Label})
	}
	wire := object{
		"library":      libraryWire(view),
		"items":        items,
		"filters":      filters,
		"continuation": "exhausted",
	}
	if page.NextCursor != "" {
		next, err := i.sealLibraryLiveCursor(cursor, page.NextCursor)
		if err != nil {
			fail(w, err, r)
			return
		}
		wire["continuation"] = "more"
		wire["nextCursor"] = next
	}
	send(w, 200, wire)
}

func (i *instance) libraryLivePlay(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	if i.host.LibraryLive == nil || i.host.Capabilities == nil {
		i.unavailable(w, r)
		return
	}
	var b struct {
		IdempotencyKey string `json:"idempotencyKey"`
		ItemID         string `json:"itemId"`
		Tab            string `json:"tab"`
		LiveMode       string `json:"liveMode"`
		Client         client `json:"client"`
	}
	if body(r, &b) != nil || b.Client.Device.ID != requestDevice(r).ID || strings.TrimSpace(b.ItemID) == "" {
		problem(w, 400, "request", "invalid_target")
		return
	}
	if b.LiveMode != "" && b.LiveMode != "adaptive" && b.LiveMode != "low-delay" {
		problem(w, 400, "request", "invalid_live_mode")
		return
	}
	views, err := i.host.Experience.Views(r.Context(), v)
	if err != nil {
		fail(w, err, r)
		return
	}
	id := r.PathValue("id")
	var view frontend.View
	found := false
	for _, candidate := range views {
		if candidate.ID == id {
			view = candidate
			found = true
			break
		}
	}
	if !found {
		problem(w, 404, "not-found", "not_found")
		return
	}
	tab, ok := findLiveTab(view, b.Tab)
	if !ok {
		problem(w, 404, "not-found", "not_found")
		return
	}
	scope, err := i.grantScope(r.Context(), token, requestDevice(r))
	if err != nil {
		fail(w, err, r)
		return
	}
	declared, err := i.clientCapabilities(r, v, b.Client)
	if err != nil {
		fail(w, err, r)
		return
	}
	itemID := strings.TrimSpace(b.ItemID)
	stream, err := i.host.LibraryLive.ResolveLive(r.Context(), v, id, tab.ID, itemID)
	if err != nil {
		fail(w, err, r)
		return
	}
	libID := view.LibraryID
	if libID == 0 {
		if parsed, parseErr := strconv.ParseInt(view.ID, 10, 64); parseErr == nil && parsed > 0 {
			libID = frontend.LibraryID(parsed)
		}
	}
	options := frontend.DecideOptions{
		MaxWidth:       b.Client.MaxWidth,
		MaxHeight:      b.Client.MaxHeight,
		MaxBitrateKbps: b.Client.MaxBitrate / 1000,
		DirectPlay:     true,
		DirectStream:   b.Client.HLS || b.Client.MSE,
		AudioStream:    -1,
		SubtitleStream: -1,
	}
	plan, err := i.host.Sessions.Plan(r.Context(), frontend.PlanRequest{
		Protocol: "vela",
		Viewer:   v,
		Device:   requestDevice(r),
		DirectLive: &frontend.DirectLiveTarget{
			LibraryID: libID,
			Tab:       tab.ID,
			ItemID:    itemID,
			Title:     stream.Title,
			URL:       stream.URL,
			Headers:   stream.Headers,
			MIME:      stream.MIME,
		},
		Caps: declared.Client,
		Intent: frontend.PlayIntent{
			Options:          options,
			DeclaredCaps:     true,
			OriginalPlayback: b.Client.OriginalPlayback,
		},
	})
	if err != nil {
		fail(w, err, r)
		return
	}
	if plan.Estimated {
		problem(w, 502, "source", "source_unresolved")
		return
	}
	s, err := i.host.Sessions.Open(r.Context(), frontend.OpenRequest{
		Protocol: "vela",
		PlanID:   plan.ID,
		Viewer:   v,
		Device:   requestDevice(r),
	})
	if err != nil {
		detail := frontend.DiagnoseLiveTV("tune", err)
		send(w, 503, object{"error": object{"class": "source", "code": detail.Code, "message": detail.Message, "retry": object{"allowed": false}}})
		return
	}
	if s.Media != nil && !s.Media.Estimated {
		plan.Media = *s.Media
	}
	if (!b.Client.HLS && !b.Client.MSE) || !plan.HLS {
		_ = i.host.Sessions.Stop(r.Context(), i.access(r, v), s.ID)
		problem(w, 422, "decoder", "unsupported_transport")
		return
	}
	streamURL, err := i.host.Sessions.StartStream(r.Context(), i.access(r, v), s.ID, frontend.StreamRequest{
		Format:     frontend.StreamHLS,
		LazyStart:  true,
		Continuous: true,
	})
	if err != nil {
		_ = i.host.Sessions.Stop(r.Context(), i.access(r, v), s.ID)
		fail(w, err, r)
		return
	}
	kind := "remux"
	if plan.Decision.Kind == frontend.DecisionTranscode {
		kind = "transcode"
	} else if plan.Decision.Kind == frontend.DecisionDirectPlay {
		kind = "direct"
	}
	path := object{
		"id":                    "primary",
		"kind":                  kind,
		"transport":             "hls",
		"url":                   streamURL,
		"mimeType":              "application/vnd.apple.mpegurl",
		"readiness":             "ready",
		"timelineOriginSeconds": float64(0),
		"qualities":             []object{},
		"boundaries":            []object{},
		"decisionReason":        plan.Decision.Reason,
	}
	_, trackChanges := i.host.Sessions.(frontend.StreamDecisionReader)
	canBurn := trackChanges && plan.Transcoding && !plan.VideoTranscodingOff && plan.HLS && (b.Client.HLS || b.Client.MSE)
	audio, subs, selectedAudio, selectedSub := i.playbackTracks(r.Context(), i.access(r, v), s.ID, plan.Media, plan.Decision, plan.Decision.SubtitleStream, false, b.Client.SubtitleFormats, canBurn)
	mode := b.LiveMode
	if mode == "" {
		mode = "adaptive"
	}
	p := object{
		"sessionId":               s.ID,
		"revision":                uint64(1),
		"sourceGeneration":        uint64(1),
		"seekGeneration":          uint64(1),
		"state":                   "ready",
		"positionSeconds":         float64(0),
		"paths":                   []object{path},
		"selectedPathId":          "primary",
		"audioTracks":             audio,
		"subtitleTracks":          subs,
		"selectedSubtitleTrackId": selectedSub,
		"chapters":                chapters(plan.Media),
		"fallback":                object{"state": "unsupported"},
		"libraryId":               id,
		"liveItemId":              itemID,
		"live":                    object{"mode": mode},
	}
	if stream.Title != "" {
		p["title"] = stream.Title
	}
	if selectedAudio != "" {
		p["selectedAudioTrackId"] = selectedAudio
	}
	dev := requestDevice(r)
	ps := &playSession{
		id:              s.ID,
		scope:           scope,
		canBurn:         canBurn,
		media:           plan.Media,
		subtitleFormats: append([]string(nil), b.Client.SubtitleFormats...),
		mse:             b.Client.MSE,
		viewer:          v.ID,
		device:          dev.ID,
		accessViewer:    v,
		accessDevice:    dev,
		plan:            p,
		source:          1,
		seek:            1,
		revision:        1,
		cursor:          1,
		signal:          make(chan struct{}),
	}
	ps.noteActivity()
	i.refreshTrackDelivery(r.Context(), i.access(r, v), s.ID, ps)
	i.addQualityPaths(r.Context(), i.access(r, v), s.ID, ps)
	i.refreshOutput(r.Context(), s.ID, ps)
	i.mu.Lock()
	for oldID, old := range i.sessions {
		old.mu.Lock()
		expired := !old.ended.IsZero() && time.Since(old.ended) > 10*time.Minute
		if expired {
			old.clearDisconnectTimerLocked()
		}
		old.mu.Unlock()
		if expired {
			delete(i.sessions, oldID)
		}
	}
	i.sessions[s.ID] = ps
	i.mu.Unlock()
	send(w, 200, p)
	i.watchSession(v, dev, s.ID, scope, ps)
}
