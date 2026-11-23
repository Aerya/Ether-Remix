package resolver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func metadataEpisode() *core.Title {
	return &core.Title{Key: core.EpisodeKey(456, 7, 3), Type: core.TypeEpisode, ShowTitle: "Los Simpson", ShowOriginalTitle: "The Simpsons", ShowYear: 1989,
		ShowIDs: core.IDs{TMDB: 456, IMDB: "tt0096697"}, Season: 7, Episode: 3, Title: "Episodio", Language: "en"}
}

func TestMetadataReleaseTitleIdentity(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ids, reported core.IDs
		err           error
		release, want string
	}{
		{"TMDB", core.IDs{TMDB: 456}, core.IDs{}, nil, "Los Simpsons - Temporada 7 [HDTV][Cap.703][Castellano][www.PCTRELOAD.COM]", ""},
		{"IMDb", core.IDs{IMDB: "tt0096697"}, core.IDs{}, nil, "Los Simpsons S07E03 HDTV Castellano", ""},
		{"different show", core.IDs{TMDB: 999}, core.IDs{}, nil, "Los Simpsons S07E03 HDTV Castellano", "title mismatch"},
		{"ambiguous", core.IDs{}, core.IDs{}, nil, "Los Simpsons S07E03 HDTV Castellano", "title mismatch"},
		{"unavailable", core.IDs{}, core.IDs{}, errors.New("offline"), "Los Simpsons S07E03 HDTV Castellano", "title mismatch"},
		{"metadata conflict", core.IDs{TMDB: 456, IMDB: "tt9999999"}, core.IDs{}, nil, "Los Simpsons S07E03 HDTV Castellano", "title mismatch"},
		{"reported conflict", core.IDs{TMDB: 456}, core.IDs{TMDB: 999}, nil, "Los Simpsons S07E03 HDTV Castellano", "indexer title identifier mismatch"},
		{"wrong episode", core.IDs{TMDB: 456}, core.IDs{}, nil, "Los Simpsons S07E04 HDTV Castellano", "season/episode mismatch"},
		{"wrong season", core.IDs{TMDB: 456}, core.IDs{}, nil, "Los Simpsons S08E03 HDTV Castellano", "season/episode mismatch"},
		{"wrong year", core.IDs{TMDB: 456}, core.IDs{}, nil, "Los Simpsons 2024 S07E03 HDTV Castellano", "series year mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{ReleaseTitleIDs: func(_ context.Context, kind core.MediaType, name string) (core.IDs, error) {
				if kind != core.TypeShow || name != "Los Simpsons" {
					t.Errorf("lookup %s %q", kind, name)
				}
				return tc.ids, tc.err
			}})
			defer s.Close()
			title := metadataEpisode()
			req := searchRequestFor(title)
			r := withParsed(rel("r", tc.release, 1500))
			r.TitleIDs = tc.reported
			s.prepareReleaseTitles(t.Context(), title, req, []core.Release{r})
			profile := SelectProfile(s.quality, webCaps(), nil)
			got := s.rejectReason(r, title, req, profile, s.resolverCfg(), webCaps(), nil, false)
			if (tc.want == "" && got != "") || (tc.want != "" && !strings.HasPrefix(got, tc.want)) {
				t.Fatalf("rejection=%q, want %q", got, tc.want)
			}
			if r.TitleIDs != tc.reported {
				t.Fatal("metadata overwrote reported IDs")
			}
		})
	}
}

func TestMetadataReleaseTitleCache(t *testing.T) {
	now := time.Unix(1800000000, 0)
	calls := 0
	s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{Now: func() time.Time { return now }, ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) {
		calls++
		return core.IDs{TMDB: 456}, nil
	}})
	defer s.Close()
	title := metadataEpisode()
	req := searchRequestFor(title)
	r := withParsed(rel("r", "Los Simpsons S07E03 HDTV Castellano", 1500))
	for range 2 {
		s.prepareReleaseTitles(t.Context(), title, req, []core.Release{r, r})
	}
	if calls != 1 || !s.metadataTitleMatches(r, req) {
		t.Fatalf("calls=%d verified=%v", calls, s.metadataTitleMatches(r, req))
	}
	other := req
	other.IDs = core.IDs{TMDB: 999}
	if s.metadataTitleMatches(r, other) {
		t.Fatal("cached mapping accepted for a different show")
	}
	now = now.Add(25 * time.Hour)
	if s.metadataTitleMatches(r, req) {
		t.Fatal("expired mapping accepted")
	}
	s.prepareReleaseTitles(t.Context(), title, req, []core.Release{r})
	if calls != 2 {
		t.Fatalf("expired lookup calls=%d", calls)
	}
	cfg := testCfg()
	cfg.Catalog.Language = "fr-FR"
	s.UpdateConfig(cfg)
	if s.metadataTitleMatches(r, req) {
		t.Fatal("cached mapping reused across languages")
	}
	s.prepareReleaseTitles(t.Context(), title, req, []core.Release{r})
	if calls != 3 {
		t.Fatalf("language lookup calls=%d", calls)
	}
	movie := core.SearchRequest{Type: core.TypeMovie, Title: "Unrelated", IDs: req.IDs}
	if s.metadataTitleMatches(r, movie) {
		t.Fatal("TV mapping reused in movie namespace")
	}
	s.prepareReleaseTitles(t.Context(), title, movie, []core.Release{r})
	if calls != 4 {
		t.Fatalf("movie namespace lookup calls=%d", calls)
	}
}

