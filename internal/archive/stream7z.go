package archive

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"

	"github.com/ulikunitz/xz/lzma"
)

type stream7zPlan struct {
	member   Member
	solid    bool
	stored   bool
	coder    sz7Coder
	start    int64
	packed   int64
	unpacked uint64
	skip     int64
	hasCRC   bool
	wantCRC  uint32
	maxDict  int64
}

// Inspect7zMember locates the matching non-directory member in vols, validates
// that its coder chain can be sequentially decoded within the supplied bounds,
// and reports whether decoding that member requires solid multi-substream
// folder decompression.
func Inspect7zMember(ctx context.Context, vols []Volume, match func(nonDirIndex int, m Member) bool, maxDictBytes, maxExpandedBytes int64) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(vols) == 0 {
		return false, fmt.Errorf("archive: 7z has no volumes: %w", ErrCorrupt)
	}
	mr := NewMultiReader(vols)
	plan, err := plan7zMember(ctx, mr, mr.Size(), match, maxDictBytes, maxExpandedBytes)
	if err != nil {
		return false, err
	}
	return plan.solid, nil
}

// Open7zMember opens a sequential reader for the first non-directory 7z member
// accepted by match. Copy, LZMA, and LZMA2 single-coder folders (including
// solid folders) are decoded sequentially and verified against the archive CRC
// when present. Encrypted and unsupported coder chains return ErrEncrypted or
// ErrUnsupported.
func Open7zMember(ctx context.Context, vols []Volume, match func(nonDirIndex int, m Member) bool, maxDictBytes, maxExpandedBytes int64) (io.ReadCloser, Member, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(vols) == 0 {
		return nil, Member{}, fmt.Errorf("archive: 7z has no volumes: %w", ErrCorrupt)
	}
	mr := NewMultiReader(vols)
	plan, err := plan7zMember(ctx, mr, mr.Size(), match, maxDictBytes, maxExpandedBytes)
	if err != nil {
		var zero Member
		if plan != nil {
			zero = plan.member
		}
		return nil, zero, err
	}
	m := plan.member
	if m.Size == 0 {
		if plan.hasCRC && plan.wantCRC != 0 {
			return nil, m, fmt.Errorf("archive: 7z member %q crc mismatch: %w", m.Name, ErrCorrupt)
		}
		return io.NopCloser(bytes.NewReader(nil)), m, nil
	}

	var raw io.Reader
	if plan.stored {
		raw = data7zContextReader{
			ctx: ctx,
			r:   io.NewSectionReader(mr, plan.start+plan.skip, m.Size),
		}
	} else {
		sec := data7zContextReader{
			ctx: ctx,
			r:   io.NewSectionReader(mr, plan.start, plan.packed),
		}
		dec, err := stream7zCoder(&plan.coder, sec, plan.unpacked, plan.maxDict)
		if err != nil {
			return nil, m, err
		}
		dec = data7zContextReader{ctx: ctx, r: dec}
		if plan.skip > 0 {
			if _, err := io.CopyN(io.Discard, dec, plan.skip); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, m, ctxErr
				}
				return nil, m, fmt.Errorf("archive: 7z member %q solid skip: %v: %w", m.Name, err, ErrCorrupt)
			}
		}
		raw = dec
	}

	return &sz7MemberReader{
		name:     m.Name,
		r:        raw,
		remain:   m.Size,
		crc:      crc32.NewIEEE(),
		checkCRC: plan.hasCRC,
		wantCRC:  plan.wantCRC,
	}, m, nil
}

