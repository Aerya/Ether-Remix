package sessions

import (
	"context"
	"crypto/subtle"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/state"
)

// Caller holds b.mu. Keeping a bounded tombstone makes late progress and
// duplicate stops harmless without retaining grants or upstream credentials.
func (b *Broker) rememberRetiredLocked(s *brokerSession) {
	now := b.deps.Now()
	for id, r := range b.retired {
		if !r.until.After(now) {
			delete(b.retired, id)
		}
	}
	if len(b.retired) >= 4096 {
		for id := range b.retired {
			delete(b.retired, id)
			break
		}
	}
	b.retired[s.public.ID] = retiredSession{viewerID: s.public.ViewerID, protocol: s.public.Protocol, deviceID: s.public.Device.ID, item: s.public.Item, until: now.Add(2 * time.Hour)}
}

// collectSupersededLocked keeps one active playback for a viewer, surface and
// device, except sibling tiles within the same multiview group. A playback
// outside the group still supersedes the whole group, and re-tuning the same
// tile replaces only that tile.
func (b *Broker) collectSupersededLocked(replacement *brokerSession) []*brokerSession {
	var old []*brokerSession
	for id, s := range b.sessions {
		if id == replacement.public.ID || s.public.ViewerID != replacement.public.ViewerID || s.clientID != replacement.clientID {
			continue
		}
		if s.public.Protocol == "plex" && replacement.public.Protocol == "plex" &&
			s.public.Item != replacement.public.Item && separatePlexPlayers(s, replacement) {
			continue
		}
		if replacement.multiviewGroup != "" && s.multiviewGroup == replacement.multiviewGroup && s.multiviewTile != replacement.multiviewTile {
			continue
		}
		delete(b.sessions, id)
		b.rememberRetiredLocked(s)
		if r, ok := b.retired[id]; ok {
			r.grant, r.replacedAt = s.grant, b.deps.Now()
			b.retired[id] = r
		}
		old = append(old, s)
	}
	return old
}

func separatePlexPlayers(a, b *brokerSession) bool {
	// Vela and Mave share a device ID across players (music and video in one
	// browser). Their Plex decisions and starts identify the delivery, with
	// the playback ID sent later on timelines, so a start in one player must
	// leave the other playing. Every other Plex client is one player per
	// device: a new title replaces the old one, as PMS does, even when the
	// client never reports the old one stopped.
	webPlayer := func(product string) bool {
		return strings.EqualFold(product, "Vela") || strings.EqualFold(product, "Mave")
	}
	return webPlayer(a.public.Device.Product) && webPlayer(b.public.Device.Product)
}

func (b *Broker) finishSuperseded(ctx context.Context, entries []*brokerSession, newSource string) {
	for _, old := range entries {
		b.mu.Lock()
		reads := slices.Clone(old.servingReads)
		b.mu.Unlock()
		waitReadRetirement(reads)
		if old.trickplay != nil {
			old.trickplay.Close(true)
		}
		b.mu.Lock()
		transfers := b.lazyTransfers[old.public.ID]
		delete(b.lazyTransfers, old.public.ID)
		b.mu.Unlock()
		for _, cancel := range transfers {
			cancel()
		}
		b.bindings.Remove(old.public.ID)
		if b.cluster != nil {
			if err := b.cluster.store.remove(ctx, old.public.ID); err != nil && b.deps.OnEventError != nil {
				b.deps.OnEventError(err)
			}
			b.cluster.announceRetired(ctx, old)
		}
		b.cleanupSampleReceipt(ctx, old)
		b.notifyStopped(old)
		if old.live {
			retired := true
			if old.playID != newSource && b.deps.Live != nil {
				if err := b.deps.Live.Stop(ctx, &core.Viewer{ID: old.public.ViewerID}, old.playID); err != nil {
					retired = false
					if b.deps.OnEventError != nil {
						b.deps.OnEventError(err)
					}
				}
			}
			b.settleRetirement(context.WithoutCancel(ctx), old, retired)
		} else {
			if old.sourcePinned {
				if pins, ok := b.deps.Playback.(core.PlaybackRevisionPin); ok {
					pins.UnpinSourceRevision(old.playID)
				}
			}
			b.mu.Lock()
			stopSource := b.releaseSessionSourceLocked(old.playID)
			b.mu.Unlock()
			if old.playID == newSource {
				stopSource = false
			}
			if err := b.retireBackends(ctx, old, stopSource); err != nil && b.deps.OnEventError != nil {
				b.deps.OnEventError(err)
			}
		}
		b.publish(ctx, sessionEvent("SessionStopped", old.public, nil))
	}
}