func TestMetadataReleaseTitleLookupBudgetAndKnownTitles(t *testing.T) {
	calls := 0
	s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) { calls++; return core.IDs{}, nil }})
	defer s.Close()
	title := metadataEpisode()
	title.ShowAlternativeTitles = []string{"Familia Simpson"}
	req := searchRequestFor(title)
	known := withParsed(rel("known", "Los Simpson S07E03 HDTV", 1500))
	alias := withParsed(rel("alias", "Familia Simpson S07E03 HDTV", 1500))
	verified := withParsed(rel("id", "Unfamiliar S07E03 HDTV", 1500))
	verified.TitleIDs = req.IDs
	conflict := verified
	conflict.TitleIDs = core.IDs{TMDB: 999}
	s.prepareReleaseTitles(t.Context(), title, req, []core.Release{known, alias, verified, conflict})
	if calls != 0 {
		t.Fatalf("known/conflicting results made %d lookups", calls)
	}
	var results []core.Release
	for _, name := range []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot"} {
		results = append(results, withParsed(rel(name, name+" S07E03 HDTV", 1500)))
	}
	s.prepareReleaseTitles(t.Context(), title, req, results)
	if calls != 4 {
		t.Fatalf("lookup budget=%d, want 4", calls)
	}
}

func TestMetadataReleaseTitleConcurrentLookups(t *testing.T) {
	var calls atomic.Int32
	started, finish := make(chan struct{}), make(chan struct{})
	s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{ReleaseTitleIDs: func(ctx context.Context, _ core.MediaType, _ string) (core.IDs, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-finish:
			return core.IDs{TMDB: 456}, nil
		case <-ctx.Done():
			return core.IDs{}, ctx.Err()
		}
	}})
	defer s.Close()
	title := metadataEpisode()
	req := searchRequestFor(title)
	r := withParsed(rel("r", "Los Simpsons S07E03 HDTV", 1500))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { s.prepareReleaseTitles(t.Context(), title, req, []core.Release{r}) })
	}
	<-started
	close(finish)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent calls=%d", calls.Load())
	}
}

func TestMetadataReleaseTitleRanking(t *testing.T) {
	cfg := testCfg()
	s := New(&fakeIndexer{}, nil, nil, cfg, Options{ReleaseTitleIDs: func(_ context.Context, kind core.MediaType, name string) (core.IDs, error) {
		if kind != core.TypeMovie || name != "Los Viajeros" {
			t.Errorf("lookup %s %q", kind, name)
		}
		return core.IDs{TMDB: 123}, nil
	}})
	defer s.Close()
	title := &core.Title{Key: core.MovieKey(123), Type: core.TypeMovie, Title: "The Travelers", IDs: core.IDs{TMDB: 123}, Year: 2016, Language: "en", Runtime: 90 * time.Minute}
	r := withParsed(rel("r", "Los Viajeros 2016 1080p WEB-DL H264 AAC", 3000))
	profile := SelectProfile(cfg.Quality, webCaps(), nil)
	candidates, _, err := s.rankCandidates(t.Context(), core.ResolveRequest{Title: title, Client: webCaps()}, searchRequestFor(title), profile, []core.Release{r}, nil, "")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates=%d err=%v", len(candidates), err)
	}
	if candidates[0].rel.TitleIDs != (core.IDs{}) {
		t.Fatal("metadata became reported IDs")
	}
}

func TestMetadataReleaseTitlePreservesRegion(t *testing.T) {
	s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) { return core.IDs{TMDB: 456}, nil }})
	defer s.Close()
	title := metadataEpisode()
	title.Countries = []string{"US"}
	req := searchRequestFor(title)
	r := withParsed(rel("r", "Los Simpsons UK S07E03 HDTV", 1500))
	s.prepareReleaseTitles(t.Context(), title, req, []core.Release{r})
	profile := SelectProfile(s.quality, webCaps(), nil)
	if got := s.rejectReason(r, title, req, profile, s.resolverCfg(), webCaps(), nil, false); !strings.HasPrefix(got, "title mismatch") {
		t.Fatalf("regional conflict accepted: %q", got)
	}
}

func TestMetadataReleaseTitleCacheMissAndErrors(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			now := time.Unix(1800000000, 0)
			calls := 0
			s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{Now: func() time.Time { return now }, ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) {
				calls++
				if fail {
					return core.IDs{}, errors.New("offline")
				}
				return core.IDs{}, nil
			}})
			defer s.Close()
			title := metadataEpisode()
			req := searchRequestFor(title)
			r := withParsed(rel("r", "Los Simpsons S07E03 HDTV", 1500))
			for range 2 {
				s.prepareReleaseTitles(t.Context(), title, req, []core.Release{r})
			}
			want := 1
			if fail {
				want = 2
			}
			if calls != want {
				t.Fatalf("calls=%d want=%d", calls, want)
			}
			now = now.Add(11 * time.Minute)
			s.prepareReleaseTitles(t.Context(), title, req, []core.Release{r})
			if calls != want+1 {
				t.Fatalf("negative cache did not expire: calls=%d", calls)
			}
		})
	}
}