func plan7zMember(ctx context.Context, r io.ReaderAt, size int64, match func(nonDirIndex int, m Member) bool, maxDictBytes, maxExpandedBytes int64) (*stream7zPlan, error) {
	hdr, err := read7zPlainHeader(ctx, r, size)
	if err != nil {
		return nil, err
	}
	b := sz7Buf{b: hdr}
	id := b.byte()
	if id == sz7ArchiveProperties {
		for b.err == nil {
			if b.byte() == 0 {
				break
			}
			b.skipData()
		}
		id = b.byte()
	}
	if id == sz7AdditionalStreamsInfo {
		return nil, fmt.Errorf("archive: additional 7z streams: %w", ErrUnsupported)
	}
	st := &sz7Streams{}
	var subCRCs [][]uint32
	var subHasCRC [][]bool
	if id == sz7MainStreamsInfo {
		st, subCRCs, subHasCRC = streamsInfoWithCRCs(&b)
		id = b.byte()
	}
	var files []sz7File
	if id == sz7FilesInfo {
		files = b.filesInfo()
		id = b.byte()
	}
	if b.err != nil {
		return nil, b.err
	}
	if id != sz7End {
		return nil, fmt.Errorf("archive: 7z header: unexpected property %#x: %w", id, ErrCorrupt)
	}

	packStart := make([]uint64, len(st.folders))
	firstPack := make([]int, len(st.folders))
	pi := 0
	pos := uint64(sz7SigHeaderLen) + st.packPos
	if pos < st.packPos {
		return nil, fmt.Errorf("archive: 7z pack position overflows: %w", ErrCorrupt)
	}
	for i := range st.folders {
		packStart[i] = pos
		firstPack[i] = pi
		for j := 0; j < st.folders[i].numPacked; j++ {
			if pi >= len(st.packSizes) {
				return nil, fmt.Errorf("archive: 7z folder %d has no pack stream: %w", i, ErrCorrupt)
			}
			pos += st.packSizes[pi]
			if pos < st.packSizes[pi] {
				return nil, fmt.Errorf("archive: 7z pack sizes overflow: %w", ErrCorrupt)
			}
			pi++
		}
	}

	fi, si := 0, 0
	var inFolder uint64
	nonDirIdx := 0
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if f.emptyStream {
			if f.anti {
				continue
			}
			dir := !f.emptyFile || (f.hasAttr && f.attr&attrDirectory != 0)
			if dir {
				continue
			}
			m := Member{Name: f.name, Stored: true, Method: "empty"}
			curIdx := nonDirIdx
			nonDirIdx++
			if match == nil || !match(curIdx, m) {
				continue
			}
			if isIrregular7zAttr(f) {
				return &stream7zPlan{member: m}, fmt.Errorf("archive: non-regular 7z member %q: %w", f.name, ErrUnsupported)
			}
			return &stream7zPlan{member: m, stored: true}, nil
		}

		for fi < len(st.folders) && si >= len(st.subSizes[fi]) {
			fi, si, inFolder = fi+1, 0, 0
		}
		if fi >= len(st.folders) {
			return nil, fmt.Errorf("archive: 7z file %q has no stream: %w", f.name, ErrCorrupt)
		}
		fo := &st.folders[fi]
		sz := st.subSizes[fi][si]
		if sz > 1<<62 {
			return nil, fmt.Errorf("archive: 7z file %q size out of range: %w", f.name, ErrCorrupt)
		}
		isDir := f.hasAttr && f.attr&attrDirectory != 0
		m := Member{
			Name:      f.name,
			Size:      int64(sz),
			Stored:    fo.stored(),
			Encrypted: fo.hasMethod(sz7MethodAES),
			Dir:       isDir,
			Method:    fo.method(),
		}
		curSi := si
		curSkip := inFolder
		inFolder += sz
		si++
		if isDir {
			continue
		}
		curIdx := nonDirIdx
		nonDirIdx++
		if match == nil || !match(curIdx, m) {
			continue
		}

		plan := &stream7zPlan{
			member: m,
			solid:  !fo.stored() && len(st.subSizes[fi]) > 1,
		}
		if m.Encrypted {
			return plan, fmt.Errorf("archive: 7z member %q: %w", m.Name, ErrEncrypted)
		}
		if isIrregular7zAttr(f) {
			return plan, fmt.Errorf("archive: non-regular 7z member %q: %w", m.Name, ErrUnsupported)
		}
		if fo.numPacked != 1 || len(fo.coders) != 1 {
			return plan, fmt.Errorf("archive: 7z member %q coder chain %s: %w", m.Name, m.Method, ErrUnsupported)
		}
		packed := st.packSizes[firstPack[fi]]
		unpacked := fo.unpackSize()
		if packStart[fi] > uint64(size) || packed > uint64(size)-packStart[fi] || curSkip > unpacked || sz > unpacked-curSkip {
			return plan, fmt.Errorf("archive: 7z member %q extent out of bounds: %w", m.Name, ErrCorrupt)
		}
		if fo.stored() && packed != unpacked {
			return plan, fmt.Errorf("archive: 7z copy folder %d size mismatch: %w", fi, ErrCorrupt)
		}
		needExpanded := curSkip + sz
		if maxExpandedBytes > 0 && needExpanded > uint64(maxExpandedBytes) {
			return plan, fmt.Errorf("archive: 7z member %q expanded size %d exceeds limit %d: %w", m.Name, needExpanded, maxExpandedBytes, ErrUnsupported)
		}
		if !fo.stored() {
			if unpacked > 0 && packed == 0 {
				return plan, fmt.Errorf("archive: 7z member %q empty pack stream: %w", m.Name, ErrCorrupt)
			}
			if _, err := stream7zCoder(&fo.coders[0], nil, unpacked, maxDictBytes); err != nil {
				return plan, err
			}
		}

		var hasCRC bool
		var wantCRC uint32
		if fi < len(subHasCRC) && curSi < len(subHasCRC[fi]) && subHasCRC[fi][curSi] {
			hasCRC = true
			wantCRC = subCRCs[fi][curSi]
		} else if len(st.subSizes[fi]) == 1 && fo.hasCRC {
			hasCRC = true
			wantCRC = fo.crc
		}

		plan.stored = fo.stored()
		plan.coder = fo.coders[0]
		plan.start = int64(packStart[fi])
		plan.packed = int64(packed)
		plan.unpacked = unpacked
		plan.skip = int64(curSkip)
		plan.hasCRC = hasCRC
		plan.wantCRC = wantCRC
		plan.maxDict = maxDictBytes
		return plan, nil
	}

	return nil, fmt.Errorf("archive: 7z member not found: %w", ErrUnsupported)
}

