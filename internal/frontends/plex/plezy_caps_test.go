package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/resolver"
)

type captureCapsPlayback struct {
	frontend.Playback
	onPrepare func(frontend.ClientCaps)
}

func (p captureCapsPlayback) Prepare(ctx context.Context, u *core.User, k core.RatingKey, caps core.ClientCaps) (*core.PlaySession, error) {
	if p.onPrepare != nil {
		p.onPrepare(caps)
	}
	return p.Playback.Prepare(ctx, u, k, caps)
}

func TestPlezyDesktopAndAppleRegistryCaps(t *testing.T) {
	for _, tc := range []struct {
		platform, device string
		wantAV1, wantDV  bool
	}{
		{"macOS", "MacBook Pro", true, false},
		{"Darwin", "MacBook Pro", true, false},
		{"MacOSX", "MacBook Pro", true, false},
		{"Windows", "Desktop", true, false},
		{"Linux", "Linux", true, false},
		{"Generic", "macOS", true, false},
		{"tvOS", "Apple TV", false, true},
		{"iOS", "iPhone", false, true},
	} {
		pr := &Request{Product: "Plezy", Platform: tc.platform, Device: tc.device}
		caps := pr.ClientCaps(0)
		if caps.MaxHeight != 2160 || !caps.SupportsVideo("hevc") || !caps.HDR10 || !caps.SupportsContainer("mkv") {
			t.Errorf("Plezy/%s: unexpected baseline caps %+v", tc.platform, caps)
		}
		if caps.SupportsVideo("av1") != tc.wantAV1 {
			t.Errorf("Plezy/%s: av1=%v want %v", tc.platform, caps.SupportsVideo("av1"), tc.wantAV1)
		}
		if caps.DolbyVision != tc.wantDV {
			t.Errorf("Plezy/%s: dv=%v want %v", tc.platform, caps.DolbyVision, tc.wantDV)
		}
	}
}

func TestTranscodeTargetLimitationsDoNotCapDirectPlay(t *testing.T) {
	extra := strings.Join([]string{
		"add-transcode-target(type=videoProfile&context=streaming&protocol=hls&container=mpegts&videoCodec=h264,hevc&audioCodec=aac,ac3)",
		"add-limitation(scope=videoCodec&scopeName=*&context=streaming&protocol=hls&type=upperBound&name=video.height&value=1080)",
		"add-limitation(scope=videoCodec&scopeName=*&context=streaming&protocol=hls&type=upperBound&name=video.width&value=1920)",
		"add-limitation(scope=videoCodec&scopeName=*&context=streaming&protocol=hls&type=upperBound&name=video.bitDepth&value=8)",
		"add-limitation(scope=videoCodec&scopeName=*&context=streaming&protocol=hls&type=notMatch&name=video.colorTrc&value=smpte2084)",
		"add-limitation(scope=videoCodec&scopeName=*&context=streaming&protocol=hls&type=notMatch&name=video.DOVIProfile&value=*)",
		"add-limitation(scope=videoCodec&scopeName=*&context=streaming&protocol=hls&type=upperBound&name=video.bitrate&value=8000)",
		"add-limitation(scope=videoTranscodeTarget&scopeName=*&type=upperBound&name=video.height&value=720)",
		"add-limitation(scope=videoCodec&scopeName=*&context=transcode&type=upperBound&name=video.height&value=720)",
	}, "+")

	pr := &Request{Product: "Plezy", Platform: "tvOS", Device: "Apple TV", ProfileExtra: extra}
	caps := pr.ClientCaps(0)
	if caps.MaxHeight != 2160 {
		t.Fatalf("direct-play MaxHeight = %d (source=%q), want 2160", caps.MaxHeight, caps.MaxHeightSource)
	}
	if !caps.SupportsVideo("hevc") || !caps.HDR10 || !caps.DolbyVision {
		t.Fatalf("transcode-target limitation degraded direct-play caps: %+v", caps)
	}
	if caps.MaxBitrateKbps != 0 {
		t.Fatalf("transcode-target bitrate leaked into direct-play caps: %d", caps.MaxBitrateKbps)
	}

	// Protocol-scoped limitations still constrain actual HLS transcodes.
	hlsExtra := "add-limitation(scope=videoCodec&scopeName=*&context=streaming&protocol=hls&type=upperBound&name=video.height&value=1080)" +
		"+add-limitation(scope=videoCodec&scopeName=*&context=streaming&protocol=hls&type=upperBound&name=video.bitrate&value=8000)"
	p := &playRequest{caps: caps}
	applyExtraTranscodeTargetLimits(hlsExtra, frontend.ProtocolHLS, nil, frontend.MediaInfo{}, p)
	if p.caps.MaxHeight != 1080 || p.caps.MaxBitrateKbps != 8000 {
		t.Fatalf("HLS transcode target limits = height=%d bitrate=%d, want 1080 / 8000", p.caps.MaxHeight, p.caps.MaxBitrateKbps)
	}
}

