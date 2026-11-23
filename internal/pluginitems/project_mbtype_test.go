package pluginitems

import (
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// TestProjectCarriesDeclaredMBItemType proves Project stamps the MediaBrowser
// type the presentation declares for each level onto the projected title,
// and leaves unmapped levels empty so other protocols keep the video
// projection.
func TestProjectCarriesDeclaredMBItemType(t *testing.T) {
	var present core.Presentations
	if err := json.Unmarshal([]byte(`{"mediaBrowser":{"collectionType":"music","itemTypes":{"artist":"MusicArtist","album":"MusicAlbum","track":"Audio"}}}`), &present); err != nil {
		t.Fatal(err)
	}
	decl := plugins.MediaKindMeta{
		Key:   "recording",
		Label: "Recordings",
		Levels: []plugins.KindLevel{
			{Key: "artist", Label: "Artist", Container: true},
			{Key: "album", Label: "Album", Container: true},
			{Key: "track", Label: "Track", Playable: true},
			{Key: "note", Label: "Note"},
		},
	}
	want := map[string]string{"artist": "MusicArtist", "album": "MusicAlbum", "track": "Audio", "note": ""}
	for level, mb := range want {
		got := Project(core.KindItem{Level: level, Title: level}, decl, present)
		if got.MBItemType != mb {
			t.Fatalf("level %s: MBItemType = %q, want %q", level, got.MBItemType, mb)
		}
	}
	if got := Project(core.KindItem{Level: "track"}, decl, core.Presentations{}); got.MBItemType != "" {
		t.Fatalf("no presentation: MBItemType = %q, want empty", got.MBItemType)
	}
	// The core projection other protocols read is unchanged by the stamp:
	// levels still map root/middle/leaf to show/season/episode.
	if got := Project(core.KindItem{Level: "artist"}, decl, present); got.Type != core.TypeShow {
		t.Fatalf("artist core type = %q, want show projection", got.Type)
	}
	if got := Project(core.KindItem{Level: "note"}, decl, present); got.Type != core.TypeEpisode {
		t.Fatalf("leaf core type = %q, want episode projection", got.Type)
	}
}
