package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/logredact"
)

func TestOwnerErrorFormatsAndRedactsCauses(t *testing.T) {
	raw := fmt.Errorf("open Movie.2025.2160p.WEB-DL: real-debrid HTTP 401 (apikey=supersecret-token): %w", core.ErrDebridAuth)
	wrapped := releasesFailed(3, raw, false)
	if !errors.Is(wrapped, core.ErrNotAvailable) || !errors.Is(wrapped, core.ErrDebridAuth) {
		t.Fatalf("sentinel wrapping changed: %v", wrapped)
	}

	got := OwnerError(wrapped)
	if strings.Contains(got, "Playback failed; see server logs") {
		t.Fatalf("OwnerError still returned fixed placeholder: %q", got)
	}
	if strings.Contains(got, "supersecret-token") {
		t.Fatalf("OwnerError leaked secret: %q", got)
	}
	if !strings.HasPrefix(got, "every release failed (3 tried): open Movie.2025.2160p.WEB-DL:") {
		t.Fatalf("OwnerError = %q, want 'every release failed (3 tried): ...'", got)
	}

	usenet := releasesFailed(3, fmt.Errorf("open Show.S01E01.1080p: news.example: 430 no such article: %w", core.ErrArticleMissing), false)
	if !errors.Is(usenet, core.ErrNotAvailable) || !errors.Is(usenet, core.ErrSourceUnavailable) {
		t.Fatalf("usenet sentinel wrapping changed: %v", usenet)
	}
	gotUsenet := OwnerError(usenet)
	if !strings.Contains(gotUsenet, "every release failed (3 tried): open Show.S01E01.1080p: news.example: 430 no such article") {
		t.Fatalf("OwnerError(usenet) = %q", gotUsenet)
	}
}

func TestFailedPlayEmitsRealRedactedErrorAndFullTimeline(t *testing.T) {
	a := mkv("Rel.One.2025.2160p", 64<<10, 10*time.Minute)
	b := mkv("Rel.Two.2025.1080p", 64<<10, 10*time.Minute)
	c := mkv("Rel.Three.2025.720p", 64<<10, 10*time.Minute)
	a.OpenErr = fmt.Errorf("news.primary: 430 missing article (password=topsecret1): %w", core.ErrArticleMissing)
	b.OpenErr = fmt.Errorf("news.backup: connection reset: %w", core.ErrSourceUnavailable)
	c.OpenErr = fmt.Errorf("debrid: HTTP 403 bad key apikey=topsecret2: %w", core.ErrDebridAuth)

	var (
		mu       sync.Mutex
		timeline []TimelineEvent
		busErrs  []Event
		logBuf   bytes.Buffer
	)
	lvl := new(slog.LevelVar)
	lvl.Set(slog.LevelDebug)
	logger := slog.New(logredact.Handler(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: lvl})))

	h := newHarness(t, []*fakeRelease{a, b, c}, []string{a.ID, b.ID, c.ID})
	// Re-create manager with Timeline and Logger wired.
	_ = h.m.Close()
	m, err := New(Deps{
		Resolver:      h.resolver,
		Engine:        h.engine,
		Transcoder:    h.tr,
		Catalog:       h.catalog,
		Cache:         h.cache,
		State:         h.state,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return h.cfg.Quality.Profiles[0] },
		Logger:        logger,
		Timeline: func(ev TimelineEvent) {
			mu.Lock()
			timeline = append(timeline, ev)
			mu.Unlock()
		},
	}, h.cfg, Options{DataDir: h.dataDir, Now: h.clock.Now, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	h.m = m

	m.Subscribe(func(ev Event) {
		if ev.Kind == "error" {
			mu.Lock()
			busErrs = append(busErrs, ev)
			mu.Unlock()
		}
	})

	caps := core.ClientCaps{
		ClientID:   "living-room-tv",
		DeviceName: "Living Room TV",
		Product:    "Plex for Apple TV",
		Platform:   "tvOS",
		Protocol:   "plex",
		MaxHeight:  2160,
	}
	_, prepErr := m.Prepare(context.Background(), owner, movieKey, caps)
	if prepErr == nil {
		t.Fatal("expected Prepare to fail when all 3 candidates fail")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(busErrs) != 1 {
		t.Fatalf("expected 1 error event on bus, got %d", len(busErrs))
	}
	ev := busErrs[0]
	if ev.Session == nil || ev.Session.ID == "" {
		t.Fatalf("expected failed play error event to carry session snapshot, got %+v", ev)
	}
	if !strings.Contains(ev.Error, "every release failed (3 tried)") || strings.Contains(ev.Error, "topsecret") {
		t.Fatalf("unexpected error cause %q", ev.Error)
	}

	var kinds []string
	var candidates []string
	for _, te := range timeline {
		if te.SessionID != ev.Session.ID {
			t.Fatalf("timeline event session %q != %q", te.SessionID, ev.Session.ID)
		}
		kinds = append(kinds, te.Kind)
		if te.Kind == "candidate" {
			candidates = append(candidates, te.Message)
		}
		if strings.Contains(te.Message, "topsecret") || strings.Contains(te.Detail, "topsecret") {
			t.Fatalf("unredacted secret in timeline event: %+v", te)
		}
	}
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidate failure events, got %d: %v (all kinds: %v)", len(candidates), candidates, kinds)
	}
	if kinds[0] != "started" || kinds[len(kinds)-1] != "error" {
		t.Fatalf("expected timeline to start with 'started' and end with 'error', got %v", kinds)
	}
	if !strings.Contains(logBuf.String(), "session="+ev.Session.ID) {
		t.Fatalf("expected session-attributed slog output for %s, got:\n%s", ev.Session.ID, logBuf.String())
	}
}

