package liveevents

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/core"
)

const (
	topicInterest = "liveevents.interest"
	topicUpdate   = "liveevents.update"
	topicAlert    = "liveevents.alert"
	topicSpoiler  = "liveevents.spoiler"

	// Follower interest lease cadence. Followers only run the refresh timer
	// while they have at least one active local subscriber (zero idle work).
	interestRefreshInterval = 30 * time.Second
	interestLeaseTTL        = 90 * time.Second

	maxRemotePeers         = 256
	maxRemoteFeedsPerPeer  = 256
	maxRemoteEventsPerPeer = 1024
)

type clusterInterestMsg struct {
	Feeds    []string `json:"feeds,omitempty"`
	EventIDs []string `json:"eventIds,omitempty"`
}

type clusterUpdateMsg struct {
	PluginID string           `json:"pluginId"`
	FeedID   string           `json:"feedId"`
	Events   []core.LiveEvent `json:"events"`
}

type clusterAlertMsg struct {
	ViewerID int64               `json:"viewerId"`
	Alert    core.LiveEventAlert `json:"alert"`
}

type clusterSpoilerMsg struct {
	ViewerID int64 `json:"viewerId"`
}

type remotePeerInterest struct {
	feeds    map[string]struct{}
	eventIDs map[string]struct{}
	expires  time.Time
}

func (h *Hub) attachClusterBus(ctx context.Context) func() {
	if h.cbus == nil {
		return func() {}
	}
	unsubs := []func(){
		h.cbus.Subscribe(topicUpdate, h.onClusterUpdate),
		h.cbus.Subscribe(topicInterest, h.onClusterInterest),
		h.cbus.Subscribe(topicAlert, h.onClusterAlert),
		h.cbus.Subscribe(topicSpoiler, h.onClusterSpoiler),
		h.cbus.Subscribe(clusterbus.TopicResync, func([]byte) {
			h.spoilers.InvalidateAll()
			if !h.IsLeading() {
				h.publishLocalInterest(ctx)
			}
		}),
	}
	return func() {
		for _, fn := range unsubs {
			if fn != nil {
				fn()
			}
		}
	}
}

func (h *Hub) onClusterUpdate(raw []byte) {
	origin, payload := clusterbus.Untag(raw)
	if origin == "" || origin == h.origin || len(payload) == 0 {
		return
	}
	var msg clusterUpdateMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		return
	}
	if msg.PluginID == "" || msg.FeedID == "" || len(msg.Events) == 0 {
		return
	}
	// Merge into local cache and fan out to local subscribers without re-broadcasting.
	h.ingestEvents(msg.PluginID, msg.FeedID, msg.Events, false)
}

func (h *Hub) onClusterInterest(raw []byte) {
	origin, payload := clusterbus.Untag(raw)
	if origin == "" || origin == h.origin || len(payload) == 0 {
		return
	}
	var msg clusterInterestMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		return
	}
	now := h.clock.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if len(msg.Feeds) == 0 && len(msg.EventIDs) == 0 {
		delete(h.remoteInterests, origin)
		if len(h.remoteInterests) == 0 && h.remotePruneCancel != nil {
			h.remotePruneCancel()
			h.remotePruneCancel = nil
		}
		h.reconcilePollersLocked()
		return
	}
	if _, exists := h.remoteInterests[origin]; !exists && len(h.remoteInterests) >= maxRemotePeers {
		h.pruneExpiredRemoteLocked(now)
		if len(h.remoteInterests) >= maxRemotePeers {
			return
		}
	}
	entry := remotePeerInterest{
		feeds:    make(map[string]struct{}, min(len(msg.Feeds), maxRemoteFeedsPerPeer)),
		eventIDs: make(map[string]struct{}, min(len(msg.EventIDs), maxRemoteEventsPerPeer)),
		expires:  now.Add(interestLeaseTTL),
	}
	for _, f := range msg.Feeds {
		if len(entry.feeds) >= maxRemoteFeedsPerPeer {
			break
		}
		if _, ok := ParseFeedRef(f); ok {
			entry.feeds[f] = struct{}{}
		}
	}
	for _, id := range msg.EventIDs {
		if len(entry.eventIDs) >= maxRemoteEventsPerPeer {
			break
		}
		if _, _, _, ok := core.ParseLiveEventID(id); ok {
			entry.eventIDs[id] = struct{}{}
		}
	}
	h.remoteInterests[origin] = entry
	h.ensureRemotePrunerLocked()
	h.reconcilePollersLocked()
}

