package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
)

// Reads never wait on TMDB for long. A title whose details are stored but
// stale is served at once and refreshed behind the read; a title with no
// details yet waits at most hydrateWait while the fetch carries on detached,
// so its result still lands in the mirror for the next read. Refreshes are
// deduplicated per key and bounded in number.
const (
	hydrateWait   = 2 * time.Second
	hydrateLimit  = 16
	hydrateBudget = 45 * time.Second
)

// errHydrating means a refresh is still running (or could not start because
// hydrateLimit refreshes already are); the caller serves what it has. A
// title with nothing stored yet is not available to this read, so the error
// is a not-found: pages skip the title instead of failing, and the fetch
// carries on so the next read has it.
var errHydrating = fmt.Errorf("%w: metadata refresh in progress", core.ErrNotFound)

type hydration struct {
	done chan struct{}
	val  any
	err  error
}

// hydrate runs fetch for refreshKey detached from ctx's cancellation (it keeps
// ctx's values) and waits for it at most wait. wait <= 0 starts the refresh
// if a slot is free and returns errHydrating at once. A caller that must wait
// also waits, within the same bound, for one of the hydrateLimit slots. A
// failed fetch is remembered with noteRefreshMiss, as a synchronous fetch was.
func (c *Catalog) hydrate(ctx context.Context, refreshKey string, wait time.Duration, fetch func(context.Context) error) error {
	_, err := c.hydrateValue(ctx, refreshKey, wait, func(ctx context.Context) (any, error) { return nil, fetch(ctx) })
	return err
}

