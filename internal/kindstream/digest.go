package kindstream

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/core"
)

var (
	// ErrDigestMismatch is returned when served bytes do not match a digest
	// or expected size the plugin declared on the source.
	ErrDigestMismatch = errors.New("kindstream: bytes do not match the declared digest")

	// ErrPluginAnswer marks an open failure that points at the plugin's own
	// answer — a missing archive member, an out-of-range member index, a
	// digest or size mismatch — rather than at a host being unavailable. A
	// fallback that opens after such a failure must not be saved as the
	// item's resolution: the plugin's first choice is broken, not absent.
	ErrPluginAnswer = errors.New("kindstream: source contradicts the plugin's answer")
)

const (
	// approxSizeMinTolerance covers per-article yEnc framing overhead on small
	// files where percentage-only tolerance would be too tight.
	approxSizeMinTolerance int64 = 4096
	// approxSizeToleranceDiv sets the relative tolerance band (1/10 = 10%) for
	// sources whose declared size is marked approximate.
	approxSizeToleranceDiv int64 = 10
)

// expectedDigests is the set of checks a PlayableSource declares.
type expectedDigests struct {
	sha256          string
	sha1            string
	crc32           string
	size            int64
	sizeApproximate bool
}

func digestsOf(src core.PlayableSource) expectedDigests {
	approx := src.SizeApproximate
	if !approx && src.Size > 0 && src.Release != nil && src.Release.Candidate != nil {
		if f, ok := selectedCandidateFile(src.Release); ok && f.SizeApproximate && f.Size == src.Size {
			approx = true
		}
	}
	size := src.Size
	if src.Release != nil && !approx {
		// Plugins copy the indexer's listed size (the whole release, rounded
		// or encoded) into a release source's size, so an exact-looking size
		// never rejects the file Ether picks inside it. An approximate hint
		// keeps its tolerance band and a declared hash still verifies bytes.
		size = 0
	}
	return expectedDigests{
		sha256:          strings.ToLower(strings.TrimSpace(src.SHA256)),
		sha1:            strings.ToLower(strings.TrimSpace(src.SHA1)),
		crc32:           strings.ToLower(strings.TrimSpace(src.CRC32)),
		size:            size,
		sizeApproximate: approx,
	}
}

func selectedCandidateFile(rel *core.ReleaseHint) (core.ReleaseFile, bool) {
	if rel == nil || rel.Candidate == nil || len(rel.Candidate.Files) == 0 {
		return core.ReleaseFile{}, false
	}
	idx := rel.FileIndex
	if idx == nil {
		idx = rel.Candidate.FileIndex
	}
	if idx != nil {
		for _, f := range rel.Candidate.Files {
			if f.Index == *idx {
				return f, true
			}
		}
	}
	want := strings.TrimSpace(strings.ReplaceAll(rel.File, "\\", "/"))
	want = strings.TrimPrefix(want, "/")
	if want == "" {
		return core.ReleaseFile{}, false
	}
	var baseMatch core.ReleaseFile
	baseCount := 0
	for _, f := range rel.Candidate.Files {
		fp := strings.TrimPrefix(strings.ReplaceAll(f.Path, "\\", "/"), "/")
		if strings.EqualFold(fp, want) {
			return f, true
		}
		if strings.EqualFold(path.Base(fp), want) {
			baseMatch = f
			baseCount++
		}
	}
	if baseCount == 1 {
		return baseMatch, true
	}
	return core.ReleaseFile{}, false
}

func (d expectedDigests) hasHash() bool  { return d.sha256 != "" || d.sha1 != "" || d.crc32 != "" }
func (d expectedDigests) hasCheck() bool { return d.hasHash() || d.size > 0 }

func approxSizeTolerance(declared int64) int64 {
	tol := declared / approxSizeToleranceDiv
	if tol < approxSizeMinTolerance {
		return approxSizeMinTolerance
	}
	return tol
}

// checkSize rejects a served size that contradicts the declared one before
// any bytes are read. Exact sizes (sizeApproximate == false) require exact
// equality; approximate sizes accept a tolerance band around the hint.
func (d expectedDigests) checkSize(got int64) error {
	if d.size <= 0 {
		return nil
	}
	if !d.sizeApproximate {
		if got != d.size {
			return fmt.Errorf("%w: size %d, want %d: %w", ErrDigestMismatch, got, d.size, ErrPluginAnswer)
		}
		return nil
	}
	if got <= 0 {
		return fmt.Errorf("%w: size %d, want ~%d: %w", ErrDigestMismatch, got, d.size, ErrPluginAnswer)
	}
	diff := got - d.size
	if diff < 0 {
		diff = -diff
	}
	if tol := approxSizeTolerance(d.size); diff > tol {
		return fmt.Errorf("%w: size %d outside approximate hint %d (±%d): %w", ErrDigestMismatch, got, d.size, tol, ErrPluginAnswer)
	}
	return nil
}