func TestMetadataReleaseTitleCacheCapacity(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{Now: func() time.Time { return now }, ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) { return core.IDs{TMDB: 456}, nil }})
	defer s.Close()
	s.releaseTitles.seen = map[releaseTitleKey]releaseTitleVerdict{}
	for i := range maxReleaseTitleEntries {
		s.releaseTitles.seen[releaseTitleKey{title: fmt.Sprint(i)}] = releaseTitleVerdict{expires: now.Add(time.Hour)}
	}
	title := metadataEpisode()
	req := searchRequestFor(title)
	r := withParsed(rel("r", "Los Simpsons S07E03 HDTV", 1500))
	s.prepareReleaseTitles(t.Context(), title, req, []core.Release{r})
	if len(s.releaseTitles.seen) != maxReleaseTitleEntries || !s.metadataTitleMatches(r, req) {
		t.Fatalf("cache size=%d verified=%v", len(s.releaseTitles.seen), s.metadataTitleMatches(r, req))
	}
}

func TestMetadataReleaseTitleCancellation(t *testing.T) {
	calls := 0
	s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{ReleaseTitleIDs: func(ctx context.Context, _ core.MediaType, _ string) (core.IDs, error) {
		calls++
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 3*time.Second {
			t.Error("lookup is not bounded to three seconds")
		}
		return core.IDs{TMDB: 456}, nil
	}})
	defer s.Close()
	title := metadataEpisode()
	req := searchRequestFor(title)
	r := withParsed(rel("r", "Los Simpsons S07E03 HDTV", 1500))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s.prepareReleaseTitles(ctx, title, req, []core.Release{r})
	if calls != 0 || s.metadataTitleMatches(r, req) {
		t.Fatal("canceled lookup cached identity")
	}
	s.prepareReleaseTitles(t.Context(), title, req, []core.Release{r})
	if calls != 1 || !s.metadataTitleMatches(r, req) {
		t.Fatal("live lookup did not recover")
	}
}

type testSyncCallKey struct{}

func TestMetadataReleaseTitleNormalRankingMakesZeroTMDBCalls(t *testing.T) {
	t.Run("normal ranking pass makes zero calls when another candidate passes", func(t *testing.T) {
		var calls atomic.Int32
		cfg := testCfg()
		s := New(&fakeIndexer{}, nil, nil, cfg, Options{ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) {
			calls.Add(1)
			return core.IDs{TMDB: 456}, nil
		}})
		defer s.Close()

		title := metadataEpisode()
		sreq := searchRequestFor(title)
		good := withParsed(rel("good", "Los Simpson S07E03 1080p WEB-DL H264 AAC", 1500))
		alias := withParsed(rel("alias", "Los Simpsons S07E03 1080p WEB-DL H264 AAC", 1500))
		profile := SelectProfile(cfg.Quality, webCaps(), nil)
		req := core.ResolveRequest{Title: title, Client: webCaps()}

		cands, _, err := s.rankCandidatesWith(t.Context(), req, sreq, profile, []core.Release{good, alias}, nil, "", true, false)
		if err != nil {
			t.Fatalf("rankCandidatesWith: %v", err)
		}
		if len(cands) != 1 || cands[0].rel.ID != "good" {
			t.Fatalf("candidates=%v, want [good]", cands)
		}
		if calls.Load() != 0 {
			t.Fatalf("normal ranking made %d TMDB calls, want 0", calls.Load())
		}
	})

	t.Run("rankCandidates and Resolve never wait on TMDB when another candidate passes", func(t *testing.T) {
		var syncCalls atomic.Int32
		gate := make(chan struct{})
		entered := make(chan struct{}, 1)
		cfg := testCfg()
		good := withParsed(rel("good", "Los Simpson S07E03 1080p WEB-DL H264 AAC", 1500))
		alias := withParsed(rel("alias", "Los Simpsons S07E03 1080p WEB-DL H264 AAC", 1500))
		s := New(&fakeIndexer{releases: []core.Release{good, alias}}, fakeHealth{}, nil, cfg, Options{
			ReleaseTitleIDs: func(ctx context.Context, _ core.MediaType, _ string) (core.IDs, error) {
				if ctx.Value(testSyncCallKey{}) != nil {
					syncCalls.Add(1)
				}
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-gate:
					return core.IDs{TMDB: 456, IMDB: "tt0096697"}, nil
				case <-ctx.Done():
					return core.IDs{}, ctx.Err()
				}
			},
		})
		defer func() {
			select {
			case <-gate:
			default:
				close(gate)
			}
			_ = s.Close()
		}()

		title := metadataEpisode()
		sreq := searchRequestFor(title)
		profile := SelectProfile(cfg.Quality, webCaps(), nil)
		reqCtx := context.WithValue(t.Context(), testSyncCallKey{}, true)

		// Even with TMDB completely blocked on gate, rankCandidates returns immediately.
		cands, _, err := s.rankCandidates(reqCtx, core.ResolveRequest{Title: title, Client: webCaps()}, sreq, profile, []core.Release{good, alias}, nil, "")
		if err != nil {
			t.Fatalf("rankCandidates: %v", err)
		}
		if len(cands) != 1 || cands[0].rel.ID != "good" {
			t.Fatalf("candidates=%d, want good", len(cands))
		}
		if syncCalls.Load() != 0 {
			t.Fatalf("rankCandidates made %d synchronous TMDB calls, want 0", syncCalls.Load())
		}

		// Full Resolve also returns good immediately without waiting on TMDB.
		res, err := s.Resolve(reqCtx, core.ResolveRequest{Title: title, Client: webCaps()})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if res.Release.ID != "good" {
			t.Fatalf("winner=%q, want good", res.Release.ID)
		}
		if syncCalls.Load() != 0 {
			t.Fatalf("Resolve made %d synchronous TMDB calls, want 0", syncCalls.Load())
		}

		// Background warm was queued asynchronously; release it and drain.
		<-entered
		close(gate)
		if err := s.flushReleaseTitleWarm(t.Context()); err != nil {
			t.Fatalf("flushReleaseTitleWarm: %v", err)
		}
		if !s.metadataTitleMatches(alias, sreq) {
			t.Fatal("background warm did not cache alias")
		}
	})

	t.Run("zero TMDB calls when rejected releases also fail non-title checks", func(t *testing.T) {
		var calls atomic.Int32
		cfg := testCfg()
		s := New(&fakeIndexer{}, nil, nil, cfg, Options{ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) {
			calls.Add(1)
			return core.IDs{TMDB: 456}, nil
		}})
		defer s.Close()

		title := metadataEpisode()
		title.Countries = []string{"US"}
		sreq := searchRequestFor(title)
		good := withParsed(rel("good", "Los Simpson S07E03 1080p WEB-DL H264 AAC", 1500))
		wrongEp := withParsed(rel("ep", "Los Simpsons S07E04 1080p WEB-DL H264 AAC", 1500))
		wrongYear := withParsed(rel("yr", "Los Simpsons 2024 S07E03 1080p WEB-DL H264 AAC", 1500))
		region := withParsed(rel("reg", "Los Simpsons UK S07E03 1080p WEB-DL H264 AAC", 1500))
		conflict := withParsed(rel("id", "Los Simpsons S07E03 1080p WEB-DL H264 AAC", 1500))
		conflict.TitleIDs = core.IDs{TMDB: 999}

		profile := SelectProfile(cfg.Quality, webCaps(), nil)
		cands, _, err := s.rankCandidates(t.Context(), core.ResolveRequest{Title: title, Client: webCaps()}, sreq, profile, []core.Release{good, wrongEp, wrongYear, region, conflict}, nil, "")
		if err != nil {
			t.Fatalf("rankCandidates: %v", err)
		}
		if len(cands) != 1 || cands[0].rel.ID != "good" {
			t.Fatalf("candidates=%d, want 1", len(cands))
		}
		if err := s.flushReleaseTitleWarm(t.Context()); err != nil {
			t.Fatalf("flushReleaseTitleWarm: %v", err)
		}
		if calls.Load() != 0 {
			t.Fatalf("non-title rejections triggered %d TMDB calls, want 0", calls.Load())
		}
	})
}

