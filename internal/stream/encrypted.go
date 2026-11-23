package stream

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"path"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

func (b *builder) rarOptions(ctx context.Context, maxFiles int) *archive.RAROptions {
	return &archive.RAROptions{Context: ctx, Password: b.rel.doc.Password(), MaxFiles: maxFiles, PasswordCache: &b.rel.rarPasswords}
}

func (b *builder) rarOptionsFor(ctx context.Context, pf *postedFile, maxFiles int) *archive.RAROptions {
	opts := b.rarOptions(ctx, maxFiles)
	opts.SizeEstimated = pf != nil && !pf.SizeKnown()
	return opts
}

// rarVolSize returns the volume size to record in a RAR layout: pf.Size() when
// exact, else at least dataOff+dataSize so an underestimated postedFile size
// cannot reject a header-verified RAR extent.
func rarVolSize(pf *postedFile, dataOff, dataSize int64) int64 {
	size := pf.Size()
	if !pf.SizeKnown() && dataOff >= 0 && dataSize >= 0 && dataSize <= math.MaxInt64-dataOff {
		if end := dataOff + dataSize; end > size {
			size = end
		}
	}
	return size
}

// encryptedPlan retains ciphertext in the existing segment caches. Only bounded
// requested ranges are decrypted, and neither keys nor plaintext are persisted.
func (b *builder) encryptedPlan(ctx context.Context, kind planKind, member *archive.Member, vols []*postedFile, info []volInfo) (*plan, error) {
	if len(info) != len(vols) {
		return nil, ErrCorrupt
	}
	for i, pf := range vols {
		if pf.SizeKnown() {
			if info[i].size != pf.Size() {
				return nil, fmt.Errorf("stream: encrypted archive volume size is unverified: %w", ErrCorrupt)
			}
		} else if kind == kindRAR && info[i].size >= pf.Size() {
			pf.setAssumed(info[i].size)
		} else {
			return nil, fmt.Errorf("stream: encrypted archive volume size is unverified: %w", ErrCorrupt)
		}
	}
	m := *member
	m.Extents = append([]archive.Extent(nil), member.Extents...)
	if kind == kind7z {
		// The 7z parser addresses the concatenated archive. Resolve those offsets
		// against learned volume sizes before publishing an immutable crypto plan.
		var total int64
		raw := &layout{}
		for i, v := range info {
			if v.size < 0 || v.size > math.MaxInt64-total {
				return nil, ErrCorrupt
			}
			raw.ext = append(raw.ext, extent{inner: total, size: v.size, vol: i})
			total += v.size
		}
		raw.size = total
		m.Extents = nil
		for _, x := range member.Extents {
			if x.Volume != 0 || x.Offset < 0 || x.Size < 0 || x.Offset > total || x.Size > total-x.Offset {
				return nil, ErrCorrupt
			}
			for _, pc := range raw.pieces(x.Offset, x.Size) {
				m.Extents = append(m.Extents, archive.Extent{Volume: pc.vol, Offset: pc.off, Size: pc.n})
			}
		}
	}
	verified := make([]bool, len(vols))
	for i := range verified {
		verified[i] = true
	}
	p := newPlan(b.rel, kind, path.Base(m.Name), m.Size, vols, info, verified)
	p.member, p.encrypted = m.Name, &m
	if err := p.init(); err != nil {
		return nil, err
	}
	r, err := p.encryptedReader(ctx, b.e, b.rel, b.prio, b.fo)
	if err != nil {
		return nil, err
	}
	// Some formats lack a password check for data-only encryption. A successfully
	// parsed header must not bless random plaintext from a wrong password. This
	// bounded signature check is additional to archive CRCs and later media probe;
	// it cannot establish whole-file integrity for random-access playback.
	head := make([]byte, min(int64(4096), p.size))
	if _, err := r.ReadAt(head, 0); err != nil {
		return nil, err
	}
	if isISOName(p.name) {
		// A disc image has no video signature; its volume descriptors are the
		// check (ISO 9660 "CD001", or UDF's "BEA01", at sector 16).
		sig := make([]byte, 6)
		if p.size < 16*2048+6 {
			return nil, fmt.Errorf("stream: decrypted archive member is too small for a disc image: %w", ErrCorrupt)
		}
		if _, err := r.ReadAt(sig, 16*2048); err != nil {
			return nil, err
		}
		if s := string(sig[1:]); s != "CD001" && s != "BEA01" {
			return nil, fmt.Errorf("stream: decrypted archive member is not a disc image (incorrect password or damaged content): %w", ErrCorrupt)
		}
		return p, nil
	}
	if f := archive.Detect(head); f == archive.FormatRAR4 || f == archive.FormatRAR5 {
		// A RAR volume packed inside an encrypted archive: its signature is
		// the check; the video inside is checked when it is read.
		return p, nil
	}
	if fam, ok := b.encryptedTargetFamily(p.name); ok {
		if !looksLikeNonVideo(head, fam) {
			label := fam
			if label == "" {
				label = "media"
			}
			return nil, fmt.Errorf("stream: decrypted archive member has no supported %s signature (incorrect password or damaged content): %w", label, ErrCorrupt)
		}
		return p, nil
	}
	if !looksLikeVideo(head) {
		return nil, fmt.Errorf("stream: decrypted archive member has no supported video signature (incorrect password or damaged content): %w", ErrCorrupt)
	}
	return p, nil
}

