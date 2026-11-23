package stremio

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type fakeAnime struct {
	mu    sync.Mutex
	calls []string
	ids   map[string][2]int64 // db -> id, episode
	err   error
}

func (f *fakeAnime) Lookup(_ context.Context, req core.SearchRequest, db string) (int64, int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, db)
	if f.err != nil {
		return 0, 0, false, f.err
	}
	v, ok := f.ids[db]
	return v[0], int(v[1]), ok, nil
}

func backendWith(t *testing.T, rawURL string, o Options, id string) *Backend {
	t.Helper()
	b, err := NewServiceWithOptions(o).Backend(config.Indexer{ID: id, Name: "addon", Kind: config.IndexerKindStremio, URL: rawURL})
	if err != nil {
		t.Fatal(err)
	}
	return b.(*Backend)
}

const kitsuManifest = `{"id":"anime","resources":[{"name":"stream","types":["series","movie"],"idPrefixes":["kitsu:"]}],"types":["series","movie"]}`

func animeRequest() core.SearchRequest {
	return core.SearchRequest{Type: core.TypeEpisode, IDs: core.IDs{IMDB: "tt2560140", TMDB: 1429}, Title: "Attack on Titan",
		Season: 3, Episode: 15, AbsoluteEpisode: 52, Anime: true}
}

func TestAnimeOnlyAddonIsAskedByMappedEntryEpisode(t *testing.T) {
	body := `{"streams":[
	 {"name":"Anime","title":"[SubsPlease] Shingeki no Kyojin - 03 (1080p) [ABCD1234].mkv\n👤 40 💾 1.4 GB","infoHash":"` + hashA + `"},
	 {"name":"Anime","title":"[Group] Shingeki no Kyojin - 52 (1080p)","infoHash":"` + hashB + `"},
	 {"name":"Anime","title":"[Other] Shingeki no Kyojin - 07 (1080p)","infoHash":"` + hashC + `"}
	]}`
	f := &fakeAddon{manifest: kitsuManifest, streams: map[string]string{"/stream/series/kitsu:41982:3.json": body}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	anime := &fakeAnime{ids: map[string][2]int64{"kitsu": {41982, 3}}}
	b := backendWith(t, srv.URL, Options{Client: srv.Client(), Anime: anime}, "ix1")
	rels, err := b.Search(t.Context(), animeRequest(), noReserve)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.streamRequests(); len(got) != 1 || got[0] != "/stream/series/kitsu:41982:3.json" {
		t.Fatalf("stream requests %v", got)
	}
	if len(rels) != 3 {
		t.Fatalf("releases %+v", rels)
	}
	// The entry's third episode is the requested S3E15...
	if i := rels[0].Info; len(i.Seasons) != 1 || i.Seasons[0] != 3 || len(i.Episodes) != 1 || i.Episodes[0] != 15 || len(i.AbsoluteEpisodes) != 0 {
		t.Fatalf("entry episode identity %+v", i)
	}
	// ...a series-absolute number keeps its own identity...
	if i := rels[1].Info; len(i.Seasons) != 0 || len(i.AbsoluteEpisodes) != 1 || i.AbsoluteEpisodes[0] != 52 {
		t.Fatalf("absolute release %+v", i)
	}
	// ...and another number is not relabelled as the requested episode.
	if i := rels[2].Info; len(i.Seasons) != 0 || (len(i.Episodes) > 0 && i.Episodes[0] == 15) || i.AbsoluteEpisodes[0] != 7 {
		t.Fatalf("other number relabelled %+v", i)
	}
}

func TestAnimeMappingIsOnlyConsultedWhenNeeded(t *testing.T) {
	f := &fakeAddon{manifest: kitsuManifest}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	anime := &fakeAnime{ids: map[string][2]int64{"kitsu": {41982, 3}}}
	b := backendWith(t, srv.URL, Options{Client: srv.Client(), Anime: anime}, "ix1")
	notAnime := animeRequest()
	notAnime.Anime = false
	season := animeRequest()
	season.Episode = 0
	for _, req := range []core.SearchRequest{notAnime, season} {
		if rels, err := b.Search(t.Context(), req, noReserve); err != nil || len(rels) != 0 {
			t.Fatalf("rels %v err %v", rels, err)
		}
	}
	if len(anime.calls) != 0 || len(f.streamRequests()) != 0 {
		t.Fatalf("mapping calls %v, requests %v", anime.calls, f.streamRequests())
	}
	// An unavailable or ambiguous mapping skips the addon quietly.
	anime.err = errors.New("unavailable")
	if rels, err := b.Search(t.Context(), animeRequest(), noReserve); err != nil || len(rels) != 0 || len(f.streamRequests()) != 0 {
		t.Fatalf("unavailable mapping: rels %v err %v requests %v", rels, err, f.streamRequests())
	}
	anime.err, anime.ids = nil, nil
	if rels, err := b.Search(t.Context(), animeRequest(), noReserve); err != nil || len(rels) != 0 || len(f.streamRequests()) != 0 {
		t.Fatalf("unmapped title: rels %v err %v", rels, err)
	}
}

func TestIDChoicePrefersIMDbThenTMDB(t *testing.T) {
	for _, tc := range []struct {
		manifest string
		req      core.SearchRequest
		want     string
		wantAbs  string // the ":1:<absolute>" follow-up for absolute-numbered anime
	}{
		{`{"id":"x","resources":[{"name":"stream","types":["movie","series"],"idPrefixes":["tt","tmdb:","kitsu:"]}]}`,
			animeRequest(), "/stream/series/tt2560140:3:15.json", "/stream/series/tt2560140:1:52.json"},
		{`{"id":"x","resources":[{"name":"stream","types":["movie","series"],"idPrefixes":["tmdb:","kitsu:"]}]}`,
			animeRequest(), "/stream/series/tmdb:1429:3:15.json", "/stream/series/tmdb:1429:1:52.json"},
		{`{"id":"x","resources":[{"name":"stream","types":["movie"],"idPrefixes":["tt","tmdb"]}]}`,
			core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 603}, Title: "M"}, "/stream/movie/tmdb:603.json", ""},
		{`{"id":"x","resources":[{"name":"stream","types":["movie"],"idPrefixes":["kitsu"]}]}`,
			core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 5000}, Title: "Film", Anime: true}, "/stream/movie/kitsu:108.json", ""},
	} {
		f := &fakeAddon{manifest: tc.manifest}
		srv := httptest.NewServer(f.handler(t))
		anime := &fakeAnime{ids: map[string][2]int64{"kitsu": {108, 0}}}
		b := backendWith(t, srv.URL, Options{Client: srv.Client(), Anime: anime}, "ix1")
		if _, err := b.Search(t.Context(), tc.req, noReserve); err != nil {
			t.Fatal(err)
		}
		want := []string{tc.want}
		if tc.wantAbs != "" {
			want = append(want, tc.wantAbs)
		}
		if got := f.streamRequests(); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("manifest %s: requests %v, want %v", tc.manifest, got, want)
		}
		srv.Close()
	}
}

