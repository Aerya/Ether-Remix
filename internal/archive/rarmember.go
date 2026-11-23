package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/nwaples/rardecode/v2"
)

const rarDefaultMaxDictBytes = 128 << 20

// OpenRARMember opens a sequential reader for the first non-directory RAR4 or
// RAR5 member accepted by match across single or multi-volume archives.
// Both non-solid and solid RAR members are decoded sequentially via a virtual
// fs.FS over vols; encrypted and unsupported entries return ErrEncrypted or
// ErrUnsupported.
func OpenRARMember(ctx context.Context, vols []Volume, match func(nonDirIndex int, m Member) bool, maxDictBytes, maxExpandedBytes int64) (io.ReadCloser, Member, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(vols) == 0 {
		return nil, Member{}, fmt.Errorf("archive: rar has no volumes: %w", ErrCorrupt)
	}
	for i, v := range vols {
		if v.R == nil || v.Size < 0 {
			return nil, Member{}, fmt.Errorf("archive: rar volume %d invalid: %w", i, ErrCorrupt)
		}
	}
	if maxDictBytes <= 0 {
		maxDictBytes = rarDefaultMaxDictBytes
	}

	vfs := newRARVolumeFS(ctx, vols)
	firstName := rarVirtualVolumeName(0)
	rc, err := rardecode.OpenReader(
		firstName,
		rardecode.FileSystem(vfs),
		rardecode.MaxDictionarySize(maxDictBytes),
		rardecode.BufferSize(32<<10),
	)
	if err != nil {
		return nil, Member{}, mapRARDecodeErr("open", err)
	}

	nonDirIdx := 0
	var solidRunBytes int64
	for {
		if err := ctx.Err(); err != nil {
			_ = rc.Close()
			return nil, Member{}, err
		}
		hdr, err := rc.Next()
		if err == io.EOF {
			_ = rc.Close()
			return nil, Member{}, fmt.Errorf("archive: rar member not found: %w", ErrUnsupported)
		}
		if err != nil {
			_ = rc.Close()
			return nil, Member{}, mapRARDecodeErr("header", err)
		}

		name := strings.ReplaceAll(hdr.Name, `\`, "/")
		stored := !hdr.Solid && !hdr.Encrypted && !hdr.UnKnownSize && hdr.PackedSize == hdr.UnPackedSize
		method := "rar"
		if stored {
			method = "store"
		}
		m := Member{
			Name:      name,
			Size:      hdr.UnPackedSize,
			Stored:    stored,
			Solid:     hdr.Solid,
			Encrypted: hdr.Encrypted || hdr.HeaderEncrypted,
			Dir:       hdr.IsDir,
			Method:    method,
		}
		if hdr.IsDir {
			continue
		}

		if hdr.Solid {
			if !hdr.UnKnownSize && hdr.UnPackedSize > 0 {
				solidRunBytes += hdr.UnPackedSize
			}
		} else if !hdr.UnKnownSize && hdr.UnPackedSize >= 0 {
			solidRunBytes = hdr.UnPackedSize
		}

		curIdx := nonDirIdx
		nonDirIdx++
		if match == nil || !match(curIdx, m) {
			if maxExpandedBytes > 0 && hdr.Solid && solidRunBytes > maxExpandedBytes {
				_ = rc.Close()
				return nil, Member{}, fmt.Errorf("archive: rar solid run %d bytes exceeds limit %d: %w", solidRunBytes, maxExpandedBytes, ErrUnsupported)
			}
			continue
		}

		if m.Encrypted {
			_ = rc.Close()
			return nil, m, fmt.Errorf("archive: rar member %q: %w", m.Name, ErrEncrypted)
		}
		if hdr.LinkType != rardecode.LinkTypeNone || (hdr.Mode()&fs.ModeType) != 0 {
			_ = rc.Close()
			return nil, m, fmt.Errorf("archive: non-regular rar member %q: %w", m.Name, ErrUnsupported)
		}
		if hdr.UnKnownSize || hdr.UnPackedSize < 0 {
			_ = rc.Close()
			return nil, m, fmt.Errorf("archive: rar member %q has unknown unpacked size: %w", m.Name, ErrUnsupported)
		}
		if maxExpandedBytes > 0 && (hdr.UnPackedSize > maxExpandedBytes || solidRunBytes > maxExpandedBytes) {
			_ = rc.Close()
			return nil, m, fmt.Errorf("archive: rar member %q expanded size exceeds limit %d: %w", m.Name, maxExpandedBytes, ErrUnsupported)
		}

		return &rarMemberReadCloser{
			ctx:  ctx,
			name: m.Name,
			rc:   rc,
		}, m, nil
	}
}

type rarMemberReadCloser struct {
	ctx  context.Context
	name string
	rc   *rardecode.ReadCloser
}

func (r *rarMemberReadCloser) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.rc.Read(p)
	if err == nil || err == io.EOF {
		return n, err
	}
	return n, mapRARDecodeErr(r.name, err)
}

func (r *rarMemberReadCloser) Close() error {
	return r.rc.Close()
}

func mapRARDecodeErr(where string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch {
	case errors.Is(err, rardecode.ErrArchiveEncrypted),
		errors.Is(err, rardecode.ErrArchivedFileEncrypted):
		return fmt.Errorf("archive: rar %s: %v: %w", where, err, ErrEncrypted)
	case errors.Is(err, rardecode.ErrUnknownVersion),
		errors.Is(err, rardecode.ErrUnsupportedDecoder),
		errors.Is(err, rardecode.ErrUnknownDecoder),
		errors.Is(err, rardecode.ErrMultipleDecoders),
		errors.Is(err, rardecode.ErrDictionaryTooLarge),
		errors.Is(err, rardecode.ErrPlatformIntSize),
		errors.Is(err, rardecode.ErrSolidOpen):
		return fmt.Errorf("archive: rar %s: %v: %w", where, err, ErrUnsupported)
	case errors.Is(err, rardecode.ErrCorruptBlockHeader),
		errors.Is(err, rardecode.ErrCorruptFileHeader),
		errors.Is(err, rardecode.ErrBadHeaderCRC),
		errors.Is(err, rardecode.ErrBadFileChecksum),
		errors.Is(err, rardecode.ErrShortFile),
		errors.Is(err, rardecode.ErrInvalidFileBlock),
		errors.Is(err, rardecode.ErrUnexpectedArcEnd),
		errors.Is(err, rardecode.ErrDecoderOutOfData),
		errors.Is(err, rardecode.ErrMultiVolume),
		errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("archive: rar %s: %v: %w", where, err, ErrCorrupt)
	default:
		if strings.HasPrefix(err.Error(), "rardecode:") {
			return fmt.Errorf("archive: rar %s: %v: %w", where, err, ErrCorrupt)
		}
		return err
	}
}

func rarVirtualVolumeName(i int) string {
	return fmt.Sprintf("vol.part%05d.rar", i+1)
}

type rarVolumeFS struct {
	ctx   context.Context
	vols  []Volume
	index map[string]int
}

func newRARVolumeFS(ctx context.Context, vols []Volume) *rarVolumeFS {
	idx := make(map[string]int, len(vols)*2)
	for i := range vols {
		idx[rarVirtualVolumeName(i)] = i
		if i > 0 {
			// Old-style RAR4 multi-volume sets (without NEWNUMBERING) derive
			// continuation names as <base>.r00, .r01, ..., .r99, .s00, ...
			letter := byte('r' + (i-1)/100)
			idx[fmt.Sprintf("vol.part00001.%c%02d", letter, (i-1)%100)] = i
		}
	}
	return &rarVolumeFS{
		ctx:   ctx,
		vols:  vols,
		index: idx,
	}
}

func (vfs *rarVolumeFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	i, ok := vfs.index[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	v := vfs.vols[i]
	sr := io.NewSectionReader(rarCtxReaderAt{ctx: vfs.ctx, r: v.R}, 0, v.Size)
	return &rarVolumeFile{
		SectionReader: sr,
		name:          name,
		size:          v.Size,
	}, nil
}

type rarCtxReaderAt struct {
	ctx context.Context
	r   io.ReaderAt
}

func (c rarCtxReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.ReadAt(p, off)
}

type rarVolumeFile struct {
	*io.SectionReader
	name string
	size int64
}

func (f *rarVolumeFile) Stat() (fs.FileInfo, error) {
	return rarVolumeInfo{name: f.name, size: f.size}, nil
}

func (f *rarVolumeFile) Close() error {
	return nil
}

type rarVolumeInfo struct {
	name string
	size int64
}

func (fi rarVolumeInfo) Name() string       { return fi.name }
func (fi rarVolumeInfo) Size() int64        { return fi.size }
func (fi rarVolumeInfo) Mode() fs.FileMode  { return 0444 }
func (fi rarVolumeInfo) ModTime() time.Time { return time.Time{} }
func (fi rarVolumeInfo) IsDir() bool        { return false }
func (fi rarVolumeInfo) Sys() any           { return nil }
