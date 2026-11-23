package releaseparse

import "testing"

func TestAnimeReleaseStyle(t *testing.T) {
	yes := []string{
		"[SubsPlease] One Piece - 001 (1080p) [A1B2C3D4].mkv",
		"[Group] One Piece - 001 [Dual Audio]",
		"One Piece - 001 (1080p WEB-DL)",
		"One Piece S01E01 (001) 1080p WEB-DL",
		"[Erai-raws] Show Name - S01E01 [1080p].mkv",
		"Show Name S01E01 1080p BluRay [DEADBEEF].mkv",
		"Show.Name.S01E01.1080p.BluRay.Dual.Audio.x265-GRP",
		"Show Name S01E01 [Dual Audio] 1080p",
		"Show Name S01E01 1080p BD FLAC",
		"Show Name S01 (Batch) 1080p HEVC",
		"Show Name S01 Batch 1080p x265",
		"Show Name S01E01 [1080p] [Judas]",
		"One.Piece.-.750.-.ENG.UNCUT.1080p.WEB.H.264.-NanDesuKa.(FUNi)",
	}
	for _, name := range yes {
		if !AnimeReleaseStyle(name, Parse(name)) {
			t.Errorf("expected AnimeReleaseStyle(%q) = true", name)
		}
	}

	no := []string{
		"One.Piece.S01E01.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX",
		"One.Piece.2023.S01E01.Romance.Dawn.1080p.WEB-DL.DDP5.1.H.264-NTb",
		"Show.S01E01.1080p.WEB-DL.x264-FLUX[TGx]",
		"Show.S01E01.1080p.HDTV.x264-LOL[rarbg]",
		"Show.S01E01.1080p.WEB.H264[rartv]",
		"[www.Torrenting.com] - Show.S01E01.1080p.WEB-DL-GRP",
		"Show.S01E01.SPANISH.DUAL.1080p.WEB-DL.DDP5.1.H.264-GRP",
		"Show.S01E01.1080p.BluRay.x264-ROVERS",
		"Show [20230831] S01E01 1080p WEB-DL-GRP",
	}
	for _, name := range no {
		if AnimeReleaseStyle(name, Parse(name)) {
			t.Errorf("expected AnimeReleaseStyle(%q) = false", name)
		}
	}
}

func TestParseAbsoluteEpisodeWithLanguageEditionTags(t *testing.T) {
	const name = "One.Piece.-.750.-.ENG.UNCUT.1080p.WEB.H.264.-NanDesuKa.(FUNi)"
	info := Parse(name)
	if info.Title != "One Piece" {
		t.Fatalf("Title = %q, want %q", info.Title, "One Piece")
	}
	if len(info.AbsoluteEpisodes) != 1 || info.AbsoluteEpisodes[0] != 750 || len(info.Seasons) != 0 || len(info.Episodes) != 0 {
		t.Fatalf("episode parse = seasons=%v episodes=%v abs=%v, want abs=[750]", info.Seasons, info.Episodes, info.AbsoluteEpisodes)
	}
	if info.Height != 1080 || info.VideoCodec != "h264" || !info.Uncensored || len(info.Languages) != 1 || info.Languages[0] != "english" {
		t.Fatalf("unexpected technical/language info: %+v", info)
	}
	if !AnimeReleaseStyle(name, info) {
		t.Fatalf("AnimeReleaseStyle(%q) = false, want true", name)
	}

	for _, nonEp := range []string{
		"One Piece - 01-12 1080p WEB-DL",
		"Fast - 2 Fast 2 Furious 2003 1080p BluRay",
	} {
		if got := Parse(nonEp); len(got.AbsoluteEpisodes) != 0 {
			t.Fatalf("Parse(%q).AbsoluteEpisodes = %v, want empty", nonEp, got.AbsoluteEpisodes)
		}
	}
}
