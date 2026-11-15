package vela

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
)

type previewRequestKey struct {
	viewer        int64
	scope, device string
	item          frontend.RatingKey
}

type previewRequest struct {
	request frontend.TrailerPreviewRequest
	expires time.Time
}

// trailerPreviewStatus rechecks identity, grant scope and broker policy on every
// poll. Only POST records an intent; GET cannot discover or prepare a trailer.
func (i *instance) trailerPreviewStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	item, err := key(r)
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	scope, err := i.grantScope(r.Context(), token, requestDevice(r))
	if err != nil {
		fail(w, err, r)
		return
	}
	k := previewRequestKey{viewer: v.ID, scope: scope, device: requestDevice(r).ID, item: item}
	i.mu.Lock()
	intent, ok := i.previewRequests[k]
	if ok && !intent.expires.After(time.Now()) {
		delete(i.previewRequests, k)
		ok = false
	}
	i.mu.Unlock()
	if !ok {
		problem(w, 404, "not-found", "not_found")
		return
	}
	status, ok := i.host.Sessions.(frontend.TrailerPreviewStatuses)
	if !ok || !i.previewAvailable() {
		send(w, 200, frontend.TrailerPreviewResult{State: "unavailable", Reason: "feature_unavailable"})
		return
	}
	req := intent.request
	req.Viewer, req.Device = v, requestDevice(r)
	result, err := status.TrailerPreviewStatus(r.Context(), req)
	if err != nil {
		fail(w, err, r)
		return
	}
	i.sendTrailerPreview(w, r, v, scope, result)
}

type previewRate struct {
	until time.Time
	count int
}

func (i *instance) previewAvailable() bool {
	_, ok := i.host.Sessions.(frontend.TrailerPreviews)
	if !ok || i.host.Capabilities == nil {
		return false
	}
	if available, ok := i.host.Sessions.(frontend.TrailerPreviewAvailability); ok {
		return available.TrailerPreviewAvailable()
	}
	return false
}

func (i *instance) allowPreview(viewer int64) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	now := time.Now()
	if i.previewRates == nil {
		i.previewRates = map[int64]previewRate{}
	}
	for id, rate := range i.previewRates {
		if !now.Before(rate.until) {
			delete(i.previewRates, id)
		}
	}
	rate, ok := i.previewRates[viewer]
	if !ok {
		if len(i.previewRates) >= 1024 {
			return false
		}
		rate.until = now.Add(10 * time.Second)
	}
	if rate.count >= 20 {
		return false
	}
	rate.count++
	i.previewRates[viewer] = rate
	return true
}

// trailerPreview is the only focus/open path that can discover and cache a
// trailer. Home, row and hero projection never call the broker preview port.
func (i *instance) trailerPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	var b struct {
		IdempotencyKey string `json:"idempotencyKey"`
		Client         client `json:"client"`
	}
	if body(r, &b) != nil || b.Client.Device.ID != requestDevice(r).ID || b.IdempotencyKey == "" || len(b.IdempotencyKey) > 256 || !boundedPreviewClient(b.Client) {
		problem(w, 400, "request", "invalid_request")
		return
	}
	item, err := key(r)
	if err != nil || (item.Kind() != frontend.KindMovie && item.Kind() != frontend.KindShow && item.Kind() != frontend.KindSeason && item.Kind() != frontend.KindEpisode) {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if !i.previewAvailable() {
		send(w, 200, frontend.TrailerPreviewResult{State: "unavailable", Reason: "feature_unavailable"})
		return
	}
	if !i.allowPreview(v.ID) {
		w.Header().Set("Retry-After", "1")
		send(w, 200, frontend.TrailerPreviewResult{State: "preparing", RetryAfterSeconds: 1})
		return
	}
	profile := caps.DeviceProfile{MaxStreamingBitrate: b.Client.MaxBitrate, DirectPlayProfiles: []caps.DirectPlayProfile{{Type: "Video", Container: strings.Join(b.Client.Containers, ","), VideoCodec: strings.Join(b.Client.VideoCodecs, ","), AudioCodec: strings.Join(b.Client.AudioCodecs, ",")}}}
	declared, err := i.host.Capabilities.Build(i.identityContext(r), frontend.CapabilityRequest{Viewer: v, Device: caps.DeviceInfo{Protocol: "vela", Product: "Vela", Platform: b.Client.Device.Platform, Version: b.Client.Device.AppVersion, ClientID: requestDevice(r).ID, DeviceName: b.Client.Device.Name, VideoCodecs: b.Client.VideoCodecs, AudioCodecs: b.Client.AudioCodecs, Containers: b.Client.Containers, MaxHeight: b.Client.MaxHeight, MaxBitrateKbps: b.Client.MaxBitrate / 1000}, Profile: profile, UserAgent: r.UserAgent()})
	if err != nil {
		fail(w, err, r)
		return
	}
	scope, err := i.grantScope(r.Context(), token, requestDevice(r))
	if err != nil {
		fail(w, err, r)
		return
	}
	language := ""
	if len(v.AudioLanguages) > 0 {
		language = v.AudioLanguages[0]
	}
	req := frontend.TrailerPreviewRequest{Viewer: v, Device: requestDevice(r), Protocol: "vela", Item: item, IdempotencyKey: b.IdempotencyKey, Language: language, Caps: declared.Client}
	i.mu.Lock()
	if i.previewRequests == nil {
		i.previewRequests = make(map[previewRequestKey]previewRequest)
	}
	for k, old := range i.previewRequests {
		if k.viewer == v.ID || !old.expires.After(time.Now()) {
			delete(i.previewRequests, k)
		}
	}
	if len(i.previewRequests) >= 128 {
		i.mu.Unlock()
		send(w, 200, frontend.TrailerPreviewResult{State: "unavailable", Reason: "feature_unavailable"})
		return
	}
	i.previewRequests[previewRequestKey{viewer: v.ID, scope: scope, device: req.Device.ID, item: item}] = previewRequest{request: req, expires: time.Now().Add(3 * time.Minute)}
	i.mu.Unlock()
	result, err := i.host.Sessions.(frontend.TrailerPreviews).TrailerPreview(r.Context(), req)
	if err != nil {
		fail(w, err, r)
		return
	}
	i.sendTrailerPreview(w, r, v, scope, result)
}