func (h *Hub) onClusterAlert(raw []byte) {
	origin, payload := clusterbus.Untag(raw)
	if origin == "" || origin == h.origin || len(payload) == 0 {
		return
	}
	var msg clusterAlertMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		return
	}
	if msg.ViewerID <= 0 {
		return
	}
	h.deliverAlertLocal(h.runCtx, msg.ViewerID, msg.Alert)
}

func (h *Hub) onClusterSpoiler(raw []byte) {
	origin, payload := clusterbus.Untag(raw)
	if origin == "" || origin == h.origin || len(payload) == 0 {
		return
	}
	var msg clusterSpoilerMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		return
	}
	if msg.ViewerID > 0 {
		h.spoilers.InvalidateViewer(msg.ViewerID)
		h.clearViewerSentHashes(msg.ViewerID)
	}
}

func (h *Hub) publishClusterSpoiler(viewerID int64) {
	if h.cbus == nil || viewerID <= 0 {
		return
	}
	raw, err := json.Marshal(clusterSpoilerMsg{ViewerID: viewerID})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(h.runCtx, clusterbus.PublishTimeout)
	defer cancel()
	_ = h.cbus.Publish(ctx, topicSpoiler, clusterbus.Tag(h.origin, raw))
}

func (h *Hub) publishClusterAlert(ctx context.Context, viewerID int64, alert core.LiveEventAlert) {
	if h.cbus == nil || viewerID <= 0 {
		return
	}
	raw, err := json.Marshal(clusterAlertMsg{ViewerID: viewerID, Alert: alert})
	if err != nil {
		return
	}
	tagged := clusterbus.Tag(h.origin, raw)
	if len(tagged) > clusterbus.MaxPayload {
		return
	}
	pubCtx, cancel := context.WithTimeout(ctx, clusterbus.PublishTimeout)
	defer cancel()
	_ = h.cbus.Publish(pubCtx, topicAlert, tagged)
}

// broadcastClusterUpdates chunks changed events so every clusterbus message
// fits comfortably under clusterbus.MaxPayload.
func (h *Hub) broadcastClusterUpdates(pluginID, feedID string, events []core.LiveEvent) {
	if h.cbus == nil || len(events) == 0 {
		return
	}
	// Reserve room for origin tag + JSON envelope overhead.
	maxBody := clusterbus.MaxPayload - len(h.origin) - 1
	if maxBody <= 128 {
		return
	}
	batch := make([]core.LiveEvent, 0, len(events))
	flush := func() {
		if len(batch) == 0 {
			return
		}
		raw, err := json.Marshal(clusterUpdateMsg{PluginID: pluginID, FeedID: feedID, Events: batch})
		if err == nil && len(raw) <= maxBody {
			ctx, cancel := context.WithTimeout(h.runCtx, clusterbus.PublishTimeout)
			_ = h.cbus.Publish(ctx, topicUpdate, clusterbus.Tag(h.origin, raw))
			cancel()
		}
		batch = batch[:0]
	}

	for _, ev := range events {
		candidate := append(batch, ev)
		raw, err := json.Marshal(clusterUpdateMsg{PluginID: pluginID, FeedID: feedID, Events: candidate})
		if err != nil {
			continue
		}
		if len(raw) <= maxBody {
			batch = candidate
			continue
		}
		if len(batch) > 0 {
			flush()
			// Retry single event on its own.
			single, err := json.Marshal(clusterUpdateMsg{PluginID: pluginID, FeedID: feedID, Events: []core.LiveEvent{ev}})
			if err == nil && len(single) <= maxBody {
				batch = append(batch, ev)
			}
		}
	}
	flush()
}

func (h *Hub) localInterestSnapshotLocked() clusterInterestMsg {
	feedSet := make(map[string]struct{})
	eventSet := make(map[string]struct{})
	for _, sub := range h.subs {
		for fk := range sub.feeds {
			feedSet[fk] = struct{}{}
		}
		for eid := range sub.eventIDs {
			p, f, _, ok := core.ParseLiveEventID(eid)
			if !ok {
				continue
			}
			fk := p + ":" + f
			if _, full := feedSet[fk]; !full {
				eventSet[eid] = struct{}{}
			}
		}
	}
	// Drop event IDs already covered by a full-feed subscription.
	for eid := range eventSet {
		p, f, _, ok := core.ParseLiveEventID(eid)
		if ok {
			if _, full := feedSet[p+":"+f]; full {
				delete(eventSet, eid)
			}
		}
	}
	msg := clusterInterestMsg{}
	if len(feedSet) > 0 {
		msg.Feeds = make([]string, 0, len(feedSet))
		for fk := range feedSet {
			msg.Feeds = append(msg.Feeds, fk)
		}
		sort.Strings(msg.Feeds)
	}
	if len(eventSet) > 0 {
		msg.EventIDs = make([]string, 0, len(eventSet))
		for eid := range eventSet {
			msg.EventIDs = append(msg.EventIDs, eid)
		}
		sort.Strings(msg.EventIDs)
	}
	return msg
}

