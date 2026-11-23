package identity

import (
	"context"
	"log/slog"
	"time"
)

// Sliding-expiry refresh of a grant Resolve already validated is
// bookkeeping, never part of the auth decision (#648). Resolve enqueues
// the touch and returns; one background drainer per Grants coalesces
// touches by (hash, surface) and applies them off the request path, so a
// valid token can never answer 401 — or wait out the SQLite write gate —
// because another writer holds the database. A touch whose write fails
// (still locked) stays queued: it keeps extending its grant in memory
// until a later Resolve's enqueue retries the write.
type grantTouchKey struct {
	hash    string
	surface string
}

type grantTouch struct {
	viewerID int64
	now      int64
	expires  int64
}

// enqueueTouch records a pending last_used/expires refresh, coalescing
// repeats for the same grant, and starts the drainer if it is not running.
func (g *Grants) enqueueTouch(key grantTouchKey, touch grantTouch) {
	g.touchMu.Lock()
	if g.touchPending == nil {
		g.touchPending = map[grantTouchKey]grantTouch{}
	}
	g.touchPending[key] = touch
	g.touchGen++
	g.touchMu.Unlock()
	if g.touchRunning.CompareAndSwap(false, true) {
		go g.drainTouches()
	}
}

func (g *Grants) drainTouches() {
	for {
		batch, gen := g.snapshotTouches()
		if len(batch) == 0 {
			if g.restartDrain(gen, false) {
				continue
			}
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		failed := g.applyTouches(ctx, batch)
		cancel()
		if failed {
			// Failed touches stay queued (applyTouches only removes
			// successes): they keep extending their grants in memory,
			// and the next enqueue retries the write. Stop here rather
			// than spinning against a held writer.
			if g.restartDrain(gen, true) {
				continue
			}
			return
		}
	}
}

// restartDrain ends a drainer run unless work arrived that this run has
// not seen. With requireNew, only an enqueue newer than gen restarts it
// (still-queued failures alone must not spin); clearing the running flag
// before the check means a racing enqueue either restarts us here or
// starts its own drainer.
func (g *Grants) restartDrain(gen uint64, requireNew bool) bool {
	g.touchRunning.Store(false)
	g.touchMu.Lock()
	pending := len(g.touchPending)
	newer := g.touchGen != gen
	g.touchMu.Unlock()
	return pending > 0 && (!requireNew || newer) && g.touchRunning.CompareAndSwap(false, true)
}

// snapshotTouches copies the queued touches. They stay in the queue while
// being written, so a Resolve racing the drainer still sees them through
// pendingTouch; applyTouches removes each one only after its write lands.
func (g *Grants) snapshotTouches() (map[grantTouchKey]grantTouch, uint64) {
	g.touchMu.Lock()
	defer g.touchMu.Unlock()
	batch := make(map[grantTouchKey]grantTouch, len(g.touchPending))
	for key, touch := range g.touchPending {
		batch[key] = touch
	}
	return batch, g.touchGen
}

// applyTouches writes one snapshot and reports whether any write failed.
// Successful touches leave the queue (unless a newer touch for the same
// grant replaced them meanwhile); failed ones remain queued.
func (g *Grants) applyTouches(ctx context.Context, batch map[grantTouchKey]grantTouch) bool {
	failed := false
	for key, touch := range batch {
		// No expires>now guard: Resolve already validated the grant's
		// effective expiry when it queued this touch, and a chained
		// touch may legitimately land after the stored expiry passed.
		// The monotonic guard keeps a stale touch from shortening a
		// grant a newer touch already extended.
		_, err := g.db.ExecContext(ctx, `UPDATE identity_grants SET last_used=?,expires=? WHERE hash=? AND surface=? AND revoked=0 AND expires<? AND EXISTS (SELECT 1 FROM auth_users WHERE id=? AND active=1 AND (origin<>'dlna_guest' OR ?='dlna'))`,
			touch.now, touch.expires, key.hash, key.surface, touch.expires, touch.viewerID, key.surface)
		if err != nil {
			slog.Debug("identity: grant last-used refresh deferred", "err", err)
			failed = true
			continue
		}
		g.touchMu.Lock()
		if current, ok := g.touchPending[key]; ok && current == touch {
			delete(g.touchPending, key)
		}
		g.touchMu.Unlock()
	}
	return failed
}

// flushTouches applies every queued touch synchronously; tests use it to
// observe the sliding-expiry write deterministically.
func (g *Grants) flushTouches(ctx context.Context) {
	batch, _ := g.snapshotTouches()
	g.applyTouches(ctx, batch)
}

// effectiveExpiry folds a queued-but-unwritten touch into the stored
// expiry, so sliding expiration holds from the caller's side the moment
// Resolve queues the refresh, not whenever the writer gets to it.
func (g *Grants) effectiveExpiry(hash, surface string, stored int64) int64 {
	if touch, ok := g.pendingTouch(grantTouchKey{hash: hash, surface: surface}); ok && touch.expires > stored {
		return touch.expires
	}
	return stored
}

func (g *Grants) pendingTouch(key grantTouchKey) (grantTouch, bool) {
	g.touchMu.Lock()
	defer g.touchMu.Unlock()
	touch, ok := g.touchPending[key]
	return touch, ok
}