// retiredAccess keeps late reports harmless while preserving viewer/device
// authorization for the short-lived supersession history.
func (b *Broker) retiredAccess(ctx context.Context, a frontend.SessionAccess, id string) (core.RatingKey, bool, error) {
	b.mu.Lock()
	r, ok := b.retired[id]
	if ok && !r.until.After(b.deps.Now()) {
		delete(b.retired, id)
		ok = false
	}
	b.mu.Unlock()
	if !ok {
		return 0, false, nil
	}
	v, err := b.viewer(ctx, a.Viewer)
	if err != nil {
		return 0, false, err
	}
	owner := a.Owner && v.IsOwner && !v.Delegated
	if a.Owner && !owner {
		return 0, false, core.ErrUnauthorized
	}
	if !owner && (v.ID != r.viewerID || a.Protocol != r.protocol || a.Device.ID == "" || a.Device.ID != r.deviceID) {
		return 0, false, core.ErrUnauthorized
	}
	return r.item, true, nil
}

// reuseDirectFileSession keeps the device's direct-file session when a new
// open would direct-play the very same source again. Native players switch
// audio and subtitle tracks of a direct-played file locally, yet some (Fladder
// on Android) still post a fresh PlaybackInfo for the switch and keep reading
// the file URL they already have. Superseding interrupted those reads, so the
// player paused for good, and its later progress and stop reports named a
// session that never streamed, so the resume position was lost. Jellyfin
// never ends a static stream on PlaybackInfo; the same request now updates
// and returns the session that is playing. The caller holds the owner gate.
func (b *Broker) reuseDirectFileSession(v *core.Viewer, req frontend.OpenRequest, p brokerPlan, ps *core.PlaySession) (frontend.Session, bool) {
	if !b.policy(req.Protocol).ReopenKeepsDirectFile || p.external || p.live || p.request.Multiview != nil || p.decision.Kind != core.DecisionDirectPlay || ps == nil {
		return frontend.Session{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return frontend.Session{}, false
	}
	for _, s := range b.sessions {
		if s.live || s.restored || s.multiviewGroup != "" || s.format != frontend.StreamDirect || s.playID != ps.ID ||
			s.public.ViewerID != v.ID || s.public.Protocol != req.Protocol || s.clientID != p.request.Caps.ClientID ||
			s.public.Device.ID != req.Device.ID || s.public.Item != p.request.Item {
			continue
		}
		// A different client playback (its own session or playback id) is a
		// new play, not a track switch inside this one.
		if (p.request.Intent.ClientSessionID != "" && p.request.Intent.ClientSessionID != s.deliverySession) ||
			(p.request.Intent.PlaybackID != "" && p.request.Intent.PlaybackID != s.playbackID) {
			continue
		}
		s.versionAdmission = p.versionAdmission
		s.opts = p.request.Intent.Options
		s.decision = p.decision
		s.manualSubtitle = s.manualSubtitle || p.request.Intent.SubtitleSet || (p.request.Intent.Hints != nil && p.request.Intent.Hints.ManualSubtitle)
		if entry, ok := b.bindings.Get(s.public.ID); ok {
			entry.Requested, entry.Opts, entry.Decision, entry.UpdatedAt = p.request.Intent, s.opts, s.decision, b.deps.Now()
			b.bindings.Put(entry)
		}
		return copySession(s.public), true
	}
	return frontend.Session{}, false
}

// collectStaleQueuedLocked retires the device's earlier queued item that never
// played when a newer one is queued; the playing session stays.
func (b *Broker) collectStaleQueuedLocked(replacement *brokerSession) []*brokerSession {
	var old []*brokerSession
	for id, s := range b.sessions {
		if !s.queued || id == replacement.public.ID || s.public.ViewerID != replacement.public.ViewerID || s.clientID != replacement.clientID {
			continue
		}
		delete(b.sessions, id)
		b.rememberRetiredLocked(s)
		old = append(old, s)
	}
	return old
}

// activateQueued makes a queued next item the device's playback once it is
// observed playing: it takes ownership and supersedes the session that was
// playing (which has ended by now on a gapless client). A no-op for any other
// session.
func (b *Broker) activateQueued(ctx context.Context, id string) {
	b.mu.Lock()
	s, ok := b.sessions[id]
	if !ok || !s.queued || b.closed {
		b.mu.Unlock()
		return
	}
	s.queued = false
	b.owners.BindFor("broker", s.public.ViewerID, s.clientID, s.public.Item, s.playbackID, s.playID, true, b.deps.Now())
	superseded := b.collectSupersededLocked(s)
	// The item it follows plays out beside it (a crossfade) for a short while:
	// it stays, demoted, until its stop or the handover ends.
	var outgoing, retire []*brokerSession
	for _, old := range superseded {
		if !old.live && !old.queued && !old.outgoing && old.multiviewGroup == "" && old.public.Item != s.public.Item {
			old.outgoing = true
			delete(b.retired, old.public.ID)
			b.sessions[old.public.ID] = old
			outgoing = append(outgoing, old)
		} else {
			retire = append(retire, old)
		}
	}
	b.bumpNonGroupEpochLocked(s.public.ViewerID, s.clientID)
	b.mu.Unlock()
	b.finishSuperseded(ctx, retire, s.playID)
	for _, old := range outgoing {
		b.completeOutgoing(ctx, old)
		id, successor := old.public.ID, s.playID
		afterHandover(OutgoingHandover, func() { b.retireOutgoing(context.WithoutCancel(ctx), id, successor) })
	}
	if b.cluster != nil {
		b.cluster.ownershipChanged(ctx)
	}
}

// replacedGrace is how long a superseded session's media URLs answer
// "replaced, retry" instead of 404.
const replacedGrace = 30 * time.Second

// replacedMedia reports whether a media request names a session that a newer
// playback on the same device replaced moments ago. Jellyfin clients switch
// tracks of a transcode by posting PlaybackInfo and only then load the new
// URL; the old session ends inside that request, so the player's next
// segment request on the old URL got a 404. Media3 treats 404 as fatal
// ("Source error, response code 404": Wholphin paused and then failed on a
// subtitle switch), but retries other failures. A 409 with Retry-After lets
// it retry the old URL until the app has swapped to the new one; nothing is
// served from the retired session.
func (b *Broker) replacedMedia(id, grant string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.retired[id]
	return ok && !r.replacedAt.IsZero() && r.grant != "" && subtle.ConstantTimeCompare([]byte(r.grant), []byte(grant)) == 1 && b.deps.Now().Sub(r.replacedAt) < replacedGrace
}

// OutgoingHandover is how long an item a queued successor took over may keep
// playing out (a crossfade) before Ether retires it.
const OutgoingHandover = frontend.QueuedHandover

// afterHandover schedules a handover's end (tests run it by hand).
var afterHandover = func(d time.Duration, fn func()) { time.AfterFunc(d, fn) }

// completeOutgoing records the item a queued successor took over as played
// through: the client moved on to the next item at its end.
func (b *Broker) completeOutgoing(ctx context.Context, s *brokerSession) {
	duration := time.Duration(0)
	if source, ok := b.deps.Playback.Session(s.playID); ok && source != nil {
		duration = source.Info.Duration
	}
	ctx = state.WithOrigin(ctx, state.Origin{Surface: s.public.Protocol, SessionID: s.public.ID, ClientID: s.public.Device.ID})
	if err := RecordProgress(ctx, b.deps.Progress, s.public.ViewerID, s.public.Item, duration, duration, "explicit-complete"); err != nil && b.deps.OnEventError != nil {
		b.deps.OnEventError(err)
	}
}

// retireOutgoing ends a handed-over item still playing out when its handover
// is over; a stop already ended it.
func (b *Broker) retireOutgoing(ctx context.Context, id, successor string) {
	b.mu.Lock()
	s, ok := b.sessions[id]
	if !ok || !s.outgoing || b.closed {
		b.mu.Unlock()
		return
	}
	delete(b.sessions, id)
	b.rememberRetiredLocked(s)
	b.mu.Unlock()
	b.finishSuperseded(ctx, []*brokerSession{s}, successor)
}
