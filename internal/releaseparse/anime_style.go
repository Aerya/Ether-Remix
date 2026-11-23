package releaseparse

import (
	"regexp"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

var (
	reDualAudioPhrase = regexp.MustCompile(`(?i)\bDUAL[\s._-]*AUDIO\b`)
	reBracketedDual   = regexp.MustCompile(`(?i)[\[(][^\])\n]*\bDUAL\b[^\])\n]*[\])]`)
	reBracketedBatch  = regexp.MustCompile(`(?i)[\[(][^\])\n]*\bBATCH\b[^\])\n]*[\])]`)
	reDomainTag       = regexp.MustCompile(`(?i)\.(?:com|net|org|io|to|re|se|me|tv|cc|mx|lt|am|ag|sx|ac|ru|ph|ch|bz|in|ws|vc)$`)
)

// AnimeReleaseStyle reports whether a release name carries anime-specific
// packaging conventions that distinguish an animated series from a
// non-animated namesake:
//   - absolute episode numbering ("Show - 001", "Show S01E01 (001)");
//   - a bracketed fansub or anime release group ("[SubsPlease] Show...", "Show [Ember]");
//   - a bracketed 8-hex CRC32 checksum ("[A1B2C3D4]");
//   - anime release metadata tags ("[Dual Audio]", "BD"/"BDMUX", or "Batch").
func AnimeReleaseStyle(name string, info core.ReleaseInfo) bool {
	raw := strings.TrimSpace(name)
	if raw == "" {
		return false
	}
	if info.Title == "" && len(info.Episodes) == 0 && len(info.Seasons) == 0 && len(info.AbsoluteEpisodes) == 0 {
		info = Parse(raw)
	}
	if len(info.AbsoluteEpisodes) > 0 {
		return true
	}

	cleaned := strings.TrimSpace(reLeadingWWWBracket.ReplaceAllString(raw, ""))
	if cleaned == "" {
		return false
	}
	cleaned = reTemporadaSE.ReplaceAllString(cleaned, "${1}S${2}E${3}")
	stem, _, _ := parseGroup(cleaned)

	if hasAnimeBracketGroupOrCRC(cleaned, stem) {
		return true
	}

	normStem, _ := stripLeadingEpisode(stem)
	normStem = normalizeSpanishCapRange(normStem)
	spaced := spacify(normStem)
	upper := " " + strings.ToUpper(spaced) + " "

	var probe core.ReleaseInfo
	markerAt := parseEpisodesWithRaw(normStem, spaced, upper, &probe)
	_, yearAt := parseYearAtWithStem(normStem, spaced)
	metaAt := firstMetadataAt(normStem, spaced)
	floor := yearAt
	if markerAt >= 0 && (floor < 0 || markerAt < floor) {
		floor = markerAt
	}
	if metaAt > 0 && (floor < 0 || metaAt-1 < floor) {
		floor = metaAt - 1
	}

	if reDualAudioPhrase.MatchString(cleaned) || reBracketedDual.MatchString(cleaned) {
		return true
	}
	if reBracketedBatch.MatchString(cleaned) {
		return true
	}
	if floor >= 0 && floor < len(spaced) {
		metadata := spaced[floor:]
		if tokenIndex(metadata, "BD") >= 0 || tokenIndex(metadata, "BDMUX") >= 0 || tokenIndex(metadata, "BATCH") >= 0 {
			return true
		}
	}
	return false
}

func hasAnimeBracketGroupOrCRC(cleaned, stem string) bool {
	if !strings.Contains(cleaned, "[") {
		return false
	}
	matches := reBracket.FindAllStringSubmatchIndex(cleaned, -1)
	if len(matches) == 0 {
		return false
	}
	noBrackets := strings.TrimSpace(reBracket.ReplaceAllString(stem, " "))
	hasSceneTail := false
	if m := reGroupTail.FindStringSubmatch(noBrackets); m != nil && !isKnownTail(m[1]) {
		hasSceneTail = true
	}
	for i, m := range matches {
		inner := strings.TrimSpace(cleaned[m[2]:m[3]])
		if inner == "" {
			continue
		}
		if isCRC32Bracket(inner) {
			return true
		}
		if len(inner) > 32 || looksLikeMeta(inner) || isTrackerOrSiteTag(inner) {
			continue
		}
		// Leading "[Group] Title..." is the standard fansub/anime layout.
		if i == 0 && m[0] == 0 {
			return true
		}
		// Trailing "... [Group]" without a scene "-GROUP" suffix also names
		// an anime encoder group (e.g. "Title S01E01 [1080p] [Judas]").
		if !hasSceneTail && isSingleGroupToken(inner) {
			return true
		}
	}
	return false
}

func isCRC32Bracket(v string) bool {
	if len(v) != 8 {
		return false
	}
	u := strings.ToUpper(v)
	if strings.Trim(u, "0123456789ABCDEF") != "" {
		return false
	}
	// Exclude 8-digit calendar dates like [20230831] or [19991020].
	if strings.Trim(u, "0123456789") == "" && (strings.HasPrefix(u, "19") || strings.HasPrefix(u, "20")) {
		return false
	}
	return true
}

func isTrackerOrSiteTag(v string) bool {
	low := strings.ToLower(strings.TrimSpace(v))
	if strings.HasPrefix(low, "www.") || strings.Contains(low, "http") || reDomainTag.MatchString(low) {
		return true
	}
	switch low {
	case "rarbg", "rartv", "tgx", "torrentgalaxy", "eztv", "ettv", "ethd", "yts", "yify", "glodls", "1337x":
		return true
	}
	return false
}

func isSingleGroupToken(v string) bool {
	if strings.ContainsAny(v, " \t()[]") {
		return false
	}
	hasLetter := false
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
			break
		}
	}
	return hasLetter
}
