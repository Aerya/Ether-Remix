package sessions

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func intPtr(n int) *int { return &n }

func TestSourceTrackSelectionUsesMixedNoncontiguousIndices(t *testing.T) {
	info := core.MediaInfo{Streams: []core.StreamInfo{
		{Type: "video", Index: 0}, {Type: "audio", Index: 2},
		{Type: "subtitle", Index: 7}, {Type: "audio", Index: 11},
		{Type: "subtitle", Index: 19},
	}}
	intent := frontend.PlayIntent{AudioOrdinal: intPtr(1), SubtitleOrdinal: intPtr(1)}
	if err := bindSourceTracks(&intent, info); err != nil || !intent.AudioSet || !intent.SubtitleSet || intent.Options.AudioStream != 11 || intent.Options.SubtitleStream != 19 {
		t.Fatalf("selection %+v: %v", intent, err)
	}
	off := frontend.PlayIntent{SubtitleOrdinal: intPtr(-1)}
	if err := bindSourceTracks(&off, info); err != nil || !off.SubtitleSet || off.Options.SubtitleStream != -1 || off.AudioSet {
		t.Fatalf("subtitle off %+v: %v", off, err)
	}
	unchanged := frontend.PlayIntent{Options: core.DecideOptions{AudioStream: 2, SubtitleStream: 7}}
	if err := bindSourceTracks(&unchanged, info); err != nil || unchanged.AudioSet || unchanged.SubtitleSet || unchanged.Options.AudioStream != 2 || unchanged.Options.SubtitleStream != 7 {
		t.Fatalf("omitted preferences %+v: %v", unchanged, err)
	}
	for _, bad := range []frontend.PlayIntent{
		{AudioOrdinal: intPtr(-1)}, {SubtitleOrdinal: intPtr(-2)},
		{AudioSet: true, AudioOrdinal: intPtr(0)},
		{SubtitleSet: true, SubtitleOrdinal: intPtr(-1)},
	} {
		if err := bindSourceTracks(&bad, info); !errors.Is(err, frontend.ErrTrackNotFound) {
			t.Fatalf("malformed selection %+v: %v", bad, err)
		}
	}
	// Positions the source cannot honour are no choice, never an error.
	duplicate := info
	duplicate.Streams = append(append([]core.StreamInfo(nil), info.Streams...), core.StreamInfo{Type: "subtitle", Index: 11})
	sidecar := info
	sidecar.Streams = append([]core.StreamInfo(nil), info.Streams...)
	sidecar.Streams[2].ExternalURL = "https://example.invalid/subtitle"
	external := info
	external.Streams = append([]core.StreamInfo(nil), info.Streams...)
	external.Streams[2].External = true
	estimated := info
	estimated.Estimated = true
	for name, tc := range map[string]struct {
		intent frontend.PlayIntent
		info   core.MediaInfo
	}{
		"audio past end":     {frontend.PlayIntent{AudioOrdinal: intPtr(2)}, info},
		"subtitle past end":  {frontend.PlayIntent{SubtitleOrdinal: intPtr(2)}, info},
		"ambiguous index":    {frontend.PlayIntent{AudioOrdinal: intPtr(1)}, duplicate},
		"unverified sidecar": {frontend.PlayIntent{SubtitleOrdinal: intPtr(0)}, sidecar},
		"external stream":    {frontend.PlayIntent{SubtitleOrdinal: intPtr(0)}, external},
		"estimated source":   {frontend.PlayIntent{AudioOrdinal: intPtr(0), SubtitleOrdinal: intPtr(0)}, estimated},
	} {
		intent := tc.intent
		intent.Options = core.DecideOptions{AudioStream: -1, SubtitleStream: -1}
		if err := bindSourceTracks(&intent, tc.info); err != nil || intent.AudioSet || intent.SubtitleSet || intent.AudioOrdinal != nil || intent.SubtitleOrdinal != nil {
			t.Fatalf("%s: %+v: %v", name, intent, err)
		}
	}
}

type sourceTrackPlayback struct {
	*fakePlayback
	info   core.MediaInfo
	source *revisionSource
	pins   int
}

