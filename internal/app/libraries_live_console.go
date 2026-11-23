package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/mediasafety"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type consoleLiveItemDTO struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Badge    string   `json:"badge,omitempty"`
	Viewers  *int     `json:"viewers,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Thumb    string   `json:"thumb,omitempty"`
	Live     bool     `json:"live"`
}

type consoleLiveFilterDTO struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type consoleLivePageDTO struct {
	Items      []consoleLiveItemDTO   `json:"items"`
	Filters    []consoleLiveFilterDTO `json:"filters,omitempty"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

type consoleLiveSession struct {
	libID core.LibraryID
	user  core.User
}

func (c *librariesConsole) consoleOwner(r *http.Request) *core.User {
	if c.app != nil && c.app.users != nil {
		if users, err := c.app.users.Users(r.Context()); err == nil {
			for _, u := range users {
				if u.IsOwner {
					cp := u
					return &cp
				}
			}
		}
	}
	return &core.User{ID: 1, IsOwner: true}
}

func (c *librariesConsole) lookupLiveSession(libID core.LibraryID, sessionID string) (*core.User, bool) {
	c.liveMu.Lock()
	defer c.liveMu.Unlock()
	ent, ok := c.liveSessions[sessionID]
	if !ok || ent.libID != libID {
		return nil, false
	}
	cp := ent.user
	return &cp, true
}

func (c *librariesConsole) forgetLiveSession(sessionID string) {
	c.liveMu.Lock()
	delete(c.liveSessions, sessionID)
	c.liveMu.Unlock()
}

func (c *librariesConsole) handleLiveList(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	if lib.Source != core.LibraryPlugin {
		libError(w, http.StatusNotFound, "Live tab is only available for plugin libraries")
		return
	}
	svc := c.app.pluginLiveService()
	if svc == nil {
		libError(w, http.StatusServiceUnavailable, "Live service is unavailable")
		return
	}
	q := r.URL.Query()
	tabKey, hasTab := svc.HasVisibleLiveTab(r.Context(), lib, q.Get("tab"))
	if !hasTab {
		libError(w, http.StatusNotFound, "Library has no live tab")
		return
	}
	lim, _ := strconv.Atoi(q.Get("limit"))
	page, err := svc.List(r.Context(), lib, frontend.LibraryLiveRequest{
		Tab:    tabKey,
		Query:  q.Get("query"),
		Filter: q.Get("filter"),
		Cursor: q.Get("cursor"),
		Limit:  lim,
	})
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			libError(w, http.StatusNotFound, "Live endpoint not found")
			return
		}
		libError(w, http.StatusBadGateway, "Could not load live streams from plugin")
		return
	}
	out := consoleLivePageDTO{
		Items:      make([]consoleLiveItemDTO, 0, len(page.Items)),
		NextCursor: page.NextCursor,
	}
	idPrefix := "/ether/api/libraries/" + strconv.FormatInt(int64(lib.ID), 10) + "/live/thumb/"
	for _, it := range page.Items {
		thumb := ""
		if hash, ok := strings.CutPrefix(it.ThumbRef, frontend.LibraryLiveThumbPrefix); ok && hash != "" {
			thumb = idPrefix + hash
		}
		out.Items = append(out.Items, consoleLiveItemDTO{
			ID:       it.ID,
			Title:    it.Title,
			Subtitle: it.Subtitle,
			Badge:    it.Badge,
			Viewers:  it.Viewers,
			Tags:     it.Tags,
			Thumb:    thumb,
			Live:     it.Live,
		})
	}
	if len(page.Filters) > 0 {
		out.Filters = make([]consoleLiveFilterDTO, 0, len(page.Filters))
		for _, f := range page.Filters {
			out.Filters = append(out.Filters, consoleLiveFilterDTO{Key: f.Key, Label: f.Label})
		}
	}
	libJSON(w, http.StatusOK, out)
}

