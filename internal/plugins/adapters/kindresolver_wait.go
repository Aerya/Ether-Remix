package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// A plugin's /resolve is staged: a cold album runs its indexer search,
// usenet check and archive listing in bounded slices. Manager.Call already
// re-polls a {pending:true} reply for up to plugins.ResolverTotal, but some
// answers still came back "preparing" at once: a 202, an empty answer while
// the host indexer search the plugin started is still running, a sibling
// holding the album, a queued debrid download. Vela retries a 503 a few
// times; Plexamp, Plex music playback, many Jellyfin audio clients and
// Subsonic apps treat it as unplayable and skip to the next track, so a cold
// album skipped track by track in seconds (W348). The open entry points
// therefore wait server-side, bounded, re-polling until the item is ready,
// truly unavailable, or the budget or the caller's context ends. Only a play
// reaches this, so nothing resolves eagerly.

const (
	// DefaultKindPreparingWait bounds one open's server-side wait for a
	// staged plugin resolve. Plex and Jellyfin clients give a media read
	// well over 20 s before giving up.
	DefaultKindPreparingWait = 18 * time.Second

	kindPreparingPollMin = 100 * time.Millisecond
	kindPreparingPollMax = 2 * time.Second
	// kindPreparingDeadlineMargin leaves room under a caller deadline to
	// answer preparing instead of timing out mid-poll.
	kindPreparingDeadlineMargin = time.Second
)

type preparingWaitKey struct{}

// waitsPreparing reports whether this open should poll a preparing resolve:
// a wait is configured and no outer open on this call path is already
// polling (OpenKindExcluding delegating to OpenKindPartExcluding must not
// multiply the budget).
func (r *KindResolver) waitsPreparing(ctx context.Context) bool {
	return r != nil && r.preparingWait > 0 && ctx.Value(preparingWaitKey{}) == nil
}

// waitPreparing runs open, and while it answers ErrPreparing polls it again
// after the resolver's retry hint, within the configured wait and the
// caller's deadline. Any other answer (media, not available, failure)
// returns at once; when the budget runs out the last preparing error is
// returned so the frontend still answers 503 + Retry-After.
func (r *KindResolver) waitPreparing(ctx context.Context, open func(context.Context) (*frontend.KindMedia, error)) (*frontend.KindMedia, error) {
	ctx = context.WithValue(ctx, preparingWaitKey{}, true)
	budget := r.preparingWait
	if dl, ok := ctx.Deadline(); ok {
		budget = min(budget, time.Until(dl)-kindPreparingDeadlineMargin)
	}
	start := time.Now()
	for {
		media, err := open(ctx)
		if err == nil || !errors.Is(err, core.ErrPreparing) {
			return media, err
		}
		delay := preparingPollDelay(err)
		if time.Since(start)+delay > budget {
			return nil, err
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, err
		case <-t.C:
		}
	}
}

// preparingPollDelay follows the resolver's retry hint, clamped so a long
// hint (a queued debrid download) is still re-checked within the wait and a
// tiny one does not spin.
func preparingPollDelay(err error) time.Duration {
	var pe *core.PreparingError
	d := time.Second
	if errors.As(err, &pe) && pe != nil && pe.RetryAfter > 0 {
		d = pe.RetryAfter
	}
	return min(max(d, kindPreparingPollMin), kindPreparingPollMax)
}

// resolveUnitKey names the unit a plugin resolves as one: the parent of a
// playable leaf (an album for its tracks). Items without a parent are their
// own unit and are not gated.
type resolveUnitKey struct {
	pluginID string
	library  string
	parent   string
}

type resolveUnitGate struct {
	slot chan struct{}
	refs int
}

// acquireResolveUnit admits one /resolve call per resolve unit at a time.
// Tracks of one album share the plugin's staged album resolve: when a
// client queues a whole album, the first call advances the album's search
// and probes, and its siblings wait for that slice and then read the result
// (or continue the same stage) instead of each starting it. Gates exist only
// while a call holds or waits for them, so the map never scales with the
// library.
func (r *KindResolver) acquireResolveUnit(ctx context.Context, key resolveUnitKey) (func(), error) {
	if key.parent == "" {
		return func() {}, nil
	}
	r.unitMu.Lock()
	if r.unitGates == nil {
		r.unitGates = make(map[resolveUnitKey]*resolveUnitGate)
	}
	g := r.unitGates[key]
	if g == nil {
		g = &resolveUnitGate{slot: make(chan struct{}, 1)}
		r.unitGates[key] = g
	}
	g.refs++
	r.unitMu.Unlock()
	drop := func() {
		r.unitMu.Lock()
		if g.refs--; g.refs == 0 && r.unitGates[key] == g {
			delete(r.unitGates, key)
		}
		r.unitMu.Unlock()
	}
	select {
	case g.slot <- struct{}{}:
		return func() {
			<-g.slot
			drop()
		}, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}