func TestProfileExtraMultiCodecAndReplaceHeightLimits(t *testing.T) {
	t.Run("h264 1080p plus hevc 2160p keeps 4K ceiling", func(t *testing.T) {
		extra := "add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.height&value=1080)" +
			"+add-limitation(scope=videoCodec&scopeName=hevc&type=upperBound&name=video.height&value=2160)"
		pr := &Request{Product: "Plezy", Platform: "tvOS", Device: "Apple TV", ProfileExtra: extra}
		caps := pr.ClientCaps(0)
		if caps.MaxHeight != 2160 {
			t.Fatalf("MaxHeight = %d (source=%q), want 2160", caps.MaxHeight, caps.MaxHeightSource)
		}
		if caps.VideoMaxHeights["h264"] != 1080 || caps.VideoMaxHeights["hevc"] != 2160 {
			t.Fatalf("VideoMaxHeights = %+v, want h264=1080 hevc=2160", caps.VideoMaxHeights)
		}
	})

	t.Run("wildcard 1080p overridden by hevc 2160p replace=true", func(t *testing.T) {
		extra := "add-limitation(scope=videoCodec&scopeName=*&type=upperBound&name=video.height&value=1080)" +
			"+add-limitation(scope=videoCodec&scopeName=hevc&type=upperBound&name=video.height&value=2160&replace=true)"
		pr := &Request{Product: "Plezy", Platform: "macOS", Device: "MacBook Pro", ProfileExtra: extra}
		caps := pr.ClientCaps(0)
		if caps.MaxHeight != 2160 {
			t.Fatalf("MaxHeight = %d (source=%q), want 2160", caps.MaxHeight, caps.MaxHeightSource)
		}
		if caps.VideoMaxHeights["h264"] != 1080 || caps.VideoMaxHeights["hevc"] != 2160 {
			t.Fatalf("VideoMaxHeights = %+v, want h264=1080 hevc=2160", caps.VideoMaxHeights)
		}
	})

	t.Run("h264 colorTrc denial does not disable HEVC HDR10", func(t *testing.T) {
		extra := "add-limitation(scope=videoCodec&scopeName=h264&type=notMatch&name=video.colorTrc&value=smpte2084)"
		pr := &Request{Product: "Plezy", Platform: "tvOS", Device: "Apple TV", ProfileExtra: extra}
		caps := pr.ClientCaps(0)
		if !caps.HDR10 || !caps.DolbyVision {
			t.Fatalf("h264 colorTrc limitation disabled HDR/DV: %+v", caps)
		}
	})

	t.Run("h264 video.level before direct-play hevc does not clamp 4K", func(t *testing.T) {
		extra := "add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.level&value=40)" +
			"+add-direct-play-profile(type=videoProfile&container=mkv&videoCodec=h264,hevc&audioCodec=aac)"
		pr := &Request{Product: "Plezy", Platform: "macOS", Device: "MacBook Pro", ProfileExtra: extra}
		caps := pr.ClientCaps(0)
		if caps.MaxHeight != 2160 {
			t.Fatalf("MaxHeight = %d (source=%q), want 2160", caps.MaxHeight, caps.MaxHeightSource)
		}
	})
}

