package transcode

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/subtitles"
)

// An embedded text subtitle is a few thousand small blocks interleaved through
// a file of tens of gigabytes. FFmpeg finds them by reading the whole file,
// which on a remote release takes longer than a viewer waits. A Matroska
// muxer (mkvmerge) indexes every subtitle block in Cues, with its cluster and
// its position inside that cluster, so the track can be read block by block:
// the container header, the index, and one small read per subtitle line, at
// a cost set by the number of lines rather than the size of the file.

// errNoSubtitleIndex: the source cannot be read through its index; FFmpeg
// reads the track instead.
var errNoSubtitleIndex = errors.New("transcode: subtitle track not indexed")

type subtitleReaderKey struct{}

// SubtitleReaderOpener opens the session's source for direct reads. release
// ends the hold on it.
type SubtitleReaderOpener func() (src io.ReaderAt, size int64, release func(), ok bool)

// WithSubtitleReader lets ExtractSubtitle read an indexed track from the
// source directly instead of having FFmpeg read the whole input.
func WithSubtitleReader(ctx context.Context, open SubtitleReaderOpener) context.Context {
	if open == nil {
		return ctx
	}
	return context.WithValue(ctx, subtitleReaderKey{}, open)
}

const (
	// subtitleIndexReaders bounds the block reads in flight for one track.
	subtitleIndexReaders = 48
	// subtitleIndexMaxLines bounds the blocks read for one track.
	subtitleIndexMaxLines = 40000
	// subtitleIndexMaxCues bounds the Cues element read.
	subtitleIndexMaxCues = 64 << 20
	subtitleBlockRead    = 4 << 10
	subtitleBlockMax     = 256 << 10
)

const (
	mkvCues          = 0x1c53bb6b
	mkvCuePoint      = 0xbb
	mkvCueTime       = 0xb3
	mkvCueTrackPos   = 0xb7
	mkvCueTrack      = 0xf7
	mkvCueClusterPos = 0xf1
	mkvCueRelPos     = 0xf0
	mkvCueDuration   = 0xb2
	mkvBlockDuration = 0x9b
	mkvCompression   = 0x5034
	mkvCompAlgo      = 0x4254
	mkvCompSettings  = 0x4255
)

type indexedLine struct {
	at, duration uint64 // Matroska ticks
	cluster, rel int64  // segment-relative cluster position, position in it
}

