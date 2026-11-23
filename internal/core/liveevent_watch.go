package core

import (
	"encoding/json"
	"slices"
)

// Watch target kinds returned by A5 "Where to watch".
const (
	WatchTargetKindChannel = LiveEventWatchChannel
	WatchTargetKindAddon   = LiveEventWatchAddon
	WatchTargetKindPlugin  = LiveEventWatchPlugin
)

// WatchTarget aliases LiveEventWatchTarget so W255, W256, and W257 share one
// canonical watch-target representation across core, hub, screens, and Vela.
type WatchTarget = LiveEventWatchTarget

// LiveEventRef links a playing live channel programme back to its live event
// for the Vela playback snapshot and session SSE (A6).
type LiveEventRef struct {
	ID   string `json:"id"`
	Feed string `json:"feed"`
}

// LiveTickerItem is the compact live event summary returned by
// GET /vela/v0/live/ticker (A6).
type LiveTickerItem struct {
	ID          string                `json:"id"`
	Feed        string                `json:"feed"`
	Category    string                `json:"category,omitempty"`
	League      LiveEventLeague       `json:"league"`
	Title       string                `json:"title,omitempty"`
	Start       string                `json:"start"` // RFC3339
	Status      LiveEventStatus       `json:"status"`
	Competitors []LiveEventCompetitor `json:"competitors"`
	Ranking     []LiveEventRankRow    `json:"ranking,omitempty"`
	Spoiler     bool                  `json:"spoiler,omitempty"`
	UpdatedAt   string                `json:"updatedAt,omitempty"` // RFC3339
}

// MarshalJSON guarantees competitors encodes as [] rather than null.
func (t LiveTickerItem) MarshalJSON() ([]byte, error) {
	type wire LiveTickerItem
	cp := wire(t)
	if cp.Competitors == nil {
		cp.Competitors = []LiveEventCompetitor{}
	}
	return json.Marshal(cp)
}

// TickerItemFromEvent projects a LiveEvent into its compact ticker form.
func TickerItemFromEvent(ev LiveEvent) LiveTickerItem {
	item := LiveTickerItem{
		ID:          ev.ID,
		Feed:        ev.Feed,
		Category:    ev.Category,
		League:      ev.League,
		Title:       ev.Title,
		Start:       ev.Start,
		Status:      ev.Status,
		Competitors: []LiveEventCompetitor{},
		Spoiler:     ev.Spoiler,
		UpdatedAt:   ev.UpdatedAt,
	}
	if len(ev.Competitors) > 0 {
		item.Competitors = slices.Clone(ev.Competitors)
	}
	if len(ev.Ranking) > 0 {
		item.Ranking = slices.Clone(ev.Ranking)
	}
	return item
}
