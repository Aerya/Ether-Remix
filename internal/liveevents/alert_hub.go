package liveevents

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
)

const alertSubscriberBuffer = 32

// AlertSubscription receives in-app live event alerts for one viewer.
// Call Close when the SSE stream (/vela/v0/live/stream?alerts=1) ends so
// the hub releases all per-viewer state immediately.
type AlertSubscription struct {
	C      <-chan core.LiveEventAlert
	once   sync.Once
	cancel func()
}

// Close unsubscribes from the AlertHub. Safe to call multiple times.
func (s *AlertSubscription) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
}

type alertSubscriber struct {
	id uint64
	ch chan core.LiveEventAlert
}

// AlertHub coordinates in-app live event alerts between W257 (alert producer)
// and W255 (/vela/v0/live/stream?alerts=1 SSE stream).
//
// Delivery is non-blocking with bounded per-subscriber buffers, and zero
// memory is retained for viewers with no active subscriptions.
type AlertHub struct {
	mu     sync.RWMutex
	nextID uint64
	subs   map[int64]map[uint64]*alertSubscriber
}

// NewAlertHub creates an empty in-app alert broker.
func NewAlertHub() *AlertHub {
	return &AlertHub{
		subs: make(map[int64]map[uint64]*alertSubscriber),
	}
}

// SubscribeAlerts registers a viewer stream to receive core.LiveEventAlert
// payloads. W255's /vela/v0/live/stream?alerts=1 subscribes here and emits
// each item as `event: alert`.
func (h *AlertHub) SubscribeAlerts(viewerID int64) *AlertSubscription {
	ch := make(chan core.LiveEventAlert, alertSubscriberBuffer)
	if h == nil || viewerID <= 0 {
		close(ch)
		return &AlertSubscription{C: ch}
	}

	h.mu.Lock()
	h.nextID++
	id := h.nextID
	sub := &alertSubscriber{id: id, ch: ch}
	if h.subs[viewerID] == nil {
		h.subs[viewerID] = make(map[uint64]*alertSubscriber)
	}
	h.subs[viewerID][id] = sub
	h.mu.Unlock()

	return &AlertSubscription{
		C: ch,
		cancel: func() {
			h.mu.Lock()
			if viewerSubs, ok := h.subs[viewerID]; ok {
				delete(viewerSubs, id)
				if len(viewerSubs) == 0 {
					delete(h.subs, viewerID)
				}
			}
			h.mu.Unlock()
		},
	}
}

// PublishAlert fans out alert to every active in-app stream belonging to
// viewerID without blocking. It returns the number of active subscribers for
// viewerID (even if a slow subscriber's buffer was full) so callers know
// whether the viewer currently has the app open.
func (h *AlertHub) PublishAlert(viewerID int64, alert core.LiveEventAlert) int {
	if h == nil || viewerID <= 0 {
		return 0
	}
	h.mu.RLock()
	viewerSubs := h.subs[viewerID]
	if len(viewerSubs) == 0 {
		h.mu.RUnlock()
		return 0
	}
	targets := make([]*alertSubscriber, 0, len(viewerSubs))
	for _, sub := range viewerSubs {
		targets = append(targets, sub)
	}
	h.mu.RUnlock()

	for _, sub := range targets {
		select {
		case sub.ch <- alert:
		default:
			// Drop oldest queued alert so slow streams keep the freshest state.
			select {
			case <-sub.ch:
			default:
			}
			select {
			case sub.ch <- alert:
			default:
			}
		}
	}
	return len(targets)
}

// SubscriberCount reports how many in-app alert streams viewerID currently has open.
func (h *AlertHub) SubscriberCount(viewerID int64) int {
	if h == nil || viewerID <= 0 {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[viewerID])
}

// FormatAlertSSE renders a core.LiveEventAlert as an exact SSE frame for
// /vela/v0/live/stream?alerts=1:
//
//	event: alert
//	data: {"id":...,"kind":...,"eventId":...,"entityKey":...,"title":...,"body":...,"at":...}
func FormatAlertSSE(alert core.LiveEventAlert) ([]byte, error) {
	b, err := json.Marshal(alert)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("event: alert\ndata: %s\n\n", b)), nil
}
