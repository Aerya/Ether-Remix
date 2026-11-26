package usenet

import (
	"github.com/braedonsaunders/ether/internal/core"
)

// Per-file miss hints (core.ProviderMissHint). The stream engine attaches one
// hint per posted file to its article requests; eachOnce records each
// provider's 430s and deliveries in it, and route asks a provider that keeps
// missing this file's articles after its tier peers. Within a tier only: a
// hint never moves a provider ahead of or behind another tier, and a demoted
// provider is still asked when its peers miss, so missing-article proof is
// unchanged. This matters for tiers of several providers (balanced mode):
// without it a short-retention account at the head of the rotation costs a
// round trip on every segment of an old post.

func samePoolTier(a, b *pool, balanced bool) bool {
	return a.cfg.Backfill == b.cfg.Backfill &&
		((balanced && !a.cfg.Backfill) || a.cfg.Priority == b.cfg.Priority)
}

// demoteMissed returns pools with, inside each run of one tier, the providers
// the hint demotes moved behind the others (stable). pools is returned as
// is when nothing moves.
func demoteMissed(h *core.ProviderMissHint, pools []*pool, balanced bool) []*pool {
	if h == nil || len(pools) < 2 {
		return pools
	}
	demoted := make([]bool, len(pools))
	moved := false
	for i, p := range pools {
		demoted[i] = h.Demoted(p.key)
		moved = moved || demoted[i]
	}
	if !moved {
		return pools
	}
	out := make([]*pool, 0, len(pools))
	for start := 0; start < len(pools); {
		end := start + 1
		for end < len(pools) && samePoolTier(pools[start], pools[end], balanced) {
			end++
		}
		for _, late := range []bool{false, true} {
			for i := start; i < end; i++ {
				if demoted[i] == late {
					out = append(out, pools[i])
				}
			}
		}
		start = end
	}
	return out
}
