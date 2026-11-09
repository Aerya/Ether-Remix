package music

import (
	"bytes"
	"encoding/binary"
)

// inspectAudio uses the bytes, never a provider's quality label or MIME header.
// Container-only matches deliberately do not imply a particular codec.
func inspectAudio(b []byte, rep *SourceReport) bool {
	switch {
	case len(b) >= 42 && bytes.Equal(b[:4], []byte("fLaC")):
		// The first mandatory metadata block is STREAMINFO (34 bytes).
		if b[4]&0x7f != 0 || b[5] != 0 || b[6] != 0 || b[7] != 34 {
			return false
		}
		info := binary.BigEndian.Uint64(b[18:26])
		rep.SampleRate = int(info >> 44)
		rep.BitDepth = int((info>>36)&31) + 1
		if rep.SampleRate == 0 {
			return false
		}
		rep.Format = "FLAC"
		rep.Lossless = true
		rep.HiRes = rep.BitDepth >= 24 && rep.SampleRate > 48000
		return true
	case len(b) >= 12 && bytes.Equal(b[4:8], []byte("ftyp")):
		rep.Format = "MP4/M4A (codec non vérifié)"
		return true
	case len(b) >= 4 && bytes.Equal(b[:4], []byte("OggS")):
		if bytes.Contains(b, []byte("OpusHead")) {
			rep.Format = "Opus"
			return true
		}
		if bytes.Contains(b, []byte("\x01vorbis")) {
			rep.Format = "Vorbis"
			return true
		}
		return false
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WAVE")):
		rep.Format = "WAV (codec non vérifié)"
		return true
	}
	// Skip an ID3v2 tag; do not mistake metadata alone for playable MP3.
	if len(b) >= 10 && bytes.Equal(b[:3], []byte("ID3")) {
		n := 10 + int(b[6]&127)<<21 + int(b[7]&127)<<14 + int(b[8]&127)<<7 + int(b[9]&127)
		if n >= len(b) {
			return false
		}
		b = b[n:]
	}
	if len(b) >= 4 && b[0] == 0xff {
		if b[1]&0xf6 == 0xf0 {
			rep.Format = "AAC"
			return true
		}
		if b[1]&0xe0 == 0xe0 && b[1]&0x18 != 0x08 && b[1]&6 != 0 && b[2]&0xf0 != 0xf0 && b[2]&0xf0 != 0 && b[2]&0x0c != 0x0c {
			rep.Format = "MP3"
			return true
		}
	}
	return false
}