func TestHTTPStreamIdentityIgnoresCredentialsAndNamesItsAddon(t *testing.T) {
	mk := func(u string) []stream {
		return []stream{{Name: "[RD+] Addon 4K", Title: "Movie 2014 2160p WEB-DL x265-GRP", URL: u, BehaviorHints: behaviorHints{VideoSize: 4 << 30}}}
	}
	req := core.SearchRequest{Type: core.TypeMovie, Title: "Movie"}
	a, _, _ := convertWith(mk("https://addon.example/play/"+secret+"/token-1"), req, false, convertOptions{instance: "ix1"})
	b, _, _ := convertWith(mk("https://addon.example/play/"+secret+"/token-2"), req, false, convertOptions{instance: "ix1"})
	c, _, _ := convertWith(mk("https://addon.example/play/"+secret+"/token-1"), req, false, convertOptions{instance: "ix2"})
	if len(a) != 1 || a[0].GUID != b[0].GUID {
		t.Fatalf("GUID changed with the link's token: %q vs %q", a[0].GUID, b[0].GUID)
	}
	if a[0].GUID == c[0].GUID {
		t.Fatal("two configured addons share an HTTP release identity")
	}
	if strings.Contains(a[0].GUID, secret) || a[0].DebridCached[0] != config.DebridRealDebrid {
		t.Fatalf("release %+v", a[0])
	}
	// A different file or size is a different release.
	d, _, _ := convertWith([]stream{{Name: "[RD+] Addon 4K", Title: "Movie 2014 2160p WEB-DL x265-GRP", URL: "https://addon.example/x", BehaviorHints: behaviorHints{VideoSize: 5 << 30}}}, req, false, convertOptions{instance: "ix1"})
	if d[0].GUID == a[0].GUID {
		t.Fatal("size not part of the identity")
	}
}

