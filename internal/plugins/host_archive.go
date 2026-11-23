package plugins

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"path"
	"strings"
	"sync"
	"unsafe"

	container "github.com/braedonsaunders/ether/internal/archive"
	"github.com/nwaples/rardecode/v2"
)

// archiveIOBudget caps all metadata and compressed input reads cumulatively,
// including decoder read-ahead; seekReaderAt propagates the call deadline.
type archiveIOBudget struct {
	r    io.ReaderAt
	left int64
}

func (r *archiveIOBudget) ReadAt(p []byte, off int64) (int, error) {
	if int64(len(p)) > r.left {
		return 0, fmt.Errorf("plugins: archive input exceeds byte grant")
	}
	n, err := r.r.ReadAt(p, off)
	r.left -= int64(n)
	return n, err
}

type dataArchive struct {
	members []ArchiveMember
	open    func(int) (io.ReadCloser, error)
}

// archiveFormatEntryBytes bounds the format's own per-member index entry
// (offsets, sizes, checksums, flags) kept beside ArchiveMember.
const archiveFormatEntryBytes = 128

// archiveDirectoryCap scales with the grant like the other archive limits
// and matches the data parser's directory cap (host_data.go).
func archiveDirectoryCap(l RuntimeLimits) int64 {
	return min(int64(maxArchiveDirectory), int64(l.ArchiveMembers)*65536, archiveMemory(l)/8)
}

func archiveMemory(l RuntimeLimits) int64 {
	if l.MemoryBytes == 0 {
		return 32 << 20
	}
	return int64(min(l.MemoryBytes, 2<<30))
}

// UnsafeArchiveMemberError describes an archive member whose stored name is
// unsafe to use as a filesystem path. By-index archive reads no longer refuse
// members for their name; Name is an identifier only.
type UnsafeArchiveMemberError struct {
	Index  int
	Name   string
	Reason string
}

func (e *UnsafeArchiveMemberError) Error() string {
	return fmt.Sprintf("plugins: unsafe archive member %d %q: %s", e.Index, e.Name, e.Reason)
}

func archiveNameReason(name string) string {
	name = strings.TrimSuffix(name, "/")
	switch {
	case name == "":
		return "empty name"
	case len(name) > 1024:
		return "name exceeds 1024 bytes"
	case strings.ContainsRune(name, '\x00'):
		return "name contains NUL"
	case strings.ContainsRune(name, '\\'):
		return "name contains a backslash"
	case strings.HasPrefix(name, "/") || (len(name) >= 3 && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) && name[1:3] == ":/"):
		return "absolute path"
	case path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../"):
		return "name contains traversal or noncanonical path segments"
	}
	// Members are streamed by index; a colon is never used in a disk path.
	return ""
}

// dataArchiveDirectory retains metadata only, never a reader, budget or context.
type dataArchiveDirectory struct {
	members []ArchiveMember
	bind    func(context.Context, *archiveIOBudget, RuntimeLimits) func(int) (io.ReadCloser, error)
	bytes   int64
}

func openDataArchive(ctx context.Context, rc io.ReadSeeker, l RuntimeLimits) (*dataArchive, error) {
	ra := &archiveIOBudget{r: seekReaderAt{ctx, rc}, left: l.FetchBytes}
	d, err := parseDataArchive(ctx, rc, ra, l)
	if err != nil {
		return nil, err
	}
	return &dataArchive{members: d.members, open: d.bind(ctx, ra, l)}, nil
}

