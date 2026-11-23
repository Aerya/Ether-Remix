package liveevents

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

func TestHubPluginCachedSnapshotHealthAndBackoffRespected(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC))

	feed := FeedInfo{
		PluginID:       "community.example.events",
		PluginName:     "Events Feed",
		FeedID:         "main",
		Title:          "Live events",
		MinIntervalSec: 15,
		EntityKinds:    []string{"entrant"},
	}
	src := newFakePluginSource(feed)

	var failing atomic.Bool
	src.handler = func(_ context.Context, _ string, req plugins.Request) (plugins.Response, error) {
		if req.Path == "/ether/v1/live/main/entities" {
			body, _ := json.Marshal(core.LiveEventEntitiesResponse{
				Entities: []core.LiveEventEntity{
					{ID: "e1", Kind: "entrant", Name: "Alpha", League: "l1"},
					{ID: "e2", Kind: "entrant", Name: "Beta", League: "l1"},
					{ID: "e3", Kind: "entrant", Name: "Gamma", League: "l2"},
				},
			})
			return plugins.Response{Status: http.StatusOK, Body: body}, nil
		}
		if failing.Load() {
			return plugins.Response{Status: http.StatusServiceUnavailable, Body: json.RawMessage(`{"error":"upstream unavailable"}`)}, nil
		}
		body, _ := json.Marshal(core.LiveEventsResponse{
			NextPollSec: 30,
			Events: []core.LiveEvent{
				{
					ID:     "ev-live",
					Feed:   "main",
					League: core.LiveEventLeague{ID: "l1", Name: "League One", Abbr: "L1"},
					Start:  "2026-11-22T17:30:00Z",
					Status: core.LiveEventStatus{State: core.LiveEventStateIn, Detail: "Half 2"},
					Competitors: []core.LiveEventCompetitor{
						{ID: "c1", Name: "Alpha", Score: "2", EntityID: "e1"},
						{ID: "c2", Name: "Beta", Score: "1", EntityID: "e2"},
					},
				},
				{
					ID:     "ev-pre",
					Feed:   "main",
					League: core.LiveEventLeague{ID: "l2", Name: "League Two", Abbr: "L2"},
					Start:  "2026-11-22T20:00:00Z",
					Status: core.LiveEventStatus{State: core.LiveEventStatePre},
					Title:  "Gamma Showcase",
				},
			},
		})
		return plugins.Response{Status: http.StatusOK, Body: body}, nil
	}

	hub := NewHub(Options{Source: src, Clock: clk})
	defer hub.Close()

	pollDone := make(chan string, 8)
	unsub := hub.OnFeedPoll(func(pid, fid string) {
		pollDone <- pid + ":" + fid
	})
	defer unsub()

	// Cold cache read must never call the plugin.
	feeds, evs := hub.PluginCachedSnapshot(ctx, feed.PluginID)
	if src.calls.Load() != 0 {
		t.Fatalf("cold PluginCachedSnapshot called plugin %d times, want 0", src.calls.Load())
	}
	if len(feeds) != 1 || len(evs) != 0 {
		t.Fatalf("cold snapshot = %d feeds, %d events; want 1, 0", len(feeds), len(evs))
	}

	// Populate events + entities via normal hub APIs.
	if _, err := hub.FetchEvents(ctx, feed.PluginID, feed.FeedID, EventQuery{}); err != nil {
		t.Fatalf("FetchEvents: %v", err)
	}
	select {
	case got := <-pollDone:
		if got != feed.Key() {
			t.Fatalf("OnFeedPoll = %q, want %q", got, feed.Key())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for OnFeedPoll after FetchEvents")
	}
	if _, err := hub.GetEntities(ctx, feed.PluginID, feed.FeedID, "entrant", "", ""); err != nil {
		t.Fatalf("GetEntities: %v", err)
	}
	callsAfterWarm := src.calls.Load()

	// Cached snapshot returns leagues, entity counts, events, and healthy poll info with zero plugin calls.
	feeds, evs = hub.PluginCachedSnapshot(ctx, feed.PluginID)
	if src.calls.Load() != callsAfterWarm {
		t.Fatalf("warm PluginCachedSnapshot triggered extra calls: %d -> %d", callsAfterWarm, src.calls.Load())
	}
	if len(evs) != 2 || len(feeds) != 1 {
		t.Fatalf("warm snapshot = %d feeds, %d events; want 1, 2", len(feeds), len(evs))
	}
	ov := feeds[0]
	if ov.LiveCount != 1 || ov.UpcomingCount != 1 || ov.EventCount != 2 {
		t.Fatalf("counts = live:%d upcoming:%d total:%d, want 1/1/2", ov.LiveCount, ov.UpcomingCount, ov.EventCount)
	}
	if len(ov.Leagues) != 2 {
		t.Fatalf("leagues = %+v, want 2", ov.Leagues)
	}
	if ov.EntityTotal != 3 || ov.EntityCounts["entrant"] != 3 {
		t.Fatalf("entities = total:%d counts:%+v, want 3 entrants", ov.EntityTotal, ov.EntityCounts)
	}
	if ov.Health.ConsecutiveErrors != 0 || ov.Health.LastError != "" || ov.Health.NextPollSec != 30 {
		t.Fatalf("health = %+v, want healthy with nextPollSec=30", ov.Health)
	}

	// Subscribing briefly while within nextPollAt must NOT force a new poll.
	sub1, err := hub.Subscribe(ctx, SubscribeRequest{Feeds: []string{feed.Key()}})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// Let poller goroutine reach its initial wait timer.
	clk.WaitForActiveTimers(t, 1)
	if src.calls.Load() != callsAfterWarm {
		t.Fatalf("brief subscribe inside nextPollAt forced poll: calls %d -> %d", callsAfterWarm, src.calls.Load())
	}
	sub1.Close()

	// Advance past nextPollAt and fail a poll; verify health captures error and backoff.
	clk.Advance(31 * time.Second)
	failing.Store(true)
	sub2, err := hub.Subscribe(ctx, SubscribeRequest{Feeds: []string{feed.Key()}})
	if err != nil {
		t.Fatalf("Subscribe failing: %v", err)
	}
	waitCall(t, src.called)
	select {
	case <-pollDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for OnFeedPoll after failure")
	}
	sub2.Close()

	feeds, _ = hub.PluginCachedSnapshot(ctx, feed.PluginID)
	fh := feeds[0].Health
	if fh.ConsecutiveErrors != 1 || fh.LastError == "" || fh.LastPollAt.IsZero() || fh.NextPollSec < 15 || !fh.NextPollAt.After(clk.Now()) {
		t.Fatalf("failing health = %+v, want error count=1, lastError, lastPollAt, future nextPollAt", fh)
	}

	// Re-subscribing during error backoff must respect NextPollAt and NOT force a new poll.
	callsAtFail := src.calls.Load()
	sub3, err := hub.Subscribe(ctx, SubscribeRequest{Feeds: []string{feed.Key()}})
	if err != nil {
		t.Fatalf("Subscribe during backoff: %v", err)
	}
	clk.WaitForActiveTimers(t, 1)
	if src.calls.Load() != callsAtFail {
		t.Fatalf("subscribe during backoff forced poll: calls %d -> %d", callsAtFail, src.calls.Load())
	}
	sub3.Close()
}
