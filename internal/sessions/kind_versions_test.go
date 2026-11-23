package sessions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
	_ "modernc.org/sqlite"
)

type kindVersionPlayback struct {
	fakePlayback
	infoByRelease map[string]core.MediaInfo
	defaultRel    string
	prepared      int
	lastInput     core.KindInput
}

func (k *kindVersionPlayback) PrepareKind(_ context.Context, u *core.User, in core.KindInput, c core.ClientCaps) (*core.PlaySession, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.lastInput = in
	relID := in.PreferSourceID
	if relID == "" || relID == core.KindAutoVersionID {
		relID = k.defaultRel
	}
	info, ok := k.infoByRelease[relID]
	if !ok {
		return nil, core.ErrNotAvailable
	}
	for _, p := range k.sessions {
		if p.UserID == u.ID && p.ClientID == c.ClientID && p.Key == in.Title.Key && p.Resolution != nil && p.Resolution.Release.ID == relID {
			return p, nil
		}
	}
	k.prepared++
	id := fmt.Sprintf("kind-ver-%d", k.prepared)
	p := &core.PlaySession{
		ID:         id,
		UserID:     u.ID,
		ClientID:   c.ClientID,
		Key:        in.Title.Key,
		Info:       info,
		Resolution: &core.Resolution{Release: core.Release{ID: relID, Name: relID, Protocol: core.ProtocolHTTP}},
	}
	k.sessions[id] = p
	return p, nil
}

func (k *kindVersionPlayback) SessionForKind(user int64, client string, key core.RatingKey) (*core.PlaySession, bool) {
	return k.SessionFor(user, client, key)
}

func (k *kindVersionPlayback) SessionForSurface(_ string, user int64, client string, key core.RatingKey, release string, _ bool) (*core.PlaySession, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, p := range k.sessions {
		if p.UserID == user && p.ClientID == client && p.Key == key {
			if release == "" || (p.Resolution != nil && p.Resolution.Release.ID == release) {
				return p, true
			}
		}
	}
	return nil, false
}

func (k *kindVersionPlayback) MarkSurfaceOpened(id string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	_, ok := k.sessions[id]
	return ok
}

func (k *kindVersionPlayback) PrepareSurface(context.Context, *core.User, core.RatingKey, core.ClientCaps, string, bool) (*core.PlaySession, error) {
	return nil, errors.New("unexpected catalogue PrepareSurface for plugin key")
}

