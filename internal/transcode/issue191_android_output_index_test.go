package transcode

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Plex for Android 2026.22.0 discards the subtitle index on a transcode and
// asks the extractor for format id 2 (selectStreamOnEngine, stream type 3).
// That id is the output stream index, so a Matroska run is one video, the
// selected audio, then the selected subtitle. The source index only has to
// be present and truthy; the decision test pins which number is written.
// An unselected audio track stays out of the mux, or the subtitle would
// land past 2 and the player would miss it.

func streamMaps(args []string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-map" {
			out = append(out, args[i+1])
		}
	}
	return out
}

func indexLayout(audio, extra, sub int) core.MediaInfo {
	return core.MediaInfo{Container: "mkv", Duration: time.Minute, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
		{Index: audio, Type: "audio", Codec: "aac", Language: "en", Channels: 2, SampleRate: 48000},
		{Index: extra, Type: "audio", Codec: "ac3", Language: "fr", Channels: 6, SampleRate: 48000},
		{Index: sub, Type: "subtitle", Codec: "subrip", Language: "en"},
	}}
}

func TestMatroskaMuxPutsTheSelectedSubtitleAtOutputIndexTwo(t *testing.T) {
	layouts := []struct{ audio, extra, sub int }{
		{1, 3, 2},
		{1, 3, 4},
		{1, 3, 7},
		{3, 1, 8},
		{6, 2, 15},
	}
	for _, lay := range layouts {
		t.Run(fmt.Sprintf("a%d-s%d", lay.audio, lay.sub), func(t *testing.T) {
			d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioCopy: true, AudioStream: lay.audio, SubtitleStream: lay.sub}
			maps := streamMaps(buildArgs(baseSpec(indexLayout(lay.audio, lay.extra, lay.sub), d)))
			want := []string{"0:0", fmt.Sprintf("0:%d", lay.audio), fmt.Sprintf("0:%d", lay.sub)}
			if !slices.Equal(maps, want) {
				t.Fatalf("maps = %q, want %q", maps, want)
			}
		})
	}
}

func TestSilenceAudioKeepsTheSubtitleAtOutputIndexTwo(t *testing.T) {
	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioCopy: true, AudioStream: 1, SubtitleStream: 4}
	s := baseSpec(indexLayout(1, 3, 4), d)
	s.SilenceAudio = true
	maps := streamMaps(buildArgs(s))
	want := []string{"0:0", "1:a:0", "0:4"}
	if !slices.Equal(maps, want) {
		t.Fatalf("maps = %q, want %q", maps, want)
	}
}

func TestBurnedSubtitleIsNotAnOutputStream(t *testing.T) {
	d := core.Decision{Kind: core.DecisionTranscode, Container: ContainerMatroska, VideoCopy: true, AudioCopy: true, AudioStream: 1, SubtitleStream: 8, BurnSubtitles: true}
	args := buildArgs(baseSpec(indexLayout(1, 3, 8), d))
	maps := streamMaps(args)
	want := []string{"0:0", "0:1"}
	if !slices.Equal(maps, want) {
		t.Fatalf("maps = %q, want %q", maps, want)
	}
	mustContain(t, args, "-sn")
	mustNotContain(t, args, "-map 0:8")
}
