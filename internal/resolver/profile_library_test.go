package resolver

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// TestSelectProfileForTitlePrecedence pins the unified ordered rules
// precedence: first matching profile-setting rule wins, DefaultAway applies
// away from home when no rule matches, and an unknown profile name falls
// through.
func TestSelectProfileForTitlePrecedence(t *testing.T) {
	profiles := []core.QualityProfile{
		{Name: "Default", MaxHeight: 2160}, {Name: "Anime", MaxHeight: 1080},
		{Name: "Away", MaxHeight: 720}, {Name: "Rule", MaxHeight: 480}, {Name: "Travel", MaxHeight: 576},
	}
	title := &core.Title{Key: core.EpisodeKey(2, 1, 1), Type: core.TypeEpisode}
	inAnime := func(id core.LibraryID) bool { return id == 7 }
	inNone := func(core.LibraryID) bool { return false }
	remote := core.ClientCaps{Remote: true}
	tests := []struct {
		name  string
		q     config.Quality
		caps  core.ClientCaps
		inLib Membership
		want  string
	}{
		{
			name: "library rule wins over Default",
			q: config.Quality{
				Profiles: profiles, Default: "Default", DefaultAway: "Away",
				Rules: []config.QualityRule{{ID: "r_1", Libraries: []int64{7}, Profile: "Anime"}},
			},
			inLib: inAnime, want: "Anime",
		},
		{
			name: "no matching rule uses Default",
			q: config.Quality{
				Profiles: profiles, Default: "Default", DefaultAway: "Away",
				Rules: []config.QualityRule{{ID: "r_1", Libraries: []int64{7}, Profile: "Anime"}},
			},
			inLib: inNone, want: "Default",
		},
		{
			name: "rule naming unknown profile falls through to Default",
			q: config.Quality{
				Profiles: profiles, Default: "Default",
				Rules: []config.QualityRule{{ID: "r_1", Libraries: []int64{7}, Profile: "Nope"}},
			},
			inLib: inAnime, want: "Default",
		},
		{
			name: "earlier device rule beats library rule",
			q: config.Quality{
				Profiles: profiles, Default: "Default",
				Rules: []config.QualityRule{
					{ID: "r_1", Devices: []config.DeviceMatch{{Contains: "Apple TV"}}, Profile: "Rule"},
					{ID: "r_2", Libraries: []int64{7}, Profile: "Anime"},
				},
			},
			caps: core.ClientCaps{Product: "Apple TV"}, inLib: inAnime, want: "Rule",
		},
		{
			name: "away rule above library rule beats library rule",
			q: config.Quality{
				Profiles: profiles, Default: "Default",
				Rules: []config.QualityRule{
					{ID: "r_1", Where: config.WhereAway, Profile: "Away"},
					{ID: "r_2", Libraries: []int64{7}, Profile: "Anime"},
				},
			},
			caps: remote, inLib: inAnime, want: "Away",
		},
		{
			name: "library away rule beats DefaultAway",
			q: config.Quality{
				Profiles: profiles, Default: "Default", DefaultAway: "Away",
				Rules: []config.QualityRule{
					{ID: "r_1", Libraries: []int64{7}, Where: config.WhereAway, Profile: "Travel"},
					{ID: "r_2", Libraries: []int64{7}, Profile: "Anime"},
				},
			},
			caps: remote, inLib: inAnime, want: "Travel",
		},
		{
			name: "with no DefaultAway, away uses matching library rule",
			q: config.Quality{
				Profiles: profiles, Default: "Default",
				Rules: []config.QualityRule{{ID: "r_1", Libraries: []int64{7}, Profile: "Anime"}},
			},
			caps: remote, inLib: inAnime, want: "Anime",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectProfileForTitle(tc.q, tc.caps, nil, title, tc.inLib)
			if got.Name != tc.want {
				t.Fatalf("SelectProfileForTitle = %q, want %q", got.Name, tc.want)
			}
		})
	}
}

type invariantLibraryProfile struct {
	member bool
	key    string
}

func (p *invariantLibraryProfile) InLibrary(context.Context, *core.Title, core.LibraryID) bool {
	return p.member
}
func (p *invariantLibraryProfile) PolicyKey(context.Context) string { return p.key }

func TestLibraryProfileEvidenceInvariant(t *testing.T) {
	cfg := testCfg()
	cfg.Quality = config.Quality{
		Profiles: []core.QualityProfile{
			{Name: "Home", MaxHeight: 2160, AllowHDR: true},
			{Name: "Library", MaxHeight: 720},
		},
		Default: "Home",
		Rules:   []config.QualityRule{{ID: "r_lib7", Libraries: []int64{7}, Profile: "Library"}},
	}
	lookup := &invariantLibraryProfile{member: true, key: "v1"}
	s := New(&fakeIndexer{}, nil, nil, cfg, Options{LibraryProfiles: lookup})
	defer s.Close()
	ctx := t.Context()
	title := matrixTitle()
	r := rel("1080", "The.Signal.1999.1080p.WEB-DL.x264-GRP", 4000)
	r.Info = releaseparse.Parse(r.Name)
	_, reason, err := s.FeedCandidate(ctx, title, r)
	if err != nil || reason == "" {
		t.Fatalf("feed ignored library: %q %v", reason, err)
	}
	ev, err := s.FeedEvaluation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := ev.Key()
	verdicts, err := ev.Evaluate(ctx, title, []core.Release{r})
	ev.Close()
	if err != nil || len(verdicts) != 1 || verdicts[0].Reason == "" {
		t.Fatalf("batch feed ignored library: %+v %v", verdicts, err)
	}
	s.setAvailability(ctx, int64(title.Key), false, time.Hour)
	lookup.member = false
	lookup.key = "v2"
	if known, _ := s.Available(ctx, title.Key); known {
		t.Fatal("old library availability survived profile change")
	}
	if got := s.KnownUnavailableSince(ctx, []core.RatingKey{title.Key}); len(got) != 0 {
		t.Fatalf("old negative policy survived: %v", got)
	}
	ev, err = s.FeedEvaluation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()
	if oldKey == ev.Key() {
		t.Fatal("library policy did not invalidate durable feed evidence")
	}
	verdicts, err = ev.Evaluate(ctx, title, []core.Release{r})
	if err != nil || verdicts[0].Reason != "" {
		t.Fatalf("home policy rejected release: %+v %v", verdicts, err)
	}
	for _, title := range []*core.Title{{Type: core.TypeShow}, {Type: core.TypeSeason}, {Key: core.ShowKey(1)}, {Key: core.SeasonKey(1, 1)}} {
		if got := titleMedia(title); got != core.MediaTV {
			t.Fatalf("show media = %q", got)
		}
	}
}
