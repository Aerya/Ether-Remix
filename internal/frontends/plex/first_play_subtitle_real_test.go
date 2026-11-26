//go:build transcodechaos

package plex

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/mediaproc"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trackprefs"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// TestFirstPlaySubtitleRealMedia plays a cold title end to end with real
// ffprobe and ffmpeg: the client renders the pre-probe estimate (no subtitle
// tracks), then plays. The picture is black and the English subtitle is
// white, so a frame from the delivered stream shows whether the subtitle
// reached the viewer (burned) or not; text renditions are checked in the
// playlist. A subtitle the client would need burned is not burned on that
// first play (#313; owner decision on #782).
func TestFirstPlaySubtitleRealMedia(t *testing.T) {
	ff, fp := os.Getenv("CHAOS_FFMPEG"), os.Getenv("CHAOS_FFPROBE")
	if ff == "" || fp == "" {
		t.Fatal("set CHAOS_FFMPEG and CHAOS_FFPROBE")
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "en.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:30,000\nSUBTITLE TEXT HERE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "base.mp4")
	chaosCmd(t, ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=c=black:s=640x360:rate=24", "-f", "lavfi", "-i", "sine=sample_rate=48000", "-t", "30", "-c:v", "libx264", "-preset", "ultrafast", "-threads", "2", "-g", "48", "-c:a", "aac", base)
	mkSub := func(name, codec string) {
		chaosCmd(t, ff, "-hide_banner", "-loglevel", "error", "-y", "-i", base, "-i", srt, "-map", "0:v", "-map", "0:a", "-map", "1:s",
			"-c:v", "copy", "-c:a", "copy", "-c:s", codec, "-metadata:s:s:0", "language=eng", "-metadata:s:a:0", "language=kor", filepath.Join(dir, name))
	}
	// ffmpeg cannot encode a bitmap subtitle from text, so the burn path is
	// exercised through Plex Web's burn mode, which burns text subtitles via
	// the same shown-only rule as image ones, and therefore plays None.
	mkSub("text.mkv", "srt")
	source := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer source.Close()

	android := []string{
		"X-Plex-Product", "Plex for Android (TV)", "X-Plex-Version", "2026.22.0",
		"X-Plex-Platform", "Android", "X-Plex-Device", "Chromecast", "X-Plex-Model", "sabrina",
		"X-Plex-Client-Identifier", "real-android", "X-Plex-Session-Identifier", "real-android-play",
		"X-Plex-Client-Profile-Extra", androidMKVProfile,
	}
	web := []string{"X-Plex-Product", "Plex Web", "X-Plex-Platform", "Chrome", "X-Plex-Client-Identifier", "real-web"}

	for _, tc := range []struct {
		name, file string
		hdr        []string
		query      string
		burns      bool
	}{
		{"android-tv-text-subtitle", "text.mkv", android, "&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=none", false},
		{"plex-web-text-subtitle-burn-mode", "text.mkv", web, "&protocol=hls&directPlay=0&directStream=1&subtitles=burn", true},
		{"plex-web-text-subtitle-auto", "text.mkv", web, "&protocol=hls&directPlay=0&directStream=1&subtitles=auto", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := transcode.New(config.Transcode{FFmpegPath: ff, FFprobePath: fp, HWAccel: "none", TempDir: filepath.Join(t.TempDir(), "work")},
				slog.New(slog.NewTextHandler(io.Discard, nil)), transcode.WithSegmentTimeout(20*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			url := source.URL + "/" + tc.file
			probed, err := m.Probe(context.Background(), url)
			if err != nil {
				t.Fatal(err)
			}
			probed.Keyframes = nil
			f := newFixture()
			t.Cleanup(func() { f.srv.Close(); m.Close() })
			f.srv.setPlayback(&chaosPlayback{f.playback, url})
			f.srv.setTranscoder(m)
			prefs := trackprefs.New(store.OpenTest(t))
			f.srv.setTrackPreferences(prefs)
			rules := trackprefs.Default()
			rules.SubtitleLanguages, rules.SubtitleMode = []string{"en"}, "always"
			if err := prefs.PutRules(context.Background(), ownerUser.ID, rules); err != nil {
				t.Fatal(err)
			}
			key := fightClub.Key.String()

			// The detail page from the estimate: no subtitle tracks.
			var estimate core.MediaInfo
			estimate.Container, estimate.Duration = probed.Container, probed.Duration
			for _, st := range probed.Streams {
				if st.Type != "subtitle" {
					estimate.Streams = append(estimate.Streams, st)
				}
			}
			f.playback.info = estimate
			meta := f.get(t, "/library/metadata/"+key, tc.hdr...)
			if meta.Code != 200 || len(subtitleTags(meta.Body.String())) != 0 {
				t.Fatalf("detail %d listed subtitles: %v", meta.Code, subtitleTags(meta.Body.String()))
			}

			// Play: the release is probed now.
			f.playback.info = *probed
			session := "real-" + tc.name
			dec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+tc.query+"&session="+session, tc.hdr...)
			if dec.Code != 200 {
				t.Fatalf("decision %d: %s", dec.Code, dec.Body.String())
			}
			selected := selectedTag(subtitleTags(dec.Body.String()))
			start := f.get(t, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+key+tc.query+"&session="+session, tc.hdr...)
			if start.Code != 200 {
				t.Fatalf("start %d: %s", start.Code, start.Body.String())
			}
			playlist := start.Body.String()
			textRendition := strings.Contains(playlist, "TYPE=SUBTITLES")

			// Follow the playlist to the first media segment and decode a frame.
			seg := firstSegment(t, f, tc.hdr, "/video/:/transcode/universal/start.m3u8", playlist, 0)
			p := filepath.Join(t.TempDir(), "seg")
			if err := os.WriteFile(p, seg, 0600); err != nil {
				t.Fatal(err)
			}
			yMax := frameYMax(t, ff, p)
			burned := yMax > 150
			muxed := segmentHasSubtitle(t, fp, p)
			// A sidecar the client fetches itself from the decision's key.
			sidecar := false
			if k := streamAttr(selected, "key"); k != "" {
				if rec := f.get(t, k, tc.hdr...); rec.Code == 200 && strings.Contains(rec.Body.String(), "SUBTITLE TEXT HERE") {
					sidecar = true
				}
			}
			// Plex Web reads a converted text track (location="segments-subs")
			// from the universal subtitles endpoint, as from PMS.
			if !sidecar && streamAttr(selected, "location") == "segments-subs" {
				subURL := "/video/:/transcode/universal/subtitles?path=/library/metadata/" + key + "&session=" + session + "&format=ass"
				for attempt := 0; attempt < 40 && !sidecar; attempt++ {
					if rec := f.get(t, subURL, tc.hdr...); rec.Code == 200 && strings.Contains(rec.Body.String(), "SUBTITLE TEXT HERE") {
						sidecar = true
						break
					}
					time.Sleep(500 * time.Millisecond)
				}
			}
			t.Logf("RESULT %s: decision selected=%q; frame luma max=%d (burned=%v); subtitle stream inside segment=%v; text rendition in playlist=%v; subtitle fetched from its own endpoint=%v",
				tc.name, streamAttr(selected, "displayTitle"), yMax, burned, muxed, textRendition, sidecar)
			if tc.burns {
				if burned || selected != "" {
					t.Fatalf("first play burned the rule's subtitle (burned=%v selected=%q)", burned, selected)
				}
				f.get(t, "/video/:/transcode/universal/stop?session="+session, tc.hdr...)
				return
			}
			if !burned && !textRendition && !muxed && !sidecar {
				t.Logf("DEBUG %s selected tag: %s", tc.name, selected)
				t.Logf("DEBUG %s start playlist:\n%s", tc.name, playlist)
				t.Fatalf("first play delivered no English subtitle")
			}
			if streamAttr(selected, "languageCode") != "eng" {
				t.Fatalf("decision shows no English subtitle selected (%q)", selected)
			}
			f.get(t, "/video/:/transcode/universal/stop?session="+session, tc.hdr...)
		})
	}
}

var playlistLine = regexp.MustCompile(`(?m)^[^#\s][^\r\n]*$`)

// firstSegment follows HLS playlists from body to the first media segment.
func firstSegment(t *testing.T, f *fixture, hdr []string, from, body string, depth int) []byte {
	t.Helper()
	if depth > 4 {
		t.Fatal("playlist nesting too deep")
	}
	line := playlistLine.FindString(body)
	if line == "" {
		t.Fatalf("no URI in playlist:\n%s", body)
	}
	target := line
	if !strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "http") {
		target = from[:strings.LastIndex(from, "/")+1] + target
	}
	if i := strings.Index(target, "://"); i >= 0 {
		target = target[strings.Index(target[i+3:], "/")+i+3:]
	}
	var init []byte
	if mm := regexp.MustCompile(`#EXT-X-MAP:URI="([^"]+)"`).FindStringSubmatch(body); mm != nil {
		u := mm[1]
		if !strings.HasPrefix(u, "/") {
			u = from[:strings.LastIndex(from, "/")+1] + u
		}
		for attempt := 0; attempt < 40; attempt++ {
			if r := f.get(t, u, hdr...); r.Code == 200 && r.Body.Len() > 0 {
				init = r.Body.Bytes()
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	var rec *httptest.ResponseRecorder
	for attempt := 0; attempt < 40; attempt++ {
		rec = f.get(t, target, hdr...)
		if rec.Code == 200 && rec.Body.Len() > 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if rec.Code != 200 {
		t.Fatalf("GET %s: %d %s", target, rec.Code, rec.Body.String())
	}
	path := target
	if q := strings.Index(path, "?"); q >= 0 {
		path = path[:q]
	}
	if strings.HasSuffix(path, ".m3u8") {
		return firstSegment(t, f, hdr, path, rec.Body.String(), depth+1)
	}
	return append(init, rec.Body.Bytes()...)
}

// segmentHasSubtitle reports whether the delivered segment muxes a subtitle.
func segmentHasSubtitle(t *testing.T, fp, segment string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := mediaproc.CommandContext(ctx, time.Minute, fp, "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", segment).CombinedOutput()
	if err != nil {
		t.Logf("ffprobe segment: %v %s", err, out)
		return false
	}
	return strings.Contains(string(out), "subtitle")
}

// frameYMax is the brightest luma value in a frame 1s into a segment.
func frameYMax(t *testing.T, ff, segment string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := mediaproc.CommandContext(ctx, 2*time.Minute, ff, "-hide_banner", "-v", "info", "-i", segment, "-ss", "1", "-frames:v", "1",
		"-vf", "signalstats,metadata=print:key=lavfi.signalstats.YMAX", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Logf("frame not decodable: %v %s", err, out)
		return 0
	}
	m := regexp.MustCompile(`lavfi\.signalstats\.YMAX=(\d+)`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("no YMAX in ffmpeg output: %s", out)
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}
