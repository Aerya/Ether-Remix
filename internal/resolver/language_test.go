package resolver

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestLanguageRankingRequiresExplicitPreference(t *testing.T) {
	s := New(&fakeIndexer{}, nil, nil, testCfg(), Options{Now: func() time.Time { return time.Unix(1800000000, 0) }})
	defer s.Close()
	r := withParsed(rel("language", "The.Signal.1999.1080p.WEB-DL.H264-GRP", 4000))
	score := func(lang string, prefs ...string) float64 {
		r.Info.Languages = []string{lang}
		return s.languageScore(r, core.QualityProfile{PreferredLanguages: prefs}, matrixTitle())
	}
	for _, lang := range []string{"french", "german", "japanese", "multi"} {
		if score(lang) != score("english") {
			t.Errorf("default language penalty for %s", lang)
		}
	}
	if score("french", "french") <= score("english", "french") {
		t.Fatal("explicit French preference did not outrank English")
	}
	if score("english", "english") <= score("french", "english") {
		t.Fatal("explicit English preference ignored")
	}
}

// A profile Language is Radarr's hard filter: a specific language rejects
// releases whose audio isn't in it, Original keeps today's behaviour and
// Any accepts everything. The anime case: on a Japanese title an English
// profile rejects the Japanese release however good its quality is.
func TestProfileLanguageGate(t *testing.T) {
	anime := &core.Title{Language: "ja"}
	english := core.QualityProfile{Language: "en"}
	info := func(langs ...string) core.ReleaseInfo { return core.ReleaseInfo{Languages: langs} }

	for _, tt := range []struct {
		name    string
		profile core.QualityProfile
		title   *core.Title
		info    core.ReleaseInfo
		reject  bool
	}{
		{"4K Japanese on an English profile", english, anime, info("japanese"), true},
		{"1080p English dub", english, anime, info("english"), false},
		{"multi audio", english, anime, info("multi"), false},
		{"dual audio", english, anime, info("dual"), false},
		{"untagged counts as the original", english, anime, info(), true},
		{"untagged, original unknown", english, &core.Title{}, info(), false},
		{"untagged English original", english, &core.Title{Language: "en"}, info(), false},
		{"no language set keeps today", core.QualityProfile{}, anime, info("japanese"), false},
		{"Original keeps today", core.QualityProfile{Language: "original"}, anime, info("french"), false},
		{"Any accepts everything", core.QualityProfile{Language: "any"}, anime, info("french"), false},
		{"unrecognised language keeps today", core.QualityProfile{Language: "klingon"}, anime, info("japanese"), false},
		{"regional variant of the profile language", core.QualityProfile{Language: "es"}, anime, info("spanish-latino"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reason := profileLanguageRejection(tt.info, tt.profile, tt.title)
			if (reason != "") != tt.reject {
				t.Fatalf("reason=%q reject=%v", reason, tt.reject)
			}
		})
	}

	// A dub into the profile's language is what the language asks for, so
	// the dub-mismatch check must not reject it; an explicit pick (a pin)
	// bypasses the gate.
	r := core.Release{Name: "Primetime (2026) 1080p WEB-DL [Eng Dub]", Info: core.ReleaseInfo{Source: core.SourceWEBDL}}
	if reason := sourceSuitability(r, anime, english, false); reason != "" {
		t.Fatalf("English dub on an English profile: %q", reason)
	}
	r = core.Release{Name: "Primetime (2026) 2160p WEB-DL [Jpn Dub]", Info: core.ReleaseInfo{Source: core.SourceWEBDL}}
	if reason := sourceSuitability(r, anime, english, true); reason != "" {
		t.Fatalf("explicit pick bypasses the gate: %q", reason)
	}

	// Accepted languages follow the mode: Original adds the title's own
	// language, a specific language accepts only itself, Any all.
	if got := acceptedAudio(core.QualityProfile{}, nil, "en", anime); len(got) != 2 || got[1] != "ja" {
		t.Fatalf("original accepted=%v", got)
	}
	if got := acceptedAudio(english, nil, "en", anime); len(got) != 1 || got[0] != "english" {
		t.Fatalf("specific accepted=%v", got)
	}
	if got := acceptedAudio(core.QualityProfile{Language: "any"}, nil, "en", anime); got != nil {
		t.Fatalf("any accepted=%v", got)
	}
	if got := wantedAudio(english, nil, "fr"); len(got) != 1 || got[0] != "english" {
		t.Fatalf("specific wanted=%v", got)
	}

	// #674: a viewer with PreferOriginal and no AudioLanguages must accept the
	// title's original language without falling back to the server language.
	prefOrig := &core.User{ID: 1, PreferOriginal: true}
	if got := wantedAudio(core.QualityProfile{}, prefOrig, "en-US"); len(got) != 0 {
		t.Fatalf("preferOriginal wanted=%v, want empty (no serverLang fallback)", got)
	}
	if got := acceptedAudio(core.QualityProfile{}, prefOrig, "en-US", anime); len(got) != 1 || got[0] != "ja" {
		t.Fatalf("preferOriginal accepted=%v, want [ja]", got)
	}
}

