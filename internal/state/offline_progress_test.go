package state

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestOfflineProgressMergesByObservationAndPreservesCompletion(t *testing.T) {
	ctx := t.Context()
	onlineAt := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	s := New(store.OpenTest(t), Options{Now: func() time.Time { return onlineAt }})
	key := core.MovieKey(101)
	const duration = 100 * time.Minute
	if err := s.Progress(ctx, 1, key, 40*time.Minute, duration, "paused"); err != nil {
		t.Fatal(err)
	}

	var events []Event
	s.Subscribe(func(ev Event) { events = append(events, ev) })
	report := func(observed, received time.Time, position time.Duration) frontend.TimelinePersistenceResult {
		t.Helper()
		got, err := s.ApplyOfflineProgress(ctx, 1, key, frontend.OfflineProgress{
			Position: position, Duration: duration, ObservedAt: observed, ReceivedAt: received,
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	received := onlineAt.Add(time.Hour)
	for _, observed := range []time.Time{onlineAt.Add(-time.Second), onlineAt} {
		if got := report(observed, received, 95*time.Minute); got.Changed || got.ViewCount != 0 || got.ViewOffset != 40*time.Minute {
			t.Fatalf("stale completion changed current progress: %+v", got)
		}
	}
	if len(events) != 0 {
		t.Fatalf("stale reports emitted events: %+v", events)
	}
	if got := report(received.Add(time.Hour), received, 90*time.Minute); !got.Completed || got.ViewCount != 1 {
		t.Fatalf("future report at the completion threshold: %+v", got)
	}
	for _, ev := range events {
		if !ev.At.Equal(received) || !ev.Offline {
			t.Fatalf("sync event lost its clamped observation: %+v", ev)
		}
	}
	if len(events) == 0 || events[len(events)-1].Kind != EventCompleted {
		t.Fatalf("missing completion event: %+v", events)
	}

	newer := received.Add(time.Minute)
	if got := report(newer, newer, 20*time.Minute); !got.Changed || got.Completed || got.ViewCount != 1 || got.ViewOffset != 0 {
		t.Fatalf("newer partial progress regressed a completion: %+v", got)
	}
	ws, err := s.Get(ctx, 1, key)
	if err != nil || !ws.LastViewedAt.Equal(newer) {
		t.Fatalf("stored watch state = %+v, %v", ws, err)
	}
}
