package plex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trackprefs"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type realDecideTranscoder struct {
	*fakeTranscoder
}

func (t realDecideTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	t.mu.Lock()
	t.lastOpts, t.lastCaps = opts, caps
	t.mu.Unlock()
	return transcode.Decide(info, caps, opts, transcode.Policy{})
}

const lgWebOSProfileExtra = "add-direct-play-profile(type=videoProfile&container=mkv,mp4&codec=hevc,h264&audioCodec=eac3,ac3,aac)+" +
	"add-transcode-target(type=videoProfile&context=streaming&protocol=hls&container=mpegts&videoCodec=h264&audioCodec=eac3,ac3,aac)+" +
	"add-limitation(scope=videoCodec&scopeName=hevc&type=upperBound&name=video.width&value=3840)+" +
	"add-limitation(scope=videoCodec&scopeName=hevc&type=upperBound&name=video.height&value=2160)+" +
	"add-limitation(scope=videoCodec&scopeName=hevc&type=upperBound&name=video.bitDepth&value=10)+" +
	"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.level&value=40)"

func lgWebOSHeaders(profileName string) map[string]string {
	return map[string]string{
		"Accept":                      "application/json",
		"X-Plex-Token":                ownerToken,
		"X-Plex-Client-Identifier":    "lg-g4-client",
		"X-Plex-Session-Identifier":   "lg-play-1",
		"X-Plex-Product":              "Plex for LG",
		"X-Plex-Version":              "5.94.4",
		"X-Plex-Platform":             "webOS",
		"X-Plex-Platform-Version":     "9.2.4",
		"X-Plex-Device":               "LG OLED65G46LS",
		"X-Plex-Model":                "OLED65G46LS.DEKQLJP",
		"X-Plex-Client-Profile-Name":  profileName,
		"X-Plex-Client-Profile-Extra": lgWebOSProfileExtra,
	}
}

func sample4KHEVCUsenetInfo() core.MediaInfo {
	return core.MediaInfo{
		Container:   "mkv",
		Duration:    58 * time.Minute,
		Size:        12_000_000_000,
		BitrateKbps: 24500,
		Streams: []core.StreamInfo{
			{
				Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10",
				Width: 3840, Height: 2160, FrameRate: 23.976, BitDepth: 10,
				ColorSpace: "bt2020nc", ColorTransfer: "smpte2084", ColorPrimaries: "bt2020",
				BitrateKbps: 23800,
			},
			{
				Index: 1, Type: "audio", Codec: "eac3", Language: "en",
				Channels: 6, ChannelLayout: "5.1(side)", SampleRate: 48000,
				Default: true, BitrateKbps: 640,
			},
		},
	}
}

