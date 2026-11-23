package state

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestSkipResumePreservesHistoryAndCompletion(t *testing.T) {
	s, now := newTestState(t)
	ctx := context.Background()

	trackKey := core.PluginKey(42)
	bookKey := core.PluginKey(43)
	s.SetSkipResume(func(_ context.Context, key core.RatingKey) bool {
		return key == trackKey
	})

	var events []Event
	unsub := s.Subscribe(func(ev Event) { events = append(events, ev) })
	defer unsub()

	// Mid-track progress: must NOT persist ViewOffset, MUST persist LastViewedAt.
	if err := s.Progress(ctx, 1, trackKey, 90*time.Second, 200*time.Second, "playing"); err != nil {
		t.Fatalf("Progress(track mid): %v", err)
	}
	st, err := s.Get(ctx, 1, trackKey)
	if err != nil {
		t.Fatal(err)
	}
	if st.ViewOffset != 0 {
		t.Fatalf("skipped track ViewOffset = %v, want 0", st.ViewOffset)
	}
	if st.LastViewedAt.IsZero() {
		t.Fatal("skipped track LastViewedAt must be recorded for history shelves")
	}
	if st.ViewCount != 0 {
		t.Fatalf("mid-track ViewCount = %d, want 0", st.ViewCount)
	}
	if len(events) != 1 || events[0].ResumeSet {
		t.Fatalf("mid-track event = %+v, want ResumeSet=false", events)
	}

	// Track must not show up in InProgress.
	inProg, err := s.InProgress(ctx, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range inProg {
		if row.Key == trackKey {
			t.Fatalf("skipped track must not appear in InProgress: %+v", row)
		}
	}

	// Explicit resume mutation must also keep ViewOffset == 0 while bumping LastViewedAt.
	*now = now.Add(time.Minute)
	if err := s.ApplyWatchMutation(ctx, 1, trackKey, frontend.WatchMutation{
		Kind:     "resume",
		Offset:   120 * time.Second,
		Duration: 200 * time.Second,
	}); err != nil {
		t.Fatalf("ApplyWatchMutation(track resume): %v", err)
	}
	st, err = s.Get(ctx, 1, trackKey)
	if err != nil {
		t.Fatal(err)
	}
	if st.ViewOffset != 0 || !st.LastViewedAt.Equal(now.UTC()) {
		t.Fatalf("after explicit resume: offset=%v lastViewed=%v, want 0 and %v", st.ViewOffset, st.LastViewedAt, now.UTC())
	}

	// Completion (>=90%) still increments ViewCount and emits EventCompleted.
	*now = now.Add(time.Minute)
	if err := s.Progress(ctx, 1, trackKey, 190*time.Second, 200*time.Second, "playing"); err != nil {
		t.Fatalf("Progress(track complete): %v", err)
	}
	st, err = s.Get(ctx, 1, trackKey)
	if err != nil {
		t.Fatal(err)
	}
	if st.ViewOffset != 0 || st.ViewCount != 1 {
		t.Fatalf("after completion: offset=%v count=%d, want 0 and 1", st.ViewOffset, st.ViewCount)
	}

	// Non-skipped plugin key (e.g. audiobook) saves resume offset as normal.
	if err := s.Progress(ctx, 1, bookKey, 15*time.Minute, 60*time.Minute, "playing"); err != nil {
		t.Fatalf("Progress(book mid): %v", err)
	}
	bookSt, err := s.Get(ctx, 1, bookKey)
	if err != nil {
		t.Fatal(err)
	}
	if bookSt.ViewOffset != 15*time.Minute {
		t.Fatalf("non-skipped book ViewOffset = %v, want 15m", bookSt.ViewOffset)
	}
}
