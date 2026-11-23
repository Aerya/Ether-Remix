package newznab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestQueryTitlesDropPunctuationAndFoldAccents(t *testing.T) {
	got := queryTitles(core.SearchRequest{Title: "O Senhor dos Anéis: A Sociedade do Anel", OriginalTitle: "The Lord of the Rings: The Fellowship of the Ring"})
	want := map[string]bool{"O Senhor dos Anéis A Sociedade do Anel": true, "O Senhor dos Aneis A Sociedade do Anel": true, "The Lord of the Rings The Fellowship of the Ring": true}
	for w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("queryTitles = %q, missing %q", got, w)
		}
	}
	for _, g := range got {
		for _, r := range g {
			if r == ':' {
				t.Fatalf("query %q keeps a colon", g)
			}
		}
	}
}

func TestQueryTitlesIncludeValidatedSubtitleAliasesAndEnglishTitle(t *testing.T) {
	got := queryTitles(core.SearchRequest{
		Title:             "Demon Slayer: Kimetsu no Yaiba",
		OriginalTitle:     "鬼滅の刃",
		AlternativeTitles: []string{"Demon Slayer", "Kimetsu no Yaiba"},
	})
	for _, want := range []string{"Demon Slayer Kimetsu no Yaiba", "鬼滅の刃", "Demon Slayer", "Kimetsu no Yaiba"} {
		if !slices.Contains(got, want) {
			t.Fatalf("queryTitles = %q, missing %q", got, want)
		}
	}

	unconfirmed := queryTitles(core.SearchRequest{
		Title:             "The Signal: Reloaded",
		AlternativeTitles: []string{"Das Signal: Reloaded"},
	})
	if slices.Contains(unconfirmed, "The Signal") || slices.Contains(unconfirmed, "Reloaded") {
		t.Fatalf("unconfirmed subtitle halves included: %q", unconfirmed)
	}

	localized := queryTitles(core.SearchRequest{
		Title:         "Divertida Mente",
		OriginalTitle: "Inside Out",
		EnglishTitle:  "Inside Out",
	})
	if !slices.Contains(localized, "Inside Out") {
		t.Fatalf("localized queryTitles = %q, missing Inside Out", localized)
	}
	nonEngOrig := queryTitles(core.SearchRequest{
		Title:         "A Viagem de Chihiro",
		OriginalTitle: "千と千尋の神隠し",
		EnglishTitle:  "Spirited Away",
	})
	if !slices.Contains(nonEngOrig, "Spirited Away") {
		t.Fatalf("non-English original queryTitles = %q, missing EnglishTitle", nonEngOrig)
	}
}

func TestUsenetEpisodeSearchFallsBackToSubtitleAliasSeasonPack(t *testing.T) {
	const packTitle = "Demon.Slayer.S01.1080p.BluRay.FLAC2.0.H.265.DUAL-Anitsu"
	const capsXML = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q,tvdbid,season,ep"/>
    <movie-search available="yes" supportedParams="q,imdbid"/>
  </searching>
  <categories>
    <category id="5000" name="TV"><subcat id="5040" name="HD"/></category>
  </categories>
</caps>`
	const emptyRSS = `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel></channel></rss>`
	packRSS := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>%s</title><guid>pack-1</guid><link>http://example.invalid/get/pack-1.nzb</link><enclosure url="http://example.invalid/get/pack-1.nzb" length="24000000000" type="application/x-nzb"/><newznab:attr name="category" value="5000"/></item></channel></rss>`, packTitle)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("t") == "caps" {
			fmt.Fprint(w, capsXML)
			return
		}
		hits.Add(1)
		if q.Get("t") == "search" && q.Get("q") == "Demon Slayer S01" {
			fmt.Fprint(w, packRSS)
			return
		}
		fmt.Fprint(w, emptyRSS)
	}))
	defer srv.Close()

	cl := New(config.Indexer{
		ID:       "idx-usenet",
		Name:     "usenet-indexer",
		URL:      srv.URL,
		Protocol: "usenet",
		Enabled:  true,
	}, nil, Options{})

	// Speculative search must still spend only 1 primary query.
	req1 := core.SearchRequest{
		Type:              core.TypeEpisode,
		IDs:               core.IDs{TMDB: 85937, TVDB: 348545, IMDB: "tt9335498"},
		Title:             "Demon Slayer: Kimetsu no Yaiba",
		OriginalTitle:     "鬼滅の刃",
		AlternativeTitles: []string{"Demon Slayer", "Kimetsu no Yaiba"},
		Year:              2019,
		Season:            1,
		Episode:           1,
		AbsoluteEpisode:   1,
		Anime:             true,
	}
	warm := core.WithUnattendedSearch(core.WithIndexerPurpose(context.Background(), core.IndexerPurposeWarmup))
	res, err := cl.Search(warm, req1)
	if err != nil {
		t.Fatalf("speculative Search error: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("speculative Search got %d releases, want 0", len(res))
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("speculative Search made %d hits, want 1", got)
	}

	// Active Play search falls back to validated subtitle alias + S01 season pack
	// for both episode 1 and episode 2.
	for _, ep := range []int{1, 2} {
		req := req1
		req.Episode = ep
		req.AbsoluteEpisode = ep
		rels, err := cl.Search(context.Background(), req)
		if err != nil {
			t.Fatalf("ep %d Search error: %v", ep, err)
		}
		if len(rels) != 1 || rels[0].Name != packTitle {
			t.Fatalf("ep %d Search = %+v, want %q", ep, rels, packTitle)
		}
	}
}

