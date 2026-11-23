package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/subtext"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trackprefs"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type issue459HLSSession struct {
	*fakeHLS
	textTracks []int
}

func (s *issue459HLSSession) HasTextSubtitle(index int) bool {
	return slices.Contains(s.textTracks, index)
}

func (s *issue459HLSSession) TextSubtitleCues(int, time.Duration, time.Duration) ([]subtext.Cue, error) {
	return nil, nil
}

func (s *issue459HLSSession) ServeTextSubtitlePlaylist(http.ResponseWriter, *http.Request, int)     {}
func (s *issue459HLSSession) ServeTextSubtitleSegment(http.ResponseWriter, *http.Request, int, int) {}

type issue459Transcoder struct {
	*fakeTranscoder
}

func (t *issue459Transcoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	t.mu.Lock()
	t.lastOpts, t.lastCaps = opts, caps
	t.mu.Unlock()
	return transcode.Decide(info, caps, opts, transcode.Policy{})
}

func (t *issue459Transcoder) StartHLS(ctx context.Context, req core.HLSRequest) (core.HLSSession, error) {
	base, err := t.fakeTranscoder.StartHLS(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(req.TextSubtitleStreams) == 0 {
		return base, nil
	}
	return &issue459HLSSession{fakeHLS: base.(*fakeHLS), textTracks: append([]int(nil), req.TextSubtitleStreams...)}, nil
}

func newIssue459Fixture(t *testing.T, info core.MediaInfo, language string) (*fixture, *fakeSubtitles, *trackprefs.Service) {
	t.Helper()
	f, subs := newSubtitleFixture(t)
	f.playback.info = info
	f.playback.resolution = &core.Resolution{
		Release:        core.Release{ID: "debrid-459", Name: "Interstellar.2014.2160p.REMUX", Protocol: core.ProtocolHTTP},
		HealthVerified: true,
	}
	f.srv.setTranscoder(&issue459Transcoder{fakeTranscoder: f.trans})
	prefs := trackprefs.New(store.OpenTest(t))
	f.srv.setTrackPreferences(prefs)
	rules := trackprefs.Default()
	rules.SubtitleLanguages = []string{language}
	rules.SubtitleMode = "always"
	if err := prefs.PutRules(context.Background(), ownerUser.ID, rules); err != nil {
		t.Fatal(err)
	}
	return f, subs, prefs
}

// TestInitialRuleSubtitleStartsOnAndroidTV reproduces #459 on Plex for Android TV
// 2026.22.0 (Chromecast sabrina): the viewer's rule selects Dutch without a prior
// PUT /library/parts, /decision marks Dutch selected, and ExoPlayer's start.m3u8
// omits X-Plex-* profile headers and either omits subtitleStreamID or sends -1.
func TestInitialRuleSubtitleStartsOnAndroidTV(t *testing.T) {
	sabrinaHeaders := []string{
		"X-Plex-Product", "Plex for Android (TV)",
		"X-Plex-Version", "2026.22.0",
		"X-Plex-Platform", "Android",
		"X-Plex-Device", "Chromecast",
		"X-Plex-Model", "sabrina",
		"X-Plex-Client-Identifier", "sabrina-459",
		"X-Plex-Session-Identifier", "play-sabrina-459",
		"X-Plex-Client-Profile-Extra", androidMKVProfile,
	}
	for _, tc := range []struct {
		name        string
		sub         core.StreamInfo
		echoOnDec   bool
		startSubArg string
		decDirect   string
		freshStart  bool
		wantBurn    bool
	}{
		{
			name:       "srt-mde-handoff-omitted-id",
			sub:        core.StreamInfo{Index: 4, Type: "subtitle", Codec: "subrip", Language: "nl"},
			echoOnDec:  true,
			decDirect:  "1",
			freshStart: false,
		},
		{
			name:        "srt-same-settings-default-minus-one",
			sub:         core.StreamInfo{Index: 4, Type: "subtitle", Codec: "subrip", Language: "nl"},
			echoOnDec:   true,
			startSubArg: "&subtitleStreamID=-1",
			decDirect:   "0",
			freshStart:  false,
		},
		{
			name:        "srt-fresh-session-id-on-start",
			sub:         core.StreamInfo{Index: 4, Type: "subtitle", Codec: "subrip", Language: "nl"},
			echoOnDec:   false,
			startSubArg: "&subtitleStreamID=-1",
			decDirect:   "1",
			freshStart:  true,
		},
		{
			name:       "pgs-mde-handoff-burns-shown-track",
			sub:        core.StreamInfo{Index: 6, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "nl"},
			echoOnDec:  false,
			decDirect:  "1",
			freshStart: false,
			wantBurn:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := core.MediaInfo{
				Container: "mkv", Duration: 169 * time.Minute, BitrateKbps: 55000,
				Streams: []core.StreamInfo{
					{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, BitDepth: 10, Default: true},
					{Index: 1, Type: "audio", Codec: "dts", Profile: "DTS-HD MA", Language: "en", Channels: 6, Default: true},
					{Index: 3, Type: "subtitle", Codec: "subrip", Language: "en"},
					tc.sub,
				},
			}
			f, _, _ := newIssue459Fixture(t, info, "nl")
			key := fightClub.Key.String()

			meta := f.get(t, "/library/metadata/"+key, sabrinaHeaders...)
			if meta.Code != http.StatusOK {
				t.Fatalf("detail: %d", meta.Code)
			}
			nlMeta := selectedTag(subtitleTags(meta.Body.String()))
			nlID := streamAttr(nlMeta, "id")
			if nlID == "" || streamAttr(nlMeta, "languageCode") != "nld" {
				t.Fatalf("metadata did not select Dutch: %q", nlMeta)
			}

			decQuery := "path=/library/metadata/" + key + "&protocol=hls&directPlay=" + tc.decDirect + "&directStream=1&hasMDE=1&subtitles=none&session=dec-459"
			if tc.echoOnDec {
				decQuery += "&subtitleStreamID=" + nlID
			}
			decRec := f.get(t, "/video/:/transcode/universal/decision?"+decQuery, sabrinaHeaders...)
			if decRec.Code != http.StatusOK {
				t.Fatalf("decision: %d %s", decRec.Code, decRec.Body.String())
			}
			if got := selectedTag(subtitleTags(decRec.Body.String())); streamAttr(got, "languageCode") != "nld" {
				t.Fatalf("decision did not select Dutch: %q", got)
			}

			startSession := "dec-459"
			if tc.freshStart {
				startSession = "start-459"
			}
			// ExoPlayer sends credentials and session identifiers on the URL and
			// drops X-Plex-Client-Profile-Extra / X-Plex-Product headers.
			startURL := "/video/:/transcode/universal/start.m3u8?path=/library/metadata/" + key +
				"&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=none&session=" + startSession + tc.startSubArg +
				"&X-Plex-Token=" + ownerToken +
				"&X-Plex-Client-Identifier=sabrina-459&X-Plex-Session-Identifier=play-sabrina-459"

			parseReq := httptest.NewRequest(http.MethodGet, startURL, nil)
			parseReq = parseReq.WithContext(withRequest(context.Background(), &Request{
				Token:     ownerToken,
				User:      &ownerUser,
				ClientID:  "sabrina-459",
				SessionID: "play-sabrina-459",
			}))
			parsed, err := f.srv.parsePlayRequest(parseReq)
			if err != nil {
				t.Fatalf("parsePlayRequest: %v", err)
			}
			if parsed.subSel != 3 {
				t.Fatalf("start subtitleStream position = %d, want 3 (Dutch)", parsed.subSel)
			}
			if parsed.manualSubtitle {
				t.Fatal("server-selected initial start was marked manualSubtitle=true")
			}

			rawReq := httptest.NewRequest(http.MethodGet, startURL, nil)
			rawReq.RemoteAddr = "192.168.1.20:51000"
			startRec := httptest.NewRecorder()
			f.srv.ServeHTTP(startRec, rawReq)
			if startRec.Code != http.StatusOK {
				t.Fatalf("start.m3u8: %d %s", startRec.Code, startRec.Body.String())
			}

			hs := f.trans.sessions[startSession]
			if hs == nil {
				t.Fatalf("missing HLS session %s", startSession)
			}
			if tc.wantBurn {
				if !hs.req.Decision.BurnSubtitles || hs.req.Decision.SubtitleStream != tc.sub.Index {
					t.Fatalf("expected Dutch PGS burn on index %d, got %+v", tc.sub.Index, hs.req.Decision)
				}
			} else {
				if hs.req.Decision.Container != transcode.ContainerMatroska {
					t.Fatalf("container = %q, want mkv so ExoPlayer receives muxed subtitle", hs.req.Decision.Container)
				}
				if hs.req.Decision.SubtitleStream != tc.sub.Index || hs.req.Decision.BurnSubtitles {
					t.Fatalf("expected muxed Dutch SRT index %d without burn, got %+v", tc.sub.Index, hs.req.Decision)
				}
			}
		})
	}
}

