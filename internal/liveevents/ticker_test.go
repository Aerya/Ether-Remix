package liveevents

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type fakeTickerSource struct {
	events     []core.LiveEvent
	lastScreen string
}

func (f *fakeTickerSource) TickerEvents(_ context.Context, _ *core.User, screen string, _ time.Time) ([]core.LiveEvent, error) {
	f.lastScreen = screen
	return f.events, nil
}

func TestTickerServiceOrdersAndAppliesSpoilerFreeWithReveal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 19, 0, 0, 0, time.UTC)

	src := &fakeTickerSource{
		events: []core.LiveEvent{
			{
				ID:     "demo:main:post",
				Feed:   "main",
				Start:  now.Add(-3 * time.Hour).UTC().Format(time.RFC3339),
				Status: core.LiveEventStatus{State: core.LiveEventStatePost, Detail: "Final"},
				Competitors: []core.LiveEventCompetitor{
					{ID: "a", Name: "North", Score: "3", Winner: true},
					{ID: "b", Name: "South", Score: "1"},
				},
			},
			{
				ID:     "demo:main:live",
				Feed:   "main",
				Start:  now.Add(-30 * time.Minute).UTC().Format(time.RFC3339),
				Status: core.LiveEventStatus{State: core.LiveEventStateIn, Detail: "Q4 · 1:10", Close: true},
				Competitors: []core.LiveEventCompetitor{
					{ID: "c", Name: "East", Score: "24"},
					{ID: "d", Name: "West", Score: "21"},
				},
			},
			{
				ID:     "demo:main:pre",
				Feed:   "main",
				Start:  now.Add(time.Hour).UTC().Format(time.RFC3339),
				Status: core.LiveEventStatus{State: core.LiveEventStatePre},
			},
			{
				ID:     "demo:main:canceled",
				Feed:   "main",
				Start:  now.UTC().Format(time.RFC3339),
				Status: core.LiveEventStatus{State: core.LiveEventStateCanceled},
			},
		},
	}

	policy := &fakeViewerPolicy{
		spoilerFree: map[int64]bool{2: true},
		revealed:    map[string]bool{"demo:main:post": true},
	}
	svc := NewTickerService(TickerServiceOptions{
		Source: src,
		Policy: policy,
		Now:    func() time.Time { return now },
	})

	// Non-spoiler-free viewer gets scores intact, ordered live -> pre -> post, canceled excluded.
	items, err := svc.LiveTicker(ctx, &core.User{ID: 1}, "live-board")
	if err != nil {
		t.Fatalf("LiveTicker viewer 1: %v", err)
	}
	if src.lastScreen != "live-board" {
		t.Fatalf("screen passed to source = %q, want live-board", src.lastScreen)
	}
	if len(items) != 3 {
		t.Fatalf("got %d ticker items, want 3 (canceled omitted)", len(items))
	}
	if items[0].ID != "demo:main:live" || items[1].ID != "demo:main:pre" || items[2].ID != "demo:main:post" {
		t.Fatalf("unexpected ticker order: [%s, %s, %s]", items[0].ID, items[1].ID, items[2].ID)
	}
	if items[0].Spoiler || items[0].Competitors[0].Score != "24" {
		t.Fatalf("viewer 1 live item should retain scores: %+v", items[0])
	}

	// Spoiler-free viewer has scores stripped on unrevealed live event, kept on explicitly revealed post event.
	sfItems, err := svc.LiveTicker(ctx, &core.User{ID: 2}, "live-board")
	if err != nil {
		t.Fatalf("LiveTicker viewer 2: %v", err)
	}
	if !sfItems[0].Spoiler || sfItems[0].Competitors[0].Score != "" || sfItems[0].Status.Close {
		t.Fatalf("spoiler-free live item leaked scores/close: %+v", sfItems[0])
	}
	if sfItems[2].Spoiler || sfItems[2].Competitors[0].Score != "3" {
		t.Fatalf("explicitly revealed event should show scores: %+v", sfItems[2])
	}
}
