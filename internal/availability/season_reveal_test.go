package availability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
)

type selectiveValidator struct {
	r      *resolver.Service
	verify func(ep core.Title, r core.Release) error
}

func (v selectiveValidator) FeedCandidate(ctx context.Context, t *core.Title, r core.Release) (core.Release, string, error) {
	return v.r.FeedCandidate(ctx, t, r)
}

func (v selectiveValidator) VerifyFeedCandidate(_ context.Context, t *core.Title, r core.Release) error {
	if v.verify == nil || t == nil {
		return core.ErrArticleMissing
	}
	return v.verify(*t, r)
}

func TestRevealSeason(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

	cfg := config.Defaults(t.TempDir())
	// Monitoring and feed verification are disabled by default; season reveal
	// still operates using the configured playback providers.
	cfg.Availability.Monitor = false
	cfg.Availability.Verify = false
	cfg.Availability.Debrid = false
	cfg.Debrid.Providers = []config.DebridProvider{{Enabled: true, APIKey: "fake"}}
	cfg.Usenet.Providers = []config.UsenetProvider{{Enabled: true, Host: "fake"}}

	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	res := resolver.New(nil, nil, nil, cfg, resolver.Options{})
	defer res.Close()

	seedShow := func(tmdbID int64, name string, eps []core.Title) core.RatingKey {
		t.Helper()
		showKey := core.ShowKey(tmdbID)
		show := core.Title{
			Key:      showKey,
			Type:     core.TypeShow,
			IDs:      core.IDs{TMDB: tmdbID},
			Title:    name,
			Year:     2026,
			Language: "en",
			Released: now.Add(-4 * 24 * time.Hour),
		}
		if err := cat.UpsertTitle(ctx, show, true); err != nil {
			t.Fatal(err)
		}
		seenSeasons := map[int]bool{}
		for _, ep := range eps {
			if !seenSeasons[ep.Season] {
				seenSeasons[ep.Season] = true
				season := core.Title{
					Key:       core.SeasonKey(tmdbID, ep.Season),
					Type:      core.TypeSeason,
					ShowKey:   showKey,
					ShowTitle: name,
					Season:    ep.Season,
					Title:     "Season",
					Year:      2026,
					Language:  "en",
					Released:  ep.Released,
				}
				if err := cat.UpsertTitle(ctx, season, true); err != nil {
					t.Fatal(err)
				}
			}
			ep.Key = core.EpisodeKey(tmdbID, ep.Season, ep.Episode)
			ep.Type = core.TypeEpisode
			ep.ShowKey = showKey
			ep.ShowTitle = name
			ep.ShowYear = 2026
			ep.Year = 2026
			ep.Language = "en"
			if err := cat.UpsertTitle(ctx, ep, true); err != nil {
				t.Fatal(err)
			}
		}
		return showKey
	}

	// Show 100 ("Cunk on Cinema"): S00 special + in-progress S01 (E01 aired,
	// E02/E03 dated in future, E04 undated, E05 not in TMDB).
	cunkShow := seedShow(100, "Cunk on Cinema", []core.Title{
		{Season: 0, Episode: 1, Title: "Special Preview", Released: now.Add(7 * 24 * time.Hour)},
		{Season: 1, Episode: 1, Title: "Episode 1", Released: now.Add(-4 * 24 * time.Hour)},
		{Season: 1, Episode: 2, Title: "Episode 2", Released: now.Add(3 * 24 * time.Hour)},
		{Season: 1, Episode: 3, Title: "Episode 3", Released: now.Add(10 * 24 * time.Hour)},
		{Season: 1, Episode: 4, Title: "Episode 4"},
	})
	// Show 200 ("Weekly Noise"): in-progress S01 with only unverified/incomplete hits.
	noiseShow := seedShow(200, "Weekly Noise", []core.Title{
		{Season: 1, Episode: 1, Title: "Part 1", Released: now.Add(-2 * 24 * time.Hour)},
		{Season: 1, Episode: 2, Title: "Part 2", Released: now.Add(5 * 24 * time.Hour)},
		{Season: 1, Episode: 3, Title: "Part 3", Released: now.Add(12 * 24 * time.Hour)},
	})
	// Show 300 ("Future Premiere"): unstarted S01 (all episodes in the future).
	unstartedShow := seedShow(300, "Future Premiere", []core.Title{
		{Season: 1, Episode: 1, Title: "Premiere", Released: now.Add(4 * 24 * time.Hour)},
		{Season: 1, Episode: 2, Title: "Second", Released: now.Add(11 * 24 * time.Hour)},
	})
	// Show 400 ("Finished Run"): completed S01 (all episodes already aired).
	completedShow := seedShow(400, "Finished Run", []core.Title{
		{Season: 1, Episode: 1, Title: "Start", Released: now.Add(-14 * 24 * time.Hour)},
		{Season: 1, Episode: 2, Title: "Finale", Released: now.Add(-7 * 24 * time.Hour)},
	})

	var (
		mu       sync.Mutex
		searches []core.SearchRequest
	)
	searchFn := func(sctx context.Context, req core.SearchRequest) ([]core.Release, error) {
		if !core.IsUnattendedSearch(sctx) {
			t.Errorf("season reveal search missing WithUnattendedSearch: %+v", req)
		}
		if req.Season <= 0 {
			t.Errorf("expected a season or episode search, got %+v", req)
		}
		mu.Lock()
		searches = append(searches, req)
		mu.Unlock()
		switch req.Title {
		case "Weekly Noise":
			return []core.Release{
				{ID: "noise-uncached", Name: "Weekly.Noise.S01E02.1080p.WEB-DL.x264", Protocol: core.ProtocolTorrent, InfoHash: "uncached02", Indexer: "Tor", Size: 2 << 30},
				{ID: "noise-partial-pack", Name: "Weekly.Noise.S01.1080p.WEB-DL.x264", Protocol: core.ProtocolTorrent, InfoHash: "partialpack", Indexer: "Tor", Size: 4 << 30},
				{ID: "noise-bad-nzb", Name: "Weekly.Noise.S01E03.1080p.WEB-DL.x264", Protocol: core.ProtocolUsenet, Indexer: "NZB", Size: 2 << 30},
			}, nil
		case "Cunk on Cinema":
			return []core.Release{
				{ID: "cunk-e02", Name: "Cunk.on.Cinema.S01E02.1080p.WEB-DL.x264", Protocol: core.ProtocolTorrent, InfoHash: "hashe02", Indexer: "Tor", Size: 2 << 30},
				{ID: "cunk-pack", Name: "Cunk.on.Cinema.S01.1080p.WEB-DL.x264", Protocol: core.ProtocolTorrent, InfoHash: "hashpack", Indexer: "Tor", Size: 8 << 30},
				{ID: "cunk-e04-nzb", Name: "Cunk.on.Cinema.S01E04.1080p.WEB-DL.x264", Protocol: core.ProtocolUsenet, Indexer: "NZB", Size: 2 << 30},
			}, nil
		default:
			return nil, nil
		}
	}

	cacheCheck := func(_ context.Context, hashes []string) (map[string]bool, bool) {
		out := map[string]bool{}
		for _, h := range hashes {
			if h == "hashe02" || h == "hashpack" || h == "partialpack" {
				out[h] = true
			}
		}
		return out, true
	}
	packFiles := func(_ context.Context, r core.Release) (func(*core.ReleaseTarget) bool, bool) {
		switch r.InfoHash {
		case "partialpack":
			return func(target *core.ReleaseTarget) bool {
				return target != nil && target.Season == 1 && target.Episode == 1
			}, true
		case "hashpack":
			// Pack contains E01, E02, E03, and unlisted E05 (not E04).
			return func(target *core.ReleaseTarget) bool {
				if target == nil || target.Season != 1 {
					return false
				}
				return target.Episode == 1 || target.Episode == 2 || target.Episode == 3 || target.Episode == 5
			}, true
		default:
			return nil, false
		}
	}
	val := selectiveValidator{
		r: res,
		verify: func(ep core.Title, r core.Release) error {
			if ep.Key == core.EpisodeKey(100, 1, 4) && r.ID == "cunk-e04-nzb" {
				return nil
			}
			return core.ErrArticleMissing
		},
	}

	newSvc := func() *Service {
		t.Helper()
		s, err := New(Deps{
			DB:         db,
			Config:     func() config.Config { return cfg },
			Feeds:      &fakeFeeds{},
			Catalog:    cat,
			Validator:  val,
			CacheCheck: cacheCheck,
			PackFiles:  packFiles,
			Search:     searchFn,
			Now:        func() time.Time { return now },
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	svc := newSvc()

	// 1. Specials, unstarted seasons, and completed seasons perform no searches.
	svc.RevealSeason(ctx, core.SeasonKey(100, 0))
	svc.RevealSeason(ctx, core.SeasonKey(unstartedShow.TMDB(), 1))
	svc.RevealSeason(ctx, core.SeasonKey(completedShow.TMDB(), 1))
	// 2. Unverified torrent, partial pack missing E02/E03, and failing NZB do not reveal.
	svc.RevealTitleSeason(noiseShow)
	// 3. Playback Observe on S01E01 triggers season reveal for Cunk on Cinema S01.
	ep1, err := cat.Get(ctx, core.EpisodeKey(100, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	svc.Observe(ctx, ep1, core.Release{
		ID: "cunk-e01", Name: "Cunk.on.Cinema.S01E01.1080p.WEB-DL.x264", Protocol: core.ProtocolTorrent, InfoHash: "hashe01", Indexer: "Tor", Size: 2 << 30,
	})
	svc.bg.Wait()
	svc.Close()

	// One season search per eligible season; still-hidden episodes then get
	// bounded per-episode lookups (Weekly Noise's unverified E02/E03).
	mu.Lock()
	seasonSearches, episodeSearches := 0, 0
	for _, req := range searches {
		if req.Episode == 0 {
			seasonSearches++
		} else {
			episodeSearches++
		}
	}
	mu.Unlock()
	if seasonSearches != 2 || episodeSearches > 2*seasonRevealEpisodeSearches {
		t.Fatalf("searches: %d season (want 2), %d per-episode", seasonSearches, episodeSearches)
	}

	noisePage, err := cat.SeasonEpisodePage(ctx, noiseShow, 1, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(noisePage.Items) != 1 || noisePage.Items[0].Episode != 1 {
		t.Fatalf("unverified hits revealed future Weekly Noise episodes: %+v", noisePage.Items)
	}

	cunkPage, err := cat.SeasonEpisodePage(ctx, cunkShow, 1, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(cunkPage.Items) != 4 {
		t.Fatalf("expected E01..E04 visible in Cunk S01, got %d (%+v)", len(cunkPage.Items), cunkPage.Items)
	}
	for i, wantEp := range []int{1, 2, 3, 4} {
		if cunkPage.Items[i].Episode != wantEp {
			t.Fatalf("item[%d] episode=%d want %d", i, cunkPage.Items[i].Episode, wantEp)
		}
	}
	if _, err := cat.Get(ctx, core.EpisodeKey(100, 1, 5)); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unlisted TMDB episode E05 was fabricated: err=%v", err)
	}

	// Verified releases are retained for Play/version selection on revealed episodes.
	svcCheck := newSvc()
	for _, epNum := range []int{2, 3, 4} {
		ep, err := cat.Get(ctx, core.EpisodeKey(100, 1, epNum))
		if err != nil {
			t.Fatal(err)
		}
		if cands := svcCheck.Candidates(ctx, ep); len(cands) == 0 {
			t.Fatalf("expected retained candidate for S01E0%d", epNum)
		}
	}

	mu.Lock()
	beforeCooldown := len(searches)
	mu.Unlock()
	// 4. Persisted 6h cooldown suppresses duplicate search across restart even if
	// TMDB later lists another unreleased episode (E06).
	ep6 := core.Title{
		Key: core.EpisodeKey(100, 1, 6), Type: core.TypeEpisode, ShowKey: cunkShow,
		ShowTitle: "Cunk on Cinema", ShowYear: 2026, Season: 1, Episode: 6,
		Title: "Episode 6", Year: 2026, Language: "en", Released: now.Add(21 * 24 * time.Hour),
	}
	if err := cat.UpsertTitle(ctx, ep6, true); err != nil {
		t.Fatal(err)
	}
	svcCheck.RevealTitleSeason(cunkShow)
	svcCheck.bg.Wait()
	svcCheck.Close()

	mu.Lock()
	afterCooldownHit := len(searches)
	mu.Unlock()
	if afterCooldownHit != beforeCooldown {
		t.Fatalf("expected persisted 6h cooldown to suppress search across restart, got %d searches", afterCooldownHit)
	}

	now = now.Add(6*time.Hour + time.Minute)
	svcAfter := newSvc()
	svcAfter.RevealTitleSeason(cunkShow)
	svcAfter.bg.Wait()
	svcAfter.Close()

	mu.Lock()
	afterExpiry := len(searches)
	mu.Unlock()
	if afterExpiry <= afterCooldownHit {
		t.Fatalf("expected search after 6h cooldown expiry, got %d searches", afterExpiry)
	}
}
