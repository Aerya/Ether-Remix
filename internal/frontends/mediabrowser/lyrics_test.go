package mediabrowser

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

// Jellyfin Web treats a line with a Start property as synced and converts
// play time to ticks (ms × 10,000), so synced lines carry Start in ticks and
// plain lines carry no Start key at all.
func TestJellyfinLyricDTO(t *testing.T) {
	synced := jellyfinLyricDTO(frontend.Lyrics{Lines: []frontend.LyricLine{
		{Start: 1500 * time.Millisecond, Text: "Hello 🎵 world", Words: []frontend.LyricWord{
			{Start: 1500 * time.Millisecond, End: 2 * time.Second, Text: "Hello"},
			{Start: 2 * time.Second, Text: "world"},
		}},
		{Start: 3 * time.Second, Text: "Second"},
	}})
	b, _ := json.Marshal(synced)
	var got struct {
		Metadata struct{ IsSynced bool }
		Lyrics   []map[string]any
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Metadata.IsSynced || len(got.Lyrics) != 2 {
		t.Fatalf("synced dto = %s", b)
	}
	if got.Lyrics[0]["Start"].(float64) != 15_000_000 || got.Lyrics[1]["Start"].(float64) != 30_000_000 {
		t.Fatalf("start ticks = %v, %v", got.Lyrics[0]["Start"], got.Lyrics[1]["Start"])
	}
	cues := got.Lyrics[0]["Cues"].([]any)
	first, second := cues[0].(map[string]any), cues[1].(map[string]any)
	if first["Position"].(float64) != 0 || first["EndPosition"].(float64) != 5 || first["End"].(float64) != 20_000_000 {
		t.Fatalf("first cue = %v", first)
	}
	if second["Position"].(float64) != 9 || second["EndPosition"].(float64) != 14 || second["End"] != nil {
		t.Fatalf("second cue = %v", second)
	}
	if _, ok := got.Lyrics[1]["Cues"]; ok {
		t.Fatal("a line without word timing got cues")
	}

	plain := jellyfinLyricDTO(frontend.Lyrics{Plain: "One\r\nTwo\n\n"})
	b, _ = json.Marshal(plain)
	got.Lyrics = nil
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Metadata.IsSynced || len(got.Lyrics) != 2 || got.Lyrics[0]["Text"] != "One" || got.Lyrics[1]["Text"] != "Two" {
		t.Fatalf("plain dto = %s", b)
	}
	if _, ok := got.Lyrics[0]["Start"]; ok {
		t.Fatal("a plain line carries Start, so Jellyfin Web would treat it as synced")
	}

	inst := jellyfinLyricDTO(frontend.Lyrics{Instrumental: true})
	if lines := inst["Lyrics"].([]map[string]any); len(lines) != 1 {
		t.Fatalf("instrumental dto = %v", inst)
	}
}

type lyricsHintExperience struct {
	frontendtest.Experience
	likely bool
}

func (e lyricsHintExperience) TitleLyricsAvailability(frontend.Title) frontend.LyricAvailability {
	if e.likely {
		return frontend.LyricsPossible
	}
	return frontend.LyricsAbsent
}

// Emby Web shows its lyrics button when the item's first media source lists
// a Subtitle stream, then reads the stream at DefaultSubtitleStreamIndex. The
// detail of a track with lyrics must give it exactly that, on Emby only.
func TestEmbyLyricStream(t *testing.T) {
	track := frontend.Title{Key: frontend.PluginKey(5), Title: "Song", MBItemType: "Audio"}
	newDTO := func() map[string]any {
		return map[string]any{"Id": "abc", "RunTimeTicks": int64(100), "MediaStreams": []any{map[string]any{"Type": "Audio", "Index": 0, "Codec": "flac"}}}
	}

	dto := newDTO()
	(&instance{protocol: "emby", host: frontend.Host{Experience: lyricsHintExperience{likely: true}}}).embyLyricStream(dto, track)
	sources := dto["MediaSources"].([]any)
	source := sources[0].(map[string]any)
	if source["Id"] != "abc" || source["RunTimeTicks"] != int64(100) {
		t.Fatalf("source = %v", source)
	}
	streams := source["MediaStreams"].([]any)
	var lyric map[string]any
	for _, entry := range streams {
		if s := entry.(map[string]any); s["Type"] == "Subtitle" && s["Index"] == source["DefaultSubtitleStreamIndex"] {
			lyric = s
		}
	}
	if len(streams) != 2 || lyric == nil || lyric["Index"] != 1 || lyric["Codec"] != "lrc" || lyric["IsTextSubtitleStream"] != true {
		t.Fatalf("streams = %v, default = %v", streams, source["DefaultSubtitleStreamIndex"])
	}
	if top := dto["MediaStreams"].([]any); len(top) != 2 || dto["HasSubtitles"] != true {
		t.Fatalf("top-level streams = %v", top)
	}

	// An existing source keeps its streams and gains the lyric stream.
	dto = newDTO()
	dto["MediaSources"] = []any{map[string]any{"Id": "v1", "MediaStreams": []any{map[string]any{"Type": "Audio", "Index": 3}}}}
	(&instance{protocol: "emby", host: frontend.Host{Experience: lyricsHintExperience{likely: true}}}).embyLyricStream(dto, track)
	source = dto["MediaSources"].([]any)[0].(map[string]any)
	if source["Id"] != "v1" || source["DefaultSubtitleStreamIndex"] != 4 || len(source["MediaStreams"].([]any)) != 2 {
		t.Fatalf("existing source = %v", source)
	}
	if top := dto["MediaStreams"].([]any); top[len(top)-1].(map[string]any)["Index"] != 4 {
		t.Fatalf("top-level lyric index = %v, want the source's 4", top[len(top)-1])
	}

	// A source that lists no streams still uses the item's next index.
	dto = newDTO()
	dto["MediaSources"] = []any{map[string]any{"Id": "abc", "MediaStreams": []any{}}}
	(&instance{protocol: "emby", host: frontend.Host{Experience: lyricsHintExperience{likely: true}}}).embyLyricStream(dto, track)
	source = dto["MediaSources"].([]any)[0].(map[string]any)
	if source["DefaultSubtitleStreamIndex"] != 1 {
		t.Fatalf("empty source lyric index = %v, want 1", source["DefaultSubtitleStreamIndex"])
	}

	for name, tc := range map[string]struct {
		protocol frontend.Protocol
		likely   bool
		title    frontend.Title
	}{
		"jellyfin":   {"jellyfin", true, track},
		"no lyrics":  {"emby", false, track},
		"video item": {"emby", true, frontend.Title{Key: frontend.PluginKey(6), MBItemType: "Movie"}},
	} {
		dto := newDTO()
		(&instance{protocol: tc.protocol, host: frontend.Host{Experience: lyricsHintExperience{likely: tc.likely}}}).embyLyricStream(dto, tc.title)
		if _, ok := dto["MediaSources"]; ok || len(dto["MediaStreams"].([]any)) != 1 {
			t.Fatalf("%s: dto changed: %v", name, dto)
		}
	}
}

// Emby Web's lyrics view highlights the last event whose StartPositionTicks
// is at or before the play position; plain lyrics have no timing.
func TestEmbyLyricTrackEvents(t *testing.T) {
	events := embyLyricTrackEvents(frontend.Lyrics{Lines: []frontend.LyricLine{
		{Start: 710 * time.Millisecond, Text: "Is this the real life?"},
		{Start: 6990 * time.Millisecond, Text: "Caught in a landslide"},
	}})
	if len(events) != 2 || events[0]["StartPositionTicks"] != int64(7_100_000) || events[0]["EndPositionTicks"] != int64(69_900_000) || events[1]["StartPositionTicks"] != int64(69_900_000) || events[1]["Text"] != "Caught in a landslide" {
		t.Fatalf("synced events = %v", events)
	}
	plain := embyLyricTrackEvents(frontend.Lyrics{Plain: "\nOne\r\nTwo\n\n"})
	if len(plain) != 2 || plain[0]["Text"] != "One" || plain[1]["Text"] != "Two" {
		t.Fatalf("plain events = %v", plain)
	}
	if _, ok := plain[0]["StartPositionTicks"]; ok {
		t.Fatal("a plain line carries a start")
	}
	if got := embyLyricTrackEvents(frontend.Lyrics{Instrumental: true}); len(got) != 1 || got[0]["Text"] != "♪" {
		t.Fatalf("instrumental = %v", got)
	}
}
