package vela

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

const (
	maxLiveEventQueryIDs   = 200
	maxLiveStreamScreens   = 32
	liveStreamHeartbeatSec = 25 * time.Second
)

func (i *instance) liveEventsAvailable() bool {
	if i.host.LiveEvents == nil {
		return false
	}
	ctx := i.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return i.host.LiveEvents.Available(ctx)
}

func parseCSVParam(raw string, maxItems int) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
		if len(out) > maxItems {
			return nil, false
		}
	}
	return out, true
}

// GET /vela/v0/live/events?ids=
func (i *instance) liveEventsSnapshot(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	if i.host.LiveEvents == nil {
		i.unavailable(w, r)
		return
	}
	ids, ok := parseCSVParam(r.URL.Query().Get("ids"), maxLiveEventQueryIDs)
	if !ok {
		problem(w, 400, "request", "invalid_request")
		return
	}
	events, err := i.host.LiveEvents.Events(r.Context(), v, ids)
	if err != nil {
		fail(w, err, r)
		return
	}
	if events == nil {
		events = []frontend.LiveEvent{}
	}
	send(w, http.StatusOK, object{"events": events})
}

// GET /vela/v0/live/events/{id}
func (i *instance) liveEventDetail(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	if i.host.LiveEvents == nil {
		i.unavailable(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		problem(w, 400, "request", "invalid_request")
		return
	}
	detail, err := i.host.LiveEvents.Detail(r.Context(), v, id)
	if err != nil {
		fail(w, err, r)
		return
	}
	if detail == nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	send(w, http.StatusOK, detail)
}

// POST /vela/v0/live/events/{id}/reveal
func (i *instance) liveEventReveal(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	if i.host.LiveEvents == nil {
		i.unavailable(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		problem(w, 400, "request", "invalid_request")
		return
	}
	if err := i.host.LiveEvents.Reveal(r.Context(), v, id); err != nil {
		fail(w, err, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// GET /vela/v0/live/preferences
func (i *instance) liveEventsPreferencesGet(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	if i.host.LiveEvents == nil {
		i.unavailable(w, r)
		return
	}
	sf, err := i.host.LiveEvents.SpoilerFree(r.Context(), v)
	if err != nil {
		fail(w, err, r)
		return
	}
	send(w, http.StatusOK, object{"spoilerFree": sf})
}

// PUT or PATCH /vela/v0/live/preferences
func (i *instance) liveEventsPreferencesSet(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	if i.host.LiveEvents == nil {
		i.unavailable(w, r)
		return
	}
	var payload struct {
		SpoilerFree *bool `json:"spoilerFree"`
	}
	if body(r, &payload) != nil || payload.SpoilerFree == nil {
		problem(w, 400, "request", "invalid_request")
		return
	}
	if err := i.host.LiveEvents.SetSpoilerFree(r.Context(), v, *payload.SpoilerFree); err != nil {
		fail(w, err, r)
		return
	}
	send(w, http.StatusOK, object{"spoilerFree": *payload.SpoilerFree})
}

// GET /vela/v0/live/stream?events=&screens=&alerts=1
func (i *instance) liveEventsStream(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	if i.host.LiveEvents == nil {
		i.unavailable(w, r)
		return
	}
	flush, ok := w.(http.Flusher)
	if !ok {
		i.unavailable(w, r)
		return
	}
	q := r.URL.Query()
	eventIDs, ok := parseCSVParam(q.Get("events"), maxLiveEventQueryIDs)
	if !ok {
		problem(w, 400, "request", "invalid_request")
		return
	}
	screens, ok := parseCSVParam(q.Get("screens"), maxLiveStreamScreens)
	if !ok {
		problem(w, 400, "request", "invalid_request")
		return
	}
	alertsParam := strings.TrimSpace(q.Get("alerts"))
	wantsAlerts := alertsParam == "1" || strings.EqualFold(alertsParam, "true")

	stream, err := i.host.LiveEvents.Subscribe(r.Context(), v, frontend.LiveEventStreamRequest{
		EventIDs: eventIDs,
		Screens:  screens,
		Alerts:   wantsAlerts,
	})
	if err != nil {
		fail(w, err, r)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	writeTimeout := i.streamWriteTimeout()

	writeEvent := func(eventName string, payload any) error {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, b); err != nil {
			return err
		}
		return flushSSE(rc, flush, writeTimeout)
	}

	// Binding Vela wire contract: send an initial update frame with the current
	// state immediately on stream connect.
	initial := stream.Initial()
	if initial == nil {
		initial = []frontend.LiveEvent{}
	}
	if err := writeEvent("update", object{"events": initial}); err != nil {
		return
	}

	var heartbeatC <-chan time.Time
	if i.liveHeartbeatCh != nil {
		heartbeatC = i.liveHeartbeatCh
	} else {
		ticker := time.NewTicker(liveStreamHeartbeatSec)
		defer ticker.Stop()
		heartbeatC = ticker.C
	}

	updatesC := stream.Updates()
	alertsC := stream.Alerts()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-i.done():
			return
		case batch, ok := <-updatesC:
			if !ok {
				return
			}
			if len(batch) == 0 {
				continue
			}
			if err := writeEvent("update", object{"events": batch}); err != nil {
				return
			}
		case alert, ok := <-alertsC:
			if !ok {
				alertsC = nil
				continue
			}
			if err := writeEvent("alert", alert); err != nil {
				return
			}
		case <-heartbeatC:
			// Revoked grants must not keep an authenticated stream alive.
			if _, _, err := i.viewer(r); err != nil {
				return
			}
			if err := writeEvent("heartbeat", object{}); err != nil {
				return
			}
		}
	}
}
