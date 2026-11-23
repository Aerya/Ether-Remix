package resolver

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestAnimeBDPassesIdentityAndImportedQuality(t *testing.T) {
	cases := []struct {
		name, title string
		episode     int
	}{
		{"[Serendipity] Lovely Complex - S01E01 (BD 1080p HEVC Opus 2.0) [Dual Audio] [7BBF1985]", "Freshman Year Summer! I'll Definitely Find a Boyfriend!", 1},
		{"Lovely Complex S01E01 BD 1080p HEVC Opus 2.0 VOSTFR", "Freshman Year Summer! I'll Definitely Find a Boyfriend!", 1},
		{"Lovely.Complex.S01E01.Freshman.Year.Summer.I.ll.Definitely.Find.a.Boyfriend.ITA.ENG.JAP.1080p.BDMux.AC3.2.0.h265", "Freshman Year Summer! I'll Definitely Find a Boyfriend!", 1},
		{"Lovely.Complex.S01E02.The.Ex.Girlfriend.Love.Triangle.ITA.ENG.JAP.1080p.BDMux.AC3.2.0.h265", "The Ex-Girlfriend Love Triangle?!", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title := &core.Title{Key: core.EpisodeKey(100, 1, tc.episode), Type: core.TypeEpisode, Title: tc.title, ShowTitle: "Lovely★Complex", ShowYear: 2007, Season: 1, Episode: tc.episode, Runtime: 24 * time.Minute}
			cfg := testCfg()
			cfg.Quality.Profiles = []core.QualityProfile{{Name: "imported", AllowedQualities: []string{"bluray-1080"}, MaxHeight: 1080}}
			cfg.Quality.Default = "imported"
			cfg.Quality.Rules = nil
			cfg.Quality.Remote = ""
			r := rel("bd", tc.name, 500)
			idx := &fakeIndexer{releases: []core.Release{r}}
			svc := New(idx, fakeHealth{}, nil, cfg, Options{})
			defer svc.Close()
			got, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: title, Client: core.ClientCaps{MaxHeight: 1080, VideoCodecs: []string{"hevc", "h264"}}})
			if err != nil || got.Release.ID != "bd" {
				t.Fatalf("resolve=%+v err=%v", got, err)
			}
			parsed := withParsed(r)
			req := searchRequestFor(title)
			req.Episode = 3
			if reason := rejectIdentity(parsed, title, req, ""); reason != "season/episode mismatch" {
				t.Fatalf("wrong episode accepted: %q", reason)
			}
		})
	}
}

type fakeAnimeCour struct {
	cour       int
	ep         int
	tvdbSeason int
	tvdbEp     int
	byEp       map[int]core.CourMapping
}

func (f fakeAnimeCour) LookupCour(_ context.Context, req core.SearchRequest) (core.CourMapping, bool, error) {
	if m, ok := f.byEp[req.Episode]; ok {
		return m, true, nil
	}
	if req.IDs.TMDB == 1000 && req.Season == 1 && req.Episode == 13 {
		return core.CourMapping{Cour: f.cour, Episode: f.ep, TVDBSeason: f.tvdbSeason, TVDBEpisode: f.tvdbEp}, true, nil
	}
	return core.CourMapping{}, false, nil
}