func isIrregular7zAttr(f sz7File) bool {
	if f.anti {
		return true
	}
	if !f.hasAttr {
		return false
	}
	// Reject Windows reparse points and Unix special/symlink modes.
	if f.attr&0x400 != 0 {
		return true
	}
	modeType := (f.attr >> 16) & 0xf000
	return modeType != 0 && modeType != 0x8000 && modeType != 0x4000
}

func read7zPlainHeader(ctx context.Context, r io.ReaderAt, size int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var sh [sz7SigHeaderLen]byte
	if size < int64(len(sig7z)) {
		return nil, fmt.Errorf("archive: 7z: %w", ErrNotArchive)
	}
	if size < sz7SigHeaderLen {
		if err := readAt(r, sh[:len(sig7z)], 0, size); err != nil {
			return nil, err
		}
		if Detect(sh[:]) != Format7z {
			return nil, fmt.Errorf("archive: 7z: %w", ErrNotArchive)
		}
		return nil, fmt.Errorf("archive: 7z signature header truncated: %w", ErrCorrupt)
	}
	if err := readAt(r, sh[:], 0, size); err != nil {
		return nil, err
	}
	if Detect(sh[:]) != Format7z {
		return nil, fmt.Errorf("archive: 7z: %w", ErrNotArchive)
	}
	if sh[6] != 0 {
		return nil, fmt.Errorf("archive: 7z major version %d: %w", sh[6], ErrUnsupported)
	}
	if got, want := crc32.ChecksumIEEE(sh[12:]), binary.LittleEndian.Uint32(sh[8:]); got != want {
		return nil, fmt.Errorf("archive: 7z start header crc %#08x, want %#08x: %w", got, want, ErrCorrupt)
	}
	nextOff := binary.LittleEndian.Uint64(sh[12:])
	nextSize := binary.LittleEndian.Uint64(sh[20:])
	nextCRC := binary.LittleEndian.Uint32(sh[28:])
	if nextSize == 0 {
		return []byte{sz7End}, nil
	}
	crypto := &sz7Crypto{ctx: ctx}
	avail := uint64(size - sz7SigHeaderLen)
	if nextSize > crypto.headerLimit() || nextOff > avail || nextSize > avail-nextOff {
		return nil, fmt.Errorf("archive: 7z header (%d bytes at %d) outside archive of %d bytes: %w",
			nextSize, nextOff, size, ErrCorrupt)
	}
	hdr := make([]byte, nextSize)
	if err := readAt(r, hdr, int64(sz7SigHeaderLen+nextOff), size); err != nil {
		return nil, err
	}
	if got := crc32.ChecksumIEEE(hdr); got != nextCRC {
		return nil, fmt.Errorf("archive: 7z header crc %#08x, want %#08x: %w", got, nextCRC, ErrCorrupt)
	}
	for depth := 0; ; depth++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(hdr) == 0 {
			return nil, fmt.Errorf("archive: 7z empty header: %w", ErrCorrupt)
		}
		switch hdr[0] {
		case sz7Header:
			return hdr[1:], nil
		case sz7EncodedHeader:
			if depth >= sz7MaxNesting {
				return nil, fmt.Errorf("archive: 7z encoded headers nested too deep: %w", ErrCorrupt)
			}
			b := sz7Buf{b: hdr[1:]}
			st := b.streamsInfo()
			if b.err != nil {
				return nil, b.err
			}
			var err error
			if hdr, err = decode7zHeader(crypto, r, size, st); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("archive: 7z header starts with %#x: %w", hdr[0], ErrCorrupt)
		}
	}
}