func TestLGWebOSUsenet4KHEVCDirectPlaysInsteadOfBrokenHLS(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.srv.setTranscoder(realDecideTranscoder{fakeTranscoder: f.trans})
	f.playback.info = sample4KHEVCUsenetInfo()
	f.playback.resolution = &core.Resolution{
		Title: bbE1.Key,
		Release: core.Release{
			ID:       "nzb:mobland-s01e01-2160p",
			Protocol: core.ProtocolUsenet,
		},
	}

	rec := f.request(
		t,
		http.MethodGet,
		"/video/:/transcode/universal/decision?path=/library/metadata/"+bbE1.Key.String()+"&protocol=hls&hasMDE=1&directPlay=1&directStream=1&directStreamAudio=1&session=lg-s1",
		lgWebOSHeaders("HTML TV App"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("decision status = %d: %s", rec.Code, rec.Body.String())
	}
	mc := decodeJSON(t, rec)
	if got, _ := mc["mdeDecisionCode"].(float64); int(got) != codeDirectPlayOK {
		t.Fatalf("mdeDecisionCode = %v, want %d; body=%s", mc["mdeDecisionCode"], codeDirectPlayOK, rec.Body.String())
	}
	md := mc["Metadata"].([]any)[0].(map[string]any)
	media := md["Media"].([]any)[0].(map[string]any)
	part := media["Part"].([]any)[0].(map[string]any)
	if part["decision"] != "directplay" {
		t.Fatalf("Part.decision = %v, want directplay (must not force HEVC HLS copy on LG webOS)", part["decision"])
	}
}

func TestLGWebOSFallbackTranscodeCapsH264At1080pAndSingleRung(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.srv.setTranscoder(realDecideTranscoder{fakeTranscoder: f.trans})
	f.playback.info = sample4KHEVCUsenetInfo()
	f.playback.resolution = &core.Resolution{
		Title: bbE1.Key,
		Release: core.Release{
			ID:       "nzb:mobland-s01e01-2160p",
			Protocol: core.ProtocolUsenet,
		},
	}
	f.trans.rungs = []core.HLSRung{
		{ID: "base", Bandwidth: 20_000_000, Width: 1920, Height: 1080, Codecs: "avc1.640028,mp4a.40.2", FrameRate: 23.976},
		{ID: "720p4m", Bandwidth: 4_000_000, Width: 1280, Height: 720, Codecs: "avc1.64001f,mp4a.40.2", FrameRate: 23.976},
		{ID: "360p800k", Bandwidth: 800_000, Width: 640, Height: 360, Codecs: "avc1.64001e,mp4a.40.2", FrameRate: 23.976},
	}

	headers := lgWebOSHeaders("Generic")
	query := "path=/library/metadata/" + bbE1.Key.String() + "&protocol=hls&hasMDE=1&directPlay=0&directStream=0&directStreamAudio=1&session=lg-fallback"
	rec := f.request(t, http.MethodGet, "/video/:/transcode/universal/decision?"+query, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("decision status = %d: %s", rec.Code, rec.Body.String())
	}
	mc := decodeJSON(t, rec)
	md := mc["Metadata"].([]any)[0].(map[string]any)
	media := md["Media"].([]any)[0].(map[string]any)
	if got := int(media["height"].(float64)); got != 1080 {
		t.Fatalf("transcode height = %d, want 1080 (H.264 level 4.0 ceiling, never 4K 2160p)", got)
	}
	if media["videoCodec"] != "h264" || media["container"] != "mpegts" {
		t.Fatalf("transcode media = videoCodec=%v container=%v, want h264/mpegts", media["videoCodec"], media["container"])
	}

	startHeaders := map[string]string{
		"X-Plex-Token":              ownerToken,
		"X-Plex-Client-Identifier":  "lg-g4-client",
		"X-Plex-Session-Identifier": "lg-play-1",
		"X-Plex-Product":            "Plex for LG",
		"X-Plex-Platform":           "webOS",
	}
	m3u8 := f.request(t, http.MethodGet, "/video/:/transcode/universal/start.m3u8?"+query, startHeaders)
	if m3u8.Code != http.StatusOK {
		t.Fatalf("start.m3u8 status = %d: %s", m3u8.Code, m3u8.Body.String())
	}
	body := m3u8.Body.String()
	if !strings.Contains(body, "session/lg-fallback/base/index.m3u8") {
		t.Fatalf("master playlist missing base rung:\n%s", body)
	}
	if strings.Contains(body, "720p4m") || strings.Contains(body, "360p800k") {
		t.Fatalf("LG webOS master playlist must not expose multi-rung ABR variants:\n%s", body)
	}

	// Non-LG clients keep the full multi-rung HLS ladder.
	iosHeaders := map[string]string{
		"Accept":                    "application/json",
		"X-Plex-Token":              ownerToken,
		"X-Plex-Client-Identifier":  "ios-client",
		"X-Plex-Session-Identifier": "ios-play-1",
		"X-Plex-Product":            "Plex for iOS",
		"X-Plex-Platform":           "iOS",
	}
	iosQuery := "path=/library/metadata/" + bbE1.Key.String() + "&protocol=hls&hasMDE=1&directPlay=0&directStream=0&session=ios-ladder"
	if d := f.request(t, http.MethodGet, "/video/:/transcode/universal/decision?"+iosQuery, iosHeaders); d.Code != http.StatusOK {
		t.Fatalf("iOS decision status = %d", d.Code)
	}
	iosM3U8 := f.request(t, http.MethodGet, "/video/:/transcode/universal/start.m3u8?"+iosQuery, iosHeaders)
	if !strings.Contains(iosM3U8.Body.String(), "360p800k/index.m3u8") {
		t.Fatalf("iOS master playlist lost ABR ladder:\n%s", iosM3U8.Body.String())
	}
}

func TestTranscodeStopAfterTimelineStoppedIsIdempotentForOwnerOnly(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	headers := map[string]string{
		"X-Plex-Token":              ownerToken,
		"X-Plex-Client-Identifier":  "lg-g4-client",
		"X-Plex-Session-Identifier": "lg-play-stop",
		"X-Plex-Product":            "Plex for LG",
		"X-Plex-Platform":           "webOS",
	}
	query := "path=/library/metadata/" + fightClub.Key.String() + "&protocol=hls&directPlay=0&directStream=1&session=stop-race"
	if rec := f.request(t, http.MethodGet, "/video/:/transcode/universal/decision?"+query, headers); rec.Code != http.StatusOK {
		t.Fatalf("decision: %d", rec.Code)
	}
	if rec := f.request(t, http.MethodGet, "/video/:/transcode/universal/start.m3u8?"+query, headers); rec.Code != http.StatusOK {
		t.Fatalf("start.m3u8: %d", rec.Code)
	}

	// Plex for LG sends /:/timeline?state=stopped right before /universal/stop.
	tl := f.request(t, http.MethodGet, "/:/timeline?ratingKey="+fightClub.Key.String()+"&key=/library/metadata/"+fightClub.Key.String()+"&state=stopped&time=570260&duration=8340000", headers)
	if tl.Code != http.StatusOK {
		t.Fatalf("timeline stopped: %d", tl.Code)
	}

	// Another viewer still gets 404, and unknown sessions still get 404.
	other := f.request(t, http.MethodGet, "/video/:/transcode/universal/stop?session=stop-race", map[string]string{"X-Plex-Token": friendToken})
	if other.Code != http.StatusNotFound {
		t.Fatalf("other user stop = %d, want 404", other.Code)
	}
	unknown := f.request(t, http.MethodGet, "/video/:/transcode/universal/stop?session=never-started", headers)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown session stop = %d, want 404", unknown.Code)
	}

	// The owning viewer gets an idempotent 200 OK instead of a 404.
	own := f.request(t, http.MethodGet, "/video/:/transcode/universal/stop?session=stop-race", headers)
	if own.Code != http.StatusOK {
		t.Fatalf("owner stop after timeline stopped = %d, want 200", own.Code)
	}
}

