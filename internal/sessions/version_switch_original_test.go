package sessions

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type switchOriginalTranscoder struct{ *fakeMediaTranscoder }

func (switchOriginalTranscoder) Decide(i core.MediaInfo, c core.ClientCaps, o core.DecideOptions) core.Decision {
	return transcode.Decide(i, c, o, transcode.Policy{})
}

// switchOriginalSources serves one MKV per release; slot:<x> resolves to
// edition-<x>, and an empty release is edition-a.
type switchOriginalSources struct {
	*fakePlayback
	pinMu      sync.Mutex
	pins       map[string]int
	streams    map[string][]core.StreamInfo
	failOpened string
	// changeAfterPin replaces that source's file right after it is pinned.
	changeAfterPin string
}

func switchOriginalRelease(release string) string {
	if release == "" {
		return "edition-a"
	}
	if len(release) > 5 && release[:5] == "slot:" {
		return "edition-" + release[5:]
	}
	return release
}

func (s *switchOriginalSources) PrepareSurface(_ context.Context, u *core.User, k core.RatingKey, c core.ClientCaps, selector string, _ bool) (*core.PlaySession, error) {
	release := switchOriginalRelease(selector)
	if current, ok := s.Session(release); ok {
		return current, nil
	}
	streams := s.streams[release]
	if streams == nil {
		streams = []core.StreamInfo{{Type: "video", Index: 0, Codec: "h264", Width: 1920, Height: 1080}, {Type: "audio", Index: 1, Codec: "truehd", Channels: 6}}
	}
	p := &core.PlaySession{ID: release, Key: k, UserID: u.ID, ClientID: c.ClientID, Client: c,
		Source:     &revisionSource{Reader: bytes.NewReader(nil), id: release, name: release + ".mkv", size: 1024},
		Resolution: &core.Resolution{Release: core.Release{ID: release}, VersionSelector: selector},
		Info:       core.MediaInfo{Container: "mkv", Duration: time.Hour, Size: 1024, Streams: streams},
	}
	s.mu.Lock()
	s.sessions[p.ID] = p
	s.mu.Unlock()
	return p, nil
}

func (s *switchOriginalSources) PrepareWithin(ctx context.Context, _ time.Duration, u *core.User, k core.RatingKey, c core.ClientCaps, release string, exact bool) (*core.PlaySession, error) {
	return s.PrepareSurface(ctx, u, k, c, release, exact)
}

func (s *switchOriginalSources) SessionForSurface(_ string, _ int64, _ string, _ core.RatingKey, release string, _ bool) (*core.PlaySession, bool) {
	return s.Session(switchOriginalRelease(release))
}

func (s *switchOriginalSources) MarkSurfaceOpened(id string) bool {
	if id == s.failOpened {
		return false
	}
	return s.fakePlayback.MarkSurfaceOpened(id)
}

func (s *switchOriginalSources) PinSourceRevision(_ context.Context, id, revision string) error {
	p, ok := s.Session(id)
	if !ok || sourceRevision(p) != revision {
		return core.ErrSourceChanged
	}
	s.pinMu.Lock()
	s.pins[id]++
	s.pinMu.Unlock()
	if id == s.changeAfterPin {
		s.replaceFile(id, id+".mkv", 2048)
	}
	return nil
}

func (s *switchOriginalSources) UnpinSourceRevision(id string) {
	s.pinMu.Lock()
	if s.pins[id]--; s.pins[id] <= 0 {
		delete(s.pins, id)
	}
	s.pinMu.Unlock()
}

func (s *switchOriginalSources) pinned(id string) bool { return s.pinCount(id) > 0 }

func (s *switchOriginalSources) pinCount(id string) int {
	s.pinMu.Lock()
	defer s.pinMu.Unlock()
	return s.pins[id]
}

