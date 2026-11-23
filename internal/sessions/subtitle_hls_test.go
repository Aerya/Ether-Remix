package sessions

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// swiftfinAVPlayerCaps is the subtitle part of Swiftfin's AVPlayer profile.
func swiftfinAVPlayerCaps() core.ClientCaps {
	return core.ClientCaps{Profiles: &core.ClientCapabilityProfiles{Subtitles: []core.ClientSubtitleProfile{
		{Format: "cc_dec", Method: "Embed"}, {Format: "ttml", Method: "Embed"},
		{Format: "pgssub", Method: "Encode"}, {Format: "dvdsub", Method: "Encode"},
		{Format: "vtt", Method: "Hls"},
	}}}
}

func hlsTestMedia() core.MediaInfo {
	return core.MediaInfo{Size: 6 << 30, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "hevc"},
		{Index: 1, Type: "audio", Codec: "eac3"},
		{Index: 3, Type: "subtitle", Codec: "subrip", Language: "por", Title: "Brazilian (Forced)", Forced: true},
		{Index: 12, Type: "subtitle", Codec: "subrip", Language: "eng", Title: "SDH"},
		{Index: 13, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "eng"},
		{Index: 14, Type: "subtitle", Codec: "webvtt", Language: "nob"},
		{Index: 15, Type: "subtitle", Codec: "subrip", Language: "fin", External: true},
	}}
}

// A client that takes WebVTT by "Hls" gets the embedded SRT and WebVTT tracks
// as renditions; bitmap and sidecar tracks, other protocols and clients
// without that profile keep their delivery.
func TestHLSTextTracks(t *testing.T) {
	info := hlsTestMedia()
	if got := hlsTextTracks("jellyfin", swiftfinAVPlayerCaps(), info); !slices.Equal(got, []int{3, 12, 14}) {
		t.Fatalf("tracks %v", got)
	}
	// Small sources too: the client cannot read a sidecar at all.
	info.Size = 1 << 20
	if got := hlsTextTracks("jellyfin", swiftfinAVPlayerCaps(), info); !slices.Equal(got, []int{3, 12, 14}) {
		t.Fatalf("small source tracks %v", got)
	}
	for _, protocol := range []string{"plex", "emby", "vela"} {
		if got := hlsTextTracks(protocol, swiftfinAVPlayerCaps(), info); got != nil {
			t.Fatalf("%s: %v", protocol, got)
		}
	}
	if got := hlsTextTracks("jellyfin", core.ClientCaps{}, info); got != nil {
		t.Fatalf("no profile: %v", got)
	}
	external := core.ClientCaps{Profiles: &core.ClientCapabilityProfiles{Subtitles: []core.ClientSubtitleProfile{{Format: "vtt", Method: "External"}, {Format: "srt", Method: "External"}}}}
	if got := hlsTextTracks("jellyfin", external, info); got != nil {
		t.Fatalf("External client: %v", got)
	}
	// The client's first matching text profile decides.
	externalFirst := core.ClientCaps{Profiles: &core.ClientCapabilityProfiles{Subtitles: []core.ClientSubtitleProfile{{Format: "subrip", Method: "Embed"}, {Format: "subrip", Method: "External"}, {Format: "vtt", Method: "Hls"}}}}
	if got := hlsTextTracks("jellyfin", externalFirst, info); got != nil {
		t.Fatalf("External before Hls: %v", got)
	}
	hlsFirst := core.ClientCaps{Profiles: &core.ClientCapabilityProfiles{Subtitles: []core.ClientSubtitleProfile{{Format: "WebVTT", Method: "hls"}, {Format: "vtt", Method: "External"}}}}
	if got := hlsTextTracks("jellyfin", hlsFirst, info); !slices.Equal(got, []int{3, 12, 14}) {
		t.Fatalf("Hls before External: %v", got)
	}
	// The progressive web player and a native HLS client share one tee.
	both := swiftfinAVPlayerCaps()
	both.Profiles.Subtitles = append(both.Profiles.Subtitles, core.ClientSubtitleProfile{Format: progressiveSubtitleFormat, Method: "External"})
	info.Size = 6 << 30
	if got := textSubtitleStreams("jellyfin", both, info); !slices.Equal(got, []int{3, 12, 14}) {
		t.Fatalf("union %v", got)
	}
}

type hlsTextTestSession struct {
	fakeMediaSession
	tracks []int
}

func (s *hlsTextTestSession) HasTextSubtitle(index int) bool { return slices.Contains(s.tracks, index) }
func (s *hlsTextTestSession) ServeTextSubtitlePlaylist(w http.ResponseWriter, _ *http.Request, index int) {
	_, _ = fmt.Fprintf(w, "playlist-%d", index)
}
func (s *hlsTextTestSession) ServeTextSubtitleSegment(w http.ResponseWriter, _ *http.Request, index, n int) {
	_, _ = fmt.Fprintf(w, "cues-%d-%d", index, n)
}

