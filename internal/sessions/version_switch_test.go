package sessions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

type switchFakePlayback struct {
	*fakePlayback
	mu        sync.Mutex
	onPrepare func(release string, exact bool) (*core.PlaySession, error)
	onSwitch  func(release string, exact bool) (*core.PlaySession, error)
	stopped   []string
}

func (f *switchFakePlayback) PrepareSurface(ctx context.Context, u *core.User, key core.RatingKey, c core.ClientCaps, release string, exact bool) (*core.PlaySession, error) {
	if f.onPrepare != nil {
		return f.onPrepare(release, exact)
	}
	return f.fakePlayback.PrepareSurface(ctx, u, key, c, release, exact)
}

func (f *switchFakePlayback) PrepareWithin(ctx context.Context, _ time.Duration, u *core.User, key core.RatingKey, c core.ClientCaps, release string, exact bool) (*core.PlaySession, error) {
	if f.onSwitch != nil {
		return f.onSwitch(release, exact)
	}
	return f.PrepareSurface(ctx, u, key, c, release, exact)
}

func (f *switchFakePlayback) Stop(id string) {
	f.mu.Lock()
	f.stopped = append(f.stopped, id)
	f.mu.Unlock()
	f.fakePlayback.Stop(id)
}

func TestBrokerSwitchVersionSuccessPreparingAndUnavailable(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	viewer := &core.Viewer{ID: 1, Username: "viewer"}
	active := func(_ context.Context, id int64) (*core.Viewer, error) {
		return &core.Viewer{ID: id, Username: "viewer"}, nil
	}
	events := NewEventBus(nil, active)
	defer events.Close()

	basePB := &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}
	pb := &switchFakePlayback{fakePlayback: basePB}

	variants := []core.ReleaseVariant{
		{ID: "slot:auto", Auto: true, Release: core.Release{ID: "rel-a"}, Availability: core.VersionInstant, Info: core.MediaInfo{Container: "mkv", Duration: time.Hour}},
		{ID: "slot:alt1", Release: core.Release{ID: "rel-b"}, Availability: core.VersionCached, Info: core.MediaInfo{Container: "mp4", Duration: time.Hour}},
		{ID: "slot:slow", Release: core.Release{ID: "rel-c"}, Availability: core.VersionFetch, Info: core.MediaInfo{Container: "mkv", Duration: time.Hour}},
		{ID: "slot:dead", Release: core.Release{ID: "rel-d"}, Availability: core.VersionUnavailable, Info: core.MediaInfo{Container: "mkv", Duration: time.Hour}},
	}

	b, err := NewBroker(BrokerDeps{
		DB:            db,
		SurfacePolicy: testSessionPolicy,
		Events:        events,
		Playback:      pb,
		Progress:      &fakeProgress{},
		ActiveViewer:  active,
		CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:  func(*core.Viewer) int { return 0 },
		OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k, Type: core.TypeMovie, Title: "Matrix"}, nil
		},
		Variants: func(context.Context, *core.Viewer, *core.Title, core.ClientCaps) ([]core.ReleaseVariant, error) {
			return variants, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)

	handles, err := NewVersionHandles(db, b, "vela", testVersionKey)
	if err != nil {
		t.Fatal(err)
	}
	item := core.MovieKey(603)
	dev := core.Device{ID: "dev-1"}
	caps := core.ClientCaps{Protocol: "vela", ClientID: "vela:dev-1", Containers: []string{"mkv", "mp4"}}
	scope := frontend.VersionScope{Viewer: viewer, Device: dev, Protocol: "vela", Caps: caps}
	issued, err := handles.(frontend.VersionHandleBatch).IssueBatch(ctx, scope, item, []string{"slot:auto", "slot:alt1", "slot:slow", "slot:dead"})
	if err != nil {
		t.Fatal(err)
	}

	// Open initial session on Auto (rel-a).
	plan, err := b.Plan(ctx, frontend.PlanRequest{
		Protocol: "vela", Viewer: viewer, Device: dev, Item: item, Caps: caps,
		Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	sess, err := b.Open(ctx, frontend.OpenRequest{Protocol: "vela", PlanID: plan.ID, Viewer: viewer, Device: dev})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	access := frontend.SessionAccess{Viewer: viewer, Device: dev, Protocol: "vela"}

	b.mu.Lock()
	initialPlayID := b.sessions[sess.ID].playID
	b.mu.Unlock()

	// 1. Slow/uncached version refuses with ErrPreparing and leaves active session untouched!
	pb.onSwitch = func(release string, exact bool) (*core.PlaySession, error) {
		return nil, &core.PreparingError{RetryAfter: 5 * time.Second}
	}
	_, err = b.SwitchVersion(ctx, access, sess.ID, issued["slot:slow"], 12*time.Minute)
	if !errors.Is(err, core.ErrPreparing) {
		t.Fatalf("slow switch err = %v, want ErrPreparing", err)
	}
	b.mu.Lock()
	afterSlowPlayID := b.sessions[sess.ID].playID
	b.mu.Unlock()
	if afterSlowPlayID != initialPlayID || len(pb.stopped) != 0 {
		t.Fatalf("slow switch disturbed active session: playID=%q (was %q), stopped=%v", afterSlowPlayID, initialPlayID, pb.stopped)
	}

	// 2. Unavailable version refuses with ErrVersionUnavailable.
	_, err = b.SwitchVersion(ctx, access, sess.ID, issued["slot:dead"], 12*time.Minute)
	if !errors.Is(err, core.ErrVersionUnavailable) {
		t.Fatalf("unavailable switch err = %v, want ErrVersionUnavailable", err)
	}

	// 3. Unknown handle refuses with ErrNotFound.
	_, err = b.SwitchVersion(ctx, access, sess.ID, "00000000-0000-4000-8000-000000000000", 12*time.Minute)
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown handle switch err = %v, want ErrNotFound", err)
	}

	// 4. Cached version succeeds: same session ID, swaps playID, stops old playID!
	pb.onSwitch = func(release string, exact bool) (*core.PlaySession, error) {
		if release != "slot:alt1" || !exact {
			t.Fatalf("unexpected switch target release=%q exact=%v", release, exact)
		}
		ps := &core.PlaySession{
			ID:         "play-switched-b",
			UserID:     viewer.ID,
			ClientID:   caps.ClientID,
			Key:        item,
			Resolution: &core.Resolution{Release: core.Release{ID: "rel-b"}, VersionSelector: "slot:alt1"},
			Info:       core.MediaInfo{Container: "mp4", Duration: time.Hour},
		}
		pb.fakePlayback.mu.Lock()
		pb.fakePlayback.sessions[ps.ID] = ps
		pb.fakePlayback.mu.Unlock()
		return ps, nil
	}
	switched, err := b.SwitchVersion(ctx, access, sess.ID, issued["slot:alt1"], 12*time.Minute)
	if err != nil {
		t.Fatalf("SwitchVersion: %v", err)
	}
	if switched.ID != sess.ID || switched.VersionHandle != issued["slot:alt1"] || switched.ReleaseID != "rel-b" {
		t.Fatalf("switched plan = %+v; want id=%q handle=%q rel=rel-b", switched, sess.ID, issued["slot:alt1"])
	}
	b.mu.Lock()
	live := b.sessions[sess.ID]
	b.mu.Unlock()
	if live.playID != "play-switched-b" || live.versionHandle != issued["slot:alt1"] || live.streamPosition != 12*time.Minute {
		t.Fatalf("live session not updated cleanly: playID=%q handle=%q pos=%v", live.playID, live.versionHandle, live.streamPosition)
	}
	if len(pb.stopped) != 1 || pb.stopped[0] != initialPlayID {
		t.Fatalf("old playID not stopped cleanly: stopped=%v, want [%s]", pb.stopped, initialPlayID)
	}
}

