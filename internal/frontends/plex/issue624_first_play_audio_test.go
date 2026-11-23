package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trackprefs"
)

// coldAudioPlayback624 serves an unprobed placeholder estimate before Prepare
// and switches to the real probed tracks when Prepare runs (simulating first
// play of an unprobed on-demand Stremio/Usenet dual-audio release).
type coldAudioPlayback624 struct {
	*fakePlayback
	placeholder core.MediaInfo
	probed      core.MediaInfo
}

func (c *coldAudioPlayback624) Estimate(_ context.Context, _ *core.Title, _ core.ClientCaps) core.MediaInfo {
	return c.placeholder
}

func (c *coldAudioPlayback624) Prepare(ctx context.Context, u *core.User, k core.RatingKey, caps core.ClientCaps) (*core.PlaySession, error) {
	c.fakePlayback.info = c.probed
	return c.fakePlayback.Prepare(ctx, u, k, caps)
}

func issue624PlaceholderInfo() core.MediaInfo {
	return core.MediaInfo{
		Container:   "mkv",
		Duration:    129 * time.Minute,
		Size:        7_500_000_000,
		BitrateKbps: 8000,
		Estimated:   true,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Profile: "high", Width: 1920, Height: 1080, FrameRate: 23.976, BitDepth: 8, Default: true, BitrateKbps: 7808},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, ChannelLayout: "stereo", SampleRate: 48000, Default: true, BitrateKbps: 192},
		},
	}
}

func issue624ProbedDualAudio(foreignLang string) core.MediaInfo {
	return core.MediaInfo{
		Container:   "mkv",
		Duration:    129 * time.Minute,
		Size:        7_500_000_000,
		BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Profile: "high", Width: 1920, Height: 1080, FrameRate: 23.976, BitDepth: 8, Default: true, BitrateKbps: 6720},
			{Index: 1, Type: "audio", Codec: "eac3", Language: foreignLang, Channels: 6, ChannelLayout: "5.1(side)", SampleRate: 48000, Default: true, BitrateKbps: 640},
			{Index: 2, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, ChannelLayout: "5.1(side)", SampleRate: 48000, Default: false, BitrateKbps: 640},
		},
	}
}

func newIssue624Fixture(t *testing.T, placeholder, probed core.MediaInfo, releaseID, releaseName string, withSlots bool) (*fixture, *trackprefs.Service) {
	t.Helper()
	f := newFixture()
	t.Cleanup(func() { _ = f.srv.Close() })
	f.playback.info = placeholder
	f.playback.resolution = &core.Resolution{
		Release:        core.Release{ID: releaseID, Name: releaseName, Protocol: core.ProtocolHTTP},
		HealthVerified: true,
	}
	f.srv.setPlayback(&coldAudioPlayback624{
		fakePlayback: f.playback,
		placeholder:  placeholder,
		probed:       probed,
	})
	f.srv.setTranscoder(&issue459Transcoder{fakeTranscoder: f.trans})
	prefs := trackprefs.New(store.OpenTest(t))
	f.srv.setTrackPreferences(prefs)
	rules := trackprefs.Default()
	rules.AudioLanguages = []string{"en"}
	rules.PreferOriginal = false
	if err := prefs.PutRules(context.Background(), ownerUser.ID, rules); err != nil {
		t.Fatal(err)
	}
	if withSlots {
		f.srv.setVariants(&configuredFakeVariants{
			plan: []core.ReleaseVariant{
				{ID: "slot:auto", Auto: true, Label: "Auto", Info: placeholder},
				{ID: "slot:1080", Label: "1080p", Info: placeholder},
			},
		})
	}
	return f, prefs
}