func TestMetadataReleaseTitleLastResortOnlyWhenTitleMismatchAlone(t *testing.T) {
	t.Run("runs last resort when nothing passes and keeps trace 1-to-1", func(t *testing.T) {
		var calls atomic.Int32
		cfg := testCfg()
		s := New(&fakeIndexer{}, nil, nil, cfg, Options{ReleaseTitleIDs: func(_ context.Context, kind core.MediaType, name string) (core.IDs, error) {
			calls.Add(1)
			if kind != core.TypeShow || name != "Los Simpsons" {
				t.Errorf("unexpected lookup %s %q", kind, name)
			}
			return core.IDs{TMDB: 456, IMDB: "tt0096697"}, nil
		}})
		defer s.Close()

		title := metadataEpisode()
		sreq := searchRequestFor(title)
		wrongEp := withParsed(rel("wrong", "Los Simpsons S07E04 1080p HDTV", 1500))
		alias := withParsed(rel("alias", "Los Simpsons - Temporada 7 [HDTV][Cap.703][Castellano][www.PCTRELOAD.COM]", 1500))
		profile := SelectProfile(cfg.Quality, webCaps(), nil)
		var trace Trace
		cands, _, err := s.rankCandidates(t.Context(), core.ResolveRequest{Title: title, Client: webCaps()}, sreq, profile, []core.Release{wrongEp, alias}, &trace, "")
		if err != nil {
			t.Fatalf("rankCandidates: %v", err)
		}
		if len(cands) != 1 || cands[0].rel.ID != "alias" {
			t.Fatalf("candidates=%v, want [alias]", cands)
		}
		if calls.Load() != 1 {
			t.Fatalf("calls=%d, want 1", calls.Load())
		}
		if len(trace.Candidates) != 2 {
			t.Fatalf("trace rows=%d, want 2 (no duplicates after re-rank)", len(trace.Candidates))
		}
		if trace.Candidates[1].Rejected || trace.Candidates[1].ReleaseID != "alias" {
			t.Fatalf("alias trace=%+v, want accepted", trace.Candidates[1])
		}
	})

	t.Run("zero synchronous calls when nothing passes for non-title reasons", func(t *testing.T) {
		var syncCalls atomic.Int32
		cfg := testCfg()
		s := New(&fakeIndexer{}, nil, nil, cfg, Options{ReleaseTitleIDs: func(ctx context.Context, _ core.MediaType, _ string) (core.IDs, error) {
			if ctx.Value(testSyncCallKey{}) != nil {
				syncCalls.Add(1)
			}
			return core.IDs{TMDB: 456}, nil
		}})
		defer s.Close()

		title := metadataEpisode()
		title.Countries = []string{"US"}
		sreq := searchRequestFor(title)
		profile := SelectProfile(cfg.Quality, webCaps(), nil)
		profile.MaxHeight = 1080
		profile.AllowCam = false

		results := []core.Release{
			withParsed(rel("ep", "Los Simpsons S07E04 1080p HDTV", 1500)),
			withParsed(rel("yr", "Los Simpsons 2024 S07E03 1080p HDTV", 1500)),
			withParsed(rel("reg", "Los Simpsons UK S07E03 1080p HDTV", 1500)),
			withParsed(rel("cam", "Los Simpsons S07E03 1080p CAM", 1500)),
			withParsed(rel("4k", "Los Simpsons S07E03 2160p WEB-DL H264 AAC", 4000)),
		}
		reqCtx := context.WithValue(t.Context(), testSyncCallKey{}, true)
		cands, _, err := s.rankCandidates(reqCtx, core.ResolveRequest{Title: title, Client: webCaps()}, sreq, profile, results, nil, "")
		if err != nil {
			t.Fatalf("rankCandidates: %v", err)
		}
		if len(cands) != 0 {
			t.Fatalf("candidates=%d, want 0", len(cands))
		}
		if syncCalls.Load() != 0 {
			t.Fatalf("syncCalls=%d, want 0 when no release failed title mismatch alone for this play", syncCalls.Load())
		}
	})

	t.Run("last resort respects 4-lookup cap and stops once a match unlocks play", func(t *testing.T) {
		var syncCalls atomic.Int32
		cfg := testCfg()
		s := New(&fakeIndexer{}, nil, nil, cfg, Options{ReleaseTitleIDs: func(ctx context.Context, _ core.MediaType, name string) (core.IDs, error) {
			if ctx.Value(testSyncCallKey{}) != nil {
				syncCalls.Add(1)
			}
			if name == "Bravo" {
				return core.IDs{TMDB: 456}, nil
			}
			return core.IDs{}, nil
		}})
		defer s.Close()

		title := metadataEpisode()
		sreq := searchRequestFor(title)
		profile := SelectProfile(cfg.Quality, webCaps(), nil)
		reqCtx := context.WithValue(t.Context(), testSyncCallKey{}, true)

		var misses []core.Release
		for _, name := range []string{"One", "Two", "Three", "Four", "Five", "Six"} {
			misses = append(misses, withParsed(rel(name, name+" S07E03 1080p HDTV", 1500)))
		}
		cands, _, err := s.rankCandidates(reqCtx, core.ResolveRequest{Title: title, Client: webCaps()}, sreq, profile, misses, nil, "")
		if err != nil || len(cands) != 0 {
			t.Fatalf("cands=%d err=%v", len(cands), err)
		}
		if syncCalls.Load() != maxReleaseTitleLookups {
			t.Fatalf("syncCalls=%d, want %d", syncCalls.Load(), maxReleaseTitleLookups)
		}

		// Stopping on first match avoids spending the remaining sync budget.
		syncCalls.Store(0)
		mixed := []core.Release{
			withParsed(rel("a", "Alpha S07E03 1080p HDTV", 1500)),
			withParsed(rel("b", "Bravo S07E03 1080p HDTV", 1500)),
			withParsed(rel("c", "Charlie S07E03 1080p HDTV", 1500)),
			withParsed(rel("d", "Delta S07E03 1080p HDTV", 1500)),
		}
		cands, _, err = s.rankCandidates(reqCtx, core.ResolveRequest{Title: title, Client: webCaps()}, sreq, profile, mixed, nil, "")
		if err != nil || len(cands) != 1 || cands[0].rel.ID != "b" {
			t.Fatalf("cands=%v err=%v", cands, err)
		}
		if syncCalls.Load() != 2 {
			t.Fatalf("syncCalls=%d, want 2 (Alpha miss + Bravo match)", syncCalls.Load())
		}
	})

	t.Run("non-play callers never block on synchronous TMDB", func(t *testing.T) {
		var syncCalls atomic.Int32
		gate := make(chan struct{})
		cfg := testCfg()
		s := New(&fakeIndexer{}, nil, nil, cfg, Options{ReleaseTitleIDs: func(ctx context.Context, _ core.MediaType, _ string) (core.IDs, error) {
			if ctx.Value(testSyncCallKey{}) != nil {
				syncCalls.Add(1)
			}
			select {
			case <-gate:
				return core.IDs{TMDB: 456}, nil
			case <-ctx.Done():
				return core.IDs{}, ctx.Err()
			}
		}})
		defer func() {
			close(gate)
			_ = s.Close()
		}()

		title := metadataEpisode()
		sreq := searchRequestFor(title)
		alias := withParsed(rel("alias", "Los Simpsons S07E03 1080p WEB-DL H264 AAC", 1500))
		profile := SelectProfile(cfg.Quality, webCaps(), nil)
		baseCtx := context.WithValue(t.Context(), testSyncCallKey{}, true)

		specCtx, _ := core.WithSpeculative(baseCtx)
		contexts := []struct {
			name string
			ctx  context.Context
			caps core.ClientCaps
		}{
			{"local response", core.WithLocalResponse(baseCtx), webCaps()},
			{"speculative", specCtx, webCaps()},
			{"unattended", core.WithUnattendedSearch(baseCtx), webCaps()},
			{"detail purpose", core.WithIndexerPurpose(baseCtx, core.IndexerPurposeDetail), webCaps()},
			{"availability purpose", core.WithIndexerPurpose(baseCtx, core.IndexerPurposeAvailability), webCaps()},
			{"broadcast channel", baseCtx, core.ClientCaps{BroadcastBitrateKbps: 8000}},
		}
		for _, tc := range contexts {
			cands, _, err := s.rankCandidates(tc.ctx, core.ResolveRequest{Title: title, Client: tc.caps}, sreq, profile, []core.Release{alias}, nil, "")
			if err != nil {
				t.Fatalf("%s: err=%v", tc.name, err)
			}
			if len(cands) != 0 {
				t.Fatalf("%s: expected 0 candidates while TMDB gated, got %d", tc.name, len(cands))
			}
		}
		if _, reason, err := s.FeedCandidate(baseCtx, title, alias); err != nil || !strings.HasPrefix(reason, "title mismatch") {
			t.Fatalf("FeedCandidate: reason=%q err=%v", reason, err)
		}
		if syncCalls.Load() != 0 {
			t.Fatalf("non-play callers made %d synchronous TMDB calls, want 0", syncCalls.Load())
		}
	})
}

