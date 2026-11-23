package resolver

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func iLiveAloneE674Title() *core.Title {
	return &core.Title{
		Key:                   core.EpisodeKey(65282, 1, 674),
		Type:                  core.TypeEpisode,
		ShowKey:               core.ShowKey(65282),
		ShowIDs:               core.IDs{TMDB: 65282, IMDB: "tt7997010", TVDB: 298704},
		ShowTitle:             "Home Alone",
		ShowOriginalTitle:     "나 혼자 산다",
		ShowEnglishTitle:      "Home Alone",
		ShowAlternativeTitles: []string{"I Live Alone", "Single Life"},
		ShowYear:              2013,
		Title:                 "Episode 674",
		Season:                1,
		Episode:               674,
		Released:              time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC),
		Language:              "ko",
		Runtime:               90 * time.Minute,
	}
}

func TestKoreanVarietyILiveAloneResolverDecision(t *testing.T) {
	ctx := context.Background()
	ep := iLiveAloneE674Title()
	cfg := config.Defaults("")

	// Namesake "Home Alone" (2017, English documentary) is present in the catalogue.
	namesakeIdentity := func(_ context.Context, show core.RatingKey, season, episode int, names []string) (core.EpisodeIdentity, error) {
		if show == ep.ShowKey && slices.Contains(names, "Home Alone") {
			return core.EpisodeIdentity{
				OtherShows: map[string][]core.EpisodeNamesake{
					"Home Alone": {{Year: 2017, EpisodeTitle: "Pilot", Language: "en"}},
				},
			}, nil
		}
		return core.EpisodeIdentity{}, nil
	}

	validNames := []string{
		"I.Live.Alone.E674.241115.1080p.WEB-DL.AAC.H.264-xxx",
		"Na.Honja.Sanda.E674.241115.720p-NEXT",
		"나 혼자 산다.E674.241115.1080p",
		"나혼자산다.E674.241115.1080p-NEXT",
		"I Live Alone 2013 E674",
		"Home Alone (2013) - S01E674",
		"I.Live.Alone.241115",
		"I.Live.Alone.2024.11.15.1080p.WEB-DL-NEXT",
	}

	// Wrong-show and namesake noise from the real logs that MUST keep being rejected.
	wrongReleases := []core.Release{
		parsedEpisodeRel("wrong-year-namesake", "Home.Alone.2017.S01E01.1080p.WEB-DL-GRP", 1800),
		parsedEpisodeRel("wrong-movie", "Home.Alone.1990.1080p.BluRay.x264-GRP", 4000),
		parsedEpisodeRel("wrong-show", "Comedians.Home.Alone.S01E01.1080p.WEB-DL-GRP", 1800),
		parsedEpisodeRel("wrong-date", "I.Live.Alone.241114.1080p.WEB-DL-NEXT", 1800),
		parsedEpisodeRel("wrong-ep", "I.Live.Alone.E673.241115.1080p.WEB-DL-NEXT", 1800),
	}

	for _, name := range validNames {
		t.Run(name, func(t *testing.T) {
			rel := parsedEpisodeRel("good", name, 1800)
			all := append([]core.Release{rel}, wrongReleases...)
			svc := New(&fakeIndexer{releases: all}, fakeHealth{}, nil, cfg, Options{
				EpisodeIdentity: namesakeIdentity,
			})
			defer svc.Close()

			res, err := svc.Resolve(ctx, core.ResolveRequest{Title: ep, Client: webCaps()})
			if err != nil {
				t.Fatalf("Resolve(%q) failed: %v (traces=%+v)", name, err, svc.Traces())
			}
			if res.Release.ID != "good" {
				t.Fatalf("Resolve(%q) picked %q (%q), want good", name, res.Release.ID, res.Release.Name)
			}
			// Every wrong release must remain rejected.
			tr := svc.Traces()[0]
			for _, bad := range wrongReleases {
				c := findTraceCandidate(t, tr, bad.ID)
				if !c.Rejected {
					t.Errorf("wrong release %q was not rejected when resolving %q", bad.Name, name)
				}
			}
		})
	}
}

