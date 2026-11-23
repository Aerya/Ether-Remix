package archive

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"math"
	"strings"
)

const (
	// ZIPMethodStore and ZIPMethodDeflate are the supported ZIP compression methods.
	ZIPMethodStore   uint16 = 0
	ZIPMethodDeflate uint16 = 8

	zipEOCDLen        = 22
	zip64LocLen       = 20
	zip64EOCDMinLen   = 56
	zipCDHeaderMinLen = 46
	zipLocalHdrMinLen = 30
	zipMaxTailSearch  = 65535 + zipEOCDLen + zip64LocLen
	// ZIPMaxTailSearch is the maximum tail window scanned for a ZIP EOCD record.
	ZIPMaxTailSearch = zipMaxTailSearch
	zipMaxCDSize     = 32 << 20
	zipMaxMembers    = 131072
	zipAttrDirectory = 0x10
)

var (
	sigZIPEOCD      = []byte("PK\x05\x06")
	sigZIP64Locator = []byte("PK\x06\x07")
	sigZIP64EOCD    = []byte("PK\x06\x06")
	sigZIPCD        = []byte("PK\x01\x02")
	sigZIPLocal     = []byte("PK\x03\x04")
)

var zipMethodNames = map[uint16]string{
	0:  "store",
	1:  "shrink",
	6:  "implode",
	8:  "deflate",
	9:  "deflate64",
	12: "bzip2",
	14: "lzma",
	93: "zstd",
	95: "xz",
	98: "ppmd",
	99: "aes",
}

func zipMethodName(m uint16) string {
	if s, ok := zipMethodNames[m]; ok {
		return s
	}
	return fmt.Sprintf("zip:%d", m)
}

// ZIPEntry is one record parsed from a ZIP Central Directory without reading
// each member's Local File Header. Call ResolveZIPEntry to read only the chosen
// member's 30-byte Local File Header and locate its payload offset.
type ZIPEntry struct {
	Member
	CompSize    int64
	CRC32       uint32
	MethodCode  uint16
	Flags       uint16
	LocalOffset int64 // global offset of the 30-byte Local File Header
}

func zipVolumeBounds(vols []Volume) ([]int64, int64, error) {
	if len(vols) == 0 {
		return nil, 0, fmt.Errorf("archive: zip has no volumes: %w", ErrNotArchive)
	}
	starts := make([]int64, len(vols)+1)
	for i, v := range vols {
		if v.Size < 0 {
			return nil, 0, fmt.Errorf("archive: zip volume %d has negative size: %w", i, ErrCorrupt)
		}
		if v.Size > math.MaxInt64-starts[i] {
			return nil, 0, fmt.Errorf("archive: zip volume sizes overflow: %w", ErrCorrupt)
		}
		starts[i+1] = starts[i] + v.Size
	}
	totalSize := starts[len(vols)]
	if totalSize < zipEOCDLen {
		return nil, 0, fmt.Errorf("archive: zip too short (%d bytes): %w", totalSize, ErrNotArchive)
	}
	return starts, totalSize, nil
}

