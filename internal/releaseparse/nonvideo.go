package releaseparse

import (
	"path"
	"regexp"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

var (
	reNonVideoYEncTail = regexp.MustCompile(`(?i)\s*(?:-\s*)?yenc\b.*$`)
	reTrailingBracket  = regexp.MustCompile(`\s*\[[^\]]{1,24}\]\s*$`)
	reBaitDoubleExt    = regexp.MustCompile(`(?i)\.(mkv|mp4|m4v|avi|mov|wmv|ts|m2ts|mts|webm|mpg|mpeg|vob|divx|flv|ogm|asf|3gp|iso|rar|zip|7z)\.(lnk|exe|scr|bat|msi|cmd|ps1|vbs|hta|pif)($|[^a-z0-9])`)
)

var baitExts = map[string]bool{
	"lnk": true, "exe": true, "scr": true, "bat": true, "msi": true,
	"cmd": true, "ps1": true, "vbs": true, "hta": true, "pif": true,
}

var nonVideoFileExts = map[string]bool{
	"epub": true, "mobi": true, "azw3": true, "cbz": true, "cbr": true, "pdf": true,
	"m4b": true, "mp3": true, "flac": true, "ogg": true, "opus": true, "wav": true, "m4a": true,
}

var videoFileExts = map[string]bool{
	"mkv": true, "mp4": true, "m4v": true, "avi": true, "ts": true, "m2ts": true,
	"mts": true, "wmv": true, "mov": true, "mpg": true, "mpeg": true, "webm": true,
	"vob": true, "ogm": true, "divx": true, "flv": true, "3gp": true, "asf": true, "iso": true,
}

var nonVideoReleaseTokens = []string{
	"ORIGINAL SOUNDTRACK",
	"SOUNDTRACKS",
	"SOUNDTRACK",
	"OST",
	"AUDIOBOOKS",
	"AUDIOBOOK",
	"AUDIO BOOK",
	"EBOOKS",
	"EBOOK",
	"E-BOOK",
	"EPUB",
	"MOBI",
	"AZW3",
	"CBZ",
	"CBR",
	"M4B",
}

// IsBaitFile reports whether s names an executable or shortcut bait file
// (.lnk, .exe, .scr, .bat, .msi, or double extensions like .mkv.lnk / .mp4.exe).
func IsBaitFile(s string) bool {
	trimmed := cleanReleaseExtTarget(s)
	if trimmed == "" {
		return false
	}
	if reBaitDoubleExt.MatchString(s) || reBaitDoubleExt.MatchString(trimmed) {
		return true
	}
	ext := trailingExt(trimmed)
	return baitExts[ext]
}

// IsVideoRelease reports whether name carries positive video release evidence
// (resolution, video codec/container, HDR, or video source tag) and is not a
// bait or non-video release (eBook, audiobook, soundtrack/OST).
func IsVideoRelease(name string) bool {
	return !IsNonVideoRelease(name, "") && hasVideoEvidence(name)
}

// IsNonVideoRelease reports whether a release name or file hint identifies
// executable/shortcut bait or a non-video release (soundtrack/OST, ebook/EPUB,
// audiobook). expectedTitles optionally lists known titles of the requested
// movie or series so words that belong to the title itself (e.g. "Soundtrack")
// are not mistaken for non-video release tags.
func IsNonVideoRelease(name, fileName string, expectedTitles ...string) bool {
	if IsBaitFile(name) || IsBaitFile(fileName) {
		return true
	}
	if fileName != "" && !hasVideoEvidence(fileName) && nonVideoFileExts[trailingExt(cleanReleaseExtTarget(fileName))] {
		return true
	}
	if name != "" && !hasVideoEvidence(name) && nonVideoFileExts[trailingExt(cleanReleaseExtTarget(name))] {
		return true
	}
	if hasVideoEvidence(name) || (fileName != "" && hasVideoEvidence(fileName)) {
		return false
	}
	return hasNonVideoPayloadTag(name, expectedTitles) || hasNonVideoPayloadTag(fileName, expectedTitles)
}

func cleanReleaseExtTarget(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = reNonVideoYEncTail.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	for reTrailingBracket.MatchString(s) {
		next := strings.TrimSpace(reTrailingBracket.ReplaceAllString(s, ""))
		if next == "" || next == s {
			break
		}
		s = strings.Trim(next, `"'`)
	}
	return strings.TrimSpace(s)
}

func trailingExt(s string) string {
	s = strings.ReplaceAll(s, `\`, "/")
	base := path.Base(s)
	if base == "." || base == "/" {
		return ""
	}
	idx := strings.LastIndexByte(base, '.')
	if idx <= 0 || idx == len(base)-1 {
		return ""
	}
	return strings.ToLower(base[idx+1:])
}

func hasVideoEvidence(s string) bool {
	trimmed := cleanReleaseExtTarget(s)
	if trimmed == "" {
		return false
	}
	if videoFileExts[trailingExt(trimmed)] {
		return true
	}
	info := Parse(s)
	if info.Height > 0 || info.VideoCodec != "" || info.Container != "" || info.HDR10 || info.DolbyVision {
		return true
	}
	switch info.Source {
	case core.SourceRemux, core.SourceBluRay, core.SourceWEBRip, core.SourceHDTV, core.SourceDVD, core.SourceScreener, core.SourceCam:
		return true
	case core.SourceWEBDL:
		// Bare "WEB" is standard on music/OST releases ("Title.2024.OST.WEB.FLAC-GRP"),
		// whereas explicit WEB-DL / WEBDL marks a video download.
		upper := " " + strings.ToUpper(spacify(s)) + " "
		return containsAny(upper, "WEB-DL", "WEBDL", " WEB DL ")
	}
	return false
}

func hasNonVideoPayloadTag(s string, expectedTitles []string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	spaced := spacify(s)
	_, yearAt := parseYearAtWithStem(s, spaced)
	metaAt := firstMetadataAt(s, spaced)
	floor := yearAt
	if metaAt > 0 && (floor < 0 || metaAt-1 < floor) {
		floor = metaAt - 1
	}
	info := Parse(s)
	normParsedTitle := NormalizeTitle(info.Title)

	var normExpected []string
	for _, t := range expectedTitles {
		if nt := NormalizeTitle(t); nt != "" {
			normExpected = append(normExpected, nt)
		}
	}

	for _, tok := range nonVideoReleaseTokens {
		if floor >= 0 && tokenIndex(spaced[floor:], tok) >= 0 {
			return true
		}
		if tokenIndex(spaced, tok) < 0 {
			continue
		}
		normTok := NormalizeTitle(tok)
		if len(normExpected) > 0 {
			inTitle := false
			for _, want := range normExpected {
				if containsWholeWords(want, normTok) {
					inTitle = true
					break
				}
			}
			if !inTitle {
				return true
			}
			continue
		}
		// Without expected titles, reject unless the parsed title before
		// any year/metadata is solely that word itself (e.g. "Soundtrack (2011)").
		if normParsedTitle != normTok {
			return true
		}
	}
	return false
}

func containsWholeWords(haystack, needle string) bool {
	if haystack == "" || needle == "" {
		return false
	}
	return strings.Contains(" "+haystack+" ", " "+needle+" ")
}
