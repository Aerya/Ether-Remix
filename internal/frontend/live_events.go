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

// LiveEventStreamRequest scopes a viewer's live stream. Plugin screens
// carry their own topics; the live-events part only delivers alerts.
type LiveEventStreamRequest struct {
	Alerts bool
}

// LiveEventStream delivers viewer-scoped in-app alerts until Close.
type LiveEventStream interface {
	// Alerts delivers alerts when Alerts was requested, or nil.
	Alerts() <-chan LiveEventAlert
	// Close unsubscribes from the hub.
	Close()
}

// LiveEvents exposes live event availability, alerts and the viewer's
// spoiler preference to mounted frontends. Nil on Host means live event
// feeds are not wired. Live event data reaches clients through plugin
// screens (docs/plugin-screens.md).
type LiveEvents interface {
	// Available reports whether at least one enabled plugin currently contributes
	// an active live event feed.
	Available(ctx context.Context) bool
	// Subscribe opens a viewer-scoped alert stream.
	Subscribe(ctx context.Context, viewer *Viewer, req LiveEventStreamRequest) (LiveEventStream, error)
	// SpoilerFree reads the viewer's spoilerFree preference (default false).
	SpoilerFree(ctx context.Context, viewer *Viewer) (bool, error)
}