func TestTorrentStreamKeepsAddonLinkAsFallback(t *testing.T) {
	streams := []stream{{Name: "[RD+] T", Title: "Movie 2014 1080p BluRay x264-GRP", URL: "https://addon.example/resolve/realdebrid/" + secret + "/" + hashA + "/null/0/Movie.mkv"}}
	rels, _, _ := convertWith(streams, core.SearchRequest{Type: core.TypeMovie, Title: "Movie"}, false, convertOptions{origin: "https://addon.example"})
	if len(rels) != 1 || rels[0].Protocol != core.ProtocolTorrent || rels[0].InfoHash != hashA || rels[0].StreamURL == "" || rels[0].StreamOrigin != "https://addon.example" {
		t.Fatalf("release %+v", rels)
	}
	// A magnet is no HTTP fallback.
	rels, _, _ = convertWith([]stream{{Title: "Movie 2014 1080p BluRay x264-GRP", URL: "magnet:?xt=urn:btih:" + hashA}}, core.SearchRequest{Type: core.TypeMovie, Title: "Movie"}, false, convertOptions{})
	if len(rels) != 1 || rels[0].StreamURL != "" {
		t.Fatalf("magnet release %+v", rels)
	}
}

func TestStreamRequestHeadersAreBounded(t *testing.T) {
	s := stream{BehaviorHints: behaviorHints{}}
	s.BehaviorHints.ProxyHeaders.Request = map[string]string{
		"referer": "https://site.example/", "Range": "bytes=0-", "Host": "evil.example",
		"X-Inject": "a\r\nX-Other: b", "User-Agent": "AddonPlayer/1.0",
	}
	got := s.requestHeaders()
	if len(got) != 2 || got["Referer"] != "https://site.example/" || got["User-Agent"] != "AddonPlayer/1.0" {
		t.Fatalf("headers %v", got)
	}
}

func TestUncachedHTTPStreamKeepsItsLabel(t *testing.T) {
	rels, _, _ := convertWith([]stream{{Name: "[RD download] Addon", Title: "Movie 2014 1080p BluRay x264-GRP", URL: "https://addon.example/x"}},
		core.SearchRequest{Type: core.TypeMovie, Title: "Movie"}, false, convertOptions{})
	if len(rels) != 1 || len(rels[0].DebridUncached) != 1 || len(rels[0].DebridCached) != 0 {
		t.Fatalf("release %+v", rels)
	}
}

func TestFetchNZBFromAddonOriginOnly(t *testing.T) {
	const nzb = `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="x"></file></nzb>`
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + secret + "/get.nzb":
			fmt.Fprint(w, nzb)
		case "/" + secret + "/page":
			fmt.Fprint(w, "<html>login</html>")
		case "/" + secret + "/gone":
			http.Error(w, "gone", http.StatusNotFound)
		case "/" + secret + "/elsewhere":
			http.Redirect(w, r, "http://127.0.0.2:1/x.nzb", http.StatusFound)
		default:
			fmt.Fprint(w, manifestJSON)
		}
	}))
	defer addon.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, nzb) }))
	defer other.Close()
	b := backendWith(t, addon.URL+"/"+secret+"/manifest.json", Options{}, "ix1")
	var grabs []string
	reserve := func(_ context.Context, kind string) error { grabs = append(grabs, kind); return nil }
	rel := core.Release{Name: "Movie 2014 1080p", Protocol: core.ProtocolUsenet, NZBURL: addon.URL + "/" + secret + "/get.nzb"}
	body, err := b.FetchNZB(t.Context(), rel, reserve)
	if err != nil || !strings.Contains(string(body), "<nzb") || len(grabs) != 1 || grabs[0] != "grab" {
		t.Fatalf("addon NZB: %v %q grabs %v", err, body, grabs)
	}
	for name, link := range map[string]string{
		"private host other than the addon": other.URL + "/x.nzb",
		"redirect away from the addon":      addon.URL + "/" + secret + "/elsewhere",
		"not an NZB":                        addon.URL + "/" + secret + "/page",
		"HTTP error":                        addon.URL + "/" + secret + "/gone",
	} {
		rel.NZBURL = link
		_, err := b.FetchNZB(t.Context(), rel, reserve)
		if err == nil {
			t.Errorf("%s: fetched", name)
			continue
		}
		if errors.Is(err, core.ErrReleaseRejected) || errors.Is(err, core.ErrArticleMissing) || errors.Is(err, core.ErrArticleCorrupt) {
			t.Errorf("%s: server answer classified as permanent: %v", name, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: error leaks the addon path: %v", name, err)
		}
	}
	if _, err := b.FetchNZB(t.Context(), core.Release{Name: "t", Protocol: core.ProtocolTorrent, InfoHash: hashA}, reserve); !errors.Is(err, core.ErrReleaseRejected) {
		t.Fatalf("torrent: %v", err)
	}
	if _, err := b.FetchNZB(t.Context(), core.Release{Name: "m", Protocol: core.ProtocolUsenet, NZBURL: "stremio:///x"}, reserve); !errors.Is(err, core.ErrReleaseRejected) {
		t.Fatalf("malformed NZB link: %v", err)
	}
	budget := errors.New("budget")
	rel.NZBURL = addon.URL + "/" + secret + "/get.nzb"
	if _, err := b.FetchNZB(t.Context(), rel, func(context.Context, string) error { return budget }); !errors.Is(err, budget) {
		t.Fatalf("grab budget ignored: %v", err)
	}
}

