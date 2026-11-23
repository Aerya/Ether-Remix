package resolver

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestUsenetBackoffDemotesUsenetInResolveAndCandidateScoring(t *testing.T) {
	ctx := context.Background()
	var usenetServing atomic.Bool
	usenetServing.Store(true)

	rels := routingReleases("The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-TORBOX")
	idx := &fakeIndexer{releases: rels}
	cfg := testCfg()
	cfg.Resolver.BlockedTerms = []string{"badgroup"}

	svc := New(idx, fakeHealth{}, nil, cfg, Options{
		UsenetServing: usenetServing.Load,
	})
	svc.SetSourceSignals(cachedSet{hashA: true})
	title := routingTitle(false)

	// 1. Healthy Usenet leads on score and verifies into svc.verified.
	if got := resolveOrder(t, svc, title); got[0] != "usenet" || got[1] != "torrent" {
		t.Fatalf("healthy order = %v, want [usenet torrent]", got)
	}

	// 2. While Usenet is in 481 simultaneous-IP backoff (UsenetServing=false),
	// Resolve demotes Usenet behind TorBox/debrid even when Usenet was previously
	// cached in svc.verified or SourcePreference explicitly prefers Usenet.
	usenetServing.Store(false)
	for _, pref := range []string{PreferAuto, PreferUsenet} {
		c := cfg
		c.Resolver.SourcePreference = pref
		svc.UpdateConfig(c)
		got := resolveOrder(t, svc, title)
		if len(got) < 2 || got[0] != "torrent" || got[1] != "usenet" {
			t.Fatalf("backoff order (pref=%q) = %v, want [torrent usenet]", pref, got)
		}
	}
	svc.UpdateConfig(cfg)

	// 3. ScoreSourceCandidates makes the 2m Usenet backoff visible in numeric
	// candidate scores while preserving input slice order and keeping rejected
	// releases below demoted Usenet candidates.
	blocked := rel("blocked", "The.Signal.1999.1080p.WEB-DL.H.264-badgroup", 8000)
	scored, err := ScoreSourceCandidates(ctx, svc, "", []core.Release{rels[0], rels[1], blocked})
	if err != nil {
		t.Fatalf("ScoreSourceCandidates during backoff: %v", err)
	}
	if len(scored) != 3 || scored[0].Score == nil || scored[1].Score == nil || scored[2].Score == nil {
		t.Fatalf("unexpected scored candidates: %+v", scored)
	}
	if !(*scored[1].Score > *scored[0].Score && *scored[0].Score > *scored[2].Score) {
		t.Fatalf("scores during Usenet backoff = usenet:%d torrent:%d blocked:%d, want torrent > usenet > blocked",
			*scored[0].Score, *scored[1].Score, *scored[2].Score)
	}

	// 4. When only Usenet candidates exist, Resolve keeps Usenet as a last resort
	// (so bounded stream-hold can still serve if backoff clears quickly) rather
	// than returning ErrNotAvailable.
	usenetOnlySvc := New(&fakeIndexer{releases: []core.Release{rels[0]}}, fakeHealth{}, nil, cfg, Options{
		UsenetServing: func() bool { return false },
	})
	res, err := usenetOnlySvc.Resolve(ctx, core.ResolveRequest{Title: title, Client: webCaps()})
	if err != nil || res.Release.ID != "usenet" {
		t.Fatalf("Usenet-only resolve during backoff = (%+v, %v), want usenet fallback", res, err)
	}

	// 5. Once Usenet leaves backoff, Usenet leads again without losing its state.
	usenetServing.Store(true)
	if got := resolveOrder(t, svc, title); got[0] != "usenet" {
		t.Fatalf("recovered order = %v, want usenet first again", got)
	}
}