func TestMetadataReleaseTitleBackgroundWarmPersistsAcrossRestart(t *testing.T) {
	db := store.OpenTest(t)
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	cfg := testCfg()
	title := metadataEpisode()
	title.Countries = []string{"US"}
	sreq := searchRequestFor(title)

	good := withParsed(rel("good", "Los Simpson S07E03 720p WEB-DL H264 AAC", 1000))
	alias := withParsed(rel("alias", "Los Simpsons - Temporada 7 [HDTV][Cap.703][Castellano][www.PCTRELOAD.COM]", 1500))

	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	var warmCalls atomic.Int32
	s1 := New(&fakeIndexer{releases: []core.Release{good, alias}}, fakeHealth{}, db, cfg, Options{
		Now: clock.now,
		ReleaseTitleIDs: func(ctx context.Context, kind core.MediaType, name string) (core.IDs, error) {
			warmCalls.Add(1)
			if kind != core.TypeShow || name != "Los Simpsons" {
				t.Errorf("lookup %s %q", kind, name)
			}
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-gate:
				return core.IDs{TMDB: 456, IMDB: "tt0096697", TVDB: 71663}, nil
			case <-ctx.Done():
				return core.IDs{}, ctx.Err()
			}
		},
	})

	// First play takes the known candidate immediately while TMDB is blocked.
	res, err := s1.Resolve(t.Context(), core.ResolveRequest{Title: title, Client: webCaps()})
	if err != nil {
		t.Fatalf("cold Resolve: %v", err)
	}
	if res.Release.ID != "good" {
		t.Fatalf("cold winner=%q, want good", res.Release.ID)
	}
	<-entered
	close(gate)
	if err := s1.flushReleaseTitleWarm(t.Context()); err != nil {
		t.Fatalf("flushReleaseTitleWarm: %v", err)
	}
	if warmCalls.Load() != 1 {
		t.Fatalf("warmCalls=%d, want 1", warmCalls.Load())
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("s1.Close: %v", err)
	}

	// Simulate server restart: new Service on the same DB must accept locally
	// with zero TMDB calls while keeping every safety rule intact.
	s2 := New(&fakeIndexer{releases: []core.Release{alias}}, fakeHealth{}, db, cfg, Options{
		Now: clock.now,
		ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) {
			t.Fatal("unexpected TMDB call after restart; durable alias should match locally")
			return core.IDs{}, nil
		},
	})
	defer s2.Close()

	res2, err := s2.Resolve(t.Context(), core.ResolveRequest{Title: title, Client: webCaps()})
	if err != nil {
		t.Fatalf("warm Resolve after restart: %v", err)
	}
	if res2.Release.ID != "alias" {
		t.Fatalf("warm winner=%q, want alias", res2.Release.ID)
	}
	if !s2.RememberedReleaseAllowed(t.Context(), title, alias) {
		t.Fatal("RememberedReleaseAllowed rejected durably verified alias")
	}

	// All safety rules still hold against the persisted alias.
	profile := SelectProfile(cfg.Quality, webCaps(), nil)
	otherShow := *title
	otherShow.ShowIDs = core.IDs{TMDB: 999}
	if got := s2.rejectReason(alias, &otherShow, searchRequestFor(&otherShow), profile, s2.resolverCfg(), webCaps(), nil, false); !strings.HasPrefix(got, "title mismatch") {
		t.Fatalf("different show accepted persisted alias: %q", got)
	}
	conflict := alias
	conflict.TitleIDs = core.IDs{TMDB: 999}
	if got := s2.rejectReason(conflict, title, sreq, profile, s2.resolverCfg(), webCaps(), nil, false); got != "indexer title identifier mismatch" {
		t.Fatalf("conflicting indexer ID = %q, want indexer title identifier mismatch", got)
	}
	wrongEp := withParsed(rel("we", "Los Simpsons S07E04 HDTV", 1500))
	if got := s2.rejectReason(wrongEp, title, sreq, profile, s2.resolverCfg(), webCaps(), nil, false); got != "season/episode mismatch" {
		t.Fatalf("wrong episode = %q, want season/episode mismatch", got)
	}
	wrongYear := withParsed(rel("wy", "Los Simpsons 2024 S07E03 HDTV", 1500))
	if got := s2.rejectReason(wrongYear, title, sreq, profile, s2.resolverCfg(), webCaps(), nil, false); got != "series year mismatch" {
		t.Fatalf("wrong year = %q, want series year mismatch", got)
	}
	regionConflict := withParsed(rel("rc", "Los Simpsons UK S07E03 HDTV", 1500))
	if got := s2.rejectReason(regionConflict, title, sreq, profile, s2.resolverCfg(), webCaps(), nil, false); !strings.HasPrefix(got, "title mismatch") {
		t.Fatalf("region conflict = %q, want title mismatch", got)
	}
	movieReq := core.SearchRequest{Type: core.TypeMovie, Title: "Unrelated", IDs: sreq.IDs}
	if s2.metadataTitleMatches(alias, movieReq) {
		t.Fatal("persisted TV mapping reused in movie namespace")
	}
	frCfg := testCfg()
	frCfg.Catalog.Language = "fr-FR"
	s2.UpdateConfig(frCfg)
	if s2.metadataTitleMatches(alias, sreq) {
		t.Fatal("persisted mapping reused across catalogue languages")
	}

	// Expiry still applies across restarts.
	clock.add(25 * time.Hour)
	s3 := New(&fakeIndexer{}, nil, db, cfg, Options{Now: clock.now})
	defer s3.Close()
	if s3.metadataTitleMatches(alias, sreq) {
		t.Fatal("expired durable mapping loaded after restart")
	}
}