func (c *librariesConsole) handleLiveThumb(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	svc := c.app.pluginLiveService()
	if svc == nil || (c.app.opts.urlImages == nil && c.app.images == nil) {
		http.NotFound(w, r)
		return
	}
	rawURL, err := svc.ResolveThumb(lib.ID, r.PathValue("hash"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var data []byte
	var ct string
	if c.app.opts.urlImages != nil {
		data, ct, err = c.app.opts.urlImages.GetUntrustedURL(r.Context(), rawURL)
	} else {
		data, ct, err = c.app.images.GetUntrustedURL(r.Context(), rawURL)
	}
	if err != nil || len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	if ct == "" {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (c *librariesConsole) handleLivePlay(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	if lib.Source != core.LibraryPlugin {
		libError(w, http.StatusNotFound, "Live playback is only available for plugin libraries")
		return
	}
	svc := c.app.pluginLiveService()
	if svc == nil || c.app.livetv == nil {
		libError(w, http.StatusServiceUnavailable, "Live playback is unavailable")
		return
	}
	var body struct {
		ItemID string `json:"itemId"`
		Tab    string `json:"tab"`
	}
	if !decodeLibBody(w, r, &body) {
		return
	}
	body.ItemID = strings.TrimSpace(body.ItemID)
	if body.ItemID == "" {
		libError(w, http.StatusBadRequest, "itemId is required")
		return
	}
	if _, hasTab := svc.HasVisibleLiveTab(r.Context(), lib, body.Tab); !hasTab {
		libError(w, http.StatusNotFound, "Library has no live tab")
		return
	}
	stream, err := svc.Resolve(r.Context(), lib, body.ItemID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			libError(w, http.StatusNotFound, "Stream is offline or unavailable")
			return
		}
		libError(w, http.StatusBadGateway, "Could not resolve live stream")
		return
	}
	owner := c.consoleOwner(r)
	caps := livetv.PreviewClientCaps(r)
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	caps.ClientID = "ether-preview-lib-" + hex.EncodeToString(rnd[:])

	libID := lib.ID
	tab := body.Tab
	itemID := body.ItemID
	resolve := func(resolveCtx context.Context) (livetv.DirectStreamResult, error) {
		svc := c.app.pluginLiveService()
		if svc == nil {
			return livetv.DirectStreamResult{}, core.ErrNotAvailable
		}
		freshLib, err := c.app.libraries.Get(resolveCtx, libID)
		if err != nil {
			return livetv.DirectStreamResult{}, err
		}
		if _, hasTab := svc.HasVisibleLiveTab(resolveCtx, freshLib, tab); !hasTab {
			return livetv.DirectStreamResult{}, core.ErrNotFound
		}
		freshStream, err := svc.Resolve(resolveCtx, freshLib, itemID)
		if err != nil {
			return livetv.DirectStreamResult{}, err
		}
		return livetv.DirectStreamResult{URL: freshStream.URL, Headers: freshStream.Headers}, nil
	}

	info, err := c.app.livetv.TuneDirectStream(r.Context(), owner, livetv.DirectStream{
		ID:      "lib-" + strconv.FormatInt(int64(lib.ID), 10) + "-" + body.ItemID,
		Title:   stream.Title,
		URL:     stream.URL,
		Headers: stream.Headers,
		Resolve: resolve,
	}, caps, caps.ClientID)
	if err != nil {
		libError(w, http.StatusBadGateway, err.Error())
		return
	}
	c.liveMu.Lock()
	if c.liveSessions == nil {
		c.liveSessions = make(map[string]consoleLiveSession)
	}
	c.liveSessions[info.ID] = consoleLiveSession{libID: lib.ID, user: *owner}
	c.liveMu.Unlock()

	libJSON(w, http.StatusOK, map[string]any{
		"id":       info.ID,
		"title":    stream.Title,
		"playlist": "/ether/api/libraries/" + strconv.FormatInt(int64(lib.ID), 10) + "/live/sessions/" + info.ID + "/index.m3u8",
	})
}

func (c *librariesConsole) handleLiveSessionPlaylist(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	id := r.PathValue("session")
	user, ok := c.lookupLiveSession(lib.ID, id)
	if !ok || c.app.livetv == nil {
		http.NotFound(w, r)
		return
	}
	c.app.livetv.ServePreviewPlaylist(w, r, id, user)
}

func (c *librariesConsole) handleLiveSessionRelay(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	id := r.PathValue("session")
	user, ok := c.lookupLiveSession(lib.ID, id)
	if !ok || c.app.livetv == nil {
		http.NotFound(w, r)
		return
	}
	mediasafety.SetHeaders(w)
	n, valid := transcode.ParseSegmentName(r.PathValue("segment"))
	if !valid {
		http.NotFound(w, r)
		return
	}
	c.app.livetv.ServeRelaySegment(w, r, id, n, user)
}

func (c *librariesConsole) handleLiveSessionResource(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	id := r.PathValue("session")
	user, ok := c.lookupLiveSession(lib.ID, id)
	if !ok || c.app.livetv == nil {
		http.NotFound(w, r)
		return
	}
	c.app.livetv.ServePreviewResource(w, r, id, r.PathValue("resource"), user)
}

func (c *librariesConsole) handleLiveSessionStop(w http.ResponseWriter, r *http.Request) {
	lib, ok := c.library(w, r)
	if !ok {
		return
	}
	id := r.PathValue("session")
	user, ok := c.lookupLiveSession(lib.ID, id)
	if !ok || c.app.livetv == nil {
		libJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	c.forgetLiveSession(id)
	_ = c.app.livetv.Stop(r.Context(), id, user)
	libJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
