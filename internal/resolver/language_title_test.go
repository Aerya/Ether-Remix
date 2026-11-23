package resolver

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// Retry only a known trailing tag and retain the remaining identity gates.
func TestTrailingLanguageTitleGate(t *testing.T) {
	title := &core.Title{Type: core.TypeMovie, Title: "Terkel in Trouble", OriginalTitle: "Terkel i knibe", Year: 2004,
		AlternativeTitles: []string{"Terkel i knipe"}}
	for _, tc := range []struct {
		parsed string
		year   int
		reject bool
	}{
		{"Terkel I Knipe NORWEGIAN", 0, false},
		{"Terkel I Knipe NORSK", 2004, false},
		{"Terkel I Knibe DANISH", 2004, false},
		{"Terkel in Trouble ENGLISH", 2004, false},
		{"Terkel I Knipe NORWEGIAN", 2020, true},
		{"Another Movie NORWEGIAN", 2004, true},
		{"Terkel I Knipe UNKNOWN", 2004, true},
		{"Terkel I Knipe NORWEGIAN Story", 2004, true},
	} {
		t.Run(tc.parsed, func(t *testing.T) {
			release := core.Release{Name: strings.ReplaceAll(tc.parsed, " ", "."), Info: core.ReleaseInfo{Title: tc.parsed, Year: tc.year}}
			reason := rejectIdentity(release, title, searchRequestFor(title), "en-US")
			if (reason != "") != tc.reject {
				t.Fatalf("rejection=%q, want rejected=%v", reason, tc.reject)
			}
		})
	}
}

