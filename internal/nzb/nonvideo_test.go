package nzb

import (
	"testing"
)

func TestNonVideoHintRoundTrip(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <file poster="p@test" date="1" subject="&quot;01 - Track.flac&quot; yEnc (1/1)">
    <groups><group>alt.binaries.music</group></groups>
    <segments><segment bytes="1000" number="1">m1@test</segment></segments>
  </file>
</nzb>`)
	doc, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := doc.NonVideoHint(); ok {
		t.Fatal("expected no NonVideoHint on plain NZB")
	}

	idx := 2
	want := NonVideoHint{
		ExplicitFile: true,
		ExactFile:    "01 - Track.flac",
		RawFile:      true,
		Member:       "CD1/01 - Track.flac",
		MemberIndex:  &idx,
		MIME:         "audio/flac",
	}
	out, ok := doc.SubsetMeta(func(*File) bool { return true }, map[string]string{MetaNonVideo: want.String()})
	if !ok {
		t.Fatal("SubsetMeta returned ok=false")
	}
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	got, ok := doc2.NonVideoHint()
	if !ok {
		t.Fatal("NonVideoHint missing after round-trip")
	}
	if !got.ExplicitFile || !got.RawFile || got.ExactFile != want.ExactFile || got.Member != want.Member || got.MIME != want.MIME || got.MemberIndex == nil || *got.MemberIndex != 2 {
		t.Fatalf("NonVideoHint = %+v, want %+v", got, want)
	}
}

func TestNonVideoMatchRank(t *testing.T) {
	cases := []struct {
		file string
		mime string
		want int
	}{
		{"01 - Dreams.flac", "audio/flac", 2},
		{"01 - Dreams.mp3", "audio/flac", 1},
		{"01 - Dreams.m4b", "audio/mp4", 2},
		{"01 - Dreams.m4a", "audio/mp4", 2},
		{"01 - Dreams.mp3", "audio/*", 1},
		{"04 - Bonus Track.flac", "audio/flac", 2},
		{"Movie.2024.mp4", "audio/mp4", 0},
		{"Movie.2024.mkv", "audio/flac", 0},
		{"cover.jpg", "audio/flac", 0},
		{"album.nfo", "audio/flac", 0},
		{"album.par2", "audio/flac", 0},
		{"album.cue", "audio/flac", 0},
		{"Dune.epub", "application/epub+zip", 2},
		{"Dune.pdf", "application/epub+zip", 1},
		{"Dune.mobi", "application/epub+zip", 1},
		{"Dune.azw3", "application/epub+zip", 1},
		{"Dune.pdf", "application/pdf", 2},
		{"metadata.opf", "application/epub+zip", 0},
		{"cover.jpg", "application/epub+zip", 0},
		{"Issue01.cbz", "application/vnd.comicbook+zip", 2},
		{"Issue01.cbr", "application/vnd.comicbook+zip", 1},
		{"01 - Dreams.flac", "video/x-matroska", 0},
		{"01 - Dreams.flac", "", 0},
	}
	for _, tc := range cases {
		if got := NonVideoMatchRank(tc.file, tc.mime); got != tc.want {
			t.Errorf("NonVideoMatchRank(%q, %q) = %d, want %d", tc.file, tc.mime, got, tc.want)
		}
	}
}

func TestMatchPathIndex(t *testing.T) {
	names := []string{
		"Album/CD1/01 - Intro.flac",
		"Album/CD1/02 - Dreams.flac",
		"Album/CD2/01 - Intro.flac",
		"Album/cover.jpg",
	}
	if idx, ok, ambig := MatchPathIndex(names, "album/cd1/02 - dreams.flac"); !ok || ambig || idx != 1 {
		t.Fatalf("exact match = (%d, %v, %v), want (1, true, false)", idx, ok, ambig)
	}
	if idx, ok, ambig := MatchPathIndex(names, "CD2/01 - Intro.flac"); !ok || ambig || idx != 2 {
		t.Fatalf("suffix match = (%d, %v, %v), want (2, true, false)", idx, ok, ambig)
	}
	if idx, ok, ambig := MatchPathIndex(names, "02 - Dreams.flac"); !ok || ambig || idx != 1 {
		t.Fatalf("unique basename match = (%d, %v, %v), want (1, true, false)", idx, ok, ambig)
	}
	if _, ok, ambig := MatchPathIndex(names, "01 - Intro.flac"); ok || !ambig {
		t.Fatalf("ambiguous basename = (ok=%v, ambig=%v), want (false, true)", ok, ambig)
	}
	if _, ok, ambig := MatchPathIndex(names, "missing.flac"); ok || ambig {
		t.Fatalf("missing match = (ok=%v, ambig=%v), want (false, false)", ok, ambig)
	}
}