func (p *sourceTrackPlayback) PrepareSurface(ctx context.Context, v *core.Viewer, key core.RatingKey, caps core.ClientCaps, release string, exact bool) (*core.PlaySession, error) {
	ps, err := p.fakePlayback.PrepareSurface(ctx, v, key, caps, release, exact)
	if err == nil {
		ps.Info = p.info
		ps.Source = p.source
	}
	return ps, err
}
func (p *sourceTrackPlayback) PinSourceRevision(_ context.Context, id, revision string) error {
	ps, ok := p.Session(id)
	if !ok || sourceRevision(ps) != revision {
		return core.ErrSourceChanged
	}
	p.pins++
	return nil
}
func (p *sourceTrackPlayback) UnpinSourceRevision(string) { p.pins-- }

func TestSourceTracksPinOpenedGenerationUntilStop(t *testing.T) {
	ctx := context.Background()
	pb := &sourceTrackPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 2}, source: &revisionSource{Reader: bytes.NewReader(nil), id: "release", name: "video.mkv", size: 100}, info: core.MediaInfo{Streams: []core.StreamInfo{{Type: "video", Index: 0}, {Type: "audio", Index: 2}, {Type: "subtitle", Index: 7}, {Type: "audio", Index: 11}, {Type: "subtitle", Index: 19}}}}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	bus := NewEventBus(nil, active)
	defer bus.Close()
	var selected core.DecideOptions
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy, Events: bus, Playback: pb, Transcoder: fakeBrokerTranscoder{}, Progress: &fakeProgress{}, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: key}, nil
	}, SelectScopedTracks: func(_ context.Context, _ *core.Viewer, _ core.Device, _ *core.Title, _ core.MediaInfo, opts core.DecideOptions, audioSet, subtitleSet bool) (core.DecideOptions, error) {
		if !audioSet || !subtitleSet {
			t.Error("explicit selections were lost")
		}
		selected = opts
		return opts, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	viewer, device := &core.Viewer{ID: 1}, core.Device{ID: "tv"}
	request := frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "silo", Item: core.MovieKey(1), Intent: frontend.PlayIntent{AudioOrdinal: intPtr(1), SubtitleOrdinal: intPtr(1), Options: core.DecideOptions{DirectPlay: true}}}
	plan, err := b.Plan(ctx, request)
	if err != nil || selected.AudioStream != 11 || selected.SubtitleStream != 19 || pb.pins != 1 {
		t.Fatalf("plan %+v options %+v pins %d error %v", plan, selected, pb.pins, err)
	}
	session, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "silo", PlanID: plan.ID})
	if err != nil || pb.pins != 1 {
		t.Fatalf("open %+v pins %d error %v", session, pb.pins, err)
	}
	if err := b.Stop(ctx, frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "silo"}, session.ID); err != nil || pb.pins != 0 {
		t.Fatalf("stop pins %d error %v", pb.pins, err)
	}
	plan, err = b.Plan(ctx, request)
	if err != nil || pb.pins != 1 {
		t.Fatalf("second plan pins %d error %v", pb.pins, err)
	}
	// A misbehaving backend that changes the pinned source must still not let
	// Open install a decision made for a different generation.
	pb.source.id = "different-release"
	if _, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "silo", PlanID: plan.ID}); !errors.Is(err, frontend.ErrSourceGone) || pb.pins != 0 {
		t.Fatalf("changed source opened: pins %d error %v", pb.pins, err)
	}
}