func TestKoreanVarietyConflictingIndexerIDVerification(t *testing.T) {
	ctx := context.Background()
	ep := iLiveAloneE674Title()
	cfg := config.Defaults("")

	var lookups atomic.Int32
	verify := func(_ context.Context, kind core.MediaType, reported, expected core.IDs) (TitleIDVerdict, error) {
		lookups.Add(1)
		if kind != core.TypeShow || expected.TMDB != 65282 {
			return TitleIDConflict, nil
		}
		// Alternate/episode TVDB ID mapped back to TMDB 65282.
		if reported.TVDB == 998877 {
			return TitleIDVerified, nil
		}
		// Movie "Home Alone" (1990) IMDb ID mis-tagged on a TV episode release.
		if reported.IMDB == "tt0099785" {
			return TitleIDWrongMedia, nil
		}
		// Different TV show.
		return TitleIDConflict, nil
	}

	// Unrelated show with conflicting ID must NEVER trigger a verification lookup.
	unrelated := parsedEpisodeRel("number96", "Number.96.S01E674.1080p.WEB-DL-GRP", 1800)
	unrelated.TitleIDs = core.IDs{TVDB: 77961}

	// Different TV show with same title & conflicting ID must stay rejected.
	otherShow := parsedEpisodeRel("other-tv", "Home.Alone.S01E674.1080p.WEB-DL-GRP", 1800)
	otherShow.TitleIDs = core.IDs{TVDB: 333333}

	// Verified alternate TVDB ID on a strongly matching Korean release must be accepted.
	verifiedAlt := parsedEpisodeRel("verified-alt", "I.Live.Alone.E674.241115.1080p.WEB-DL-NEXT", 1800)
	verifiedAlt.TitleIDs = core.IDs{TVDB: 998877}

	svc := New(&fakeIndexer{releases: []core.Release{unrelated, otherShow, verifiedAlt}}, fakeHealth{}, nil, cfg, Options{
		VerifyTitleIDs: verify,
	})
	defer svc.Close()

	res, err := svc.Resolve(ctx, core.ResolveRequest{Title: ep, Client: webCaps()})
	if err != nil {
		t.Fatalf("Resolve with verified alternate TVDB ID failed: %v (traces=%+v)", err, svc.Traces())
	}
	if res.Release.ID != "verified-alt" {
		t.Fatalf("Resolve picked %q, want verified-alt", res.Release.ID)
	}
	if res.Release.TitleIDs.TMDB != 65282 {
		t.Fatalf("accepted release TitleIDs = %+v, want normalized to expected TMDB 65282", res.Release.TitleIDs)
	}
	if c := findTraceCandidate(t, svc.Traces()[0], "other-tv"); !c.Rejected || c.Reason != "indexer title identifier mismatch" {
		t.Fatalf("other-tv = %+v, want indexer title identifier mismatch", c)
	}
	if c := findTraceCandidate(t, svc.Traces()[0], "number96"); !c.Rejected || c.Reason != "indexer title identifier mismatch" {
		t.Fatalf("number96 = %+v, want indexer title identifier mismatch", c)
	}
	// Only otherShow and verifiedAlt passed title+episode checks; number96 paid zero lookups.
	if got := lookups.Load(); got != 2 {
		t.Fatalf("VerifyTitleIDs lookups = %d, want 2 (unrelated show must pay 0)", got)
	}

	// Movie-only IMDb ID mis-tagged on strongly corroborated Korean episode is accepted,
	// while bare colliding title + S01E01 with movie-only ID stays rejected.
	ep1 := *ep
	ep1.Key = core.EpisodeKey(65282, 1, 1)
	ep1.Episode = 1
	ep1.Released = time.Date(2013, 3, 22, 0, 0, 0, 0, time.UTC)

	bareMovieTagged := parsedEpisodeRel("bare-movie-tag", "Home.Alone.S01E01.1080p.WEB-DL-GRP", 1800)
	bareMovieTagged.TitleIDs = core.IDs{IMDB: "tt0099785"}
	svcBare := New(&fakeIndexer{releases: []core.Release{bareMovieTagged}}, fakeHealth{}, nil, cfg, Options{
		VerifyTitleIDs: verify,
	})
	defer svcBare.Close()
	if _, err := svcBare.Resolve(ctx, core.ResolveRequest{Title: &ep1, Client: webCaps()}); err == nil {
		t.Fatal("bare colliding title + S01E01 with movie-only ID should stay rejected")
	}

	strongMovieTagged := parsedEpisodeRel("strong-movie-tag", "I.Live.Alone.E674.241115.1080p.WEB-DL-NEXT", 1800)
	strongMovieTagged.TitleIDs = core.IDs{IMDB: "tt0099785"}
	svcStrong := New(&fakeIndexer{releases: []core.Release{strongMovieTagged}}, fakeHealth{}, nil, cfg, Options{
		VerifyTitleIDs: verify,
	})
	defer svcStrong.Close()
	resStrong, err := svcStrong.Resolve(ctx, core.ResolveRequest{Title: ep, Client: webCaps()})
	if err != nil || resStrong.Release.ID != "strong-movie-tag" {
		t.Fatalf("strongly corroborated episode with mis-tagged movie ID failed: res=%+v err=%v", resStrong, err)
	}
	if resStrong.Release.TitleIDs != (core.IDs{}) {
		t.Fatalf("wrong-media TitleIDs should be cleared, got %+v", resStrong.Release.TitleIDs)
	}
}

