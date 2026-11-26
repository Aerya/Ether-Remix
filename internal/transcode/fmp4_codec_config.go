package transcode

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// An fMP4 init must describe every track's decoder configuration: an AAC
// sample entry's esds needs its AudioSpecificConfig (DecoderSpecificInfo),
// an avc1/hvc1 entry a complete avcC/hvcC. MSE players refuse an init
// without them (Chrome: CHUNK_DEMUXER_ERROR_APPEND_FAILED).
//
// FFmpeg's HLS muxer forces the nested mp4 muxer to write the moov at the
// first segment cut, whether or not every track has seen a packet by then.
// AAC copied from ADTS (MPEG-TS, HLS, raw AAC) has no configuration until
// aac_adtstoasc derives it from the first audio packet, so when the source's
// audio starts after the first cut (a demuxed HLS input whose audio
// playlist lags, an ad break, an encoder switch) the init goes out without
// one. Such a run also writes the first ADTS frame to a side file (see
// adtsConfigOutput); the session builds the same AudioSpecificConfig
// aac_adtstoasc would and splices it into the init before publishing it.

var errNoAudioConfig = errors.New("fMP4 init has no AAC AudioSpecificConfig")

// initCodecConfig checks every sample entry of an fMP4 init. missingAAC
// reports an AAC entry without an AudioSpecificConfig (repairable);
// video names an avc1/hvc1 entry whose avcC/hvcC lacks a parameter set.
// FFmpeg's MP4 muxer writes the record from the stream's extradata and does
// not rebuild it from length-prefixed packets, so a copy whose source record
// is empty publishes an empty one. Copies of such sources run the
// h264_metadata/hevc_metadata filter, which publishes the in-band sets as
// extradata before the header (see mp4CopyParameterSets, #783); video is
// reported for diagnosis only. err is an init this reader cannot walk; it is
// judged no further.
func initCodecConfig(init []byte) (missingAAC bool, video string, err error) {
	entries, err := initSampleEntries(init)
	if err != nil {
		return false, "", err
	}
	for _, e := range entries {
		switch e.box.typ {
		case "mp4a":
			esds, ok := e.child(init, "esds")
			if !ok {
				continue
			}
			if d, err := parseESDS(esds.payload(init)); err == nil && d.needsASC() && d.dsi == nil {
				missingAAC = true
			}
		case "avc1":
			if c, ok := e.child(init, "avcC"); !ok || !avcCComplete(c.payload(init)) {
				video = "avc1 without a complete avcC (SPS and PPS)"
			}
		case "hvc1":
			if c, ok := e.child(init, "hvcC"); !ok || !hvcCComplete(c.payload(init)) {
				video = "hvc1 without a complete hvcC (VPS, SPS and PPS)"
			}
		}
	}
	return missingAAC, video, nil
}

// CheckInitCodecConfig reports an fMP4 init whose AAC track a player could
// not configure its decoder for.
func CheckInitCodecConfig(init []byte) error {
	if missing, _, _ := initCodecConfig(init); missing {
		return errNoAudioConfig
	}
	return nil
}

// withAACConfig returns init with asc as the DecoderSpecificInfo of every
// AAC sample entry that lacks one; box sizes up to the moov are adjusted.
func withAACConfig(init, asc []byte) ([]byte, error) {
	if len(asc) < 2 {
		return nil, errors.New("AudioSpecificConfig too short")
	}
	out := init
	for {
		entries, err := initSampleEntries(out)
		if err != nil {
			return nil, err
		}
		done := true
		for _, e := range entries {
			if e.box.typ != "mp4a" {
				continue
			}
			esds, ok := e.child(out, "esds")
			if !ok {
				continue
			}
			d, err := parseESDS(esds.payload(out))
			if err != nil {
				return nil, err
			}
			if !d.needsASC() || d.dsi != nil {
				continue
			}
			payload := d.withDSI(asc)
			box := make([]byte, 8+len(payload))
			binary.BigEndian.PutUint32(box, uint32(len(box)))
			copy(box[4:], "esds")
			copy(box[8:], payload)
			if out, err = replaceBox(out, append(e.path, e.box), esds, box); err != nil {
				return nil, err
			}
			done = false
			break // offsets moved: walk again
		}
		if done {
			return out, nil
		}
	}
}

