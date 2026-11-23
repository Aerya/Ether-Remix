package subtitles

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/container/ebml"
)

// Published Matroska element IDs (https://www.matroska.org/technical/elements.html).
const (
	mkvEBMLID          = 0x1a45dfa3
	mkvSegmentID       = 0x18538067
	mkvSeekHeadID      = 0x114d9b74
	mkvSeekID          = 0x4dbb
	mkvSeekIDID        = 0x53ab
	mkvSeekPositionID  = 0x53ac
	mkvInfoID          = 0x1549a966
	mkvTimecodeScaleID = 0x2ad7b1
	mkvTracksID        = 0x1654ae6b
	mkvTrackEntryID    = 0xae
	mkvTrackNumberID   = 0xd7
	mkvTrackTypeID     = 0x83
	mkvCodecIDID       = 0x86
	mkvCuesID          = 0x1c53bb6b
	mkvCuePointID      = 0xbb
	mkvCueTimeID       = 0xb3
	mkvCueTrackPosID   = 0xb7
	mkvCueTrackID      = 0xf7
	mkvCueClusterPosID = 0xf1
	mkvCueRelPosID     = 0xf0
	mkvCueDurationID   = 0xb2
	mkvClusterID       = 0x1f43b675
	mkvTimestampID     = 0xe7
	mkvSimpleBlockID   = 0xa3
	mkvBlockGroupID    = 0xa0
	mkvBlockID         = 0xa1
	mkvBlockDurationID = 0x9b
)

var errMKV = errors.New("matroska subtitle index unreadable")

type mkvElem struct {
	id         uint64
	start, end int64
	hdr        int
}

func mkvVint(b []byte, id bool) (uint64, int, error) {
	v, n := ebml.Vint(b, id)
	if n == 0 || (id && n > 4) {
		return 0, 0, errMKV
	}
	return v, n, nil
}

func (r *rangeReader) mkvElement(off, end int64) (mkvElem, error) {
	n := int(min(int64(12), end-off))
	if n < 2 {
		return mkvElem{}, errMKV
	}
	b, err := r.read(off, n)
	if err != nil {
		return mkvElem{}, err
	}
	id, ni, err := mkvVint(b, true)
	if err != nil {
		return mkvElem{}, err
	}
	size, ns, err := mkvVint(b[ni:], false)
	if err != nil {
		return mkvElem{}, err
	}
	start := off + int64(ni+ns)
	if size == (uint64(1)<<uint(7*ns))-1 {
		size = uint64(end - start)
	}
	if size > uint64(end-start) {
		return mkvElem{}, errMKV
	}
	return mkvElem{id: id, start: start, end: start + int64(size), hdr: ni + ns}, nil
}

func mkvChildren(b []byte) ([]struct {
	id uint64
	b  []byte
}, error) {
	var out []struct {
		id uint64
		b  []byte
	}
	for len(b) > 0 {
		id, ni, err := mkvVint(b, true)
		if err != nil {
			return nil, err
		}
		size, ns, err := mkvVint(b[ni:], false)
		if err != nil || size > uint64(len(b)-ni-ns) {
			return nil, errMKV
		}
		out = append(out, struct {
			id uint64
			b  []byte
		}{id, b[ni+ns : ni+ns+int(size)]})
		b = b[ni+ns+int(size):]
		if len(out) > 200000 {
			return nil, errMKV
		}
	}
	return out, nil
}

func mkvUint(b []byte) uint64 {
	if len(b) > 8 {
		return 0
	}
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

func isTextCodec(id string) bool {
	id = strings.ToUpper(strings.TrimSpace(id))
	switch {
	case strings.HasPrefix(id, "S_TEXT/"):
		return true
	case id == "S_UTF8" || id == "S_ASS" || id == "S_SSA":
		return true
	case id == "S_VOBSUB" || id == "S_HDMV/PGS" || id == "S_HDMV/TEXTST":
		return false
	}
	return false
}

// ReadEmbeddedTextCues reads cue times from the richest embedded TEXT
// subtitle track of a Matroska or MP4 source, using only the container
// index and bounded range reads.
func ReadEmbeddedTextCues(ctx context.Context, src io.ReaderAt, size int64) ([]Cue, error) {
	tracks, err := readEmbeddedTextCueTracks(ctx, src, size, size <= sourceReadBudget)
	if err != nil || len(tracks) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, errMKV
	}
	return tracks[0], nil
}

func readEmbeddedTextCueTracks(ctx context.Context, src io.ReaderAt, size int64, scanClusters bool) ([][]Cue, error) {
	if src == nil || size < 16 {
		return nil, errMKV
	}
	r := newRangeReader(ctx, src, size)
	head, err := r.read(0, 8)
	if err != nil || len(head) < 4 {
		return nil, errMKV
	}
	if binary.BigEndian.Uint32(head) == mkvEBMLID {
		return r.mkvTextCueTracks(size, scanClusters)
	}
	return r.mp4TextCueTracks(size)
}

