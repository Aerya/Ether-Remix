package nzb

import (
	"testing"
	"time"
)

func TestContentIdentityInvarianceAndRoundTrip(t *testing.T) {
	nzbIndexerA := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <file poster="PosterOne &lt;p1@usenet.example&gt;" date="1763510400" subject="Movie.2024.1080p.WEB-DL.DDP5.1.H.264-GRP [01/02] - &quot;movie.part01.rar&quot; yEnc (1/2)">
    <groups><group>alt.binaries.movies</group></groups>
    <segments>
      <segment bytes="500000" number="1">&lt;seg-b@news.example&gt;</segment>
      <segment bytes="500000" number="2">seg-a@news.example</segment>
    </segments>
  </file>
  <file poster="PosterOne &lt;p1@usenet.example&gt;" date="1763510405" subject="Movie.2024.1080p.WEB-DL.DDP5.1.H.264-GRP [02/02] - &quot;movie.par2&quot; yEnc (1/1)">
    <groups><group>alt.binaries.movies</group></groups>
    <segments>
      <segment bytes="100000" number="1">seg-c@news.example</segment>
    </segments>
  </file>
</nzb>`)

	// Same Usenet articles on another indexer: files reversed, obfuscated subjects, no angle brackets.
	nzbIndexerB := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<nzb>
  <file poster="PosterOne &lt;p1@usenet.example&gt;" date="1763510405" subject="9f8e7d6c5b4a3210 [1/1] - &quot;9f8e7d6c5b4a3210&quot; yEnc (1/1)">
    <groups><group>alt.binaries.boneless</group></groups>
    <segments>
      <segment bytes="100000" number="1">seg-c@news.example</segment>
    </segments>
  </file>
  <file poster="PosterOne &lt;p1@usenet.example&gt;" date="1763510400" subject="1a2b3c4d5e6f7890 [1/2] - &quot;1a2b3c4d5e6f7890&quot; yEnc (1/2)">
    <groups><group>alt.binaries.boneless</group></groups>
    <segments>
      <segment bytes="500000" number="1">seg-a@news.example</segment>
      <segment bytes="500000" number="2">seg-b@news.example</segment>
    </segments>
  </file>
</nzb>`)

	idA, poster, uploaded, total := InspectContent(nzbIndexerA)
	if idA == "" {
		t.Fatal("expected non-empty content ID for indexer A NZB")
	}
	if poster != "PosterOne <p1@usenet.example>" {
		t.Fatalf("poster = %q", poster)
	}
	if !uploaded.Equal(time.Unix(1763510400, 0).UTC()) {
		t.Fatalf("uploadDate = %v", uploaded)
	}
	if total != 1_100_000 {
		t.Fatalf("totalBytes = %d, want 1100000", total)
	}

	idB := ContentID(nzbIndexerB)
	if idB != idA {
		t.Fatalf("cross-indexer content ID mismatch: %q vs %q", idA, idB)
	}

	doc, err := Parse(nzbIndexerB)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.ContentID() != idA {
		t.Fatalf("parsed NZB ContentID = %q, want %q", doc.ContentID(), idA)
	}
	sumA, ok := ContentSumBytes(nzbIndexerA)
	if !ok {
		t.Fatal("ContentSumBytes returned false")
	}
	sumDoc, ok := doc.ContentSum()
	if !ok || sumDoc != sumA {
		t.Fatalf("ContentSum mismatch: %x vs %x", sumDoc, sumA)
	}
	parsedSum, ok := ParseContentID(idA)
	if !ok || parsedSum != sumA {
		t.Fatalf("ParseContentID(%q) = %x, %v; want %x", idA, parsedSum, ok, sumA)
	}

	otherNZB := []byte(`<nzb><file><segments><segment number="1">different-msg@news.example</segment></segments></file></nzb>`)
	if got := ContentID(otherNZB); got == "" || got == idA {
		t.Fatalf("distinct NZB produced %q (collided with %q)", got, idA)
	}
	if got := ContentID([]byte(`{"protocol":"torrent"}`)); got != "" {
		t.Fatalf("JSON payload produced NZB content ID %q", got)
	}
}