// indexedSubtitleVTT reads the text track FFmpeg numbers streamIndex through
// the Matroska index and returns it as WebVTT. codec is the probed codec of
// that stream; a track that does not match it is not read.
func indexedSubtitleVTT(ctx context.Context, src io.ReaderAt, size int64, streamIndex int, codec string) ([]byte, error) {
	if size <= 0 || streamIndex < 0 || !IsTextSubtitle(codec) {
		return nil, errNoSubtitleIndex
	}
	s := &nativeSource{ctx: ctx, src: src, size: size, left: nativeReadBudget + subtitleIndexMaxCues}
	head, err := s.read(0, int(min(size, nativeHeadBytes)))
	if err != nil {
		return nil, err
	}
	s.head = head
	if len(head) < 4 || binary.BigEndian.Uint32(head) != mkvEBML {
		return nil, errNoSubtitleIndex
	}
	id, _, end, err := s.header(0, size)
	if err != nil || id != mkvEBML {
		return nil, errNoSubtitleIndex
	}
	var segStart, segEnd int64
	for off, n := end, 0; off < size && n < 16; n++ {
		id, start, stop, err := s.header(off, size)
		if err != nil {
			return nil, err
		}
		if id == mkvSegment {
			segStart, segEnd = start, stop
			break
		}
		off = stop
	}
	if segStart == 0 {
		return nil, errNoSubtitleIndex
	}
	var info, tracks, cues []byte
	targets := map[uint64]int64{}
	load := func(start, stop int64, limit int64) ([]byte, error) {
		if stop-start > limit {
			return nil, errNoSubtitleIndex
		}
		return s.read(start, int(stop-start))
	}
	for off, n := segStart, 0; off < segEnd && n < 64; n++ {
		id, start, stop, err := s.header(off, segEnd)
		if err != nil {
			return nil, err
		}
		if id == mkvCluster {
			break
		}
		switch id {
		case mkvSeekHead:
			b, err := load(start, stop, nativeElementLimit)
			if err != nil {
				return nil, err
			}
			seekTargets(b, segStart, segEnd, targets)
		case mkvInfo:
			if info, err = load(start, stop, nativeElementLimit); err != nil {
				return nil, err
			}
		case mkvTracks:
			if tracks, err = load(start, stop, nativeElementLimit); err != nil {
				return nil, err
			}
		case mkvCues:
			if cues, err = load(start, stop, subtitleIndexMaxCues); err != nil {
				return nil, err
			}
		}
		if stop <= off {
			return nil, errNoSubtitleIndex
		}
		off = stop
	}
	for _, want := range []struct {
		id    uint64
		dst   *[]byte
		limit int64
	}{{mkvInfo, &info, nativeElementLimit}, {mkvTracks, &tracks, nativeElementLimit}, {mkvCues, &cues, subtitleIndexMaxCues}} {
		if *want.dst != nil {
			continue
		}
		off, ok := targets[want.id]
		if !ok {
			return nil, errNoSubtitleIndex
		}
		id, start, stop, err := s.header(off, segEnd)
		if err != nil || id != want.id {
			return nil, errNoSubtitleIndex
		}
		if *want.dst, err = load(start, stop, want.limit); err != nil {
			return nil, err
		}
	}
	scale := uint64(1000000)
	if nodes, ok := mkvNodes(info); ok {
		for _, c := range nodes {
			if c.id == mkvTimecodeScl && mkvUint(c.body) > 0 {
				scale = mkvUint(c.body)
			}
		}
	}
	track, frame, err := indexedTextTrack(tracks, streamIndex, codec)
	if err != nil {
		return nil, err
	}
	lines, err := indexedLines(cues, track.number)
	if err != nil {
		return nil, err
	}
	// mkvmerge records each track's block count in its statistics tags. An
	// index that lists fewer blocks than the track holds would drop lines.
	if off, ok := targets[mkvTags]; ok {
		if id, start, stop, err := s.header(off, segEnd); err == nil && id == mkvTags {
			if tags, err := load(start, stop, nativeElementLimit); err == nil {
				if frames, ok := trackFrameCount(tags, track.uid); ok && frames != uint64(len(lines)) {
					return nil, errNoSubtitleIndex
				}
			}
		}
	}
	r := &blockReader{ctx: ctx, src: src, size: size, segStart: segStart, track: track.number, frame: frame}
	out, err := r.readAll(lines)
	if err != nil {
		return nil, err
	}
	cuesOut := make([]subtitles.Cue, 0, len(out))
	ass := isASSCodecID(track.codecID)
	for i, line := range lines {
		text, ok := out[i], out[i] != nil
		if !ok {
			continue
		}
		body := strings.ReplaceAll(string(text), "\r\n", "\n")
		if ass {
			body = assDialogueText(body)
		}
		body = strings.TrimSpace(subtitles.CleanCueText(body))
		if body == "" {
			continue
		}
		start := time.Duration(line.at * scale)
		dur := time.Duration(r.durations[i] * scale)
		if dur <= 0 {
			dur = time.Duration(line.duration * scale)
		}
		if dur <= 0 {
			dur = 2 * time.Second
		}
		cuesOut = append(cuesOut, subtitles.Cue{Start: start, End: start + dur, Text: body})
	}
	if len(cuesOut) == 0 {
		return nil, errNoSubtitleIndex
	}
	sort.SliceStable(cuesOut, func(i, j int) bool { return cuesOut[i].Start < cuesOut[j].Start })
	return []byte(subtitles.RenderVTT(cuesOut)), nil
}

// frameDecoding undoes a track's frame ContentEncoding: header stripping
// (prefix) or zlib.
type frameDecoding struct {
	prefix []byte
	zlib   bool
}