func TestBrokerPlanAndSwitchExplicitSlotFailoverAndStepDown(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	viewer := &core.Viewer{ID: 1, Username: "viewer"}
	active := func(_ context.Context, id int64) (*core.Viewer, error) {
		return &core.Viewer{ID: id, Username: "viewer"}, nil
	}
	events := NewEventBus(nil, active)
	defer events.Close()

	slot1080 := core.VersionSelectorPrefix + "eyJrIjoidGllciIsImgiOjEwODB9"
	variants := []core.ReleaseVariant{
		{ID: core.VersionSelectorPrefix + "eyJrIjoiYXV0byJ9", Auto: true, Release: core.Release{ID: "uhd1"}, Availability: core.VersionInstant, Info: core.MediaInfo{Container: "mkv", Duration: time.Hour}},
		// First copy behind the 1080p slot is marked unavailable; the slot must
		// still run playback failover across backup copies instead of failing closed.
		{ID: slot1080, Release: core.Release{ID: "hd1"}, Availability: core.VersionUnavailable, Info: core.MediaInfo{Container: "mkv", Duration: time.Hour}},
	}

	basePB := &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}
	pb := &switchFakePlayback{fakePlayback: basePB}
	b, err := NewBroker(BrokerDeps{
		DB:            db,
		SurfacePolicy: testSessionPolicy,
		Events:        events,
		Playback:      pb,
		Progress:      &fakeProgress{},
		ActiveViewer:  active,
		CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:  func(*core.Viewer) int { return 0 },
		OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k, Type: core.TypeMovie, Title: "Matrix"}, nil
		},
		Variants: func(context.Context, *core.Viewer, *core.Title, core.ClientCaps) ([]core.ReleaseVariant, error) {
			return variants, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)

	handles, err := NewVersionHandles(db, b, "jellyfin", testVersionKey)
	if err != nil {
		t.Fatal(err)
	}
	item := core.MovieKey(603)
	dev := core.Device{ID: "dev-jf"}
	caps := core.ClientCaps{Protocol: "jellyfin", ClientID: "jf:dev-jf", Containers: []string{"mkv", "mp4"}}
	scope := frontend.VersionScope{Viewer: viewer, Device: dev, Protocol: "jellyfin", Caps: caps}
	issued, err := handles.(frontend.VersionHandleBatch).IssueBatch(ctx, scope, item, []string{slot1080})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Plan on an explicit slot with a stepped-down 720p release must pass exact=false
	// and accept the lower-tier fallback rather than rejecting it or jumping to Auto.
	pb.onPrepare = func(release string, exact bool) (*core.PlaySession, error) {
		if release != slot1080 || exact {
			t.Fatalf("Plan PrepareSurface got release=%q exact=%v; want (%q, false)", release, exact, slot1080)
		}
		ps := &core.PlaySession{
			ID:         "play-stepdown-sd1",
			UserID:     viewer.ID,
			ClientID:   caps.ClientID,
			Key:        item,
			Resolution: &core.Resolution{Release: core.Release{ID: "sd1"}, QualityFallback: "1080p unavailable; playing 720p"},
			Info:       core.MediaInfo{Container: "mkv", Duration: time.Hour},
		}
		pb.fakePlayback.mu.Lock()
		pb.fakePlayback.sessions[ps.ID] = ps
		pb.fakePlayback.mu.Unlock()
		return ps, nil
	}
	plan, err := b.Plan(ctx, frontend.PlanRequest{
		Protocol: "jellyfin", Viewer: viewer, Device: dev, Item: item, Caps: caps, Version: issued[slot1080],
		Intent: frontend.PlayIntent{ExactVersion: true, Options: core.DecideOptions{DirectPlay: true, DirectStream: true}},
	})
	if err != nil || plan.ReleaseID != "sd1" {
		t.Fatalf("Plan(slot1080 step-down) = %+v, %v; want release sd1", plan, err)
	}

	// 2. Plan on an explicit slot that hits ErrSourceChanged must NOT silently fall back to Auto.
	var calls []string
	pb.onPrepare = func(release string, exact bool) (*core.PlaySession, error) {
		calls = append(calls, release)
		return nil, core.ErrSourceChanged
	}
	if _, err := b.Plan(ctx, frontend.PlanRequest{
		Protocol: "jellyfin", Viewer: viewer, Device: dev, Item: item, Caps: caps, Version: issued[slot1080],
		Intent: frontend.PlayIntent{ExactVersion: true, Options: core.DecideOptions{DirectPlay: true, DirectStream: true}},
	}); err == nil {
		t.Fatal("Plan(slot1080 ErrSourceChanged) succeeded, want error")
	}
	for _, rel := range calls {
		if rel == "" {
			t.Fatalf("Plan fell back to Auto after explicit slot error: calls=%v", calls)
		}
	}

	// 3. SwitchVersion on an explicit slot (Vela) runs with exact=false even when the
	// first copy is marked unavailable, and accepts a backup copy / step-down.
	pb.onPrepare = nil
	autoPlan, err := b.Plan(ctx, frontend.PlanRequest{
		Protocol: "jellyfin", Viewer: viewer, Device: dev, Item: item, Caps: caps,
		Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true}},
	})
	if err != nil {
		t.Fatalf("Auto Plan: %v", err)
	}
	sess, err := b.Open(ctx, frontend.OpenRequest{Protocol: "jellyfin", PlanID: autoPlan.ID, Viewer: viewer, Device: dev})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pb.onSwitch = func(release string, exact bool) (*core.PlaySession, error) {
		if release != slot1080 || exact {
			t.Fatalf("SwitchVersion PrepareWithin got release=%q exact=%v; want (%q, false)", release, exact, slot1080)
		}
		ps := &core.PlaySession{
			ID:         "play-slot-hd2",
			UserID:     viewer.ID,
			ClientID:   caps.ClientID,
			Key:        item,
			Resolution: &core.Resolution{Release: core.Release{ID: "hd2"}, VersionSelector: slot1080},
			Info:       core.MediaInfo{Container: "mkv", Duration: time.Hour},
		}
		pb.fakePlayback.mu.Lock()
		pb.fakePlayback.sessions[ps.ID] = ps
		pb.fakePlayback.mu.Unlock()
		return ps, nil
	}
	access := frontend.SessionAccess{Viewer: viewer, Device: dev, Protocol: "jellyfin"}
	switched, err := b.SwitchVersion(ctx, access, sess.ID, issued[slot1080], 5*time.Minute)
	if err != nil || switched.ReleaseID != "hd2" {
		t.Fatalf("SwitchVersion(slot1080) = %+v, %v; want hd2", switched, err)
	}
}