// TestInitialRuleSubtitleStartsOnPlexIOS reproduces #459 on Plex for iOS 2026.20.0
// across both Matroska HLS (MPV) and native Apple HLS WebVTT renditions (AVPlayer).
func TestInitialRuleSubtitleStartsOnPlexIOS(t *testing.T) {
	info := core.MediaInfo{
		Container: "mkv", Duration: 65 * time.Minute, BitrateKbps: 18000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, BitDepth: 10, Default: true},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
			{Index: 3, Type: "subtitle", Codec: "subrip", Language: "nl"},
		},
	}

	t.Run("matroska-hls", func(t *testing.T) {
		iosHeaders := []string{
			"X-Plex-Product", "Plex for iOS",
			"X-Plex-Version", "2026.20.0",
			"X-Plex-Platform", "iOS",
			"X-Plex-Device", "iPhone",
			"X-Plex-Client-Identifier", "ios-mkv-459",
			"X-Plex-Session-Identifier", "play-ios-mkv-459",
			"X-Plex-Client-Profile-Extra", androidMKVProfile,
		}
		f, _, _ := newIssue459Fixture(t, info, "nl")
		key := fightClub.Key.String()
		if rec := f.get(t, "/library/metadata/"+key, iosHeaders...); rec.Code != http.StatusOK {
			t.Fatalf("detail: %d", rec.Code)
		}
		decRec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+"&protocol=hls&directPlay=1&directStream=1&hasMDE=1&subtitles=none&session=ios-mkv", iosHeaders...)
		if decRec.Code != http.StatusOK {
			t.Fatalf("decision: %d %s", decRec.Code, decRec.Body.String())
		}
		// MDE accepts direct play in /decision then opens start.m3u8 without profile headers.
		startReq := httptest.NewRequest(http.MethodGet, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+key+
			"&protocol=hls&directPlay=1&directStream=1&hasMDE=1&subtitles=none&subtitleStreamID=-1&session=ios-mkv"+
			"&X-Plex-Token="+ownerToken+"&X-Plex-Client-Identifier=ios-mkv-459&X-Plex-Session-Identifier=play-ios-mkv-459", nil)
		startReq.RemoteAddr = "192.168.1.21:51000"
		startRec := httptest.NewRecorder()
		f.srv.ServeHTTP(startRec, startReq)
		if startRec.Code != http.StatusOK {
			t.Fatalf("start.m3u8: %d %s", startRec.Code, startRec.Body.String())
		}
		hs := f.trans.sessions["ios-mkv"]
		if hs == nil || hs.req.Decision.Container != transcode.ContainerMatroska || hs.req.Decision.SubtitleStream != 3 {
			t.Fatalf("iOS Matroska start lost Dutch subtitle: %+v", hs)
		}
	})

	t.Run("apple-webvtt-renditions", func(t *testing.T) {
		iosHeaders := []string{
			"X-Plex-Product", "Plex for iOS",
			"X-Plex-Version", "2026.20.0",
			"X-Plex-Platform", "iOS",
			"X-Plex-Device", "iPhone",
			"X-Plex-Client-Identifier", "ios-vtt-459",
			"X-Plex-Session-Identifier", "play-ios-vtt-459",
			"X-Plex-Client-Profile-Extra", "add-transcode-target(type=videoProfile&context=streaming&protocol=hls&container=mp4&videoCodec=h264,hevc&audioCodec=aac,eac3&subtitleCodec=vtt)",
		}
		f, _, _ := newIssue459Fixture(t, info, "nl")
		key := fightClub.Key.String()
		if rec := f.get(t, "/library/metadata/"+key, iosHeaders...); rec.Code != http.StatusOK {
			t.Fatalf("detail: %d", rec.Code)
		}
		decRec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+"&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=auto&session=ios-vtt", iosHeaders...)
		if decRec.Code != http.StatusOK {
			t.Fatalf("decision: %d %s", decRec.Code, decRec.Body.String())
		}
		// AVPlayer fetches start.m3u8 without X-Plex-Platform or subtitleStreamID.
		startReq := httptest.NewRequest(http.MethodGet, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+key+
			"&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=auto&session=ios-vtt"+
			"&X-Plex-Token="+ownerToken+"&X-Plex-Client-Identifier=ios-vtt-459&X-Plex-Session-Identifier=play-ios-vtt-459", nil)
		startReq.RemoteAddr = "192.168.1.21:51001"
		startRec := httptest.NewRecorder()
		f.srv.ServeHTTP(startRec, startReq)
		if startRec.Code != http.StatusOK {
			t.Fatalf("start.m3u8: %d %s", startRec.Code, startRec.Body.String())
		}
		body := startRec.Body.String()
		if !strings.Contains(body, `SUBTITLES="text"`) || !strings.Contains(body, `LANGUAGE="nl",DEFAULT=YES,AUTOSELECT=YES`) {
			t.Fatalf("master playlist missing selected Dutch WebVTT rendition:\n%s", body)
		}
		if !strings.Contains(body, `LANGUAGE="en",DEFAULT=NO,AUTOSELECT=NO`) {
			t.Fatalf("master playlist marked English default:\n%s", body)
		}
	})
}

