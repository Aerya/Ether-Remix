package livetv

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestActivityRefreshAndPublication(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{now: guideTestNow}
	var hits atomic.Int32
	var failing atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if failing.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		_, _ = w.Write([]byte(m3uFixture("one=One", "two=Two")))
	}))
	defer provider.Close()
	s := testService(t, Options{Now: clock.Now, HTTPClient: provider.Client()})
	src := Source{ID: "activity", Name: "Activity", PlaylistURL: provider.URL, RefreshHours: 24, ActivityRefreshMinutes: 60}
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	base := hits.Load()
	clock.Set(guideTestNow.Add(59 * time.Minute))
	if due := s.refreshSources(ctx, true); len(due) != 0 || hits.Load() != base {
		t.Fatal("cooldown bypassed")
	}
	clock.Set(guideTestNow.Add(time.Hour))
	wait := ownerEditLeaseWait
	ownerEditLeaseWait = 0
	t.Cleanup(func() { ownerEditLeaseWait = wait })
	release, err := s.acquireRefresh(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	other := &Service{o: s.o}
	if _, err := other.acquireRefresh(ctx, src.ID); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("cross-node lease: %v", err)
	}
	if _, err := s.RefreshSource(ctx, src.ID); !errors.Is(err, core.ErrNotAvailable) || hits.Load() != base {
		t.Fatalf("duplicate refresh: %v", err)
	}
	if _, err := s.ImportSource(ctx, src); !errors.Is(err, core.ErrNotAvailable) || hits.Load() != base {
		t.Fatalf("owner edit overlapped refresh: %v", err)
	}
	release()
	if due := s.refreshSources(ctx, true); len(due) != 1 || hits.Load() != base+1 {
		t.Fatal("activity did not refresh")
	}
	if due := s.refreshSources(ctx, true); len(due) != 0 || hits.Load() != base+1 {
		t.Fatal("unchanged import did not reset cooldown")
	}

	owner := &core.User{ID: 1, IsOwner: true}
	channels, err := s.AllowedChannels(ctx, owner)
	if err != nil || len(channels) != 2 {
		t.Fatalf("channels: %v %v", channels, err)
	}
	if _, err := s.UpdateChannels(ctx, ChannelBatch{IDs: []string{channels[0].ID}, HideApps: []string{"plex"}}); err != nil {
		t.Fatal(err)
	}
	plexCtx := core.WithLiveApp(ctx, "plex")
	if got, err := s.AllowedChannels(plexCtx, owner); err != nil || len(got) != 1 || got[0].ID != channels[1].ID {
		t.Fatalf("Plex subset: %v %v", got, err)
	}
	if _, err := s.AllowedChannel(plexCtx, owner, channels[0].ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("hidden Plex tune: %v", err)
	}
	if got, err := s.AllowedChannels(ctx, owner); err != nil || len(got) != 2 {
		t.Fatalf("external lineup changed: %v %v", got, err)
	}
	if _, err := s.RefreshSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Channel(ctx, channels[0].ID); err != nil || len(got.HiddenApps) != 1 || got.HiddenApps[0] != "plex" {
		t.Fatalf("refresh lost publication choice: %v", err)
	}

	failing.Store(true)
	clock.Set(guideTestNow.Add(2 * time.Hour))
	s.refreshSources(ctx, true)
	failedHits := hits.Load()
	clock.Set(guideTestNow.Add(2*time.Hour + 30*time.Minute))
	if due := s.refreshSources(ctx, true); len(due) != 0 || hits.Load() != failedHits {
		t.Fatal("failure cooldown bypassed")
	}
	if got, err := s.AllowedChannels(ctx, owner); err != nil || len(got) != 2 {
		t.Fatalf("failure discarded lineup: %v", err)
	}
	if activityRefreshDue(Source{PlaylistURL: provider.URL, RefreshHours: -1, ActivityRefreshMinutes: 30}, clock.Now()) {
		t.Fatal("manual source refreshed automatically")
	}
	if activityRefreshDue(Source{Playlist: "upload", ActivityRefreshMinutes: 30}, clock.Now()) {
		t.Fatal("upload refreshed automatically")
	}
}

func TestClientActivityCoalescesWithoutRequestIO(t *testing.T) {
	clock := &testClock{now: guideTestNow}
	s := &Service{o: Options{Now: clock.Now}, ctx: context.Background(), activityWake: make(chan struct{}, 1), sessions: make(map[string]*session)}
	for range 100 {
		s.NoteClientActivity()
	}
	if len(s.activityWake) != 1 {
		t.Fatal("requests did not coalesce")
	}
	<-s.activityWake
	clock.Set(guideTestNow.Add(clientIdleWindow))
	s.sessions["playing"] = &session{}
	s.NoteClientActivity()
	if len(s.activityWake) != 0 {
		t.Fatal("active playback triggered activity refresh")
	}
	delete(s.sessions, "playing")
	clock.Set(guideTestNow.Add(2 * clientIdleWindow))
	s.NoteClientActivity()
	if len(s.activityWake) != 1 {
		t.Fatal("return after idle did not wake refresh")
	}
}

