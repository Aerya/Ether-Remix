package newznab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestParsePreservesPosterAndUsenetDateForPreFetchIdentity(t *testing.T) {
	xmlFeed := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
  <channel>
    <item>
      <title>Example.Movie.2024.1080p.WEB-DL.DDP5.1.H.264-GRP</title>
      <guid>https://idx1.example/details/abc123</guid>
      <link>https://idx1.example/getnzb/abc123.nzb</link>
      <pubDate>Thu, 20 Nov 2026 02:15:00 +0000</pubDate>
      <enclosure url="https://idx1.example/getnzb/abc123.nzb" length="4800000000" type="application/x-nzb" />
      <newznab:attr name="size" value="4800000000" />
      <newznab:attr name="poster" value="PosterBot@usenet.test" />
      <newznab:attr name="usenetdate" value="Wed, 19 Nov 2026 23:40:00 +0000" />
    </item>
  </channel>
</rss>`)

	jsonFeed := []byte(`{
  "channel": {
    "item": [{
      "title": "Example.Movie.2024.1080p.WEB-DL.DDP5.1.H.264-GRP-Obfuscated",
      "guid": "https://idx2.example/details/xyz987",
      "link": "https://idx2.example/getnzb/xyz987.nzb",
      "pubDate": "Thu, 20 Nov 2026 09:00:00 +0000",
      "enclosure": {"@attributes": {"url": "https://idx2.example/getnzb/xyz987.nzb", "length": "4850000000", "type": "application/x-nzb"}},
      "attr": [
        {"@attributes": {"name": "poster", "value": "  posterbot@usenet.test  "}},
        {"@attributes": {"name": "usenetDate", "value": "Wed, 19 Nov 2026 18:05:00 +0000"}}
      ]
    }]
  }
}`)

	relsA, err := parseFeed(xmlFeed, "Idx1")
	if err != nil || len(relsA) != 1 {
		t.Fatalf("parseFeed XML: len=%d err=%v", len(relsA), err)
	}
	relsB, err := parseFeed(jsonFeed, "Idx2")
	if err != nil || len(relsB) != 1 {
		t.Fatalf("parseFeed JSON: len=%d err=%v", len(relsB), err)
	}

	preA := relsA[0].PreFetchContentID()
	preB := relsB[0].PreFetchContentID()
	if preA == "" || preA != preB {
		t.Fatalf("expected matching pre-fetch content identity across indexers, got %q vs %q", preA, preB)
	}
	if relsA[0].ID == relsB[0].ID {
		t.Fatalf("per-indexer IDs must stay distinct: %q", relsA[0].ID)
	}
}

func TestManagerRetainsCrossIndexerDuplicatesAsFallbacks(t *testing.T) {
	feedFor := func(guid string) string {
		return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
  <channel>
    <item>
      <title>Same.Release.2024.1080p.BluRay.x264-GRP</title>
      <guid>` + guid + `</guid>
      <link>http://example.invalid/` + guid + `.nzb</link>
      <pubDate>Wed, 19 Nov 2026 12:00:00 +0000</pubDate>
      <newznab:attr name="size" value="5000000000" />
      <newznab:attr name="poster" value="poster@usenet.example" />
    </item>
  </channel>
</rss>`
	}
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(feedFor("guid-a")))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(feedFor("guid-b")))
	}))
	defer srvB.Close()

	mgr := NewManager(nil, []config.Indexer{
		{ID: "ix-a", Name: "Alpha", Kind: "newznab", URL: srvA.URL, APIKey: "k", Enabled: true, Priority: 1},
		{ID: "ix-b", Name: "Beta", Kind: "newznab", URL: srvB.URL, APIKey: "k", Enabled: true, Priority: 2},
	}, Options{HTTPClient: srvA.Client()})

	got, err := mgr.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "Same Release", Year: 2024})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 merged entry", len(got))
	}
	if len(got[0].Fallbacks) != 1 {
		t.Fatalf("len(Fallbacks) = %d, want 1 cross-indexer copy", len(got[0].Fallbacks))
	}
	if got[0].ID == got[0].Fallbacks[0].ID {
		t.Fatalf("expected distinct per-indexer IDs on leader and fallback: %q", got[0].ID)
	}
}