// replaceFile swaps the file behind a source for another one; an empty name
// leaves it without a revision.
func (s *switchOriginalSources) replaceFile(id, name string, size int64) {
	s.mu.Lock()
	if p := s.sessions[id]; p != nil {
		changed := *p
		changed.Source = &revisionSource{Reader: bytes.NewReader(nil), id: id, name: name, size: size}
		s.sessions[id] = &changed
	}
	s.mu.Unlock()
}

type switchOriginalFixture struct {
	b       *Broker
	src     *switchOriginalSources
	req     frontend.PlanRequest
	access  frontend.SessionAccess
	handles map[string]string
}

func newSwitchOriginalFixture(t *testing.T, deps func(*BrokerDeps), streams map[string][]core.StreamInfo) *switchOriginalFixture {
	t.Helper()
	db := store.OpenTest(t)
	src := &switchOriginalSources{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 8}, pins: map[string]int{}, streams: streams}
	tx := switchOriginalTranscoder{&fakeMediaTranscoder{sessions: map[string]*fakeMediaSession{}}}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	lookup := func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k, Type: core.TypeMovie, Title: "Fixture"}, nil
	}
	bus := NewEventBus(nil, active)
	t.Cleanup(bus.Close)
	slots := []string{"slot:a", "slot:b", "slot:c", "slot:dts"}
	d := BrokerDeps{DB: db, SurfacePolicy: testSessionPolicy, Playback: src, Transcoder: tx, Events: bus, Progress: &fakeProgress{}, Lookup: lookup, MediaLookup: lookup, ActiveViewer: active,
		CanPlay:      func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit: func(*core.Viewer) int { return 0 },
		OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Variants: func(context.Context, *core.Viewer, *core.Title, core.ClientCaps) ([]core.ReleaseVariant, error) {
			out := make([]core.ReleaseVariant, 0, len(slots))
			for _, slot := range slots {
				out = append(out, core.ReleaseVariant{ID: slot, Release: core.Release{ID: switchOriginalRelease(slot)}, Availability: core.VersionCached})
			}
			return out, nil
		},
	}
	if deps != nil {
		deps(&d)
	}
	b, err := NewBroker(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(context.Background()) })
	v := &core.Viewer{ID: 1}
	dev := core.Device{ID: "apple-fixture"}
	req := frontend.PlanRequest{Protocol: "vela", Viewer: v, Device: dev, Item: core.MovieKey(11),
		Caps: core.ClientCaps{Protocol: "vela", ClientID: "vela:apple-fixture", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, HLSContainers: []string{"mpegts"}},
		Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}, DeclaredCaps: true,
			OriginalPlayback: &frontend.OriginalPlaybackCapabilities{Version: 1, Range: true, DemuxContainers: []string{"mkv"}, VideoDecoders: []string{"h264"}, AudioDecoders: []string{"truehd"}}}}
	h, err := NewVersionHandles(db, b, "vela", testVersionKey)
	if err != nil {
		t.Fatal(err)
	}
	handles, err := h.(frontend.VersionHandleBatch).IssueBatch(context.Background(), frontend.VersionScope{Protocol: req.Protocol, Viewer: v, Device: dev, Caps: req.Caps}, req.Item, slots)
	if err != nil {
		t.Fatal(err)
	}
	return &switchOriginalFixture{b: b, src: src, req: req, access: frontend.SessionAccess{Viewer: v, Device: dev, Protocol: "vela"}, handles: handles}
}

