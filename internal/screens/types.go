// Package screens resolves generic plugin-declared screens (spec A3) and
// live entity search/favourite overlays (spec A4) on top of the live event
// hub, viewer favourites, and optional watch-target matcher.
package screens

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

var (
	// ErrInvalidDate reports a malformed schedule date query (expected YYYY-MM-DD).
	ErrInvalidDate = errors.New("screens: invalid date")
	// ErrFeedNotFound reports an unknown, unpublished, or disallowed live feed.
	ErrFeedNotFound = errors.New("screens: feed not found")
)

// EventQuery asks the live event hub for events from one plugin feed
// (maps onto GET /ether/v1/live/{feed}/events?from=&to=&league=&ids=).
type EventQuery struct {
	PluginID string
	Feed     string
	From     time.Time
	To       time.Time
	League   string
	IDs      []string
}

// EntityQuery asks the live event hub for entities from one plugin feed
// (maps onto GET /ether/v1/live/{feed}/entities?kind=&league=&q=&ids=).
type EntityQuery struct {
	PluginID string
	Feed     string
	Kind     string
	League   string
	Query    string
	IDs      []string
}

// LiveFeedHub is the read boundary screens require from the live event hub
// (W255, internal/liveevents). Returned events are already viewer-scoped and
// spoiler-filtered by the hub.
type LiveFeedHub interface {
	Events(ctx context.Context, viewer *core.User, q EventQuery) ([]core.LiveEvent, error)
	Standings(ctx context.Context, viewer *core.User, pluginID, feed, league string) ([]core.LiveEventTable, error)
	Entities(ctx context.Context, viewer *core.User, q EntityQuery) ([]core.LiveEventEntity, error)
}

// WatchTargetResolver attaches viewer-scoped "where to watch" targets to a
// live event (W257, spec A5). A nil resolver yields an empty watch slice.
type WatchTargetResolver interface {
	WatchTargets(ctx context.Context, viewer *core.User, event core.LiveEvent) ([]core.LiveEventWatchTarget, error)
}

// BatchWatchTargetResolver is an optional extension of WatchTargetResolver
// allowing screens to resolve watch targets for all events in a screen in one
// bounded guide lookup.
type BatchWatchTargetResolver interface {
	WatchTargetsBatch(ctx context.Context, viewer *core.User, events []core.LiveEvent) (map[string][]core.LiveEventWatchTarget, error)
}

// FavoriteEntities reads the viewer's favorited entity keys (spec A4).
type FavoriteEntities interface {
	ListEntityKeys(ctx context.Context, viewer *core.User) ([]string, error)
}

// DeclaredScreen is one published, viewer-permitted plugin screen ready for
// navigation or resolution.
type DeclaredScreen struct {
	ID          string
	ScreenID    string
	PluginID    string
	Title       string
	Icon        string
	DefaultFeed string
	EntityKind  string
	Sections    []plugins.ScreenSection
}

// NavigationItem is one plugin screen entry for Vela bootstrap navigation:
// {kind: "screen", id, title, icon?}.
type NavigationItem struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Icon     string `json:"icon,omitempty"`
	PluginID string `json:"-"`
}

// ResolveRequest identifies a screen resolution call from a frontend.
type ResolveRequest struct {
	FrontendID string
	ScreenID   string
	League     string
	Date       string
}

// ResolvedEvent is a core.LiveEvent augmented with viewer-scoped watch targets
// (spec A5) and favorite-entity membership for generic rendering.
type ResolvedEvent struct {
	core.LiveEvent
	Watch     []core.LiveEventWatchTarget `json:"watch"`
	Favorite  bool                        `json:"favorite,omitempty"`
	startTime time.Time
}

// MarshalJSON preserves embedded LiveEvent fields while guaranteeing
// competitors and watch always encode as [] rather than null.
func (e ResolvedEvent) MarshalJSON() ([]byte, error) {
	type liveEventAlias core.LiveEvent
	type wire struct {
		liveEventAlias
		Watch    []core.LiveEventWatchTarget `json:"watch"`
		Favorite bool                        `json:"favorite,omitempty"`
	}
	base := liveEventAlias(e.LiveEvent)
	if base.Competitors == nil {
		base.Competitors = []core.LiveEventCompetitor{}
	}
	watch := e.Watch
	if watch == nil {
		watch = []core.LiveEventWatchTarget{}
	}
	return json.Marshal(wire{
		liveEventAlias: base,
		Watch:          watch,
		Favorite:       e.Favorite,
	})
}

// ScheduleDay is one selectable date chip on a schedule section.
type ScheduleDay struct {
	Date  string `json:"date"`
	Label string `json:"label"`
	Count int    `json:"count,omitempty"`
}