// indexedTextTrack finds the track FFmpeg numbers streamIndex. Stream
// numbering follows the native probe, which mirrors FFmpeg's demuxer; the
// probed codec must agree, so a numbering disagreement is never read.
func indexedTextTrack(raw []byte, streamIndex int, codec string) (mkvTrack, frameDecoding, error) {
	parsed, err := mkvTracksOf(raw)
	if err != nil {
		return mkvTrack{}, frameDecoding{}, errNoSubtitleIndex
	}
	index := 0
	for _, t := range parsed {
		if !mkvTrackAccepted(t) {
			continue
		}
		if index != streamIndex {
			index++
			continue
		}
		if t.kind != 0x11 || t.encrypted || !strings.EqualFold(mkvSubtitleCodec(t.codecID), codec) {
			return mkvTrack{}, frameDecoding{}, errNoSubtitleIndex
		}
		switch id := strings.ToUpper(t.codecID); id {
		case "S_TEXT/UTF8", "S_TEXT/ASCII", "S_TEXT/ASS", "S_TEXT/SSA", "S_ASS", "S_SSA", "S_TEXT/WEBVTT":
		default:
			return mkvTrack{}, frameDecoding{}, errNoSubtitleIndex
		}
		frame, ok := trackFrameDecoding(raw, t.number)
		if !ok {
			return mkvTrack{}, frameDecoding{}, errNoSubtitleIndex
		}
		return t, frame, nil
	}
	return mkvTrack{}, frameDecoding{}, errNoSubtitleIndex
}

// mkvTrackAccepted is whether FFmpeg's matroska demuxer gives the track a
// stream index (the acceptance half of mkvTrack.stream).
func mkvTrackAccepted(t mkvTrack) bool {
	if t.kind != 1 && t.kind != 2 && t.kind != 0x11 && t.kind != 0x21 {
		return false
	}
	if t.codecID == "" {
		return false
	}
	lead := t.codecID[0]
	return !((t.kind == 1 && lead != 'V') || (t.kind == 2 && lead != 'A') || ((t.kind == 0x11 || t.kind == 0x21) && lead != 'D' && lead != 'S'))
}

func trackFrameDecoding(raw []byte, number uint64) (frameDecoding, bool) {
	entries, ok := mkvNodes(raw)
	if !ok {
		return frameDecoding{}, false
	}
	for _, e := range entries {
		if e.id != mkvTrackEntry {
			continue
		}
		fields, ok := mkvNodes(e.body)
		if !ok {
			return frameDecoding{}, false
		}
		var num uint64
		var encodings []byte
		for _, f := range fields {
			switch f.id {
			case mkvTrackNumber:
				num = mkvUint(f.body)
			case mkvEncodings:
				encodings = f.body
			}
		}
		if num != number {
			continue
		}
		var out frameDecoding
		list, _ := mkvNodes(encodings)
		for _, enc := range list {
			if enc.id != mkvEncoding {
				continue
			}
			scope, kind := uint64(1), uint64(0)
			var compression []byte
			hasCompression := false
			fs, _ := mkvNodes(enc.body)
			for _, x := range fs {
				switch x.id {
				case mkvEncScope:
					scope = mkvUint(x.body)
				case mkvEncType:
					kind = mkvUint(x.body)
				case mkvCompression:
					compression, hasCompression = x.body, true
				}
			}
			if scope&1 == 0 {
				continue
			}
			if kind != 0 || !hasCompression || len(list) > 1 {
				return frameDecoding{}, false
			}
			algo := uint64(0)
			var settings []byte
			cs, _ := mkvNodes(compression)
			for _, x := range cs {
				switch x.id {
				case mkvCompAlgo:
					algo = mkvUint(x.body)
				case mkvCompSettings:
					settings = x.body
				}
			}
			switch algo {
			case 0:
				out.zlib = true
			case 3:
				out.prefix = settings
			default:
				return frameDecoding{}, false
			}
		}
		return out, true
	}
	return frameDecoding{}, false
}