// TestIssue624FirstPlayDualAudioDirectPlay reproduces #624 on Apple TV and
// Fire TV: pre-probe metadata exposes a single unlabelled placeholder audio
// stream, first-play /decision returns both real probed tracks with English
// (index 2) selected for direct play, a placeholder audioStreamID sent back
// via query or PUT /library/parts resolves to English rather than track 1,
// and explicit choices of real probed tracks still win.
func TestIssue624FirstPlayDualAudioDirectPlay(t *testing.T) {
	clients := []struct {
		name        string
		foreignLang string
		foreignCode string
		releaseName string
		headers     []string
	}{
		{
			name:        "apple-tv-por-eng",
			foreignLang: "pt",
			foreignCode: "por",
			releaseName: "Beauty.and.the.Beast.2017.1080p.DSNP.WEB-DL.DDP5.1.Atmos.H.264.DUAL-DM",
			headers: []string{
				"X-Plex-Product", "Plex for Apple TV",
				"X-Plex-Platform", "tvOS",
				"X-Plex-Platform-Version", "27.2.1",
				"X-Plex-Device", "Apple TV",
				"X-Plex-Client-Identifier", "atv-624",
				"X-Plex-Session-Identifier", "play-atv-624",
			},
		},
		{
			name:        "fire-tv-tur-eng",
			foreignLang: "tr",
			foreignCode: "tur",
			releaseName: "How_I_Met_Your_Mother_S06E09_1080p_DSNP_WEBDL_H264_DDP51_TurkSeed",
			headers: []string{
				"X-Plex-Product", "Plex for Amazon FireTV",
				"X-Plex-Version", "2026.22.0",
				"X-Plex-Platform", "Android",
				"X-Plex-Device", "AFTKRT",
				"X-Plex-Client-Identifier", "firetv-624",
				"X-Plex-Session-Identifier", "play-firetv-624",
			},
		},
	}

	for _, client := range clients {
		for _, withSlots := range []bool{false, true} {
			for _, placeholderReturn := range []string{"omitted", "query", "put"} {
				name := fmt.Sprintf("%s/slots=%v/placeholder=%s", client.name, withSlots, placeholderReturn)
				t.Run(name, func(t *testing.T) {
					placeholder := issue624PlaceholderInfo()
					probed := issue624ProbedDualAudio(client.foreignLang)
					f, prefs := newIssue624Fixture(t, placeholder, probed, "usenet-624", client.releaseName, withSlots)
					key := fightClub.Key.String()

					// 1. Pre-probe playQueue and metadata expose the placeholder
					// without claiming a confident language it does not know.
					uri := url.QueryEscape("server://0123456789abcdef0123456789abcdef01234567/com.plexapp.plugins.library/library/metadata/" + key)
					pq := f.request(t, http.MethodPost, "/playQueues?type=video&uri="+uri, headerMap(client.headers...))
					if pq.Code != http.StatusOK {
						t.Fatalf("POST /playQueues %d: %s", pq.Code, pq.Body.String())
					}
					metaBefore := f.get(t, "/library/metadata/"+key, client.headers...)
					if metaBefore.Code != http.StatusOK {
						t.Fatalf("pre-probe metadata %d: %s", metaBefore.Code, metaBefore.Body.String())
					}
					preAudios := audioTags(metaBefore.Body.String())
					if len(preAudios) == 0 {
						t.Fatalf("pre-probe metadata missing placeholder audio:\n%s", metaBefore.Body.String())
					}
					placeholderTag := preAudios[0]
					placeholderID := streamAttr(placeholderTag, "id")
					if placeholderID == "" {
						t.Fatalf("pre-probe placeholder missing id: %s", placeholderTag)
					}
					if strings.Contains(placeholderTag, `languageCode="`) || strings.Contains(placeholderTag, `language="English"`) {
						t.Fatalf("pre-probe placeholder claimed a confident language: %s", placeholderTag)
					}

					// 2. If the client sends the placeholder audioStreamID back
					// via PUT /library/parts or on /decision, it must be treated
					// as no explicit choice and must not poison track preferences.
					extra := ""
					switch placeholderReturn {
					case "query":
						extra = "&audioStreamID=" + placeholderID
					case "put":
						put := f.request(t, http.MethodPut, "/library/parts/"+key+"?audioStreamID="+placeholderID+"&allParts=1", headerMap(client.headers...))
						if put.Code != http.StatusOK {
							t.Fatalf("PUT placeholder audio %d: %s", put.Code, put.Body.String())
						}
						if sel := f.srv.selections.get(ownerUser.ID, int64(fightClub.Key)); sel.audioSet {
							t.Fatalf("PUT placeholder locked explicit selection: %+v", sel)
						}
						// Track preferences must still pick English on real probed tracks.
						got, err := prefs.Select(context.Background(), ownerUser.ID, fightClub.Key, probed, "en")
						if err != nil || got.Audio != 2 {
							t.Fatalf("PUT placeholder poisoned trackprefs: got=%+v err=%v", got, err)
						}
					}

					// 3. First-play direct-play decision probes the release and
					// returns BOTH real probed audio tracks with English (index 2)
					// marked selected="1" and the foreign default (index 1) unselected.
					decPath := "/video/:/transcode/universal/decision?path=/library/metadata/" + key +
						"&hasMDE=1&directPlay=1&directStream=1&mediaIndex=0&partIndex=0&protocol=hls&session=s624" + extra
					dec := f.get(t, decPath, client.headers...)
					if dec.Code != http.StatusOK {
						t.Fatalf("decision %d: %s", dec.Code, dec.Body.String())
					}
					body := dec.Body.String()
					if !strings.Contains(body, `decision="directplay"`) {
						t.Fatalf("expected directplay decision:\n%s", body)
					}
					// Inspect streams inside the selected Media (configured slots include
					// alternate Media entries alongside the selected one).
					decAudios := selectedMediaAudioTags(body)
					if len(decAudios) != 2 {
						t.Fatalf("decision returned %d audio tracks, want 2 (foreign + English):\n%s", len(decAudios), body)
					}
					foreignTag := audioByLang(decAudios, client.foreignCode)
					engTag := audioByLang(decAudios, "eng")
					if foreignTag == "" || engTag == "" {
						t.Fatalf("decision missing real probed tracks (%s, eng): %q", client.foreignCode, decAudios)
					}
					if streamAttr(foreignTag, "index") != "1" || strings.Contains(foreignTag, `selected="1"`) {
						t.Fatalf("foreign track should be index 1 and unselected: %s", foreignTag)
					}
					if streamAttr(engTag, "index") != "2" || !strings.Contains(engTag, `selected="1"`) || !strings.Contains(engTag, `location="direct"`) {
						t.Fatalf("English track should be index 2, selected, location=direct: %s", engTag)
					}
					if f.trans.lastOpts.AudioStream != 2 {
						t.Fatalf("decision selected audio stream %d, want 2 (English)", f.trans.lastOpts.AudioStream)
					}

					// 4. Subsequent metadata read agrees with the decision's real
					// tracks, IDs, and English selection.
					metaAfter := f.get(t, "/library/metadata/"+key, client.headers...)
					if metaAfter.Code != http.StatusOK {
						t.Fatalf("post-probe metadata %d: %s", metaAfter.Code, metaAfter.Body.String())
					}
					afterAudios := selectedMediaAudioTags(metaAfter.Body.String())
					afterEng := audioByLang(afterAudios, "eng")
					if afterEng == "" || !strings.Contains(afterEng, `selected="1"`) || streamAttr(afterEng, "id") != streamAttr(engTag, "id") {
						t.Fatalf("post-probe metadata disagrees with decision: after=%q decEng=%q", afterAudios, engTag)
					}

					// 5. Explicit choice of a REAL probed stream still wins (both
					// switching to foreign track 1 and switching back to real English
					// track 2, even when English reused the aliased placeholder ID).
					foreignID := streamAttr(foreignTag, "id")
					realEngID := streamAttr(engTag, "id")
					putForeign := f.request(t, http.MethodPut, "/library/parts/"+key+"?audioStreamID="+foreignID+"&allParts=1", headerMap(client.headers...))
					if putForeign.Code != http.StatusOK {
						t.Fatalf("PUT real foreign audio %d: %s", putForeign.Code, putForeign.Body.String())
					}
					decForeign := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+
						"&hasMDE=1&directPlay=1&directStream=1&mediaIndex=0&partIndex=0&protocol=hls&session=s624-foreign", client.headers...)
					if decForeign.Code != http.StatusOK || f.trans.lastOpts.AudioStream != 1 {
						t.Fatalf("explicit real foreign choice lost: code=%d audio=%d\n%s", decForeign.Code, f.trans.lastOpts.AudioStream, decForeign.Body.String())
					}
					if selTag := selectedTag(selectedMediaAudioTags(decForeign.Body.String())); streamAttr(selTag, "languageCode") != client.foreignCode {
						t.Fatalf("explicit real foreign choice not marked selected: %q", selTag)
					}

					putEng := f.request(t, http.MethodPut, "/library/parts/"+key+"?audioStreamID="+realEngID+"&allParts=1", headerMap(client.headers...))
					if putEng.Code != http.StatusOK {
						t.Fatalf("PUT real English audio %d: %s", putEng.Code, putEng.Body.String())
					}
					decEng := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+
						"&hasMDE=1&directPlay=1&directStream=1&mediaIndex=0&partIndex=0&protocol=hls&session=s624-eng", client.headers...)
					if decEng.Code != http.StatusOK || f.trans.lastOpts.AudioStream != 2 {
						t.Fatalf("explicit real English choice lost: code=%d audio=%d\n%s", decEng.Code, f.trans.lastOpts.AudioStream, decEng.Body.String())
					}
				})
			}
		}
	}
}

