package transcode

import (
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Plex for Android 2026.22.0, on a decision that is not direct play, drops
// the source subtitle index and asks the Matroska mux for format id 2
// (selectStreamOnEngine, stream type 3): video, selected audio, subtitle.
// These tests pin the -map order buildArgs emits. Direct play never reaches
// buildArgs.

func noAudioStreamMaps(args []string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-map" {
			out = append(out, args[i+1])
		}
	}
	return out
}

func noAudioMatroska(sub int) core.MediaInfo {
	return core.MediaInfo{Container: "mkv", Duration: 10 * time.Minute, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
		{Index: sub, Type: "subtitle", Codec: "subrip", Language: "en"},
	}}
}

func TestMatroskaNoAudioMapsSubtitleSecond(t *testing.T) {
	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioStream: -1, SubtitleStream: 4}
	args := buildArgs(baseSpec(noAudioMatroska(4), d))
	want := []string{"0:0", "0:4"}
	if maps := noAudioStreamMaps(args); !slices.Equal(maps, want) {
		t.Fatalf("maps = %q, want %q", maps, want)
	}
	mustNotContain(t, args, "anullsrc")
}

func TestAndroidNoAudioPutsSubtitleAtOutputIndexTwo(t *testing.T) {
	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioStream: -1, SubtitleStream: 4, AndroidMatroskaHLS: true}
	s := baseSpec(noAudioMatroska(4), d)
	args := buildArgs(s)
	want := []string{"0:0", "1:a:0", "0:4"}
	if maps := noAudioStreamMaps(args); !slices.Equal(maps, want) {
		t.Fatalf("maps = %q, want %q", maps, want)
	}
	remain := secs(s.Info.Duration - s.Start)
	mustContain(t, args, "-t "+remain+" -f lavfi -i anullsrc=r=48000:cl=2c", "-c:a aac -ac 2 -ar 48000 -b:a 192k")
	mustNotContain(t, args, "-c:a copy", "-re -f lavfi")
}

func TestAndroidSilenceStaysOffWhenAudioIsMappedOrClientIsNotAndroid(t *testing.T) {
	withAudio := core.MediaInfo{Container: "mkv", Duration: 10 * time.Minute, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
		{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000},
		{Index: 4, Type: "subtitle", Codec: "subrip", Language: "en"},
	}}
	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioCopy: true, AudioStream: 1, SubtitleStream: 4}
	plain := buildArgs(baseSpec(withAudio, d))
	d.AndroidMatroskaHLS = true
	flagged := buildArgs(baseSpec(withAudio, d))
	if !slices.Equal(plain, flagged) {
		t.Fatalf("android flag changed a mapped-audio command\nplain: %q\nflagged: %q", plain, flagged)
	}
	want := []string{"0:0", "0:1", "0:4"}
	if maps := noAudioStreamMaps(flagged); !slices.Equal(maps, want) {
		t.Fatalf("maps = %q, want %q", maps, want)
	}
	mustNotContain(t, flagged, "anullsrc")

	off := buildArgs(baseSpec(noAudioMatroska(4), core.Decision{Kind: core.DecisionRemux, Container: ContainerMatroska, VideoCopy: true, AudioStream: -1, SubtitleStream: 4}))
	mustNotContain(t, off, "anullsrc")
}