func TestAnimeMultiCourResolveAndReject(t *testing.T) {
	titleEp13 := &core.Title{
		Key:                   core.EpisodeKey(1000, 1, 13),
		Type:                  core.TypeEpisode,
		Title:                 "It's Too Late",
		ShowTitle:             "86",
		ShowAlternativeTitles: []string{"Eighty-Six"},
		ShowYear:              2021,
		Season:                1,
		Episode:               13,
		Language:              "ja",
		Genres:                []string{"Animation"},
		Runtime:               24 * time.Minute,
	}
	part1Ep1 := rel("p1e1", "[SubsPlease] Eighty-Six S01 Part 1 - 01 (WEB 1080p) [1234ABCD].mkv", 1400)
	part2Ep1 := rel("p2e1", "[SubsPlease] Eighty-Six S01 Part 2 - 01 (WEB 1080p) [ABCD1234].mkv", 1400)
	part2Ep1.Protocol = core.ProtocolTorrent
	idx := &fakeIndexer{releases: []core.Release{part1Ep1, part2Ep1}}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{AnimeCour: fakeAnimeCour{cour: 2, ep: 1}})
	defer svc.Close()

	got, err := svc.Resolve(context.Background(), core.ResolveRequest{
		Title:  titleEp13,
		Client: core.ClientCaps{MaxHeight: 1080, VideoCodecs: []string{"h264", "hevc"}},
	})
	if err != nil || got.Release.ID != "p2e1" {
		t.Fatalf("expected p2e1 for S01E13 (Cour 2 Ep 1), got %+v err=%v", got, err)
	}
	if got.Release.Target == nil || got.Release.Target.Cour != 2 || got.Release.Target.CourEpisode != 1 {
		t.Fatalf("expected Target Cour=2 CourEpisode=1, got %+v", got.Release.Target)
	}

	// S01E01 (Cour 1) must reject Part 2 Episode 1.
	titleEp1 := *titleEp13
	titleEp1.Key = core.EpisodeKey(1000, 1, 1)
	titleEp1.Episode = 1
	sreqEp1 := svc.searchRequest(context.Background(), &titleEp1)
	if reason := rejectIdentity(withParsed(part2Ep1), &titleEp1, sreqEp1, ""); reason != "season/episode mismatch" {
		t.Fatalf("expected Part 2 Episode 1 to be rejected for S01E01, got %q", reason)
	}
	mapped := svc.searchRequest(context.Background(), titleEp13)
	firstCour := withParsed(part1Ep1).Info
	firstCour.Episodes = []int{13}
	if episodeMatchesRequest(firstCour, mapped) {
		t.Fatal("first cour matched an episode mapped to the second cour")
	}
	pack := withParsed(part2Ep1)
	pack.Info.FullSeason = true
	pack.Info.Episodes = []int{1, 2}
	if reason := rejectIdentity(pack, titleEp13, mapped, ""); reason != "" {
		t.Fatalf("mapped cour episode range rejected: %s", reason)
	}
}

