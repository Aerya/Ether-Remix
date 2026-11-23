package stream

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	at "github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

func annotateNonVideoNZB(t *testing.T, raw []byte, hint nzb.NonVideoHint) []byte {
	t.Helper()
	doc, err := nzb.Parse(raw)
	if err != nil {
		t.Fatalf("nzb.Parse: %v", err)
	}
	out, ok := doc.SubsetMeta(func(*nzb.File) bool { return true }, map[string]string{nzb.MetaNonVideo: hint.String()})
	if !ok {
		t.Fatal("SubsetMeta returned ok=false")
	}
	return out
}

func flacBytes(seed uint64, n int) []byte {
	b := randBytes(seed, n)
	copy(b, []byte("fLaC"))
	return b
}

func epubBytes(seed uint64, n int) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	w, _ := zw.CreateHeader(hdr)
	_, _ = w.Write([]byte("application/epub+zip"))
	ch, _ := zw.CreateHeader(&zip.FileHeader{Name: "OEBPS/ch1.xhtml", Method: zip.Store})
	_, _ = ch.Write(randBytes(seed, n))
	_ = zw.Close()
	return buf.Bytes()
}

func buildZipArchive(t *testing.T, method uint16, files []struct {
	name string
	data []byte
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: method})
		if err != nil {
			t.Fatalf("zip CreateHeader(%s): %v", f.name, err)
		}
		if _, err := w.Write(f.data); err != nil {
			t.Fatalf("zip Write(%s): %v", f.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	return buf.Bytes()
}

func TestNonVideoLooseAudioExplicitAndAutomatic(t *testing.T) {
	f := newFakeFetcher()
	e := newTestEngine(t, f, nil, Options{})

	track1 := flacBytes(11, 200_000)
	track2 := flacBytes(12, 350_000)
	biggerMP3 := append([]byte("ID3"), randBytes(13, 500_000)...)
	cover := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, randBytes(14, 16_000)...)

	rawNZB := post(t, f, 64*1024,
		postFile{name: "cover.jpg", data: cover},
		postFile{name: "01 - Bonus Track.flac", data: track1},
		postFile{name: "02 - Dreams.flac", data: track2},
		postFile{name: "03 - Bigger.mp3", data: biggerMP3},
	)

	// Without NonVideoHint, video planning still rejects audio-only releases.
	if _, err := e.Open(context.Background(), "video-only-check", rawNZB); !errors.Is(err, ErrNoPlayableFile) || !errors.Is(err, core.ErrNoUsableFile) {
		t.Fatalf("plain audio NZB without NonVideoHint: err = %v, want ErrNoPlayableFile + ErrNoUsableFile", err)
	}

	// Explicit loose file serves exact bytes with Range reads and keeps exact name.
	explicitNZB := annotateNonVideoNZB(t, rawNZB, nzb.NonVideoHint{
		ExplicitFile: true,
		ExactFile:    "01 - Bonus Track.flac",
		RawFile:      true,
		MIME:         "audio/flac",
	})
	src := openSource(t, e, "explicit-flac", explicitNZB)
	if src.Name() != "01 - Bonus Track.flac" {
		t.Fatalf("explicit Name() = %q, want 01 - Bonus Track.flac", src.Name())
	}
	if src.Size() != int64(len(track1)) {
		t.Fatalf("explicit Size() = %d, want %d", src.Size(), len(track1))
	}
	checkRandomReads(t, src, track1, 101, 16)

	// Automatic audio/flac prefers exact-MIME .flac (largest) over larger .mp3.
	autoNZB := annotateNonVideoNZB(t, rawNZB, nzb.NonVideoHint{MIME: "audio/flac"})
	autoSrc := openSource(t, e, "auto-flac", autoNZB)
	if autoSrc.Name() != "02 - Dreams.flac" {
		t.Fatalf("auto Name() = %q, want 02 - Dreams.flac", autoSrc.Name())
	}
	if !bytes.Equal(readAll(t, autoSrc, 32*1024), track2) {
		t.Fatal("auto audio/flac bytes mismatch")
	}

	// Automatic non-video MIME on a video-only NZB fails with ErrNoUsableFile.
	videoOnlyNZB := annotateNonVideoNZB(t, post(t, f, 64*1024,
		postFile{name: "Movie.2024.1080p.mkv", data: mkvBytes(21, 128*1024)},
	), nzb.NonVideoHint{MIME: "audio/flac"})
	if _, err := e.Open(context.Background(), "video-for-audio", videoOnlyNZB); !errors.Is(err, ErrNoPlayableFile) || !errors.Is(err, core.ErrNoUsableFile) {
		t.Fatalf("video-only NZB for audio/flac: err = %v, want ErrNoPlayableFile + ErrNoUsableFile", err)
	}
}