func TestKoreanMultiSeasonTitleSuffix(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults("")
	ep := &core.Title{
		Key:                   core.EpisodeKey(219465, 4, 1),
		Type:                  core.TypeEpisode,
		ShowKey:               core.ShowKey(219465),
		ShowIDs:               core.IDs{TMDB: 219465, IMDB: "tt27700161", TVDB: 431502},
		ShowTitle:             "World Dice Tour",
		ShowOriginalTitle:     "지구마불 세계여행",
		ShowEnglishTitle:      "World Dice Tour",
		ShowAlternativeTitles: []string{"Earth Marble World Tour"},
		ShowYear:              2023,
		Title:                 "Episode 1",
		Season:                4,
		Episode:               1,
		Released:              time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC),
		Language:              "ko",
		Runtime:               90 * time.Minute,
	}

	s4 := parsedEpisodeRel("wdt-s4", "World.Dice.Tour.4.E01.261017.1080p-NEXT", 1800)
	s3 := parsedEpisodeRel("wdt-s3", "World.Dice.Tour.3.E01.250301.1080p-NEXT", 1800)
	svc := New(&fakeIndexer{releases: []core.Release{s3, s4}}, fakeHealth{}, nil, cfg, Options{})
	defer svc.Close()

	res, err := svc.Resolve(ctx, core.ResolveRequest{Title: ep, Client: webCaps()})
	if err != nil {
		t.Fatalf("Resolve(World Dice Tour S04E01) failed: %v (traces=%+v)", err, svc.Traces())
	}
	if res.Release.ID != "wdt-s4" {
		t.Fatalf("Resolve picked %q, want wdt-s4", res.Release.ID)
	}
	if c := findTraceCandidate(t, svc.Traces()[0], "wdt-s3"); !c.Rejected {
		t.Fatalf("season 3 release %+v must be rejected for season 4", c)
	}
}

func TestCandidateCheckWaitPreservesFetchedNZBOnTimeout(t *testing.T) {
	// Simulates a discovery-prepared Usenet check that already fetched its NZB
	// and is still inside a congested CheckHealth when the per-candidate
	// deadline fires: candidateCheck.wait must preserve nzb and healthChecked
	// with DeadlineExceeded so lastResort can play unverified.
	nzbBytes := []byte("<nzb>fetched-before-timeout</nzb>")
	rel := core.Release{
		ID:   "slow-health-usenet",
		Name: "I.Live.Alone.S01E661.1080p.WEB-DL-NEXT",
		Info: releaseparse.Parse("I.Live.Alone.S01E661.1080p.WEB-DL-NEXT"),
	}
	checkCtx, checkCancel := context.WithCancel(context.Background())
	chk := &candidateCheck{
		started: time.Now(),
		done:    make(chan struct{}),
		cancel:  checkCancel,
	}
	go func() {
		defer close(chk.done)
		<-checkCtx.Done()
		chk.result = validation{
			rel:           rel,
			nzb:           nzbBytes,
			healthChecked: true,
			err:           checkCtx.Err(),
		}
	}()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	got := chk.wait(waitCtx)
	if !errors.Is(got.err, context.DeadlineExceeded) {
		t.Fatalf("wait err = %v, want context.DeadlineExceeded", got.err)
	}
	if string(got.nzb) != string(nzbBytes) || !got.healthChecked {
		t.Fatalf("wait discarded fetched NZB/healthChecked: len(nzb)=%d healthChecked=%v", len(got.nzb), got.healthChecked)
	}
	if !inconclusiveCheck(got) {
		t.Fatal("preserved timeout validation must satisfy inconclusiveCheck for unverified last-resort playback")
	}

	// Verify foreground final-wave check budget extends only for foreground Play.
	if b := checkBudgetFor(context.Background(), rel, true); b != finalCandidateBudget {
		t.Fatalf("foreground final-wave budget = %v, want %v", b, finalCandidateBudget)
	}
	if b := checkBudgetFor(context.Background(), rel, false); b != candidateBudget {
		t.Fatalf("non-final wave budget = %v, want %v", b, candidateBudget)
	}
	specCtx, specCancel := core.WithSpeculative(context.Background())
	defer specCancel()
	if b := checkBudgetFor(specCtx, rel, true); b != candidateBudget {
		t.Fatalf("speculative budget = %v, want %v", b, candidateBudget)
	}
}