// replaceBox swaps old (a box inside the ancestors chain) for repl and
// grows or shrinks each ancestor's size by the difference.
func replaceBox(b []byte, ancestors []mp4Box, old mp4Box, repl []byte) ([]byte, error) {
	delta := len(repl) - (old.end - old.start)
	out := make([]byte, 0, len(b)+delta)
	out = append(out, b[:old.start]...)
	out = append(out, repl...)
	out = append(out, b[old.end:]...)
	for _, a := range ancestors {
		switch a.hdr {
		case 8:
			size := int64(binary.BigEndian.Uint32(out[a.start:])) + int64(delta)
			if binary.BigEndian.Uint32(out[a.start:]) == 0 || size < 8 || size > 0xffffffff {
				return nil, fmt.Errorf("fmp4: cannot resize %q", a.typ)
			}
			binary.BigEndian.PutUint32(out[a.start:], uint32(size))
		case 16:
			size := int64(binary.BigEndian.Uint64(out[a.start+8:])) + int64(delta)
			binary.BigEndian.PutUint64(out[a.start+8:], uint64(size))
		}
	}
	return out, nil
}

// sampleEntry is one stsd entry and the boxes enclosing it (moov first).
type sampleEntry struct {
	path []mp4Box
	box  mp4Box
	kids int // offset of the entry's child boxes within b, or -1
}

func (e sampleEntry) child(b []byte, typ string) (mp4Box, bool) {
	if e.kids < 0 || e.kids > e.box.end {
		return mp4Box{}, false
	}
	kids, _ := readBoxes(b, e.kids, e.box.end)
	for _, k := range kids {
		if k.typ == typ {
			return k, true
		}
	}
	return mp4Box{}, false
}

// initSampleEntries lists the sample entries of every track in init's moov.
func initSampleEntries(b []byte) ([]sampleEntry, error) {
	top, err := readBoxes(b, 0, len(b))
	if err != nil {
		return nil, err
	}
	var out []sampleEntry
	found := false
	for _, moov := range top {
		if moov.typ != "moov" {
			continue
		}
		found = true
		traks, err := readBoxes(b, moov.start+moov.hdr, moov.end)
		if err != nil {
			return nil, err
		}
		for _, trak := range traks {
			if trak.typ != "trak" {
				continue
			}
			chain := []mp4Box{moov, trak}
			cur := trak
			for _, t := range []string{"mdia", "minf", "stbl", "stsd"} {
				next, ok := child(b, cur, t)
				if !ok {
					return nil, fmt.Errorf("fmp4: trak without %s", t)
				}
				chain = append(chain, next)
				cur = next
			}
			stsd := cur
			if stsd.end-stsd.start < stsd.hdr+8 {
				return nil, errShortBox
			}
			entries, err := readBoxes(b, stsd.start+stsd.hdr+8, stsd.end)
			if err != nil {
				return nil, err
			}
			handler := ""
			if hdlr, ok := path(b, trak, "mdia", "hdlr"); ok && len(hdlr.payload(b)) >= 12 {
				handler = string(hdlr.payload(b)[8:12])
			}
			for _, e := range entries {
				kids := -1
				switch handler {
				case "vide":
					kids = e.start + e.hdr + 78
				case "soun":
					p := e.payload(b)
					if len(p) >= 28 {
						kids = e.start + e.hdr + 28
						switch binary.BigEndian.Uint16(p[8:]) {
						case 1:
							kids += 16
						case 2:
							kids += 36
						}
					}
				}
				out = append(out, sampleEntry{path: append([]mp4Box(nil), chain...), box: e, kids: kids})
			}
		}
	}
	if !found {
		return nil, errors.New("fmp4: init without moov")
	}
	return out, nil
}

// esDescriptor is a parsed esds payload: the ES_Descriptor around its
// DecoderConfigDescriptor, kept as byte ranges so it can be re-serialised.
type esDescriptor struct {
	prefix  []byte // version/flags
	esHead  []byte // ES_ID, flags and optional fields
	dcdHead []byte // the DecoderConfigDescriptor's 13 fixed bytes
	dsi     []byte // DecoderSpecificInfo body, nil when absent
	dcdRest []byte // descriptors after the DSI inside the DCD
	esRest  []byte // descriptors after the DCD inside the ES_Descriptor (SLConfig)
	tail    []byte // bytes after the ES_Descriptor
}

// needsASC reports an MPEG-4 or MPEG-2 AAC object type, which a decoder
// cannot configure without its DecoderSpecificInfo.
func (d esDescriptor) needsASC() bool {
	switch d.dcdHead[0] {
	case 0x40, 0x66, 0x67, 0x68:
		return true
	}
	return false
}

