package sessions

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type kindPinRemuxPlayback struct {
	*sourceTrackPlayback
	requiresRemux map[string]bool
}

func (p *kindPinRemuxPlayback) PrepareKind(_ context.Context, u *core.User, in core.KindInput, c core.ClientCaps) (*core.PlaySession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.sessions {
		if s.UserID == u.ID && s.ClientID == c.ClientID && s.Key == in.Title.Key {
			cp := *s
			cp.RequiresRemux = p.requiresRemux[s.ID]
			return &cp, nil
		}
	}
	id := "kind-pin-1"
	ps := &core.PlaySession{
		ID:            id,
		UserID:        u.ID,
		ClientID:      c.ClientID,
		Key:           in.Title.Key,
		Info:          p.info,
		Source:        p.source,
		RequiresRemux: p.requiresRemux[id],
		Resolution: &core.Resolution{
			HealthVerified: true,
			Release:        core.Release{ID: "usenet-kind-1", Name: in.Title.Title, Protocol: core.ProtocolUsenet, Size: p.info.Size},
		},
	}
	p.sessions[id] = ps
	cp := *ps
	return &cp, nil
}

func (p *kindPinRemuxPlayback) SessionForKind(user int64, client string, key core.RatingKey) (*core.PlaySession, bool) {
	return p.SessionFor(user, client, key)
}

func (p *kindPinRemuxPlayback) RequiresRemux(id string) bool {
	return p.requiresRemux[id]
}

