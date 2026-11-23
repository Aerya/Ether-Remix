package plex

import (
	"context"
	"encoding/json"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type w10Editions struct {
	testEditions
	rs *resolver.Service
}

func (e w10Editions) VersionSlots(ctx context.Context, req core.ResolveRequest, max int, allowDiscovery bool) ([]core.ReleaseVariant, error) {
	return e.rs.VersionSlots(ctx, req, max, allowDiscovery)
}

type w10Health struct {
	altChecks atomic.Int32
}

func (h *w10Health) Check(_ context.Context, nzb []byte) (bool, error) {
	if string(nzb) == "00326d40" {
		h.altChecks.Add(1)
		return false, context.DeadlineExceeded
	}
	return true, nil
}

type w10Fixture struct {
	f      *fixture
	mgr    *playback.Manager
	rs     *resolver.Service
	tr     *issue94Transcoder
	health *w10Health
}

func newW10Fixture(t *testing.T) *w10Fixture {
	t.Helper()
	cfg := config.Defaults(t.TempDir())
	cfg.Quality.Default = "4K"
	cfg.Quality.Remote = "4K"
	cfg.Transcode.RemoteMaxBitrateKbps = 8000
	cfg.Resolver.SameQualityAlternatives = 1

	now := time.Now()
	releases := []core.Release{
		{
			ID:      "fc68e004",
			GUID:    "fc68e004",
			Indexer: "offline",
			Name:    "Fjord.Harbor.1999.2160p.WEBRip.x265.10bit.DTS-HD.MA-GRP",
			Size:    24000 << 20,
			PubDate: now.Add(-24 * time.Hour),
			NZBURL:  "offline",
		},
		{
			ID:      "00326d40",
			GUID:    "00326d40",
			Indexer: "offline",
			Name:    "Fjord.Harbor.1999.2160p.WEB-DL.DDP5.1.H.265-ALT",
			Size:    19000 << 20,
			PubDate: now.Add(-24 * time.Hour),
			NZBURL:  "offline",
		},
		{
			ID:      "1f963fa5",
			GUID:    "1f963fa5",
			Indexer: "offline",
			Name:    "Fjord.Harbor.1999.1080p.WEB-DL.DD5.1.H.264-EVO",
			Size:    4200 << 20,
			PubDate: now.Add(-24 * time.Hour),
			NZBURL:  "offline",
		},
	}

	health := &w10Health{}
	rs := resolver.New(&issue94Indexer{releases: releases}, health, nil, cfg, resolver.Options{})
	t.Cleanup(func() { rs.Close() })

	f := newFixture(func(d *testServerDeps) {
		d.DisableEnrichment = true
		setDepsConfig(d, func() config.Config { return cfg })
	})
	f.srv.mutateServices(func(svc *hostServices) {
		svc.variants = w10Editions{testEditions: testEditions{src: rs}, rs: rs}
	})

	tr := &issue94Transcoder{newFakeTranscoder(core.Decision{})}
	mgr, err := playback.New(playback.Deps{
		Catalog:    f.catalog,
		Resolver:   rs,
		Engine:     issue94Engine{},
		Transcoder: tr,
		SelectProfile: func(c core.ClientCaps, u *core.User, _ string) core.QualityProfile {
			return rs.ProfileFor(c, u)
		},
		NativeProbe: func(_ context.Context, s core.MediaSource) (*core.MediaInfo, error) {
			switch s.ReleaseID() {
			case "fc68e004":
				return &core.MediaInfo{
					Container:   "mkv",
					Duration:    139 * time.Minute,
					BitrateKbps: 30594,
					Size:        24000 << 20,
					Streams: []core.StreamInfo{
						{Index: 0, Type: "video", Codec: "hevc", Profile: "main 10", Width: 3840, Height: 2160, BitDepth: 10},
						{Index: 1, Type: "audio", Codec: "dts", Channels: 6, Language: "eng", Default: true},
					},
				}, nil
			default:
				return &core.MediaInfo{
					Container:   "mkv",
					Duration:    139 * time.Minute,
					BitrateKbps: 5200,
					Size:        4200 << 20,
					Streams: []core.StreamInfo{
						{Index: 0, Type: "video", Codec: "h264", Profile: "high", Level: 41, Width: 1920, Height: 1080, BitDepth: 8},
						{Index: 1, Type: "audio", Codec: "ac3", Channels: 6, Language: "eng", Default: true},
					},
				}, nil
			}
		},
	}, cfg, playback.Options{
		SessionSurface: func(surface string) string {
			if surface == "plex" {
				return ""
			}
			return surface
		},
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.srv.setPlayback(mgr)
	f.srv.setTranscoder(tr)
	t.Cleanup(func() {
		f.srv.Close()
		mgr.Close()
	})
	return &w10Fixture{f: f, mgr: mgr, rs: rs, tr: tr, health: health}
}

func TestRedecisionWithBitrateCapSwitchesReleaseAndDirectStreams(t *testing.T) {
	for _, mediaIndex := range []string{"0", "1"} {
		t.Run("mediaIndex="+mediaIndex, func(t *testing.T) {
			w := newW10Fixture(t)
			hdr := []string{
				"X-Plex-Client-Identifier", "04a814bdf7a4d41c-com-plexapp-android",
				"X-Plex-Session-Identifier", "play-1",
				"X-Plex-Product", "Plex for Android (TV)",
				"X-Plex-Platform", "Android",
				"X-Plex-Device", "BRAVIA 4K VH2",
				"Accept", "application/json",
			}

			// Detail fetch populates configured version slots for this client.
			if r := w.f.get(t, "/library/metadata/"+fightClub.Key.String(), hdr...); r.Code != 200 {
				t.Fatalf("detail %d", r.Code)
			}

			// Initial remote 4K play opens fc68e004 (~30.6 Mbps 2160p).
			initQ := url.Values{
				"path":         {"/library/metadata/" + fightClub.Key.String()},
				"mediaIndex":   {"0"},
				"partIndex":    {"0"},
				"protocol":     {"hls"},
				"hasMDE":       {"1"},
				"location":     {"wan"},
				"directPlay":   {"1"},
				"directStream": {"1"},
				"session":      {"04a814bdf7a4d41c-com-plexapp-android"},
			}
			if r := w.f.get(t, "/video/:/transcode/universal/decision?"+initQ.Encode(), hdr...); r.Code != 200 {
				t.Fatalf("initial decision %d: %s", r.Code, r.Body.String())
			}
			first, ok := w.mgr.SessionFor(ownerUser.ID, "04a814bdf7a4d41c-com-plexapp-android", fightClub.Key)
			if !ok || first.Resolution == nil || first.Resolution.Release.ID != "fc68e004" {
				t.Fatalf("initial session = %+v (ok=%t), want fc68e004", first, ok)
			}

			// Viewer lowers quality in the app: directPlay=0&directStream=0&maxVideoBitrate=8000.
			reQ := url.Values{
				"path":            {"/library/metadata/" + fightClub.Key.String()},
				"mediaIndex":      {mediaIndex},
				"partIndex":       {"0"},
				"protocol":        {"hls"},
				"hasMDE":          {"1"},
				"location":        {"wan"},
				"directPlay":      {"0"},
				"directStream":    {"0"},
				"maxVideoBitrate": {"8000"},
				"session":         {"04a814bdf7a4d41c-com-plexapp-android"},
			}
			decResp := w.f.get(t, "/video/:/transcode/universal/decision?"+reQ.Encode(), hdr...)
			if decResp.Code != 200 {
				t.Fatalf("re-decision %d: %s", decResp.Code, decResp.Body.String())
			}
			startResp := w.f.get(t, "/video/:/transcode/universal/start.m3u8?"+reQ.Encode(), hdr...)
			if startResp.Code != 200 {
				t.Fatalf("start.m3u8 %d: %s", startResp.Code, startResp.Body.String())
			}

			cur, ok := w.mgr.SessionFor(ownerUser.ID, "04a814bdf7a4d41c-com-plexapp-android", fightClub.Key)
			if !ok || cur.Resolution == nil || cur.Resolution.Release.ID != "1f963fa5" {
				t.Fatalf("active session after 8 Mbps re-decision = %+v (ok=%t), want 1f963fa5", cur, ok)
			}

			w.tr.mu.Lock()
			hls := w.tr.sessions["04a814bdf7a4d41c-com-plexapp-android"]
			w.tr.mu.Unlock()
			if hls == nil {
				t.Fatal("missing started HLS session")
			}
			if hls.req.ReleaseID != "1f963fa5" {
				t.Fatalf("transcode started on %q, want 1f963fa5", hls.req.ReleaseID)
			}
			if !hls.req.Decision.VideoCopy {
				t.Fatalf("5.2 Mbps H.264 source under 8 Mbps cap was video-transcoded instead of direct-streamed: %+v", hls.req.Decision)
			}

			var wire struct {
				MediaContainer struct {
					Metadata []struct {
						Media []struct {
							Part []struct {
								Stream []struct {
									StreamType int    `json:"streamType"`
									Decision   string `json:"decision"`
								} `json:"Stream"`
							} `json:"Part"`
						} `json:"Media"`
					} `json:"Metadata"`
				} `json:"MediaContainer"`
			}
			if err := json.Unmarshal(decResp.Body.Bytes(), &wire); err != nil {
				t.Fatal(err)
			}
			videoDec := ""
			for _, m := range wire.MediaContainer.Metadata[0].Media {
				if len(m.Part) > 0 && len(m.Part[0].Stream) > 0 {
					for _, st := range m.Part[0].Stream {
						if st.StreamType == 1 && st.Decision != "" {
							videoDec = st.Decision
						}
					}
				}
			}
			if videoDec != "copy" {
				t.Fatalf("decision response video stream decision = %q, want copy", videoDec)
			}

			// Question 2: when mediaIndex=1 timed out on 00326d40, stalePrefers must
			// prevent re-checking 00326d40 on subsequent decisions in the session.
			if mediaIndex == "1" {
				if got := w.health.altChecks.Load(); got != 1 {
					t.Fatalf("initial mediaIndex=1 health checks on 00326d40 = %d, want 1", got)
				}
				again := w.f.get(t, "/video/:/transcode/universal/decision?"+reQ.Encode(), hdr...)
				if again.Code != 200 {
					t.Fatalf("repeat decision %d", again.Code)
				}
				if got := w.health.altChecks.Load(); got != 1 {
					t.Fatalf("repeat mediaIndex=1 re-checked unavailable preferred release: checks=%d, want 1", got)
				}
			}
		})
	}
}

func TestRedecisionBelowFittingReleaseTranscodesChosenHDSource(t *testing.T) {
	w := newW10Fixture(t)
	hdr := []string{
		"X-Plex-Client-Identifier", "04a814bdf7a4d41c-com-plexapp-android",
		"X-Plex-Session-Identifier", "play-4m",
		"X-Plex-Product", "Plex for Android (TV)",
		"X-Plex-Platform", "Android",
		"Accept", "application/json",
	}

	initQ := url.Values{
		"path":         {"/library/metadata/" + fightClub.Key.String()},
		"mediaIndex":   {"0"},
		"partIndex":    {"0"},
		"protocol":     {"hls"},
		"hasMDE":       {"1"},
		"location":     {"wan"},
		"directPlay":   {"1"},
		"directStream": {"1"},
		"session":      {"sess-4m"},
	}
	if r := w.f.get(t, "/video/:/transcode/universal/decision?"+initQ.Encode(), hdr...); r.Code != 200 {
		t.Fatalf("initial decision %d", r.Code)
	}

	// Lower quality to 4 Mbps: neither 4K (30.6 Mbps) nor 1080p (5.2 Mbps) fits
	// without transcoding, so transcode must run on 1f963fa5 rather than fc68e004.
	reQ := url.Values{
		"path":            {"/library/metadata/" + fightClub.Key.String()},
		"mediaIndex":      {"0"},
		"partIndex":       {"0"},
		"protocol":        {"hls"},
		"hasMDE":          {"1"},
		"location":        {"wan"},
		"directPlay":      {"0"},
		"directStream":    {"0"},
		"maxVideoBitrate": {"4000"},
		"videoResolution": {"852x480"},
		"session":         {"sess-4m"},
	}
	if r := w.f.get(t, "/video/:/transcode/universal/decision?"+reQ.Encode(), hdr...); r.Code != 200 {
		t.Fatalf("4 Mbps decision %d: %s", r.Code, r.Body.String())
	}
	if r := w.f.get(t, "/video/:/transcode/universal/start.m3u8?"+reQ.Encode(), hdr...); r.Code != 200 {
		t.Fatalf("4 Mbps start.m3u8 %d: %s", r.Code, r.Body.String())
	}

	w.tr.mu.Lock()
	hls := w.tr.sessions["sess-4m"]
	w.tr.mu.Unlock()
	if hls == nil {
		t.Fatal("missing started HLS session")
	}
	if hls.req.ReleaseID != "1f963fa5" {
		t.Fatalf("4 Mbps transcode started on %q, want 1f963fa5", hls.req.ReleaseID)
	}
	if hls.req.Decision.Kind != core.DecisionTranscode || hls.req.Decision.VideoCopy {
		t.Fatalf("expected video transcode for 5.2 Mbps source under 4 Mbps cap, got %+v", hls.req.Decision)
	}
	_ = transcode.Policy{}
}