func TestAnimeLongSeasonCourTVDBResolveAndPacks(t *testing.T) {
	courMap := fakeAnimeCour{
		byEp: map[int]core.CourMapping{
			25: {Cour: 3, Episode: 1, TVDBSeason: 3, TVDBEpisode: 1},
			30: {Cour: 3, Episode: 6, TVDBSeason: 3, TVDBEpisode: 6},
		},
	}
	mkTitle := func(ep int, epTitle string) *core.Title {
		return &core.Title{
			Key:                   core.EpisodeKey(237150, 1, ep),
			Type:                  core.TypeEpisode,
			Title:                 epTitle,
			ShowTitle:             "As a Reincarnated Aristocrat, I'll Use My Appraisal Skill to Rise in the World",
			ShowAlternativeTitles: []string{"Tensei Kizoku, Kantei Skill de Nariagaru"},
			ShowIDs:               core.IDs{TMDB: 237150, TVDB: 434704},
			ShowYear:              2024,
			Season:                1,
			Episode:               ep,
			Language:              "ja",
			Genres:                []string{"Animation"},
			Runtime:               24 * time.Minute,
		}
	}
	caps := core.ClientCaps{MaxHeight: 1080, VideoCodecs: []string{"h264", "hevc"}}

	// 1. Scene/WEB-DL S03E01 and fansub "3rd Season - 01" both pass for TMDB S01E25,
	// while Cour 1 / Cour 2 / bare "- 01" are rejected.
	wrongS01E01 := rel("s01e01", "As.a.Reincarnated.Aristocrat.Ill.Use.My.Appraisal.Skill.to.Rise.in.the.World.S01E01.1080p.CR.WEB-DL.H.264-Kitsune", 1400)
	wrongS02E01 := rel("s02e01", "As.a.Reincarnated.Aristocrat.Ill.Use.My.Appraisal.Skill.to.Rise.in.the.World.S02E01.1080p.CR.WEB-DL.H.264-Kitsune", 1400)
	wrongAbs01 := rel("abs01", "[ASW] Tensei Kizoku, Kantei Skill de Nariagaru - 01 [1080p HEVC]", 600)
	s03e01 := rel("s03e01", "As.a.Reincarnated.Aristocrat.Ill.Use.My.Appraisal.Skill.to.Rise.in.the.World.S03E01.Headman.1080p.CR.WEB-DL.DUAL.DDP2.0.H.264-Kitsune", 1400)
	ordinalEp := rel("ord01", "[Erai-raws] Tensei Kizoku, Kantei Skill de Nariagaru 3rd Season - 01 [1080p CR WEB-DL AVC AAC][MultiSub]", 1400)
	ordinalEp.Protocol = core.ProtocolTorrent

	title25 := mkTitle(25, "Headman")
	svc := New(&fakeIndexer{releases: []core.Release{wrongS01E01, wrongS02E01, wrongAbs01, s03e01}}, fakeHealth{}, nil, testCfg(), Options{AnimeCour: courMap})
	defer svc.Close()
	got, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: title25, Client: caps})
	if err != nil || got.Release.ID != "s03e01" {
		t.Fatalf("expected s03e01 for S01E25, got %+v err=%v", got, err)
	}

	svcOrd := New(&fakeIndexer{releases: []core.Release{wrongAbs01, ordinalEp}}, fakeHealth{}, nil, testCfg(), Options{AnimeCour: courMap})
	defer svcOrd.Close()
	gotOrd, err := svcOrd.Resolve(context.Background(), core.ResolveRequest{Title: title25, Client: caps})
	if err != nil || gotOrd.Release.ID != "ord01" {
		t.Fatalf("expected ord01 for S01E25, got %+v err=%v", gotOrd, err)
	}
	if gotOrd.Release.Target == nil || gotOrd.Release.Target.Season != 3 || gotOrd.Release.Target.Episode != 1 || gotOrd.Release.Target.CanonicalSeason != 1 || gotOrd.Release.Target.CanonicalEpisode != 25 {
		t.Fatalf("unexpected target on ordinal TVDB release: %+v", gotOrd.Release.Target)
	}

	// 2. Absolute numbering ("- 30") resolves for TMDB S01E30 (Cour 3 Ep 6).
	abs30 := rel("abs30", "[ASW] Tensei Kizoku, Kantei Skill de Nariagaru - 30 [1080p HEVC]", 600)
	title30 := mkTitle(30, "Episode 30")
	svcAbs := New(&fakeIndexer{releases: []core.Release{wrongAbs01, s03e01, abs30}}, fakeHealth{}, nil, testCfg(), Options{AnimeCour: courMap})
	defer svcAbs.Close()
	gotAbs, err := svcAbs.Resolve(context.Background(), core.ResolveRequest{Title: title30, Client: caps})
	if err != nil || gotAbs.Release.ID != "abs30" {
		t.Fatalf("expected abs30 for S01E30, got %+v err=%v", gotAbs, err)
	}

	// 3. TVDB season packs (S03 / 3rd Season) cover TMDB S01E25 with target (3, 1),
	// while S02 packs and Cour 1 requests against S03 packs are rejected.
	sreq25 := svc.searchRequest(context.Background(), title25)
	for _, packName := range []string{
		"As.a.Reincarnated.Aristocrat.Ill.Use.My.Appraisal.Skill.to.Rise.in.the.World.S03.1080p.CR.WEB-DL.H.264-Kitsune",
		"[Batch] Tensei Kizoku, Kantei Skill de Nariagaru 3rd Season [1080p CR WEB-DL]",
	} {
		p := rel("pack-s03", packName, 14000)
		p.Protocol = core.ProtocolTorrent
		parsedPack := withTarget(withParsed(p), sreq25)
		if reason := rejectIdentity(parsedPack, title25, sreq25, ""); reason != "" {
			t.Fatalf("TVDB S03 pack %q rejected for S01E25: %s", packName, reason)
		}
		if parsedPack.Target == nil || parsedPack.Target.Season != 3 || parsedPack.Target.Episode != 1 || parsedPack.Target.CanonicalEpisode != 25 || parsedPack.Target.AbsoluteEpisode != 25 {
			t.Fatalf("TVDB S03 pack %q target = %+v, want Season=3 Episode=1 CanonicalEpisode=25 AbsoluteEpisode=25", packName, parsedPack.Target)
		}
		title1 := mkTitle(1, "Episode 1")
		sreq1 := svc.searchRequest(context.Background(), title1)
		if reason := rejectIdentity(withParsed(p), title1, sreq1, ""); reason != "season/episode mismatch" {
			t.Fatalf("TVDB S03 pack %q accepted for S01E01: %q", packName, reason)
		}
	}
	packS02 := rel("pack-s02", "As.a.Reincarnated.Aristocrat.Ill.Use.My.Appraisal.Skill.to.Rise.in.the.World.S02.1080p.CR.WEB-DL.H.264-Kitsune", 14000)
	packS02.Protocol = core.ProtocolTorrent
	if reason := rejectIdentity(withParsed(packS02), title25, sreq25, ""); reason != "season/episode mismatch" {
		t.Fatalf("S02 pack accepted for S01E25: %q", reason)
	}
}
