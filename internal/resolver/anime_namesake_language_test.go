package resolver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/stremio"
)

func onePieceAnimeEpisode() *core.Title {
	return &core.Title{
		Key:             core.EpisodeKey(37854, 1, 1),
		Type:            core.TypeEpisode,
		ShowKey:         core.ShowKey(37854),
		ShowTitle:       "One Piece",
		ShowYear:        1999,
		ShowIDs:         core.IDs{TMDB: 37854},
		Title:           "I'm Luffy! The Man Who's Gonna Be King of the Pirates!",
		Season:          1,
		Episode:         1,
		AbsoluteEpisode: 1,
		Language:        "ja",
		Genres:          []string{"Animation", "Action & Adventure"},
		Keywords:        []string{"anime"},
		Runtime:         24 * time.Minute,
	}
}

func onePieceLiveActionEpisode() *core.Title {
	return &core.Title{
		Key:       core.EpisodeKey(111110, 1, 1),
		Type:      core.TypeEpisode,
		ShowKey:   core.ShowKey(111110),
		ShowTitle: "One Piece",
		ShowYear:  2023,
		ShowIDs:   core.IDs{TMDB: 111110},
		Title:     "Romance Dawn",
		Season:    1,
		Episode:   1,
		Language:  "en",
		Genres:    []string{"Action & Adventure", "Sci-Fi & Fantasy"},
		Runtime:   60 * time.Minute,
	}
}

func englishOnlyRule() []core.ReleaseRule {
	return []core.ReleaseRule{{
		ID:         "eng-only",
		Name:       "English audio",
		Action:     "require",
		Conditions: []core.ReleaseCondition{{Field: "language", Operator: "equals", Value: "en"}},
	}}
}

func parsedEpisodeRel(id, name string, mb int64) core.Release {
	r := rel(id, name, mb)
	r.Info = releaseparse.Parse(name)
	return r
}

func findTraceCandidate(t *testing.T, tr Trace, idOrName string) CandidateTrace {
	t.Helper()
	for _, c := range tr.Candidates {
		if c.ReleaseID == idOrName || c.Name == idOrName {
			return c
		}
	}
	t.Fatalf("candidate %q not found in trace: %+v", idOrName, tr.Candidates)
	return CandidateTrace{}
}