// ResolvedEntity is one followable LiveEntity annotated with its canonical
// favourite key (pluginID:feed:entityKind:id) and current viewer favourite
// status (binding Vela wire shape).
type ResolvedEntity struct {
	Key      string `json:"key"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Abbr     string `json:"abbr,omitempty"`
	Logo     string `json:"logo,omitempty"`
	League   string `json:"league,omitempty"`
	Color    string `json:"color,omitempty"`
	Favorite bool   `json:"favorite"`
}

// EntitySearchResult is the wire payload for GET /vela/v0/live/entities.
type EntitySearchResult struct {
	Feed       string           `json:"feed"`
	EntityKind string           `json:"entityKind"`
	Entities   []ResolvedEntity `json:"entities"`
}

// MarshalJSON guarantees entities encodes as [] rather than null.
func (r EntitySearchResult) MarshalJSON() ([]byte, error) {
	type wire EntitySearchResult
	cp := wire(r)
	if cp.Entities == nil {
		cp.Entities = []ResolvedEntity{}
	}
	return json.Marshal(cp)
}

// ResolvedSection is one generically rendered section in a resolved screen.
// Its MarshalJSON produces the exact per-type wire shapes agreed with Vela:
//   - hero:     {id, type, title?, event, also?: [event]}
//   - events:   {id, type, title?, query?, events: [event]}
//   - table:    {id, type, title?, feed, league?, tables: [table]}
//   - schedule: {id, type, title?, date, previous?, next?, days?: [...], events: [event]}
//   - leagues:  {id, type, title?, leagues: [league], selected?}
//   - entities: {id, type, title?, feed, entityKind, entities: [LiveEntity]}
type ResolvedSection struct {
	ID         string
	Type       string
	Title      string
	Feed       string
	League     string
	EntityKind string
	Query      *plugins.ScreenEventQuery
	Event      *ResolvedEvent
	Also       []ResolvedEvent
	Events     []ResolvedEvent
	Tables     []core.LiveEventTable
	Date       string
	Previous   string
	Next       string
	Days       []ScheduleDay
	Leagues    []core.LiveEventLeague
	Selected   string
	Entities   []ResolvedEntity
}

// MarshalJSON encodes section-type-specific fields deterministically according
// to the binding Vela screen section wire contract.
func (s ResolvedSection) MarshalJSON() ([]byte, error) {
	base := map[string]any{
		"id":   s.ID,
		"type": s.Type,
	}
	if s.Title != "" {
		base["title"] = s.Title
	}
	switch s.Type {
	case "hero":
		base["event"] = s.Event
		if len(s.Also) > 0 {
			base["also"] = s.Also
		}
	case "events":
		if s.Query != nil {
			base["query"] = s.Query
		}
		events := s.Events
		if events == nil {
			events = []ResolvedEvent{}
		}
		base["events"] = events
	case "table":
		base["feed"] = s.Feed
		if s.League != "" {
			base["league"] = s.League
		}
		tables := s.Tables
		if tables == nil {
			tables = []core.LiveEventTable{}
		}
		base["tables"] = tables
	case "schedule":
		base["date"] = s.Date
		if s.Previous != "" {
			base["previous"] = s.Previous
		}
		if s.Next != "" {
			base["next"] = s.Next
		}
		if len(s.Days) > 0 {
			base["days"] = s.Days
		}
		events := s.Events
		if events == nil {
			events = []ResolvedEvent{}
		}
		base["events"] = events
	case "leagues":
		leagues := s.Leagues
		if leagues == nil {
			leagues = []core.LiveEventLeague{}
		}
		base["leagues"] = leagues
		if s.Selected != "" {
			base["selected"] = s.Selected
		}
	case "entities":
		base["feed"] = s.Feed
		base["entityKind"] = s.EntityKind
		entities := s.Entities
		if entities == nil {
			entities = []ResolvedEntity{}
		}
		base["entities"] = entities
	}
	return json.Marshal(base)
}

// ResolvedScreen is the generic screen payload returned by GET /vela/v0/screens/{id}.
type ResolvedScreen struct {
	ID       string            `json:"id"`
	PluginID string            `json:"pluginId"`
	Title    string            `json:"title"`
	Icon     string            `json:"icon,omitempty"`
	Feed     string            `json:"feed,omitempty"`
	League   string            `json:"league,omitempty"`
	Date     string            `json:"date"`
	Sections []ResolvedSection `json:"sections"`
}

// MarshalJSON guarantees sections encodes as [] rather than null.
func (s ResolvedScreen) MarshalJSON() ([]byte, error) {
	type wire ResolvedScreen
	cp := wire(s)
	if cp.Sections == nil {
		cp.Sections = []ResolvedSection{}
	}
	return json.Marshal(cp)
}