func (d expectedDigests) hashers() (writers []io.Writer, finish func() error) {
	var h256, h1 hash.Hash
	var hcrc hash.Hash32
	if d.sha256 != "" {
		h256 = sha256.New()
		writers = append(writers, h256)
	}
	if d.sha1 != "" {
		h1 = sha1.New()
		writers = append(writers, h1)
	}
	if d.crc32 != "" {
		hcrc = crc32.NewIEEE()
		writers = append(writers, hcrc)
	}
	finish = func() error {
		if h256 != nil {
			if got := hex.EncodeToString(h256.Sum(nil)); got != d.sha256 {
				return fmt.Errorf("%w: sha256 %s, want %s: %w", ErrDigestMismatch, got, d.sha256, ErrPluginAnswer)
			}
		}
		if h1 != nil {
			if got := hex.EncodeToString(h1.Sum(nil)); got != d.sha1 {
				return fmt.Errorf("%w: sha1 %s, want %s: %w", ErrDigestMismatch, got, d.sha1, ErrPluginAnswer)
			}
		}
		if hcrc != nil {
			want, err := strconv.ParseUint(strings.TrimPrefix(d.crc32, "0x"), 16, 32)
			if err != nil {
				return fmt.Errorf("%w: declared crc32 %q is not hex: %w", ErrDigestMismatch, d.crc32, ErrPluginAnswer)
			}
			if got := hcrc.Sum32(); uint64(got) != want {
				return fmt.Errorf("%w: crc32 %08x, want %08x: %w", ErrDigestMismatch, got, want, ErrPluginAnswer)
			}
		}
		return nil
	}
	return writers, finish
}

// verifyReaderAt hashes a whole ByteSource sequentially and compares every
// declared digest.
func (d expectedDigests) verifyReaderAt(r io.ReaderAt, size int64) error {
	if err := d.checkSize(size); err != nil {
		return err
	}
	if !d.hasHash() {
		return nil
	}
	writers, finish := d.hashers()
	sr := io.NewSectionReader(r, 0, size)
	if _, err := io.CopyBuffer(io.MultiWriter(writers...), sr, make([]byte, 256<<10)); err != nil {
		return err
	}
	return finish()
}

func (d expectedDigests) verifyBytes(b []byte) error {
	if err := d.checkSize(int64(len(b))); err != nil {
		return err
	}
	if !d.hasHash() {
		return nil
	}
	writers, finish := d.hashers()
	_, _ = io.MultiWriter(writers...).Write(b)
	return finish()
}

// digestVerifyReader fails instead of returning io.EOF when the stream it
// saw does not match the declared digests or size, so a cache Put consuming it
// discards the bytes.
type digestVerifyReader struct {
	r      io.Reader
	w      io.Writer
	d      expectedDigests
	finish func() error
	nread  int64
	done   bool
	err    error
}

func newDigestVerifyReader(r io.Reader, d expectedDigests) io.Reader {
	if !d.hasCheck() {
		return r
	}
	var w io.Writer
	var finish func() error
	if d.hasHash() {
		writers, fn := d.hashers()
		w = io.MultiWriter(writers...)
		finish = fn
	}
	return &digestVerifyReader{r: r, w: w, d: d, finish: finish}
}

func (v *digestVerifyReader) Read(p []byte) (int, error) {
	if v.done {
		return 0, v.err
	}
	n, err := v.r.Read(p)
	if n > 0 {
		v.nread += int64(n)
		if v.w != nil {
			_, _ = v.w.Write(p[:n])
		}
	}
	if errors.Is(err, io.EOF) {
		v.done = true
		if serr := v.d.checkSize(v.nread); serr != nil {
			v.err = serr
			return n, serr
		}
		if v.finish != nil {
			if ferr := v.finish(); ferr != nil {
				v.err = ferr
				return n, ferr
			}
		}
		v.err = io.EOF
		return n, io.EOF
	}
	return n, err
}

