package stream

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/par2/par2test"
)

func TestListFilesExactSizesFromPar2WithoutProbingMedia(t *testing.T) {
	f := newFakeFetcher()
	track1 := randBytes(101, 250_000)
	track2 := randBytes(102, 180_543)
	idx := par2Index(16_384,
		par2test.File{Name: "01 - Opening.flac", Data: track1},
		par2test.File{Name: "02 - Finale.flac", Data: track2},
	)
	doc := post(t, f, 100_000,
		postFile{name: "01 - Opening.flac", data: track1},
		postFile{name: "02 - Finale.flac", data: track2},
		postFile{name: "album.par2", data: idx},
	)

	e := newTestEngine(t, f, nil, Options{})
	files, err := e.ListFiles(context.Background(), "rel-par2-music", doc)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("len(files) = %d, want 3: %+v", len(files), files)
	}
	if files[0].Path != "01 - Opening.flac" || files[0].Size != int64(len(track1)) || files[0].SizeApproximate {
		t.Fatalf("track 1 = %+v, want exact %d", files[0], len(track1))
	}
	if files[1].Path != "02 - Finale.flac" || files[1].Size != int64(len(track2)) || files[1].SizeApproximate {
		t.Fatalf("track 2 = %+v, want exact %d", files[1], len(track2))
	}
	// PAR2 index alone resolved both audio tracks without fetching either track's articles.
	if f.calls(postID("01 - Opening.flac", 1)) != 0 || f.calls(postID("02 - Finale.flac", 1)) != 0 {
		t.Fatalf("expected 0 media article probes when PAR2 resolves sizes; calls = %d, %d",
			f.calls(postID("01 - Opening.flac", 1)), f.calls(postID("02 - Finale.flac", 1)))
	}
}

func TestListFilesExactSizesFromYEncWithoutPar2(t *testing.T) {
	f := newFakeFetcher()
	track1 := randBytes(201, 220_333)
	track2 := randBytes(202, 45_123)
	doc := post(t, f, 100_000,
		postFile{name: "01 - Track.flac", data: track1},
		postFile{name: "02 - SinglePart.flac", data: track2},
	)

	// Confirm the NZB's encoded segment sum differs from decoded bytes.
	parsed, err := nzb.Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Files[0].Bytes() == int64(len(track1)) {
		// Inflate listed segment bytes as real indexers do for yEnc overhead.
		doc = postWithInflatedBytes(t, f, 100_000, track1, track2)
	}

	e := newTestEngine(t, f, nil, Options{})
	files, err := e.ListFiles(context.Background(), "rel-yenc-music", doc)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
	if files[0].Size != int64(len(track1)) || files[0].SizeApproximate {
		t.Fatalf("track 1 = %+v, want exact %d", files[0], len(track1))
	}
	if files[1].Size != int64(len(track2)) || files[1].SizeApproximate {
		t.Fatalf("track 2 = %+v, want exact %d", files[1], len(track2))
	}
}

func TestListFilesObfuscatedPar2NonVideo(t *testing.T) {
	f := newFakeFetcher()
	track := randBytes(301, 140_777)
	idx := par2Index(16_384, par2test.File{Name: "01 - Hidden.flac", Data: track})
	doc := post(t, f, 100_000,
		postFile{name: obfName(7), data: track},
		postFile{name: "album.par2", data: idx},
	)

	e := newTestEngine(t, f, nil, Options{})
	files, err := e.ListFiles(context.Background(), "rel-obf-music", doc)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if files[0].Path != "01 - Hidden.flac" || files[0].Size != int64(len(track)) || files[0].SizeApproximate {
		t.Fatalf("obfuscated track = %+v, want path=01 - Hidden.flac exact size=%d", files[0], len(track))
	}
	// Repeated ListFiles and NameFiles after ListFiles must keep the learned
	// name stable and preserve the original subject name in the re-encoded NZB.
	again, err := e.ListFiles(context.Background(), "rel-obf-music", doc)
	if err != nil {
		t.Fatalf("second ListFiles: %v", err)
	}
	if again[0].Path != "01 - Hidden.flac" || again[0].Size != int64(len(track)) || again[0].SizeApproximate {
		t.Fatalf("second ListFiles drifted: %+v", again[0])
	}
	namedXML, err := e.NameFiles(context.Background(), "rel-obf-music", doc)
	if err != nil {
		t.Fatalf("NameFiles after ListFiles: %v", err)
	}
	parsedNamed, err := nzb.Parse(namedXML)
	if err != nil {
		t.Fatal(err)
	}
	if parsedNamed.Files[0].Name != "01 - Hidden.flac" || parsedNamed.Files[0].OriginalName() != obfName(7) {
		t.Fatalf("NameFiles after ListFiles = name=%q orig=%q", parsedNamed.Files[0].Name, parsedNamed.Files[0].OriginalName())
	}
}

