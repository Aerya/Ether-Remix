package debrid

import (
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestChooseFileExplicitPluginSelectorAllowsNonVideo(t *testing.T) {
	pack := []File{
		{ID: "f0", Path: "Collection/Book One (2024)/Book One.epub", Size: 1_250_000},
		{ID: "f1", Path: "Collection/Comic Issue 001.cbz", Size: 42_000_000},
		{ID: "f2", Path: "Collection/Comic Issue 002.cbr", Size: 38_000_000},
		{ID: "f3", Path: "Collection/Manual.pdf", Size: 8_500_000},
		{ID: "f4", Path: "Collection/Audio/01 - Opening.flac", Size: 29_000_000},
		{ID: "f5", Path: "Collection/Audiobook.m4b", Size: 185_000_000},
		{ID: "f6", Path: "Collection/Audio/02 - Commentary.mp3", Size: 12_000_000},
		{ID: "f7", Path: "Collection/Notes.txt", Size: 4_096},
		{ID: "f8", Path: "Collection/Payload.bin", Size: 65_536},
		{ID: "f9", Path: "Collection/Bonus/sample.mkv", Size: 50_000_000},
		{ID: "f10", Path: "Collection/Feature.1080p.mkv", Size: 900_000_000},
	}

	// Every non-video extension is selectable by explicit 0-based FileIndex.
	for i := 0; i <= 8; i++ {
		idx := i
		got, err := ChooseFile(pack, &core.ReleaseTarget{
			FileIndex:     &idx,
			AllowNonVideo: true,
		})
		if err != nil {
			t.Fatalf("FileIndex=%d (%s): unexpected error %v", idx, pack[idx].Path, err)
		}
		if got.ID != pack[idx].ID {
			t.Fatalf("FileIndex=%d: got ID %q, want %q", idx, got.ID, pack[idx].ID)
		}
	}

	// Non-video-only torrent (zero video files) succeeds when explicitly selected.
	nonVideoOnly := pack[:9]
	zero := 0
	got, err := ChooseFile(nonVideoOnly, &core.ReleaseTarget{
		FileIndex:     &zero,
		AllowNonVideo: true,
	})
	if err != nil || got.ID != "f0" {
		t.Fatalf("FileIndex=0 on non-video torrent: got %+v, err=%v", got, err)
	}

	// ExactFile matches full normalized path (with or without leading slash) and unique basename.
	for _, tc := range []struct {
		exact  string
		wantID string
	}{
		{exact: "Collection/Book One (2024)/Book One.epub", wantID: "f0"},
		{exact: "/Collection/Comic Issue 001.cbz", wantID: "f1"},
		{exact: "01 - Opening.flac", wantID: "f4"},
		{exact: "Audiobook.m4b", wantID: "f5"},
		{exact: "Notes.txt", wantID: "f7"},
	} {
		got, err := ChooseFile(nonVideoOnly, &core.ReleaseTarget{
			ExactFile:     tc.exact,
			AllowNonVideo: true,
		})
		if err != nil || got.ID != tc.wantID {
			t.Fatalf("ExactFile=%q: got %+v, err=%v, want %q", tc.exact, got, err, tc.wantID)
		}
	}

	// Matching FileIndex + ExactFile together succeeds.
	idx3 := 3
	got, err = ChooseFile(nonVideoOnly, &core.ReleaseTarget{
		FileIndex:     &idx3,
		ExactFile:     "Collection/Manual.pdf",
		AllowNonVideo: true,
	})
	if err != nil || got.ID != "f3" {
		t.Fatalf("matching FileIndex+ExactFile: got %+v, err=%v", got, err)
	}
}

func TestChooseFileExplicitPluginSelectorRejectsInvalidOrEmpty(t *testing.T) {
	files := []File{
		{ID: "empty", Path: "Pack/empty.epub", Size: 0},
		{ID: "dupEmpty", Path: "Disc1/track01.flac", Size: 0},
		{ID: "dupReal", Path: "Disc2/track01.flac", Size: 24_000_000},
		{ID: "valid", Path: "Pack/book.epub", Size: 512_000},
	}

	// Empty file rejected with ErrReleaseRejected by both FileIndex and ExactFile.
	zero := 0
	if _, err := ChooseFile(files, &core.ReleaseTarget{FileIndex: &zero, AllowNonVideo: true}); !errors.Is(err, core.ErrReleaseRejected) {
		t.Fatalf("empty FileIndex=0: got %v, want ErrReleaseRejected", err)
	}
	if _, err := ChooseFile(files, &core.ReleaseTarget{ExactFile: "Pack/empty.epub", AllowNonVideo: true}); !errors.Is(err, core.ErrReleaseRejected) {
		t.Fatalf("empty ExactFile: got %v, want ErrReleaseRejected", err)
	}

	// Negative and out-of-range FileIndex rejected with ErrWrongContent.
	for _, bad := range []int{-1, len(files), 99} {
		idx := bad
		if _, err := ChooseFile(files, &core.ReleaseTarget{FileIndex: &idx, AllowNonVideo: true}); !errors.Is(err, core.ErrWrongContent) {
			t.Fatalf("FileIndex=%d: got %v, want ErrWrongContent", bad, err)
		}
	}

	// Mismatched FileIndex + ExactFile rejected with ErrWrongContent.
	idx3 := 3
	if _, err := ChooseFile(files, &core.ReleaseTarget{
		FileIndex:     &idx3,
		ExactFile:     "Disc2/track01.flac",
		AllowNonVideo: true,
	}); !errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("mismatched FileIndex+ExactFile: got %v, want ErrWrongContent", err)
	}

	// Missing ExactFile rejected with ErrWrongContent.
	if _, err := ChooseFile(files, &core.ReleaseTarget{ExactFile: "Pack/missing.pdf", AllowNonVideo: true}); !errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("missing ExactFile: got %v, want ErrWrongContent", err)
	}

	// Ambiguous basename rejected even when one duplicate is 0 bytes; full path disambiguates.
	if _, err := ChooseFile(files, &core.ReleaseTarget{ExactFile: "track01.flac", AllowNonVideo: true}); !errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("ambiguous basename ExactFile: got %v, want ErrWrongContent", err)
	}
	got, err := ChooseFile(files, &core.ReleaseTarget{ExactFile: "Disc2/track01.flac", AllowNonVideo: true})
	if err != nil || got.ID != "dupReal" {
		t.Fatalf("disambiguated full path ExactFile: got %+v, err=%v", got, err)
	}
}

