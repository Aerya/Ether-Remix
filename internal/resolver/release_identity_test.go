package resolver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/store"
)

type identityTestIndexer struct {
	mu      sync.Mutex
	results []core.Release
	nzbs    map[string][]byte
	fetchFn func(core.Release) ([]byte, error)
	grabs   []string
}

func (ix *identityTestIndexer) Name() string { return "identity-test" }

func (ix *identityTestIndexer) Search(_ context.Context, _ core.SearchRequest) ([]core.Release, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	out := make([]core.Release, len(ix.results))
	copy(out, ix.results)
	return out, nil
}

func (ix *identityTestIndexer) FetchNZB(_ context.Context, rel core.Release) ([]byte, error) {
	ix.mu.Lock()
	ix.grabs = append(ix.grabs, rel.ID)
	fn := ix.fetchFn
	data := ix.nzbs[rel.ID]
	ix.mu.Unlock()
	if fn != nil {
		return fn(rel)
	}
	if data == nil {
		return nil, &newznab.APIError{Code: 404, Description: "not found"}
	}
	return data, nil
}

func (ix *identityTestIndexer) grabIDs() []string {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return append([]string(nil), ix.grabs...)
}

type identityTestHealth struct {
	mu      sync.Mutex
	verdict map[string]error // releaseID -> nil (healthy) or error
	checks  []string
}

func (h *identityTestHealth) Check(ctx context.Context, payload []byte) (bool, error) {
	return h.CheckRelease(ctx, "", payload)
}

func (h *identityTestHealth) CheckRelease(_ context.Context, releaseID string, _ []byte) (bool, error) {
	h.mu.Lock()
	h.checks = append(h.checks, releaseID)
	err, exists := h.verdict[releaseID]
	h.mu.Unlock()
	if !exists {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (h *identityTestHealth) checkedIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.checks...)
}

