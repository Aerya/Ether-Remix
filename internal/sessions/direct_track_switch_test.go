package sessions

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestDirectPlayReopenOfSameSourceKeepsSession(t *testing.T) {
	ctx := context.Background()
	p := &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 2}
	progress := &fakeProgress{}
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
	d := core.Device{ID: "tv"}
	a := frontend.SessionAccess{Viewer: v, Device: d, Protocol: "jellyfin"}
	open := func(audio int) (frontend.Session, frontend.PlayPlan) { // Fladder posts PlaybackInfo again for a track switch
		t.Helper()
		pl, err := b.Plan(ctx, frontend.PlanRequest{Viewer: v, Device: d, Protocol: "jellyfin", Item: core.MovieKey(1), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, AudioStream: audio, SubtitleStream: -1}}})
		if err != nil {
			t.Fatal(err)
		}
		s, err := b.Open(ctx, frontend.OpenRequest{Viewer: v, Device: d, Protocol: "jellyfin", PlanID: pl.ID})
		if err != nil {
			t.Fatal(err)
		}
		return s, pl
	}
	first, _ := open(-1)
	url1, err := b.StartStream(ctx, a, first.ID, frontend.StreamRequest{Format: frontend.StreamDirect})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := open(2)
	url2, err := b.StartStream(ctx, a, second.ID, frontend.StreamRequest{Format: frontend.StreamDirect, Position: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || url1 != url2 {
		t.Fatal("track switch replaced the playing direct session")
	}
}