// #674: ExcludeReleases drops content-equivalent reposts (same release name
// across indexers), while ID-only Exclude still lets another indexer's repost
// serve transport/article failover.
func TestResolveExcludeReleasesSkipsReposts(t *testing.T) {
	repost1 := rel("idx1-repost", "The.Signal.1999.1080p.WEB-DL.H264-DUP", 4000)
	repost1.Indexer = "idx1"
	repost2 := rel("idx2-repost", "The.Signal.1999.1080p.WEB-DL.H264-DUP", 4000)
	repost2.Indexer = "idx2"
	other := rel("idx1-other", "The.Signal.1999.1080p.WEB-DL.H264-ALT", 3500)
	other.Indexer = "idx1"

	idx := &fakeIndexer{releases: []core.Release{repost1, repost2, other}}
	s := New(idx, nil, nil, testCfg(), Options{Now: func() time.Time { return time.Unix(1800000000, 0) }})
	defer s.Close()

	// ID-only Exclude still picks idx2-repost for broken-upload failover.
	res, err := s.Resolve(context.Background(), core.ResolveRequest{
		Title:   matrixTitle(),
		Exclude: []string{repost1.ID},
	})
	if err != nil || res.Release.ID != repost2.ID {
		t.Fatalf("ID-only Exclude got (%+v, %v), want %s", res, err, repost2.ID)
	}

	// ExcludeReleases excludes both repost1 and idx2-repost so failover advances to other.
	res, err = s.Resolve(context.Background(), core.ResolveRequest{
		Title:           matrixTitle(),
		Exclude:         []string{repost1.ID},
		ExcludeReleases: []core.Release{repost1},
	})
	if err != nil || res.Release.ID != other.ID {
		t.Fatalf("ExcludeReleases got (%+v, %v), want %s", res, err, other.ID)
	}
}

func TestViewerSubtitlesScoringAndRemoval(t *testing.T) {
	viewer := &core.User{ID: 7, SubtitleLanguages: []string{"ar"}}
	plain := rel("plain", "The.Signal.1999.1080p.WEB-DL.H264-AAA", 4000)
	withSubs := rel("subs", "The.Signal.1999.1080p.WEB-DL.H264-BBB", 3800)
	withSubs.IndexerSubtitles = []string{"english", "arabic"}

	// When the profile scores FormatViewerSubtitles, the release listing the
	// viewer's subtitle language wins the tie over the slightly larger release.
	idx := &fakeIndexer{releases: []core.Release{plain, withSubs}}
	cfg := testCfg()
	s := New(idx, nil, nil, cfg, Options{Now: func() time.Time { return time.Unix(1800000000, 0) }})
	defer s.Close()

	res, err := s.Resolve(context.Background(), core.ResolveRequest{Title: matrixTitle(), User: viewer})
	if err != nil || res.Release.ID != withSubs.ID {
		t.Fatalf("scored viewer-subtitles got (%+v, %v), want %s", res, err, withSubs.ID)
	}

	// Removing FormatViewerSubtitles from CustomFormatScores (as the console
	// Remove button does) must genuinely turn it off.
	cfgNoSubs := testCfg()
	for i := range cfgNoSubs.Quality.Profiles {
		delete(cfgNoSubs.Quality.Profiles[i].CustomFormatScores, core.FormatViewerSubtitles)
	}
	sOff := New(idx, nil, nil, cfgNoSubs, Options{Now: func() time.Time { return time.Unix(1800000000, 0) }})
	defer sOff.Close()

	res, err = sOff.Resolve(context.Background(), core.ResolveRequest{Title: matrixTitle(), User: viewer})
	if err != nil || res.Release.ID != plain.ID {
		t.Fatalf("removed viewer-subtitles got (%+v, %v), want %s", res, err, plain.ID)
	}

	// Default score (15) must not force a video transcode (-20) over DirectPlay.
	direct := rel("direct", "The.Signal.1999.1080p.WEB-DL.H264-AAA", 4000)
	transcodeWithSubs := rel("xvid-subs", "The.Signal.1999.1080p.WEB-DL.XviD-BBB", 4000)
	transcodeWithSubs.IndexerSubtitles = []string{"arabic"}
	idx2 := &fakeIndexer{releases: []core.Release{transcodeWithSubs, direct}}
	s2 := New(idx2, nil, nil, testCfg(), Options{Now: func() time.Time { return time.Unix(1800000000, 0) }})
	defer s2.Close()

	res, err = s2.Resolve(context.Background(), core.ResolveRequest{
		Title:  matrixTitle(),
		User:   viewer,
		Client: core.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
	})
	if err != nil || res.Release.ID != direct.ID {
		t.Fatalf("transcode with subs beat DirectPlay: got (%+v, %v), want %s", res, err, direct.ID)
	}
}

