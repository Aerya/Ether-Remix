// Package rangefile serves seekable media and download files with stable
// strong validators (ETag and Last-Modified) and HTTP byte-range support so
// clients such as Apple URLSession / Plex for iOS can validate and resume
// interrupted transfers with Range and If-Range.
package rangefile

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// StrongETag formats a deterministic strong quoted ETag (`"<20 hex>"`) from
// a stable representation identity, byte size, and modification timestamp
// truncated to HTTP-date second resolution (matching internal/playback).
func StrongETag(identity string, size int64, modTime time.Time) string {
	var sec int64
	if !modTime.IsZero() {
		sec = modTime.UTC().Truncate(time.Second).Unix()
	}
	sum := sha1.Sum(fmt.Appendf(nil, "%s:%d:%d", identity, size, sec))
	return `"` + hex.EncodeToString(sum[:10]) + `"`
}

// Serve serves content with http.ServeContent after attaching a strong ETag
// (when identity is non-empty, size >= 0, and no ETag is already set) and
// truncating modTime to UTC seconds so HEAD/GET validators, Accept-Ranges,
// Content-Length, Range, and If-Range stay consistent across retries.
func Serve(w http.ResponseWriter, r *http.Request, name, identity string, size int64, modTime time.Time, content io.ReadSeeker) {
	if !modTime.IsZero() {
		modTime = modTime.UTC().Truncate(time.Second)
	}
	if identity != "" && size >= 0 && w.Header().Get("ETag") == "" {
		w.Header().Set("ETag", StrongETag(identity, size, modTime))
	}
	http.ServeContent(w, r, name, modTime, content)
}

// ServeFile stats f, falls back to f's mtime when modTime is zero, and serves
// the file with a strong ETag and HTTP byte-range support.
func ServeFile(w http.ResponseWriter, r *http.Request, name, identity string, modTime time.Time, f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if modTime.IsZero() {
		modTime = info.ModTime()
	}
	Serve(w, r, name, identity, info.Size(), modTime, f)
	return nil
}

// ServeSeeker determines content's byte length via Seek when the caller holds
// an io.ReadSeeker without an *os.File, then delegates to Serve.
func ServeSeeker(w http.ResponseWriter, r *http.Request, name, identity string, modTime time.Time, content io.ReadSeeker) {
	var size int64 = -1
	if n, err := content.Seek(0, io.SeekEnd); err == nil {
		if _, err := content.Seek(0, io.SeekStart); err == nil {
			size = n
		}
	}
	Serve(w, r, name, identity, size, modTime, content)
}
