package newznab

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Keep expired descriptors for one day, only as a last resort during an
// outage. They are candidates, never evidence that a stream is still cached.
const staleSearchGrace = 24 * time.Hour

func (c *Client) staleFallback(ctx context.Context, key string, err error) ([]core.Release, bool) {
	if ctx.Err() != nil || c.ttl() <= 0 || core.IsBackgroundCheck(ctx) || core.SpeculativeIndexerWork(ctx) ||
		!(errors.Is(err, ErrIndexerPaused) || transientFailure(err)) {
		return nil, false
	}
	now := c.now()
	var results []core.Release
	if c.db == nil {
		c.cacheMu.RLock()
		e, ok := c.memCache[key]
		if ok && now.After(e.expires) && now.Before(e.expires.Add(staleSearchGrace)) {
			results = append([]core.Release(nil), e.results...)
		}
		c.cacheMu.RUnlock()
	} else {
		var raw string
		var expires int64
		if c.db.QueryRowContext(ctx, `SELECT results,expires_at FROM newznab_cache WHERE cache_key=? AND indexer_id=?`, key, c.indexerID()).Scan(&raw, &expires) != nil || now.Unix() <= expires || now.Unix() >= expires+int64(staleSearchGrace/time.Second) {
			return nil, false
		}
		results, _ = c.decodeCached(raw)
	}
	out := make([]core.Release, 0, len(results))
	for _, r := range results {
		// A signed HTTP stream may have expired or changed owner. Only reusable
		// descriptors go through the normal torrent/NZB health checks again.
		if r.Protocol == core.ProtocolHTTP {
			continue
		}
		r.DebridCached, r.DebridUncached = nil, nil
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, false
	}
	core.NoteSearchIncomplete(ctx)
	c.emit(Event{Kind: "stale-cache", Purpose: usagePurpose(ctx), Cached: true, Results: len(out)})
	c.log.Warn("indexer unavailable: using expired search candidates", "indexer", c.Name(), "results", len(out))
	return out, true
}