// TestIssue624EstimatedPlaceholderWithPresumedLanguageStillIgnored proves that
// even when an estimated release track carries a presumed language (e.g. from
// release-name heuristics or a legacy estimate) at index 1, sending that
// pre-probe ID back is treated as no explicit choice and resolves to the
// viewer's preferred language on the real probed file.
func TestIssue624EstimatedPlaceholderWithPresumedLanguageStillIgnored(t *testing.T) {
	placeholder := issue624PlaceholderInfo()
	placeholder.Streams[1].Language = "pt" // presumed language on estimated track 1
	probed := issue624ProbedDualAudio("pt")
	f, _ := newIssue624Fixture(t, placeholder, probed, "usenet-624-est", "Movie.2017.1080p.DUAL-DM", false)
	key := fightClub.Key.String()
	hdr := []string{
		"X-Plex-Product", "Plex for Apple TV",
		"X-Plex-Platform", "tvOS",
		"X-Plex-Client-Identifier", "atv-624-est",
		"X-Plex-Session-Identifier", "play-atv-624-est",
	}

	preMeta := f.get(t, "/library/metadata/"+key, hdr...)
	estID := streamAttr(audioTags(preMeta.Body.String())[0], "id")
	if estID == "" {
		t.Fatalf("missing estimated stream id:\n%s", preMeta.Body.String())
	}

	// Returning the estimated stream ID on /decision (or PUT) must NOT pin
	// Portuguese; viewer preference (English) must win on the real streams.
	dec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+
		"&directPlay=0&directStream=1&protocol=hls&session=s624-est&audioStreamID="+estID, hdr...)
	if dec.Code != http.StatusOK {
		t.Fatalf("decision %d: %s", dec.Code, dec.Body.String())
	}
	if f.trans.lastOpts.AudioStream != 2 {
		t.Fatalf("estimated stream ID overrode English preference: AudioStream=%d", f.trans.lastOpts.AudioStream)
	}

	// Follow-up start.m3u8 (even echoing the stale placeholder ID) must also
	// deliver English (stream 2), never track 1.
	start := f.get(t, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+key+
		"&directPlay=0&directStream=1&protocol=hls&session=s624-est&audioStreamID="+estID, hdr...)
	if start.Code != http.StatusOK {
		t.Fatalf("start.m3u8 %d: %s", start.Code, start.Body.String())
	}
	hs := f.trans.sessions["s624-est"]
	if hs == nil || hs.req.Decision.AudioStream != 2 {
		t.Fatalf("start.m3u8 audio = %+v, want stream 2 (English)", hs)
	}
}

