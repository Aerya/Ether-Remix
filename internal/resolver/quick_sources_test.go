package resolver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/stremio"
)

func TestQuickSeedstremAvailabilityNeverStartsDownload(t *testing.T) {
	var opens atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { opens.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	hash := "0123456789abcdef0123456789abcdef01234567"
	link := server.URL + "/stremio/play/" + hash + "?magnet=" + url.QueryEscape("magnet:?xt=urn:btih:"+hash)
	seed := stremio.Convert([]stremio.Stream{{Name: "Seedstrem", Title: "The.Signal.1999.1080p.WEB-DL.H.264", URL: link}}, core.SearchRequest{Type: core.TypeMovie}, stremio.Lookup{}, stremio.ConvertOptions{Origin: server.URL, Instance: "seed"}).Releases
	if len(seed) != 1 || seed[0].MagnetURL != "" || seed[0].Protocol != core.ProtocolHTTP {
		t.Fatal("Seedstrem link converted to torrent")
	}
	seed[0].ID = "seed-http"
	cfg := testCfg()
	cfg.Resolver.SourceOrder.Quick = config.QuickSources{Enabled: true, Order: []string{"seedstrem", "usenet"}, SeedstremID: "seed"}
	good := rel("news-good", "The.Signal.1999.1080p.WEB-DL.H.264", 6000)
	good.Protocol = core.ProtocolUsenet
	ix := debrid.NewIndexer(&fakeIndexer{}, debrid.NewService(config.Debrid{}, debrid.Options{}))
	svc := New(ix, fakeHealth{}, nil, cfg, Options{QuickSearch: func(_ context.Context, _ core.SearchRequest, source, _ string) ([]core.Release, error) {
		if source == "seedstrem" {
			return seed, nil
		}
		return []core.Release{good}, nil
	}})
	defer svc.Close()
	for _, purpose := range []string{core.IndexerPurposeAvailability, core.IndexerPurposeDetail, core.IndexerPurposeWarmup} {
		ctx := core.WithOnDemandStreamsForAvailability(core.WithIndexerPurpose(t.Context(), purpose))
		got, err := svc.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
		if err != nil || got.Release.ID != "news-good" {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	}
	if opens.Load() != 0 {
		t.Fatalf("opened Seedstrem %d times", opens.Load())
	}
}

func TestQuickSeriesAndAnime(t *testing.T) {
	for _, anime := range []bool{false, true} {
		title := &core.Title{Key: core.EpisodeKey(1396, 2, 3), Type: core.TypeEpisode, ShowIDs: core.IDs{TMDB: 1396}, ShowTitle: "Boundary Bay", Title: "Episode", Season: 2, Episode: 3, Runtime: 45 * time.Minute}
		if anime {
			title.Keywords = []string{"anime"}
		}
		cfg := testCfg()
		cfg.Resolver.SourceOrder.Quick = config.QuickSources{Enabled: true, Order: []string{"usenet"}}
		svc := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{QuickSearch: func(_ context.Context, req core.SearchRequest, source, _ string) ([]core.Release, error) {
			if req.IDs.TMDB != 1396 || req.Season != 2 || req.Episode != 3 || req.Anime != anime {
				t.Fatalf("wrong request: %+v", req)
			}
			r := rel("episode", "Boundary.Bay.S02E03.1080p.WEB-DL.H.264", 2500)
			r.Protocol = core.ProtocolUsenet
			return []core.Release{r}, nil
		}})
		defer svc.Close()
		got, err := svc.Resolve(t.Context(), core.ResolveRequest{Title: title, Client: webCaps()})
		if err != nil || got.Release.ID != "episode" {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	}
}

func TestQuickCancellationStopsFallback(t *testing.T) {
	cfg := testCfg()
	cfg.Resolver.SourceOrder.Quick = config.QuickSources{Enabled: true, Order: []string{"alko", "usenet"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	svc := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{QuickSearch: func(context.Context, core.SearchRequest, string, string) ([]core.Release, error) {
		calls++
		cancel()
		return nil, ctx.Err()
	}})
	defer svc.Close()
	_, err := svc.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestQuickTriesAllUsableReleasesBeforeNextSource(t *testing.T) {
	cfg := testCfg()
	cfg.Resolver.SourceOrder.Quick = config.QuickSources{Enabled: true, Order: []string{"usenet", "alko"}}
	calls := 0
	svc := New(&fakeIndexer{}, fakeHealth{unhealthy: map[string]bool{"first-bad": true}}, nil, cfg, Options{QuickSearch: func(_ context.Context, _ core.SearchRequest, source, _ string) ([]core.Release, error) {
		calls++
		if source != "usenet" {
			t.Fatal("left source before trying its next release")
		}
		a := rel("first-bad", "The.Signal.1999.1080p.BluRay.H.264", 6000)
		a.Protocol = core.ProtocolUsenet
		b := rel("second-good", "The.Signal.1999.1080p.WEB-DL.H.264", 6000)
		b.Protocol = core.ProtocolUsenet
		return []core.Release{a, b}, nil
	}})
	defer svc.Close()
	got, err := svc.Resolve(t.Context(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
	if err != nil || got.Release.ID != "second-good" || calls != 1 {
		t.Fatalf("got=%+v err=%v calls=%d", got, err, calls)
	}
}

func TestQuickSourceOrderAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []string
		bad   map[string]bool
		want  string
		calls []string
	}{
		{"first source wins", []string{"usenet", "alko"}, nil, "usenet-release", []string{"usenet"}},
		{"reordered", []string{"alko", "usenet"}, nil, "sfr-alko:file", []string{"alko"}},
		{"failed playback", []string{"alko", "usenet"}, map[string]bool{"sfr-alko:file": true}, "usenet-release", []string{"alko", "usenet"}},
		{"final addons", []string{"alko", "usenet"}, map[string]bool{"sfr-alko:file": true, "usenet-release": true}, "backup", []string{"alko", "usenet", "addons"}},
		{"duplicate source", []string{"alko", "alko", "usenet"}, map[string]bool{"sfr-alko:file": true}, "usenet-release", []string{"alko", "usenet"}},
		{"unconfigured", []string{"seedstrem", "usenet"}, nil, "usenet-release", []string{"seedstrem", "usenet"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg()
			cfg.Resolver.SourceOrder.Quick = config.QuickSources{Enabled: true, Order: tc.order}
			name := "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP"
			nzb := rel("usenet-release", name, 6000)
			nzb.Protocol = core.ProtocolUsenet
			alko := alkoRelease("file", name, 6000)
			backup := rel("backup", name, 6000)
			backup.Protocol = core.ProtocolHTTP
			backup.StreamURL = "https://example.org/video"
			var calls []string
			svc := New(&fakeIndexer{}, fakeHealth{unhealthy: tc.bad}, nil, cfg, Options{
				QuickSearch: func(_ context.Context, _ core.SearchRequest, source, _ string) ([]core.Release, error) {
					calls = append(calls, source)
					return map[string][]core.Release{"usenet": {nzb}, "alko": {alko}, "addons": {backup}}[source], nil
				},
				Discovered: func(context.Context, *core.Title) []core.Release {
					t.Fatal("indexed release bypassed source order")
					return []core.Release{backup}
				},
			})
			defer svc.Close()
			svc.rememberReleases(matrixTitle(), []core.Release{backup})
			ctx := core.WithIndexerPurpose(t.Context(), core.IndexerPurposePlayback)
			got, err := svc.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
			if err != nil || got.Release.ID != tc.want {
				t.Fatalf("got=%+v err=%v traces=%+v", got, err, svc.Traces())
			}
			if !reflect.DeepEqual(calls, tc.calls) {
				t.Fatalf("calls=%v want=%v", calls, tc.calls)
			}
		})
	}
}

func TestQuickRejectsUnusableAndContinues(t *testing.T) {
	for _, name := range []string{"Wrong.Movie.1999.1080p.WEB-DL.H.264", "The.Signal.1999.480p.WEB-DL.H.264", "The.Signal.1999.FRENCH.1080p.WEB-DL.H.264"} {
		t.Run(name, func(t *testing.T) {
			cfg := testCfg()
			cfg.Resolver.SourceOrder.Quick = config.QuickSources{Enabled: true, Order: []string{"alko", "usenet"}}
			for i := range cfg.Quality.Profiles {
				cfg.Quality.Profiles[i].PreferredLanguages = []string{"en"}
				cfg.Quality.Profiles[i].Language = "en"
			}
			good := rel("good", "The.Signal.1999.ENGLISH.1080p.WEB-DL.H.264", 6000)
			good.Protocol = core.ProtocolUsenet
			svc := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{QuickSearch: func(_ context.Context, _ core.SearchRequest, source, _ string) ([]core.Release, error) {
				if source == "alko" {
					return []core.Release{alkoRelease("bad", name, 6000)}, nil
				}
				return []core.Release{good}, nil
			}})
			defer svc.Close()
			title := matrixTitle()
			title.Language = "en"
			got, err := svc.Resolve(t.Context(), core.ResolveRequest{Title: title, Client: webCaps()})
			if err != nil || got.Release.ID != "good" {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
}

func TestQuickDisabledKeepsNormalResolver(t *testing.T) {
	good := rel("good", "The.Signal.1999.1080p.WEB-DL.H.264", 6000)
	svc := New(&fakeIndexer{releases: []core.Release{good}}, fakeHealth{}, nil, testCfg(), Options{QuickSearch: func(context.Context, core.SearchRequest, string, string) ([]core.Release, error) {
		t.Fatal("quick mode used by default")
		return nil, nil
	}})
	defer svc.Close()
	if _, err := svc.Resolve(t.Context(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}); err != nil {
		t.Fatal(err)
	}
}

func TestQuickAlkoNeverAdmitsMagnets(t *testing.T) {
	r := alkoRelease("a", "title", 1)
	if !quickReleaseAllowed("alko", r) {
		t.Fatal("verified file refused")
	}
	r.MagnetURL = "magnet:?xt=urn:btih:abc"
	if quickReleaseAllowed("alko", r) {
		t.Fatal("magnet admitted")
	}
	if quickReleaseAllowed("alko", remuxTorrent()) {
		t.Fatal("ordinary torrent admitted")
	}
}

func TestQuickSearchFailureAndExclusions(t *testing.T) {
	cfg := testCfg()
	cfg.Resolver.SourceOrder.Quick = config.QuickSources{Enabled: true, Order: []string{"alko", "usenet"}}
	var calls []string
	good := rel("excluded", "The.Signal.1999.1080p.WEB-DL.H.264", 6000)
	good.Protocol = core.ProtocolUsenet
	svc := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{QuickSearch: func(_ context.Context, _ core.SearchRequest, source, _ string) ([]core.Release, error) {
		calls = append(calls, source)
		if source == "alko" {
			return nil, errors.New("source unavailable")
		}
		return []core.Release{good}, nil
	}})
	defer svc.Close()
	_, err := svc.Resolve(t.Context(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps(), Exclude: []string{"excluded"}})
	if !errors.Is(err, core.ErrNotAvailable) || !reflect.DeepEqual(calls, []string{"alko", "usenet", "addons", "addons-rescue"}) {
		t.Fatalf("err=%v calls=%v", err, calls)
	}
}