// encryptedTargetFamily returns the expected non-video signature family when
// this plan targets a non-video archive member, or ("", false) when standard
// video signature validation must be enforced.
func (b *builder) encryptedTargetFamily(memberName string) (string, bool) {
	hint, ok := b.nonVideoArchiveHint()
	if !ok {
		return "", false
	}
	fam := nzb.MIMEFamily(hint.MIME)
	if fam == "video" {
		return "", false
	}
	fileFam := nzb.FileFamily(memberName)
	if fam == "" || (fam == "archive" && fileFam != "") {
		fam = fileFam
	}
	if fam == "" && hint.Track.NonEmpty() {
		fam = "audio"
	}
	if fam == "" && nzb.IsVideoExt(strings.TrimPrefix(path.Ext(memberName), ".")) {
		return "", false
	}
	return fam, true
}

// looksLikeNonVideo verifies that decrypted archive bytes begin with a file
// signature matching the expected non-video family ("audio", "book", "comic",
// "image", "archive", "text", or "" for any known media/book/archive format).
func looksLikeNonVideo(b []byte, family string) bool {
	if len(b) == 0 {
		return false
	}
	switch family {
	case "audio":
		return looksLikeAudio(b)
	case "book":
		return looksLikeBook(b)
	case "comic":
		return looksLikeComic(b)
	case "image":
		return looksLikeImage(b)
	case "archive":
		return looksLikeZIP(b) || archive.Detect(b) != archive.FormatUnknown
	case "text":
		return looksLikeText(b)
	default:
		return looksLikeAudio(b) || looksLikeBook(b) || looksLikeComic(b) || looksLikeImage(b) || looksLikeVideo(b)
	}
}

func looksLikeAudio(b []byte) bool {
	switch {
	case len(b) >= 4 && string(b[:4]) == "fLaC": // FLAC
		return true
	case len(b) >= 3 && string(b[:3]) == "ID3": // MP3 / audio with ID3v2 tag
		return true
	case len(b) >= 4 && b[0] == 0xFF && (b[1]&0xE0) == 0xE0 &&
		(b[1]&0x18) != 0x08 && (b[1]&0x06) != 0x00 &&
		(b[2]&0xF0) != 0xF0 && (b[2]&0x0C) != 0x0C: // MPEG audio (MP3/MP2/MP1) frame sync
		return true
	case len(b) >= 7 && b[0] == 0xFF && (b[1]&0xF6) == 0xF0 &&
		((b[2]>>2)&0x0F) < 13 &&
		(int(b[3]&0x03)<<11|int(b[4])<<3|int(b[5]>>5)) >= 7: // ADTS AAC
		return true
	case len(b) >= 8 && string(b[4:8]) == "ftyp": // M4A / M4B / MP4 audio
		return true
	case len(b) >= 4 && string(b[:4]) == "OggS": // OGG / Opus / Vorbis
		return true
	case len(b) >= 12 && (string(b[:4]) == "RIFF" || string(b[:4]) == "RF64") && string(b[8:12]) == "WAVE": // WAV
		return true
	case len(b) >= 12 && string(b[:4]) == "FORM" && (string(b[8:12]) == "AIFF" || string(b[8:12]) == "AIFC"): // AIFF
		return true
	case len(b) >= 4 && (string(b[:4]) == "wvpk" || string(b[:4]) == "MAC " || string(b[:4]) == "DSD "): // WavPack / APE / DSF
		return true
	case len(b) >= 16 && string(b[:4]) == "FRM8" && string(b[12:16]) == "DSD ": // DSDIFF
		return true
	case len(b) >= 16 && bytes.Equal(b[:8], []byte{0x30, 0x26, 0xb2, 0x75, 0x8e, 0x66, 0xcf, 0x11}): // ASF / WMA
		return true
	case len(b) >= 4 && bytes.Equal(b[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}): // Matroska / WebM audio (MKA/WEBA)
		return true
	}
	return false
}

