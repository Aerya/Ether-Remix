package sessions

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type directLiveBackendFake struct {
	allowErr   error
	allowCalls int
	tuneCalls  int
	lastTarget *frontend.DirectLiveTarget
	stopped    []string
}

func (f *directLiveBackendFake) Allowed(context.Context, *core.Viewer, string) error {
	return errors.New("unexpected channel Allowed call")
}

func (f *directLiveBackendFake) Tune(context.Context, *core.Viewer, string, core.ClientCaps, string) (string, error) {
	return "", errors.New("unexpected channel Tune call")
}

func (f *directLiveBackendFake) Serve(http.ResponseWriter, *http.Request, *core.Viewer, string, string, string) {
}

func (f *directLiveBackendFake) Stop(_ context.Context, _ *core.Viewer, id string) error {
	f.stopped = append(f.stopped, id)
	return nil
}

func (f *directLiveBackendFake) DirectAllowed(_ context.Context, _ *core.Viewer, target *frontend.DirectLiveTarget) error {
	f.allowCalls++
	f.lastTarget = target
	return f.allowErr
}

func (f *directLiveBackendFake) TuneDirect(_ context.Context, _ *core.Viewer, target *frontend.DirectLiveTarget, _ core.ClientCaps, _ string) (string, *core.MediaInfo, error) {
	f.tuneCalls++
	f.lastTarget = target
	return "direct-live-1", &core.MediaInfo{Container: "mpegts"}, nil
}

func TestBrokerDirectLivePlanOpenPolicyAndSanitization(t *testing.T) {
	ctx := context.Background()
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := NewEventBus(nil, active)
	defer events.Close()

	backend := &directLiveBackendFake{}
	viewer := &core.Viewer{ID: 7, Username: "alice"}
	broker, err := NewBroker(BrokerDeps{
		SurfacePolicy: testSessionPolicy,
		Live:          backend,
		Events:        events,
		Playback:      &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 2},
		Progress:      &fakeProgress{},
		ActiveViewer:  active,
		CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:  func(*core.Viewer) int { return 0 },
		OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Now:           func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) },
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close(ctx)

	plan, err := broker.Plan(ctx, frontend.PlanRequest{
		Protocol: "vela",
		Viewer:   viewer,
		Device:   core.Device{ID: "dev-1"},
		DirectLive: &frontend.DirectLiveTarget{
			LibraryID: 42,
			Tab:       "live",
			ItemID:    "stream-1",
			Title:     "Main Stage",
			URL:       "https://edge.example.invalid/master.m3u8?token=secret",
			Headers:   map[string]string{"Authorization": "Bearer secret"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HLS || plan.ID == "" || backend.allowCalls != 1 {
		t.Fatalf("unexpected direct live plan: %+v allowCalls=%d", plan, backend.allowCalls)
	}

	sess, err := broker.Open(ctx, frontend.OpenRequest{
		Protocol: "vela",
		PlanID:   plan.ID,
		Viewer:   viewer,
		Device:   core.Device{ID: "dev-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Channel != "" || backend.tuneCalls != 1 {
		t.Fatalf("direct live session must keep Channel empty: %+v tuneCalls=%d", sess, backend.tuneCalls)
	}

	broker.mu.Lock()
	bs := broker.sessions[sess.ID]
	broker.mu.Unlock()
	if bs == nil || !bs.live || bs.directLive == nil {
		t.Fatalf("expected directLive metadata on brokerSession")
	}
	if bs.directLive.URL != "" || len(bs.directLive.Headers) != 0 {
		t.Fatalf("brokerSession must strip secret URL/headers after tuning: %+v", bs.directLive)
	}

	// Ongoing media policy re-checks DirectAllowed (not channel Allowed).
	if !broker.currentMediaPolicy(ctx, viewer, bs) {
		t.Fatalf("currentMediaPolicy returned false while allowed")
	}
	backend.allowErr = core.ErrNotFound
	if broker.currentMediaPolicy(ctx, viewer, bs) {
		t.Fatalf("currentMediaPolicy must fail once DirectAllowed revokes access")
	}
}
