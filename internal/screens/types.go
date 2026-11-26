// Package screens serves plugin screens and surfaces drawn from documents
// (docs/plugin-screens.md): it indexes plugins' compiled documents, resolves
// their data sources per viewer on top of the live-events hub, favourites and
// watch targets, and streams live changes while someone watches.
package screens

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
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