func TestMetadataReleaseTitleBackgroundWarmDedupRateLimitAndBounds(t *testing.T) {
	t.Run("deduplication and deterministic rate limiting", func(t *testing.T) {
		clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
		var calls atomic.Int32
		var waitMu sync.Mutex
		var waits []time.Duration

		s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{
			Now: clock.now,
			ReleaseTitleIDs: func(_ context.Context, _ core.MediaType, _ string) (core.IDs, error) {
				calls.Add(1)
				return core.IDs{TMDB: 456}, nil
			},
		})
		defer s.Close()
		s.releaseTitleWarmWait = func(_ context.Context, d time.Duration) error {
			waitMu.Lock()
			waits = append(waits, d)
			waitMu.Unlock()
			clock.add(d)
			return nil
		}

		title := metadataEpisode()
		sreq := searchRequestFor(title)
		alpha := withParsed(rel("a", "Alpha S07E03 1080p HDTV", 1500))
		bravo := withParsed(rel("b", "Bravo S07E03 1080p HDTV", 1500))
		charlie := withParsed(rel("c", "Charlie S07E03 1080p HDTV", 1500))

		// Queue duplicates repeatedly before flushing.
		for range 3 {
			s.queueReleaseTitleWarm(title, sreq, []core.Release{alpha, alpha, bravo, charlie})
		}
		if err := s.flushReleaseTitleWarm(t.Context()); err != nil {
			t.Fatalf("flushReleaseTitleWarm: %v", err)
		}
		if calls.Load() != 3 {
			t.Fatalf("calls=%d, want 3 (deduplicated)", calls.Load())
		}
		waitMu.Lock()
		gotWaits := append([]time.Duration(nil), waits...)
		waitMu.Unlock()
		if len(gotWaits) != 2 || gotWaits[0] != defaultReleaseTitleWarmInterval || gotWaits[1] != defaultReleaseTitleWarmInterval {
			t.Fatalf("rate-limit waits=%v, want two waits of %v", gotWaits, defaultReleaseTitleWarmInterval)
		}
	})

	t.Run("unaccepted mappings are not persisted to DB", func(t *testing.T) {
		db := store.OpenTest(t)
		clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
		s := New(&fakeIndexer{}, nil, db, testCfg(), Options{
			Now: clock.now,
			ReleaseTitleIDs: func(_ context.Context, _ core.MediaType, name string) (core.IDs, error) {
				if name == "Different Show" {
					return core.IDs{TMDB: 999}, nil
				}
				return core.IDs{}, nil
			},
		})
		defer s.Close()
		s.releaseTitleWarmWait = func(_ context.Context, d time.Duration) error { clock.add(d); return nil }

		title := metadataEpisode()
		sreq := searchRequestFor(title)
		s.queueReleaseTitleWarm(title, sreq, []core.Release{
			withParsed(rel("d", "Different Show S07E03 1080p HDTV", 1500)),
			withParsed(rel("a", "Ambiguous Show S07E03 1080p HDTV", 1500)),
		})
		if err := s.flushReleaseTitleWarm(t.Context()); err != nil {
			t.Fatalf("flushReleaseTitleWarm: %v", err)
		}
		var count int
		if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM resolver_release_titles`).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Fatalf("persisted rows=%d, want 0 for unaccepted mappings", count)
		}
	})

	t.Run("durable table is bounded and pruned", func(t *testing.T) {
		db := store.OpenTest(t)
		clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
		s := New(&fakeIndexer{}, nil, db, testCfg(), Options{
			Now: clock.now,
			ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) {
				return core.IDs{TMDB: 456}, nil
			},
		})
		defer s.Close()
		s.releaseTitleMaxEntries = 3

		title := metadataEpisode()
		sreq := searchRequestFor(title)
		for _, name := range []string{"One", "Two", "Three", "Four", "Five"} {
			clock.add(time.Minute)
			r := withParsed(rel(name, name+" S07E03 1080p HDTV", 1500))
			s.prepareReleaseTitles(t.Context(), title, sreq, []core.Release{r})
		}
		var count int
		if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM resolver_release_titles`).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 3 {
			t.Fatalf("persisted rows=%d, want 3 (bounded)", count)
		}
		// Newest 3 remain after restart.
		s2 := New(&fakeIndexer{}, nil, db, testCfg(), Options{Now: clock.now})
		defer s2.Close()
		for _, kept := range []string{"Three", "Four", "Five"} {
			r := withParsed(rel(kept, kept+" S07E03 1080p HDTV", 1500))
			if !s2.metadataTitleMatches(r, sreq) {
				t.Fatalf("expected newest alias %q to survive DB cap", kept)
			}
		}
		// Prune removes expired rows from both memory and DB.
		clock.add(25 * time.Hour)
		if _, err := s2.Prune(t.Context()); err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM resolver_release_titles`).Scan(&count); err != nil {
			t.Fatalf("count after prune: %v", err)
		}
		if count != 0 {
			t.Fatalf("persisted rows after prune=%d, want 0", count)
		}
	})

	t.Run("background error backoff does not block live last-resort retry", func(t *testing.T) {
		clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
		var calls atomic.Int32
		fail := atomic.Bool{}
		fail.Store(true)
		s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{
			Now: clock.now,
			ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) {
				calls.Add(1)
				if fail.Load() {
					return core.IDs{}, errors.New("tmdb unavailable")
				}
				return core.IDs{TMDB: 456}, nil
			},
		})
		defer s.Close()
		s.releaseTitleWarmWait = func(_ context.Context, d time.Duration) error { clock.add(d); return nil }

		title := metadataEpisode()
		sreq := searchRequestFor(title)
		r := withParsed(rel("r", "Los Simpsons S07E03 1080p HDTV", 1500))

		s.queueReleaseTitleWarm(title, sreq, []core.Release{r})
		if err := s.flushReleaseTitleWarm(t.Context()); err != nil {
			t.Fatalf("flush 1: %v", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("calls=%d, want 1", calls.Load())
		}
		// Second background enqueue within backoff window is suppressed.
		s.queueReleaseTitleWarm(title, sreq, []core.Release{r})
		if err := s.flushReleaseTitleWarm(t.Context()); err != nil {
			t.Fatalf("flush 2: %v", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("backed-off warm made %d calls, want 1", calls.Load())
		}
		// Live last-resort still recovers immediately when TMDB comes back.
		fail.Store(false)
		s.prepareReleaseTitles(t.Context(), title, sreq, []core.Release{r})
		if calls.Load() != 2 || !s.metadataTitleMatches(r, sreq) {
			t.Fatalf("live last-resort did not recover: calls=%d matched=%v", calls.Load(), s.metadataTitleMatches(r, sreq))
		}
	})

	t.Run("Close cancels and joins in-flight background warm", func(t *testing.T) {
		started := make(chan struct{})
		s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{
			ReleaseTitleIDs: func(ctx context.Context, _ core.MediaType, _ string) (core.IDs, error) {
				close(started)
				<-ctx.Done()
				return core.IDs{}, ctx.Err()
			},
		})
		title := metadataEpisode()
		sreq := searchRequestFor(title)
		r := withParsed(rel("r", "Los Simpsons S07E03 1080p HDTV", 1500))
		s.queueReleaseTitleWarm(title, sreq, []core.Release{r})
		<-started
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
}

func TestMetadataReleaseTitleRefillWarmsOnDetailOpen(t *testing.T) {
	db := store.OpenTest(t)
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	alias := withParsed(rel("alias", "Los Simpsons S07E03 1080p WEB-DL H264 AAC", 1500))
	var calls atomic.Int32

	s := New(&fakeIndexer{releases: []core.Release{alias}}, fakeHealth{}, db, testCfg(), Options{
		Now: clock.now,
		ReleaseTitleIDs: func(context.Context, core.MediaType, string) (core.IDs, error) {
			calls.Add(1)
			return core.IDs{TMDB: 456, IMDB: "tt0096697"}, nil
		},
	})
	defer s.Close()

	title := metadataEpisode()
	done := s.RefillSearchCache(title)
	if done == nil {
		t.Fatal("RefillSearchCache returned nil channel")
	}
	<-done
	if err := s.flushReleaseTitleWarm(t.Context()); err != nil {
		t.Fatalf("flushReleaseTitleWarm: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("warm calls=%d, want 1", calls.Load())
	}
	// Subsequent Play hits the local cache with zero extra TMDB calls.
	res, err := s.Resolve(t.Context(), core.ResolveRequest{Title: title, Client: webCaps()})
	if err != nil || res.Release.ID != "alias" {
		t.Fatalf("Resolve=%v err=%v", res, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("Play after detail warm made extra TMDB calls: total=%d", calls.Load())
	}
}
