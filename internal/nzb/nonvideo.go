package nzb

import (
	"path"
	"strings"
)

// NormalizeMIME strips parameters and normalises ASCII case on a MIME string.
func NormalizeMIME(raw string) string {
	s := strings.TrimSpace(raw)
	if idx := strings.IndexByte(s, ';'); idx >= 0 {
		s = s[:idx]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// IsArchiveMIME reports whether raw names a generic wrapper archive format
// (ZIP, RAR, 7z) rather than a self-contained media format like EPUB or CBZ.
func IsArchiveMIME(raw string) bool {
	switch NormalizeMIME(raw) {
	case "application/zip", "application/x-zip-compressed",
		"application/vnd.rar", "application/x-rar-compressed", "application/x-rar",
		"application/x-7z-compressed":
		return true
	default:
		return false
	}
}

// MIMEFamily maps a MIME type onto a broad non-video or video media family
// ("audio", "book", "comic", "image", "text", "video", "archive", or "").
func MIMEFamily(raw string) string {
	m := NormalizeMIME(raw)
	if m == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(m, "video/"):
		return "video"
	case strings.HasPrefix(m, "audio/"), m == "application/ogg":
		return "audio"
	case m == "image/vnd.djvu", m == "application/vnd.djvu",
		m == "application/epub+zip",
		m == "application/pdf",
		m == "application/x-mobipocket-ebook",
		m == "application/vnd.amazon.ebook",
		m == "application/vnd.amazon.mobi8-ebook",
		m == "application/x-fictionbook+xml":
		return "book"
	case m == "application/vnd.comicbook+zip",
		m == "application/vnd.comicbook-rar",
		m == "application/x-cbz",
		m == "application/x-cbr",
		m == "application/x-cb7":
		return "comic"
	case strings.HasPrefix(m, "image/"):
		return "image"
	case strings.HasPrefix(m, "text/"):
		return "text"
	case IsArchiveMIME(m):
		return "archive"
	default:
		return ""
	}
}

// IsNonVideoMIME reports whether raw names a concrete non-video playable media
// type (audio, ebook, comic, image, or text) suitable for automatic non-video
// release file selection.
func IsNonVideoMIME(raw string) bool {
	switch MIMEFamily(raw) {
	case "audio", "book", "comic", "image", "text":
		return true
	default:
		return false
	}
}

// NonVideoMatchRank scores how well filename matches declaredMIME for
// automatic non-video release selection:
//
//	2 = extension matches the exact declared MIME type
//	1 = extension belongs to the same non-video media family
//	0 = not a usable match (wrong family, video, metadata/junk, or no MIME)
func NonVideoMatchRank(filename, declaredMIME string) int {
	want := NormalizeMIME(declaredMIME)
	wantFam := MIMEFamily(want)
	if wantFam == "" || wantFam == "video" || wantFam == "archive" {
		return 0
	}
	ext := strings.ToLower(path.Ext(strings.TrimSpace(filename)))
	if ext == "" {
		return 0
	}
	extFam, exactMIMEs := nonVideoExtInfo(ext)
	if extFam == "" || extFam != wantFam {
		return 0
	}
	for _, m := range exactMIMEs {
		if m == want {
			return 2
		}
	}
	return 1
}

// nonVideoExtInfo maps a lowercase extension (with leading dot) to its media
// family and the canonical MIME types it matches exactly. Video extensions,
// archive wrappers, and release metadata (.par2, .nfo, .sfv, .cue, .log, .opf)
// intentionally return ("", nil) so automatic selection never picks them.
func nonVideoExtInfo(ext string) (string, []string) {
	switch ext {
	// Audio (tracks and audiobooks). Note: .mp4 is excluded so video MP4s are
	// never mistaken for audio.
	case ".flac":
		return "audio", []string{"audio/flac", "audio/x-flac"}
	case ".mp3":
		return "audio", []string{"audio/mpeg", "audio/mp3"}
	case ".m4a":
		return "audio", []string{"audio/mp4", "audio/x-m4a", "audio/aac"}
	case ".m4b":
		return "audio", []string{"audio/mp4", "audio/x-m4b", "audio/x-m4a"}
	case ".aac":
		return "audio", []string{"audio/aac", "audio/mp4"}
	case ".ogg", ".oga":
		return "audio", []string{"audio/ogg", "application/ogg"}
	case ".opus":
		return "audio", []string{"audio/opus", "audio/ogg"}
	case ".wav":
		return "audio", []string{"audio/wav", "audio/x-wav", "audio/wave"}
	case ".aiff", ".aif":
		return "audio", []string{"audio/aiff", "audio/x-aiff"}
	case ".alac", ".wma", ".wv", ".ape", ".dsf", ".dff", ".ac3", ".eac3", ".dts":
		return "audio", nil

	// Books / documents.
	case ".epub":
		return "book", []string{"application/epub+zip"}
	case ".pdf":
		return "book", []string{"application/pdf"}
	case ".mobi", ".prc":
		return "book", []string{"application/x-mobipocket-ebook", "application/vnd.amazon.ebook"}
	case ".azw", ".azw3", ".kfx":
		return "book", []string{"application/vnd.amazon.ebook", "application/vnd.amazon.mobi8-ebook", "application/x-mobipocket-ebook"}
	case ".fb2":
		return "book", []string{"application/x-fictionbook+xml"}
	case ".djvu", ".djv":
		return "book", []string{"image/vnd.djvu", "application/vnd.djvu"}
	case ".lit":
		return "book", nil

	// Comics.
	case ".cbz":
		return "comic", []string{"application/vnd.comicbook+zip", "application/x-cbz"}
	case ".cbr":
		return "comic", []string{"application/vnd.comicbook-rar", "application/x-cbr"}
	case ".cb7":
		return "comic", []string{"application/x-cb7"}

	// Images.
	case ".jpg", ".jpeg":
		return "image", []string{"image/jpeg"}
	case ".png":
		return "image", []string{"image/png"}
	case ".webp":
		return "image", []string{"image/webp"}
	case ".gif":
		return "image", []string{"image/gif"}
	case ".bmp":
		return "image", []string{"image/bmp"}
	case ".tif", ".tiff":
		return "image", []string{"image/tiff"}
	case ".avif":
		return "image", []string{"image/avif"}
	case ".heic", ".heif":
		return "image", []string{"image/heic", "image/heif"}

	// Text.
	case ".txt":
		return "text", []string{"text/plain"}
	case ".md":
		return "text", []string{"text/markdown", "text/plain"}
	case ".html", ".htm":
		return "text", []string{"text/html"}
	case ".vtt":
		return "text", []string{"text/vtt"}
	case ".srt", ".ass", ".ssa":
		return "text", []string{"text/plain"}

	default:
		return "", nil
	}
}

// FileFamily returns the non-video media family for name's file extension
// ("audio", "book", "comic", "image", "text", or "").
func FileFamily(name string) string {
	ext := strings.ToLower(path.Ext(strings.TrimSpace(name)))
	family, _ := nonVideoExtInfo(ext)
	return family
}

// IsNonVideoFile reports whether name carries a known non-video media
// extension (audio, book, comic, image, or text).
func IsNonVideoFile(name string) bool {
	return FileFamily(name) != ""
}

// CleanArchivePath normalises a release or archive member path for matching.
func CleanArchivePath(raw string) string {
	s := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	s = strings.TrimLeft(s, "/")
	if s == "" {
		return ""
	}
	c := path.Clean(s)
	if c == "." || c == "/" {
		return ""
	}
	return c
}

// MatchPathIndex finds the unique index in names matching want by:
//  1. exact normalised path (case-insensitive)
//  2. unique suffix path match (when want contains a slash, e.g. "CD1/01.flac" inside "Album/CD1/01.flac")
//  3. unique basename match (when want has no slash)
//
// It returns (idx, true, false) on a unique match, (-1, false, true) when
// multiple entries match ambiguously, or (-1, false, false) when none match.
func MatchPathIndex(names []string, want string) (idx int, found bool, ambiguous bool) {
	normWant := strings.ToLower(CleanArchivePath(want))
	if normWant == "" {
		return -1, false, false
	}
	exactIdx := -1
	exactCount := 0
	for i, raw := range names {
		if strings.ToLower(CleanArchivePath(raw)) == normWant {
			exactIdx = i
			exactCount++
		}
	}
	if exactCount == 1 {
		return exactIdx, true, false
	}
	if exactCount > 1 {
		return -1, false, true
	}

	if strings.Contains(normWant, "/") {
		suffix := "/" + normWant
		sufIdx := -1
		sufCount := 0
		for i, raw := range names {
			if strings.HasSuffix(strings.ToLower(CleanArchivePath(raw)), suffix) {
				sufIdx = i
				sufCount++
			}
		}
		if sufCount == 1 {
			return sufIdx, true, false
		}
		if sufCount > 1 {
			return -1, false, true
		}
		return -1, false, false
	}

	baseIdx := -1
	baseCount := 0
	for i, raw := range names {
		norm := strings.ToLower(CleanArchivePath(raw))
		if norm != "" && path.Base(norm) == normWant {
			baseIdx = i
			baseCount++
		}
	}
	if baseCount == 1 {
		return baseIdx, true, false
	}
	if baseCount > 1 {
		return -1, false, true
	}
	return -1, false, false
}
