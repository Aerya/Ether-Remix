package resolver

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// A warm-up nobody has pressed Play on checks at most
// speculativeHealthAttempts releases: every check downloads an NZB and
// indexers count grabs (#224). A failed capped warm-up proves nothing, so the
// title is not marked unavailable, and a real Play still checks them all.
type quotaIndexer struct {
	fakeIndexer
	grabs   atomic.Int64
	promote func()
}

func (f *quotaIndexer) FetchNZB(ctx context.Context, r core.Release) ([]byte, error) {
	if n := f.grabs.Add(1); n == speculativeHealthAttempts && f.promote != nil {
		f.promote()
	}
	return f.fakeIndexer.FetchNZB(ctx, r)
}

func TestSpeculativeWarmUpCapsHealthChecks(t *testing.T) {
	for _, tc := range []struct {
		name string
		feed bool
		race int
	}{
		{name: "ranked"}, {name: "feed", feed: true}, {name: "feed-race", feed: true, race: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var releases []core.Release
			unhealthy := map[string]bool{}
			for i := range 12 {
				id := fmt.Sprintf("bad%02d", i)
				releases = append(releases, rel(id, fmt.Sprintf("The.Signal.1999.1080p.WEB-DL.H264-G%02d", i), int64(4000+i)))
				unhealthy[id] = true
			}
			var checks atomic.Int64
			idx := &quotaIndexer{fakeIndexer: fakeIndexer{releases: releases}}
			cfg := testCfg()
			cfg.Resolver.HealthRace = tc.race
			opts := Options{}
			if tc.feed {
				// Later search results outrank the first feed batch. A per-stage limit
				// would spend another allowance on the new leaders.
				opts.Discovered = func(context.Context, *core.Title) []core.Release { return releases[:6] }
			}
			svc := New(idx, fakeHealth{unhealthy: unhealthy, checks: &checks}, nil, cfg, opts)
			defer svc.Close()

			warm, _ := core.WithSpeculative(context.Background())
			if _, err := svc.Resolve(warm, core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}); err == nil {
				t.Fatal("unhealthy releases resolved")
			}
			if n := checks.Load(); n == 0 || n > speculativeHealthAttempts {
				t.Fatalf("warm-up ran %d health checks, want 1..%d", n, speculativeHealthAttempts)
			}
			if n := idx.grabs.Load(); n == 0 || n > speculativeHealthAttempts {
				t.Fatalf("warm-up downloaded %d NZBs, want 1..%d", n, speculativeHealthAttempts)
			}
			if ok, known := svc.Available(context.Background(), matrixTitle().Key); known && !ok {
				t.Fatal("a capped warm-up marked the title unavailable")
			}

			checks.Store(0)
			if _, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}); err == nil {
				t.Fatal("unhealthy releases resolved")
			}
			if n := checks.Load(); n <= speculativeHealthAttempts {
				t.Fatalf("a real Play ran only %d health checks", n)
			}

		})
	}
	t.Run("promoted-feed", func(t *testing.T) {
		var releases []core.Release
		unhealthy := map[string]bool{}
		for i := range 12 {
			id := fmt.Sprintf("bad%02d", i)
			releases = append(releases, rel(id, fmt.Sprintf("The.Signal.1999.1080p.WEB-DL.H264-G%02d", i), int64(4000+i)))
			unhealthy[id] = true
		}
		ctx, promote := core.WithSpeculative(context.Background())
		idx := &quotaIndexer{fakeIndexer: fakeIndexer{releases: releases}, promote: promote}
		svc := New(idx, fakeHealth{unhealthy: unhealthy}, nil, testCfg(), Options{Discovered: func(context.Context, *core.Title) []core.Release { return releases }})
		defer svc.Close()
		_, _ = svc.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
		if n := idx.grabs.Load(); n <= speculativeHealthAttempts {
			t.Fatalf("promoted Play stayed capped at %d NZBs", n)
		}
	})
}