// #743: A viewer's ordered audio languages must keep their priority even when
// the title's original language appears as a later fallback (e.g. ["de","en"]
// on an English-original show), bare DUAL must not act as a wildcard for a
// wanted language, and an owner with [] (falling back to the catalogue
// language) must behave unchanged.
func TestOrderedViewerAudioLanguagesAndBareDual(t *testing.T) {
	mobland := &core.Title{
		Key:       core.EpisodeKey(247718, 1, 1),
		Type:      core.TypeEpisode,
		ShowKey:   core.ShowKey(247718),
		ShowTitle: "MobLand",
		ShowYear:  2025,
		ShowIDs:   core.IDs{TMDB: 247718},
		Title:     "Stick or Twist",
		Season:    1,
		Episode:   1,
		Language:  "en",
		Runtime:   50 * time.Minute,
	}
	percy := &core.Title{
		Key:       core.EpisodeKey(103540, 1, 1),
		Type:      core.TypeEpisode,
		ShowKey:   core.ShowKey(103540),
		ShowTitle: "Percy Jackson and the Olympians",
		ShowYear:  2023,
		ShowIDs:   core.IDs{TMDB: 103540},
		Title:     "I Accidentally Vaporize My Pre-Algebra Teacher",
		Season:    1,
		Episode:   1,
		Language:  "en",
		Runtime:   45 * time.Minute,
	}
	anime := &core.Title{
		Key:       core.EpisodeKey(90099, 1, 1),
		Type:      core.TypeEpisode,
		ShowKey:   core.ShowKey(90099),
		ShowTitle: "Distant Horizon",
		ShowYear:  2025,
		ShowIDs:   core.IDs{TMDB: 90099},
		Title:     "Departure",
		Season:    1,
		Episode:   1,
		Language:  "ja",
		Runtime:   24 * time.Minute,
	}

	makeRel := func(id, name string, mb int64) core.Release {
		return withParsed(rel(id, name, mb))
	}

	moblandPool := []core.Release{
		makeRel("spweb", "MobLand.S01E01.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-SPWEB", 4200),
		makeRel("german-dl", "MobLand.S01E01.German.DL.1080p.WEB.H264-GRP", 3200),
		makeRel("ger-multi", "MobLand.S01E01.1080p.WEB-DL.GER.ENG.MULTI.DDP5.1.H.264-GRP", 3600),
		makeRel("ita-multi", "MobLand.S01E01.1080p.WEB-DL.ITA.SPA.ENG.MULTI.DDP5.1.H.264-GRP", 3900),
		makeRel("eng-rip", "MobLand.S01E01.1080p.WEBRip.ENG.DDP5.1.H.264-LOW", 3800),
		makeRel("french", "MobLand.S01E01.FRENCH.1080p.BluRay.DDP5.1.H.264-GRP", 4800),
	}

	percyPool := []core.Release{
		makeRel("dual-turg", "Percy.Jackson.and.the.Olympians.S01E01.1080p.DSNP.WEB-DL.DDP5.1.Atmos.H.264.DUAL-TURG", 4400),
		makeRel("spweb", "Percy.Jackson.and.the.Olympians.S01E01.1080p.DSNP.WEB-DL.DDP5.1.H.264-SPWEB", 3600),
		makeRel("german-dl", "Percy.Jackson.and.the.Olympians.S01E01.German.DL.1080p.DSNP.WEB-DL.DDP5.1.H.264-GRP", 3100),
		makeRel("french", "Percy.Jackson.and.the.Olympians.S01E01.FRENCH.1080p.DSNP.WEB-DL.DDP5.1.H.264-GRP", 4500),
	}

	animePool := []core.Release{
		makeRel("untagged-ja", "Distant.Horizon.S01E01.1080p.WEB-DL.DDP5.1.Atmos.H.264-SUBS", 2400),
		makeRel("dual-bare", "Distant.Horizon.S01E01.1080p.WEB-DL.DDP5.1.Atmos.H.264.DUAL-TURG", 2500),
		makeRel("english", "Distant.Horizon.S01E01.1080p.WEB-DL.ENGLISH.DDP5.1.H.264-GRP", 2200),
		makeRel("german", "Distant.Horizon.S01E01.1080p.WEB-DL.GERMAN.DDP5.1.H.264-GRP", 2000),
		makeRel("french", "Distant.Horizon.S01E01.1080p.BluRay.FRENCH.DDP5.1.H.264-GRP", 2800),
	}

	threeLangPool := []core.Release{
		makeRel("dual-turg", "MobLand.S01E01.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264.DUAL-TURG", 4400),
		makeRel("spweb", "MobLand.S01E01.1080p.AMZN.WEB-DL.DDP5.1.H.264-SPWEB", 4200),
		makeRel("german-dl", "MobLand.S01E01.German.DL.1080p.WEB-DL.DDP5.1.H.264-GRP", 3600),
		makeRel("french", "MobLand.S01E01.FRENCH.1080p.WEB-DL.DDP5.1.H.264-GRP", 3200),
	}

	type viewerScoreCheck struct {
		id         string
		wantFound  bool
		wantScore  int
		wantNameIn string
	}

	cases := []struct {
		name         string
		catalogLang  string
		title        *core.Title
		user         *core.User
		pool         []core.Release
		exclude      []string
		wantOrder    []string
		scoreChecks  []viewerScoreCheck
		compatChecks map[string]string
	}{
		{
			name:        "MobLand viewer [de,en] prefers German over untagged SPWEB and ITA-ENG multi",
			catalogLang: "de-DE",
			title:       mobland,
			user:        &core.User{ID: 2, AudioLanguages: []string{"de", "en"}, SubtitleLanguages: []string{"de"}},
			pool:        moblandPool,
			wantOrder:   []string{"ger-multi", "german-dl", "spweb", "ita-multi", "eng-rip", "french"},
			scoreChecks: []viewerScoreCheck{
				{id: "german-dl", wantFound: true, wantScore: 20000, wantNameIn: "#1, german"},
				{id: "ger-multi", wantFound: true, wantScore: 20000, wantNameIn: "#1, german"},
				{id: "spweb", wantFound: true, wantScore: 10000, wantNameIn: "#2, english original"},
				{id: "ita-multi", wantFound: true, wantScore: 10000, wantNameIn: "#2, english"},
				{id: "french", wantFound: false},
			},
		},
		{
			name:        "MobLand viewer [de,en] falls back to English SPWEB when no German is playable",
			catalogLang: "de-DE",
			title:       mobland,
			user:        &core.User{ID: 2, AudioLanguages: []string{"de", "en"}},
			pool:        moblandPool,
			exclude:     []string{"german-dl", "ger-multi"},
			wantOrder:   []string{"spweb", "ita-multi", "eng-rip", "french"},
		},
		{
			name:        "MobLand owner [] falls back to catalogue de unchanged",
			catalogLang: "de-DE",
			title:       mobland,
			user:        &core.User{ID: 1, IsOwner: true, AudioLanguages: []string{}},
			pool:        moblandPool,
			wantOrder:   []string{"ita-multi", "ger-multi", "german-dl", "spweb", "eng-rip", "french"},
			scoreChecks: []viewerScoreCheck{
				{id: "german-dl", wantFound: true, wantScore: 20000, wantNameIn: "Viewer's language"},
				{id: "ger-multi", wantFound: true, wantScore: 20000, wantNameIn: "Viewer's language"},
				{id: "spweb", wantFound: false},
			},
		},
		{
			name:        "MobLand owner [] with English catalogue leaves bonus off unchanged",
			catalogLang: "en-US",
			title:       mobland,
			user:        &core.User{ID: 1, IsOwner: true, AudioLanguages: []string{}},
			pool:        moblandPool,
			wantOrder:   []string{"spweb", "ita-multi", "ger-multi", "eng-rip", "french", "german-dl"},
			scoreChecks: []viewerScoreCheck{
				{id: "spweb", wantFound: false},
				{id: "german-dl", wantFound: false},
				{id: "ger-multi", wantFound: false},
			},
		},
		{
			name:        "MobLand viewer [en,de] puts English ahead of German",
			catalogLang: "de-DE",
			title:       mobland,
			user:        &core.User{ID: 3, AudioLanguages: []string{"en", "de"}},
			pool:        moblandPool,
			wantOrder:   []string{"spweb", "ita-multi", "ger-multi", "eng-rip", "german-dl", "french"},
			scoreChecks: []viewerScoreCheck{
				{id: "spweb", wantFound: true, wantScore: 20000, wantNameIn: "#1, english original"},
				{id: "german-dl", wantFound: true, wantScore: 10000, wantNameIn: "#2, german"},
			},
		},
		{
			name:        "Percy Jackson viewer [de,en] ranks German.DL > SPWEB > DUAL-TURG > French",
			catalogLang: "de-DE",
			title:       percy,
			user:        &core.User{ID: 2, AudioLanguages: []string{"de", "en"}},
			pool:        percyPool,
			wantOrder:   []string{"german-dl", "spweb", "dual-turg", "french"},
			scoreChecks: []viewerScoreCheck{
				{id: "german-dl", wantFound: true, wantScore: 20000, wantNameIn: "#1, german"},
				{id: "spweb", wantFound: true, wantScore: 10000, wantNameIn: "#2, english original"},
				{id: "dual-turg", wantFound: false},
			},
			compatChecks: map[string]string{
				"dual-turg": "bare DUAL names no dub language",
				"french":    "accepted audio language",
			},
		},
		{
			name:        "Percy Jackson viewer [de,en] falls back to DUAL-TURG only behind SPWEB and ahead of French",
			catalogLang: "de-DE",
			title:       percy,
			user:        &core.User{ID: 2, AudioLanguages: []string{"de", "en"}},
			pool:        percyPool,
			exclude:     []string{"german-dl", "spweb"},
			wantOrder:   []string{"dual-turg", "french"},
		},
		{
			name:        "Percy Jackson owner [] with catalogue de picks German.DL over DUAL-TURG",
			catalogLang: "de-DE",
			title:       percy,
			user:        &core.User{ID: 1, IsOwner: true},
			pool:        percyPool,
			wantOrder:   []string{"german-dl", "dual-turg", "spweb", "french"},
			scoreChecks: []viewerScoreCheck{
				{id: "german-dl", wantFound: true, wantScore: 20000, wantNameIn: "Viewer's language"},
				{id: "dual-turg", wantFound: false},
				{id: "spweb", wantFound: false},
			},
		},
		{
			name:        "Foreign Japanese title viewer [de,en] scales first > second > untagged/original and bare DUAL",
			catalogLang: "de-DE",
			title:       anime,
			user:        &core.User{ID: 2, AudioLanguages: []string{"de", "en"}},
			pool:        animePool,
			wantOrder:   []string{"german", "english", "dual-bare", "untagged-ja", "french"},
			scoreChecks: []viewerScoreCheck{
				{id: "german", wantFound: true, wantScore: 20000, wantNameIn: "#1, german"},
				{id: "english", wantFound: true, wantScore: 10000, wantNameIn: "#2, english"},
				{id: "untagged-ja", wantFound: false},
				{id: "dual-bare", wantFound: false},
			},
		},
		{
			name:        "Three-language viewer [fr,de,en] scales #1 > #2 > #3 > bare DUAL",
			catalogLang: "en-US",
			title:       mobland,
			user:        &core.User{ID: 4, AudioLanguages: []string{"fr", "de", "en"}},
			pool:        threeLangPool,
			wantOrder:   []string{"french", "german-dl", "spweb", "dual-turg"},
			scoreChecks: []viewerScoreCheck{
				{id: "french", wantFound: true, wantScore: 20000, wantNameIn: "#1, french"},
				{id: "german-dl", wantFound: true, wantScore: 10000, wantNameIn: "#2, german"},
				{id: "spweb", wantFound: true, wantScore: 5000, wantNameIn: "#3, english original"},
				{id: "dual-turg", wantFound: false},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg()
			cfg.Catalog.Language = tc.catalogLang
			svc := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{Now: func() time.Time { return corpusNow }})
			defer svc.Close()

			caps := appleTV4K()
			profile := SelectProfileForMedia(cfg.Quality, caps, tc.user, titleMedia(tc.title))
			var tr Trace
			req := core.ResolveRequest{Title: tc.title, Client: caps, User: tc.user, Exclude: tc.exclude}
			cands, _, err := svc.rankCandidates(context.Background(), req, searchRequestFor(tc.title), profile, slices.Clone(tc.pool), &tr, "")
			if err != nil {
				t.Fatalf("rankCandidates: %v", err)
			}
			gotOrder := make([]string, len(cands))
			for i, c := range cands {
				gotOrder[i] = c.rel.ID
			}
			if !slices.Equal(gotOrder, tc.wantOrder) {
				t.Fatalf("order = %v, want %v", gotOrder, tc.wantOrder)
			}
			for _, sc := range tc.scoreChecks {
				ct := findTraceCandidate(t, tr, sc.id)
				var matched *FormatMatch
				for i := range ct.CustomFormats {
					if ct.CustomFormats[i].ID == core.FormatViewerLanguage {
						matched = &ct.CustomFormats[i]
						break
					}
				}
				if sc.wantFound != (matched != nil) {
					t.Errorf("%s: viewer-language match = %+v, wantFound=%v", sc.id, matched, sc.wantFound)
					continue
				}
				if matched != nil {
					if matched.Score != sc.wantScore {
						t.Errorf("%s: viewer-language score = %d, want %d", sc.id, matched.Score, sc.wantScore)
					}
					if sc.wantNameIn != "" && !strings.Contains(matched.Name, sc.wantNameIn) {
						t.Errorf("%s: viewer-language name = %q, want substring %q", sc.id, matched.Name, sc.wantNameIn)
					}
					if tc.user != nil && len(tc.user.AudioLanguages) == 0 && matched.Name != BuiltinFormatName(core.FormatViewerLanguage) {
						t.Errorf("%s: owner [] name = %q, want plain %q", sc.id, matched.Name, BuiltinFormatName(core.FormatViewerLanguage))
					}
				}
			}
			for id, wantSub := range tc.compatChecks {
				ct := findTraceCandidate(t, tr, id)
				if !strings.Contains(ct.CompatibilityReason, wantSub) {
					t.Errorf("%s: CompatibilityReason = %q, want substring %q", id, ct.CompatibilityReason, wantSub)
				}
			}
		})
	}
}