// indexedLines lists the track's indexed blocks in time order. Every block
// must carry its position inside its cluster; an index without it is not
// read block by block.
func indexedLines(cues []byte, number uint64) ([]indexedLine, error) {
	points, ok := mkvNodes(cues)
	if !ok {
		return nil, errNoSubtitleIndex
	}
	var lines []indexedLine
	for _, p := range points {
		if p.id != mkvCuePoint {
			continue
		}
		fields, ok := mkvNodes(p.body)
		if !ok {
			return nil, errNoSubtitleIndex
		}
		var at uint64
		hasTime := false
		for _, f := range fields {
			if f.id == mkvCueTime {
				at, hasTime = mkvUint(f.body), true
			}
		}
		for _, f := range fields {
			if f.id != mkvCueTrackPos {
				continue
			}
			ps, ok := mkvNodes(f.body)
			if !ok {
				return nil, errNoSubtitleIndex
			}
			var trackNo, cluster, rel, dur uint64
			hasCluster, hasRel := false, false
			for _, q := range ps {
				switch q.id {
				case mkvCueTrack:
					trackNo = mkvUint(q.body)
				case mkvCueClusterPos:
					cluster, hasCluster = mkvUint(q.body), true
				case mkvCueRelPos:
					rel, hasRel = mkvUint(q.body), true
				case mkvCueDuration:
					dur = mkvUint(q.body)
				}
			}
			if trackNo != number {
				continue
			}
			if !hasTime || !hasCluster || !hasRel || cluster > 1<<62 || rel > 1<<40 {
				return nil, errNoSubtitleIndex
			}
			lines = append(lines, indexedLine{at: at, duration: dur, cluster: int64(cluster), rel: int64(rel)})
			if len(lines) > subtitleIndexMaxLines {
				return nil, errNoSubtitleIndex
			}
		}
	}
	if len(lines) == 0 {
		return nil, errNoSubtitleIndex
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at < lines[j].at })
	return lines, nil
}

type blockReader struct {
	ctx      context.Context
	src      io.ReaderAt
	size     int64
	segStart int64
	track    uint64
	frame    frameDecoding

	mu        sync.Mutex
	headers   map[int64]int64 // cluster position -> header length
	guess     int64           // the header length clusters usually have
	durations []uint64
}

func (r *blockReader) read(off int64, n int) ([]byte, error) {
	if off < 0 || n <= 0 || off >= r.size {
		return nil, errNoSubtitleIndex
	}
	n = int(min(int64(n), r.size-off))
	b := make([]byte, n)
	var got int
	var err error
	if mr, ok := r.src.(metadataReader); ok {
		got, err = mr.ReadMetadataAtContext(r.ctx, b, off)
	} else {
		got, err = r.src.ReadAt(b, off)
	}
	if got != n && err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return b[:got], nil
}

// readAll reads every line's block concurrently. A block that cannot be read
// fails the whole track, so a partial track is never cached as complete.
func (r *blockReader) readAll(lines []indexedLine) ([][]byte, error) {
	out := make([][]byte, len(lines))
	r.durations = make([]uint64, len(lines))
	r.headers = map[int64]int64{}
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	r.ctx = ctx
	next := make(chan int)
	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	for range min(subtitleIndexReaders, len(lines)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				text, dur, err := r.line(lines[i])
				if err != nil {
					errOnce.Do(func() { firstErr = err; cancel() })
					continue
				}
				out[i], r.durations[i] = text, dur
			}
		}()
	}
feed:
	for i := range lines {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// line reads one indexed block: its payload and its BlockDuration (0 when
// the block has none).
func (r *blockReader) line(l indexedLine) ([]byte, uint64, error) {
	cluster := r.segStart + l.cluster
	r.mu.Lock()
	header, known := r.headers[cluster]
	if !known {
		header = r.guess
	}
	r.mu.Unlock()
	if header > 0 {
		if text, dur, ok, err := r.block(cluster+header+l.rel, l); err != nil || ok {
			return text, dur, err
		}
		if known {
			return nil, 0, errNoSubtitleIndex
		}
	}
	// Learn this cluster's header length, then read the block at its exact
	// position.
	b, err := r.read(cluster, 12)
	if err != nil {
		return nil, 0, err
	}
	id, ni, ok := mkvVint(b, true)
	if !ok || id != mkvCluster {
		return nil, 0, errNoSubtitleIndex
	}
	_, ns, ok := mkvVint(b[ni:], false)
	if !ok {
		return nil, 0, errNoSubtitleIndex
	}
	header = int64(ni + ns)
	r.mu.Lock()
	r.headers[cluster] = header
	if r.guess == 0 {
		r.guess = header
	}
	r.mu.Unlock()
	text, dur, ok, err := r.block(cluster+header+l.rel, l)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, 0, errNoSubtitleIndex
	}
	return text, dur, nil
}