func TestPluginKeyVersionsPlanSwitchAndPlexPartSources(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	key := core.PluginKey(99)
	rel4K := "kind:4k-hash"
	rel1080 := "kind:1080p-hash"
	infoFor := func(h int) core.MediaInfo {
		return core.MediaInfo{
			Container: "mp4",
			Duration:  time.Hour,
			Size:      int64(h) << 20,
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264", Width: h * 16 / 9, Height: h},
				{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
			},
		}
	}
	pb := &kindVersionPlayback{
		fakePlayback: fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4},
		infoByRelease: map[string]core.MediaInfo{
			rel4K:   infoFor(2160),
			rel1080: infoFor(1080),
		},
		defaultRel: rel4K,
	}
	versions := []core.ReleaseVariant{
		{ID: core.KindAutoVersionID, Label: "Auto · 4K", Auto: true, Availability: core.VersionCached, Release: core.Release{ID: rel4K, Name: "4K"}, Info: infoFor(2160)},
		{ID: rel1080, Label: "1080p", Availability: core.VersionCached, Release: core.Release{ID: rel1080, Name: "1080p"}, Info: infoFor(1080)},
	}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := NewEventBus(nil, active)
	t.Cleanup(events.Close)
	b, err := NewBroker(BrokerDeps{
		SurfacePolicy: testSessionPolicy,
		DB:            db,
		Events:        events,
		Playback:      pb,
		Transcoder:    profileTranscoder{},
		Progress:      &fakeProgress{},
		ActiveViewer:  active,
		CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:  func(*core.Viewer) int { return 0 },
		OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		KindSource: func(_ context.Context, _ *core.Viewer, k core.RatingKey) (*core.KindInput, error) {
			return &core.KindInput{Title: &core.Title{Key: k, Type: core.TypeMovie, Title: "Plugin Movie"}, SourceID: "s"}, nil
		},
		KindVersions: func(_ context.Context, _ *core.Viewer, k core.RatingKey) ([]core.ReleaseVariant, error) {
			if k != key {
				return nil, core.ErrNotFound
			}
			return versions, nil
		},
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k, Type: core.TypeMovie, Title: "Plugin Movie"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(ctx) })

	vh, err := NewVersionHandles(db, b, "vela", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	viewer := &core.Viewer{ID: 1}
	dev := core.Device{ID: "vela-dev"}
	caps := core.ClientCaps{
		Product: "Vela", ClientID: "vela-dev",
		Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"},
	}
	scope := frontend.VersionScope{Viewer: viewer, Device: dev, Protocol: "vela", Caps: caps}

	// 1. VersionHandles issues handles for both Auto and concrete plugin releases.
	handles, err := vh.(frontend.VersionHandleBatch).IssueBatch(ctx, scope, key, []string{core.KindAutoVersionID, rel1080})
	if err != nil {
		t.Fatalf("IssueBatch: %v", err)
	}
	hAuto := handles[core.KindAutoVersionID]
	h1080 := handles[rel1080]
	if hAuto == "" || h1080 == "" || hAuto == h1080 {
		t.Fatalf("unexpected handles: auto=%q 1080p=%q", hAuto, h1080)
	}

	// 2. Plan with explicit 1080p handle opens exact 1080p release and sets VersionHandle + ReleaseID.
	allow := true
	plan, err := b.Plan(ctx, frontend.PlanRequest{
		Viewer: viewer, Device: dev, Protocol: "vela", Item: key, Version: h1080, Caps: caps,
		Intent: frontend.PlayIntent{ExactVersion: true, AllowTranscoding: &allow, Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}},
	})
	if err != nil {
		t.Fatalf("Plan(1080p): %v", err)
	}
	if plan.ReleaseID != rel1080 || plan.VersionHandle != h1080 {
		t.Fatalf("plan = (rel:%q handle:%q), want (%q, %q)", plan.ReleaseID, plan.VersionHandle, rel1080, h1080)
	}
	if !pb.lastInput.Exact || !pb.lastInput.DeferSupersession || pb.lastInput.PreferSourceID != rel1080 {
		t.Fatalf("PrepareKind input = %+v, want Exact+DeferSupersession+PreferSourceID=%s", pb.lastInput, rel1080)
	}

	// 3. Open session and mid-play SwitchVersion to Auto (4K) then back to 1080p.
	sess, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: dev, Protocol: "vela", PlanID: plan.ID})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	access := frontend.SessionAccess{Viewer: viewer, Device: dev, Protocol: "vela"}
	swAuto, err := b.SwitchVersion(ctx, access, sess.ID, hAuto, 30*time.Second)
	if err != nil {
		t.Fatalf("SwitchVersion(Auto): %v", err)
	}
	if swAuto.ReleaseID != rel4K || swAuto.VersionHandle != hAuto {
		t.Fatalf("SwitchVersion(Auto) = (rel:%q handle:%q), want (%q, %q)", swAuto.ReleaseID, swAuto.VersionHandle, rel4K, hAuto)
	}
	sw1080, err := b.SwitchVersion(ctx, access, sess.ID, h1080, 45*time.Second)
	if err != nil {
		t.Fatalf("SwitchVersion(1080p): %v", err)
	}
	if sw1080.ReleaseID != rel1080 || sw1080.VersionHandle != h1080 {
		t.Fatalf("SwitchVersion(1080p) = (rel:%q handle:%q), want (%q, %q)", sw1080.ReleaseID, sw1080.VersionHandle, rel1080, h1080)
	}

	// 4. Plex PartSources honours explicit plugin release pick and detects mismatch.
	psrc := PartSources{
		Playback: pb,
		KindSource: func(_ context.Context, _ *core.Viewer, k core.RatingKey) (*core.KindInput, error) {
			return &core.KindInput{Title: &core.Title{Key: k, Type: core.TypeMovie, Title: "Plugin Movie"}, SourceID: "s"}, nil
		},
	}
	plexCaps := core.ClientCaps{Product: "Plex", ClientID: "plex-1"}
	psPlex, err := psrc.Prepare(ctx, viewer, key, plexCaps, rel1080, time.Second)
	if err != nil {
		t.Fatalf("PartSources.Prepare(1080p): %v", err)
	}
	if psPlex.Resolution == nil || psPlex.Resolution.Release.ID != rel1080 {
		t.Fatalf("Plex Prepare opened %+v, want %s", psPlex.Resolution, rel1080)
	}
	if got := psrc.Part(viewer.ID, plexCaps.ClientID, key, rel1080); !got.Matches || got.Session == nil || got.Session.ID != psPlex.ID {
		t.Fatalf("PartSources.Part(matching) = %+v", got)
	}
	if got := psrc.Part(viewer.ID, plexCaps.ClientID, key, rel4K); got.Matches {
		t.Fatalf("PartSources.Part(mismatched release) matched unexpectedly: %+v", got)
	}
}
