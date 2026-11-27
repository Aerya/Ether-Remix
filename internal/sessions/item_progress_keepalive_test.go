package sessions

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// progressKeepOutput is an HLS output that records the playback states reported
// to it; nothing else on it is used.
type progressKeepOutput struct {
	core.HLSSession
	mu     sync.Mutex
	states []string
}

func (o *progressKeepOutput) PlaybackState(state string) {
	o.mu.Lock()
	o.states = append(o.states, state)
	o.mu.Unlock()
}

type progressKeepTranscoder struct {
	fakeBrokerTranscoder
	mu      sync.Mutex
	asked   []string
	outputs map[string]*progressKeepOutput
}

func (t *progressKeepTranscoder) Session(id string) (core.HLSSession, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.asked = append(t.asked, id)
	if t.outputs[id] == nil {
		t.outputs[id] = &progressKeepOutput{}
	}
	return t.outputs[id], true
}

type progressKeepPlayback struct {
	livenessFakePlayback
	touched []string
}

func (p *progressKeepPlayback) Touch(id string) {
	p.mu.Lock()
	p.touched = append(p.touched, id)
	p.mu.Unlock()
}

// Jellyfin Web reports progress without a PlaySessionId, so it arrives at
// ReportItemProgress. Playing and paused reports there must keep the open
// session's output active as Report does, or a player that buffered ahead
// (or paused) past the idle timeout has its stream closed and its next
// segment request answered 404.
func TestReportItemProgressKeepsOpenSessionActive(t *testing.T) {
	ctx := context.Background()
	p := &progressKeepPlayback{livenessFakePlayback: livenessFakePlayback{
		fakePlayback: fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4},
		active:       map[string]bool{},
		stopped:      map[string]bool{},
	}}
	tr := &progressKeepTranscoder{outputs: map[string]*progressKeepOutput{}}
	active := func(_ context.Context, id int64) (*core.Viewer, error) {
		return &core.Viewer{ID: id, IsOwner: true}, nil
	}
	events := NewEventBus(nil, active)
	defer events.Close()
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy,
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k}, nil
		},
		Playback:     p,
		Transcoder:   tr,
		Progress:     &fakeProgress{},
		ActiveViewer: active,
		CanPlay:      func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit: func(*core.Viewer) int { return 0 },
		OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Events:       events,
	})
	if err != nil {
		t.Fatal(err)
	}
	viewer := &core.Viewer{ID: 1, IsOwner: true}
	dev := core.Device{ID: "jellyfin-web"}
	access := frontend.SessionAccess{Viewer: viewer, Device: dev, Protocol: "jellyfin", Owner: true}
	plan, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: dev, Protocol: "jellyfin", Item: core.MovieKey(42),
		Caps: core.ClientCaps{ClientID: "jellyfin-web"}, Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true}}})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: dev, Protocol: "jellyfin", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	tr.mu.Lock()
	tr.asked = nil
	tr.mu.Unlock()

	for _, state := range []string{"playing", "paused"} {
		if err := b.ReportItemProgress(ctx, access, core.MovieKey(42), frontend.Timeline{Position: time.Minute, Duration: time.Hour, State: state}); err != nil {
			t.Fatal(err)
		}
	}
	out := tr.outputs[opened.ID]
	if out == nil {
		t.Fatalf("the open session's output was never told about the reports (asked %v)", tr.asked)
	}
	if len(out.states) != 2 || out.states[0] != "playing" || out.states[1] != "paused" {
		t.Fatalf("output states = %v, want [playing paused]", out.states)
	}
	if len(p.touched) < 2 {
		t.Fatalf("playback session touched %d times, want 2", len(p.touched))
	}

	// Another title, or another device, keeps nothing alive.
	tr.mu.Lock()
	tr.asked = nil
	tr.mu.Unlock()
	if err := b.ReportItemProgress(ctx, access, core.MovieKey(43), frontend.Timeline{Position: time.Minute, Duration: time.Hour, State: "playing"}); err != nil {
		t.Fatal(err)
	}
	other := frontend.SessionAccess{Viewer: viewer, Device: core.Device{ID: "phone"}, Protocol: "jellyfin", Owner: true}
	if err := b.ReportItemProgress(ctx, other, core.MovieKey(42), frontend.Timeline{Position: time.Minute, Duration: time.Hour, State: "playing"}); err != nil {
		t.Fatal(err)
	}
	if len(tr.asked) != 0 {
		t.Fatalf("an unrelated report touched outputs %v", tr.asked)
	}
}
