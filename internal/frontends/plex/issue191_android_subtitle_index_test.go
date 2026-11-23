package plex

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trackprefs"
)

// The selected subtitle of an Android Matroska decision has to carry its
// source index. Plex for Android 2026.22.0 (decompiled) copies that row
// into the engine at playback start. selectStream passes a null index as
// -1 (plex.js selectStream), and TrackSelector treats -1 as Disabled
// (bh/s.java), which is the log "Selecting subtitle stream with index -1"
// measured while the panel still showed the track selected. On a transcode
// the same function then replaces any present index with 2 for a subtitle;
// the Matroska mux writes one video, one audio and this subtitle, so 2 is
// that track. A burn stays without an index: -1 leaves the text renderer
// off and the cues are in the picture.

func androidIndexInfo(sub core.StreamInfo) core.MediaInfo {
	return core.MediaInfo{
		Container: "mkv", Duration: 1400 * time.Second, BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
			{Index: 1, Type: "audio", Codec: "aac", Language: "en", Channels: 2, Default: true},
			{Index: 3, Type: "audio", Codec: "ac3", Language: "fr", Channels: 6},
			sub,
			{Index: 5, Type: "subtitle", Codec: "subrip", Language: "fr"},
		},
	}
}

func androidIndexFixture(t *testing.T, info core.MediaInfo, language string) *fixture {
	t.Helper()
	f, _ := newSubtitleFixture(t)
	f.playback.info = info
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	prefs := trackprefs.New(db)
	f.srv.setTrackPreferences(prefs)
	rules := trackprefs.Default()
	rules.SubtitleLanguages = []string{language}
	rules.SubtitleMode = "always"
	if err := prefs.PutRules(context.Background(), ownerUser.ID, rules); err != nil {
		t.Fatal(err)
	}
	return f
}

// androidMKVProfile is the transcode target Plex for Android sends. Without
// container=mkv, subtitles=none clears the encoder's subtitle stream and the
// decision answers ignore, which is a different player path.
const androidMKVProfile = "add-transcode-target(type=videoProfile&context=all&protocol=hls&container=mkv&videoCodec=h264,hevc&audioCodec=aac,eac3&subtitleCodec=ass,ssa,pgs,srt)"

func withMKVTarget(headers []string) []string {
	return append(append([]string{}, headers...), "X-Plex-Client-Profile-Extra", androidMKVProfile)
}

func androidIndexDecision(t *testing.T, f *fixture, headers []string, query string, dec *core.Decision) string {
	t.Helper()
	if dec != nil {
		f.trans.decision = *dec
	}
	key := fightClub.Key.String()
	if rec := f.get(t, "/library/metadata/"+key, headers...); rec.Code != 200 {
		t.Fatalf("detail: %d", rec.Code)
	}
	rec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+"&session=android-index&"+query, headers...)
	if rec.Code != 200 {
		t.Fatalf("decision: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestAndroidMatroskaDecisionKeepsTheSelectedSubtitleIndex(t *testing.T) {
	clients := []struct {
		name    string
		headers []string
	}{
		{"mobile", []string{"X-Plex-Product", "Plex for Android (Mobile)", "X-Plex-Platform", "Android", "X-Plex-Device", "Pixel 9 Pro", "X-Plex-Client-Identifier", "android-index-mobile"}},
		{"tv", []string{"X-Plex-Product", "Plex for Android (TV)", "X-Plex-Platform", "Android", "X-Plex-Device", "SHIELD Android TV", "X-Plex-Client-Identifier", "android-index-tv"}},
	}
	tracks := []struct {
		name  string
		sub   core.StreamInfo
		index string
	}{
		{"srt", core.StreamInfo{Index: 4, Type: "subtitle", Codec: "subrip", Language: "en"}, "4"},
		{"ass", core.StreamInfo{Index: 4, Type: "subtitle", Codec: "ass", Language: "en"}, "4"},
		{"srt-late", core.StreamInfo{Index: 7, Type: "subtitle", Codec: "subrip", Language: "en"}, "7"},
	}
	remux := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "mkv"}
	query := "directPlay=0&directStream=1&protocol=hls&hasMDE=1&subtitles=none"
	for _, client := range clients {
		for _, track := range tracks {
			t.Run(client.name+"/"+track.name, func(t *testing.T) {
				f := androidIndexFixture(t, androidIndexInfo(track.sub), "en")
				body := androidIndexDecision(t, f, withMKVTarget(client.headers), query, &remux)
				subs := subtitleTags(body)
				selected := selectedTag(subs)
				if streamAttr(selected, "index") != track.index {
					t.Fatalf("selected index = %q, want %s\n%s", streamAttr(selected, "index"), track.index, selected)
				}
				if streamAttr(selected, "decision") != "copy" || streamAttr(selected, "location") != "segments-av" {
					t.Fatalf("selected delivery = %s", selected)
				}
				var other string
				for _, tag := range subs {
					if tag != selected && streamAttr(tag, "codec") == "srt" {
						other = tag
					}
				}
				if streamAttr(other, "index") != "5" {
					t.Fatalf("unselected subtitle index = %q, want 5\n%s", streamAttr(other, "index"), other)
				}
				// The same selectStream rewrite turns a truthy audio index
				// into 1. Leaving the selected audio index off keeps the
				// path that already plays. Video is not a text track.
				for _, tag := range audioTags(body) {
					if strings.Contains(tag, `selected="1"`) && streamAttr(tag, "index") != "" {
						t.Fatalf("selected audio index = %q, want omitted\n%s", streamAttr(tag, "index"), tag)
					}
				}
				if got := streamAttr(audioByLang(audioTags(body), "fra"), "index"); got != "3" {
					t.Fatalf("unselected audio index = %q, want 3", got)
				}
				for _, tag := range streamTagRe.FindAllString(body, -1) {
					if strings.Contains(tag, `streamType="1"`) && streamAttr(tag, "index") != "" {
						t.Fatalf("video index = %q, want omitted\n%s", streamAttr(tag, "index"), tag)
					}
				}
			})
		}
	}
}