func (d esDescriptor) withDSI(asc []byte) []byte {
	dcd := append(append(append([]byte(nil), d.dcdHead...), descriptor(0x05, asc)...), d.dcdRest...)
	es := append(append(append([]byte(nil), d.esHead...), descriptor(0x04, dcd)...), d.esRest...)
	return append(append(append([]byte(nil), d.prefix...), descriptor(0x03, es)...), d.tail...)
}

// descriptor encodes an MPEG-4 descriptor with a four-byte length, as
// FFmpeg writes them.
func descriptor(tag byte, body []byte) []byte {
	n := len(body)
	return append([]byte{tag, byte(n>>21) | 0x80, byte(n>>14) | 0x80, byte(n>>7) | 0x80, byte(n) & 0x7f}, body...)
}

// readDescriptor splits d into the first descriptor's tag and body and the
// bytes after it.
func readDescriptor(d []byte) (tag byte, body, rest []byte, ok bool) {
	if len(d) < 2 {
		return 0, nil, nil, false
	}
	tag = d[0]
	n, i := 0, 1
	for ; i < len(d) && i <= 4; i++ {
		n = n<<7 | int(d[i]&0x7f)
		if d[i]&0x80 == 0 {
			i++
			break
		}
	}
	if i+n > len(d) {
		return 0, nil, nil, false
	}
	return tag, d[i : i+n], d[i+n:], true
}

func parseESDS(p []byte) (esDescriptor, error) {
	var d esDescriptor
	if len(p) < 4 {
		return d, errShortBox
	}
	d.prefix = p[:4]
	tag, es, tail, ok := readDescriptor(p[4:])
	if !ok || tag != 0x03 || len(es) < 3 {
		return d, errors.New("esds without an ES_Descriptor")
	}
	d.tail = tail
	head := 3
	flags := es[2]
	if flags&0x80 != 0 {
		head += 2
	}
	if flags&0x40 != 0 {
		if head >= len(es) {
			return d, errShortBox
		}
		head += 1 + int(es[head])
	}
	if flags&0x20 != 0 {
		head += 2
	}
	if head > len(es) {
		return d, errShortBox
	}
	d.esHead = es[:head]
	tag, dcd, esRest, ok := readDescriptor(es[head:])
	if !ok || tag != 0x04 || len(dcd) < 13 {
		return d, errors.New("esds without a DecoderConfigDescriptor")
	}
	d.esRest = esRest
	d.dcdHead = dcd[:13]
	d.dcdRest = dcd[13:]
	if tag, dsi, rest, ok := readDescriptor(dcd[13:]); ok && tag == 0x05 && len(dsi) > 0 {
		d.dsi, d.dcdRest = dsi, rest
	}
	return d, nil
}

// adtsAudioSpecificConfig builds the AudioSpecificConfig aac_adtstoasc
// derives from an ADTS frame: object type (profile+1), sampling frequency
// index and channel configuration, 1024-sample frames, no extension. ADTS
// cannot signal SBR or PS explicitly, so HE-AAC stays implicit exactly as
// in the source. A channel configuration of zero (a PCE) is not handled.
func adtsAudioSpecificConfig(frame []byte) ([]byte, error) {
	if len(frame) < 7 || frame[0] != 0xff || frame[1]&0xf6 != 0xf0 {
		return nil, errors.New("not an ADTS frame")
	}
	profile := frame[2] >> 6
	sf := (frame[2] >> 2) & 0x0f
	channels := (frame[2]&1)<<2 | frame[3]>>6
	if sf >= 13 {
		return nil, fmt.Errorf("ADTS sampling frequency index %d", sf)
	}
	if channels == 0 {
		return nil, errors.New("ADTS channel configuration in a PCE")
	}
	aot := profile + 1
	return []byte{aot<<3 | sf>>1, (sf&1)<<7 | channels<<3}, nil
}

// avcCComplete reports an AVCDecoderConfigurationRecord with at least one
// SPS and one PPS, all within the box.
func avcCComplete(p []byte) bool {
	sps, pps, ok := avcCSets(p)
	return ok && sps > 0 && pps > 0
}

