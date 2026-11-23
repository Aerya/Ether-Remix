package nzb

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type parsedTrack struct {
	idx    int
	disc   int
	number int
	tokens []string
}

var (
	reDirDisc     = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:cd|dis[ck]|vol(?:ume)?|part)\s*[-_.]?\s*0*([1-9][0-9]?)(?:$|[^a-z0-9])`)
	reFileDiscNum = regexp.MustCompile(`(?i)^(?:(?:cd|dis[ck]|d)\s*0*([1-9][0-9]?)\s*[-_.t ]+|([0-9]{1,2})\s*[-_.]\s*)(?:t(?:rack)?\s*)?([0-9]{1,3})(?:$|[^0-9])`)
	reFileLeadNum = regexp.MustCompile(`(?i)^(?:(?:track|tr|t)\s*[-_.]?\s*)?0*([1-9][0-9]{0,2})(?:$|[^0-9])`)
	reFileMidNum  = regexp.MustCompile(`(?i)(?:\s+-\s+|\s*[-_]\s*)0*([0-9]{2})(?:\s+-\s+|\s*[-_.]\s+)`)
)

// MatchTrackIndex finds a unique audio file in names matching want:
//  1. Disc + track number first (disambiguated by normalised title tokens when
//     multiple candidates share a number, failing with ambiguous=true if still
//     not unique).
//  2. Normalised title tokens when no track number was given or no candidate
//     matched by number (contiguous token sequence first, then all tokens
//     present), failing with ambiguous=true when multiple candidates tie.
func MatchTrackIndex(names []string, want TrackHint, mimeFilter string) (idx int, found bool, ambiguous bool) {
	if !want.NonEmpty() {
		return -1, false, false
	}
	wantMIME := NormalizeMIME(mimeFilter)
	if wantMIME == "" {
		wantMIME = "audio/*"
	}
	var cands []parsedTrack
	for i, raw := range names {
		if !isTrackCandidateFile(raw, wantMIME) {
			continue
		}
		cands = append(cands, parseTrackCandidate(i, raw))
	}
	// If a specific audio MIME (e.g. audio/flac) found no files (e.g. release
	// holds mp3/m4a instead), retry across all audio files.
	if len(cands) == 0 && wantMIME != "audio/*" {
		for i, raw := range names {
			if !isTrackCandidateFile(raw, "audio/*") {
				continue
			}
			cands = append(cands, parseTrackCandidate(i, raw))
		}
	}
	if len(cands) == 0 {
		return -1, false, false
	}

	hasAnyDisc := false
	for _, c := range cands {
		if c.disc > 0 {
			hasAnyDisc = true
			break
		}
	}
	wantTokens := normaliseTrackTokens(want.Title)

	// 1. Match by disc + track number first.
	if want.Number > 0 {
		var byNum []parsedTrack
		for _, c := range cands {
			if c.number == want.Number {
				if trackDiscMatches(c.disc, want.Disc, hasAnyDisc) {
					byNum = append(byNum, c)
				}
				continue
			}
			// Support 3-digit disc+track numbering (e.g. 105 / 205) when a
			// specific disc was requested and the file had no separate disc tag.
			if want.Disc > 0 && want.Number < 100 && c.disc == 0 && c.number == want.Disc*100+want.Number {
				byNum = append(byNum, c)
			}
		}
		if len(byNum) == 1 {
			return byNum[0].idx, true, false
		}
		if len(byNum) > 1 {
			if len(wantTokens) > 0 {
				if midx, mok, mambig := matchTrackByTokens(byNum, wantTokens); mok || mambig {
					return midx, mok, mambig
				}
			}
			return -1, false, true
		}
	}

	// 2. Fallback to normalised title tokens.
	if len(wantTokens) > 0 {
		var eligible []parsedTrack
		for _, c := range cands {
			if want.Disc > 0 && c.disc > 0 && c.disc != want.Disc {
				continue
			}
			eligible = append(eligible, c)
		}
		return matchTrackByTokens(eligible, wantTokens)
	}
	return -1, false, false
}

func trackDiscMatches(candDisc, wantDisc int, releaseHasDiscs bool) bool {
	if wantDisc <= 0 {
		return true
	}
	if candDisc == wantDisc {
		return true
	}
	// Single-disc releases rarely tag disc 1 in filenames; allow untagged
	// candidates when want.Disc == 1 and nothing in the release declares discs.
	return wantDisc == 1 && candDisc == 0 && !releaseHasDiscs
}

func isTrackCandidateFile(raw, wantMIME string) bool {
	norm := CleanArchivePath(raw)
	if norm == "" {
		return false
	}
	ext := strings.ToLower(path.Ext(norm))
	switch ext {
	case "", ".nfo", ".sfv", ".m3u", ".m3u8", ".cue", ".log", ".txt", ".url",
		".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp",
		".par2", ".nzb", ".rar", ".zip", ".7z", ".epub", ".pdf", ".cbz", ".cbr":
		return false
	}
	if IsNonVideoMIME(wantMIME) {
		return NonVideoMatchRank(norm, wantMIME) > 0
	}
	return NonVideoMatchRank(norm, "audio/*") > 0
}

func parseTrackCandidate(idx int, raw string) parsedTrack {
	norm := CleanArchivePath(raw)
	dir := path.Dir(norm)
	base := path.Base(norm)
	stem := strings.TrimSuffix(base, path.Ext(base))

	var disc, num int
	if dir != "" && dir != "." {
		for _, seg := range strings.Split(dir, "/") {
			if m := reDirDisc.FindStringSubmatch(seg); len(m) == 2 {
				if d, err := strconv.Atoi(m[1]); err == nil && d > 0 && d <= 50 {
					disc = d
				}
			}
		}
	}

	if m := reFileDiscNum.FindStringSubmatch(stem); len(m) == 4 {
		dRaw := m[1]
		if dRaw == "" {
			dRaw = m[2]
		}
		d, derr := strconv.Atoi(dRaw)
		n, nerr := strconv.Atoi(m[3])
		// Distinguish "1-05-song" (disc 1, track 5) from "05-1999-song" or
		// "05-song" by checking that leading disc number is 1..20, has at most
		// 2 digits, and track is 1..199 (and not a 4-digit year).
		if derr == nil && nerr == nil && d >= 1 && d <= 20 && n >= 1 && n <= 199 &&
			(m[1] != "" || (len(strings.TrimLeft(dRaw, "0")) <= 2 && len(m[3]) >= 2 && len(m[3]) <= 3 && (disc == 0 || disc == d))) {
			// Guard against "01-02-..." where "01" is track 1 and "02" is part
			// of a date/title when dRaw had a leading zero and no disc prefix:
			// only treat bare "DD-NN" as disc+track if dRaw is 1 digit ("1-05")
			// OR starts with explicit CD/Disc/D prefix.
			if m[1] != "" || len(dRaw) == 1 {
				disc = d
				num = n
			}
		}
	}
	if num == 0 {
		if m := reFileLeadNum.FindStringSubmatch(stem); len(m) == 2 {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 999 {
				num = n
			}
		}
	}
	if num == 0 {
		if m := reFileMidNum.FindStringSubmatch(stem); len(m) == 2 {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 99 {
				num = n
			}
		}
	}

	return parsedTrack{
		idx:    idx,
		disc:   disc,
		number: num,
		tokens: normaliseTrackTokens(stem),
	}
}

func normaliseTrackTokens(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur.WriteRune(r)
		case r == '\'' || r == '\u2019':
			// Collapse apostrophes so "Don't" matches "dont" and "don_t" both
			// gracefully: skip apostrophe inside words.
		default:
			flush()
		}
	}
	flush()
	return out
}

func matchTrackByTokens(cands []parsedTrack, want []string) (idx int, found bool, ambiguous bool) {
	if len(want) == 0 || len(cands) == 0 {
		return -1, false, false
	}
	var contiguous []parsedTrack
	var allPresent []parsedTrack
	for _, c := range cands {
		if hasContiguousTokens(c.tokens, want) {
			contiguous = append(contiguous, c)
		} else if hasAllTokens(c.tokens, want) {
			allPresent = append(allPresent, c)
		}
	}
	if len(contiguous) == 1 {
		return contiguous[0].idx, true, false
	}
	if len(contiguous) > 1 {
		return -1, false, true
	}
	if len(allPresent) == 1 {
		return allPresent[0].idx, true, false
	}
	if len(allPresent) > 1 {
		return -1, false, true
	}
	return -1, false, false
}

func hasContiguousTokens(haystack, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		ok := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func hasAllTokens(haystack, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	counts := make(map[string]int, len(haystack))
	for _, tok := range haystack {
		counts[tok]++
	}
	for _, tok := range needle {
		if counts[tok] <= 0 {
			return false
		}
		counts[tok]--
	}
	return true
}
