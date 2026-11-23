package segcache

import (
	"time"

	"github.com/braedonsaunders/ether/internal/events"
)

const (
	evictSpikeWindow   = 60 * time.Second
	evictSpikeCooldown = 60 * time.Second
	evictSpikeMinCount = 128
)

type cacheEventState struct {
	bus             *events.Bus
	budgetFull      bool
	evictWinStart   time.Time
	evictWinBytes   int64
	evictWinEntries int
	lastSpikeAt     time.Time
}

type pendingCacheEvents struct {
	budgetFull      *events.CacheBudgetFullPayload
	budgetRecovered *events.CacheBudgetRecoveredPayload
	evictionSpike   *events.CacheEvictionSpikePayload
}

// SetEventBus attaches the non-blocking domain event bus to the segment cache.
func (c *Cache) SetEventBus(bus *events.Bus) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.ev.bus = bus
	c.mu.Unlock()
}

func (c *Cache) checkBudgetTransitionLocked(p *pendingCacheEvents) {
	if c.ev.bus == nil {
		return
	}
	if c.budget > 0 && c.used >= c.budget {
		if !c.ev.budgetFull {
			c.ev.budgetFull = true
			p.budgetFull = &events.CacheBudgetFullPayload{
				CacheKind:   "segment",
				UsedBytes:   c.used,
				BudgetBytes: c.budget,
				PercentUsed: float64(c.used) * 100 / float64(c.budget),
			}
		}
		return
	}
	if c.ev.budgetFull {
		recovered := c.budget <= 0 || c.used < lowWatermark(c.budget)
		if recovered {
			c.ev.budgetFull = false
			var pct float64
			if c.budget > 0 {
				pct = float64(c.used) * 100 / float64(c.budget)
			}
			p.budgetRecovered = &events.CacheBudgetRecoveredPayload{
				CacheKind:   "segment",
				UsedBytes:   c.used,
				BudgetBytes: c.budget,
				PercentUsed: pct,
			}
		}
	}
}

func (c *Cache) recordEvictionLocked(evictedBytes int64, evictedEntries int, p *pendingCacheEvents) {
	if c.ev.bus == nil || (evictedBytes <= 0 && evictedEntries <= 0) {
		return
	}
	now := c.now()
	if c.ev.evictWinStart.IsZero() || now.Sub(c.ev.evictWinStart) >= evictSpikeWindow {
		c.ev.evictWinStart = now
		c.ev.evictWinBytes = 0
		c.ev.evictWinEntries = 0
	}
	c.ev.evictWinBytes += evictedBytes
	c.ev.evictWinEntries += evictedEntries

	overEntries := c.ev.evictWinEntries >= evictSpikeMinCount
	overBudgetShare := c.budget > 0 && c.ev.evictWinBytes >= max(c.budget/4, 1)
	if !overEntries && !overBudgetShare {
		return
	}
	if !c.ev.lastSpikeAt.IsZero() && now.Sub(c.ev.lastSpikeAt) < evictSpikeCooldown {
		return
	}
	c.ev.lastSpikeAt = now
	p.evictionSpike = &events.CacheEvictionSpikePayload{
		CacheKind:      "segment",
		EvictedBytes:   c.ev.evictWinBytes,
		EvictedEntries: c.ev.evictWinEntries,
		WindowSec:      int(evictSpikeWindow / time.Second),
	}
}

func (c *Cache) publishPendingEvents(p pendingCacheEvents) {
	c.mu.Lock()
	bus := c.ev.bus
	c.mu.Unlock()
	if bus == nil {
		return
	}
	if p.budgetFull != nil {
		_ = bus.PublishTyped(events.EventCacheBudgetFull, *p.budgetFull)
	}
	if p.budgetRecovered != nil {
		_ = bus.PublishTyped(events.EventCacheBudgetRecovered, *p.budgetRecovered)
	}
	if p.evictionSpike != nil {
		_ = bus.PublishTyped(events.EventCacheEvictionSpike, *p.evictionSpike)
	}
}
