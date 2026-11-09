package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeFinder struct {
	hits    []TorrentHit
	queries atomic.Int32
}

func (f *fakeFinder) FindAlbum(_ context.Context, q string) ([]TorrentHit, error) {
	f.queries.Add(1)
	return f.hits, nil
}

func (f *fakeFinder) Magnet(_ context.Context, h TorrentHit) (TorrentHit, error) {
	h.Magnet = "magnet:?xt=urn:btih:" + strings.Repeat("c", 40)
	return h, nil
}

type fakeAccount struct {
	cached map[string][]DebridFile // magnet → fichiers
	opened []string
}

func (a *fakeAccount) OpenCached(_ context.Context, h TorrentHit) ([]DebridFile, error) {
	a.opened = append(a.opened, h.Title)
	if fs, ok := a.cached[h.Magnet]; ok {
		return fs, nil
	}
	return nil, ErrNotCached
}

func (a *fakeAccount) Unlock(_ context.Context, file DebridFile) (Stream, error) {
	return Stream{URL: "https://cdn.example/" + file.Link}, nil
}

func track(id int64, n int, title string) Track {
	return Track{ID: id, Number: n, Title: title, Artist: Artist{Name: "Stromae"}, Album: Album{ID: 9, Title: "Racine carrée (Deluxe Edition)"}}
}

func TestDebridResolverPlaysCachedAlbum(t *testing.T) {
	album := []DebridFile{
		{Path: "Stromae - Racine Carree [FLAC]/cover.jpg", Link: "jpg"},
		{Path: "Stromae - Racine Carree [FLAC]/01 - Ta fête.flac", Link: "a1"},
		{Path: "Stromae - Racine Carree [FLAC]/02 - Papaoutai.flac", Link: "a2"},
		{Path: "Stromae - Racine Carree [FLAC]/03 - Bâtard.flac", Link: "a3"},
	}
	f := &fakeFinder{hits: []TorrentHit{
		{Title: "Daft Punk - Discovery", Magnet: "m0", Seeders: 500},
		{Title: "Stromae - Racine Carree (2013) MP3 320", Magnet: "m1", Seeders: 40},
		{Title: "Stromae - Racine Carrée [FLAC]", Magnet: "m2", Seeders: 10},
		{Title: "Stromae - Discographie", Magnet: "m3", Seeders: 90},
		{Title: "Stromae - Racine Carree [FLAC]", Magnet: "m2", Seeders: 10}, // doublon
	}}
	acc := &fakeAccount{cached: map[string][]DebridFile{"m2": album}}
	r := &DebridResolver{Finder: f, Account: acc}

	s, err := r.Resolve(context.Background(), track(1, 2, "Papaoutai"))
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://cdn.example/a2" || s.Mime != "audio/flac" || s.Quality != "LOSSLESS" {
		t.Fatalf("flux %+v", s)
	}
	// Le FLAC passe avant le MP3, l'album hors sujet n'est jamais ouvert.
	if len(acc.opened) != 1 || acc.opened[0] != "Stromae - Racine Carrée [FLAC]" {
		t.Fatalf("ouverts : %v", acc.opened)
	}
	// Le titre suivant du même album ne relance ni recherche ni vérification.
	s, err = r.Resolve(context.Background(), track(2, 3, "Bâtard"))
	if err != nil || s.URL != "https://cdn.example/a3" {
		t.Fatalf("titre 3 : %+v %v", s, err)
	}
	if f.queries.Load() != 1 || len(acc.opened) != 1 {
		t.Fatalf("recherches %d, ouvertures %d", f.queries.Load(), len(acc.opened))
	}
}