func TestDirectPlayDecisionDoesNotCapSourceSelection(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	// Remote Original playback on Plezy Apple TV with videoResolution and
	// maxVideoBitrate transcode hints must never cap direct-play source selection.
	target := "/video/:/transcode/universal/decision?path=/library/metadata/" + fightClub.Key.String() +
		"&directPlay=1&directStream=1&location=wan&videoResolution=1920x1080&maxVideoBitrate=8000"
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.RemoteAddr = "198.51.100.10:41234"
	r.Header.Set("X-Plex-Token", ownerToken)
	r.Header.Set("X-Plex-Product", "Plezy")
	r.Header.Set("X-Plex-Platform", "tvOS")
	r.Header.Set("X-Plex-Device", "Apple TV")
	r.Header.Set("X-Plex-Client-Identifier", "plezy-atv-remote")
	pr := ParseRequest(r)
	pr.User = &ownerUser
	r = r.WithContext(withRequest(r.Context(), pr))

	p, err := f.srv.parsePlayRequest(r)
	if err != nil {
		t.Fatalf("parsePlayRequest: %v", err)
	}
	sel := p.sourceSelectionCaps()
	if sel.MaxHeight != 2160 {
		t.Fatalf("direct-play source MaxHeight = %d (source=%q), want 2160", sel.MaxHeight, sel.MaxHeightSource)
	}
	if strings.Contains(sel.MaxHeightSource, "videoResolution") {
		t.Fatalf("videoResolution leaked into direct-play MaxHeightSource: %q", sel.MaxHeightSource)
	}
	if sel.MaxBitrateKbps != 0 && !sel.DefaultRemoteBitrate {
		t.Fatalf("direct-play transcode hint turned remote bitrate into explicit cap: %+v", sel)
	}
	if !sel.SupportsVideo("hevc") || !sel.HDR10 || !sel.DolbyVision {
		t.Fatalf("direct-play source caps lost codecs/HDR/DV: %+v", sel)
	}

	// maxVideoBitrate=0 ("Original") with remote server default also stays uncapped.
	r0 := httptest.NewRequest(http.MethodGet,
		"/video/:/transcode/universal/decision?path=/library/metadata/"+fightClub.Key.String()+
			"&directPlay=1&directStream=1&location=wan&maxVideoBitrate=0", nil)
	r0.RemoteAddr = "198.51.100.10:41235"
	r0.Header.Set("X-Plex-Token", ownerToken)
	r0.Header.Set("X-Plex-Product", "Plezy")
	r0.Header.Set("X-Plex-Platform", "macOS")
	r0.Header.Set("X-Plex-Device", "MacBook Pro")
	r0.Header.Set("X-Plex-Client-Identifier", "plezy-mac-remote")
	pr0 := ParseRequest(r0)
	pr0.User = &ownerUser
	r0 = r0.WithContext(withRequest(r0.Context(), pr0))

	p0, err := f.srv.parsePlayRequest(r0)
	if err != nil {
		t.Fatalf("parsePlayRequest maxVideoBitrate=0: %v", err)
	}
	sel0 := p0.sourceSelectionCaps()
	if sel0.MaxHeight != 2160 || (sel0.MaxBitrateKbps != 0 && !sel0.DefaultRemoteBitrate) {
		t.Fatalf("maxVideoBitrate=0 source caps = %+v, want 2160 and no explicit bitrate cap", sel0)
	}

	// Owner bitrate policy still applies even when directPlay=1.
	p.caps.MaxBitrateKbps = 6000
	p.caps.DefaultRemoteBitrate = false
	p.ownerBitrateCap = true
	selOwner := p.sourceSelectionCaps()
	if selOwner.MaxBitrateKbps != 6000 || selOwner.DefaultRemoteBitrate {
		t.Fatalf("owner bitrate policy ignored on directPlay=1: %+v", selOwner)
	}

	// When directPlay=0, an explicit client maxVideoBitrate still steers source selection.
	rTranscode := httptest.NewRequest(http.MethodGet,
		"/video/:/transcode/universal/decision?path=/library/metadata/"+fightClub.Key.String()+
			"&directPlay=0&directStream=1&location=wan&maxVideoBitrate=8000", nil)
	rTranscode.RemoteAddr = "198.51.100.10:41236"
	rTranscode.Header.Set("X-Plex-Token", ownerToken)
	rTranscode.Header.Set("X-Plex-Product", "Plezy")
	rTranscode.Header.Set("X-Plex-Platform", "tvOS")
	rTranscode.Header.Set("X-Plex-Client-Identifier", "plezy-atv-remote")
	prT := ParseRequest(rTranscode)
	prT.User = &ownerUser
	rTranscode = rTranscode.WithContext(withRequest(rTranscode.Context(), prT))

	pT, err := f.srv.parsePlayRequest(rTranscode)
	if err != nil {
		t.Fatalf("parsePlayRequest directPlay=0: %v", err)
	}
	selT := pT.sourceSelectionCaps()
	if selT.MaxBitrateKbps != 8000 || selT.DefaultRemoteBitrate {
		t.Fatalf("directPlay=0 explicit maxVideoBitrate dropped: %+v", selT)
	}
}