func TestUsenetEpisodeSearchFallsBackToTVDBSeasonPackAndSkipsOnPrimaryHit(t *testing.T) {
	const epRSS = `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><item><title>Sample.Show.S01E01.1080p.WEB-DL-GRP</title><guid>ep-1</guid><link>http://example.invalid/get/ep-1.nzb</link><enclosure url="http://example.invalid/get/ep-1.nzb" length="1500000000" type="application/x-nzb"/></item></channel></rss>`
	const packRSS = `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><item><title>Pack.Only.Show.S01.1080p.BluRay-GRP</title><guid>pk-1</guid><link>http://example.invalid/get/pk-1.nzb</link><enclosure url="http://example.invalid/get/pk-1.nzb" length="15000000000" type="application/x-nzb"/></item></channel></rss>`
	const emptyRSS = `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel></channel></rss>`
	const capsXML = `<?xml version="1.0" encoding="UTF-8"?><caps><searching><search available="yes" supportedParams="q"/><tv-search available="yes" supportedParams="q,tvdbid,season,ep"/></searching></caps>`

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("t") == "caps" {
			fmt.Fprint(w, capsXML)
			return
		}
		hits.Add(1)
		if q.Get("tvdbid") == "111" && q.Get("season") == "1" && q.Get("ep") == "1" {
			fmt.Fprint(w, epRSS)
			return
		}
		if q.Get("tvdbid") == "222" && q.Get("season") == "1" && q.Get("ep") == "" {
			fmt.Fprint(w, packRSS)
			return
		}
		fmt.Fprint(w, emptyRSS)
	}))
	defer srv.Close()

	cl := New(config.Indexer{
		ID:       "idx-usenet-2",
		Name:     "usenet-indexer-2",
		URL:      srv.URL,
		Protocol: "usenet",
		Enabled:  true,
	}, nil, Options{})

	// Primary episode hit stops immediately without running season-pack queries.
	rels, err := cl.Search(context.Background(), core.SearchRequest{
		Type: core.TypeEpisode, IDs: core.IDs{TVDB: 111}, Title: "Sample Show", Season: 1, Episode: 1,
	})
	if err != nil || len(rels) != 1 || hits.Load() != 1 {
		t.Fatalf("primary hit: len=%d hits=%d err=%v", len(rels), hits.Load(), err)
	}

	// Episode miss falls back to tvdbid+season pack query.
	rels, err = cl.Search(context.Background(), core.SearchRequest{
		Type: core.TypeEpisode, IDs: core.IDs{TVDB: 222}, Title: "Pack Only Show", Season: 1, Episode: 3,
	})
	if err != nil || len(rels) != 1 || rels[0].Name != "Pack.Only.Show.S01.1080p.BluRay-GRP" {
		t.Fatalf("season pack fallback: rels=%+v err=%v", rels, err)
	}
}

func TestRescueAttemptsKeepAlternativeEpisodeQueriesBeforeSeasonPacks(t *testing.T) {
	for _, proto := range []string{"usenet", "torrent"} {
		cl := New(config.Indexer{Name: "test", URL: "http://example.invalid", Protocol: proto, Enabled: true}, nil, Options{})
		req := core.SearchRequest{
			Type:              core.TypeEpisode,
			IDs:               core.IDs{TMDB: 65282, IMDB: "tt7997010", TVDB: 298704},
			Title:             "Home Alone",
			OriginalTitle:     "나 혼자 산다",
			AlternativeTitles: []string{"I Live Alone"},
			Year:              2013,
			Season:            1,
			Episode:           674,
			Released:          time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC),
		}
		attempts := cl.rescueAttempts(req, &Caps{})
		altIdx, packIdx := -1, -1
		for i, a := range attempts {
			if a.Get("q") == "I Live Alone S01E674" && altIdx < 0 {
				altIdx = i
			}
			if a.Get("q") == "Home Alone S01" && packIdx < 0 {
				packIdx = i
			}
		}
		if altIdx < 0 || packIdx < 0 || altIdx >= packIdx {
			t.Fatalf("proto=%s: altIdx=%d packIdx=%d; expected alternative episode query before season pack", proto, altIdx, packIdx)
		}
	}
}