func TestDebridResolverMissIsRemembered(t *testing.T) {
	f := &fakeFinder{hits: []TorrentHit{{Title: "Stromae Racine Carree", Magnet: "m9"}}}
	acc := &fakeAccount{}
	r := &DebridResolver{Finder: f, Account: acc}
	if _, err := r.Resolve(context.Background(), track(1, 1, "Ta fête")); err == nil {
		t.Fatal("un album hors cache ne doit pas se lire")
	}
	if _, err := r.Resolve(context.Background(), track(2, 2, "Papaoutai")); err == nil {
		t.Fatal("toujours hors cache")
	}
	if f.queries.Load() != 1 {
		t.Fatalf("l'absence doit être gardée : %d recherches", f.queries.Load())
	}
}

func TestDebridResolverCompletesDownloadOnlyHits(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:" + strings.Repeat("c", 40)
	f := &fakeFinder{hits: []TorrentHit{{Title: "Stromae - Racine Carree", Download: "https://ix/dl/1"}}}
	acc := &fakeAccount{cached: map[string][]DebridFile{magnet: {{Path: "1-01 Ta fete.mp3", Link: "x"}}}}
	r := &DebridResolver{Finder: f, Account: acc}
	s, err := r.Resolve(context.Background(), track(1, 1, "Ta fête"))
	if err != nil || s.Mime != "audio/mpeg" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestPickDebridFileByNumberAndDisc(t *testing.T) {
	files := []DebridFile{
		{Path: "CD1/01 Intro.flac", Link: "d1"},
		{Path: "CD2/01 Intro (Reprise).flac", Link: "d2"},
		{Path: "CD1/02 Intro du soir.flac", Link: "d3"},
	}
	f, ok := pickDebridFile(files, Track{Title: "Intro", Number: 1, Volume: 1})
	if !ok || f.Link != "d1" {
		t.Fatalf("disque 1 : %+v", f)
	}
	f, ok = pickDebridFile(files, Track{Title: "Intro (Reprise)", Number: 1, Volume: 2})
	if !ok || f.Link != "d2" {
		t.Fatalf("disque 2 : %+v", f)
	}
	if _, ok := pickDebridFile(files, Track{Title: "Absent", Number: 9}); ok {
		t.Fatal("aucun fichier ne correspond")
	}
}

type staticResolver struct {
	s   Stream
	err error
}

func (r staticResolver) Resolve(context.Context, Track) (Stream, error) { return r.s, r.err }

func TestResolversFallBack(t *testing.T) {
	rs := Resolvers{staticResolver{err: ErrNotFound}, staticResolver{s: Stream{URL: "https://b"}}}
	s, err := rs.Resolve(context.Background(), Track{})
	if err != nil || s.URL != "https://b" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestSettingsAllDebridAlone(t *testing.T) {
	s := Settings{Debrid: true}
	if !s.Enabled() || s.Mode() != "alldebrid" {
		t.Fatalf("%+v %s", s, s.Mode())
	}
	if _, ok := s.NewSource(DebridSources{{Kind: "alldebrid", Name: "AllDebrid", Resolver: staticResolver{}}}).(*DeezerSource); !ok {
		t.Fatal("catalogue Deezer attendu")
	}
	if (Settings{EclipseURLs: "https://e/manifest.json", Debrid: true}).Mode() != "eclipse+alldebrid" {
		t.Fatal("mode combiné")
	}
}

func TestIndexerFinderProwlarrAndTorznab(t *testing.T) {
	hash := strings.Repeat("a", 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/search":
			if r.Header.Get("X-Api-Key") != "pk" || r.URL.Query().Get("categories") != "3000" {
				http.Error(w, "nope", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`[{"title":"Stromae - Racine Carree","protocol":"torrent","infoHash":"` + hash + `","seeders":12},
				{"title":"Usenet","protocol":"usenet"},
				{"title":"Sans empreinte","protocol":"torrent","downloadUrl":"https://ix/dl"}]`))
		case strings.HasSuffix(r.URL.Path, "/api"):
			if r.URL.Query().Get("cat") != "3000" || r.URL.Query().Get("apikey") != "tk" {
				http.Error(w, "nope", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`<?xml version="1.0"?><rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
				<item><title>Stromae - Multitude FLAC</title><link>https://ix/t.torrent</link>
				<torznab:attr name="seeders" value="7"/><torznab:attr name="magneturl" value="magnet:?xt=urn:btih:` + strings.Repeat("b", 40) + `"/></item>
			</channel></rss>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	f := &IndexerFinder{Config: func() IndexerConfig { return IndexerConfig{ProwlarrURL: srv.URL, ProwlarrKey: "pk"} }}
	hits, err := f.FindAlbum(context.Background(), "stromae racine carree")
	if err != nil || len(hits) != 2 {
		t.Fatalf("prowlarr : %+v %v", hits, err)
	}
	if hits[0].InfoHash != hash || !strings.HasPrefix(hits[0].Magnet, "magnet:?xt=urn:btih:"+hash) || hits[0].Seeders != 12 {
		t.Fatalf("résultat : %+v", hits[0])
	}
	if hits[1].Magnet != "" || hits[1].Download != "https://ix/dl" {
		t.Fatalf("sans empreinte : %+v", hits[1])
	}

	f = &IndexerFinder{Config: func() IndexerConfig {
		return IndexerConfig{Torznab: []TorznabIndexer{{Name: "t", URL: srv.URL + "/1/", APIKey: "tk"}}}
	}}
	hits, err = f.FindAlbum(context.Background(), "stromae multitude")
	if err != nil || len(hits) != 1 || hits[0].InfoHash != strings.Repeat("b", 40) || hits[0].Seeders != 7 {
		t.Fatalf("torznab : %+v %v", hits, err)
	}
}

// slowFinder ne répond qu'une fois libéré.
type slowFinder struct {
	fakeFinder
	release chan struct{}
}

func (f *slowFinder) FindAlbum(ctx context.Context, q string) ([]TorrentHit, error) {
	select {
	case <-f.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return f.fakeFinder.FindAlbum(ctx, q)
}

func TestDebridResolverLookupOutlivesClient(t *testing.T) {
	f := &slowFinder{fakeFinder: fakeFinder{hits: []TorrentHit{{Title: "Stromae - Racine Carree [FLAC]", Magnet: "m2"}}}, release: make(chan struct{})}
	acc := &fakeAccount{cached: map[string][]DebridFile{"m2": {{Path: "a/01 - Ta fête.flac", Link: "a1"}}}}
	r := &DebridResolver{Finder: f, Account: acc}

	// Le client abandonne avant la fin de la recherche…
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Resolve(ctx, track(1, 1, "Ta fête")); err == nil {
		t.Fatal("la requête abandonnée doit échouer")
	}
	// … la recherche continue, et la tentative suivante en profite.
	close(f.release)
	s, err := r.Resolve(context.Background(), track(1, 1, "Ta fête"))
	if err != nil || s.URL != "https://cdn.example/a1" {
		t.Fatalf("seconde tentative : %+v %v", s, err)
	}
	if f.queries.Load() != 1 {
		t.Fatalf("une seule recherche attendue : %d", f.queries.Load())
	}
}

func TestDebridSourcesActivationAndFallback(t *testing.T) {
	sources := DebridSources{
		{Kind: "torbox", Name: "TorBox", Resolver: staticResolver{err: ErrNotCached}},
		{Kind: "realdebrid", Name: "Real-Debrid", Resolver: staticResolver{s: Stream{URL: "https://rd.example/track"}}},
		{Kind: "alldebrid", Name: "AllDebrid", Resolver: staticResolver{s: Stream{URL: "https://ad.example/track"}}},
	}
	for _, cfg := range []Settings{{TorBox: true}, {RealDebrid: true}, {TorBox: true, RealDebrid: true}} {
		if !cfg.Enabled() {
			t.Fatal("source not enabled")
		}
		src := cfg.NewSource(sources).(*DeezerSource)
		rs := src.Streams.(Resolvers)
		want := 1
		if cfg.TorBox && cfg.RealDebrid {
			want = 2
		}
		if len(rs) != want {
			t.Fatalf("disabled account included: %d", len(rs))
		}
		st, err := rs.Resolve(t.Context(), Track{})
		if cfg.RealDebrid && (err != nil || st.URL != "https://rd.example/track") {
			t.Fatalf("fallback failed: %+v %v", st, err)
		}
	}
}

type expiringMusicAccount struct {
	calls int
	now   time.Time
}

func (a *expiringMusicAccount) OpenCached(context.Context, TorrentHit) ([]DebridFile, error) {
	return []DebridFile{{Path: "01 Ta fête.flac", TorrentID: "7", FileID: "0"}}, nil
}
func (a *expiringMusicAccount) Unlock(context.Context, DebridFile) (Stream, error) {
	a.calls++
	return Stream{URL: "https://audio.example/" + strconv.Itoa(a.calls), ExpiresAt: a.now.Add(20 * time.Second).Unix()}, nil
}
func TestDebridRenewsExpiredAndRefusedLinks(t *testing.T) {
	now := time.Now()
	acc := &expiringMusicAccount{now: now}
	r := &DebridResolver{Account: acc, Finder: &fakeFinder{hits: []TorrentHit{{Title: "Stromae Racine Carree FLAC", Magnet: "cached"}}}, Now: func() time.Time { return now }}
	tr := track(1, 1, "Ta fête")
	first, err := r.Resolve(t.Context(), tr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Resolve(t.Context(), tr); err != nil || acc.calls != 1 {
		t.Fatal("valid link not cached")
	}
	if _, err := r.Resolve(WithSkippedStreams(t.Context(), first.URL), tr); err == nil {
		t.Fatal("refused source did not yield to fallback")
	}
	second, err := r.Resolve(t.Context(), tr)
	if err != nil || second.URL == first.URL {
		t.Fatalf("refused link not refreshed for next playback: %+v %v", second, err)
	}
	now = now.Add(21 * time.Second)
	acc.now = now
	third, err := r.Resolve(t.Context(), tr)
	if err != nil || third.URL == second.URL || acc.calls != 3 {
		t.Fatalf("expired link reused: %+v %v", third, err)
	}
}

type fixedDebridAccount struct{ url string }

func (a fixedDebridAccount) OpenCached(context.Context, TorrentHit) ([]DebridFile, error) {
	return []DebridFile{{Path: "01 Song.flac", Link: "cached"}}, nil
}
func (a fixedDebridAccount) Unlock(context.Context, DebridFile) (Stream, error) {
	return Stream{URL: a.url}, nil
}

type debridRelaySource struct {
	Source
	rs Resolvers
}

func (s debridRelaySource) Stream(ctx context.Context, _ int64, _ string) (Stream, error) {
	return s.rs.Resolve(ctx, Track{ID: 1, Number: 1, Title: "Song", Artist: Artist{Name: "Artist"}, Album: Album{ID: 1, Title: "Album"}})
}
func TestDebridRelayRefusalFallsBack(t *testing.T) {
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer refused.Close()
	audio := []byte("fLaC-test-audio")
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		_, _ = w.Write(audio)
	}))
	defer ready.Close()
	finder := &fakeFinder{hits: []TorrentHit{{Title: "Artist Album FLAC", Magnet: "cached"}}}
	source := debridRelaySource{rs: Resolvers{
		&DebridResolver{Provider: "Real-Debrid", Finder: finder, Account: fixedDebridAccount{refused.URL}},
		&DebridResolver{Provider: "TorBox", Finder: finder, Account: fixedDebridAccount{ready.URL}},
	}}
	rec := httptest.NewRecorder()
	if err := NewServer(Options{Source: source}).relayAudio(rec, httptest.NewRequest("GET", "/rest/stream", nil), 1); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != string(audio) || rec.Header().Get("X-Accent-Audio-Source") != "TorBox" {
		t.Fatalf("fallback stream: %s %v", rec.Body.String(), rec.Header())
	}
}