func TestLivePlayTimelineLifecycleAndBackgroundSuppression(t *testing.T) {
	a := mkv("Show.S01E01.2160p.WEB-DL", 128<<10, 10*time.Minute)
	b := mkv("Show.S01E01.1080p.WEB-DL", 128<<10, 10*time.Minute)

	var (
		mu       sync.Mutex
		timeline []TimelineEvent
	)
	h := newHarness(t, []*fakeRelease{a, b}, []string{a.ID, b.ID})
	_ = h.m.Close()
	m, err := New(Deps{
		Resolver:      h.resolver,
		Engine:        h.engine,
		Transcoder:    h.tr,
		Catalog:       h.catalog,
		Cache:         h.cache,
		State:         h.state,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return h.cfg.Quality.Profiles[0] },
		Timeline: func(ev TimelineEvent) {
			mu.Lock()
			timeline = append(timeline, ev)
			mu.Unlock()
		},
	}, h.cfg, Options{DataDir: h.dataDir, Now: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	// Background analysis must not emit timeline events.
	bgCtx := core.WithBackgroundAnalysis(context.Background())
	bgSess, err := m.PrepareSurface(bgCtx, owner, epKey, core.ClientCaps{ClientID: "bg-probe", Protocol: "vela"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	m.Stop(bgSess.ID)
	mu.Lock()
	if len(timeline) != 0 {
		t.Fatalf("background analysis emitted timeline events: %+v", timeline)
	}
	mu.Unlock()

	// Real play: started -> resolve -> candidate -> decision -> quality -> stall -> failover -> stop.
	caps := core.ClientCaps{ClientID: "shield-tv", DeviceName: "Shield", Protocol: "plex", MaxHeight: 2160}
	ps, err := m.Prepare(context.Background(), owner, movieKey, caps)
	if err != nil {
		t.Fatal(err)
	}
	m.SetDecision(ps.ID, core.Decision{
		Kind:        core.DecisionTranscode,
		MaxHeight:   1080,
		BitrateKbps: 8000,
		Reason:      "audio codec truehd not supported",
	})
	h.clock.Advance(2 * time.Second)
	m.SetDecision(ps.ID, core.Decision{
		Kind:        core.DecisionTranscode,
		MaxHeight:   720,
		BitrateKbps: 4000,
		Reason:      "viewer lowered remote quality",
	})
	h.clock.Advance(time.Second)
	m.PlaybackProgress(owner.ID, "shield-tv", movieKey, 10*time.Second, "playing")
	h.clock.Advance(time.Second)
	m.PlaybackProgress(owner.ID, "shield-tv", movieKey, 11*time.Second, "buffering")
	h.clock.Advance(4 * time.Second)
	m.PlaybackProgress(owner.ID, "shield-tv", movieKey, 11*time.Second, "playing")

	h.clock.Advance(time.Second)
	if _, err := m.Failover(context.Background(), ps.ID, 12*time.Second); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(3 * time.Second)
	m.Stop(ps.ID)

	mu.Lock()
	defer mu.Unlock()
	seen := map[string]bool{}
	for _, ev := range timeline {
		seen[ev.Kind] = true
	}
	for _, want := range []string{"started", "resolve", "candidate", "decision", "quality", "stall", "failover", "stop"} {
		if !seen[want] {
			t.Fatalf("missing timeline kind %q in %+v", want, timeline)
		}
	}
}