func TestVelaPinPolicySeparatesOriginalRuntimeAndExplicitPicksFromAutoPlay(t *testing.T) {
	ctx := context.Background()
	info := core.MediaInfo{
		Container: "mp4",
		Duration:  120 * time.Second,
		Size:      50 << 20,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
			{Index: 2, Type: "subtitle", Codec: "subrip"},
		},
	}
	viewer := &core.Viewer{ID: 1, IsOwner: true}
	device := core.Device{ID: "vela-web"}
	pluginKey := core.PluginKey(42)
	movieKey := core.MovieKey(99)

	newHarness := func() (*Broker, *kindPinRemuxPlayback) {
		active := func(_ context.Context, id int64) (*core.Viewer, error) {
			return &core.Viewer{ID: id, IsOwner: true}, nil
		}
		bus := NewEventBus(nil, active)
		t.Cleanup(bus.Close)
		pb := &kindPinRemuxPlayback{
			sourceTrackPlayback: &sourceTrackPlayback{
				fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4},
				info:         info,
				source:       &revisionSource{Reader: bytes.NewReader(nil), id: "usenet-kind-1", name: "movie.mp4", size: info.Size},
			},
			requiresRemux: map[string]bool{},
		}
		tc := &fakeMediaTranscoder{sessions: map[string]*fakeMediaSession{}}
		b, err := NewBroker(BrokerDeps{
			SurfacePolicy: testSessionPolicy,
			Events:        bus,
			Playback:      pb,
			Transcoder:    tc,
			Progress:      &fakeProgress{},
			ActiveViewer:  active,
			CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
			BitrateLimit:  func(*core.Viewer) int { return 0 },
			OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
			Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
				if key.IsPluginKey() {
					return nil, core.ErrNotFound
				}
				return &core.Title{Key: key, Type: core.TypeMovie, Title: "Catalogue Movie"}, nil
			},
			MediaLookup: func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
				if key.IsPluginKey() {
					return nil, core.ErrNotFound
				}
				return &core.Title{Key: key, Type: core.TypeMovie, Title: "Catalogue Movie"}, nil
			},
			KindSource: func(_ context.Context, _ *core.Viewer, k core.RatingKey) (*core.KindInput, error) {
				return &core.KindInput{
					Title:    &core.Title{Key: k, Type: core.TypeMovie, Title: "Kind Movie"},
					SourceID: "leaf-42",
					Open:     func(context.Context) (core.MediaSource, error) { return pb.source, nil },
				}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.Close(ctx) })
		return b, pb
	}

	allow := true
	velaBrowserCaps := core.ClientCaps{
		Product:     "Vela",
		ClientID:    "vela-browser-1",
		Protocol:    "vela",
		MaxHeight:   1080,
		Containers:  []string{"mp4"},
		VideoCodecs: []string{"h264"},
		AudioCodecs: []string{"aac"},
	}
	baseReq := func(item core.RatingKey) frontend.PlanRequest {
		return frontend.PlanRequest{
			Viewer:   viewer,
			Device:   device,
			Protocol: "vela",
			Item:     item,
			Caps:     velaBrowserCaps,
			Intent: frontend.PlayIntent{
				AllowTranscoding: &allow,
				Options:          core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1},
			},
		}
	}

	// 1. Automatic Vela <video> direct play on BOTH plugin kind items and
	// catalogue items must NOT pin the source revision, allowing mid-stream failover.
	for _, tc := range []struct {
		name string
		item core.RatingKey
	}{
		{"plugin kind auto play", pluginKey},
		{"catalogue auto play", movieKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, pb := newHarness()
			plan, err := b.Plan(ctx, baseReq(tc.item))
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if plan.Decision.Kind != core.DecisionDirectPlay {
				t.Fatalf("Decision = %+v, want directplay", plan.Decision)
			}
			if pb.pins != 0 {
				t.Fatalf("pb.pins = %d, want 0 so automatic release remains replaceable", pb.pins)
			}
		})
	}

	// 2. Explicit track selection (AudioOrdinal) pins the source revision.
	t.Run("explicit track selection pins revision", func(t *testing.T) {
		b, pb := newHarness()
		req := baseReq(pluginKey)
		req.Intent.AudioOrdinal = intPtr(0)
		plan, err := b.Plan(ctx, req)
		if err != nil {
			t.Fatalf("Plan with AudioOrdinal: %v", err)
		}
		if pb.pins != 1 || plan.ETag == "" {
			t.Fatalf("pins = %d etag = %q, want 1 and non-empty ETag", pb.pins, plan.ETag)
		}
	})

	// 3. Admitted original_runtime pins the source revision, UNLESS a prior
	// non-spliceable direct-play failover flagged RequiresRemux, in which case
	// re-planning switches to remux/HLS without re-admitting original_runtime.
	t.Run("original_runtime pins until RequiresRemux forces remux", func(t *testing.T) {
		b, pb := newHarness()
		runtime := &frontend.OriginalPlaybackCapabilities{
			Version:         1,
			Range:           true,
			DemuxContainers: []string{"mp4", "mkv"},
			VideoDecoders:   []string{"h264", "hevc"},
			AudioDecoders:   []string{"aac"},
			WebGPU:          true,
		}
		req := baseReq(pluginKey)
		req.Intent.OriginalPlayback = runtime
		plan, err := b.Plan(ctx, req)
		if err != nil {
			t.Fatalf("Plan original_runtime: %v", err)
		}
		if plan.Decision.Reason != "original_runtime" || pb.pins != 1 {
			t.Fatalf("original_runtime plan = %+v, pins = %d, want original_runtime and 1 pin", plan.Decision, pb.pins)
		}

		// Simulate mid-stream failover on a second unpinned session setting RequiresRemux.
		b2, pb2 := newHarness()
		pb2.requiresRemux["kind-pin-1"] = true
		reqRemux := baseReq(pluginKey)
		reqRemux.Intent.OriginalPlayback = runtime
		remuxPlan, err := b2.Plan(ctx, reqRemux)
		if err != nil {
			t.Fatalf("Plan after RequiresRemux: %v", err)
		}
		if remuxPlan.Decision.Kind == core.DecisionDirectPlay || remuxPlan.Decision.Reason == "original_runtime" {
			t.Fatalf("RequiresRemux session re-admitted direct play: %+v", remuxPlan.Decision)
		}
		if pb2.pins != 0 {
			t.Fatalf("remux re-plan pinned source revision: pins = %d, want 0", pb2.pins)
		}
	})
}