func looksLikeZIP(b []byte) bool {
	if len(b) < 4 || b[0] != 'P' || b[1] != 'K' {
		return false
	}
	return (b[2] == 0x03 && b[3] == 0x04) || (b[2] == 0x05 && b[3] == 0x06) || (b[2] == 0x07 && b[3] == 0x08)
}

func looksLikeBook(b []byte) bool {
	switch {
	case looksLikeZIP(b): // EPUB / ZIP
		return true
	case len(b) >= 4 && string(b[:4]) == "%PDF": // PDF
		return true
	case len(b) >= 8 && string(b[:8]) == "AT&TFORM": // DjVu
		return true
	case len(b) >= 68 && (string(b[60:68]) == "BOOKMOBI" || string(b[60:68]) == "TEXtREAd"): // MOBI / AZW / PRC
		return true
	case len(b) >= 8 && string(b[:8]) == "ITOLITLS": // MS Reader LIT
		return true
	}
	trimmed := bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	trimmed = bytes.TrimLeft(trimmed, " \t\r\n")
	return bytes.HasPrefix(trimmed, []byte("<?xml")) || bytes.HasPrefix(trimmed, []byte("<FictionBook"))
}

func looksLikeComic(b []byte) bool {
	return looksLikeZIP(b) || archive.Detect(b) != archive.FormatUnknown || (len(b) >= 4 && string(b[:4]) == "%PDF")
}

func looksLikeImage(b []byte) bool {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF: // JPEG
		return true
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}): // PNG
		return true
	case len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"): // GIF
		return true
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP": // WebP
		return true
	case len(b) >= 18 && b[0] == 'B' && b[1] == 'M' && binary.LittleEndian.Uint32(b[6:10]) == 0: // BMP
		return true
	case len(b) >= 4 && (bytes.Equal(b[:4], []byte{'I', 'I', 0x2a, 0x00}) || bytes.Equal(b[:4], []byte{'M', 'M', 0x00, 0x2a})): // TIFF
		return true
	case len(b) >= 8 && string(b[4:8]) == "ftyp": // AVIF / HEIC / HEIF
		return true
	case len(b) >= 8 && string(b[:8]) == "AT&TFORM": // DjVu
		return true
	}
	return false
}

func looksLikeText(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	// If the 4 KiB signature window split a multi-byte UTF-8 rune at the tail,
	// trim the trailing continuation/lead fragment before validating UTF-8.
	s := b
	if len(s) == 4096 {
		for trim := 0; trim < 3 && len(s) > 0 && !utf8.Valid(s); trim++ {
			s = s[:len(s)-1]
		}
	}
	if len(s) == 0 || !utf8.Valid(s) {
		return false
	}
	for _, r := range string(s) {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' && r != '\f' {
			return false
		}
	}
	return true
}