// TestIssue624StartUpgradesEstimatedDecisionOnceProbed verifies that if a
// decision was answered while the session was still estimated, and the probe
// completed before start.m3u8 / start.mpd / start, the start endpoint uses the
// newly probed real streams and selects English instead of reusing the
// estimated stream 1 decision.
func TestIssue624StartUpgradesEstimatedDecisionOnceProbed(t *testing.T) {
	placeholder := issue624PlaceholderInfo()
	probed := issue624ProbedDualAudio("pt")
	f := newFixture()
	defer f.srv.Close()
	f.playback.info = placeholder
	f.playback.resolution = &core.Resolution{
		Release:        core.Release{ID: "usenet-624-late", Name: "Movie.2017.1080p.DUAL-DM", Protocol: core.ProtocolUsenet},
		HealthVerified: true,
	}
	f.srv.setTranscoder(&issue459Transcoder{fakeTranscoder: f.trans})
	prefs := trackprefs.New(store.OpenTest(t))
	f.srv.setTrackPreferences(prefs)
	rules := trackprefs.Default()
	rules.AudioLanguages = []string{"en"}
	rules.PreferOriginal = false
	if err := prefs.PutRules(context.Background(), ownerUser.ID, rules); err != nil {
		t.Fatal(err)
	}

	key := fightClub.Key.String()
	hdr := []string{
		"X-Plex-Product", "Plex for Amazon FireTV",
		"X-Plex-Platform", "Android",
		"X-Plex-Device", "AFTKRT",
		"X-Plex-Client-Identifier", "firetv-624-late",
		"X-Plex-Session-Identifier", "play-firetv-624-late",
	}

	// Decision runs while session info is still estimated (audio stream 1).
	dec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+
		"&directPlay=0&directStream=1&protocol=hls&session=s624-late", hdr...)
	if dec.Code != http.StatusOK {
		t.Fatalf("decision %d: %s", dec.Code, dec.Body.String())
	}
	if f.trans.lastOpts.AudioStream != 1 {
		t.Fatalf("estimated decision audio = %d, want 1", f.trans.lastOpts.AudioStream)
	}

	// Probe finishes on the open PlaySession before start.m3u8 arrives.
	for _, ps := range f.playback.Sessions() {
		ps.Info = probed
	}
	f.playback.info = probed

	start := f.get(t, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+key+
		"&directPlay=0&directStream=1&protocol=hls&session=s624-late", hdr...)
	if start.Code != http.StatusOK {
		t.Fatalf("start.m3u8 %d: %s", start.Code, start.Body.String())
	}
	hs := f.trans.sessions["s624-late"]
	if hs == nil || hs.req.Decision.AudioStream != 2 {
		t.Fatalf("start.m3u8 reused estimated audio instead of probed English: %+v", hs)
	}
	if id, err := strconv.ParseInt(streamAttr(audioByLang(selectedMediaAudioTags(f.get(t, "/library/metadata/"+key, hdr...).Body.String()), "eng"), "id"), 10, 64); err != nil || id <= 0 {
		t.Fatalf("post-start metadata missing probed English track: %v", err)
	}
}

// selectedMediaAudioTags returns audio Stream tags from the selected Media
// element (or the entire body if only one Media is present).
func selectedMediaAudioTags(body string) []string {
	const open = "<Media "
	const closeTag = "</Media>"
	rest := body
	var firstBlock string
	for {
		i := strings.Index(rest, open)
		if i < 0 {
			break
		}
		rest = rest[i:]
		end := strings.Index(rest, closeTag)
		if end < 0 {
			break
		}
		block := rest[:end+len(closeTag)]
		if firstBlock == "" {
			firstBlock = block
		}
		headEnd := strings.Index(block, ">")
		if headEnd > 0 && strings.Contains(block[:headEnd], `selected="1"`) {
			return audioTags(block)
		}
		rest = rest[end+len(closeTag):]
	}
	if firstBlock != "" {
		return audioTags(firstBlock)
	}
	return audioTags(body)
}