func TestNonVideoStoredArchivesRARAndZIP(t *testing.T) {
	f := newFakeFetcher()
	e := newTestEngine(t, f, nil, Options{})

	track1 := flacBytes(31, 120_000)
	track2 := flacBytes(32, 240_000)
	cover := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, randBytes(33, 8_000)...)

	// Multi-volume stored RAR5 archive holding an album.
	vols, err := at.BuildRAR([]at.RARFile{
		{Name: "Album/cover.jpg", Data: cover},
		{Name: "Album/01 - Intro.flac", Data: track1},
		{Name: "Album/02 - Dreams.flac", Data: track2},
	}, at.RAROptions{Version: 5, VolumeSize: 150_000})
	if err != nil {
		t.Fatalf("BuildRAR: %v", err)
	}
	if len(vols) < 2 {
		t.Fatalf("expected multi-volume RAR, got %d volume(s)", len(vols))
	}
	var posts []postFile
	for i, v := range vols {
		posts = append(posts, postFile{name: "Album.part0" + string(rune('1'+i)) + ".rar", data: v})
	}
	rarNZB := post(t, f, 64*1024, posts...)

	// Explicit archive member via fileHint.
	memberNZB := annotateNonVideoNZB(t, rarNZB, nzb.NonVideoHint{
		Member: "01 - Intro.flac",
		MIME:   "audio/flac",
	})
	src := openSource(t, e, "rar-member", memberNZB)
	if src.Name() != "01 - Intro.flac" {
		t.Fatalf("RAR member Name() = %q, want 01 - Intro.flac", src.Name())
	}
	rms, ok := src.(core.ResolvedMemberSource)
	if !ok || !rms.ResolvedArchiveMember() {
		t.Fatalf("expected ResolvedArchiveMember() == true on stored RAR member source")
	}
	checkRandomReads(t, src, track1, 202, 16)

	// Automatic audio/flac selects largest .flac (never cover.jpg).
	autoRAR := annotateNonVideoNZB(t, rarNZB, nzb.NonVideoHint{MIME: "audio/flac"})
	autoSrc := openSource(t, e, "rar-auto", autoRAR)
	if autoSrc.Name() != "02 - Dreams.flac" {
		t.Fatalf("RAR auto Name() = %q, want 02 - Dreams.flac", autoSrc.Name())
	}
	if !bytes.Equal(readAll(t, autoSrc, 32*1024), track2) {
		t.Fatal("RAR auto bytes mismatch")
	}

	// Stored ZIP holding an EPUB + cover.jpg.
	book := epubBytes(41, 64_000)
	zipRaw := buildZipArchive(t, zip.Store, []struct {
		name string
		data []byte
	}{
		{name: "cover.jpg", data: cover},
		{name: "Books/Dune.epub", data: book},
	})
	zipNZB := annotateNonVideoNZB(t, post(t, f, 64*1024, postFile{name: "Dune.zip", data: zipRaw}), nzb.NonVideoHint{
		MIME: "application/epub+zip",
	})
	zipSrc := openSource(t, e, "zip-epub", zipNZB)
	if zipSrc.Name() != "Dune.epub" {
		t.Fatalf("ZIP auto Name() = %q, want Dune.epub", zipSrc.Name())
	}
	if !bytes.Equal(readAll(t, zipSrc, 16*1024), book) {
		t.Fatal("ZIP stored epub bytes mismatch")
	}
}

func TestNonVideoCompressedArchiveHandsOffVolumes(t *testing.T) {
	f := newFakeFetcher()
	e := newTestEngine(t, f, nil, Options{})

	// 1. Deflate ZIP -> hands off ArchiveVolumeSource("zip", "Books/Dune.epub").
	book := epubBytes(51, 32_000)
	zipDeflate := buildZipArchive(t, zip.Deflate, []struct {
		name string
		data []byte
	}{
		{name: "Books/Dune.epub", data: book},
	})
	zipNZB := annotateNonVideoNZB(t, post(t, f, 32*1024, postFile{name: "Dune-deflate.zip", data: zipDeflate}), nzb.NonVideoHint{
		MIME: "application/epub+zip",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ms, err := e.Open(ctx, "zip-deflate", zipNZB)
	if err != nil {
		t.Fatalf("Open deflate ZIP: %v", err)
	}
	avs, ok := ms.(core.ArchiveVolumeSource)
	if !ok {
		_ = ms.Close()
		t.Fatalf("expected ArchiveVolumeSource for deflate ZIP, got %T", ms)
	}
	vols, format, member := avs.ArchiveVolumes()
	if format != "zip" || member != "Books/Dune.epub" || len(vols) != 1 {
		_ = ms.Close()
		t.Fatalf("ArchiveVolumes = (len=%d, fmt=%q, member=%q)", len(vols), format, member)
	}
	_ = ms.Close()
	if st := e.Stats(); st.ActiveSources != 0 {
		t.Fatalf("ActiveSources after close = %d, want 0", st.ActiveSources)
	}

	// 2. Real multi-volume compressed RAR5 fixture -> hands off all 3 volumes with balanced refs.
	var rarPosts []postFile
	for _, name := range []string{"multi.part1.rar", "multi.part2.rar", "multi.part3.rar"} {
		data, err := os.ReadFile(filepath.Join("..", "archive", "testdata", "altmount", name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		rarPosts = append(rarPosts, postFile{name: name, data: data})
	}
	rarNZB := annotateNonVideoNZB(t, post(t, f, 8*1024, rarPosts...), nzb.NonVideoHint{
		Member: "release.nzb",
	})
	ms2, err := e.Open(ctx, "rar-multi-compressed", rarNZB)
	if err != nil {
		t.Fatalf("Open multi-vol compressed RAR: %v", err)
	}
	avs2, ok := ms2.(core.ArchiveVolumeSource)
	if !ok {
		_ = ms2.Close()
		t.Fatalf("expected ArchiveVolumeSource for compressed RAR, got %T", ms2)
	}
	vols2, fmt2, mem2 := avs2.ArchiveVolumes()
	if fmt2 != "rar" || mem2 != "release.nzb" || len(vols2) != 3 {
		_ = ms2.Close()
		t.Fatalf("ArchiveVolumes(rar) = (len=%d, fmt=%q, member=%q)", len(vols2), fmt2, mem2)
	}
	_ = ms2.Close()
	if st := e.Stats(); st.ActiveSources != 0 {
		t.Fatalf("ActiveSources after multi-vol RAR close = %d, want 0", st.ActiveSources)
	}
}