func TestListFilesKeepsStableSubjectNameWhenRenamed(t *testing.T) {
	f := newFakeFetcher()
	raw := []byte(`<nzb><file poster="p" date="1" subject="Some Show Episode Title 4k.mp4 (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="100" number="1">s1@test</segment></segments></file></nzb>`)
	doc, err := nzb.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	doc.Files[0].Name = "4k.mp4"
	reencoded, ok := doc.Subset(func(*nzb.File) bool { return true })
	if !ok {
		t.Fatal("Subset failed")
	}
	e := newTestEngine(t, f, nil, Options{})
	files, err := e.ListFiles(context.Background(), "rel-renamed", reencoded)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != "Some Show Episode Title 4k.mp4" {
		t.Fatalf("ListFiles = %+v, want stable subject name", files)
	}
}

func TestListFilesFallsBackToApproximateWhenArticlesUnavailable(t *testing.T) {
	f := newFakeFetcher()
	// Put an NZB referencing an article that is missing from the provider.
	nzbDoc := []byte(`<nzb>` +
		`<file poster="p" date="1" subject="&quot;01 - Track.flac&quot; yEnc (1/2)">` +
		`<groups><group>alt.binaries.music</group></groups>` +
		`<segments>` +
		`<segment bytes="750000" number="1">missing-1@test</segment>` +
		`<segment bytes="750000" number="2">missing-2@test</segment>` +
		`</segments></file></nzb>`)
	parsed, err := nzb.Parse(nzbDoc)
	if err != nil {
		t.Fatal(err)
	}
	f.setMissing("missing-1@test")

	e := newTestEngine(t, f, nil, Options{})
	files, err := e.ListFiles(context.Background(), "rel-missing", nzbDoc)
	if err != nil {
		t.Fatalf("ListFiles should degrade to approximate estimate, got err: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	wantEst := parsed.Files[0].EstimatedDecodedBytes()
	if !files[0].SizeApproximate || files[0].Size != wantEst || files[0].Size >= parsed.Files[0].Bytes() {
		t.Fatalf("fallback file = %+v, want approximate size %d (< encoded %d)", files[0], wantEst, parsed.Files[0].Bytes())
	}
}

func postWithInflatedBytes(t *testing.T, f *fakeFetcher, part int, track1, track2 []byte) []byte {
	t.Helper()
	// Re-post with realistic +3.1% encoded segment sizes in the NZB XML while
	// the fakeFetcher serves the real decoded yEnc headers (=ybegin size=).
	doc := post(t, f, part,
		postFile{name: "01 - Track.flac", data: track1},
		postFile{name: "02 - SinglePart.flac", data: track2},
	)
	parsed, err := nzb.Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, pf := range parsed.Files {
		for i := range pf.Segments {
			pf.Segments[i].Bytes = pf.Segments[i].Bytes * 1031 / 1000
		}
	}
	out, ok := parsed.Subset(func(*nzb.File) bool { return true })
	if !ok {
		t.Fatal("subset failed")
	}
	return out
}