func (h *Hub) publishLocalInterest(ctx context.Context) {
	if h.cbus == nil {
		return
	}
	h.mu.Lock()
	if h.closed || h.leading {
		h.mu.Unlock()
		return
	}
	msg := h.localInterestSnapshotLocked()
	h.mu.Unlock()

	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	tagged := clusterbus.Tag(h.origin, raw)
	// If huge event list exceeds MaxPayload, collapse to feed-level interest.
	if len(tagged) > clusterbus.MaxPayload && len(msg.EventIDs) > 0 {
		feedSet := make(map[string]struct{}, len(msg.Feeds))
		for _, f := range msg.Feeds {
			feedSet[f] = struct{}{}
		}
		for _, eid := range msg.EventIDs {
			if p, f, _, ok := core.ParseLiveEventID(eid); ok {
				feedSet[p+":"+f] = struct{}{}
			}
		}
		collapsed := clusterInterestMsg{Feeds: make([]string, 0, len(feedSet))}
		for f := range feedSet {
			collapsed.Feeds = append(collapsed.Feeds, f)
		}
		sort.Strings(collapsed.Feeds)
		raw, err = json.Marshal(collapsed)
		if err != nil {
			return
		}
		tagged = clusterbus.Tag(h.origin, raw)
		if len(tagged) > clusterbus.MaxPayload {
			return
		}
	}
	if ctx == nil {
		ctx = h.runCtx
	}
	pubCtx, cancel := context.WithTimeout(ctx, clusterbus.PublishTimeout)
	defer cancel()
	_ = h.cbus.Publish(pubCtx, topicInterest, tagged)
}

// reconcileInterestRefresherLocked starts the follower interest heartbeat
// goroutine ONLY while this node is a follower with >= 1 active subscription,
// and stops it immediately when idle or promoted to leader.
func (h *Hub) reconcileInterestRefresherLocked() {
	if h.cbus == nil || h.closed {
		return
	}
	need := !h.leading && h.hasLocalFeedInterestLocked()
	if need && h.interestCancel == nil {
		ctx, cancel := context.WithCancel(h.runCtx)
		h.interestCancel = cancel
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			h.runInterestRefresher(ctx)
		}()
	} else if !need && h.interestCancel != nil {
		h.interestCancel()
		h.interestCancel = nil
	}
}

func (h *Hub) runInterestRefresher(ctx context.Context) {
	timer := h.clock.NewTimer(interestRefreshInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C():
			h.publishLocalInterest(ctx)
			timer.Reset(interestRefreshInterval)
		}
	}
}

func (h *Hub) hasLocalFeedInterestLocked() bool {
	for _, sub := range h.subs {
		if len(sub.feeds) > 0 || len(sub.eventIDs) > 0 {
			return true
		}
	}
	return false
}

// ensureRemotePrunerLocked starts a bounded expiry pruner ONLY while at least
// one remote follower has announced interest. It exits when remoteInterests empties.
func (h *Hub) ensureRemotePrunerLocked() {
	if h.closed || h.remotePruneCancel != nil || len(h.remoteInterests) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(h.runCtx)
	h.remotePruneCancel = cancel
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		timer := h.clock.NewTimer(interestRefreshInterval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C():
				h.mu.Lock()
				changed := h.pruneExpiredRemoteLocked(h.clock.Now())
				if changed {
					h.reconcilePollersLocked()
				}
				empty := len(h.remoteInterests) == 0
				if empty {
					h.remotePruneCancel = nil
				}
				h.mu.Unlock()
				if empty {
					return
				}
				timer.Reset(interestRefreshInterval)
			}
		}
	}()
}

func (h *Hub) pruneExpiredRemoteLocked(now time.Time) bool {
	changed := false
	for origin, peer := range h.remoteInterests {
		if !now.Before(peer.expires) {
			delete(h.remoteInterests, origin)
			changed = true
		}
	}
	return changed
}