// A start never fails over a stale track id. Positions from another release,
// an older probe or a remembered choice fall back to the viewer's rules, take
// no source pin, and the plan opens.
func TestStaleTrackIDFallsBackAndStarts(t *testing.T) {
	ctx := context.Background()
	pb := &sourceTrackPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 2}, source: &revisionSource{Reader: bytes.NewReader(nil), id: "release", name: "video.mkv", size: 100}, info: core.MediaInfo{Streams: []core.StreamInfo{{Type: "video", Index: 0}, {Type: "audio", Index: 1}, {Type: "subtitle", Index: 2}}}}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	bus := NewEventBus(nil, active)
	defer bus.Close()
	var explicitAudio, explicitSubtitle bool
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy, Events: bus, Playback: pb, Transcoder: fakeBrokerTranscoder{}, Progress: &fakeProgress{}, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: key}, nil
	}, SelectScopedTracks: func(_ context.Context, _ *core.Viewer, _ core.Device, _ *core.Title, _ core.MediaInfo, opts core.DecideOptions, audioSet, subtitleSet bool) (core.DecideOptions, error) {
		explicitAudio, explicitSubtitle = audioSet, subtitleSet
		return opts, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	viewer, device := &core.Viewer{ID: 1}, core.Device{ID: "tv"}
	request := frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "silo", Item: core.MovieKey(1), Intent: frontend.PlayIntent{AudioOrdinal: intPtr(3), SubtitleOrdinal: intPtr(4), Options: core.DecideOptions{DirectPlay: true, AudioStream: -1, SubtitleStream: -1}}}
	plan, err := b.Plan(ctx, request)
	if err != nil {
		t.Fatalf("stale track ids failed the start: %v", err)
	}
	if explicitAudio || explicitSubtitle || pb.pins != 0 {
		t.Fatalf("stale ids stayed explicit: audio=%v subtitle=%v pins=%d", explicitAudio, explicitSubtitle, pb.pins)
	}
	session, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "silo", PlanID: plan.ID})
	if err != nil || session.ID == "" {
		t.Fatalf("open %+v: %v", session, err)
	}
}

type burnInTranscoder struct{ fakeBrokerTranscoder }

func (burnInTranscoder) Decide(_ core.MediaInfo, _ core.ClientCaps, opts core.DecideOptions) core.Decision {
	if opts.SubtitleStream >= 0 {
		return core.Decision{Kind: core.DecisionTranscode, AudioCopy: true, BurnSubtitles: true, AudioStream: opts.AudioStream, SubtitleStream: opts.SubtitleStream, Container: "mpegts"}
	}
	return core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, AudioStream: opts.AudioStream, SubtitleStream: -1, Container: "mkv"}
}

// With video transcoding off, an image subtitle the viewer's rules chose must
// not turn the start into a refused burn-in transcode; it plays without it.
// A subtitle the client named keeps the clear refusal.
func TestRuleChosenBurnInNeverFailsStart(t *testing.T) {
	ctx := context.Background()
	pb := &sourceTrackPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 2}, source: &revisionSource{Reader: bytes.NewReader(nil), id: "release", name: "video.mkv", size: 100}, info: core.MediaInfo{Container: "mkv", Streams: []core.StreamInfo{{Type: "video", Index: 0, Codec: "h264"}, {Type: "audio", Index: 1, Codec: "aac"}, {Type: "subtitle", Index: 2, Codec: "hdmv_pgs_subtitle"}}}}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	bus := NewEventBus(nil, active)
	defer bus.Close()
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy, Events: bus, Playback: pb, Transcoder: burnInTranscoder{}, Progress: &fakeProgress{}, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, VideoTranscodingOff: func() bool { return true }, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: key}, nil
	}, SelectScopedTracks: func(_ context.Context, _ *core.Viewer, _ core.Device, _ *core.Title, _ core.MediaInfo, opts core.DecideOptions, _, subtitleSet bool) (core.DecideOptions, error) {
		if !subtitleSet {
			opts.SubtitleStream = 2 // the viewer's "always English" rule
		}
		return opts, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	request := frontend.PlanRequest{Viewer: &core.Viewer{ID: 1}, Device: core.Device{ID: "tv"}, Protocol: "silo", Item: core.MovieKey(1), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}}}
	plan, err := b.Plan(ctx, request)
	if err != nil || plan.Decision.Kind != core.DecisionDirectPlay || plan.Decision.SubtitleStream != -1 {
		t.Fatalf("rule-chosen burn-in: decision %+v err %v", plan.Decision, err)
	}
	request.Device.ID = "named"
	request.Intent.SubtitleOrdinal = intPtr(0)
	if _, err := b.Plan(ctx, request); !errors.Is(err, core.ErrVideoTranscodingDisabled) {
		t.Fatalf("named image subtitle with transcoding off: %v", err)
	}
}