type fakeOnDemandKind struct {
	id       string
	interval time.Duration
	hits     *atomic.Int32
}

func (k fakeOnDemandKind) ID() string { return k.id }
func (k fakeOnDemandKind) Describe() SourceDescribe {
	return SourceDescribe{YieldsChannels: true, YieldsGuide: true}
}
func (k fakeOnDemandKind) Matches(src Source) bool                { return src.Kind == k.id }
func (k fakeOnDemandKind) OnDemandRefreshInterval() time.Duration { return k.interval }
func (k fakeOnDemandKind) Fetch(_ context.Context, _ *Service, src *Source) (Fetched, error) {
	k.hits.Add(1)
	return Fetched{
		Channels: []Channel{{
			ID:        "ev-ch",
			SourceID:  src.ID,
			GuideID:   "ev-ch",
			Name:      "Live Now",
			Kind:      "iptv",
			Enabled:   true,
			StreamURL: "plugin://fake/event/sport/live",
		}},
		Guide:       XMLGuide{Channels: map[string]Channel{"ev-ch": {GuideID: "ev-ch", Name: "Live Now"}}},
		AddonInputs: true,
	}, nil
}

func TestOnDemandSourceRefreshesOnlyWhileInUse(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{now: guideTestNow}
	s := testService(t, Options{Now: clock.Now})
	var hits atomic.Int32
	if err := s.RegisterSourceKind(fakeOnDemandKind{id: "plugin:fake/channels", interval: 5 * time.Minute, hits: &hits}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportSource(ctx, Source{ID: "ev", Name: "Sports", Kind: "plugin:fake/channels"}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("import hits = %d, want 1", hits.Load())
	}

	// Idle: even 2 hours later, no background timer is armed and no scheduled refresh fires.
	clock.Set(guideTestNow.Add(2 * time.Hour))
	// A standalone server arms no timer at all; a clustered (PostgreSQL)
	// server always keeps its periodic check, which must still refresh nothing.
	if _, armed := s.nextSourceRefreshWait(ctx); armed && store.DialectOf(s.o.DB) == store.SQLite {
		t.Fatal("idle on-demand source armed a background refresh timer")
	}
	if due := s.refreshDueSources(ctx); len(due) != 0 || hits.Load() != 1 {
		t.Fatalf("idle source refreshed: due=%v hits=%d", due, hits.Load())
	}

	// Client activity triggers an immediate due refresh and arms the short 5m schedule.
	s.NoteClientActivity()
	if due := s.refreshSources(ctx, true); len(due) != 1 || hits.Load() != 2 {
		t.Fatalf("activity did not refresh due on-demand source: due=%v hits=%d", due, hits.Load())
	}
	wait, armed := s.nextSourceRefreshWait(ctx)
	if !armed || wait != 5*time.Minute {
		t.Fatalf("in-use wait=%v armed=%v, want 5m/true", wait, armed)
	}
	clock.Set(guideTestNow.Add(2*time.Hour + 5*time.Minute))
	if due := s.refreshDueSources(ctx); len(due) != 1 || hits.Load() != 3 {
		t.Fatalf("short in-use refresh did not run: due=%v hits=%d", due, hits.Load())
	}

	// Active viewer session keeps the source refreshing even past onDemandInUseWindow.
	s.mu.Lock()
	s.sessions["watching"] = &session{info: SessionInfo{Channel: Channel{ID: "ev-ch", SourceID: "ev"}}}
	s.mu.Unlock()
	clock.Set(guideTestNow.Add(3 * time.Hour))
	if due := s.refreshDueSources(ctx); len(due) != 1 || hits.Load() != 4 {
		t.Fatalf("watched source did not refresh: due=%v hits=%d", due, hits.Load())
	}

	// Once nobody is watching and the idle window passes, refreshing stops again.
	s.mu.Lock()
	delete(s.sessions, "watching")
	s.mu.Unlock()
	clock.Set(guideTestNow.Add(3*time.Hour + onDemandInUseWindow + time.Minute))
	if _, armed := s.nextSourceRefreshWait(ctx); armed && store.DialectOf(s.o.DB) == store.SQLite {
		t.Fatal("returned-to-idle on-demand source kept timer armed")
	}
	if due := s.refreshDueSources(ctx); len(due) != 0 || hits.Load() != 4 {
		t.Fatalf("source refreshed after becoming idle: due=%v hits=%d", due, hits.Load())
	}
}
