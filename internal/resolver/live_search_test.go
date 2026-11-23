package resolver

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type gatedStreamIndexer struct {
	batch1      []core.Release
	batch2      []core.Release
	batch1Sent  chan struct{}
	releaseSlow chan struct{}
}

func (*gatedStreamIndexer) Name() string { return "gated-stream" }

func (g *gatedStreamIndexer) Search(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
	return g.SearchWith(ctx, req, nil)
}

func (g *gatedStreamIndexer) SearchWith(ctx context.Context, _ core.SearchRequest, arrived func([]core.Release)) ([]core.Release, error) {
	if arrived != nil && len(g.batch1) > 0 {
		arrived(append([]core.Release(nil), g.batch1...))
	}
	select {
	case <-g.batch1Sent:
	default:
		close(g.batch1Sent)
	}
	select {
	case <-g.releaseSlow:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if arrived != nil && len(g.batch2) > 0 {
		arrived(append([]core.Release(nil), g.batch2...))
	}
	all := make([]core.Release, 0, len(g.batch1)+len(g.batch2))
	all = append(all, g.batch1...)
	all = append(all, g.batch2...)
	return all, nil
}

func (*gatedStreamIndexer) FetchNZB(_ context.Context, r core.Release) ([]byte, error) {
	return []byte("nzb-" + r.ID), nil
}

// TestWatchVersionSlotsStreamsArrivalsBeforeSlowIndexerFinishes verifies the
// core #715 resolver invariants:
//   - WatchVersionSlots started before searchCached wakes when the live search
//     begins and emits filled non-Auto slots as soon as fast indexers answer,
//     without waiting for slow straggler indexers.
//   - Play-intent VersionSlots (without LocalResponse) sees arrived live
//     releases while the slow indexer is still running, without writing an
//     incomplete fleet answer into searchCache.
//   - RefillSearchCache does not launch a duplicate search while a live search
//     is already running for the title.
func TestWatchVersionSlotsStreamsArrivalsBeforeSlowIndexerFinishes(t *testing.T) {
	cfg := testCfg()
	cfg.Quality.Default = "1080p"
	cfg.Quality.Profiles = []core.QualityProfile{{Name: "1080p", MaxHeight: 1080}}
	cfg.Resolver.OfferHigherVersions = true
	cfg.Resolver.AlternativesPerResolution = true
	cfg.Resolver.SameQualityAlternatives = 0

	rel1080 := withParsed(core.Release{
		ID: "rel-1080", Indexer: "gated-stream",
		Name: "The.Signal.1999.1080p.WEB-DL.DDP5.1.H264-FAST", Size: 4 << 30,
		PubDate: time.Now().Add(-24 * time.Hour), NZBURL: "https://example.invalid/1080",
	})
	rel4K := withParsed(core.Release{
		ID: "rel-4k", Indexer: "gated-stream",
		Name: "The.Signal.1999.2160p.WEB-DL.HDR10.DDP5.1.HEVC-FAST", Size: 12 << 30,
		PubDate: time.Now().Add(-24 * time.Hour), NZBURL: "https://example.invalid/4k",
	})
	rel720 := withParsed(core.Release{
		ID: "rel-720", Indexer: "gated-stream",
		Name: "The.Signal.1999.720p.WEB-DL.DDP5.1.H264-SLOW", Size: 2 << 30,
		PubDate: time.Now().Add(-24 * time.Hour), NZBURL: "https://example.invalid/720",
	})

	idx := &gatedStreamIndexer{
		batch1:      []core.Release{rel1080, rel4K},
		batch2:      []core.Release{rel720},
		batch1Sent:  make(chan struct{}),
		releaseSlow: make(chan struct{}),
	}
	svc := New(idx, fakeHealth{}, nil, cfg, Options{})
	defer svc.Close()

	title := matrixTitle()
	req := core.ResolveRequest{Title: title, Client: appleTVCaps()}

	got4K := make(chan struct{}, 1)
	got720 := make(chan struct{}, 1)
	watchDone := make(chan error, 1)
	autoDone := make(chan struct{})

	watchCtx, watchCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer watchCancel()
	go func() {
		watchDone <- svc.WatchVersionSlots(watchCtx, req, core.MaxVariants, autoDone, func(vs []core.ReleaseVariant) {
			for _, v := range vs {
				if !v.Auto && v.Release.ID == rel4K.ID {
					select {
					case got4K <- struct{}{}:
					default:
					}
				}
				if !v.Auto && v.Release.ID == rel720.ID {
					select {
					case got720 <- struct{}{}:
					default:
					}
				}
			}
		})
	}()

	resolveDone := make(chan error, 1)
	go func() {
		defer close(autoDone)
		_, err := svc.Resolve(watchCtx, req)
		resolveDone <- err
	}()

	// Fast batch arrives while the slow indexer is still blocked.
	select {
	case <-got4K:
	case <-time.After(3 * time.Second):
		t.Fatal("WatchVersionSlots did not emit 4K slot before slow indexer finished")
	}

	// searchCache must still be empty (never cache an incomplete fleet answer),
	// yet play-intent VersionSlots must already see the arrived 4K release and
	// RefillSearchCache must not launch a duplicate search.
	if _, cached := svc.peekSearchCache(searchCacheKey(searchRequestFor(title))); cached {
		t.Fatal("searchCache was populated before slow indexer finished")
	}
	if ch := svc.RefillSearchCache(title); ch != nil {
		t.Fatal("RefillSearchCache started a duplicate search while live search was active")
	}
	liveSlots, err := svc.VersionSlots(core.WithoutLocalResponse(watchCtx), req, core.MaxVariants, true)
	if err != nil {
		t.Fatalf("VersionSlots during live search: %v", err)
	}
	found4K := false
	for _, v := range liveSlots {
		if !v.Auto && v.Release.ID == rel4K.ID {
			found4K = true
			break
		}
	}
	if !found4K {
		t.Fatalf("play-intent VersionSlots missed live arrived 4K release: %+v", liveSlots)
	}

	// Unblock slow indexer: WatchVersionSlots receives batch 2 and completes,
	// and searchCache is now populated with the complete fleet.
	close(idx.releaseSlow)

	select {
	case <-got720:
	case <-time.After(3 * time.Second):
		t.Fatal("WatchVersionSlots did not emit 720p slot after slow indexer completed")
	}
	if err := <-watchDone; err != nil {
		t.Fatalf("WatchVersionSlots: %v", err)
	}
	if err := <-resolveDone; err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, cached := svc.peekSearchCache(searchCacheKey(searchRequestFor(title))); !cached {
		t.Fatal("searchCache not populated after full fleet completed")
	}
}