// ReadZIPDirectory reads only the End of Central Directory (including ZIP64
// extensions) and Central Directory of a single or split ZIP archive. Unlike
// ListZIP, it does not probe offset 0 or read per-member Local File Headers,
// keeping remote range reads O(1) even for multi-thousand-file archives.
func ReadZIPDirectory(vols []Volume) ([]ZIPEntry, error) {
	starts, totalSize, err := zipVolumeBounds(vols)
	if err != nil {
		return nil, err
	}
	mr := NewMultiReader(vols)

	tailLen := min(totalSize, int64(zipMaxTailSearch))
	tailOff := totalSize - tailLen
	tail := make([]byte, tailLen)
	if err := readAt(mr, tail, tailOff, totalSize); err != nil {
		return nil, err
	}

	eocdIdx := findZIPEOCD(tail)
	if eocdIdx < 0 {
		return nil, fmt.Errorf("archive: zip EOCD not found: %w", ErrCorrupt)
	}
	eocd := tail[eocdIdx : eocdIdx+zipEOCDLen]
	cdDisk := uint32(binary.LittleEndian.Uint16(eocd[6:8]))
	numRecords := uint64(binary.LittleEndian.Uint16(eocd[10:12]))
	cdSize := uint64(binary.LittleEndian.Uint32(eocd[12:16]))
	cdOffset := uint64(binary.LittleEndian.Uint32(eocd[16:20]))

	// Check for ZIP64 End of Central Directory Locator immediately preceding EOCD.
	if eocdIdx >= zip64LocLen && bytes.Equal(tail[eocdIdx-zip64LocLen:eocdIdx-zip64LocLen+4], sigZIP64Locator) {
		loc := tail[eocdIdx-zip64LocLen : eocdIdx]
		z64Disk := binary.LittleEndian.Uint32(loc[4:8])
		z64Off := binary.LittleEndian.Uint64(loc[8:16])
		var z64GlobalOff int64
		if int(z64Disk) < len(vols) && z64Off <= uint64(vols[z64Disk].Size) {
			z64GlobalOff = starts[z64Disk] + int64(z64Off)
		} else if z64Off <= uint64(totalSize) {
			z64GlobalOff = int64(z64Off)
		} else {
			return nil, fmt.Errorf("archive: zip64 EOCD offset out of range: %w", ErrCorrupt)
		}
		var z64 [zip64EOCDMinLen]byte
		if err := readAt(mr, z64[:], z64GlobalOff, totalSize); err != nil {
			return nil, err
		}
		if !bytes.Equal(z64[:4], sigZIP64EOCD) {
			return nil, fmt.Errorf("archive: zip64 EOCD signature mismatch: %w", ErrCorrupt)
		}
		cdDisk = binary.LittleEndian.Uint32(z64[20:24])
		numRecords = binary.LittleEndian.Uint64(z64[32:40])
		cdSize = binary.LittleEndian.Uint64(z64[40:48])
		cdOffset = binary.LittleEndian.Uint64(z64[48:56])
	}

	if cdSize > zipMaxCDSize {
		return nil, fmt.Errorf("archive: zip central directory %d bytes exceeds %d limit: %w", cdSize, zipMaxCDSize, ErrUnsupported)
	}
	if numRecords > zipMaxMembers {
		return nil, fmt.Errorf("archive: zip member count %d exceeds %d limit: %w", numRecords, zipMaxMembers, ErrUnsupported)
	}

	var cdGlobalOff int64
	if len(vols) > 1 && int(cdDisk) < len(vols) && cdOffset <= uint64(vols[cdDisk].Size) &&
		starts[cdDisk]+int64(cdOffset)+int64(cdSize) <= totalSize {
		// Standard WinZip split .z01/.zip where cdOffset is relative to cdDisk.
		cdGlobalOff = starts[cdDisk] + int64(cdOffset)
	} else if cdOffset <= uint64(totalSize) && int64(cdOffset)+int64(cdSize) <= totalSize {
		// Single or raw-concatenated .zip.001 where cdOffset is global.
		cdGlobalOff = int64(cdOffset)
	} else {
		return nil, fmt.Errorf("archive: zip central directory [%d,+%d) outside archive (%d bytes): %w", cdOffset, cdSize, totalSize, ErrCorrupt)
	}

	cd := make([]byte, cdSize)
	if err := readAt(mr, cd, cdGlobalOff, totalSize); err != nil {
		return nil, err
	}

	entries := make([]ZIPEntry, 0, numRecords)
	pos := 0
	for i := uint64(0); i < numRecords; i++ {
		if len(cd)-pos < zipCDHeaderMinLen || !bytes.Equal(cd[pos:pos+4], sigZIPCD) {
			return nil, fmt.Errorf("archive: zip central directory entry %d corrupt: %w", i, ErrCorrupt)
		}
		hdr := cd[pos : pos+zipCDHeaderMinLen]
		flags := binary.LittleEndian.Uint16(hdr[8:10])
		method := binary.LittleEndian.Uint16(hdr[10:12])
		wantCRC := binary.LittleEndian.Uint32(hdr[16:20])
		compSize := uint64(binary.LittleEndian.Uint32(hdr[20:24]))
		uncompSize := uint64(binary.LittleEndian.Uint32(hdr[24:28]))
		nameLen := int(binary.LittleEndian.Uint16(hdr[28:30]))
		extraLen := int(binary.LittleEndian.Uint16(hdr[30:32]))
		commentLen := int(binary.LittleEndian.Uint16(hdr[32:34]))
		diskStart := uint32(binary.LittleEndian.Uint16(hdr[34:36]))
		extAttrs := binary.LittleEndian.Uint32(hdr[38:42])
		localOff := uint64(binary.LittleEndian.Uint32(hdr[42:46]))

		recLen := zipCDHeaderMinLen + nameLen + extraLen + commentLen
		if recLen > len(cd)-pos {
			return nil, fmt.Errorf("archive: zip central directory entry %d truncated: %w", i, ErrCorrupt)
		}
		nameBytes := cd[pos+zipCDHeaderMinLen : pos+zipCDHeaderMinLen+nameLen]
		extra := cd[pos+zipCDHeaderMinLen+nameLen : pos+zipCDHeaderMinLen+nameLen+extraLen]
		pos += recLen

		if uncompSize == 0xffffffff || compSize == 0xffffffff || localOff == 0xffffffff || diskStart == 0xffff {
			var err error
			uncompSize, compSize, localOff, diskStart, err = parseZIP64Extra(extra, uncompSize, compSize, localOff, diskStart)
			if err != nil {
				return nil, err
			}
		}
		if uncompSize > math.MaxInt64 || compSize > math.MaxInt64 {
			return nil, fmt.Errorf("archive: zip member size overflows int64: %w", ErrCorrupt)
		}

		name := strings.ReplaceAll(string(nameBytes), `\`, "/")
		isDir := strings.HasSuffix(name, "/") || (extAttrs&zipAttrDirectory != 0 && uncompSize == 0)
		encrypted := (flags&0x01) != 0 || method == 99
		stored := method == ZIPMethodStore && !encrypted && compSize == uncompSize

		var localGlobalOff int64
		if !isDir {
			if len(vols) > 1 && int(diskStart) < len(vols) && localOff < uint64(vols[diskStart].Size) {
				localGlobalOff = starts[diskStart] + int64(localOff)
			} else if localOff < uint64(totalSize) {
				localGlobalOff = int64(localOff)
			} else {
				return nil, fmt.Errorf("archive: zip member %q local header offset %d out of range: %w", name, localOff, ErrCorrupt)
			}
		}

		entries = append(entries, ZIPEntry{
			Member: Member{
				Name:      name,
				Size:      int64(uncompSize),
				Stored:    stored,
				Encrypted: encrypted,
				Dir:       isDir,
				Method:    zipMethodName(method),
			},
			CompSize:    int64(compSize),
			CRC32:       wantCRC,
			MethodCode:  method,
			Flags:       flags,
			LocalOffset: localGlobalOff,
		})
	}
	return entries, nil
}

// ResolveZIPEntry reads only entry's 30-byte Local File Header to compute the
// global payload data offset inside vols (and populates Extents on the
// returned Member when entry is stored uncompressed).
func ResolveZIPEntry(vols []Volume, entry ZIPEntry) (Member, int64, error) {
	_, totalSize, err := zipVolumeBounds(vols)
	if err != nil {
		return Member{}, 0, err
	}
	if entry.LocalOffset < 0 || entry.LocalOffset >= totalSize {
		return Member{}, 0, fmt.Errorf("archive: zip member %q local header offset %d out of range: %w", entry.Name, entry.LocalOffset, ErrCorrupt)
	}
	mr := NewMultiReader(vols)
	var lfh [zipLocalHdrMinLen]byte
	if err := readAt(mr, lfh[:], entry.LocalOffset, totalSize); err != nil {
		return Member{}, 0, err
	}
	if !bytes.Equal(lfh[:4], sigZIPLocal) {
		return Member{}, 0, fmt.Errorf("archive: zip member %q missing local header signature: %w", entry.Name, ErrCorrupt)
	}
	lfhFlags := binary.LittleEndian.Uint16(lfh[6:8])
	lfhMethod := binary.LittleEndian.Uint16(lfh[8:10])
	if lfhMethod != entry.MethodCode || ((lfhFlags&0x01) != 0) != ((entry.Flags&0x01) != 0) {
		return Member{}, 0, fmt.Errorf("archive: zip member %q local flags or method disagree with directory: %w", entry.Name, ErrCorrupt)
	}
	lfhNameLen := int64(binary.LittleEndian.Uint16(lfh[26:28]))
	lfhExtraLen := int64(binary.LittleEndian.Uint16(lfh[28:30]))
	dataOff := entry.LocalOffset + zipLocalHdrMinLen + lfhNameLen + lfhExtraLen
	dataSize := entry.CompSize
	if entry.Stored {
		dataSize = entry.Size
	}
	if dataOff < 0 || dataSize < 0 || dataOff > totalSize || dataSize > totalSize-dataOff {
		return Member{}, 0, fmt.Errorf("archive: zip member %q data [%d,+%d) exceeds archive size %d: %w", entry.Name, dataOff, dataSize, totalSize, ErrCorrupt)
	}
	m := entry.Member
	if entry.Stored && entry.Size > 0 {
		m.Extents = []Extent{{Volume: 0, Offset: dataOff, Size: entry.Size}}
	}
	return m, dataOff, nil
}

// OpenZIPDataReader decodes a stored or deflate ZIP member payload stream r
// and verifies uncompressed length and IEEE CRC-32 at EOF.
func OpenZIPDataReader(r io.Reader, name string, method uint16, uncompSize int64, wantCRC uint32) (io.ReadCloser, error) {
	var raw io.ReadCloser
	switch method {
	case ZIPMethodStore:
		raw = io.NopCloser(r)
	case ZIPMethodDeflate:
		raw = flate.NewReader(r)
	default:
		return nil, fmt.Errorf("archive: zip member %q method %s: %w", name, zipMethodName(method), ErrUnsupported)
	}
	return &zipVerifyReader{
		rc:       raw,
		crc:      crc32.NewIEEE(),
		wantCRC:  wantCRC,
		wantSize: uncompSize,
		name:     name,
	}, nil
}

// ListZIP reads the End of Central Directory (including ZIP64 extensions) and
// Central Directory of a single or concatenated/split ZIP archive, then
// inspects each stored member's 30-byte Local File Header to compute exact
// zero-extract data extents across vols.
func ListZIP(vols []Volume) ([]Member, error) {
	_, totalSize, err := zipVolumeBounds(vols)
	if err != nil {
		return nil, err
	}
	mr := NewMultiReader(vols)
	var head [8]byte
	if err := readAt(mr, head[:4], 0, totalSize); err != nil {
		return nil, err
	}
	if Detect(head[:4]) != FormatZIP {
		return nil, fmt.Errorf("archive: zip missing PK signature: %w", ErrNotArchive)
	}

	entries, err := ReadZIPDirectory(vols)
	if err != nil {
		return nil, err
	}
	members := make([]Member, 0, len(entries))
	for _, entry := range entries {
		if entry.Dir || !entry.Stored {
			members = append(members, entry.Member)
			continue
		}
		m, _, err := ResolveZIPEntry(vols, entry)
		if err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

// OpenZIPMember sequentially decodes a ZIP member (stored or deflate) matched
// by match(nonDirIndex, member). It verifies CRC-32 and uncompressed size when
// the returned reader reaches EOF.
func OpenZIPMember(vols []Volume, match func(nonDirIndex int, m Member) bool) (io.ReadCloser, Member, error) {
	_, totalSize, err := zipVolumeBounds(vols)
	if err != nil {
		return nil, Member{}, err
	}
	mr := NewMultiReader(vols)
	var head [8]byte
	if err := readAt(mr, head[:4], 0, totalSize); err != nil {
		return nil, Member{}, err
	}
	if Detect(head[:4]) != FormatZIP {
		return nil, Member{}, fmt.Errorf("archive: zip missing PK signature: %w", ErrNotArchive)
	}

	entries, err := ReadZIPDirectory(vols)
	if err != nil {
		return nil, Member{}, err
	}
	nonDirIdx := 0
	for _, entry := range entries {
		if entry.Dir {
			continue
		}
		curIdx := nonDirIdx
		nonDirIdx++
		if match == nil || !match(curIdx, entry.Member) {
			continue
		}
		if entry.Encrypted {
			return nil, entry.Member, fmt.Errorf("archive: zip member %q: %w", entry.Name, ErrEncrypted)
		}
		if entry.MethodCode != ZIPMethodStore && entry.MethodCode != ZIPMethodDeflate {
			return nil, entry.Member, fmt.Errorf("archive: zip member %q method %s: %w", entry.Name, entry.Method, ErrUnsupported)
		}
		m, dataOff, err := ResolveZIPEntry(vols, entry)
		if err != nil {
			return nil, entry.Member, err
		}
		sec := io.NewSectionReader(mr, dataOff, entry.CompSize)
		rc, err := OpenZIPDataReader(sec, entry.Name, entry.MethodCode, entry.Size, entry.CRC32)
		if err != nil {
			return nil, m, err
		}
		return rc, m, nil
	}
	return nil, Member{}, fmt.Errorf("archive: zip member not found: %w", ErrCorrupt)
}

type zipVerifyReader struct {
	rc       io.ReadCloser
	crc      hash.Hash32
	read     int64
	wantCRC  uint32
	wantSize int64
	name     string
}

func (z *zipVerifyReader) Read(p []byte) (int, error) {
	n, err := z.rc.Read(p)
	if n > 0 {
		z.read += int64(n)
		_, _ = z.crc.Write(p[:n])
	}
	if err == io.EOF {
		if z.read != z.wantSize {
			return n, fmt.Errorf("archive: zip member %q unpacked %d bytes, want %d: %w", z.name, z.read, z.wantSize, ErrCorrupt)
		}
		if got := z.crc.Sum32(); got != z.wantCRC {
			return n, fmt.Errorf("archive: zip member %q crc32 %#08x, want %#08x: %w", z.name, got, z.wantCRC, ErrCorrupt)
		}
	}
	return n, err
}

func (z *zipVerifyReader) Close() error {
	return z.rc.Close()
}

func findZIPEOCD(tail []byte) int {
	for i := len(tail) - zipEOCDLen; i >= 0; i-- {
		if !bytes.Equal(tail[i:i+4], sigZIPEOCD) {
			continue
		}
		commentLen := int(binary.LittleEndian.Uint16(tail[i+20 : i+22]))
		if i+zipEOCDLen+commentLen == len(tail) {
			return i
		}
	}
	return -1
}

func parseZIP64Extra(extra []byte, uncomp, comp, localOff uint64, disk uint32) (uint64, uint64, uint64, uint32, error) {
	for len(extra) >= 4 {
		id := binary.LittleEndian.Uint16(extra[0:2])
		sz := int(binary.LittleEndian.Uint16(extra[2:4]))
		extra = extra[4:]
		if sz > len(extra) {
			return 0, 0, 0, 0, fmt.Errorf("archive: zip64 extra field truncated: %w", ErrCorrupt)
		}
		field := extra[:sz]
		extra = extra[sz:]
		if id != 0x0001 {
			continue
		}
		if uncomp == 0xffffffff {
			if len(field) < 8 {
				return 0, 0, 0, 0, fmt.Errorf("archive: zip64 unpacked size missing: %w", ErrCorrupt)
			}
			uncomp = binary.LittleEndian.Uint64(field[:8])
			field = field[8:]
		}
		if comp == 0xffffffff {
			if len(field) < 8 {
				return 0, 0, 0, 0, fmt.Errorf("archive: zip64 packed size missing: %w", ErrCorrupt)
			}
			comp = binary.LittleEndian.Uint64(field[:8])
			field = field[8:]
		}
		if localOff == 0xffffffff {
			if len(field) < 8 {
				return 0, 0, 0, 0, fmt.Errorf("archive: zip64 local header offset missing: %w", ErrCorrupt)
			}
			localOff = binary.LittleEndian.Uint64(field[:8])
			field = field[8:]
		}
		if disk == 0xffff {
			if len(field) < 4 {
				return 0, 0, 0, 0, fmt.Errorf("archive: zip64 disk number missing: %w", ErrCorrupt)
			}
			disk = binary.LittleEndian.Uint32(field[:4])
		}
		return uncomp, comp, localOff, disk, nil
	}
	return 0, 0, 0, 0, fmt.Errorf("archive: zip64 extra field (0x0001) not found: %w", ErrCorrupt)
}