// hydrateValue is hydrate for a fetch whose result every waiter shares.
func (c *Catalog) hydrateValue(ctx context.Context, refreshKey string, wait time.Duration, fetch func(context.Context) (any, error)) (any, error) {
	if budget, ok := ctx.Value(waitBudgetKey{}).(*waitBudget); ok && wait > 0 {
		wait = min(wait, time.Duration(budget.remaining.Load()))
		if wait > 0 {
			start := time.Now()
			defer func() { budget.remaining.Add(-int64(time.Since(start))) }()
		}
	}
	var deadline <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		deadline = timer.C
	}
	h := c.startHydration(ctx, refreshKey, wait > 0, deadline, fetch)
	if h == nil || wait <= 0 {
		return nil, errHydrating
	}
	select {
	case <-h.done:
		return h.val, h.err
	case <-deadline:
		return nil, errHydrating
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// startHydration joins the running refresh of refreshKey or starts one in a
// free slot. It returns nil when no slot came free in time.
func (c *Catalog) startHydration(ctx context.Context, refreshKey string, block bool, deadline <-chan time.Time, fetch func(context.Context) (any, error)) *hydration {
	c.hydrateMu.Lock()
	if h := c.hydrating[refreshKey]; h != nil {
		c.hydrateMu.Unlock()
		return h
	}
	c.hydrateMu.Unlock()
	if block {
		select {
		case c.hydrateSlots <- struct{}{}:
		case <-deadline:
			return nil
		case <-ctx.Done():
			return nil
		}
	} else {
		select {
		case c.hydrateSlots <- struct{}{}:
		default:
			return nil
		}
	}
	c.hydrateMu.Lock()
	if h := c.hydrating[refreshKey]; h != nil {
		// Started by another reader while this one waited for a slot.
		c.hydrateMu.Unlock()
		<-c.hydrateSlots
		return h
	}
	if c.hydrating == nil {
		c.hydrating = make(map[string]*hydration)
	}
	h := &hydration{done: make(chan struct{})}
	c.hydrating[refreshKey] = h
	c.hydrateMu.Unlock()
	bg := context.WithoutCancel(ctx)
	if deferred, _ := ctx.Value(deferAiredCountsKey{}).(bool); deferred {
		bg = ctx
	}
	if !block {
		// Nobody waits for a refresh behind the read: it is not the
		// read's work (statements, writes, lock waits).
		bg = store.DetachQueryStats(bg)
	}
	bg, cancel := context.WithTimeout(bg, hydrateBudget)
	go func() {
		defer cancel()
		h.val, h.err = fetch(bg)
		if h.err != nil && !errors.Is(bg.Err(), context.DeadlineExceeded) {
			c.noteRefreshMiss(refreshKey)
		}
		c.hydrateMu.Lock()
		delete(c.hydrating, refreshKey)
		c.hydrateMu.Unlock()
		<-c.hydrateSlots
		close(h.done)
	}()
	return h
}

// waitBudget is what is left of one request's time to wait on TMDB.
type waitBudget struct{ remaining atomic.Int64 }

type waitBudgetKey struct{}

// WithReadWaitBudget bounds the total time reads under ctx wait on TMDB,
// across every title they touch. A page that lists thirty titles with no
// details yet waits at most d in all, not 2 s for each; the rest are served
// as stored and refreshed in the background.
func WithReadWaitBudget(ctx context.Context, d time.Duration) context.Context {
	b := &waitBudget{}
	b.remaining.Store(int64(d))
	return context.WithValue(ctx, waitBudgetKey{}, b)
}

// refreshing reports whether a refresh of refreshKey is running.
func (c *Catalog) refreshing(refreshKey string) bool {
	c.hydrateMu.Lock()
	defer c.hydrateMu.Unlock()
	return c.hydrating[refreshKey] != nil
}

// WaitRefreshes blocks until no metadata refresh started by a read is still
// running.
func (c *Catalog) WaitRefreshes() { c.waitHydrations() }

func (c *Catalog) waitHydrations() {
	for {
		c.hydrateMu.Lock()
		var h *hydration
		for _, x := range c.hydrating {
			h = x
			break
		}
		var seasonDone chan struct{}
		for _, done := range c.seasonLoading {
			seasonDone = done
			break
		}
		c.hydrateMu.Unlock()
		if h != nil {
			<-h.done
		} else if seasonDone != nil {
			<-seasonDone
		} else {
			return
		}
	}
}

// BackgroundBusy is how many refreshes run behind reads now: the
// performance gate waits for none before each measured request.
func (c *Catalog) BackgroundBusy() int {
	c.hydrateMu.Lock()
	defer c.hydrateMu.Unlock()
	return len(c.hydrating) + len(c.seasonLoading)
}

// noReadWait reports an explicit local-response policy. Hydrations may still
// run in the bounded detached pool, but neither remote results nor a first
// in-memory search index build should hold the response.
func noReadWait(ctx context.Context) bool {
	b, ok := ctx.Value(waitBudgetKey{}).(*waitBudget)
	return ok && b.remaining.Load() <= 0
}

// Orchestrators use their own small, on-demand allowance: holding a hydration
// slot while waiting for child hydrations can exhaust every slot and deadlock
// discovery. No queue accumulates behind these two jobs; later reads can retry.
func (c *Catalog) loadSeasonsLater(ctx context.Context, showID int64, seasons []core.Title) {
	if c.tmdb() == nil {
		return
	}
	c.hydrateMu.Lock()
	if c.seasonLoading[showID] != nil || len(c.seasonLoading) >= 2 || c.now().Sub(c.seasonLoaded[showID]) < time.Minute {
		c.hydrateMu.Unlock()
		return
	}
	if c.seasonLoading == nil {
		c.seasonLoading = make(map[int64]chan struct{})
	}
	done := make(chan struct{})
	c.seasonLoading[showID] = done
	c.hydrateMu.Unlock()
	bg, cancel := context.WithTimeout(store.DetachQueryStats(reqmemo.With(context.WithoutCancel(ctx))), hydrateBudget)
	go func() {
		defer cancel()
		worker := context.WithValue(context.WithValue(bg, deferAiredCountsKey{}, true), waitBudgetKey{}, struct{}{})
		if len(seasons) == 0 {
			if _, err := c.ensureShow(worker, showID); err == nil {
				// Discovery must include undated summaries: their first aired
				// episode can establish the premiere that makes them listable.
				seasons, _ = c.scanTitleKeys(worker, `SELECT `+titleCols+` FROM catalog_titles`+c.indexedBy("catalog_titles_show")+` WHERE type='season' AND show_key=? ORDER BY season`, int64(core.ShowKey(showID)))
			}
		}
		_ = c.loadSeasons(worker, showID, seasons)
		// Publish final tallies only once, after the chunked fills. Intermediate
		// metadata loads must not recount the show after each season/chunk.
		c.cacheMu.Lock()
		delete(c.titleCache, core.ShowKey(showID))
		for _, season := range seasons {
			delete(c.titleCache, season.Key)
		}
		c.cacheMu.Unlock()
		keys := []core.RatingKey{core.ShowKey(showID)}
		for _, season := range seasons {
			keys = append(keys, season.Key)
		}
		// Read the committed records (including detail_at), not the original
		// summary slice: caching summaries here would make a finished season
		// look undetailed and force the next viewer to fetch it again.
		c.PreloadStored(bg, keys)
		c.hydrateMu.Lock()
		if c.seasonLoaded == nil || len(c.seasonLoaded) >= 256 {
			c.seasonLoaded = map[int64]time.Time{}
		}
		c.seasonLoaded[showID] = c.now()
		delete(c.seasonLoading, showID)
		close(done)
		c.hydrateMu.Unlock()
	}()
}
