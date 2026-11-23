package resolver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/animemap"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

func testAnimeMap(t *testing.T, hits *atomic.Int64) *animemap.Service {
	t.Helper()
	const body = `[
		{"type":"TV","anilist_id":1,"anidb_id":23,"themoviedb_id":{"tv":30991},"season":{"tmdb":1}},
		{"type":"MOVIE","anilist_id":5,"themoviedb_id":{"movie":[112233]}}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return animemap.New(animemap.Options{
		URL:    srv.URL,
		Client: srv.Client(),
	})
}

func testAnimeEpisode() *core.Title {
	return &core.Title{
		Key:       core.EpisodeKey(30991, 1, 1),
		ShowKey:   core.ShowKey(30991),
		Type:      core.TypeEpisode,
		Title:     "Asteroid Blues",
		ShowTitle: "Cowboy Bebop",
		ShowIDs:   core.IDs{TMDB: 30991},
		Season:    1,
		Episode:   1,
		Language:  "ja",
		Genres:    []string{"Animation", "Action"},
		Keywords:  []string{"anime"},
		Runtime:   24 * time.Minute,
	}
}

func makeAnimeRelease(id, name, hash string, size int64) core.Release {
	info := releaseparse.Parse(name)
	proto := core.ProtocolUsenet
	if hash != "" {
		proto = core.ProtocolTorrent
	}
	return core.Release{
		ID:       id,
		GUID:     id,
		Indexer:  "nyaa",
		Name:     name,
		Protocol: proto,
		NZBURL:   "https://idx.invalid/" + id + ".nzb",
		InfoHash: hash,
		Size:     size,
		Info:     info,
	}
}

func TestSeaDexColdPlayNeverBlocksAndRanksBestOverAlt(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	animeMap := testAnimeMap(t, nil)

	const (
		bestHash = "db5f77e1b99a26dcbbdd49ef1ab931bb87ab9e01"
		altHash  = "aa1177e1b99a26dcbbdd49ef1ab931bb87ab9e02"
	)
	entered := make(chan struct{}, 1)
	releaseGate := make(chan struct{})

	seaDexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-releaseGate
		_, _ = fmt.Fprintf(w, `{
			"totalItems": 1,
			"items": [{
				"alID": 1,
				"expand": {
					"trs": [
						{"infoHash": "<redacted>", "releaseGroup": "JySzE", "isBest": true},
						{"infoHash": %q, "releaseGroup": "BestHashOnly", "isBest": true},
						{"infoHash": %q, "releaseGroup": "JySzE", "isBest": false},
						{"infoHash": "cc3377e1b99a26dcbbdd49ef1ab931bb87ab9e03", "releaseGroup": "SCY", "isBest": false}
					]
				}
			}]
		}`, bestHash, altHash)
	}))
	t.Cleanup(seaDexSrv.Close)

	// Uncurated has a larger size so it wins tie-breaks before SeaDex loads.
	uncurated := makeAnimeRelease("uncurated", "[RandomGrp] Cowboy Bebop - S01E01 [1080p BluRay x264].mkv", "", 1_800<<20)
	altByGroup := makeAnimeRelease("alt-grp", "[SCY] Cowboy Bebop - S01E01 [1080p BluRay x264].mkv", "", 1_400<<20)
	altOverride := makeAnimeRelease("alt-hash", "[JySzE] Cowboy Bebop - S01E01 [1080p BluRay x264].mkv", altHash, 1_500<<20)
	bestByGroup := makeAnimeRelease("best-grp", "[JySzE] Cowboy Bebop - S01E01 [1080p BluRay x264].mkv", "", 1_200<<20)
	bestByHash := makeAnimeRelease("best-hash", "[UnlistedGrp] Cowboy Bebop - S01E01 [1080p BluRay x264].mkv", bestHash, 1_100<<20)

	ix := &fakeIndexer{releases: []core.Release{uncurated, altByGroup, altOverride, bestByGroup, bestByHash}}
	cfg := config.Defaults("")
	svc := New(ix, fakeHealth{}, store.OpenTest(t), cfg, Options{
		Now:          clock.now,
		AnimeIDs:     animeMap,
		SeaDexURL:    seaDexSrv.URL,
		SeaDexClient: seaDexSrv.Client(),
	})
	t.Cleanup(func() { _ = svc.Close() })

	anime := testAnimeEpisode()
	caps := core.ClientCaps{VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "flac"}}

	// Cold play must return immediately even while releaseGate blocks SeaDex HTTP.
	res, err := svc.Resolve(ctx, core.ResolveRequest{Title: anime, Client: caps})
	if err != nil {
		t.Fatalf("cold Resolve: %v", err)
	}
	if res.Release.ID != "uncurated" {
		t.Fatalf("cold winner = %q, want uncurated before SeaDex loads", res.Release.ID)
	}

	// Background fetch was triggered by the play; unblock it and drain.
	<-entered
	close(releaseGate)
	if err := svc.flushSeaDex(ctx); err != nil {
		t.Fatalf("flushSeaDex: %v", err)
	}

	// Exact best hash beats best group, alt, and uncurated.
	res, err = svc.Resolve(ctx, core.ResolveRequest{Title: anime, Client: caps})
	if err != nil {
		t.Fatalf("warm Resolve (best hash): %v", err)
	}
	if res.Release.ID != "best-hash" {
		t.Fatalf("warm winner = %q, want best-hash", res.Release.ID)
	}

	// Excluding best-hash promotes best-grp (matched from redacted-hash JySzE entry).
	res, err = svc.Resolve(ctx, core.ResolveRequest{Title: anime, Client: caps, Exclude: []string{"best-hash"}})
	if err != nil {
		t.Fatalf("warm Resolve (best group): %v", err)
	}
	if res.Release.ID != "best-grp" {
		t.Fatalf("winner without best-hash = %q, want best-grp", res.Release.ID)
	}

	// Excluding both best releases promotes alt-hash (exact alt hash overrides JySzE best group,
	// and ranks above alt-grp and uncurated).
	res, err = svc.Resolve(ctx, core.ResolveRequest{Title: anime, Client: caps, Exclude: []string{"best-hash", "best-grp"}})
	if err != nil {
		t.Fatalf("warm Resolve (alt hash): %v", err)
	}
	if res.Release.ID != "alt-hash" {
		t.Fatalf("winner without best releases = %q, want alt-hash", res.Release.ID)
	}

	// Excluding alt-hash promotes alt-grp ahead of uncurated.
	res, err = svc.Resolve(ctx, core.ResolveRequest{Title: anime, Client: caps, Exclude: []string{"best-hash", "best-grp", "alt-hash"}})
	if err != nil {
		t.Fatalf("warm Resolve (alt group): %v", err)
	}
	if res.Release.ID != "alt-grp" {
		t.Fatalf("winner without alt-hash = %q, want alt-grp", res.Release.ID)
	}
}

func TestSeaDexDurableCacheAndStaleWhileRevalidate(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	db := store.OpenTest(t)

	var phase atomic.Int32
	var seaDexHits atomic.Int64
	refreshEntered := make(chan struct{}, 1)
	refreshGate := make(chan struct{})

	seaDexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seaDexHits.Add(1)
		if phase.Load() == 1 {
			select {
			case refreshEntered <- struct{}{}:
			default:
			}
			<-refreshGate
			_, _ = w.Write([]byte(`{"items":[{"alID":1,"expand":{"trs":[{"infoHash":"<redacted>","releaseGroup":"Vodes","isBest":true}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"alID":1,"expand":{"trs":[{"infoHash":"<redacted>","releaseGroup":"JySzE","isBest":true}]}}]}`))
	}))
	t.Cleanup(seaDexSrv.Close)

	uncurated := makeAnimeRelease("uncurated", "[Other] Cowboy Bebop - S01E01 [1080p BluRay x264].mkv", "", 1_800<<20)
	jysze := makeAnimeRelease("jysze", "[JySzE] Cowboy Bebop - S01E01 [1080p BluRay x264].mkv", "", 1_200<<20)
	vodes := makeAnimeRelease("vodes", "[Vodes] Cowboy Bebop - S01E01 [1080p BluRay x264].mkv", "", 1_100<<20)
	ix := &fakeIndexer{releases: []core.Release{uncurated, jysze, vodes}}
	cfg := config.Defaults("")
	caps := core.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}
	anime := testAnimeEpisode()

	svc1 := New(ix, fakeHealth{}, db, cfg, Options{
		Now:          clock.now,
		AnimeIDs:     testAnimeMap(t, nil),
		SeaDexURL:    seaDexSrv.URL,
		SeaDexClient: seaDexSrv.Client(),
		SeaDexTTL:    24 * time.Hour,
	})
	if _, err := svc1.Resolve(ctx, core.ResolveRequest{Title: anime, Client: caps}); err != nil {
		t.Fatalf("svc1 initial Resolve: %v", err)
	}
	if err := svc1.flushSeaDex(ctx); err != nil {
		t.Fatalf("svc1 flushSeaDex: %v", err)
	}
	_ = svc1.Close()

	// Restart on the same SQLite DB with a cold animemap: the persisted
	// lookup_key -> anilist_id and SeaDex entry apply immediately with zero HTTP hits.
	var coldMapHits atomic.Int64
	hitsBeforeRestart := seaDexHits.Load()
	svc2 := New(ix, fakeHealth{}, db, cfg, Options{
		Now:          clock.now,
		AnimeIDs:     testAnimeMap(t, &coldMapHits),
		SeaDexURL:    seaDexSrv.URL,
		SeaDexClient: seaDexSrv.Client(),
		SeaDexTTL:    24 * time.Hour,
	})
	t.Cleanup(func() { _ = svc2.Close() })

	res, err := svc2.Resolve(ctx, core.ResolveRequest{Title: anime, Client: caps})
	if err != nil {
		t.Fatalf("svc2 warm DB Resolve: %v", err)
	}
	if res.Release.ID != "jysze" {
		t.Fatalf("svc2 warm DB winner = %q, want jysze", res.Release.ID)
	}
	if seaDexHits.Load() != hitsBeforeRestart || coldMapHits.Load() != 0 {
		t.Fatalf("restart unexpectedly hit network: seadex=%d (was %d) animemap=%d", seaDexHits.Load(), hitsBeforeRestart, coldMapHits.Load())
	}

	// Expire TTL: play still serves stale jysze without blocking on refreshGate,
	// then background refresh updates curation to vodes.
	phase.Store(1)
	clock.add(25 * time.Hour)

	res, err = svc2.Resolve(ctx, core.ResolveRequest{Title: anime, Client: caps})
	if err != nil {
		t.Fatalf("svc2 stale Resolve: %v", err)
	}
	if res.Release.ID != "jysze" {
		t.Fatalf("stale winner = %q, want stale jysze while refreshing", res.Release.ID)
	}

	<-refreshEntered
	close(refreshGate)
	if err := svc2.flushSeaDex(ctx); err != nil {
		t.Fatalf("svc2 refresh flushSeaDex: %v", err)
	}

	res, err = svc2.Resolve(ctx, core.ResolveRequest{Title: anime, Client: caps})
	if err != nil {
		t.Fatalf("svc2 post-refresh Resolve: %v", err)
	}
	if res.Release.ID != "vodes" {
		t.Fatalf("refreshed winner = %q, want vodes", res.Release.ID)
	}
}

func TestSeaDexOnlyFetchesForPlayedOrOpenedTitles(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}

	var seaDexHits atomic.Int64
	seaDexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seaDexHits.Add(1)
		_, _ = w.Write([]byte(`{"items":[{"alID":1,"expand":{"trs":[{"infoHash":"<redacted>","releaseGroup":"JySzE","isBest":true}]}}]}`))
	}))
	t.Cleanup(seaDexSrv.Close)

	uncurated := makeAnimeRelease("uncurated", "[Other] Cowboy Bebop - S01E01 [1080p BluRay x264].mkv", "", 1_800<<20)
	jysze := makeAnimeRelease("jysze", "[JySzE] Cowboy Bebop - S01E01 [1080p BluRay x264].mkv", "", 1_200<<20)
	ix := &fakeIndexer{releases: []core.Release{uncurated, jysze}}
	cfg := config.Defaults("")
	svc := New(ix, fakeHealth{}, store.OpenTest(t), cfg, Options{
		Now:          clock.now,
		AnimeIDs:     testAnimeMap(t, nil),
		SeaDexURL:    seaDexSrv.URL,
		SeaDexClient: seaDexSrv.Client(),
	})
	t.Cleanup(func() { _ = svc.Close() })

	anime := testAnimeEpisode()
	caps := core.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}

	// Unattended, background, warmup, feed, and live-channel probes must never fetch SeaDex.
	specCtx, _ := core.WithSpeculative(ctx)
	bgBudgetCtx := core.WithBackgroundBudget(ctx, func(context.Context, string, string, int64) error { return nil })
	for _, bgCtx := range []context.Context{
		bgBudgetCtx,
		core.WithUnattendedSearch(ctx),
		specCtx,
		core.WithIndexerPurpose(ctx, core.IndexerPurposeWarmup),
		core.WithIndexerPurpose(ctx, core.IndexerPurposeAvailability),
	} {
		_, _ = svc.Resolve(bgCtx, core.ResolveRequest{Title: anime, Client: caps})
	}
	if ch := svc.RefillWarmupSearchCache(anime); ch != nil {
		<-ch
	}
	_, _, _ = svc.FeedCandidate(ctx, anime, jysze)
	_, _ = svc.ReadyChannelRelease(ctx, core.ResolveRequest{
		Title:  anime,
		Client: core.ClientCaps{BroadcastBitrateKbps: 8000, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
	})
	// Non-anime playback must never fetch SeaDex either.
	nonAnime := &core.Title{
		Key:      core.MovieKey(999999),
		Type:     core.TypeMovie,
		Title:    "Heat",
		IDs:      core.IDs{TMDB: 999999},
		Year:     1995,
		Language: "en",
		Genres:   []string{"Crime", "Drama"},
		Runtime:  170 * time.Minute,
	}
	_, _ = svc.Resolve(ctx, core.ResolveRequest{Title: nonAnime, Client: caps})
	if err := svc.flushSeaDex(ctx); err != nil {
		t.Fatalf("flushSeaDex after unattended work: %v", err)
	}
	if seaDexHits.Load() != 0 {
		t.Fatalf("unattended or non-anime paths triggered %d SeaDex requests, want 0", seaDexHits.Load())
	}

	// Opening the anime title's detail page (RefillSearchCache) warms SeaDex in background.
	if ch := svc.RefillSearchCache(anime); ch != nil {
		<-ch
	}
	if err := svc.flushSeaDex(ctx); err != nil {
		t.Fatalf("flushSeaDex after detail open: %v", err)
	}
	if seaDexHits.Load() != 1 {
		t.Fatalf("detail open SeaDex hits = %d, want 1", seaDexHits.Load())
	}

	res, err := svc.Resolve(ctx, core.ResolveRequest{Title: anime, Client: caps})
	if err != nil {
		t.Fatalf("Resolve after detail open: %v", err)
	}
	if res.Release.ID != "jysze" {
		t.Fatalf("winner after detail open = %q, want jysze", res.Release.ID)
	}
}