func TestOnePieceAnimeNamesakeAndEnglishDubProfile(t *testing.T) {
	ctx := context.Background()
	anime := onePieceAnimeEpisode()

	dualAnime := parsedEpisodeRel("dual-anime", "[Group] One Piece - 001 [Dual Audio] [1080p]", 900)
	liveYear := parsedEpisodeRel("live-2023", "One Piece 2023 S01E01 1080p WEB-DL English-GRP", 1800)
	liveEp := parsedEpisodeRel("live-ep", "One.Piece.S01E01.Romance.Dawn.1080p.WEB-DL.English-GRP", 1800)
	bareShared := parsedEpisodeRel("bare-shared", "One.Piece.S01E01.1080p.WEB-DL.English-GRP", 1800)
	jpnOnly := parsedEpisodeRel("jpn-only", "[Group] One Piece - 001 [Japanese] [1080p]", 900)

	cfg := testCfg()
	for i := range cfg.Quality.Profiles {
		cfg.Quality.Profiles[i].ReleaseRules = englishOnlyRule()
	}
	svc := New(&fakeIndexer{releases: []core.Release{liveYear, liveEp, bareShared, jpnOnly, dualAnime}}, fakeHealth{}, nil, cfg, Options{
		SeriesNamesake: func(context.Context, core.RatingKey, string, int) (bool, error) {
			return true, nil
		},
		EpisodeIdentity: func(_ context.Context, show core.RatingKey, season, episode int, _ []string) (core.EpisodeIdentity, error) {
			if show == anime.ShowKey && season == 1 && episode == 1 {
				return core.EpisodeIdentity{
					Genres:   []string{"Animation", "Action & Adventure"},
					Keywords: []string{"anime"},
					OtherShows: map[string][]core.EpisodeNamesake{
						"One Piece": {{
							Year:         2023,
							EpisodeTitle: "Romance Dawn",
							Language:     "en",
							Genres:       []string{"Action & Adventure", "Sci-Fi & Fantasy"},
						}},
					},
				}, nil
			}
			return core.EpisodeIdentity{}, nil
		},
	})
	defer svc.Close()

	got, err := svc.Resolve(ctx, core.ResolveRequest{Title: anime, Client: webCaps()})
	if err != nil {
		t.Fatalf("Resolve failed: %v (traces=%+v)", err, svc.Traces())
	}
	if got.Release.ID != "dual-anime" {
		t.Fatalf("Resolve picked %q (%s), want dual-anime", got.Release.ID, got.Release.Name)
	}

	hist := svc.Traces()
	if len(hist) != 1 {
		t.Fatalf("expected 1 trace entry, got %d", len(hist))
	}
	tr := hist[0]

	winnerTrace := findTraceCandidate(t, tr, "dual-anime")
	if winnerTrace.Rejected {
		t.Fatalf("dual-anime should not be rejected: %+v", winnerTrace)
	}
	if !strings.Contains(winnerTrace.CompatibilityReason, AnimeReleaseStyleIdentityReason) {
		t.Fatalf("winner CompatibilityReason %q missing %q", winnerTrace.CompatibilityReason, AnimeReleaseStyleIdentityReason)
	}
	if !strings.Contains(winnerTrace.CompatibilityReason, "audio language unverified:") || !strings.Contains(winnerTrace.CompatibilityReason, "verified by media probe before open") {
		t.Fatalf("winner CompatibilityReason %q missing unverified-audio trace note", winnerTrace.CompatibilityReason)
	}

	if ct := findTraceCandidate(t, tr, "live-2023"); !ct.Rejected || ct.Reason != "release names another show's premiere year" {
		t.Fatalf("live-2023 trace = %+v", ct)
	}
	if ct := findTraceCandidate(t, tr, "live-ep"); !ct.Rejected || ct.Reason != "release names another show's episode" {
		t.Fatalf("live-ep trace = %+v", ct)
	}
	if ct := findTraceCandidate(t, tr, "bare-shared"); !ct.Rejected || !strings.HasPrefix(ct.Reason, "identity ambiguous:") || !strings.Contains(ct.Reason, "anime release style") {
		t.Fatalf("bare-shared trace = %+v", ct)
	}
	if ct := findTraceCandidate(t, tr, "jpn-only"); !ct.Rejected || !strings.HasPrefix(ct.Reason, "audio language absent:") || !strings.Contains(ct.Reason, "needs declared english audio") {
		t.Fatalf("jpn-only trace = %+v", ct)
	}
}