func TestPlezyHeaderlessPartReadInheritsCapsAndIsolatesDevices(t *testing.T) {
	sendReq := func(f *fixture, method, target, remoteAddr string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req.RemoteAddr = remoteAddr
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, req)
		return rec
	}

	t.Run("Plezy Apple TV headerless libmpv part read keeps 4K HEVC HDR DV", func(t *testing.T) {
		f := newFixture()
		defer f.srv.Close()
		var gotCaps frontend.ClientCaps
		f.srv.setPlayback(captureCapsPlayback{Playback: f.playback, onPrepare: func(caps frontend.ClientCaps) { gotCaps = caps }})

		sendReq(f, http.MethodGet, "/library/metadata/"+fightClub.Key.String(), "198.51.100.20:50001", map[string]string{
			"X-Plex-Token":             ownerToken,
			"X-Plex-Product":           "Plezy",
			"X-Plex-Platform":          "tvOS",
			"X-Plex-Device":            "Apple TV",
			"X-Plex-Client-Identifier": "plezy-atv-1",
		})
		rec := sendReq(f, http.MethodGet, "/library/parts/"+fightClub.Key.String()+"/0/file.mkv?X-Plex-Token="+ownerToken, "198.51.100.20:50002", map[string]string{
			"User-Agent": "libmpv",
			"Range":      "bytes=0-15",
		})
		if rec.Code != http.StatusPartialContent && rec.Code != http.StatusOK {
			t.Fatalf("part status = %d", rec.Code)
		}
		if gotCaps.MaxHeight != 2160 || !gotCaps.SupportsVideo("hevc") || !gotCaps.HDR10 || !gotCaps.DolbyVision {
			t.Fatalf("Plezy Apple TV headerless part caps = %+v, want 2160p HEVC HDR10 DV", gotCaps)
		}
	})

	t.Run("Plezy macOS headerless mpv part read after playQueue keeps 4K HEVC AV1 HDR", func(t *testing.T) {
		f := newFixture()
		defer f.srv.Close()
		var gotCaps frontend.ClientCaps
		f.srv.setPlayback(captureCapsPlayback{Playback: f.playback, onPrepare: func(caps frontend.ClientCaps) { gotCaps = caps }})

		pqURI := url.QueryEscape("/library/metadata/" + fightClub.Key.String())
		sendReq(f, http.MethodPost, "/playQueues?type=video&uri="+pqURI, "198.51.100.21:50001", map[string]string{
			"X-Plex-Token":             ownerToken,
			"X-Plex-Product":           "Plezy",
			"X-Plex-Platform":          "macOS",
			"X-Plex-Device":            "MacBook Pro",
			"X-Plex-Client-Identifier": "plezy-mac-1",
		})
		rec := sendReq(f, http.MethodGet, "/library/parts/"+fightClub.Key.String()+"/0/file.mkv?X-Plex-Token="+ownerToken, "198.51.100.21:50002", map[string]string{
			"User-Agent": "mpv 0.38.0",
			"Range":      "bytes=0-15",
		})
		if rec.Code != http.StatusPartialContent && rec.Code != http.StatusOK {
			t.Fatalf("part status = %d", rec.Code)
		}
		if gotCaps.MaxHeight != 2160 || !gotCaps.SupportsVideo("hevc") || !gotCaps.SupportsVideo("av1") || !gotCaps.HDR10 || gotCaps.DolbyVision {
			t.Fatalf("Plezy macOS headerless part caps = %+v, want 2160p HEVC AV1 HDR10 no-DV", gotCaps)
		}
	})

	t.Run("same user multi-device isolation on same IP and explicit client ID", func(t *testing.T) {
		f := newFixture()
		defer f.srv.Close()
		var gotCaps frontend.ClientCaps
		f.srv.setPlayback(captureCapsPlayback{Playback: f.playback, onPrepare: func(caps frontend.ClientCaps) { gotCaps = caps }})

		// Device A (Android phone, 1080p) and Device B (Plezy Apple TV, 4K DV)
		// both use the same user + same remote IP.
		sendReq(f, http.MethodGet, "/library/metadata/"+fightClub.Key.String(), "198.51.100.30:51001", map[string]string{
			"X-Plex-Token":             ownerToken,
			"X-Plex-Product":           "Plex for Android (Mobile)",
			"X-Plex-Platform":          "Android",
			"X-Plex-Device":            "Pixel 8",
			"X-Plex-Client-Identifier": "android-phone-1",
		})
		sendReq(f, http.MethodGet, "/library/metadata/"+fightClub.Key.String(), "198.51.100.30:51002", map[string]string{
			"X-Plex-Token":             ownerToken,
			"X-Plex-Product":           "Plezy",
			"X-Plex-Platform":          "tvOS",
			"X-Plex-Device":            "Apple TV",
			"X-Plex-Client-Identifier": "plezy-atv-1",
		})

		// An explicit ClientID belonging to an unseen device must NEVER inherit
		// either recorded device's caps.
		gotCaps = frontend.ClientCaps{}
		sendReq(f, http.MethodGet, "/library/parts/"+fightClub.Key.String()+"/0/file.mkv?X-Plex-Token="+ownerToken, "198.51.100.30:51003", map[string]string{
			"X-Plex-Client-Identifier": "unseen-device",
			"User-Agent":               "libmpv",
			"Range":                    "bytes=0-15",
		})
		if gotCaps.Product != "" || gotCaps.MaxHeight != 1080 {
			t.Fatalf("unseen explicit ClientID inherited another device's caps: %+v", gotCaps)
		}

		// A clientless part read for the contested title on the contested IP must
		// NOT guess between the Android phone and Apple TV.
		gotCaps = frontend.ClientCaps{}
		sendReq(f, http.MethodGet, "/library/parts/"+fightClub.Key.String()+"/0/file.mkv?X-Plex-Token="+ownerToken, "198.51.100.30:51004", map[string]string{
			"User-Agent": "libmpv",
			"Range":      "bytes=0-15",
		})
		if gotCaps.Product != "Native player" || gotCaps.MaxHeight != 1080 {
			t.Fatalf("contested title/IP clientless read guessed wrong caps: %+v", gotCaps)
		}

		// When only Apple TV requests a distinct title (alien), byTitlePeer is
		// unambiguous even though byPeer is ambiguous, so Apple TV gets 4K DV.
		sendReq(f, http.MethodGet, "/library/metadata/"+alien.Key.String(), "198.51.100.30:51005", map[string]string{
			"X-Plex-Token":             ownerToken,
			"X-Plex-Product":           "Plezy",
			"X-Plex-Platform":          "tvOS",
			"X-Plex-Device":            "Apple TV",
			"X-Plex-Client-Identifier": "plezy-atv-1",
		})
		gotCaps = frontend.ClientCaps{}
		sendReq(f, http.MethodGet, "/library/parts/"+alien.Key.String()+"/0/file.mkv?X-Plex-Token="+ownerToken, "198.51.100.30:51006", map[string]string{
			"User-Agent": "libmpv",
			"Range":      "bytes=0-15",
		})
		if gotCaps.MaxHeight != 2160 || !gotCaps.DolbyVision {
			t.Fatalf("unambiguous title on shared IP did not get Apple TV caps: %+v", gotCaps)
		}

		// A part read whose ClientID explicitly matches plezy-atv-1 (without
		// X-Plex-Product/Platform) resolves deterministically via byClient even on
		// the contested title.
		gotCaps = frontend.ClientCaps{}
		sendReq(f, http.MethodGet, "/library/parts/"+fightClub.Key.String()+"/0/file.mkv?X-Plex-Token="+ownerToken, "198.51.100.30:51007", map[string]string{
			"X-Plex-Client-Identifier": "plezy-atv-1",
			"Range":                    "bytes=0-15",
		})
		if gotCaps.MaxHeight != 2160 || !gotCaps.DolbyVision {
			t.Fatalf("matching ClientID did not inherit exact device caps: %+v", gotCaps)
		}
	})

	t.Run("contradicting User-Agent never inherits incompatible platform", func(t *testing.T) {
		f := newFixture()
		defer f.srv.Close()
		var gotCaps frontend.ClientCaps
		f.srv.setPlayback(captureCapsPlayback{Playback: f.playback, onPrepare: func(caps frontend.ClientCaps) { gotCaps = caps }})

		sendReq(f, http.MethodGet, "/library/metadata/"+fightClub.Key.String(), "198.51.100.40:52001", map[string]string{
			"X-Plex-Token":             ownerToken,
			"X-Plex-Product":           "Plezy",
			"X-Plex-Platform":          "tvOS",
			"X-Plex-Device":            "Apple TV",
			"X-Plex-Client-Identifier": "plezy-atv-2",
		})
		sendReq(f, http.MethodGet, "/library/parts/"+fightClub.Key.String()+"/0/file.mkv?X-Plex-Token="+ownerToken, "198.51.100.40:52002", map[string]string{
			"User-Agent": "ExoPlayerLib/2.19 (Linux; Android 14)",
			"Range":      "bytes=0-15",
		})
		if gotCaps.Product != "Native player" || gotCaps.DolbyVision {
			t.Fatalf("Android UA inherited tvOS profile: %+v", gotCaps)
		}
	})
}

