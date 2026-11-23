package vela

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

const defaultLiveEventCheckInterval = 15 * time.Second

func (i *instance) liveEventPollInterval() time.Duration {
	if i != nil && i.liveEventInterval > 0 {
		return i.liveEventInterval
	}
	return defaultLiveEventCheckInterval
}

func liveEventObject(ref *frontend.LiveEventRef) any {
	if ref == nil {
		return nil
	}
	return object{"id": ref.ID, "feed": ref.Feed}
}

func cloneLiveEventRef(ref *frontend.LiveEventRef) *frontend.LiveEventRef {
	if ref == nil {
		return nil
	}
	cp := *ref
	return &cp
}

func sameLiveEventRef(a, b *frontend.LiveEventRef) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.Feed == b.Feed
}

// publishLiveEventLocked appends an `event: liveEvent` SSE frame with data
// `{"liveEvent": {"id": ..., "feed": ...}}` or `{"liveEvent": null}` and wakes
// active session SSE streams. Caller must hold s.mu.
func (s *playSession) publishLiveEventLocked(ref *frontend.LiveEventRef) {
	s.cursor++
	s.liveEvent = cloneLiveEventRef(ref)
	if s.plan != nil {
		if ref != nil {
			s.plan["liveEvent"] = liveEventObject(ref)
		} else {
			delete(s.plan, "liveEvent")
		}
	}
	event := object{
		"type":   "liveEvent",
		"cursor": strconv.FormatUint(s.cursor, 10),
		"data":   object{"liveEvent": liveEventObject(ref)},
	}
	s.events = append(s.events, event)
	if len(s.events) > 64 {
		s.events = s.events[len(s.events)-64:]
	}
	if s.signal != nil {
		close(s.signal)
		s.signal = make(chan struct{})
	}
}

// refreshSessionLiveEvent re-checks the programme currently airing on a live
// channel session and emits `event: liveEvent` whenever the programme on air
// or matched live event changes. No-op for VOD sessions or when no linker is wired.
func (i *instance) refreshSessionLiveEvent(ctx context.Context, viewer *frontend.Viewer, s *playSession) {
	if i == nil || i.host.LiveEventLink == nil || viewer == nil || s == nil {
		return
	}
	s.mu.Lock()
	channelID := s.channelID
	ended := !s.ended.IsZero()
	s.mu.Unlock()
	if channelID == "" || ended {
		return
	}

	progKey, ref, err := i.host.LiveEventLink.LiveEventForChannel(ctx, viewer, channelID)
	if err != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended.IsZero() {
		return
	}
	progChanged := progKey != s.liveProgrammeKey
	refChanged := !sameLiveEventRef(ref, s.liveEvent)
	if !progChanged && !refChanged {
		return
	}
	s.liveProgrammeKey = progKey
	s.publishLiveEventLocked(ref)
}

// liveTicker serves GET /vela/v0/live/ticker?screen= (A6), returning a compact,
// viewer-scoped, spoiler-filtered list of live events for Vela's overlay ticker.
func (i *instance) liveTicker(w http.ResponseWriter, r *http.Request) {
	v, _, e := i.viewer(r)
	if e != nil {
		fail(w, e, r)
		return
	}
	if i.host.LiveEventTicker == nil {
		i.unavailable(w, r)
		return
	}
	screen := strings.TrimSpace(r.URL.Query().Get("screen"))
	items, err := i.host.LiveEventTicker.LiveTicker(r.Context(), v, screen)
	if err != nil {
		fail(w, err, r)
		return
	}
	if items == nil {
		items = []frontend.LiveTickerItem{}
	}
	res := object{"events": items}
	if screen != "" {
		res["screen"] = screen
	}
	send(w, 200, res)
}