func TestBareDualAcceptabilityAndOtherLanguagePenalty(t *testing.T) {
	english := &core.Title{Language: "en"}
	spanish := &core.Title{Language: "es"}
	anime := &core.Title{Language: "ja"}
	bareDual := releaseparse.Parse("Percy.Jackson.and.the.Olympians.S01E01.1080p.DSNP.WEB-DL.DDP5.1.Atmos.H.264.DUAL-TURG")
	if !slices.Equal(bareDual.Languages, []string{releaseparse.LanguageDual}) {
		t.Fatalf("expected bare DUAL parse, got %v", bareDual.Languages)
	}

	acceptCases := []struct {
		name       string
		sourceLang string
		accepted   []string
		title      *core.Title
		want       bool
	}{
		{"strict German rejects bare DUAL on English title", "", []string{"german"}, english, false},
		{"strict English rejects bare DUAL on Spanish title", "", []string{"english"}, spanish, false},
		{"strict English rejects bare DUAL on Japanese anime", "", []string{"english"}, anime, false},
		{"English source does not satisfy strict German on English title", "en", []string{"german"}, english, false},
		{"original fallback accepts bare DUAL when English is accepted", "", []string{"de", "en"}, english, true},
		{"original fallback accepts bare DUAL when Spanish is accepted", "", []string{"en", "es"}, spanish, true},
		{"non-original indexer language accepts bare DUAL", "pt-BR", []string{"pt-BR"}, english, true},
		{"non-original German indexer language accepts bare DUAL", "de", []string{"german"}, english, true},
	}
	for _, tc := range acceptCases {
		t.Run("audioAcceptable/"+tc.name, func(t *testing.T) {
			if got := audioAcceptable(bareDual, tc.sourceLang, tc.accepted, tc.title); got != tc.want {
				t.Fatalf("audioAcceptable = %v, want %v", got, tc.want)
			}
		})
	}

	formatCases := []struct {
		name       string
		sourceLang string
		prefs      []string
		orig       string
		want       []string
	}{
		{"bare DUAL on English title with German-only prefs is other language", "", []string{"de"}, "en", []string{core.FormatOtherLanguage}},
		{"bare DUAL on Spanish title with English-only prefs is other language", "", []string{"en"}, "es", []string{core.FormatOtherLanguage}},
		{"bare DUAL on English title with [de,en] is neutral original fallback", "", []string{"de", "en"}, "en", nil},
		{"bare DUAL from English source on English title with [de,en] stays neutral", "en", []string{"de", "en"}, "en", nil},
		{"bare DUAL from German source on English title with [de,en] is preferred", "de", []string{"de", "en"}, "en", []string{core.FormatPreferredLanguage}},
		{"bare DUAL from pt-BR source on English title with [pt-BR] is preferred", "pt-BR", []string{"pt-BR"}, "en", []string{core.FormatPreferredLanguage}},
	}
	for _, tc := range formatCases {
		t.Run("languageFormats/"+tc.name, func(t *testing.T) {
			got := languageFormats(bareDual, tc.sourceLang, tc.prefs, tc.orig)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("languageFormats = %v, want %v", got, tc.want)
			}
		})
	}

	frenchDual := releaseparse.Parse("Percy.Jackson.and.the.Olympians.S01E01.FRENCH.1080p.DSNP.WEB-DL.DDP5.1.H.264.DUAL-GRP")
	if !slices.Contains(frenchDual.Languages, "french") || !slices.Contains(frenchDual.Languages, releaseparse.LanguageDual) {
		t.Fatalf("expected FRENCH+DUAL parse, got %v", frenchDual.Languages)
	}
	if audioAcceptable(frenchDual, "de", []string{"german"}, english) {
		t.Fatal("explicit FRENCH.DUAL must not be accepted as German via sourceLang")
	}
	if got := languageFormats(frenchDual, "de", []string{"de"}, "en"); !slices.Equal(got, []string{core.FormatOtherLanguage}) {
		t.Fatalf("FRENCH.DUAL with German sourceLang formats = %v, want [%s]", got, core.FormatOtherLanguage)
	}
}