func (r *rangeReader) mkvTextCueTracks(size int64, scanClusters bool) ([][]Cue, error) {
	var seg mkvElem
	for off, n := int64(0), 0; off < size && n < 32; n++ {
		e, err := r.mkvElement(off, size)
		if err != nil {
			return nil, err
		}
		if e.id == mkvSegmentID {
			seg = e
			break
		}
		off = e.end
	}
	if seg.id == 0 {
		return nil, errMKV
	}
	scale := uint64(1000000)
	var (
		trackOrder      []uint64
		trackSet        = map[uint64]bool{}
		cuesRaw         []byte
		seenInfo        bool
		seenTrk         bool
		firstClusterOff int64
	)
	targets := map[uint64]int64{}
	visited := map[int64]bool{}
	inspect := func(e mkvElem) error {
		switch e.id {
		case mkvSeekHeadID, mkvInfoID, mkvTracksID, mkvCuesID:
		default:
			return nil
		}
		b, err := r.read(e.start, int(e.end-e.start))
		if err != nil {
			return err
		}
		children, err := mkvChildren(b)
		if err != nil {
			return err
		}
		switch e.id {
		case mkvCuesID:
			cuesRaw = b
		case mkvInfoID:
			seenInfo = true
			for _, c := range children {
				if c.id == mkvTimecodeScaleID {
					if v := mkvUint(c.b); v > 0 {
						scale = v
					}
				}
			}
		case mkvTracksID:
			seenTrk = true
			for _, c := range children {
				if c.id != mkvTrackEntryID {
					continue
				}
				fields, _ := mkvChildren(c.b)
				var num, typ uint64
				codec := ""
				for _, f := range fields {
					switch f.id {
					case mkvTrackNumberID:
						num = mkvUint(f.b)
					case mkvTrackTypeID:
						typ = mkvUint(f.b)
					case mkvCodecIDID:
						codec = string(f.b)
					}
				}
				if typ == 0x11 && num != 0 && isTextCodec(codec) && !trackSet[num] {
					trackSet[num] = true
					trackOrder = append(trackOrder, num)
				}
			}
		case mkvSeekHeadID:
			for _, c := range children {
				if c.id != mkvSeekID {
					continue
				}
				fs, _ := mkvChildren(c.b)
				var id, pos uint64
				for _, f := range fs {
					if f.id == mkvSeekIDID {
						id = mkvUint(f.b)
					}
					if f.id == mkvSeekPositionID {
						pos = mkvUint(f.b)
					}
				}
				if pos < uint64(seg.end-seg.start) {
					targets[id] = seg.start + int64(pos)
				}
			}
		}
		return nil
	}
	for off, n := seg.start, 0; off < seg.end && n < 64; n++ {
		e, err := r.mkvElement(off, seg.end)
		if err != nil {
			return nil, err
		}
		if e.id == mkvClusterID {
			firstClusterOff = off
			break
		}
		visited[off] = true
		if err = inspect(e); err != nil {
			return nil, err
		}
		if cuesRaw != nil && seenInfo && seenTrk && !scanClusters {
			break
		}
		if e.end <= off {
			return nil, errMKV
		}
		off = e.end
	}
	for _, id := range []uint64{mkvInfoID, mkvTracksID, mkvCuesID} {
		off, ok := targets[id]
		if !ok || visited[off] {
			continue
		}
		e, err := r.mkvElement(off, seg.end)
		if err != nil || e.id != id {
			continue
		}
		if err = inspect(e); err != nil {
			return nil, err
		}
	}
	if len(trackOrder) == 0 || scale == 0 {
		return nil, errMKV
	}
	byTrack := map[uint64][]Cue{}
	if cuesRaw != nil {
		points, err := mkvChildren(cuesRaw)
		if err != nil {
			return nil, err
		}
		for _, p := range points {
			if p.id != mkvCuePointID {
				continue
			}
			fields, err := mkvChildren(p.b)
			if err != nil {
				continue
			}
			var stamp uint64
			hasTime := false
			for _, f := range fields {
				if f.id == mkvCueTimeID {
					stamp = mkvUint(f.b)
					hasTime = true
				}
			}
			if !hasTime {
				continue
			}
			for _, f := range fields {
				if f.id != mkvCueTrackPosID {
					continue
				}
				ps, err := mkvChildren(f.b)
				if err != nil {
					continue
				}
				var track, dur uint64
				hasPos := false
				for _, q := range ps {
					switch q.id {
					case mkvCueTrackID:
						track = mkvUint(q.b)
					case mkvCueClusterPosID:
						hasPos = true
					case mkvCueDurationID:
						dur = mkvUint(q.b)
					}
				}
				if !trackSet[track] || !hasPos {
					continue
				}
				start := time.Duration(stamp * scale)
				end := start + time.Duration(dur*scale)
				if dur == 0 {
					end = start + 2*time.Second
				}
				if end <= start {
					continue
				}
				byTrack[track] = append(byTrack[track], Cue{Start: start, End: end})
			}
		}
	}
	tracks := orderCueTracks(trackOrder, byTrack)
	if (len(tracks) == 0 || len(tracks[0]) < 3) && scanClusters && firstClusterOff > 0 && seg.end-firstClusterOff <= sourceReadBudget {
		if scanned := r.scanMKVClusters(firstClusterOff, seg.end, scale, trackOrder, trackSet); len(scanned) > 0 {
			if len(tracks) == 0 || len(scanned[0]) > len(tracks[0]) {
				tracks = scanned
			}
		}
	}
	if len(tracks) == 0 {
		return nil, errMKV
	}
	return tracks, nil
}