// TestInheritedRuleSubtitleStartsOnAppleTV covers #617's playback symptom:
// the viewer has no saved row and inherits the household Always-on/English
// rule (user 0). Plex for Apple TV sends subtitles=none on first play; the
// decision and start must still carry the rule-selected English track.
func TestInheritedRuleSubtitleStartsOnAppleTV(t *testing.T) {
	info := core.MediaInfo{
		Container: "mkv", Duration: 100 * time.Minute, BitrateKbps: 20000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, BitDepth: 10, Default: true},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
			{Index: 3, Type: "subtitle", Codec: "subrip", Language: "fr"},
		},
	}
	f, _, prefs := newIssue459Fixture(t, info, "nl")
	// Replace the fixture's explicit viewer rule with a household rule only.
	if err := prefs.DeleteRules(context.Background(), ownerUser.ID); err != nil {
		t.Fatal(err)
	}
	shared := trackprefs.Default()
	shared.SubtitleMode = "always"
	shared.SubtitleLanguages = []string{"en"}
	if err := prefs.PutRules(context.Background(), 0, shared); err != nil {
		t.Fatal(err)
	}
	headers := []string{
		"X-Plex-Product", "Plex for Apple TV",
		"X-Plex-Version", "2026.20.0",
		"X-Plex-Platform", "tvOS",
		"X-Plex-Device", "Apple TV",
		"X-Plex-Client-Identifier", "atv-617",
		"X-Plex-Session-Identifier", "play-atv-617",
		"X-Plex-Client-Profile-Extra", "add-transcode-target(type=videoProfile&context=all&protocol=hls&container=mkv&videoCodec=h264,hevc&audioCodec=aac,eac3)",
	}
	key := fightClub.Key.String()
	if rec := f.get(t, "/library/metadata/"+key, headers...); rec.Code != http.StatusOK {
		t.Fatalf("detail: %d", rec.Code)
	}
	decRec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+"&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=none&session=atv-617", headers...)
	if decRec.Code != http.StatusOK {
		t.Fatalf("decision: %d %s", decRec.Code, decRec.Body.String())
	}
	if got := selectedTag(subtitleTags(decRec.Body.String())); streamAttr(got, "languageCode") != "eng" {
		t.Fatalf("decision did not select inherited English subtitle: %q", got)
	}
	startReq := httptest.NewRequest(http.MethodGet, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+key+
		"&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=none&subtitleStreamID=-1&session=atv-617"+
		"&X-Plex-Token="+ownerToken+"&X-Plex-Client-Identifier=atv-617&X-Plex-Session-Identifier=play-atv-617", nil)
	startReq.RemoteAddr = "192.168.1.30:51000"
	startRec := httptest.NewRecorder()
	f.srv.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start.m3u8: %d %s", startRec.Code, startRec.Body.String())
	}
	hs := f.trans.sessions["atv-617"]
	if hs == nil || hs.req.Decision.SubtitleStream != 2 || hs.req.Decision.BurnSubtitles {
		t.Fatalf("Apple TV start lost inherited English subtitle: %+v", hs)
	}
}

