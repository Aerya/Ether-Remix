package sessions

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// A queued next item (gapless audio) opens beside the playing session: the
// playing one keeps streaming and reporting until the queued one is observed
// playing. Preloading the queued item's media does not end the playing one.
func TestQueuedOpenKeepsThePlayingSessionUntilItPlays(t *testing.T) {
	ctx := context.Background()
	p := &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}
	progress := &recordingProgress{}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	bus := NewEventBus(nil, active)
	defer bus.Close()
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k}, nil
	}, Playback: p, Transcoder: fakeBrokerTranscoder{}, Progress: progress, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Events: bus})
	if err != nil {
		t.Fatal(err)
	}
	v := &core.Viewer{ID: 2}
	d := core.Device{ID: "web"}
	a := frontend.SessionAccess{Viewer: v, Device: d, Protocol: "vela"}
	open := func(item int64, queued bool) frontend.Session {
		t.Helper()
		pl, err := b.Plan(ctx, frontend.PlanRequest{Viewer: v, Device: d, Protocol: "vela", Item: core.MovieKey(item), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, AudioStream: -1, SubtitleStream: -1}, Queued: queued}})
		if err != nil {
			t.Fatal(err)
		}
		s, err := b.Open(ctx, frontend.OpenRequest{Viewer: v, Device: d, Protocol: "vela", PlanID: pl.ID})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	live := func() []string {
		t.Helper()
		rows, err := b.Sessions(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		slices.Sort(ids)
		return ids
	}
	sorted := func(ids ...string) []string { slices.Sort(ids); return ids }

	playing := open(1, false)
	if _, err := b.StartStream(ctx, a, playing.ID, frontend.StreamRequest{Format: frontend.StreamDirect}); err != nil {
		t.Fatal(err)
	}
	next := open(2, true)
	if _, err := b.StartStream(ctx, a, next.ID, frontend.StreamRequest{Format: frontend.StreamDirect}); err != nil { // the client preloads it
		t.Fatal(err)
	}
	if got := live(); !slices.Equal(got, sorted(playing.ID, next.ID)) {
		t.Fatalf("queued open ended the playing session: %v", got)
	}
	if err := b.Report(ctx, a, playing.ID, frontend.Timeline{Position: 3 * time.Minute, State: "playing"}); err != nil {
		t.Fatalf("the playing session stopped taking reports: %v", err)
	}
	// A newer queued item replaces the stale queued one, never the playing one.
	after := open(3, true)
	if got := live(); !slices.Equal(got, sorted(playing.ID, after.ID)) {
		t.Fatalf("second queued open: %v", got)
	}
	// Observed playing, the queued item takes over the device.
	if err := b.Report(ctx, a, after.ID, frontend.Timeline{Position: time.Second, State: "playing"}); err != nil {
		t.Fatal(err)
	}
	// The item it followed plays out beside it (a crossfade): it stays for the
	// handover, recorded as played, and its reports write nothing more.
	if got := live(); !slices.Equal(got, sorted(playing.ID, after.ID)) {
		t.Fatalf("activation left %v", got)
	}
	if !slices.Contains(progress.completed, core.MovieKey(1)) {
		t.Fatalf("the handed-over item was not recorded as played: %+v", progress.completed)
	}
	calls := progress.calls
	if err := b.Report(ctx, a, playing.ID, frontend.Timeline{Position: 4 * time.Minute, State: "playing"}); err != nil {
		t.Fatalf("a playing-out report failed: %v", err)
	}
	if got := live(); !slices.Contains(got, playing.ID) || progress.calls != calls {
		t.Fatalf("a playing-out report ended it or wrote progress: %v calls=%d", got, progress.calls)
	}
	// Its stop ends it; the successor keeps the device.
	if err := b.Report(ctx, a, playing.ID, frontend.Timeline{Position: 4 * time.Minute, State: "stopped"}); err != nil {
		t.Fatal(err)
	}
	if got := live(); !slices.Equal(got, []string{after.ID}) {
		t.Fatalf("after the playing-out stop: %v", got)
	}
	// An ordinary open still supersedes a queued item that never played.
	stale := open(4, true)
	fresh := open(5, false)
	if got := live(); !slices.Equal(got, []string{fresh.ID}) || slices.Contains(got, stale.ID) {
		t.Fatalf("ordinary open left %v", got)
	}
}

// Without a stop, a handed-over item is retired when its handover ends.
func TestOutgoingRetiresWhenTheHandoverEnds(t *testing.T) {
	var fire func()
	saved := afterHandover
	afterHandover = func(d time.Duration, fn func()) {
		if d != OutgoingHandover {
			t.Errorf("handover = %v", d)
		}
		fire = fn
	}
	defer func() { afterHandover = saved }()
	ctx := context.Background()
	p := &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	bus := NewEventBus(nil, active)
	defer bus.Close()
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k}, nil
	}, Playback: p, Transcoder: fakeBrokerTranscoder{}, Progress: &fakeProgress{}, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Events: bus})
	if err != nil {
		t.Fatal(err)
	}
	v := &core.Viewer{ID: 3}
	d := core.Device{ID: "web"}
	a := frontend.SessionAccess{Viewer: v, Device: d, Protocol: "vela"}
	open := func(item int64, queued bool) frontend.Session {
		pl, err := b.Plan(ctx, frontend.PlanRequest{Viewer: v, Device: d, Protocol: "vela", Item: core.MovieKey(item), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, AudioStream: -1, SubtitleStream: -1}, Queued: queued}})
		if err != nil {
			t.Fatal(err)
		}
		s, err := b.Open(ctx, frontend.OpenRequest{Viewer: v, Device: d, Protocol: "vela", PlanID: pl.ID})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	playing := open(1, false)
	next := open(2, true)
	if err := b.Report(ctx, a, next.ID, frontend.Timeline{Position: time.Second, State: "playing"}); err != nil {
		t.Fatal(err)
	}
	gone := func() bool {
		rows, _ := b.Sessions(ctx, a)
		for _, r := range rows {
			if r.ID == playing.ID {
				return false
			}
		}
		return true
	}
	if gone() || fire == nil {
		t.Fatal("retired before its handover")
	}
	fire()
	if !gone() {
		t.Fatal("not retired when its handover ended")
	}
}

type recordingProgress struct {
	calls     int
	completed []core.RatingKey
}

func (r *recordingProgress) Progress(_ context.Context, _ int64, key core.RatingKey, _, _ time.Duration, state string) error {
	r.calls++
	if state == "explicit-complete" {
		r.completed = append(r.completed, key)
	}
	return nil
}