// DigestReporter is implemented by a ByteSource whose declared digest is
// still being verified as it streams. DigestState reports whether a digest
// was declared and whether the full bytes have been verified yet.
type DigestReporter interface {
	DigestState() (declared, verified bool)
}

// verifyingByteSource hashes bytes as they are read in order and fails the
// read that completes the file when the digests disagree. A seek past the
// hashed frontier makes verification impossible; bytes still serve, but the
// source never reports verified.
type verifyingByteSource struct {
	ByteSource
	mu       sync.Mutex
	w        io.Writer
	finish   func() error
	frontier int64
	broken   bool
	failed   error
	verified bool
}

func newVerifyingByteSource(bs ByteSource, d expectedDigests) *verifyingByteSource {
	writers, finish := d.hashers()
	return &verifyingByteSource{ByteSource: bs, w: io.MultiWriter(writers...), finish: finish}
}

func (v *verifyingByteSource) DigestState() (bool, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return true, v.verified
}

func (v *verifyingByteSource) account(p []byte, off int64, n int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failed != nil {
		return v.failed
	}
	if v.broken || v.verified || n == 0 {
		return nil
	}
	end := off + int64(n)
	if off > v.frontier {
		v.broken = true
		return nil
	}
	if end > v.frontier {
		_, _ = v.w.Write(p[v.frontier-off : n])
		v.frontier = end
	}
	if v.frontier >= v.ByteSource.Size() {
		if err := v.finish(); err != nil {
			v.failed = err
			return err
		}
		v.verified = true
	}
	return nil
}

func (v *verifyingByteSource) ReadAt(p []byte, off int64) (int, error) {
	n, err := v.ByteSource.ReadAt(p, off)
	if aerr := v.account(p, off, n); aerr != nil {
		return 0, aerr
	}
	return n, err
}

func (v *verifyingByteSource) ReadAtContext(ctx context.Context, p []byte, off int64) (int, error) {
	var n int
	var err error
	if cr, ok := v.ByteSource.(core.ContextReaderAt); ok {
		n, err = cr.ReadAtContext(ctx, p, off)
	} else {
		n, err = v.ByteSource.ReadAt(p, off)
	}
	if aerr := v.account(p, off, n); aerr != nil {
		return 0, aerr
	}
	return n, err
}

// applyDigests enforces a source's declared size and digests on an opened
// ByteSource. When the bytes fit the download limit they are read into the
// cache (or memory) and verified before anything is served; larger sources
// stream behind a verifyingByteSource. cacheKey seeds the cache entry.
func applyDigests(ctx context.Context, bs ByteSource, d expectedDigests, maxBytes int64, cacheKey string, deps Deps) (ByteSource, error) {
	if err := d.checkSize(bs.Size()); err != nil {
		_ = bs.Close()
		return nil, err
	}
	if !d.hasHash() {
		return bs, nil
	}
	limit := effectiveDownloadMaxBytes(maxBytes, deps)
	if limit > 0 && bs.Size() > limit {
		return newVerifyingByteSource(bs, d), nil
	}
	name, mime := bs.Name(), bs.MIME()
	sr := io.NewSectionReader(bs, 0, bs.Size())
	if deps.Cache != nil {
		ref, err := deps.Cache.SingleFlightPut(ctx, cacheKey, func(ctx context.Context) (blobcache.Ref, error) {
			if _, err := sr.Seek(0, io.SeekStart); err != nil {
				return "", err
			}
			ref, err := deps.Cache.PutWithExpectedSHA256(ctx, cacheKey, newDigestVerifyReader(sr, d), bs.Size(), d.sha256, mime)
			if err != nil {
				if errors.Is(err, blobcache.ErrTooLarge) {
					return "", fmt.Errorf("%w: %v", ErrSizeLimitExceeded, err)
				}
				return "", err
			}
			return ref, nil
		})
		if err != nil {
			_ = bs.Close()
			return nil, err
		}
		cached, err := deps.Cache.Open(ref)
		_ = bs.Close()
		if err != nil {
			return nil, err
		}
		return &renamedByteSource{ByteSource: cached, name: name, mime: mime}, nil
	}
	data, err := io.ReadAll(sr)
	_ = bs.Close()
	if err != nil {
		return nil, err
	}
	if err := d.verifyBytes(data); err != nil {
		return nil, err
	}
	return &bytesByteSource{r: bytes.NewReader(data), size: int64(len(data)), name: name, mime: mime}, nil
}
