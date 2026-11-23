package debrid

import (
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

func TestChooseFileNonVideoAutomaticAndMember(t *testing.T) {
	album := []File{
		{ID: "1", Path: "Album/cover.jpg", Size: 500_000},
		{ID: "2", Path: "Album/album.nfo", Size: 2_000},
		{ID: "3", Path: "Album/01 - Short.flac", Size: 20_000_000},
		{ID: "4", Path: "Album/02 - Long.flac", Size: 45_000_000},
		{ID: "5", Path: "Album/03 - Bigger.mp3", Size: 80_000_000},
	}

	// Exact MIME match (.flac) beats a larger same-family fallback (.mp3), and
	// chooses the largest .flac among ties.
	got, err := ChooseFile(album, &core.ReleaseTarget{AllowNonVideo: true, MIME: "audio/flac"})
	if err != nil {
		t.Fatalf("ChooseFile(audio/flac): %v", err)
	}
	if got.Path != "Album/02 - Long.flac" {
		t.Fatalf("ChooseFile(audio/flac) = %q, want Album/02 - Long.flac", got.Path)
	}

	// Broad audio/* MIME picks the largest audio file.
	got, err = ChooseFile(album, &core.ReleaseTarget{AllowNonVideo: true, MIME: "audio/*"})
	if err != nil {
		t.Fatalf("ChooseFile(audio/*): %v", err)
	}
	if got.Path != "Album/03 - Bigger.mp3" {
		t.Fatalf("ChooseFile(audio/*) = %q, want Album/03 - Bigger.mp3", got.Path)
	}

	// Member hint selects an unambiguous basename directly.
	got, err = ChooseFile(album, &core.ReleaseTarget{AllowNonVideo: true, Member: "01 - Short.flac", MIME: "audio/flac"})
	if err != nil {
		t.Fatalf("ChooseFile(member): %v", err)
	}
	if got.Path != "Album/01 - Short.flac" {
		t.Fatalf("ChooseFile(member) = %q, want Album/01 - Short.flac", got.Path)
	}

	// Fallback to a single-volume archive when no loose book file exists.
	archivedBook := []File{
		{ID: "1", Path: "Release/info.nfo", Size: 1_000},
		{ID: "2", Path: "Release/Dune.zip", Size: 12_000_000},
	}
	got, err = ChooseFile(archivedBook, &core.ReleaseTarget{AllowNonVideo: true, MIME: "application/epub+zip"})
	if err != nil {
		t.Fatalf("ChooseFile(archived book): %v", err)
	}
	if got.Path != "Release/Dune.zip" {
		t.Fatalf("ChooseFile(archived book) = %q, want Release/Dune.zip", got.Path)
	}

	// Multi-volume archive sets cannot be opened from a single debrid URL: fail
	// structurally with ErrNoUsableFile instead of picking part01.rar.
	multiVol := []File{
		{ID: "1", Path: "Release/Album.part01.rar", Size: 50_000_000},
		{ID: "2", Path: "Release/Album.part02.rar", Size: 50_000_000},
	}
	if _, err := ChooseFile(multiVol, &core.ReleaseTarget{AllowNonVideo: true, MIME: "audio/flac"}); !errors.Is(err, core.ErrNoUsableFile) || !errors.Is(err, core.ErrReleaseRejected) {
		t.Fatalf("ChooseFile(multi-vol rar) err = %v, want ErrNoUsableFile + ErrReleaseRejected", err)
	}

	// Video-only torrent rejected structurally for an audio request.
	videoOnly := []File{
		{ID: "1", Path: "Movie.2024.1080p.mkv", Size: 4_000_000_000},
		{ID: "2", Path: "Movie.2024.mp4", Size: 2_000_000_000},
	}
	if _, err := ChooseFile(videoOnly, &core.ReleaseTarget{AllowNonVideo: true, MIME: "audio/mp4"}); !errors.Is(err, core.ErrNoUsableFile) || !errors.Is(err, core.ErrReleaseRejected) {
		t.Fatalf("ChooseFile(videoOnly for audio/mp4) err = %v, want ErrNoUsableFile + ErrReleaseRejected", err)
	}
}

func TestEpisodeNZBNonVideoMetadataAndPayloadKey(t *testing.T) {
	data := packNZB(
		`&quot;01 - Track.flac&quot; yEnc (1/1)`,
		`&quot;02 - Track.flac&quot; yEnc (1/1)`,
		`&quot;Album.part1.rar&quot; yEnc (1/1)`,
		`&quot;Album.part2.rar&quot; yEnc (1/1)`,
	)

	// Explicit loose file narrows and marks RawFile=true.
	out, err := EpisodeNZB(data, &core.ReleaseTarget{
		AllowNonVideo: true,
		ExactFile:     "02 - Track.flac",
		MIME:          "audio/flac",
	})
	if err != nil {
		t.Fatalf("EpisodeNZB explicit loose: %v", err)
	}
	doc, err := nzb.Parse(out)
	if err != nil {
		t.Fatalf("nzb.Parse: %v", err)
	}
	hint, ok := doc.NonVideoHint()
	if !ok || !hint.ExplicitFile || !hint.RawFile || hint.ExactFile != "02 - Track.flac" || hint.MIME != "audio/flac" {
		t.Fatalf("explicit loose hint = (%+v, %v)", hint, ok)
	}
	if len(doc.Files) != 1 || doc.Files[0].Name != "02 - Track.flac" {
		t.Fatalf("explicit loose files = %+v", doc.Files)
	}

	// Automatic non-video MIME preserves all files and embeds NonVideoHint;
	// an existing NZB password is preserved, and ArchivePassword is added when absent.
	out, err = EpisodeNZB(data, &core.ReleaseTarget{
		AllowNonVideo:   true,
		Member:          "01 - Track.flac",
		MIME:            "audio/flac",
		ArchivePassword: "secret",
	})
	if err != nil {
		t.Fatalf("EpisodeNZB auto: %v", err)
	}
	doc, err = nzb.Parse(out)
	if err != nil {
		t.Fatalf("nzb.Parse auto: %v", err)
	}
	hint, ok = doc.NonVideoHint()
	if !ok || hint.ExplicitFile || hint.Member != "01 - Track.flac" || hint.MIME != "audio/flac" {
		t.Fatalf("auto hint = (%+v, %v)", hint, ok)
	}
	if doc.Password() != "pw" {
		t.Fatalf("Password() = %q, want existing pw", doc.Password())
	}

	noPwNZB := []byte(`<nzb><file poster="p" date="1" subject="&quot;Album.rar&quot; yEnc (1/1)"><groups><group>a.b</group></groups><segments><segment bytes="100" number="1">id0@x</segment></segments></file></nzb>`)
	outNoPw, err := EpisodeNZB(noPwNZB, &core.ReleaseTarget{
		AllowNonVideo:   true,
		MIME:            "audio/flac",
		ArchivePassword: "secret",
	})
	if err != nil {
		t.Fatalf("EpisodeNZB noPw: %v", err)
	}
	docNoPw, err := nzb.Parse(outNoPw)
	if err != nil {
		t.Fatalf("nzb.Parse noPw: %v", err)
	}
	if docNoPw.Password() != "secret" {
		t.Fatalf("noPw Password() = %q, want secret", docNoPw.Password())
	}

	// payloadKey isolates different non-video targets on the same release.
	k1 := payloadKey(core.Release{ID: "rel1", Target: &core.ReleaseTarget{AllowNonVideo: true, Member: "01.flac", MIME: "audio/flac"}})
	k2 := payloadKey(core.Release{ID: "rel1", Target: &core.ReleaseTarget{AllowNonVideo: true, Member: "02.flac", MIME: "audio/flac"}})
	if k1 == k2 {
		t.Fatalf("payloadKey collided for distinct members: %q", k1)
	}
}
