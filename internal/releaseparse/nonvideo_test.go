package releaseparse

import "testing"

func TestIsVideoRelease(t *testing.T) {
	videos := []string{
		"Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR10.HEVC.Atmos-GRP",
		"Severance.S01E01.1080p.WEB-DL.DDP5.1.H.264-NTb",
		"Show.Name.S02E05.HDTV.x265-GRP",
		"Classic.Film.1962.BDRip.AV1-GRP",
	}
	for _, name := range videos {
		if !IsVideoRelease(name) {
			t.Errorf("IsVideoRelease(%q) = false, want true", name)
		}
	}

	nonVideos := []string{
		"Isaac Asimov - Foundation (2020) [EPUB]",
		"Artist - Album Name (2024) [FLAC 24bit-96kHz]",
		"Dune Unabridged Audiobook [M4B 64kbps]",
		"Saga 067 (2024) (Digital) (Zone-Empire).cbz",
		"Plain Unstructured Document Release",
	}
	for _, name := range nonVideos {
		if IsVideoRelease(name) {
			t.Errorf("IsVideoRelease(%q) = true, want false", name)
		}
	}
}
