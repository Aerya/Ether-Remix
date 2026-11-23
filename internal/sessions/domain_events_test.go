package sessions

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestBrokerEmitsAdmissionDenied(t *testing.T) {
	ctx := context.Background()
	bus, rec := events.NewTestBus(t)
	reg := events.NewBuiltinRegistry()

	viewer := &core.Viewer{ID: 42, Username: "alex"}
	key := core.MovieKey(603)
	now := time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC)

	active := func(context.Context, int64) (*core.Viewer, error) { return viewer, nil }
	b, err := NewBroker(BrokerDeps{
		SurfacePolicy: testSessionPolicy,
		Playback:      &fakePlayback{sessions: map[string]*core.PlaySession{}},
		Progress:      &fakeProgress{},
		Events:        NewEventBus(nil, active),
		DomainEvents:  bus,
		ActiveViewer:  active,
		CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
		FreshPlay: func(context.Context, *core.Viewer, bool, time.Time) (*core.Viewer, error) {
			return nil, &core.ErrBedtime{NextAllowedAt: now.Add(8 * time.Hour)}
		},
		BitrateLimit: func(*core.Viewer) int { return 0 },
		OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k, Title: "The Matrix", Type: core.TypeMovie}, nil
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)

	_, err = b.Plan(ctx, frontend.PlanRequest{
		Protocol: "vela",
		Viewer:   viewer,
		Device:   core.Device{ID: "ios-1"},
		Item:     key,
		Caps:     core.ClientCaps{Product: "Vela iOS", Remote: true},
	})
	if err == nil {
		t.Fatal("expected bedtime denial error")
	}

	ev := rec.WaitFor(t, events.EventPlaybackAdmissionDenied, time.Second)
	if err := reg.ValidatePayload(ev.Type, ev.Payload); err != nil {
		t.Fatalf("admission denied payload invalid: %v", err)
	}
	p, err := events.DecodePayload[events.PlaybackAdmissionDeniedPayload](ev)
	if err != nil {
		t.Fatal(err)
	}
	if p.Policy != "bedtime" || p.ViewerID != 42 || p.Username != "alex" || !p.Remote || p.TitleKey != key.String() {
		t.Fatalf("unexpected admission denied payload: %+v", p)
	}
}