func TestEnglishTitleIdentityMatching(t *testing.T) {
	ctx := context.Background()

	spirited := &core.Title{
		Key:           core.MovieKey(129),
		Type:          core.TypeMovie,
		IDs:           core.IDs{TMDB: 129, IMDB: "tt0245429"},
		Title:         "Chihiro og heksene",
		OriginalTitle: "千と千尋の神隠し",
		EnglishTitle:  "Spirited Away",
		Year:          2001,
		Language:      "ja",
		Runtime:       125 * time.Minute,
	}
	totoro := &core.Title{
		Key:           core.MovieKey(8392),
		Type:          core.TypeMovie,
		IDs:           core.IDs{TMDB: 8392, IMDB: "tt0096283"},
		Title:         "Min nabo Totoro",
		OriginalTitle: "となりのトトロ",
		EnglishTitle:  "My Neighbor Totoro",
		Year:          1988,
		Language:      "ja",
		Runtime:       86 * time.Minute,
	}

	for _, tc := range []struct {
		title *core.Title
		name  string
		want  string
	}{
		{spirited, "Spirited.Away.2001.1080p.BluRay.x264.ENGLISH-GRP", ""},
		{spirited, "Spirited.Away.2001.1080p.BluRay.x264.Dual.Audio-GRP", ""},
		{spirited, "Chihiro.og.heksene.2001.NORWEGIAN.1080p.BluRay.x264-GRP", ""},
		{spirited, "Spirited.Away.2001.NORWEGIAN.1080p.BluRay.x264-GRP", ""},
		{spirited, "Spirited.Away.2019.1080p.BluRay.x264-GRP", "year mismatch"},
		{spirited, "Howls.Moving.Castle.2004.1080p.BluRay.x264-GRP", `title mismatch (parsed "Howls Moving Castle"; expected "Chihiro og heksene", "千と千尋の神隠し" or "Spirited Away")`},
		{totoro, "My.Neighbor.Totoro.1988.1080p.BluRay.x264-GRP", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := core.Release{Name: tc.name, Info: releaseparse.Parse(tc.name), Protocol: core.ProtocolUsenet}
			if got := rejectIdentity(r, tc.title, searchRequestFor(tc.title), "nb-NO"); got != tc.want {
				t.Fatalf("rejectIdentity(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}

	cfg := testCfg()
	cfg.Catalog.Language = "nb-NO"
	for i := range cfg.Quality.Profiles {
		cfg.Quality.Profiles[i].PreferredLanguages = []string{"en"}
	}
	engMovieRel := parsedEpisodeRel("spirited-en", "Spirited.Away.2001.1080p.BluRay.x264.ENGLISH-GRP", 4500)
	movieSvc := New(&fakeIndexer{releases: []core.Release{engMovieRel}}, fakeHealth{}, nil, cfg, Options{})
	defer movieSvc.Close()

	res, err := movieSvc.Resolve(ctx, core.ResolveRequest{Title: spirited, Client: webCaps()})
	if err != nil {
		t.Fatalf("Resolve(Spirited Away) failed: %v (traces=%+v)", err, movieSvc.Traces())
	}
	if res.Release.ID != "spirited-en" {
		t.Fatalf("Resolve picked %q, want spirited-en", res.Release.ID)
	}
	if res.Release.Target == nil || !slices.Contains(res.Release.Target.MovieTitles, "Spirited Away") {
		t.Fatalf("MovieTitles = %+v, want Spirited Away included", res.Release.Target)
	}

	// TV episode with non-English ShowTitle + ShowEnglishTitle preserves
	// series-year and namesake ambiguity guards when another show shares EnglishTitle.
	ep := &core.Title{
		Key:               core.EpisodeKey(9001, 1, 1),
		Type:              core.TypeEpisode,
		ShowKey:           core.ShowKey(9001),
		ShowIDs:           core.IDs{TMDB: 9001},
		ShowTitle:         "Drømmenes rike",
		ShowOriginalTitle: "夢の国",
		ShowEnglishTitle:  "Dream Kingdom",
		ShowYear:          2021,
		Title:             "Episode 1",
		Season:            1,
		Episode:           1,
		Language:          "ja",
		Runtime:           45 * time.Minute,
	}
	wrongYear := parsedEpisodeRel("wrong-year", "Dream.Kingdom.2015.S01E01.1080p.WEB-DL-GRP", 1800)
	conflictYear := parsedEpisodeRel("conflict-2018", "Dream.Kingdom.2018.S01E01.1080p.WEB-DL-GRP", 1800)
	bareAmbig := parsedEpisodeRel("bare-ambig", "Dream.Kingdom.S01E01.1080p.WEB-DL-GRP", 1800)
	datedGood := parsedEpisodeRel("dated-2021", "Dream.Kingdom.2021.S01E01.1080p.WEB-DL-GRP", 1800)

	tvSvc := New(&fakeIndexer{releases: []core.Release{wrongYear, conflictYear, bareAmbig, datedGood}}, fakeHealth{}, nil, cfg, Options{
		SeriesNamesake: func(_ context.Context, show core.RatingKey, title string, year int) (bool, error) {
			return show == ep.ShowKey && title == "Dream Kingdom" && year == 2021, nil
		},
		SeriesYearConflict: func(_ context.Context, show core.RatingKey, title string, year int) (bool, error) {
			return show == ep.ShowKey && title == "Dream Kingdom" && (year == 2015 || year == 2018), nil
		},
		EpisodeIdentity: func(_ context.Context, show core.RatingKey, season, episode int, names []string) (core.EpisodeIdentity, error) {
			if show == ep.ShowKey && season == 1 && episode == 1 && slices.Contains(names, "Dream Kingdom") {
				return core.EpisodeIdentity{
					OtherShows: map[string][]core.EpisodeNamesake{
						"Dream Kingdom": {{Year: 2015, EpisodeTitle: "Pilot", Language: "en"}},
					},
				}, nil
			}
			return core.EpisodeIdentity{}, nil
		},
	})
	defer tvSvc.Close()

	tvRes, err := tvSvc.Resolve(ctx, core.ResolveRequest{Title: ep, Client: webCaps()})
	if err != nil {
		t.Fatalf("Resolve(Dream Kingdom) failed: %v (traces=%+v)", err, tvSvc.Traces())
	}
	if tvRes.Release.ID != "dated-2021" {
		t.Fatalf("Resolve picked %q, want dated-2021", tvRes.Release.ID)
	}
	tr := tvSvc.Traces()[0]
	if c := findTraceCandidate(t, tr, "wrong-year"); !c.Rejected || c.Reason != "release names another show's premiere year" {
		t.Fatalf("wrong-year candidate = %+v, want another show's premiere year rejection", c)
	}
	if c := findTraceCandidate(t, tr, "conflict-2018"); !c.Rejected || c.Reason != "series year mismatch" {
		t.Fatalf("conflict-2018 candidate = %+v, want series year mismatch", c)
	}
	if c := findTraceCandidate(t, tr, "bare-ambig"); !c.Rejected || !strings.Contains(c.Reason, "ambiguous") {
		t.Fatalf("bare-ambig candidate = %+v, want ambiguous rejection", c)
	}
}