func TestAndroidIgnoredSubtitleCarriesNoPlayerIndex(t *testing.T) {
	sub := core.StreamInfo{Index: 4, Type: "subtitle", Codec: "subrip", Language: "en"}
	remux := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "mpegts"}
	mobile := []string{"X-Plex-Product", "Plex for Android (Mobile)", "X-Plex-Platform", "Android", "X-Plex-Device", "Pixel 9 Pro", "X-Plex-Client-Identifier", "android-index-ignore"}
	f := androidIndexFixture(t, androidIndexInfo(sub), "en")
	body := androidIndexDecision(t, f, mobile, "directPlay=0&directStream=1&protocol=hls&hasMDE=1&subtitles=none", &remux)
	selected := selectedTag(subtitleTags(body))
	if streamAttr(selected, "decision") != "ignore" {
		t.Fatalf("non-matroska row: %s", selected)
	}
	if got := streamAttr(selected, "index"); got != "" {
		t.Fatalf("ignored subtitle index = %q, want omitted so the player does not ask for a track the mux left out", got)
	}
}

func TestAndroidSubtitleOffLeavesNoSelectedTrack(t *testing.T) {
	sub := core.StreamInfo{Index: 4, Type: "subtitle", Codec: "subrip", Language: "en"}
	remux := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "mkv"}
	f := androidIndexFixture(t, androidIndexInfo(sub), "en")
	body := androidIndexDecision(t, f, androidPhone, "directPlay=0&directStream=1&protocol=hls&hasMDE=1&subtitles=none&subtitleStreamID=0", &remux)
	if selected := selectedTag(subtitleTags(body)); selected != "" {
		t.Fatalf("off selected a subtitle the player would enable: %s", selected)
	}
}

func TestAndroidBurnedSubtitleCarriesNoPlayerIndex(t *testing.T) {
	sub := core.StreamInfo{Index: 6, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "es"}
	info := core.MediaInfo{
		Container: "mkv", Duration: 1400 * time.Second, BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
			{Index: 1, Type: "audio", Codec: "aac", Language: "en", Channels: 2, Default: true},
			sub,
		},
	}
	burn := core.Decision{Kind: core.DecisionTranscode, VideoCopy: true, AudioCopy: true, Container: "mkv", BurnSubtitles: true}
	f := androidIndexFixture(t, info, "es")
	body := androidIndexDecision(t, f, androidPhone, "directPlay=0&directStream=1&protocol=hls&hasMDE=1&subtitles=none", &burn)
	selected := selectedTag(subtitleTags(body))
	if streamAttr(selected, "codec") != "pgs" || streamAttr(selected, "decision") != "burn" {
		t.Fatalf("burn row: %s", selected)
	}
	if got := streamAttr(selected, "index"); got != "" {
		t.Fatalf("burned subtitle index = %q, want none so the player leaves text disabled", got)
	}
}

func TestOtherClientsOmitTheSelectedSubtitleIndex(t *testing.T) {
	sub := core.StreamInfo{Index: 4, Type: "subtitle", Codec: "subrip", Language: "en"}
	remux := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "mkv"}
	clients := []struct {
		name    string
		headers []string
	}{
		{"web", []string{"X-Plex-Product", "Plex Web", "X-Plex-Platform", "Chrome", "X-Plex-Client-Identifier", "web-index"}},
		{"ios", []string{"X-Plex-Product", "Plex for iOS", "X-Plex-Platform", "iOS", "X-Plex-Client-Identifier", "ios-index"}},
		{"tvos", appleTV},
	}
	query := "directPlay=0&directStream=1&protocol=hls&hasMDE=1&subtitles=none"
	for _, client := range clients {
		t.Run(client.name, func(t *testing.T) {
			f := androidIndexFixture(t, androidIndexInfo(sub), "en")
			body := androidIndexDecision(t, f, withMKVTarget(client.headers), query, &remux)
			selected := selectedTag(subtitleTags(body))
			if streamAttr(selected, "decision") != "copy" || streamAttr(selected, "location") != "segments-av" {
				t.Fatalf("%s did not take the in-segment row: %s", client.name, selected)
			}
			if got := streamAttr(selected, "index"); got != "" {
				t.Fatalf("%s selected index = %q, want it omitted", client.name, got)
			}
		})
	}
}

func TestAndroidDirectPlayKeepsTheSourceSubtitleIndex(t *testing.T) {
	sub := core.StreamInfo{Index: 4, Type: "subtitle", Codec: "subrip", Language: "en"}
	direct := core.Decision{Kind: core.DecisionDirectPlay, Container: "mkv"}
	f := androidIndexFixture(t, androidIndexInfo(sub), "en")
	body := androidIndexDecision(t, f, androidPhone, "directPlay=1&directStream=1&protocol=http&hasMDE=1&subtitles=none", &direct)
	selected := selectedTag(subtitleTags(body))
	if streamAttr(selected, "index") != "4" {
		t.Fatalf("direct play index = %q, want 4\n%s", streamAttr(selected, "index"), selected)
	}
}
