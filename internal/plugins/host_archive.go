package plugins

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

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

func archiveMemory(l RuntimeLimits) int64 {
	if l.MemoryBytes == 0 {
		return 32 << 20
	}
	return int64(min(l.MemoryBytes, 2<<30))
}
func safeArchiveName(name string) bool {
	name = strings.TrimSuffix(name, "/")
	return name != "" && len(name) <= 1024 && !strings.ContainsAny(name, "\\:\x00") && !strings.HasPrefix(name, "/") && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}
func openDataArchive(ctx context.Context, rc io.ReadSeeker, l RuntimeLimits) (*dataArchive, error) {
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
	ra := &archiveIOBudget{r: seekReaderAt{ctx, rc}, left: l.FetchBytes}
	var sig [8]byte
	if _, err = ra.ReadAt(sig[:], 0); err != nil {
		return nil, err
	}
	format := container.Detect(sig[:])
	a := &dataArchive{members: make([]ArchiveMember, 0)}
	switch format {
	case container.Format7z:
		files, e := container.List7zData(ctx, ra, size, container.Data7zLimits{MemoryBytes: memory, ExpandedBytes: l.ArchiveExpandedBytes, Members: l.ArchiveMembers})
		if e != nil {
			return nil, e
		}
		for i, f := range files {
			a.members = append(a.members, ArchiveMember{i, f.Name, uint64(f.Size)})
		}
		a.open = func(i int) (io.ReadCloser, error) {
			r, e := container.Open7zData(ra, files[i])
			if e != nil {
				return nil, e
			}
			return io.NopCloser(r), nil
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
			if f.SplitBefore || f.SplitAfter || f.Encrypted || f.NonRegular || f.UnsafeName || f.UnpackedSize < 0 {
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
			a.members = append(a.members, ArchiveMember{i, f.Name, uint64(f.UnpackedSize)})
		}
		a.open = func(i int) (io.ReadCloser, error) {
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
				if h.Name != v.Files[j].Name || h.UnPackedSize != v.Files[j].UnpackedSize || h.Encrypted || h.HeaderEncrypted {
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
	default:
		z, e := openDataZIP(ctx, rc, l)
		if e != nil {
			return nil, e
		}
		for i, f := range z.File {
			a.members = append(a.members, ArchiveMember{i, f.Name, f.UncompressedSize64})
		}
		a.open = func(i int) (io.ReadCloser, error) {
			if z.File[i].FileInfo().IsDir() {
				return nil, fmt.Errorf("plugins: cannot open an archive directory")
			}
			return z.File[i].Open()
		}
	}
	var total uint64
	var names int64
	for _, m := range a.members {
		names += int64(len(m.Name)) + 256
		if !safeArchiveName(m.Name) || names > memory/4 || m.Size > uint64(l.ArchiveMemberBytes) || m.Size > uint64(l.ArchiveExpandedBytes)-total {
			return nil, fmt.Errorf("plugins: unsafe archive member or size exceeds grant")
		}
		total += m.Size
	}
	return a, nil
}