func TestChooseFileAutomaticAndMovieFileIdxKeepVideoRules(t *testing.T) {
	nonVideoOnly := []File{
		{ID: "book", Path: "Archive/Novel.epub", Size: 2_000_000},
		{ID: "audio", Path: "Archive/Audiobook.m4b", Size: 95_000_000},
	}

	// Automatic selection (nil target or AllowNonVideo without explicit selector) still returns errNoVideo.
	for _, tgt := range []*core.ReleaseTarget{
		nil,
		{},
		{AllowNonVideo: true},
	} {
		_, err := ChooseFile(nonVideoOnly, tgt)
		if !errors.Is(err, errNoVideo) || !errors.Is(err, core.ErrReleaseRejected) {
			t.Fatalf("automatic selection (%+v) on non-video torrent: got %v, want errNoVideo", tgt, err)
		}
	}

	// Automatic selection with AllowNonVideo=true (no explicit selector) still filters sample extras and picks video.
	mixed := []File{
		{ID: "nfo", Path: "Movie.2024/movie.nfo", Size: 2_048},
		{ID: "sample", Path: "Movie.2024/Sample/sample.mkv", Size: 80_000_000},
		{ID: "feature", Path: "Movie.2024/Movie.2024.1080p.mkv", Size: 4_500_000_000},
	}
	got, err := ChooseFile(mixed, &core.ReleaseTarget{AllowNonVideo: true})
	if err != nil || got.ID != "feature" {
		t.Fatalf("automatic mixed selection: got %+v, err=%v, want feature", got, err)
	}

	// Movie/episode fileIdx (AllowNonVideo == false) still rejects non-video and sample files with errNoVideo (#507).
	idx0 := 0
	if _, err := ChooseFile(mixed, &core.ReleaseTarget{FileIndex: &idx0}); !errors.Is(err, errNoVideo) {
		t.Fatalf("AllowNonVideo=false on .nfo: got %v, want errNoVideo", err)
	}
	idx1 := 1
	if _, err := ChooseFile(mixed, &core.ReleaseTarget{FileIndex: &idx1}); !errors.Is(err, errNoVideo) {
		t.Fatalf("AllowNonVideo=false on sample.mkv: got %v, want errNoVideo", err)
	}
	idx2 := 2
	got, err = ChooseFile(mixed, &core.ReleaseTarget{FileIndex: &idx2})
	if err != nil || got.ID != "feature" {
		t.Fatalf("AllowNonVideo=false on feature.mkv: got %+v, err=%v", got, err)
	}
}

func TestPayloadKeyDistinguishesAllowNonVideo(t *testing.T) {
	idx := 0
	base := core.Release{
		ID:       "rel-1",
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		Target:   &core.ReleaseTarget{FileIndex: &idx},
	}
	withNonVideo := core.Release{
		ID:       "rel-1",
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		Target:   &core.ReleaseTarget{FileIndex: &idx, AllowNonVideo: true},
	}

	if k1, k2 := payloadKey(base), payloadKey(withNonVideo); k1 == k2 {
		t.Fatalf("payloadKey did not distinguish AllowNonVideo: %q == %q", k1, k2)
	}
}
