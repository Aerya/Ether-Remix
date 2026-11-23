package debrid

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type resolvingInner struct {
	fakeInner
	resolved int
}

func (r *resolvingInner) ResolveTorrent(_ context.Context, rel core.Release) (core.Release, error) {
	r.resolved++
	rel.InfoHash = testHash
	return rel, nil
}

func TestIndexerSearchFiltersAndOrdersTorrents(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox")
	h.provs["torbox-0"].bulk = true
	uncached := strings.Repeat("99", 20)
	h.provs["torbox-0"].cached[testHash] = true
	inner := &fakeInner{results: []core.Release{
		{ID: "u1", Name: "Usenet"},
		{ID: "t-unc", Name: "Uncached", Protocol: core.ProtocolTorrent, InfoHash: uncached},
		{ID: "t-nohash", Name: "NoHash", Protocol: core.ProtocolTorrent},
		{ID: "t-cached", Name: "Cached", Protocol: core.ProtocolTorrent, InfoHash: testHash},
	}}
	x := NewIndexer(inner, h.svc)
	got, err := x.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "t-cached,u1,t-nohash" {
		t.Fatalf("results %v", ids)
	}
	// With no provider, torrents disappear entirely and Usenet is untouched.
	h.svc.UpdateConfig(config.Debrid{})
	got, _ = x.Search(context.Background(), core.SearchRequest{})
	if len(got) != 1 || got[0].ID != "u1" {
		t.Fatalf("no provider: %+v", got)
	}
}

func TestIndexerFetchDispatch(t *testing.T) {
	h := newHarness(t, 1<<20, "realdebrid")
	h.cacheEverywhere(testHash)
	inner := &resolvingInner{}
	x := NewIndexer(inner, h.svc)
	ctx := context.Background()
	b, err := x.FetchNZB(ctx, core.Release{ID: "u", Name: "Usenet"})
	if err != nil || string(b) != "<nzb/>" || inner.fetched.Load() != 1 {
		t.Fatalf("usenet: %q %v", b, err)
	}
	// A .torrent-only result is resolved to a hash through the inner indexer.
	b, err = x.FetchNZB(ctx, core.Release{ID: "t", Name: "Movie.2024", Protocol: core.ProtocolTorrent, NZBURL: "http://x/1.torrent"})
	if err != nil || !IsPayload(b) || inner.resolved != 1 {
		t.Fatalf("torrent: %v resolved %d", err, inner.resolved)
	}
	h.svc.UpdateConfig(config.Debrid{})
	if _, err := x.FetchNZB(ctx, core.Release{ID: "t", Protocol: core.ProtocolTorrent, InfoHash: testHash}); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("no provider: %v", err)
	}
}

// streamingInner answers one fast indexer at once and a slow one later.
type streamingInner struct {
	fakeInner
	fast, slow []core.Release
	h          *harness
}

func (s *streamingInner) SearchWith(ctx context.Context, _ core.SearchRequest, arrived func([]core.Release)) ([]core.Release, error) {
	arrived(s.fast)
	// Let the fast batch's cache check run while the slow indexer works.
	deadline := time.After(2 * time.Second)
	for s.h.provs["torbox-0"].callCount("check") == 0 {
		select {
		case <-deadline:
			return nil, errors.New("no early cache check")
		case <-time.After(5 * time.Millisecond):
		}
	}
	arrived(s.slow)
	return append(append([]core.Release(nil), s.fast...), s.slow...), nil
}

func TestIndexerChecksCacheAsIndexersAnswer(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox")
	h.provs["torbox-0"].bulk = true
	other := strings.Repeat("cd", 20)
	h.provs["torbox-0"].cached[testHash] = true
	inner := &streamingInner{h: h,
		fast: []core.Release{{ID: "fast", Name: "Fast", Protocol: core.ProtocolTorrent, InfoHash: testHash}},
		slow: []core.Release{{ID: "slow", Name: "Slow", Protocol: core.ProtocolTorrent, InfoHash: other}, {ID: "u", Name: "Usenet"}},
	}
	got, err := NewIndexer(inner, h.svc).Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	// The uncached slow torrent is dropped, the cached fast one leads.
	if strings.Join(ids, ",") != "fast,u" {
		t.Fatalf("results %v", ids)
	}
	if n := h.provs["torbox-0"].callCount("check"); n != 2 {
		t.Fatalf("cache checks = %d, want one per answering indexer", n)
	}
}

type packFallbackInner struct {
	fakeInner
	episodeResults  []core.Release
	packResults     []core.Release
	packCalls       int
	packTorrentOnly bool
}

func (p *packFallbackInner) Search(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
	if req.Episode == 0 {
		p.packCalls++
		p.packTorrentOnly = core.TorrentOnlySearch(ctx)
		return append([]core.Release(nil), p.packResults...), nil
	}
	return append([]core.Release(nil), p.episodeResults...), nil
}