func (i *instance) sendTrailerPreview(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, scope string, result frontend.TrailerPreviewResult) {
	if result.State == "ready" && result.Grant == nil {
		problem(w, 503, "transient", "invalid_preview_grant")
		return
	}
	if result.State == "ready" && result.Grant != nil {
		g := result.Grant
		if r.Context().Err() != nil {
			if r.Method != http.MethodGet {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
				defer cancel()
				_ = i.host.Sessions.Stop(ctx, i.access(r, v), g.SessionID)
			}
			return
		}
		if g.Purpose != "preview" || g.SessionID == "" || !strings.HasPrefix(g.URL, "/sessions/media/") || strings.HasPrefix(g.URL, "//") || !g.ExpiresAt.After(time.Now()) || g.SourceGeneration <= 0 || g.SeekGeneration <= 0 {
			problem(w, 503, "transient", "invalid_preview_grant")
			return
		}
		// Keep only command lineage, never a normal movie plan, watcher or receipt.
		plan := object{"sessionId": g.SessionID, "purpose": "preview", "revision": uint64(1), "sourceGeneration": uint64(g.SourceGeneration), "seekGeneration": uint64(g.SeekGeneration), "state": "ready", "positionSeconds": 0, "paths": []object{}, "selectedPathId": "", "audioTracks": []object{}, "subtitleTracks": []object{}, "selectedSubtitleTrackId": nil, "chapters": []object{}, "fallback": object{"state": "unsupported"}}
		i.mu.Lock()
		if current := i.sessions[g.SessionID]; current != nil {
			current.mu.Lock()
			active := current.viewer == v.ID && current.device == requestDevice(r).ID && current.scope == scope && current.ended.IsZero()
			current.mu.Unlock()
			i.mu.Unlock()
			if !active {
				problem(w, 404, "not-found", "not_found")
				return
			}
			send(w, 200, result)
			return
		}
		for id, old := range i.sessions {
			old.mu.Lock()
			preview := old.plan["purpose"] == "preview"
			expired := preview && (!old.previewExpires.After(time.Now()) || !old.ended.IsZero())
			if preview && old.viewer == v.ID {
				old.ended = time.Now()
				old.plan["state"] = "ended"
			}
			old.mu.Unlock()
			if expired {
				delete(i.sessions, id)
			}
		}
		if i.sessions == nil {
			i.sessions = map[string]*playSession{}
		}
		previewCount := 0
		for _, old := range i.sessions {
			old.mu.Lock()
			if old.plan["purpose"] == "preview" {
				previewCount++
			}
			old.mu.Unlock()
		}
		if previewCount >= 128 {
			i.mu.Unlock()
			_ = i.host.Sessions.Stop(r.Context(), i.access(r, v), g.SessionID)
			send(w, 200, frontend.TrailerPreviewResult{State: "preparing", RetryAfterSeconds: 1})
			return
		}
		i.sessions[g.SessionID] = &playSession{scope: scope, viewer: v.ID, device: requestDevice(r).ID, plan: plan, source: uint64(g.SourceGeneration), seek: uint64(g.SeekGeneration), revision: 1, cursor: 1, signal: make(chan struct{}), previewExpires: g.ExpiresAt}
		i.mu.Unlock()
	}
	if result.State == "preparing" {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, result.RetryAfterSeconds)))
	}
	send(w, 200, result)
}

func boundedPreviewClient(c client) bool {
	if c.MaxWidth < 0 || c.MaxWidth > 16384 || c.MaxHeight < 0 || c.MaxHeight > 16384 || c.MaxBitrate < 0 || c.MaxBitrate > 1000000000 || len(c.ProtocolVersions) > 8 {
		return false
	}
	supported := false
	for _, v := range c.ProtocolVersions {
		if v == 0 {
			supported = true
		}
	}
	if !supported {
		return false
	}
	for _, list := range [][]string{c.Containers, c.VideoCodecs, c.AudioCodecs, c.SubtitleFormats, c.HDR} {
		if len(list) > 32 {
			return false
		}
		for _, v := range list {
			if len(v) > 64 {
				return false
			}
		}
	}
	return len(c.Device.ID) <= 256 && len(c.Device.Name) <= 256 && len(c.Device.Platform) <= 64 && len(c.Device.AppVersion) <= 64
}
