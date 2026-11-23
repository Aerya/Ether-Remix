package vela

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestMediaSummaryUsesProbedVersionTracksWhenDetailTracksEstimated(t *testing.T) {
	// Estimated tracks with no probed version stay omitted.
	estimated := frontend.ItemDetail{
		TracksEstimated: true,
		Tracks: []frontend.StreamInfo{
			{Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Type: "audio", Codec: "eac3", Channels: 6, Language: "ja"},
		},
		Versions: []frontend.Version{
			{ID: "r1", Media: frontend.MediaInfo{Estimated: true, Streams: []frontend.StreamInfo{{Type: "audio", Language: "ja"}}}},
		},
	}
	if summary := mediaSummary(estimated); summary != nil {
		t.Fatalf("estimated detail exposed mediaSummary: %#v", summary)
	}

	// Once a version is probed, mediaSummary surfaces its real video and audio tracks.
	probed := frontend.ItemDetail{
		TracksEstimated: true,
		Versions: []frontend.Version{
			{
				ID: "r1",
				Media: frontend.MediaInfo{
					Estimated: false,
					Streams: []frontend.StreamInfo{
						{Type: "video", Codec: "hevc", Width: 1920, Height: 1080},
						{Type: "audio", Codec: "eac3", Channels: 6, Language: "jpn"},
						{Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"},
					},
				},
			},
		},
	}
	summary := mediaSummary(probed)
	if summary == nil {
		t.Fatal("probed version produced nil mediaSummary")
	}
	audio, _ := summary["audio"].([]object)
	if len(audio) != 2 || audio[0]["language"] != "jpn" || audio[1]["language"] != "eng" {
		t.Fatalf("mediaSummary audio = %#v, want [jpn eng]", audio)
	}
}
