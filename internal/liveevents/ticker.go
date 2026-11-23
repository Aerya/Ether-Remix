package liveevents

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const maxTickerItems = 32

// TickerEventsSource lists live or active events for an optional screen ID.
type TickerEventsSource interface {
	TickerEvents(ctx context.Context, viewer *core.User, screen string, now time.Time) ([]core.LiveEvent, error)
}

// TickerService builds the viewer-scoped, spoiler-filtered compact list for
// GET /vela/v0/live/ticker?screen= (A6), satisfying frontend.LiveEventTicker.
type TickerService struct {
	source TickerEventsSource
	policy AlertViewerPolicy
	now    func() time.Time
}

// TickerServiceOptions configures a TickerService.
type TickerServiceOptions struct {
	Source TickerEventsSource
	Policy AlertViewerPolicy
	Now    func() time.Time
}

// NewTickerService creates an A6 compact ticker provider.
func NewTickerService(opts TickerServiceOptions) *TickerService {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &TickerService{
		source: opts.Source,
		policy: opts.Policy,
		now:    nowFn,
	}
}

// LiveTicker returns a compact, viewer-scoped, spoiler-filtered list of live
// events ordered with in-progress events first, then close/upcoming events.
func (t *TickerService) LiveTicker(ctx context.Context, viewer *core.User, screen string) ([]core.LiveTickerItem, error) {
	if t == nil || t.source == nil || viewer == nil {
		return []core.LiveTickerItem{}, nil
	}
	events, err := t.source.TickerEvents(ctx, viewer, strings.TrimSpace(screen), t.now())
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return []core.LiveTickerItem{}, nil
	}

	spoilerFree := false
	if t.policy != nil {
		if sf, err := t.policy.SpoilerFree(ctx, viewer.ID); err == nil {
			spoilerFree = sf
		}
	}

	items := make([]core.LiveTickerItem, 0, len(events))
	for _, ev := range events {
		if ev.ID == "" {
			continue
		}
		if ev.Status.State == core.LiveEventStateCanceled || ev.Status.State == core.LiveEventStatePostponed {
			continue
		}
		if spoilerFree {
			revealed := false
			if t.policy != nil {
				if rev, err := t.policy.EventRevealed(ctx, viewer.ID, ev.ID); err == nil {
					revealed = rev
				}
			}
			if !revealed {
				ev = MaskEvent(ev)
			}
		}
		items = append(items, core.TickerItemFromEvent(ev))
	}

	sort.Slice(items, func(i, j int) bool {
		ri, rj := tickerStateRank(items[i].Status.State), tickerStateRank(items[j].Status.State)
		if ri != rj {
			return ri < rj
		}
		if items[i].Status.Close != items[j].Status.Close {
			return items[i].Status.Close
		}
		si, sj := parseEventTime(items[i].Start), parseEventTime(items[j].Start)
		if !si.Equal(sj) {
			return si.Before(sj)
		}
		return items[i].ID < items[j].ID
	})

	if len(items) > maxTickerItems {
		items = items[:maxTickerItems]
	}
	return items, nil
}

func tickerStateRank(state string) int {
	switch state {
	case core.LiveEventStateIn:
		return 0
	case core.LiveEventStateDelayed:
		return 1
	case core.LiveEventStatePre:
		return 2
	case core.LiveEventStatePost:
		return 3
	default:
		return 4
	}
}
