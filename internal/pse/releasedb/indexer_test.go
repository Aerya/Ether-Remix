package releasedb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/stremio"
)

// fakeAddon imite un addon Stremio configuré avec AllDebrid (AIOStreams).
func fakeAddon(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifest.json"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "test.aio", "version": "1.0.0", "name": "AIO test",
				"resources": []string{"stream"}, "types": []string{"movie", "series"},
				"idPrefixes": []string{"tt"}, "catalogs": []any{},
			})
		case strings.Contains(r.URL.Path, "/stream/"):
			hits.Add(1)
			if strings.Contains(r.URL.Path, "/upload") || strings.Contains(r.URL.Path, "magnet") {
				t.Errorf("appel inattendu : %s", r.URL.Path)
			}
			base := "http://" + r.Host + "/play/"
			_ = json.NewEncoder(w).Encode(map[string]any{"streams": []map[string]any{
				{"name": "[AD⚡] AIO 2160p", "title": "Dune.Part.Two.2024.MULTi.VFF.2160p.WEB-DL.DV.HDR.x265-GRP\n💾 18.2 GB", "url": base + "a"},
				{"name": "[AD⚡] AIO 1080p", "title": "Dune.Part.Two.2024.FRENCH.1080p.WEB.H264-GRP\n💾 4.1 GB", "url": base + "b"},
				{"name": "[AD] AIO 1080p", "title": "Dune.Part.Two.2024.1080p.BluRay.x264-ENG\n💾 9 GB", "url": base + "c"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIndexerWithStremioAddon(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	var hits atomic.Int64
	srv := fakeAddon(t, &hits)
	cfg := config.Config{Indexers: []config.Indexer{
		{ID: "aio", Name: "AIO", URL: srv.URL + "/manifest.json", Kind: config.IndexerKindStremio, Protocol: "torrent", Enabled: true},
		// Un indexeur Newznab non listé : jamais interrogé.
		{ID: "nzb", Name: "nzb", URL: "http://nzb.invalid", Enabled: true},
	}}
	addons := stremio.NewService(nil)
	mgr := newznab.NewManager(db, cfg.Indexers, newznab.Options{Backend: func(ix config.Indexer) (newznab.Backend, error) {
		return addons.Backend(ix)
	}})
	titles := []core.Title{
		{Key: 101, Type: core.TypeMovie, Title: "Dune: Part Two", Year: 2024, IDs: core.IDs{IMDB: "tt15239678", TMDB: 693134}},
		{Key: 102, Type: core.TypeMovie, Title: "Arrival", Year: 2016, IDs: core.IDs{IMDB: "tt2543164", TMDB: 329865}},
	}
	browses := 0
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s := New(Deps{
		DB:     db,
		Config: func() config.Config { return cfg },
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			browses++
			if q.Filter.Type != core.TypeMovie || q.Offset >= len(titles) {
				return core.Page{}, nil
			}
			return core.Page{Items: titles[q.Offset:]}, nil
		},
		Search:   mgr.Search,
		Now:      func() time.Time { return now },
		Settings: Settings{Enabled: true, Daily: 1, Pace: time.Second, Types: []core.MediaType{core.TypeMovie}},
	})
	if got := s.sources(); len(got) != 1 || got[0].id != "aio" {
		t.Fatalf("sources = %+v, seul l'addon Stremio est attendu", got)
	}
	t.Cleanup(OverridePlayable(NewPlayable(2, "lang=fr", false)))
	if err := s.Step(ctx); err != nil {
		t.Fatalf("premier titre : %v", err)
	}
	var playable int
	if err := db.QueryRowContext(ctx, `SELECT releases FROM pse_playable WHERE title_key=101`).Scan(&playable); err != nil || playable != 2 {
		t.Fatalf("pse_playable = %d, %v ; 2 releases FR attendues", playable, err)
	}
	all, err := s.Store().Releases(ctx, 101, Filter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("releases = %d, %v ; 3 attendues", len(all), err)
	}
	fr, _ := s.Store().Count(ctx, 101, ParseFilter("lang=fr;min=1080;cached=1"))
	if en, _ := s.Store().Count(ctx, 101, ParseFilter("cached=1")); en != 2 {
		t.Fatalf("releases en cache = %d, 2 attendues (la release [AD] sans ⚡ ne l'est pas)", en)
	}
	if fr != 2 {
		t.Fatalf("releases FR ≥1080p lisibles = %d, 2 attendues (%+v)", fr, all)
	}
	uhd, _ := s.Store().Count(ctx, 101, ParseFilter("lang=fr;min=2160;codec=hevc"))
	if uhd != 1 {
		t.Fatalf("FR 2160p HEVC = %d, 1 attendue", uhd)
	}
	// Budget quotidien de 1 : le second titre est refusé et reste à échéance.
	if err := s.Step(ctx); err == nil {
		t.Fatal("le budget quotidien aurait dû bloquer la seconde requête")
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("requêtes à l'addon = %d, 1 attendue", n)
	}
	st, _ := s.Store().Stats(ctx, now)
	if st.Titles != 1 || st.TitlesWithReleases != 1 || st.Releases != 3 {
		t.Fatalf("stats = %+v", st)
	}
	// Le lendemain, le budget repart et le second titre est indexé ; le
	// premier n'est pas encore à échéance.
	now = now.Add(24 * time.Hour)
	s.mu.Lock()
	s.queue = map[core.MediaType][]core.Title{}
	s.mu.Unlock()
	if err := s.Step(ctx); err != nil {
		t.Fatalf("lendemain : %v", err)
	}
	if n, _ := s.Store().Count(ctx, 102, Filter{}); n != 3 {
		t.Fatalf("second titre : %d releases", n)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("requêtes à l'addon = %d, 2 attendues", n)
	}
	// Une nouvelle indexation conserve first_seen et ne duplique rien.
	before, _ := s.Store().Releases(ctx, 101, Filter{})
	if err := s.IndexTitle(ctx, titles[0], []source{{id: "aio", limit: 10}}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Store().Releases(ctx, 101, Filter{})
	if len(after) != len(before) || !after[0].FirstSeen.Equal(before[0].FirstSeen) {
		t.Fatalf("réindexation : %d → %d releases", len(before), len(after))
	}
}

func TestParseFilterAndSettings(t *testing.T) {
	f := ParseFilter(" lang=FR,fr-ca ; min=1080p;max=2160;codec=HEVC;cached=oui;x=y")
	if len(f.Languages) != 2 || f.MinHeight != 1080 || f.MaxHeight != 2160 || f.VideoCodecs[0] != "hevc" || !f.CachedOnly {
		t.Fatalf("filtre = %+v", f)
	}
	if !f.Match(Release{Languages: releaseLanguages("Film.2024.MULTi.VFQ.2160p", []string{"french"}), Height: 2160, VideoCodec: "HEVC", DebridCached: []string{"alldebrid"}}) {
		t.Fatal("release VFQ 2160p HEVC refusée")
	}
	if f.Match(Release{Languages: []string{"english"}, Height: 1080, VideoCodec: "hevc", DebridCached: []string{"alldebrid"}}) {
		t.Fatal("release anglaise acceptée")
	}
	if got := releaseLanguages("Show.S01E01.MULTi.TRUEFRENCH.1080p", nil); strings.Join(got, ",") != "french,vff,multi" {
		t.Fatalf("langues = %v", got)
	}
	if got := releaseLanguages("Le.Film.2023.FRENCH.QC.1080p", []string{"french"}); strings.Join(got, ",") != "french,vfq" {
		t.Fatalf("langues = %v", got)
	}
	if ParseFilter("lang=qc").Match(Release{Languages: []string{"french", "vff"}}) {
		t.Fatal("release anglaise acceptée")
	}
	t.Setenv("PSE_INDEXER", "on")
	t.Setenv("PSE_INDEXER_PACE", "1s")
	t.Setenv("PSE_INDEXER_TYPES", "films")
	t.Setenv("PSE_INDEXER_SOURCES", "a, b")
	s := LoadSettings()
	if !s.Enabled || s.Pace != 2*time.Second || len(s.Types) != 1 || s.Types[0] != core.TypeMovie || len(s.Sources) != 2 || s.Daily != 1000 {
		t.Fatalf("réglages = %+v", s)
	}
}

func TestCachedHintsDirectStreams(t *testing.T) {
	cases := []struct {
		r    core.Release
		want string
	}{
		{core.Release{Protocol: core.ProtocolHTTP}, "direct"},
		{core.Release{Protocol: core.ProtocolHTTP, DebridUncached: []string{"alldebrid"}}, ""},
		{core.Release{Protocol: core.ProtocolHTTP, DebridCached: []string{"alldebrid"}}, "alldebrid"},
		{core.Release{Protocol: core.ProtocolTorrent}, ""},
	}
	for _, c := range cases {
		if got := strings.Join(cachedHints(c.r), ","); got != c.want {
			t.Fatalf("%+v → %q, %q attendu", c.r, got, c.want)
		}
	}
}

func TestLocalVideosNeverReachRemoteIndexers(t *testing.T) {
	s := New(Deps{DB: store.OpenTest(t), Settings: Settings{Types: []core.MediaType{core.TypeMovie}}, Search: func(context.Context, core.SearchRequest) ([]core.Release, error) {
		t.Fatal("personal title sent to remote indexer")
		return nil, nil
	}})
	local := core.Title{Key: core.LocalKey(3_000_000_000_001), Type: core.TypeMovie, Title: "Private home video"}
	if err := s.IndexTitle(t.Context(), local, []source{{id: "remote"}}); err != nil {
		t.Fatal(err)
	}
	s.urgent[core.TypeMovie] = []core.Title{local}
	s.prio[core.TypeMovie] = []core.Title{local}
	s.d.Priority = func(context.Context, core.MediaType) []PriorityKey { return nil }
	if _, ok, err := s.nextPriority(t.Context(), core.TypeMovie, time.Now()); err != nil || ok {
		t.Fatal(ok, err)
	}
}