// TestAutomaticExternalSidecarSurvivesInitialStart verifies that an automatic
// external subtitle choice (SelectAutomaticSubtitle, stored with position -1)
// survives /decision -> start.m3u8 and /subtitles?subtitleStreamID=-1 without
// dropping the attached sidecar or flipping manual intent.
func TestAutomaticExternalSidecarSurvivesInitialStart(t *testing.T) {
	info := core.MediaInfo{
		Container: "mkv", Duration: 120 * time.Minute, BitrateKbps: 12000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
			{Index: 1, Type: "audio", Codec: "aac", Language: "en", Channels: 2, Default: true},
		},
	}
	f, subs, _ := newIssue459Fixture(t, info, "nl")
	key := fightClub.Key
	keyS := key.String()
	const dutchSRT = "1\n00:00:02,000 --> 00:00:04,000\nGoedenavond.\n\n"
	nlSidecar := sidecar("nl", "ether-subtitle:auto-nl")
	subs.files[nlSidecar.ExternalURL] = dutchSRT

	access := frontend.SessionAccess{Viewer: &ownerUser, Device: frontend.Device{ID: "atv-auto-459"}, Protocol: "plex"}
	if err := f.srv.SelectAutomaticSubtitle(access, key, nlSidecar); err != nil {
		t.Fatalf("SelectAutomaticSubtitle: %v", err)
	}

	headers := []string{
		"X-Plex-Product", "Plex for Android (TV)",
		"X-Plex-Platform", "Android",
		"X-Plex-Device", "Chromecast",
		"X-Plex-Client-Identifier", "atv-auto-459",
		"X-Plex-Session-Identifier", "play-auto-459",
		"X-Plex-Client-Profile-Extra", androidMKVProfile,
	}
	decRec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+keyS+"&protocol=hls&directPlay=0&directStream=1&hasMDE=1&session=auto-459", headers...)
	if decRec.Code != http.StatusOK {
		t.Fatalf("decision: %d %s", decRec.Code, decRec.Body.String())
	}
	startRec := f.get(t, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+keyS+"&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitleStreamID=-1&session=auto-459", headers...)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start.m3u8: %d %s", startRec.Code, startRec.Body.String())
	}
	entry, ok := f.srv.transcodes.get("auto-459")
	if !ok || entry.subtitle < 0 || entry.subtitle >= len(entry.info.Streams) || entry.info.Streams[entry.subtitle].ExternalURL != nlSidecar.ExternalURL {
		t.Fatalf("entry lost automatic Dutch sidecar: ok=%v subtitle=%d streams=%+v", ok, entry.subtitle, entry.info.Streams)
	}

	subRec := f.get(t, "/video/:/transcode/universal/subtitles?path=/library/metadata/"+keyS+"&session=auto-459&subtitleStreamID=-1&format=srt", headers...)
	if subRec.Code != http.StatusOK || !strings.Contains(subRec.Body.String(), "Goedenavond.") {
		t.Fatalf("subtitles with subtitleStreamID=-1 = %d %q", subRec.Code, subRec.Body.String())
	}
}