func (r *rangeReader) scanMKVClusters(startOff, endOff int64, scale uint64, trackOrder []uint64, trackSet map[uint64]bool) [][]Cue {
	total := endOff - startOff
	if total <= 0 || total > sourceReadBudget {
		return nil
	}
	// Read sequential 1 MiB windows so many small clusters stay well under
	// rangeMaxSegments.
	const chunkSize = 1 << 20
	raw := make([]byte, 0, int(total))
	for off := startOff; off < endOff; {
		n := int(min(int64(chunkSize), endOff-off))
		b, err := r.read(off, n)
		if err != nil || len(b) == 0 {
			break
		}
		raw = append(raw, b...)
		off += int64(len(b))
	}
	byTrack := map[uint64][]Cue{}
	for len(raw) >= 2 {
		id, ni, err := mkvVint(raw, true)
		if err != nil {
			break
		}
		sz, ns, err := mkvVint(raw[ni:], false)
		if err != nil {
			break
		}
		hdr := ni + ns
		if sz == (uint64(1)<<uint(7*ns))-1 {
			sz = uint64(len(raw) - hdr)
		}
		if sz > uint64(len(raw)-hdr) {
			break
		}
		body := raw[hdr : hdr+int(sz)]
		raw = raw[hdr+int(sz):]
		if id != mkvClusterID {
			continue
		}
		children, err := mkvChildren(body)
		if err != nil {
			continue
		}
		var clusterTS uint64
		for _, c := range children {
			if c.id == mkvTimestampID {
				clusterTS = mkvUint(c.b)
				break
			}
		}
		for _, c := range children {
			switch c.id {
			case mkvSimpleBlockID:
				appendMKVBlockCue(byTrack, trackSet, clusterTS, scale, c.b, 0)
			case mkvBlockGroupID:
				fields, err := mkvChildren(c.b)
				if err != nil {
					continue
				}
				var block []byte
				var dur uint64
				for _, f := range fields {
					switch f.id {
					case mkvBlockID:
						block = f.b
					case mkvBlockDurationID:
						dur = mkvUint(f.b)
					}
				}
				if len(block) > 0 {
					appendMKVBlockCue(byTrack, trackSet, clusterTS, scale, block, dur)
				}
			}
		}
	}
	return orderCueTracks(trackOrder, byTrack)
}

func appendMKVBlockCue(byTrack map[uint64][]Cue, trackSet map[uint64]bool, clusterTS, scale uint64, block []byte, dur uint64) {
	trackNum, n := ebml.Vint(block, false)
	if n == 0 || len(block) < n+3 || !trackSet[trackNum] {
		return
	}
	rel := int16(binary.BigEndian.Uint16(block[n : n+2]))
	stamp := int64(clusterTS) + int64(rel)
	if stamp < 0 {
		return
	}
	start := time.Duration(stamp) * time.Duration(scale)
	end := start + time.Duration(dur*scale)
	if dur == 0 {
		end = start + 2*time.Second
	}
	if end <= start || len(byTrack[trackNum]) >= 20000 {
		return
	}
	byTrack[trackNum] = append(byTrack[trackNum], Cue{Start: start, End: end})
}

func orderCueTracks(trackOrder []uint64, byTrack map[uint64][]Cue) [][]Cue {
	var out [][]Cue
	for _, num := range trackOrder {
		cues := byTrack[num]
		if len(cues) == 0 {
			continue
		}
		sort.SliceStable(cues, func(i, j int) bool { return cues[i].Start < cues[j].Start })
		out = append(out, cues)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}
