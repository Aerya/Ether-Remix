package releaseparse

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	reClassicPartEpisode    = regexp.MustCompile(`(?i)\b(?:Part|Pt)\.?\s*(\d{1,2})\b`)
	reClassicLeadingEpisode = regexp.MustCompile(`^\s*(\d{2,3})(?:\s*[-._]\s*|\s+)`)
)

// FolderSeasons returns the seasons the folders around filePath give the
// file. The folder nearest the file that names a season on its own decides,
// so in a range pack "Show.S01-S05/S02/Show.Ep05.mkv" or "Show.Season.1-5/
// Season 2/05 - Title.mkv" the file is season 2, not seasons 1 to 5. A bare
// NxM folder ("4x3", "16x9") is an aspect ratio or disc label more often
// than a season and does not decide alone. When no single folder names a
// season, the folders read as one name are the fallback.
func FolderSeasons(filePath string) []int {
	dir := path.Dir(strings.ReplaceAll(filePath, "\\", "/"))
	if dir == "." || dir == "/" || dir == "" {
		return nil
	}
	rest := dir
	for rest != "." && rest != "/" && rest != "" {
		folder := path.Base(rest)
		if seasons := Parse(reXEpisode.ReplaceAllString(folder, " ")).Seasons; len(seasons) > 0 {
			return seasons
		}
		rest = path.Dir(rest)
	}
	return Parse(strings.ReplaceAll(dir, "/", " ")).Seasons
}

// ClassicPackEpisodeMatches reports whether a file in a season pack names the
// wanted episode the classic way, when no SxxExx is present: "Season 2/05 -
// Title.mkv", "205 - Title.avi", or a miniseries "Part 2 - Title.mkv". A
// season folder in the path must be the wanted season.
func ClassicPackEpisodeMatches(filePath string, wantSeason, wantEp int) bool {
	if wantSeason == 0 {
		return false
	}
	normPath := strings.ReplaceAll(filePath, "\\", "/")
	folderSeasons := FolderSeasons(normPath)
	if len(folderSeasons) > 0 && !slices.Contains(folderSeasons, wantSeason) {
		return false
	}
	base := path.Base(normPath)
	if wantSeason == 1 {
		if m := reClassicPartEpisode.FindStringSubmatch(base); m != nil {
			if ep, err := strconv.Atoi(m[1]); err == nil && ep == wantEp {
				return true
			}
		}
	}
	if m := reClassicLeadingEpisode.FindStringSubmatch(base); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return false
		}
		// Avoid mistaking 4-digit years or resolutions ("1080p") for episode numbers.
		if n == wantEp && (len(folderSeasons) > 0 || wantSeason == 1) {
			return true
		}
		if wantSeason > 0 && wantEp > 0 && wantEp < 100 && n == wantSeason*100+wantEp {
			return true
		}
	}
	return false
}

// ClassicPackEpisode extracts a classic pack episode key for wantSeason when
// no standard SxxExx marker is present ("Season 2/05 - Title.mkv", "205 -
// Title.avi", or miniseries "Part 2 - Title.mkv").
func ClassicPackEpisode(filePath string, wantSeason int) (int, bool) {
	if wantSeason <= 0 {
		return 0, false
	}
	normPath := strings.ReplaceAll(filePath, "\\", "/")
	folderSeasons := FolderSeasons(normPath)
	if len(folderSeasons) > 0 && !slices.Contains(folderSeasons, wantSeason) {
		return 0, false
	}
	base := path.Base(normPath)
	if wantSeason == 1 {
		if m := reClassicPartEpisode.FindStringSubmatch(base); m != nil {
			if ep, err := strconv.Atoi(m[1]); err == nil && ep > 0 {
				return ep, true
			}
		}
	}
	if m := reClassicLeadingEpisode.FindStringSubmatch(base); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return 0, false
		}
		switch n {
		case 360, 480, 576, 720:
			return 0, false
		}
		if n > wantSeason*100 && n < (wantSeason+1)*100 {
			return n - wantSeason*100, true
		}
		if len(folderSeasons) > 0 || wantSeason == 1 {
			return n, true
		}
	}
	return 0, false
}