func TestReverseLiveActionNamesakeRejectsAnimeStyle(t *testing.T) {
	ctx := context.Background()
	live := onePieceLiveActionEpisode()

	animeDual := parsedEpisodeRel("anime-dual", "[Group] One Piece - 001 [Dual Audio] [1080p]", 900)
	animeCRC := parsedEpisodeRel("anime-crc", "One Piece S01E01 1080p WEB-DL [A1B2C3D4]", 900)
	animeBD := parsedEpisodeRel("anime-bd", "One Piece S01E01 1080p BD FLAC", 1200)
	animeBatch := parsedEpisodeRel("anime-batch", "One Piece S01 Batch 1080p HEVC", 8000)
	animeBatch.Protocol = core.ProtocolTorrent
	liveRelease := parsedEpisodeRel("live-ok", "One.Piece.2023.S01E01.1080p.WEB-DL.DDP5.1.H.264-FLUX", 1800)

	cfg := testCfg()
	svc := New(&fakeIndexer{releases: []core.Release{animeDual, animeCRC, animeBD, animeBatch, liveRelease}}, fakeHealth{}, nil, cfg, Options{
		SeriesNamesake: func(context.Context, core.RatingKey, string, int) (bool, error) {
			return true, nil
		},
		EpisodeIdentity: func(_ context.Context, show core.RatingKey, _, _ int, _ []string) (core.EpisodeIdentity, error) {
			if show == live.ShowKey {
				return core.EpisodeIdentity{
					Genres: []string{"Action & Adventure", "Sci-Fi & Fantasy"},
					OtherShows: map[string][]core.EpisodeNamesake{
						"One Piece": {{
							Year:         1999,
							EpisodeTitle: "I'm Luffy! The Man Who's Gonna Be King of the Pirates!",
							Language:     "ja",
							Genres:       []string{"Animation", "Action & Adventure"},
						}},
					},
				}, nil
			}
			return core.EpisodeIdentity{}, nil
		},
	})
	defer svc.Close()

	got, err := svc.Resolve(ctx, core.ResolveRequest{Title: live, Client: webCaps()})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got.Release.ID != "live-ok" {
		t.Fatalf("Resolve picked %q, want live-ok", got.Release.ID)
	}
	tr := svc.Traces()[0]
	for _, idOrName := range []string{"anime-dual", "anime-crc", "anime-bd", animeBatch.Name} {
		ct := findTraceCandidate(t, tr, idOrName)
		if !ct.Rejected || !strings.HasPrefix(ct.Reason, "identity ambiguous: anime release style identifies the animated namesake; needs series year 2023") {
			t.Fatalf("%s should be rejected as animated namesake, got %+v", idOrName, ct)
		}
	}
}

func TestUnknownAudioRanksAfterConfirmedAndCapsAtFour(t *testing.T) {
	ctx := context.Background()
	anime := onePieceAnimeEpisode()

	confirmed720 := parsedEpisodeRel("confirmed-720", "[Group] One Piece - 001 [Eng Dub] [720p]", 500)
	var unverified []core.Release
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("unv-%d", i)
		name := fmt.Sprintf("[Group%d] One Piece - 001 [1080p]", i)
		if i == 1 {
			name = fmt.Sprintf("[Group%d] One Piece - 001 [Dual Audio] [1080p]", i)
		}
		unverified = append(unverified, parsedEpisodeRel(id, name, int64(1200-i*10)))
	}
	all := append(append([]core.Release{}, unverified...), confirmed720)

	cfg := testCfg()
	for i := range cfg.Quality.Profiles {
		cfg.Quality.Profiles[i].ReleaseRules = englishOnlyRule()
	}
	svc := New(&fakeIndexer{releases: all}, fakeHealth{}, nil, cfg, Options{
		EpisodeIdentity: func(_ context.Context, _ core.RatingKey, _, _ int, _ []string) (core.EpisodeIdentity, error) {
			return core.EpisodeIdentity{
				Genres: []string{"Animation"},
				OtherShows: map[string][]core.EpisodeNamesake{
					"One Piece": {{Year: 2023, Genres: []string{"Action & Adventure"}}},
				},
			}, nil
		},
	})
	defer svc.Close()

	sreq := svc.numberedRequest(ctx, searchRequestFor(anime), true)
	profile := SelectProfileForMedia(cfg.Quality, webCaps(), nil, titleMedia(anime))
	var tr Trace
	cands, _, err := svc.rankCandidates(ctx, core.ResolveRequest{Title: anime, Client: webCaps()}, sreq, profile, all, &tr, "")
	if err != nil {
		t.Fatal(err)
	}
	// 1 confirmed + 4 unverified (capped at MaxUnknownAudioProbes=4) = 5 candidates.
	if len(cands) != 1+MaxUnknownAudioProbes {
		t.Fatalf("got %d ranked candidates, want %d: %+v", len(cands), 1+MaxUnknownAudioProbes, cands)
	}
	if cands[0].rel.ID != "confirmed-720" {
		t.Fatalf("confirmed 720p English dub did not rank first: got %s", cands[0].rel.ID)
	}
	if cands[1].rel.ID != "unv-1" {
		t.Fatalf("Dual Audio candidate did not lead unverified tier: got %s", cands[1].rel.ID)
	}
	for _, id := range []string{"unv-5", "unv-6"} {
		ct := findTraceCandidate(t, tr, id)
		if !ct.Rejected || !strings.Contains(ct.Reason, "audio language unverified:") || !strings.Contains(ct.Reason, "4-candidate probe limit reached") {
			t.Fatalf("%s trace = %+v", id, ct)
		}
	}

	// Excluding two already-probed unverified candidates leaves budget for only 2 more.
	var tr2 Trace
	cands2, _, err := svc.rankCandidates(ctx, core.ResolveRequest{
		Title:   anime,
		Client:  webCaps(),
		Exclude: []string{"confirmed-720", "unv-1", "unv-2"},
	}, sreq, profile, all, &tr2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands2) != 2 || cands2[0].rel.ID != "unv-3" || cands2[1].rel.ID != "unv-4" {
		t.Fatalf("remaining probe budget after 2 exclusions = %+v, want [unv-3, unv-4]", cands2)
	}
}