func streamsInfoWithCRCs(s *sz7Buf) (*sz7Streams, [][]uint32, [][]bool) {
	st := &sz7Streams{}
	var subCRCs [][]uint32
	var subHasCRC [][]bool
	for s.err == nil {
		switch id := s.byte(); id {
		case sz7End:
			if st.subSizes == nil {
				st.subSizes = make([][]uint64, len(st.folders))
				for i := range st.folders {
					st.subSizes[i] = []uint64{st.folders[i].unpackSize()}
				}
			} else if len(st.subSizes) != len(st.folders) {
				s.fail("substreams for %d folders, %d folders defined", len(st.subSizes), len(st.folders))
			}
			return st, subCRCs, subHasCRC
		case sz7PackInfo:
			s.packInfo(st)
		case sz7UnpackInfo:
			s.unpackInfo(st)
		case sz7SubStreamsInfo:
			subCRCs, subHasCRC = subStreamsInfoWithCRCs(s, st)
		default:
			s.fail("unexpected property %#x in StreamsInfo", id)
		}
	}
	return st, nil, nil
}

func subStreamsInfoWithCRCs(s *sz7Buf, st *sz7Streams) ([][]uint32, [][]bool) {
	nf := len(st.folders)
	numSub := make([]int, nf)
	for i := range numSub {
		numSub[i] = 1
	}
	var id byte
	for s.err == nil {
		id = s.byte()
		if id == sz7NumUnpackStream {
			for i := range numSub {
				numSub[i] = s.count(1<<24, "substream")
			}
			continue
		}
		if id == sz7CRC || id == sz7Size || id == sz7End {
			break
		}
		s.skipData()
	}
	st.subSizes = make([][]uint64, nf)
	for i := 0; i < nf && s.err == nil; i++ {
		if numSub[i] == 0 {
			continue
		}
		if numSub[i] > 1 && id != sz7Size {
			s.fail("folder %d has %d substreams but no sizes", i, numSub[i])
			return nil, nil
		}
		if uint64(numSub[i]-1) > uint64(len(s.b)) {
			s.fail("substream count %d out of range", numSub[i])
			return nil, nil
		}
		sizes := make([]uint64, 0, numSub[i])
		var sum uint64
		for j := 1; j < numSub[i]; j++ {
			v := s.number()
			sum += v
			if sum < v {
				s.fail("substream sizes overflow")
				return nil, nil
			}
			sizes = append(sizes, v)
		}
		total := st.folders[i].unpackSize()
		if sum > total {
			s.fail("substreams exceed folder %d size", i)
			return nil, nil
		}
		st.subSizes[i] = append(sizes, total-sum)
	}
	if id == sz7Size {
		id = s.byte()
	}
	var subCRCs [][]uint32
	var subHasCRC [][]bool
	for s.err == nil {
		switch id {
		case sz7End:
			return subCRCs, subHasCRC
		case sz7CRC:
			n := 0
			for i, f := range st.folders {
				if !(numSub[i] == 1 && f.hasCRC) {
					n += numSub[i]
				}
			}
			if uint64(n) > uint64(len(s.b))*8+8 {
				s.fail("digest count %d out of range", n)
				return nil, nil
			}
			crcs, def := s.digests(n)
			if s.err != nil {
				return nil, nil
			}
			subCRCs = make([][]uint32, nf)
			subHasCRC = make([][]bool, nf)
			k := 0
			for i, f := range st.folders {
				if numSub[i] == 1 && f.hasCRC {
					subCRCs[i] = []uint32{f.crc}
					subHasCRC[i] = []bool{true}
					continue
				}
				subCRCs[i] = make([]uint32, numSub[i])
				subHasCRC[i] = make([]bool, numSub[i])
				for j := 0; j < numSub[i]; j++ {
					subCRCs[i][j] = crcs[k]
					subHasCRC[i][j] = def[k]
					k++
				}
			}
		default:
			s.skipData()
		}
		id = s.byte()
	}
	return nil, nil
}