type rescueCountingIndexer struct {
	fakeIndexer
	rescueCalls int
}

func (r *rescueCountingIndexer) SearchRescue(_ context.Context, _ core.SearchRequest) ([]core.Release, error) {
	r.rescueCalls++
	return nil, nil
}

// W321: Profile Audio language can be Preferred (ranking with original-language
// fallback, composed with the viewer's ordered language list from #743) or
// Required (strict gate that fails fast without rescue searches and tells the
// player how to allow other languages).
func TestProfileAudioLanguagePreferredVsRequired(t *testing.T) {
	bluey := &core.Title{
		Key:       core.EpisodeKey(82728, 1, 30),
		Type:      core.TypeEpisode,
		ShowKey:   core.ShowKey(82728),
		ShowTitle: "Bluey",
		ShowYear:  2018,
		ShowIDs:   core.IDs{TMDB: 82728},
		Title:     "The Claw",
		Season:    1,
		Episode:   30,
		Language:  "en",
		Runtime:   7 * time.Minute,
	}
	koreanShow := &core.Title{
		Key:       core.EpisodeKey(93405, 1, 1),
		Type:      core.TypeEpisode,
		ShowKey:   core.ShowKey(93405),
		ShowTitle: "Seoul Drift",
		ShowYear:  2024,
		ShowIDs:   core.IDs{TMDB: 93405},
		Title:     "Pilot",
		Season:    1,
		Episode:   1,
		Language:  "ko",
		Runtime:   50 * time.Minute,
	}

	makeRel := func(id, name string, mb int64) core.Release {
		return withParsed(rel(id, name, mb))
	}

	blueyPool := []core.Release{
		makeRel("bluey-es", "Bluey.S01E30.1080p.WEB-DL.SPANISH.H.264-GRP", 900),
		makeRel("bluey-ja", "Bluey.S01E30.1080p.WEB-DL.JAPANESE.H.264-GRP", 850),
		makeRel("bluey-en", "Bluey.S01E30.1080p.WEB-DL.H.264-GRP", 700),
		makeRel("bluey-multi-en", "Bluey.S01E30.1080p.WEB-DL.MULTI.ENGLISH.SPANISH.H.264-GRP", 500),
		makeRel("bluey-fr", "Bluey.S01E30.1080p.WEB-DL.FRENCH.H.264-GRP", 950),
	}

	koreanOnlyPool := []core.Release{
		makeRel("ko-orig", "Seoul.Drift.S01E01.1080p.WEB-DL.KOREAN.H.264-GRP", 3600),
		makeRel("ko-untagged", "Seoul.Drift.S01E01.1080p.WEB-DL.H.264-RAW", 3400),
		makeRel("ko-fr", "Seoul.Drift.S01E01.1080p.WEB-DL.FRENCH.H.264-DUB", 4200),
	}

	koreanWithDubPool := []core.Release{
		makeRel("ko-orig", "Seoul.Drift.S01E01.1080p.WEB-DL.KOREAN.H.264-GRP", 4200),
		makeRel("ko-en-dub", "Seoul.Drift.S01E01.1080p.WEB-DL.ENGLISH.H.264-GRP", 3200),
		makeRel("ko-multi-en", "Seoul.Drift.S01E01.1080p.WEB-DL.MULTI.ENGLISH.KOREAN.H.264-GRP", 3500),
		makeRel("ko-multi-es-fr", "Seoul.Drift.S01E01.1080p.WEB-DL.MULTI.SPANISH.FRENCH.H.264-GRP", 4500),
		makeRel("ko-es", "Seoul.Drift.S01E01.1080p.WEB-DL.SPANISH.H.264-GRP", 3800),
		makeRel("ko-fr", "Seoul.Drift.S01E01.1080p.WEB-DL.FRENCH.H.264-GRP", 4000),
	}

	cases := []struct {
		name               string
		title              *core.Title
		profileLang        string
		profileMode        string
		stripViewerLangCF  bool
		user               *core.User
		pool               []core.Release
		exclude            []string
		wantWinner         string
		wantOrder          []string
		wantErrMsg         string
		wantRescueCalls    int
		wantCompatContains map[string]string
		wantFormatName     map[string]string
	}{
		{
			name:        "Bluey English Preferred ranks English and MULTI-with-English above Spanish and Japanese",
			title:       bluey,
			profileLang: "English",
			profileMode: core.AudioLanguageModePreferred,
			pool:        blueyPool,
			wantWinner:  "bluey-en",
			wantOrder:   []string{"bluey-en", "bluey-multi-en", "bluey-es", "bluey-fr", "bluey-ja"},
			wantCompatContains: map[string]string{
				"bluey-multi-en": "preferred language (English)",
				"bluey-en":       "preferred language (English)",
				"bluey-es":       "other language (fallback)",
				"bluey-ja":       "other language (fallback)",
			},
			wantFormatName: map[string]string{
				"bluey-multi-en": "preferred language (English)",
				"bluey-en":       "preferred language (English)",
			},
		},
		{
			name:        "Bluey English Preferred counts MULTI release including English as matching over larger Spanish/Japanese",
			title:       bluey,
			profileLang: "English",
			profileMode: core.AudioLanguageModePreferred,
			pool:        []core.Release{blueyPool[0], blueyPool[1], blueyPool[3]}, // es (900MB), ja (850MB), multi-en (500MB)
			wantWinner:  "bluey-multi-en",
			wantOrder:   []string{"bluey-multi-en", "bluey-es", "bluey-ja"},
			wantCompatContains: map[string]string{
				"bluey-multi-en": "preferred language (English)",
				"bluey-es":       "other language (fallback)",
				"bluey-ja":       "other language (fallback)",
			},
			wantFormatName: map[string]string{
				"bluey-multi-en": "preferred language (English)",
			},
		},
		{
			name:        "Bluey English Preferred falls back to Spanish/Japanese only when no English exists",
			title:       bluey,
			profileLang: "English",
			profileMode: core.AudioLanguageModePreferred,
			pool:        []core.Release{blueyPool[0], blueyPool[1]}, // Spanish (900MB) and Japanese (850MB) only
			wantWinner:  "bluey-es",
			wantOrder:   []string{"bluey-es", "bluey-ja"},
			wantCompatContains: map[string]string{
				"bluey-es": "other language (fallback)",
				"bluey-ja": "other language (fallback)",
			},
		},
		{
			name:        "Korean show with English Preferred plays Korean original ahead of French fallback",
			title:       koreanShow,
			profileLang: "English",
			profileMode: core.AudioLanguageModePreferred,
			pool:        koreanOnlyPool,
			wantWinner:  "ko-orig",
			wantOrder:   []string{"ko-orig", "ko-untagged", "ko-fr"},
			wantCompatContains: map[string]string{
				"ko-orig": "other language (fallback)",
				"ko-fr":   "other language (fallback)",
			},
		},
		{
			name:              "Korean show with English Preferred still boosts English even without FormatViewerLanguage in CustomFormatScores",
			title:             koreanShow,
			profileLang:       "English",
			profileMode:       core.AudioLanguageModePreferred,
			stripViewerLangCF: true,
			pool:              koreanWithDubPool,
			wantWinner:        "ko-multi-en",
			wantOrder:         []string{"ko-multi-en", "ko-en-dub", "ko-multi-es-fr", "ko-orig", "ko-fr", "ko-es"},
			wantFormatName: map[string]string{
				"ko-multi-en": "preferred language (English)",
				"ko-en-dub":   "preferred language (English)",
			},
		},
		{
			name:            "Korean show with English Required fails fast without rescue search and explains how to allow other languages",
			title:           koreanShow,
			profileLang:     "English",
			profileMode:     core.AudioLanguageModeRequired,
			pool:            koreanOnlyPool,
			wantErrMsg:      "No English release found; set Audio language to Preferred to allow other languages",
			wantRescueCalls: 0,
		},
		{
			name:            "Korean show with unset audioLanguageMode keeps existing Required strictness and fails fast",
			title:           koreanShow,
			profileLang:     "English",
			profileMode:     "",
			pool:            koreanOnlyPool,
			wantErrMsg:      "No English release found; set Audio language to Preferred to allow other languages",
			wantRescueCalls: 0,
		},
		{
			name:        "Interplay: Bluey English Preferred + viewer [ja,es] puts English first and refines fallback to Japanese over Spanish",
			title:       bluey,
			profileLang: "English",
			profileMode: core.AudioLanguageModePreferred,
			user:        &core.User{ID: 7, AudioLanguages: []string{"ja", "es"}},
			pool:        blueyPool,
			wantWinner:  "bluey-en",
			wantOrder:   []string{"bluey-en", "bluey-multi-en", "bluey-ja", "bluey-es", "bluey-fr"},
			wantFormatName: map[string]string{
				"bluey-en": "preferred language (English)",
				"bluey-ja": "#2, japanese",
				"bluey-es": "#3, spanish",
			},
		},
		{
			name:        "Interplay: Bluey English Preferred + viewer [ja,es] falls back to Japanese ahead of Spanish when English is absent",
			title:       bluey,
			profileLang: "English",
			profileMode: core.AudioLanguageModePreferred,
			user:        &core.User{ID: 7, AudioLanguages: []string{"ja", "es"}},
			pool:        []core.Release{blueyPool[0], blueyPool[1], blueyPool[4]}, // es, ja, fr
			wantWinner:  "bluey-ja",
			wantOrder:   []string{"bluey-ja", "bluey-es", "bluey-fr"},
		},
		{
			name:        "Interplay: Korean show English Preferred + viewer [es,en] deduplicates English at #1, refines Spanish to #2, Korean original to #3",
			title:       koreanShow,
			profileLang: "English",
			profileMode: core.AudioLanguageModePreferred,
			user:        &core.User{ID: 8, AudioLanguages: []string{"es", "en"}},
			pool:        koreanWithDubPool,
			wantWinner:  "ko-multi-en",
			wantOrder:   []string{"ko-multi-en", "ko-en-dub", "ko-multi-es-fr", "ko-es", "ko-orig", "ko-fr"},
			wantFormatName: map[string]string{
				"ko-en-dub":      "preferred language (English)",
				"ko-multi-es-fr": "#2, spanish",
				"ko-es":          "#2, spanish",
			},
		},
		{
			name:            "Interplay: Korean show English Required stays strict even when viewer asks for Korean",
			title:           koreanShow,
			profileLang:     "English",
			profileMode:     core.AudioLanguageModeRequired,
			user:            &core.User{ID: 9, AudioLanguages: []string{"ko"}},
			pool:            koreanOnlyPool,
			wantErrMsg:      "No English release found; set Audio language to Preferred to allow other languages",
			wantRescueCalls: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg()
			for i := range cfg.Quality.Profiles {
				cfg.Quality.Profiles[i].Language = tc.profileLang
				cfg.Quality.Profiles[i].AudioLanguageMode = tc.profileMode
				if tc.stripViewerLangCF {
					delete(cfg.Quality.Profiles[i].CustomFormatScores, core.FormatViewerLanguage)
				}
			}
			idx := &rescueCountingIndexer{fakeIndexer: fakeIndexer{releases: tc.pool}}
			svc := New(idx, fakeHealth{}, nil, cfg, Options{Now: func() time.Time { return corpusNow }})
			defer svc.Close()

			caps := appleTV4K()
			req := core.ResolveRequest{Title: tc.title, Client: caps, User: tc.user, Exclude: tc.exclude}
			res, err := svc.Resolve(context.Background(), req)

			if tc.wantErrMsg != "" {
				if err == nil {
					t.Fatalf("Resolve() succeeded with %q, want error %q", res.Release.ID, tc.wantErrMsg)
				}
				var reqLangErr *core.RequiredAudioLanguageError
				if !strings.Contains(err.Error(), tc.wantErrMsg) {
					t.Fatalf("Resolve() err = %q, want substring %q", err.Error(), tc.wantErrMsg)
				}
				if !errors.As(err, &reqLangErr) {
					t.Fatalf("Resolve() err = %T (%v), want *core.RequiredAudioLanguageError", err, err)
				}
				if !errors.Is(err, core.ErrNotAvailable) {
					t.Fatalf("RequiredAudioLanguageError must unwrap to core.ErrNotAvailable")
				}
				if idx.rescueCalls != tc.wantRescueCalls {
					t.Fatalf("SearchRescue calls = %d, want %d (must fail fast)", idx.rescueCalls, tc.wantRescueCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() err = %v", err)
			}
			if res.Release.ID != tc.wantWinner {
				t.Fatalf("winner = %q, want %q", res.Release.ID, tc.wantWinner)
			}

			profile := SelectProfileForMedia(cfg.Quality, caps, tc.user, titleMedia(tc.title))
			var tr Trace
			cands, _, rerr := svc.rankCandidates(context.Background(), req, searchRequestFor(tc.title), profile, slices.Clone(tc.pool), &tr, "")
			if rerr != nil {
				t.Fatalf("rankCandidates: %v", rerr)
			}
			if len(tc.wantOrder) > 0 {
				gotOrder := make([]string, len(cands))
				for i, c := range cands {
					gotOrder[i] = c.rel.ID
				}
				if !slices.Equal(gotOrder, tc.wantOrder) {
					t.Fatalf("order = %v, want %v", gotOrder, tc.wantOrder)
				}
			}
			for id, wantSub := range tc.wantCompatContains {
				ct := findTraceCandidate(t, tr, id)
				if !strings.Contains(ct.CompatibilityReason, wantSub) {
					t.Errorf("%s: CompatibilityReason = %q, want substring %q", id, ct.CompatibilityReason, wantSub)
				}
			}
			for id, wantSub := range tc.wantFormatName {
				ct := findTraceCandidate(t, tr, id)
				found := false
				for _, fm := range ct.CustomFormats {
					if fm.ID == core.FormatViewerLanguage && strings.Contains(fm.Name, wantSub) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s: CustomFormats = %+v, want %s containing %q", id, ct.CustomFormats, core.FormatViewerLanguage, wantSub)
				}
			}
		})
	}
}