func parseDataArchive(ctx context.Context, rc io.ReadSeeker, ra *archiveIOBudget, l RuntimeLimits) (*dataArchiveDirectory, error) {
	size, err := rc.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if size < 8 || size > l.FetchBytes {
		return nil, fmt.Errorf("plugins: archive size outside grant")
	}
	memory := archiveMemory(l)
	if memory < 1<<20 {
		return nil, fmt.Errorf("plugins: archive memory grant too small")
	}
	var sig [8]byte
	if _, err = ra.ReadAt(sig[:], 0); err != nil {
		return nil, err
	}
	format := container.Detect(sig[:])
	a := &dataArchiveDirectory{members: make([]ArchiveMember, 0)}
	switch format {
	case container.Format7z:
		files, e := container.List7zData(ctx, ra, size, container.Data7zLimits{MemoryBytes: memory, ExpandedBytes: l.ArchiveExpandedBytes, Members: l.ArchiveMembers})
		if e != nil {
			return nil, e
		}
		for i, f := range files {
			a.members = append(a.members, ArchiveMember{Index: i, Name: f.Name, Size: uint64(f.Size)})
		}
		a.bind = func(ctx context.Context, ra *archiveIOBudget, l RuntimeLimits) func(int) (io.ReadCloser, error) {
			return func(i int) (io.ReadCloser, error) {
				r, e := container.Open7zData(ctx, ra, files[i])
				if e != nil {
					return nil, e
				}
				return io.NopCloser(r), nil
			}
		}
	case container.FormatRAR4, container.FormatRAR5:
		// Header-only walk: listing a solid RAR never inflates earlier members.
		metadata := &archiveIOBudget{r: ra, left: min(int64(maxArchiveDirectory), memory/4)}
		v, e := container.ReadRARVolume(metadata, size, &container.RAROptions{Context: ctx, MaxHeaderBytes: memory / 16, MaxFiles: min(l.ArchiveMembers+1, int(memory/4096))})
		if e != nil {
			return nil, e
		}
		if v.MultiVolume || v.MoreVolumes || len(v.Files) > l.ArchiveMembers || len(v.Files) >= int(memory/4096) {
			return nil, fmt.Errorf("plugins: split RAR or directory exceeds grant")
		}
		var solidPacked, solidExpanded int64
		for i, f := range v.Files {
			if f.SplitBefore || f.SplitAfter || f.Encrypted || f.NonRegular || f.UnpackedSize < 0 {
				return nil, fmt.Errorf("plugins: split, encrypted or unsupported RAR member")
			}
			if !f.Solid {
				solidPacked, solidExpanded = 0, 0
			}
			solidPacked += f.PackedSize
			if f.UnpackedSize > l.ArchiveExpandedBytes-solidExpanded {
				return nil, fmt.Errorf("plugins: RAR solid block exceeds byte grant")
			}
			solidExpanded += f.UnpackedSize
			if solidExpanded > 0 && (solidPacked == 0 || (solidPacked <= 1<<53 && solidExpanded > solidPacked*maxArchiveRatio)) {
				return nil, fmt.Errorf("plugins: RAR member exceeds compression-ratio limit")
			}
			a.members = append(a.members, ArchiveMember{Index: i, Name: f.StoredName, Size: uint64(f.UnpackedSize)})
		}
		a.bind = func(ctx context.Context, ra *archiveIOBudget, l RuntimeLimits) func(int) (io.ReadCloser, error) {
			return func(i int) (io.ReadCloser, error) {
				if v.Files[i].Dir {
					return nil, fmt.Errorf("plugins: cannot open an archive directory")
				}
				r, e := rardecode.NewReader(io.NewSectionReader(ra, 0, size), rardecode.MaxDictionarySize(memory/8), rardecode.BufferSize(4096))
				if e != nil {
					return nil, e
				}
				// Explicitly consume dependencies under the expanded-byte grant. This also
				// keeps implicit solid skipping inside the decoder from doing uncounted work.
				left := l.ArchiveExpandedBytes
				start := i
				for start > 0 && v.Files[start].Solid {
					start--
				}
				for j := 0; j <= i; j++ {
					h, e := r.Next()
					if e != nil {
						return nil, e
					}
					normName := strings.ReplaceAll(h.Name, `\`, "/")
					if (normName != v.Files[j].Name && normName != strings.ReplaceAll(v.Files[j].StoredName, `\`, "/")) || h.UnPackedSize != v.Files[j].UnpackedSize || h.Encrypted || h.HeaderEncrypted {
						return nil, fmt.Errorf("plugins: inconsistent RAR member")
					}
					if j == i {
						return io.NopCloser(io.LimitReader(r, h.UnPackedSize)), nil
					}
					if j >= start || v.Solid {
						if h.UnPackedSize > left {
							return nil, fmt.Errorf("plugins: RAR decode exceeds byte grant")
						}
						if _, e = io.CopyN(io.Discard, &boundedDataReader{ctx: ctx, r: r, left: left}, h.UnPackedSize); e != nil {
							return nil, e
						}
						left -= h.UnPackedSize
					}
				}
				return nil, io.EOF
			}
		}
	default:
		z, e := parseDataZIP(rc, ra, l)
		if e != nil {
			return nil, e
		}
		files := make([]dataZIPMember, len(z.File))
		for i, f := range z.File {
			offset, e := f.DataOffset()
			if e != nil {
				return nil, e
			}
			if offset < 0 || offset > size || f.CompressedSize64 > uint64(size-offset) {
				return nil, zip.ErrFormat
			}
			// Descriptor metadata is immutable too; validate it once while
			// building the index, rather than retaining a zip.File's reader.
			if f.Flags&8 != 0 {
				at := offset + int64(f.CompressedSize64)
				var descriptor [12]byte
				if _, e := ra.ReadAt(descriptor[:], at); e != nil {
					return nil, e
				}
				crc := binary.LittleEndian.Uint32(descriptor[:])
				if crc == 0x08074b50 {
					var rest [4]byte
					if _, e := ra.ReadAt(rest[:], at+12); e != nil {
						return nil, e
					}
					crc = binary.LittleEndian.Uint32(descriptor[4:])
				}
				if crc != f.CRC32 {
					return nil, zip.ErrChecksum
				}
			}
			files[i] = dataZIPMember{offset: offset, packed: int64(f.CompressedSize64), size: f.UncompressedSize64, crc: f.CRC32, method: f.Method, dir: f.FileInfo().IsDir(), verifyCRC: f.Flags&8 != 0 || f.CRC32 != 0}
			a.members = append(a.members, ArchiveMember{Index: i, Name: f.Name, Size: f.UncompressedSize64})
		}
		a.bind = func(_ context.Context, ra *archiveIOBudget, _ RuntimeLimits) func(int) (io.ReadCloser, error) {
			return func(i int) (io.ReadCloser, error) { return files[i].open(ra) }
		}
	}
	var total uint64
	var names int64
	for i := range a.members {
		m := &a.members[i]
		m.UnsafeReason = archiveNameReason(m.Name)
		m.Unsafe = m.UnsafeReason != ""
		names += int64(len(m.Name) + len(m.UnsafeReason))
		if names > memory/4 || m.Size > uint64(l.ArchiveMemberBytes) || m.Size > uint64(l.ArchiveExpandedBytes)-total {
			return nil, fmt.Errorf("plugins: archive directory or member size exceeds grant")
		}
		total += m.Size
	}
	// Measure what the directory retains: each member's entry (including the
	// slice's spare capacity), a bounded per-member format table, and the name
	// bytes. ZIP comments/extras and raw directory buffers are not retained.
	a.bytes = 1024 + int64(cap(a.members))*(int64(unsafe.Sizeof(ArchiveMember{}))+archiveFormatEntryBytes) + names
	if a.bytes > archiveDirectoryCap(l) {
		return nil, fmt.Errorf("plugins: archive directory exceeds limit")
	}
	return a, nil
}

var (
	emptyFlateSource = bytes.NewReader(nil)
	flateReaderPool  sync.Pool
)

type pooledFlateReader struct {
	zr   io.ReadCloser
	br   *bufio.Reader
	done bool
}

func newPooledFlateReader(src io.Reader) io.ReadCloser {
	if p, ok := flateReaderPool.Get().(*pooledFlateReader); ok {
		p.br.Reset(src)
		if resetter, ok := p.zr.(flate.Resetter); ok && resetter.Reset(p.br, nil) == nil {
			p.done = false
			return p
		}
	}
	br := bufio.NewReaderSize(src, 4096)
	return &pooledFlateReader{zr: flate.NewReader(br), br: br}
}

func (p *pooledFlateReader) Read(b []byte) (int, error) {
	return p.zr.Read(b)
}

func (p *pooledFlateReader) Close() error {
	if p.done {
		return nil
	}
	p.done = true
	err := p.zr.Close()
	p.br.Reset(emptyFlateSource)
	if resetter, ok := p.zr.(flate.Resetter); ok {
		_ = resetter.Reset(emptyFlateSource, nil)
	}
	flateReaderPool.Put(p)
	return err
}

// ZIP member offsets come from the validated immutable central directory. Each
// open creates a decoder and checksum state over this call's reader only.
type dataZIPMember struct {
	offset, packed int64
	size           uint64
	crc            uint32
	method         uint16
	dir            bool
	verifyCRC      bool
}

func (f dataZIPMember) open(ra io.ReaderAt) (io.ReadCloser, error) {
	if f.dir {
		return nil, fmt.Errorf("plugins: cannot open an archive directory")
	}
	section := io.NewSectionReader(ra, f.offset, f.packed)
	var r io.ReadCloser
	if f.method == zip.Deflate {
		r = newPooledFlateReader(section)
	} else {
		r = io.NopCloser(section)
	}
	return &dataZIPChecksum{ReadCloser: r, hash: crc32.NewIEEE(), size: f.size, crc: f.crc, verifyCRC: f.verifyCRC}, nil
}

type dataZIPChecksum struct {
	io.ReadCloser
	hash       hash.Hash32
	read, size uint64
	crc        uint32
	verifyCRC  bool
}

func (r *dataZIPChecksum) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.read += uint64(n)
	r.hash.Write(p[:n])
	if r.read > r.size {
		return n, zip.ErrFormat
	}
	if err == io.EOF {
		if r.read != r.size {
			return n, io.ErrUnexpectedEOF
		}
		if r.verifyCRC && r.hash.Sum32() != r.crc {
			return n, zip.ErrChecksum
		}
	}
	return n, err
}
