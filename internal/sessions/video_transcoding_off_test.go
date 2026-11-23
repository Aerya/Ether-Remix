package sessions

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestDecideProfileVideoTranscodingOff(t *testing.T) {
	hevc := core.MediaInfo{
		Container: "mkv",
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
		},
	}
	h264TrueHD := core.MediaInfo{
		Container: "mkv",
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "truehd", Channels: 6},
		},
	}
	h264OnlyCaps := core.ClientCaps{
		Product:       "Plex Web",
		Platform:      "Chrome",
		MaxHeight:     1080,
		Containers:    []string{"mp4"},
		HLSContainers: []string{"fmp4", "mpegts"},
		VideoCodecs:   []string{"h264"},
		AudioCodecs:   []string{"aac"},
	}

	b := &Broker{deps: BrokerDeps{
		SurfacePolicy:       testSessionPolicy,
		Transcoder:          profileTranscoder{},
		VideoTranscodingOff: func() bool { return true },
	}}

	// Without declared profiles: HEVC on H.264 client fails with ErrVideoTranscodingDisabled.
	req := frontend.PlanRequest{
		Caps:   h264OnlyCaps,
		Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}},
	}
	_, err := b.decideProfile(&core.PlaySession{Info: hevc}, req)
	if !errors.Is(err, core.ErrVideoTranscodingDisabled) {
		t.Fatalf("HEVC without profiles: err = %v, want ErrVideoTranscodingDisabled", err)
	}
	var noPath *frontend.NoCompatiblePathError
	if !errors.As(err, &noPath) || !strings.Contains(err.Error(), reasonVideoTranscodingOff) {
		t.Fatalf("expected NoCompatiblePathError mentioning %q, got %v", reasonVideoTranscodingOff, err)
	}

	// With declared profiles: HEVC still rejected with ErrVideoTranscodingDisabled.
	profCaps := h264OnlyCaps
	profCaps.Profiles = &core.ClientCapabilityProfiles{
		DirectPlay:  []core.ClientDirectPlayProfile{{MediaType: "video", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}},
		Transcoding: []core.ClientTranscodeProfile{{MediaType: "video", Delivery: "hls", Containers: []string{"mpegts", "fmp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}},
	}
	reqWithProfiles := req
	reqWithProfiles.Caps = profCaps
	_, err = b.decideProfile(&core.PlaySession{Info: hevc}, reqWithProfiles)
	if !errors.Is(err, core.ErrVideoTranscodingDisabled) {
		t.Fatalf("HEVC with profiles: err = %v, want ErrVideoTranscodingDisabled", err)
	}

	// Video copy + audio conversion (H.264 + TrueHD -> AAC) succeeds!
	dec, err := b.decideProfile(&core.PlaySession{Info: h264TrueHD}, reqWithProfiles)
	if err != nil {
		t.Fatalf("H.264 + TrueHD audio conversion failed: %v", err)
	}
	if !dec.VideoCopy || dec.AudioCopy {
		t.Fatalf("expected video copy + audio conversion, got %+v", dec)
	}

	// planAlternatives still reports encode=true for Jellyfin audio-only conversion,
	// without enabling video transcode.
	_, _, encode := b.planAlternatives(&core.PlaySession{Info: h264TrueHD}, reqWithProfiles)
	if !encode {
		t.Fatal("expected audio-only conversion to report Transcoding=true in planAlternatives")
	}

	// Proactive fallback and Vela quality plan are disabled when video transcoding is off.
	if b.ProactiveFallbackAvailable() {
		t.Fatal("ProactiveFallbackAvailable() must be false when video transcoding is off")
	}
	variants, limits, err := b.qualityPlan(context.Background(), &brokerSession{public: frontend.Session{Protocol: "vela"}}, &core.Viewer{ID: 1})
	if err != nil || len(variants) != 0 || len(limits) != 0 {
		t.Fatalf("qualityPlan = (%v, %v, %v), want empty variants and no height-0 limitations", variants, limits, err)
	}
}

type realQualityTranscoder struct {
	profileTranscoder
}

func (realQualityTranscoder) ProactiveFallbackAvailable() bool { return true }

type liveNoOpBackend struct{}

func (liveNoOpBackend) Allowed(context.Context, *core.Viewer, string) error { return nil }
func (liveNoOpBackend) Tune(context.Context, *core.Viewer, string, core.ClientCaps, string) (string, error) {
	return "live-1", nil
}
func (liveNoOpBackend) Serve(http.ResponseWriter, *http.Request, *core.Viewer, string, string, string) {
}
func (liveNoOpBackend) Stop(context.Context, *core.Viewer, string) error { return nil }

func TestPerViewerNoVideoTranscodeBrokerPaths(t *testing.T) {
	ctx := context.Background()
	hevc := core.MediaInfo{
		Container: "mkv",
		Duration:  time.Hour,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
		},
	}
	h264TrueHD := core.MediaInfo{
		Container: "mkv",
		Duration:  time.Hour,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "truehd", Channels: 6},
		},
	}
	h264OnlyCaps := core.ClientCaps{
		ClientID:      "web-1",
		Product:       "Plex Web",
		Platform:      "Chrome",
		MaxHeight:     1080,
		Containers:    []string{"mp4"},
		HLSContainers: []string{"fmp4", "mpegts"},
		VideoCodecs:   []string{"h264"},
		AudioCodecs:   []string{"aac"},
	}

	db := store.OpenTest(t)
	unrestricted := &core.Viewer{ID: 1, Username: "owner", IsOwner: true}
	restricted := &core.Viewer{ID: 2, Username: "friend"}
	active := func(_ context.Context, id int64) (*core.Viewer, error) {
		if id == restricted.ID {
			return restricted, nil
		}
		return unrestricted, nil
	}
	events := NewEventBus(nil, active)
	defer events.Close()

	pb := &switchFakePlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}}
	tr := realQualityTranscoder{}
	b, err := NewBroker(BrokerDeps{
		DB: db,
		SurfacePolicy: func(p core.Protocol) core.SessionPolicy {
			if p == "plex" {
				return core.SessionPolicy{ExternalAdmission: true}
			}
			return testSessionPolicy(p)
		},
		Playback:     pb,
		Transcoder:   tr,
		Progress:     &fakeProgress{},
		OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		ActiveViewer: active,
		CanPlay:      func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit: func(*core.Viewer) int { return 0 },
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k, Type: core.TypeMovie, Title: "Test"}, nil
		},
		Variants: func(context.Context, *core.Viewer, *core.Title, core.ClientCaps) ([]core.ReleaseVariant, error) {
			return []core.ReleaseVariant{
				{ID: "rel-h264", Release: core.Release{ID: "rel-h264"}, Availability: core.VersionInstant, Info: h264TrueHD},
				{ID: "rel-hevc", Release: core.Release{ID: "rel-hevc"}, Availability: core.VersionCached, Info: hevc},
			}, nil
		},
		VideoTranscodingOff: func() bool { return false },
		ViewerVideoTranscodingOff: func(v *core.Viewer) bool {
			return v != nil && v.ID == restricted.ID
		},
		Events: events,
		Now:    time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)

	// 1. ProactiveFallbackAvailable stays true server-wide, false for restricted viewer.
	if !b.ProactiveFallbackAvailable() {
		t.Fatal("ProactiveFallbackAvailable() must remain true when only one viewer is restricted")
	}
	if !b.ProactiveFallbackAvailableFor(unrestricted) {
		t.Fatal("ProactiveFallbackAvailableFor(unrestricted) = false, want true")
	}
	if b.ProactiveFallbackAvailableFor(restricted) {
		t.Fatal("ProactiveFallbackAvailableFor(restricted) = true, want false")
	}

	// Vela qualityPlan returns empty for the restricted viewer even while server transcoding is on.
	pb.sessions["vela-src"] = &core.PlaySession{ID: "vela-src", Info: core.MediaInfo{
		Container: "mp4", Duration: time.Hour, BitrateKbps: 15000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", BitrateKbps: 192},
		},
	}}
	velaSess := &brokerSession{
		public:   frontend.Session{Protocol: "vela", ViewerID: restricted.ID, Item: core.MovieKey(1)},
		playID:   "vela-src",
		caps:     core.ClientCaps{VideoCodecs: []string{"h264"}, MaxHeight: 1080},
		opts:     core.DecideOptions{DirectPlay: true, DirectStream: true},
		decision: core.Decision{Kind: core.DecisionRemux, Container: "fmp4", VideoCopy: true, AudioCopy: true, AudioStream: 1},
	}
	variants, limits, err := b.qualityPlan(ctx, velaSess, restricted)
	if err != nil || len(variants) != 0 || len(limits) != 0 {
		t.Fatalf("qualityPlan(restricted) = (%v, %v, %v), want empty", variants, limits, err)
	}

	// 2. Plex admit path (planPlexAdmission):
	//    - Restricted viewer admitting HEVC on H.264-only client -> ErrVideoTranscodingDisabled.
	//    - Restricted viewer admitting H.264 + TrueHD (even with AlwaysEncodeVideo) -> video copy succeeds.
	//    - Unrestricted viewer admitting HEVC -> succeeds with video transcode.
	t.Run("Plex admit path enforces ViewerVideoTranscodingOff and overrides AlwaysEncodeVideo", func(t *testing.T) {
		hevcPS := &core.PlaySession{
			ID: "ps-hevc", UserID: restricted.ID, ClientID: h264OnlyCaps.ClientID, Key: core.MovieKey(10),
			Info: hevc, Resolution: &core.Resolution{Release: core.Release{ID: "rel-hevc"}},
		}
		pb.sessions[hevcPS.ID] = hevcPS
		_, err := b.planPlexAdmission(ctx, frontend.PlanRequest{
			Viewer:   restricted,
			Device:   core.Device{ID: h264OnlyCaps.ClientID},
			Protocol: "plex",
			Item:     hevcPS.Key,
			Caps:     h264OnlyCaps,
			Intent:   frontend.PlayIntent{ClientSessionID: "plex-sess-hevc", Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}},
		}, func(context.Context) (PlanAdmission, error) {
			return PlanAdmission{Source: hevcPS, Caps: h264OnlyCaps, Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}}, nil
		})
		if !errors.Is(err, core.ErrVideoTranscodingDisabled) {
			t.Fatalf("planPlexAdmission(restricted, HEVC) err = %v, want ErrVideoTranscodingDisabled", err)
		}

		// H.264 + TrueHD with conflicting AlwaysEncodeVideo=true: NoVideoTranscode wins -> VideoCopy=true.
		h264PS := &core.PlaySession{
			ID: "ps-h264", UserID: restricted.ID, ClientID: h264OnlyCaps.ClientID, Key: core.MovieKey(11),
			Info: h264TrueHD, Resolution: &core.Resolution{Release: core.Release{ID: "rel-h264"}},
		}
		pb.sessions[h264PS.ID] = h264PS
		conflictCaps := h264OnlyCaps
		conflictCaps.AlwaysEncodeVideo = true
		plan, err := b.planPlexAdmission(ctx, frontend.PlanRequest{
			Viewer:   restricted,
			Device:   core.Device{ID: h264OnlyCaps.ClientID},
			Protocol: "plex",
			Item:     h264PS.Key,
			Caps:     conflictCaps,
			Intent:   frontend.PlayIntent{ClientSessionID: "plex-sess-h264", Options: core.DecideOptions{DirectPlay: false, DirectStream: true, AudioStream: 1, SubtitleStream: -1}},
		}, func(context.Context) (PlanAdmission, error) {
			return PlanAdmission{Source: h264PS, Caps: conflictCaps, Options: core.DecideOptions{DirectPlay: false, DirectStream: true, AudioStream: 1, SubtitleStream: -1}}, nil
		})
		if err != nil {
			t.Fatalf("planPlexAdmission(restricted, H264+AlwaysEncodeVideo) err = %v, want video copy", err)
		}
		if !plan.Decision.VideoCopy || plan.Decision.AudioCopy || !plan.VideoTranscodingOff {
			t.Fatalf("planPlexAdmission(restricted, H264) = %+v, want VideoCopy=true AudioCopy=false VideoTranscodingOff=true", plan)
		}

		// Unrestricted viewer admitting HEVC still transcodes normally.
		ownerPS := &core.PlaySession{
			ID: "ps-owner-hevc", UserID: unrestricted.ID, ClientID: h264OnlyCaps.ClientID, Key: core.MovieKey(12),
			Info: hevc, Resolution: &core.Resolution{Release: core.Release{ID: "rel-hevc"}},
		}
		pb.sessions[ownerPS.ID] = ownerPS
		ownerPlan, err := b.planPlexAdmission(ctx, frontend.PlanRequest{
			Viewer:   unrestricted,
			Device:   core.Device{ID: h264OnlyCaps.ClientID},
			Protocol: "plex",
			Item:     ownerPS.Key,
			Caps:     h264OnlyCaps,
			Intent:   frontend.PlayIntent{ClientSessionID: "plex-sess-owner", Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}},
		}, func(context.Context) (PlanAdmission, error) {
			return PlanAdmission{Source: ownerPS, Caps: h264OnlyCaps, Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}}, nil
		})
		if err != nil {
			t.Fatalf("planPlexAdmission(unrestricted, HEVC) err = %v, want transcode success", err)
		}
		if ownerPlan.Decision.VideoCopy || ownerPlan.VideoTranscodingOff {
			t.Fatalf("planPlexAdmission(unrestricted, HEVC) = %+v, want video transcode", ownerPlan)
		}
	})

	// 3. SwitchVersion enforces ViewerVideoTranscodingOff per viewer.
	t.Run("SwitchVersion enforces ViewerVideoTranscodingOff", func(t *testing.T) {
		key := core.MovieKey(20)
		h264Start := &core.PlaySession{
			ID: "sw-start", UserID: restricted.ID, ClientID: clientKey("vela", core.Device{ID: "dev-1"}), Key: key,
			Info: h264TrueHD, Resolution: &core.Resolution{Release: core.Release{ID: "rel-h264"}},
		}
		hevcTarget := &core.PlaySession{
			ID: "sw-hevc", UserID: restricted.ID, ClientID: clientKey("vela", core.Device{ID: "dev-1"}), Key: key,
			Info: hevc, Resolution: &core.Resolution{Release: core.Release{ID: "rel-hevc"}},
		}
		pb.sessions[h264Start.ID] = h264Start
		pb.sessions[hevcTarget.ID] = hevcTarget
		pb.onSwitch = func(release string, _ bool) (*core.PlaySession, error) {
			if release == "rel-hevc" {
				return hevcTarget, nil
			}
			return h264Start, nil
		}
		access := frontend.SessionAccess{Viewer: restricted, Device: core.Device{ID: "dev-1"}, Protocol: "vela"}
		handles, err := NewVersionHandles(db, b, "vela", testVersionKey)
		if err != nil {
			t.Fatal(err)
		}
		switchCaps := h264OnlyCaps
		switchCaps.ClientID = clientKey("vela", access.Device)
		scope := frontend.VersionScope{Viewer: restricted, Device: access.Device, Protocol: "vela", Caps: switchCaps}
		issued, err := handles.(frontend.VersionHandleBatch).IssueBatch(ctx, scope, key, []string{"rel-h264", "rel-hevc"})
		if err != nil {
			t.Fatal(err)
		}
		hevcHandle, h264Handle := issued["rel-hevc"], issued["rel-h264"]
		b.mu.Lock()
		b.sessions["sess-sw"] = &brokerSession{
			public:   frontend.Session{ID: "sess-sw", Protocol: "vela", ViewerID: restricted.ID, Device: access.Device, Item: key},
			playID:   h264Start.ID,
			clientID: clientKey("vela", access.Device),
			caps:     switchCaps,
			opts:     core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1},
			decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: false, AudioStream: 1},
		}
		b.mu.Unlock()

		if _, err := b.SwitchVersion(ctx, access, "sess-sw", hevcHandle, 0); !errors.Is(err, core.ErrVideoTranscodingDisabled) {
			t.Fatalf("SwitchVersion(restricted, HEVC) err = %v, want ErrVideoTranscodingDisabled", err)
		}
		swPlan, err := b.SwitchVersion(ctx, access, "sess-sw", h264Handle, 0)
		if err != nil {
			t.Fatalf("SwitchVersion(restricted, H264) err = %v", err)
		}
		if !swPlan.VideoTranscodingOff || !swPlan.Decision.VideoCopy {
			t.Fatalf("SwitchVersion(restricted, H264) = %+v, want VideoTranscodingOff=true VideoCopy=true", swPlan)
		}
	})

	// 4. Live TV ignores per-viewer VideoTranscodingOff (just like the server-wide switch).
	t.Run("Live TV unaffected by ViewerVideoTranscodingOff", func(t *testing.T) {
		b.deps.Live = liveNoOpBackend{}
		livePlan, err := b.Plan(ctx, frontend.PlanRequest{
			Viewer:   restricted,
			Device:   core.Device{ID: "dev-live"},
			Protocol: "vela",
			Channel:  "ch-7",
			Caps:     core.ClientCaps{ClientID: "dev-live", VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, NoVideoTranscode: true},
			Intent:   frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}},
		})
		if err != nil {
			t.Fatalf("Live TV Plan for restricted viewer failed: %v", err)
		}
		if livePlan.VideoTranscodingOff {
			t.Fatalf("Live TV plan VideoTranscodingOff = true, want false: %+v", livePlan)
		}
		b.mu.Lock()
		storedPlan, ok := b.plans[livePlan.ID]
		b.mu.Unlock()
		if !ok || storedPlan.request.Caps.NoVideoTranscode {
			t.Fatalf("stored live plan ok=%v NoVideoTranscode=%v, want ok=true NoVideoTranscode=false", ok, storedPlan.request.Caps.NoVideoTranscode)
		}
	})
}