func TestFetchNZBAppliesDownloadIdentityRules(t *testing.T) {
	var gotUA, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `<?xml version="1.0"?><nzb><file/></nzb>`)
	}))
	defer srv.Close()

	rel := core.Release{Name: "Movie 2024 1080p", Protocol: core.ProtocolUsenet, NZBURL: srv.URL + "/get.nzb?id=1"}
	reserve := func(context.Context, string) error { return nil }

	b := backendWith(t, srv.URL+"/manifest.json", Options{
		ClientIdentity: func() config.ClientIdentity {
			return config.ClientIdentity{
				Rules: []config.IdentityRule{{
					ID:      "r-sab",
					Target:  config.TargetNZBDownloads,
					Profile: "sabnzbd",
					Params:  []config.KVOverride{{Name: "dl", Value: "1"}},
				}},
			}
		},
	}, "ix-stremio")

	body, err := b.FetchNZB(t.Context(), rel, reserve)
	if err != nil || !strings.Contains(string(body), "<nzb") {
		t.Fatalf("stremio FetchNZB with rule: %v %q", err, body)
	}
	if gotUA != config.ProfileUserAgent("sabnzbd") || gotQuery != "dl=1&id=1" {
		t.Fatalf("stremio FetchNZB wire: ua=%q query=%q", gotUA, gotQuery)
	}
}

