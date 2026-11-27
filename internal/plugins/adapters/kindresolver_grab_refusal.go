package adapters

import (
	"crypto/sha256"
	"maps"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/newznab"
)

// kindGrabRejects is indexed by the originating indexer and NZB link so the
// refusal applies to every track in that release.
type kindGrabRejects struct {
	mu    sync.Mutex
	until map[[sha256.Size]byte]time.Time
}

func (c *kindGrabRejects) lookup(key [sha256.Size]byte, now time.Time) *newznab.GrabRefusal {
	c.mu.Lock()
	defer c.mu.Unlock()
	if until := c.until[key]; now.Before(until) {
		return &newznab.GrabRefusal{RetryAt: until}
	}
	delete(c.until, key)
	return nil
}

func (c *kindGrabRejects) record(key [sha256.Size]byte, now, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.until == nil {
		c.until = make(map[[sha256.Size]byte]time.Time)
	}
	if len(c.until) >= maxUsenetCheckCacheEntries {
		maps.DeleteFunc(c.until, func(_ [sha256.Size]byte, deadline time.Time) bool {
			return !now.Before(deadline)
		})
		if len(c.until) >= maxUsenetCheckCacheEntries {
			for existing := range c.until {
				delete(c.until, existing)
				break
			}
		}
	}
	if until.After(c.until[key]) {
		c.until[key] = until
	}
}

func kindGrabRejectKey(rel core.Release) [sha256.Size]byte {
	return sha256.Sum256([]byte(firstNonEmpty(rel.IndexerID, rel.Indexer) + "\x00" + rel.NZBURL))
}
