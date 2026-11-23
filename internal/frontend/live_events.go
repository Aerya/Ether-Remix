package frontend

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// Live event model aliases so frontends exchange live event payloads without
// importing internal/core or internal/liveevents directly.
type LiveEvent = core.LiveEvent
type LiveEventDetail = core.LiveEventDetail
type LiveEventAlert = core.LiveEventAlert
type LiveEventEntity = core.LiveEventEntity
type LiveEventTable = core.LiveEventTable
type LiveEventWatchTarget = core.LiveEventWatchTarget

// LiveEventStreamRequest scopes a viewer's live events SSE subscription.
type LiveEventStreamRequest struct {
	EventIDs []string
	Screens  []string
	Alerts   bool
}

// LiveEventStream delivers live event update batches and optional alerts until
// Close is called.
type LiveEventStream interface {
	// Initial returns the snapshot of subscribed events to send immediately on connect.
	Initial() []LiveEvent
	// Updates delivers batches of changed events.
	Updates() <-chan []LiveEvent
	// Alerts delivers viewer-scoped in-app alerts when Alerts was requested, or nil.
	Alerts() <-chan LiveEventAlert
	// Close unsubscribes from the hub.
	Close()
}

// LiveEvents exposes live event feeds, spoiler preferences, and SSE streaming
// to mounted frontends. Nil on Host means live event feeds are not wired.
type LiveEvents interface {
	// Available reports whether at least one enabled plugin currently contributes
	// an active live event feed.
	Available(ctx context.Context) bool
	// Events returns a viewer-scoped, spoiler-filtered snapshot for ids.
	Events(ctx context.Context, viewer *Viewer, ids []string) ([]LiveEvent, error)
	// Detail returns the viewer-scoped, spoiler-filtered detail payload for id.
	Detail(ctx context.Context, viewer *Viewer, id string) (*LiveEventDetail, error)
	// Subscribe opens a viewer-scoped, spoiler-filtered live stream.
	Subscribe(ctx context.Context, viewer *Viewer, req LiveEventStreamRequest) (LiveEventStream, error)
	// Reveal records a per-viewer override revealing scores for event id.
	Reveal(ctx context.Context, viewer *Viewer, id string) error
	// SpoilerFree reads the viewer's spoilerFree preference (default false).
	SpoilerFree(ctx context.Context, viewer *Viewer) (bool, error)
	// SetSpoilerFree updates the viewer's spoilerFree preference.
	SetSpoilerFree(ctx context.Context, viewer *Viewer, enabled bool) error
}