func TestAddonFixtureQueriesCanonicalAndAliasNumbering(t *testing.T) {
	const manifest = `{"id":"addon","resources":[{"name":"stream","types":["series","movie"],"idPrefixes":["tt"]}],"types":["series","movie"]}`
	canonicalBody := `{"streams":[
	 {"name":"Torrentio","title":"One.Piece.-.750.-.ENG.UNCUT.1080p.WEB.H.264.-NanDesuKa.(FUNi)\n👤 42 💾 1.2 GB","infoHash":"` + hashA + `"},
	 {"name":"Torrentio","title":"One Piece Season 18 Complete 1080p WEB-DL\nOne.Piece.S18E04.A.Desperate.Situation.1080p.WEB-DL.mkv\n👤 20 💾 900 MB","infoHash":"` + hashC + `","fileIdx":3}
	]}`
	aliasBody := `{"streams":[
	 {"name":"Torrentio","title":"[Judas] One Piece - S18E04 [1080p]\n👤 35 💾 550 MB","infoHash":"` + hashB + `"},
	 {"name":"Torrentio","title":"One Piece Season 18 Complete 1080p WEB-DL\nOne.Piece.S18E04.A.Desperate.Situation.1080p.WEB-DL.mkv\n👤 20 💾 900 MB","infoHash":"` + hashC + `","fileIdx":3}
	]}`
	f := &fakeAddon{
		manifest: manifest,
		streams: map[string]string{
			"/stream/series/tt0388629:1:750.json": canonicalBody,
			"/stream/series/tt0388629:18:4.json":  aliasBody,
		},
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	b := backendWith(t, srv.URL, Options{Client: srv.Client()}, "ix1")
	req := core.SearchRequest{
		Type:            core.TypeEpisode,
		IDs:             core.IDs{IMDB: "tt0388629", TMDB: 37854, TVDB: 81797},
		Title:           "One Piece",
		Season:          1,
		Episode:         750,
		AbsoluteEpisode: 750,
		Anime:           true,
		ReleaseAliases:  []core.EpisodeNumber{{Season: 18, Episode: 4}},
	}
	rels, err := b.Search(t.Context(), req, noReserve)
	if err != nil {
		t.Fatal(err)
	}
	gotReqs := f.streamRequests()
	if len(gotReqs) != 2 || gotReqs[0] != "/stream/series/tt0388629:1:750.json" || gotReqs[1] != "/stream/series/tt0388629:18:4.json" {
		t.Fatalf("stream requests = %v, want canonical then alias", gotReqs)
	}
	if len(rels) != 3 {
		t.Fatalf("got %d releases (%+v), want 3 deduplicated releases", len(rels), rels)
	}
	if rels[0].InfoHash != hashA || len(rels[0].Info.AbsoluteEpisodes) != 1 || rels[0].Info.AbsoluteEpisodes[0] != 750 {
		t.Fatalf("rels[0] = %+v", rels[0])
	}
	if rels[1].InfoHash != hashC || rels[1].FileName != "One.Piece.S18E04.A.Desperate.Situation.1080p.WEB-DL" || len(rels[1].Info.Seasons) != 1 || rels[1].Info.Seasons[0] != 18 || len(rels[1].Info.Episodes) != 1 || rels[1].Info.Episodes[0] != 4 {
		t.Fatalf("pack alias line was not chosen by shared matcher: %+v", rels[1])
	}
	if rels[2].InfoHash != hashB || len(rels[2].Info.Seasons) != 1 || rels[2].Info.Seasons[0] != 18 || rels[2].Info.Episodes[0] != 4 {
		t.Fatalf("rels[2] = %+v", rels[2])
	}

	// Straight-through TMDB numbering (#613: One Piece S15E583 is absolute
	// 583) asks ":1:<absolute>" first, the season form second, then the
	// group alias (S16E05), even with no XEM alias beyond the group, and
	// merges the answers.
	f613 := &fakeAddon{
		manifest: manifest,
		streams: map[string]string{
			"/stream/series/tt0388629:1:583.json":  `{"streams":[{"name":"AIO","title":"[Group] One Piece - 583 (1080p)","infoHash":"` + hashA + `"}]}`,
			"/stream/series/tt0388629:15:583.json": `{"streams":[{"name":"AIO","title":"One.Piece.S15E583.1080p.WEB-DL-GRP","infoHash":"` + hashB + `"}]}`,
			"/stream/series/tt0388629:16:5.json":   `{"streams":[{"name":"AIO","title":"One.Piece.S16E005.1080p.WEB-DL-GRP","infoHash":"` + hashC + `"}]}`,
		},
	}
	srv613 := httptest.NewServer(f613.handler(t))
	defer srv613.Close()
	b613 := backendWith(t, srv613.URL, Options{Client: srv613.Client()}, "ix613")
	req613 := core.SearchRequest{
		Type: core.TypeEpisode, IDs: core.IDs{IMDB: "tt0388629", TMDB: 37854, TVDB: 81797}, Title: "One Piece",
		Season: 15, Episode: 583, AbsoluteEpisode: 583, Anime: true,
		ReleaseAliases: []core.EpisodeNumber{{Season: 16, Episode: 5}},
	}
	rels613, err := b613.Search(t.Context(), req613, noReserve)
	if err != nil {
		t.Fatal(err)
	}
	got613 := f613.streamRequests()
	if len(got613) < 3 || got613[0] != "/stream/series/tt0388629:1:583.json" || got613[1] != "/stream/series/tt0388629:15:583.json" || got613[2] != "/stream/series/tt0388629:16:5.json" {
		t.Fatalf("straight-through requests = %v, want absolute, season, group in that order", got613)
	}
	hashes := map[string]bool{}
	for _, r := range rels613 {
		hashes[r.InfoHash] = true
	}
	if !hashes[hashA] || !hashes[hashB] || !hashes[hashC] {
		t.Fatalf("merged releases missing a lookup's answer: %+v", rels613)
	}

	// Automatic mode (no XEM/group alias at all) still asks the absolute form.
	fAuto := &fakeAddon{manifest: manifest, streams: map[string]string{
		"/stream/series/tt0388629:1:1161.json":  `{"streams":[{"name":"AIO","title":"[Group] One Piece - 1161 (1080p)","infoHash":"` + hashA + `"}]}`,
		"/stream/series/tt0388629:23:1161.json": `{"streams":[]}`,
	}}
	srvAuto := httptest.NewServer(fAuto.handler(t))
	defer srvAuto.Close()
	bAuto := backendWith(t, srvAuto.URL, Options{Client: srvAuto.Client()}, "ixauto")
	reqAuto := req613
	reqAuto.Season, reqAuto.Episode, reqAuto.AbsoluteEpisode, reqAuto.ReleaseAliases = 23, 1161, 1161, nil
	relsAuto, err := bAuto.Search(t.Context(), reqAuto, noReserve)
	if err != nil {
		t.Fatal(err)
	}
	if got := fAuto.streamRequests(); len(got) < 2 || got[0] != "/stream/series/tt0388629:1:1161.json" || got[1] != "/stream/series/tt0388629:23:1161.json" || len(relsAuto) != 1 {
		t.Fatalf("automatic requests=%v rels=%+v", got, relsAuto)
	}

	// ReleaseAliasesOnly queries only the alias route without repeating canonical.
	fOnly := &fakeAddon{
		manifest: manifest,
		streams:  map[string]string{"/stream/series/tt0388629:18:4.json": aliasBody},
	}
	srvOnly := httptest.NewServer(fOnly.handler(t))
	defer srvOnly.Close()
	bOnly := backendWith(t, srvOnly.URL, Options{Client: srvOnly.Client()}, "ix2")
	aliasOnly := req
	aliasOnly.ReleaseAliasesOnly = true
	relsOnly, err := bOnly.Search(t.Context(), aliasOnly, noReserve)
	if err != nil {
		t.Fatal(err)
	}
	if got := fOnly.streamRequests(); len(got) != 1 || got[0] != "/stream/series/tt0388629:18:4.json" || len(relsOnly) != 2 {
		t.Fatalf("alias-only requests=%v rels=%+v", got, relsOnly)
	}
}

func TestConvertPerEpisodeUsenetSeasonPackTargetsEpisode(t *testing.T) {
	packStream := stream{
		Name:   "AIO Usenet",
		Title:  "Ride.or.Die.S01.2160p.WEB-DL\nRide.or.Die.S01E04.2160p.WEB-DL.mkv\n💾 7.3 GB",
		NZBURL: "https://addon.example/nzb/season1.nzb",
	}
	req4 := core.SearchRequest{Type: core.TypeEpisode, Title: "Ride or Die", Season: 1, Episode: 4}
	req5 := core.SearchRequest{Type: core.TypeEpisode, Title: "Ride or Die", Season: 1, Episode: 5}
	packStream5 := packStream
	packStream5.Title = "Ride.or.Die.S01.2160p.WEB-DL\nRide.or.Die.S01E05.2160p.WEB-DL.mkv\n💾 6.8 GB"

	rels4, _, _ := convertWith([]stream{packStream}, req4, false, convertOptions{instance: "aio"})
	rels5, _, _ := convertWith([]stream{packStream5}, req5, false, convertOptions{instance: "aio"})
	if len(rels4) != 1 || len(rels5) != 1 {
		t.Fatalf("got rels4=%d rels5=%d, want 1 each", len(rels4), len(rels5))
	}
	if rels4[0].Target == nil || rels4[0].Target.Season != 1 || rels4[0].Target.Episode != 4 {
		t.Fatalf("ep4 Usenet pack target = %+v, want S01E04", rels4[0].Target)
	}
	if rels5[0].Target == nil || rels5[0].Target.Season != 1 || rels5[0].Target.Episode != 5 {
		t.Fatalf("ep5 Usenet pack target = %+v, want S01E05", rels5[0].Target)
	}
	k4 := rels4[0].ScopedContentID("nzb:samepack")
	k5 := rels5[0].ScopedContentID("nzb:samepack")
	if k4 == "nzb:samepack" || k5 == "nzb:samepack" || k4 == k5 {
		t.Fatalf("Usenet pack episodes did not get distinct scoped keys: k4=%q k5=%q", k4, k5)
	}
}
