package vela

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestAudioQualityWireNamesTheStreamPlayed(t *testing.T) {
	bits := 24
	q := audioQualityWire(frontend.MediaInfo{BitrateKbps: 2304, Streams: []frontend.StreamInfo{
		{Type: "video", Codec: "mjpeg"}, // cover art
		{Type: "audio", Codec: "flac", SampleRate: 96000, BitsPerRawSample: &bits},
	}})
	if q["codec"] != "flac" || q["bitDepth"] != 24 || q["sampleRateHz"] != 96000 || q["lossless"] != true || q["bitrateKbps"] != 2304 {
		t.Fatalf("flac: %v", q)
	}
	lossy := audioQualityWire(frontend.MediaInfo{Streams: []frontend.StreamInfo{{Type: "audio", Codec: "mp3", SampleRate: 44100, BitrateKbps: 320, SampleFormat: "s16p"}}})
	if lossy["lossless"] != nil || lossy["bitDepth"] != nil || lossy["bitrateKbps"] != 320 {
		t.Fatalf("mp3: %v", lossy)
	}
	if audioQualityWire(frontend.MediaInfo{}) != nil {
		t.Fatal("no audio stream")
	}
}
