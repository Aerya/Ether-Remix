package nzb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"
)

// ContentIDPrefix namespaces NZB content identities derived from segment message-IDs.
const ContentIDPrefix = "nzb:"

// FormatContentID formats a 32-byte NZB content digest as "nzb:<64-char lowercase hex>".
func FormatContentID(sum [32]byte) string {
	return ContentIDPrefix + hex.EncodeToString(sum[:])
}

// ParseContentID decodes an "nzb:<64-char hex>" (or bare 64-char hex) content identity
// into its 32-byte SHA-256 digest.
func ParseContentID(id string) ([32]byte, bool) {
	var out [32]byte
	s := strings.TrimSpace(id)
	s = strings.TrimPrefix(s, ContentIDPrefix)
	if len(s) != sha256.Size*2 {
		return out, false
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != sha256.Size {
		return out, false
	}
	copy(out[:], b)
	return out, true
}

// ContentSum computes the SHA-256 digest over all sorted unique primary segment
// message-IDs across every file in doc (each normalized without angle brackets
// and terminated by '\n'). All files are included so subject de-obfuscation
// differences across indexers never alter the key.
func (n *NZB) ContentSum() ([32]byte, bool) {
	var zero [32]byte
	if n == nil || len(n.Files) == 0 {
		return zero, false
	}
	total := 0
	for _, f := range n.Files {
		if f != nil {
			total += len(f.Segments)
		}
	}
	if total == 0 {
		return zero, false
	}
	ids := make([]string, 0, total)
	for _, f := range n.Files {
		if f == nil {
			continue
		}
		for _, s := range f.Segments {
			id := normalizeMessageID(s.MessageID)
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return zero, false
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	h := sha256.New()
	var nl [1]byte
	nl[0] = '\n'
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write(nl[:])
	}
	var sum [32]byte
	h.Sum(sum[:0])
	return sum, true
}

// ContentID returns "nzb:<sha256>" for the parsed NZB document, or "" if empty.
func (n *NZB) ContentID() string {
	sum, ok := n.ContentSum()
	if !ok {
		return ""
	}
	return FormatContentID(sum)
}

// ContentSumBytes parses data as an NZB and returns its 32-byte content digest.
func ContentSumBytes(data []byte) ([32]byte, bool) {
	doc, ok := parseForContent(data)
	if !ok {
		return [32]byte{}, false
	}
	return doc.ContentSum()
}

// ContentID returns "nzb:<sha256>" over the sorted unique segment message-IDs
// in data, or "" when data is not an NZB document with at least one segment.
func ContentID(data []byte) string {
	sum, ok := ContentSumBytes(data)
	if !ok {
		return ""
	}
	return FormatContentID(sum)
}

// InspectContent extracts the NZB content identity ("nzb:<sha256>" over sorted
// segment message-IDs) together with the first posted file's poster, UTC upload
// timestamp, and total listed segment bytes. Non-NZB payloads (such as empty
// slices or JSON descriptors) return zero values.
func InspectContent(data []byte) (contentID, poster string, uploadDate time.Time, totalBytes int64) {
	doc, ok := parseForContent(data)
	if !ok {
		return "", "", time.Time{}, 0
	}
	sum, ok := doc.ContentSum()
	if !ok {
		return "", "", time.Time{}, 0
	}
	for _, f := range doc.Files {
		if f == nil {
			continue
		}
		if poster == "" && f.Poster != "" {
			poster = f.Poster
		}
		if uploadDate.IsZero() && !f.Date.IsZero() {
			uploadDate = f.Date.UTC()
		}
		for _, s := range f.Segments {
			if s.Bytes > 0 {
				totalBytes += s.Bytes
			}
		}
	}
	return FormatContentID(sum), poster, uploadDate, totalBytes
}

func parseForContent(data []byte) (*NZB, bool) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] == '{' || trimmed[0] == '[' {
		return nil, false
	}
	doc, err := Parse(trimmed)
	if err != nil {
		return nil, false
	}
	return doc, true
}

func normalizeMessageID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return strings.TrimSpace(id)
}
