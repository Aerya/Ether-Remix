package plex

import (
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestFirstPlayRuleSubtitleAcrossClients replays a first play: the client
// holds the pre-playback estimate (no subtitle tracks), then plays once the
// release is probed and has an English subtitle. The viewer's rules say
// English, always. It reports what each client is actually given. A rule
// subtitle the client would need burned is not burned on that first play
// (#313; owner decision on #782): it starts fast, and the rules apply once
// the client holds the probed list.
func TestFirstPlayRuleSubtitleAcrossClients(t *testing.T) {
	android := []string{
		"X-Plex-Product", "Plex for Android (TV)", "X-Plex-Version", "2026.22.0",
		"X-Plex-Platform", "Android", "X-Plex-Device", "Chromecast", "X-Plex-Model", "sabrina",
		"X-Plex-Client-Identifier", "fp-android", "X-Plex-Session-Identifier", "fp-android-play",
		"X-Plex-Client-Profile-Extra", androidMKVProfile,
	}
	web := []string{"X-Plex-Product", "Plex Web", "X-Plex-Platform", "Chrome", "X-Plex-Client-Identifier", "fp-web"}
	srt := core.StreamInfo{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"}
	pgs := core.StreamInfo{Index: 2, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "en"}
	type cse struct {
		name      string
		hdr       []string
		sub       core.StreamInfo
		query     string
		mustServe bool
		sidecar   bool
		burns     bool // the rule's track would have to be burned
	}
	base := []cse{
		{"android-tv-srt", android, srt, "&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=none", true, false, false},
		{"android-tv-pgs", android, pgs, "&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=none", false, false, true},
		{"plex-web-srt-auto-burn", web, srt, "&protocol=hls&directPlay=0&directStream=1&subtitles=burn", false, false, true},
		{"plex-web-srt-no-burn", web, srt, "&protocol=hls&directPlay=0&directStream=1&subtitles=auto", false, false, false},
	}
	var cases []cse
	for _, c := range base {
		cases = append(cases, c)
		withSidecar := c
		withSidecar.name += "+opensubtitles-cached"
		withSidecar.sidecar, withSidecar.mustServe, withSidecar.burns = true, false, false
		cases = append(cases, withSidecar)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probed := core.MediaInfo{
				Container: "mkv", Duration: 100 * time.Minute, BitrateKbps: 12000,
				Streams: []core.StreamInfo{
					{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
					{Index: 1, Type: "audio", Codec: "aac", Language: "ko", Channels: 2, Default: true},
					tc.sub,
				},
			}
			f, subs, _ := newIssue459Fixture(t, probed, "en")
			key := fightClub.Key.String()
			if !tc.sidecar {
				subs.mu.Lock()
				delete(subs.cached, fightClub.Key)
				subs.mu.Unlock()
			}

			// The detail page the client rendered: the estimate, no subtitles.
			f.playback.info = core.MediaInfo{Container: "mkv", Duration: probed.Duration, Streams: probed.Streams[:2]}
			if meta := f.get(t, "/library/metadata/"+key, tc.hdr...); meta.Code != http.StatusOK {
				t.Fatalf("detail: %d", meta.Code)
			} else {
				t.Logf("RESULT %s: detail page subtitle list before probe: %d entries, selected=%q", tc.name, len(subtitleTags(meta.Body.String())), streamAttr(selectedTag(subtitleTags(meta.Body.String())), "displayTitle"))
			}

			// Play: the release is now probed.
			f.playback.info = probed
			dec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+tc.query+"&session=fp-"+tc.name, tc.hdr...)
			if dec.Code != http.StatusOK {
				t.Fatalf("decision: %d %s", dec.Code, dec.Body.String())
			}
			selected := selectedTag(subtitleTags(dec.Body.String()))
			start := f.get(t, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+key+tc.query+"&session=fp-"+tc.name, tc.hdr...)
			if start.Code != http.StatusOK {
				t.Fatalf("start: %d %s", start.Code, start.Body.String())
			}
			hs := f.trans.sessions["fp-"+tc.name]
			if tc.burns {
				if selected != "" {
					t.Fatalf("first play selected a subtitle it would burn: %q", selected)
				}
				if hs != nil && (hs.req.Decision.BurnSubtitles || hs.req.Decision.SubtitleStream == tc.sub.Index) {
					t.Fatalf("first play burns the rule's subtitle: %+v", hs.req.Decision)
				}
				return
			}
			if streamAttr(selected, "languageCode") != "eng" {
				t.Fatalf("first play shows no English subtitle selected (decision %q)", selected)
			}
			if hs == nil {
				t.Logf("RESULT %s: decision selected=%q (%s); no HLS transcode session (served as direct play/sidecar)",
					tc.name, streamAttr(selected, "displayTitle"), streamAttr(selected, "codec"))
				return
			}
			served := hs.req.Decision.SubtitleStream == tc.sub.Index
			t.Logf("RESULT %s: decision selected=%q; embedded English in stream=%v burn=%v stream index=%d; sidecar/text tracks=%v",
				tc.name, streamAttr(selected, "displayTitle"), served, hs.req.Decision.BurnSubtitles, hs.req.Decision.SubtitleStream, hs.req.TextSubtitleStreams)
			external := streamAttr(selected, "external") == "1"
			if !external && !served {
				t.Fatalf("decision shows the embedded English subtitle but the stream does not carry it")
			}
		})
	}
}
