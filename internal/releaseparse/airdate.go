package releaseparse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

var (
	// Full air date with separators: 2024.11.15, 2024-11-15, 2024/11/15, or 2024 11 15.
	reAirDateFull = regexp.MustCompile(`(?:^|[\s._\-\(\[])((?:19|20)\d{2})([.\-/ ])(0[1-9]|1[0-2])([.\-/ ])(0[1-9]|[12]\d|3[01])(?:$|[\s._\-\)\]])`)
	// Compact 8-digit air date: 20241115.
	reAirDateCompact8 = regexp.MustCompile(`(?:^|[\s._\-\(\[])((?:19|20)\d{2})(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])(?:$|[\s._\-\)\]])`)
	// Compact 6-digit Korean/Asian broadcast date: 241115 (YYMMDD).
	reAirDateCompact6 = regexp.MustCompile(`(?:^|[\s._\-\(\[])(\d{2})(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])(?:$|[\s._\-\)\]])`)
)

type airDateMatch struct {
	date        string // canonical YYYY-MM-DD
	stemStart   int
	stemEnd     int
	spacedStart int
	spacedEnd   int
}

func validCalendarDate(year, month, day int) bool {
	if year < 1950 || year > 2049 || month < 1 || month > 12 || day < 1 || day > 31 {
		return false
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return t.Year() == year && int(t.Month()) == month && t.Day() == day
}

func airDateSpanInSpaced(stem, spaced string, stemStart, stemEnd int) (int, int, bool) {
	dateSpaced := spacify(stem[stemStart:stemEnd])
	if dateSpaced == "" {
		return -1, -1, false
	}
	prefix := spacify(stem[:stemStart])
	start := 0
	if prefix != "" {
		start = len(prefix) + 1
	}
	end := start + len(dateSpaced)
	if start < 0 || end > len(spaced) || spaced[start:end] != dateSpaced {
		idx := strings.Index(spaced, dateSpaced)
		if idx < 0 {
			return -1, -1, false
		}
		start, end = idx, idx+len(dateSpaced)
	}
	return start, end, true
}

// parseAirDate finds an episode broadcast date (YYYY.MM.DD, YYYY-MM-DD,
// YYYYMMDD, or YYMMDD) after the title portion of stem. Returns the earliest
// valid calendar date and its byte spans in stem and spaced.
func parseAirDate(stem, spaced string) (airDateMatch, bool) {
	var best airDateMatch
	found := false
	consider := func(date string, stemStart, stemEnd int) {
		if stemStart <= 0 {
			return
		}
		// Require a non-empty title before the air date.
		if spacify(stem[:stemStart]) == "" {
			return
		}
		spStart, spEnd, ok := airDateSpanInSpaced(stem, spaced, stemStart, stemEnd)
		if !ok || spStart <= 0 {
			return
		}
		if !found || stemStart < best.stemStart || (stemStart == best.stemStart && stemEnd > best.stemEnd) {
			best = airDateMatch{
				date:        date,
				stemStart:   stemStart,
				stemEnd:     stemEnd,
				spacedStart: spStart,
				spacedEnd:   spEnd,
			}
			found = true
		}
	}

	// Scan full YYYY.MM.DD / YYYY-MM-DD / YYYY/MM/DD / YYYY MM DD dates.
	for off := 0; off < len(stem); {
		loc := reAirDateFull.FindStringSubmatchIndex(stem[off:])
		if loc == nil {
			break
		}
		s1, e1 := off+loc[2], off+loc[3]
		sep1 := stem[off+loc[4] : off+loc[5]]
		mStr := stem[off+loc[6] : off+loc[7]]
		sep2 := stem[off+loc[8] : off+loc[9]]
		dStart, dEnd := off+loc[10], off+loc[11]
		if sep1 == sep2 {
			y, _ := strconv.Atoi(stem[s1:e1])
			m, _ := strconv.Atoi(mStr)
			d, _ := strconv.Atoi(stem[dStart:dEnd])
			if validCalendarDate(y, m, d) {
				consider(fmt.Sprintf("%04d-%02d-%02d", y, m, d), s1, dEnd)
			}
		}
		off = s1 + 1
	}

	// Scan compact YYYYMMDD dates.
	for off := 0; off < len(stem); {
		loc := reAirDateCompact8.FindStringSubmatchIndex(stem[off:])
		if loc == nil {
			break
		}
		s1, e1 := off+loc[2], off+loc[3]
		mStr := stem[off+loc[4] : off+loc[5]]
		dStart, dEnd := off+loc[6], off+loc[7]
		y, _ := strconv.Atoi(stem[s1:e1])
		m, _ := strconv.Atoi(mStr)
		d, _ := strconv.Atoi(stem[dStart:dEnd])
		if validCalendarDate(y, m, d) {
			consider(fmt.Sprintf("%04d-%02d-%02d", y, m, d), s1, dEnd)
		}
		off = s1 + 1
	}

	// Scan compact YYMMDD dates (Korean/Asian variety releases: 241115).
	for off := 0; off < len(stem); {
		loc := reAirDateCompact6.FindStringSubmatchIndex(stem[off:])
		if loc == nil {
			break
		}
		s1, e1 := off+loc[2], off+loc[3]
		mStr := stem[off+loc[4] : off+loc[5]]
		dStart, dEnd := off+loc[6], off+loc[7]
		yy, _ := strconv.Atoi(stem[s1:e1])
		y := 2000 + yy
		if yy >= 50 {
			y = 1900 + yy
		}
		m, _ := strconv.Atoi(mStr)
		d, _ := strconv.Atoi(stem[dStart:dEnd])
		if validCalendarDate(y, m, d) {
			consider(fmt.Sprintf("%04d-%02d-%02d", y, m, d), s1, dEnd)
		}
		off = s1 + 1
	}

	return best, found
}

// EpisodeMatchesAirDate reports whether parsed carries a broadcast air date
// matching the episode's catalogue air date (YYYY-MM-DD).
func EpisodeMatchesAirDate(parsed core.ReleaseInfo, released time.Time) bool {
	if parsed.AirDate == "" || released.IsZero() {
		return false
	}
	return parsed.AirDate == released.UTC().Format("2006-01-02")
}

// EpisodeMatchesAirDateString reports whether parsed carries a broadcast air
// date matching a canonical "YYYY-MM-DD" string (used when only ReleaseTarget
// metadata is available, such as pack file selection).
func EpisodeMatchesAirDateString(parsed core.ReleaseInfo, airDate string) bool {
	return parsed.AirDate != "" && airDate != "" && parsed.AirDate == airDate
}
