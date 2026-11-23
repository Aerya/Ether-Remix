package plex

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// Contract: the start.m3u8 served to Plex for Android for a Matroska HLS
// session declares no CODECS, and every other client/transport keeps it. With
// CODECS declared, the app's media3 HLS source builds its tracks from the
// master alone and drops the subrip stream muxed into the Matroska segments,
// so the subtitle is listed and ticked in the panel but never drawn.
//
// This test serves the real start.m3u8 for a real decision (registry quirk,
// transcode.Decide, writeMasterPlaylist), because the bug lived in the joint
// between those pieces and was invisible to each piece's own tests.

// The app finds CODECS with an unanchored CODECS="..." pattern, so this one is
// unanchored too: SUPPLEMENTAL-CODECS="..." must count as a hit.
var plexMasterCodecs = regexp.MustCompile(`CODECS="[^"]*"`)

func TestStartMasterCodecsFollowClientTransport(t *testing.T) {
	extra := "add-direct-play-profile(type=videoProfile&container=mkv&videoCodec=h264,hevc&audioCodec=aac,eac3,ac3,dts,flac)+add-transcode-target(type=videoProfile&context=all&protocol=hls&container=%s&videoCodec=h264,hevc&audioCodec=mp3,aac,ac3,eac3,dts,flac,vorbis)"
	phone := []string{"X-Plex-Product", "Plex for Android (Mobile)", "X-Plex-Platform", "Android", "X-Plex-Device", "Pixel 9 Pro", "X-Plex-Model", "komodo"}
	apple := []string{"X-Plex-Product", "Plex for Apple TV", "X-Plex-Platform", "tvOS", "X-Plex-Device", "Apple TV"}
	shield := []string{"X-Plex-Product", "Plex for Android (TV)", "X-Plex-Platform", "Android", "X-Plex-Device", "SHIELD Android TV", "X-Plex-Model", "mdarcy"}
	for _, tc := range []struct {
		name       string
		client     []string
		container  string // the HLS container the client's transcode target asks for
		dv         bool   // Dolby Vision profile 8.1 source, copied to a client the owner enabled Dolby Vision for
		wantCodecs bool
	}{
		{"Android Matroska HLS declares no CODECS", phone, "mkv", false, false},
		{"Android fMP4 HLS keeps CODECS", phone, "mp4", false, true},
		{"Apple TV Matroska HLS keeps CODECS", apple, "mkv", false, true},
		// Dolby Vision is off for Plex for Android unless the owner turns it
		// on. When it is on the copy keeps its DV signalling, and the master
		// must still hand the app nothing it reads as CODECS: SUPPLEMENTAL-CODECS
		// would be read that way (the app's pattern is unanchored).
		{"Android TV Matroska HLS, Dolby Vision copy, declares no CODECS", shield, "mkv", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(func(d *testServerDeps) {
				base := depsConfig(d)
				setDepsConfig(d, func() config.Config {
					c := base()
					c.Quality = config.DefaultQuality()
					if tc.dv {
						on := true
						c.Clients.Overrides = []core.ClientCapabilityOverride{{Product: "Plex for Android (TV)", DolbyVision: &on, DolbyVisionProfiles: []int{8}}}
					}
					return c
				})
			})
			defer f.srv.Close()
			f.srv.setTranscoder(&realDecider{f.trans})
			// A usenet source is remuxed rather than direct played, which is
			// the path that reaches the segmented Matroska transport.
			f.playback.resolution = &core.Resolution{Release: core.Release{ID: "nzb-191", Protocol: "usenet"}}
			video := core.StreamInfo{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, FrameRate: 23.976}
			if tc.dv {
				video = core.StreamInfo{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160, BitDepth: 10, FrameRate: 23.976,
					ColorTransfer: "smpte2084", DolbyVision: true, DolbyVisionProfile: 8, DolbyVisionLevel: 6, DolbyVisionCompatibilityID: 1}
			}
			f.playback.info = core.MediaInfo{Container: "mkv", BitrateKbps: 5000, Streams: []core.StreamInfo{
				video,
				{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Default: true},
				{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng"},
			}}
			hdr := append(append([]string{}, tc.client...), "X-Plex-Client-Identifier", "client-191", "X-Plex-Client-Profile-Name", "Generic",
				"X-Plex-Client-Profile-Extra", strings.Replace(extra, "%s", tc.container, 1))
			query := "path=%2Flibrary%2Fmetadata%2F" + fightClub.Key.String() + "&hasMDE=1&directPlay=0&directStream=1&protocol=hls&session=issue191-" + tc.container + "&subtitleStreamID=3&subtitles=embedded"
			if rec := f.get(t, "/video/:/transcode/universal/decision?"+query, append(jsonAccept, hdr...)...); rec.Code != http.StatusOK {
				t.Fatalf("decision: %d %s", rec.Code, rec.Body.String())
			}
			rec := f.get(t, "/video/:/transcode/universal/start.m3u8?"+query, hdr...)
			if rec.Code != http.StatusOK {
				t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()

			// Guard the premise: the session really was the transport under test.
			var got core.Decision
			f.trans.mu.Lock()
			for _, h := range f.trans.sessions {
				got = h.req.Decision
			}
			f.trans.mu.Unlock()
			wantMatroska := tc.container == "mkv"
			if (got.Container == transcode.ContainerMatroska) != wantMatroska {
				t.Fatalf("decision container %q, want matroska=%v: %+v", got.Container, wantMatroska, got)
			}
			if got.AndroidMatroskaHLS != strings.HasPrefix(tc.client[1], "Plex for Android") {
				t.Fatalf("quirk flag does not follow the client: %+v", got)
			}
			if tc.dv && (!got.VideoCopy || got.StripDolbyVision) {
				t.Fatalf("the Dolby Vision case must copy the stream with its DV signalling, or it proves nothing: %+v", got)
			}

			if strings.Count(body, "#EXT-X-STREAM-INF") != 1 {
				t.Fatalf("want a single variant:\n%s", body)
			}
			if has := plexMasterCodecs.MatchString(body); has != tc.wantCodecs {
				t.Fatalf("CODECS present=%v, want %v:\n%s", has, tc.wantCodecs, body)
			}
			// Whatever else changes, the picture signalling must stay.
			wantRange := ",VIDEO-RANGE=SDR"
			if tc.dv {
				wantRange = ",VIDEO-RANGE=PQ"
			}
			if !strings.Contains(body, wantRange) {
				t.Fatalf("%s lost:\n%s", wantRange, body)
			}
			// Subtitles stay muxed in the segments (as a real PMS does); the
			// master must not add a sidecar subtitle rendition group.
			if strings.Contains(body, "#EXT-X-MEDIA") && strings.Contains(body, "TYPE=SUBTITLES") {
				t.Fatalf("subtitles must stay muxed in the segments, not a rendition group:\n%s", body)
			}
		})
	}
}
