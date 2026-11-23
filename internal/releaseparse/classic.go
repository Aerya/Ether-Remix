package releaseparse

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

var (
	reClassicPartEpisode    = regexp.MustCompile(`(?i)\b(?:Part|Pt)\.?\s*(\d{1,2})\b`)
	reClassicLeadingEpisode = regexp.MustCompile(`^\s*(\d{2,3})(?:\s*[-._]\s*|\s+)`)
)

// ClassicPackEpisodeMatches reports whether a file in a season pack names the
// wanted episode the classic way, when no SxxExx is present: "Season 2/05 -
// Title.mkv", "205 - Title.avi", or a miniseries "Part 2 - Title.mkv". A
// season folder in the path must be the wanted season.
func ClassicPackEpisodeMatches(filePath string, wantSeason, wantEp int) bool {
	if wantSeason == 0 {
		return false
	}
	normPath := strings.ReplaceAll(filePath, "\\", "/")
	dirInfo := Parse(strings.ReplaceAll(path.Dir(normPath), "/", " "))
	if len(dirInfo.Seasons) > 0 {
		seasonOK := false
		for _, s := range dirInfo.Seasons {
			if s == wantSeason {
				seasonOK = true
			}
		}
		if !seasonOK {
			return false
		}
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
		if n == wantEp && (len(dirInfo.Seasons) > 0 || wantSeason == 1) {
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
	dirInfo := Parse(strings.ReplaceAll(path.Dir(normPath), "/", " "))
	if len(dirInfo.Seasons) > 0 {
		seasonOK := false
		for _, s := range dirInfo.Seasons {
			if s == wantSeason {
				seasonOK = true
			}
		}
		if !seasonOK {
			return 0, false
		}
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
		if len(dirInfo.Seasons) > 0 || wantSeason == 1 {
			return n, true
		}
	}
	return 0, false
}
