package vault

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/klauspost/compress/zstd"
)

// Headers change much less often than articles. Keep the extensible header
// length-prefixed, then encode each file's densely repeated article fields.
type header struct {
	Version    int
	Key        Key
	Kind       Kind
	Name       string
	Layout     *Layout
	First      map[int][]byte
	Torrent    *Torrent
	Info, Par2 []byte
	Doc        *nzb.NZB
	IDBytes    int
	Table      []string
}

const maxPayload = 128 << 20

var invalid = errors.New("vault: invalid record")
var encoder = sync.OnceValues(func() (*zstd.Encoder, error) {
	return zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
})
var decoder = sync.OnceValues(func() (*zstd.Decoder, error) {
	return zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxPayload))
})

func Encode(r *Record) ([]byte, error) {
	h := header{Version: 1, Key: r.Key, Kind: r.Kind, Name: r.Name, Layout: r.Layout, Torrent: r.Torrent, Par2: r.Par2}
	if r.Layout != nil {
		h.First = r.Layout.FirstArticles
	}
	if r.Torrent != nil {
		h.Info = r.Torrent.Info
	}
	var data, locals []byte
	integer := func(n int64) { data = binary.AppendVarint(data, n) }
	indexes := map[string]int{}
	ref := func(s string) {
		i, ok := indexes[s]
		if !ok {
			i = len(h.Table)
			h.Table = append(h.Table, s)
			indexes[s] = i
		}
		integer(int64(i))
	}
	id := func(s string) {
		p := strings.LastIndexByte(s, '@')
		local, domain := s, ""
		if p >= 0 {
			local, domain = s[:p], s[p:]
		}
		ref(domain)
		integer(int64(len(local)))
		locals = append(locals, local...)
	}
	if r.NZB != nil {
		h.Doc = &nzb.NZB{Meta: r.NZB.Meta}
		for _, f := range r.NZB.Files {
			meta := *f
			meta.Groups = nil
			meta.Segments = nil
			h.Doc.Files = append(h.Doc.Files, &meta)
			integer(int64(len(f.Groups)))
			for _, g := range f.Groups {
				ref(g)
			}
			counts := map[int64]int{}
			var mode int64
			for _, s := range f.Segments {
				counts[s.Bytes]++
				if counts[s.Bytes] > counts[mode] {
					mode = s.Bytes
				}
			}
			integer(mode)
			integer(int64(len(f.Segments)))
			prev := 0
			for _, s := range f.Segments {
				integer(int64(s.Number - prev))
				prev = s.Number
				integer(s.Bytes - mode)
				id(s.MessageID)
				integer(int64(len(s.Alternates)))
				for _, a := range s.Alternates {
					id(a)
				}
			}
		}
	}
	h.IDBytes = len(locals)
	b, e := json.Marshal(h)
	if e != nil {
		return nil, e
	}
	raw := binary.AppendUvarint(nil, uint64(len(b)))
	raw = append(raw, b...)
	raw = append(raw, data...)
	raw = append(raw, locals...)
	if len(raw) > maxPayload {
		return nil, invalid
	}
	enc, e := encoder()
	if e != nil {
		return nil, e
	}
	return enc.EncodeAll(raw, nil), nil
}

type reader struct {
	*bytes.Reader
	err    error
	budget int
}

func (d *reader) integer() int64 {
	if d.err != nil {
		return 0
	}
	v, e := binary.ReadVarint(d.Reader)
	d.err = e
	return v
}
func (d *reader) count() int {
	v := d.integer()
	if v < 0 || v > int64(d.Len()) {
		d.err = invalid
		return 0
	}
	return int(v)
}
func (d *reader) spend(n int) {
	d.budget -= n
	if d.budget < 0 {
		d.err = invalid
	}
}
func Decode(payload []byte) (*Record, error) {
	dec, e := decoder()
	if e != nil {
		return nil, e
	}
	raw, e := dec.DecodeAll(payload, nil)
	if e != nil {
		return nil, e
	}
	return decodeRaw(raw)
}

func decodeRaw(raw []byte) (*Record, error) {
	d := reader{Reader: bytes.NewReader(raw), budget: maxPayload * 2}
	n, e := binary.ReadUvarint(d.Reader)
	if e != nil || n > uint64(d.Len()) {
		return nil, invalid
	}
	pos := len(raw) - d.Len()
	var h header
	if e := json.Unmarshal(raw[pos:pos+int(n)], &h); e != nil || h.Version != 1 {
		return nil, invalid
	}
	d.Seek(int64(n), io.SeekCurrent)
	if h.IDBytes < 0 || h.IDBytes > d.Len() {
		return nil, invalid
	}
	ids := raw[len(raw)-h.IDBytes:]
	d.Reader = bytes.NewReader(raw[pos+int(n) : len(raw)-h.IDBytes])
	ref := func() string {
		i := d.integer()
		if i < 0 || i >= int64(len(h.Table)) {
			d.err = invalid
			return ""
		}
		return h.Table[i]
	}
	id := func() string {
		domain := ref()
		length := d.integer()
		if length < 0 || length > int64(len(ids)) {
			d.err = invalid
			return ""
		}
		n := int(length)
		d.spend(n + len(domain))
		if d.err != nil {
			return ""
		}
		local := ids[:n]
		ids = ids[n:]
		return string(local) + domain
	}
	r := &Record{Key: h.Key, Kind: h.Kind, Name: h.Name, NZB: h.Doc, Torrent: h.Torrent, Layout: h.Layout, Par2: h.Par2}
	if r.Layout != nil {
		r.Layout.FirstArticles = h.First
	}
	if r.Torrent != nil {
		r.Torrent.Info = h.Info
	}
	if r.Kind == KindUsenet && r.NZB != nil && len(r.NZB.Files) > 0 && r.Torrent == nil {
		for _, f := range r.NZB.Files {
			if f == nil || len(f.Segments) != 0 || len(f.Groups) != 0 {
				return nil, invalid
			}
			if f.Subject != "" && f.SubjectName == "" {
				subjName := nzb.NameFromSubject(f.Subject)
				f.SubjectName = subjName
				if f.Name != subjName {
					// Records written before a NameFromSubject change still carry
					// the raw Subject alongside the older derived Name; refresh
					// derived fields so re-encoding keeps the original Subject
					// instead of downgrading it to the stale Name.
					f.Name = subjName
					f.Info = nzb.Classify(subjName)
					f.Obfuscated = nzb.IsObfuscated(subjName)
				}
			}
			for range d.count() {
				if d.err != nil {
					return nil, invalid
				}
				group := ref()
				d.spend(16 + len(group))
				f.Groups = append(f.Groups, group)
			}
			mode := d.integer()
			prev := 0
			for range d.count() {
				d.spend(64)
				if d.err != nil {
					return nil, invalid
				}
				prev += int(d.integer())
				s := nzb.Segment{Number: prev, Bytes: mode + d.integer(), MessageID: id()}
				for range d.count() {
					if d.err != nil {
						return nil, invalid
					}
					d.spend(16)
					s.Alternates = append(s.Alternates, id())
				}
				f.Segments = append(f.Segments, s)
			}
		}
	} else if r.Kind != KindTorrent || r.Torrent == nil || r.NZB != nil {
		return nil, invalid
	}
	if d.err != nil || d.Len() != 0 || len(ids) != 0 {
		return nil, invalid
	}
	return r, nil
}
