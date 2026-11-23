package newznab

import (
	"slices"
	"testing"
)

func TestIndexerSubtitlesAttribute(t *testing.T) {
	feed := `<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
<item><title>Movie.2023.1080p.WEB-DL.H264-GRP</title><guid>a</guid><link>https://x.test/a.nzb</link>
<newznab:attr name="subs " value="English, French - spa, ara, isl, None"/></item>
<item><title>Movie.2023.1080p.BluRay.H264-GRP</title><guid>b</guid><link>https://x.test/b.nzb</link></item>
</channel></rss>`
	rels, err := parseFeed([]byte(feed), "idx")
	if err != nil || len(rels) != 2 {
		t.Fatalf("parseFeed = %d releases, %v", len(rels), err)
	}
	if want := []string{"english", "french", "spanish", "arabic", "icelandic"}; !slices.Equal(rels[0].IndexerSubtitles, want) {
		t.Fatalf("subs = %v, want %v", rels[0].IndexerSubtitles, want)
	}
	if len(rels[1].IndexerSubtitles) != 0 {
		t.Fatalf("release without the attribute lists %v", rels[1].IndexerSubtitles)
	}
}