// The master lists each carried track as a SUBTITLES rendition the variant
// references, with the viewer's selection as the default.
func TestMasterListsTextRenditions(t *testing.T) {
	info := hlsTestMedia()
	play := &fakePlayback{sessions: map[string]*core.PlaySession{"p": {ID: "p", Info: info}}}
	bindings := NewBindingTable[frontend.PlayIntent]()
	bindings.Put(&Binding[frontend.PlayIntent]{ID: "s", Decision: core.Decision{Container: "fmp4"}})
	broker := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Playback: play}, bindings: bindings}
	session := &brokerSession{public: frontend.Session{ID: "s", Protocol: "jellyfin"}, playID: "p", caps: swiftfinAVPlayerCaps(), format: frontend.StreamHLS, opts: core.DecideOptions{SubtitleStream: 12}}
	hs := &hlsTextTestSession{tracks: []int{3, 12, 14}}
	w := httptest.NewRecorder()
	broker.serveMaster(w, httptest.NewRequest(http.MethodGet, "/", nil), session, hs)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %q", w.Code, body)
	}
	for _, want := range []string{
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="Brazilian (Forced) (3)",LANGUAGE="por",DEFAULT=NO,AUTOSELECT=YES,FORCED=YES,URI="text-3/index.m3u8"`,
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="SDH (12)",LANGUAGE="eng",DEFAULT=YES,AUTOSELECT=YES,FORCED=NO,URI="text-12/index.m3u8"`,
		`NAME="nob (14)"`,
		`,SUBTITLES="subs"` + "\nbase/index.m3u8\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("master lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "text-13") || strings.Contains(body, "text-15") {
		t.Fatalf("bitmap or sidecar track listed:\n%s", body)
	}
	if strings.Count(w.Body.String(), "#EXTM3U") != 1 || w.Header().Get("Content-Length") != fmt.Sprint(w.Body.Len()) {
		t.Fatalf("malformed master: %q", body)
	}
	// Other clients' masters are unchanged.
	session.caps = core.ClientCaps{}
	w = httptest.NewRecorder()
	broker.serveMaster(w, httptest.NewRequest(http.MethodGet, "/", nil), session, hs)
	if strings.Contains(w.Body.String(), "SUBTITLES") {
		t.Fatalf("ordinary client got renditions:\n%s", w.Body.String())
	}
}

func TestServeTextRendition(t *testing.T) {
	hs := &hlsTextTestSession{tracks: []int{12}}
	for _, tc := range []struct {
		tail    []string
		handled bool
		code    int
		body    string
	}{
		{[]string{"text-12", "index.m3u8"}, true, 200, "playlist-12"},
		{[]string{"text-12", "00007.vtt"}, true, 200, "cues-12-7"},
		{[]string{"text-13", "index.m3u8"}, true, 404, ""},
		{[]string{"text-12", "00007.ts"}, true, 404, ""},
		{[]string{"text-x", "index.m3u8"}, true, 404, ""},
		{[]string{"base", "index.m3u8"}, false, 200, ""},
		{[]string{"text-12"}, false, 200, ""},
	} {
		w := httptest.NewRecorder()
		handled := serveTextRendition(w, httptest.NewRequest(http.MethodGet, "/", nil), hs, tc.tail)
		if handled != tc.handled || w.Code != tc.code || (tc.body != "" && w.Body.String() != tc.body) {
			t.Fatalf("%v: handled=%v code=%d body=%q", tc.tail, handled, w.Code, w.Body.String())
		}
	}
	// A session that cannot tee text answers 404 rather than falling through.
	w := httptest.NewRecorder()
	if !serveTextRendition(w, httptest.NewRequest(http.MethodGet, "/", nil), &fakeMediaSession{}, []string{"text-12", "index.m3u8"}) || w.Code != 404 {
		t.Fatalf("plain session: %d", w.Code)
	}
}

// The rendition URL exists only for an HLS session carrying the track.
func TestHLSTextRenditionURL(t *testing.T) {
	info := hlsTestMedia()
	b := &Broker{deps: BrokerDeps{Transcoder: progressiveTestTranscoder{session: &hlsTextTestSession{tracks: []int{3, 12, 14}}}}}
	s := &brokerSession{public: frontend.Session{ID: "s", Protocol: "jellyfin"}, grant: "g", caps: swiftfinAVPlayerCaps(), format: frontend.StreamHLS}
	if url, ok := b.hlsTextRenditionURL(s, info, 12); !ok || url != "/sessions/media/s/g/text-12/index.m3u8" {
		t.Fatalf("rendition %q %v", url, ok)
	}
	for _, index := range []int{13, 15, 99} {
		if url, ok := b.hlsTextRenditionURL(s, info, index); ok {
			t.Fatalf("track %d: %q", index, url)
		}
	}
	s.format = frontend.StreamDirect
	if _, ok := b.hlsTextRenditionURL(s, info, 12); ok {
		t.Fatal("direct stream has renditions")
	}
	// The running session is authoritative when it did not tee the track.
	s.format = frontend.StreamHLS
	b.deps.Transcoder = progressiveTestTranscoder{session: &hlsTextTestSession{tracks: []int{3}}}
	if _, ok := b.hlsTextRenditionURL(s, info, 12); ok {
		t.Fatal("untee'd track has a rendition")
	}
}