func (f *switchOriginalFixture) open(t *testing.T, r frontend.PlanRequest) (frontend.PlayPlan, frontend.Session) {
	t.Helper()
	p, err := f.b.Plan(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := f.b.Open(context.Background(), frontend.OpenRequest{Viewer: r.Viewer, Device: r.Device, Protocol: r.Protocol, PlanID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	return p, s
}

// freshPlan is what a new start of the version would get before Play.
func (f *switchOriginalFixture) freshPlan(t *testing.T, slot string) frontend.PlayPlan {
	t.Helper()
	r := f.req
	r.Version = f.handles[slot]
	r.Intent.ExactVersion = true
	p, err := f.b.Plan(context.Background(), r)
	if err != nil {
		t.Fatalf("fresh %s: %v", slot, err)
	}
	f.b.dropPlan(context.Background(), p.ID, true)
	return p
}

func TestSwitchVersionKeepsOriginalPlaybackDeclaration(t *testing.T) {
	ctx := context.Background()
	f := newSwitchOriginalFixture(t, nil, map[string][]core.StreamInfo{
		"edition-dts": {{Type: "video", Index: 0, Codec: "h264", Width: 1920, Height: 1080}, {Type: "audio", Index: 1, Codec: "dts", Channels: 6}},
	})
	plan, s := f.open(t, f.req)
	if plan.Decision.Reason != "original_runtime" || !f.src.pinned("edition-a") {
		t.Fatalf("start: %+v pins=%v", plan.Decision, f.src.pins)
	}
	for i, slot := range []string{"slot:b", "slot:c"} {
		fresh := f.freshPlan(t, slot)
		sw, err := f.b.SwitchVersion(ctx, f.access, s.ID, f.handles[slot], time.Duration(i+1)*time.Minute)
		if err != nil {
			t.Fatalf("switch %s: %v", slot, err)
		}
		release := switchOriginalRelease(slot)
		if fresh.Decision.Reason != "original_runtime" || sw.Decision.Kind != fresh.Decision.Kind || sw.Decision.Reason != fresh.Decision.Reason || sw.ReleaseID != release {
			t.Fatalf("switch %s = %s/%s release %s, fresh start = %s/%s", slot, sw.Decision.Kind, sw.Decision.Reason, sw.ReleaseID, fresh.Decision.Kind, fresh.Decision.Reason)
		}
		if !f.src.pinned(release) {
			t.Fatalf("switch %s did not pin the new revision: %v", slot, f.src.pins)
		}
	}
	if f.src.pinned("edition-a") || f.src.pinned("edition-b") {
		t.Fatalf("old revisions still pinned: %v", f.src.pins)
	}
	// A file the declared decoders cannot play still converts.
	sw, err := f.b.SwitchVersion(ctx, f.access, s.ID, f.handles["slot:dts"], 3*time.Minute)
	if err != nil {
		t.Fatalf("switch dts: %v", err)
	}
	if sw.Decision.Kind == core.DecisionDirectPlay || sw.Decision.Reason == "original_runtime" || sw.ReleaseID != "edition-dts" {
		t.Fatalf("undeclared codec admitted as original: %+v", sw.Decision)
	}
	if f.src.pinned("edition-c") {
		t.Fatalf("old revision still pinned: %v", f.src.pins)
	}
}

func TestSwitchVersionWithoutDeclarationUnchanged(t *testing.T) {
	f := newSwitchOriginalFixture(t, nil, nil)
	r := f.req
	r.Intent.OriginalPlayback = nil
	plan, s := f.open(t, r)
	sw, err := f.b.SwitchVersion(context.Background(), f.access, s.ID, f.handles["slot:b"], time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision.Kind == core.DecisionDirectPlay || sw.Decision.Kind == core.DecisionDirectPlay || sw.Decision.Kind != plan.Decision.Kind {
		t.Fatalf("no declaration: start %+v switch %+v", plan.Decision, sw.Decision)
	}
}

func TestSwitchVersionDeclarationIsCopied(t *testing.T) {
	f := newSwitchOriginalFixture(t, nil, nil)
	r := f.req
	declared := *r.Intent.OriginalPlayback
	declared.AudioDecoders = slices.Clone(declared.AudioDecoders)
	r.Intent.OriginalPlayback = &declared
	_, s := f.open(t, r)
	declared.AudioDecoders[0] = "opus"
	sw, err := f.b.SwitchVersion(context.Background(), f.access, s.ID, f.handles["slot:b"], time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if sw.Decision.Reason != "original_runtime" {
		t.Fatalf("caller's later change reached the stored declaration: %+v", sw.Decision)
	}
}

func TestSwitchVersionAfterTrackRestartKeepsOriginal(t *testing.T) {
	ctx := context.Background()
	video := core.StreamInfo{Type: "video", Index: 0, Codec: "h264", Width: 1920, Height: 1080}
	f := newSwitchOriginalFixture(t, nil, map[string][]core.StreamInfo{
		"edition-a": {video, {Type: "audio", Index: 1, Codec: "truehd", Language: "eng"}, {Type: "audio", Index: 2, Codec: "truehd", Language: "jpn"}},
		"edition-b": {video, {Type: "audio", Index: 1, Codec: "truehd", Language: "eng"}, {Type: "audio", Index: 3, Codec: "truehd", Language: "jpn"}},
	})
	_, s := f.open(t, f.req)
	audio, off := 2, -1
	if _, err := f.b.StartStream(ctx, f.access, s.ID, frontend.StreamRequest{Format: frontend.StreamHLS, AudioStream: &audio, SubtitleStream: &off, LazyStart: true}); err != nil {
		t.Fatal(err)
	}
	f.b.mu.Lock()
	restarted := f.b.sessions[s.ID].opts.DirectPlay
	f.b.mu.Unlock()
	if restarted {
		t.Fatal("track restart kept direct play; the case is not exercised")
	}
	fresh := f.freshPlan(t, "slot:b")
	sw, err := f.b.SwitchVersion(ctx, f.access, s.ID, f.handles["slot:b"], time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Decision.Reason != "original_runtime" || sw.Decision.Reason != "original_runtime" || sw.Decision.Kind != core.DecisionDirectPlay {
		t.Fatalf("after a track restart: switch %s/%s, fresh %s/%s", sw.Decision.Kind, sw.Decision.Reason, fresh.Decision.Kind, fresh.Decision.Reason)
	}
	f.b.mu.Lock()
	live := *f.b.sessions[s.ID]
	f.b.mu.Unlock()
	if sw.Decision.AudioStream != 3 || sw.SubtitleStream() != -1 || !live.manualSubtitle || live.streamPosition != time.Minute {
		t.Fatalf("switch lost selected tracks or position: decision %+v, position %v", sw.Decision, live.streamPosition)
	}
}

func TestSwitchVersionRuleSubtitleFallsBackLikeStart(t *testing.T) {
	streams := []core.StreamInfo{{Type: "video", Index: 0, Codec: "h264", Width: 1920, Height: 1080}, {Type: "audio", Index: 1, Codec: "aac", Channels: 2}, {Type: "subtitle", Index: 2, Codec: "hdmv_pgs_subtitle", Language: "eng"}}
	f := newSwitchOriginalFixture(t, func(d *BrokerDeps) {
		d.VideoTranscodingOff = func() bool { return true }
		d.SelectTracks = func(_ context.Context, _ *core.Viewer, _ *core.Title, info core.MediaInfo, o core.DecideOptions, _, subtitleSet bool) (core.DecideOptions, error) {
			if !subtitleSet {
				for _, st := range info.Streams {
					if st.Type == "subtitle" {
						o.SubtitleStream = st.Index
					}
				}
			}
			return o, nil
		}
	}, map[string][]core.StreamInfo{"edition-a": streams, "edition-b": streams})
	f.req.Intent.OriginalPlayback = nil
	plan, s := f.open(t, f.req)
	fresh := f.freshPlan(t, "slot:b")
	sw, err := f.b.SwitchVersion(context.Background(), f.access, s.ID, f.handles["slot:b"], time.Minute)
	if err != nil {
		t.Fatalf("switch refused where a fresh start plays (start %s, fresh %s): %v", plan.Decision.Kind, fresh.Decision.Kind, err)
	}
	if sw.Decision.Kind != fresh.Decision.Kind || sw.Decision.BurnSubtitles || decisionEncodesVideo(sw.Decision, core.MediaInfo{Streams: streams}) {
		t.Fatalf("switch %+v, fresh %+v", sw.Decision, fresh.Decision)
	}
	f.b.mu.Lock()
	subtitle := f.b.sessions[s.ID].opts.SubtitleStream
	f.b.mu.Unlock()
	if sw.SelectedSubtitle != nil || sw.Decision.SubtitleStream >= 0 || subtitle >= 0 {
		t.Fatalf("rule subtitle still selected after the switch: plan %v decision %d session %d", sw.SelectedSubtitle, sw.Decision.SubtitleStream, subtitle)
	}
}

func TestSwitchVersionFailureReleasesNewPin(t *testing.T) {
	f := newSwitchOriginalFixture(t, nil, nil)
	_, s := f.open(t, f.req)
	f.src.failOpened = "edition-b"
	if _, err := f.b.SwitchVersion(context.Background(), f.access, s.ID, f.handles["slot:b"], time.Minute); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("err = %v", err)
	}
	f.b.mu.Lock()
	playID := f.b.sessions[s.ID].playID
	f.b.mu.Unlock()
	if playID != "edition-a" || !f.src.pinned("edition-a") || f.src.pinned("edition-b") {
		t.Fatalf("failed switch left playID=%s pins=%v", playID, f.src.pins)
	}
}

func TestSwitchVersionKeepsClientLimits(t *testing.T) {
	for _, liftAtStart := range []bool{false, true} {
		var videoOff atomic.Bool
		videoOff.Store(liftAtStart)
		f := newSwitchOriginalFixture(t, func(d *BrokerDeps) {
			d.VideoTranscodingOff = videoOff.Load
		}, nil)
		f.req.Intent.Options.MaxHeight = 720
		if liftAtStart {
			f.req.Intent.Options.MaxBitrateKbps = 4000
		}
		plan, s := f.open(t, f.req)
		if original := plan.Decision.Reason == "original_runtime"; original != liftAtStart {
			t.Fatalf("lift=%v: start %+v", liftAtStart, plan.Decision)
		}
		videoOff.Store(false)
		fresh := f.freshPlan(t, "slot:b")
		sw, err := f.b.SwitchVersion(context.Background(), f.access, s.ID, f.handles["slot:b"], time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if sw.Decision.Kind == core.DecisionDirectPlay || sw.Decision.Kind != fresh.Decision.Kind || sw.Decision.MaxHeight != fresh.Decision.MaxHeight {
			t.Fatalf("lift=%v: switch %+v, fresh %+v", liftAtStart, sw.Decision, fresh.Decision)
		}
	}
}

func TestSwitchVersionSameSourcePinsOriginal(t *testing.T) {
	streams := []core.StreamInfo{{Type: "video", Index: 0, Codec: "h264", Width: 1920, Height: 1080}, {Type: "audio", Index: 1, Codec: "truehd", Channels: 6}, {Type: "audio", Index: 2, Codec: "dts", Channels: 6}}
	f := newSwitchOriginalFixture(t, nil, map[string][]core.StreamInfo{"edition-a": streams})
	r := f.req
	r.Version = f.handles["slot:a"]
	r.Intent.ExactVersion = true
	r.Intent.Options.AudioStream = 2
	plan, s := f.open(t, r)
	if plan.Decision.Reason == "original_runtime" || f.src.pinned("edition-a") {
		t.Fatalf("start on the DTS track: %+v pins=%v", plan.Decision, f.src.pins)
	}
	// Select the supported track over HLS, then re-admit this source without
	// losing the track or the original runtime declaration.
	audio := 1
	if _, err := f.b.StartStream(context.Background(), f.access, s.ID, frontend.StreamRequest{
		Format: frontend.StreamHLS, AudioStream: &audio, LazyStart: true,
	}); err != nil {
		t.Fatal(err)
	}
	sw, err := f.b.SwitchVersion(context.Background(), f.access, s.ID, f.handles["slot:a"], time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.b.mu.Lock()
	live := *f.b.sessions[s.ID]
	f.b.mu.Unlock()
	if sw.Decision.Reason != "original_runtime" || sw.Decision.AudioStream != 1 || live.playID != "edition-a" {
		t.Fatalf("same-version switch = %+v on %s", sw.Decision, live.playID)
	}
	if !f.src.pinned("edition-a") || !live.sourcePinned || live.sourceETag == "" {
		t.Fatalf("original playback without a pin: pins=%v sourcePinned=%v etag=%q", f.src.pins, live.sourcePinned, live.sourceETag)
	}
	// Reselecting again reuses the pin the session holds.
	if _, err := f.b.SwitchVersion(context.Background(), f.access, s.ID, f.handles["slot:a"], 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	f.b.mu.Lock()
	still := f.b.sessions[s.ID].sourcePinned
	f.b.mu.Unlock()
	if !still || f.src.pinCount("edition-a") != 1 {
		t.Fatalf("second reselect: pins=%v sourcePinned=%v, want one pin", f.src.pins, still)
	}
	if err := f.b.Stop(context.Background(), f.access, s.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.src.pinCount("edition-a"); n != 0 {
		t.Fatalf("pin references after stop = %d", n)
	}
}

func TestSwitchVersionSameSourceChangedRevisionRefused(t *testing.T) {
	for _, name := range []string{"edition-a.mkv", ""} {
		f := newSwitchOriginalFixture(t, nil, nil)
		r := f.req
		r.Version = f.handles["slot:a"]
		r.Intent.ExactVersion = true
		_, s := f.open(t, r)
		f.b.mu.Lock()
		before := *f.b.sessions[s.ID]
		f.b.mu.Unlock()
		if !before.sourcePinned || before.sourceETag == "" {
			t.Fatalf("start did not pin: %+v", before.decision)
		}
		f.src.replaceFile("edition-a", name, 2048)
		if _, err := f.b.SwitchVersion(context.Background(), f.access, s.ID, f.handles["slot:a"], time.Minute); !errors.Is(err, frontend.ErrSourceGone) {
			t.Fatalf("name %q: reselect on a replaced file: err = %v", name, err)
		}
		f.b.mu.Lock()
		after := *f.b.sessions[s.ID]
		f.b.mu.Unlock()
		if after.playID != "edition-a" || !after.sourcePinned || after.sourceETag != before.sourceETag || f.src.pinCount("edition-a") != 1 {
			t.Fatalf("name %q: refused reselect changed the session: playID=%s sourcePinned=%v etag changed=%v pins=%v", name, after.playID, after.sourcePinned, after.sourceETag != before.sourceETag, f.src.pins)
		}
		if _, err := f.b.StartStream(context.Background(), f.access, s.ID, frontend.StreamRequest{Format: frontend.StreamDirect, LazyStart: true}); !errors.Is(err, frontend.ErrSourceGone) {
			t.Fatalf("name %q: stream start after the refused reselect: err = %v", name, err)
		}
	}
}

func TestSwitchVersionRevisionChangedAfterPinRollsBack(t *testing.T) {
	f := newSwitchOriginalFixture(t, nil, nil)
	_, s := f.open(t, f.req)
	f.src.changeAfterPin = "edition-b"
	if _, err := f.b.SwitchVersion(context.Background(), f.access, s.ID, f.handles["slot:b"], time.Minute); !errors.Is(err, frontend.ErrSourceGone) {
		t.Fatalf("err = %v", err)
	}
	f.b.mu.Lock()
	playID := f.b.sessions[s.ID].playID
	f.b.mu.Unlock()
	if playID != "edition-a" || f.src.pinCount("edition-a") != 1 || f.src.pinCount("edition-b") != 0 {
		t.Fatalf("failed switch left playID=%s pins=%v", playID, f.src.pins)
	}
}