type fakeResolverIndexer struct{ releases []core.Release }

func (fakeResolverIndexer) Name() string { return "fake" }

func (f fakeResolverIndexer) Search(context.Context, core.SearchRequest) ([]core.Release, error) {
	return f.releases, nil
}

func (f fakeResolverIndexer) FetchNZB(_ context.Context, r core.Release) ([]byte, error) {
	return []byte("nzb:" + r.ID), nil
}

type fakeResolverHealth struct{}

func (fakeResolverHealth) Check(context.Context, []byte) (bool, error) {
	return true, nil
}

func TestCeilingWhyNamesDeclarationSource(t *testing.T) {
	title := &core.Title{
		Key: core.MovieKey(550), Type: core.TypeMovie, Title: "Fight Club", Year: 1999,
		Runtime: 139 * time.Minute, Language: "en",
	}
	mkRel := func(id, name string, sizeMB int64) core.Release {
		return core.Release{
			ID: id, GUID: id, Indexer: "fake", Name: name, NZBURL: "x",
			Size: sizeMB << 20, PubDate: fixedNow.Add(-30 * 24 * time.Hour), Grabs: 10,
			Info: releaseparse.Parse(name),
		}
	}
	r4k := mkRel("r4k", "Fight.Club.1999.2160p.UHD.BluRay.REMUX.DV.HDR.HEVC-GRP", 40<<10)
	r1080 := mkRel("r1080", "Fight.Club.1999.1080p.BluRay.x264-GRP", 10<<10)

	cfg := config.Defaults(t.TempDir())
	cfg.Quality.Default = "4K"
	cfg.Quality.Profiles = []core.QualityProfile{{Name: "4K", MinHeight: 1080, MaxHeight: 2160, AllowHDR: true, AllowDolbyVision: true}}

	cases := []struct {
		name     string
		caps     core.ClientCaps
		wantSubs []string
	}{
		{
			name:     "unknown client generic registry fallback",
			caps:     (&Request{Product: "MysteryClient"}).ClientCaps(0),
			wantSubs: []string{"1080p", `generic registry fallback for unknown client "MysteryClient"`},
		},
		{
			name: "clientless part read generic fallback",
			caps: func() core.ClientCaps {
				req := httptest.NewRequest(http.MethodGet, "/library/parts/1000000000000550/0/file.mkv", nil)
				req.Header.Set("User-Agent", "libmpv")
				pr := &Request{User: &core.User{ID: 1}}
				pr.adoptNativeDevice(req)
				return pr.ClientCaps(0)
			}(),
			wantSubs: []string{"1080p", "generic fallback profile (no client headers on /library/parts request", `"libmpv"`},
		},
		{
			name: "profile extra add-limitation",
			caps: (&Request{
				Product:      "Plezy",
				Platform:     "tvOS",
				ProfileExtra: "add-limitation(scope=videoCodec&scopeName=hevc&type=upperBound&name=video.height&value=1080)",
			}).ClientCaps(0),
			wantSubs: []string{"1080p", "X-Plex-Client-Profile-Extra add-limitation(scope=videoCodec, scopeName=hevc, video.height=1080)"},
		},
		{
			name: "legacy client capabilities",
			caps: (&Request{
				Product:            "Plezy",
				Platform:           "tvOS",
				ClientCapabilities: "videoDecoders=hevc{resolution:1080},h264{resolution:1080}",
			}).ClientCaps(0),
			wantSubs: []string{"1080p", "X-Plex-Client-Capabilities (videoDecoders resolution=1080)"},
		},
		{
			name: "request parameter videoResolution",
			caps: func() core.ClientCaps {
				c := (&Request{Product: "Plezy", Platform: "tvOS"}).ClientCaps(0)
				c.MaxHeight = 1080
				c.MaxHeightSource = "request parameter videoResolution=1920x1080"
				return c
			}(),
			wantSubs: []string{"1080p", "request parameter videoResolution=1920x1080"},
		},
		{
			name: "owner app compatibility override",
			caps: (&Request{Product: "Plezy", Platform: "tvOS"}).ClientCapsWith(0, []frontend.ClientCapabilityOverride{
				{Product: "Plezy", Force: true, MaxHeight: 1080},
			}),
			wantSubs: []string{"1080p", "App compatibility forced override"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := resolver.New(fakeResolverIndexer{releases: []core.Release{r4k, r1080}}, fakeResolverHealth{}, nil, cfg, resolver.Options{})
			defer svc.Close()
			if _, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: title, Client: tc.caps}); err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			traces := svc.Traces()
			if len(traces) == 0 {
				t.Fatal("missing resolver trace")
			}
			got := traces[0].Ceiling
			for _, sub := range tc.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("why = %q, want substring %q", got, sub)
				}
			}
		})
	}
}
