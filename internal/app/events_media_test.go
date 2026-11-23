package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestAppMediaEventsRegistryCompletedAndTerminated(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	a := &App{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		db:    db,
		state: state.New(db, state.Options{}),
	}
	a.initEventBus()
	a.initMediaEventBridges()
	t.Cleanup(func() {
		for i := len(a.closers) - 1; i >= 0; i-- {
			a.closers[i]()
		}
	})

	// 1. Registry contains all 61 built-in media & delivery schemas.
	reg := a.EventRegistry()
	if reg == nil {
		t.Fatal("EventRegistry() is nil")
	}
	mediaDomains := map[string]int{
		"playback": 0,
		"livetv":   0,
		"resolver": 0,
		"indexer":  0,
		"provider": 0,
		"vault":    0,
		"cache":    0,
	}
	for _, s := range reg.List() {
		if _, ok := mediaDomains[s.Domain]; ok {
			mediaDomains[s.Domain]++
		}
	}
	wantCounts := map[string]int{
		"playback": 17,
		"livetv":   12,
		"resolver": 8,
		"indexer":  6,
		"provider": 8,
		"vault":    5,
		"cache":    5,
	}
	for dom, want := range wantCounts {
		if got := mediaDomains[dom]; got != want {
			t.Errorf("domain %q schema count = %d, want %d", dom, got, want)
		}
	}

	// Capture events from App.EventBus().
	sub := a.EventBus().Subscribe(events.SubscriptionOptions{
		Name:   "test-media",
		Buffer: 16,
	})
	defer sub.Unsubscribe()

	waitType := func(want string) events.Envelope {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case ev, ok := <-sub.C():
				if !ok {
					t.Fatalf("event subscription closed while waiting for %q", want)
				}
				if ev.Type == want {
					return ev
				}
			case <-deadline:
				t.Fatalf("timed out waiting for event %q", want)
			}
		}
	}

	// 2. playback.session.completed via state.Progress crossing 90% threshold.
	key := core.MovieKey(603)
	dur := 100 * time.Minute
	pos := 92 * time.Minute
	originCtx := state.WithOrigin(ctx, state.Origin{Surface: "vela", ClientID: "atv-1", SessionID: "sess-comp-1"})
	if err := a.state.Progress(originCtx, 42, key, pos, dur, "playing"); err != nil {
		t.Fatalf("state.Progress: %v", err)
	}
	evComp := waitType(events.EventPlaybackSessionCompleted)
	if err := reg.ValidatePayload(evComp.Type, evComp.Payload); err != nil {
		t.Fatalf("completed payload invalid: %v", err)
	}
	compP, err := events.DecodePayload[events.PlaybackSessionCompletedPayload](evComp)
	if err != nil {
		t.Fatal(err)
	}
	if compP.SessionID != "sess-comp-1" || compP.ThresholdPercent != 90 || compP.WatchedRatio < 0.90 || compP.LibraryName != "Movies" {
		t.Fatalf("unexpected completed payload: %+v", compP)
	}

	// 3. playback.session.terminated via stopOwnerSession.
	if err := a.stopOwnerSession("sess-term-1"); err != nil {
		t.Fatalf("stopOwnerSession: %v", err)
	}
	evTerm := waitType(events.EventPlaybackSessionTerminated)
	if err := reg.ValidatePayload(evTerm.Type, evTerm.Payload); err != nil {
		t.Fatalf("terminated payload invalid: %v", err)
	}
	termP, err := events.DecodePayload[events.PlaybackSessionTerminatedPayload](evTerm)
	if err != nil {
		t.Fatal(err)
	}
	if termP.SessionID != "sess-term-1" || termP.Actor != "owner" || termP.Reason != "owner_stopped" {
		t.Fatalf("unexpected terminated payload: %+v", termP)
	}
}
