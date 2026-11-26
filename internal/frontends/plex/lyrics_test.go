package plex

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// Plexamp reads simple LRC only: one [mm:ss.xx] stamp per line.
func TestLyricsTextIsSimpleLRC(t *testing.T) {
	got := lyricsText(frontend.Lyrics{Lines: []frontend.LyricLine{
		{Start: 1500 * time.Millisecond, Text: "Hello"},
		{Start: 61*time.Second + 230*time.Millisecond, Text: "Later"},
	}})
	if want := "[00:01.50]Hello\n[01:01.23]Later\n"; got != want {
		t.Fatalf("lrc = %q, want %q", got, want)
	}
	if got := lyricsText(frontend.Lyrics{Plain: "One\r\nTwo\n"}); got != "One\nTwo\n" {
		t.Fatalf("plain = %q", got)
	}
	if got := lyricsText(frontend.Lyrics{}); got != "" {
		t.Fatalf("empty = %q", got)
	}
}

// The lyric stream rides on a Part that lists its streams, using its issued ID.
func TestAddLyricStream(t *testing.T) {
	const id = 340046
	media := []model.Media{
		{Part: []model.Part{{Stream: []model.Stream{{ID: 1387133930, StreamType: 2}}}}},
		{Part: []model.Part{{}}}, // a list row: no streams listed, nothing added
	}
	addLyricStream(media, id, frontend.LyricsPossible)
	streams := media[0].Part[0].Stream
	if len(streams) != 2 {
		t.Fatalf("streams = %+v", streams)
	}
	lyric := streams[1]
	if lyric.StreamType != 4 || lyric.Codec != "lrc" || (lyric.Timed == nil || !*lyric.Timed) || lyric.ID != id || lyric.Key != "/library/streams/340046" {
		t.Fatalf("lyric stream = %+v", lyric)
	}
	if len(media[1].Part[0].Stream) != 0 {
		t.Fatal("a part without streams got a lyric stream")
	}
}