// TestExplicitSubtitleChoicesOverrideInitialRuleSelection verifies that explicit
// subtitleStreamID=0 (Off) or an explicit switch to another track on start.m3u8
// still overrides the session's rule-selected subtitle.
func TestExplicitSubtitleChoicesOverrideInitialRuleSelection(t *testing.T) {
	info := core.MediaInfo{
		Container: "mkv", Duration: 100 * time.Minute, BitrateKbps: 10000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
			{Index: 1, Type: "audio", Codec: "aac", Language: "en", Channels: 2, Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "nl"},
			{Index: 3, Type: "subtitle", Codec: "subrip", Language: "fr"},
		},
	}
	headers := []string{
		"X-Plex-Product", "Plex for Android (TV)",
		"X-Plex-Platform", "Android",
		"X-Plex-Device", "Chromecast",
		"X-Plex-Client-Identifier", "override-459",
		"X-Plex-Session-Identifier", "play-override-459",
		"X-Plex-Client-Profile-Extra", androidMKVProfile,
	}
	f, _, _ := newIssue459Fixture(t, info, "nl")
	key := fightClub.Key.String()

	decRec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+"&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=none&session=ovr-459", headers...)
	if decRec.Code != http.StatusOK {
		t.Fatalf("decision: %d", decRec.Code)
	}
	var frID string
	for _, tag := range subtitleTags(decRec.Body.String()) {
		if streamAttr(tag, "languageCode") == "fra" {
			frID = streamAttr(tag, "id")
		}
	}
	if frID == "" {
		t.Fatalf("missing French subtitle in decision:\n%s", decRec.Body.String())
	}

	// Explicit Off (subtitleStreamID=0) on start.m3u8 wins.
	offRec := f.get(t, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+key+"&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=none&subtitleStreamID=0&session=ovr-459", headers...)
	if offRec.Code != http.StatusOK {
		t.Fatalf("off start.m3u8: %d", offRec.Code)
	}
	if got := f.trans.sessions["ovr-459"].req.Decision.SubtitleStream; got != -1 {
		t.Fatalf("explicit Off kept SubtitleStream=%d, want -1", got)
	}

	// Explicit switch to French on start.m3u8 wins.
	frRec := f.get(t, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+key+"&protocol=hls&directPlay=0&directStream=1&hasMDE=1&subtitles=none&subtitleStreamID="+frID+"&session=ovr-459", headers...)
	if frRec.Code != http.StatusOK {
		t.Fatalf("fr start.m3u8: %d", frRec.Code)
	}
	if got := f.trans.sessions["ovr-459"].req.Decision.SubtitleStream; got != 3 {
		t.Fatalf("explicit French got SubtitleStream=%d, want 3", got)
	}
}