func stream7zCoder(c *sz7Coder, source io.Reader, unpack uint64, maxDictBytes int64) (io.Reader, error) {
	if c.numIn != 1 || c.numOut != 1 {
		return nil, fmt.Errorf("archive: unsupported 7z coder arity: %w", ErrUnsupported)
	}
	var dict uint64
	switch {
	case bytes.Equal(c.id, sz7MethodCopy):
		return source, nil
	case bytes.Equal(c.id, sz7MethodLZMA):
		if len(c.props) != 5 {
			return nil, fmt.Errorf("archive: 7z lzma properties: %w", ErrCorrupt)
		}
		props, err := lzma.PropertiesForCode(c.props[0])
		if err != nil {
			return nil, fmt.Errorf("archive: 7z lzma properties: %v: %w", err, ErrCorrupt)
		}
		if maxDictBytes > 0 && (int64(0x300)<<uint(props.LC+props.LP))*2 > maxDictBytes/2 {
			return nil, fmt.Errorf("archive: LZMA model exceeds memory limit: %w", ErrUnsupported)
		}
		dict = uint64(binary.LittleEndian.Uint32(c.props[1:]))
	case bytes.Equal(c.id, sz7MethodLZMA2):
		if len(c.props) != 1 || c.props[0] > 40 {
			return nil, fmt.Errorf("archive: 7z lzma2 properties: %w", ErrCorrupt)
		}
		if c.props[0] == 40 {
			dict = 0xffffffff
		} else {
			dict = uint64(2|int(c.props[0]&1)) << (uint(c.props[0])/2 + 11)
		}
	default:
		return nil, fmt.Errorf("archive: unsupported 7z coder %s: %w", c.name(), ErrUnsupported)
	}
	dict = max(dict, uint64(lzma.MinDictCap))
	if maxDictBytes > 0 && dict > uint64(maxDictBytes) {
		return nil, fmt.Errorf("archive: 7z dictionary %d bytes exceeds limit %d: %w", dict, maxDictBytes, ErrUnsupported)
	}
	if source == nil {
		return nil, nil
	}
	if bytes.Equal(c.id, sz7MethodLZMA2) {
		allocDict := min(dict, max(uint64(lzma.MinDictCap), unpack))
		r, err := (lzma.Reader2Config{DictCap: int(allocDict)}).NewReader2(source)
		if err != nil {
			return nil, fmt.Errorf("archive: 7z lzma2 init: %v: %w", err, ErrCorrupt)
		}
		return r, nil
	}
	h := make([]byte, lzma.HeaderLen)
	copy(h, c.props)
	binary.LittleEndian.PutUint64(h[5:], unpack)
	r, err := lzma.NewReader(io.MultiReader(bytes.NewReader(h), source))
	if err != nil {
		return nil, fmt.Errorf("archive: 7z lzma init: %v: %w", err, ErrCorrupt)
	}
	return r, nil
}

type sz7MemberReader struct {
	name     string
	r        io.Reader
	remain   int64
	crc      hash.Hash32
	checkCRC bool
	wantCRC  uint32
	done     bool
}

func (z *sz7MemberReader) Read(p []byte) (int, error) {
	if z.done {
		return 0, io.EOF
	}
	if z.remain <= 0 {
		z.done = true
		if z.checkCRC {
			if got := z.crc.Sum32(); got != z.wantCRC {
				return 0, fmt.Errorf("archive: 7z member %q crc %#08x != %#08x: %w", z.name, got, z.wantCRC, ErrCorrupt)
			}
		}
		return 0, io.EOF
	}
	if int64(len(p)) > z.remain {
		p = p[:z.remain]
	}
	n, err := z.r.Read(p)
	if n > 0 {
		z.remain -= int64(n)
		if z.checkCRC {
			_, _ = z.crc.Write(p[:n])
		}
	}
	if z.remain == 0 && (err == nil || err == io.EOF) {
		z.done = true
		if z.checkCRC {
			if got := z.crc.Sum32(); got != z.wantCRC {
				return n, fmt.Errorf("archive: 7z member %q crc %#08x != %#08x: %w", z.name, got, z.wantCRC, ErrCorrupt)
			}
		}
		return n, io.EOF
	}
	if err == io.EOF && z.remain > 0 {
		return n, fmt.Errorf("archive: 7z member %q truncated (%d bytes remaining): %w", z.name, z.remain, ErrCorrupt)
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrCorrupt) {
		return n, fmt.Errorf("archive: 7z decode %q: %v: %w", z.name, err, ErrCorrupt)
	}
	return n, err
}

func (z *sz7MemberReader) Close() error {
	z.done = true
	return nil
}