// avcCSets counts the SPS and PPS entries of an AVCDecoderConfigurationRecord.
// ok is false for a record that does not parse: another version, an entry
// running past the box, an empty entry, or no PPS count.
func avcCSets(p []byte) (sps, pps int, ok bool) {
	if len(p) < 7 || p[0] != 1 {
		return 0, 0, false
	}
	i := 6
	entries := func(count int) bool {
		for range count {
			if i+2 > len(p) {
				return false
			}
			n := int(binary.BigEndian.Uint16(p[i:]))
			i += 2 + n
			if n == 0 || i > len(p) {
				return false
			}
		}
		return true
	}
	sps = int(p[5] & 0x1f)
	if !entries(sps) || i >= len(p) {
		return 0, 0, false
	}
	pps = int(p[i])
	i++
	if !entries(pps) {
		return 0, 0, false
	}
	return sps, pps, true
}

// hvcCComplete reports an HEVCDecoderConfigurationRecord carrying a VPS, an
// SPS and a PPS, all within the box.
func hvcCComplete(p []byte) bool {
	have, _, ok := hvcCSets(p)
	return ok && have[32] && have[33] && have[34]
}

// hvcCSets reads an HEVCDecoderConfigurationRecord: have marks each NAL type
// with a non-empty entry, nals counts every entry. ok is false for a record
// that does not parse (another version, an array or entry past the box).
func hvcCSets(p []byte) (have map[byte]bool, nals int, ok bool) {
	if len(p) < 23 || p[0] != 1 {
		return nil, 0, false
	}
	have = map[byte]bool{}
	i := 23
	for range int(p[22]) {
		if i+3 > len(p) {
			return nil, 0, false
		}
		typ := p[i] & 0x3f
		count := int(binary.BigEndian.Uint16(p[i+1:]))
		i += 3
		for range count {
			if i+2 > len(p) {
				return nil, 0, false
			}
			n := int(binary.BigEndian.Uint16(p[i:]))
			i += 2 + n
			if i > len(p) {
				return nil, 0, false
			}
			nals++
			if n > 0 {
				have[typ] = true
			}
		}
	}
	return have, nals, true
}

// initCodecConfigLocked holds run r's fMP4 init, and with it the run's
// segments, until it carries every track's decoder configuration. An AAC
// configuration the HLS muxer could not know yet is built from the run's
// first ADTS frame once that has been written; an init that cannot be
// completed fails the run instead of reaching a player.
func (s *Session) initCodecConfigLocked(r *run, exited bool) (bool, error) {
	if r.initConfigured || s.container != ContainerFMP4 || s.isDASH() || r.spec.InitName == "" {
		return true, nil
	}
	initPath := filepath.Join(r.spec.Dir, r.spec.InitName)
	b, err := os.ReadFile(initPath)
	if err != nil {
		// FFmpeg writes the init before it lists a segment; noteInitLocked
		// publishes nothing without one.
		return true, nil
	}
	missing, video, err := initCodecConfig(b)
	if video != "" {
		s.log.Warn("fMP4 init carries an incomplete video configuration", "run", r.seq, "detail", video)
	}
	if err != nil || !missing {
		r.initConfigured = true
		return true, nil
	}
	side, ok := r.spec.adtsConfigOutput()
	if !ok {
		return false, fmt.Errorf("transcode: %w", errNoAudioConfig)
	}
	frame, err := os.ReadFile(side)
	if err != nil || len(frame) < 7 {
		if exited {
			return false, fmt.Errorf("transcode: %w and no audio frame arrived", errNoAudioConfig)
		}
		if !r.initHeld {
			r.initHeld = true
			s.log.Info("fMP4 init held until the first audio frame", "run", r.seq)
		}
		return false, nil
	}
	asc, err := adtsAudioSpecificConfig(frame)
	if err != nil {
		return false, fmt.Errorf("transcode: %w: %w", errNoAudioConfig, err)
	}
	fixed, err := withAACConfig(b, asc)
	if err != nil {
		return false, fmt.Errorf("transcode: complete fMP4 init: %w", err)
	}
	if missing, _, err := initCodecConfig(fixed); err != nil || missing {
		return false, fmt.Errorf("transcode: complete fMP4 init: %w", errors.Join(err, errNoAudioConfig))
	}
	if err := writeFileAtomic(initPath, fixed); err != nil {
		return false, fmt.Errorf("transcode: complete fMP4 init: %w", err)
	}
	r.initConfigured = true
	s.log.Info("fMP4 init completed with the AAC configuration of the first audio frame", "run", r.seq, "asc", fmt.Sprintf("%x", asc))
	return true, nil
}

// writeFileAtomic replaces path with b through a renamed temporary file, so
// a reader sees the old or the new bytes, never a mix.
func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".config.tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
