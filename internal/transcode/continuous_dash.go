package transcode

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"time"
)

// DASHTrack describes a separately addressable track of a shared broadcast.
type DASHTrack struct {
	ID                                             uint32
	Kind, Codec                                    string
	Width, Height, SampleRate, Channels, Bandwidth int
	Timescale, DefaultDuration                     uint32
	EditShift                                      int64
}

// ParseDASHInit inspects an fMP4 initialization segment and describes each
// video and audio track it declares.
func ParseDASHInit(init []byte) ([]DASHTrack, error) {
	tracks, err := parseInitTracks(init)
	if err != nil {
		return nil, err
	}
	out := make([]DASHTrack, 0, len(tracks))
	for _, t := range tracks {
		kind := "video"
		if t.Handler == "soun" {
			kind = "audio"
		} else if t.Handler != "vide" {
			continue
		}
		out = append(out, DASHTrack{
			ID:              t.TrackID,
			Kind:            kind,
			Codec:           t.Codecs,
			Width:           t.Width,
			Height:          t.Height,
			SampleRate:      t.SampleRate,
			Channels:        t.Channels,
			Timescale:       t.Timescale,
			DefaultDuration: t.DefaultDuration,
			EditShift:       t.EditShift,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("DASH init has no video or audio tracks")
	}
	return out, nil
}

// IsolateDASHTrack returns a single-track fMP4 init or media segment for track id.
func IsolateDASHTrack(src []byte, id uint32) ([]byte, error) {
	return isolateDASHTrack(src, id)
}

// FMP4TrackSpan reports a media segment's presentation start time and total
// duration for trackID in that track's media timescale.
func FMP4TrackSpan(seg []byte, trackID, defaultDur uint32, editShift int64) (start, dur int64, ok bool) {
	top, err := readBoxes(seg, 0, len(seg))
	if err != nil {
		return 0, 0, false
	}
	haveStart := false
	var total int64
	for _, box := range top {
		if box.typ != "moof" {
			continue
		}
		trafs, err := readBoxes(seg, box.start+box.hdr, box.end)
		if err != nil {
			continue
		}
		for _, traf := range trafs {
			if traf.typ != "traf" {
				continue
			}
			kids, err := readBoxes(seg, traf.start+traf.hdr, traf.end)
			if err != nil {
				continue
			}
			var id uint32
			def := defaultDur
			var base, cto, fragDur int64
			haveBase, haveRun := false, false
			for _, k := range kids {
				p := k.payload(seg)
				switch k.typ {
				case "tfhd":
					if len(p) < 8 {
						continue
					}
					id = binary.BigEndian.Uint32(p[4:])
					flags := uint32(p[1])<<16 | uint32(p[2])<<8 | uint32(p[3])
					o := 8
					if flags&0x01 != 0 {
						o += 8
					}
					if flags&0x02 != 0 {
						o += 4
					}
					if flags&0x08 != 0 && len(p) >= o+4 {
						def = binary.BigEndian.Uint32(p[o:])
					}
				case "tfdt":
					if len(p) < 8 {
						continue
					}
					if p[0] == 1 && len(p) >= 12 {
						base = int64(binary.BigEndian.Uint64(p[4:]))
						if base < 0 {
							base = 0
						}
					} else {
						base = int64(binary.BigEndian.Uint32(p[4:]))
					}
					haveBase = true
				case "trun":
					if !haveRun {
						if off, n, has := trunFirstOffset(p); has && n > 0 {
							cto, haveRun = off, true
						}
					}
					fragDur += trunDuration(p, def)
				}
			}
			if id != trackID || !haveBase {
				continue
			}
			if !haveStart {
				start = base + cto - editShift
				if start < 0 {
					start = 0
				}
				haveStart = true
			}
			total += fragDur
		}
	}
	return start, total, haveStart && total > 0
}

// DASHSegmentIndex preserves the shared encoder while exposing separate tracks
// to DASH clients whose capability checks reject multiplexed representations.
type DASHSegmentIndex interface {
	DASHTracks() ([]DASHTrack, error)
	ServeDASHTrack(http.ResponseWriter, *http.Request, int, int64, bool)
}

var _ DASHSegmentIndex = (*continuousSegmentIndex)(nil)

func (s *continuousSegmentIndex) DASHTracks() ([]DASHTrack, error) {
	b, ok := s.enc.store.read("init.mp4")
	if !ok {
		return nil, fmt.Errorf("DASH init unavailable")
	}
	out, err := ParseDASHInit(b)
	if err != nil {
		return nil, err
	}
	_, _, _, videoBW := s.Format()
	audioBW := max(s.enc.req.Decision.AudioBitrateKbps, 128) * 1000
	for i := range out {
		if out[i].Kind == "audio" {
			out[i].Bandwidth = audioBW
		} else {
			out[i].Bandwidth = videoBW
		}
	}
	return out, nil
}

func (s *continuousSegmentIndex) ServeDASHTrack(w http.ResponseWriter, r *http.Request, rep int, seq int64, init bool) {
	tracks, err := s.DASHTracks()
	if err != nil || rep < 0 || rep >= len(tracks) {
		http.NotFound(w, r)
		return
	}
	name := "init.mp4"
	if !init {
		name = s.mp4(seq)
	}
	b, ok := s.enc.store.read(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err = isolateDASHTrack(b, tracks[rep].ID)
	if err != nil {
		http.Error(w, "invalid DASH segment", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", tracks[rep].Kind+"/mp4")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
}

// Replacing unwanted metadata with free boxes preserves every byte offset in
// tfhd/trun/sidx, including data offsets into a shared mdat. The source remains
// immutable; media runs are compacted afterward.
func isolateDASHTrack(src []byte, id uint32) ([]byte, error) {
	b := bytes.Clone(src)
	var walk func(int, int) error
	walk = func(start, end int) error {
		boxes, err := readBoxes(b, start, end)
		if err != nil {
			return err
		}
		for _, box := range boxes {
			remove := false
			switch box.typ {
			case "moov", "moof", "mvex":
				if err := walk(box.start+box.hdr, box.end); err != nil {
					return err
				}
			case "trak":
				hd, ok := child(b, box, "tkhd")
				if !ok {
					return fmt.Errorf("missing tkhd")
				}
				p := hd.payload(b)
				off := 12
				if len(p) > 0 && p[0] == 1 {
					off = 20
				}
				if len(p) < off+4 {
					return errShortBox
				}
				remove = binary.BigEndian.Uint32(p[off:]) != id
			case "trex":
				p := box.payload(b)
				if len(p) < 8 {
					return errShortBox
				}
				remove = binary.BigEndian.Uint32(p[4:]) != id
			case "traf":
				hd, ok := child(b, box, "tfhd")
				if !ok {
					return fmt.Errorf("missing tfhd")
				}
				p := hd.payload(b)
				if len(p) < 8 {
					return errShortBox
				}
				remove = binary.BigEndian.Uint32(p[4:]) != id
			}
			if remove {
				copy(b[box.start+4:box.start+8], "free")
			}
		}
		return nil
	}
	if err := walk(0, len(b)); err != nil {
		return nil, err
	}
	return compactDASHMedia(b, id)
}

// Compact selected runs into one mdat per moof. Init segments pass through.
// All trun data offsets are rebuilt; sidx boxes (which index old sizes) drop.
func compactDASHMedia(b []byte, id uint32) ([]byte, error) {
	top, err := readBoxes(b, 0, len(b))
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, box := range top {
		if box.typ == "mdat" || box.typ == "sidx" {
			continue
		}
		if box.typ != "moof" {
			out = append(out, b[box.start:box.end]...)
			continue
		}
		moof := bytes.Clone(b[box.start:box.end])
		kids, err := readBoxes(moof, box.hdr, len(moof))
		if err != nil {
			return nil, err
		}
		var payload []byte
		found := false
		for _, traf := range kids {
			if traf.typ != "traf" {
				continue
			}
			tfhd, ok := child(moof, traf, "tfhd")
			if !ok {
				return nil, fmt.Errorf("missing tfhd")
			}
			h := tfhd.payload(moof)
			if len(h) < 8 {
				return nil, errShortBox
			}
			if binary.BigEndian.Uint32(h[4:]) != id {
				return nil, fmt.Errorf("unexpected track")
			}
			flags := binary.BigEndian.Uint32(h[:4]) & 0xffffff
			// The shared HLS muxer emits default-base-is-moof, never absolute
			// offsets. Refuse other bases rather than guessing their file origin.
			if flags&1 != 0 || flags&0x20000 == 0 {
				return nil, fmt.Errorf("unsupported fragment data base")
			}
			pos := 8
			if flags&2 != 0 {
				pos += 4
			}
			if flags&8 != 0 {
				pos += 4
			}
			var defSize uint32
			if flags&16 != 0 {
				if len(h) < pos+4 {
					return nil, errShortBox
				}
				defSize = binary.BigEndian.Uint32(h[pos:])
			}
			runs, err := readBoxes(moof, traf.start+traf.hdr, traf.end)
			if err != nil {
				return nil, err
			}
			for _, run := range runs {
				if run.typ != "trun" {
					continue
				}
				p := run.payload(moof)
				if len(p) < 12 {
					return nil, errShortBox
				}
				f := binary.BigEndian.Uint32(p[:4]) & 0xffffff
				if f&1 == 0 {
					return nil, fmt.Errorf("missing explicit run offset")
				}
				count := uint64(binary.BigEndian.Uint32(p[4:]))
				start := int64(box.start) + int64(int32(binary.BigEndian.Uint32(p[8:])))
				pos := 12
				if f&4 != 0 {
					pos += 4
				}
				per := 0
				sizeOffset := 0
				if f&0x100 != 0 {
					per += 4
					sizeOffset += 4
				}
				if f&0x200 != 0 {
					per += 4
				}
				if f&0x400 != 0 {
					per += 4
				}
				if f&0x800 != 0 {
					per += 4
				}
				if pos > len(p) || count*uint64(per) > uint64(len(p)-pos) {
					return nil, errShortBox
				}
				total := count * uint64(defSize)
				if f&0x200 != 0 {
					total = 0
					for i := uint64(0); i < count; i++ {
						total += uint64(binary.BigEndian.Uint32(p[pos+int(i)*per+sizeOffset:]))
					}
				}
				if start < 0 || total > uint64(len(b)) || uint64(start)+total > uint64(len(b)) {
					return nil, errShortBox
				}
				inMdat := false
				for _, data := range top {
					if data.typ == "mdat" && start >= int64(data.start+data.hdr) && start+int64(total) <= int64(data.end) {
						inMdat = true
						break
					}
				}
				if !inMdat {
					return nil, fmt.Errorf("run outside media payload")
				}
				offset := len(moof) + 8 + len(payload)
				if uint64(offset) > 0x7fffffff {
					return nil, fmt.Errorf("fragment too large")
				}
				binary.BigEndian.PutUint32(p[8:], uint32(offset))
				payload = append(payload, b[int(start):int(start)+int(total)]...)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("missing selected media run")
		}
		out = append(out, moof...)
		var hdr [8]byte
		binary.BigEndian.PutUint32(hdr[:4], uint32(8+len(payload)))
		copy(hdr[4:], "mdat")
		out = append(out, hdr[:]...)
		out = append(out, payload...)
	}
	return out, nil
}