// lgSubtitleTranscoder implements indexed extraction, cached lookup, and
// format-aware streaming so tests can verify both the fast indexed/finite SRT
// path and the unindexed streaming SRT path for Plex for LG (#728).
type lgSubtitleTranscoder struct {
	*fakeTranscoder
	mu           sync.Mutex
	indexedVTT   []byte
	indexedCalls int
	streamFormat string
	streamFrom   time.Duration
	streamOut    string
	streamCalls  int
	delayFirst   chan struct{}
}

func (l *lgSubtitleTranscoder) CachedSubtitle(context.Context, int, string) ([]byte, bool) {
	return nil, false
}

func (l *lgSubtitleTranscoder) ExtractIndexedSubtitle(context.Context, int, string) ([]byte, bool) {
	l.mu.Lock()
	l.indexedCalls++
	vtt := l.indexedVTT
	l.mu.Unlock()
	if len(vtt) == 0 {
		return nil, false
	}
	return vtt, true
}

func (l *lgSubtitleTranscoder) StreamSubtitle(ctx context.Context, input string, idx int, codec string, from time.Duration, w io.Writer) error {
	return l.StreamSubtitleFormat(ctx, input, idx, codec, "ass", from, w)
}

func (l *lgSubtitleTranscoder) StreamSubtitleFormat(ctx context.Context, _ string, _ int, _, format string, from time.Duration, w io.Writer) error {
	l.mu.Lock()
	l.streamCalls++
	l.streamFormat = format
	l.streamFrom = from
	out := l.streamOut
	delay := l.delayFirst
	l.mu.Unlock()
	if delay != nil {
		select {
		case <-delay:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	_, err := io.WriteString(w, out)
	return err
}

// Regression for #728 (part 1): Plex for LG and Samsung direct-play decisions
// must list every audio and subtitle track (including on local files), and
// cold on-demand PlayQueues + pre-play metadata must probe once so the in-player
// audio menu sees all tracks before direct play starts.
func TestLGWebOSDirectPlayListsAllAudioTracksAndProbesPlayQueue(t *testing.T) {
	t.Run("local decision lists all audio and subtitle tracks", func(t *testing.T) {
		f := newFixture()
		defer f.srv.Close()
		f.srv.setTranscoder(realDecideTranscoder{fakeTranscoder: f.trans})
		f.playback.info = core.MediaInfo{
			Container: "mkv", Duration: 58 * time.Minute, BitrateKbps: 18000,
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160, FrameRate: 23.976, BitDepth: 10},
				{Index: 1, Type: "audio", Codec: "aac", Language: "it", Channels: 2, Default: true, BitrateKbps: 192},
				{Index: 2, Type: "audio", Codec: "aac", Language: "en", Channels: 2, BitrateKbps: 192},
				{Index: 3, Type: "subtitle", Codec: "subrip", Language: "it"},
				{Index: 4, Type: "subtitle", Codec: "subrip", Language: "en"},
			},
		}
		for _, tc := range []struct {
			name    string
			headers map[string]string
		}{
			{"lg-webos", lgWebOSHeaders("HTML TV App")},
			{"samsung-tizen", map[string]string{
				"Accept": "application/json", "X-Plex-Token": ownerToken,
				"X-Plex-Client-Identifier": "sam-1", "X-Plex-Product": "Plex for Samsung", "X-Plex-Platform": "Tizen",
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				rec := f.request(t, http.MethodGet,
					"/video/:/transcode/universal/decision?path=/library/metadata/"+fightClub.Key.String()+
						"&protocol=hls&hasMDE=1&directPlay=1&directStream=1&directStreamAudio=1&subtitles=sidecar&session=dp-"+tc.name,
					tc.headers)
				if rec.Code != http.StatusOK {
					t.Fatalf("decision %d: %s", rec.Code, rec.Body.String())
				}
				mc := decodeJSON(t, rec)
				part := mc["Metadata"].([]any)[0].(map[string]any)["Media"].([]any)[0].(map[string]any)["Part"].([]any)[0].(map[string]any)
				if part["decision"] != "directplay" {
					t.Fatalf("decision=%v, want directplay", part["decision"])
				}
				audios, subs := 0, 0
				for _, raw := range part["Stream"].([]any) {
					st := raw.(map[string]any)
					switch int(st["streamType"].(float64)) {
					case 2:
						audios++
					case 3:
						subs++
					}
				}
				if audios != 2 || subs != 2 {
					t.Fatalf("directplay streams audio=%d subs=%d, want 2/2: %s", audios, subs, rec.Body.String())
				}
			})
		}
	})

	t.Run("cold playqueue and pre-play metadata probe once for LG webOS", func(t *testing.T) {
		dir := t.TempDir()
		cfg := config.Defaults(dir)
		cfg.Quality.Default = "lab4k"
		cfg.Quality.Remote = ""
		cfg.Quality.Rules = nil
		cfg.Quality.Profiles = []core.QualityProfile{{Name: "lab4k", MaxHeight: 2160}}

		const relSize = int64(4) << 30
		ep1Name := "Boundary.Bay.S01E01.2160p.WEB-DL.HEVC.AAC.ITA.ENG-GRP"
		idx := &bedrockLabIndexer{
			byEpisode: map[int]core.Release{
				1: {
					ID: "lg-ep1-dual", GUID: "lg-ep1-dual", Indexer: "stremio-usenet-addon",
					Name: ep1Name, Size: relSize, PubDate: fixedNow.Add(-24 * time.Hour),
					NZBURL: "offline", Info: releaseparse.Parse(ep1Name),
				},
			},
		}
		var clock atomic.Int64
		clock.Store(fixedNow.UnixNano())
		nowFn := func() time.Time { return time.Unix(0, clock.Load()).UTC() }

		f := newFixture(func(d *testServerDeps) {
			setDepsConfig(d, func() config.Config { return cfg })
			setDepsNow(d, nowFn)
			d.DisableEnrichment = true
			d.HomeCache = &testHomeCache{NoProviderWait: func(ctx context.Context) context.Context {
				return core.WithLocalResponse(catalog.WithReadWaitBudget(ctx, 0))
			}}
		})
		defer f.srv.Close()
		f.srv.setTranscoder(&issue459Transcoder{fakeTranscoder: f.trans})

		prefs := trackprefs.New(store.OpenTest(t))
		f.srv.setTrackPreferences(prefs)
		rules := trackprefs.Default()
		rules.AudioLanguages = []string{"en"}
		rules.PreferOriginal = false
		if err := prefs.PutRules(context.Background(), ownerUser.ID, rules); err != nil {
			t.Fatal(err)
		}

		rs := resolver.New(idx, nil, nil, cfg, resolver.Options{Now: nowFn})
		defer rs.Close()
		db, err := store.Open(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		var probes atomic.Int32
		probedDual := core.MediaInfo{
			Container: "mkv", Duration: bbE1.Runtime, BitrateKbps: 18000,
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160, BitDepth: 10, Default: true, BitrateKbps: 17500},
				{Index: 1, Type: "audio", Codec: "aac", Language: "ita", Channels: 2, Default: true, BitrateKbps: 192},
				{Index: 2, Type: "audio", Codec: "aac", Language: "eng", Channels: 2, BitrateKbps: 192},
				{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng"},
			},
		}
		mgr, err := playback.New(playback.Deps{
			Catalog:       f.catalog,
			State:         f.state,
			Resolver:      rs,
			Engine:        &shieldLocalEngine{size: relSize},
			Transcoder:    f.trans,
			SelectProfile: func(c core.ClientCaps, u *core.User, _ string) core.QualityProfile { return rs.ProfileFor(c, u) },
			DB:            db,
			NativeProbe: func(_ context.Context, s core.MediaSource) (*core.MediaInfo, error) {
				probes.Add(1)
				info := core.CloneMediaInfo(probedDual)
				info.Size = s.Size()
				return &info, nil
			},
		}, cfg, playback.Options{
			SessionSurface: func(surface string) string {
				if surface == "plex" {
					return ""
				}
				return surface
			},
			DataDir: dir,
			Now:     nowFn,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer mgr.Close()
		f.srv.setPlayback(mgr)
		f.srv.setVariants(rs)

		lgHdr := []string{
			"X-Plex-Product", "Plex for LG",
			"X-Plex-Platform", "webOS",
			"X-Plex-Platform-Version", "11.2.0",
			"X-Plex-Device", "LG OLED65C6",
			"X-Plex-Client-Identifier", "lg-728",
			"X-Plex-Session-Identifier", "sess-728",
		}
		ep1Key := bbE1.Key.String()

		// Detail browse must not trigger a foreground play search.
		browse := f.get(t, "/library/metadata/"+ep1Key+"?includeRelated=1&includeExtras=1", lgHdr...)
		if browse.Code != http.StatusOK || idx.playSearches.Load() != 0 {
			t.Fatalf("detail browse code=%d searches=%d", browse.Code, idx.playSearches.Load())
		}

		// POST /playQueues runs ProbePlayMetadata once; follow-up pre-play metadata
		// returns both ita and eng tracks (with eng selected) and embedded subs, and
		// /decision adopts the warm flight without re-probing.
		ep1URI := url.QueryEscape("server://" + f.srv.identity().MachineIdentifier + "/com.plexapp.plugins.library/library/metadata/" + ep1Key)
		pq := f.request(t, http.MethodPost, "/playQueues?type=video&continuous=1&excludeElements=Part&uri="+ep1URI, headerMap(lgHdr...))
		if pq.Code != http.StatusOK {
			t.Fatalf("POST /playQueues %d: %s", pq.Code, pq.Body.String())
		}
		searchesAfterPQ := idx.playSearches.Load()
		probesAfterPQ := probes.Load()
		if probesAfterPQ != 1 {
			t.Fatalf("probes after POST /playQueues = %d, want 1", probesAfterPQ)
		}

		meta := f.get(t, "/library/metadata/"+ep1Key, lgHdr...)
		if meta.Code != http.StatusOK {
			t.Fatalf("GET /library/metadata %d: %s", meta.Code, meta.Body.String())
		}
		audios := selectedMediaAudioTags(meta.Body.String())
		if len(audios) != 2 {
			t.Fatalf("pre-play metadata returned %d audio tracks, want 2 (ita + eng):\n%s", len(audios), meta.Body.String())
		}
		if eng := audioByLang(audios, "eng"); eng == "" || !strings.Contains(eng, `selected="1"`) {
			t.Fatalf("pre-play metadata did not select English track: %q", audios)
		}
		if len(subtitleTags(meta.Body.String())) == 0 {
			t.Fatalf("pre-play metadata missing embedded subtitle track:\n%s", meta.Body.String())
		}
		dec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+ep1Key+
			"&hasMDE=1&directPlay=1&directStream=1&subtitles=sidecar&protocol=hls&session=lg-728-1", lgHdr...)
		if dec.Code != http.StatusOK {
			t.Fatalf("decision %d: %s", dec.Code, dec.Body.String())
		}
		if idx.playSearches.Load() != searchesAfterPQ || probes.Load() != probesAfterPQ {
			t.Fatalf("decision repeated work instead of adopting warm flight: searches %d->%d probes %d->%d",
				searchesAfterPQ, idx.playSearches.Load(), probesAfterPQ, probes.Load())
		}
	})
}

// Regression for #728 (part 2): Plex for LG (and other HTML TV apps declaring
// subtitleProfile=srt or using non-browser profiles) must receive SRT — not
// ASS — on GET /subtitles/:/transcode/universal/start for both downloaded
// external sidecars (OpenSubtitles/SubDL) and embedded text tracks (fast
// finite SRT when indexed/cached, or playhead-streamed SRT when unindexed).
// Plex Web must continue receiving ASS.
func TestLGWebOSTextSubtitlesDeliverSRTNotASS(t *testing.T) {
	const lgSubExtra = lgWebOSProfileExtra +
		"+add-transcode-target(type=subtitleProfile&context=all&protocol=http&container=srt&subtitleCodec=srt)"

	t.Run("external OpenSubtitles sidecar serves SRT for LG and ASS for Plex Web", func(t *testing.T) {
		f, subs := newSubtitleFixture(t)
		f.trans.decision = core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, Container: "mkv"}
		ext := core.StreamInfo{Index: 9, Type: "subtitle", Codec: "srt", Language: "en", External: true, ExternalURL: "ether-subtitle:subdl-728"}
		subs.cached[fightClub.Key] = []core.StreamInfo{ext}
		subs.files[ext.ExternalURL] = "1\n00:00:05,000 --> 00:00:08,000\nHello from SubDL\n\n"
		f.playback.info = core.MediaInfo{
			Container: "mkv", Duration: 58 * time.Minute, BitrateKbps: 18000,
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, Default: true},
				{Index: 1, Type: "audio", Codec: "aac", Language: "en", Channels: 2, Default: true},
			},
		}
		key := fightClub.Key.String()
		meta := f.get(t, "/library/metadata/"+key)
		extID := subtitleChoice(subtitleTags(meta.Body.String()), "eng", true)
		if extID == "" {
			t.Fatalf("missing external subtitle in metadata: %s", meta.Body.String())
		}
		putSubtitle(t, f, key, extID)

		for _, tc := range []struct {
			name       string
			hdr        []string
			wantCT     string
			wantPrefix string
			forbidSub  string
		}{
			{
				name: "lg-with-subtitleProfile",
				hdr: []string{
					"X-Plex-Product", "Plex for LG", "X-Plex-Platform", "webOS",
					"X-Plex-Client-Identifier", "lg-ext-1",
					"X-Plex-Client-Profile-Name", "Generic",
					"X-Plex-Client-Profile-Extra", lgSubExtra,
				},
				wantCT: "application/x-subrip", wantPrefix: "1\n00:00:05,000", forbidSub: "[Script Info]",
			},
			{
				name: "lg-with-ass-subtitleProfile",
				hdr: []string{
					"X-Plex-Product", "Plex for LG", "X-Plex-Platform", "webOS",
					"X-Plex-Client-Identifier", "lg-ext-ass",
					"X-Plex-Client-Profile-Name", "Generic",
					"X-Plex-Client-Profile-Extra", lgWebOSProfileExtra + "+add-transcode-target(type=subtitleProfile&protocol=http&context=all&subtitleCodec=ass&container=ass)",
				},
				wantCT: "text/x-ssa", wantPrefix: "[Script Info]", forbidSub: "00:00:05,000 -->",
			},
			{
				name: "lg-without-profile-extra",
				hdr: []string{
					"X-Plex-Product", "Plex for LG", "X-Plex-Platform", "webOS",
					"X-Plex-Client-Identifier", "lg-ext-2",
					"X-Plex-Client-Profile-Name", "HTML TV App",
				},
				wantCT: "application/x-subrip", wantPrefix: "1\n00:00:05,000", forbidSub: "[Script Info]",
			},
			{
				name: "samsung-profile",
				hdr: []string{
					"X-Plex-Product", "Plex for Samsung", "X-Plex-Platform", "Tizen",
					"X-Plex-Client-Identifier", "sam-ext-1",
					"X-Plex-Client-Profile-Name", "Samsung",
				},
				wantCT: "application/x-subrip", wantPrefix: "1\n00:00:05,000", forbidSub: "[Script Info]",
			},
			{
				name: "plex-web-stays-ass",
				hdr: []string{
					"X-Plex-Product", "Plex Web", "X-Plex-Platform", "Chrome",
					"X-Plex-Client-Identifier", "web-ext-1",
				},
				wantCT: "text/x-ssa", wantPrefix: "[Script Info]", forbidSub: "00:00:05,000 -->",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				sess := "sess-" + tc.name
				dec := f.get(t, fmt.Sprintf("/video/:/transcode/universal/decision?path=/library/metadata/%s&protocol=hls&directPlay=1&directStream=1&hasMDE=1&subtitles=sidecar&session=%s", key, sess), tc.hdr...)
				if dec.Code != http.StatusOK {
					t.Fatalf("decision %d: %s", dec.Code, dec.Body.String())
				}
				// Subtitle fetch omits format= and profile-extra just like LG's FetchConnection.
				sub := f.get(t, fmt.Sprintf("/subtitles/:/transcode/universal/start?path=/library/metadata/%s&session=%s&directPlay=1&subtitles=sidecar&copyts=1&offset=0", key, sess),
					"X-Plex-Client-Identifier", tc.hdr[5])
				if sub.Code != http.StatusOK {
					t.Fatalf("subtitle status %d: %s", sub.Code, sub.Body.String())
				}
				if ct := sub.Header().Get("Content-Type"); ct != tc.wantCT {
					t.Fatalf("Content-Type = %q, want %q; body=%q", ct, tc.wantCT, sub.Body.String())
				}
				body := sub.Body.String()
				if !strings.HasPrefix(body, tc.wantPrefix) || !strings.Contains(body, "Hello from SubDL") || strings.Contains(body, tc.forbidSub) {
					t.Fatalf("unexpected subtitle body: %q", body)
				}
			})
		}
	})

	t.Run("embedded indexed MKV returns finite SRT immediately without streaming", func(t *testing.T) {
		f, _ := newSubtitleFixture(t)
		tr := &lgSubtitleTranscoder{
			fakeTranscoder: f.trans,
			indexedVTT:     []byte("WEBVTT\n\n00:00:10.000 --> 00:00:13.000\nIndexed cue\n\n"),
		}
		f.srv.setTranscoder(tr)
		f.trans.decision = core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, Container: "mkv"}
		f.playback.info = core.MediaInfo{
			Container: "mkv", Duration: 58 * time.Minute, BitrateKbps: 18000,
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, Default: true},
				{Index: 1, Type: "audio", Codec: "aac", Language: "en", Channels: 2, Default: true},
				{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
			},
		}
		key := fightClub.Key.String()
		srtID := subtitleChoice(subtitleTags(f.get(t, "/library/metadata/"+key).Body.String()), "eng", false)
		putSubtitle(t, f, key, srtID)

		lgHdr := []string{
			"X-Plex-Product", "Plex for LG", "X-Plex-Platform", "webOS",
			"X-Plex-Client-Identifier", "lg-idx",
			"X-Plex-Client-Profile-Extra", lgSubExtra,
		}
		dec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+"&protocol=hls&directPlay=1&directStream=1&hasMDE=1&subtitles=sidecar&session=lg-idx-s", lgHdr...)
		if dec.Code != http.StatusOK {
			t.Fatalf("decision %d: %s", dec.Code, dec.Body.String())
		}
		sub := f.get(t, "/subtitles/:/transcode/universal/start?path=/library/metadata/"+key+"&session=lg-idx-s&directPlay=1&subtitles=sidecar&copyts=1&offset=125", lgHdr...)
		if sub.Code != http.StatusOK || sub.Header().Get("Content-Type") != "application/x-subrip" {
			t.Fatalf("status=%d ct=%q body=%q", sub.Code, sub.Header().Get("Content-Type"), sub.Body.String())
		}
		body := sub.Body.String()
		if !strings.HasPrefix(body, "1\n00:00:10,000 --> 00:00:13,000\nIndexed cue") {
			t.Fatalf("indexed SRT body = %q", body)
		}
		if tr.streamCalls != 0 || tr.indexedCalls != 1 {
			t.Fatalf("indexedCalls=%d streamCalls=%d, want 1/0", tr.indexedCalls, tr.streamCalls)
		}
	})

	t.Run("embedded unindexed MKV streams SRT from playhead with atomic cue blocks", func(t *testing.T) {
		quiet, alive := subtitleQuietWait, subtitleKeepAlive
		subtitleQuietWait, subtitleKeepAlive = 20*time.Millisecond, 20*time.Millisecond
		t.Cleanup(func() { subtitleQuietWait, subtitleKeepAlive = quiet, alive })

		f, _ := newSubtitleFixture(t)
		release := make(chan struct{})
		tr := &lgSubtitleTranscoder{
			fakeTranscoder: f.trans,
			streamOut:      "1\n00:02:16,000 --> 00:02:20,000\nStreamed cue\n\n",
			delayFirst:     release,
		}
		f.srv.setTranscoder(tr)
		f.trans.decision = core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, Container: "mkv"}
		f.playback.info = core.MediaInfo{
			Container: "mkv", Duration: 58 * time.Minute, BitrateKbps: 18000,
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, Default: true},
				{Index: 1, Type: "audio", Codec: "aac", Language: "en", Channels: 2, Default: true},
				{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
			},
		}
		key := fightClub.Key.String()
		srtID := subtitleChoice(subtitleTags(f.get(t, "/library/metadata/"+key).Body.String()), "eng", false)
		putSubtitle(t, f, key, srtID)

		lgHdr := []string{
			"X-Plex-Product", "Plex for LG", "X-Plex-Platform", "webOS",
			"X-Plex-Client-Identifier", "lg-stream",
			"X-Plex-Client-Profile-Extra", lgSubExtra,
		}
		dec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+"&protocol=hls&directPlay=1&directStream=1&hasMDE=1&subtitles=sidecar&session=lg-stream-s", lgHdr...)
		if dec.Code != http.StatusOK {
			t.Fatalf("decision %d: %s", dec.Code, dec.Body.String())
		}
		out := &flushSignal{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
		req := httptest.NewRequest(http.MethodGet, "/subtitles/:/transcode/universal/start?path=/library/metadata/"+key+"&session=lg-stream-s&directPlay=1&subtitles=sidecar&copyts=1&offset=125", nil)
		req.RemoteAddr = "192.168.1.50:51000"
		req.Header.Set("X-Plex-Token", ownerToken)
		for i := 0; i+1 < len(lgHdr); i += 2 {
			req.Header.Set(lgHdr[i], lgHdr[i+1])
		}
		done := make(chan struct{})
		go func() { f.srv.ServeHTTP(out, req); close(done) }()
		select {
		case <-out.flushed:
		case <-time.After(5 * time.Second):
			t.Fatal("streaming SRT did not flush keepalive while waiting for first cue")
		}
		close(release)
		<-done

		if out.Code != http.StatusOK || out.Header().Get("Content-Type") != "application/x-subrip" {
			t.Fatalf("status=%d ct=%q body=%q", out.Code, out.Header().Get("Content-Type"), out.Body.String())
		}
		if out.Header().Get("X-Accel-Buffering") != "no" {
			t.Fatalf("streaming SRT missing X-Accel-Buffering: no")
		}
		if got := strings.TrimLeft(out.Body.String(), "\n"); got != "1\n00:02:16,000 --> 00:02:20,000\nStreamed cue\n\n" {
			t.Fatalf("streamed SRT body corrupted: %q", out.Body.String())
		}
		if tr.streamFormat != "srt" || tr.streamFrom != 115*time.Second {
			t.Fatalf("stream format=%q from=%v, want srt / 115s", tr.streamFormat, tr.streamFrom)
		}
	})
}
