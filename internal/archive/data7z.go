package archive

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ulikunitz/xz/lzma"
)

// Data7zLimits bounds metadata allocation, decoder dictionaries and solid work
// for plugin data calls. It does not enable passwords or external volumes.
type Data7zLimits struct {
	MemoryBytes   int64
	ExpandedBytes int64
	Members       int
}

type data7zPlan struct {
	coder                       sz7Coder
	start, packed, skip, memory int64
	unpacked                    uint64
}

func (c *sz7Crypto) headerLimit() uint64 {
	if c.data == nil {
		return sz7MaxHeader
	}
	return uint64(min(int64(sz7MaxHeader), c.data.MemoryBytes/16))
}
func (c *sz7Crypto) countLimit() uint64 {
	if c.data == nil {
		return 0
	}
	return uint64(min(c.data.Members, int(c.data.MemoryBytes/4096)))
}

// List7zData prepares bounded Copy, LZMA and LZMA2 member reads, including
// solid blocks. Other coder chains are refused before any member is opened.
func List7zData(ctx context.Context, r io.ReaderAt, size int64, limits Data7zLimits) ([]Member, error) {
	if limits.MemoryBytes < 1<<20 || limits.ExpandedBytes <= 0 || limits.Members <= 0 {
		return nil, ErrUnsupported
	}
	return list7z(&sz7Crypto{ctx: ctx, data: &limits}, r, size)
}

// Open7zData decodes from the member's solid block boundary. The caller must
// bound the requested offset plus length and propagate cancellation on r.
func Open7zData(ctx context.Context, r io.ReaderAt, m Member) (io.Reader, error) {
	if m.Dir {
		return nil, ErrUnsupported
	}
	p := m.data7z
	if p == nil {
		if m.Size == 0 && m.Stored && !m.Dir {
			return bytes.NewReader(nil), nil
		}
		return nil, ErrUnsupported
	}
	dec, err := data7zCoder(&p.coder, io.NewSectionReader(r, p.start, p.packed), p.unpacked, p.memory)
	if err != nil {
		return nil, err
	}
	dec = data7zContextReader{ctx: ctx, r: dec}
	if _, err = io.CopyN(io.Discard, dec, p.skip); err != nil {
		return nil, err
	}
	return io.LimitReader(dec, m.Size), nil
}

// A nil source validates properties without allocating a dictionary.
func data7zCoder(c *sz7Coder, source io.Reader, unpack uint64, memory int64) (io.Reader, error) {
	if c.numIn != 1 || c.numOut != 1 {
		return nil, ErrUnsupported
	}
	var dict uint64
	switch {
	case bytes.Equal(c.id, sz7MethodCopy):
		return source, nil
	case bytes.Equal(c.id, sz7MethodLZMA):
		if len(c.props) != 5 {
			return nil, ErrCorrupt
		}
		props, err := lzma.PropertiesForCode(c.props[0])
		if err != nil {
			return nil, ErrCorrupt
		}
		if (int64(0x300)<<uint(props.LC+props.LP))*2 > memory/4 {
			return nil, fmt.Errorf("archive: LZMA model exceeds memory grant: %w", ErrUnsupported)
		}
		dict = uint64(binary.LittleEndian.Uint32(c.props[1:]))
	case bytes.Equal(c.id, sz7MethodLZMA2):
		if len(c.props) != 1 || c.props[0] > 40 {
			return nil, ErrCorrupt
		}
		if c.props[0] == 40 {
			dict = 0xffffffff
		} else {
			dict = uint64(2|int(c.props[0]&1)) << (uint(c.props[0])/2 + 11)
		}
	default:
		return nil, fmt.Errorf("archive: unsupported data coder %s: %w", c.name(), ErrUnsupported)
	}
	dict = max(dict, uint64(lzma.MinDictCap))
	if dict > uint64(memory) {
		return nil, fmt.Errorf("archive: decoder dictionary exceeds memory grant: %w", ErrUnsupported)
	}
	if source == nil {
		return nil, nil
	}
	if bytes.Equal(c.id, sz7MethodLZMA2) {
		return (lzma.Reader2Config{DictCap: int(dict)}).NewReader2(source)
	}
	h := make([]byte, lzma.HeaderLen)
	copy(h, c.props)
	binary.LittleEndian.PutUint64(h[5:], unpack)
	return lzma.NewReader(io.MultiReader(bytes.NewReader(h), source))
}

// Check cancellation even while the decoder serves buffered solid output.
type data7zContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r data7zContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