func TestIndexerRescuesSeasonPackWhenSingleEpisodesUncached(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox")
	h.provs["torbox-0"].bulk = true
	deadEpHash := strings.Repeat("11", 20)
	cachedPackHash := testHash
	h.provs["torbox-0"].cached[cachedPackHash] = true
	inner := &packFallbackInner{
		episodeResults: []core.Release{
			{ID: "dead-ep", Name: "Obscure.Show.S02E07.480p.XviD", Protocol: core.ProtocolTorrent, InfoHash: deadEpHash},
		},
		packResults: []core.Release{
			{ID: "cached-pack", Name: "Obscure.Show.S02.COMPLETE.1080p.BluRay", Protocol: core.ProtocolTorrent, InfoHash: cachedPackHash, Info: core.ReleaseInfo{Seasons: []int{2}, FullSeason: true}},
		},
	}
	idx := NewIndexer(inner, h.svc)
	req := core.SearchRequest{Type: core.TypeEpisode, Title: "Obscure Show", Season: 2, Episode: 7}

	// Speculative callers must not fan out into a second Episode=0 season-pack search.
	sctx, _ := core.WithSpeculative(context.Background())
	specCtx := core.WithIndexerPurpose(sctx, core.IndexerPurposeWarmup)
	if _, err := idx.Search(specCtx, req); err != nil {
		t.Fatal(err)
	}
	if inner.packCalls != 0 {
		t.Fatalf("speculative search ran season-pack rescue: packCalls=%d", inner.packCalls)
	}

	// Foreground Play still rescues via cached season pack, scoped strictly to torrent indexers.
	got, err := idx.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "cached-pack" || inner.packCalls != 1 || !inner.packTorrentOnly {
		t.Fatalf("expected torrent-only cached season pack rescue, got=%+v packCalls=%d torrentOnly=%v", got, inner.packCalls, inner.packTorrentOnly)
	}
}

func TestIndexerResolveTorrentForwardsToInner(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox")
	inner := &resolvingInner{}
	x := NewIndexer(inner, h.svc)
	got, err := x.ResolveTorrent(context.Background(), core.Release{
		Name:     "Album",
		Protocol: core.ProtocolTorrent,
		NZBURL:   "https://cardigann.example/album.html",
	})
	if err != nil || got.InfoHash != testHash || inner.resolved != 1 {
		t.Fatalf("ResolveTorrent = %+v err=%v resolved=%d", got, err, inner.resolved)
	}
}

func TestListReleaseFilesChecksUnknownHashesAndRespectsUncached(t *testing.T) {
	ctx := context.Background()

	// Bulk-check provider (TorBox): unknown cached hash is checked via CheckCached
	// and listed; unknown uncached hash is skipped without calling Acquire.
	hBulk := newHarness(t, 1<<20, "torbox")
	hBulk.provs["torbox-0"].bulk = true
	hBulk.provs["torbox-0"].cached[testHash] = true
	hBulk.provs["torbox-0"].files = []File{
		{ID: "1", Path: "Album/01.flac", Size: 20_000_000},
		{ID: "2", Path: "Album/02.flac", Size: 22_000_000},
	}
	files, err := hBulk.svc.ListReleaseFiles(ctx, core.Release{
		Name:     "Album",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
	})
	if err != nil || len(files) != 2 || files[0].Path != "Album/01.flac" || files[1].Index != 1 {
		t.Fatalf("bulk cached ListReleaseFiles = %+v err=%v", files, err)
	}

	uncachedHash := strings.Repeat("55", 20)
	if _, err := hBulk.svc.ListReleaseFiles(ctx, core.Release{
		Name:     "Missing",
		Protocol: core.ProtocolTorrent,
		InfoHash: uncachedHash,
	}); !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("bulk uncached err = %v, want ErrNotCached", err)
	}
	if got := hBulk.provs["torbox-0"].callCount("acquire"); got != 1 {
		t.Fatalf("bulk provider acquire calls = %d, want 1 (only cached hash)", got)
	}

	// Non-bulk provider (Real-Debrid): newly-resolved cached hash lists files,
	// and an uncached hash returns ErrNotCached even if Choose inspected files.
	hRD := newHarness(t, 1<<20, "realdebrid")
	hRD.provs["realdebrid-0"].cached[testHash] = true
	hRD.provs["realdebrid-0"].files = []File{
		{ID: "1", Path: "Album/01.flac", Size: 20_000_000},
	}
	files, err = hRD.svc.ListReleaseFiles(ctx, core.Release{
		Name:     "Album",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
	})
	if err != nil || len(files) != 1 || files[0].Path != "Album/01.flac" {
		t.Fatalf("non-bulk cached ListReleaseFiles = %+v err=%v", files, err)
	}
	if _, err := hRD.svc.ListReleaseFiles(ctx, core.Release{
		Name:     "Uncached",
		Protocol: core.ProtocolTorrent,
		InfoHash: uncachedHash,
	}); !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("non-bulk uncached err = %v, want ErrNotCached", err)
	}
}