func sampleNZBXML(poster string, unixSec int64, msgIDs ...string) []byte {
	var segs string
	for i, id := range msgIDs {
		segs += fmt.Sprintf(`<segment bytes="1000000" number="%d">%s</segment>`, i+1, id)
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <file poster=%q date="%d" subject="Movie.2020.1080p.BluRay.x264-GRP (1/1)">
    <groups><group>alt.binaries.movies</group></groups>
    <segments>%s</segments>
  </file>
</nzb>`, poster, unixSec, segs))
}

func openResolverTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatalf("store.OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testMovieTitle() *core.Title {
	return &core.Title{
		Key:      core.MovieKey(4242),
		Type:     core.TypeMovie,
		Title:    "Test Movie",
		Year:     2020,
		Runtime:  100 * time.Minute,
		Language: "en",
	}
}

func TestCrossIndexerBlocklistAndMigration8(t *testing.T) {
	ctx := context.Background()
	db := openResolverTestDB(t)
	now := time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC)
	upload := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	sharedNZB := sampleNZBXML("poster1@usenet.example", upload.Unix(), "part1@usenet.example", "part2@usenet.example")
	distinctNZB := sampleNZBXML("poster2@usenet.example", upload.Add(-48*time.Hour).Unix(), "healthy1@usenet.example", "healthy2@usenet.example")

	relIdxA := core.Release{
		ID:         "sha1-idxA-guid1",
		GUID:       "guid1",
		Indexer:    "IndexerA",
		IndexerID:  "ix-a",
		Name:       "Test.Movie.2020.1080p.BluRay.x264-GRP",
		Size:       8_000_000_000,
		PubDate:    upload.Add(2 * time.Hour),
		UsenetDate: upload,
		Poster:     "poster1@usenet.example",
	}
	// Same Usenet post on Indexer B: different GUID/ID, slightly different yEnc byte count (~2%), same poster & upload day.
	relIdxBPreFetch := core.Release{
		ID:         "sha1-idxB-guid2",
		GUID:       "guid2",
		Indexer:    "IndexerB",
		IndexerID:  "ix-b",
		Name:       "Test.Movie.2020.1080p.BluRay.x264-GRP-Obfuscated",
		Size:       8_120_000_000,
		PubDate:    upload.Add(5 * time.Hour),
		UsenetDate: upload.Add(30 * time.Minute),
		Poster:     "Poster1@usenet.example",
	}
	// Same Usenet post on Indexer C, which omitted poster in search feed so identity is discovered only from NZB message-IDs.
	relIdxCPostFetch := core.Release{
		ID:        "sha1-idxC-guid3",
		GUID:      "guid3",
		Indexer:   "IndexerC",
		IndexerID: "ix-c",
		Name:      "Test.Movie.2020.1080p.WEB-DL.DDP5.1.H.264-OTHER",
		Size:      5_000_000_000,
		PubDate:   upload,
	}
	relHealthy := core.Release{
		ID:         "sha1-idxD-healthy",
		GUID:       "guid4",
		Indexer:    "IndexerD",
		IndexerID:  "ix-d",
		Name:       "Test.Movie.2020.720p.BluRay.x264-GOOD",
		Size:       4_200_000_000,
		PubDate:    upload,
		UsenetDate: upload.Add(-48 * time.Hour),
		Poster:     "poster2@usenet.example",
	}

	ix := &identityTestIndexer{
		results: []core.Release{relIdxA},
		nzbs: map[string][]byte{
			relIdxA.ID:          sharedNZB,
			relIdxBPreFetch.ID:  sharedNZB,
			relIdxCPostFetch.ID: sharedNZB,
			relHealthy.ID:       distinctNZB,
		},
	}
	health := &identityTestHealth{}
	cfg := config.Defaults("")
	svc := New(ix, health, db, cfg, Options{Now: func() time.Time { return now }})
	movie := testMovieTitle()

	// Resolve Indexer A first so its NZB content identity is learned, then mark it bad (as mid-stream failover does).
	res1, err := svc.Resolve(ctx, core.ResolveRequest{Title: movie})
	if err != nil {
		t.Fatalf("initial Resolve: %v", err)
	}
	if res1.Release.ID != relIdxA.ID {
		t.Fatalf("winner = %q, want %q (per-indexer ID preserved)", res1.Release.ID, relIdxA.ID)
	}
	if res1.Release.ContentID != nzb.ContentID(sharedNZB) {
		t.Fatalf("Release.ContentID = %q, want %q", res1.Release.ContentID, nzb.ContentID(sharedNZB))
	}
	if err := svc.MarkBad(ctx, relIdxA.ID, "missing articles mid-stream"); err != nil {
		t.Fatalf("MarkBad: %v", err)
	}
	if err := svc.flushBookkeeping(ctx); err != nil {
		t.Fatalf("flushBookkeeping: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Restart a fresh Service on the same SQLite database; Indexer B & C offer the same dead post under new GUIDs.
	ix2 := &identityTestIndexer{
		results: []core.Release{relIdxBPreFetch, relIdxCPostFetch, relHealthy},
		nzbs:    ix.nzbs,
	}
	health2 := &identityTestHealth{}
	svc2 := New(ix2, health2, db, cfg, Options{Now: func() time.Time { return now.Add(time.Minute) }})
	defer svc2.Close()

	res2, err := svc2.Resolve(ctx, core.ResolveRequest{Title: movie})
	if err != nil {
		t.Fatalf("second Resolve after restart: %v", err)
	}
	if res2.Release.ID != relHealthy.ID {
		t.Fatalf("expected healthy distinct post %q, got %q", relHealthy.ID, res2.Release.ID)
	}
	for _, grabbed := range ix2.grabIDs() {
		if grabbed == relIdxBPreFetch.ID {
			t.Fatalf("Indexer B copy %q matched pre-fetch content identity and should not have been grabbed", relIdxBPreFetch.ID)
		}
	}
	for _, checked := range health2.checkedIDs() {
		if checked == relIdxBPreFetch.ID || checked == relIdxCPostFetch.ID {
			t.Fatalf("blocklisted post %q should never have reached NNTP health check", checked)
		}
	}

	// Blocklist display still preserves the original per-indexer Release.ID.
	entries, total, err := svc2.Blocklist(ctx, BlockFilter{})
	if err != nil || total != 1 || entries[0].ReleaseID != relIdxA.ID {
		t.Fatalf("Blocklist = (%v, %d, %v), want single entry for %q", entries, total, err, relIdxA.ID)
	}

	// Unblocking clears the cross-indexer content block too.
	if _, err := svc2.Unblock(ctx, relIdxA.ID); err != nil {
		t.Fatalf("Unblock: %v", err)
	}
	if svc2.isBadRelease(relIdxBPreFetch) {
		t.Fatal("expected Indexer B copy to be unblocked after Unblock")
	}
}

func TestCrossIndexerVerifiedHealthAndFailedGrabMemory(t *testing.T) {
	ctx := context.Background()
	db := openResolverTestDB(t)
	now := time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC)
	upload := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)

	deadNZB := sampleNZBXML("deadposter@usenet.example", upload.Unix(), "dead-seg-1@usenet.example")
	goodNZB := sampleNZBXML("goodposter@usenet.example", upload.Unix(), "good-seg-1@usenet.example")

	deadIdxA := core.Release{
		ID: "dead-a", GUID: "g-dead-a", Indexer: "IdxA", IndexerID: "ix-a",
		Name: "Test.Movie.2020.1080p.Remux.AVC.DTS-HD.MA.5.1-GRP", Size: 20_000_000_000,
		PubDate: upload, UsenetDate: upload, Poster: "deadposter@usenet.example",
	}
	deadIdxB := core.Release{
		ID: "dead-b", GUID: "g-dead-b", Indexer: "IdxB", IndexerID: "ix-b",
		Name: "Test.Movie.2020.1080p.Remux.AVC.DTS-HD.MA.5.1-GRP-xpost", Size: 20_100_000_000,
		PubDate: upload, UsenetDate: upload, Poster: "deadposter@usenet.example",
	}
	goodIdxA := core.Release{
		ID: "good-a", GUID: "g-good-a", Indexer: "IdxA", IndexerID: "ix-a",
		Name: "Test.Movie.2020.1080p.BluRay.x264-GOOD", Size: 8_000_000_000,
		PubDate: upload, UsenetDate: upload, Poster: "goodposter@usenet.example",
	}
	goodIdxB := core.Release{
		ID: "good-b", GUID: "g-good-b", Indexer: "IdxB", IndexerID: "ix-b",
		Name: "Test.Movie.2020.1080p.BluRay.x264-GOOD", Size: 8_050_000_000,
		PubDate: upload, UsenetDate: upload, Poster: "goodposter@usenet.example",
	}

	ix1 := &identityTestIndexer{
		results: []core.Release{deadIdxA, goodIdxA},
		nzbs: map[string][]byte{
			deadIdxA.ID: deadNZB,
			goodIdxA.ID: goodNZB,
		},
	}
	health1 := &identityTestHealth{
		verdict: map[string]error{
			deadIdxA.ID: core.ErrArticleMissing,
			goodIdxA.ID: nil,
		},
	}
	cfg := config.Defaults("")
	svc1 := New(ix1, health1, db, cfg, Options{Now: func() time.Time { return now }})
	movie := testMovieTitle()

	res1, err := svc1.Resolve(ctx, core.ResolveRequest{Title: movie})
	if err != nil {
		t.Fatalf("Resolve 1: %v", err)
	}
	if res1.Release.ID != goodIdxA.ID {
		t.Fatalf("winner = %q, want %q", res1.Release.ID, goodIdxA.ID)
	}
	if err := svc1.flushBookkeeping(ctx); err != nil {
		t.Fatalf("flushBookkeeping: %v", err)
	}
	if err := svc1.Close(); err != nil {
		t.Fatalf("Close svc1: %v", err)
	}

	// Advance past in-memory failedHealthTTL (2m) and restart:
	// - deadIdxB (same dead post on Indexer B) is skipped via durable resolver_failed_grabs without grabbing!
	// - goodIdxB (same verified post on Indexer B) reuses resolver_verified without running a second NNTP check!
	now2 := now.Add(10 * time.Minute)
	ix2 := &identityTestIndexer{
		results: []core.Release{deadIdxB, goodIdxB},
		nzbs: map[string][]byte{
			deadIdxB.ID: deadNZB,
			goodIdxB.ID: goodNZB,
		},
	}
	health2 := &identityTestHealth{
		verdict: map[string]error{
			deadIdxB.ID: core.ErrArticleMissing,
			goodIdxB.ID: nil,
		},
	}
	svc2 := New(ix2, health2, db, cfg, Options{Now: func() time.Time { return now2 }})
	defer svc2.Close()

	res2, err := svc2.Resolve(ctx, core.ResolveRequest{Title: movie})
	if err != nil {
		t.Fatalf("Resolve 2: %v", err)
	}
	if res2.Release.ID != goodIdxB.ID {
		t.Fatalf("winner = %q, want %q", res2.Release.ID, goodIdxB.ID)
	}
	for _, grabbed := range ix2.grabIDs() {
		if grabbed == deadIdxB.ID {
			t.Fatalf("dead post %q was grabbed on Indexer B despite failed-grab memory", deadIdxB.ID)
		}
	}
	if len(health2.checkedIDs()) != 0 {
		t.Fatalf("expected 0 NNTP health checks for already-verified post on Indexer B, got %v", health2.checkedIDs())
	}
}

func TestRepostGroupingOneGrabOneSlotAndQuietFallback(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

	// Group 1 (1080p WEB-DL): three near-duplicate reposts within ±10% size:
	// - g1Best: highest custom/revision rank (PROPER), will fail health in subtest B
	// - g1SameDeadPost: same dead Usenet upload on another indexer (same poster+day+size bucket)
	// - g1FreshRepost: genuine repost by a different poster/day with healthy articles
	g1Best := core.Release{
		ID: "g1-best", GUID: "g1-1", Indexer: "IdxA", IndexerID: "ix-a",
		Name: "Test.Movie.2020.PROPER.1080p.WEB-DL.DDP5.1.H.264-FLUX", Size: 5_000_000_000,
		PubDate: t1, UsenetDate: t1, Poster: "orig@usenet.example",
	}
	g1SameDeadPost := core.Release{
		ID: "g1-same-dead", GUID: "g1-2", Indexer: "IdxB", IndexerID: "ix-b",
		Name: "test movie (2020) proper 1080p web-dl ddp5.1 h264-FLUX-Obfuscated", Size: 5_050_000_000,
		PubDate: t1, UsenetDate: t1, Poster: "orig@usenet.example",
	}
	g1FreshRepost := core.Release{
		ID: "g1-fresh-repost", GUID: "g1-3", Indexer: "IdxC", IndexerID: "ix-c",
		Name: "Test.Movie.2020.1080p.WEB-DL.DDP5.1.H.264-NTb-AsRequested-xpost.mkv", Size: 4_700_000_000,
		PubDate: t2, UsenetDate: t2, Poster: "reposter@usenet.example",
	}

	// Group 2 (720p BluRay): two near-duplicate reposts within ±10%.
	g2Best := core.Release{
		ID: "g2-best", GUID: "g2-1", Indexer: "IdxA", IndexerID: "ix-a",
		Name: "Test.Movie.2020.PROPER.720p.BluRay.DTS.x264-SPARKS", Size: 4_200_000_000,
		PubDate: t2, UsenetDate: t2, Poster: "sparks@usenet.example",
	}
	g2Dup := core.Release{
		ID: "g2-dup", GUID: "g2-2", Indexer: "IdxB", IndexerID: "ix-b",
		Name: "Test Movie 2020 720p BluRay DTS x264-DRONES-Scrambled", Size: 4_000_000_000,
		PubDate: t1, UsenetDate: t1, Poster: "drones@usenet.example",
	}

	// Group 3 (1080p WEB-DL but >10% larger, so NOT in Group 1).
	g3DistinctSize := core.Release{
		ID: "g3-large", GUID: "g3-1", Indexer: "IdxA", IndexerID: "ix-a",
		Name: "Test.Movie.2020.1080p.WEB-DL.DDP5.1.H.264-BIG", Size: 7_500_000_000,
		PubDate: t1, UsenetDate: t1, Poster: "big@usenet.example",
	}

	nzbs := map[string][]byte{
		g1Best.ID:         sampleNZBXML("orig@usenet.example", t1.Unix(), "g1-dead@usenet.example"),
		g1SameDeadPost.ID: sampleNZBXML("orig@usenet.example", t1.Unix(), "g1-dead@usenet.example"),
		g1FreshRepost.ID:  sampleNZBXML("reposter@usenet.example", t2.Unix(), "g1-fresh@usenet.example"),
		g2Best.ID:         sampleNZBXML("sparks@usenet.example", t2.Unix(), "g2-best@usenet.example"),
		g2Dup.ID:          sampleNZBXML("drones@usenet.example", t1.Unix(), "g2-dup@usenet.example"),
		g3DistinctSize.ID: sampleNZBXML("big@usenet.example", t1.Unix(), "g3-big@usenet.example"),
	}

	t.Run("healthy leader uses one grab and each group takes one failover slot", func(t *testing.T) {
		ix := &identityTestIndexer{
			results: []core.Release{g1FreshRepost, g1Best, g1SameDeadPost, g2Dup, g2Best, g3DistinctSize},
			nzbs:    nzbs,
		}
		health := &identityTestHealth{}
		svc := New(ix, health, nil, config.Defaults(""), Options{Now: func() time.Time { return now }})
		defer svc.Close()

		res, err := svc.Resolve(ctx, core.ResolveRequest{Title: testMovieTitle()})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if res.Release.ID != g1Best.ID {
			t.Fatalf("winner = %q, want best-ranked member %q", res.Release.ID, g1Best.ID)
		}
		if grabs := ix.grabIDs(); len(grabs) != 1 || grabs[0] != g1Best.ID {
			t.Fatalf("expected exactly 1 health-check grab for %q, got %v", g1Best.ID, grabs)
		}
		// Alternates must have 1 slot for Group 3 and 1 slot for Group 2 — no duplicates from Group 1 or Group 2.
		if len(res.Alternates) != 2 {
			t.Fatalf("Alternates = %v (len %d), want 2 (one per other group)", res.Alternates, len(res.Alternates))
		}
		for _, alt := range res.Alternates {
			if alt.ID == g1FreshRepost.ID || alt.ID == g1SameDeadPost.ID || alt.ID == g2Dup.ID {
				t.Fatalf("unexpected duplicate group member %q in Alternates: %v", alt.ID, res.Alternates)
			}
		}
	})

	t.Run("dead leader skips same-content copy and promotes healthy quiet fallback", func(t *testing.T) {
		ix := &identityTestIndexer{
			results: []core.Release{g1Best, g1SameDeadPost, g1FreshRepost, g2Best},
			nzbs:    nzbs,
		}
		health := &identityTestHealth{
			verdict: map[string]error{
				g1Best.ID:         core.ErrArticleMissing,
				g1SameDeadPost.ID: core.ErrArticleMissing,
				g1FreshRepost.ID:  nil,
			},
		}
		svc := New(ix, health, nil, config.Defaults(""), Options{Now: func() time.Time { return now }})
		defer svc.Close()

		res, err := svc.Resolve(ctx, core.ResolveRequest{Title: testMovieTitle()})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if res.Release.ID != g1FreshRepost.ID {
			t.Fatalf("expected quiet fallback %q to win after leader died, got %q", g1FreshRepost.ID, res.Release.ID)
		}
		for _, grabbed := range ix.grabIDs() {
			if grabbed == g1SameDeadPost.ID {
				t.Fatalf("same dead upload %q should have been skipped via content identity, grabs=%v", g1SameDeadPost.ID, ix.grabIDs())
			}
		}
	})

	t.Run("transient NZB error is not persisted as dead post", func(t *testing.T) {
		transientErr := errors.New("NZB unavailable")
		ix := &identityTestIndexer{
			results: []core.Release{g1Best},
			fetchFn: func(core.Release) ([]byte, error) { return nil, transientErr },
		}
		svc := New(ix, &identityTestHealth{}, nil, config.Defaults(""), Options{Now: func() time.Time { return now }})
		defer svc.Close()

		if _, err := svc.Resolve(ctx, core.ResolveRequest{Title: testMovieTitle()}); err == nil {
			t.Fatal("expected error from transient NZB failure")
		}
		if svc.recentlyFailedRelease(g1Best) {
			t.Fatal("transient NZB error must not mark release in failed-grab memory")
		}
	})
}
