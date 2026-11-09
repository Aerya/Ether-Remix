package music

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestAudioEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		b      []byte
		ok     bool
		format string
	}{
		{"html", []byte("<html>not audio</html>"), false, ""},
		{"json", []byte(`{"quality":"LOSSLESS"}`), false, ""},
		{"truncated flac", []byte("fLaC"), false, ""},
		{"metadata only", []byte("ID3"), false, ""},
		{"mp3", []byte{255, 251, 144, 0}, true, "MP3"},
		{"aac", []byte{255, 241, 80, 128}, true, "AAC"},
		{"m4a", []byte("\x00\x00\x00\x18ftypM4A "), true, "MP4/M4A (codec non vérifié)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r SourceReport
			if got := inspectAudio(tc.b, &r); got != tc.ok || r.Format != tc.format || r.Lossless {
				t.Fatalf("%v %+v", got, r)
			}
		})
	}
	for _, bits := range []int{16, 24} {
		b := make([]byte, 42)
		copy(b, "fLaC")
		b[7] = 34
		binary.BigEndian.PutUint64(b[18:26], uint64(96000)<<44|uint64(bits-1)<<36)
		var r SourceReport
		if !inspectAudio(b, &r) || !r.Lossless || r.BitDepth != bits || r.SampleRate != 96000 || r.HiRes != (bits == 24) {
			t.Fatalf("%+v", r)
		}
	}
	if got := guessMime("", "HI_RES_LOSSLESS", "/audio"); got != "" {
		t.Fatalf("unproven MIME %s", got)
	}
}

func TestStreamExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	if got := streamExpiry(Stream{ExpiresAt: 1020}, now); !got.Equal(time.Unix(1015, 0)) {
		t.Fatal(got)
	}
	if got := streamExpiry(Stream{}, now); !got.Equal(now.Add(streamTTL)) {
		t.Fatal(got)
	}
}