// block reads a BlockGroup or SimpleBlock at off. ok is false when off does
// not hold a block of this track (a wrong guess of the cluster header).
func (r *blockReader) block(off int64, l indexedLine) ([]byte, uint64, bool, error) {
	b, err := r.read(off, subtitleBlockRead)
	if err != nil {
		return nil, 0, false, err
	}
	id, ni, ok := mkvVint(b, true)
	if !ok || (id != mkvBlockGroup && id != mkvSimpleBlock) {
		return nil, 0, false, nil
	}
	size, ns, ok := mkvVint(b[ni:], false)
	if !ok || size == 0 || size > subtitleBlockMax {
		return nil, 0, false, nil
	}
	total := ni + ns + int(size)
	if total > len(b) {
		if b, err = r.read(off, total); err != nil {
			return nil, 0, false, err
		}
		if len(b) < total {
			return nil, 0, false, nil
		}
	}
	body := b[ni+ns : total]
	payload, dur := body, uint64(0)
	if id == mkvBlockGroup {
		fields, ok := mkvNodes(body)
		if !ok {
			return nil, 0, false, nil
		}
		payload = nil
		for _, f := range fields {
			switch f.id {
			case mkvBlock:
				payload = f.body
			case mkvBlockDuration:
				dur = mkvUint(f.body)
			}
		}
		if payload == nil {
			return nil, 0, false, nil
		}
	}
	number, nt, ok := mkvVint(payload, false)
	if !ok || number != r.track || len(payload) < nt+3 {
		return nil, 0, false, nil
	}
	if payload[nt+2]&0x06 != 0 {
		return nil, 0, false, errNoSubtitleIndex // laced subtitle frames
	}
	data := payload[nt+3:]
	switch {
	case r.frame.zlib:
		zr, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, 0, false, errNoSubtitleIndex
		}
		inflated, err := io.ReadAll(io.LimitReader(zr, subtitleBlockMax+1))
		if err != nil || len(inflated) > subtitleBlockMax {
			return nil, 0, false, errNoSubtitleIndex
		}
		data = inflated
	case len(r.frame.prefix) > 0:
		data = append(append([]byte(nil), r.frame.prefix...), data...)
	}
	return append([]byte(nil), data...), dur, true, nil
}

func isASSCodecID(id string) bool {
	switch strings.ToUpper(id) {
	case "S_TEXT/ASS", "S_TEXT/SSA", "S_ASS", "S_SSA":
		return true
	}
	return false
}

// assDialogueText is the text of a Matroska ASS/SSA event (ReadOrder, Layer
// or Marked, Style, Name, MarginL, MarginR, MarginV, Effect, Text), with the
// italic, bold and underline overrides kept as WebVTT markup. A vector
// drawing has no text.
func assDialogueText(event string) string {
	fields := strings.SplitN(event, ",", 9)
	if len(fields) < 9 {
		return ""
	}
	text := fields[8]
	if strings.Contains(text, `\p1`) || strings.Contains(text, `\p2`) || strings.Contains(text, `\p4`) {
		return ""
	}
	return strings.NewReplacer(`\N`, "\n", `\n`, "\n", `\h`, " ",
		`{\i1}`, "<i>", `{\i0}`, "</i>", `{\b1}`, "<b>", `{\b0}`, "</b>", `{\u1}`, "<u>", `{\u0}`, "</u>").Replace(text)
}

// trackFrameCount is the NUMBER_OF_FRAMES statistics tag of the track with
// uid.
func trackFrameCount(tags []byte, uid uint64) (uint64, bool) {
	list, ok := mkvNodes(tags)
	if !ok || uid == 0 {
		return 0, false
	}
	for _, tag := range list {
		if tag.id != mkvTag {
			continue
		}
		fields, ok := mkvNodes(tag.body)
		if !ok {
			continue
		}
		target := false
		var frames string
		for _, f := range fields {
			switch f.id {
			case mkvTargets:
				ts, _ := mkvNodes(f.body)
				for _, t := range ts {
					if t.id == mkvTagTrackUID && mkvUint(t.body) == uid {
						target = true
					}
				}
			case mkvSimpleTag:
				st, _ := mkvNodes(f.body)
				var name, value string
				for _, x := range st {
					switch x.id {
					case mkvTagName:
						name = string(x.body)
					case mkvTagString:
						value = string(x.body)
					}
				}
				if name == "NUMBER_OF_FRAMES" {
					frames = value
				}
			}
		}
		if target && frames != "" {
			n, err := strconv.ParseUint(strings.TrimSpace(frames), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}
