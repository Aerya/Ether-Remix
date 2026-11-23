// Package reading centralizes ebook format detection, safe virtual naming,
// and media-class predicates shared by frontends that expose books for reading
// (Vela /read and Jellyfin/Emby /Items/{id}/Download & /File).
package reading

import (
	"bytes"
	"context"
	"io"
	"path"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/libpath"
)

// Formats lists the canonical reading formats Ether serves to reader clients.
var Formats = []string{"epub", "pdf", "cbz"}

// AcceptPatterns lists the kindstream retrieval patterns readable book
// endpoints can serve as byte-addressable streams.
var AcceptPatterns = []string{"range", "download", "container", "release", "file"}

// IsBookTitle reports whether t represents a book/ebook item that must open
// via reading/download paths rather than A/V playback.
func IsBookTitle(t frontend.Title) bool {
	return t.MediaClass == frontend.MediaClassBook || t.MBItemType == "Book"
}

// CanonicalFormat normalizes a format name, file extension, or MIME type into
// a supported reading format ("epub", "pdf", or "cbz") and canonical MIME type.
func CanonicalFormat(raw string) (format, mimeType string, ok bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	switch s {
	case "epub", ".epub", "application/epub+zip":
		return "epub", "application/epub+zip", true
	case "pdf", ".pdf", "application/pdf":
		return "pdf", "application/pdf", true
	case "cbz", ".cbz", "application/vnd.comicbook+zip", "application/x-cbz":
		return "cbz", "application/vnd.comicbook+zip", true
	default:
		return "", "", false
	}
}

// TitleFormat returns the projected reading format and MIME type for a book
// title from its metadata, defaulting to EPUB when unspecified.
func TitleFormat(t frontend.Title) (format, mimeType string) {
	if t.MediaReading != nil {
		if f, m, ok := CanonicalFormat(t.MediaReading.Format); ok {
			return f, m
		}
	}
	return "epub", "application/epub+zip"
}

// VirtualPath builds a deterministic, O(1) virtual book path, container name,
// and MIME type for a book title without scanning any library.
func VirtualPath(t frontend.Title) (virtualPath, container, mimeType string) {
	format, mime := TitleFormat(t)
	return "/Books/" + libpath.Safe(t.Title) + "." + format, format, mime
}

// DetectFormat identifies the canonical reading format and MIME type from an
// opened KindMedia stream, falling back to title metadata and magic bytes.
func DetectFormat(media *frontend.KindMedia, title frontend.Title) (format, mimeType string, ok bool) {
	if media != nil {
		if f, m, ok := CanonicalFormat(media.MIME); ok {
			return f, m, true
		}
		if ext := path.Ext(media.Name); ext != "" {
			if f, m, ok := CanonicalFormat(ext); ok {
				return f, m, true
			}
		}
	}
	if title.MediaReading != nil {
		if f, m, ok := CanonicalFormat(title.MediaReading.Format); ok {
			return f, m, true
		}
	}
	if media != nil && media.ReaderAt != nil && media.Size >= 5 {
		var hdr [64]byte
		n, _ := media.ReadAt(hdr[:], 0)
		b := hdr[:n]
		if len(b) >= 5 && bytes.Equal(b[:5], []byte("%PDF-")) {
			return "pdf", "application/pdf", true
		}
		if len(b) >= 4 && bytes.Equal(b[:4], []byte{'P', 'K', 0x03, 0x04}) && bytes.Contains(b, []byte("application/epub+zip")) {
			return "epub", "application/epub+zip", true
		}
	}
	return "", "", false
}

// Filename returns a safe download filename ending with "."+format.
func Filename(mediaName, fallbackTitle, format string) string {
	ext := "." + format
	raw := strings.TrimSpace(strings.ReplaceAll(mediaName, `\`, "/"))
	base := path.Base(raw)
	if base == "" || base == "." || base == "/" {
		if t := strings.TrimSpace(fallbackTitle); t != "" {
			return libpath.Safe(t) + ext
		}
		return "book" + ext
	}
	safe := libpath.Safe(base)
	if strings.EqualFold(path.Ext(safe), ext) {
		return safe
	}
	return safe + ext
}

// ContextReaderAt binds a request context to a ReaderAt that supports
// core.ContextReaderAt so client disconnects cancel blocked upstream reads.
func ContextReaderAt(ctx context.Context, r io.ReaderAt) io.ReaderAt {
	if cr, ok := r.(core.ContextReaderAt); ok {
		return contextReaderAt{ctx: ctx, r: cr}
	}
	return r
}

type contextReaderAt struct {
	ctx context.Context
	r   core.ContextReaderAt
}

func (c contextReaderAt) ReadAt(p []byte, off int64) (int, error) {
	return c.r.ReadAtContext(c.ctx, p, off)
}