// encryptedLayout builds a plaintext view for read-ahead scheduling while
// retaining the complete ciphertext view for CBC dependencies and padding.
func (p *plan) encryptedLayout(version int) (*layout, error) {
	m := p.encrypted
	if m == nil || m.Encryption == nil || len(p.info) != len(p.vols) || len(p.verified) != len(p.vols) {
		return nil, ErrCorrupt
	}
	c := &layout{version: version}
	for _, x := range m.Extents {
		if x.Volume < 0 || x.Volume >= len(p.info) || !p.verified[x.Volume] {
			return nil, ErrCorrupt
		}
		size := p.info[x.Volume].size
		if size < 0 || x.Offset < 0 || x.Size < 0 || x.Offset > size || x.Size > size-x.Offset || x.Size > math.MaxInt64-c.size {
			return nil, ErrCorrupt
		}
		c.ext = append(c.ext, extent{inner: c.size, size: x.Size, vol: x.Volume, off: x.Offset})
		c.size += x.Size
	}
	enc := m.Encryption
	if p.size < 0 || enc.Size < 0 || enc.Offset < 0 || enc.Offset > enc.Size || p.size > enc.Size-enc.Offset || enc.Size > c.size || c.size-enc.Size > 15 || c.size%16 != 0 {
		return nil, ErrCorrupt
	}
	l := &layout{version: version, size: p.size}
	for _, pc := range c.pieces(enc.Offset, p.size) {
		l.ext = append(l.ext, extent{inner: pc.bufOff, size: pc.n, vol: pc.vol, off: pc.off})
	}
	p.cipherLayout = c
	return l, nil
}

func (p *plan) encryptedReader(ctx context.Context, e *Engine, rel *release, prio core.Priority, fo fetchOpts) (io.ReaderAt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := &builder{e: e, rel: rel, prio: prio, fo: fo}
	vols := make([]archive.Volume, len(p.vols))
	for i, pf := range p.vols {
		vols[i] = archive.Volume{R: b.reader(ctx, pf), Size: p.info[i].size}
	}
	return archive.NewMemberReader(*p.encrypted, vols)
}

// fetchPieces includes the CBC predecessor block and end padding. Persistent
// preloading must retain these bytes too, even when they sit in another article
// or another archive volume. Piece bufOff values here are ciphertext-relative;
// these pieces are for cache warming, never copied directly to a media response.
func (p *plan) fetchPieces(off, n int64) []piece {
	if p.encrypted == nil {
		return p.layout().pieces(off, n)
	}
	if off < 0 {
		off = 0
	}
	if off >= p.size || n <= 0 {
		return nil
	}
	n = min(n, p.size-off)
	start := p.encrypted.Encryption.Offset + off
	end := start + n
	start -= start % 16
	if start > 0 {
		start -= 16
	}
	if rem := end % 16; rem != 0 {
		end += 16 - rem
	}
	return p.cipherLayout.pieces(start, end-start)
}

// readEncryptedPlan preserves demand-read parallelism across articles. The CBC
// adapter has bounded scratch chunks, but serially feeding one large range to
// it would serialize network requests when speculative read-ahead is disabled.
// Each range has its own CBC state and predecessor; the shared ReaderAt is safe
// for concurrent calls. Workers are joined before returning caller-owned bytes.
func readEncryptedPlan(parent context.Context, e *Engine, rel *release, p *plan, buf []byte, off int64, prio core.Priority, fo fetchOpts) (int, error) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	r, err := p.encryptedReader(ctx, e, rel, prio, fo)
	if err != nil {
		return 0, err
	}
	const chunk = 256 << 10
	count := (len(buf) + chunk - 1) / chunk
	if count <= 1 {
		return r.ReadAt(buf, off)
	}
	type result struct {
		n   int
		err error
	}
	results := make([]result, count)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < min(count, max(1, e.opts.ReadParallel)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				lo := index * chunk
				hi := min(lo+chunk, len(buf))
				if err := ctx.Err(); err != nil {
					results[index].err = err
					continue
				}
				n, err := r.ReadAt(buf[lo:hi], off+int64(lo))
				results[index] = result{n, err}
				if err != nil {
					cancel(err)
				}
			}
		}()
	}
	for index := 0; index < count; index++ {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	n := 0
	for _, res := range results {
		n += res.n
		if res.err != nil {
			if cause := context.Cause(ctx); cause != nil {
				return n, cause
			}
			return n, res.err
		}
	}
	return n, nil
}
