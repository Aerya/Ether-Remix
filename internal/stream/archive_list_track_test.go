package stream

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	at "github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

func TestListArchiveMembersAndTrackHintSceneRARZIPAnd7z(t *testing.T) {
	t.Run("scene RAR5 members cached + track hint + unchanged fileHint", func(t *testing.T) {
		f := newFakeFetcher()
		e := newTestEngine(t, f, nil, Options{})

		track1 := flacBytes(501, 96_000)
		track5 := flacBytes(505, 180_000)
		nfo := []byte("Metallica - 72 Seasons (2023) - MOONBLOOD\n")

		vols, err := at.BuildRAR([]at.RARFile{
			{Name: "00-metallica-72_seasons-2023-moonblood.nfo", Data: nfo},
			{Name: "01-metallica-72_seasons-moonblood.flac", Data: track1},
			{Name: "05-metallica-you_must_burn-moonblood.flac", Data: track5},
		}, at.RAROptions{Version: 5, VolumeSize: 160_000})
		if err != nil {
			t.Fatalf("BuildRAR: %v", err)
		}
		var files []postFile
		for i, v := range vols {
			files = append(files, postFile{name: fmt.Sprintf("metallica.part%02d.rar", i+1), data: v})
		}
		rawNZB := post(t, f, 64*1024, files...)

		// 1. ListArchiveMembers reads the archive directory and keys it by first volume index (0).
		byFile, err := e.ListArchiveMembers(context.Background(), rawNZB)
		if err != nil {
			t.Fatalf("ListArchiveMembers(RAR5): %v", err)
		}
		members := byFile[0]
		if len(members) != 3 {
			t.Fatalf("RAR5 members = %+v, want 3 entries", members)
		}
		var foundTrack5 bool
		for _, m := range members {
			if m.Path == "05-metallica-you_must_burn-moonblood.flac" && m.Size == int64(len(track5)) {
				foundTrack5 = true
			}
		}
		if !foundTrack5 {
			t.Fatalf("expected scene track 5 in members: %+v", members)
		}

		// 2. Second call hits cache with ZERO additional NNTP article fetches.
		fetchesBefore := f.totalCalls()
		byFile2, err := e.ListArchiveMembers(context.Background(), rawNZB)
		if err != nil {
			t.Fatalf("ListArchiveMembers cached: %v", err)
		}
		if len(byFile2[0]) != 3 {
			t.Fatalf("cached members = %+v", byFile2[0])
		}
		if got := f.totalCalls(); got != fetchesBefore {
			t.Fatalf("cached ListArchiveMembers performed %d extra NNTP fetches, want 0", got-fetchesBefore)
		}

		// 3. A track-style fileHint that names no member exactly ("05 You Must
		// Burn!") falls back to track matching (#753); an unmatched real path
		// still fails with archive holds no member.
		fuzzyHintNZB := annotateNonVideoNZB(t, rawNZB, nzb.NonVideoHint{
			Member: "05 You Must Burn!",
			MIME:   "audio/flac",
		})
		fuzzySrc := openSource(t, e, "rar-fuzzy-hint", fuzzyHintNZB)
		if fuzzySrc.Name() != "05-metallica-you_must_burn-moonblood.flac" {
			t.Fatalf("track-style fileHint opened %q", fuzzySrc.Name())
		}
		badHintNZB := annotateNonVideoNZB(t, rawNZB, nzb.NonVideoHint{
			Member: "Other Album/05-someone-else.flac",
			MIME:   "audio/flac",
		})
		if _, err := e.Open(context.Background(), "rar-bad-hint", badHintNZB); !errors.Is(err, core.ErrNoUsableFile) || !strings.Contains(err.Error(), "archive holds no member") || strings.Contains(err.Error(), "video") {
			t.Fatalf("unmatched path fileHint err = %v, want media-neutral 'archive holds no member'", err)
		}

		// 4. Exact member name discovered from ListArchiveMembers succeeds.
		exactHintNZB := annotateNonVideoNZB(t, rawNZB, nzb.NonVideoHint{
			Member: "05-metallica-you_must_burn-moonblood.flac",
			MIME:   "audio/flac",
		})
		exactSrc := openSource(t, e, "rar-exact-member", exactHintNZB)
		if !bytes.Equal(readAll(t, exactSrc, 32*1024), track5) {
			t.Fatal("exact member bytes mismatch")
		}

		// 5. Structured release.track hint matches scene member without needing exact inner name.
		trackHintNZB := annotateNonVideoNZB(t, rawNZB, nzb.NonVideoHint{
			MIME:  "audio/flac",
			Track: &nzb.TrackHint{Number: 5, Title: "You Must Burn!"},
		})
		trackSrc := openSource(t, e, "rar-track-hint", trackHintNZB)
		if trackSrc.Name() != "05-metallica-you_must_burn-moonblood.flac" {
			t.Fatalf("trackSrc.Name() = %q", trackSrc.Name())
		}
		if !bytes.Equal(readAll(t, trackSrc, 32*1024), track5) {
			t.Fatal("track-hint member bytes mismatch")
		}
	})

	t.Run("ZIP archive members + ambiguous track hint fails", func(t *testing.T) {
		f := newFakeFetcher()
		e := newTestEngine(t, f, nil, Options{})

		cd1 := flacBytes(601, 48_000)
		cd2 := flacBytes(602, 52_000)
		zipData := buildZipArchive(t, zip.Store, []struct {
			name string
			data []byte
		}{
			{name: "CD1/05 - Alpha.flac", data: cd1},
			{name: "CD2/05 - Beta.flac", data: cd2},
		})
		rawNZB := post(t, f, 64*1024, postFile{name: "album.zip", data: zipData})

		byFile, err := e.ListArchiveMembers(context.Background(), rawNZB)
		if err != nil {
			t.Fatalf("ListArchiveMembers(ZIP): %v", err)
		}
		if len(byFile[0]) != 2 {
			t.Fatalf("ZIP members = %+v, want 2", byFile[0])
		}

		// Ambiguous track {number: 5} without disc or title MUST fail.
		ambigNZB := annotateNonVideoNZB(t, rawNZB, nzb.NonVideoHint{
			MIME:  "audio/flac",
			Track: &nzb.TrackHint{Number: 5},
		})
		if _, err := e.Open(context.Background(), "zip-ambig", ambigNZB); !errors.Is(err, core.ErrNoUsableFile) {
			t.Fatalf("ambiguous track err = %v, want ErrNoUsableFile", err)
		}

		// Disc=2 disambiguates cleanly.
		disc2NZB := annotateNonVideoNZB(t, rawNZB, nzb.NonVideoHint{
			MIME:  "audio/flac",
			Track: &nzb.TrackHint{Disc: 2, Number: 5},
		})
		src := openSource(t, e, "zip-disc2", disc2NZB)
		if !bytes.Equal(readAll(t, src, 32*1024), cd2) {
			t.Fatal("disc 2 bytes mismatch")
		}
	})

	t.Run("7z archive members", func(t *testing.T) {
		f := newFakeFetcher()
		e := newTestEngine(t, f, nil, Options{})

		t1 := flacBytes(701, 40_000)
		t2 := flacBytes(702, 44_000)
		szData := at.Build7zCopy([]at.SevenZipFile{
			{Name: "01-opening.flac", Data: t1},
			{Name: "02-finale.flac", Data: t2},
		}, false)
		rawNZB := post(t, f, 64*1024, postFile{name: "album.7z", data: szData})

		byFile, err := e.ListArchiveMembers(context.Background(), rawNZB)
		if err != nil {
			t.Fatalf("ListArchiveMembers(7z): %v", err)
		}
		if len(byFile[0]) != 2 || byFile[0][1].Path != "02-finale.flac" || byFile[0][1].Size != int64(len(t2)) {
			t.Fatalf("7z members = %+v", byFile[0])
		}
	})
}