func TestOnePiece750StremioAliasFixtureAndNamesakeIdentity(t *testing.T) {
	const (
		hashAbs  = "1111111111111111111111111111111111111111"
		hashTVDB = "2222222222222222222222222222222222222222"
		absTitle = "One.Piece.-.750.-.ENG.UNCUT.1080p.WEB.H.264.-NanDesuKa.(FUNi)"
		tvdbName = "[Judas] One Piece - S18E04 [Dual Audio] [1080p]"
	)
	for _, mode := range []string{"automatic", "group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var mu sync.Mutex
			var paths []string
			addonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/manifest.json" {
					_, _ = w.Write([]byte(`{"id":"org.example.torrentio","version":"1.0.0","name":"Torrentio","resources":[{"name":"stream","types":["series","movie"],"idPrefixes":["tt"]}],"types":["series","movie"]}`))
					return
				}
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				switch r.URL.Path {
				case "/stream/series/tt0388629:1:750.json":
					_, _ = fmt.Fprintf(w, `{"streams":[{"name":"[RD+] Torrentio 1080p","title":%q,"url":"https://debrid.example/play/abs.mkv","infoHash":%q,"behaviorHints":{"videoSize":1258291200}}]}`, absTitle, hashAbs)
				case "/stream/series/tt0388629:18:4.json":
					_, _ = fmt.Fprintf(w, `{"streams":[{"name":"[RD+] Torrentio 1080p","title":%q,"url":"https://debrid.example/play/tvdb.mkv","infoHash":%q,"behaviorHints":{"videoSize":943718400}}]}`, tvdbName, hashTVDB)
				default:
					_, _ = w.Write([]byte(`{"streams":[]}`))
				}
			}))
			defer addonSrv.Close()

			addons := stremio.NewServiceWithOptions(stremio.Options{Client: addonSrv.Client()})
			ixCfg := config.Indexer{ID: "stremio-op", Name: "Torrentio", Kind: config.IndexerKindStremio, URL: addonSrv.URL, Enabled: true}
			mgr := newznab.NewManager(nil, []config.Indexer{ixCfg}, newznab.Options{
				SearchCacheTTL: time.Minute,
				Backend:        addons.Backend,
			})
			debridIdx := debridStubIndexer{Indexer: mgr}

			ep := onePieceAnimeEpisode()
			ep.Key = core.EpisodeKey(37854, 1, 750)
			ep.ShowIDs = core.IDs{IMDB: "tt0388629", TMDB: 37854, TVDB: 81797}
			ep.Title = "A Desperate Situation! Luffy Fights a Battle in Extreme Heat!"
			ep.Season = 1
			ep.Episode = 750
			ep.AbsoluteEpisode = 750

			cfg := testCfg()
			for i := range cfg.Quality.Profiles {
				cfg.Quality.Profiles[i].ReleaseRules = englishOnlyRule()
			}
			svc := New(debridIdx, fakeHealth{}, nil, cfg, Options{
				EpisodeNumbering: func(_ context.Context, req core.SearchRequest, cachedOnly bool) (core.ReleaseNumbering, error) {
					if mode == "group" {
						if cachedOnly {
							return core.ReleaseNumbering{Explicit: true}, nil
						}
						return core.ReleaseNumbering{
							Explicit: true,
							Aliases:  []core.EpisodeNumber{{Season: 18, Episode: 4}},
						}, nil
					}
					if cachedOnly {
						return core.ReleaseNumbering{}, nil
					}
					return core.ReleaseNumbering{
						Aliases: []core.EpisodeNumber{{Season: 18, Episode: 4}},
					}, nil
				},
				SeriesNamesake: func(context.Context, core.RatingKey, string, int) (bool, error) {
					return true, nil
				},
				EpisodeIdentity: func(_ context.Context, show core.RatingKey, season, episode int, _ []string) (core.EpisodeIdentity, error) {
					if show == ep.ShowKey && season == 1 && episode == 750 {
						return core.EpisodeIdentity{
							Genres:   []string{"Animation", "Action & Adventure"},
							Keywords: []string{"anime"},
							OtherShows: map[string][]core.EpisodeNamesake{
								"One Piece": {{
									Year:     2023,
									Language: "en",
									Genres:   []string{"Action & Adventure", "Sci-Fi & Fantasy"},
								}},
							},
						}, nil
					}
					return core.EpisodeIdentity{}, nil
				},
			})
			defer svc.Close()

			got, err := svc.Resolve(ctx, core.ResolveRequest{Title: ep, Client: webCaps()})
			if err != nil {
				t.Fatalf("Resolve(%s) failed: %v (traces=%+v)", mode, err, svc.Traces())
			}

			mu.Lock()
			asked := append([]string(nil), paths...)
			mu.Unlock()
			if len(asked) != 2 || asked[0] != "/stream/series/tt0388629:1:750.json" || asked[1] != "/stream/series/tt0388629:18:4.json" {
				t.Fatalf("mode %s asked paths = %v, want canonical then alias", mode, asked)
			}

			// Confirmed English NanDesuKa release ranks first ahead of unverified Dual Audio S18E04,
			// and both candidates are accepted without namesake or episode-number rejection.
			if got.Release.Name != absTitle {
				t.Fatalf("winner = %q, want %q", got.Release.Name, absTitle)
			}
			if got.Release.Target == nil || got.Release.Target.CanonicalSeason != 1 || got.Release.Target.CanonicalEpisode != 750 || len(got.Release.Target.Aliases) != 1 || got.Release.Target.Aliases[0] != (core.EpisodeNumber{Season: 18, Episode: 4}) {
				t.Fatalf("winner target missing alias metadata: %+v", got.Release.Target)
			}
			if len(got.Alternates) != 1 || got.Alternates[0].Name != tvdbName {
				t.Fatalf("alternates = %+v, want [%q]", got.Alternates, tvdbName)
			}
			if altTgt := got.Alternates[0].Target; altTgt == nil || altTgt.Season != 18 || altTgt.Episode != 4 || altTgt.CanonicalSeason != 1 || altTgt.CanonicalEpisode != 750 {
				t.Fatalf("alternate target = %+v, want Season=18 Episode=4 Canonical=1:750", altTgt)
			}

			tr := svc.Traces()[0]
			for _, name := range []string{absTitle, tvdbName} {
				ct := findTraceCandidate(t, tr, name)
				if ct.Rejected {
					t.Fatalf("mode %s unexpectedly rejected %q: %+v", mode, name, ct)
				}
			}
		})
	}
}

type debridStubIndexer struct {
	core.Indexer
}

func (d debridStubIndexer) FetchNZB(_ context.Context, rel core.Release) ([]byte, error) {
	return []byte("debrid:" + rel.ID), nil
}
